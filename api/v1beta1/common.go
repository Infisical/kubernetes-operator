package v1beta1

type TLSConfig struct {
	// Reference to secret containing CA cert
	// +kubebuilder:validation:Optional
	CaRef CaReference `json:"caRef,omitempty"`
}

type CaReference struct {
	// The name of the Kubernetes Secret
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName"`

	// The namespace where the Kubernetes Secret is located
	// +kubebuilder:validation:Required
	SecretNamespace string `json:"secretNamespace"`

	// +kubebuilder:validation:Required
	// The name of the secret property with the CA certificate value
	SecretKey string `json:"key"`
}