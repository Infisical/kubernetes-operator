package model

import (
	"errors"
	"fmt"
)

var ErrValidation = errors.New("validation error")

func NewNamespaceScopedError(err error, resourceName string) error {
	return fmt.Errorf("unable to fetch %q. Your Operator is namespace scoped, and cannot get resources outside of its namespace. Please ensure the %q is in the same namespace as the operator. [err=%v]", resourceName, resourceName, err)
}
