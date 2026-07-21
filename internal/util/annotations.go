package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// AnnotationNameMaxLength is the maximum length Kubernetes permits for the name portion of an
// annotation key, meaning everything after the optional "<dns-subdomain>/" prefix.
const AnnotationNameMaxLength = 63

// annotationHashLength is the number of hex characters kept from the digest used to
// disambiguate truncated names.
const annotationHashLength = 8

// BuildManagedSecretAnnotationKey returns the annotation key used to track a managed secret on a
// workload, shortening it deterministically when the name portion of the key would exceed
// Kubernetes' 63 byte limit.
//
// keyPrefix is the full annotation prefix, for example
// "secrets.infisical.com/managed-secret". Only the portion after the "/" counts towards the
// limit, so the DNS subdomain is excluded from the length calculation.
//
// Names that already fit are returned unchanged, so existing workloads keep their current
// annotation. Longer names are truncated and suffixed with a short digest of the full secret
// name, which keeps the key unique, stable across reconciles, and still recognizable.
func BuildManagedSecretAnnotationKey(keyPrefix string, secretName string) string {
	// Annotation keys are "<dns-subdomain>/<name>"; only <name> is bounded by the 63 byte limit.
	domain, namePrefix := "", keyPrefix
	if idx := strings.Index(keyPrefix, "/"); idx != -1 {
		domain, namePrefix = keyPrefix[:idx+1], keyPrefix[idx+1:]
	}

	if len(namePrefix)+1+len(secretName) <= AnnotationNameMaxLength {
		return fmt.Sprintf("%s.%s", keyPrefix, secretName)
	}

	sum := sha256.Sum256([]byte(secretName))
	digest := hex.EncodeToString(sum[:])[:annotationHashLength]
	suffix := "-" + digest

	// Room left for the secret name once the name prefix, separator and digest are accounted for.
	available := AnnotationNameMaxLength - len(namePrefix) - 1 - len(suffix)
	if available < 1 {
		// The prefix alone leaves no room for a name, so fall back to a digest-only name that
		// still fits. Truncating the prefix would risk colliding with other operator keys.
		return domain + digest
	}

	return fmt.Sprintf("%s.%s%s", keyPrefix, secretName[:available], suffix)
}
