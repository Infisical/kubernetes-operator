package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	infisicalSdk "github.com/infisical/go-sdk"
)

type awsIamAuth struct{}

func NewAWSIamAuth() InfisicalAuthStrategy {
	return &awsIamAuth{}
}

func (a *awsIamAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth.Spec.AWSIam == nil {
		return fmt.Errorf("auth method is %q but .spec.aws-iam is not set", v1beta1.AWSIamAuth)
	}

	return nil
}

func (a *awsIamAuth) Authenticate(
	ctx context.Context,
	connection *model.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:       connection.Host,
		CaCertificate: connection.CaCertificate,
	})

	cred, err := sdkClient.Auth().AwsIamAuthLogin(auth.Spec.AWSIam.IdentityID)
	if err != nil {
		return nil, fmt.Errorf("Unable to authenticate with AWS IAM: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
