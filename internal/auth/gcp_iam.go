package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	infisicalSdk "github.com/infisical/go-sdk"
)

type gcpIamAuth struct{}

func NewGCPIamAuth() InfisicalAuthStrategy {
	return &gcpIamAuth{}
}

func (g *gcpIamAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth.Spec.GCPIam == nil {
		return fmt.Errorf("auth method is %q but .spec.gcp-iam is not set", v1beta1.GCPIamAuth)
	}

	return nil
}

func (g *gcpIamAuth) Authenticate(
	ctx context.Context,
	connection *model.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:       connection.Host,
		CaCertificate: connection.CaCertificate,
	})

	cred, err := sdkClient.Auth().GcpIamAuthLogin(auth.Spec.GCPIam.IdentityID, auth.Spec.GCPIam.ServiceAccountKeyFilePath)
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with GCP IAM: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
