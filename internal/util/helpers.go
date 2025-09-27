package util

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Infisical/infisical/k8-operator/internal/config"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/go-resty/resty/v2"
)

func ConvertIntervalToDuration(resyncInterval *string) (time.Duration, error) {

	if resyncInterval == nil || *resyncInterval == "" {
		return 0, nil
	}

	length := len(*resyncInterval)
	if length < 2 {
		return 0, fmt.Errorf("invalid format")
	}

	unit := (*resyncInterval)[length-1:]
	numberPart := (*resyncInterval)[:length-1]

	number, err := strconv.Atoi(numberPart)
	if err != nil {
		return 0, err
	}

	switch unit {
	case "s":
		if number < 5 {
			return 0, fmt.Errorf("resync interval must be at least 5 seconds")
		}
		return time.Duration(number) * time.Second, nil
	case "m":
		return time.Duration(number) * time.Minute, nil
	case "h":
		return time.Duration(number) * time.Hour, nil
	case "d":
		return time.Duration(number) * 24 * time.Hour, nil
	case "w":
		return time.Duration(number) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid time unit")
	}
}

func AppendAPIEndpoint(address string) string {
	if strings.HasSuffix(address, "/api") {
		return address
	}
	if address[len(address)-1] == '/' {
		return address + "api"
	}
	return address + "/api"
}

func IsNamespaceScopedError(err error, isNamespaceScoped bool) bool {
	return isNamespaceScoped && err != nil && strings.Contains(err.Error(), "unknown namespace for the cache")
}

func CreateRestyClient(options model.CreateRestyClientOptions) (*resty.Client, error) {

	httpClient := resty.New()

	if options.AccessToken != "" {
		httpClient.SetAuthToken(options.AccessToken)
	}

	// no nil check needed when using range on a map
	for key, value := range options.Headers {
		httpClient.SetHeader(key, value)
	}
	httpClient.SetHeader("User-Agent", constants.USER_AGENT_NAME)

	if config.API_CA_CERTIFICATE != "" {
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("failed to load system root CA pool: %v", err)
		}

		if ok := caCertPool.AppendCertsFromPEM([]byte(config.API_CA_CERTIFICATE)); !ok {
			return nil, fmt.Errorf("failed to append CA certificate")
		}

		tlsConfig := &tls.Config{
			RootCAs: caCertPool,
		}

		httpClient.SetTLSClientConfig(tlsConfig)
	}

	return httpClient, nil
}
