package infisicalsecret

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	"github.com/Infisical/infisical/k8-operator/internal/config"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func countingServer(hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
}

// Another controller can overwrite config.API_HOST_URL between SetupAPIConfig and the moment the
// cached client is built. The client must still use the host resolved for this resource.
func TestCachedClientUsesTheResourceHostNotTheSharedGlobal(t *testing.T) {
	var hitsA, hitsB int32
	srvA, srvB := countingServer(&hitsA), countingServer(&hitsB)
	defer srvA.Close()
	defer srvB.Close()
	old := config.API_HOST_URL
	defer func() { config.API_HOST_URL = old }()

	cr := v1alpha1.InfisicalSecret{ObjectMeta: metav1.ObjectMeta{UID: types.UID("cr-a")}}
	cr.Spec.HostAPI = srvA.URL

	h := NewInfisicalSecretHandler(nil, nil, false)
	if err := h.SetupAPIConfig(cr, config.InfisicalGlobalConfig{HostAPI: srvB.URL + "/api"}); err != nil {
		t.Fatal(err)
	}
	config.API_HOST_URL = srvB.URL + "/api" // another controller reconciles a resource on host B

	r := &InfisicalSecretReconciler{HostAPI: h.hostAPI}
	vars := r.getResourceVariables(cr, map[string]util.ResourceVariables{})
	defer vars.CancelCtx()
	_, _ = vars.InfisicalClient.Auth().UniversalAuthLogin("id", "secret")

	if atomic.LoadInt32(&hitsA) == 0 || atomic.LoadInt32(&hitsB) != 0 {
		t.Fatalf("client called host A %d times and host B %d times, want only A", hitsA, hitsB)
	}
}
