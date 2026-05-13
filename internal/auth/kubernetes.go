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
		return ErrInvalidAuthObject
	}

	k8s := auth.Spec.Kubernetes
	if k8s == nil {
		return fmt.Errorf("auth method is %q but .spec.kubernetes is not set", v1beta1.KubernetesAuth)
	}

	if !k8s.AutoCreateServiceAccountToken {
		// If we are not going to create it, the secret must exist
		if _, err := util.ResolveSecretReference(ctx, k.client, k8s.IdentityIDRef, ".spec.kubernetes.identityIdRef"); err != nil {
			return err
		}
	} else {
		// But here the SA doesn't exist, we are going to create it, so we just need to ensure
		// name and namespace are set.
		if k8s.ServiceAccountRef.Name == "" || k8s.ServiceAccountRef.Namespace == "" {
			return fmt.Errorf(`"serviceAccountRef.name" and "serviceAccountRef.namespace" are required when "autoCreateServiceAccountToken" is enabled`)
		}
	}

	return nil
}

func (k *kubernetesAuth) Authenticate(
	ctx context.Context,
	connection *model.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	if auth == nil {
		return nil, ErrInvalidAuthObject
	}

	identityID, err := util.ResolveSecretReference(ctx, k.client, auth.Spec.Kubernetes.IdentityIDRef, ".spec.kubernetes.identityIdRef")
	if err != nil {
		return nil, err
	}

	saToken, err := k.getServiceAccount(ctx, auth)
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

func (k *kubernetesAuth) getServiceAccount(ctx context.Context, auth *v1beta1.InfisicalAuth) (string, error) {
	k8s := auth.Spec.Kubernetes

	if k8s.AutoCreateServiceAccountToken {
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

		saRef := auth.Spec.Kubernetes.ServiceAccountRef

		result := &authenticationv1.TokenRequest{}
		err = restClient.
			Post().
			Namespace(saRef.Namespace).
			Resource("serviceaccounts").
			Name(saRef.Name).
			SubResource("token").
			Body(tokenRequest).
			Do(ctx).
			Into(result)
		if err != nil {
			return "", fmt.Errorf("unable to create service account token: %w", err)
		}

		return result.Status.Token, nil
	}

	serviceAccount := &corev1.ServiceAccount{}
	err := k.client.Get(ctx, client.ObjectKey{Name: k8s.ServiceAccountRef.Name, Namespace: k8s.ServiceAccountRef.Namespace}, serviceAccount)
	if err != nil {
		if util.IsNamespaceScopedError(err, k.namespaceScoped) {
			return "", model.NewNamespaceScopedError(err, "service account")
		}
		return "", err
	}

	if len(serviceAccount.Secrets) == 0 {
		return "", fmt.Errorf("no secrets found for service account %s", k8s.ServiceAccountRef.Name)
	}

	secretName := serviceAccount.Secrets[0].Name

	secret := &corev1.Secret{}
	err = k.client.Get(ctx, client.ObjectKey{Name: secretName, Namespace: k8s.ServiceAccountRef.Namespace}, secret)
	if err != nil {
		if util.IsNamespaceScopedError(err, k.namespaceScoped) {
			return "", model.NewNamespaceScopedError(err, "service account token secret")
		}
		return "", err
	}

	token := secret.Data["token"]

	return string(token), nil

}
