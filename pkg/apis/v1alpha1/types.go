// Package v1alpha1 contains the StaticSite CRD types
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Domain",type=string,JSONPath=`.spec.domain`
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.repo`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// StaticSite definiert eine statische Website
type StaticSite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StaticSiteSpec   `json:"spec,omitempty"`
	Status StaticSiteStatus `json:"status,omitempty"`
}

// StaticSiteSpec definiert die gewünschte Konfiguration
type StaticSiteSpec struct {
	// Repo ist die Git Repository URL (required)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://.*\.git$`
	Repo string `json:"repo"`

	// Branch ist der Git Branch (default: main)
	// +kubebuilder:default=main
	// +optional
	Branch string `json:"branch,omitempty"`

	// Path ist der Subpfad im Repo der served wird (default: /)
	// z.B. "/dist" oder "/public" bei Build-Output
	// +kubebuilder:default=/
	// +optional
	Path string `json:"path,omitempty"`

	// Domain ist die custom Domain für diese Site
	// Wenn leer: <name>.pages.kup6s.io
	// +optional
	Domain string `json:"domain,omitempty"`

	// SecretRef verweist auf ein Secret mit Git Credentials
	// Für private Repos
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// SyncInterval definiert wie oft der Syncer pullt (default: 5m)
	// +kubebuilder:default="5m"
	// +optional
	SyncInterval string `json:"syncInterval,omitempty"`
}

// SecretReference verweist auf ein Kubernetes Secret
type SecretReference struct {
	// Name des Secrets
	Name string `json:"name"`
	
	// Key im Secret für das Password/Token (default: password)
	// +kubebuilder:default=password
	// +optional
	Key string `json:"key,omitempty"`
}

// StaticSiteStatus beschreibt den aktuellen Zustand
type StaticSiteStatus struct {
	// Phase: Pending, Syncing, Ready, Error
	Phase Phase `json:"phase,omitempty"`

	// Message mit Details zum aktuellen Status
	Message string `json:"message,omitempty"`

	// LastSync Zeitpunkt des letzten erfolgreichen Syncs
	// +optional
	LastSync *metav1.Time `json:"lastSync,omitempty"`

	// LastCommit SHA des zuletzt synchronisierten Commits
	// +optional
	LastCommit string `json:"lastCommit,omitempty"`

	// URL der veröffentlichten Site
	// +optional
	URL string `json:"url,omitempty"`

	// Conditions für detaillierte Status-Infos
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Phase beschreibt den Lifecycle-Status
// +kubebuilder:validation:Enum=Pending;Syncing;Ready;Error
type Phase string

const (
	PhasePending Phase = "Pending"
	PhaseSyncing Phase = "Syncing"
	PhaseReady   Phase = "Ready"
	PhaseError   Phase = "Error"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// StaticSiteList ist eine Liste von StaticSites
type StaticSiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StaticSite `json:"items"`
}
