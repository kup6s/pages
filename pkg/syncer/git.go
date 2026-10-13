// Package syncer synchronizes Git repos for all StaticSites
package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// statusPatch is used to construct JSON patches for StaticSite status updates
type statusPatch struct {
	Status statusPatchData `json:"status"`
}

type statusPatchData struct {
	Phase      string `json:"phase"`
	Message    string `json:"message"`
	LastSync   string `json:"lastSync"`
	LastCommit string `json:"lastCommit"`
}

// updateStatus updates the status of the StaticSite
func (s *Syncer) updateStatus(ctx context.Context, site *staticSiteData, phase, message, commit string) {
	now := metav1.Now()

	patch := statusPatch{
		Status: statusPatchData{
			Phase:      phase,
			Message:    message,
			LastSync:   now.Format(time.RFC3339),
			LastCommit: commit,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to marshal status patch", "site", site.Name)
		return
	}

	_, err = s.DynamicClient.Resource(staticSiteGVR).
		Namespace(site.Namespace).
		Patch(ctx, site.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}, "status")

	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to update status", "site", site.Name)
	}
}

// staticSiteData is a simplified structure for the Syncer
type staticSiteData struct {
	Name      string
	Namespace string
	Repo      string
	Branch    string
	Path      string
	SecretRef *secretRef
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

// Cleanup removes directories of deleted sites
func (s *Syncer) Cleanup(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Load all StaticSites
	list, err := s.DynamicClient.Resource(staticSiteGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StaticSites: %w", err)
	}

	s.removeLeftovers(ctx)

	// Set with all active site names
	activeSites := make(map[string]bool)
	for _, item := range list.Items {
		activeSites[item.GetName()] = true
	}

	// Iterate through directories in /sites
	entries, err := os.ReadDir(s.SitesRoot)
	if err != nil {
		return fmt.Errorf("failed to read sites directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		// Dot entries are .repos and temporary entries, handled by removeLeftovers
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Check if site still exists
		if !activeSites[name] {
			sitePath := filepath.Join(s.SitesRoot, name)
			logger.Info("Removing orphaned site directory", "name", name)

			if err := removePathOrSymlink(sitePath); err != nil {
				logger.Error(err, "Failed to remove orphaned site", "path", sitePath)
			}
		}
	}

	// Clean up .repos directories
	reposDir := filepath.Join(s.SitesRoot, ".repos")
	if entries, err := os.ReadDir(reposDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if !activeSites[name] {
				repoPath := filepath.Join(reposDir, name)
				logger.Info("Removing orphaned repo directory", "name", name)
				if err := os.RemoveAll(repoPath); err != nil {
					logger.Error(err, "Failed to remove orphaned repo", "path", repoPath)
				}
			}
		}
	}

	return nil
}

// DeleteSite deletes a specific site (for webhook calls)
func (s *Syncer) DeleteSite(ctx context.Context, name string) error {
	logger := log.FromContext(ctx)
	logger.Info("Deleting site", "name", name)

	lock := s.locks.get(name)
	lock.Lock()
	defer lock.Unlock()

	// Remove symlink/directory in /sites
	sitePath := filepath.Join(s.SitesRoot, name)
	if err := removePathOrSymlink(sitePath); err != nil {
		return fmt.Errorf("failed to remove site path %s: %w", sitePath, err)
	}

	// Remove repo directory in .repos
	repoPath := filepath.Join(s.SitesRoot, ".repos", name)
	if err := removePathOrSymlink(repoPath); err != nil {
		return fmt.Errorf("failed to remove repo path %s: %w", repoPath, err)
	}

	return nil
}
