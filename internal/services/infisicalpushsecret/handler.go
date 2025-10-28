package infisicalpushsecret

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	"github.com/Infisical/infisical/k8-operator/internal/config"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/go-logr/logr"
	k8Errors "k8s.io/apimachinery/pkg/api/errors"
)

type InfisicalPushSecretHandler struct {
	client.Client
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
}

func NewInfisicalPushSecretHandler(client client.Client, scheme *runtime.Scheme, isNamespaceScoped bool) *InfisicalPushSecretHandler {
	return &InfisicalPushSecretHandler{
		Client:            client,
		Scheme:            scheme,
		IsNamespaceScoped: isNamespaceScoped,
	}
}

func (h *InfisicalPushSecretHandler) SetupAPIConfig(infisicalPushSecret v1alpha1.InfisicalPushSecret, infisicalGlobalConfig *config.InfisicalGlobalConfig) error {
	if infisicalPushSecret.Spec.HostAPI == "" {
		config.API_HOST_URL = infisicalGlobalConfig.HostAPI
	} else {
		config.API_HOST_URL = util.AppendAPIEndpoint(infisicalPushSecret.Spec.HostAPI)
	}
	return nil
}

func (h *InfisicalPushSecretHandler) getInfisicalCaCertificateFromKubeSecret(ctx context.Context, tlsConfig v1alpha1.TLSConfig) (caCertificate string, err error) {

	caCertificateFromKubeSecret, err := util.GetKubeSecretByNamespacedName(ctx, h.Client, types.NamespacedName{
		Namespace: tlsConfig.CaRef.SecretNamespace,
		Name:      tlsConfig.CaRef.SecretName,
	})

	if k8Errors.IsNotFound(err) {
		return "", fmt.Errorf("kubernetes secret containing custom CA certificate cannot be found. [err=%s]", err)
	}

	if util.IsNamespaceScopedError(err, h.IsNamespaceScoped) {
		return "", fmt.Errorf("unable to fetch Kubernetes CA certificate secret. Your Operator installation is namespace scoped, and cannot read secrets outside of the namespace it is installed in. Please ensure the CA certificate secret is in the same namespace as the operator. [err=%v]", err)
	}

	if err != nil {
		return "", fmt.Errorf("something went wrong when fetching your CA certificate [err=%s]", err)
	}

	caCertificateFromSecret := string(caCertificateFromKubeSecret.Data[tlsConfig.CaRef.SecretKey])

	return caCertificateFromSecret, nil
}

func (h *InfisicalPushSecretHandler) HandleCACertificate(ctx context.Context, infisicalPushSecret v1alpha1.InfisicalPushSecret, infisicalGlobalConfig *config.InfisicalGlobalConfig) error {
	if infisicalGlobalConfig.TLS != nil {
		caCert, err := h.getInfisicalCaCertificateFromKubeSecret(ctx, *infisicalGlobalConfig.TLS)
		if err != nil {
			return err
		}
		config.API_CA_CERTIFICATE = caCert
	} else if infisicalPushSecret.Spec.TLS.CaRef.SecretName != "" {
		caCert, err := h.getInfisicalCaCertificateFromKubeSecret(ctx, infisicalPushSecret.Spec.TLS)
		if err != nil {
			return err
		}
		config.API_CA_CERTIFICATE = caCert
	} else {
		config.API_CA_CERTIFICATE = ""
	}
	return nil
}

func (h *InfisicalPushSecretHandler) ReconcileInfisicalPushSecret(ctx context.Context, logger logr.Logger, infisicalPushSecret *v1alpha1.InfisicalPushSecret, resourceVariablesMap map[string]util.ResourceVariables) error {
	reconciler := &InfisicalPushSecretReconciler{
		Client:            h.Client,
		Scheme:            h.Scheme,
		IsNamespaceScoped: h.IsNamespaceScoped,
	}
	return reconciler.ReconcileInfisicalPushSecret(ctx, logger, infisicalPushSecret, resourceVariablesMap)
}

func (h *InfisicalPushSecretHandler) DeleteManagedSecrets(ctx context.Context, logger logr.Logger, infisicalPushSecret *v1alpha1.InfisicalPushSecret, resourceVariablesMap map[string]util.ResourceVariables) error {
	reconciler := &InfisicalPushSecretReconciler{
		Client:            h.Client,
		Scheme:            h.Scheme,
		IsNamespaceScoped: h.IsNamespaceScoped,
	}
	return reconciler.DeleteManagedSecrets(ctx, logger, infisicalPushSecret, resourceVariablesMap)
}

func (h *InfisicalPushSecretHandler) SetReconcileStatusCondition(ctx context.Context, infisicalPushSecret *v1alpha1.InfisicalPushSecret, err error) {
	reconciler := &InfisicalPushSecretReconciler{
		Client:            h.Client,
		Scheme:            h.Scheme,
		IsNamespaceScoped: h.IsNamespaceScoped,
	}
	reconciler.SetReconcileStatusCondition(ctx, infisicalPushSecret, err)
}
