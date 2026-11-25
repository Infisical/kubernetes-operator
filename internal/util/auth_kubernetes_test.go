package util_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	infisicalSdk "github.com/infisical/go-sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

var _ = Describe("Kubernetes auth with an auto-created service account token", func() {
	var (
		mu        sync.Mutex
		minted    int
		loginJWTs []string
		client    infisicalSdk.InfisicalClientInterface
		input     util.SecretAuthInput
	)

	logins := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), loginJWTs...)
	}

	BeforeEach(func() {
		minted = 0
		loginJWTs = nil

		kubeAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces/default/serviceaccounts/operator-sa/token" {
				http.NotFound(w, r)
				return
			}
			mu.Lock()
			minted++
			token := fmt.Sprintf("sa-token-%d", minted)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":       "TokenRequest",
				"apiVersion": "authentication.k8s.io/v1",
				"status":     map[string]any{"token": token, "expirationTimestamp": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)},
			})
		}))
		DeferCleanup(kubeAPI.Close)

		kubeconfig := filepath.Join(GinkgoT().TempDir(), "kubeconfig")
		Expect(os.WriteFile(kubeconfig, []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: %s
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
current-context: fake
users:
- name: fake
  user:
    token: fake
`, kubeAPI.URL)), 0o600)).To(Succeed())
		GinkgoT().Setenv("KUBECONFIG", kubeconfig)

		infisicalAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/auth/kubernetes-auth/login":
				var body struct {
					JWT string `json:"jwt"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				loginJWTs = append(loginJWTs, body.JWT)
				n := len(loginJWTs)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"accessToken":       fmt.Sprintf("access-%d", n),
					"expiresIn":         1,
					"accessTokenMaxTTL": 0,
					"tokenType":         "Bearer",
				})
			default:
				http.Error(w, `{"message":"Your token has expired. Please re-authenticate."}`, http.StatusUnauthorized)
			}
		}))
		DeferCleanup(infisicalAPI.Close)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		client = infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{SiteUrl: infisicalAPI.URL, SilentMode: true})

		input = util.SecretAuthInput{
			Type: util.SecretCrd.INFISICAL_SECRET,
			Secret: v1alpha1.InfisicalSecret{Spec: v1alpha1.InfisicalSecretSpec{Authentication: v1alpha1.Authentication{
				KubernetesAuth: v1alpha1.KubernetesAuthDetails{
					IdentityID:                    "identity-id",
					ServiceAccountRef:             v1alpha1.KubernetesServiceAccountRef{Namespace: "default", Name: "operator-sa"},
					AutoCreateServiceAccountToken: true,
				},
			}}},
		}
	})

	It("never replays the short-lived token, and logs in again with a fresh one", func() {
		details, err := util.HandleKubernetesAuth(context.Background(), nil, input, client, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(logins()).To(Equal([]string{"sa-token-1"}))
		Expect(details.NeedsAuthentication()).To(BeFalse())

		Consistently(logins, 2500*time.Millisecond, 100*time.Millisecond).Should(Equal([]string{"sa-token-1"}))
		Expect(details.NeedsAuthentication()).To(BeTrue())

		_, err = util.HandleKubernetesAuth(context.Background(), nil, input, client, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(logins()).To(Equal([]string{"sa-token-1", "sa-token-2"}))
		Expect(client.Auth().GetAccessToken()).To(Equal("access-2"))
	})
})
