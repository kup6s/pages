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

// markClone drops a file into .git that survives updates but not re-clones.
func markClone(t *testing.T, s *Syncer) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.repoDir("site"), ".git", "sentinel"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func cloneReplaced(s *Syncer) bool {
	_, err := os.Stat(filepath.Join(s.repoDir("site"), ".git", "sentinel"))
	return os.IsNotExist(err)
}

func mustSync(t *testing.T, s *Syncer, site *staticSiteData) {
	t.Helper()
	if err := s.syncSite(context.Background(), site); err != nil {
		t.Fatalf("syncSite: %v", err)
	}
}

func TestSyncSite_UpdatesInPlaceAfterCommitAndForcePush(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if got := servedFile(t, s, "index.html"); got != "2018 v2" {
		t.Errorf("after commit: %q", got)
	}

	r.amend(map[string]string{"2018/index.html": "2018 v3"}, "v3")
	mustSync(t, s, site)
	if got := servedFile(t, s, "index.html"); got != "2018 v3" {
		t.Errorf("after force-push: %q", got)
	}
	if cloneReplaced(s) {
		t.Error("updates re-cloned the site, want in-place update")
	}
}

func TestSyncSite_FetchFailureKeepsServedContent(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)

	r.close()
	err := s.syncSite(context.Background(), site)
	if syncReason(err) != pagesv1.SyncReasonFetchFailed {
		t.Fatalf("err = %v (reason %q), want FetchFailed", err, syncReason(err))
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served content = %q after failed fetch", got)
	}
}

func TestSyncSite_RecloneWhenSpecChanges(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	other := newTestRemote(t, map[string]string{"2018/index.html": "other repo"}, remoteOptions{})
	r.pushBranch("preview", map[string]string{"2018/index.html": "preview"})
	s := newTestSyncer(t, r)
	s.AllowedHosts = []string{r.Host, other.Host}
	site := testSite(r, "/2018")
	mustSync(t, s, site)

	for _, change := range []struct {
		name   string
		apply  func()
		served string
	}{
		{"path", func() { site.Path = "/2005" }, "2005 v1"},
		{"branch", func() { site.Path = "/2018"; site.Branch = "preview" }, "preview"},
		{"repo", func() { site.Repo = other.URL; site.Branch = "main" }, "other repo"},
	} {
		markClone(t, s)
		change.apply()
		mustSync(t, s, site)
		assertNoTempEntries(t, s)
		if !cloneReplaced(s) {
			t.Errorf("%s change did not re-clone", change.name)
		}
		if got := servedFile(t, s, "index.html"); got != change.served {
			t.Errorf("%s change: served %q, want %q", change.name, got, change.served)
		}
	}
}

func TestSyncSite_RecloneBrokenClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	// The state an interrupted clone used to leave: .git exists, HEAD points nowhere.
	head := filepath.Join(s.repoDir("site"), ".git", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, site)
	if !cloneReplaced(s) {
		t.Error("broken clone was not replaced")
	}
}

func TestSyncSite_RecloneLegacyClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	if err := os.MkdirAll(filepath.Join(s.SitesRoot, reposDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	// A full clone without marker, as go-git left it.
	runGit(t, s.SitesRoot, "clone", "--quiet", "--branch", "main", r.URL, s.repoDir("site"))
	markClone(t, s)

	mustSync(t, s, testSite(r, "/2018"))
	if !cloneReplaced(s) {
		t.Error("legacy clone without marker was not replaced")
	}
}

func TestSyncSite_RecloneOvergrownClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	saved := recloneSlackBytes
	recloneSlackBytes = -1 << 40 // every clone counts as overgrown
	t.Cleanup(func() { recloneSlackBytes = saved })

	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if !cloneReplaced(s) {
		t.Error("overgrown clone was not replaced")
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v2" {
		t.Errorf("served %q", got)
	}
}

func TestSyncSite_PrivateRepository(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{User: "deploy", Password: "token"})
	secret := newTestSecret("default", "git-creds", map[string][]byte{
		"username": []byte("deploy"), "password": []byte("token"),
	})
	s := newTestSyncer(t, r, secret)

	site := testSite(r, "/2018")
	site.SecretRef = &secretRef{Name: "git-creds", Key: "password"}
	mustSync(t, s, site)
	markClone(t, s)
	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if got := servedFile(t, s, "index.html"); got != "2018 v2" || cloneReplaced(s) {
		t.Errorf("private update: served %q, replaced=%v", got, cloneReplaced(s))
	}

	anonymous := testSite(r, "/2005")
	anonymous.Name = "anon"
	if err := s.syncSite(context.Background(), anonymous); syncReason(err) != pagesv1.SyncReasonAuthFailed {
		t.Errorf("without secret: err = %v (reason %q), want AuthFailed", err, syncReason(err))
	}
}

func TestSyncSite_InvalidPath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	err := s.syncSite(context.Background(), testSite(r, "/../etc"))
	if syncReason(err) != pagesv1.SyncReasonInvalidSpec {
		t.Errorf("err = %v (reason %q), want InvalidSpec", err, syncReason(err))
	}
}

// Review focus 1
func TestSyncSite_BranchWithSlashUpdatesInPlace(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	r.pushBranch("release/2026", map[string]string{"2018/index.html": "release v1"})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	site.Branch = "release/2026"
	mustSync(t, s, site)
	markClone(t, s)

	mustSync(t, s, site)
	if cloneReplaced(s) {
		t.Error("branch with slash re-cloned on every sync")
	}
	if got := servedFile(t, s, "index.html"); got != "release v1" {
		t.Errorf("served %q", got)
	}
}

// Review focus 2
func TestSyncSite_PathSpellingsDoNotReclone(t *testing.T) {
	for _, path := range []string{"/docs/public/", "./docs/public", "docs/public", "/docs/public"} {
		t.Run(path, func(t *testing.T) {
			r := newTestRemote(t, archiveFiles, remoteOptions{})
			s := newTestSyncer(t, r)
			site := testSite(r, path)
			mustSync(t, s, site)
			markClone(t, s)
			mustSync(t, s, site)
			if cloneReplaced(s) {
				t.Errorf("path %q re-cloned on the second sync", path)
			}
			if got := servedFile(t, s, "index.html"); got != "docs v1" {
				t.Errorf("served %q", got)
			}
		})
	}
}

// Review focus 3
func TestSyncSite_ServerWithoutFilterUpdatesInPlace(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{NoFilter: true})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if cloneReplaced(s) {
		t.Error("server without partial clone support caused a re-clone")
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v2" {
		t.Errorf("served %q", got)
	}
}

// Review focus 4
func TestSyncSite_MissingBranchKeepsServedContent(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)

	site.Branch = "gone"
	err := s.syncSite(context.Background(), site)
	if syncReason(err) != pagesv1.SyncReasonCloneFailed {
		t.Fatalf("err = %v (reason %q), want CloneFailed", err, syncReason(err))
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("error %q does not name the branch", err)
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served content changed to %q", got)
	}
	assertNoTempEntries(t, s)
}

func TestSyncSite_RecloneAfterStaleFetchLock(t *testing.T) {
	for _, lock := range []string{"shallow.lock", "refs/remotes/origin/main.lock"} {
		t.Run(lock, func(t *testing.T) {
			r := newTestRemote(t, archiveFiles, remoteOptions{})
			s := newTestSyncer(t, r)
			site := testSite(r, "/2018")
			mustSync(t, s, site)
			markClone(t, s)
			if err := os.WriteFile(filepath.Join(s.repoDir("site"), ".git", lock), nil, 0o644); err != nil {
				t.Fatal(err)
			}

			r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
			mustSync(t, s, site)
			if !cloneReplaced(s) {
				t.Error("stale lock did not cause a re-clone")
			}
			if got := servedFile(t, s, "index.html"); got != "2018 v2" {
				t.Errorf("served %q", got)
			}
			assertNoTempEntries(t, s)
		})
	}
}

func TestSyncSite_NonAsciiAndQuotedPathsDoNotReclone(t *testing.T) {
	for _, dir := range []string{"übersicht", `quo"te`} {
		t.Run(dir, func(t *testing.T) {
			r := newTestRemote(t, map[string]string{dir + "/index.html": "special"}, remoteOptions{})
			s := newTestSyncer(t, r)
			site := testSite(r, "/"+dir)
			mustSync(t, s, site)
			markClone(t, s)
			mustSync(t, s, site)
			if cloneReplaced(s) {
				t.Errorf("path %q re-cloned on the second sync", dir)
			}
			if got := servedFile(t, s, "index.html"); got != "special" {
				t.Errorf("served %q", got)
			}
		})
	}
}
