package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
)

type PortRange struct {
	Start int32 `json:"start"`
	End   int32 `json:"end"`
}

type GSDeploymentSpec struct {
	Image                  string                      `json:"image,omitempty"`
	PollPath               string                      `json:"pollPath,omitempty"`
	MinReplicas            int32                       `json:"minReplicas"`
	MaxReplicas            int32                       `json:"maxReplicas"`
	ScaleUpThresholdPercent int32                      `json:"scaleUpThresholdPercent,omitempty"` // default 80
	ScaleDownZeroSeconds   int32                       `json:"scaleDownZeroSeconds,omitempty"`    // default 60
	PortRange              PortRange                   `json:"portRange"`
	NodeSelector           map[string]string           `json:"nodeSelector,omitempty"`
	Resources              corev1.ResourceRequirements `json:"resources,omitempty"`
	Env                    []corev1.EnvVar             `json:"env,omitempty"`
}

type GSDeploymentStatus struct {
	Replicas       int32              `json:"replicas,omitempty"`
	ReadyReplicas  int32              `json:"readyReplicas,omitempty"`
	AllocatedPorts []int32            `json:"allocatedPorts,omitempty"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=gsd
type GSDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec   GSDeploymentSpec   `json:"spec,omitempty"`
	Status GSDeploymentStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true
type GSDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GSDeployment `json:"items"`
}
