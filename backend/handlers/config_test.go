package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetTokenEncryptionKey(t *testing.T) {
	old := tokenEncryptionKey
	t.Cleanup(func() { tokenEncryptionKey = old })

	SetTokenEncryptionKey("key-123")
	assert.Equal(t, "key-123", tokenEncryptionKey)
}

func TestSetAppEnv(t *testing.T) {
	old := appEnv
	t.Cleanup(func() { appEnv = old })

	SetAppEnv("production")
	assert.Equal(t, "production", appEnv)
}
