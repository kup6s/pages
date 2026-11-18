// Package syncer - move clones of the layout before #158
package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// migrateLegacyLayout moves legacy clones .repos/<name> to
// .clones/<namespace>/<name> before the first sync, so an upgrade neither
// clones again nor interrupts a site: the legacy link /<name> keeps serving
// from the moved clone until the controller has switched the site to
// /<namespace>/<name>, and Cleanup removes the legacy link afterwards.
func (s *Syncer) migrateLegacyLayout(ctx context.Context) {
	logger := log.FromContext(ctx)
	if _, err := os.Stat(filepath.Join(s.SitesRoot, reposDirName)); err != nil {
		return
	}
	list, err := s.DynamicClient.Resource(staticSiteGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Cannot list StaticSites, legacy clones stay until the next start")
		return
	}
	for i := range list.Items {
		site := &staticSiteData{}
		if err := site.fromUnstructured(&list.Items[i]); err != nil {
			continue
		}
		if err := s.migrateSite(ctx, site); err != nil {
			logger.Error(err, "Failed to move legacy clone", "namespace", site.Namespace, "name", site.Name)
		}
	}
}

// migrateSite moves the legacy clone of the site, if the clone was made for
// it. Another site of the same name, or a broken clone, gets a fresh clone
// from the next sync instead.
func (s *Syncer) migrateSite(ctx context.Context, site *staticSiteData) error {
	lock := s.locks.get(siteKey(site.Namespace, site.Name))
	lock.Lock()
	defer lock.Unlock()

	legacy := filepath.Join(s.SitesRoot, reposDirName, site.Name)
	final := s.cloneDir(site.Namespace, site.Name)
	if _, err := os.Lstat(legacy); err != nil {
		return nil
	}
	if _, err := os.Lstat(final); err == nil {
		return nil
	}
	subpath, err := normalizeSubpath(site.Path)
	if err != nil || !s.cloneUsable(ctx, legacy, site, subpath) {
		return nil
	}
	if err := s.ensureNamespaceDir(site.Namespace); err != nil {
		if syncReason(err) == pagesv1.SyncReasonLegacyPathInUse {
			log.FromContext(ctx).Info("Legacy clone waits for its namespace directory", "namespace", site.Namespace, "name", site.Name, "reason", err.Error())
			return nil
		}
		return err
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	if err := os.Rename(legacy, final); err != nil {
		return err
	}
	if err := s.relinkLegacyPath(site.Name, legacy, final); err != nil {
		// Put the clone back, so the legacy path does not dangle.
		if rerr := os.Rename(final, legacy); rerr != nil {
			log.FromContext(ctx).Error(rerr, "Failed to put legacy clone back", "path", legacy)
		}
		return err
	}
	if err := s.linkSite(site, subpath); err != nil {
		return err
	}
	log.FromContext(ctx).Info("Moved legacy clone", "namespace", site.Namespace, "name", site.Name, "path", final)
	return s.setContentPath(ctx, site)
}

// relinkLegacyPath points the legacy link /<name> into the moved clone, so
// the site keeps serving there until its prefix has switched.
func (s *Syncer) relinkLegacyPath(name, legacy, final string) error {
	link := filepath.Join(s.SitesRoot, name)
	current, err := os.Readlink(link)
	if err != nil {
		return nil
	}
	rest, ok := strings.CutPrefix(current, legacy)
	if !ok || (rest != "" && !strings.HasPrefix(rest, string(filepath.Separator))) {
		return nil
	}
	return replaceSymlink(final+rest, link)
}
