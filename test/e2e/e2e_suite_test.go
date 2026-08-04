package e2e

import (
	"net"
	"net/url"
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

	gateway, err := operator.KindIPv4Gateway("kind")
	Expect(err).NotTo(HaveOccurred())

	err = testInfra.StartTLSProxy(infra.TLSBundleOpts{
		DNSNames:    []string{"tls-proxy"},
		IPAddresses: []net.IP{net.ParseIP(gateway)},
	})
	Expect(err).NotTo(HaveOccurred())

	tlsProxyURL, err := url.Parse(testInfra.Nginx().URL())
	Expect(err).NotTo(HaveOccurred())

	testManager, err = operator.Install(operator.InstallOpts{
		HostAPIURL:   testInfra.NodeJS().URL(),
<<<<<<< Updated upstream
		InClusterURL: testInfra.NodeJS().InClusterURL(),
=======
		TLSProxyPort: tlsProxyURL.Port(),
>>>>>>> Stashed changes
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
