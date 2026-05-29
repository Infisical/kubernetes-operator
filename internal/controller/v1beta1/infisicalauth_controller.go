package v1beta1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalauth"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

type InfisicalAuthReconciler struct {
	client.Client
	BaseLogger        logr.Logger
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	AuthResolver      *auth.AuthStrategyResolver
}

func (r *InfisicalAuthReconciler) GetLogger(req ctrl.Request) logr.Logger {
	return r.BaseLogger.WithValues("infisicalauth", req.NamespacedName)
}

// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalauths,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalauths/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list
//+kubebuilder:rbac:groups="authentication.k8s.io",resources=tokenreviews,verbs=create
//+kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create

func (r *InfisicalAuthReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.GetLogger(req)

	var authCRD secretsv1beta1.InfisicalAuth

	err := r.Get(ctx, req.NamespacedName, &authCRD)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return ctrl.Result{}, model.NewNamespaceScopedError(err, "InfisicalAuth")
		}

		if errors.IsNotFound(err) {
			logger.Info("Infisical Auth CRD not found")
			return ctrl.Result{
				RequeueAfter: 0,
			}, nil
		}

		logger.Error(err, "Unable to fetch Infisical Auth CRD from cluster")
		return ctrl.Result{}, fmt.Errorf("unable to fetch Infisical Auth CRD from cluster: %w", err)
	}

	// If it's being deleted, we should not attempt to do anything
	// As this is a simple CRD, we don't need a finalizer to cleanup either.
	if !authCRD.DeletionTimestamp.IsZero() {
		// If we are deleting the CRD, we must ensure cache is cleaned for this one.
		r.AuthResolver.DeleteCacheEntry(&authCRD)
		return ctrl.Result{}, nil
	}

	handler := infisicalauth.NewInfisicalAuthHandler(r.Client, r.Scheme, r.IsNamespaceScoped, r.AuthResolver)
	err = handler.ValidateAndAuthenticate(ctx, logger, &authCRD)
	handler.SetReconcileConditionStatus(ctx, logger, &authCRD, err)

	return ctrl.Result{}, err
}

func (r *InfisicalAuthReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1beta1.InfisicalAuth{}, builder.WithPredicates(&SpecChangedPredicate{
			AuthResolver: r.AuthResolver,
		})).
		// Watch for secret references that got updated
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.findAuthCRDsReferencingSecret)).
		// Watch for updates on the referenced infisicalConnection, including status updates
		Watches(&secretsv1beta1.InfisicalConnection{}, handler.EnqueueRequestsFromMapFunc(r.findAuthCRDsReferencingConnection)).
		Complete(r)
}

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
			// Connection changed, we should invalidate the cache
			r.AuthResolver.DeleteCacheEntry(&auth)

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
	if auth.Spec.Kubernetes != nil {
		refs = append(refs, auth.Spec.Kubernetes.IdentityIDRef)
	}
	if auth.Spec.AWSIam != nil {
		refs = append(refs, auth.Spec.AWSIam.IdentityIDRef)
	}
	if auth.Spec.Azure != nil {
		refs = append(refs, auth.Spec.Azure.IdentityIDRef)
	}
	if auth.Spec.GCPIdToken != nil {
		refs = append(refs, auth.Spec.GCPIdToken.IdentityIDRef)
	}
	if auth.Spec.GCPIam != nil {
		refs = append(refs, auth.Spec.GCPIam.IdentityIDRef)
	}
	if auth.Spec.LDAP != nil {
		refs = append(refs, auth.Spec.LDAP.UsernameRef, auth.Spec.LDAP.PasswordRef, auth.Spec.LDAP.IdentityIDRef)
	}
	return refs
}

type SpecChangedPredicate struct {
	predicate.GenerationChangedPredicate
	AuthResolver *auth.AuthStrategyResolver
}

func (p *SpecChangedPredicate) Update(e event.UpdateEvent) bool {
	if !p.GenerationChangedPredicate.Update(e) {
		return false
	}

	oldAuth, oldOk := e.ObjectOld.(*secretsv1beta1.InfisicalAuth)
	newAuth, newOk := e.ObjectNew.(*secretsv1beta1.InfisicalAuth)

	// If there were changes to the InfisicalAuth.spec, we remove the cache entry
	// to ensure we don't reuse a credential that might not be aligned with the new
	// CRD definition.
	if oldOk && newOk && hashSpec(oldAuth.Spec) != hashSpec(newAuth.Spec) {
		p.AuthResolver.DeleteCacheEntry(oldAuth)
	}

	return true
}

func hashSpec(spec secretsv1beta1.InfisicalAuthSpec) string {
	data, _ := json.Marshal(spec)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
