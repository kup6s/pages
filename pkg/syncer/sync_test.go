package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func newTestSyncer(t *testing.T, r *testRemote, secrets ...*corev1.Secret) *Syncer {
	t.Helper()
	return &Syncer{
		SitesRoot:     t.TempDir(),
		AllowedHosts:  []string{r.Host},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"site"}},
		ClientSet:     newFakeClientset(secrets...),
		Git:           &gitCLI{Timeout: time.Minute},
	}
}

func testSite(r *testRemote, path string) *staticSiteData {
	return &staticSiteData{Name: "site", Namespace: "default", Repo: r.URL, Branch: "main", Path: path}
}

// servedFile reads a file through the site symlink, as nginx would.
func servedFile(t *testing.T, s *Syncer, rel string) string {
	t.Helper()
	return readFile(t, filepath.Join(s.SitesRoot, "site", rel))
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertNoTempEntries(t *testing.T, s *Syncer) {
	t.Helper()
	for _, dir := range []string{s.SitesRoot, filepath.Join(s.SitesRoot, reposDirName)} {
		for _, name := range listDir(t, dir) {
			if strings.HasPrefix(name, ".tmp-") || strings.HasPrefix(name, ".old-") {
				t.Errorf("temporary entry %s left in %s", name, dir)
			}
		}
	}
}

func TestCloneSite_ServesSubpathThroughSymlink(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)

	if err := s.cloneSite(context.Background(), testSite(r, "/2018"), "2018", nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served index.html = %q, want 2018 v1", got)
	}
	if link, _ := os.Readlink(filepath.Join(s.SitesRoot, "site")); link != filepath.Join(s.repoDir("site"), "2018") {
		t.Errorf("site -> %q", link)
	}
	if _, err := os.Stat(filepath.Join(s.repoDir("site"), ".git", markerFile)); err != nil {
		t.Errorf("marker missing: %v", err)
	}
	assertNoTempEntries(t, s)
}

func TestCloneSite_RootPathLinksRepository(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)

	if err := s.cloneSite(context.Background(), testSite(r, "/"), "", nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	if got := servedFile(t, s, "index.html"); got != "root" {
		t.Errorf("served index.html = %q, want root", got)
	}
}

func TestCloneSite_ReplacesLegacyDirectory(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacy := filepath.Join(s.SitesRoot, "site")
	if err := os.MkdirAll(filepath.Join(legacy, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "nested/old.html"} {
		if err := os.WriteFile(filepath.Join(legacy, f), []byte("legacy"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.cloneSite(context.Background(), testSite(r, "/"), "", nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	if info, err := os.Lstat(legacy); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("site is not a symlink after migration (err=%v)", err)
	}
	if got := servedFile(t, s, "index.html"); got != "root" {
		t.Errorf("served index.html = %q, want root", got)
	}
	if _, err := os.Stat(filepath.Join(s.SitesRoot, "site", "nested", "old.html")); err == nil {
		t.Error("legacy content still present")
	}
	assertNoTempEntries(t, s)
}

func TestCloneSite_FailureKeepsPreviousClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	ctx := context.Background()
	if err := s.cloneSite(ctx, testSite(r, "/2018"), "2018", nil); err != nil {
		t.Fatalf("first clone: %v", err)
	}

	broken := testSite(r, "/2018")
	broken.Branch = "does-not-exist"
	err := s.cloneSite(ctx, broken, "2018", nil)
	if syncReason(err) != pagesv1.SyncReasonCloneFailed {
		t.Fatalf("err = %v (reason %q), want CloneFailed", err, syncReason(err))
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served content changed to %q after failed clone", got)
	}
	assertNoTempEntries(t, s)
}

func TestCloneSite_MissingSubpath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)

	err := s.cloneSite(context.Background(), testSite(r, "/1999"), "1999", nil)
	if syncReason(err) != pagesv1.SyncReasonSubpathMissing {
		t.Fatalf("err = %v (reason %q), want SubpathMissing", err, syncReason(err))
	}
	if _, err := os.Lstat(filepath.Join(s.SitesRoot, "site")); !os.IsNotExist(err) {
		t.Error("site link created for a missing subpath")
	}
	assertNoTempEntries(t, s)
}
