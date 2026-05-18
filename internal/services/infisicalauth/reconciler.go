package infisicalauth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InfisicalAuthReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
}

func (r *InfisicalAuthReconciler) getInfisicalConnection(ctx context.Context, connectionRef v1beta1.NamespacedName) (*v1beta1.InfisicalConnection, error) {
	conn := v1beta1.InfisicalConnection{}

	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      connectionRef.Name,
		Namespace: connectionRef.Namespace,
	}, &conn)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "InfisicalConnection")
		}
		return nil, fmt.Errorf("Unable to fetch Infisical Connection CRD from cluster: %w", err)
	}

	readyCond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return nil, fmt.Errorf("InfisicalConnection is not ready")
	}

	return &conn, nil
}

func (r *InfisicalAuthReconciler) ValidateAndAuthenticate(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth) error {
	if infisicalAuth == nil {
		return model.ErrInvalidAuthObject
	}

	conn, err := r.getInfisicalConnection(ctx, infisicalAuth.Spec.InfisicalConnectionRef)
	if err != nil {
		return err
	}

	if err := r.authResolver.Validate(ctx, infisicalAuth); err != nil {
		return err
	}

	_, err = r.authResolver.Authenticate(ctx, conn, infisicalAuth)
	return err
}
