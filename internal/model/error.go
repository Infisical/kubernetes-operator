package model

import (
	"errors"
	"fmt"
)

var (
	ErrValidation                = errors.New("validation error")
	ErrInvalidAuthObject         = errors.New("invalid auth object")
	ErrInvalidStaticSecretObject = errors.New("invalid static secret object")
	ErrInvalidConnectionObject   = errors.New("invalid connection object")
)

func NewNamespaceScopedError(err error, resourceName string) error {
	return fmt.Errorf("unable to fetch %q. Your Operator is namespace scoped, and cannot get resources outside of its namespace. Please ensure the %q is in the same namespace as the operator. [err=%w]", resourceName, resourceName, err)
}

func NewTargetNamespaceScopedError(err error, targetName, targetNamespace string) error {
	return fmt.Errorf("unable to sync target %q in namespace %q: the operator is namespace-scoped and cannot manage resources outside of its allowed namespaces. [err=%w]", targetName, targetNamespace, err)
}
