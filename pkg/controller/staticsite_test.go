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
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	// After finalizer removal: no requeue needed
	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 after deletion handling")
	}

	// The object is deleted by the API server after finalizer removal
	// The fake client simulates this - so NotFound is expected
	updatedSite := &pagesv1.StaticSite{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedSite)
	if err == nil {
		// If it still exists, the finalizer should be removed
		if len(updatedSite.Finalizers) > 0 {
			t.Errorf("Finalizers = %v, want empty", updatedSite.Finalizers)
		}
	}
	// NotFound is also OK - means object was deleted
}

func TestDomainGeneration(t *testing.T) {
	tests := []struct {
		name        string
		pagesDomain string
		siteName    string
		specDomain  string
		wantDomain  string
		wantErr     bool
	}{
		{
			name:        "custom domain",
			pagesDomain: "pages.kup6s.com",
			siteName:    "mysite",
			specDomain:  "example.com",
			wantDomain:  "example.com",
		},
		{
			name:        "auto-generated domain",
			pagesDomain: "pages.kup6s.com",
			siteName:    "mysite",
			specDomain:  "",
			wantDomain:  "mysite.pages.kup6s.com",
		},
		{
			name:        "site with dashes",
			pagesDomain: "pages.kup6s.com",
			siteName:    "my-cool-site",
			specDomain:  "",
			wantDomain:  "my-cool-site.pages.kup6s.com",
		},
		{
			name:        "custom domain with empty PagesDomain",
			pagesDomain: "",
			siteName:    "mysite",
			specDomain:  "example.com",
			wantDomain:  "example.com",
		},
		{
			name:        "no custom domain and no PagesDomain",
			pagesDomain: "",
			siteName:    "mysite",
			specDomain:  "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &StaticSiteReconciler{
				PagesDomain: tt.pagesDomain,
			}
			site := &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.siteName,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: tt.specDomain,
				},
			}

			domain, err := r.determineDomain(site)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
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
		pagesTlsMode    string // TLS mode for auto-generated domains
		wantStatus      metav1.ConditionStatus
		wantReason      string
		wantNoCondition bool
	}{
		{
			name: "wildcard mode auto-domain removes condition",
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
			pagesTlsMode:    TlsModeWildcard, // Wildcard mode - no managed cert
			wantNoCondition: true,
		},
		{
			name: "individual mode auto-domain checks certificate",
			site: &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-site",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: "", // No custom domain
				},
			},
			domain:       "test-site.pages.kup6s.com",
			pagesTlsMode: TlsModeIndividual, // Individual mode - has managed cert
			certGetErr:   &notFoundError{},
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "CertificateNotFound",
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
				PagesTlsMode:   tt.pagesTlsMode,
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

// deletionErrorDynamicClient returns errors on deletion for testing handleDeletion error paths
type deletionErrorDynamicClient struct {
	ingressRouteDeleteErr error
	middlewareDeleteErr   error
	deletedResources      []string
}

func (d *deletionErrorDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &deletionErrorNamespaceableResource{
		gvr:    resource,
		client: d,
	}
}

type deletionErrorNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *deletionErrorDynamicClient
}

func (d *deletionErrorNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &deletionErrorNamespaceableResource{
		gvr:       d.gvr,
		namespace: ns,
		client:    d.client,
	}
}

func (d *deletionErrorNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, &notFoundError{}
}

func (d *deletionErrorNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (d *deletionErrorNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (d *deletionErrorNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	d.client.deletedResources = append(d.client.deletedResources, d.gvr.Resource+"/"+name)
	if d.gvr == ingressRouteGVR && d.client.ingressRouteDeleteErr != nil {
		return d.client.ingressRouteDeleteErr
	}
	if d.gvr == middlewareGVR && d.client.middlewareDeleteErr != nil {
		return d.client.middlewareDeleteErr
	}
	return nil
}

func (d *deletionErrorNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (d *deletionErrorNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (d *deletionErrorNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (d *deletionErrorNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (d *deletionErrorNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (d *deletionErrorNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (d *deletionErrorNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestHandleDeletion_IngressRouteDeleteError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	now := metav1.Now()
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ir-delete-error",
			Namespace:         "default",
			UID:               "test-uid-ir-err",
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

	dynClient := &deletionErrorDynamicClient{
		ingressRouteDeleteErr: errors.New("IngressRoute deletion failed"),
	}

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
			Name:      "ir-delete-error",
			Namespace: "default",
		},
	}

	// Reconcile should continue despite IngressRoute deletion error
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error should be logged not returned)", err)
	}

	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 after deletion handling")
	}

	// Verify that deletion was still attempted for all resources
	// IngressRoute delete was called (and errored), middleware delete should also have been called
	if len(dynClient.deletedResources) < 2 {
		t.Errorf("expected at least 2 delete attempts, got %d: %v", len(dynClient.deletedResources), dynClient.deletedResources)
	}
}

func TestHandleDeletion_MiddlewareDeleteError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	now := metav1.Now()
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "mw-delete-error",
			Namespace:         "default",
			UID:               "test-uid-mw-err",
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

	dynClient := &deletionErrorDynamicClient{
		middlewareDeleteErr: errors.New("Middleware deletion failed"),
	}

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
			Name:      "mw-delete-error",
			Namespace: "default",
		},
	}

	// Reconcile should continue despite Middleware deletion error
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil (error should be logged not returned)", err)
	}

	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 after deletion handling")
	}
}

func TestHandleDeletion_StripMiddlewareDeleteError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	now := metav1.Now()
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "strip-delete-error",
			Namespace:         "default",
			UID:               "test-uid-strip-err",
			Finalizers:        []string{finalizerName},
			DeletionTimestamp: &now,
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:       "https://github.com/example/repo.git",
			Domain:     "example.com",
			PathPrefix: "/2019",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		Build()

	dynClient := &deletionErrorDynamicClient{
		middlewareDeleteErr: errors.New("strip middleware deletion failed"),
	}

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
			Name:      "strip-delete-error",
			Namespace: "default",
		},
	}

	// Reconcile should continue despite stripPrefix Middleware deletion error
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}

	if result.RequeueAfter != 0 {
		t.Error("expected RequeueAfter=0 after deletion handling")
	}

	// Should have attempted to delete: ingressroute, addPrefix middleware, stripPrefix middleware
	if len(dynClient.deletedResources) < 3 {
		t.Errorf("expected at least 3 delete attempts (for pathPrefix site), got %d: %v", len(dynClient.deletedResources), dynClient.deletedResources)
	}
}

// failingStatusClient wraps a real client but fails on status updates
type failingStatusClient struct {
	client.Client
	statusUpdateErr error
}

func (f *failingStatusClient) Status() client.StatusWriter {
	return &failingStatusWriter{
		StatusWriter: f.Client.Status(),
		err:          f.statusUpdateErr,
	}
}

type failingStatusWriter struct {
	client.StatusWriter
	err error
}

func (f *failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return f.err
}

func TestEnsureFinalizerAndToken_StatusUpdateFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "status-update-fail",
			Namespace:  "default",
			UID:        "test-uid-status-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
		// SyncToken is empty, so ensureFinalizerAndToken will try to set it
	}

	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	failingClient := &failingStatusClient{
		Client:          baseClient,
		statusUpdateErr: errors.New("status update failed"),
	}

	r := &StaticSiteReconciler{
		Client:           failingClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "status-update-fail",
			Namespace: "default",
		},
	}

	// Reconcile should return the status update error
	_, err := r.Reconcile(context.Background(), req)
	if err == nil {
		t.Fatal("Reconcile() error = nil, want error from status update")
	}
	if err.Error() != "status update failed" {
		t.Errorf("Reconcile() error = %v, want 'status update failed'", err)
	}
}

func TestSetError_StatusUpdateFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	// PathPrefix without domain triggers validation error which calls setError
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "set-error-fail",
			Namespace:  "default",
			UID:        "test-uid-seterror-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:       "https://github.com/example/repo.git",
			PathPrefix: "/2019", // PathPrefix without Domain triggers validation error
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	failingClient := &failingStatusClient{
		Client:          baseClient,
		statusUpdateErr: errors.New("setError status update failed"),
	}

	r := &StaticSiteReconciler{
		Client:           failingClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "set-error-fail",
			Namespace: "default",
		},
	}

	// Reconcile should return the status update error from setError
	_, err := r.Reconcile(context.Background(), req)
	if err == nil {
		t.Fatal("Reconcile() error = nil, want error from setError status update")
	}
	if err.Error() != "setError status update failed" {
		t.Errorf("Reconcile() error = %v, want 'setError status update failed'", err)
	}
}

func TestReconcile_StripPrefixMiddlewareCreationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "strip-mw-fail",
			Namespace:  "default",
			UID:        "test-uid-strip-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:       "https://github.com/example/repo.git",
			Domain:     "example.com",
			PathPrefix: "/2019",
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

	// Use a client that fails on the second middleware creation (stripPrefix)
	dynClient := &countingMiddlewareDynamicClient{
		failOnMiddlewareCreate: 2, // Fail on second middleware create (stripPrefix)
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
			Name:      "strip-mw-fail",
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
	if updatedSite.Status.Message != "stripPrefix middleware creation failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "stripPrefix middleware creation failed")
	}
}

// countingMiddlewareDynamicClient counts middleware creates and can fail on a specific one
type countingMiddlewareDynamicClient struct {
	middlewareCreateCount  int
	failOnMiddlewareCreate int // Fail when count reaches this number
}

func (c *countingMiddlewareDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &countingMiddlewareNamespaceableResource{
		gvr:    resource,
		client: c,
	}
}

type countingMiddlewareNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *countingMiddlewareDynamicClient
}

func (c *countingMiddlewareNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &countingMiddlewareNamespaceableResource{
		gvr:       c.gvr,
		namespace: ns,
		client:    c.client,
	}
}

func (c *countingMiddlewareNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, &notFoundError{}
}

func (c *countingMiddlewareNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if c.gvr == middlewareGVR {
		c.client.middlewareCreateCount++
		if c.client.middlewareCreateCount == c.client.failOnMiddlewareCreate {
			return nil, errors.New("stripPrefix middleware creation failed")
		}
	}
	return obj, nil
}

func (c *countingMiddlewareNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (c *countingMiddlewareNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (c *countingMiddlewareNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (c *countingMiddlewareNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (c *countingMiddlewareNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (c *countingMiddlewareNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (c *countingMiddlewareNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (c *countingMiddlewareNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (c *countingMiddlewareNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestUpdateFinalStatus_StatusUpdateFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "final-status-fail",
			Namespace:  "default",
			UID:        "test-uid-final-fail",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo: "https://github.com/example/repo.git",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "already-generated",
		},
	}

	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	// Create a client that fails only on final status update (not initial ones)
	failingClient := &selectiveFailingStatusClient{
		Client:      baseClient,
		failOnPhase: pagesv1.PhaseReady,
	}

	r := &StaticSiteReconciler{
		Client:           failingClient,
		DynamicClient:    &fakeDynamicClient{},
		Recorder:         events.NewFakeRecorder(10),
		PagesDomain:      "pages.kup6s.com",
		ClusterIssuer:    "letsencrypt-prod",
		NginxNamespace:   "kup6s-pages",
		NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "final-status-fail",
			Namespace: "default",
		},
	}

	// Reconcile should return the status update error from updateFinalStatus
	_, err := r.Reconcile(context.Background(), req)
	if err == nil {
		t.Fatal("Reconcile() error = nil, want error from final status update")
	}
	if err.Error() != "final status update failed" {
		t.Errorf("Reconcile() error = %v, want 'final status update failed'", err)
	}
}

// selectiveFailingStatusClient fails status update only when phase is set to a specific value
type selectiveFailingStatusClient struct {
	client.Client
	failOnPhase pagesv1.Phase
}

func (s *selectiveFailingStatusClient) Status() client.StatusWriter {
	return &selectiveFailingStatusWriter{
		StatusWriter: s.Client.Status(),
		failOnPhase:  s.failOnPhase,
	}
}

type selectiveFailingStatusWriter struct {
	client.StatusWriter
	failOnPhase pagesv1.Phase
}

func (s *selectiveFailingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if site, ok := obj.(*pagesv1.StaticSite); ok {
		if site.Status.Phase == s.failOnPhase {
			return errors.New("final status update failed")
		}
	}
	return s.StatusWriter.Update(ctx, obj, opts...)
}

func TestReconcile_GetSiteError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	// Create a client that returns an error on Get (not NotFound)
	fakeClient := &getErrorClient{
		err: errors.New("connection refused"),
	}

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
			Name:      "error-site",
			Namespace: "default",
		},
	}

	// Reconcile should return the Get error
	_, err := r.Reconcile(context.Background(), req)
	if err == nil {
		t.Fatal("Reconcile() error = nil, want error from Get")
	}
	if err.Error() != "connection refused" {
		t.Errorf("Reconcile() error = %v, want 'connection refused'", err)
	}
}

// getErrorClient returns an error on Get operations
type getErrorClient struct {
	client.Client
	err error
}

func (g *getErrorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return g.err
}

func TestCreateOrUpdateMiddleware_GetError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "mw-get-error",
			Namespace:  "default",
			UID:        "test-uid-mw-get-err",
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

	// Use a dynamic client that returns a non-NotFound error on Get for middleware
	dynClient := &getErrorDynamicClient{
		middlewareGetErr: errors.New("middleware get failed"),
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
			Name:      "mw-get-error",
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
	if updatedSite.Status.Message != "middleware get failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "middleware get failed")
	}
}

// getErrorDynamicClient returns errors on Get for specific resources
type getErrorDynamicClient struct {
	middlewareGetErr    error
	ingressRouteGetErr  error
	certificateGetErr   error
}

func (g *getErrorDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &getErrorNamespaceableResource{
		gvr:    resource,
		client: g,
	}
}

type getErrorNamespaceableResource struct {
	gvr       schema.GroupVersionResource
	namespace string
	client    *getErrorDynamicClient
}

func (g *getErrorNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &getErrorNamespaceableResource{
		gvr:       g.gvr,
		namespace: ns,
		client:    g.client,
	}
}

func (g *getErrorNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if g.gvr == middlewareGVR && g.client.middlewareGetErr != nil {
		return nil, g.client.middlewareGetErr
	}
	if g.gvr == ingressRouteGVR && g.client.ingressRouteGetErr != nil {
		return nil, g.client.ingressRouteGetErr
	}
	if g.gvr == certificateGVR && g.client.certificateGetErr != nil {
		return nil, g.client.certificateGetErr
	}
	return nil, &notFoundError{}
}

func (g *getErrorNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (g *getErrorNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (g *getErrorNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (g *getErrorNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (g *getErrorNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (g *getErrorNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (g *getErrorNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (g *getErrorNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (g *getErrorNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (g *getErrorNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func TestReconcileIngressRoute_GetError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ir-get-error",
			Namespace:  "default",
			UID:        "test-uid-ir-get-err",
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

	// Use a dynamic client that returns a non-NotFound error on Get for IngressRoute
	dynClient := &getErrorDynamicClient{
		ingressRouteGetErr: errors.New("ingressroute get failed"),
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
			Name:      "ir-get-error",
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
	if updatedSite.Status.Message != "ingressroute get failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "ingressroute get failed")
	}
}

func TestReconcileCertificate_GetError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cert-get-error",
			Namespace:  "default",
			UID:        "test-uid-cert-get-err",
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

	// Use a dynamic client that returns a non-NotFound error on Get for Certificate
	dynClient := &getErrorDynamicClient{
		certificateGetErr: errors.New("certificate get failed"),
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
			Name:      "cert-get-error",
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
	if updatedSite.Status.Message != "certificate get failed" {
		t.Errorf("Message = %q, want %q", updatedSite.Status.Message, "certificate get failed")
	}
}

func TestTlsModeConstants(t *testing.T) {
	if TlsModeIndividual != "individual" {
		t.Errorf("TlsModeIndividual = %q, want %q", TlsModeIndividual, "individual")
	}
	if TlsModeWildcard != "wildcard" {
		t.Errorf("TlsModeWildcard = %q, want %q", TlsModeWildcard, "wildcard")
	}
}

func TestReconcile_AutoDomainWildcardMode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "wildcard-site",
			Namespace:  "default",
			UID:        "test-uid-wildcard",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Branch: "main",
			// No custom domain - uses auto-generated domain
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "existing-token", // Skip token generation
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	dc := newCapturingDynamicClient()
	r := &StaticSiteReconciler{
		Client:              fakeClient,
		DynamicClient:       dc,
		Recorder:            events.NewFakeRecorder(10),
		PagesDomain:         "pages.kup6s.com",
		ClusterIssuer:       "letsencrypt-prod",
		NginxNamespace:      "kup6s-pages",
		NginxServiceName:    "kup6s-pages-nginx",
		PagesTlsMode:        TlsModeWildcard,
		PagesWildcardSecret: "my-wildcard-tls",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "wildcard-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Check IngressRoute was created with wildcard secret
	ir := dc.getCreatedResource("ingressroutes")
	if ir == nil {
		t.Fatal("IngressRoute was not created")
	}

	tls, found, err := unstructured.NestedMap(ir.Object, "spec", "tls")
	if err != nil || !found {
		t.Fatal("IngressRoute tls config not found")
	}
	secretName := tls["secretName"].(string)
	if secretName != "my-wildcard-tls" {
		t.Errorf("IngressRoute tls.secretName = %q, want %q", secretName, "my-wildcard-tls")
	}

	// Verify NO Certificate was created (wildcard mode uses existing cert)
	cert := dc.getCreatedResource("certificates")
	if cert != nil {
		t.Error("Certificate should NOT be created in wildcard mode for auto-generated domains")
	}
}

func TestReconcile_AutoDomainIndividualMode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "individual-site",
			Namespace:  "default",
			UID:        "test-uid-individual",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Branch: "main",
			// No custom domain - uses auto-generated domain
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "existing-token", // Skip token generation
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	dc := newCapturingDynamicClient()
	r := &StaticSiteReconciler{
		Client:              fakeClient,
		DynamicClient:       dc,
		Recorder:            events.NewFakeRecorder(10),
		PagesDomain:         "pages.kup6s.com",
		ClusterIssuer:       "letsencrypt-prod",
		NginxNamespace:      "kup6s-pages",
		NginxServiceName:    "kup6s-pages-nginx",
		PagesTlsMode:        TlsModeIndividual,
		PagesWildcardSecret: "", // Not used in individual mode
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "individual-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Check IngressRoute was created with individual secret name
	ir := dc.getCreatedResource("ingressroutes")
	if ir == nil {
		t.Fatal("IngressRoute was not created")
	}

	tls, found, err := unstructured.NestedMap(ir.Object, "spec", "tls")
	if err != nil || !found {
		t.Fatal("IngressRoute tls config not found")
	}
	// For auto-generated domain "individual-site.pages.kup6s.com"
	// the secret should be "individual-site-pages-kup6s-com-tls"
	secretName := tls["secretName"].(string)
	expectedSecret := "individual-site-pages-kup6s-com-tls"
	if secretName != expectedSecret {
		t.Errorf("IngressRoute tls.secretName = %q, want %q", secretName, expectedSecret)
	}

	// Verify Certificate WAS created (individual mode creates per-site certs)
	cert := dc.getCreatedResource("certificates")
	if cert == nil {
		t.Fatal("Certificate should be created in individual mode for auto-generated domains")
	}

	// Verify certificate domain
	dnsNames, found, _ := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
	if !found || len(dnsNames) == 0 {
		t.Fatal("Certificate dnsNames not found")
	}
	if dnsNames[0] != "individual-site.pages.kup6s.com" {
		t.Errorf("Certificate dnsNames[0] = %q, want %q", dnsNames[0], "individual-site.pages.kup6s.com")
	}
}

func TestReconcile_CustomDomainAlwaysIndividual(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	// Custom domain sites should always use individual certs, regardless of PagesTlsMode
	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "custom-domain-site",
			Namespace:  "default",
			UID:        "test-uid-custom",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Branch: "main",
			Domain: "www.example.com",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "existing-token",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	dc := newCapturingDynamicClient()
	r := &StaticSiteReconciler{
		Client:              fakeClient,
		DynamicClient:       dc,
		Recorder:            events.NewFakeRecorder(10),
		PagesDomain:         "pages.kup6s.com",
		ClusterIssuer:       "letsencrypt-prod",
		NginxNamespace:      "kup6s-pages",
		NginxServiceName:    "kup6s-pages-nginx",
		PagesTlsMode:        TlsModeWildcard, // Even with wildcard mode
		PagesWildcardSecret: "pages-wildcard-tls",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "custom-domain-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Check IngressRoute was created with domain-based secret (NOT wildcard)
	ir := dc.getCreatedResource("ingressroutes")
	if ir == nil {
		t.Fatal("IngressRoute was not created")
	}

	tls, found, err := unstructured.NestedMap(ir.Object, "spec", "tls")
	if err != nil || !found {
		t.Fatal("IngressRoute tls config not found")
	}
	secretName := tls["secretName"].(string)
	if secretName != "www-example-com-tls" {
		t.Errorf("IngressRoute tls.secretName = %q, want %q", secretName, "www-example-com-tls")
	}

	// Verify Certificate WAS created for custom domain
	cert := dc.getCreatedResource("certificates")
	if cert == nil {
		t.Fatal("Certificate should be created for custom domain sites")
	}
}

func TestReconcile_DefaultTlsModeIsIndividual(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "default-mode-site",
			Namespace:  "default",
			UID:        "test-uid-default",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{
			Repo:   "https://github.com/example/repo.git",
			Branch: "main",
		},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "existing-token",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(site).
		WithStatusSubresource(site).
		Build()

	dc := newCapturingDynamicClient()
	r := &StaticSiteReconciler{
		Client:              fakeClient,
		DynamicClient:       dc,
		Recorder:            events.NewFakeRecorder(10),
		PagesDomain:         "pages.kup6s.com",
		ClusterIssuer:       "letsencrypt-prod",
		NginxNamespace:      "kup6s-pages",
		NginxServiceName:    "kup6s-pages-nginx",
		PagesTlsMode:        "", // Empty = default to individual
		PagesWildcardSecret: "",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "default-mode-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Should behave like individual mode - create certificate
	cert := dc.getCreatedResource("certificates")
	if cert == nil {
		t.Fatal("Certificate should be created when PagesTlsMode is empty (default to individual)")
	}
}

// capturingDynamicClient captures created resources for verification in tests
type capturingDynamicClient struct {
	created map[string]*unstructured.Unstructured
}

func newCapturingDynamicClient() *capturingDynamicClient {
	return &capturingDynamicClient{
		created: make(map[string]*unstructured.Unstructured),
	}
}

func (c *capturingDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &capturingNamespaceableResource{client: c, resource: resource.Resource}
}

func (c *capturingDynamicClient) getCreatedResource(resourceType string) *unstructured.Unstructured {
	return c.created[resourceType]
}

type capturingNamespaceableResource struct {
	client   *capturingDynamicClient
	resource string
}

func (f *capturingNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return f
}

func (f *capturingNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (f *capturingNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	// Store reference directly - no DeepCopy needed for test verification
	f.client.created[f.resource] = obj
	return obj, nil
}

func (f *capturingNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *capturingNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *capturingNamespaceableResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (f *capturingNamespaceableResource) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (f *capturingNamespaceableResource) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, &notFoundError{}
}

func (f *capturingNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *capturingNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (f *capturingNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *capturingNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}
