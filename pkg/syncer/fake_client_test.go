package syncer

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// fakeDynamicClient ist ein minimaler Mock für Tests
type fakeDynamicClient struct {
	activeSites []string
}

func (f *fakeDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeNamespaceableResource{activeSites: f.activeSites}
}

type fakeNamespaceableResource struct {
	activeSites []string
	namespace   string
}

func (f *fakeNamespaceableResource) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeNamespaceableResource{activeSites: f.activeSites, namespace: ns}
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

// Nicht benötigte Methoden - minimal implementiert
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
	return &unstructured.Unstructured{}, nil
}

func (f *fakeNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}
