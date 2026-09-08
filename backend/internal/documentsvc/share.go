package documentsvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// ShareToken is a bearer link to one file (portal-lite public sharing).
type ShareToken struct {
	Token     string    `json:"token"`
	EntityID  int64     `json:"entity_id"`
	DocID     int64     `json:"doc_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// MintToken generates a random 256-bit hex token.
func MintToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// ShareStore persists share tokens.
type ShareStore interface {
	CreateShare(ctx context.Context, s *ShareToken) error
	ShareTarget(ctx context.Context, token string) (ShareToken, error)
}

// CreateShare records a token on the PG store.
func (s *PGStore) CreateShare(ctx context.Context, st *ShareToken) error {
	if st.Token == "" || st.DocID <= 0 || st.ExpiresAt.IsZero() {
		return errors.New("documentsvc: bad share token")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO ferp_share_tokens (token, entity_id, doc_id, expires_at)
		VALUES ($1,$2,$3,$4)`, st.Token, st.EntityID, st.DocID, st.ExpiresAt)
	if err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `SELECT created_at FROM ferp_share_tokens WHERE token=$1`,
		st.Token).Scan(&st.CreatedAt)
}

// ShareTarget resolves a live token (expired/missing → not found).
func (s *PGStore) ShareTarget(ctx context.Context, token string) (ShareToken, error) {
	var st ShareToken
	err := s.pool.QueryRow(ctx, `SELECT token, entity_id, doc_id, expires_at, created_at
		FROM ferp_share_tokens WHERE token=$1 AND expires_at > now()`, token).Scan(
		&st.Token, &st.EntityID, &st.DocID, &st.ExpiresAt, &st.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ShareToken{}, identity.ErrNotFound
	}
	return st, err
}

// CreateShare records a token on the memory fake.
func (m *MemoryStore) CreateShare(_ context.Context, st *ShareToken) error {
	if st.Token == "" || st.DocID <= 0 || st.ExpiresAt.IsZero() {
		return errors.New("documentsvc: bad share token")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st.CreatedAt = time.Now().UTC()
	m.shares[st.Token] = *st
	return nil
}

// ShareTarget resolves a live token on the memory fake.
func (m *MemoryStore) ShareTarget(_ context.Context, token string) (ShareToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.shares[token]
	if !ok || time.Now().UTC().After(st.ExpiresAt) {
		return ShareToken{}, identity.ErrNotFound
	}
	return st, nil
}
