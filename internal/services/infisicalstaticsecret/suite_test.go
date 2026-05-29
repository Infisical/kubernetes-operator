package infisicalstaticsecret_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInfisicalStaticSecret(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "InfisicalStaticSecret Suite")
}
