package api_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/api"
)

var _ = Describe("FindProjectBySlug", func() {
	It("returns the project", func() {
		server, client := newFakeInfisical()
		server.AddProjectSlug("my-project", projectID)

		project, err := api.FindProjectBySlug(client, "my-project")

		Expect(err).NotTo(HaveOccurred())
		Expect(project.ID).To(Equal(projectID))
	})

	DescribeTable("maps unsuccessful responses to errors",
		func(status int, headers map[string]string, body string, check func(error)) {
			_, err := api.FindProjectBySlug(respondWith(status, headers, body), "my-project")
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
		Entry("404", http.StatusNotFound, nil, `{"message":"Project not found"}`, func(err error) {
			Expect(err.Error()).To(Equal(`project with slug "my-project" not found`))
		}),
		Entry("500", http.StatusInternalServerError, nil, `oops`, func(err error) {
			Expect(err.Error()).To(Equal("failed to find project by slug [status=500, body=oops]"))
		}),
		Entry("429 with Retry-After", http.StatusTooManyRequests, map[string]string{"Retry-After": "7"}, `{}`, func(err error) {
			var rateLimitErr *api.TooManyRequestsError
			Expect(err).To(BeAssignableToTypeOf(rateLimitErr))
			Expect(err.(*api.TooManyRequestsError).RetryAfter).To(Equal(7))
		}),
	)
})
