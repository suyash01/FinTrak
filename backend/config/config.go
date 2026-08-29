// Package config loads and centralizes the runtime configuration for the
// backend. Values come from environment variables (optionally seeded from a
// .env file via godotenv), with safe development defaults and hard failures in
// production when secrets are missing.
package config

import (
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
}

const (
	// Development-only fallbacks. Production startup fails unless the real
	// secrets are provided via the environment.
	defaultJWTSecret   = "dev-secret-change-me-in-production"
	defaultTokenEncKey = "dev-token-encryption-key-change-me"
)

// Load reads the environment (loading .env first) and returns a fully resolved
// Config, applying defaults for anything not set. It exits the process when a
// required secret is missing in production.
func Load() *Config {
	godotenv.Load()

	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
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
		if env == "production" {
			logLevel = "info"
		} else {
			logLevel = "debug"
		}
	}

	// Byte cap for request/response bodies written to the log at debug level.
	// 0 (the default) disables truncation so full bodies are captured; a
	// positive value caps the logged payload.
	logBodyLimit := 0
	if raw := os.Getenv("LOG_BODY_LIMIT"); raw != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n >= 0 {
			logBodyLimit = n
		}
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		if env == "production" {
			log.Fatal("JWT_SECRET must be set when APP_ENV=production")
		}
		jwtSecret = defaultJWTSecret
	}

	tokenEncryptionKey := os.Getenv("TOKEN_ENCRYPTION_KEY")
	if tokenEncryptionKey == "" {
		if env == "production" {
			log.Fatal("TOKEN_ENCRYPTION_KEY must be set when APP_ENV=production")
		}
		tokenEncryptionKey = defaultTokenEncKey
	}

	rawOrigins := strings.Split(allowedOrigins, ",")
	var origins []string
	for _, o := range rawOrigins {
		origins = append(origins, strings.TrimSpace(o))
	}

	rawAdmins := strings.Split(os.Getenv("ADMIN_EMAILS"), ",")
	var adminEmails []string
	for _, e := range rawAdmins {
		if e = strings.TrimSpace(e); e != "" {
			adminEmails = append(adminEmails, e)
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
	}
}
