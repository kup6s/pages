// Package syncer - sync flow of a single site
package syncer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// cloneDir is the clone of a site: .clones/<namespace>/<name>.
func (s *Syncer) cloneDir(namespace, name string) string {
	return filepath.Join(s.SitesRoot, clonesDirName, namespace, name)
}

// siteLink is the served path of a site: /<namespace>/<name>.
func (s *Syncer) siteLink(namespace, name string) string {
	return filepath.Join(s.SitesRoot, namespace, name)
}

// cloneSite clones into a temporary directory and swaps the result into
// place, so an interrupted clone never looks like a valid one.
func (s *Syncer) cloneSite(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) error {
	defer s.Metrics.observeGit("clone", time.Now())
	clones := filepath.Dir(s.cloneDir(site.Namespace, site.Name))
	if err := os.MkdirAll(clones, 0o755); err != nil {
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	tmp := filepath.Join(clones, tempName(tmpRepoPrefix, site.Name))
	log.FromContext(ctx).Info("Cloning repository", "site", site.Name, "repo", site.Repo, "path", subpath)

	cloneCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go s.watchFreeSpace(cloneCtx, cancel)
	if err := s.git().clone(cloneCtx, site.Repo, site.Branch, subpath, tmp, auth); err != nil {
		_ = os.RemoveAll(tmp)
		var diskFull *syncError
		if errors.As(context.Cause(cloneCtx), &diskFull) {
			return diskFull
		}
		return classifyGitError(pagesv1.SyncReasonCloneFailed, err)
	}
	if info, err := s.git().inspect(ctx, tmp); err == nil && info.Branch != site.Branch {
		_ = os.RemoveAll(tmp)
		return newSyncError(pagesv1.SyncReasonInvalidSpec, fmt.Errorf("spec.branch %q is not a branch", site.Branch))
	}
	if err := checkSubpath(tmp, subpath); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".git", markerFile), []byte("1\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	return s.swapClone(ctx, site, subpath, tmp)
}

// swapClone moves a finished clone to its clone directory and points the site at it.
func (s *Syncer) swapClone(ctx context.Context, site *staticSiteData, subpath, tmp string) error {
	final := s.cloneDir(site.Namespace, site.Name)
	old := ""
	if _, err := os.Lstat(final); err == nil {
		old = filepath.Join(filepath.Dir(final), tempName(oldRepoPrefix, site.Name))
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
	if err := s.linkSite(site, subpath); err != nil {
		return err
	}
	if old != "" {
		if err := os.RemoveAll(old); err != nil {
			log.FromContext(ctx).Error(err, "Failed to remove previous clone", "path", old)
		}
	}
	return nil
}

// linkSite points /<namespace>/<name> at the served directory of the clone.
func (s *Syncer) linkSite(site *staticSiteData, subpath string) error {
	if err := s.ensureNamespaceDir(site); err != nil {
		return err
	}
	target := filepath.Join(s.cloneDir(site.Namespace, site.Name), subpath)
	link := s.siteLink(site.Namespace, site.Name)
	if current, err := os.Readlink(link); err == nil && current == target {
		return nil
	}
	if err := replaceSymlink(target, link); err != nil {
		return newSyncError(pagesv1.SyncReasonSyncFailed, err)
	}
	return nil
}

// ensureNamespaceDir creates /<namespace> for the site. Anything else there is
// the legacy path /<name> of a site named like the namespace. If that is this
// site's own name, the legacy path goes now: it can only be removed once the
// site has moved, and the site can only move into this directory (#192). The
// site is unreachable under its legacy prefix until the controller has
// switched it, seconds after the status update. Another site's legacy path
// makes the site wait instead; creating the directory would write into that
// site's content.
func (s *Syncer) ensureNamespaceDir(site *staticSiteData) error {
	dir := filepath.Join(s.SitesRoot, site.Namespace)
	info, err := os.Lstat(dir)
	switch {
	case err == nil && info.IsDir():
		return nil
	case err == nil && site.Name == site.Namespace:
		if err := removePathOrSymlink(dir); err != nil {
			return newSyncError(pagesv1.SyncReasonSyncFailed, fmt.Errorf("remove own legacy path %s: %w", dir, err))
		}
	case err == nil:
		return newSyncError(pagesv1.SyncReasonLegacyPathInUse,
			fmt.Errorf("%s is still the legacy path of a site named %q; it is removed once that site has moved", dir, site.Namespace))
	case !os.IsNotExist(err):
		return newSyncError(pagesv1.SyncReasonSyncFailed, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return newSyncError(pagesv1.SyncReasonSyncFailed, err)
	}
	return nil
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

// recloneSlackBytes is the allowance on top of twice the checkout size.
// git gc never prunes promisor packs, so an overgrown clone is replaced.
var recloneSlackBytes int64 = 50 << 20

// syncSite brings one site to the head of its branch and reports the result.
func (s *Syncer) syncSite(ctx context.Context, site *staticSiteData) error {
	lock := s.locks.get(siteKey(site.Namespace, site.Name))
	lock.Lock()
	defer lock.Unlock()

	commit, err := s.syncSiteContent(ctx, site)
	s.Metrics.observeSync(err)
	if err != nil {
		s.reportStatus(ctx, site, metav1.Condition{
			Status: metav1.ConditionFalse, Reason: syncReason(err), Message: err.Error(),
		}, "")
		return err
	}
	s.reportStatus(ctx, site, metav1.Condition{
		Status: metav1.ConditionTrue, Reason: pagesv1.SyncReasonSynced, Message: "Synced " + commit,
	}, commit)
	log.FromContext(ctx).Info("Sync complete", "site", site.Name, "commit", commit)
	return nil
}

func (s *Syncer) syncSiteContent(ctx context.Context, site *staticSiteData) (string, error) {
	if err := s.validateRepoURL(site.Repo); err != nil {
		return "", newSyncError(pagesv1.SyncReasonInvalidSpec, fmt.Errorf("repo URL validation failed: %w", err))
	}
	subpath, err := normalizeSubpath(site.Path)
	if err != nil {
		return "", newSyncError(pagesv1.SyncReasonInvalidSpec, err)
	}
	auth, err := s.siteAuth(ctx, site)
	if err != nil {
		return "", newSyncError(pagesv1.SyncReasonAuthFailed, fmt.Errorf("failed to get git credentials: %w", err))
	}
	return s.syncRepo(ctx, site, subpath, auth)
}

// siteAuth reads the site's git credentials; nil means anonymous access.
func (s *Syncer) siteAuth(ctx context.Context, site *staticSiteData) (*gitAuth, error) {
	if site.SecretRef == nil {
		return nil, nil
	}
	password, err := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, site.SecretRef.Key)
	if err != nil {
		return nil, err
	}
	username, _ := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, "username")
	if username == "" {
		username = "git"
	}
	return &gitAuth{Username: username, Password: password}, nil
}

// syncRepo updates a usable clone in place and replaces anything else.
func (s *Syncer) syncRepo(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) (string, error) {
	if err := s.checkFreeSpace(); err != nil {
		return "", err
	}
	logger := log.FromContext(ctx)
	dir := s.cloneDir(site.Namespace, site.Name)
	if err := cancelled(ctx); err != nil {
		return "", err
	}
	if !s.cloneUsable(ctx, dir, site, subpath) {
		return s.recloneSite(ctx, site, subpath, auth)
	}

	before, _ := s.git().head(ctx, dir)
	start := time.Now()
	if err := s.git().fetch(ctx, dir, site.Repo, site.Branch, auth); err != nil {
		if cerr := cancelled(ctx); cerr != nil {
			return "", cerr
		}
		if isLockError(err) {
			logger.Info("Fetch blocked by a stale lock, cloning again", "site", site.Name, "error", err.Error())
			return s.recloneSite(ctx, site, subpath, auth)
		}
		return "", classifyGitError(pagesv1.SyncReasonFetchFailed, err)
	}
	if err := s.git().resetHard(ctx, dir, site.Repo, site.Branch, auth); err != nil {
		if cerr := cancelled(ctx); cerr != nil {
			return "", cerr
		}
		logger.Info("Reset failed, cloning again", "site", site.Name, "error", err.Error())
		return s.recloneSite(ctx, site, subpath, auth)
	}
	s.Metrics.observeGit("update", start)
	after, err := s.git().head(ctx, dir)
	if err != nil {
		return s.recloneSite(ctx, site, subpath, auth)
	}
	if after != before && cloneOvergrown(dir) {
		logger.Info("Clone outgrew its checkout, cloning again", "site", site.Name)
		return s.recloneSite(ctx, site, subpath, auth)
	}
	if err := checkSubpath(dir, subpath); err != nil {
		return "", err
	}
	if err := s.linkSite(site, subpath); err != nil {
		return "", err
	}
	return after, nil
}

// cancelled reports an interrupted sync. A cancelled context says nothing
// about the state of the clone, so callers must not replace it.
func cancelled(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	return classifyGitError(pagesv1.SyncReasonSyncFailed, ctx.Err())
}

func (s *Syncer) recloneSite(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) (string, error) {
	s.reportStatus(ctx, site, metav1.Condition{
		Status: metav1.ConditionUnknown, Reason: pagesv1.SyncReasonSyncing, Message: "Cloning " + site.Repo,
	}, "")
	if err := s.cloneSite(ctx, site, subpath, auth); err != nil {
		return "", err
	}
	commit, err := s.git().head(ctx, s.cloneDir(site.Namespace, site.Name))
	if err != nil {
		return "", newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	return commit, nil
}

// cloneUsable reports whether dir is a complete clone of the site's spec.
func (s *Syncer) cloneUsable(ctx context.Context, dir string, site *staticSiteData, subpath string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git", markerFile)); err != nil {
		return false
	}
	info, err := s.git().inspect(ctx, dir)
	if err != nil {
		return false
	}
	return info.OriginURL == site.Repo && info.Branch == site.Branch && info.SparsePath == subpath
}

func cloneOvergrown(dir string) bool {
	gitSize := dirSize(filepath.Join(dir, ".git"))
	checkout := dirSize(dir) - gitSize
	return gitSize > 2*checkout+recloneSlackBytes
}

func dirSize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	return size
}
