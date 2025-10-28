package config

import (
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
)

type InfisicalGlobalConfig struct {
	HostAPI string              `json:"hostAPI"`
	TLS     *v1alpha1.TLSConfig `json:"tls,omitempty"`
}

var API_HOST_URL string = "https://app.infisical.com/api"
var API_CA_CERTIFICATE string = ""

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
