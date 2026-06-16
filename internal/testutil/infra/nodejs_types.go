package infra

type BootstrapRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	Organization string `json:"organization"`
}

type BootstrapResponse struct {
	Organization struct {
		ID string `json:"id"`
	} `json:"organization"`
	Identity struct {
		Credentials struct {
			Token string `json:"token"`
		} `json:"credentials"`
	} `json:"identity"`
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken string `json:"accessToken"`
}

type SelectOrgRequest struct {
	OrganizationID string `json:"organizationId"`
}

type SelectOrgResponse struct {
	Token string `json:"token"`
}

type CreateProjectRequest struct {
	ProjectName string `json:"projectName"`
	Slug        string `json:"slug"`
	Type        string `json:"type"`
}

type CreateProjectResponse struct {
	Project struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"project"`
}

type CreateIdentityRequest struct {
	Name           string `json:"name"`
	OrganizationID string `json:"organizationId"`
	Role           string `json:"role"`
}

type CreateIdentityResponse struct {
	Identity struct {
		ID string `json:"id"`
	} `json:"identity"`
}

type RoleAssignment struct {
	Role                     string `json:"role"`
	IsTemporary              bool   `json:"isTemporary,omitempty"`
	TemporaryMode            string `json:"temporaryMode,omitempty"`
	TemporaryRange           string `json:"temporaryRange,omitempty"`
	TemporaryAccessStartTime string `json:"temporaryAccessStartTime,omitempty"`
}

type AddIdentityToProjectWithRolesRequest struct {
	Roles []RoleAssignment `json:"roles"`
}

type AddUserToProjectRequest struct {
	Usernames []string `json:"usernames"`
	RoleSlugs []string `json:"roleSlugs"`
}

type Permission struct {
	Subject    string         `json:"subject"`
	Action     any            `json:"action"`
	Conditions map[string]any `json:"conditions,omitempty"`
	Inverted   bool           `json:"inverted,omitempty"`
}

type CreateSecretRequest struct {
	ProjectID      string                `json:"projectId"`
	Environment    string                `json:"environment"`
	SecretPath     string                `json:"secretPath"`
	SecretValue    string                `json:"secretValue"`
	SecretComment  string                `json:"secretComment,omitempty"`
	SecretMetadata []SecretMetadataEntry `json:"secretMetadata,omitempty"`
	Type           string                `json:"type"`
	TagIDs         []string              `json:"tagIds,omitempty"`
}

type SecretMetadataEntry struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	IsEncrypted bool   `json:"isEncrypted,omitempty"`
}

type CreateSecretResponse struct {
	Secret struct {
		ID string `json:"id"`
	} `json:"secret"`
}

type CreateFolderRequest struct {
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment"`
	Path        string `json:"path"`
	Name        string `json:"name"`
}

type CreateFolderResponse struct {
	Folder struct {
		ID string `json:"id"`
	} `json:"folder"`
}

type SecretImportTarget struct {
	Environment string `json:"environment"`
	Path        string `json:"path"`
}

type CreateSecretImportRequest struct {
	ProjectID   string             `json:"projectId"`
	Environment string             `json:"environment"`
	Path        string             `json:"path"`
	Import      SecretImportTarget `json:"import"`
}

type CreateSecretImportResponse struct {
	SecretImport struct {
		ID string `json:"id"`
	} `json:"secretImport"`
}

type CreateEnvironmentRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type CreateEnvironmentResponse struct {
	Environment struct {
		ID string `json:"id"`
	} `json:"environment"`
}

type CreateTagRequest struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type CreateTagResponse struct {
	Tag struct {
		ID string `json:"id"`
	} `json:"tag"`
}

type IPAddress struct {
	IPAddress string `json:"ipAddress"`
}

type CreateUniversalAuthRequest struct {
	IdentityID                    string      `json:"identityId"`
	AccessTokenTrustedIPs         []IPAddress `json:"accessTokenTrustedIps"`
	AccessTokenTTL                int         `json:"accessTokenTTL"`
	AccessTokenMaxTTL             int         `json:"accessTokenMaxTTL"`
	AccessTokenNumUsesLimit       int         `json:"accessTokenNumUsesLimit"`
	ClientSecretTrustedIPs        []IPAddress `json:"clientSecretTrustedIps"`
	ClientSecretNumUsesLimit      int         `json:"clientSecretNumUsesLimit"`
	IsClientSecretRotationEnabled bool        `json:"isClientSecretRotationEnabled"`
}

type CreateUniversalAuthResponse struct {
	IdentityUniversalAuth struct {
		ID       string `json:"id"`
		ClientID string `json:"clientId"`
	} `json:"identityUniversalAuth"`
}

type CreateClientSecretRequest struct {
	Description  string `json:"description"`
	TTL          int    `json:"ttl"`
	NumUsesLimit int    `json:"numUsesLimit"`
}

type CreateClientSecretResponse struct {
	ClientSecretData struct {
		ID                 string `json:"id"`
		ClientSecretPrefix string `json:"clientSecretPrefix"`
	} `json:"clientSecretData"`
	ClientSecret string `json:"clientSecret"`
}

type UniversalAuthLoginRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type UniversalAuthLoginResponse struct {
	AccessToken string `json:"accessToken"`
}

type UniversalAuthCredentials struct {
	ClientID     string
	ClientSecret string
}

type CreateKubernetesAuthRequest struct {
	KubernetesHost          string      `json:"kubernetesHost"`
	CACert                  string      `json:"caCert"`
	TokenReviewerJwt        string      `json:"tokenReviewerJwt"`
	AllowedNamespaces       string      `json:"allowedNamespaces"`
	AllowedNames            string      `json:"allowedNames"`
	AllowedAudience         string      `json:"allowedAudience"`
	AccessTokenTrustedIPs   []IPAddress `json:"accessTokenTrustedIps"`
	AccessTokenTTL          int         `json:"accessTokenTTL"`
	AccessTokenMaxTTL       int         `json:"accessTokenMaxTTL"`
	AccessTokenNumUsesLimit int         `json:"accessTokenNumUsesLimit"`
}

type CreateKubernetesAuthResponse struct {
	IdentityKubernetesAuth struct {
		ID string `json:"id"`
	} `json:"identityKubernetesAuth"`
}

type KubernetesAuthSetup struct {
	KubernetesHost    string
	CACert            string
	TokenReviewerJwt  string
	AllowedNamespaces string
	AllowedNames      string
}

type CreateProjectRoleRequest struct {
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
}

type CreateProjectRoleResponse struct {
	Role struct {
		ID string `json:"id"`
	} `json:"role"`
}

type ProjectRoleSeed struct {
	ID   string
	Slug string
}
