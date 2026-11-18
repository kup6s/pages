// Package syncer synchronizes Git repos for all StaticSites
package syncer

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// removePathOrSymlink removes a path, handling both symlinks and regular files/directories.
// Returns nil if path doesn't exist.
func removePathOrSymlink(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}
	return os.RemoveAll(path)
}

// Syncer synchronizes all StaticSites
type Syncer struct {
	DynamicClient dynamic.Interface
	ClientSet     kubernetes.Interface

	// Recorder emits SyncFailed and SyncRecovered events; nil disables events.
	Recorder events.EventRecorder

	// Base directory for sites (e.g. /sites)
	SitesRoot string

	// Default sync interval
	DefaultInterval time.Duration

	// AllowedHosts is a list of allowed Git hosts (SSRF protection).
	// This field is mandatory - startup will fail if empty.
	AllowedHosts []string

	// GitTimeout bounds every git invocation; zero means no limit.
	GitTimeout time.Duration

	// Git overrides the git wrapper (tests); nil uses git from PATH.
	Git *gitCLI

	// MinFreePercent is the free space on the sites volume below which clones
	// and updates are skipped; zero disables the check.
	MinFreePercent float64

	// Metrics records sync results; nil disables metrics.
	Metrics *Metrics

	locks siteLocks
}

// validateRepoURL checks if the repo URL is allowed (SSRF protection)
func (s *Syncer) validateRepoURL(repoURL string) error {
	// Defensive check - AllowedHosts should never be empty at runtime
	// since main() validates this at startup
	if len(s.AllowedHosts) == 0 {
		return fmt.Errorf("internal error: AllowedHosts not configured")
	}

	parsed, err := url.Parse(repoURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow HTTP(S)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s (only http/https allowed)", parsed.Scheme)
	}

	// Check host against allowlist
	host := strings.ToLower(parsed.Host)
	// Remove port if present
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		host = host[:colonIdx]
	}

	for _, allowed := range s.AllowedHosts {
		if strings.ToLower(allowed) == host {
			return nil
		}
		// Wildcard-Support: *.example.com
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) {
				return nil
			}
		}
	}

	return fmt.Errorf("host %q not in allowed hosts list", host)
}

var staticSiteGVR = schema.GroupVersionResource{
	Group:    "pages.kup6s.com",
	Version:  "v1beta1",
	Resource: "staticsites",
}

// SyncAll synchronizes all StaticSites
func (s *Syncer) SyncAll(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Load all StaticSites from all namespaces
	list, err := s.DynamicClient.Resource(staticSiteGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StaticSites: %w", err)
	}

	logger.Info("Starting sync", "count", len(list.Items))

	for _, item := range list.Items {
		site := &staticSiteData{}
		if err := site.fromUnstructured(&item); err != nil {
			logger.Error(err, "Failed to parse StaticSite", "name", item.GetName())
			continue
		}

		if err := s.syncSite(ctx, site); err != nil {
			logger.Error(err, "Failed to sync site", "name", site.Name)
		}
	}

	return nil
}

// SyncOne synchronizes a single site (for webhooks)
func (s *Syncer) SyncOne(ctx context.Context, namespace, name string) error {
	logger := log.FromContext(ctx)
	
	item, err := s.DynamicClient.Resource(staticSiteGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get StaticSite %s/%s: %w", namespace, name, err)
	}

	site := &staticSiteData{}
	if err := site.fromUnstructured(item); err != nil {
		return err
	}

	logger.Info("Syncing single site", "name", name, "repo", site.Repo)
	return s.syncSite(ctx, site)
}

// getSecretValue reads a value from a Kubernetes Secret
func (s *Syncer) getSecretValue(ctx context.Context, namespace, name, key string) (string, error) {
	if key == "" {
		key = "password"
	}
	
	secret, err := s.ClientSet.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in secret %s", key, name)
	}

	return string(value), nil
}

// staticSiteData is a simplified structure for the Syncer
type staticSiteData struct {
	Name      string
	Namespace string
	Repo      string
	Branch    string
	Path      string
	SecretRef *secretRef
	// ContentPath is status.contentPath: set once the site is served from
	// its per-namespace path.
	ContentPath string
}

type secretRef struct {
	Name string
	Key  string
}

func (s *staticSiteData) fromUnstructured(u *unstructured.Unstructured) error {
	s.Name = u.GetName()
	s.Namespace = u.GetNamespace()

	spec, ok := u.Object["spec"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid spec")
	}

	s.ContentPath, _, _ = unstructured.NestedString(u.Object, "status", "contentPath")
	s.Repo, _ = spec["repo"].(string)
	s.Branch, _ = spec["branch"].(string)
	s.Path, _ = spec["path"].(string)

	if s.Branch == "" {
		s.Branch = "main"
	}
	if s.Path == "" {
		s.Path = "/"
	}

	if secretRefMap, ok := spec["secretRef"].(map[string]interface{}); ok {
		name, nameOK := secretRefMap["name"].(string)
		if !nameOK {
			return fmt.Errorf("secretRef.name is required and must be a string")
		}
		s.SecretRef = &secretRef{Name: name}
		if key, ok := secretRefMap["key"].(string); ok {
			s.SecretRef.Key = key
		}
	}

	return nil
}

// RunLoop starts the sync loop
func (s *Syncer) RunLoop(ctx context.Context) {
	logger := log.FromContext(ctx)

	ticker := time.NewTicker(s.DefaultInterval)
	defer ticker.Stop()

	s.removeLeftovers(ctx)
	s.migrateLegacyLayout(ctx)

	// Initial sync
	if err := s.SyncAll(ctx); err != nil {
		logger.Error(err, "Initial sync failed")
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("Syncer stopped")
			return
		case <-ticker.C:
			if err := s.SyncAll(ctx); err != nil {
				logger.Error(err, "Sync failed")
			}
			// Cleanup after each sync
			if err := s.Cleanup(ctx); err != nil {
				logger.Error(err, "Cleanup failed")
			}
		}
	}
}

// Cleanup removes the paths of deleted sites. A legacy path /<name> (and
// .repos/<name>) stays while any site of that name may still be routed to it,
// see routedToContentPath.
func (s *Syncer) Cleanup(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Load all StaticSites
	list, err := s.DynamicClient.Resource(staticSiteGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StaticSites: %w", err)
	}

	s.removeLeftovers(ctx)

	active := map[string]map[string]bool{} // namespace -> site names
	legacyInUse := map[string]bool{}       // legacy names still serving
	for _, item := range list.Items {
		namespace, name := item.GetNamespace(), item.GetName()
		if active[namespace] == nil {
			active[namespace] = map[string]bool{}
		}
		active[namespace][name] = true
		if !routedToContentPath(&item) {
			legacyInUse[name] = true
		}
	}

	entries, err := os.ReadDir(s.SitesRoot)
	if err != nil {
		return fmt.Errorf("failed to read sites directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		// Dot entries are .clones, .repos and temporary entries
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(s.SitesRoot, name)
		if names, ok := active[name]; ok && entry.IsDir() {
			s.removeOrphans(ctx, path, names)
			continue
		}
		if legacyInUse[name] {
			continue
		}
		logger.Info("Removing orphaned site path", "path", path)
		if err := removePathOrSymlink(path); err != nil {
			logger.Error(err, "Failed to remove orphaned site path", "path", path)
		}
	}

	clones := filepath.Join(s.SitesRoot, clonesDirName)
	for _, namespace := range subdirs(clones) {
		path := filepath.Join(clones, namespace)
		if names, ok := active[namespace]; ok {
			s.removeOrphans(ctx, path, names)
			continue
		}
		logger.Info("Removing clones of a namespace without sites", "path", path)
		if err := os.RemoveAll(path); err != nil {
			logger.Error(err, "Failed to remove clones", "path", path)
		}
	}

	s.removeLegacyClones(ctx, legacyInUse)
	return nil
}

// routedToContentPath reports whether a site no longer needs its legacy path:
// the syncer has linked it at /<namespace>/<name> (status.contentPath), and the
// controller has switched its route there. The controller writes
// status.internalURL from the same path after it has updated the addPrefix
// Middleware, so a contentPath alone may still be routed to the legacy path.
func routedToContentPath(site *unstructured.Unstructured) bool {
	want := pagesv1.ContentPathFor(site.GetNamespace(), site.GetName())
	contentPath, _, _ := unstructured.NestedString(site.Object, "status", "contentPath")
	internalURL, _, _ := unstructured.NestedString(site.Object, "status", "internalURL")
	return contentPath == want && strings.HasSuffix(internalURL, want+"/")
}

// removeOrphans removes the entries in a namespace directory that belong to
// no site of that namespace.
func (s *Syncer) removeOrphans(ctx context.Context, dir string, active map[string]bool) {
	logger := log.FromContext(ctx)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || active[name] {
			continue
		}
		path := filepath.Join(dir, name)
		logger.Info("Removing orphaned site path", "path", path)
		if err := removePathOrSymlink(path); err != nil {
			logger.Error(err, "Failed to remove orphaned site path", "path", path)
		}
	}
}

// removeLegacyClones removes legacy clones .repos/<name> no site serves from
// any more, and .repos itself once it is empty.
func (s *Syncer) removeLegacyClones(ctx context.Context, legacyInUse map[string]bool) {
	logger := log.FromContext(ctx)
	repos := filepath.Join(s.SitesRoot, reposDirName)
	entries, err := os.ReadDir(repos)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || legacyInUse[name] {
			continue
		}
		path := filepath.Join(repos, name)
		logger.Info("Removing legacy clone", "path", path)
		if err := os.RemoveAll(path); err != nil {
			logger.Error(err, "Failed to remove legacy clone", "path", path)
		}
	}
	// Fails while temporary entries or clones in use are left
	_ = os.Remove(repos)
}

// DeleteSite deletes a specific site (for webhook calls). Legacy entries of a
// site that never moved are left to Cleanup, which knows the other sites of
// the same name.
func (s *Syncer) DeleteSite(ctx context.Context, namespace, name string) error {
	logger := log.FromContext(ctx)
	logger.Info("Deleting site", "namespace", namespace, "name", name)

	lock := s.locks.get(siteKey(namespace, name))
	lock.Lock()
	defer lock.Unlock()

	sitePath := s.siteLink(namespace, name)
	if err := removePathOrSymlink(sitePath); err != nil {
		return fmt.Errorf("failed to remove site path %s: %w", sitePath, err)
	}

	clonePath := s.cloneDir(namespace, name)
	if err := removePathOrSymlink(clonePath); err != nil {
		return fmt.Errorf("failed to remove clone %s: %w", clonePath, err)
	}

	return nil
}
