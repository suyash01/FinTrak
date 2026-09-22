// Package config loads and centralizes the runtime configuration for the
// backend. Values come from environment variables (optionally seeded from a
// .env file via godotenv), with safe development defaults and hard failures in
// production when secrets are missing.
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
	// envDevelopment and envProduction are the only accepted APP_ENV values. Any
	// other value stops the process rather than falling back to development:
	// a typo (APP_ENV=prod, staging, Production) would otherwise run a
	// production deployment on the built-in development JWT secret and
	// encryption key, with a non-Secure cookie and body logging enabled.
	envDevelopment = "development"
	envProduction  = "production"

	// Development-only fallbacks. Production startup fails unless the real
	// secrets are provided via the environment.
	defaultJWTSecret   = "dev-secret-change-me-in-production"
	defaultTokenEncKey = "dev-token-encryption-key-change-me"
)

// resolveEnv validates APP_ENV. Unset means development; an unknown value is a
// configuration error, not a default.
func resolveEnv() (string, error) {
	switch env := strings.TrimSpace(os.Getenv("APP_ENV")); env {
	case "":
		return envDevelopment, nil
	case envDevelopment, envProduction:
		return env, nil
	default:
		return "", fmt.Errorf("APP_ENV must be %q or %q (got %q)", envDevelopment, envProduction, env)
	}
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

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		if env == envProduction {
			log.Fatal("JWT_SECRET must be set when APP_ENV=production")
		}
		jwtSecret = defaultJWTSecret
	}
	if env == envProduction && jwtSecret == defaultJWTSecret {
		log.Fatal("JWT_SECRET must not be the built-in development default when APP_ENV=production")
	}

	tokenEncryptionKey := os.Getenv("TOKEN_ENCRYPTION_KEY")
	if tokenEncryptionKey == "" {
		if env == envProduction {
			log.Fatal("TOKEN_ENCRYPTION_KEY must be set when APP_ENV=production")
		}
		tokenEncryptionKey = defaultTokenEncKey
	}
	if env == envProduction && tokenEncryptionKey == defaultTokenEncKey {
		log.Fatal("TOKEN_ENCRYPTION_KEY must not be the built-in development default when APP_ENV=production")
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
