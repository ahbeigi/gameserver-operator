package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PortRange struct {
	Start int32 `json:"start"`
	End   int32 `json:"end"`
}

// Minimal rollout knobs (PoC)
type UpdateStrategy struct {
	// Only "NoDisruption" supported in PoC; leave empty to default.
	Type string `json:"type,omitempty"`
	// If a server stays busy, we stop waiting after this timeout (seconds).
	DrainTimeoutSeconds int32 `json:"drainTimeoutSeconds,omitempty"`
	// How many extra servers we can add during rollout (above MinReplicas).
	MaxSurge int32 `json:"maxSurge,omitempty"`
	// How many ready servers we can have unavailable during rollout.
	MaxUnavailable int32 `json:"maxUnavailable,omitempty"`
}

// Tiny inline config
type Parameters struct {
	MaxPlayers *int32 `json:"maxPlayers,omitempty"`
}

type GSDeploymentSpec struct {
	Image                   string                      `json:"image,omitempty"`
	PollPath                string                      `json:"pollPath,omitempty"`
	MinReplicas             int32                       `json:"minReplicas"`
	MaxReplicas             int32                       `json:"maxReplicas"`
	ScaleUpThresholdPercent int32                       `json:"scaleUpThresholdPercent,omitempty"` // default 80
	ScaleDownZeroSeconds    int32                       `json:"scaleDownZeroSeconds,omitempty"`    // default 60
	PortRange               PortRange                   `json:"portRange"`
	NodeSelector            map[string]string           `json:"nodeSelector,omitempty"`
	Resources               corev1.ResourceRequirements `json:"resources,omitempty"`
	Env                     []corev1.EnvVar             `json:"env,omitempty"`
	// NEW: rollout policy (simple PoC defaults)
	UpdateStrategy UpdateStrategy `json:"updateStrategy,omitempty"`
	// NEW: tiny inline knobs (e.g., maxPlayers)
	Parameters *Parameters `json:"parameters,omitempty"`
}

type GSDeploymentStatus struct {
	Replicas       int32              `json:"replicas,omitempty"`
	ReadyReplicas  int32              `json:"readyReplicas,omitempty"`
	AllocatedPorts []int32            `json:"allocatedPorts,omitempty"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gsd
type GSDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GSDeploymentSpec   `json:"spec,omitempty"`
	Status            GSDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GSDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GSDeployment `json:"items"`
}
