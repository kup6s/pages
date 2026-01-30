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

// StaticSite defines a static website
type StaticSite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StaticSiteSpec   `json:"spec,omitempty"`
	Status StaticSiteStatus `json:"status,omitempty"`
}

// StaticSiteSpec defines the desired configuration
type StaticSiteSpec struct {
	// Repo is the Git repository URL (required)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://.*\.git$`
	Repo string `json:"repo"`

	// Branch is the Git branch (default: main)
	// +kubebuilder:default=main
	// +optional
	Branch string `json:"branch,omitempty"`

	// Path is the subpath in the repo that gets served (default: /)
	// e.g. "/dist" or "/public" for build output
	// +kubebuilder:default=/
	// +optional
	Path string `json:"path,omitempty"`

	// Domain is the custom domain for this site
	// If empty: <name>.pages.kup6s.com
	// +optional
	Domain string `json:"domain,omitempty"`

	// SecretRef references a Secret with Git credentials
	// For private repos
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// SyncInterval defines how often the Syncer pulls (default: 5m)
	// +kubebuilder:default="5m"
	// +optional
	SyncInterval string `json:"syncInterval,omitempty"`
}

// SecretReference references a Kubernetes Secret
type SecretReference struct {
	// Name of the Secret
	Name string `json:"name"`

	// Key in the Secret for the password/token (default: password)
	// +kubebuilder:default=password
	// +optional
	Key string `json:"key,omitempty"`
}

// StaticSiteStatus describes the current state
type StaticSiteStatus struct {
	// Phase: Pending, Syncing, Ready, Error
	Phase Phase `json:"phase,omitempty"`

	// Message with details about the current status
	Message string `json:"message,omitempty"`

	// LastSync timestamp of the last successful sync
	// +optional
	LastSync *metav1.Time `json:"lastSync,omitempty"`

	// LastCommit SHA of the last synchronized commit
	// +optional
	LastCommit string `json:"lastCommit,omitempty"`

	// URL of the published site
	// +optional
	URL string `json:"url,omitempty"`

	// Conditions for detailed status information
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Phase describes the lifecycle status
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

// StaticSiteList is a list of StaticSites
type StaticSiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StaticSite `json:"items"`
}
