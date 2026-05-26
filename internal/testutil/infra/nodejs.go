package infra

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type ProjectSeed struct {
	ID      string
	Slug    string
	EnvSlug string
}

type IdentitySeed struct {
	ID   string
	Name string
}

type SecretSeed struct {
	ID      string
	Key     string
	Value   string
	Version int
}

type FolderSeed struct {
	ID   string
	Name string
}

type SecretImportSeed struct {
	ID string
}

type EnvironmentSeed struct {
	ID   string
	Slug string
	Name string
}

type TagSeed struct {
	ID   string
	Slug string
	Name string
}

type NodeJSService struct {
	container     testcontainers.Container
	url           string
	client        *resty.Client
	orgID         string
	userID        string
	userEmail     string
	identityToken string
	userToken     string
}

func (n *NodeJSService) URL() string           { return n.url }
func (n *NodeJSService) ContainerID() string   { return n.container.GetContainerID() }
func (n *NodeJSService) OrgID() string         { return n.orgID }
func (n *NodeJSService) UserID() string        { return n.userID }
func (n *NodeJSService) UserEmail() string     { return n.userEmail }
func (n *NodeJSService) IdentityToken() string { return n.identityToken }
func (n *NodeJSService) UserToken() string     { return n.userToken }
func (n *NodeJSService) Client() *resty.Client { return n.client }

func startNodeJS(ctx context.Context, networkName string, files []testcontainers.ContainerFile, cmd []string) (*NodeJSService, error) {
	user := ""
	if len(cmd) > 0 {
		user = "root"
	}

	req := testcontainers.ContainerRequest{
		Image:        "infisical/infisical:latest",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"backend-nodejs"},
		},
		User: user,
		Env: map[string]string{
			"NODE_ENV":          "development",
			"DB_CONNECTION_URI": fmt.Sprintf("postgres://%s:%s@db:5432/%s?sslmode=disable", pgUser, pgPassword, pgDB),
			"REDIS_URL":         "redis://redis:6379",
			"ENCRYPTION_KEY":    EncryptionKey,
			"AUTH_SECRET":       AuthSecret,
			"SITE_URL":          "http://localhost:8080",
			"TELEMETRY_ENABLED": "false",
			"SMTP_HOST":         "",
		},
		Files:      files,
		Cmd:        cmd,
		WaitingFor: wait.ForHTTP("/api/status").WithPort("8080/tcp").WithStartupTimeout(120 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting nodejs: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting nodejs host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "8080/tcp")
	if err != nil {
		return nil, fmt.Errorf("getting nodejs port: %w", err)
	}

	baseURL := fmt.Sprintf("http://%s:%d", host, mappedPort.Int())

	return &NodeJSService{
		container: container,
		url:       baseURL,
		client:    resty.New().SetBaseURL(baseURL),
	}, nil
}

func (n *NodeJSService) bootstrap() {
	var bootstrapResp BootstrapResponse
	resp, err := n.client.R().
		SetBody(BootstrapRequest{
			Email:        "test-admin@example.com",
			Password:     "testpassword123",
			Organization: "test-org",
		}).
		SetResult(&bootstrapResp).
		Post("/api/v1/admin/bootstrap")
	if err != nil {
		log.Fatalf("infra.bootstrap: request failed: %v", err)
	}
	if resp.IsError() {
		log.Fatalf("infra.bootstrap: returned %d: %s", resp.StatusCode(), resp.String())
	}

	n.orgID = bootstrapResp.Organization.ID
	n.identityToken = bootstrapResp.Identity.Credentials.Token
	n.userEmail = bootstrapResp.User.Email
	n.userID = bootstrapResp.User.ID

	var loginResp LoginResponse
	resp, err = n.client.R().
		SetBody(LoginRequest{
			Email:    n.userEmail,
			Password: "testpassword123",
		}).
		SetResult(&loginResp).
		Post("/api/v3/auth/login")
	if err != nil {
		log.Fatalf("infra.bootstrap: login request failed: %v", err)
	}
	if resp.IsError() {
		log.Fatalf("infra.bootstrap: login returned %d: %s", resp.StatusCode(), resp.String())
	}

	var selectOrgResp SelectOrgResponse
	resp, err = n.client.R().
		SetHeader("Authorization", "Bearer "+loginResp.AccessToken).
		SetBody(SelectOrgRequest{
			OrganizationID: n.orgID,
		}).
		SetResult(&selectOrgResp).
		Post("/api/v3/auth/select-organization")
	if err != nil {
		log.Fatalf("infra.bootstrap: select-org request failed: %v", err)
	}
	if resp.IsError() {
		log.Fatalf("infra.bootstrap: select-org returned %d: %s", resp.StatusCode(), resp.String())
	}
	n.userToken = selectOrgResp.Token
}

func (n *NodeJSService) MustCreateProject(name string) *ProjectSeed {
	var projectResp CreateProjectResponse
	resp, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateProjectRequest{
			ProjectName: name,
			Slug:        fmt.Sprintf("test-%s", name),
			Type:        "secret-manager",
		}).
		SetResult(&projectResp).
		Post("/api/v1/projects")
	if err != nil {
		log.Fatalf("infra.MustCreateProject: request failed: %v", err)
	}
	if resp.IsError() {
		log.Fatalf("infra.MustCreateProject: returned %d: %s", resp.StatusCode(), resp.String())
	}

	return &ProjectSeed{
		ID:      projectResp.Project.ID,
		Slug:    projectResp.Project.Slug,
		EnvSlug: "dev",
	}
}

func (n *NodeJSService) CreateProject(t *testing.T, name string) *ProjectSeed {
	t.Helper()

	slug := RandomID(fmt.Sprintf("t-%s-", name))
	if len(slug) > 36 {
		slug = slug[:36]
	}

	var projectResp CreateProjectResponse
	resp, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateProjectRequest{
			ProjectName: name,
			Slug:        slug,
			Type:        "secret-manager",
		}).
		SetResult(&projectResp).
		Post("/api/v1/projects")
	if err != nil {
		t.Fatalf("infra.CreateProject: request failed: %v", err)
	}
	if resp.IsError() {
		t.Fatalf("infra.CreateProject: returned %d: %s", resp.StatusCode(), resp.String())
	}

	projectID := projectResp.Project.ID

	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(AddUserToProjectRequest{
			Usernames: []string{n.userEmail},
			RoleSlugs: []string{"admin"},
		}).
		Post(fmt.Sprintf("/api/v1/projects/%s/memberships", projectID))
	if err != nil {
		t.Fatalf("infra.CreateProject: add user failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateProject: add user returned %d: %s", r.StatusCode(), r.String())
	}

	return &ProjectSeed{
		ID:      projectID,
		Slug:    projectResp.Project.Slug,
		EnvSlug: "dev",
	}
}

func (n *NodeJSService) DeleteProject(t *testing.T, projectID string) {
	t.Helper()

	resp, err := n.client.R().
		SetAuthToken(n.identityToken).
		Delete("/api/v1/projects/" + projectID)
	if err != nil {
		t.Fatalf("infra.DeleteProject: request failed: %v", err)
	}
	if resp.IsError() {
		t.Fatalf("infra.DeleteProject: returned %d: %s", resp.StatusCode(), resp.String())
	}
}

func (n *NodeJSService) CreateIdentity(t *testing.T, name string) *IdentitySeed {
	t.Helper()

	var resp CreateIdentityResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateIdentityRequest{
			Name:           name,
			OrganizationID: n.orgID,
			Role:           "no-access",
		}).
		SetResult(&resp).
		Post("/api/v1/identities")
	if err != nil {
		t.Fatalf("infra.CreateIdentity: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateIdentity: returned %d: %s", r.StatusCode(), r.String())
	}

	return &IdentitySeed{
		ID:   resp.Identity.ID,
		Name: name,
	}
}

func (n *NodeJSService) DeleteIdentity(t *testing.T, identityID string) {
	t.Helper()

	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		Delete("/api/v1/identities/" + identityID)
	if err != nil {
		t.Fatalf("infra.DeleteIdentity: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.DeleteIdentity: returned %d: %s", r.StatusCode(), r.String())
	}
}

func (n *NodeJSService) AddIdentityToProject(t *testing.T, projectID, identityID string, roles []RoleAssignment) {
	t.Helper()

	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(AddIdentityToProjectWithRolesRequest{
			Roles: roles,
		}).
		Post(fmt.Sprintf("/api/v1/projects/%s/memberships/identities/%s", projectID, identityID))
	if err != nil {
		t.Fatalf("infra.AddIdentityToProject: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.AddIdentityToProject: returned %d: %s", r.StatusCode(), r.String())
	}
}

func Role(slug string) []RoleAssignment {
	return []RoleAssignment{{Role: slug}}
}

func (n *NodeJSService) CreateSecret(t *testing.T, projectID, environment, secretPath, key, value string, opts *CreateSecretOpts) *SecretSeed {
	t.Helper()

	var comment string
	var metadata []SecretMetadataEntry
	var tagIDs []string
	secretType := "shared"

	if opts != nil {
		comment = opts.Comment
		metadata = opts.Metadata
		tagIDs = opts.TagIDs
		if opts.Type != "" {
			secretType = opts.Type
		}
	}

	token := n.identityToken
	if secretType == "personal" {
		token = n.userToken
	}

	var resp CreateSecretResponse
	r, err := n.client.R().
		SetAuthToken(token).
		SetBody(CreateSecretRequest{
			ProjectID:      projectID,
			Environment:    environment,
			SecretPath:     secretPath,
			SecretValue:    value,
			SecretComment:  comment,
			SecretMetadata: metadata,
			Type:           secretType,
			TagIDs:         tagIDs,
		}).
		SetResult(&resp).
		Post(fmt.Sprintf("/api/v4/secrets/%s", key))
	if err != nil {
		t.Fatalf("infra.CreateSecret: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateSecret: returned %d: %s", r.StatusCode(), r.String())
	}

	return &SecretSeed{
		ID:      resp.Secret.ID,
		Key:     key,
		Value:   value,
		Version: 1,
	}
}

type CreateSecretOpts struct {
	Comment  string
	Metadata []SecretMetadataEntry
	TagIDs   []string
	Type     string
}

func (n *NodeJSService) CreateFolder(t *testing.T, projectID, environment, path, name string) *FolderSeed {
	t.Helper()

	var resp CreateFolderResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateFolderRequest{
			ProjectID:   projectID,
			Environment: environment,
			Path:        path,
			Name:        name,
		}).
		SetResult(&resp).
		Post("/api/v2/folders")
	if err != nil {
		t.Fatalf("infra.CreateFolder: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateFolder: returned %d: %s", r.StatusCode(), r.String())
	}

	return &FolderSeed{
		ID:   resp.Folder.ID,
		Name: name,
	}
}

func (n *NodeJSService) CreateSecretImport(t *testing.T, projectID, environment, path, importEnv, importPath string) *SecretImportSeed {
	t.Helper()

	var resp CreateSecretImportResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateSecretImportRequest{
			ProjectID:   projectID,
			Environment: environment,
			Path:        path,
			Import: SecretImportTarget{
				Environment: importEnv,
				Path:        importPath,
			},
		}).
		SetResult(&resp).
		Post("/api/v2/secret-imports")
	if err != nil {
		t.Fatalf("infra.CreateSecretImport: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateSecretImport: returned %d: %s", r.StatusCode(), r.String())
	}

	return &SecretImportSeed{
		ID: resp.SecretImport.ID,
	}
}

func (n *NodeJSService) CreateEnvironment(t *testing.T, projectID, slug, name string) *EnvironmentSeed {
	t.Helper()

	var resp CreateEnvironmentResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateEnvironmentRequest{
			Slug: slug,
			Name: name,
		}).
		SetResult(&resp).
		Post(fmt.Sprintf("/api/v1/projects/%s/environments", projectID))
	if err != nil {
		t.Fatalf("infra.CreateEnvironment: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateEnvironment: returned %d: %s", r.StatusCode(), r.String())
	}

	return &EnvironmentSeed{
		ID:   resp.Environment.ID,
		Slug: slug,
		Name: name,
	}
}

func (n *NodeJSService) CreateTag(t *testing.T, projectID, slug, name, color string) *TagSeed {
	t.Helper()

	var resp CreateTagResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateTagRequest{
			Slug:  slug,
			Name:  name,
			Color: color,
		}).
		SetResult(&resp).
		Post(fmt.Sprintf("/api/v1/projects/%s/tags", projectID))
	if err != nil {
		t.Fatalf("infra.CreateTag: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.CreateTag: returned %d: %s", r.StatusCode(), r.String())
	}

	return &TagSeed{
		ID:   resp.Tag.ID,
		Slug: slug,
		Name: name,
	}
}

func (n *NodeJSService) SetupUniversalAuth(t *testing.T, identityID string) *UniversalAuthCredentials {
	t.Helper()

	var universalAuthResp CreateUniversalAuthResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateUniversalAuthRequest{
			IdentityID:                    identityID,
			AccessTokenTrustedIPs:         []IPAddress{{IPAddress: "0.0.0.0/0"}},
			AccessTokenTTL:                3600,
			AccessTokenMaxTTL:             7200,
			AccessTokenNumUsesLimit:       0,
			ClientSecretTrustedIPs:        []IPAddress{{IPAddress: "0.0.0.0/0"}},
			ClientSecretNumUsesLimit:      0,
			IsClientSecretRotationEnabled: false,
		}).
		SetResult(&universalAuthResp).
		Post("/api/v1/auth/universal-auth/identities/" + identityID)
	if err != nil {
		t.Fatalf("infra.SetupUniversalAuth: create auth failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.SetupUniversalAuth: create auth returned %d: %s", r.StatusCode(), r.String())
	}

	var clientSecretResp CreateClientSecretResponse
	r, err = n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateClientSecretRequest{
			Description:  "test-client-secret",
			TTL:          0,
			NumUsesLimit: 0,
		}).
		SetResult(&clientSecretResp).
		Post("/api/v1/auth/universal-auth/identities/" + identityID + "/client-secrets")
	if err != nil {
		t.Fatalf("infra.SetupUniversalAuth: create client secret failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.SetupUniversalAuth: create client secret returned %d: %s", r.StatusCode(), r.String())
	}

	return &UniversalAuthCredentials{
		ClientID:     universalAuthResp.IdentityUniversalAuth.ClientID,
		ClientSecret: clientSecretResp.ClientSecret,
	}
}

func (n *NodeJSService) GetIdentityAccessToken(t *testing.T, identityID string) string {
	t.Helper()

	var universalAuthResp CreateUniversalAuthResponse
	r, err := n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateUniversalAuthRequest{
			IdentityID:                    identityID,
			AccessTokenTrustedIPs:         []IPAddress{{IPAddress: "0.0.0.0/0"}},
			AccessTokenTTL:                3600,
			AccessTokenMaxTTL:             7200,
			AccessTokenNumUsesLimit:       0,
			ClientSecretTrustedIPs:        []IPAddress{{IPAddress: "0.0.0.0/0"}},
			ClientSecretNumUsesLimit:      0,
			IsClientSecretRotationEnabled: false,
		}).
		SetResult(&universalAuthResp).
		Post("/api/v1/auth/universal-auth/identities/" + identityID)
	if err != nil {
		t.Fatalf("infra.GetIdentityAccessToken: create auth failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.GetIdentityAccessToken: create auth returned %d: %s", r.StatusCode(), r.String())
	}

	clientID := universalAuthResp.IdentityUniversalAuth.ClientID

	var clientSecretResp CreateClientSecretResponse
	r, err = n.client.R().
		SetAuthToken(n.identityToken).
		SetBody(CreateClientSecretRequest{
			Description:  "test-client-secret",
			TTL:          0,
			NumUsesLimit: 0,
		}).
		SetResult(&clientSecretResp).
		Post("/api/v1/auth/universal-auth/identities/" + identityID + "/client-secrets")
	if err != nil {
		t.Fatalf("infra.GetIdentityAccessToken: create client secret failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.GetIdentityAccessToken: create client secret returned %d: %s", r.StatusCode(), r.String())
	}

	clientSecret := clientSecretResp.ClientSecret

	var loginResp UniversalAuthLoginResponse
	r, err = n.client.R().
		SetBody(UniversalAuthLoginRequest{
			ClientID:     clientID,
			ClientSecret: clientSecret,
		}).
		SetResult(&loginResp).
		Post("/api/v1/auth/universal-auth/login")
	if err != nil {
		t.Fatalf("infra.GetIdentityAccessToken: login failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.GetIdentityAccessToken: login returned %d: %s", r.StatusCode(), r.String())
	}

	return loginResp.AccessToken
}

func (n *NodeJSService) RevokeAccessToken(t *testing.T, accessToken string) {
	t.Helper()

	r, err := n.client.R().
		SetBody(map[string]string{"accessToken": accessToken}).
		Post("/api/v1/auth/token/revoke")
	if err != nil {
		t.Fatalf("infra.RevokeAccessToken: request failed: %v", err)
	}
	if r.IsError() {
		t.Fatalf("infra.RevokeAccessToken: returned %d: %s", r.StatusCode(), r.String())
	}
}
