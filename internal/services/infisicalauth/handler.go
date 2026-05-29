package infisicalauth

import (
	"context"
	"math/rand/v2"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewInfisicalAuthHandler(
	client client.Client,
	scheme *runtime.Scheme,
	isNamespaceScoped bool,
	authResolver *auth.AuthStrategyResolver,
) *InfisicalAuthHandler {
	return &InfisicalAuthHandler{
		Client:            client,
		Scheme:            scheme,
		IsNamespaceScoped: isNamespaceScoped,
		reconciler: &InfisicalAuthReconciler{
			Client:            client,
			Scheme:            scheme,
			IsNamespaceScoped: isNamespaceScoped,
			authResolver:      authResolver,
		},
	}
}

type InfisicalAuthHandler struct {
	client.Client
	Scheme            *runtime.Scheme
	Random            *rand.Rand
	IsNamespaceScoped bool
	reconciler        *InfisicalAuthReconciler
}

func (h *InfisicalAuthHandler) ValidateAndAuthenticate(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth) error {
	return h.reconciler.ValidateAndAuthenticate(ctx, logger, infisicalAuth)
}

func (h *InfisicalAuthHandler) SetReconcileConditionStatus(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth, errorToConditionOn error) {
	h.reconciler.SetReconcileConditionStatus(ctx, logger, infisicalAuth, errorToConditionOn)
}
