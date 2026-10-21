package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Environment       string
	HTTPAddress       string
	DatabaseDSN       string
	RedisURL          string
	JWTSecret         string
	JWTIssuer         string
	JWTExpiry         time.Duration
	CORSAllowedOrigin []string
	TrustedProxies    []string
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env file: %w", err)
	}

	port := env("HTTP_PORT", "8080")
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("HTTP_PORT must be an integer between 1 and 65535")
	}

	expiry, err := time.ParseDuration(env("JWT_EXPIRY", "15m"))
	if err != nil || expiry <= 0 {
		return Config{}, fmt.Errorf("JWT_EXPIRY must be a positive duration")
	}

	cfg := Config{
		Environment:       env("APP_ENV", "development"),
		HTTPAddress:       ":" + port,
		DatabaseDSN:       strings.TrimSpace(os.Getenv("DATABASE_DSN")),
		RedisURL:          strings.TrimSpace(os.Getenv("REDIS_URL")),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		JWTIssuer:         env("JWT_ISSUER", "parabeacon-api"),
		JWTExpiry:         expiry,
		CORSAllowedOrigin: splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		TrustedProxies:    splitCSV(os.Getenv("TRUSTED_PROXIES")),
	}
	if cfg.DatabaseDSN == "" {
		return Config{}, fmt.Errorf("DATABASE_DSN is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			result = append(result, origin)
		}
	}
	return result
}
