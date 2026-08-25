// Package crypto provides AES-GCM encryption for secrets stored at rest, such
// as the user's Paperless-ngx API token.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Versioned ciphertext prefix so legacy plaintext values remain readable.
const Prefix = "enc:v1:"

// keyFromString derives a 32-byte AES-256 key from the configured key string.
func keyFromString(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// Encrypt seals plaintext with AES-GCM and returns a versioned, base64 string.
func Encrypt(plaintext, key string) (string, error) {
	block, err := aes.NewCipher(keyFromString(key))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return Prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens an Encrypt-format value. Non-versioned values are returned
// unchanged so legacy plaintext tokens keep working.
func Decrypt(ciphertext, key string) (string, error) {
	if len(ciphertext) < len(Prefix) || ciphertext[:len(Prefix)] != Prefix {
		return ciphertext, nil
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext[len(Prefix):])
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext encoding: %w", err)
	}
	block, err := aes.NewCipher(keyFromString(key))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}
	return string(plain), nil
}