/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfisicalConnection is the Schema for the infisicalconnection API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Method",type=string,JSONPath=`.spec.method`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/IsReady")].status`
type InfisicalAuth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalAuthSpec         `json:"spec,omitempty"`
	Status InfisicalConnectionStatus `json:"status,omitempty"`
}

type InfisicalAuthSpec struct {
	// +kubebuilder:validation:Required
	InfisicalConnectionRef InfisicalConnectionRef `json:"infisicalConnectionRef"`

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
	IdentityID string `json:"identityId"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="default"
	ServiceAccountName string `json:"serviceAccountName"`
}

type AWSIamAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityID string `json:"identityId"`
}

type AzureAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityID string `json:"identityId"`
}

type GCPIdTokenAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityID string `json:"identityId"`
}

type GCPIamAuthConfig struct {
	// +kubebuilder:validation:Required
	IdentityID string `json:"identityId"`

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

type InfisicalConnectionRef struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
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

// InfisicalAuthList contains a list of InfisicalConnection.
// +kubebuilder:object:root=true
type InfisicalAuthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfisicalAuth `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfisicalAuth{}, &InfisicalAuthList{})
}
