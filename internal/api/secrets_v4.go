package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

type ListSecretsRequest struct {
	ProjectId       string   `json:"workspaceId"`
	EnvironmentSlug string   `json:"environment"`
	SecretPath      string   `json:"secretPath"`
	Tags            []string `json:"tags,omitempty"`
	Recursive       bool     `json:"recursive,omitempty"`

	// SkipReferenceExpansion returns values exactly as stored, with references such as
	// ${KEY} left as written. It isn't a user option: no CRD exposes it. Only
	// InfisicalStaticSecretPush sets it, always, because it compares stored values with
	// the ones it pushes. InfisicalStaticSecret leaves it unset, so references are expanded.
	SkipReferenceExpansion bool `json:"-"`
}

type Secret struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	SecretKey   string `json:"secretKey"`
	Environment string `json:"environment"`
	SecretValue string `json:"secretValue"`
	SecretPath  string `json:"secretPath"`
	ProjectID   string `json:"workspace"`
}

type SecretImport struct {
	SecretPath  string   `json:"secretPath"`
	Environment string   `json:"environment"`
	FolderID    string   `json:"folderId"`
	Secrets     []Secret `json:"secrets"`
}

type ListSecretsResponse struct {
	Secrets []Secret       `json:"secrets"`
	Imports []SecretImport `json:"imports,omitempty"`
}

func ListSecrets(httpClient *resty.Client, request ListSecretsRequest) (ListSecretsResponse, error) {
	queryParams := map[string]string{
		"projectId":   request.ProjectId,
		"environment": request.EnvironmentSlug,
		"secretPath":  request.SecretPath,
		"recursive":   strconv.FormatBool(request.Recursive),
	}

	if len(request.Tags) > 0 {
		queryParams["tagSlugs"] = strings.Join(request.Tags, ",")
	}

	if request.SkipReferenceExpansion {
		queryParams["expandSecretReferences"] = "false"
	}

	var listSecretsResponse ListSecretsResponse
	response, err := httpClient.
		R().
		SetResult(&listSecretsResponse).
		SetQueryParams(queryParams).
		Get("/api/v4/secrets")
	if err != nil {
		return ListSecretsResponse{}, fmt.Errorf("failed to list secrets: %w", err)
	}

	if err := checkResponse(response, "list secrets"); err != nil {
		return ListSecretsResponse{}, err
	}

	return listSecretsResponse, nil
}

// SecretLocation identifies a folder in Infisical.
type SecretLocation struct {
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment"`
	SecretPath  string `json:"secretPath"`
}

type UpsertSecret struct {
	SecretKey   string `json:"secretKey"`
	SecretValue string `json:"secretValue"`
}

type UpsertSecretsRequest struct {
	SecretLocation
	Secrets []UpsertSecret `json:"secrets"`
}

// UpsertSecrets creates the given secrets in one folder, or updates them when they
// already exist, and returns the stored secrets. It returns ErrApprovalRequired when
// the change was submitted for approval instead of applied.
func UpsertSecrets(httpClient *resty.Client, request UpsertSecretsRequest) ([]Secret, error) {
	body := struct {
		UpsertSecretsRequest
		Mode string `json:"mode"`
	}{
		UpsertSecretsRequest: request,
		Mode:                 "upsert",
	}

	var result struct {
		Secrets  []Secret        `json:"secrets"`
		Approval json.RawMessage `json:"approval"`
	}
	response, err := httpClient.
		R().
		SetBody(body).
		SetResult(&result).
		Patch("/api/v4/secrets/batch")
	if err != nil {
		return nil, fmt.Errorf("failed to upsert secrets: %w", err)
	}

	if err := checkResponse(response, "upsert secrets"); err != nil {
		return nil, err
	}

	if isApproval(result.Approval) {
		return nil, ErrApprovalRequired
	}

	return result.Secrets, nil
}

type DeleteSecret struct {
	SecretKey string `json:"secretKey"`
}

type DeleteSecretsRequest struct {
	SecretLocation
	Secrets []DeleteSecret `json:"secrets"`
}

// DeleteSecrets deletes the given secrets from one folder. It returns
// ErrApprovalRequired when the change was submitted for approval instead of applied.
func DeleteSecrets(httpClient *resty.Client, request DeleteSecretsRequest) error {
	var result struct {
		Approval json.RawMessage `json:"approval"`
	}
	response, err := httpClient.
		R().
		SetBody(request).
		SetResult(&result).
		Delete("/api/v4/secrets/batch")
	if err != nil {
		return fmt.Errorf("failed to delete secrets: %w", err)
	}

	if err := checkResponse(response, "delete secrets"); err != nil {
		return err
	}

	if isApproval(result.Approval) {
		return ErrApprovalRequired
	}

	return nil
}

// GetSecretByID returns the secret with the given ID, including where it is stored.
// It returns an error wrapping ErrNotFound when the secret doesn't exist.
func GetSecretByID(httpClient *resty.Client, secretID string) (Secret, error) {
	var result struct {
		Secret Secret `json:"secret"`
	}
	response, err := httpClient.
		R().
		SetResult(&result).
		Get(fmt.Sprintf("/api/v4/secrets/id/%s", url.PathEscape(secretID)))
	if err != nil {
		return Secret{}, fmt.Errorf("failed to get secret by ID: %w", err)
	}

	if err := checkResponse(response, "get secret by ID"); err != nil {
		return Secret{}, err
	}

	return result.Secret, nil
}

func isApproval(approval json.RawMessage) bool {
	return len(approval) > 0 && string(approval) != "null"
}
