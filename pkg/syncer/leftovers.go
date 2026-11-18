// Package syncer - per-site locking and cleanup of interrupted work
package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// siteLocks serialises work on one site, keyed by siteKey.
type siteLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (l *siteLocks) get(name string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}
	m, ok := l.locks[name]
	if !ok {
		m = &sync.Mutex{}
		l.locks[name] = m
	}
	return m
}

// removeLeftovers deletes temporary clones, previous clones and temporary
// links of interrupted syncs. Entries of a site that is syncing are kept.
// Scans the clone directories only for .tmp-/.old- and the link directories
// only for .tmp-link-, in each namespace and in the legacy layout.
func (s *Syncer) removeLeftovers(ctx context.Context) {
	clones := filepath.Join(s.SitesRoot, clonesDirName)
	for _, namespace := range subdirs(clones) {
		s.removeTempEntries(ctx, filepath.Join(clones, namespace), namespace, tmpRepoPrefix, oldRepoPrefix)
	}
	for _, namespace := range subdirs(s.SitesRoot) {
		s.removeTempEntries(ctx, filepath.Join(s.SitesRoot, namespace), namespace, tmpLinkPrefix)
	}
	// Entries of the legacy layout carry no namespace, and no sync writes
	// them any more, so their lock is never held by a sync.
	s.removeTempEntries(ctx, filepath.Join(s.SitesRoot, reposDirName), "", tmpRepoPrefix, oldRepoPrefix)
	s.removeTempEntries(ctx, s.SitesRoot, "", tmpLinkPrefix)
}

// subdirs lists the directories in dir, without dot entries and symlinks.
func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// removeTempEntries removes temporary entries in dir. namespace selects the
// lock of the site an entry belongs to; empty means the legacy layout.
func (s *Syncer) removeTempEntries(ctx context.Context, dir, namespace string, prefixes ...string) {
	logger := log.FromContext(ctx)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		for _, prefix := range prefixes {
			name, ok := siteFromTempName(prefix, entry.Name())
			if !ok {
				continue
			}
			key := name
			if namespace != "" {
				key = siteKey(namespace, name)
			}
			lock := s.locks.get(key)
			if lock.TryLock() {
				p := filepath.Join(dir, entry.Name())
				if err := removePathOrSymlink(p); err != nil {
					logger.Error(err, "Failed to remove leftover", "path", p)
				} else {
					logger.Info("Removed leftover", "path", p)
				}
				lock.Unlock()
			}
			break
		}
	}
}
