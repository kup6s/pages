package syncer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// fakeSite is a StaticSite as the syncer lists it. moved sets status.contentPath
// and, as the controller does once it has switched the site, status.internalURL
// to the per-namespace path; staleRoute keeps the legacy internalURL, as a
// controller that has not switched yet (or predates #158) leaves it.
type fakeSite struct {
	namespace, name string
	moved           bool
	staleRoute      bool
	repo, path      string // defaults: an unreachable repo, "/"
}

func newSitesClient(t *testing.T, sites ...fakeSite) *dynamicfake.FakeDynamicClient {
	t.Helper()
	var objects []runtime.Object
	for _, site := range sites {
		repo, path := site.repo, site.path
		if repo == "" {
			repo = "https://example.org/r.git"
		}
		if path == "" {
			path = "/"
		}
		obj := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "pages.kup6s.com/v1beta1",
			"kind":       "StaticSite",
			"metadata":   map[string]interface{}{"name": site.name, "namespace": site.namespace},
			"spec":       map[string]interface{}{"repo": repo, "path": path},
		}}
		if site.moved {
			if err := unstructured.SetNestedField(obj.Object, pagesv1.ContentPathFor(site.namespace, site.name), "status", "contentPath"); err != nil {
				t.Fatal(err)
			}
			route := pagesv1.ContentPathFor(site.namespace, site.name)
			if site.staleRoute {
				route = "/" + site.name
			}
			if err := unstructured.SetNestedField(obj.Object, "http://pages-nginx.pages.svc"+route+"/", "status", "internalURL"); err != nil {
				t.Fatal(err)
			}
		}
		objects = append(objects, obj)
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{staticSiteGVR: "StaticSiteList"}, objects...)
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdirs(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestCleanup_RemovesOrphansOfTheNamespaceLayout(t *testing.T) {
	root := t.TempDir()
	s := &Syncer{SitesRoot: root, DynamicClient: newSitesClient(t, fakeSite{namespace: "default", name: "site", moved: true})}
	for _, p := range []string{"default/site", "default/gone", "team-b/old"} {
		clone := filepath.Join(root, clonesDirName, p)
		mkdirs(t, clone)
		symlink(t, clone, filepath.Join(root, p))
	}

	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if got := listDir(t, filepath.Join(root, "default")); !slices.Equal(got, []string{"site"}) {
		t.Errorf("default = %v, want [site]", got)
	}
	if got := listDir(t, filepath.Join(root, clonesDirName, "default")); !slices.Equal(got, []string{"site"}) {
		t.Errorf(".clones/default = %v, want [site]", got)
	}
	for _, p := range []string{filepath.Join(root, "team-b"), filepath.Join(root, clonesDirName, "team-b")} {
		if exists(p) {
			t.Errorf("%s of a namespace without sites still exists", p)
		}
	}
}

// The legacy path /docs serves every site named docs that has not moved yet.
func TestCleanup_KeepsLegacyPathUntilEverySiteOfTheNameMoved(t *testing.T) {
	root := t.TempDir()
	legacyClone := filepath.Join(root, reposDirName, "docs")
	legacyLink := filepath.Join(root, "docs")
	mkdirs(t, legacyClone)
	symlink(t, legacyClone, legacyLink)

	s := &Syncer{SitesRoot: root, DynamicClient: newSitesClient(t,
		fakeSite{namespace: "team-a", name: "docs", moved: true}, fakeSite{namespace: "team-b", name: "docs", moved: false})}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !exists(legacyLink) || !exists(legacyClone) {
		t.Fatal("legacy path removed while team-b/docs is still served from it")
	}

	s.DynamicClient = newSitesClient(t, fakeSite{namespace: "team-a", name: "docs", moved: true}, fakeSite{namespace: "team-b", name: "docs", moved: true})
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	for _, p := range []string{legacyLink, legacyClone, filepath.Join(root, reposDirName)} {
		if exists(p) {
			t.Errorf("%s still exists after every site named docs moved", p)
		}
	}
}

// The syncer's contentPath only says the content is in place. Until the
// controller has switched the route, which its internalURL shows, Traefik may
// still send the site's requests to the legacy path.
func TestCleanup_KeepsLegacyPathUntilTheControllerSwitchedTheRoute(t *testing.T) {
	root := t.TempDir()
	legacyLink := filepath.Join(root, "docs")
	symlink(t, filepath.Join(root, clonesDirName, "team-a", "docs"), legacyLink)

	s := &Syncer{SitesRoot: root, DynamicClient: newSitesClient(t,
		fakeSite{namespace: "team-a", name: "docs", moved: true, staleRoute: true})}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !exists(legacyLink) {
		t.Fatal("legacy link removed while the controller still routes team-a/docs to it")
	}
}

// A moved site named like a namespace frees that namespace's directory.
func TestCleanup_RemovesMovedLegacyLinkThatBlocksANamespace(t *testing.T) {
	root := t.TempDir()
	legacyLink := filepath.Join(root, "default")
	symlink(t, filepath.Join(root, clonesDirName, "other", "default"), legacyLink)

	s := &Syncer{SitesRoot: root, DynamicClient: newSitesClient(t,
		fakeSite{namespace: "other", name: "default", moved: true}, fakeSite{namespace: "default", name: "site", moved: false})}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if exists(legacyLink) {
		t.Error("legacy link of the moved site other/default still blocks namespace default")
	}
}

func TestStaticSiteDataReadsContentPath(t *testing.T) {
	c := newSitesClient(t, fakeSite{namespace: "default", name: "site", moved: true})
	obj, err := c.Resource(staticSiteGVR).Namespace("default").Get(context.Background(), "site", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	site := &staticSiteData{}
	if err := site.fromUnstructured(obj); err != nil {
		t.Fatal(err)
	}
	if site.ContentPath != "/default/site" {
		t.Errorf("ContentPath = %q, want /default/site", site.ContentPath)
	}
}
