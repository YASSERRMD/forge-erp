// Package identity implements users, groups, and Dolibarr-style rights triples
// (module.entity.action). Passwords use Argon2id; tokens are dev HS256 JWT
// (production uses Keycloak OIDC behind the Verifier interface).
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// User status machine (Dolibarr llx_user.statut equivalent: active vs closed).
type UserStatus int16

const (
	UserActive   UserStatus = 1
	UserLocked   UserStatus = 2 // temporary lockout after failed logins
	UserDisabled UserStatus = 0 // administrator-disabled; login always rejected
)

// Login policy.
const (
	MinPasswordLength = 10
	MaxFailedAttempts = 5
	LockoutDuration   = 15 * time.Minute
)

// User is a login-capable account scoped to an entity (Dolibarr: llx_user + entity).
type User struct {
	ID             int64
	EntityID       int64
	Login          string
	Email          string
	FirstName      string
	LastName       string
	Status         UserStatus
	PasswordHash   string // argon2id PHC string; empty for SSO-only accounts
	IsAdmin        bool   // bypasses rights checks (Dolibarr admin flag)
	FailedAttempts int
	LockedUntil    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CreatedBy      *int64
	UpdatedBy      *int64
	RowVersion     int64
}

// Group bundles rights (Dolibarr: llx_usergroup).
type Group struct {
	ID        int64
	EntityID  int64
	Code      string
	Label     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Right is one grant in the matrix (Dolibarr: llx_rights_def + grants).
// Entity "*" means all entities within the scope the grant was issued.
type Right struct {
	Module string // e.g. "partners"
	Entity string // e.g. "organization" or "*"
	Action string // e.g. "read" | "write" | "delete" | "validate"
}

// Key returns the canonical triple string.
func (r Right) Key() string { return r.Module + "." + r.Entity + "." + r.Action }

// Validate checks naming rules shared by grants and Require() calls.
func (r Right) Validate() error {
	for name, v := range map[string]string{"module": r.Module, "entity": r.Entity, "action": r.Action} {
		if v == "" {
			return fmt.Errorf("identity: right %s is empty", name)
		}
		if strings.ContainsAny(v, " \t.") {
			return fmt.Errorf("identity: right %s %q contains whitespace or dot", name, v)
		}
	}
	return nil
}

// Can evaluates the matrix: default-deny, admin bypass, direct grants first,
// then group-inherited grants. Entity "*" grants match any entity.
func Can(u User, direct []Right, inherited []Right, module, entity, action string) bool {
	if u.IsAdmin && u.Status == UserActive {
		return true
	}
	if u.Status != UserActive {
		return false
	}
	for _, r := range direct {
		if match(r, module, entity, action) {
			return true
		}
	}
	for _, r := range inherited {
		if match(r, module, entity, action) {
			return true
		}
	}
	return false
}

func match(r Right, module, entity, action string) bool {
	if r.Module != module || r.Action != action {
		return false
	}
	return r.Entity == "*" || r.Entity == entity
}

// CheckPasswordPolicy enforces the baseline (SSO-only accounts pass empty only
// when explicitly marked — enforced at the handler layer, not here).
func CheckPasswordPolicy(pw string) error {
	if len(pw) < MinPasswordLength {
		return fmt.Errorf("identity: password must be at least %d characters", MinPasswordLength)
	}
	return nil
}

// argon2id parameters (OWASP-reasonable for interactive login).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword returns an encoded argon2id hash ("$ferp-argon2id$v=1$m=..,t=..,p=..$salt$key").
func HashPassword(pw string) (string, error) {
	if err := CheckPasswordPolicy(pw); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity: salt: %w", err)
	}
	hash := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$ferp-argon2id$v=1$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword checks pw against an encoded hash; malformed hashes fail closed.
func VerifyPassword(encoded, pw string) bool {
	var mem, tt, pp uint32
	var saltB64, hashB64 string
	n, err := fmt.Sscanf(encoded, "$ferp-argon2id$v=1$m=%d,t=%d,p=%d$", &mem, &tt, &pp)
	if err != nil || n != 3 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "ferp-argon2id" || parts[2] != "v=1" {
		return false
	}
	saltB64, hashB64 = parts[4], parts[5]
	salt, err1 := base64.RawStdEncoding.DecodeString(saltB64)
	want, err2 := base64.RawStdEncoding.DecodeString(hashB64)
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, tt, mem, uint8(pp), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RegisterFailure counts a failed login; locks the account at the threshold.
// Returns the updated user and whether it just became locked.
func RegisterFailure(u User, now time.Time) (User, bool) {
	u.FailedAttempts++
	if u.FailedAttempts >= MaxFailedAttempts {
		u.Status = UserLocked
		until := now.Add(LockoutDuration)
		u.LockedUntil = &until
		return u, true
	}
	return u, false
}

// RegisterSuccess resets counters and unlocks expired lockouts.
// Returns false when login must still be rejected (disabled, or lockout active).
func RegisterSuccess(u User, now time.Time) (User, bool) {
	if u.Status == UserDisabled {
		return u, false
	}
	if u.Status == UserLocked {
		if u.LockedUntil == nil || now.Before(*u.LockedUntil) {
			return u, false
		}
		u.Status = UserActive
	}
	u.FailedAttempts = 0
	u.LockedUntil = nil
	return u, true
}

// LoginAllowed reports whether a login attempt may proceed (throttling gate).
func LoginAllowed(u User, now time.Time) error {
	switch {
	case u.Status == UserDisabled:
		return errors.New("identity: account disabled")
	case u.Status == UserLocked && (u.LockedUntil == nil || now.Before(*u.LockedUntil)):
		return errors.New("identity: account temporarily locked")
	}
	return nil
}
