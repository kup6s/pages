package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// stubFreeSpace makes statFS report percent free of a 100 GiB volume.
func stubFreeSpace(t *testing.T, percent uint64) {
	t.Helper()
	saved := statFS
	statFS = func(string) (uint64, uint64, error) {
		const total = 100 << 30
		return total / 100 * percent, total, nil
	}
	t.Cleanup(func() { statFS = saved })
}

func TestCheckFreeSpace(t *testing.T) {
	for _, tt := range []struct {
		min     float64
		free    uint64
		wantErr bool
	}{
		{min: 10, free: 50, wantErr: false},
		{min: 10, free: 9, wantErr: true},
		{min: 0, free: 1, wantErr: false},
	} {
		stubFreeSpace(t, tt.free)
		err := (&Syncer{SitesRoot: "/sites", MinFreePercent: tt.min}).checkFreeSpace()
		if (err != nil) != tt.wantErr {
			t.Errorf("min=%v free=%v%%: err = %v, wantErr %v", tt.min, tt.free, err, tt.wantErr)
		}
		if err != nil && syncReason(err) != pagesv1.SyncReasonDiskFull {
			t.Errorf("reason = %q, want DiskFull", syncReason(err))
		}
	}

	saved := statFS
	statFS = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	t.Cleanup(func() { statFS = saved })
	if err := (&Syncer{MinFreePercent: 0}).checkFreeSpace(); err != nil {
		t.Errorf("disabled guard returned %v on statfs error", err)
	}
}

func TestSyncSite_DiskFullSkipsClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	s.MinFreePercent = 10
	stubFreeSpace(t, 5)

	err := s.syncSite(context.Background(), testSite(r, "/2018"))
	if syncReason(err) != pagesv1.SyncReasonDiskFull {
		t.Fatalf("err = %v (reason %q), want DiskFull", err, syncReason(err))
	}
	if names := listDir(t, s.SitesRoot); len(names) != 0 {
		t.Errorf("sites root = %v, want nothing cloned", names)
	}
}

func TestCloneSite_WatchdogCancelsClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	s.Git = &gitCLI{Binary: sleepingGit(t)}
	s.MinFreePercent = 10
	stubFreeSpace(t, 1)
	saved := freeSpaceCheckInterval
	freeSpaceCheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { freeSpaceCheckInterval = saved })

	start := time.Now()
	err := s.cloneSite(context.Background(), testSite(r, "/2018"), "2018", nil)
	if syncReason(err) != pagesv1.SyncReasonDiskFull {
		t.Fatalf("err = %v (reason %q), want DiskFull", err, syncReason(err))
	}
	if time.Since(start) > 5*time.Second {
		t.Error("watchdog did not stop the clone")
	}
	assertNoTempEntries(t, s)
}
