package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Environment          string
	HTTPAddress          string
	DatabaseDSN          string
	RedisURL             string
	JWTSecret            string
	JWTIssuer            string
	JWTExpiry            time.Duration
	CORSAllowedOrigin    []string
	TrustedProxies       []string
	APKStorageDir        string
	APKPackageName       string
	APKReleaseAdminToken string
	FlightLogStorageDir  string
	COSBucketURL         string
	COSSecretID          string
	COSSecretKey         string
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env file: %w", err)
	}

	environment := env("APP_ENV", "development")
	databaseDSN := strings.TrimSpace(os.Getenv("DATABASE_DSN"))
	if databaseDSN == "" && (environment == "development" || environment == "test") {
		databaseDSN = "postgres://parabeacon:parabeacon-dev@localhost:5432/parabeacon?sslmode=disable"
	}

	port := env("HTTP_PORT", "8080")
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("HTTP_PORT must be an integer between 1 and 65535")
	}
	httpHost := strings.TrimSpace(os.Getenv("HTTP_HOST"))

	expiry, err := time.ParseDuration(env("JWT_EXPIRY", "15m"))
	if err != nil || expiry <= 0 {
		return Config{}, fmt.Errorf("JWT_EXPIRY must be a positive duration")
	}

	cosBucketURL := strings.TrimSpace(os.Getenv("COS_BUCKET_URL"))
	cosSecretID := strings.TrimSpace(os.Getenv("COS_SECRET_ID"))
	cosSecretKey := strings.TrimSpace(os.Getenv("COS_SECRET_KEY"))
	cosConfigured := cosBucketURL != "" || cosSecretID != "" || cosSecretKey != ""
	if cosConfigured && (cosBucketURL == "" || cosSecretID == "" || cosSecretKey == "") {
		return Config{}, fmt.Errorf("COS_BUCKET_URL, COS_SECRET_ID and COS_SECRET_KEY must be configured together")
	}
	if cosConfigured {
		if err := validateCOSBucketURL(cosBucketURL); err != nil {
			return Config{}, err
		}
	}

	cfg := Config{
		Environment:          environment,
		HTTPAddress:          net.JoinHostPort(httpHost, port),
		DatabaseDSN:          databaseDSN,
		RedisURL:             strings.TrimSpace(os.Getenv("REDIS_URL")),
		JWTSecret:            os.Getenv("JWT_SECRET"),
		JWTIssuer:            env("JWT_ISSUER", "parabeacon-api"),
		JWTExpiry:            expiry,
		CORSAllowedOrigin:    splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		TrustedProxies:       splitCSV(os.Getenv("TRUSTED_PROXIES")),
		APKStorageDir:        env("APK_STORAGE_DIR", "./data/apks"),
		APKPackageName:       env("APK_PACKAGE_NAME", "com.beacon.parabeacon"),
		APKReleaseAdminToken: os.Getenv("APK_RELEASE_ADMIN_TOKEN"),
		FlightLogStorageDir:  env("FLIGHT_LOG_STORAGE_DIR", "./data/flights"),
		COSBucketURL:         cosBucketURL,
		COSSecretID:          cosSecretID,
		COSSecretKey:         cosSecretKey,
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

func validateCOSBucketURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("COS_BUCKET_URL is invalid")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		(host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".cos.") || !strings.HasSuffix(host, ".myqcloud.com")) ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("COS_BUCKET_URL must be an HTTPS Tencent COS bucket host")
	}
	return nil
}
