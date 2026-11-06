package infisicalstaticsecretpush_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInfisicalStaticSecretPush(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "InfisicalStaticSecretPush Suite")
}
