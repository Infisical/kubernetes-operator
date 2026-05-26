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

	if err := operator.InstallCRDs(); err != nil {
		testInfra.Stop()
		log.Fatalf("install CRDs: %v", err)
	}

	var err error
	testManager, err = operator.Start(testInfra.NodeJS().URL())
	if err != nil {
		testInfra.Stop()
		log.Fatalf("start operator: %v", err)
	}

	code := m.Run()

	testManager.Stop()
	testInfra.Stop()
	os.Exit(code)
}
