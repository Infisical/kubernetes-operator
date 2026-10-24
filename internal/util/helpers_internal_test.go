package util

import (
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/model"
)

var _ = Describe("CreateRestyClient response header timeout", func() {
	const timeout = 200 * time.Millisecond

	BeforeEach(func() {
		original := responseHeaderTimeout
		responseHeaderTimeout = timeout
		DeferCleanup(func() { responseHeaderTimeout = original })
	})

	It("fails when the server never sends response headers", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		DeferCleanup(server.Close)

		client, err := CreateRestyClient(model.CreateRestyClientOptions{})
		Expect(err).NotTo(HaveOccurred())

		start := time.Now()
		_, err = client.R().Get(server.URL)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timeout awaiting response headers"))
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	It("does not cut off a streamed body that outlives the timeout", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			for _, chunk := range []string{"first\n", "second\n"} {
				time.Sleep(2 * timeout)
				_, _ = io.WriteString(w, chunk)
				w.(http.Flusher).Flush()
			}
		}))
		DeferCleanup(server.Close)

		client, err := CreateRestyClient(model.CreateRestyClientOptions{})
		Expect(err).NotTo(HaveOccurred())

		resp, err := client.R().SetDoNotParseResponse(true).Get(server.URL)
		Expect(err).NotTo(HaveOccurred())
		body := resp.RawBody()
		defer body.Close()

		data, err := io.ReadAll(body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("first\nsecond\n"))
	})
})
