package api

import (
	"encoding/json"
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
