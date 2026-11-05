package infisicalstaticsecretpush

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/go-resty/resty/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalsession"
)

func NewInfisicalStaticSecretPushHandler(
	client client.Client,
	isNamespaceScoped bool,
	authResolver *auth.AuthStrategyResolver,
	projectIDCache *cache.ResourceCache[string],
	logger logr.Logger,
) *InfisicalStaticSecretPushHandler {
	return &InfisicalStaticSecretPushHandler{
		reconciler: &InfisicalStaticSecretPushReconciler{
			Client:            client,
			IsNamespaceScoped: isNamespaceScoped,
			session:           infisicalsession.NewResolver(client, isNamespaceScoped, authResolver, projectIDCache),
			logger:            logger,
		},
	}
}

type InfisicalStaticSecretPushHandler struct {
	reconciler *InfisicalStaticSecretPushReconciler
}

// SyncSecrets pushes the source Secrets to every target, deletes what is no longer
// pushed, and records the outcome on the status. It returns the number of keys read
// from the source Secrets, whether or not they had to be written to Infisical.
func (h *InfisicalStaticSecretPushHandler) SyncSecrets(ctx context.Context, push *v1beta1.InfisicalStaticSecretPush) (sourceKeys int, err error) {
	if push == nil {
		return 0, model.ErrInvalidStaticSecretPushObject
	}

	original := push.DeepCopy()
	defer func() {
		setReconcileStatusCondition(push, err)
		if err == nil {
			setLastSuccessfulReconcileAtCondition(push)
		}
		if statusErr := h.reconciler.patchStatus(ctx, push, original); statusErr != nil {
			err = errors.Join(err, statusErr)
		}
	}()

	if err := h.reconciler.Validate(push); err != nil {
		return 0, fmt.Errorf("validation failed: %w", err)
	}

	session, err := h.reconciler.Authenticate(ctx, push)
	if err != nil {
		return 0, fmt.Errorf("Unable to authenticate: %w", err)
	}

	desired, err := h.reconciler.ReadSources(ctx, push)
	if err != nil {
		return 0, err
	}

	restClient, err := h.newAPIClient(ctx, session)
	if err != nil {
		return 0, err
	}

	if err := h.reconciler.PushToTargets(restClient, session, push, desired); err != nil {
		return 0, err
	}

	return len(desired), nil
}

// DeleteManagedSecrets deletes every secret this resource pushed. Secrets it couldn't
// delete stay recorded on the status, so a retry only repeats what failed. Requests to
// Infisical stop when ctx is done, and the status is still updated after that.
func (h *InfisicalStaticSecretPushHandler) DeleteManagedSecrets(ctx context.Context, push *v1beta1.InfisicalStaticSecretPush) (err error) {
	if push == nil {
		return model.ErrInvalidStaticSecretPushObject
	}

	if len(push.Status.ManagedSecrets) == 0 {
		return nil
	}

	original := push.DeepCopy()
	defer func() {
		if err != nil {
			setReconcileStatusCondition(push, fmt.Errorf("unable to delete pushed secrets: %w", err))
		}
		// The secrets deleted before ctx ended must still be removed from the status.
		if statusErr := h.reconciler.patchStatus(context.WithoutCancel(ctx), push, original); statusErr != nil {
			err = errors.Join(err, statusErr)
		}
	}()

	session, err := h.reconciler.Authenticate(ctx, push)
	if err != nil {
		return fmt.Errorf("Unable to authenticate: %w", err)
	}

	restClient, err := h.newAPIClient(ctx, session)
	if err != nil {
		return err
	}

	if err := h.reconciler.DeleteManagedSecrets(restClient, push); err != nil {
		h.reconciler.session.InvalidateOnUnauthorized(session, err)
		return err
	}

	return nil
}

// newAPIClient returns an Infisical API client whose requests are cancelled when ctx is
// done. The client only waits a bounded time for response headers, so without ctx a
// stalled response body could block a reconcile indefinitely.
func (h *InfisicalStaticSecretPushHandler) newAPIClient(ctx context.Context, session infisicalsession.Session) (*resty.Client, error) {
	restClient, err := h.reconciler.session.NewAPIClient(ctx, session)
	if err != nil {
		return nil, err
	}

	restClient.OnBeforeRequest(func(_ *resty.Client, request *resty.Request) error {
		request.SetContext(ctx)
		return nil
	})
	return restClient, nil
}
