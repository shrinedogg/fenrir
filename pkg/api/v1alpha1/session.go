package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Represents a Session CRD.
// +kubebuilder:object:root=true
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
type Session struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SessionSpec `json:"spec"`

	//+kubebuilder:validation:Optional
	//+optional
	Status SessionStatus `json:"status"`
}

// A session refers to a certain user playing a specific game with a specific
// client. This object is meant to live for the duration of the user's session.
//
// A session is created in response to a user's /launch request.
//
// It is meant to be deleted by the session controller once the agent has reported
// that it is ended.
//
// A user can have multiple sessions active at a time, and even multiple copies
// of the same "game" if the underlying persistentVolumeClass supports it multiple
// binding.
//
// A session is created when moonlight calls /launch to launch a game.
type SessionSpec struct {
	//+kubebuilder:validation:Required
	UserReference UserReference `json:"userReference"`

	//+kubebuilder:validation:Required
	GameReference GameReference `json:"gameReference"`

	//+kubebuilder:validation:Required
	PairingReference PairingReference `json:"pairingReference"`

	// The name of the Gateway used to access the moonlight server.
	// The gateway IP used for the stream session must be the same as the IP of
	// the moonlight server used to initiate the connection due to moonlight
	// protocol restrictions.
	GatewayReference GatewayReference `json:"gateway"`

	// Wolf-specific config for the session
	Config SessionInfo `json:"config"`
}

// Session State machine
// Pending -> Initializing -> WaitForPing -> Streaming -> Ended
type SessionStatus struct {
	// Represents the observations of a session's state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// The ports allocated to the session on the shared gateway.
	Ports SessionPorts `json:"ports"`

	// The RTSP url to access the stream.
	WolfSessionID string `json:"wolfSessionID,omitempty"`
	StreamURL     string `json:"streamURL,omitempty"`

	DeploymentName string `json:"deploymentName,omitempty"`
	ServiceName    string `json:"serviceName,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}

type SessionInfo struct {
	//+kubebuilder:validation:Optional
	ClientIP string `json:"clientIP,omitempty"`

	//+kubebuilder:validation:Required
	AESKey string `json:"aesKey,omitempty"`

	//+kubebuilder:validation:Required
	AESIV string `json:"aesIV,omitempty"`

	//+kubebuilder:validation:Required
	VideoWidth int `json:"videoWidth,omitempty"`

	//+kubebuilder:validation:Required
	VideoHeight int `json:"videoHeight,omitempty"`

	//+kubebuilder:validation:Required
	VideoRefreshRate int `json:"videoRefreshRate,omitempty"`

	//+kubebuilder:validation:Required
	SurroundAudioFlags int `json:"surroundAudioFlags,omitempty"`
}

// Each session will have a block of 4 ports allocated to it on the shared
// gateway VIP. Using port forward allows us to avoid the need for a separate
// IP per session or a relay which could add latency to the stream.
//
// All per-session RTP Services share one LoadBalancer VIP (see the
// lb-sharing-key annotation), so the ports must be unique per session: the
// Moonlight client dials VIP:port for RTSP and, after the RTSP SETUP response,
// VIP:<video/audio/control> for the media, and only the port distinguishes one
// session's Service from another's. wolf listens on and advertises exactly
// these ports via WOLF_RTSP_SETUP_PORT / WOLF_CONTROL_PORT /
// WOLF_VIDEO_PING_PORT / WOLF_AUDIO_PING_PORT, so the operator sets them
// per session.
type SessionPorts struct {
	// Agent is the wolf-agent API port. It is exposed on the shared VIP too
	// (so it must be unique per session) and dialed by the operator via the
	// per-session Service.
	Agent int32 `json:"agent,omitempty"`
	// RTSP is the wolf RTSP setup port (Moonlight RTSP handshake).
	RTSP int32 `json:"rtsp,omitempty"`
	// Control is the wolf ENET/control port (Moonlight input).
	Control int32 `json:"control,omitempty"`
	// VideoRTP is the wolf video RTP port.
	VideoRTP int32 `json:"videoRTP,omitempty"`
	// AudioRTP is the wolf audio RTP port.
	AudioRTP int32 `json:"audioRTP,omitempty"`
}
