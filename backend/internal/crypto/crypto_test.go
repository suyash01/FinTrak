package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc, err := Encrypt("tok-123", "key")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(enc, Prefix))

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
	_, err := Decrypt(Prefix+"not-base64!!", "key")
	assert.Error(t, err)
}