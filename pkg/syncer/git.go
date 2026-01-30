// Package syncer synchronisiert Git Repos für alle StaticSites
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

// Syncer synchronisiert alle StaticSites
type Syncer struct {
	DynamicClient dynamic.Interface
	ClientSet     kubernetes.Interface

	// Basisverzeichnis für Sites (z.B. /sites)
	SitesRoot string

	// Default Sync-Interval
	DefaultInterval time.Duration

	// AllowedHosts ist eine Liste erlaubter Git-Hosts (SSRF-Protection)
	// Wenn leer, sind alle Hosts erlaubt
	AllowedHosts []string
}

// validateRepoURL prüft ob die Repo-URL erlaubt ist (SSRF-Protection)
func (s *Syncer) validateRepoURL(repoURL string) error {
	// Wenn keine Allowlist konfiguriert, alles erlauben
	if len(s.AllowedHosts) == 0 {
		return nil
	}

	parsed, err := url.Parse(repoURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Nur HTTP(S) erlauben
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s (only http/https allowed)", parsed.Scheme)
	}

	// Host gegen Allowlist prüfen
	host := strings.ToLower(parsed.Host)
	// Port entfernen falls vorhanden
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
	Group:    "pages.kup6s.io",
	Version:  "v1alpha1",
	Resource: "staticsites",
}

// SyncAll synchronisiert alle StaticSites
func (s *Syncer) SyncAll(ctx context.Context) error {
	logger := log.FromContext(ctx)
	
	// Alle StaticSites aus allen Namespaces laden
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

// SyncOne synchronisiert eine einzelne Site (für Webhooks)
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

// syncSite synchronisiert eine einzelne Site
func (s *Syncer) syncSite(ctx context.Context, site *staticSiteData) error {
	logger := log.FromContext(ctx)

	// SSRF-Protection: Repo-URL validieren
	if err := s.validateRepoURL(site.Repo); err != nil {
		return fmt.Errorf("repo URL validation failed: %w", err)
	}

	// Zielverzeichnis für das Repo
	// Bei Subpath: Klone nach .repos/<name>, Symlink nach <name>
	// Ohne Subpath: Klone direkt nach <name>
	var destDir string
	hasSubpath := site.Path != "" && site.Path != "/"
	if hasSubpath {
		destDir = filepath.Join(s.SitesRoot, ".repos", site.Name)
	} else {
		destDir = filepath.Join(s.SitesRoot, site.Name)
	}
	
	// Git Auth falls vorhanden
	var auth *http.BasicAuth
	if site.SecretRef != nil {
		password, err := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, site.SecretRef.Key)
		if err != nil {
			return fmt.Errorf("failed to get git credentials: %w", err)
		}
		auth = &http.BasicAuth{
			Username: "git", // Username ist bei Tokens egal
			Password: password,
		}
	}

	var commitHash string

	// Prüfen ob Repo bereits existiert
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

		head, _ := repo.Head()
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

		head, _ := repo.Head()
		if head != nil {
			commitHash = head.Hash().String()[:8]
		}
	}

	// Wenn ein Subpath definiert ist, Symlink erstellen
	// z.B. /sites/mysite -> /sites/.repos/mysite/dist
	if hasSubpath {
		if err := s.setupSubpath(site.Name, destDir, site.Path); err != nil {
			return fmt.Errorf("failed to setup subpath: %w", err)
		}
	}

	// Status aktualisieren
	s.updateStatus(ctx, site, "Ready", "Synced successfully", commitHash)
	
	logger.Info("Sync complete", "site", site.Name, "commit", commitHash)
	return nil
}

// setupSubpath erstellt einen Symlink für Subpaths
// Das Repo wird in .repos/<name> geklont und ein Symlink von
// /sites/<name> -> /sites/.repos/<name>/<subpath> erstellt
func (s *Syncer) setupSubpath(siteName, repoDir, subpath string) error {
	// Subpath normalisieren (führenden / entfernen)
	subpath = filepath.Clean(subpath)
	if subpath[0] == '/' {
		subpath = subpath[1:]
	}

	// Quellverzeichnis: Das geklonte Repo + Subpath
	srcDir := filepath.Join(repoDir, subpath)

	// Prüfen ob Subpath existiert
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("subpath %q does not exist in repository", subpath)
	}

	// Symlink-Pfad: /sites/<name> (wo nginx die Dateien erwartet)
	linkPath := filepath.Join(s.SitesRoot, siteName)

	// Alten Symlink entfernen falls vorhanden
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			os.Remove(linkPath)
		}
	}

	// Symlink erstellen
	return os.Symlink(srcDir, linkPath)
}

// getSecretValue liest einen Wert aus einem Kubernetes Secret
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

// updateStatus aktualisiert den Status der StaticSite
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

// staticSiteData ist eine vereinfachte Struktur für den Syncer
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

// RunLoop startet die Sync-Schleife
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
			// Cleanup nach jedem Sync
			if err := s.Cleanup(ctx); err != nil {
				logger.Error(err, "Cleanup failed")
			}
		}
	}
}

// Cleanup entfernt Verzeichnisse von gelöschten Sites
func (s *Syncer) Cleanup(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Alle StaticSites laden
	list, err := s.DynamicClient.Resource(staticSiteGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StaticSites: %w", err)
	}

	// Set mit allen aktiven Site-Namen
	activeSites := make(map[string]bool)
	for _, item := range list.Items {
		activeSites[item.GetName()] = true
	}

	// Verzeichnisse in /sites durchgehen
	entries, err := os.ReadDir(s.SitesRoot)
	if err != nil {
		return fmt.Errorf("failed to read sites directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		// .repos Verzeichnis überspringen (wird separat behandelt)
		if name == ".repos" {
			continue
		}

		// Prüfen ob Site noch existiert
		if !activeSites[name] {
			sitePath := filepath.Join(s.SitesRoot, name)
			logger.Info("Removing orphaned site directory", "name", name)

			// Symlink oder Verzeichnis entfernen
			if info, err := os.Lstat(sitePath); err == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					os.Remove(sitePath)
				} else {
					os.RemoveAll(sitePath)
				}
			}
		}
	}

	// .repos Verzeichnisse aufräumen
	reposDir := filepath.Join(s.SitesRoot, ".repos")
	if entries, err := os.ReadDir(reposDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !activeSites[name] {
				repoPath := filepath.Join(reposDir, name)
				logger.Info("Removing orphaned repo directory", "name", name)
				os.RemoveAll(repoPath)
			}
		}
	}

	return nil
}

// DeleteSite löscht eine spezifische Site (für Webhook-Aufrufe)
func (s *Syncer) DeleteSite(ctx context.Context, name string) error {
	logger := log.FromContext(ctx)
	logger.Info("Deleting site", "name", name)

	// Symlink/Verzeichnis in /sites entfernen
	sitePath := filepath.Join(s.SitesRoot, name)
	if info, err := os.Lstat(sitePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			os.Remove(sitePath)
		} else {
			os.RemoveAll(sitePath)
		}
	}

	// Repo-Verzeichnis in .repos entfernen
	repoPath := filepath.Join(s.SitesRoot, ".repos", name)
	os.RemoveAll(repoPath)

	return nil
}
