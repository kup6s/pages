package syncer

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeDynamicClient ist ein minimaler Mock für Tests
type fakeDynamicClient struct {
	activeSites []string
	// lastPatch captures the last patch data for testing
	lastPatch []byte
}

func (f *fakeDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeNamespaceableResource{activeSites: f.activeSites, client: f}
}

type fakeNamespaceableResource struct {
	activeSites []string
	namespace   string
	client      *fakeDynamicClient
}

func (f *fakeNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeNamespaceableResource{activeSites: f.activeSites, namespace: ns, client: f.client}
}

func (f *fakeNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	items := make([]unstructured.Unstructured, len(f.activeSites))
	for i, name := range f.activeSites {
		items[i] = unstructured.Unstructured{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": "default",
				},
			},
		}
	}
	return &unstructured.UnstructuredList{Items: items}, nil
}

// Unused methods - minimally implemented
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
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": f.namespace,
			},
			"spec": map[string]interface{}{
				"repo": "https://example.com/repo.git",
			},
		},
	}, nil
}

func (f *fakeNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if f.client != nil {
		f.client.lastPatch = data
	}
	return &unstructured.Unstructured{}, nil
}

func (f *fakeNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

// siteSpec represents a StaticSite for testing
type siteSpec struct {
	name      string
	namespace string
	repo      string
	branch    string
	path      string
}

// fakeDynamicClientWithSites is a fake dynamic client that returns sites with full specs
type fakeDynamicClientWithSites struct {
	sites     []siteSpec
	getError  bool
	lastPatch []byte
}

func (f *fakeDynamicClientWithSites) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeResourceWithSites{client: f}
}

type fakeResourceWithSites struct {
	client    *fakeDynamicClientWithSites
	namespace string
}

func (f *fakeResourceWithSites) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeResourceWithSites{client: f.client, namespace: ns}
}

func (f *fakeResourceWithSites) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	items := make([]unstructured.Unstructured, len(f.client.sites))
	for i, site := range f.client.sites {
		branch := site.branch
		if branch == "" {
			branch = "main"
		}
		path := site.path
		if path == "" {
			path = "/"
		}
		items[i] = unstructured.Unstructured{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      site.name,
					"namespace": site.namespace,
				},
				"spec": map[string]interface{}{
					"repo":   site.repo,
					"branch": branch,
					"path":   path,
				},
			},
		}
	}
	return &unstructured.UnstructuredList{Items: items}, nil
}

func (f *fakeResourceWithSites) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if f.client.getError {
		return nil, fmt.Errorf("not found")
	}
	for _, site := range f.client.sites {
		if site.name == name && (f.namespace == "" || site.namespace == f.namespace) {
			branch := site.branch
			if branch == "" {
				branch = "main"
			}
			path := site.path
			if path == "" {
				path = "/"
			}
			return &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      site.name,
						"namespace": site.namespace,
					},
					"spec": map[string]interface{}{
						"repo":   site.repo,
						"branch": branch,
						"path":   path,
					},
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (f *fakeResourceWithSites) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithSites) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithSites) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithSites) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (f *fakeResourceWithSites) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (f *fakeResourceWithSites) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeResourceWithSites) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	f.client.lastPatch = data
	return &unstructured.Unstructured{}, nil
}

func (f *fakeResourceWithSites) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithSites) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

// fakeDynamicClientWithToken is a fake dynamic client that returns sites with a syncToken
type fakeDynamicClientWithToken struct {
	token string
}

func (f *fakeDynamicClientWithToken) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeResourceWithToken{token: f.token}
}

type fakeResourceWithToken struct {
	token     string
	namespace string
}

func (f *fakeResourceWithToken) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeResourceWithToken{token: f.token, namespace: ns}
}

func (f *fakeResourceWithToken) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return &unstructured.UnstructuredList{}, nil
}

func (f *fakeResourceWithToken) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": f.namespace,
			},
			"spec": map[string]interface{}{
				"repo": "https://example.com/repo.git",
			},
			"status": map[string]interface{}{
				"syncToken": f.token,
			},
		},
	}, nil
}

func (f *fakeResourceWithToken) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithToken) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithToken) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithToken) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (f *fakeResourceWithToken) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (f *fakeResourceWithToken) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeResourceWithToken) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (f *fakeResourceWithToken) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithToken) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

// fakeDynamicClientWithInvalidSite returns sites with invalid spec (no spec field)
type fakeDynamicClientWithInvalidSite struct{}

func (f *fakeDynamicClientWithInvalidSite) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeResourceWithInvalidSite{}
}

type fakeResourceWithInvalidSite struct {
	namespace string
}

func (f *fakeResourceWithInvalidSite) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeResourceWithInvalidSite{namespace: ns}
}

func (f *fakeResourceWithInvalidSite) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	// Return a site with invalid spec (no spec field, just a string)
	return &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{
			{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "invalid-site",
						"namespace": "default",
					},
					"spec": "invalid", // Should be a map, not string
				},
			},
		},
	}, nil
}

func (f *fakeResourceWithInvalidSite) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, fmt.Errorf("not found")
}

func (f *fakeResourceWithInvalidSite) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithInvalidSite) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithInvalidSite) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithInvalidSite) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (f *fakeResourceWithInvalidSite) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (f *fakeResourceWithInvalidSite) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeResourceWithInvalidSite) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (f *fakeResourceWithInvalidSite) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithInvalidSite) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

// fakeDynamicClientWithListError returns an error on List
type fakeDynamicClientWithListError struct{}

func (f *fakeDynamicClientWithListError) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeResourceWithListError{}
}

type fakeResourceWithListError struct {
	namespace string
}

func (f *fakeResourceWithListError) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeResourceWithListError{namespace: ns}
}

func (f *fakeResourceWithListError) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return nil, fmt.Errorf("list error")
}

func (f *fakeResourceWithListError) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, fmt.Errorf("not found")
}

func (f *fakeResourceWithListError) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithListError) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithListError) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithListError) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	return nil
}

func (f *fakeResourceWithListError) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	return nil
}

func (f *fakeResourceWithListError) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeResourceWithListError) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{}, nil
}

func (f *fakeResourceWithListError) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeResourceWithListError) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

// newFakeClientset creates a fake Kubernetes clientset for testing
func newFakeClientset(secrets ...*corev1.Secret) kubernetes.Interface {
	if len(secrets) == 0 {
		return fake.NewClientset()
	}
	// Convert secrets to runtime.Object for fake.NewClientset
	objects := make([]runtime.Object, len(secrets))
	for i, s := range secrets {
		objects[i] = s
	}
	return fake.NewClientset(objects...)
}

// newTestSecret creates a test secret with the given data
func newTestSecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
}
