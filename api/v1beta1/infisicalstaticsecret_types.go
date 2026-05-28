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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretTargetKind string

const (
	SecretTargetKindSecret    SecretTargetKind = "Secret"
	SecretTargetKindConfigMap SecretTargetKind = "ConfigMap"
)

// +kubebuilder:validation:Enum=Owner;Orphan
type CreationPolicy string

const (
	CreationPolicyOwner  CreationPolicy = "Owner"
	CreationPolicyOrphan CreationPolicy = "Orphan"
)

// InfisicalStaticSecret is the Schema for the InfisicalStaticSecret API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Auth Method",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/LastReconcileAuthMethod")].message`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/LastReconcileStatus")].status`
// +kubebuilder:printcolumn:name="Affected Deployments",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/LastReconcileAffectedDeployments")].message`
type InfisicalStaticSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalStaticSecretSpec   `json:"spec,omitempty"`
	Status InfisicalStaticSecretStatus `json:"status,omitempty"`
}

type InfisicalStaticSecretSpec struct {
	// +kubebuilder:validation:Required
	InfisicalAuthRef NamespacedName `json:"infisicalAuthRef"`

	// +kubebuilder:validation:Required
	SyncOptions *SyncOptions `json:"syncOptions"`

	// +kubebuilder:validation:Required
	Sources []SecretSource `json:"sources"`

	// +kubebuilder:validation:Required
	Targets []SecretTarget `json:"targets"`
}

type SyncOptions struct {
	// +kubebuilder:validation:Required
	RefreshInterval string `json:"refreshInterval"`

	// +kubebuilder:validation:Optional
	InstantUpdates bool `json:"instantUpdates"`
}

type SecretSource struct {
	// +kubebuilder:validation:Required
	ProjectId string `json:"projectId"`

	// +kubebuilder:validation:Required
	EnvironmentSlug string `json:"environmentSlug"`

	// +kubebuilder:validation:Required
	SecretPath string `json:"secretPath"`

	// +kubebuilder:validation:Optional
	TagSlugs []string `json:"tagSlugs,omitempty"`

	// +kubebuilder:validation:Optional
	Recursive bool `json:"recursive,omitempty"`
}

type SecretTarget struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Secret;ConfigMap
	Kind SecretTargetKind `json:"kind"`

	// +kubebuilder:validation:Optional
	SecretType corev1.SecretType `json:"secretType,omitempty"`

	// +kubebuilder:validation:Required
	CreationPolicy CreationPolicy `json:"creationPolicy"`

	// +kubebuilder:validation:Optional
	Template *SecretTemplate `json:"template,omitempty"`

	// +kubebuilder:validation:Optional
	Metadata *SecretTargetMetadata `json:"metadata,omitempty"`
}

type SecretTargetMetadata struct {
	Annotations map[string]string `json:"annotations"`
	Labels      map[string]string `json:"labels"`
}

type SecretTemplate struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=v1
	EngineVersion string `json:"engineVersion"`

	// Data defines the templated output. It accepts either a map of per-key
	// Go templates (each entry becomes one key in the resulting Secret /
	// ConfigMap) or a single Go template string whose rendered output is
	// YAML-decoded into a map of key/value pairs.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:XPreserveUnknownFields
	Data SecretTemplateData `json:"data"`
}

// InfisicalStaticSecretStatus defines the observed state of InfisicalAuth
type InfisicalStaticSecretStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}

// InfisicalStaticSecretList contains a list of InfisicalStaticSecret.
// +kubebuilder:object:root=true
type InfisicalStaticSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfisicalStaticSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfisicalStaticSecret{}, &InfisicalStaticSecretList{})
}
