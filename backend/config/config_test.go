package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Strong, unpublished values: what a real deployment sets (openssl rand -hex
// 32). The config guards reject anything shorter or repetitive outside explicit
// development, so every test that loads a production-shaped environment needs
// these rather than a placeholder like "test-secret".
const (
	strongJWTSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	strongTokenKey  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestLoadDefaults(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "STATEMENT_PARSER_URL", "TOKEN_ENCRYPTION_KEY")
	// The development fallbacks are reachable only from an explicit
	// development environment (see TestResolveEnv for the unset case).
	t.Setenv("APP_ENV", "development")

	cfg := Load()

	assert.Equal(t, "", cfg.DatabaseURL)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, defaultJWTSecret, cfg.JWTSecret)
	assert.Equal(t, []string{"http://localhost:5173", "http://127.0.0.1:5173"}, cfg.AllowedOrigins)
	assert.Equal(t, "http://localhost:5000", cfg.ParserURL)
}

func TestLoadFromEnvironment(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "APP_ENV", "STATEMENT_PARSER_URL")

	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fintrak")
	t.Setenv("PORT", "9090")
	t.Setenv("JWT_SECRET", strongJWTSecret)
	t.Setenv("TOKEN_ENCRYPTION_KEY", strongTokenKey)
	t.Setenv("ALLOWED_ORIGINS", "https://app.example.com")
	t.Setenv("STATEMENT_PARSER_URL", "http://parser:5000")

	cfg := Load()

	assert.Equal(t, "postgres://user:pass@localhost:5432/fintrak", cfg.DatabaseURL)
	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, strongJWTSecret, cfg.JWTSecret)
	assert.Equal(t, strongTokenKey, cfg.TokenEncryptionKey)
	assert.Equal(t, []string{"https://app.example.com"}, cfg.AllowedOrigins)
	assert.Equal(t, "http://parser:5000", cfg.ParserURL)
}

func TestLoadTrimsAndSplitsOrigins(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "STATEMENT_PARSER_URL", "TOKEN_ENCRYPTION_KEY")
	t.Setenv("APP_ENV", "development")

	t.Setenv("ALLOWED_ORIGINS", " https://a.example.com ,https://b.example.com,  https://c.example.com ")

	cfg := Load()

	assert.Equal(t, []string{
		"https://a.example.com",
		"https://b.example.com",
		"https://c.example.com",
	}, cfg.AllowedOrigins)
}

func TestLoadAllowsWildcardOrigin(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "STATEMENT_PARSER_URL", "TOKEN_ENCRYPTION_KEY")
	t.Setenv("APP_ENV", "development")

	t.Setenv("ALLOWED_ORIGINS", "*")

	cfg := Load()

	assert.Equal(t, []string{"*"}, cfg.AllowedOrigins)
}

func TestLoadLogBodyLimit(t *testing.T) {
	unsetEnv(t, "DATABASE_URL", "PORT", "ALLOWED_ORIGINS", "JWT_SECRET", "APP_ENV", "STATEMENT_PARSER_URL", "LOG_BODY_LIMIT", "TOKEN_ENCRYPTION_KEY")
	t.Setenv("APP_ENV", "development")

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
	t.Setenv("JWT_SECRET", strongJWTSecret)
	t.Setenv("TOKEN_ENCRYPTION_KEY", strongTokenKey)

	// An UNSET APP_ENV resolves to production, so the Secure cookie (and the
	// mandatory-secret policy) applies: a deployment that forgets the variable
	// must not quietly run the development stack over plain HTTP.
	assert.True(t, Load().CookieSecure)

	// Explicit development defaults to a non-Secure cookie (plain HTTP).
	t.Setenv("APP_ENV", "development")
	assert.False(t, Load().CookieSecure)

	// Production (explicit) is Secure too, and an explicit override still wins
	// (e.g. a plain-HTTP isolated deployment behind no TLS terminator).
	t.Setenv("APP_ENV", "production")
	assert.True(t, Load().CookieSecure)

	t.Setenv("COOKIE_SECURE", "false")
	assert.False(t, Load().CookieSecure)
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

// An unrecognized APP_ENV must not silently resolve to development, and an unset
// one must not either: that is what makes every production-only guard
// (mandatory secrets, the strength floor, Secure cookies, body logging off) apply
// to a deployment that forgets or typos the value.
func TestResolveEnv(t *testing.T) {
	cases := []struct {
		value   string
		want    string
		wantErr bool
	}{
		{value: "", want: envProduction},
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

// TestResolveSecrets pins the secret policy for every environment. It is tested
// through resolveSecrets rather than Load because Load exits the process on a
// refused value.
func TestResolveSecrets(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		jwt        string
		key        string
		wantErrFor string // the offending variable, "" for success
	}{
		{
			name: "explicit development falls back to the built-in defaults",
			env:  envDevelopment,
		},
		{
			name: "explicit development accepts the dev compose JWT secret",
			env:  envDevelopment,
			jwt:  "dev-only-secret-change-me",
		},
		{
			name: "explicit development still refuses a weak custom value",
			env:  envDevelopment,
			jwt:  "hunter2",
			key:  strongTokenKey,
			// JWT is refused before the key is looked at.
			wantErrFor: "JWT_SECRET",
		},
		{
			name:       "production requires JWT_SECRET",
			env:        envProduction,
			key:        strongTokenKey,
			wantErrFor: "JWT_SECRET",
		},
		{
			name:       "production requires TOKEN_ENCRYPTION_KEY",
			env:        envProduction,
			jwt:        strongJWTSecret,
			wantErrFor: "TOKEN_ENCRYPTION_KEY",
		},
		{
			name:       "production refuses the built-in JWT default",
			env:        envProduction,
			jwt:        defaultJWTSecret,
			key:        strongTokenKey,
			wantErrFor: "JWT_SECRET",
		},
		{
			// The value docker-compose.yml ships for the dev stack is just as
			// public as the code fallback, so a copy-pasted deployment that
			// kept it would sign tokens anyone can forge.
			name:       "production refuses the dev compose JWT secret",
			env:        envProduction,
			jwt:        "dev-only-secret-change-me",
			key:        strongTokenKey,
			wantErrFor: "JWT_SECRET",
		},
		{
			name:       "production refuses the built-in encryption-key default",
			env:        envProduction,
			jwt:        strongJWTSecret,
			key:        defaultTokenEncKey,
			wantErrFor: "TOKEN_ENCRYPTION_KEY",
		},
		{
			name:       "production refuses a short secret",
			env:        envProduction,
			jwt:        "short-but-random-ish-secret",
			key:        strongTokenKey,
			wantErrFor: "JWT_SECRET",
		},
		{
			name:       "production refuses a repetitive secret",
			env:        envProduction,
			jwt:        strings.Repeat("ab", 32),
			key:        strongTokenKey,
			wantErrFor: "JWT_SECRET",
		},
		{
			name:       "production refuses a whitespace-only secret",
			env:        envProduction,
			jwt:        "   ",
			key:        strongTokenKey,
			wantErrFor: "JWT_SECRET",
		},
		{
			name: "production accepts strong distinct secrets",
			env:  envProduction,
			jwt:  strongJWTSecret,
			key:  strongTokenKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t, "JWT_SECRET", "TOKEN_ENCRYPTION_KEY")
			t.Setenv("JWT_SECRET", tc.jwt)
			t.Setenv("TOKEN_ENCRYPTION_KEY", tc.key)

			jwt, key, err := resolveSecrets(tc.env)
			if tc.wantErrFor != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrFor)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, jwt)
			assert.NotEmpty(t, key)
		})
	}
}

func TestCheckSecretStrength(t *testing.T) {
	require.NoError(t, checkSecretStrength(strongJWTSecret))
	require.NoError(t, checkSecretStrength("correct-horse-battery-staple-2026!"))

	// One character under the floor.
	require.Error(t, checkSecretStrength(strings.Repeat("a1b2c3d4", 3)+"a1b2c3d"))
	// Long enough, but a repeated pattern rather than a key.
	require.Error(t, checkSecretStrength(strings.Repeat("abc123", 8)))
	require.Error(t, checkSecretStrength(strings.Repeat("x", 64)))
}
