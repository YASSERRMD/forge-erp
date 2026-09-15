package identity

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

var oauthTestKey = []byte{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
}

func testOAuthMemory(t *testing.T) *OAuthMemoryStore {
	t.Helper()
	m, err := NewOAuthMemoryStore(oauthTestKey)
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	return m
}

func TestLoadOAuthKeyVectors(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 printable bytes (raw form)
	t.Setenv("FERP_OAUTH_KEY", string(key))
	if got, err := LoadOAuthKey(); err != nil || string(got) != string(key) {
		t.Fatalf("raw key: %v", err)
	}
	t.Setenv("FERP_OAUTH_KEY", hex.EncodeToString(key))
	if got, err := LoadOAuthKey(); err != nil || string(got) != string(key) {
		t.Fatalf("hex key: %v", err)
	}
	t.Setenv("FERP_OAUTH_KEY", base64.StdEncoding.EncodeToString(key))
	if got, err := LoadOAuthKey(); err != nil || string(got) != string(key) {
		t.Fatalf("base64 key: %v", err)
	}
	for name, val := range map[string]string{
		"unset": "", "short": "abc", "badhex": "zzzz",
		"shortraw": "only-sixteen-bytes!",
	} {
		t.Setenv("FERP_OAUTH_KEY", val)
		if _, err := LoadOAuthKey(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := NewOAuthMemoryStore([]byte("short")); err == nil {
		t.Error("short store key accepted")
	}
}

func TestOAuthSealRoundtrip(t *testing.T) {
	c, err := newOAuthCrypter(oauthTestKey)
	if err != nil {
		t.Fatalf("crypter: %v", err)
	}
	sealed, err := c.Seal("access-secret-value")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if string(sealed) == "access-secret-value" || strings.Contains(string(sealed), "access-secret") {
		t.Fatal("ciphertext leaks plaintext")
	}
	again, err := c.Seal("access-secret-value")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if string(sealed) == string(again) {
		t.Error("nonces must differ between seals")
	}
	if got, err := c.Open(sealed); err != nil || got != "access-secret-value" {
		t.Fatalf("open: %q %v", got, err)
	}
	// Tamper → fail closed.
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := c.Open(tampered); err == nil {
		t.Error("tampered ciphertext opened")
	}
	if _, err := c.Open([]byte("short")); err == nil {
		t.Error("truncated ciphertext opened")
	}
	// Wrong key → fail closed.
	other, _ := newOAuthCrypter(bytes32(9))
	if _, err := other.Open(sealed); err == nil {
		t.Error("wrong key opened ciphertext")
	}
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestOAuthMemoryFlow(t *testing.T) {
	ctx := context.Background()
	m := testOAuthMemory(t)
	exp := time.Now().UTC().Add(time.Hour)

	meta, err := m.SaveToken(ctx, nil, 1, "paypal", "merchant-1", "acc-1", "ref-1", exp, []string{"read", "write"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if meta.ID == 0 || meta.RowVersion != 1 || !strings.HasPrefix(meta.TokenRef, "paypal:") {
		t.Fatalf("bad meta: %+v", meta)
	}
	if strings.Contains(meta.TokenRef, "acc-1") {
		t.Fatalf("token ref leaks secret: %q", meta.TokenRef)
	}
	// Ciphertext at rest, never plaintext (same-package introspection).
	stored := m.rows[oauthKey(1, "paypal", "merchant-1")]
	if strings.Contains(string(stored.accessSealed), "acc-1") ||
		strings.Contains(string(stored.refreshSealed), "ref-1") {
		t.Fatal("plaintext persisted in memory store")
	}

	access, refresh, gotExp, err := m.FetchSecrets(ctx, nil, 1, "paypal", "merchant-1")
	if err != nil || access != "acc-1" || refresh != "ref-1" || !gotExp.Equal(exp) {
		t.Fatalf("fetch: %q %q %v %v", access, refresh, gotExp, err)
	}
	got, err := m.Meta(ctx, nil, 1, "paypal", "merchant-1")
	if err != nil || got.TokenRef != meta.TokenRef {
		t.Fatalf("meta: %+v %v", got, err)
	}

	// Upsert rotates material on the same row.
	meta2, err := m.SaveToken(ctx, nil, 1, "paypal", "merchant-1", "acc-2", "ref-2", exp, nil)
	if err != nil {
		t.Fatalf("resave: %v", err)
	}
	if meta2.ID != meta.ID || meta2.RowVersion != 2 || meta2.TokenRef == meta.TokenRef {
		t.Fatalf("rotation must bump version + ref on same row: %+v", meta2)
	}

	// Per-entity / per-owner isolation.
	if _, err := m.SaveToken(ctx, nil, 2, "paypal", "merchant-1", "acc-x", "ref-x", exp, nil); err != nil {
		t.Fatalf("entity 2 save: %v", err)
	}
	if _, _, _, err := m.FetchSecrets(ctx, nil, 2, "paypal", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner fetch err=%v want not-found", err)
	}
	if _, _, _, err := m.FetchSecrets(ctx, nil, 3, "paypal", "merchant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-entity fetch err=%v want not-found", err)
	}
	list1, _ := m.ListMeta(ctx, nil, 1)
	list2, _ := m.ListMeta(ctx, nil, 2)
	if len(list1) != 1 || len(list2) != 1 {
		t.Fatalf("list scoping: %d %d", len(list1), len(list2))
	}

	// Delete + validation.
	if err := m.DeleteToken(ctx, nil, 1, "paypal", "merchant-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := m.DeleteToken(ctx, nil, 1, "paypal", "merchant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err=%v want not-found", err)
	}
	for name, tc := range map[string]struct {
		entity         int64
		provider, owner, access string
		exp            time.Time
	}{
		"entity":   {0, "p", "o", "a", exp},
		"provider": {1, "", "o", "a", exp},
		"owner":    {1, "p", "", "a", exp},
		"access":   {1, "p", "o", "", exp},
		"expiry":   {1, "p", "o", "a", time.Time{}},
	} {
		if _, err := m.SaveToken(ctx, nil, tc.entity, tc.provider, tc.owner, tc.access, "r", tc.exp, nil); !errors.Is(err, platform.ErrValidation) {
			t.Errorf("%s: err=%v want validation", name, err)
		}
	}
}

func TestOAuthRefreshFlow(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("fresh token never calls refresher", func(t *testing.T) {
		m := testOAuthMemory(t)
		meta, err := m.SaveToken(ctx, nil, 1, "stripe", "acct", "fresh-acc", "fresh-ref", now.Add(time.Hour), nil)
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		called := false
		got, refreshed, err := m.RefreshToken(ctx, nil, 1, "stripe", "acct", now, time.Minute,
			func(_ context.Context, _ string) (string, string, time.Time, error) {
				called = true
				return "x", "y", now.Add(time.Hour), nil
			})
		if err != nil || refreshed || called || got.TokenRef != meta.TokenRef {
			t.Fatalf("fresh refresh: refreshed=%v called=%v err=%v", refreshed, called, err)
		}
	})

	t.Run("expired token rotates", func(t *testing.T) {
		m := testOAuthMemory(t)
		if _, err := m.SaveToken(ctx, nil, 1, "stripe", "acct", "old-acc", "old-ref", now.Add(-time.Minute), nil); err != nil {
			t.Fatalf("save: %v", err)
		}
		var seenRefresh string
		got, refreshed, err := m.RefreshToken(ctx, nil, 1, "stripe", "acct", now, time.Minute,
			func(_ context.Context, rt string) (string, string, time.Time, error) {
				seenRefresh = rt
				return "new-acc", "new-ref", now.Add(time.Hour), nil
			})
		if err != nil || !refreshed {
			t.Fatalf("expired refresh: refreshed=%v err=%v", refreshed, err)
		}
		if seenRefresh != "old-ref" {
			t.Fatalf("refresher got %q want stored refresh token", seenRefresh)
		}
		if got.RowVersion != 2 {
			t.Fatalf("version=%d want 2", got.RowVersion)
		}
		access, refresh, _, err := m.FetchSecrets(ctx, nil, 1, "stripe", "acct")
		if err != nil || access != "new-acc" || refresh != "new-ref" {
			t.Fatalf("rotated fetch: %q %q %v", access, refresh, err)
		}
	})

	t.Run("skew triggers early refresh", func(t *testing.T) {
		m := testOAuthMemory(t)
		if _, err := m.SaveToken(ctx, nil, 1, "p", "o", "a", "r", now.Add(5*time.Minute), nil); err != nil {
			t.Fatalf("save: %v", err)
		}
		_, refreshed, err := m.RefreshToken(ctx, nil, 1, "p", "o", now, 10*time.Minute,
			func(_ context.Context, _ string) (string, string, time.Time, error) {
				return "a2", "", now.Add(time.Hour), nil
			})
		if err != nil || !refreshed {
			t.Fatalf("skewed refresh: %v %v", refreshed, err)
		}
		// Empty returned refresh keeps the stored one.
		_, kept, _, err := m.FetchSecrets(ctx, nil, 1, "p", "o")
		if err != nil || kept != "r" {
			t.Fatalf("refresh keep: %q %v", kept, err)
		}
	})

	t.Run("refresher failure keeps old row", func(t *testing.T) {
		m := testOAuthMemory(t)
		if _, err := m.SaveToken(ctx, nil, 1, "p", "o", "old-a", "old-r", now.Add(-time.Hour), nil); err != nil {
			t.Fatalf("save: %v", err)
		}
		fail := errors.New("provider down")
		if _, _, err := m.RefreshToken(ctx, nil, 1, "p", "o", now, 0,
			func(_ context.Context, _ string) (string, string, time.Time, error) {
				return "", "", time.Time{}, fail
			}); !errors.Is(err, fail) {
			t.Fatalf("err=%v want provider failure", err)
		}
		access, _, _, err := m.FetchSecrets(ctx, nil, 1, "p", "o")
		if err != nil || access != "old-a" {
			t.Fatalf("old row lost: %q %v", access, err)
		}
		// Empty access material is a validation error, row untouched.
		if _, _, err := m.RefreshToken(ctx, nil, 1, "p", "o", now, 0,
			func(_ context.Context, _ string) (string, string, time.Time, error) {
				return "", "r", now.Add(time.Hour), nil
			}); !errors.Is(err, platform.ErrValidation) {
			t.Fatalf("empty material err=%v want validation", err)
		}
	})

	t.Run("missing refresh token and nil fn", func(t *testing.T) {
		m := testOAuthMemory(t)
		if _, err := m.SaveToken(ctx, nil, 1, "p", "o", "a", "", now.Add(-time.Hour), nil); err != nil {
			t.Fatalf("save: %v", err)
		}
		okFn := func(_ context.Context, _ string) (string, string, time.Time, error) {
			return "a", "r", now.Add(time.Hour), nil
		}
		if _, _, err := m.RefreshToken(ctx, nil, 1, "p", "o", now, 0, okFn); !errors.Is(err, platform.ErrValidation) {
			t.Fatalf("missing refresh err=%v want validation", err)
		}
		if _, _, err := m.RefreshToken(ctx, nil, 1, "p", "o", now, 0, nil); !errors.Is(err, platform.ErrValidation) {
			t.Fatalf("nil fn err=%v want validation", err)
		}
		if _, _, err := m.RefreshToken(ctx, nil, 9, "p", "o", now, 0, okFn); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-entity refresh err=%v want not-found", err)
		}
	})
}

func TestOAuthRefresherHTTP(t *testing.T) {
	var gotForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = map[string]string{
			"grant_type":    r.Form.Get("grant_type"),
			"refresh_token": r.Form.Get("refresh_token"),
			"client_id":     r.Form.Get("client_id"),
		}
		if r.Form.Get("client_secret") != "s3cr3t" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		if r.Form.Get("refresh_token") == "bad" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "n-acc", "refresh_token": "n-ref", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	r := &OAuthRefresher{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "s3cr3t"}
	before := time.Now().UTC()
	access, refresh, exp, err := r.Refresh(context.Background(), "old-ref")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if access != "n-acc" || refresh != "n-ref" {
		t.Fatalf("material: %q %q", access, refresh)
	}
	if exp.Before(before.Add(59*time.Minute)) || exp.After(before.Add(61*time.Minute)) {
		t.Fatalf("expiry not ~3600s: %v", exp)
	}
	if gotForm["grant_type"] != "refresh_token" || gotForm["refresh_token"] != "old-ref" || gotForm["client_id"] != "cid" {
		t.Fatalf("form: %+v", gotForm)
	}
	// Provider rejection carries status, never the secret.
	if _, _, _, err := r.Refresh(context.Background(), "bad"); err == nil ||
		!strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("bad grant err=%v", err)
	}
	badCreds := &OAuthRefresher{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "wrong"}
	if _, _, _, err := badCreds.Refresh(context.Background(), "old-ref"); err == nil {
		t.Fatal("bad creds accepted")
	}
	unconfigured := &OAuthRefresher{}
	if _, _, _, err := unconfigured.Refresh(context.Background(), "x"); err == nil {
		t.Error("empty token URL accepted")
	}
}

func TestOAuthPGFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st, err := NewOAuthPGStore(pool, oauthTestKey)
	if err != nil {
		t.Fatalf("pg store: %v", err)
	}
	now := time.Now().UTC()
	exp := now.Add(time.Hour)

	meta, err := st.SaveToken(ctx, pool, 1, "paypal", "merchant-1", "pg-acc", "pg-ref", exp, []string{"read"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// Raw row holds ciphertext only — never the plaintext.
	var sealed []byte
	if err := pool.QueryRow(ctx, `SELECT access_sealed FROM ferp_oauth_tokens WHERE id=$1`, meta.ID).Scan(&sealed); err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if strings.Contains(string(sealed), "pg-acc") {
		t.Fatal("plaintext persisted in postgres")
	}
	access, refresh, _, err := st.FetchSecrets(ctx, pool, 1, "paypal", "merchant-1")
	if err != nil || access != "pg-acc" || refresh != "pg-ref" {
		t.Fatalf("fetch: %q %q %v", access, refresh, err)
	}
	list, err := st.ListMeta(ctx, pool, 1)
	if err != nil || len(list) != 1 || list[0].TokenRef != meta.TokenRef {
		t.Fatalf("list: %+v %v", list, err)
	}

	// Cross-entity isolation via a second tenant.
	ent2 := pgtest.NewEntity(t, pool, "oauth-ent-2")
	if _, _, _, err := st.FetchSecrets(ctx, pool, ent2, "paypal", "merchant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-entity fetch err=%v want not-found", err)
	}
	if _, _, err := st.RefreshToken(ctx, pool, ent2, "paypal", "merchant-1", now, 0,
		func(_ context.Context, _ string) (string, string, time.Time, error) {
			return "x", "y", now.Add(time.Hour), nil
		}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-entity refresh err=%v want not-found", err)
	}
	if err := st.DeleteToken(ctx, pool, ent2, "paypal", "merchant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-entity delete err=%v want not-found", err)
	}

	// Expired row refreshes through the injected flow with row-version bump.
	if _, err := st.SaveToken(ctx, pool, 1, "stripe", "acct", "old-a", "old-r", now.Add(-time.Hour), nil); err != nil {
		t.Fatalf("save expired: %v", err)
	}
	got, refreshed, err := st.RefreshToken(ctx, pool, 1, "stripe", "acct", now, time.Minute,
		func(_ context.Context, rt string) (string, string, time.Time, error) {
			if rt != "old-r" {
				t.Errorf("refresher got %q", rt)
			}
			return "new-a", "new-r", now.Add(time.Hour), nil
		})
	if err != nil || !refreshed || got.RowVersion != 2 {
		t.Fatalf("pg refresh: %+v %v %v", got, refreshed, err)
	}
	if access, _, _, err := st.FetchSecrets(ctx, pool, 1, "stripe", "acct"); err != nil || access != "new-a" {
		t.Fatalf("post-refresh fetch: %q %v", access, err)
	}

	if err := st.DeleteToken(ctx, pool, 1, "paypal", "merchant-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, _, err := st.FetchSecrets(ctx, pool, 1, "paypal", "merchant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-delete fetch err=%v want not-found", err)
	}
}
