// Package portal is the authenticated customer self-service surface
// (Phase 2 WebPortal): a customer sees their own invoices (sales seam),
// accepts their quotes (proposal→signed via the sales seam), and opens
// support tickets (services seam). Cross-customer isolation is structural:
// every operation is scoped to the org bound to the caller's token.
//
// Customer auth is an opaque bearer token bound to a partners contact.
// Tokens are 256-bit random hex; only SHA256(salt‖token) is persisted
// (salt per token). SHA256 — not identity's argon2id helper — is the right
// primitive here, and the choice is deliberate:
//   - these tokens are machine-generated with 256 bits of entropy, so the
//     threat model is database-read exfiltration, not human-password
//     guessing; a salted fast hash suffices and keeps per-request auth cheap;
//   - identity.HashPassword additionally enforces a human-password policy
//     (min length) that makes no sense for random tokens;
//   - importing identity for the helper would be cycle-safe today (identity
//     only imports platform) but would couple customer auth to the staff
//     password-hashing parameters; the portal owns its token scheme instead.
package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// TokenTTL is the default lifetime for freshly minted portal tokens.
const TokenTTL = 90 * 24 * time.Hour

// PortalToken is one customer bearer credential (ferp_portal_tokens row).
// The raw token is shown exactly once at mint time and never stored.
type PortalToken struct {
	ID        int64      `json:"id"`
	EntityID  int64      `json:"entity_id"`
	OrgID     int64      `json:"org_id"`     // customer organization (isolation scope)
	ContactID *int64     `json:"contact_id"` // partners contact, optional binding
	Salt      string     `json:"-"`
	TokenHash string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Validate checks token-row invariants.
func (t PortalToken) Validate() error {
	if t.EntityID <= 0 {
		return fmt.Errorf("portal: entity_id required: %w", platform.ErrValidation)
	}
	if t.OrgID <= 0 {
		return fmt.Errorf("portal: org_id required: %w", platform.ErrValidation)
	}
	if t.Salt == "" || t.TokenHash == "" {
		return fmt.Errorf("portal: salt and hash required: %w", platform.ErrValidation)
	}
	if t.ExpiresAt.IsZero() {
		return fmt.Errorf("portal: expires_at required: %w", platform.ErrValidation)
	}
	return nil
}

// Live reports whether the token still authenticates at now.
func (t PortalToken) Live(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// MintToken generates a random 256-bit hex bearer token.
func MintToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("portal: entropy: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// MintSalt generates a random 128-bit hex salt (one per token).
func MintSalt() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("portal: entropy: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// HashToken returns hex(SHA256(salt‖raw)) for storage/comparison.
func HashToken(salt, raw string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + raw))
	return hex.EncodeToString(sum[:])
}

// VerifyToken compares a presented token against a stored row in constant time.
func VerifyToken(t PortalToken, raw string) bool {
	if t.Salt == "" || t.TokenHash == "" || strings.TrimSpace(raw) == "" {
		return false
	}
	got := HashToken(t.Salt, raw)
	return subtle.ConstantTimeCompare([]byte(got), []byte(t.TokenHash)) == 1
}

// Identity is the authenticated customer carried in the request context.
type Identity struct {
	EntityID  int64
	OrgID     int64
	ContactID *int64
	TokenID   int64
}

type identityKey struct{}

// ContextWithIdentity carries the portal customer for IdentityOf.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityOf resolves the portal customer; requests without one get
// (Identity{}, ErrUnauthorized) so handlers answer 401 via WriteError.
func IdentityOf(r *http.Request) (Identity, error) {
	return IdentityOfCtx(r.Context())
}

// IdentityOfCtx resolves the portal customer from a context (service layer).
func IdentityOfCtx(ctx context.Context) (Identity, error) {
	if id, ok := ctx.Value(identityKey{}).(Identity); ok && id.OrgID != 0 {
		return id, nil
	}
	return Identity{}, platform.ErrUnauthorized
}
