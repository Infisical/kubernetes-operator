package infisicalauth

import (
	"context"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalsession"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AutoReloadAnnotation       = "secrets.infisical.com/auto-reload"
	ManagedSecretAnnotationFmt = "secrets.infisical.com/managed-secret.%s"
)

type InfisicalAuthReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
}

func (r *InfisicalAuthReconciler) ValidateAndAuthenticate(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth) error {
	if infisicalAuth == nil {
		return model.ErrInvalidAuthObject
	}

	conn, err := infisicalsession.GetInfisicalConnection(ctx, r.Client, r.IsNamespaceScoped, infisicalAuth.Spec.InfisicalConnectionRef)
	if err != nil {
		return err
	}

	if err := r.authResolver.Validate(ctx, infisicalAuth); err != nil {
		return err
	}

	_, err = r.authResolver.Authenticate(ctx, conn, infisicalAuth)
	return err
}
