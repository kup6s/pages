package syncer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoveLeftovers_RemovesTemporaryEntries(t *testing.T) {
	root := t.TempDir()
	repos := filepath.Join(root, reposDirName)
	mkdirs(t, filepath.Join(repos, ".tmp-site-0123abcd"), filepath.Join(repos, ".old-site-0123abcd"), filepath.Join(repos, "site"))
	if err := os.Symlink(filepath.Join(repos, "site"), filepath.Join(root, ".tmp-link-site-0123abcd")); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{SitesRoot: root}

	s.removeLeftovers(context.Background())

	if got := listDir(t, repos); !slices.Equal(got, []string{"site"}) {
		t.Errorf(".repos = %v, want [site]", got)
	}
	if got := listDir(t, root); !slices.Equal(got, []string{reposDirName}) {
		t.Errorf("sites root = %v, want [.repos]", got)
	}
}

// Review focus 5
func TestRemoveLeftovers_DistinguishesSimilarNames(t *testing.T) {
	root := t.TempDir()
	repos := filepath.Join(root, reposDirName)
	busy := filepath.Join(repos, ".tmp-archive-0123abcd")
	idle := filepath.Join(repos, ".tmp-archive-2018-0123abcd")
	mkdirs(t, busy, idle)
	s := &Syncer{SitesRoot: root}

	lock := s.locks.get("archive")
	lock.Lock()
	s.removeLeftovers(context.Background())
	lock.Unlock()

	if _, err := os.Stat(busy); err != nil {
		t.Error("temp clone of the running sync of 'archive' was removed")
	}
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Error("leftover of 'archive-2018' was kept")
	}
}

func TestCleanup_KeepsDotEntries(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, ".tmp-link-busy-0123abcd")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{SitesRoot: root, DynamicClient: &fakeDynamicClient{activeSites: []string{"busy"}}}
	lock := s.locks.get("busy")
	lock.Lock()
	defer lock.Unlock()

	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("Cleanup removed the temporary link of a running sync")
	}
}

func TestSyncSite_WaitsForRunningSyncOfSameSite(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	lock := s.locks.get("site")
	lock.Lock()

	done := make(chan error, 1)
	go func() { done <- s.syncSite(context.Background(), testSite(r, "/2018")) }()

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(s.repoDir("site")); !os.IsNotExist(err) {
		t.Error("syncSite cloned while another sync held the site lock")
	}
	lock.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("syncSite: %v", err)
	}
}
