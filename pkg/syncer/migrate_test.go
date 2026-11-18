package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// legacySite lays out a site as the syncer before #158 left it: the clone in
// .repos/<name>, served through /<name>. A sentinel in .git shows whether the
// clone was moved or cloned again.
func legacySite(t *testing.T, s *Syncer, site *staticSiteData, subpath string) {
	t.Helper()
	if err := s.cloneSite(context.Background(), site, subpath, nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	legacy := filepath.Join(s.SitesRoot, reposDirName, site.Name)
	mkdirs(t, filepath.Dir(legacy))
	if err := os.Rename(s.cloneDir(site.Namespace, site.Name), legacy); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(s.SitesRoot, site.Namespace), filepath.Join(s.SitesRoot, clonesDirName)} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, filepath.Join(legacy, subpath), filepath.Join(s.SitesRoot, site.Name))
	if err := os.WriteFile(filepath.Join(legacy, ".git", "sentinel"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func contentPathOf(t *testing.T, c *dynamicfake.FakeDynamicClient, namespace, name string) string {
	t.Helper()
	obj, err := c.Resource(staticSiteGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	contentPath, _, _ := unstructured.NestedString(obj.Object, "status", "contentPath")
	return contentPath
}

func TestMigrate_MovesLegacyCloneWithoutCloningAgain(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacySite(t, s, testSite(r, "/2018"), "2018")
	c := newSitesClient(t, fakeSite{namespace: "default", name: "site", repo: r.URL, path: "/2018"})
	s.DynamicClient = c

	s.migrateLegacyLayout(context.Background())

	if _, err := os.Stat(filepath.Join(s.cloneDir("default", "site"), ".git", "sentinel")); err != nil {
		t.Errorf("clone was not moved to .clones/default/site: %v", err)
	}
	if exists(filepath.Join(s.SitesRoot, reposDirName, "site")) {
		t.Error("legacy clone .repos/site still exists")
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("default/site serves %q, want 2018 v1", got)
	}
	if got := readFile(t, filepath.Join(s.SitesRoot, "site", "index.html")); got != "2018 v1" {
		t.Errorf("legacy path /site serves %q, want 2018 v1 until the prefix switched", got)
	}
	if got := contentPathOf(t, c, "default", "site"); got != "/default/site" {
		t.Errorf("status.contentPath = %q, want /default/site", got)
	}
}

// Two sites share the legacy name; the clone goes to the one it was made for.
func TestMigrate_LegacyCloneGoesToTheSiteItMatches(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacySite(t, s, siteIn(r, "team-b", "/2018"), "2018")
	c := newSitesClient(t,
		fakeSite{namespace: "team-a", name: "site", repo: r.URL, path: "/2005"},
		fakeSite{namespace: "team-b", name: "site", repo: r.URL, path: "/2018"})
	s.DynamicClient = c

	s.migrateLegacyLayout(context.Background())

	if _, err := os.Stat(filepath.Join(s.cloneDir("team-b", "site"), ".git", "sentinel")); err != nil {
		t.Errorf("team-b/site did not get the legacy clone: %v", err)
	}
	if exists(s.cloneDir("team-a", "site")) {
		t.Error("team-a/site got a clone made for another spec")
	}
	if got := contentPathOf(t, c, "team-a", "site"); got != "" {
		t.Errorf("team-a/site status.contentPath = %q before its first sync", got)
	}
}

func TestMigrate_WaitsWhileLegacyLinkTakesNamespaceDirectory(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacySite(t, s, testSite(r, "/2018"), "2018")
	blocker := filepath.Join(s.SitesRoot, "default")
	symlink(t, filepath.Join(s.SitesRoot, reposDirName, "default"), blocker)
	s.DynamicClient = newSitesClient(t, fakeSite{namespace: "default", name: "site", repo: r.URL, path: "/2018"})

	s.migrateLegacyLayout(context.Background())

	if !exists(filepath.Join(s.SitesRoot, reposDirName, "site")) {
		t.Error("legacy clone moved although its namespace directory is taken")
	}
	if got := readFile(t, filepath.Join(s.SitesRoot, "site", "index.html")); got != "2018 v1" {
		t.Errorf("legacy path /site serves %q, want 2018 v1", got)
	}
}

func TestRunLoop_MovesLegacyClonesBeforeTheFirstSync(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacySite(t, s, testSite(r, "/2018"), "2018")
	s.DynamicClient = newSitesClient(t, fakeSite{namespace: "default", name: "site", repo: r.URL, path: "/2018"})
	s.DefaultInterval = time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()
	for !exists(s.siteLink("default", "site")) && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if _, err := os.Stat(filepath.Join(s.cloneDir("default", "site"), ".git", "sentinel")); err != nil {
		t.Errorf("the first sync cloned again instead of using the moved clone: %v", err)
	}
}

// When the legacy link cannot follow the clone, the clone goes back, so the
// legacy path never dangles.
func TestMigrate_FailedRelinkPutsTheCloneBack(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacySite(t, s, testSite(r, "/2018"), "2018")
	mkdirs(t, filepath.Join(s.SitesRoot, "default"), filepath.Join(s.SitesRoot, clonesDirName, "default"))
	s.DynamicClient = newSitesClient(t, fakeSite{namespace: "default", name: "site", repo: r.URL, path: "/2018"})
	// The legacy link lives in the sites root; without write access it cannot be replaced.
	if err := os.Chmod(s.SitesRoot, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.SitesRoot, 0o755) })

	s.migrateLegacyLayout(context.Background())

	if got := readFile(t, filepath.Join(s.SitesRoot, "site", "index.html")); got != "2018 v1" {
		t.Errorf("legacy path /site serves %q after a failed move, want 2018 v1", got)
	}
	if exists(s.cloneDir("default", "site")) {
		t.Error("clone stayed in .clones although the legacy link could not follow it")
	}
}

// A site named like its namespace has its own legacy link where its namespace
// directory belongs (#192). It replaces that link instead of waiting for it.
func TestMigrate_MovesSiteNamedLikeItsNamespace(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := &staticSiteData{Name: "docs", Namespace: "docs", Repo: r.URL, Branch: "main", Path: "/2018"}
	legacySite(t, s, site, "2018")
	c := newSitesClient(t, fakeSite{namespace: "docs", name: "docs", repo: r.URL, path: "/2018"})
	s.DynamicClient = c

	s.migrateLegacyLayout(context.Background())

	if _, err := os.Stat(filepath.Join(s.cloneDir("docs", "docs"), ".git", "sentinel")); err != nil {
		t.Errorf("clone was not moved to .clones/docs/docs: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(s.SitesRoot, "docs")); err != nil || !info.IsDir() {
		t.Fatalf("/docs is not the namespace directory: %v", err)
	}
	if got := readFile(t, filepath.Join(s.siteLink("docs", "docs"), "index.html")); got != "2018 v1" {
		t.Errorf("docs/docs serves %q, want 2018 v1", got)
	}
	if got := contentPathOf(t, c, "docs", "docs"); got != "/docs/docs" {
		t.Errorf("status.contentPath = %q, want /docs/docs", got)
	}
}
