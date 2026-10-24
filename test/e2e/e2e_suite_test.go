package e2e

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/operator"
)

// staticSecretWorkers is the number of InfisicalStaticSecret workers the operator runs with
// during e2e tests. It is above the default of 1 so the whole suite exercises concurrent
// reconciliation.
const staticSecretWorkers = 2

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
	testInfra = infra.New().WithNodeJSApi().WithNetwork(operator.KindNetwork).MustStart()

	var err error
	testManager, err = operator.Install(operator.InstallOpts{
		HostAPIURL:               testInfra.NodeJS().URL(),
		APIContainerID:           testInfra.NodeJS().ContainerID(),
		APIContainerPort:         "8080",
		DefaultConnection:        true,
		DefaultConnectionAddress: testInfra.NodeJS().URL() + "/api",
		MaxConcurrency:           staticSecretWorkers,
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
