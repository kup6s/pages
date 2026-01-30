// Package v1alpha1 contains API Schema definitions
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group   = "pages.kup6s.com"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the API Group Version
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder is used to register types
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds types to the scheme
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registers our types
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&StaticSite{},
		&StaticSiteList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// Resource returns the GroupResource for StaticSites
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}
