package controller

import (
	"context"
	"sort"
	"strings"
	"testing"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// visibilityEnv runs the reconciler against stateful fakes, so tests can
// follow a site through public and private states and check what is left.
type visibilityEnv struct {
	t   *testing.T
	r   *StaticSiteReconciler
	c   client.Client
	dyn *dynamicfake.FakeDynamicClient
}

func newVisibilityEnv(t *testing.T, tlsMode string, sites ...*pagesv1.StaticSite) *visibilityEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&pagesv1.StaticSite{})
	for _, site := range sites {
		builder = builder.WithObjects(site)
	}
	c := builder.Build()

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		ingressRouteGVR: "IngressRouteList",
		middlewareGVR:   "MiddlewareList",
		certificateGVR:  "CertificateList",
	})

	return &visibilityEnv{
		t:   t,
		c:   c,
		dyn: dyn,
		r: &StaticSiteReconciler{
			Client:           c,
			DynamicClient:    dyn,
			Recorder:         events.NewFakeRecorder(100),
			PagesDomain:      "pages.kup6s.com",
			ClusterIssuer:    "letsencrypt-prod",
			NginxNamespace:   "kup6s-pages",
			NginxServiceName: "kup6s-pages-nginx",
			PagesTlsMode:     tlsMode,
		},
	}
}

func newVisibilitySite(name string, spec pagesv1.StaticSiteSpec) *pagesv1.StaticSite {
	if spec.Repo == "" {
		spec.Repo = "https://github.com/example/repo.git"
	}
	return &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
}

// reconcile runs the reconciler until the site is configured: the first two
// passes only add the finalizer and the sync token.
func (e *visibilityEnv) reconcile(name string) {
	e.t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}
	for i := 0; i < 3; i++ {
		if _, err := e.r.Reconcile(context.Background(), req); err != nil {
			e.t.Fatalf("Reconcile(%s) pass %d: %v", name, i+1, err)
		}
	}
}

func (e *visibilityEnv) site(name string) *pagesv1.StaticSite {
	e.t.Helper()
	site := &pagesv1.StaticSite{}
	if err := e.c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, site); err != nil {
		e.t.Fatalf("get site %s: %v", name, err)
	}
	return site
}

func (e *visibilityEnv) updateSpec(name string, mutate func(*pagesv1.StaticSiteSpec)) {
	e.t.Helper()
	site := e.site(name)
	mutate(&site.Spec)
	if err := e.c.Update(context.Background(), site); err != nil {
		e.t.Fatalf("update site %s: %v", name, err)
	}
}

func (e *visibilityEnv) names(gvr schema.GroupVersionResource) []string {
	e.t.Helper()
	list, err := e.dyn.Resource(gvr).Namespace("kup6s-pages").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		e.t.Fatalf("list %s: %v", gvr.Resource, err)
	}
	var names []string
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	sort.Strings(names)
	return names
}

func (e *visibilityEnv) expectNames(gvr schema.GroupVersionResource, want ...string) {
	e.t.Helper()
	got := e.names(gvr)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		e.t.Errorf("%s = %v, want %v", gvr.Resource, got, want)
	}
}

func (e *visibilityEnv) expectNoPublicResources() {
	e.t.Helper()
	e.expectNames(ingressRouteGVR)
	e.expectNames(middlewareGVR)
	e.expectNames(certificateGVR)
}

func expectPrivateStatus(t *testing.T, site *pagesv1.StaticSite) {
	t.Helper()
	if site.Status.URL != "" {
		t.Errorf("URL = %q, want empty", site.Status.URL)
	}
	wantInternal := "http://kup6s-pages-nginx.kup6s-pages.svc/" + site.Name + "/"
	if site.Status.InternalURL != wantInternal {
		t.Errorf("InternalURL = %q, want %q", site.Status.InternalURL, wantInternal)
	}
	if site.Status.Resources != nil {
		t.Errorf("Resources = %+v, want nil", site.Status.Resources)
	}
	if meta.FindStatusCondition(site.Status.Conditions, pagesv1.ConditionCertificateReady) != nil {
		t.Error("CertificateReady condition is set on a private site")
	}
	if site.Status.Phase != pagesv1.PhasePending {
		t.Errorf("Phase = %q, want %q", site.Status.Phase, pagesv1.PhasePending)
	}
}

func TestPrivateSite_CreatesNoPublicResources(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{Visibility: pagesv1.VisibilityPrivate}))

	e.reconcile("docs")

	e.expectNoPublicResources()
	expectPrivateStatus(t, e.site("docs"))
}

func TestPrivateSite_RejectsDomainAndPathPrefix(t *testing.T) {
	tests := []struct {
		name string
		spec pagesv1.StaticSiteSpec
	}{
		{"domain", pagesv1.StaticSiteSpec{Visibility: pagesv1.VisibilityPrivate, Domain: "docs.example.com"}},
		{"pathPrefix", pagesv1.StaticSiteSpec{Visibility: pagesv1.VisibilityPrivate, PathPrefix: "/docs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", tt.spec))

			e.reconcile("docs")

			e.expectNoPublicResources()
			site := e.site("docs")
			if site.Status.Phase != pagesv1.PhaseError {
				t.Errorf("Phase = %q, want %q", site.Status.Phase, pagesv1.PhaseError)
			}
			if !strings.Contains(site.Status.Message, "Private") {
				t.Errorf("Message = %q, want it to mention Private", site.Status.Message)
			}
		})
	}
}

func TestPublicToPrivate_RemovesPublicResources(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{Domain: "docs.example.com", PathPrefix: "/v1"}))
	e.reconcile("docs")
	e.expectNames(ingressRouteGVR, "default--docs")
	e.expectNames(middlewareGVR, "default--docs-prefix", "default--docs-strip")
	e.expectNames(certificateGVR, "docs-example-com-tls")

	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) {
		s.Visibility = pagesv1.VisibilityPrivate
		s.Domain = ""
		s.PathPrefix = ""
	})
	e.reconcile("docs")

	e.expectNoPublicResources()
	expectPrivateStatus(t, e.site("docs"))
}

func TestPublicToPrivate_RemovesAutoDomainCertificate(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", pagesv1.StaticSiteSpec{}))
	e.reconcile("docs")
	e.expectNames(certificateGVR, "docs-pages-kup6s-com-tls")

	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) { s.Visibility = pagesv1.VisibilityPrivate })
	e.reconcile("docs")

	e.expectNoPublicResources()
}

func TestPublicToPrivate_KeepsSharedCertificate(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("v1", pagesv1.StaticSiteSpec{Domain: "docs.example.com", PathPrefix: "/v1"}),
		newVisibilitySite("v2", pagesv1.StaticSiteSpec{Domain: "docs.example.com", PathPrefix: "/v2"}))
	e.reconcile("v1")
	e.reconcile("v2")

	e.updateSpec("v1", func(s *pagesv1.StaticSiteSpec) {
		s.Visibility = pagesv1.VisibilityPrivate
		s.Domain = ""
		s.PathPrefix = ""
	})
	e.reconcile("v1")

	e.expectNames(ingressRouteGVR, "default--v2")
	e.expectNames(middlewareGVR, "default--v2-prefix", "default--v2-strip")
	e.expectNames(certificateGVR, "docs-example-com-tls")
}

func TestPrivateToPublic_RecreatesResources(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{Visibility: pagesv1.VisibilityPrivate}))
	e.reconcile("docs")
	e.expectNoPublicResources()

	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) {
		s.Visibility = pagesv1.VisibilityPublic
		s.Domain = "docs.example.com"
	})
	e.reconcile("docs")

	e.expectNames(ingressRouteGVR, "default--docs")
	e.expectNames(middlewareGVR, "default--docs-prefix")
	e.expectNames(certificateGVR, "docs-example-com-tls")
	site := e.site("docs")
	if site.Status.URL != "https://docs.example.com" {
		t.Errorf("URL = %q, want %q", site.Status.URL, "https://docs.example.com")
	}
	if site.Status.InternalURL != "http://kup6s-pages-nginx.kup6s-pages.svc/docs/" {
		t.Errorf("InternalURL = %q", site.Status.InternalURL)
	}
	if site.Status.Resources == nil || site.Status.Resources.IngressRoute == "" {
		t.Errorf("Resources = %+v, want the IngressRoute recorded", site.Status.Resources)
	}
}

func TestPathPrefixRemoved_DeletesStripMiddleware(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{Domain: "docs.example.com", PathPrefix: "/v1"}))
	e.reconcile("docs")

	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) { s.PathPrefix = "" })
	e.reconcile("docs")

	e.expectNames(middlewareGVR, "default--docs-prefix")
	if got := e.site("docs").Status.Resources.StripMiddleware; got != "" {
		t.Errorf("StripMiddleware = %q, want empty", got)
	}
}

func TestDomainChange_ReleasesOldCertificate(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{Domain: "old.example.com"}))
	e.reconcile("docs")

	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) { s.Domain = "new.example.com" })
	e.reconcile("docs")

	e.expectNames(certificateGVR, "new-example-com-tls")
}

func TestDomainChange_KeepsCertificateStillInUse(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("v1", pagesv1.StaticSiteSpec{Domain: "old.example.com", PathPrefix: "/v1"}),
		newVisibilitySite("v2", pagesv1.StaticSiteSpec{Domain: "old.example.com", PathPrefix: "/v2"}))
	e.reconcile("v1")
	e.reconcile("v2")

	e.updateSpec("v1", func(s *pagesv1.StaticSiteSpec) { s.Domain = "new.example.com" })
	e.reconcile("v1")

	e.expectNames(certificateGVR, "new-example-com-tls", "old-example-com-tls")
}

func TestDeletion_RemovesAutoDomainCertificate(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual, newVisibilitySite("docs", pagesv1.StaticSiteSpec{}))
	e.reconcile("docs")
	e.expectNames(certificateGVR, "docs-pages-kup6s-com-tls")

	if err := e.c.Delete(context.Background(), e.site("docs")); err != nil {
		t.Fatalf("delete site: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "docs", Namespace: "default"}}
	if _, err := e.r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() after delete: %v", err)
	}

	e.expectNoPublicResources()
}

func TestDeletion_RemovesStripMiddlewareOfRemovedPathPrefix(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{Domain: "docs.example.com", PathPrefix: "/v1"}))
	e.reconcile("docs")
	// The spec loses its prefix, and the site is deleted before the
	// operator reconciles that change.
	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) { s.PathPrefix = "" })

	if err := e.c.Delete(context.Background(), e.site("docs")); err != nil {
		t.Fatalf("delete site: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "docs", Namespace: "default"}}
	if _, err := e.r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() after delete: %v", err)
	}

	e.expectNoPublicResources()
}

func TestWildcardMode_PublicToPrivate(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeWildcard, newVisibilitySite("docs", pagesv1.StaticSiteSpec{}))
	e.reconcile("docs")
	e.expectNames(certificateGVR)

	e.updateSpec("docs", func(s *pagesv1.StaticSiteSpec) { s.Visibility = pagesv1.VisibilityPrivate })
	e.reconcile("docs")

	e.expectNoPublicResources()
	expectPrivateStatus(t, e.site("docs"))
}

func TestMapCertificateToStaticSites_AutoDomain(t *testing.T) {
	e := newVisibilityEnv(t, TlsModeIndividual,
		newVisibilitySite("docs", pagesv1.StaticSiteSpec{}),
		newVisibilitySite("hidden", pagesv1.StaticSiteSpec{Visibility: pagesv1.VisibilityPrivate}))
	e.reconcile("docs")

	cert, err := e.dyn.Resource(certificateGVR).Namespace("kup6s-pages").Get(context.Background(), "docs-pages-kup6s-com-tls", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get certificate: %v", err)
	}

	requests := e.r.mapCertificateToStaticSites(context.Background(), cert)
	if len(requests) != 1 || requests[0].Name != "docs" {
		t.Errorf("requests = %v, want only docs", requests)
	}
}
