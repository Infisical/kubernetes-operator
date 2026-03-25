package util

import (
	"context"

	"github.com/Infisical/infisical/k8-operator/internal/util/sse"
	infisicalSdk "github.com/infisical/go-sdk"
)

type ResourceVariables struct {
	InfisicalClient  infisicalSdk.InfisicalClientInterface
	CancelCtx        context.CancelFunc
	AuthDetails      AuthenticationDetails
	ServerSentEvents *sse.ConnectionRegistry
	ServerETag       string // ETag from last successful API response, used for If-None-Match
	LastSecretsCount int    // Secret count from last successful fetch (returned on 304 path)
}
