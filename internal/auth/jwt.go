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

type jwtAuth struct {
	client client.Client
}

func NewJWTAuth(c client.Client) InfisicalAuthStrategy {
	return &jwtAuth{client: c}
}

func (j *jwtAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	if auth.Spec.JWT == nil {
		return fmt.Errorf("auth method is %q but .spec.jwt is not set", v1beta1.JWTAuth)
	}

	if _, err := util.ResolveSecretReference(ctx, j.client, auth.Spec.JWT.IdentityIDRef, ".spec.jwt.identityIdRef"); err != nil {
		return err
	}
	if _, err := util.ResolveSecretReference(ctx, j.client, auth.Spec.JWT.JWTRef, ".spec.jwt.jwtRef"); err != nil {
		return err
	}

	return nil
}

func (j *jwtAuth) Authenticate(
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

	if auth.Spec.JWT == nil {
		return nil, fmt.Errorf("%w: spec.jwt is nil", model.ErrInvalidAuthObject)
	}

	identityID, err := util.ResolveSecretReference(ctx, j.client, auth.Spec.JWT.IdentityIDRef, ".spec.jwt.identityIdRef")
	if err != nil {
		return nil, err
	}

	jwt, err := util.ResolveSecretReference(ctx, j.client, auth.Spec.JWT.JWTRef, ".spec.jwt.jwtRef")
	if err != nil {
		return nil, err
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: infisicalSdk.BoolPtr(false),
	})

	cred, err := sdkClient.Auth().JwtAuthLogin(string(identityID), string(jwt))
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with JWT Auth: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
