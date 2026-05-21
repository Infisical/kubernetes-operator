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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=Secret;ConfigMap
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
// +kubebuilder:printcolumn:name="Connection",type=string,JSONPath=`.spec.infisicalConnectionRef.name`
// +kubebuilder:printcolumn:name="Method",type=string,JSONPath=`.spec.method`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/IsReady")].status`
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
	RefreshInterval string `json:"refreshInterval"`

	// +kubebuilder:validation:Required
	InstantUpdates bool `json:"instantUpdates"`

	// +kubebuilder:validation:Required
	Sources []SecretSource `json:"sources"`

	// +kubebuilder:validation:Required
	Targets []SecretTarget `json:"targets"`
}

type SecretSource struct {
	// +kubebuilder:validation:Required
	ProjectId string `json:"projectId"`

	// +kubebuilder:validation:Required
	EnvironmentSlug string `json:"environmentSlug"`

	// +kubebuilder:validation:Required
	SecretPath string `json:"secretPath"`

	// +kubebuilder:validation:Optional
	Tags []string `json:"tags,omitempty"`

	// +kubebuilder:validation:Optional
	Recursive bool `json:"recursive,omitempty"`

	// +kubebuilder:validation:Optional
	Metadata string `json:"metadata,omitempty"`
}

type SecretTarget struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	Kind SecretTargetKind `json:"kind"`

	// +kubebuilder:validation:Optional
	SecretType corev1.SecretType `json:"secretType,omitempty"`

	// +kubebuilder:validation:Required
	CreationPolicy CreationPolicy `json:"creationPolicy"`

	// +kubebuilder:validation:Optional
	Template *SecretTemplate `json:"template,omitempty"`
}

type SecretTemplate struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=v1
	EngineVersion string `json:"engineVersion"`

	// +kubebuilder:validation:Required
	Data map[string]string `json:"data,omitempty"`
}

// InfisicalStaticSecretStatus defines the observed state of InfisicalAuth
type InfisicalStaticSecretStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}
