package util

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	infisical "github.com/infisical/go-sdk"
)

type DecodedSymmetricEncryptionDetails = struct {
	Cipher []byte
	IV     []byte
	Tag    []byte
	Key    []byte
}

type SecretsResult struct {
	Secrets     []model.SingleEnvironmentVariable
	ETag        string
	NotModified bool
}

func VerifyServiceToken(serviceToken string) (string, error) {
	serviceTokenParts := strings.SplitN(serviceToken, ".", 4)
	if len(serviceTokenParts) < 4 {
		return "", fmt.Errorf("invalid service token entered. Please double check your service token and try again")
	}

	serviceToken = fmt.Sprintf("%v.%v.%v", serviceTokenParts[0], serviceTokenParts[1], serviceTokenParts[2])
	return serviceToken, nil
}

func GetServiceTokenDetails(infisicalToken string) (api.GetServiceTokenDetailsResponse, error) {
	serviceTokenParts := strings.SplitN(infisicalToken, ".", 4)
	if len(serviceTokenParts) < 4 {
		return api.GetServiceTokenDetailsResponse{}, fmt.Errorf("invalid service token entered. Please double check your service token and try again")
	}

	serviceToken := fmt.Sprintf("%v.%v.%v", serviceTokenParts[0], serviceTokenParts[1], serviceTokenParts[2])

	httpClient, err := CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: serviceToken,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})
	if err != nil {
		return api.GetServiceTokenDetailsResponse{}, fmt.Errorf("unable to create resty client. [err=%v]", err)
	}

	serviceTokenDetails, err := api.CallGetServiceTokenDetailsV2(httpClient)
	if err != nil {
		return api.GetServiceTokenDetailsResponse{}, fmt.Errorf("unable to get service token details. [err=%v]", err)
	}

	return serviceTokenDetails, nil
}

func GetPlainTextSecretsViaMachineIdentity(infisicalClient infisical.InfisicalClientInterface, secretScope v1alpha1.MachineIdentityScopeInWorkspace, ifNoneMatch string) (SecretsResult, error) {

	var environmentVariables []model.SingleEnvironmentVariable

	if secretScope.SecretName == "" {

		result, err := infisicalClient.Secrets().ListSecrets(infisical.ListSecretsOptions{
			ProjectID:              secretScope.ProjectID,
			Environment:            secretScope.EnvSlug,
			Recursive:              secretScope.Recursive,
			SecretPath:             secretScope.SecretsPath,
			IncludeImports:         true,
			ExpandSecretReferences: true,
			IfNoneMatch:            ifNoneMatch,
		})

		if err != nil {
			var notModifiedErr *infisical.NotModifiedError
			if errors.As(err, &notModifiedErr) {
				return SecretsResult{NotModified: true, ETag: ifNoneMatch}, nil
			}
			return SecretsResult{}, fmt.Errorf("unable to get secrets. [err=%v]", err)
		}

		for _, secret := range result.Secrets {

			environmentVariables = append(environmentVariables, model.SingleEnvironmentVariable{
				Key:        secret.SecretKey,
				Value:      secret.SecretValue,
				Type:       secret.Type,
				ID:         secret.ID,
				SecretPath: secret.SecretPath,
			})
		}

		return SecretsResult{Secrets: environmentVariables, ETag: result.ETag}, nil
	} else {
		// Note: ETag caching is not supported for single-secret retrieval because the
		// go-sdk's RetrieveSecretOptions does not accept an IfNoneMatch parameter.
		// Every reconcile for a single-secret config will make a full API round-trip.
		secret, err := infisicalClient.Secrets().Retrieve(infisical.RetrieveSecretOptions{
			SecretKey:              secretScope.SecretName,
			ProjectID:              secretScope.ProjectID,
			Environment:            secretScope.EnvSlug,
			SecretPath:             secretScope.SecretsPath,
			IncludeImports:         true,
			ExpandSecretReferences: true,
		})

		if err != nil {
			return SecretsResult{}, fmt.Errorf("unable to get single secret [secretName=%s]. [err=%v]", secretScope.SecretName, err)
		}

		environmentVariables = append(environmentVariables, model.SingleEnvironmentVariable{
			Key:        secret.SecretKey,
			Value:      secret.SecretValue,
			Type:       secret.Type,
			ID:         secret.ID,
			SecretPath: secret.SecretPath,
		})
	}

	return SecretsResult{Secrets: environmentVariables}, nil
}

func GetPlainTextSecretsViaServiceToken(infisicalClient infisical.InfisicalClientInterface, fullServiceToken string, envSlug string, secretPath string, recursive bool, ifNoneMatch string) (SecretsResult, error) {
	serviceTokenParts := strings.SplitN(fullServiceToken, ".", 4)
	if len(serviceTokenParts) < 4 {
		return SecretsResult{}, fmt.Errorf("invalid service token entered. Please double check your service token and try again")
	}

	serviceToken := fmt.Sprintf("%v.%v.%v", serviceTokenParts[0], serviceTokenParts[1], serviceTokenParts[2])

	httpClient, err := CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: serviceToken,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})

	if err != nil {
		return SecretsResult{}, fmt.Errorf("unable to create resty client. [err=%v]", err)
	}

	serviceTokenDetails, err := api.CallGetServiceTokenDetailsV2(httpClient)
	if err != nil {
		return SecretsResult{}, fmt.Errorf("unable to get service token details. [err=%v]", err)
	}

	result, err := infisicalClient.Secrets().ListSecrets(infisical.ListSecretsOptions{
		ProjectID:              serviceTokenDetails.Workspace,
		Environment:            envSlug,
		Recursive:              recursive,
		SecretPath:             secretPath,
		IncludeImports:         true,
		ExpandSecretReferences: true,
		IfNoneMatch:            ifNoneMatch,
	})

	if err != nil {
		var notModifiedErr *infisical.NotModifiedError
		if errors.As(err, &notModifiedErr) {
			return SecretsResult{NotModified: true, ETag: ifNoneMatch}, nil
		}
		return SecretsResult{}, err
	}

	var environmentVariables []model.SingleEnvironmentVariable

	for _, secret := range result.Secrets {

		environmentVariables = append(environmentVariables, model.SingleEnvironmentVariable{
			Key:        secret.SecretKey,
			Value:      secret.SecretValue,
			Type:       secret.Type,
			ID:         secret.ID,
			SecretPath: secret.SecretPath,
		})
	}

	return SecretsResult{Secrets: environmentVariables, ETag: result.ETag}, nil

}

// Fetches plaintext secrets from an API endpoint using a service account.
// The function fetches the service account details and keys, decrypts the workspace key, fetches the encrypted secrets for the specified project and environment, and decrypts the secrets using the decrypted workspace key.
// Returns the plaintext secrets, encrypted secrets response, and any errors that occurred during the process.
func GetPlainTextSecretsViaServiceAccount(infisicalClient infisical.InfisicalClientInterface, serviceAccountCreds model.ServiceAccountDetails, projectId string, environmentName string, ifNoneMatch string) (SecretsResult, error) {

	httpClient, err := CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: serviceAccountCreds.AccessKey,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})
	if err != nil {
		return SecretsResult{}, fmt.Errorf("unable to create resty client. [err=%v]", err)
	}

	serviceAccountDetails, err := api.CallGetServiceTokenAccountDetailsV2(httpClient)
	if err != nil {
		return SecretsResult{}, fmt.Errorf("GetPlainTextSecretsViaServiceAccount: unable to get service account details. [err=%v]", err)
	}

	serviceAccountKeys, err := api.CallGetServiceAccountKeysV2(httpClient, api.GetServiceAccountKeysRequest{ServiceAccountId: serviceAccountDetails.ServiceAccount.ID})
	if err != nil {
		return SecretsResult{}, fmt.Errorf("GetPlainTextSecretsViaServiceAccount: unable to get service account key details. [err=%v]", err)
	}

	// find key for requested project
	var workspaceServiceAccountKey api.ServiceAccountKey
	for _, serviceAccountKey := range serviceAccountKeys.ServiceAccountKeys {
		if serviceAccountKey.Workspace == projectId {
			workspaceServiceAccountKey = serviceAccountKey
		}
	}

	if workspaceServiceAccountKey.ID == "" || workspaceServiceAccountKey.EncryptedKey == "" || workspaceServiceAccountKey.Nonce == "" || serviceAccountCreds.PublicKey == "" || serviceAccountCreds.PrivateKey == "" {
		return SecretsResult{}, fmt.Errorf("unable to find key for [projectId=%s] [err=%v]. Ensure that the given service account has access to given projectId", projectId, err)
	}

	result, err := infisicalClient.Secrets().ListSecrets(infisical.ListSecretsOptions{
		ProjectID:              projectId,
		Environment:            environmentName,
		Recursive:              false,
		SecretPath:             "/",
		IncludeImports:         true,
		ExpandSecretReferences: true,
		IfNoneMatch:            ifNoneMatch,
	})

	if err != nil {
		var notModifiedErr *infisical.NotModifiedError
		if errors.As(err, &notModifiedErr) {
			return SecretsResult{NotModified: true, ETag: ifNoneMatch}, nil
		}
		return SecretsResult{}, err
	}

	var environmentVariables []model.SingleEnvironmentVariable

	for _, secret := range result.Secrets {
		environmentVariables = append(environmentVariables, model.SingleEnvironmentVariable{
			Key:        secret.SecretKey,
			Value:      secret.SecretValue,
			Type:       secret.Type,
			ID:         secret.ID,
			SecretPath: secret.SecretPath,
		})
	}

	return SecretsResult{Secrets: environmentVariables, ETag: result.ETag}, nil
}
