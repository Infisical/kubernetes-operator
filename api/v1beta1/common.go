package v1beta1

type TLSConfig struct {
	// Reference to secret containing CA cert
	// +kubebuilder:validation:Optional
	CaCertificate CaCertificate `json:"caCertificate,omitempty"`
}

type CaCertificate struct {
	// The name of the Kubernetes Secret
	// +kubebuilder:validation:Required
	SecretName string `json:"name"`

	// The namespace where the Kubernetes Secret is located
	// +kubebuilder:validation:Required
	SecretNamespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	// The name of the secret property with the CA certificate value
	SecretKey string `json:"key"`
}
