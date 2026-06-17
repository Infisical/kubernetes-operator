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
	ErrUnauthorized = errors.New("Unauthorized access")
	ErrBadRequest   = errors.New("Bad request")

	RetryAfterHeader = "retry-after"
)

type TooManyRequestsError struct {
	RetryAfter int
}

func (e *TooManyRequestsError) Error() string {
	return "too many requests"
}

type ListSecretsRequest struct {
	ProjectId       string   `json:"workspaceId"`
	ProjectSlug     string   `json:"projectSlug"`
	EnvironmentSlug string   `json:"environment"`
	SecretPath      string   `json:"secretPath"`
	Tags            []string `json:"tags,omitempty"`
	Recursive       bool     `json:"recursive,omitempty"`
}

type Secret struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	SecretKey   string `json:"secretKey"`
	Environment string `json:"environment"`
	SecretValue string `json:"secretValue"`
	SecretPath  string `json:"secretPath"`
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
		retryHeader := response.Header().Get(RetryAfterHeader)
		if retryHeader != "" {
			retryAfter, err := strconv.Atoi(retryHeader)
			if err != nil {
				// Our rate limit window is one minute, so if we can't get the
				// Retry-After header, we wait one minute.
				retryAfter = 60
			}

			return ListSecretsResponse{}, &TooManyRequestsError{RetryAfter: retryAfter}
		}

		// if no retry header, return generic error
		return ListSecretsResponse{}, fmt.Errorf("failed to list secrets due to rate limit")
	}

	if response.StatusCode() == 400 {
		return ListSecretsResponse{}, fmt.Errorf("%w: failed to list secrets [body=%s]", ErrBadRequest, string(response.Body()))
	}

	if response.StatusCode() > 400 {
		return ListSecretsResponse{}, fmt.Errorf("failed to list secrets [status=%d, body=%s]", response.StatusCode(), string(response.Body()))
	}

	return listSecretsResponse, nil
}
