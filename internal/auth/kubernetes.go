package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	infisicalSdk "github.com/infisical/go-sdk"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type kubernetesAuth struct {
	client client.Client
}

func NewKubernetesAuth(c client.Client) InfisicalAuthStrategy {
	return &kubernetesAuth{client: c}
}

func (k *kubernetesAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth.Spec.Kubernetes == nil {
		return fmt.Errorf("auth method is %q but .spec.kubernetes is not set", v1beta1.KubernetesAuth)
	}

	return nil
}

func (k *kubernetesAuth) Authenticate(
	ctx context.Context,
	connection *model.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	restClient, err := util.GetRestClientFromClient()
	if err != nil {
		return nil, fmt.Errorf("unable to get REST client: %w", err)
	}

	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: ptr.To[int64](600),
		},
	}

	result := &authenticationv1.TokenRequest{}
	err = restClient.
		Post().
		Namespace(auth.Namespace).
		Resource("serviceaccounts").
		Name(auth.Spec.Kubernetes.ServiceAccountName).
		SubResource("token").
		Body(tokenRequest).
		Do(ctx).
		Into(result)
	if err != nil {
		return nil, fmt.Errorf("unable to create service account token: %w", err)
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:       connection.Host,
		CaCertificate: connection.CaCertificate,
	})

	cred, err := sdkClient.Auth().KubernetesRawServiceAccountTokenLogin(auth.Spec.Kubernetes.IdentityID, result.Status.Token)
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with Kubernetes: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}
