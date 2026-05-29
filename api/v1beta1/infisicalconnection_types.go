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
	"cmp"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InfisicalConnection is the Schema for the infisicalconnection API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/IsReady")].status`
type InfisicalConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalConnectionSpec   `json:"spec,omitempty"`
	Status InfisicalConnectionStatus `json:"status,omitempty"`
}

func (c InfisicalConnection) Address() string {
	return cmp.Or(c.Spec.Address, os.Getenv("INFISICAL_HOST_API"))
}

// InfisicalConnectionSpec defines how the operator connects to a Infisical instance
type InfisicalConnectionSpec struct {
	// +kubebuilder:validation:Optional
	Address string `json:"address,omitempty"`

	// +kubebuilder:validation:Optional
	TLS *TLSConfig `json:"tls"`
}

// InfisicalConnectionStatus defines the observed state of InfisicalConnection
type InfisicalConnectionStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}

// InfisicalConnectionList contains a list of InfisicalConnection.
// +kubebuilder:object:root=true
type InfisicalConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfisicalConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfisicalConnection{}, &InfisicalConnectionList{})
}
