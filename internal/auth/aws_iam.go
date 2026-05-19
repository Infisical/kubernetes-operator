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

type awsIamAuth struct {
	client client.Client
}

func NewAWSIamAuth(client client.Client) InfisicalAuthStrategy {
	return &awsIamAuth{
		client: client,
	}
}

func (a *awsIamAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	if auth.Spec.AWSIam == nil {
		return fmt.Errorf("auth method is %q but .spec.awsIam is not set", v1beta1.AWSIamAuth)
	}

	return nil
}

func (a *awsIamAuth) Authenticate(
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

	if auth.Spec.AWSIam == nil {
		return nil, fmt.Errorf("%w: spec.awsIam is nil", model.ErrInvalidAuthObject)
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: false,
	})

	identityID, err := util.ResolveSecretReference(ctx, a.client, auth.Spec.AWSIam.IdentityIDRef, ".spec.awsIam.identityIdRef")
	if err != nil {
		return nil, err
	}

	cred, err := sdkClient.Auth().AwsIamAuthLogin(string(identityID))
	if err != nil {
		return nil, fmt.Errorf("Unable to authenticate with AWS IAM: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
