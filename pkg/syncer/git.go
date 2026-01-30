// Package syncer synchronisiert Git Repos für alle StaticSites
package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	
	// Zielverzeichnis
	destDir := filepath.Join(s.SitesRoot, site.Name)
	
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

	// Wenn ein Subpath definiert ist, symlink erstellen
	// z.B. /sites/mysite -> /sites/.repos/mysite/dist
	if site.Path != "" && site.Path != "/" {
		if err := s.setupSubpath(destDir, site.Path); err != nil {
			return fmt.Errorf("failed to setup subpath: %w", err)
		}
	}

	// Status aktualisieren
	s.updateStatus(ctx, site, "Ready", "Synced successfully", commitHash)
	
	logger.Info("Sync complete", "site", site.Name, "commit", commitHash)
	return nil
}

// setupSubpath erstellt einen Symlink für Subpaths
func (s *Syncer) setupSubpath(repoDir, subpath string) error {
	// Hier könnte man einen Symlink erstellen
	// Für nginx ist es einfacher, den Subpath direkt zu servieren
	// Das machen wir aber über die addPrefix Middleware
	return nil
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
		Patch(ctx, site.Name, "application/merge-patch+json", []byte(patch), metav1.PatchOptions{}, "status")
	
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
		}
	}
}
