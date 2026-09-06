package platform

import (
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()
	if cfg.HTTPPort != "8080" {
		t.Fatalf("default port = %q, want 8080", cfg.HTTPPort)
	}
	if cfg.Env != "development" {
		t.Fatalf("default env = %q, want development", cfg.Env)
	}
	if cfg.DatabaseURL == "" || cfg.JWTSecret == "" {
		t.Fatal("defaults must provide database URL and (dev) JWT secret")
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("FERP_HTTP_PORT", "9090")
	t.Setenv("FERP_ENV", "test")
	cfg := Load()
	if cfg.HTTPPort != "9090" || cfg.Env != "test" {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
}
