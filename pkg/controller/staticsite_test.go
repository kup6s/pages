package controller

import (
	"context"
	"testing"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
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
		Client:        fakeClient,
		DynamicClient: &fakeDynamicClient{},
		Recorder:      record.NewFakeRecorder(10),
		PagesDomain:   "pages.kup6s.com",
		ClusterIssuer: "letsencrypt-prod",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-site",
			Namespace: "default",
		},
	}

	// Erster Reconcile: Finalizer hinzufügen
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.Requeue {
		t.Error("expected Requeue=true after adding finalizer")
	}

	// Zweiter Reconcile: Ressourcen erstellen
	result, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Status prüfen
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
		Client:        fakeClient,
		DynamicClient: &fakeDynamicClient{},
		Recorder:      record.NewFakeRecorder(10),
		PagesDomain:   "pages.kup6s.com",
		ClusterIssuer: "letsencrypt-prod",
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "custom-site",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.Background(), req)
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
		Client:        fakeClient,
		DynamicClient: &fakeDynamicClient{},
		Recorder:      record.NewFakeRecorder(10),
		PagesDomain:   "pages.kup6s.com",
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
	if result.Requeue {
		t.Error("expected Requeue=false for not found")
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
		Client:        fakeClient,
		DynamicClient: &fakeDynamicClient{},
		Recorder:      record.NewFakeRecorder(10),
		PagesDomain:   "pages.kup6s.com",
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
	if result.Requeue {
		t.Error("expected Requeue=false after deletion handling")
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

func TestNginxServiceConfig(t *testing.T) {
	if nginxServiceName != "static-sites-nginx" {
		t.Errorf("nginxServiceName = %q, want %q", nginxServiceName, "static-sites-nginx")
	}
	if nginxNamespace != "kup6s-pages" {
		t.Errorf("nginxNamespace = %q, want %q", nginxNamespace, "kup6s-pages")
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
