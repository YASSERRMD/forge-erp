// Package ldap implements read-only user sync from an LDAP directory
// (Dolibarr LDAP sync, lite): behind a FERP_LDAP_URL gate with a
// fake-friendly dialer interface. Without a server, sync runs from a test
// double in tests. This package never writes to the directory and never
// stores credentials — it mirrors directory entries into an in-memory cache.
package ldap

import (
	"fmt"
	"os"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// LDAPUser is one mirrored directory entry.
type LDAPUser struct {
	Login    string `json:"login"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
}

// Validate checks directory-entry invariants.
func (u LDAPUser) Validate() error {
	if strings.TrimSpace(u.Login) == "" {
		return fmt.Errorf("ldap: login required: %w", platform.ErrValidation)
	}
	return nil
}

// Config gates the sync surface. Empty URL = disabled (sync refuses with
// ErrValidation and /status reports disabled).
type Config struct {
	URL    string
	BaseDN string
	BindDN string
}

// ConfigFromEnv reads FERP_LDAP_URL (+ optional FERP_LDAP_BASE_DN,
// FERP_LDAP_BIND_DN). No server is required: an empty URL disables sync.
func ConfigFromEnv() Config {
	return Config{
		URL:    strings.TrimSpace(os.Getenv("FERP_LDAP_URL")),
		BaseDN: strings.TrimSpace(os.Getenv("FERP_LDAP_BASE_DN")),
		BindDN: strings.TrimSpace(os.Getenv("FERP_LDAP_BIND_DN")),
	}
}

// Enabled reports whether sync is configured.
func (c Config) Enabled() bool { return c.URL != "" }
