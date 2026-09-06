package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Token lifetimes.
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
)

// Claims carried by dev JWTs (production: Keycloak tokens validated via Verifier).
type Claims struct {
	Subject  int64  `json:"sub"`
	EntityID int64  `json:"entity"`
	Admin    bool   `json:"admin"`
	Expiry   int64  `json:"exp"`
	IssuedAt int64  `json:"iat"`
	ID       string `json:"jti"`
}

// Issuer signs and validates HS256 JWTs for development and tests.
// Production fronts Keycloak; Issuer remains the local-session fallback.
type Issuer struct {
	secret []byte
	now    func() time.Time
}

// NewIssuer builds an Issuer; empty secrets are rejected (fail closed).
func NewIssuer(secret string) (*Issuer, error) {
	if secret == "" {
		return nil, errors.New("identity: empty JWT secret")
	}
	return &Issuer{secret: []byte(secret), now: time.Now}, nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// IssueAccess mints a short-lived access token for u.
func (iss *Issuer) IssueAccess(u User) (string, error) {
	now := iss.now().UTC()
	jti := make([]byte, 12)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	c := Claims{Subject: u.ID, EntityID: u.EntityID, Admin: u.IsAdmin,
		Expiry: now.Add(AccessTokenTTL).Unix(), IssuedAt: now.Unix(), ID: hex.EncodeToString(jti)}
	return iss.sign(c)
}

func (iss *Issuer) sign(c Claims) (string, error) {
	header := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := b64url(payloadBytes)
	mac := hmac.New(sha256.New, iss.secret)
	mac.Write([]byte(header + "." + payload))
	return header + "." + payload + "." + b64url(mac.Sum(nil)), nil
}

// Verify parses and authenticates a token; expired/forged tokens fail closed.
func (iss *Issuer) Verify(token string) (Claims, error) {
	var zero Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return zero, errors.New("identity: malformed token")
	}
	mac := hmac.New(sha256.New, iss.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := b64url(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return zero, errors.New("identity: bad token signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, errors.New("identity: bad token payload")
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return zero, errors.New("identity: bad token claims")
	}
	if iss.now().UTC().Unix() > c.Expiry {
		return zero, errors.New("identity: token expired")
	}
	return c, nil
}

// MintRefresh creates an opaque refresh token and returns it with its SHA-256 hex
// (only the hash is stored — Dolibarr llx_session equivalent without replay value).
func MintRefresh() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// sha256hex hashes an opaque token for storage/lookup.
func sha256hex(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
// The KeycloakVerifier (below) is a config carrier today; full signature
// validation against the realm JWKS lands with the production identity wiring.
type Verifier interface {
	VerifyOIDC(ctx context.Context, idToken string) (subject string, email string, err error)
}

// KeycloakConfig carries realm connection settings (env: FERP_OIDC_*).
type KeycloakConfig struct {
	IssuerURL string
	Realm     string
	ClientID  string
	Enabled   bool
}

// LoadKeycloakConfig reads FERP_OIDC_* env (helpers live in platform; kept here to
// avoid an import cycle — values are plain strings).
func LoadKeycloakConfig(get func(key, def string) string) KeycloakConfig {
	iss := get("FERP_OIDC_ISSUER", "")
	return KeycloakConfig{
		IssuerURL: iss,
		Realm:     get("FERP_OIDC_REALM", "forgeerp"),
		ClientID:  get("FERP_OIDC_CLIENT_ID", "forgeerp-api"),
		Enabled:   iss != "",
	}
}

// ErrOIDCNotConfigured is returned when no realm is configured.
var ErrOIDCNotConfigured = errors.New("identity: OIDC not configured")

// StaticVerifier maps fixed tokens to identities — tests and offline development only.
type StaticVerifier struct {
	Users map[string]User // token -> user
}

// VerifyOIDC implements Verifier for offline use.
func (s StaticVerifier) VerifyOIDC(_ context.Context, idToken string) (string, string, error) {
	u, ok := s.Users[idToken]
	if !ok {
		return "", "", fmt.Errorf("identity: unknown test token")
	}
	return fmt.Sprint(u.ID), u.Email, nil
}
