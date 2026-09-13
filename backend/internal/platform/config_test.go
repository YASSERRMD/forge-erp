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

// TestCheckProdSecrets proves production refuses the development JWT
// secret while development and operator-set secrets boot (Phase 0 task 7).
func TestCheckProdSecrets(t *testing.T) {
	if err := CheckProdSecrets(Config{Env: "production", JWTSecret: DefaultJWTSecret}); err == nil {
		t.Fatal("production with default secret booted")
	}
	if err := CheckProdSecrets(Config{Env: "production", JWTSecret: "operator-set-secret"}); err != nil {
		t.Fatalf("production with operator secret refused: %v", err)
	}
	if err := CheckProdSecrets(Config{Env: "development", JWTSecret: DefaultJWTSecret}); err != nil {
		t.Fatalf("development refused: %v", err)
	}
}
