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

type ldapAuth struct {
	client client.Client
}

func NewLDAPAuth(c client.Client) InfisicalAuthStrategy {
	return &ldapAuth{client: c}
}

func (l *ldapAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	if auth.Spec.LDAP == nil {
		return fmt.Errorf("auth method is %q but .spec.ldap is not set", v1beta1.LDAPAuth)
	}

	if _, err := util.ResolveSecretReference(ctx, l.client, auth.Spec.LDAP.UsernameRef, ".spec.ldap.usernameRef"); err != nil {
		return err
	}
	if _, err := util.ResolveSecretReference(ctx, l.client, auth.Spec.LDAP.PasswordRef, ".spec.ldap.passwordRef"); err != nil {
		return err
	}
	if _, err := util.ResolveSecretReference(ctx, l.client, auth.Spec.LDAP.IdentityIDRef, ".spec.ldap.identityIdRef"); err != nil {
		return err
	}

	return nil
}

func (l *ldapAuth) Authenticate(
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

	if auth.Spec.LDAP == nil {
		return nil, fmt.Errorf("%w: spec.ldap is nil", model.ErrInvalidAuthObject)
	}

	username, err := util.ResolveSecretReference(ctx, l.client, auth.Spec.LDAP.UsernameRef, ".spec.ldap.usernameRef")
	if err != nil {
		return nil, err
	}

	password, err := util.ResolveSecretReference(ctx, l.client, auth.Spec.LDAP.PasswordRef, ".spec.ldap.passwordRef")
	if err != nil {
		return nil, err
	}

	identityID, err := util.ResolveSecretReference(ctx, l.client, auth.Spec.LDAP.IdentityIDRef, ".spec.ldap.identityIdRef")
	if err != nil {
		return nil, err
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: infisicalSdk.BoolPtr(false),
	})

	cred, err := sdkClient.Auth().LdapAuthLogin(string(identityID), string(username), string(password))
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with LDAP: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
