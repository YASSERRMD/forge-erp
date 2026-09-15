package identity

// Outbound OAuth token vault (Phase 2 Stripe/PayPal + OAuth depth).
//
// This file is deliberately distinct from the INBOUND OIDC login path
// (oidc.go, handler.go OIDCLogin): those verify tokens that authenticate
// USERS to us. This vault stores tokens that authenticate US (per entity,
// per provider) to THIRD PARTIES — e.g. a PayPal/Stripe connected account or
// any OAuth2 integration needing offline refresh.
//
// Security posture (honest scope — no external crypto review available):
//   - Tokens are sealed with AES-256-GCM under FERP_OAUTH_KEY (32 bytes,
//     hex/base64/raw via LoadOAuthKey) with a fresh random nonce per value.
//   - Only ciphertext + a TokenRef (truncated SHA-256 over the access token,
//     log-safe) are persisted. Raw secrets are NEVER logged, NEVER returned
//     by list/meta APIs, and only leave the vault through FetchSecrets / the
//     refresh path, in memory.
//   - Key management limits are documented on LoadOAuthKey (no rotation or
//     HSM support; key comes from OS env only). The HTTP admin surface for
//     token connect/disconnect is intentionally deferred (see RefreshToken).
//
// All state changes are per-entity isolated: cross-entity access returns
// ErrNotFound, never the row.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// LoadOAuthKey reads FERP_OAUTH_KEY and returns the 32-byte AES key. The env
// value may be 32 raw bytes, or hex/base64 of 32 bytes. Empty or malformed
// values fail closed. LIMITS (no crypto review): single key, no rotation —
// rotating the env value orphans previously sealed rows (re-connect required);
// the key must come from the process environment / secret manager, never from
// the repo, logs, or the database.
func LoadOAuthKey() ([]byte, error) {
	raw := os.Getenv("FERP_OAUTH_KEY")
	if raw == "" {
		return nil, errors.New("identity: FERP_OAUTH_KEY unset")
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	if dec, err := hex.DecodeString(strings.TrimSpace(raw)); err == nil && len(dec) == 32 {
		return dec, nil
	}
	if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw)); err == nil && len(dec) == 32 {
		return dec, nil
	}
	if dec, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw)); err == nil && len(dec) == 32 {
		return dec, nil
	}
	return nil, errors.New("identity: FERP_OAUTH_KEY must be 32 bytes (raw, hex or base64)")
}

// OAuthCrypter seals token material with AES-256-GCM. Zero value is unusable;
// build via newOAuthCrypter (key length enforced).
type OAuthCrypter struct {
	gcm cipher.AEAD
}

func newOAuthCrypter(key []byte) (*OAuthCrypter, error) {
	if len(key) != 32 {
		return nil, errors.New("identity: oauth key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("identity: oauth cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("identity: oauth gcm: %w", err)
	}
	return &OAuthCrypter{gcm: gcm}, nil
}

// Seal encrypts plaintext with a fresh random nonce; output is nonce‖ciphertext.
func (c *OAuthCrypter) Seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("identity: oauth nonce: %w", err)
	}
	return c.gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts nonce‖ciphertext produced by Seal; tampered input fails closed.
func (c *OAuthCrypter) Open(sealed []byte) (string, error) {
	if len(sealed) < c.gcm.NonceSize() {
		return "", errors.New("identity: oauth sealed value too short")
	}
	plain, err := c.gcm.Open(nil, sealed[:c.gcm.NonceSize()], sealed[c.gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("identity: oauth open failed")
	}
	return string(plain), nil
}

// tokenRef derives the log-safe reference for access material (truncated
// SHA-256 hex — a fingerprint, never the secret).
func tokenRef(provider, access string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + access))
	return provider + ":" + hex.EncodeToString(sum[:])[:12]
}

// OAuthTokenMeta is the safe view of a vault row: identity + expiry only.
// Ciphertext and plaintext NEVER appear here (lists and logs use this type).
type OAuthTokenMeta struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Provider   string    `json:"provider"`
	Owner      string    `json:"owner"`
	TokenRef   string    `json:"token_ref"`
	ExpiresAt  time.Time `json:"expires_at"`
	Scopes     []string  `json:"scopes"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	RowVersion int64     `json:"row_version"`
}

// Expired reports whether the token must be refreshed as of now (skew gives
// early-refresh headroom so in-flight calls don't race the expiry).
func (m OAuthTokenMeta) Expired(now time.Time, skew time.Duration) bool {
	return !now.Add(skew).Before(m.ExpiresAt)
}

// oauthRow is the persisted shape (ciphertext only — no plaintext field exists).
type oauthRow struct {
	meta         OAuthTokenMeta
	accessSealed []byte
	refreshSealed []byte
	scopesJoined string
}

// OAuthRefreshFunc exchanges a refresh token for fresh material. It is
// injected (OAuthRefresher.Refresh is the HTTP implementation) so expiry and
// rotation are testable without network. An empty returned refresh token
// keeps the previous one; an empty access token is an error.
type OAuthRefreshFunc func(ctx context.Context, refreshToken string) (access, refresh string, expiresAt time.Time, err error)

// OAuthStore is the persistence contract for the outbound token vault.
// PGStore implements it against PostgreSQL; OAuthMemoryStore is the test fake.
type OAuthStore interface {
	SaveToken(ctx context.Context, db platform.DBTX, entityID int64, provider, owner, access, refresh string, expiresAt time.Time, scopes []string) (OAuthTokenMeta, error)
	FetchSecrets(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) (access, refresh string, expiresAt time.Time, err error)
	Meta(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) (OAuthTokenMeta, error)
	ListMeta(ctx context.Context, db platform.DBTX, entityID int64) ([]OAuthTokenMeta, error)
	DeleteToken(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) error
	RefreshToken(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string, now time.Time, skew time.Duration, fn OAuthRefreshFunc) (OAuthTokenMeta, bool, error)
}

func validateOAuthInput(entityID int64, provider, owner, access string, expiresAt time.Time) error {
	if entityID <= 0 {
		return errors.New("identity: oauth entity_id required")
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(owner) == "" {
		return errors.New("identity: oauth provider and owner required")
	}
	if access == "" {
		return errors.New("identity: oauth access token required")
	}
	if expiresAt.IsZero() {
		return errors.New("identity: oauth expiry required")
	}
	return nil
}

// OAuthPGStore implements OAuthStore against PostgreSQL (table
// ferp_oauth_tokens — see backend/migrations/pending_oauth.up.sql).
type OAuthPGStore struct {
	pool    *pgxpool.Pool
	crypter *OAuthCrypter
}

// NewOAuthPGStore wraps a pool with the sealing key (32 bytes).
func NewOAuthPGStore(pool *pgxpool.Pool, key []byte) (*OAuthPGStore, error) {
	c, err := newOAuthCrypter(key)
	if err != nil {
		return nil, err
	}
	return &OAuthPGStore{pool: pool, crypter: c}, nil
}

const oauthCols = `id, entity_id, provider, owner, access_sealed, refresh_sealed, token_ref, expires_at, scopes, created_at, updated_at, row_version`

func scanOAuthRow(row pgx.Row) (oauthRow, error) {
	var r oauthRow
	var refreshNULL []byte
	err := row.Scan(&r.meta.ID, &r.meta.EntityID, &r.meta.Provider, &r.meta.Owner,
		&r.accessSealed, &refreshNULL, &r.meta.TokenRef, &r.meta.ExpiresAt,
		&r.scopesJoined, &r.meta.CreatedAt, &r.meta.UpdatedAt, &r.meta.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthRow{}, ErrNotFound
	}
	if err != nil {
		return oauthRow{}, err
	}
	r.refreshSealed = refreshNULL
	if r.scopesJoined == "" {
		r.meta.Scopes = nil
	} else {
		r.meta.Scopes = strings.Split(r.scopesJoined, ",")
	}
	return r, nil
}

// SaveToken seals and upserts the token for (entity, provider, owner).
func (s *OAuthPGStore) SaveToken(ctx context.Context, db platform.DBTX, entityID int64, provider, owner, access, refresh string, expiresAt time.Time, scopes []string) (OAuthTokenMeta, error) {
	if err := validateOAuthInput(entityID, provider, owner, access, expiresAt); err != nil {
		return OAuthTokenMeta{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	accessSealed, err := s.crypter.Seal(access)
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	refreshSealed, err := s.crypter.Seal(refresh)
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	joined := strings.Join(scopes, ",")
	r, err := scanOAuthRow(db.QueryRow(ctx, `INSERT INTO ferp_oauth_tokens
		(entity_id, provider, owner, access_sealed, refresh_sealed, token_ref, expires_at, scopes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (entity_id, provider, owner) DO UPDATE SET
			access_sealed=EXCLUDED.access_sealed, refresh_sealed=EXCLUDED.refresh_sealed,
			token_ref=EXCLUDED.token_ref, expires_at=EXCLUDED.expires_at, scopes=EXCLUDED.scopes,
			updated_at=now(), row_version=ferp_oauth_tokens.row_version+1
		RETURNING `+oauthCols,
		entityID, provider, owner, accessSealed, refreshSealed, tokenRef(provider, access), expiresAt, joined))
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	return r.meta, nil
}

// oauthRowFor loads the full row (entity-scoped → cross-entity is not-found).
func (s *OAuthPGStore) oauthRowFor(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) (oauthRow, error) {
	return scanOAuthRow(db.QueryRow(ctx, `SELECT `+oauthCols+` FROM ferp_oauth_tokens WHERE entity_id=$1 AND provider=$2 AND owner=$3`, entityID, provider, owner))
}

// FetchSecrets decrypts and returns live material (in-memory only; callers
// must never log or persist the return values).
func (s *OAuthPGStore) FetchSecrets(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) (string, string, time.Time, error) {
	r, err := s.oauthRowFor(ctx, db, entityID, provider, owner)
	if err != nil {
		return "", "", time.Time{}, err
	}
	access, err := s.crypter.Open(r.accessSealed)
	if err != nil {
		return "", "", time.Time{}, err
	}
	refresh, err := s.crypter.Open(r.refreshSealed)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return access, refresh, r.meta.ExpiresAt, nil
}

// Meta returns the safe view (no secrets).
func (s *OAuthPGStore) Meta(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) (OAuthTokenMeta, error) {
	r, err := s.oauthRowFor(ctx, db, entityID, provider, owner)
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	return r.meta, nil
}

// ListMeta lists safe views within the caller's entity (never cross-entity).
func (s *OAuthPGStore) ListMeta(ctx context.Context, db platform.DBTX, entityID int64) ([]OAuthTokenMeta, error) {
	rows, err := db.Query(ctx, `SELECT `+oauthCols+` FROM ferp_oauth_tokens WHERE entity_id=$1 ORDER BY provider, owner`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OAuthTokenMeta
	for rows.Next() {
		r, err := scanOAuthRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.meta)
	}
	return out, rows.Err()
}

// DeleteToken removes the row (entity-scoped).
func (s *OAuthPGStore) DeleteToken(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string) error {
	tag, err := db.Exec(ctx, `DELETE FROM ferp_oauth_tokens WHERE entity_id=$1 AND provider=$2 AND owner=$3`, entityID, provider, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RefreshToken returns current meta untouched when the token is still valid
// (refreshed=false, fn never called); otherwise it exchanges the stored
// refresh token via fn, reseals, and updates with an optimistic-lock check.
// fn failures keep the old row (error, no state change).
func (s *OAuthPGStore) RefreshToken(ctx context.Context, db platform.DBTX, entityID int64, provider, owner string, now time.Time, skew time.Duration, fn OAuthRefreshFunc) (OAuthTokenMeta, bool, error) {
	if fn == nil {
		return OAuthTokenMeta{}, false, fmt.Errorf("identity: oauth refresher required: %w", platform.ErrValidation)
	}
	r, err := s.oauthRowFor(ctx, db, entityID, provider, owner)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	if !r.meta.Expired(now, skew) {
		return r.meta, false, nil
	}
	refresh, err := s.crypter.Open(r.refreshSealed)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	if refresh == "" {
		return OAuthTokenMeta{}, false, fmt.Errorf("identity: oauth no refresh token stored: %w", platform.ErrValidation)
	}
	// fn receives live secrets in memory only; it must not log or persist them either.
	newAccess, newRefresh, newExp, err := fn(ctx, refresh)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	if newAccess == "" || newExp.IsZero() {
		return OAuthTokenMeta{}, false, fmt.Errorf("identity: oauth refresh returned no material: %w", platform.ErrValidation)
	}
	if newRefresh == "" {
		newRefresh = refresh // provider rotates access only; keep the stored refresh token
	}
	return s.rotateRow(ctx, db, r, newAccess, newRefresh, newExp)
}

// rotateRow reseals new material over row r (optimistic lock on row_version).
func (s *OAuthPGStore) rotateRow(ctx context.Context, db platform.DBTX, r oauthRow, access, refresh string, expiresAt time.Time) (OAuthTokenMeta, bool, error) {
	accessSealed, err := s.crypter.Seal(access)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	refreshSealed, err := s.crypter.Seal(refresh)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	upd, err := scanOAuthRow(db.QueryRow(ctx, `UPDATE ferp_oauth_tokens SET
			access_sealed=$1, refresh_sealed=$2, token_ref=$3, expires_at=$4,
			updated_at=now(), row_version=row_version+1
		WHERE id=$5 AND row_version=$6 AND entity_id=$7 RETURNING `+oauthCols,
		accessSealed, refreshSealed, tokenRef(r.meta.Provider, access), expiresAt,
		r.meta.ID, r.meta.RowVersion, r.meta.EntityID))
	if errors.Is(err, ErrNotFound) {
		// The row was proven present (and entity-scoped) moments ago, so a
		// lost update is a version conflict, not a disappearance.
		return OAuthTokenMeta{}, false, ErrVersionConflict
	}
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	return upd.meta, true, nil
}

// OAuthMemoryStore is the in-process fake (same contract, ciphertext at rest
// in the map — never plaintext).
type OAuthMemoryStore struct {
	mu      sync.Mutex
	seq     int64
	crypter *OAuthCrypter
	rows    map[string]oauthRow // key(entity, provider, owner)
}

// NewOAuthMemoryStore builds an empty fake (key must be 32 bytes).
func NewOAuthMemoryStore(key []byte) (*OAuthMemoryStore, error) {
	c, err := newOAuthCrypter(key)
	if err != nil {
		return nil, err
	}
	return &OAuthMemoryStore{crypter: c, rows: map[string]oauthRow{}}, nil
}

func oauthKey(entityID int64, provider, owner string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", entityID, provider, owner)
}

func (m *OAuthMemoryStore) SaveToken(_ context.Context, _ platform.DBTX, entityID int64, provider, owner, access, refresh string, expiresAt time.Time, scopes []string) (OAuthTokenMeta, error) {
	if err := validateOAuthInput(entityID, provider, owner, access, expiresAt); err != nil {
		return OAuthTokenMeta{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	accessSealed, err := m.crypter.Seal(access)
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	refreshSealed, err := m.crypter.Seal(refresh)
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := oauthKey(entityID, provider, owner)
	prev, exists := m.rows[k]
	meta := OAuthTokenMeta{
		EntityID: entityID, Provider: provider, Owner: owner,
		TokenRef: tokenRef(provider, access), ExpiresAt: expiresAt,
		Scopes: append([]string(nil), scopes...),
		UpdatedAt: time.Now().UTC(),
	}
	if exists {
		meta.ID = prev.meta.ID
		meta.CreatedAt = prev.meta.CreatedAt
		meta.RowVersion = prev.meta.RowVersion + 1
	} else {
		m.seq++
		meta.ID = m.seq
		meta.CreatedAt = meta.UpdatedAt
		meta.RowVersion = 1
	}
	m.rows[k] = oauthRow{meta: meta, accessSealed: accessSealed,
		refreshSealed: refreshSealed, scopesJoined: strings.Join(scopes, ",")}
	return meta, nil
}

func (m *OAuthMemoryStore) rowFor(entityID int64, provider, owner string) (oauthRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[oauthKey(entityID, provider, owner)]
	if !ok {
		return oauthRow{}, ErrNotFound
	}
	return r, nil
}

func (m *OAuthMemoryStore) FetchSecrets(_ context.Context, _ platform.DBTX, entityID int64, provider, owner string) (string, string, time.Time, error) {
	r, err := m.rowFor(entityID, provider, owner)
	if err != nil {
		return "", "", time.Time{}, err
	}
	access, err := m.crypter.Open(r.accessSealed)
	if err != nil {
		return "", "", time.Time{}, err
	}
	refresh, err := m.crypter.Open(r.refreshSealed)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return access, refresh, r.meta.ExpiresAt, nil
}

func (m *OAuthMemoryStore) Meta(_ context.Context, _ platform.DBTX, entityID int64, provider, owner string) (OAuthTokenMeta, error) {
	r, err := m.rowFor(entityID, provider, owner)
	if err != nil {
		return OAuthTokenMeta{}, err
	}
	return r.meta, nil
}

func (m *OAuthMemoryStore) ListMeta(_ context.Context, _ platform.DBTX, entityID int64) ([]OAuthTokenMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OAuthTokenMeta
	for _, r := range m.rows {
		if r.meta.EntityID == entityID {
			out = append(out, r.meta)
		}
	}
	return out, nil
}

func (m *OAuthMemoryStore) DeleteToken(_ context.Context, _ platform.DBTX, entityID int64, provider, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[oauthKey(entityID, provider, owner)]; !ok {
		return ErrNotFound
	}
	delete(m.rows, oauthKey(entityID, provider, owner))
	return nil
}

func (m *OAuthMemoryStore) RefreshToken(_ context.Context, _ platform.DBTX, entityID int64, provider, owner string, now time.Time, skew time.Duration, fn OAuthRefreshFunc) (OAuthTokenMeta, bool, error) {
	if fn == nil {
		return OAuthTokenMeta{}, false, fmt.Errorf("identity: oauth refresher required: %w", platform.ErrValidation)
	}
	r, err := m.rowFor(entityID, provider, owner)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	if !r.meta.Expired(now, skew) {
		return r.meta, false, nil
	}
	refresh, err := m.crypter.Open(r.refreshSealed)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	if refresh == "" {
		return OAuthTokenMeta{}, false, fmt.Errorf("identity: oauth no refresh token stored: %w", platform.ErrValidation)
	}
	newAccess, newRefresh, newExp, err := fn(context.Background(), refresh)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	if newAccess == "" || newExp.IsZero() {
		return OAuthTokenMeta{}, false, fmt.Errorf("identity: oauth refresh returned no material: %w", platform.ErrValidation)
	}
	if newRefresh == "" {
		newRefresh = refresh
	}
	accessSealed, err := m.crypter.Seal(newAccess)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	refreshSealed, err := m.crypter.Seal(newRefresh)
	if err != nil {
		return OAuthTokenMeta{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rows[oauthKey(entityID, provider, owner)]
	if !ok || cur.meta.RowVersion != r.meta.RowVersion {
		return OAuthTokenMeta{}, false, ErrVersionConflict
	}
	cur.accessSealed, cur.refreshSealed = accessSealed, refreshSealed
	cur.meta.TokenRef = tokenRef(provider, newAccess)
	cur.meta.ExpiresAt = newExp
	cur.meta.UpdatedAt = time.Now().UTC()
	cur.meta.RowVersion++
	m.rows[oauthKey(entityID, provider, owner)] = cur
	return cur.meta, true, nil
}

// OAuthRefresher is the HTTP implementation of OAuthRefreshFunc: a standard
// OAuth2 refresh_token grant against a configurable token endpoint.
// ClientSecret travels only in the POST body (never in logs/errors); token
// endpoint errors are truncated like provider errors in payments.
type OAuthRefresher struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

// Refresh exchanges refreshToken for fresh material.
func (r *OAuthRefresher) Refresh(ctx context.Context, refreshToken string) (access, refresh string, expiresAt time.Time, err error) {
	if strings.TrimSpace(r.TokenURL) == "" || refreshToken == "" {
		return "", "", time.Time{}, errors.New("identity: oauth refresh not configured")
	}
	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", r.ClientID)
	form.Set("client_secret", r.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("identity: oauth refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("identity: oauth refresh call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("identity: oauth refresh read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snip := strings.TrimSpace(string(raw))
		const maxSnip = 300
		if len(snip) > maxSnip {
			snip = snip[:maxSnip] + "…"
		}
		if snip == "" {
			snip = "empty body"
		}
		return "", "", time.Time{}, fmt.Errorf("identity: oauth refresh failed (status %d): %s", resp.StatusCode, snip)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", time.Time{}, fmt.Errorf("identity: oauth refresh decode: %w", err)
	}
	if out.AccessToken == "" {
		return "", "", time.Time{}, errors.New("identity: oauth refresh missing access_token")
	}
	exp := time.Now().UTC().Add(time.Duration(out.ExpiresIn) * time.Second)
	if out.ExpiresIn <= 0 {
		exp = time.Now().UTC().Add(5 * time.Minute)
	}
	return out.AccessToken, out.RefreshToken, exp, nil
}
