package infisicalstaticsecretpush

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
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
		// The IDs of the secrets pushed before ctx ended must still be recorded, or the
		// next sync wouldn't recognize them as pushed by this resource.
		if statusErr := h.reconciler.patchStatus(context.WithoutCancel(ctx), push, original); statusErr != nil {
			err = errors.Join(err, statusErr)
		}
	}()

	if err := h.reconciler.Validate(push); err != nil {
		return 0, fmt.Errorf("validation failed: %w", err)
	}

	session, err := h.reconciler.Authenticate(ctx, push)
	if err != nil {
		return 0, fmt.Errorf("unable to authenticate: %w", err)
	}

	desired, err := h.reconciler.ReadSources(ctx, push)
	if err != nil {
		return 0, err
	}

	restClient, err := h.reconciler.NewAPIClient(ctx, session)
	if err != nil {
		return 0, err
	}
	defer restClient.GetClient().CloseIdleConnections()

	if err := h.reconciler.PushToTargets(ctx, restClient, push, desired); err != nil {
		h.reconciler.InvalidateOnUnauthorized(session, err)
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

	// Deleting can take minutes when Infisical is slow or unreachable, so show it on the
	// status before starting instead of only once it has finished.
	original := push.DeepCopy()
	setDeletingCondition(push)
	if err := h.reconciler.patchStatus(ctx, push, original); err != nil {
		return err
	}

	original = push.DeepCopy()
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
		return fmt.Errorf("unable to authenticate: %w", err)
	}

	restClient, err := h.reconciler.NewAPIClient(ctx, session)
	if err != nil {
		return err
	}
	defer restClient.GetClient().CloseIdleConnections()

	if err := h.reconciler.DeleteManagedSecrets(ctx, restClient, push); err != nil {
		h.reconciler.InvalidateOnUnauthorized(session, err)
		return err
	}

	return nil
}
