package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	infisicalSdk "github.com/infisical/go-sdk"
)

type gcpIdTokenAuth struct{}

func NewGCPIdTokenAuth() InfisicalAuthStrategy {
	return &gcpIdTokenAuth{}
}

func (g *gcpIdTokenAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth.Spec.GCPIdToken == nil {
		return fmt.Errorf("auth method is %q but .spec.gcp-id-token is not set", v1beta1.GCPIdTokenAuth)
	}

	return nil
}

func (g *gcpIdTokenAuth) Authenticate(
	ctx context.Context,
	connection *model.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:       connection.Host,
		CaCertificate: connection.CaCertificate,
	})

	cred, err := sdkClient.Auth().GcpIdTokenAuthLogin(auth.Spec.GCPIdToken.IdentityID)
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with GCP ID Token: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
