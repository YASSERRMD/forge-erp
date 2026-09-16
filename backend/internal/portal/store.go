package portal

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
)

// Sales is the LOCAL mirror of the sales seam the portal needs (pos pattern:
// define the narrow interface here, never modify the sales package). The real
// sales.PGStore / sales.MemoryStore both satisfy it by method set.
type Sales interface {
	DocByID(ctx context.Context, db platform.DBTX, entityID, id int64) (sales.Document, error)
	ListDocs(ctx context.Context, db platform.DBTX, entityID int64, t documents.DocType, limit, offset int) ([]sales.Document, error)
	SetStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to int16) (sales.Document, error)
}

// Services is the LOCAL mirror of the services seam the portal needs.
type Services interface {
	CreateTicket(ctx context.Context, db platform.DBTX, t *services.Ticket) error
	TicketByID(ctx context.Context, db platform.DBTX, entityID, id int64) (services.Ticket, error)
	ListTickets(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]services.Ticket, error)
}

// Store persists portal tokens (customer credentials). Tokens are looked up
// by hash alone: the entity is resolved FROM the token row (customers carry
// no JWT), and hashes are 256-bit random so cross-entity collision is
// impossible; entity scoping is then enforced by every service call.
type Store interface {
	CreateToken(ctx context.Context, db platform.DBTX, t *PortalToken) error
	TokenByHash(ctx context.Context, db platform.DBTX, hash string) (PortalToken, error)
	RevokeToken(ctx context.Context, db platform.DBTX, entityID, id int64) error
}

// PGStore implements Store against PostgreSQL (ferp_portal_tokens).
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const tokenCols = `id, entity_id, org_id, contact_id, salt, token_hash, expires_at, revoked_at, created_at`

func scanToken(row pgx.Row) (PortalToken, error) {
	var t PortalToken
	err := row.Scan(&t.ID, &t.EntityID, &t.OrgID, &t.ContactID, &t.Salt,
		&t.TokenHash, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PortalToken{}, identity.ErrNotFound
	}
	return t, err
}

func (s *PGStore) CreateToken(ctx context.Context, db platform.DBTX, t *PortalToken) error {
	if err := t.Validate(); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_portal_tokens
		(entity_id, org_id, contact_id, salt, token_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`,
		t.EntityID, t.OrgID, t.ContactID, t.Salt, t.TokenHash, t.ExpiresAt,
	).Scan(&t.ID, &t.CreatedAt)
}

// TokenByHash resolves a live token by its hash (revoked/expired → not found,
// so stolen-hash replays of dead tokens and timing oracles both fail closed).
func (s *PGStore) TokenByHash(ctx context.Context, db platform.DBTX, hash string) (PortalToken, error) {
	t, err := scanToken(db.QueryRow(ctx, `SELECT `+tokenCols+` FROM ferp_portal_tokens
		WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at > now()`, hash))
	if err != nil {
		return PortalToken{}, err
	}
	return t, nil
}

func (s *PGStore) RevokeToken(ctx context.Context, db platform.DBTX, entityID, id int64) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_portal_tokens SET revoked_at=now()
		WHERE id=$1 AND entity_id=$2 AND revoked_at IS NULL`, id, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	tokens map[int64]PortalToken
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{tokens: map[int64]PortalToken{}} }

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateToken(_ context.Context, _ platform.DBTX, t *PortalToken) error {
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = m.next()
	t.CreatedAt = time.Now().UTC()
	m.tokens[t.ID] = *t
	return nil
}

func (m *MemoryStore) TokenByHash(_ context.Context, _ platform.DBTX, hash string) (PortalToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, t := range m.tokens {
		if t.TokenHash == hash && t.Live(now) {
			return t, nil
		}
	}
	return PortalToken{}, identity.ErrNotFound
}

func (m *MemoryStore) RevokeToken(_ context.Context, _ platform.DBTX, entityID, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.EntityID != entityID || t.RevokedAt != nil {
		return identity.ErrNotFound
	}
	now := time.Now().UTC()
	t.RevokedAt = &now
	m.tokens[id] = t
	return nil
}
