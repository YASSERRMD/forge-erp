// Package platform is the shared kernel of the ForgeERP modular monolith:
// environment-first configuration, PostgreSQL connectivity, schema migrations,
// an in-process event bus (NATS-JetStream-compatible interface), and HTTP plumbing.
package platform

import (
	"os"
	"strconv"
	"time"
)

// Config is loaded from FERP_* environment variables (MASTER §3: env-first, secrets never in repo).
type Config struct {
	Env            string // development | test | production
	HTTPPort       string
	DatabaseURL    string
	JWTSecret      string
	AdminEmail     string
	AdminPassword  string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	ShutdownTimout time.Duration
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}

// Load returns Config with sane development defaults; production must override secrets.
func Load() Config {
	return Config{
		Env:            getenv("FERP_ENV", "development"),
		HTTPPort:       getenv("FERP_HTTP_PORT", "8080"),
		DatabaseURL:    getenv("FERP_DATABASE_URL", "postgres://forgeerp:forgeerp@localhost:5432/forgeerp?sslmode=disable"),
		JWTSecret:      getenv("FERP_JWT_SECRET", "dev-only-insecure-secret-change-me"),
		AdminEmail:     getenv("FERP_ADMIN_EMAIL", "admin@forgeerp.local"),
		AdminPassword:  getenv("FERP_ADMIN_PASSWORD", ""),
		ReadTimeout:    getDur("FERP_READ_TIMEOUT_S", 10*time.Second),
		WriteTimeout:   getDur("FERP_WRITE_TIMEOUT_S", 15*time.Second),
		ShutdownTimout: getDur("FERP_SHUTDOWN_TIMEOUT_S", 10*time.Second),
	}
}
