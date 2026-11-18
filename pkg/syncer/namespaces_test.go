package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func siteIn(r *testRemote, namespace, path string) *staticSiteData {
	return &staticSiteData{Name: "site", Namespace: namespace, Repo: r.URL, Branch: "main", Path: path}
}

func TestSyncSite_SameNameInTwoNamespacesGetsTwoDirectories(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	ctx := context.Background()

	if err := s.syncSite(ctx, siteIn(r, "team-a", "/2018")); err != nil {
		t.Fatalf("sync team-a/site: %v", err)
	}
	if err := s.syncSite(ctx, siteIn(r, "team-b", "/2005")); err != nil {
		t.Fatalf("sync team-b/site: %v", err)
	}

	if got := readFile(t, filepath.Join(s.SitesRoot, "team-a", "site", "index.html")); got != "2018 v1" {
		t.Errorf("team-a/site serves %q, want 2018 v1", got)
	}
	if got := readFile(t, filepath.Join(s.SitesRoot, "team-b", "site", "index.html")); got != "2005 v1" {
		t.Errorf("team-b/site serves %q, want 2005 v1", got)
	}
	want := filepath.Join(s.SitesRoot, clonesDirName, "team-a", "site", "2018")
	if link, _ := os.Readlink(filepath.Join(s.SitesRoot, "team-a", "site")); link != want {
		t.Errorf("team-a/site -> %q, want %q", link, want)
	}
	assertNoTempEntries(t, s)
}

func TestSyncSite_ReportsContentPath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	c := newStatusClient(t)
	s.DynamicClient = c

	if err := s.syncSite(context.Background(), testSite(r, "/2018")); err != nil {
		t.Fatalf("syncSite: %v", err)
	}
	_, obj := storedConditions(t, c)
	if got, _, _ := unstructured.NestedString(obj.Object, "status", "contentPath"); got != "/default/site" {
		t.Errorf("status.contentPath = %q, want /default/site", got)
	}
}

func TestSyncSite_FailureReportsNoContentPath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	c := newStatusClient(t)
	s.DynamicClient = c

	if err := s.syncSite(context.Background(), testSite(r, "/missing")); err == nil {
		t.Fatal("syncSite of a missing subpath succeeded")
	}
	_, obj := storedConditions(t, c)
	if got, found, _ := unstructured.NestedString(obj.Object, "status", "contentPath"); found {
		t.Errorf("status.contentPath = %q after a failed first sync", got)
	}
}

// A site named like a namespace still serves from its legacy link /sites/<name>.
// Sites of that namespace wait instead of writing into the legacy content.
func TestSyncSite_WaitsWhileLegacyLinkTakesNamespaceDirectory(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacyTarget := filepath.Join(s.SitesRoot, reposDirName, "default")
	mkdirs(t, legacyTarget)
	legacyLink := filepath.Join(s.SitesRoot, "default")
	if err := os.Symlink(legacyTarget, legacyLink); err != nil {
		t.Fatal(err)
	}

	err := s.syncSite(context.Background(), testSite(r, "/2018"))

	if syncReason(err) != pagesv1.SyncReasonLegacyPathInUse {
		t.Fatalf("err = %v (reason %q), want %s", err, syncReason(err), pagesv1.SyncReasonLegacyPathInUse)
	}
	if target, _ := os.Readlink(legacyLink); target != legacyTarget {
		t.Errorf("legacy link -> %q, want it untouched", target)
	}
	if names := listDir(t, legacyTarget); len(names) != 0 {
		t.Errorf("legacy content got %v", names)
	}
}

func TestDeleteSite_RemovesOnlyItsNamespace(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	ctx := context.Background()
	for _, ns := range []string{"team-a", "team-b"} {
		if err := s.syncSite(ctx, siteIn(r, ns, "/2018")); err != nil {
			t.Fatalf("sync %s/site: %v", ns, err)
		}
	}

	if err := s.DeleteSite(ctx, "team-a", "site"); err != nil {
		t.Fatalf("DeleteSite: %v", err)
	}

	for _, p := range []string{filepath.Join(s.SitesRoot, "team-a", "site"), s.cloneDir("team-a", "site")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if got := readFile(t, filepath.Join(s.SitesRoot, "team-b", "site", "index.html")); got != "2018 v1" {
		t.Errorf("team-b/site serves %q after deleting team-a/site", got)
	}
}
