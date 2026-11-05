package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-resty/resty/v2"
)

type CreateFolderRequest struct {
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment"`
	// Path is the parent folder of the new folder. Missing parent folders are created too.
	Path string `json:"path"`
	Name string `json:"name"`
}

// CreateFolder creates a folder and any missing parent folders. A folder that
// already exists counts as success.
func CreateFolder(httpClient *resty.Client, request CreateFolderRequest) error {
	response, err := httpClient.
		R().
		SetBody(request).
		Post("/api/v2/folders")
	if err != nil {
		return fmt.Errorf("failed to create folder: %w", err)
	}

	err = checkResponse(response, "create folder")
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && strings.Contains(apiErr.Message, "already exists") {
		return nil
	}

	return err
}
