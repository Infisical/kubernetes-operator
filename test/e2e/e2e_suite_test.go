package e2e

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/operator"
)

var (
	testInfra   *infra.Stack
	testManager *operator.Manager
)

func TestE2E(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "true" {
		t.Skip("set INTEGRATION_TESTS=true to run e2e tests")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var _ = BeforeSuite(func() {
	testInfra = infra.New().WithNodeJSApi().MustStart()

	var err error
	testManager, err = operator.Install(operator.InstallOpts{
		HostAPIURL: testInfra.NodeJS().URL(),
	})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testManager != nil {
		testManager.Stop()
	}
	if testInfra != nil {
		testInfra.Stop()
	}
})
