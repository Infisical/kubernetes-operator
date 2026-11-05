package v1beta1

// Exposes unexported helpers to the external test package.
var (
	IndexStaticSecretPushBySourceSecret = indexStaticSecretPushBySourceSecret
	IndexStaticSecretPushByAuth         = indexStaticSecretPushByAuth
	SecretDataChangedPredicate          = secretDataChangedPredicate
	DeletionStartedPredicate            = deletionStartedPredicate
)
