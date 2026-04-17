package config

import (
	"fmt"
	"io"
	"os"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
)

type SDKLogWriter string

const (
	Stderr SDKLogWriter = "stderr"
	Stdout SDKLogWriter = "stdout"
)

func (w SDKLogWriter) Writer() io.Writer {
	switch w {
	case Stderr:
		return os.Stderr
	case Stdout:
		return os.Stdout
	default:
		return os.Stderr
	}
}

func parseSDKLogWriter(s string) SDKLogWriter {
	if s == "" {
		return Stderr
	}
	w := SDKLogWriter(s)
	switch w {
	case Stderr, Stdout:
		return w
	default:
		return Stderr
	}
}

type InfisicalGlobalConfig struct {
	HostAPI string              `json:"hostAPI"`
	TLS     *v1alpha1.TLSConfig `json:"tls,omitempty"`
}

func GetDefaultHostAPI() string {
	if v := os.Getenv("INFISICAL_HOST_API"); v != "" {
		return v
	}
	return "https://app.infisical.com/api"
}

func GetDefaultLogWriter() SDKLogWriter {
	return parseSDKLogWriter(os.Getenv("INFISICAL_LOG_WRITER"))
}

var API_HOST_URL string = GetDefaultHostAPI()
var API_CA_CERTIFICATE string = ""
var API_LOG_WRITER io.Writer = GetDefaultLogWriter().Writer()

func ParseInfisicalGlobalConfig(rawMap map[string]string) (InfisicalGlobalConfig, error) {
	config := InfisicalGlobalConfig{}

	if hostAPI, ok := rawMap["hostAPI"]; ok {
		config.HostAPI = hostAPI
	}

	secretName := rawMap["tls.caRef.secretName"]
	secretNamespace := rawMap["tls.caRef.secretNamespace"]
	secretKey := rawMap["tls.caRef.key"]

	if secretName != "" || secretNamespace != "" || secretKey != "" {
		if secretName == "" || secretNamespace == "" || secretKey == "" {
			return config, fmt.Errorf("when tls.caRef is configured in the infisical-config, all fields must be set (secretName, secretNamespace, key)")
		}
		config.TLS = &v1alpha1.TLSConfig{
			CaRef: v1alpha1.CaReference{
				SecretName:      secretName,
				SecretNamespace: secretNamespace,
				SecretKey:       secretKey,
			},
		}
	}

	return config, nil
}
