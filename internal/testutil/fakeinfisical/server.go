// Package fakeinfisical provides an in-memory Infisical API for tests. It implements
// the endpoints the operator uses to push secrets, with the same status codes and
// error messages as the real API for the cases the operator handles.
package fakeinfisical

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"sync"
)

// Secret is a secret stored by the fake server.
type Secret struct {
	ID          string
	ProjectID   string
	Environment string
	SecretPath  string
	Key         string
	Value       string
}

// Server is an in-memory Infisical API.
type Server struct {
	*httptest.Server

	mu            sync.Mutex
	folders       map[string]bool
	secrets       map[string]*Secret
	projects      map[string]string // slug -> ID
	approvalPaths map[string]bool
	nextID        int
	requests      []string
	stall         chan struct{}
}

// New starts a fake Infisical in which every given environment of every given project exists.
func New(projectIDs []string, environments []string) *Server {
	s := &Server{
		folders:       map[string]bool{},
		secrets:       map[string]*Secret{},
		projects:      map[string]string{},
		approvalPaths: map[string]bool{},
	}
	for _, projectID := range projectIDs {
		for _, env := range environments {
			s.folders[folderKey(projectID, env, "/")] = true
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/secrets", s.listSecrets)
	mux.HandleFunc("PATCH /api/v4/secrets/batch", s.upsertSecrets)
	mux.HandleFunc("DELETE /api/v4/secrets/batch", s.deleteSecrets)
	mux.HandleFunc("GET /api/v4/secrets/id/{id}", s.getSecretByID)
	mux.HandleFunc("POST /api/v2/folders", s.createFolder)
	mux.HandleFunc("GET /api/v1/projects/slug/{slug}", s.getProjectBySlug)
	s.Server = httptest.NewServer(s.record(mux))
	return s
}

// AddProjectSlug makes a project resolvable by slug.
func (s *Server) AddProjectSlug(slug, projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[slug] = projectID
}

// RequireApproval makes every change to the folder return an approval request.
func (s *Server) RequireApproval(projectID, environment, secretPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvalPaths[folderKey(projectID, environment, secretPath)] = true
}

// CreateFolder creates a folder and its parents.
func (s *Server) CreateFolder(projectID, environment, secretPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createFolderLocked(projectID, environment, secretPath)
}

// FolderExists reports whether the folder exists.
func (s *Server) FolderExists(projectID, environment, secretPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.folders[folderKey(projectID, environment, secretPath)]
}

// CreateSecret stores a secret, creating its folder if needed, and returns its ID.
func (s *Server) CreateSecret(projectID, environment, secretPath, key, value string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createFolderLocked(projectID, environment, secretPath)
	return s.createSecretLocked(projectID, environment, secretPath, key, value).ID
}

// DeleteSecret removes a secret as if someone deleted it in Infisical.
func (s *Server) DeleteSecret(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, id)
}

// Secrets returns the secrets in a folder, keyed by secret key.
func (s *Server) Secrets(projectID, environment, secretPath string) map[string]Secret {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]Secret{}
	for _, secret := range s.secretsAtLocked(projectID, environment, secretPath) {
		result[secret.Key] = *secret
	}
	return result
}

// Requests returns the requests received so far as "METHOD /path".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// CountRequests returns how many requests matched "METHOD /path".
func (s *Server) CountRequests(request string) int {
	count := 0
	for _, r := range s.Requests() {
		if r == request {
			count++
		}
	}
	return count
}

// Stall makes every request wait without a response until the client gives up, as an
// unresponsive Infisical would. It returns a function that ends the stall.
func (s *Server) Stall() (release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stall := make(chan struct{})
	s.stall = stall

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.stall = nil
			s.mu.Unlock()
			close(stall)
		})
	}
}

// ResetRequests forgets the recorded requests.
func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *Server) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		stall := s.stall
		s.mu.Unlock()

		if stall != nil {
			select {
			case <-r.Context().Done():
				return
			case <-stall:
			}
		}
		next.ServeHTTP(w, r)
	})
}

type secretJSON struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	SecretKey   string `json:"secretKey"`
	SecretValue string `json:"secretValue"`
	Environment string `json:"environment"`
	SecretPath  string `json:"secretPath"`
	Workspace   string `json:"workspace"`
}

func toJSON(secret *Secret) secretJSON {
	return secretJSON{
		ID:          secret.ID,
		Type:        "shared",
		SecretKey:   secret.Key,
		SecretValue: secret.Value,
		Environment: secret.Environment,
		SecretPath:  secret.SecretPath,
		Workspace:   secret.ProjectID,
	}
}

type locationBody struct {
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment"`
	SecretPath  string `json:"secretPath"`
}

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	projectID, env, secretPath := query.Get("projectId"), query.Get("environment"), cleanPath(query.Get("secretPath"))

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.folders[folderKey(projectID, env, secretPath)] {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Folder with path '%s' in environment '%s' was not found. Please ensure the environment slug and secret path is correct.", secretPath, env))
		return
	}

	secrets := []secretJSON{}
	for _, secret := range s.secretsAtLocked(projectID, env, secretPath) {
		secrets = append(secrets, toJSON(secret))
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets, "imports": []any{}})
}

func (s *Server) upsertSecrets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		locationBody
		Mode    string `json:"mode"`
		Secrets []struct {
			SecretKey   string `json:"secretKey"`
			SecretValue string `json:"secretValue"`
		} `json:"secrets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Mode != "upsert" || len(body.Secrets) == 0 {
		writeError(w, http.StatusBadRequest, "invalid upsert request")
		return
	}
	secretPath := cleanPath(body.SecretPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	key := folderKey(body.ProjectID, body.Environment, secretPath)
	if s.approvalPaths[key] {
		writeJSON(w, http.StatusOK, map[string]any{"approval": map[string]any{"id": "approval-1"}})
		return
	}
	if !s.folders[key] {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Folder with path '%s' in environment '%s' not found", secretPath, body.Environment))
		return
	}

	existing := map[string]*Secret{}
	for _, secret := range s.secretsAtLocked(body.ProjectID, body.Environment, secretPath) {
		existing[secret.Key] = secret
	}

	stored := []secretJSON{}
	for _, input := range body.Secrets {
		secret, found := existing[input.SecretKey]
		if found {
			secret.Value = normalizeValue(input.SecretValue)
		} else {
			secret = s.createSecretLocked(body.ProjectID, body.Environment, secretPath, input.SecretKey, input.SecretValue)
		}
		stored = append(stored, toJSON(secret))
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": stored})
}

func (s *Server) deleteSecrets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		locationBody
		Secrets []struct {
			SecretKey string `json:"secretKey"`
		} `json:"secrets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Secrets) == 0 {
		writeError(w, http.StatusBadRequest, "invalid delete request")
		return
	}
	secretPath := cleanPath(body.SecretPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	key := folderKey(body.ProjectID, body.Environment, secretPath)
	if s.approvalPaths[key] {
		writeJSON(w, http.StatusOK, map[string]any{"approval": map[string]any{"id": "approval-1"}})
		return
	}
	if !s.folders[key] {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Folder with path '%s' in environment '%s' not found", secretPath, body.Environment))
		return
	}

	existing := map[string]*Secret{}
	for _, secret := range s.secretsAtLocked(body.ProjectID, body.Environment, secretPath) {
		existing[secret.Key] = secret
	}

	var missing []string
	for _, input := range body.Secrets {
		if _, found := existing[input.SecretKey]; !found {
			missing = append(missing, input.SecretKey)
		}
	}
	if len(missing) > 0 {
		writeError(w, http.StatusNotFound, fmt.Sprintf("One or more secrets does not exist: %s", strings.Join(missing, ", ")))
		return
	}

	deleted := []secretJSON{}
	for _, input := range body.Secrets {
		secret := existing[input.SecretKey]
		deleted = append(deleted, toJSON(secret))
		delete(s.secrets, secret.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": deleted})
}

func (s *Server) getSecretByID(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	secret, found := s.secrets[r.PathValue("id")]
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Secret with ID '%s' not found", r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": toJSON(secret)})
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID   string `json:"projectId"`
		Environment string `json:"environment"`
		Path        string `json:"path"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid folder request")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.folders[folderKey(body.ProjectID, body.Environment, "/")] {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Environment with slug '%s' in project with ID '%s' not found", body.Environment, body.ProjectID))
		return
	}

	parent := cleanPath(body.Path)
	full := cleanPath(path.Join(parent, body.Name))
	if s.folders[folderKey(body.ProjectID, body.Environment, full)] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Folder with name '%s' already exists in path '%s'", body.Name, parent))
		return
	}

	s.createFolderLocked(body.ProjectID, body.Environment, full)
	writeJSON(w, http.StatusOK, map[string]any{"folder": map[string]any{"name": body.Name, "path": full}})
}

func (s *Server) getProjectBySlug(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	projectID, found := s.projects[r.PathValue("slug")]
	if !found {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": projectID, "slug": r.PathValue("slug")})
}

func (s *Server) createFolderLocked(projectID, environment, secretPath string) {
	current := "/"
	s.folders[folderKey(projectID, environment, current)] = true
	for _, segment := range strings.Split(strings.Trim(cleanPath(secretPath), "/"), "/") {
		if segment == "" {
			continue
		}
		current = path.Join(current, segment)
		s.folders[folderKey(projectID, environment, current)] = true
	}
}

func (s *Server) createSecretLocked(projectID, environment, secretPath, key, value string) *Secret {
	s.nextID++
	secret := &Secret{
		ID:          fmt.Sprintf("secret-%d", s.nextID),
		ProjectID:   projectID,
		Environment: environment,
		SecretPath:  cleanPath(secretPath),
		Key:         key,
		Value:       normalizeValue(value),
	}
	s.secrets[secret.ID] = secret
	return secret
}

func (s *Server) secretsAtLocked(projectID, environment, secretPath string) []*Secret {
	var result []*Secret
	for _, secret := range s.secrets {
		if secret.ProjectID == projectID && secret.Environment == environment && secret.SecretPath == cleanPath(secretPath) {
			result = append(result, secret)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

// normalizeValue mirrors how Infisical stores values: trimmed, keeping a trailing newline.
func normalizeValue(value string) string {
	if strings.HasSuffix(value, "\n") {
		return strings.TrimSpace(value) + "\n"
	}
	return strings.TrimSpace(value)
}

func cleanPath(secretPath string) string {
	return path.Clean("/" + secretPath)
}

func folderKey(projectID, environment, secretPath string) string {
	return projectID + "/" + environment + ":" + cleanPath(secretPath)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"statusCode": status, "message": message, "error": http.StatusText(status)})
}
