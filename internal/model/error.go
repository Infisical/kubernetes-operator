package model

import (
	"errors"
	"fmt"
)

var (
	ErrValidation              = errors.New("validation error")
	ErrInvalidAuthObject       = errors.New("invalid auth object")
	ErrInvalidConnectionObject = errors.New("invalid connection object")
)

func NewNamespaceScopedError(err error, resourceName string) error {
	return fmt.Errorf("unable to fetch %q. Your Operator is namespace scoped, and cannot get resources outside of its namespace. Please ensure the %q is in the same namespace as the operator. [err=%w]", resourceName, resourceName, err)
}
