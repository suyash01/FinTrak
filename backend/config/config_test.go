package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV", "STATEMENT_PARSER_URL")

	cfg := Load()

	assert.Equal(t, "", cfg.DatabaseURL)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, defaultJWTSecret, cfg.JWTSecret)
	assert.Equal(t, []string{"http://localhost:5173", "http://127.0.0.1:5173"}, cfg.AllowedOrigins)
	assert.Equal(t, "http://localhost:5000", cfg.ParserURL)
}

func TestLoadFromEnvironment(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV", "STATEMENT_PARSER_URL")

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fintrak")
	t.Setenv("PORT", "9090")
	t.Setenv("JWT_SECRET", "env-secret")
	t.Setenv("ALLOWED_ORIGINS", "https://app.example.com")
	t.Setenv("STATEMENT_PARSER_URL", "http://parser:5000")

	cfg := Load()

	assert.Equal(t, "postgres://user:pass@localhost:5432/fintrak", cfg.DatabaseURL)
	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, "env-secret", cfg.JWTSecret)
	assert.Equal(t, []string{"https://app.example.com"}, cfg.AllowedOrigins)
	assert.Equal(t, "http://parser:5000", cfg.ParserURL)
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

func TestLoadLogBodyLimit(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV", "STATEMENT_PARSER_URL", "LOG_BODY_LIMIT")

	assert.Equal(t, 0, Load().LogBodyLimit)

	t.Setenv("LOG_BODY_LIMIT", "4096")
	assert.Equal(t, 4096, Load().LogBodyLimit)

	t.Setenv("LOG_BODY_LIMIT", "not-a-number")
	assert.Equal(t, 0, Load().LogBodyLimit)

	t.Setenv("LOG_BODY_LIMIT", "-1")
	assert.Equal(t, 0, Load().LogBodyLimit)
}

func TestLoadCookieSecure(t *testing.T) {
	unsetEnv(t, "APP_ENV", "COOKIE_SECURE")

	// Development defaults to a non-Secure cookie (plain HTTP).
	assert.False(t, Load().CookieSecure)

	// Production defaults to Secure (JWT_SECRET/TOKEN_ENCRYPTION_KEY are
	// required there, so provide them before loading).
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("TOKEN_ENCRYPTION_KEY", "test-key")
	assert.True(t, Load().CookieSecure)

	// Explicit override wins (e.g. plain-HTTP isolated deployment).
	t.Setenv("COOKIE_SECURE", "false")
	assert.False(t, Load().CookieSecure)
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

// An unrecognized APP_ENV must not silently resolve to development: that is
// what makes every production-only guard (mandatory secrets, Secure cookies,
// body logging off) apply to a deployment that typos the value.
func TestResolveEnv(t *testing.T) {
	cases := []struct {
		value   string
		want    string
		wantErr bool
	}{
		{value: "", want: envDevelopment},
		{value: "development", want: envDevelopment},
		{value: "production", want: envProduction},
		{value: " production ", want: envProduction},
		{value: "prod", wantErr: true},
		{value: "staging", wantErr: true},
		{value: "Production", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("APP_ENV", tc.value)
			got, err := resolveEnv()
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "APP_ENV must be")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
