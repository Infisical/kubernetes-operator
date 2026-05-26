package infra

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const NodeEnv = "test"

var (
	AuthSecret    string
	EncryptionKey string

	pgUser     string
	pgPassword string
	pgDB       string
)

func init() {
	AuthSecret = "test-auth-" + randomHex(16)
	EncryptionKey = randomHex(16)
	pgUser = "test_" + randomHex(4)
	pgPassword = randomHex(16)
	pgDB = "test_" + randomHex(4)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}
