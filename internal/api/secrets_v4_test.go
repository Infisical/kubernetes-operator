package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/go-resty/resty/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/fakeinfisical"
)

const (
	projectID   = "project-1"
	environment = "dev"
)

var location = api.SecretLocation{ProjectID: projectID, Environment: environment, SecretPath: "/app"}

// respondWith starts a server that answers every request with the given status, headers and body.
func respondWith(status int, headers map[string]string, body string) *resty.Client {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key, value := range headers {
			w.Header().Set(key, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	DeferCleanup(server.Close)
	return resty.New().SetBaseURL(server.URL)
}

func newFakeInfisical() (*fakeinfisical.Server, *resty.Client) {
	server := fakeinfisical.New([]string{projectID}, []string{environment})
	DeferCleanup(server.Close)
	return server, resty.New().SetBaseURL(server.URL)
}

var _ = Describe("ListSecrets", func() {
	It("only sends expandSecretReferences when expansion is skipped", func() {
		var queries []url.Values
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			queries = append(queries, r.URL.Query())
			_, _ = w.Write([]byte(`{"secrets":[]}`))
		}))
		DeferCleanup(server.Close)
		client := resty.New().SetBaseURL(server.URL)

		_, err := api.ListSecrets(client, api.ListSecretsRequest{ProjectId: projectID, EnvironmentSlug: environment, SecretPath: "/"})
		Expect(err).NotTo(HaveOccurred())
		_, err = api.ListSecrets(client, api.ListSecretsRequest{ProjectId: projectID, EnvironmentSlug: environment, SecretPath: "/", SkipReferenceExpansion: true})
		Expect(err).NotTo(HaveOccurred())

		Expect(queries[0].Has("expandSecretReferences")).To(BeFalse())
		Expect(queries[1].Get("expandSecretReferences")).To(Equal("false"))
	})

	DescribeTable("maps unsuccessful responses to the same errors as before",
		func(status int, headers map[string]string, body string, check func(error)) {
			_, err := api.ListSecrets(respondWith(status, headers, body), api.ListSecretsRequest{ProjectId: projectID, EnvironmentSlug: environment, SecretPath: "/"})
			Expect(err).To(HaveOccurred())
			check(err)
		},
		Entry("401", http.StatusUnauthorized, nil, `{}`, func(err error) {
			Expect(err).To(MatchError(api.ErrUnauthorized))
			Expect(err.Error()).To(Equal("Unauthorized access: status 401"))
		}),
		Entry("403", http.StatusForbidden, nil, `{}`, func(err error) {
			Expect(err).To(MatchError(api.ErrUnauthorized))
		}),
		Entry("400", http.StatusBadRequest, nil, `{"message":"bad"}`, func(err error) {
			Expect(err).To(MatchError(api.ErrBadRequest))
			Expect(err.Error()).To(Equal(`Bad request: failed to list secrets [body={"message":"bad"}]`))
		}),
		Entry("404", http.StatusNotFound, nil, `{"message":"nope"}`, func(err error) {
			Expect(err).To(MatchError(api.ErrNotFound))
			Expect(err.Error()).To(Equal(`failed to list secrets [status=404, body={"message":"nope"}]`))
		}),
		Entry("500", http.StatusInternalServerError, nil, `oops`, func(err error) {
			Expect(err.Error()).To(Equal("failed to list secrets [status=500, body=oops]"))
		}),
		Entry("429 with Retry-After", http.StatusTooManyRequests, map[string]string{"Retry-After": "7"}, `{}`, func(err error) {
			var rateLimitErr *api.TooManyRequestsError
			Expect(err).To(BeAssignableToTypeOf(rateLimitErr))
			Expect(err.(*api.TooManyRequestsError).RetryAfter).To(Equal(7))
		}),
		Entry("429 with an unparsable Retry-After", http.StatusTooManyRequests, map[string]string{"Retry-After": "soon"}, `{}`, func(err error) {
			Expect(err.(*api.TooManyRequestsError).RetryAfter).To(Equal(60))
		}),
		Entry("429 without Retry-After", http.StatusTooManyRequests, nil, `{}`, func(err error) {
			Expect(err.Error()).To(Equal("failed to list secrets due to rate limit"))
		}),
	)
})

var _ = Describe("IsFolderNotFound", func() {
	It("matches the missing folder error of listing and of batch updates", func() {
		server, client := newFakeInfisical()

		_, err := api.ListSecrets(client, api.ListSecretsRequest{ProjectId: projectID, EnvironmentSlug: environment, SecretPath: "/missing"})
		Expect(api.IsFolderNotFound(err)).To(BeTrue())

		_, err = api.UpsertSecrets(client, api.UpsertSecretsRequest{
			SecretLocation: api.SecretLocation{ProjectID: projectID, Environment: environment, SecretPath: "/missing"},
			Secrets:        []api.UpsertSecret{{SecretKey: "KEY", SecretValue: "value"}},
		})
		Expect(api.IsFolderNotFound(err)).To(BeTrue())
		Expect(server.FolderExists(projectID, environment, "/missing")).To(BeFalse())
	})

	It("doesn't match other not found errors", func() {
		_, client := newFakeInfisical()

		_, err := api.GetSecretByID(client, "unknown")
		Expect(err).To(MatchError(api.ErrNotFound))
		Expect(api.IsFolderNotFound(err)).To(BeFalse())
	})
})

var _ = Describe("UpsertSecrets", func() {
	It("creates missing secrets and updates existing ones", func() {
		server, client := newFakeInfisical()
		existingID := server.CreateSecret(projectID, environment, "/app", "EXISTING", "old")

		stored, err := api.UpsertSecrets(client, api.UpsertSecretsRequest{
			SecretLocation: location,
			Secrets: []api.UpsertSecret{
				{SecretKey: "EXISTING", SecretValue: "new"},
				{SecretKey: "CREATED", SecretValue: "value"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(2))
		Expect(stored[0].ID).To(Equal(existingID))
		Expect(stored[1].ID).NotTo(BeEmpty())

		secrets := server.Secrets(projectID, environment, "/app")
		Expect(secrets["EXISTING"].Value).To(Equal("new"))
		Expect(secrets["CREATED"].Value).To(Equal("value"))
	})

	It("returns ErrApprovalRequired when the change was submitted for approval", func() {
		server, client := newFakeInfisical()
		server.RequireApproval(projectID, environment, "/app")

		_, err := api.UpsertSecrets(client, api.UpsertSecretsRequest{
			SecretLocation: location,
			Secrets:        []api.UpsertSecret{{SecretKey: "KEY", SecretValue: "value"}},
		})
		Expect(err).To(MatchError(api.ErrApprovalRequired))
	})
})

var _ = Describe("DeleteSecrets", func() {
	It("sends the secrets to delete in the request body", func() {
		server, client := newFakeInfisical()
		server.CreateSecret(projectID, environment, "/app", "KEEP", "value")
		server.CreateSecret(projectID, environment, "/app", "DELETE", "value")

		err := api.DeleteSecrets(client, api.DeleteSecretsRequest{
			SecretLocation: location,
			Secrets:        []api.DeleteSecret{{SecretKey: "DELETE"}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(server.Secrets(projectID, environment, "/app")).To(HaveKey("KEEP"))
		Expect(server.Secrets(projectID, environment, "/app")).NotTo(HaveKey("DELETE"))
	})

	It("returns ErrApprovalRequired when the change was submitted for approval", func() {
		server, client := newFakeInfisical()
		server.CreateSecret(projectID, environment, "/app", "KEY", "value")
		server.RequireApproval(projectID, environment, "/app")

		err := api.DeleteSecrets(client, api.DeleteSecretsRequest{
			SecretLocation: location,
			Secrets:        []api.DeleteSecret{{SecretKey: "KEY"}},
		})
		Expect(err).To(MatchError(api.ErrApprovalRequired))
	})
})

var _ = Describe("GetSecretByID", func() {
	It("returns where the secret is stored", func() {
		server, client := newFakeInfisical()
		id := server.CreateSecret(projectID, environment, "/app/nested", "KEY", "value")

		secret, err := api.GetSecretByID(client, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(secret.ProjectID).To(Equal(projectID))
		Expect(secret.Environment).To(Equal(environment))
		Expect(secret.SecretPath).To(Equal("/app/nested"))
		Expect(secret.SecretKey).To(Equal("KEY"))
	})
})

var _ = Describe("CreateFolder", func() {
	It("creates the folder and its missing parents", func() {
		server, client := newFakeInfisical()

		Expect(api.CreateFolder(client, api.CreateFolderRequest{ProjectID: projectID, Environment: environment, Path: "/a/b", Name: "c"})).To(Succeed())
		Expect(server.FolderExists(projectID, environment, "/a")).To(BeTrue())
		Expect(server.FolderExists(projectID, environment, "/a/b/c")).To(BeTrue())
	})

	It("treats an existing folder as success", func() {
		server, client := newFakeInfisical()
		server.CreateFolder(projectID, environment, "/app")

		Expect(api.CreateFolder(client, api.CreateFolderRequest{ProjectID: projectID, Environment: environment, Path: "/", Name: "app"})).To(Succeed())
	})

	It("returns other errors", func() {
		_, client := newFakeInfisical()

		err := api.CreateFolder(client, api.CreateFolderRequest{ProjectID: projectID, Environment: "unknown", Path: "/", Name: "app"})
		Expect(err).To(MatchError(api.ErrNotFound))
	})
})
