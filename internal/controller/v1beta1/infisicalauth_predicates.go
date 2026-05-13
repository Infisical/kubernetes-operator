package v1beta1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

type SpecChangedPredicate struct {
	predicate.GenerationChangedPredicate
	AuthResolver *auth.AuthStrategyResolver
}

func (p *SpecChangedPredicate) Update(e event.UpdateEvent) bool {
	if !p.GenerationChangedPredicate.Update(e) {
		return false
	}

	oldAuth, oldOk := e.ObjectOld.(*secretsv1beta1.InfisicalAuth)
	newAuth, newOk := e.ObjectNew.(*secretsv1beta1.InfisicalAuth)

	// If there were changes to the InfisicalAuth.spec, we remove the cache entry
	// to ensure we don't reuse a credential that might not be aligned with the new
	// CRD definition.
	if oldOk && newOk && hashSpec(oldAuth.Spec) != hashSpec(newAuth.Spec) {
		p.AuthResolver.DeleteCacheEntry(oldAuth)
	}

	return true
}

func hashSpec(spec secretsv1beta1.InfisicalAuthSpec) string {
	data, _ := json.Marshal(spec)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
