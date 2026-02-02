package controller

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcile_NewSite(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-site",
			Namespace: "default",
			UID:       "test-uid-123",
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Branch: "main",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-site",
			Namespace: "default",
		},
	}

	// First Reconcile: add finalizer
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 after adding finalizer")
	}

	// Second Reconcile: generate sync token
	result, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 after generating sync token")
	}

	// Third Reconcile: create resources
	result, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Check status
	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	if updatedSite.Status.Phase != pagesv1.PhaseReady {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseReady)
	}
	if updatedSite.Status.URL != "https://test-site.pages.kup6s.com" {
		t.Errorf("URL = %q, want %q", updatedSite.Status.URL, "https://test-site.pages.kup6s.com")
	}
}

func TestReconcile_CustomDomain(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "custom-site",
			Namespace:  "default",
			UID:        "test-uid-456",
			Finalizers: []string{finalizerName}, // Already has finalizer
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Domain: "custom.example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "custom-site",
			Namespace: "default",
		},
	}

	// First Reconcile: generate sync token
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Second Reconcile: create resources
	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	// Custom domain should be used in URL
	if updatedSite.Status.URL != "https://custom.example.com" {
		t.Errorf("URL = %q, want %q", updatedSite.Status.URL, "https://custom.example.com")
	}
}

func TestReconcile_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "default",
		},
	}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 for not found")
	}
}

func TestReconcile_Deletion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	now := metav1.Now()
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-site",
			Namespace:         "default",
			UID:               "test-uid-789",
			Finalizers:        []string{finalizerName},
			DeletionTimestamp: &now,
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "deleting-site",
			Namespace: "default",
		},
	}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Nach Finalizer-Entfernung: keine Requeue nötig
	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 after deletion handling")
	}

	// Das Objekt wird nach Finalizer-Entfernung vom API-Server gelöscht
	// Der Fake-Client simuliert das - daher ist NotFound erwartet
	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err == nil {
		// Wenn es noch existiert, sollte der Finalizer entfernt sein
		if len(updatedSite.Finalizers) > 0 {
			t.Errorf("Finalizers = %v, want empty", updatedSite.Finalizers)
		}
	}
	// NotFound ist auch OK - bedeutet Objekt wurde gelöscht
}

func TestDomainGeneration(t *testing.T) {
	r := &StaticSiteReconciler{
		PagesDomain: "pages.kup6s.com",
	}

	tests := []struct {
		name       string
		siteName   string
		specDomain string
		wantDomain string
	}{
		{
			name:       "custom domain",
			siteName:   "mysite",
			specDomain: "example.com",
			wantDomain: "example.com",
		},
		{
			name:       "auto-generated domain",
			siteName:   "mysite",
			specDomain: "",
			wantDomain: "mysite.pages.kup6s.com",
		},
		{
			name:       "site with dashes",
			siteName:   "my-cool-site",
			specDomain: "",
			wantDomain: "my-cool-site.pages.kup6s.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.siteName,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: tt.specDomain,
				},
			}

			domain := site.Spec.Domain
			if domain == "" {
				domain = site.Name + "." + r.PagesDomain
			}

			if domain != tt.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tt.wantDomain)
			}
		})
	}
}

func TestFinalizerName(t *testing.T) {
	if finalizerName != "pages.kup6s.com/finalizer" {
		t.Errorf("finalizerName = %q, want %q", finalizerName, "pages.kup6s.com/finalizer")
	}
}

func TestResourceName(t *testing.T) {
	tests := []struct {
		namespace string
		name      string
		want      string
	}{
		{"default", "my-site", "default--my-site"},
		{"pages", "customer-website", "pages--customer-website"},
		{"kup6s-pages", "test", "kup6s-pages--test"},
	}

	for _, tt := range tests {
		t.Run(tt.namespace+"/"+tt.name, func(t *testing.T) {
			site := &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.name,
					Namespace: tt.namespace,
				},
			}
			got := resourceName(site)
			if got != tt.want {
				t.Errorf("resourceName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResourceNameWithSuffix(t *testing.T) {
	tests := []struct {
		namespace string
		name      string
		suffix    string
		want      string
	}{
		{"default", "my-site", "prefix", "default--my-site-prefix"},
		{"pages", "customer", "strip", "pages--customer-strip"},
	}

	for _, tt := range tests {
		t.Run(tt.namespace+"/"+tt.name+"/"+tt.suffix, func(t *testing.T) {
			site := &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.name,
					Namespace: tt.namespace,
				},
			}
			got := resourceNameWithSuffix(site, tt.suffix)
			if got != tt.want {
				t.Errorf("resourceNameWithSuffix() = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeDynamicClient für Controller-Tests
type fakeDynamicClient struct{}

func (f *fakeDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeNamespaceableResource{}
}

type fakeNamespaceableResource struct {
	namespace string
}

func (f *fakeNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeNamespaceableResource{namespace: ns}
}

func (f *fakeNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (f *fakeNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (f *fakeNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (f *fakeNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	// Simuliere "nicht gefunden" um Create zu triggern
	return nil, &notFoundError{}
}

func (f *fakeNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (f *fakeNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

type notFoundError struct{}

func (e *notFoundError) Error() string   { return "not found" }
func (e *notFoundError) Status() metav1.Status { return metav1.Status{Reason: metav1.StatusReasonNotFound} }

func TestValidatePathPrefix(t *testing.T) {
	tests := []struct {
		name       string
		domain     string
		pathPrefix string
		wantErr    bool
	}{
		{
			name:       "empty prefix is valid",
			domain:     "example.com",
			pathPrefix: "",
			wantErr:    false,
		},
		{
			name:       "valid prefix",
			domain:     "example.com",
			pathPrefix: "/2019",
			wantErr:    false,
		},
		{
			name:       "nested prefix",
			domain:     "example.com",
			pathPrefix: "/archive/2019",
			wantErr:    false,
		},
		{
			name:       "prefix requires domain",
			domain:     "",
			pathPrefix: "/2019",
			wantErr:    true,
		},
		{
			name:       "just slash is invalid",
			domain:     "example.com",
			pathPrefix: "/",
			wantErr:    true,
		},
		{
			name:       "missing leading slash",
			domain:     "example.com",
			pathPrefix: "2019",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &pagesv1.StaticSite{
				Spec: pagesv1.StaticSiteSpec{
					Domain:     tt.domain,
					PathPrefix: tt.pathPrefix,
				},
			}
			err := validatePathPrefix(site)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePathPrefix() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTruncateK8sName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "short name unchanged",
			in:   "my-resource",
			want: "my-resource",
		},
		{
			name: "exactly 63 characters",
			in:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name: "longer than 63 characters truncated",
			in:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 64 chars
			want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",  // 63 chars
		},
		{
			name: "truncation removes trailing hyphen",
			in:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa---x", // 64 chars with hyphens before 63rd
			want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name: "multiple trailing hyphens removed",
			in:   "short-name---",
			want: "short-name",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateK8sName(tt.in)
			if got != tt.want {
				t.Errorf("truncateK8sName(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if len(got) > maxK8sResourceNameLen {
				t.Errorf("truncateK8sName(%q) length = %d, want <= %d", tt.in, len(got), maxK8sResourceNameLen)
			}
		})
	}
}

func TestSanitizeDomainForResourceName(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "example-com"},
		{"www.example.com", "www-example-com"},
		{"sub.domain.example.com", "sub-domain-example-com"},
		{"EXAMPLE.COM", "example-com"},
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			got := sanitizeDomainForResourceName(tt.domain)
			if got != tt.want {
				t.Errorf("sanitizeDomainForResourceName(%q) = %q, want %q", tt.domain, got, tt.want)
			}
		})
	}
}

func TestReconcile_PathPrefix(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "archive-2019",
			Namespace:  "default",
			UID:        "test-uid-path",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:       "https://github.com/example/archive.git",
			Domain:     "www.example.com",
			PathPrefix: "/2019",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "archive-2019",
			Namespace: "default",
		},
	}

	// First Reconcile: generate sync token
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Second Reconcile: create resources
	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	// URL should include pathPrefix
	wantURL := "https://www.example.com/2019"
	if updatedSite.Status.URL != wantURL {
		t.Errorf("URL = %q, want %q", updatedSite.Status.URL, wantURL)
	}

	if updatedSite.Status.Phase != pagesv1.PhaseReady {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseReady)
	}
}

func TestReconcile_PathPrefixValidationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	// PathPrefix without domain should fail validation
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "invalid-site",
			Namespace:  "default",
			UID:        "test-uid-invalid",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:       "https://github.com/example/repo.git",
			PathPrefix: "/2019", // PathPrefix without Domain
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "invalid-site",
			Namespace: "default",
		},
	}

	// First Reconcile: generate sync token
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}

	// Second Reconcile: validation fails, sets Error status
	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error in status)", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	// Should be in Error phase
	if updatedSite.Status.Phase != pagesv1.PhaseError {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseError)
	}
}

// failingReader always returns an error when Read is called
type failingReader struct{}

func (f failingReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("simulated random source failure")
}

func TestGenerateSecureToken(t *testing.T) {
	t.Run("success with default rand.Reader", func(t *testing.T) {
		// Use the default crypto/rand reader
		randReader = rand.Reader
		defer func() { randReader = rand.Reader }()

		token, err := generateSecureToken(32)
		if err != nil {
			t.Fatalf("generateSecureToken() error = %v, want nil", err)
		}
		if len(token) != 32 {
			t.Errorf("token length = %d, want 32", len(token))
		}
	})

	t.Run("error when rand.Reader fails", func(t *testing.T) {
		// Replace with failing reader
		randReader = failingReader{}
		defer func() { randReader = rand.Reader }()

		token, err := generateSecureToken(32)
		if err == nil {
			t.Fatal("generateSecureToken() error = nil, want error")
		}
		if token != "" {
			t.Errorf("token = %q, want empty string on error", token)
		}
	})
}

func TestReconcile_TokenGenerationFails(t *testing.T) {
	// Replace with failing reader for this test
	randReader = failingReader{}
	defer func() { randReader = rand.Reader }()

	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "token-fail-site",
			Namespace:  "default",
			UID:        "test-uid-token-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "token-fail-site",
			Namespace: "default",
		},
	}

	// Reconcile should set error status when token generation fails
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error in status)", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	// Should be in Error phase
	if updatedSite.Status.Phase != pagesv1.PhaseError {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseError)
	}
}

func TestMapCertificateToStaticSites(t *testing.T) {
	tests := []struct {
		name          string
		cert          *unstructured.Unstructured
		existingSites []pagesv1.StaticSite
		nginxNS       string
		wantRequests  int
	}{
		{
			name: "cert in wrong namespace",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "test-cert",
						"namespace": "other-namespace",
						"labels": map[string]interface{}{
							"pages.kup6s.com/managed": "true",
							"pages.kup6s.com/domain":  "example.com",
						},
					},
				},
			},
			existingSites: nil,
			nginxNS:       "kup6s-pages",
			wantRequests:  0,
		},
		{
			name: "cert without managed label",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "test-cert",
						"namespace": "kup6s-pages",
						"labels": map[string]interface{}{
							"pages.kup6s.com/domain": "example.com",
						},
					},
				},
			},
			existingSites: nil,
			nginxNS:       "kup6s-pages",
			wantRequests:  0,
		},
		{
			name: "cert with managed=false",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "test-cert",
						"namespace": "kup6s-pages",
						"labels": map[string]interface{}{
							"pages.kup6s.com/managed": "false",
							"pages.kup6s.com/domain":  "example.com",
						},
					},
				},
			},
			existingSites: nil,
			nginxNS:       "kup6s-pages",
			wantRequests:  0,
		},
		{
			name: "cert without domain label",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "test-cert",
						"namespace": "kup6s-pages",
						"labels": map[string]interface{}{
							"pages.kup6s.com/managed": "true",
						},
					},
				},
			},
			existingSites: nil,
			nginxNS:       "kup6s-pages",
			wantRequests:  0,
		},
		{
			name: "cert with one matching site",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
						"labels": map[string]interface{}{
							"pages.kup6s.com/managed": "true",
							"pages.kup6s.com/domain":  "example.com",
						},
					},
				},
			},
			existingSites: []pagesv1.StaticSite{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "site1",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain: "example.com",
					},
				},
			},
			nginxNS:      "kup6s-pages",
			wantRequests: 1,
		},
		{
			name: "cert with multiple matching sites",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "shared-domain-tls",
						"namespace": "kup6s-pages",
						"labels": map[string]interface{}{
							"pages.kup6s.com/managed": "true",
							"pages.kup6s.com/domain":  "shared.example.com",
						},
					},
				},
			},
			existingSites: []pagesv1.StaticSite{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "site-2019",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain:     "shared.example.com",
						PathPrefix: "/2019",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "site-2020",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain:     "shared.example.com",
						PathPrefix: "/2020",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-site",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain: "other.example.com",
					},
				},
			},
			nginxNS:      "kup6s-pages",
			wantRequests: 2,
		},
		{
			name: "cert with no matching sites",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "orphan-tls",
						"namespace": "kup6s-pages",
						"labels": map[string]interface{}{
							"pages.kup6s.com/managed": "true",
							"pages.kup6s.com/domain":  "orphan.example.com",
						},
					},
				},
			},
			existingSites: []pagesv1.StaticSite{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "site1",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain: "different.example.com",
					},
				},
			},
			nginxNS:      "kup6s-pages",
			wantRequests: 0,
		},
		{
			name: "cert with nil labels",
			cert: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "no-labels",
						"namespace": "kup6s-pages",
					},
				},
			},
			existingSites: nil,
			nginxNS:       "kup6s-pages",
			wantRequests:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = pagesv1.AddToScheme(scheme)

			builder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range tt.existingSites {
				builder = builder.WithObjects(&tt.existingSites[i])
			}
			fakeClient := builder.Build()

			r := &StaticSiteReconciler{
				Client:         fakeClient,
				NginxNamespace: tt.nginxNS,
			}

			requests := r.mapCertificateToStaticSites(context.Background(), tt.cert)
			if len(requests) != tt.wantRequests {
				t.Errorf("mapCertificateToStaticSites() returned %d requests, want %d", len(requests), tt.wantRequests)
			}
		})
	}
}

func TestUpdateCertificateCondition(t *testing.T) {
	tests := []struct {
		name            string
		site            *pagesv1.StaticSite
		domain          string
		certData        *unstructured.Unstructured
		certGetErr      error
		wantStatus      metav1.ConditionStatus
		wantReason      string
		wantNoCondition bool
	}{
		{
			name: "no custom domain removes condition",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "", // No custom domain
				},
				Status: pagesv1.StaticSiteStatus{
					Conditions: []metav1.Condition{
						{
							Type:   pagesv1.ConditionCertificateReady,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
					},
				},
			},
			domain:          "test-site.pages.kup6s.com",
			wantNoCondition: true,
		},
		{
			name: "cert not found",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain:     "example.com",
			certGetErr: &notFoundError{},
			wantStatus: metav1.ConditionFalse,
			wantReason: "CertificateNotFound",
		},
		{
			name: "cert fetch error",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain:     "example.com",
			certGetErr: errors.New("connection refused"),
			wantStatus: metav1.ConditionUnknown,
			wantReason: "CertificateFetchError",
		},
		{
			name: "cert ready",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":    "Ready",
								"status":  "True",
								"reason":  "Ready",
								"message": "Certificate is ready",
							},
						},
					},
				},
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: "Ready",
		},
		{
			name: "cert not ready (pending)",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":    "Ready",
								"status":  "False",
								"reason":  "Pending",
								"message": "Waiting for certificate issuance",
							},
						},
					},
				},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: "Pending",
		},
		{
			name: "cert status unknown",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":    "Ready",
								"status":  "Unknown",
								"reason":  "InProgress",
								"message": "Certificate issuance in progress",
							},
						},
					},
				},
			},
			wantStatus: metav1.ConditionUnknown,
			wantReason: "InProgress",
		},
		{
			name: "cert status not available yet",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					// No status field
				},
			},
			wantStatus: metav1.ConditionUnknown,
			wantReason: "StatusNotAvailable",
		},
		{
			name: "cert conditions not available yet",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					"status": map[string]interface{}{
						// No conditions
					},
				},
			},
			wantStatus: metav1.ConditionUnknown,
			wantReason: "ConditionsNotAvailable",
		},
		{
			name: "ready condition not found in conditions",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Issuing",
								"status": "True",
							},
						},
					},
				},
			},
			wantStatus: metav1.ConditionUnknown,
			wantReason: "ReadyConditionNotFound",
		},
		{
			name: "condition with invalid type is skipped",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "example.com",
				},
			},
			domain: "example.com",
			certData: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name":      "example-com-tls",
						"namespace": "kup6s-pages",
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							"invalid-not-a-map", // Invalid condition entry
							map[string]interface{}{
								"type":    "Ready",
								"status":  "True",
								"reason":  "Ready",
								"message": "Certificate is ready",
							},
						},
					},
				},
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: "Ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dynClient := &configurableDynamicClient{
				getCertificate: tt.certData,
				getCertErr:     tt.certGetErr,
			}

			r := &StaticSiteReconciler{
				DynamicClient:  dynClient,
				NginxNamespace: "kup6s-pages",
			}

			r.updateCertificateCondition(context.Background(), tt.site, tt.domain)

			// Find the CertificateReady condition
			var foundCondition *metav1.Condition
			for i := range tt.site.Status.Conditions {
				if tt.site.Status.Conditions[i].Type == pagesv1.ConditionCertificateReady {
					foundCondition = &tt.site.Status.Conditions[i]
					break
				}
			}

			if tt.wantNoCondition {
				if foundCondition != nil {
					t.Errorf("expected no CertificateReady condition, but found one with status=%s, reason=%s",
						foundCondition.Status, foundCondition.Reason)
				}
				return
			}

			if foundCondition == nil {
				t.Fatal("expected CertificateReady condition, but none found")
			}

			if foundCondition.Status != tt.wantStatus {
				t.Errorf("condition.Status = %v, want %v", foundCondition.Status, tt.wantStatus)
			}
			if foundCondition.Reason != tt.wantReason {
				t.Errorf("condition.Reason = %v, want %v", foundCondition.Reason, tt.wantReason)
			}
		})
	}
}

// configurableDynamicClient is a more flexible fake dynamic client for testing
type configurableDynamicClient struct {
	getCertificate *unstructured.Unstructured
	getCertErr     error
	createErr      error
}

func (c *configurableDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &configurableNamespaceableResource{
		gvr:    resource,
		client: c,
	}
}

type configurableNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *configurableDynamicClient
}

func (c *configurableNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &configurableNamespaceableResource{
		gvr:       c.gvr,
		namespace: ns,
		client:    c.client,
	}
}

func (c *configurableNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if c.gvr == certificateGVR {
		if c.client.getCertErr != nil {
			return nil, c.client.getCertErr
		}
		if c.client.getCertificate != nil {
			return c.client.getCertificate, nil
		}
	}
	return nil, &notFoundError{}
}

func (c *configurableNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if c.client.createErr != nil {
		return nil, c.client.createErr
	}
	return obj, nil
}

func (c *configurableNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (c *configurableNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (c *configurableNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (c *configurableNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (c *configurableNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (c *configurableNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (c *configurableNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (c *configurableNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (c *configurableNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestReconcile_MiddlewareCreationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "middleware-fail",
			Namespace:  "default",
			UID:        "test-uid-mw-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	r := &StaticSiteReconciler{
		Client: fakeClient,
		DynamicClient: &configurableDynamicClient{
			createErr: errors.New("middleware creation failed"),
		},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "middleware-fail",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error in status)", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	if updatedSite.Status.Phase != pagesv1.PhaseError {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseError)
	}
	if updatedSite.Status.Message != "middleware creation failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "middleware creation failed")
	}
}

func TestReconcile_IngressRouteCreationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ingress-fail",
			Namespace:  "default",
			UID:        "test-uid-ir-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	// Use a dynamic client that fails on IngressRoute create but not middleware
	dynClient := &selectiveErrorDynamicClient{
		ingressRouteCreateErr: errors.New("ingress route creation failed"),
	}

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    dynClient,
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "ingress-fail",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error in status)", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	if updatedSite.Status.Phase != pagesv1.PhaseError {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseError)
	}
	if updatedSite.Status.Message != "ingress route creation failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "ingress route creation failed")
	}
}

func TestReconcile_CertificateCreationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cert-fail",
			Namespace:  "default",
			UID:        "test-uid-cert-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Domain: "custom.example.com",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	// Use a dynamic client that fails on Certificate create
	dynClient := &selectiveErrorDynamicClient{
		certificateCreateErr: errors.New("certificate creation failed"),
	}

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    dynClient,
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "cert-fail",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error in status)", err)
	}

	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	if updatedSite.Status.Phase != pagesv1.PhaseError {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseError)
	}
	if updatedSite.Status.Message != "certificate creation failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "certificate creation failed")
	}
}

// selectiveErrorDynamicClient allows different errors for different resource types
type selectiveErrorDynamicClient struct {
	ingressRouteCreateErr error
	certificateCreateErr  error
}

func (s *selectiveErrorDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &selectiveErrorNamespaceableResource{
		gvr:    resource,
		client: s,
	}
}

type selectiveErrorNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *selectiveErrorDynamicClient
}

func (s *selectiveErrorNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &selectiveErrorNamespaceableResource{
		gvr:       s.gvr,
		namespace: ns,
		client:    s.client,
	}
}

func (s *selectiveErrorNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, &notFoundError{}
}

func (s *selectiveErrorNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if s.gvr == ingressRouteGVR && s.client.ingressRouteCreateErr != nil {
		return nil, s.client.ingressRouteCreateErr
	}
	if s.gvr == certificateGVR && s.client.certificateCreateErr != nil {
		return nil, s.client.certificateCreateErr
	}
	return obj, nil
}

func (s *selectiveErrorNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (s *selectiveErrorNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (s *selectiveErrorNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (s *selectiveErrorNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (s *selectiveErrorNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (s *selectiveErrorNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (s *selectiveErrorNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (s *selectiveErrorNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (s *selectiveErrorNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestCleanupOrphanedCertificate(t *testing.T) {
	tests := []struct {
		name            string
		deletingSite    *pagesv1.StaticSite
		otherSites      []pagesv1.StaticSite
		wantDeleteCert  bool
		certDeleteErr   error
		wantErr         bool
	}{
		{
			name: "certificate deleted when no other sites use domain",
			deletingSite: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "site-to-delete",
					Namespace: "default",
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "orphan.example.com",
				},
			},
			otherSites:     nil,
			wantDeleteCert: true,
		},
		{
			name: "certificate kept when another site uses same domain",
			deletingSite: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "site-to-delete",
					Namespace: "default",
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain:     "shared.example.com",
					PathPrefix: "/2019",
				},
			},
			otherSites: []pagesv1.StaticSite{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "site-2020",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain:     "shared.example.com",
						PathPrefix: "/2020",
					},
				},
			},
			wantDeleteCert: false,
		},
		{
			name: "certificate deleted when other sites have different domains",
			deletingSite: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "site-to-delete",
					Namespace: "default",
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "delete-me.example.com",
				},
			},
			otherSites: []pagesv1.StaticSite{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-site",
						Namespace: "default",
					},
					Spec: pagesv1.StaticSiteSpec{
						Domain: "different.example.com",
					},
				},
			},
			wantDeleteCert: true,
		},
		{
			name: "handles delete error gracefully",
			deletingSite: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "site-to-delete",
					Namespace: "default",
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "error.example.com",
				},
			},
			otherSites:     nil,
			certDeleteErr:  errors.New("delete failed"),
			wantDeleteCert: true,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = pagesv1.AddToScheme(scheme)

			builder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range tt.otherSites {
				builder = builder.WithObjects(&tt.otherSites[i])
			}
			fakeClient := builder.Build()

			dynClient := &trackingDynamicClient{
				deleteErr: tt.certDeleteErr,
			}

			r := &StaticSiteReconciler{
				Client:         fakeClient,
				DynamicClient:  dynClient,
				NginxNamespace: "kup6s-pages",
			}

			err := r.cleanupOrphanedCertificate(context.Background(), tt.deletingSite)

			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if tt.wantDeleteCert && !dynClient.deleteCalled {
				t.Error("expected certificate delete to be called, but it wasn't")
			}
			if !tt.wantDeleteCert && dynClient.deleteCalled {
				t.Error("expected certificate delete NOT to be called, but it was")
			}
		})
	}
}

// trackingDynamicClient tracks which operations were called
type trackingDynamicClient struct {
	deleteCalled bool
	deleteErr    error
}

func (t *trackingDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &trackingNamespaceableResource{
		gvr:    resource,
		client: t,
	}
}

type trackingNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *trackingDynamicClient
}

func (t *trackingNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &trackingNamespaceableResource{
		gvr:       t.gvr,
		namespace: ns,
		client:    t.client,
	}
}

func (t *trackingNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, &notFoundError{}
}

func (t *trackingNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (t *trackingNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (t *trackingNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	t.client.deleteCalled = true
	return t.client.deleteErr
}

func (t *trackingNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (t *trackingNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (t *trackingNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (t *trackingNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (t *trackingNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (t *trackingNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (t *trackingNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestReconcile_DeletionWithCustomDomain(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	now := metav1.Now()
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-custom-site",
			Namespace:         "default",
			UID:               "test-uid-delete-custom",
			Finalizers:        []string{finalizerName},
			DeletionTimestamp: &now,
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:       "https://github.com/example/repo.git",
			Domain:     "custom.example.com",
			PathPrefix: "/2019",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		Build()

	dynClient := &trackingDynamicClient{}

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    dynClient,
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "deleting-custom-site",
			Namespace: "default",
		},
	}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 after deletion handling")
	}

	// Certificate should have been deleted (or at least delete was called)
	if !dynClient.deleteCalled {
		t.Error("expected delete to be called for certificate cleanup")
	}
}

func TestReconcile_MiddlewareUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "update-site",
			Namespace:  "default",
			UID:        "test-uid-update",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	// Use a client that returns existing resources (triggers update path)
	dynClient := &existingResourceDynamicClient{
		existingMiddleware: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion":      "traefik.io/v1alpha1",
				"kind":            "Middleware",
				"metadata":        map[string]interface{}{"name": "test", "namespace": "kup6s-pages", "resourceVersion": "123"},
				"spec":            map[string]interface{}{},
			},
		},
		existingIngressRoute: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion":      "traefik.io/v1alpha1",
				"kind":            "IngressRoute",
				"metadata":        map[string]interface{}{"name": "test", "namespace": "kup6s-pages", "resourceVersion": "456"},
				"spec":            map[string]interface{}{},
			},
		},
	}

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    dynClient,
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "update-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify updates were called
	if !dynClient.middlewareUpdateCalled {
		t.Error("expected middleware update to be called")
	}
	if !dynClient.ingressRouteUpdateCalled {
		t.Error("expected IngressRoute update to be called")
	}
}

// existingResourceDynamicClient returns existing resources to trigger update paths
type existingResourceDynamicClient struct {
	existingMiddleware       *unstructured.Unstructured
	existingIngressRoute     *unstructured.Unstructured
	existingCertificate      *unstructured.Unstructured
	middlewareUpdateCalled   bool
	ingressRouteUpdateCalled bool
}

func (e *existingResourceDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &existingResourceNamespaceableResource{
		gvr:    resource,
		client: e,
	}
}

type existingResourceNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *existingResourceDynamicClient
}

func (e *existingResourceNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &existingResourceNamespaceableResource{
		gvr:       e.gvr,
		namespace: ns,
		client:    e.client,
	}
}

func (e *existingResourceNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if e.gvr == middlewareGVR && e.client.existingMiddleware != nil {
		return e.client.existingMiddleware.DeepCopy(), nil
	}
	if e.gvr == ingressRouteGVR && e.client.existingIngressRoute != nil {
		return e.client.existingIngressRoute.DeepCopy(), nil
	}
	if e.gvr == certificateGVR && e.client.existingCertificate != nil {
		return e.client.existingCertificate.DeepCopy(), nil
	}
	return nil, &notFoundError{}
}

func (e *existingResourceNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (e *existingResourceNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if e.gvr == middlewareGVR {
		e.client.middlewareUpdateCalled = true
	}
	if e.gvr == ingressRouteGVR {
		e.client.ingressRouteUpdateCalled = true
	}
	return obj, nil
}

func (e *existingResourceNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (e *existingResourceNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (e *existingResourceNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (e *existingResourceNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (e *existingResourceNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (e *existingResourceNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (e *existingResourceNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (e *existingResourceNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestReconcile_ExistingCertificateSkipsCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "existing-cert-site",
			Namespace:  "default",
			UID:        "test-uid-existing-cert",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Domain: "existing.example.com",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	// Use a client that returns existing certificate
	dynClient := &existingResourceDynamicClient{
		existingCertificate: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cert-manager.io/v1",
				"kind":       "Certificate",
				"metadata":   map[string]interface{}{"name": "existing-example-com-tls", "namespace": "kup6s-pages"},
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":    "Ready",
							"status":  "True",
							"reason":  "Ready",
							"message": "Certificate is ready",
						},
					},
				},
			},
		},
	}

	r := &StaticSiteReconciler{
		Client:           fakeClient,
		DynamicClient:    dynClient,
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "existing-cert-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify site is ready
	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err != nil {
		t.Fatalf("failed to get site: %v", err)
	}

	if updatedSite.Status.Phase != pagesv1.PhaseReady {
		t.Errorf("Phase = %q, want %q", updatedSite.Status.Phase, pagesv1.PhaseReady)
	}
}
