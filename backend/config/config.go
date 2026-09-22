// Package config loads and centralizes the runtime configuration for the
// backend. Values come from environment variables (optionally seeded from a
// .env file via godotenv). Development defaults exist only behind an explicit
// APP_ENV=development; an unset APP_ENV resolves to production, so a deployment
// that forgets the variable gets the fail-closed behaviour (mandatory secrets,
// the strength floor, Secure cookies) rather than the development keys.
package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the resolved runtime configuration for the API server.
type Config struct {
	DatabaseURL        string
	Port               string
	AllowedOrigins     []string
	JWTSecret          string
	ParserURL          string
	AdminEmails        []string
	Env                string
	LogLevel           string
	LogBodyLimit       int
	TokenEncryptionKey string
	// TrustedProxies is the allowlist of reverse-proxy CIDRs whose
	// X-Forwarded-For / X-Real-IP headers are believed. Empty means no proxy is
	// trusted, so c.ClientIP() always reports the direct peer address (and
	// client-supplied forwarding headers cannot spoof the rate-limit key).
	TrustedProxies []string
	// CookieSecure marks the session cookie Secure (HTTPS-only). Defaults to
	// true in production; set COOKIE_SECURE=false for an isolated plain-HTTP
	// deployment (behind no TLS terminator).
	CookieSecure bool
	// AdminSetupToken is the shared secret that lets a registrant whose email
	// is in AdminEmails self-register with the 'admin' role. Empty disables
	// admin self-registration entirely (admin-listed emails are refused at
	// registration); existing admins are unaffected.
	AdminSetupToken string
}

const (
	// envDevelopment and envProduction are the only accepted APP_ENV values. An
	// unset APP_ENV resolves to production (see resolveEnv) and any other value
	// stops the process rather than falling back to development: a deployment
	// that forgets the variable, or typos it (APP_ENV=prod, staging,
	// Production), would otherwise run on the built-in development JWT secret
	// and encryption key, with a non-Secure cookie, body logging enabled and the
	// permissive SSRF branch selected.
	envDevelopment = "development"
	envProduction  = "production"

	// Development-only fallbacks. They are used only when APP_ENV is explicitly
	// development and the corresponding variable is unset.
	defaultJWTSecret   = "dev-secret-change-me-in-production"
	defaultTokenEncKey = "dev-token-encryption-key-change-me"

	// minSecretLength and minSecretAlphabet are the strength floor for
	// JWT_SECRET and TOKEN_ENCRYPTION_KEY. A short or repetitive value is
	// brute-forceable regardless of how well it is kept, and the signing key is
	// the only thing standing between a deployment and forged admin tokens.
	minSecretLength   = 32
	minSecretAlphabet = 8
)

// publishedDevSecrets are every development secret value the repository itself
// publishes, mapped to where it appears. Outside explicit development they are
// refused outright rather than only the one value that happened to be the code
// default: the dev compose stack's JWT_SECRET is just as public as the built-in
// fallback, so a copy-paste deployment that used it would sign tokens any
// reader of this repository can forge.
var publishedDevSecrets = map[string]string{
	defaultJWTSecret:            "the built-in development JWT_SECRET fallback",
	defaultTokenEncKey:          "the built-in development TOKEN_ENCRYPTION_KEY fallback",
	"dev-only-secret-change-me": "the JWT_SECRET docker-compose.yml hands the dev stack",
}

// resolveEnv validates APP_ENV. An unset value resolves to production, which is
// the fail-closed direction: every development-only behaviour (the fallback
// secrets, a non-Secure cookie, debug body logging, the permissive SSRF branch)
// is selected only by an explicit APP_ENV=development. An unknown value is a
// configuration error, not a default.
func resolveEnv() (string, error) {
	switch env := strings.TrimSpace(os.Getenv("APP_ENV")); env {
	case "":
		return envProduction, nil
	case envDevelopment, envProduction:
		return env, nil
	default:
		return "", fmt.Errorf("APP_ENV must be %q or %q (got %q)", envDevelopment, envProduction, env)
	}
}

// resolveSecrets resolves JWT_SECRET and TOKEN_ENCRYPTION_KEY for the resolved
// environment, returning an error instead of exiting so the policy is testable.
//
//   - Explicit development: the values the repository publishes for its dev
//     stack (publishedDevSecrets, and the fallbacks when the variables are
//     unset) are allowed, because `make dev` hands the backend exactly one of
//     them. Any other value must still meet the strength floor.
//   - Production, including an unset APP_ENV: both must be set, must not be a
//     value this repository publishes, and must meet the strength floor.
func resolveSecrets(env string) (jwtSecret, tokenEncryptionKey string, err error) {
	dev := env == envDevelopment
	if jwtSecret, err = resolveSecret("JWT_SECRET", defaultJWTSecret, dev); err != nil {
		return "", "", err
	}
	if tokenEncryptionKey, err = resolveSecret("TOKEN_ENCRYPTION_KEY", defaultTokenEncKey, dev); err != nil {
		return "", "", err
	}
	return jwtSecret, tokenEncryptionKey, nil
}

// resolveSecret resolves one secret. surrounding whitespace is trimmed (a value
// that is nothing but whitespace counts as unset), which also means the
// published-value comparison cannot be sidestepped with a stray space.
func resolveSecret(name, devFallback string, explicitDevelopment bool) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		if !explicitDevelopment {
			return "", fmt.Errorf("%s must be set when APP_ENV is not %q", name, envDevelopment)
		}
		return devFallback, nil
	}
	if source, published := publishedDevSecrets[value]; published {
		if explicitDevelopment {
			// The documented dev allowlist: `make dev` runs with exactly this
			// value, and it is only reachable when the environment is
			// explicitly development.
			return value, nil
		}
		return "", fmt.Errorf("%s must not be %s (%s)", name, value, source)
	}
	if err := checkSecretStrength(value); err != nil {
		return "", fmt.Errorf("%s %w", name, err)
	}
	return value, nil
}

// checkSecretStrength enforces the minimum length and a crude entropy floor: a
// secret made of fewer than minSecretAlphabet distinct characters is a repeated
// pattern, not a key. Both checks are cheap and only reject values that cannot
// plausibly come from a generator (`openssl rand -hex 32`).
func checkSecretStrength(value string) error {
	if len(value) < minSecretLength {
		return fmt.Errorf("must be at least %d characters (generate one with `openssl rand -hex 32`)", minSecretLength)
	}
	distinct := make(map[rune]struct{}, len(value))
	for _, r := range value {
		distinct[r] = struct{}{}
	}
	if len(distinct) < minSecretAlphabet {
		return fmt.Errorf("looks low-entropy (%d distinct characters); generate one with `openssl rand -hex 32`", len(distinct))
	}
	return nil
}

// Load reads the environment (loading .env first) and returns a fully resolved
// Config, applying defaults for anything not set. It exits the process when a
// required secret is missing in production.
func Load() *Config {
	godotenv.Load()

	env, err := resolveEnv()
	if err != nil {
		log.Fatal(err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "http://localhost:5173,http://127.0.0.1:5173"
	}
	parserURL := os.Getenv("STATEMENT_PARSER_URL")
	if parserURL == "" {
		parserURL = "http://localhost:5000"
	}

	// Log level. Debug in development captures request/response bodies; info is
	// the production default so secrets and payloads stay out of the logs.
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		if env == envProduction {
			logLevel = "info"
		} else {
			logLevel = "debug"
		}
	}

	// Byte cap for request/response bodies written to the log at debug level.
	// 0 (the default) captures no bodies at all; a positive value enables
	// capture and caps each logged payload at that many bytes. Non-positive
	// values are normalized to 0 so they never mean "unlimited".
	logBodyLimit := 0
	if raw := os.Getenv("LOG_BODY_LIMIT"); raw != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n >= 0 {
			logBodyLimit = n
		}
	}

	// Secrets. The guards are centralized in resolveSecrets so the same policy
	// (mandatory in production, the published dev values refused, a strength
	// floor) applies to both.
	jwtSecret, tokenEncryptionKey, err := resolveSecrets(env)
	if err != nil {
		log.Fatal(err)
	}

	rawOrigins := strings.Split(allowedOrigins, ",")
	var origins []string
	for _, o := range rawOrigins {
		origins = append(origins, strings.TrimSpace(o))
	}

	// Trusted reverse-proxy CIDRs. Empty (the default) trusts no forwarded
	// headers, so a direct client cannot spoof X-Forwarded-For to evade the
	// per-IP auth rate limit.
	var trustedProxies []string
	for _, p := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			trustedProxies = append(trustedProxies, p)
		}
	}

	rawAdmins := strings.Split(os.Getenv("ADMIN_EMAILS"), ",")
	var adminEmails []string
	for _, e := range rawAdmins {
		if e = strings.TrimSpace(e); e != "" {
			adminEmails = append(adminEmails, e)
		}
	}

	// Admin setup token: required to self-register an admin-listed email. Empty
	// means admin-listed addresses cannot self-register at all.
	adminSetupToken := os.Getenv("ADMIN_SETUP_TOKEN")

	// Session cookie Secure flag. Production defaults to true (HTTPS); an
	// operator with a deliberately plain-HTTP deployment can opt out.
	cookieSecure := env == envProduction
	if raw := os.Getenv("COOKIE_SECURE"); raw != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			cookieSecure = b
		}
	}

	return &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Port:               port,
		AllowedOrigins:     origins,
		JWTSecret:          jwtSecret,
		ParserURL:          parserURL,
		AdminEmails:        adminEmails,
		Env:                env,
		LogLevel:           logLevel,
		LogBodyLimit:       logBodyLimit,
		TokenEncryptionKey: tokenEncryptionKey,
		TrustedProxies:     trustedProxies,
		AdminSetupToken:    adminSetupToken,
		CookieSecure:       cookieSecure,
	}
}
