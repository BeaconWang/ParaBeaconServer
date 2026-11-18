package config

import (
	"testing"
)

func TestLoadBuildsHTTPAddressFromHostAndPort(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_HOST", "192.0.2.10")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("DATABASE_DSN", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != "192.0.2.10:9090" {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, "192.0.2.10:9090")
	}
}

func TestLoadBuildsIPv6HTTPAddress(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_HOST", "::1")
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("DATABASE_DSN", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != "[::1]:8080" {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, "[::1]:8080")
	}
}
