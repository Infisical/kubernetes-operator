/*
MIT License

Copyright (c) 2024 Infisical

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfisicalAuth is the Schema for the InfisicalAuth API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Connection",type=string,JSONPath=`.spec.infisicalConnectionRef.name`
// +kubebuilder:printcolumn:name="Method",type=string,JSONPath=`.spec.method`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/IsReady")].status`
type InfisicalAuth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalAuthSpec   `json:"spec,omitempty"`
	Status InfisicalAuthStatus `json:"status,omitempty"`
}

type InfisicalAuthSpec struct {
	// +kubebuilder:validation:Required
	InfisicalConnectionRef NamespacedName `json:"infisicalConnectionRef"`

	// +kubebuilder:validation:Required
	Method InfisicalAuthMethod `json:"method"`

	// +kubebuilder:validation:Optional
	Universal *UniversalAuthConfig `json:"universal,omitempty"`

	// +kubebuilder:validation:Optional
	Kubernetes *KubernetesAuthConfig `json:"kubernetes,omitempty"`

	// +kubebuilder:validation:Optional
	AWSIam *AWSIamAuthConfig `json:"awsIam,omitempty"`

	// +kubebuilder:validation:Optional
	Azure *AzureAuthConfig `json:"azure,omitempty"`

	// +kubebuilder:validation:Optional
	GCPIdToken *GCPIdTokenAuthConfig `json:"gcpIdToken,omitempty"`

	// +kubebuilder:validation:Optional
	GCPIam *GCPIamAuthConfig `json:"gcpIam,omitempty"`

	// +kubebuilder:validation:Optional
	LDAP *LDAPAuthConfig `json:"ldap,omitempty"`
}

type UniversalAuthConfig struct {
	// +kubebuilder:validation:Required
	ClientIdRef SecretReference `json:"clientIdRef"`

	// +kubebuilder:validation:Required
	ClientSecretRef SecretReference `json:"clientSecretRef"`
}

type KubernetesAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityIDRef SecretReference `json:"identityIdRef"`

	// +kubebuilder:validation:Required
	ServiceAccountRef NamespacedName `json:"serviceAccountRef"`

	// The audiences to use for the service account token. This is only relevant if `autoCreateServiceAccountToken` is true.
	// +kubebuilder:validation:Optional
	ServiceAccountTokenAudiences []string `json:"serviceAccountTokenAudiences"`
}

type AWSIamAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityIDRef SecretReference `json:"identityIdRef"`
}

type AzureAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityIDRef SecretReference `json:"identityIdRef"`

	// +kubebuilder:validation:Optional
	Resource string `json:"resource"`
}

type GCPIdTokenAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityIDRef SecretReference `json:"identityIdRef"`
}

type GCPIamAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityIDRef SecretReference `json:"identityIdRef"`

	// +kubebuilder:validation:Required
	ServiceAccountKeyFilePath string `json:"serviceAccountKeyFilePath"`
}

type LDAPAuthConfig struct {
	// +kubebuilder:validation:Required
	UsernameRef SecretReference `json:"usernameRef"`

	// +kubebuilder:validation:Required
	PasswordRef SecretReference `json:"passwordRef"`

	// +kubebuilder:validation:Required
	IdentityIDRef SecretReference `json:"identityIdRef"`
}

type NamespacedName struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// InfisicalAuthStatus defines the observed state of InfisicalAuth
type InfisicalAuthStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}

// +kubebuilder:validation:Enum=universal;kubernetes;aws-iam;azure;gcp-id-token;gcp-iam;ldap
type InfisicalAuthMethod string

const (
	AWSIamAuth     InfisicalAuthMethod = "aws-iam"
	AzureAuth      InfisicalAuthMethod = "azure"
	GCPIamAuth     InfisicalAuthMethod = "gcp-iam"
	GCPIdTokenAuth InfisicalAuthMethod = "gcp-id-token"
	KubernetesAuth InfisicalAuthMethod = "kubernetes"
	LDAPAuth       InfisicalAuthMethod = "ldap"
	UniversalAuth  InfisicalAuthMethod = "universal"
)

// InfisicalAuthList contains a list of InfisicalAuth.
// +kubebuilder:object:root=true
type InfisicalAuthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfisicalAuth `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfisicalAuth{}, &InfisicalAuthList{})
}
