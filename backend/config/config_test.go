package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadDefaults(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV")

	cfg := Load()

	assert.Equal(t, "", cfg.DatabaseURL)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, defaultJWTSecret, cfg.JWTSecret)
	assert.Equal(t, []string{"http://localhost:5173", "http://127.0.0.1:5173"}, cfg.AllowedOrigins)
}

func TestLoadFromEnvironment(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV")

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fintrak")
	t.Setenv("PORT", "9090")
	t.Setenv("JWT_SECRET", "env-secret")
	t.Setenv("ALLOWED_ORIGINS", "https://app.example.com")

	cfg := Load()

	assert.Equal(t, "postgres://user:pass@localhost:5432/fintrak", cfg.DatabaseURL)
	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, "env-secret", cfg.JWTSecret)
	assert.Equal(t, []string{"https://app.example.com"}, cfg.AllowedOrigins)
}

func TestLoadTrimsAndSplitsOrigins(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV")

	t.Setenv("ALLOWED_ORIGINS", " https://a.example.com ,https://b.example.com,  https://c.example.com ")

	cfg := Load()

	assert.Equal(t, []string{
		"https://a.example.com",
		"https://b.example.com",
		"https://c.example.com",
	}, cfg.AllowedOrigins)
}

func TestLoadAllowsWildcardOrigin(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV")

	t.Setenv("ALLOWED_ORIGINS", "*")

	cfg := Load()

	assert.Equal(t, []string{"*"}, cfg.AllowedOrigins)
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
	}
}
