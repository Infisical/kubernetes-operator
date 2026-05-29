package model

import infisicalSdk "github.com/infisical/go-sdk"

type ServiceAccountDetails struct {
	AccessKey  string
	PublicKey  string
	PrivateKey string
}

type UniversalAuthIdentityDetails struct {
	ClientId     string
	ClientSecret string
}

type LdapIdentityDetails struct {
	Username string
	Password string
}

type SingleEnvironmentVariable struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	SecretPath string `json:"secretPath"`
	Type       string `json:"type"`
	ID         string `json:"id"`
}

type SecretTemplateOptions struct {
	Value      string `json:"value"`
	SecretPath string `json:"secretPath"`
}

type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	OrgID        string `json:"orgId"`
	Environments []struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
		ID   string `json:"id"`
	}
}

type CreateRestyClientOptions struct {
	AccessToken   string
	Headers       map[string]string
	CaCertificate string
}

type InfisicalConnection struct {
	Host          string
	CaCertificate string
}

type AuthenticationResult struct {
	MachineIdentity infisicalSdk.MachineIdentityCredential
	SdkClient       infisicalSdk.InfisicalClientInterface
}
