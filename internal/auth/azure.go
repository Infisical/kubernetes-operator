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

type azureAuth struct {
	client client.Client
}

func NewAzureAuth(client client.Client) InfisicalAuthStrategy {
	return &azureAuth{
		client: client,
	}
}

func (a *azureAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	if auth.Spec.Azure == nil {
		return fmt.Errorf("auth method is %q but .spec.azure is not set", v1beta1.AzureAuth)
	}

	return nil
}

func (a *azureAuth) Authenticate(
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

	if auth.Spec.Azure == nil {
		return nil, fmt.Errorf("%w: spec.azure is nil", model.ErrInvalidAuthObject)
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: false,
	})

	identityID, err := util.ResolveSecretReference(ctx, a.client, auth.Spec.Azure.IdentityIDRef, ".spec.azure.identityIdRef")
	if err != nil {
		return nil, err
	}

	cred, err := sdkClient.Auth().AzureAuthLogin(string(identityID), auth.Spec.Azure.Resource)
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with Azure: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
