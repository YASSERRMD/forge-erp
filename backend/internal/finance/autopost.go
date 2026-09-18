// Auto-posting (Phase 3): validated commercial invoices post to the ledger
// through bindings, idempotently. The poster subscribes to document-status
// events; only validated sales/supplier invoices act, everything else is a
// no-op. The (entity, doc_type, doc_id) row in ferp_account_postings is the
// idempotency key: replays and concurrent racers return the existing entry
// instead of double-posting. Missing bindings fail closed (422 naming the
// key) — the operator maps them via the bindings endpoints first.
package finance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Posted document families (ferp_account_postings.doc_type values).
const (
	DocSalesInvoice    = "sales_invoice"
	DocSupplierInvoice = "supplier_invoice"
)

// ErrSkipPosting signals "not a posting document" (wrong family or not
// validated): event subscribers swallow it silently, operator triggers
// surface it as a 422 via PostValidated's wrap below.
var ErrSkipPosting = errors.New("finance: not a posting document")

// VATSlice is one rate's VAT total on an invoice.
type VATSlice struct {
	RateBps int   `json:"rate_bps"`
	VAT     int64 `json:"vat"`
}

// InvoiceDoc is the minimal validated-invoice projection auto-posting needs
// (built by adapters over sales/procurement stores, so finance never imports
// commercial packages).
type InvoiceDoc struct {
	ID        int64
	Ref       string
	Date      time.Time
	OrgRef    string // customer/supplier code for partner binding
	Net       int64
	VAT       int64
	VATSlices []VATSlice
}

// InvoiceLoader resolves one validated invoice (ErrValidation unless the
// document exists, is an invoice of the expected family, and is validated).
type InvoiceLoader interface {
	LoadValidatedInvoice(ctx context.Context, db platform.DBTX, entityID, docID int64) (InvoiceDoc, error)
}

// Posting is one idempotency row: which entry a document posted.
type Posting struct {
	EntityID  int64     `json:"entity_id"`
	DocType   string    `json:"doc_type"`
	DocID     int64     `json:"doc_id"`
	EntryID   int64     `json:"entry_id"`
	CreatedAt time.Time `json:"created_at"`
}

// PostingStore persists idempotency rows. *PGStore implements it against
// ferp_account_postings; MemoryPostingStore is the fake.
type PostingStore interface {
	FindPosting(ctx context.Context, db platform.DBTX, entityID int64, docType string, docID int64) (Posting, error)
	RecordPosting(ctx context.Context, db platform.DBTX, p *Posting) error
	ListPostings(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Posting, error)
}

func scanPosting(row pgx.Row) (Posting, error) {
	var p Posting
	err := row.Scan(&p.EntityID, &p.DocType, &p.DocID, &p.EntryID, &p.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Posting{}, platform.ErrNotFound
		}
		return Posting{}, err
	}
	return p, nil
}

// FindPosting returns the existing posting (ErrNotFound = never posted).
func (s *PGStore) FindPosting(ctx context.Context, db platform.DBTX, entityID int64, docType string, docID int64) (Posting, error) {
	return scanPosting(db.QueryRow(ctx, `SELECT entity_id, doc_type, doc_id, entry_id, created_at
		FROM ferp_account_postings WHERE entity_id=$1 AND doc_type=$2 AND doc_id=$3`,
		entityID, docType, docID))
}

// RecordPosting inserts the idempotency row (unique violation = a concurrent
// poster won; callers re-read via FindPosting).
func (s *PGStore) RecordPosting(ctx context.Context, db platform.DBTX, p *Posting) error {
	return db.QueryRow(ctx, `INSERT INTO ferp_account_postings (entity_id, doc_type, doc_id, entry_id)
		VALUES ($1,$2,$3,$4) RETURNING created_at`,
		p.EntityID, p.DocType, p.DocID, p.EntryID).Scan(&p.CreatedAt)
}

// ListPostings pages postings newest first.
func (s *PGStore) ListPostings(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Posting, error) {
	rows, err := db.Query(ctx, `SELECT entity_id, doc_type, doc_id, entry_id, created_at
		FROM ferp_account_postings WHERE entity_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`,
		entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Posting
	for rows.Next() {
		p, err := scanPosting(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MemoryPostingStore is the in-process fake for poster tests.
type MemoryPostingStore struct {
	mu   sync.Mutex
	rows map[string]Posting
}

// NewMemoryPostingStore builds an empty fake.
func NewMemoryPostingStore() *MemoryPostingStore {
	return &MemoryPostingStore{rows: map[string]Posting{}}
}

func postingKey(entityID int64, docType string, docID int64) string {
	return fmt.Sprintf("%d\x00%s\x00%d", entityID, docType, docID)
}

// FindPosting returns the existing posting (ErrNotFound = never posted).
func (m *MemoryPostingStore) FindPosting(_ context.Context, _ platform.DBTX, entityID int64, docType string, docID int64) (Posting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[postingKey(entityID, docType, docID)]
	if !ok {
		return Posting{}, fmt.Errorf("finance: no posting for %s %d: %w", docType, docID, platform.ErrNotFound)
	}
	return p, nil
}

// RecordPosting inserts the idempotency row (duplicate = ErrConflict, so a
// concurrent poster re-reads instead of double-posting).
func (m *MemoryPostingStore) RecordPosting(_ context.Context, _ platform.DBTX, p *Posting) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := postingKey(p.EntityID, p.DocType, p.DocID)
	if _, dup := m.rows[k]; dup {
		return fmt.Errorf("finance: posting for %s %d exists: %w", p.DocType, p.DocID, platform.ErrConflict)
	}
	m.rows[k] = *p
	return nil
}

// ListPostings pages postings newest first.
func (m *MemoryPostingStore) ListPostings(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Posting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Posting
	for _, p := range m.rows {
		if p.EntityID == entityID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Poster posts validated invoices idempotently through bindings. Loaders is
// keyed by posting family (DocSalesInvoice/DocSupplierInvoice).
type Poster struct {
	Store    Store // PostEntry + JournalByCode
	Postings PostingStore
	Bindings BindingStore
	Loaders  map[string]InvoiceLoader
	Bus      platform.Bus
	DB       platform.DBTX
}

// PostResult is one auto-post outcome.
type PostResult struct {
	EntryID int64 `json:"entry_id"`
	Created bool  `json:"created"` // false = idempotent replay
}

// resolve tries the specific key, then the "default" key (fail-closed when
// neither is bound — the error names the missing mapping).
func (p *Poster) resolve(ctx context.Context, entityID int64, kind, key string) (int64, error) {
	if id, err := p.Bindings.ResolveAccount(ctx, p.DB, entityID, kind, key); err == nil {
		return id, nil
	}
	if id, err := p.Bindings.ResolveAccount(ctx, p.DB, entityID, kind, "default"); err == nil {
		return id, nil
	}
	return 0, fmt.Errorf("finance: no account bound for %s/%s (or %s/default): %w",
		kind, key, kind, platform.ErrValidation)
}

// PostValidated posts one validated invoice (or replays the existing entry).
// Non-invoice documents are rejected; unvalidated documents are rejected —
// the event subscriber filters before calling.
func (p *Poster) PostValidated(ctx context.Context, entityID int64, docType string, docID int64) (PostResult, error) {
	var journalCode string
	switch docType {
	case DocSalesInvoice:
		journalCode = "VEN"
	case DocSupplierInvoice:
		journalCode = "ACH"
	default:
		return PostResult{}, fmt.Errorf("finance: cannot auto-post %q: %w", docType, platform.ErrValidation)
	}
	if existing, err := p.Postings.FindPosting(ctx, p.DB, entityID, docType, docID); err == nil {
		return PostResult{EntryID: existing.EntryID}, nil
	}
	loader, ok := p.Loaders[docType]
	if !ok {
		return PostResult{}, fmt.Errorf("finance: no loader for %q: %w", docType, platform.ErrValidation)
	}
	doc, err := loader.LoadValidatedInvoice(ctx, p.DB, entityID, docID)
	if err != nil {
		return PostResult{}, err
	}
	journal, err := p.Store.JournalByCode(ctx, p.DB, entityID, journalCode)
	if err != nil {
		return PostResult{}, fmt.Errorf("finance: journal %s not configured (load a chart pack): %w",
			journalCode, err)
	}
	control, err := p.resolve(ctx, entityID, BindingPartner, doc.OrgRef)
	if err != nil {
		return PostResult{}, err
	}
	revenue, err := p.resolve(ctx, entityID, BindingProduct, "default")
	if err != nil {
		return PostResult{}, err
	}
	lines := []EntryLine{
		{AccountID: control, Label: "control " + doc.Ref, Debit: doc.Net + doc.VAT},
		{AccountID: revenue, Label: "revenue " + doc.Ref, Credit: doc.Net},
	}
	for _, s := range doc.VATSlices {
		if s.VAT == 0 {
			continue
		}
		vatAcct, err := p.resolve(ctx, entityID, BindingVAT, fmt.Sprintf("%d", s.RateBps))
		if err != nil {
			return PostResult{}, err
		}
		lines = append(lines, EntryLine{AccountID: vatAcct,
			Label: fmt.Sprintf("vat %s", doc.Ref), Credit: s.VAT, VATRateBps: s.RateBps})
	}
	e := &Entry{EntityID: entityID, JournalID: journal.ID, Ref: doc.Ref,
		Date: doc.Date, Memo: "auto-post " + docType + " " + doc.Ref, Lines: lines}
	if err := p.Store.PostEntry(ctx, p.DB, e); err != nil {
		return PostResult{}, err
	}
	posting := &Posting{EntityID: entityID, DocType: docType, DocID: docID, EntryID: e.ID}
	if err := p.Postings.RecordPosting(ctx, p.DB, posting); err != nil {
		// Lost race with a concurrent poster: return the winner's entry.
		if existing, rerr := p.Postings.FindPosting(ctx, p.DB, entityID, docType, docID); rerr == nil {
			return PostResult{EntryID: existing.EntryID}, nil
		}
		return PostResult{}, err
	}
	if p.Bus != nil {
		_ = p.Bus.Publish(ctx, platform.Event{Subject: "forgeerp.finance.entry.autoposted.v1",
			Entity: "entry", EntityID: entityID, ID: e.ID})
	}
	return PostResult{EntryID: e.ID, Created: true}, nil
}
