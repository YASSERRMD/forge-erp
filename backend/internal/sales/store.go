package sales

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Payment records money against one or more invoices.
type Payment struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	Ref       string    `json:"ref"`
	OrgID     int64     `json:"org_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Method    string    `json:"method"`
	PaidAt    time.Time `json:"paid_at"`
	CreatedBy *int64    `json:"created_by"`
}

// Store is the persistence contract for the sales context.
type Store interface {
	CreateDoc(ctx context.Context, d *Document, yearMonth string) error
	DocByID(ctx context.Context, id int64) (Document, error)
	ListDocs(ctx context.Context, entityID int64, t documents.DocType, limit, offset int) ([]Document, error)
	// SetStatus enforces the kernel transition table before persisting.
	SetStatus(ctx context.Context, id int64, to int16) (Document, error)
	// UpdateDocLines replaces a DRAFT document's lines and recomputed totals.
	UpdateDocLines(ctx context.Context, id int64, lines []documents.Line) (Document, error)
	RecordPayment(ctx context.Context, p *Payment, invoiceIDs []int64, yearMonth string) ([]int64, error)
	InvoiceBalance(ctx context.Context, invoiceID int64) (int64, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const docCols = `id, entity_id, type, ref, status, org_id, currency, rate_to_base,
	source_type, source_id, total_net, total_vat, total_gross,
	created_at, updated_at, created_by, updated_by, row_version`

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.EntityID, &d.Type, &d.Ref, &d.Status, &d.OrgID, &d.Currency,
		&d.RateToBase, &d.SourceType, &d.SourceID, &d.Totals.Net, &d.Totals.VAT, &d.Totals.Gross,
		&d.CreatedAt, &d.UpdatedAt, &d.CreatedBy, &d.UpdatedBy, &d.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, identity.ErrNotFound
	}
	return d, err
}

// queryFunc abstracts row queries (pool or transaction).
type queryFunc func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)

func loadLines(ctx context.Context, q queryFunc, docID int64) ([]documents.Line, error) {
	rows, err := q(ctx, `SELECT product_id, label, qty, unit_net, vat_rate_bps, discount_pc
		FROM ferp_doc_lines WHERE doc_id=$1 ORDER BY pos, id`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []documents.Line
	for rows.Next() {
		var l documents.Line
		if err := rows.Scan(&l.ProductID, &l.Label, &l.Qty, &l.UnitNet, &l.VATRateBps, &l.DiscountPc); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// nextRefLocked bumps the monthly counter inside the caller's transaction.
func nextRefLocked(ctx context.Context, tx pgx.Tx, entityID int64, t documents.DocType, ym string) (string, error) {
	var seq int64
	err := tx.QueryRow(ctx, `INSERT INTO ferp_doc_counters (entity_id, type, year_month, next_seq)
		VALUES ($1,$2,$3,2) ON CONFLICT (entity_id, type, year_month)
		DO UPDATE SET next_seq=ferp_doc_counters.next_seq+1
		RETURNING next_seq-1`, entityID, string(t), ym).Scan(&seq)
	if err != nil {
		return "", err
	}
	return documents.NextRef(t, ym, seq), nil
}

func (s *PGStore) CreateDoc(ctx context.Context, d *Document, yearMonth string) error {
	if err := d.Validate(); err != nil {
		return err
	}
	tot, err := documents.Sum(d.Lines)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ref, err := nextRefLocked(ctx, tx, d.EntityID, d.Type, yearMonth)
	if err != nil {
		return err
	}
	d.Ref = ref
	d.Totals = tot
	err = tx.QueryRow(ctx, `INSERT INTO ferp_documents
		(entity_id, type, ref, status, org_id, currency, rate_to_base, source_type, source_id,
		 total_net, total_vat, total_gross, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, created_at, updated_at, row_version`,
		d.EntityID, string(d.Type), d.Ref, d.Status, d.OrgID, d.Currency, d.RateToBase,
		string(d.SourceType), d.SourceID, tot.Net, tot.VAT, tot.Gross, d.CreatedBy, d.UpdatedBy,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt, &d.RowVersion)
	if err != nil {
		return err
	}
	for i, l := range d.Lines {
		if _, err := tx.Exec(ctx, `INSERT INTO ferp_doc_lines
			(doc_id, pos, product_id, label, qty, unit_net, vat_rate_bps, discount_pc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			d.ID, i, nullProduct(l.ProductID), l.Label, l.Qty, l.UnitNet, l.VATRateBps, l.DiscountPc); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func nullProduct(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *PGStore) DocByID(ctx context.Context, id int64) (Document, error) {
	d, err := scanDoc(s.pool.QueryRow(ctx, `SELECT `+docCols+` FROM ferp_documents WHERE id=$1`, id))
	if err != nil {
		return Document{}, err
	}
	lines, err := loadLines(ctx, s.pool.Query, id)
	if err != nil {
		return Document{}, err
	}
	d.Lines = lines
	return d, nil
}

func (s *PGStore) ListDocs(ctx context.Context, entityID int64, t documents.DocType, limit, offset int) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+docCols+` FROM ferp_documents
		WHERE entity_id=$1 AND type=$2 ORDER BY id DESC LIMIT $3 OFFSET $4`,
		entityID, string(t), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *PGStore) SetStatus(ctx context.Context, id int64, to int16) (Document, error) {
	d, err := s.DocByID(ctx, id)
	if err != nil {
		return Document{}, err
	}
	if err := d.MoveTo(to); err != nil {
		return Document{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_documents SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, d.RowVersion)
	if err != nil {
		return Document{}, err
	}
	if tag.RowsAffected() == 0 {
		return Document{}, identity.ErrVersionConflict
	}
	d.Status = to
	d.RowVersion++
	return d, nil
}

// UpdateDocLines replaces a DRAFT document's lines with recomputed totals.
// Non-draft documents are rejected (validated docs are API-immutable).
func (s *PGStore) UpdateDocLines(ctx context.Context, id int64, lines []documents.Line) (Document, error) {
	if len(lines) == 0 {
		return Document{}, errors.New("sales: document requires at least one line")
	}
	tot, err := documents.Sum(lines)
	if err != nil {
		return Document{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status int16
	if err := tx.QueryRow(ctx, `SELECT status FROM ferp_documents WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, identity.ErrNotFound
		}
		return Document{}, err
	}
	if status != 0 {
		return Document{}, errors.New("sales: only draft documents can be edited")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ferp_doc_lines WHERE doc_id=$1`, id); err != nil {
		return Document{}, err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO ferp_doc_lines
			(doc_id, pos, product_id, label, qty, unit_net, vat_rate_bps, discount_pc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, i, nullProduct(l.ProductID), l.Label, l.Qty, l.UnitNet, l.VATRateBps, l.DiscountPc); err != nil {
			return Document{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE ferp_documents SET total_net=$1, total_vat=$2, total_gross=$3,
		updated_at=now(), row_version=row_version+1 WHERE id=$4`,
		tot.Net, tot.VAT, tot.Gross, id); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.DocByID(ctx, id)
}

func (s *PGStore) RecordPayment(ctx context.Context, p *Payment, invoiceIDs []int64, yearMonth string) ([]int64, error) {
	if p.Amount <= 0 {
		return nil, errors.New("sales: payment amount must be positive")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	balances := make([]int64, len(invoiceIDs))
	for i, invID := range invoiceIDs {
		var gross, paid int64
		err := tx.QueryRow(ctx, `SELECT total_gross FROM ferp_documents WHERE id=$1 AND type='invoice'`, invID).Scan(&gross)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("sales: invoice %d not found", invID)
		}
		if err != nil {
			return nil, err
		}
		_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM ferp_payment_allocations WHERE invoice_id=$1`, invID).Scan(&paid)
		balances[i] = gross - paid
		if balances[i] <= 0 {
			return nil, fmt.Errorf("sales: invoice %d already settled", invID)
		}
	}
	applied, rest, err := AllocateAcross(balances, p.Amount)
	if err != nil {
		return nil, err
	}
	if rest != 0 {
		return nil, fmt.Errorf("sales: overpayment refused (unapplied %d)", rest)
	}
	var seq int64
	err = tx.QueryRow(ctx, `INSERT INTO ferp_doc_counters (entity_id, type, year_month, next_seq)
		VALUES ($1,'payment',$2,2) ON CONFLICT (entity_id, type, year_month)
		DO UPDATE SET next_seq=ferp_doc_counters.next_seq+1 RETURNING next_seq-1`,
		p.EntityID, yearMonth).Scan(&seq)
	if err != nil {
		return nil, err
	}
	p.Ref = fmt.Sprintf("PAY-%s-%04d", yearMonth, seq)
	var pid int64
	err = tx.QueryRow(ctx, `INSERT INTO ferp_payments
		(entity_id, ref, org_id, amount, currency, method, paid_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		p.EntityID, p.Ref, p.OrgID, p.Amount, p.Currency, p.Method, p.PaidAt, p.CreatedBy).Scan(&pid)
	if err != nil {
		return nil, err
	}
	p.ID = pid
	for i, invID := range invoiceIDs {
		if applied[i] == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ferp_payment_allocations (payment_id, invoice_id, amount)
			VALUES ($1,$2,$3)`, pid, invID, applied[i]); err != nil {
			return nil, err
		}
		var gross, paid int64
		_ = tx.QueryRow(ctx, `SELECT total_gross FROM ferp_documents WHERE id=$1`, invID).Scan(&gross)
		_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM ferp_payment_allocations WHERE invoice_id=$1`, invID).Scan(&paid)
		st := int16(InvoicePartPaid)
		if paid >= gross {
			st = InvoicePaid
		}
		if _, err := tx.Exec(ctx, `UPDATE ferp_documents SET status=$1, updated_at=now() WHERE id=$2`, st, invID); err != nil {
			return nil, err
		}
	}
	return applied, tx.Commit(ctx)
}

func (s *PGStore) InvoiceBalance(ctx context.Context, invoiceID int64) (int64, error) {
	var gross, paid int64
	if err := s.pool.QueryRow(ctx, `SELECT total_gross FROM ferp_documents WHERE id=$1 AND type='invoice'`, invoiceID).Scan(&gross); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, identity.ErrNotFound
		}
		return 0, err
	}
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM ferp_payment_allocations WHERE invoice_id=$1`, invoiceID).Scan(&paid)
	return gross - paid, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	docs     map[int64]Document
	counters map[string]int64
	pays     map[int64]Payment
	alloc    map[int64]int64 // invoiceID -> paid total
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{docs: map[int64]Document{}, counters: map[string]int64{},
		pays: map[int64]Payment{}, alloc: map[int64]int64{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) ref(entityID int64, kind, ym string) string {
	k := fmt.Sprintf("%d/%s/%s", entityID, kind, ym)
	m.counters[k]++
	prefix := map[string]string{"proposal": "PROP", "order": "ORD", "shipment": "SHIP", "invoice": "INV", "payment": "PAY"}[kind]
	return fmt.Sprintf("%s-%s-%04d", prefix, ym, m.counters[k])
}

func (m *MemoryStore) CreateDoc(_ context.Context, d *Document, yearMonth string) error {
	if err := d.Validate(); err != nil {
		return err
	}
	tot, err := documents.Sum(d.Lines)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d.ID = m.next()
	d.Ref = m.ref(d.EntityID, string(d.Type), yearMonth)
	d.Totals = tot
	d.RowVersion = 1
	m.docs[d.ID] = *d
	return nil
}

func (m *MemoryStore) DocByID(_ context.Context, id int64) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok {
		return Document{}, identity.ErrNotFound
	}
	return d, nil
}

func (m *MemoryStore) ListDocs(_ context.Context, entityID int64, t documents.DocType, limit, offset int) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Document
	for _, d := range m.docs {
		if d.EntityID == entityID && d.Type == t {
			out = append(out, d)
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

func (m *MemoryStore) SetStatus(_ context.Context, id int64, to int16) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok {
		return Document{}, identity.ErrNotFound
	}
	if err := d.MoveTo(to); err != nil {
		return Document{}, err
	}
	d.Status = to
	d.RowVersion++
	m.docs[id] = d
	return d, nil
}

// UpdateDocLines replaces a DRAFT document's lines with recomputed totals.
func (m *MemoryStore) UpdateDocLines(_ context.Context, id int64, lines []documents.Line) (Document, error) {
	if len(lines) == 0 {
		return Document{}, errors.New("sales: document requires at least one line")
	}
	tot, err := documents.Sum(lines)
	if err != nil {
		return Document{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok {
		return Document{}, identity.ErrNotFound
	}
	if d.Status != 0 {
		return Document{}, errors.New("sales: only draft documents can be edited")
	}
	d.Lines = lines
	d.Totals = tot
	d.RowVersion++
	m.docs[id] = d
	return d, nil
}

func (m *MemoryStore) RecordPayment(_ context.Context, p *Payment, invoiceIDs []int64, yearMonth string) ([]int64, error) {
	if p.Amount <= 0 {
		return nil, errors.New("sales: payment amount must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	balances := make([]int64, len(invoiceIDs))
	for i, invID := range invoiceIDs {
		inv, ok := m.docs[invID]
		if !ok || inv.Type != documents.TypeInvoice {
			return nil, fmt.Errorf("sales: invoice %d not found", invID)
		}
		balances[i] = inv.Totals.Gross - m.alloc[invID]
		if balances[i] <= 0 {
			return nil, fmt.Errorf("sales: invoice %d already settled", invID)
		}
	}
	applied, rest, err := AllocateAcross(balances, p.Amount)
	if err != nil {
		return nil, err
	}
	if rest != 0 {
		return nil, fmt.Errorf("sales: overpayment refused (unapplied %d)", rest)
	}
	p.ID = m.next()
	p.Ref = m.ref(p.EntityID, "payment", yearMonth)
	m.pays[p.ID] = *p
	for i, invID := range invoiceIDs {
		if applied[i] == 0 {
			continue
		}
		m.alloc[invID] += applied[i]
		inv := m.docs[invID]
		if m.alloc[invID] >= inv.Totals.Gross {
			inv.Status = InvoicePaid
		} else {
			inv.Status = InvoicePartPaid
		}
		m.docs[invID] = inv
	}
	return applied, nil
}

func (m *MemoryStore) InvoiceBalance(_ context.Context, invoiceID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.docs[invoiceID]
	if !ok || inv.Type != documents.TypeInvoice {
		return 0, identity.ErrNotFound
	}
	return inv.Totals.Gross - m.alloc[invoiceID], nil
}
