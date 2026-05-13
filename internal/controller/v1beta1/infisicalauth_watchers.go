package v1beta1

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
)

func (r *InfisicalAuthReconciler) findAuthCRDsReferencingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	var authList secretsv1beta1.InfisicalAuthList
	if err := r.List(ctx, &authList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, auth := range authList.Items {
		if authReferencesSecret(&auth, secret.Name, secret.Namespace) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      auth.Name,
					Namespace: auth.Namespace,
				},
			})
		}
	}
	return requests
}

func (r *InfisicalAuthReconciler) findAuthCRDsReferencingConnection(ctx context.Context, obj client.Object) []reconcile.Request {
	conn, ok := obj.(*secretsv1beta1.InfisicalConnection)
	if !ok {
		return nil
	}

	var authList secretsv1beta1.InfisicalAuthList
	if err := r.List(ctx, &authList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, auth := range authList.Items {
		ref := auth.Spec.InfisicalConnectionRef
		if ref.Name == conn.Name && ref.Namespace == conn.Namespace {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      auth.Name,
					Namespace: auth.Namespace,
				},
			})
		}
	}
	return requests
}

func authReferencesSecret(auth *secretsv1beta1.InfisicalAuth, secretName, secretNamespace string) bool {
	for _, ref := range getSecretReferences(auth) {
		if ref.Name == secretName && ref.Namespace == secretNamespace {
			return true
		}
	}
	return false
}

func getSecretReferences(auth *secretsv1beta1.InfisicalAuth) []secretsv1beta1.SecretReference {
	var refs []secretsv1beta1.SecretReference
	if auth.Spec.Universal != nil {
		refs = append(refs, auth.Spec.Universal.ClientIdRef, auth.Spec.Universal.ClientSecretRef)
	}
	if auth.Spec.LDAP != nil {
		refs = append(refs, auth.Spec.LDAP.UsernameRef, auth.Spec.LDAP.PasswordRef, auth.Spec.LDAP.IdentityIDRef)
	}
	return refs
}
