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

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
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
	Version:  "v1alpha1",
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
			s.updateStatus(ctx, site, "Error", err.Error(), "")
			continue
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

// syncSite synchronizes a single site
func (s *Syncer) syncSite(ctx context.Context, site *staticSiteData) error {
	logger := log.FromContext(ctx)

	// SSRF protection: validate repo URL
	if err := s.validateRepoURL(site.Repo); err != nil {
		return fmt.Errorf("repo URL validation failed: %w", err)
	}

	// Target directory for the repo
	// With subpath: clone to .repos/<name>, symlink to <name>
	// Without subpath: clone directly to <name>
	var destDir string
	hasSubpath := site.Path != "" && site.Path != "/"
	if hasSubpath {
		destDir = filepath.Join(s.SitesRoot, ".repos", site.Name)
	} else {
		destDir = filepath.Join(s.SitesRoot, site.Name)
	}
	
	// Git auth if available
	var auth *http.BasicAuth
	if site.SecretRef != nil {
		password, err := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, site.SecretRef.Key)
		if err != nil {
			return fmt.Errorf("failed to get git credentials: %w", err)
		}
		// Get username from secret, default to "git" if not present
		username, _ := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, "username")
		if username == "" {
			username = "git"
		}
		auth = &http.BasicAuth{
			Username: username,
			Password: password,
		}
	}

	var commitHash string

	// Check if repo already exists
	if _, err := os.Stat(filepath.Join(destDir, ".git")); os.IsNotExist(err) {
		// Clone
		logger.Info("Cloning repository", "repo", site.Repo, "dest", destDir)
		
		cloneOpts := &git.CloneOptions{
			URL:           site.Repo,
			ReferenceName: plumbing.NewBranchReferenceName(site.Branch),
			SingleBranch:  true,
			Depth:         1, // Shallow clone
			Progress:      os.Stdout,
		}
		if auth != nil {
			cloneOpts.Auth = auth
		}

		repo, err := git.PlainClone(destDir, false, cloneOpts)
		if err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}

		head, err := repo.Head()
		if err != nil {
			logger.V(1).Info("Failed to get HEAD after clone", "error", err)
		}
		if head != nil {
			commitHash = head.Hash().String()[:8]
		}
	} else {
		// Pull
		logger.Info("Pulling repository", "repo", site.Repo, "dest", destDir)
		
		repo, err := git.PlainOpen(destDir)
		if err != nil {
			return fmt.Errorf("failed to open repo: %w", err)
		}

		worktree, err := repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}

		pullOpts := &git.PullOptions{
			ReferenceName: plumbing.NewBranchReferenceName(site.Branch),
			SingleBranch:  true,
			Depth:         1,
			Force:         true,
		}
		if auth != nil {
			pullOpts.Auth = auth
		}

		err = worktree.Pull(pullOpts)
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("git pull failed: %w", err)
		}

		head, err := repo.Head()
		if err != nil {
			logger.V(1).Info("Failed to get HEAD after pull", "error", err)
		}
		if head != nil {
			commitHash = head.Hash().String()[:8]
		}
	}

	// If a subpath is defined, create symlink
	// e.g. /sites/mysite -> /sites/.repos/mysite/dist
	if hasSubpath {
		if err := s.setupSubpath(site.Name, destDir, site.Path); err != nil {
			return fmt.Errorf("failed to setup subpath: %w", err)
		}
	}

	// Update status
	s.updateStatus(ctx, site, "Ready", "Synced successfully", commitHash)
	
	logger.Info("Sync complete", "site", site.Name, "commit", commitHash)
	return nil
}

// setupSubpath creates a symlink for subpaths
// The repo is cloned to .repos/<name> and a symlink from
// /sites/<name> -> /sites/.repos/<name>/<subpath> is created
func (s *Syncer) setupSubpath(siteName, repoDir, subpath string) error {
	// Normalize subpath (remove leading /)
	subpath = filepath.Clean(subpath)
	if subpath[0] == '/' {
		subpath = subpath[1:]
	}

	// Source directory: the cloned repo + subpath
	srcDir := filepath.Join(repoDir, subpath)

	// Check if subpath exists
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("subpath %q does not exist in repository", subpath)
	}

	// Symlink path: /sites/<name> (where nginx expects the files)
	linkPath := filepath.Join(s.SitesRoot, siteName)

	// Remove old symlink or directory if present
	if err := removePathOrSymlink(linkPath); err != nil {
		return fmt.Errorf("failed to remove existing path %s: %w", linkPath, err)
	}

	// Create symlink
	return os.Symlink(srcDir, linkPath)
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

// updateStatus updates the status of the StaticSite
func (s *Syncer) updateStatus(ctx context.Context, site *staticSiteData, phase, message, commit string) {
	now := metav1.Now()
	
	patch := fmt.Sprintf(`{
		"status": {
			"phase": %q,
			"message": %q,
			"lastSync": %q,
			"lastCommit": %q
		}
	}`, phase, message, now.Format(time.RFC3339), commit)

	_, err := s.DynamicClient.Resource(staticSiteGVR).
		Namespace(site.Namespace).
		Patch(ctx, site.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}, "status")
	
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
		s.SecretRef = &secretRef{
			Name: secretRefMap["name"].(string),
		}
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

		// Skip .repos directory (handled separately)
		if name == ".repos" {
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
