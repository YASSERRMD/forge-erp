package identity

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testRealm spins discovery + JWKS endpoints backed by key, returning the
// issuer URL and a mint function for tokens.
func testRealm(t *testing.T, key *rsa.PrivateKey) (issuer string, mint func(sub, email, aud string, exp int64, kid string) string) {
	t.Helper()
	n := b64url(key.PublicKey.N.Bytes())
	e := b64url(big.NewInt(int64(key.PublicKey.E)).Bytes())
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/test/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   "http://" + r.Host + "/realms/test",
			"jwks_uri": "http://" + r.Host + "/realms/test/protocol/openid-connect/certs",
		})
	})
	mux.HandleFunc("/realms/test/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256", "n": n, "e": e},
		}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	issuer = srv.URL + "/realms/test"
	mint = func(sub, email, aud string, exp int64, kid string) string {
		header := b64url([]byte(fmt.Sprintf(`{"alg":"RS256","kid":%q,"typ":"JWT"}`, kid)))
		body, _ := json.Marshal(map[string]any{
			"sub": sub, "email": email, "iss": issuer, "aud": aud, "exp": exp,
		})
		payload := b64url(body)
		sum := sha256.Sum256([]byte(header + "." + payload))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return header + "." + payload + "." + b64url(sig)
	}
	return issuer, mint
}

func TestJWKSVerifierVectors(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	issuer, mint := testRealm(t, key)
	cfg := KeycloakConfig{IssuerURL: issuer, Realm: "test", ClientID: "forgeerp-api", Enabled: true}
	v, err := NewKeycloakVerifier(cfg, nil)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	future := time.Now().Add(time.Hour).Unix()
	sub, email, err := v.VerifyOIDC(t.Context(), mint("u1", "u1@x.io", "forgeerp-api", future, "k1"))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if sub != "u1" || email != "u1@x.io" {
		t.Fatalf("claims=%q %q", sub, email)
	}
	// Tampered payload.
	tok := mint("u1", "u1@x.io", "forgeerp-api", future, "k1")
	parts := splitToken(tok)
	bad := parts[0] + "." + b64url([]byte(`{"sub":"mallory"}`)) + "." + parts[2]
	if _, _, err := v.VerifyOIDC(t.Context(), bad); err == nil {
		t.Error("tampered payload accepted")
	}
	// Expired.
	if _, _, err := v.VerifyOIDC(t.Context(), mint("u1", "", "forgeerp-api", time.Now().Add(-time.Hour).Unix(), "k1")); err == nil {
		t.Error("expired token accepted")
	}
	// Wrong audience.
	if _, _, err := v.VerifyOIDC(t.Context(), mint("u1", "", "other-client", future, "k1")); err == nil {
		t.Error("wrong audience accepted")
	}
	// Unknown kid.
	if _, _, err := v.VerifyOIDC(t.Context(), mint("u1", "", "forgeerp-api", future, "nope")); err == nil {
		t.Error("unknown kid accepted")
	}
}

func splitToken(tok string) []string {
	var out []string
	start := 0
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			out = append(out, tok[start:i])
			start = i + 1
		}
	}
	return append(out, tok[start:])
}

func TestKeycloakVerifierDisabled(t *testing.T) {
	if _, err := NewKeycloakVerifier(KeycloakConfig{}, nil); err != ErrOIDCNotConfigured {
		t.Fatalf("disabled: err=%v", err)
	}
}
