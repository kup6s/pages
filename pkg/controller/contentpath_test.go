package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// setContentPath writes status.contentPath as the syncer would.
func (e *visibilityEnv) setContentPath(name, contentPath string) {
	e.t.Helper()
	site := e.site(name)
	site.Status.ContentPath = contentPath
	if err := e.c.Status().Update(context.Background(), site); err != nil {
		e.t.Fatalf("update status of %s: %v", name, err)
	}
}

func (e *visibilityEnv) addPrefix(name string) string {
	e.t.Helper()
	mw, err := e.dyn.Resource(middlewareGVR).Namespace("kup6s-pages").Get(context.Background(), "default--"+name+"-prefix", metav1.GetOptions{})
	if err != nil {
		e.t.Fatalf("get addPrefix middleware of %s: %v", name, err)
	}
	prefix, _, _ := unstructured.NestedString(mw.Object, "spec", "addPrefix", "prefix")
	return prefix
}

func TestReconcile_LegacyPathUntilSyncerMovedTheSite(t *testing.T) {
	env := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", pagesv1.StaticSiteSpec{}))

	env.reconcile("docs")

	if got := env.addPrefix("docs"); got != "/docs" {
		t.Errorf("addPrefix = %q, want /docs", got)
	}
	if got := env.site("docs").Status.InternalURL; got != "http://kup6s-pages-nginx.kup6s-pages.svc/docs/" {
		t.Errorf("InternalURL = %q", got)
	}
}

func TestReconcile_ContentPathSwitchesPrefixAndInternalURL(t *testing.T) {
	env := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", pagesv1.StaticSiteSpec{}))
	env.reconcile("docs")

	env.setContentPath("docs", "/default/docs")
	env.reconcile("docs")

	if got := env.addPrefix("docs"); got != "/default/docs" {
		t.Errorf("addPrefix = %q, want /default/docs", got)
	}
	site := env.site("docs")
	if site.Status.InternalURL != "http://kup6s-pages-nginx.kup6s-pages.svc/default/docs/" {
		t.Errorf("InternalURL = %q", site.Status.InternalURL)
	}
	if site.Status.ContentPath != "/default/docs" {
		t.Errorf("ContentPath = %q, the controller dropped it", site.Status.ContentPath)
	}
}

func TestReconcile_PrivateSiteInternalURLFollowsContentPath(t *testing.T) {
	env := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", pagesv1.StaticSiteSpec{Visibility: pagesv1.VisibilityPrivate}))
	env.reconcile("docs")

	env.setContentPath("docs", "/default/docs")
	env.reconcile("docs")

	if got := env.site("docs").Status.InternalURL; got != "http://kup6s-pages-nginx.kup6s-pages.svc/default/docs/" {
		t.Errorf("InternalURL = %q", got)
	}
}

// A content path that does not belong to the site must never route to
// another site's directory.
func TestReconcile_ForeignContentPathKeepsLegacyPath(t *testing.T) {
	env := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", pagesv1.StaticSiteSpec{}))
	env.reconcile("docs")

	env.setContentPath("docs", "/other-team/secret")
	env.reconcile("docs")

	if got := env.addPrefix("docs"); got != "/docs" {
		t.Errorf("addPrefix = %q, want the legacy /docs", got)
	}
}
