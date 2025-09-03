package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
)

// GameServerSpec defines the desired state of a single game server.
type GameServerSpec struct {
	Image       string                       `json:"image,omitempty"`
	Port        int32                        `json:"port"` // allocated by GSDeployment
	PollPath    string                       `json:"pollPath,omitempty"` // default /status
	Env         []corev1.EnvVar              `json:"env,omitempty"`
	Resources   corev1.ResourceRequirements  `json:"resources,omitempty"`
	NodeSelector map[string]string           `json:"nodeSelector,omitempty"`
}

// GameServerStatus reflects observed state.
type GameServerStatus struct {
	Players    int32           `json:"players,omitempty"`
	MaxPlayers int32           `json:"maxPlayers,omitempty"`
	Endpoint   string          `json:"endpoint,omitempty"`
	NodeName   string          `json:"nodeName,omitempty"`
	LastPolled *metav1.Time    `json:"lastPolled,omitempty"`
	Phase      string          `json:"phase,omitempty"` // Pending|Running|Unreachable|Error|Terminating
	ZeroSince  *metav1.Time    `json:"zeroSince,omitempty"` // when players last became zero
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
type GameServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec   GameServerSpec   `json:"spec,omitempty"`
	Status GameServerStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true
type GameServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GameServer `json:"items"`
}
