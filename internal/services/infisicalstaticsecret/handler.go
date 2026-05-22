package infisicalstaticsecret

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewInfisicalStaticSecretHandler(
	client client.Client,
	scheme *runtime.Scheme,
	isNamespaceScoped bool,
	authResolver *auth.AuthStrategyResolver,
	logger logr.Logger,
) *InfisicalAuthHandler {
	return &InfisicalAuthHandler{
		Client:            client,
		Scheme:            scheme,
		IsNamespaceScoped: isNamespaceScoped,
		authResolver:      authResolver,
		reconciler: &InfisicalStaticSecretReconciler{
			Client:            client,
			Scheme:            scheme,
			IsNamespaceScoped: isNamespaceScoped,
			authResolver:      authResolver,
			logger:            logger,
		},
	}
}

type InfisicalAuthHandler struct {
	client.Client
	Scheme            *runtime.Scheme
	Random            *rand.Rand
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
	reconciler        *InfisicalStaticSecretReconciler
}

func (h *InfisicalAuthHandler) SyncSecrets(ctx context.Context, infisicalStaticSecret *v1beta1.InfisicalStaticSecret) error {
	authenticationResult, err := h.reconciler.Authenticate(ctx, infisicalStaticSecret)
	if err != nil {
		return fmt.Errorf("Unable to authenticate: %w", err)
	}

	secrets, importedSecrets, err := h.reconciler.ListSecretsFromSources(ctx, infisicalStaticSecret, authenticationResult)
	if err != nil {
		return fmt.Errorf("unable to fetch secrets: %w", err)
	}

	mergedSecrets := h.reconciler.MergeSecretSources(secrets, importedSecrets)

	for _, target := range infisicalStaticSecret.Spec.Targets {
		err = h.syncTargetSecrets(ctx, infisicalStaticSecret, mergedSecrets, target)
		if err != nil {
			return fmt.Errorf("unable to sync target %q: %w", target.Name, err)
		}
	}

	return nil
}

func (h *InfisicalAuthHandler) syncTargetSecrets(ctx context.Context, owner *v1beta1.InfisicalStaticSecret, secrets []api.Secret, target v1beta1.SecretTarget) error {
	content, err := h.reconciler.RenderTargetOutput(secrets, target)
	if err != nil {
		return fmt.Errorf("failed to render target output: %w", err)
	}

	var targetResourceChanged = false
	if target.Kind == v1beta1.SecretTargetKindSecret {
		targetResourceChanged, err = h.reconciler.SyncKubeSecret(ctx, owner, content, target)
		if err != nil {
			return fmt.Errorf("failed to sync Secret %q: %w", target.Name, err)
		}
	}

	if target.Kind == v1beta1.SecretTargetKindConfigMap {
		targetResourceChanged, err = h.reconciler.SyncKubeConfigMap(ctx, owner, content, target)
		if err != nil {
			return fmt.Errorf("failed to sync ConfigMap %q: %w", target.Name, err)
		}
	}

	if targetResourceChanged {
		err = h.reconciler.PropagateSecretToWorkloads(ctx, target)
		if err != nil {
			return fmt.Errorf("failed to reconcile dependent resources of target %q: %w", target.Name, err)
		}
	}

	return nil
}
