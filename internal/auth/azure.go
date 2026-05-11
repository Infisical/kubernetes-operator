package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	infisicalSdk "github.com/infisical/go-sdk"
)

type azureAuth struct{}

func NewAzureAuth() InfisicalAuthStrategy {
	return &azureAuth{}
}

func (a *azureAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
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
	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:       connection.Host,
		CaCertificate: connection.CaCertificate,
	})

	cred, err := sdkClient.Auth().AzureAuthLogin(auth.Spec.Azure.IdentityID, "")
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with Azure: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
