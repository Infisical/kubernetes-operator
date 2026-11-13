package api

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/go-resty/resty/v2"
)

func FindProjectBySlug(httpClient *resty.Client, slug string) (model.Project, error) {
	var project model.Project
	response, err := httpClient.
		R().
		SetResult(&project).
		Get(fmt.Sprintf("/api/v1/projects/slug/%s", url.PathEscape(slug)))
	if err != nil {
		return model.Project{}, fmt.Errorf("failed to find project by slug: %w", err)
	}

	if response.StatusCode() == http.StatusNotFound {
		return model.Project{}, fmt.Errorf("project with slug %q not found", slug)
	}

	if err := checkResponse(response, "find project by slug"); err != nil {
		return model.Project{}, err
	}

	return project, nil
}
