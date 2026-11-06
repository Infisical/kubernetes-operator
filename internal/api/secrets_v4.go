package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

var (
	ErrUnauthorized = errors.New("Unauthorized access")
	ErrBadRequest   = errors.New("Bad request")
	ErrNotFound     = errors.New("Not found")

	// ErrApprovalRequired is returned when a change was submitted as an approval
	// request instead of being applied, because a secret approval policy covers
	// the environment and path.
	ErrApprovalRequired = errors.New("change requires approval")

	RetryAfterHeader = "retry-after"
)

type TooManyRequestsError struct {
	RetryAfter int
}

func (e *TooManyRequestsError) Error() string {
	return "too many requests"
}

// Error is returned for an unsuccessful response from the Infisical API. It wraps
// ErrUnauthorized, ErrBadRequest or ErrNotFound when one of them applies, so
// callers can match it with errors.Is.
type Error struct {
	// Operation describes the request, e.g. "list secrets".
	Operation  string
	StatusCode int
	// Message is the message field of the response body, when it has one.
	Message string
	Body    string
}

func (e *Error) Error() string {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("%v: status %d", ErrUnauthorized, e.StatusCode)
	case http.StatusBadRequest:
		return fmt.Sprintf("%v: failed to %s [body=%s]", ErrBadRequest, e.Operation, e.Body)
	default:
		return fmt.Sprintf("failed to %s [status=%d, body=%s]", e.Operation, e.StatusCode, e.Body)
	}
}

func (e *Error) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusBadRequest:
		return ErrBadRequest
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return nil
	}
}

// IsFolderNotFound reports whether err is the error Infisical returns when the
// folder a request targets doesn't exist.
func IsFolderNotFound(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) &&
		apiErr.StatusCode == http.StatusNotFound &&
		strings.HasPrefix(apiErr.Message, "Folder with path")
}

// checkResponse returns nil for a successful response. Otherwise it returns a
// *TooManyRequestsError for rate limits that carry a Retry-After header, or an
// *Error describing the failed operation.
func checkResponse(response *resty.Response, operation string) error {
	if !response.IsError() {
		return nil
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

			return &TooManyRequestsError{RetryAfter: retryAfter}
		}

		// if no retry header, return generic error
		return fmt.Errorf("failed to %s due to rate limit", operation)
	}

	body := string(response.Body())
	var errorBody struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(response.Body(), &errorBody)

	return &Error{
		Operation:  operation,
		StatusCode: response.StatusCode(),
		Message:    errorBody.Message,
		Body:       body,
	}
}

type ListSecretsRequest struct {
	ProjectId       string   `json:"workspaceId"`
	EnvironmentSlug string   `json:"environment"`
	SecretPath      string   `json:"secretPath"`
	Tags            []string `json:"tags,omitempty"`
	Recursive       bool     `json:"recursive,omitempty"`

	// ExcludeImports leaves out secrets imported into the path. Imports are included by default.
	ExcludeImports bool `json:"-"`
	// SkipReferenceExpansion returns secret references such as ${KEY} as written
	// instead of their resolved values. References are expanded by default.
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

	if request.ExcludeImports {
		queryParams["includeImports"] = "false"
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
