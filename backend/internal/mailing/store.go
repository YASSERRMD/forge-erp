package mailing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for the mailing context.
type Store interface {
	CreateCampaign(ctx context.Context, db platform.DBTX, c *Campaign) error
	CampaignByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Campaign, error)
	ListCampaigns(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Campaign, error)
	SetCampaignStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to int16, rowVersion int64) (Campaign, error)
	AddRecipient(ctx context.Context, db platform.DBTX, r *Recipient) error
	RecipientsOf(ctx context.Context, db platform.DBTX, entityID, campaignID int64) ([]Recipient, error)
	QueuedOf(ctx context.Context, db platform.DBTX, entityID, campaignID int64) ([]Recipient, error)
	SetRecipientStatus(ctx context.Context, db platform.DBTX, entityID, id int64, status, errMsg string) error
	RecipientByToken(ctx context.Context, db platform.DBTX, token string) (Recipient, error)
	SuppressEmail(ctx context.Context, db platform.DBTX, entityID int64, email string) error
	SuppressedMap(ctx context.Context, db platform.DBTX, entityID int64) (map[string]bool, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const campaignCols = `id, entity_id, subject, body, status, row_version`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.EntityID, &c.Subject, &c.Body, &c.Status, &c.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, platform.ErrNotFound
	}
	return c, err
}

func (s *PGStore) CreateCampaign(ctx context.Context, db platform.DBTX, c *Campaign) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_mailing_campaigns (entity_id, subject, body, status)
		VALUES ($1,$2,$3,$4) RETURNING id, row_version`,
		c.EntityID, c.Subject, c.Body, c.Status).Scan(&c.ID, &c.RowVersion)
}

func (s *PGStore) CampaignByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Campaign, error) {
	return scanCampaign(db.QueryRow(ctx, `SELECT `+campaignCols+` FROM ferp_mailing_campaigns WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListCampaigns(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Campaign, error) {
	rows, err := db.Query(ctx, `SELECT `+campaignCols+` FROM ferp_mailing_campaigns
		WHERE entity_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) SetCampaignStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to int16, rowVersion int64) (Campaign, error) {
	c, err := s.CampaignByID(ctx, db, entityID, id)
	if err != nil {
		return Campaign{}, err
	}
	if c.RowVersion != rowVersion {
		return Campaign{}, platform.ErrVersionConflict
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_mailing_campaigns SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$3 AND row_version=$4`, to, id, entityID, rowVersion)
	if err != nil {
		return Campaign{}, err
	}
	if tag.RowsAffected() == 0 {
		return Campaign{}, platform.ErrVersionConflict
	}
	c.Status = to
	c.RowVersion++
	return c, nil
}

const recipientCols = `id, entity_id, campaign_id, email, token, status, error`

func scanRecipient(row pgx.Row) (Recipient, error) {
	var r Recipient
	err := row.Scan(&r.ID, &r.EntityID, &r.CampaignID, &r.Email, &r.Token, &r.Status, &r.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recipient{}, platform.ErrNotFound
	}
	return r, err
}

func (s *PGStore) AddRecipient(ctx context.Context, db platform.DBTX, r *Recipient) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if r.Status == "" {
		r.Status = RecipientQueued
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_mailing_recipients (entity_id, campaign_id, email, token, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		r.EntityID, r.CampaignID, strings.ToLower(strings.TrimSpace(r.Email)), r.Token, r.Status).Scan(&r.ID)
}

func (s *PGStore) RecipientsOf(ctx context.Context, db platform.DBTX, entityID, campaignID int64) ([]Recipient, error) {
	rows, err := db.Query(ctx, `SELECT `+recipientCols+` FROM ferp_mailing_recipients
		WHERE entity_id=$1 AND campaign_id=$2 ORDER BY id`, entityID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) QueuedOf(ctx context.Context, db platform.DBTX, entityID, campaignID int64) ([]Recipient, error) {
	rows, err := db.Query(ctx, `SELECT `+recipientCols+` FROM ferp_mailing_recipients
		WHERE entity_id=$1 AND campaign_id=$2 AND status='queued' ORDER BY id`, entityID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) SetRecipientStatus(ctx context.Context, db platform.DBTX, entityID, id int64, status, errMsg string) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_mailing_recipients SET status=$1, error=$2, updated_at=now() WHERE id=$3 AND campaign_id IN (SELECT id FROM ferp_mailing_campaigns WHERE entity_id=$4)`,
		status, errMsg, id, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

func (s *PGStore) RecipientByToken(ctx context.Context, db platform.DBTX, token string) (Recipient, error) {
	return scanRecipient(db.QueryRow(ctx, `SELECT `+recipientCols+` FROM ferp_mailing_recipients WHERE token=$1`, token))
}

func (s *PGStore) SuppressEmail(ctx context.Context, db platform.DBTX, entityID int64, email string) error {
	e := strings.ToLower(strings.TrimSpace(email))
	if !validEmail(e) {
		return fmt.Errorf("mailing: bad email %q: %w", email, platform.ErrValidation)
	}
	_, err := db.Exec(ctx, `INSERT INTO ferp_mailing_suppressions (entity_id, email) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		entityID, e)
	return err
}

func (s *PGStore) SuppressedMap(ctx context.Context, db platform.DBTX, entityID int64) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT email FROM ferp_mailing_suppressions WHERE entity_id=$1`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out[e] = true
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu         sync.Mutex
	seq        int64
	campaigns  map[int64]Campaign
	recipients map[int64]Recipient
	suppressed map[int64]map[string]bool // entity -> email set
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{campaigns: map[int64]Campaign{}, recipients: map[int64]Recipient{}, suppressed: map[int64]map[string]bool{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateCampaign(_ context.Context, _ platform.DBTX, c *Campaign) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID = m.next()
	c.RowVersion = 1
	m.campaigns[c.ID] = *c
	return nil
}

func (m *MemoryStore) CampaignByID(_ context.Context, _ platform.DBTX, entityID, id int64) (Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.campaigns[id]
	if !ok || c.EntityID != entityID {
		return Campaign{}, platform.ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) ListCampaigns(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Campaign
	for _, c := range m.campaigns {
		if c.EntityID == entityID {
			out = append(out, c)
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

func (m *MemoryStore) SetCampaignStatus(_ context.Context, _ platform.DBTX, entityID, id int64, to int16, rowVersion int64) (Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.campaigns[id]
	if !ok || c.EntityID != entityID {
		return Campaign{}, platform.ErrNotFound
	}
	if c.RowVersion != rowVersion {
		return Campaign{}, platform.ErrVersionConflict
	}
	c.Status = to
	c.RowVersion++
	m.campaigns[id] = c
	return c, nil
}

func (m *MemoryStore) AddRecipient(_ context.Context, _ platform.DBTX, r *Recipient) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.campaigns[r.CampaignID]; !ok {
		return fmt.Errorf("mailing: campaign not found: %w", platform.ErrNotFound)
	}
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
	for _, e := range m.recipients {
		if e.CampaignID == r.CampaignID && e.Email == r.Email {
			return fmt.Errorf("mailing: duplicate recipient: %w", platform.ErrConflict)
		}
		if e.Token == r.Token {
			return fmt.Errorf("mailing: duplicate token: %w", platform.ErrConflict)
		}
	}
	if r.Status == "" {
		r.Status = RecipientQueued
	}
	r.ID = m.next()
	m.recipients[r.ID] = *r
	return nil
}

func (m *MemoryStore) RecipientsOf(_ context.Context, _ platform.DBTX, entityID, campaignID int64) ([]Recipient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Recipient
	for _, r := range m.recipients {
		if r.EntityID == entityID && r.CampaignID == campaignID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) QueuedOf(_ context.Context, _ platform.DBTX, entityID, campaignID int64) ([]Recipient, error) {
	all, _ := m.RecipientsOf(context.Background(), nil, entityID, campaignID)
	var out []Recipient
	for _, r := range all {
		if r.Status == RecipientQueued {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetRecipientStatus(_ context.Context, _ platform.DBTX, entityID, id int64, status, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recipients[id]
	if !ok || r.EntityID != entityID {
		return platform.ErrNotFound
	}
	r.Status = status
	r.Error = errMsg
	m.recipients[id] = r
	return nil
}

func (m *MemoryStore) RecipientByToken(_ context.Context, _ platform.DBTX, token string) (Recipient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.recipients {
		if r.Token == token {
			return r, nil
		}
	}
	return Recipient{}, platform.ErrNotFound
}

func (m *MemoryStore) SuppressEmail(_ context.Context, _ platform.DBTX, entityID int64, email string) error {
	e := strings.ToLower(strings.TrimSpace(email))
	if !validEmail(e) {
		return fmt.Errorf("mailing: bad email %q: %w", email, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.suppressed[entityID] == nil {
		m.suppressed[entityID] = map[string]bool{}
	}
	m.suppressed[entityID][e] = true
	return nil
}

func (m *MemoryStore) SuppressedMap(_ context.Context, _ platform.DBTX, entityID int64) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for e := range m.suppressed[entityID] {
		out[e] = true
	}
	return out, nil
}
