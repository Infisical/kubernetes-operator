package infisicalconnection

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/go-logr/logr"
	"github.com/go-resty/resty/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewInfisicalConnectionHandler(client client.Client, scheme *runtime.Scheme, isNamespaceScoped bool) *InfisicalConnectionHandler {
	return &InfisicalConnectionHandler{
		Client:            client,
		Scheme:            scheme,
		IsNamespaceScoped: isNamespaceScoped,
	}
}

type InfisicalConnectionHandler struct {
	client.Client
	Scheme            *runtime.Scheme
	Random            *rand.Rand
	IsNamespaceScoped bool
}

func (h *InfisicalConnectionHandler) getInfisicalCaCertificate(ctx context.Context, caRef *v1beta1.SecretReference) (string, error) {
	secret, err := util.GetKubeSecretByNamespacedName(ctx, h.Client, types.NamespacedName{
		Namespace: caRef.Namespace,
		Name:      caRef.Name,
	})
	if err != nil {
		if util.IsNamespaceScopedError(err, h.IsNamespaceScoped) {
			return "", fmt.Errorf("unable to fetch CA certificate secret: operator is namespace-scoped and cannot read secrets outside its namespace [err=%w]", err)
		}
		return "", fmt.Errorf("unable to fetch CA certificate secret [err=%w]", err)
	}

	caCert := string(secret.Data[caRef.Key])
	if caCert == "" {
		return "", fmt.Errorf("CA certificate key %q is empty in secret %s/%s", caRef.Key, caRef.Namespace, caRef.Name)
	}

	return caCert, nil
}

type apiStatusResponse struct {
	Message string `json:"message"`
}

func (h *InfisicalConnectionHandler) ResolveAddress(connection *v1beta1.InfisicalConnection) string {
	hostAPIFromEnv := os.Getenv("INFISICAL_HOST_API")
	if connection == nil {
		return hostAPIFromEnv
	}

	return cmp.Or(connection.Spec.Address, hostAPIFromEnv)
}

func (h *InfisicalConnectionHandler) GetInfisicalConnection(ctx context.Context, namespacedName types.NamespacedName) (*v1beta1.InfisicalConnection, error) {
	var connection v1beta1.InfisicalConnection
	err := h.Client.Get(ctx, namespacedName, &connection)
	if err != nil {
		if util.IsNamespaceScopedError(err, h.IsNamespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "InfisicalConnection")
		}
		return nil, err
	}

	if h.ResolveAddress(&connection) == "" {
		return &connection, fmt.Errorf("%w: .spec.address is empty and INFISICAL_HOST_API env var is not set", model.ErrValidation)
	}

	return &connection, nil
}

func (h *InfisicalConnectionHandler) TestConnection(ctx context.Context, infisicalConnection *v1beta1.InfisicalConnection) error {
	hostURL := util.AppendAPIEndpoint(h.ResolveAddress(infisicalConnection))

	httpClient := resty.New().
		SetBaseURL(hostURL).
		SetHeader("User-Agent", constants.USER_AGENT_NAME).
		SetTimeout(30 * time.Second)

	if tlsConfig := infisicalConnection.Spec.TLS; tlsConfig != nil && tlsConfig.CaCertificate != nil {
		caCert, err := h.getInfisicalCaCertificate(ctx, infisicalConnection.Spec.TLS.CaCertificate)
		if err != nil {
			return fmt.Errorf("failed to resolve CA certificate: %w", err)
		}

		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("failed to load system CA pool: %w", err)
		}

		if ok := caCertPool.AppendCertsFromPEM([]byte(caCert)); !ok {
			return fmt.Errorf("failed to parse CA certificate from secret %s/%s", infisicalConnection.Spec.TLS.CaCertificate.Namespace, infisicalConnection.Spec.TLS.CaCertificate.Name)
		}

		httpClient.SetTLSClientConfig(&tls.Config{
			RootCAs: caCertPool,
		})
	}

	resp, err := httpClient.R().SetContext(ctx).Get("/status")
	if err != nil {
		return fmt.Errorf("unable to reach Infisical at %s: %w", hostURL, err)
	}

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("unexpected status from Infisical at %s/status: %s", hostURL, resp.Status())
	}

	var response = apiStatusResponse{}
	err = json.Unmarshal(resp.Body(), &response)
	if err != nil {
		return fmt.Errorf("unexpected status from Infisical at %s/status: invalid JSON", hostURL)
	}

	if response.Message != "Ok" {
		return fmt.Errorf("unexpected status from Infisical at %s/status: expected Ok, got %q", hostURL, response.Message)
	}

	return nil
}

func (r *InfisicalConnectionHandler) SetReconcileConditionStatus(ctx context.Context, logger logr.Logger, infisicalConnection *v1beta1.InfisicalConnection, errorToConditionOn error) {
	if infisicalConnection.Status.Conditions == nil {
		infisicalConnection.Status.Conditions = []metav1.Condition{}
	}

	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalConnection.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/IsReady",
			Status:             metav1.ConditionTrue,
			Reason:             "Ok",
			Message:            "InfisicalConnection is ready to be used.",
			ObservedGeneration: infisicalConnection.Generation,
		})
	} else {
		meta.SetStatusCondition(&infisicalConnection.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/IsReady",
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            fmt.Sprintf("InfisicalConnection is not ready to be used due to an error: %v", errorToConditionOn),
			ObservedGeneration: infisicalConnection.Generation,
		})
	}

	err := r.Client.Status().Update(ctx, infisicalConnection)
	if err != nil {
		logger.Error(err, "Could not set condition for IsReady")
	}
}
