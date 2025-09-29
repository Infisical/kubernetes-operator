package util

import (
	"fmt"

	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
)

func GetProjectByID(accessToken string, projectId string) (model.Project, error) {

	httpClient, err := CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: accessToken,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})

	if err != nil {
		return model.Project{}, fmt.Errorf("unable to create resty client. [err=%v]", err)
	}

	projectDetails, err := api.CallGetProjectByID(httpClient, api.GetProjectByIDRequest{
		ProjectID: projectId,
	})
	if err != nil {
		return model.Project{}, fmt.Errorf("unable to get project by slug. [err=%v]", err)
	}

	return projectDetails.Project, nil
}

func GetProjectBySlug(accessToken string, projectSlug string) (model.Project, error) {
	httpClient, err := CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: accessToken,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})

	if err != nil {
		return model.Project{}, fmt.Errorf("unable to create resty client. [err=%v]", err)
	}

	project, err := api.CallGetProjectBySlugV2(httpClient, api.GetProjectBySlugRequest{
		ProjectSlug: projectSlug,
	})

	if err != nil {
		return model.Project{}, fmt.Errorf("unable to get project by slug. [err=%v]", err)
	}

	return project, nil
}

func ExtractProjectIdFromSlug(accessToken string, projectSlug string) (string, error) {

	httpClient, err := CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: accessToken,
		Headers: map[string]string{
			"Accept": "application/json",
		},
	})

	if err != nil {
		return "", fmt.Errorf("unable to create resty client. [err=%v]", err)
	}

	project, err := api.CallGetProjectBySlugV2(httpClient, api.GetProjectBySlugRequest{
		ProjectSlug: projectSlug,
	})

	if err != nil {
		return "", fmt.Errorf("unable to get project by slug. [err=%v]", err)
	}

	return project.ID, nil

}
