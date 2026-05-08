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
type InfisicalConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalConnectionSpec   `json:"spec,omitempty"`
	Status InfisicalConnectionStatus `json:"status,omitempty"`
}

// InfisicalConnectionSpec defines how the operator connects to a Infisical instance
type InfisicalConnectionSpec struct {
	// +kubebuilder:validation:Required
	Host string `json:"host"`

	// +kubebuilder:validation:Optional
	TLS TLSConfig `json:"tls"`
}

// InfisicalConnectionStatus defines the observed state of InfisicalConnection
type InfisicalConnectionStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}