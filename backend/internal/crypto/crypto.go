// Package crypto provides AES-GCM encryption for secrets stored at rest, such
// as the user's Paperless-ngx API token.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Versioned ciphertext prefixes. Prefix is the legacy v1 format, whose key was
// derived with a bare SHA-256 of the configured passphrase; PrefixV2 derives a
// per-ciphertext key with HKDF-SHA256 over a random salt. New writes always use
// v2 while v1 values remain readable so stored secrets survive the upgrade.
const (
	Prefix   = "enc:v1:"
	PrefixV2 = "enc:v2:"
)

const (
	saltSize = 16
	keySize  = 32
	// keyInfo domain-separates this key derivation from any other use of the
	// configured passphrase.
	keyInfo = "fintrak/token-encryption/v2"
)

// legacyKeyFromString reproduces the v1 derivation: a bare SHA-256 of the key
// string. Kept only to decrypt existing v1 ciphertexts.
func legacyKeyFromString(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// deriveKeyV2 stretches the configured passphrase into a 32-byte AES-256 key
// using HKDF-SHA256 with a per-ciphertext random salt.
func deriveKeyV2(salt []byte, s string) ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(s), salt, keyInfo, keySize)
}

// newGCM returns an AES-GCM AEAD for the supplied 32-byte key.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals plaintext with AES-GCM and returns a versioned, base64 string.
// The v2 payload is salt || nonce || sealed so each record carries the salt
// needed to re-derive its key.
func Encrypt(plaintext, key string) (string, error) {
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	derived, err := deriveKeyV2(salt, key)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(derived)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	// Seal appends the ciphertext to nonce, giving salt || nonce || sealed.
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	payload := append(salt, sealed...)
	return PrefixV2 + base64.StdEncoding.EncodeToString(payload), nil
}

// Decrypt opens an Encrypt-format value. Non-versioned values are returned
// unchanged so legacy plaintext tokens keep working.
func Decrypt(ciphertext, key string) (string, error) {
	switch {
	case len(ciphertext) >= len(PrefixV2) && ciphertext[:len(PrefixV2)] == PrefixV2:
		return decryptV2(ciphertext[len(PrefixV2):], key)
	case len(ciphertext) >= len(Prefix) && ciphertext[:len(Prefix)] == Prefix:
		return decryptV1(ciphertext[len(Prefix):], key)
	default:
		return ciphertext, nil
	}
}

// IsLegacy reports whether ciphertext uses the pre-HKDF v1 format. New writes
// always produce v2, so a true result means the value can be transparently
// re-sealed under the current derivation.
func IsLegacy(ciphertext string) bool {
	return strings.HasPrefix(ciphertext, Prefix)
}

// Reencrypt decrypts ciphertext with oldKey and seals the plaintext with
// newKey. It powers key rotation: values sealed under a retired key are
// migrated to the active one. Unversioned legacy plaintext is sealed with
// newKey as-is.
func Reencrypt(ciphertext, oldKey, newKey string) (string, error) {
	plaintext, err := Decrypt(ciphertext, oldKey)
	if err != nil {
		return "", err
	}
	return Encrypt(plaintext, newKey)
}

// decryptV1 opens the legacy salt-less format: base64(nonce || sealed) with a
// SHA-256-derived key.
func decryptV1(encoded, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext encoding: %w", err)
	}
	gcm, err := newGCM(legacyKeyFromString(key))
	if err != nil {
		return "", err
	}
	return open(gcm, raw)
}

// decryptV2 opens the salted format: base64(salt || nonce || sealed).
func decryptV2(encoded, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext encoding: %w", err)
	}
	if len(raw) < saltSize {
		return "", errors.New("ciphertext too short")
	}
	derived, err := deriveKeyV2(raw[:saltSize], key)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(derived)
	if err != nil {
		return "", err
	}
	return open(gcm, raw[saltSize:])
}

// open splits a nonce-prefixed payload and decrypts it with gcm.
func open(gcm cipher.AEAD, raw []byte) (string, error) {
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
