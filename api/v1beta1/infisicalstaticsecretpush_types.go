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

// PushUpdatePolicy decides whether keys that already exist in Infisical are updated.
// +kubebuilder:validation:Enum=Replace;None
type PushUpdatePolicy string

const (
	// PushUpdatePolicyReplace keeps existing keys in sync with the sources, and takes
	// over keys created outside this resource.
	PushUpdatePolicyReplace PushUpdatePolicy = "Replace"
	// PushUpdatePolicyNone only creates missing keys. It never changes a key that
	// already exists, including the keys this resource created.
	PushUpdatePolicyNone PushUpdatePolicy = "None"
)

// PushDeletionPolicy decides whether the operator deletes the secrets it pushed.
// +kubebuilder:validation:Enum=Delete;None
type PushDeletionPolicy string

const (
	// PushDeletionPolicyDelete deletes pushed secrets when their key is removed from
	// the sources, when their target is removed, or when the resource is deleted.
	PushDeletionPolicyDelete PushDeletionPolicy = "Delete"
	// PushDeletionPolicyNone never deletes secrets from Infisical.
	PushDeletionPolicyNone PushDeletionPolicy = "None"
)

// InfisicalStaticSecretPush pushes Kubernetes Secrets to one or more paths in Infisical.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Auth Method",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/LastReconcileAuthMethod")].message`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="secrets.infisical.com/LastReconcileStatus")].status`
// +kubebuilder:printcolumn:name="Error",type=boolean,JSONPath=`.status.error`
type InfisicalStaticSecretPush struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InfisicalStaticSecretPushSpec   `json:"spec,omitempty"`
	Status InfisicalStaticSecretPushStatus `json:"status,omitempty"`
}

type InfisicalStaticSecretPushSpec struct {
	// The InfisicalAuth used to push. Defaults to the "default" InfisicalAuth in the operator namespace.
	// +kubebuilder:validation:Optional
	InfisicalAuthRef NamespacedName `json:"infisicalAuthRef,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={}
	SyncOptions PushSyncOptions `json:"syncOptions,omitempty"`

	// The Kubernetes Secrets to push. Every key of every source is pushed to every target.
	// +kubebuilder:validation:Required
	Sources PushSources `json:"sources"`

	// The Infisical paths to push to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Targets []PushTarget `json:"targets"`
}

type PushSyncOptions struct {
	// How often to push again, e.g. 60s, 5m, 1h. The minimum is 5s. When omitted, the
	// operator pushes only when a source Secret or this resource changes.
	// +kubebuilder:validation:Optional
	RefreshInterval string `json:"refreshInterval,omitempty"`

	// Whether keys that already exist in Infisical are updated. Replace keeps them in
	// sync with the sources and takes over keys created outside this resource. None only
	// creates missing keys and never changes an existing one, including the keys this
	// resource created, so later changes to the sources don't reach Infisical. A key
	// another InfisicalStaticSecretPush pushed is never changed.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=Replace
	UpdatePolicy PushUpdatePolicy `json:"updatePolicy,omitempty"`

	// Delete removes pushed secrets from Infisical when their key is removed from the
	// sources, when their target is removed, or when this resource is deleted.
	// None never deletes secrets from Infisical.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=Delete
	DeletionPolicy PushDeletionPolicy `json:"deletionPolicy,omitempty"`

	// Creates the target folder, and any missing parent folders, when it doesn't exist.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	CreateMissingFolders *bool `json:"createMissingFolders,omitempty"`
}

// GetUpdatePolicy returns the update policy, applying the default when it is unset.
func (o PushSyncOptions) GetUpdatePolicy() PushUpdatePolicy {
	if o.UpdatePolicy == "" {
		return PushUpdatePolicyReplace
	}
	return o.UpdatePolicy
}

// GetDeletionPolicy returns the deletion policy, applying the default when it is unset.
func (o PushSyncOptions) GetDeletionPolicy() PushDeletionPolicy {
	if o.DeletionPolicy == "" {
		return PushDeletionPolicyDelete
	}
	return o.DeletionPolicy
}

// ShouldCreateMissingFolders returns whether missing folders are created, applying the default when it is unset.
func (o PushSyncOptions) ShouldCreateMissingFolders() bool {
	return o.CreateMissingFolders == nil || *o.CreateMissingFolders
}

type PushSources struct {
	// Kubernetes Secrets to push. When several Secrets contain the same key, the
	// Secret listed first wins.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Secrets []NamespacedName `json:"secrets"`
}

type PushTarget struct {
	// The Infisical project ID. Set either projectId or projectSlug.
	// +kubebuilder:validation:Optional
	ProjectId string `json:"projectId,omitempty"`

	// The Infisical project slug. Set either projectId or projectSlug.
	// +kubebuilder:validation:Optional
	ProjectSlug string `json:"projectSlug,omitempty"`

	// +kubebuilder:validation:Required
	EnvironmentSlug string `json:"environmentSlug"`

	// The folder to push to, e.g. /app. The root is /.
	// +kubebuilder:validation:Required
	SecretPath string `json:"secretPath"`
}

// InfisicalStaticSecretPushStatus defines the observed state of InfisicalStaticSecretPush
type InfisicalStaticSecretPushStatus struct {
	// ObservedGeneration is the most recent metadata.generation the operator has reconciled.
	// When it is lower than metadata.generation, the status does not yet reflect the current spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Error is true when the last reconciliation failed. The failure details are in the
	// secrets.infisical.com/LastReconcileStatus condition. Only trust this value once
	// observedGeneration matches metadata.generation.
	// +optional
	Error bool `json:"error"`

	Conditions []metav1.Condition `json:"conditions"`

	// ManagedSecrets maps the ID of every secret this resource pushed to Infisical to
	// its key. Only these secrets are ever deleted by the operator.
	// +optional
	ManagedSecrets map[string]string `json:"managedSecrets,omitempty"`
}

// InfisicalStaticSecretPushList contains a list of InfisicalStaticSecretPush.
// +kubebuilder:object:root=true
type InfisicalStaticSecretPushList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InfisicalStaticSecretPush `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfisicalStaticSecretPush{}, &InfisicalStaticSecretPushList{})
}
