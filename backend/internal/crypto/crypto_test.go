package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc, err := Encrypt("tok-123", "key")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(enc, PrefixV2))

	dec, err := Decrypt(enc, "key")
	require.NoError(t, err)
	assert.Equal(t, "tok-123", dec)
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	a, err := Encrypt("same", "key")
	require.NoError(t, err)
	b, err := Encrypt("same", "key")
	require.NoError(t, err)
	assert.NotEqual(t, a, b)

	dec, err := Decrypt(a, "key")
	require.NoError(t, err)
	assert.Equal(t, "same", dec)
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, err := Encrypt("tok", "key-a")
	require.NoError(t, err)
	_, err = Decrypt(enc, "key-b")
	assert.Error(t, err)
}

func TestDecryptPassesLegacyPlaintext(t *testing.T) {
	dec, err := Decrypt("plain-token", "key")
	require.NoError(t, err)
	assert.Equal(t, "plain-token", dec)
}

func TestDecryptRejectsGarbage(t *testing.T) {
	for _, prefix := range []string{Prefix, PrefixV2} {
		_, err := Decrypt(prefix+"not-base64!!", "key")
		assert.Error(t, err)
	}
}

// TestDecryptReadsLegacyV1 verifies values encrypted before the HKDF upgrade
// (bare SHA-256 key, no salt) remain decryptable.
func TestDecryptReadsLegacyV1(t *testing.T) {
	const key = "legacy-key"
	block, err := aes.NewCipher(legacyKeyFromString(key))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	sealed := gcm.Seal(nonce, nonce, []byte("legacy-secret"), nil)
	legacy := Prefix + base64.StdEncoding.EncodeToString(sealed)

	dec, err := Decrypt(legacy, key)
	require.NoError(t, err)
	assert.Equal(t, "legacy-secret", dec)
}
