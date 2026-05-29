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

package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InfisicalDynamicSecretLease struct {
	ID                string      `json:"id"`
	Version           int64       `json:"version"`
	CreationTimestamp metav1.Time `json:"creationTimestamp"`
	ExpiresAt         metav1.Time `json:"expiresAt"`
}

type DynamicSecretDetails struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Immutable
	SecretName string `json:"secretName"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Immutable
	SecretPath string `json:"secretsPath"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Immutable
	EnvironmentSlug string `json:"environmentSlug"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Immutable
	ProjectID string `json:"projectId"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Immutable
	ProjectSlug string `json:"projectSlug"`
}

func (d *DynamicSecretDetails) ValidateDetails() error {

	if d.ProjectID == "" && d.ProjectSlug == "" {
		return fmt.Errorf("either projectId or projectSlug must be specified")
	}
	if d.ProjectID != "" && d.ProjectSlug != "" {
		return fmt.Errorf("projectId and projectSlug cannot both be specified")
	}
	return nil
}

// InfisicalDynamicSecretSpec defines the desired state of InfisicalDynamicSecret.
type InfisicalDynamicSecretSpec struct {
	// +kubebuilder:validation:Required
	ManagedSecretReference ManagedKubeSecretConfig `json:"managedSecretReference"` // The destination to store the lease in.

	// +kubebuilder:validation:Required
	Authentication GenericInfisicalAuthentication `json:"authentication"` // The authentication to use for authenticating with Infisical.

	// +kubebuilder:validation:Required
	DynamicSecret DynamicSecretDetails `json:"dynamicSecret"` // The dynamic secret to create the lease for. Required.

	LeaseRevocationPolicy string `json:"leaseRevocationPolicy"` // Revoke will revoke the lease when the resource is deleted. Optional, will default to no revocation.
	LeaseTTL              string `json:"leaseTTL"`              // The TTL of the lease in seconds. Optional, will default to the dynamic secret default TTL.

	// +kubebuilder:validation:Optional
	HostAPI string `json:"hostAPI"`

	// +kubebuilder:validation:Optional
	TLS TLSConfig `json:"tls"`
}

// InfisicalDynamicSecretStatus defines the observed state of InfisicalDynamicSecret.
type InfisicalDynamicSecretStatus struct {
	Conditions []metav1.Condition `json:"conditions"`

	Lease           *InfisicalDynamicSecretLease `json:"lease,omitempty"`
	DynamicSecretID string                       `json:"dynamicSecretId,omitempty"`
	// The MaxTTL can be null, if it's null, there's no max TTL and we should never have to renew.
	MaxTTL string `json:"maxTTL,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// InfisicalDynamicSecret is the Schema for the infisicaldynamicsecrets API.
type InfisicalDynamicSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalDynamicSecretSpec   `json:"spec,omitempty"`
	Status InfisicalDynamicSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InfisicalDynamicSecretList contains a list of InfisicalDynamicSecret.
type InfisicalDynamicSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfisicalDynamicSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfisicalDynamicSecret{}, &InfisicalDynamicSecretList{})
}
