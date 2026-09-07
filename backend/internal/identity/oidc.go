package identity

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// jwksKey is one RSA verification key from a JWKS document.
type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// jwksDoc is a parsed JWKS key set.
type jwksDoc struct {
	Keys []jwksKey `json:"keys"`
}

// discoveryDoc is the subset of OIDC discovery metadata we need.
type discoveryDoc struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// JWKSVerifier validates OIDC id tokens (RS256) against a realm JWKS using
// only the standard library. It replaces the config-carrier stub so Keycloak
// SSO is production-capable without new dependencies.
type JWKSVerifier struct {
	issuer   string
	audience string
	jwksURL  string
	client   *http.Client
	now      func() time.Time

	mu       sync.Mutex
	keys     map[string]*rsa.PublicKey
	fetched  time.Time
	cacheTTL time.Duration
}

// NewKeycloakVerifier discovers the JWKS endpoint from Keycloak OIDC
// discovery ({issuer}/realms/{realm}/.well-known/openid-configuration).
// cfg.IssuerURL must be the realm issuer, e.g.
// http://keycloak:8080/realms/forgeerp.
func NewKeycloakVerifier(cfg KeycloakConfig, client *http.Client) (*JWKSVerifier, error) {
	if !cfg.Enabled {
		return nil, ErrOIDCNotConfigured
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	issuer := strings.TrimRight(cfg.IssuerURL, "/")
	discovery := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequest(http.MethodGet, discovery, nil)
	if err != nil {
		return nil, fmt.Errorf("identity: discovery request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity: discovery fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("identity: discovery status %d", resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("identity: discovery decode: %w", err)
	}
	if doc.JWKSURI == "" {
		return nil, errors.New("identity: discovery missing jwks_uri")
	}
	return &JWKSVerifier{
		issuer:   doc.Issuer,
		audience: cfg.ClientID,
		jwksURL:  doc.JWKSURI,
		client:   client,
		now:      time.Now,
		cacheTTL: 10 * time.Minute,
		keys:     map[string]*rsa.PublicKey{},
	}, nil
}

// VerifyOIDC implements Verifier: RS256 signature, issuer, audience, expiry.
func (v *JWKSVerifier) VerifyOIDC(ctx context.Context, idToken string) (subject string, email string, err error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", "", errors.New("identity: malformed token")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", errors.New("identity: bad token header")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return "", "", errors.New("identity: bad token header")
	}
	if header.Alg != "RS256" {
		return "", "", fmt.Errorf("identity: unexpected alg %q", header.Alg)
	}
	if header.Kid == "" {
		return "", "", errors.New("identity: missing kid")
	}
	key, err := v.keyFor(ctx, header.Kid)
	if err != nil {
		return "", "", err
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errors.New("identity: bad token payload")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", errors.New("identity: bad token signature")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return "", "", errors.New("identity: signature mismatch")
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Iss   string `json:"iss"`
		Aud   any    `json:"aud"`
		Exp   int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return "", "", errors.New("identity: bad token claims")
	}
	if claims.Sub == "" {
		return "", "", errors.New("identity: missing sub")
	}
	if claims.Iss != v.issuer {
		return "", "", errors.New("identity: issuer mismatch")
	}
	if !audContains(claims.Aud, v.audience) {
		return "", "", errors.New("identity: audience mismatch")
	}
	if claims.Exp == 0 || v.now().Unix() > claims.Exp {
		return "", "", errors.New("identity: token expired")
	}
	return claims.Sub, claims.Email, nil
}

// audContains accepts string or []any audiences.
func audContains(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// keyFor returns the cached RSA key for kid, refreshing the JWKS on miss/expiry.
func (v *JWKSVerifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if key, ok := v.keys[kid]; ok && time.Since(v.fetched) < v.cacheTTL {
		return key, nil
	}
	keys, err := v.fetchJWKS(ctx)
	if err != nil {
		return nil, err
	}
	v.keys = keys
	v.fetched = time.Now()
	key, ok := keys[kid]
	if !ok {
		return nil, errors.New("identity: unknown kid")
	}
	return key, nil
}

// fetchJWKS downloads and parses RSA verification keys.
func (v *JWKSVerifier) fetchJWKS(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("identity: jwks request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity: jwks fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("identity: jwks status %d", resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("identity: jwks decode: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := rsaKey(k.N, k.E)
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("identity: no RSA keys in JWKS")
	}
	return out, nil
}

// rsaKey builds a public key from base64url n/e.
func rsaKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nRaw, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eRaw, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	if len(eRaw) > 8 {
		return nil, errors.New("identity: oversized exponent")
	}
	padded := make([]byte, 8)
	copy(padded[8-len(eRaw):], eRaw)
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nRaw),
		E: int(binary.BigEndian.Uint64(padded)),
	}, nil
}
