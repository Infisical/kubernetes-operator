package v1beta1

type TLSConfig struct {
	// Reference to secret containing CA cert
	// +kubebuilder:validation:Optional
	CaCertificate *SecretReference `json:"caCertificate,omitempty"`
}

type SecretReference struct {
	// The name of the Kubernetes Secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// The namespace where the Kubernetes Secret is located
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	// The name of the secret property with the value
	Key string `json:"key"`
}
