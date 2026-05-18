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

type gcpIdTokenAuth struct {
	client client.Client
}

func NewGCPIdTokenAuth(client client.Client) InfisicalAuthStrategy {
	return &gcpIdTokenAuth{
		client: client,
	}
}

func (g *gcpIdTokenAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	if auth.Spec.GCPIdToken == nil {
		return fmt.Errorf("auth method is %q but .spec.gcpIdToken is not set", v1beta1.GCPIdTokenAuth)
	}

	return nil
}

func (g *gcpIdTokenAuth) Authenticate(
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

	if auth.Spec.GCPIdToken == nil {
		return nil, fmt.Errorf("%w: spec.gcpIdToken is nil", model.ErrInvalidAuthObject)
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: false,
	})

	identityID, err := util.ResolveSecretReference(ctx, g.client, auth.Spec.GCPIdToken.IdentityIDRef, ".spec.gcpIdToken.identityIdRef")
	if err != nil {
		return nil, err
	}

	cred, err := sdkClient.Auth().GcpIdTokenAuthLogin(string(identityID))
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with GCP ID Token: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
