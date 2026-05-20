package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase represents the lifecycle state of a PostgresDatabase.
type Phase string

const (
	PhasePending Phase = "Pending"
	PhaseRunning Phase = "Running"
	PhaseFailed  Phase = "Failed"
)

// Condition type constants used in Status.Conditions.
const (
	ConditionReady       = "Ready"
	ConditionProgressing = "Progressing"
	ConditionDegraded    = "Degraded"
)

// PostgresDatabaseSpec defines the desired state of PostgresDatabase.
type PostgresDatabaseSpec struct {
	// Version of PostgreSQL to deploy (e.g. "16", "15").
	// +kubebuilder:default="16"
	// +kubebuilder:validation:Enum="14";"15";"16";"17"
	Version string `json:"version"`

	// Replicas is the number of PostgreSQL pods.
	// 1 = standalone; 2+ = one primary + N-1 streaming replicas.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	Replicas int32 `json:"replicas,omitempty"`

	// Storage defines the PVC configuration for each pod's data directory.
	Storage StorageSpec `json:"storage"`

	// Resources sets CPU and memory requests/limits for each PostgreSQL container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Database is the name of the initial database created on first boot.
	// +kubebuilder:default="appdb"
	Database string `json:"database,omitempty"`

	// Username is the owner of the initial database.
	// +kubebuilder:default="appuser"
	Username string `json:"username,omitempty"`

	// PasswordSecretRef points to an existing Secret that holds the password.
	// If omitted, the operator generates a random password and stores it in
	// a managed Secret named "<name>-credentials".
	// +optional
	PasswordSecretRef *SecretKeySelector `json:"passwordSecretRef,omitempty"`
}

// StorageSpec configures the PersistentVolumeClaim for each pod.
type StorageSpec struct {
	// Size is the storage request (e.g. "10Gi").
	Size resource.Quantity `json:"size"`

	// StorageClassName is the Kubernetes StorageClass to use.
	// Defaults to the cluster default StorageClass when omitted.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`
}

// SecretKeySelector references a key within a Kubernetes Secret.
type SecretKeySelector struct {
	// Name of the Secret.
	Name string `json:"name"`
	// Key within the Secret.
	Key string `json:"key"`
}

// PostgresDatabaseStatus defines the observed state of PostgresDatabase.
type PostgresDatabaseStatus struct {
	// Phase is the high-level lifecycle state (Pending, Running, Failed).
	Phase Phase `json:"phase,omitempty"`

	// ReadyReplicas is the count of pods that passed their readiness probe.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Conditions is a list of fine-grained status conditions.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// SecretName is the name of the Secret that holds the connection credentials.
	SecretName string `json:"secretName,omitempty"`

	// ServiceName is the ClusterIP Service used to reach the primary instance.
	ServiceName string `json:"serviceName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.readyReplicas
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PostgresDatabase is the Schema for the postgresdatabases API.
type PostgresDatabase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresDatabaseSpec   `json:"spec,omitempty"`
	Status PostgresDatabaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresDatabaseList contains a list of PostgresDatabase.
type PostgresDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresDatabase{}, &PostgresDatabaseList{})
}
