package util

import (
	"fmt"

	"github.com/Infisical/infisical/k8-operator/internal/config"
	"github.com/Infisical/infisical/k8-operator/internal/model"
)

// CheckNamespaceIsolation returns an error when namespace isolation is enabled and a resource
// in resourceNamespace references, through fieldPath, an object in another namespace.
func CheckNamespaceIsolation(resourceNamespace, referencedNamespace, fieldPath string) error {
	if !config.NAMESPACE_ISOLATION || referencedNamespace == resourceNamespace {
		return nil
	}
	return fmt.Errorf("%w: namespace isolation is enabled, so %s must be in this resource's namespace %q, not %q",
		model.ErrValidation, fieldPath, resourceNamespace, referencedNamespace)
}
