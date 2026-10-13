// Package syncer - sync flow of a single site
package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// markerFile marks a complete clone made by this syncer. Clones without it,
// including those made by go-git, are replaced.
const markerFile = "pages-syncer"

func (s *Syncer) git() *gitCLI {
	if s.Git != nil {
		return s.Git
	}
	return &gitCLI{Timeout: s.GitTimeout}
}

func (s *Syncer) repoDir(name string) string {
	return filepath.Join(s.SitesRoot, reposDirName, name)
}

// cloneSite clones into a temporary directory and swaps the result into
// place, so an interrupted clone never looks like a valid one.
func (s *Syncer) cloneSite(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) error {
	reposDir := filepath.Join(s.SitesRoot, reposDirName)
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	tmp := filepath.Join(reposDir, tempName(tmpRepoPrefix, site.Name))
	log.FromContext(ctx).Info("Cloning repository", "site", site.Name, "repo", site.Repo, "path", subpath)

	if err := s.git().clone(ctx, site.Repo, site.Branch, subpath, tmp, auth); err != nil {
		_ = os.RemoveAll(tmp)
		return classifyGitError(pagesv1.SyncReasonCloneFailed, err)
	}
	if err := checkSubpath(tmp, subpath); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".git", markerFile), []byte("1\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	return s.swapClone(ctx, site.Name, subpath, tmp)
}

// swapClone moves a finished clone to .repos/<name> and points the site at it.
func (s *Syncer) swapClone(ctx context.Context, name, subpath, tmp string) error {
	final := s.repoDir(name)
	old := ""
	if _, err := os.Lstat(final); err == nil {
		old = filepath.Join(filepath.Dir(final), tempName(oldRepoPrefix, name))
		if err := os.Rename(final, old); err != nil {
			_ = os.RemoveAll(tmp)
			return newSyncError(pagesv1.SyncReasonCloneFailed, err)
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		if old != "" {
			_ = os.Rename(old, final)
		}
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	if err := s.linkSite(name, subpath); err != nil {
		return newSyncError(pagesv1.SyncReasonSyncFailed, err)
	}
	if old != "" {
		if err := os.RemoveAll(old); err != nil {
			log.FromContext(ctx).Error(err, "Failed to remove previous clone", "path", old)
		}
	}
	return nil
}

// linkSite points /sites/<name> at the served directory of the clone.
func (s *Syncer) linkSite(name, subpath string) error {
	target := filepath.Join(s.repoDir(name), subpath)
	link := filepath.Join(s.SitesRoot, name)
	if current, err := os.Readlink(link); err == nil && current == target {
		return nil
	}
	return replaceSymlink(target, link)
}

// checkSubpath fails when the served directory does not exist in the clone.
func checkSubpath(repo, subpath string) error {
	if subpath == "" {
		return nil
	}
	info, err := os.Stat(filepath.Join(repo, subpath))
	if err != nil || !info.IsDir() {
		return newSyncError(pagesv1.SyncReasonSubpathMissing,
			fmt.Errorf("subpath %q does not exist in repository", subpath))
	}
	return nil
}
