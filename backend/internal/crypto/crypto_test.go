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
	legacy := v1Ciphertext(t, key, "legacy-secret")

	dec, err := Decrypt(legacy, key)
	require.NoError(t, err)
	assert.Equal(t, "legacy-secret", dec)
}

func TestIsLegacy(t *testing.T) {
	assert.True(t, IsLegacy(Prefix+"abc"))
	assert.False(t, IsLegacy(PrefixV2+"abc"))
	assert.False(t, IsLegacy("plain-token"))
	assert.False(t, IsLegacy(""))
}

func TestReencryptMigratesV1(t *testing.T) {
	const key = "same-key"
	out, err := Reencrypt(v1Ciphertext(t, key, "legacy-secret"), key, key)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, PrefixV2))

	dec, err := Decrypt(out, key)
	require.NoError(t, err)
	assert.Equal(t, "legacy-secret", dec)
}

func TestReencryptRotatesKey(t *testing.T) {
	enc, err := Encrypt("token", "old-key")
	require.NoError(t, err)

	out, err := Reencrypt(enc, "old-key", "new-key")
	require.NoError(t, err)

	dec, err := Decrypt(out, "new-key")
	require.NoError(t, err)
	assert.Equal(t, "token", dec)

	_, err = Decrypt(out, "old-key")
	assert.Error(t, err)
}

func TestReencryptSealsPlaintext(t *testing.T) {
	out, err := Reencrypt("plain-token", "old-key", "new-key")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, PrefixV2))

	dec, err := Decrypt(out, "new-key")
	require.NoError(t, err)
	assert.Equal(t, "plain-token", dec)
}

// v1Ciphertext builds a value in the pre-HKDF format the same way the legacy
// production code did: a bare SHA-256 key and nonce || sealed.
func v1Ciphertext(t *testing.T, key, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher(legacyKeyFromString(key))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return Prefix + base64.StdEncoding.EncodeToString(sealed)
}
