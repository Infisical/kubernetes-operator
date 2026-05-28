package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	infisicalSdk "github.com/infisical/go-sdk"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type universalAuth struct {
	client client.Client
}

func NewUniversalAuth(c client.Client) InfisicalAuthStrategy {
	return &universalAuth{client: c}
}

func (u *universalAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	if auth.Spec.Universal == nil {
		return fmt.Errorf("auth method is %q but .spec.universal is not set", v1beta1.UniversalAuth)
	}

	if _, err := util.ResolveSecretReference(ctx, u.client, auth.Spec.Universal.ClientIdRef, ".spec.universal.clientIdRef"); err != nil {
		return err
	}
	if _, err := util.ResolveSecretReference(ctx, u.client, auth.Spec.Universal.ClientSecretRef, ".spec.universal.clientSecretRef"); err != nil {
		return err
	}

	return nil
}

func (u *universalAuth) Authenticate(
	ctx context.Context,
	connection *model.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	if connection == nil {
		return nil, model.ErrInvalidConnectionObject
	}

	if auth == nil {
		return nil, model.ErrInvalidAuthObject
	}

	if auth.Spec.Universal == nil {
		return nil, fmt.Errorf("%w: spec.universal is nil", model.ErrInvalidAuthObject)
	}

	clientId, err := util.ResolveSecretReference(ctx, u.client, auth.Spec.Universal.ClientIdRef, ".spec.universal.clientIdRef")
	if err != nil {
		return nil, err
	}

	clientSecret, err := util.ResolveSecretReference(ctx, u.client, auth.Spec.Universal.ClientSecretRef, ".spec.universal.clientSecretRef")
	if err != nil {
		return nil, err
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: infisicalSdk.BoolPtr(false),
	})

	cred, err := sdkClient.Auth().UniversalAuthLogin(string(clientId), string(clientSecret))
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with Universal Auth: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
