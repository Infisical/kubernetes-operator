package util

import (
	"fmt"

	"github.com/Infisical/infisical/k8-operator/internal/constants"
)

// Version is the operator version. It defaults to "dev" for local builds and is
// overridden at build time via:
//
//	-ldflags "-X github.com/Infisical/infisical/k8-operator/internal/util.Version=<version>"
var Version = "dev"

// UserAgent returns the standard product/version User-Agent token sent on every
// outbound request from the operator, e.g. "k8-operator/v0.11.4". This is the
// single source of truth for the User-Agent string.
func UserAgent() string {
	return fmt.Sprintf("%s/%s", constants.USER_AGENT_NAME, Version)
}
