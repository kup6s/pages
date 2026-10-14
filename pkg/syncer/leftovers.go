// Package syncer - per-site locking and cleanup of interrupted work
package syncer

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// siteLocks serialises work on one site directory. Directories are keyed by
// name only (see #158), so the lock is too.
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
// Scans .repos only for .tmp-/.old- and the sites root only for .tmp-link-.
func (s *Syncer) removeLeftovers(ctx context.Context) {
	s.removeTempEntries(ctx, filepath.Join(s.SitesRoot, reposDirName), tmpRepoPrefix, oldRepoPrefix)
	s.removeTempEntries(ctx, s.SitesRoot, tmpLinkPrefix)
}

func (s *Syncer) removeTempEntries(ctx context.Context, dir string, prefixes ...string) {
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
			lock := s.locks.get(name)
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
