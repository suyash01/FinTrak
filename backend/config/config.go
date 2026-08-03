package config

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL    string
	Port           string
	AllowedOrigins []string
	JWTSecret      string
}

const defaultJWTSecret = "dev-secret-change-me-in-production"

func Load() *Config {
	godotenv.Load()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "http://localhost:5173,http://127.0.0.1:5173"
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		if os.Getenv("APP_ENV") == "production" {
			log.Fatal("JWT_SECRET must be set when APP_ENV=production")
		}
		jwtSecret = defaultJWTSecret
	}

	rawOrigins := strings.Split(allowedOrigins, ",")
	var origins []string
	for _, o := range rawOrigins {
		origins = append(origins, strings.TrimSpace(o))
	}

	return &Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		Port:           port,
		AllowedOrigins: origins,
		JWTSecret:      jwtSecret,
	}
}
