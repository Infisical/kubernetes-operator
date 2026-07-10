package infisicalstaticsecret

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"path"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/Infisical/infisical/k8-operator/internal/util/sse"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func NewInfisicalStaticSecretHandler(
	client client.Client,
	scheme *runtime.Scheme,
	isNamespaceScoped bool,
	authResolver *auth.AuthStrategyResolver,
	resourceCache *cache.ResourceCache,
	logger logr.Logger,
) *InfisicalStaticSecretHandler {
	return &InfisicalStaticSecretHandler{
		Client:            client,
		Scheme:            scheme,
		IsNamespaceScoped: isNamespaceScoped,
		authResolver:      authResolver,
		resourceCache:     resourceCache,
		logger:            logger,
		reconciler: &InfisicalStaticSecretReconciler{
			Client:            client,
			Scheme:            scheme,
			IsNamespaceScoped: isNamespaceScoped,
			authResolver:      authResolver,
			resourceCache:     resourceCache,
			logger:            logger,
		},
	}
}

type InfisicalStaticSecretHandler struct {
	client.Client
	Scheme            *runtime.Scheme
	Random            *rand.Rand
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
	resourceCache     *cache.ResourceCache
	reconciler        *InfisicalStaticSecretReconciler
	logger            logr.Logger
}

func (h *InfisicalStaticSecretHandler) SyncSecrets(ctx context.Context, infisicalStaticSecret *v1beta1.InfisicalStaticSecret) (int, error) {
	defer h.reconciler.UpdateConditions(ctx, infisicalStaticSecret)

	if err := h.reconciler.Validate(infisicalStaticSecret); err != nil {
		setReconcileStatusCondition(infisicalStaticSecret, err)
		return 0, fmt.Errorf("validation failed: %w", err)
	}

	authenticationResult, err := h.reconciler.Authenticate(ctx, infisicalStaticSecret)
	if err != nil {
		setReconcileStatusCondition(infisicalStaticSecret, err)
		return 0, fmt.Errorf("Unable to authenticate: %w", err)
	}

	secrets, importedSecrets, err := h.reconciler.ListSecretsFromSources(ctx, infisicalStaticSecret, authenticationResult)
	if err != nil {
		setReconcileStatusCondition(infisicalStaticSecret, err)
		return 0, fmt.Errorf("unable to fetch secrets: %w", err)
	}

	mergedSecrets := h.reconciler.MergeSecretSources(secrets, importedSecrets)

	var totalAffectedWorkloads = 0
	for _, target := range infisicalStaticSecret.Spec.Targets {
		affectedWorkloads, err := h.syncTargetSecrets(ctx, infisicalStaticSecret, mergedSecrets, secrets, target)
		totalAffectedWorkloads += affectedWorkloads
		if err != nil {
			setReconcileStatusCondition(infisicalStaticSecret, err)
			return 0, fmt.Errorf("unable to sync target %q: %w", target.Name, err)
		}
	}

	setAutoRedeployReadyCondition(infisicalStaticSecret, totalAffectedWorkloads)
	setReconcileStatusCondition(infisicalStaticSecret, nil)
	setLastSuccessfulReconcileAtCondition(infisicalStaticSecret)

	return len(mergedSecrets), nil
}

func sourceSSEKey(source v1beta1.SecretSource) string {
	return path.Join(source.ProjectId, source.EnvironmentSlug, source.SecretPath)
}

func (h *InfisicalStaticSecretHandler) OpenInstantUpdatesStreams(
	ctx context.Context,
	infisicalStaticSecret *v1beta1.InfisicalStaticSecret,
	registries map[string]*sse.ConnectionRegistry,
	eventCh chan<- event.TypedGenericEvent[client.Object],
) (map[string]*sse.ConnectionRegistry, error) {
	if infisicalStaticSecret == nil {
		return registries, model.ErrInvalidStaticSecretObject
	}

	auth, err := h.reconciler.Authenticate(ctx, infisicalStaticSecret)
	if err != nil {
		return registries, fmt.Errorf("could not authenticate: %w", err)
	}

	token := auth.Credentials.MachineIdentity.AccessToken
	baseURL := util.AppendAPIEndpoint(auth.Connection.Address())

	if registries == nil {
		registries = make(map[string]*sse.ConnectionRegistry)
	}

	// Allows us to reference a CRD without using pointers and
	// prevent possible GC issues, especially because SSE might
	// hold this reference for a long time.
	crdRef := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Name:      infisicalStaticSecret.Name,
			Namespace: infisicalStaticSecret.Namespace,
		},
	}
	crdRef.SetGroupVersionKind(v1beta1.GroupVersion.WithKind("InfisicalStaticSecret"))

	activeKeys := make(map[string]struct{}, len(infisicalStaticSecret.Spec.Sources))

	for _, source := range infisicalStaticSecret.Spec.Sources {
		secretsPath := source.SecretPath
		if source.Recursive {
			secretsPath = path.Join(secretsPath, "**")
		}

		key := sourceSSEKey(source)
		activeKeys[key] = struct{}{}

		currentParams := sse.SubscriptionParams{
			ProjectID:   source.ProjectId,
			EnvSlug:     source.EnvironmentSlug,
			SecretsPath: secretsPath,
		}

		registry, exists := registries[key]
		if exists {
			if existingParams, ok := registry.GetParams(); ok {
				if existingParams.Equals(currentParams) && registry.IsConnected() {
					continue
				}
			}
		}

		if !exists {
			registry = sse.NewConnectionRegistry(
				func(ev sse.Event) {
					h.logger.Info("Received SSE event", "event", ev.Event, "source", key)
					eventCh <- event.TypedGenericEvent[client.Object]{
						Object: crdRef,
					}
				},
				func(err error) {
					h.logger.Error(err, "SSE error", "source", key)
				},
				func() {
					h.logger.Info("SSE max retries exceeded, triggering reconciliation", "source", key)
					eventCh <- event.TypedGenericEvent[client.Object]{
						Object: crdRef,
					}
				},
			)
			registries[key] = registry
		}

		err := registry.SubscribeWithParams(currentParams, func() (*http.Response, error) {
			caCertificate := ""
			if auth.Connection != nil && auth.Connection.Spec.TLS != nil && auth.Connection.Spec.TLS.CaCertificate != nil {
				certificateRef := auth.Connection.Spec.TLS.CaCertificate
				caContent, err := util.ResolveSecretReference(ctx, h.Client, *certificateRef, certificateRef.Key)
				if err != nil {
					return nil, fmt.Errorf("could not resolve InfisicalConnection TLS Certificate: %w", err)
				}

				caCertificate = string(caContent)
			}
			httpClient, err := util.CreateRestyClient(model.CreateRestyClientOptions{
				AccessToken: token,
				Headers: map[string]string{
					"Content-Type": "application/json",
					"Accept":       "text/event-stream",
					"Connection":   "keep-alive",
				},
				CaCertificate: caCertificate,
			})
			if err != nil {
				return nil, fmt.Errorf("unable to create resty client: %w", err)
			}

			return api.CallSubscribeProjectEventsWithBaseURL(httpClient, baseURL, currentParams.ProjectID, currentParams.SecretsPath, currentParams.EnvSlug)
		})

		if err != nil {
			h.logger.Error(err, "Failed to open SSE stream", "source", key)
			continue
		}

		h.logger.Info("SSE connection established", "source", key, "projectID", source.ProjectId, "envSlug", source.EnvironmentSlug, "secretsPath", secretsPath)
	}

	for key, registry := range registries {
		if _, active := activeKeys[key]; !active {
			registry.Close()
			delete(registries, key)
			h.logger.Info("Closed SSE connection for removed source", "source", key)
		}
	}

	return registries, nil
}

func CloseInstantUpdatesStreams(registries map[string]*sse.ConnectionRegistry) {
	for _, registry := range registries {
		registry.Close()
	}
}

func (h *InfisicalStaticSecretHandler) syncTargetSecrets(ctx context.Context, owner *v1beta1.InfisicalStaticSecret, mergedSecrets, rawSecrets []api.Secret, target v1beta1.SecretTarget) (int, error) {
	content, err := h.reconciler.RenderTargetOutput(mergedSecrets, rawSecrets, target)
	if err != nil {
		return 0, fmt.Errorf("failed to render target output: %w", err)
	}

	var targetResourceChanged bool
	var etag string
	if target.Kind == v1beta1.SecretTargetKindSecret {
		targetResourceChanged, etag, err = h.reconciler.SyncKubeSecret(ctx, owner, content, target)
		if err != nil {
			return 0, fmt.Errorf("failed to sync Secret %q: %w", target.Name, err)
		}
	} else if target.Kind == v1beta1.SecretTargetKindConfigMap {
		targetResourceChanged, etag, err = h.reconciler.SyncKubeConfigMap(ctx, owner, content, target)
		if err != nil {
			return 0, fmt.Errorf("failed to sync ConfigMap %q: %w", target.Name, err)
		}
	} else {
		return 0, fmt.Errorf("invalid target type %q", target.Kind)
	}

	if targetResourceChanged {
		affectedWorkloads, err := h.reconciler.PropagateSecretToWorkloads(ctx, target, etag)
		if err != nil {
			return affectedWorkloads, fmt.Errorf("failed to reconcile dependent resources of target %q: %w", target.Name, err)
		}

		return affectedWorkloads, nil
	}

	return 0, nil
}
