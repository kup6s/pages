// Package syncer - free-space guard for the shared sites volume
package syncer

import (
	"context"
	"fmt"
	"syscall"
	"time"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// statFS reports free and total bytes of the filesystem holding path.
var statFS = func(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) //nolint:gosec // block size is never negative
	return st.Bavail * bsize, st.Blocks * bsize, nil
}

// freeSpaceCheckInterval is how often a running clone checks free space.
var freeSpaceCheckInterval = 5 * time.Second

// checkFreeSpace fails with DiskFull below MinFreePercent.
func (s *Syncer) checkFreeSpace() error {
	if s.MinFreePercent <= 0 {
		return nil
	}
	free, total, err := statFS(s.SitesRoot)
	if err != nil {
		return newSyncError(pagesv1.SyncReasonDiskFull, fmt.Errorf("cannot read free space of %s: %w", s.SitesRoot, err))
	}
	if total == 0 {
		return nil
	}
	percent := float64(free) * 100 / float64(total)
	if percent < s.MinFreePercent {
		return newSyncError(pagesv1.SyncReasonDiskFull,
			fmt.Errorf("only %.1f%% free on %s, minimum is %.1f%%", percent, s.SitesRoot, s.MinFreePercent))
	}
	return nil
}

// watchFreeSpace cancels ctx with the DiskFull error once free space drops
// below the threshold.
func (s *Syncer) watchFreeSpace(ctx context.Context, cancel context.CancelCauseFunc) {
	if s.MinFreePercent <= 0 {
		return
	}
	ticker := time.NewTicker(freeSpaceCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.checkFreeSpace(); err != nil {
				cancel(err)
				return
			}
		}
	}
}
