package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

var (
	ErrTooManyRequests = errors.New("too many requests")
	ErrUnauthorized    = errors.New("Unauthorized access")
	ErrBadRequest      = errors.New("Bad request")
)

type ListSecretsRequest struct {
	ProjectId              string   `json:"workspaceId"`
	EnvironmentSlug        string   `json:"environment"`
	SecretPath             string   `json:"secretPath"`
	Tags                   []string `json:"tags,omitempty"`
	Recursive              bool     `json:"recursive,omitempty"`
	IncludeImports         bool     `json:"include_imports,omitempty"`
	ExpandSecretReferences bool     `json:"expandSecretReferences,omitempty"`
}

type SecretMetadata struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	IsEncrypted bool   `json:"isEncrypted"`
}

type SecretTag struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type SecretActor struct {
	ActorID      string `json:"actorId"`
	ActorType    string `json:"actorType"`
	Name         string `json:"name"`
	MembershipID string `json:"membershipId"`
	GroupID      string `json:"groupId"`
}

type Secret struct {
	ID                       string           `json:"id"`
	LegacyID                 string           `json:"_id"`
	Workspace                string           `json:"workspace"`
	Environment              string           `json:"environment"`
	Version                  int              `json:"version"`
	Type                     string           `json:"type"`
	SecretKey                string           `json:"secretKey"`
	SecretValue              string           `json:"secretValue"`
	SecretComment            string           `json:"secretComment"`
	CreatedAt                string           `json:"createdAt"`
	UpdatedAt                string           `json:"updatedAt"`
	SecretValueHidden        bool             `json:"secretValueHidden"`
	SecretReminderNote       string           `json:"secretReminderNote"`
	SecretReminderRepeatDays int              `json:"secretReminderRepeatDays"`
	SkipMultilineEncoding    bool             `json:"skipMultilineEncoding"`
	Actor                    SecretActor      `json:"actor"`
	IsRotatedSecret          bool             `json:"isRotatedSecret"`
	RotationID               string           `json:"rotationId"`
	SecretPath               string           `json:"secretPath"`
	SecretMetadata           []SecretMetadata `json:"secretMetadata,omitempty"`
	Tags                     []SecretTag      `json:"tags,omitempty"`
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
		"projectId":              request.ProjectId,
		"environment":            request.EnvironmentSlug,
		"secretPath":             request.SecretPath,
		"recursive":              strconv.FormatBool(request.Recursive),
		"includeImports":         strconv.FormatBool(request.IncludeImports),
		"expandSecretReferences": strconv.FormatBool(request.ExpandSecretReferences),
	}

	if len(request.Tags) > 0 {
		queryParams["tagSlugs"] = strings.Join(request.Tags, ",")
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

	if response.StatusCode() == http.StatusUnauthorized || response.StatusCode() == http.StatusForbidden {
		return ListSecretsResponse{}, fmt.Errorf("%w: status %d", ErrUnauthorized, response.StatusCode())
	}

	if response.StatusCode() == http.StatusTooManyRequests {
		return ListSecretsResponse{}, ErrTooManyRequests
	}

	if response.StatusCode() == 400 {
		return ListSecretsResponse{}, fmt.Errorf("%w: failed to list secrets [body=%s]", ErrBadRequest, string(response.Body()))
	}

	if response.StatusCode() > 400 {
		return ListSecretsResponse{}, fmt.Errorf("failed to list secrets [status=%d, body=%s]", response.StatusCode(), string(response.Body()))
	}

	return listSecretsResponse, nil
}
