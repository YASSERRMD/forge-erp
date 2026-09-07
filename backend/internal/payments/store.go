package payments

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for payment attempts.
type Store interface {
	CreateAttempt(ctx context.Context, a *PaymentAttempt) error
	AttemptByID(ctx context.Context, id int64) (PaymentAttempt, error)
	AttemptByWebhook(ctx context.Context, entityID int64, key string) (PaymentAttempt, bool)
	ListAttempts(ctx context.Context, entityID int64, limit, offset int) ([]PaymentAttempt, error)
	SetAttemptStatus(ctx context.Context, id int64, to AttemptStatus, rowVersion int64) (PaymentAttempt, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const attemptCols = `id, entity_id, ref, org_id, invoice_id, amount, currency, provider, status, webhook_key, created_at, updated_at, row_version`

func scanAttempt(row pgx.Row) (PaymentAttempt, error) {
	var a PaymentAttempt
	err := row.Scan(&a.ID, &a.EntityID, &a.Ref, &a.OrgID, &a.InvoiceID, &a.Amount,
		&a.Currency, &a.Provider, &a.Status, &a.WebhookKey,
		&a.CreatedAt, &a.UpdatedAt, &a.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentAttempt{}, identity.ErrNotFound
	}
	return a, err
}

func (s *PGStore) CreateAttempt(ctx context.Context, a *PaymentAttempt) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_payment_attempts
		(entity_id, ref, org_id, invoice_id, amount, currency, provider, status, webhook_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')) RETURNING id, row_version`,
		a.EntityID, a.Ref, a.OrgID, a.InvoiceID, a.Amount, a.Currency, a.Provider, a.Status, a.WebhookKey,
	).Scan(&a.ID, &a.RowVersion)
}

func (s *PGStore) AttemptByID(ctx context.Context, id int64) (PaymentAttempt, error) {
	return scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM ferp_payment_attempts WHERE id=$1`, id))
}

func (s *PGStore) AttemptByWebhook(ctx context.Context, entityID int64, key string) (PaymentAttempt, bool) {
	a, err := scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM ferp_payment_attempts
		WHERE entity_id=$1 AND webhook_key=$2`, entityID, key))
	if err != nil {
		return PaymentAttempt{}, false
	}
	return a, true
}

func (s *PGStore) ListAttempts(ctx context.Context, entityID int64, limit, offset int) ([]PaymentAttempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+attemptCols+` FROM ferp_payment_attempts
		WHERE entity_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PaymentAttempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) SetAttemptStatus(ctx context.Context, id int64, to AttemptStatus, rowVersion int64) (PaymentAttempt, error) {
	a, err := s.AttemptByID(ctx, id)
	if err != nil {
		return PaymentAttempt{}, err
	}
	if a.RowVersion != rowVersion {
		return PaymentAttempt{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return PaymentAttempt{}, errors.New("payments: illegal attempt transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_payment_attempts SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return PaymentAttempt{}, err
	}
	if tag.RowsAffected() == 0 {
		return PaymentAttempt{}, identity.ErrVersionConflict
	}
	a.Status = to
	a.RowVersion++
	return a, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	attempts map[int64]PaymentAttempt
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{attempts: map[int64]PaymentAttempt{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateAttempt(_ context.Context, a *PaymentAttempt) error {
	if err := a.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.attempts {
		if e.EntityID == a.EntityID && e.Ref == a.Ref {
			return errors.New("payments: duplicate attempt ref")
		}
		if a.WebhookKey != "" && e.EntityID == a.EntityID && e.WebhookKey == a.WebhookKey {
			return errors.New("payments: duplicate webhook key")
		}
	}
	a.ID = m.next()
	a.RowVersion = 1
	m.attempts[a.ID] = *a
	return nil
}

func (m *MemoryStore) AttemptByID(_ context.Context, id int64) (PaymentAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attempts[id]
	if !ok {
		return PaymentAttempt{}, identity.ErrNotFound
	}
	return a, nil
}

func (m *MemoryStore) AttemptByWebhook(_ context.Context, entityID int64, key string) (PaymentAttempt, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.attempts {
		if a.EntityID == entityID && a.WebhookKey == key && key != "" {
			return a, true
		}
	}
	return PaymentAttempt{}, false
}

func (m *MemoryStore) ListAttempts(_ context.Context, entityID int64, limit, offset int) ([]PaymentAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PaymentAttempt
	for _, a := range m.attempts {
		if a.EntityID == entityID {
			out = append(out, a)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) SetAttemptStatus(_ context.Context, id int64, to AttemptStatus, rowVersion int64) (PaymentAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attempts[id]
	if !ok {
		return PaymentAttempt{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return PaymentAttempt{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return PaymentAttempt{}, errors.New("payments: illegal attempt transition")
	}
	a.Status = to
	a.RowVersion++
	m.attempts[id] = a
	return a, nil
}
