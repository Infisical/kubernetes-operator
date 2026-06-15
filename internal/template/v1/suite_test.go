package v1_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTemplateV1(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Template V1 Suite")
}
