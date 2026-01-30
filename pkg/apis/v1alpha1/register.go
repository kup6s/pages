// Package v1alpha1 contains API Schema definitions
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group   = "pages.kup6s.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion ist die API Group Version
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder wird verwendet um Types zu registrieren
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme fügt Types zum Scheme hinzu
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registriert unsere Types
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&StaticSite{},
		&StaticSiteList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// Resource gibt die GroupResource für StaticSites zurück
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}
