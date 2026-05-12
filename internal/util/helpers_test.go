package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/util"
)

var _ = Describe("AppendAPIEndpoint", func() {

	type TestCase struct {
		name   string
		input  string
		output string
	}

	DescribeTable("appending /api to the address",
		func(tc TestCase) {
			Expect(util.AppendAPIEndpoint(tc.input)).To(Equal(tc.output))
		},
		Entry("already ends with /api", TestCase{
			name:   "already ends with /api",
			input:  "https://app.infisical.com/api",
			output: "https://app.infisical.com/api",
		}),
		Entry("ends with trailing slash", TestCase{
			name:   "ends with trailing slash",
			input:  "https://app.infisical.com/",
			output: "https://app.infisical.com/api",
		}),
		Entry("no trailing slash", TestCase{
			name:   "no trailing slash",
			input:  "https://app.infisical.com",
			output: "https://app.infisical.com/api",
		}),
		Entry("path contains /api but does not end with it", TestCase{
			name:   "path contains /api but does not end with it",
			input:  "https://app.infisical.com/api/v1",
			output: "https://app.infisical.com/api/v1/api",
		}),
		Entry("bare host without trailing slash", TestCase{
			name:   "bare host without trailing slash",
			input:  "http://localhost:8080",
			output: "http://localhost:8080/api",
		}),
		Entry("bare host with trailing slash", TestCase{
			name:   "bare host with trailing slash",
			input:  "http://localhost:8080/",
			output: "http://localhost:8080/api",
		}),
		Entry("ends with /api/", TestCase{
			name:   "ends with /api/",
			input:  "https://app.infisical.com/api/",
			output: "https://app.infisical.com/api",
		}),
	)
})
