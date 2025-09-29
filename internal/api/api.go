package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Infisical/infisical/k8-operator/internal/config"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/go-resty/resty/v2"
)

func CallGetServiceTokenDetailsV2(httpClient *resty.Client) (GetServiceTokenDetailsResponse, error) {
	var tokenDetailsResponse GetServiceTokenDetailsResponse
	response, err := httpClient.
		R().
		SetResult(&tokenDetailsResponse).
		Get(fmt.Sprintf("%v/v2/service-token", config.API_HOST_URL))

	if err != nil {
		return GetServiceTokenDetailsResponse{}, fmt.Errorf("CallGetServiceTokenDetails: Unable to complete api request [err=%s]", err)
	}

	if response.IsError() {
		return GetServiceTokenDetailsResponse{}, fmt.Errorf("CallGetServiceTokenDetails: Unsuccessful response: [response=%s]", response)
	}

	return tokenDetailsResponse, nil
}

func CallGetServiceTokenAccountDetailsV2(httpClient *resty.Client) (ServiceAccountDetailsResponse, error) {
	var serviceAccountDetailsResponse ServiceAccountDetailsResponse
	response, err := httpClient.
		R().
		SetResult(&serviceAccountDetailsResponse).
		Get(fmt.Sprintf("%v/v2/service-accounts/me", config.API_HOST_URL))

	if err != nil {
		return ServiceAccountDetailsResponse{}, fmt.Errorf("CallGetServiceTokenAccountDetailsV2: Unable to complete api request [err=%s]", err)
	}

	if response.IsError() {
		return ServiceAccountDetailsResponse{}, fmt.Errorf("CallGetServiceTokenAccountDetailsV2: Unsuccessful response: [response=%s]", response)
	}

	return serviceAccountDetailsResponse, nil
}

func CallGetServiceAccountKeysV2(httpClient *resty.Client, request GetServiceAccountKeysRequest) (GetServiceAccountKeysResponse, error) {
	var serviceAccountKeysResponse GetServiceAccountKeysResponse
	response, err := httpClient.
		R().
		SetResult(&serviceAccountKeysResponse).
		Get(fmt.Sprintf("%v/v2/service-accounts/%v/keys", config.API_HOST_URL, request.ServiceAccountId))

	if err != nil {
		return GetServiceAccountKeysResponse{}, fmt.Errorf("CallGetServiceAccountKeysV2: Unable to complete api request [err=%s]", err)
	}

	if response.IsError() {
		return GetServiceAccountKeysResponse{}, fmt.Errorf("CallGetServiceAccountKeysV2: Unsuccessful response: [response=%s]", response)
	}

	return serviceAccountKeysResponse, nil
}

func CallGetProjectByID(httpClient *resty.Client, request GetProjectByIDRequest) (GetProjectByIDResponse, error) {

	var projectResponse GetProjectByIDResponse

	response, err := httpClient.
		R().SetResult(&projectResponse).
		Get(fmt.Sprintf("%s/v1/workspace/%s", config.API_HOST_URL, request.ProjectID))

	if err != nil {
		return GetProjectByIDResponse{}, fmt.Errorf("CallGetProject: Unable to complete api request [err=%s]", err)
	}

	if response.IsError() {
		return GetProjectByIDResponse{}, fmt.Errorf("CallGetProject: Unsuccessful response: [response=%s]", response)
	}

	return projectResponse, nil

}

func CallGetProjectBySlugV2(httpClient *resty.Client, request GetProjectBySlugRequest) (model.Project, error) {
	var projectResponse model.Project

	response, err := httpClient.
		R().SetResult(&projectResponse).
		Get(fmt.Sprintf("%s/v2/workspace/%s", config.API_HOST_URL, request.ProjectSlug))

	if err != nil {
		return model.Project{}, fmt.Errorf("CallGetProject: Unable to complete api request [err=%s]", err)
	}

	if response.IsError() {
		return model.Project{}, fmt.Errorf("CallGetProject: Unsuccessful response: [response=%s]", response)
	}

	return projectResponse, nil

}

func CallSubscribeProjectEvents(httpClient *resty.Client, projectId, secretsPath, envSlug string) (*http.Response, error) {
	conditions := &SubscribeProjectEventsRequestCondition{
		SecretPath:      secretsPath,
		EnvironmentSlug: envSlug,
	}

	body, err := json.Marshal(&SubscribeProjectEventsRequest{
		ProjectID: projectId,
		Register: []SubscribeProjectEventsRequestRegister{
			{
				Event:      "secret:create",
				Conditions: conditions,
			},
			{
				Event:      "secret:update",
				Conditions: conditions,
			},
			{
				Event:      "secret:delete",
				Conditions: conditions,
			},
			{
				Event:      "secret:import-mutation",
				Conditions: conditions,
			},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("CallSubscribeProjectEvents: Unable to marshal body [err=%s]", err)
	}

	response, err := httpClient.
		R().
		SetDoNotParseResponse(true).
		SetBody(body).
		Post(fmt.Sprintf("%s/v1/events/subscribe/project-events", config.API_HOST_URL))

	if err != nil {
		return nil, fmt.Errorf("CallSubscribeProjectEvents: Unable to complete api request [err=%s]", err)
	}

	if response.IsError() {
		data := struct {
			Message string `json:"message"`
		}{}

		if err := json.NewDecoder(response.RawBody()).Decode(&data); err != nil {
			return nil, err
		}

		return nil, fmt.Errorf("CallSubscribeProjectEvents: Unsuccessful response: [message=%s]", data.Message)
	}

	return response.RawResponse, nil
}
