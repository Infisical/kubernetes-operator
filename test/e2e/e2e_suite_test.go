package e2e

import (
	"log"
	"os"
	"testing"

	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/operator"
)

var (
	testInfra   *infra.Stack
	testManager *operator.Manager
)

func TestMain(m *testing.M) {
	testInfra = infra.New().WithNodeJSApi().MustStart()

	var err error
	testManager, err = operator.Install(operator.InstallOpts{
		HostAPIURL: testInfra.NodeJS().URL(),
	})
	if err != nil {
		testInfra.Stop()
		log.Fatalf("install operator: %v", err)
	}

	code := m.Run()

	testManager.Stop()
	testInfra.Stop()
	os.Exit(code)
}
