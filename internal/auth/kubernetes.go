package auth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	infisicalSdk "github.com/infisical/go-sdk"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type kubernetesAuth struct {
	client          client.Client
	namespaceScoped bool
}

func NewKubernetesAuth(c client.Client, namespaceScoped bool) InfisicalAuthStrategy {
	return &kubernetesAuth{client: c, namespaceScoped: namespaceScoped}
}

func (k *kubernetesAuth) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	if auth == nil {
		return model.ErrInvalidAuthObject
	}

	k8s := auth.Spec.Kubernetes
	if k8s == nil {
		return fmt.Errorf("auth method is %q but .spec.kubernetes is not set", v1beta1.KubernetesAuth)
	}

	if k8s.ServiceAccountRef.Name == "" || k8s.ServiceAccountRef.Namespace == "" {
		return fmt.Errorf(`"serviceAccountRef.name" and "serviceAccountRef.namespace" are required when "autoCreateServiceAccountToken" is enabled`)
	}

	return nil
}

func (k *kubernetesAuth) Authenticate(
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

	if auth.Spec.Kubernetes == nil {
		return nil, fmt.Errorf("%w: spec.kubernetes is nil", model.ErrInvalidAuthObject)
	}

	identityID, err := util.ResolveSecretReference(ctx, k.client, auth.Spec.Kubernetes.IdentityIDRef, ".spec.kubernetes.identityIdRef")
	if err != nil {
		return nil, err
	}

	serviceAccount, err := k.getServiceAccount(ctx, auth)
	if err != nil {
		return nil, err
	}

	saToken, err := k.getServiceAccountToken(ctx, auth, serviceAccount)
	if err != nil {
		return nil, err
	}

	sdkClient := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
		SiteUrl:          connection.Host,
		CaCertificate:    connection.CaCertificate,
		AutoTokenRefresh: false,
	})

	cred, err := sdkClient.Auth().KubernetesRawServiceAccountTokenLogin(string(identityID), saToken)
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate with Kubernetes: %w", err)
	}

	return &model.AuthenticationResult{MachineIdentity: cred, SdkClient: sdkClient}, nil
}

func (k *kubernetesAuth) getServiceAccount(ctx context.Context, auth *v1beta1.InfisicalAuth) (*corev1.ServiceAccount, error) {
	k8s := auth.Spec.Kubernetes
	key := client.ObjectKey{
		Namespace: k8s.ServiceAccountRef.Namespace,
		Name:      k8s.ServiceAccountRef.Name,
	}

	sa := &corev1.ServiceAccount{}
	if err := k.client.Get(ctx, key, sa); err != nil {
		if util.IsNamespaceScopedError(err, k.namespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "service account")
		}
		return nil, fmt.Errorf("unable to get service account: %w", err)
	}

	return sa, nil
}

func (k *kubernetesAuth) getServiceAccountToken(ctx context.Context, auth *v1beta1.InfisicalAuth, serviceAccount *corev1.ServiceAccount) (string, error) {
	k8s := auth.Spec.Kubernetes

	restClient, err := util.GetRestClientFromClient()
	if err != nil {
		return "", fmt.Errorf("unable to get REST client: %w", err)
	}

	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: ptr.To[int64](600),
		},
	}

	if len(k8s.ServiceAccountTokenAudiences) > 0 {
		tokenRequest.Spec.Audiences = k8s.ServiceAccountTokenAudiences
	}

	result := &authenticationv1.TokenRequest{}
	err = restClient.
		Post().
		Namespace(serviceAccount.Namespace).
		Resource("serviceaccounts").
		Name(serviceAccount.Name).
		SubResource("token").
		Body(tokenRequest).
		Do(ctx).
		Into(result)
	if err != nil {
		return "", fmt.Errorf("unable to create service account token: %w", err)
	}

	return result.Status.Token, nil
}
