package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"hash/crc32"
	"sort"

	"golang.org/x/crypto/nacl/box"
)

func DecryptSymmetric(key []byte, encryptedPrivateKey []byte, tag []byte, IV []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCMWithNonceSize(block, len(IV))
	if err != nil {
		return nil, err
	}

	var nonce = IV
	var ciphertext = append(encryptedPrivateKey, tag...)

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

func DecryptAsymmetric(ciphertext []byte, nonce []byte, publicKey []byte, privateKey []byte) (plainText []byte) {
	plainTextToReturn, _ := box.Open(nil, ciphertext, (*[24]byte)(nonce), (*[32]byte)(publicKey), (*[32]byte)(privateKey))
	return plainTextToReturn
}

func ComputeEtag(data []byte) string {
	crc := crc32.ChecksumIEEE(data)
	return fmt.Sprintf(`W/"secrets-%d-%08X"`, len(data), crc)
}

// ComputeRenderedEtag hashes the post-template bytes that land in a managed
// Secret/ConfigMap. Keys are sorted so iteration order doesn't perturb the hash;
// each key/value is null-separated to prevent boundary collisions.
func ComputeRenderedEtag(rendered map[string][]byte) string {
	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte(0)
		buf.Write(rendered[k])
		buf.WriteByte(0)
	}
	return ComputeEtag(buf.Bytes())
}
