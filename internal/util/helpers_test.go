package util_test

import (
	"strings"

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

var _ = Describe("ComputeManagedSecretAnnotation", func() {

	const template = "secrets.infisical.com/managed-secret.%s"

	type TestCase struct {
		name       string
		secretName string
	}

	DescribeTable("never surpasses 63 characters",
		func(tc TestCase) {
			annotation := util.ComputeManagedSecretAnnotation(template, tc.secretName)
			annotationName := strings.Split(annotation, "/")[1]
			Expect(len(annotationName)).To(BeNumerically("<", 64))
		},
		Entry("input is less than 63 characters long", TestCase{
			name:       "input is less than 63 characters long",
			secretName: "short-name",
		}),
		Entry("input is 63 characters long", TestCase{
			name:       "input is 63 characters long",
			secretName: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKL",
		}),
		Entry("input is longer than 63 characters", TestCase{
			name:       "input is longer than 63 characters",
			secretName: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKL_LONG_STRING",
		}),
	)
})
