// Package number implements Kernel 4 (pluggable document numbering models).
//
// Selection is per-entity-per-doctype, read from ferp_config (Dolibarr llx_const
// equivalent, see backend/migrations/0001_platform.up.sql) under the key
// convention:
//
//	numbering.<doctype> = <model code>   e.g. numbering.invoice = "mercure"
//
// When the key is missing or empty the "standard" model is used.
//
// Built-ins:
//   - "standard": the current documents.NextRef scheme (PREFIX-YYYYMM-####,
//     monthly counter). Gap-TOLERANT: allocation is a single-statement atomic
//     UPSERT on ferp_doc_counters, so concurrent committers get distinct
//     sequences, but a rolled-back transaction burns its value and leaves a
//     gap. That matches today's sales/procurement nextRefLocked behaviour.
//   - "mercure": a visibly different, Mercure-style scheme — an ENTITY-SCOPED
//     running sequence with no date segment: MC-<PREFIX>-<entity:04d>-<seq:06d>
//     (e.g. MC-INV-0001-000042). The counter reuses ferp_doc_counters with a
//     fixed year_month bucket ("*"), so it never resets month-to-month and is
//     monotonic per (entity, doctype). Same gap tolerance as standard.
//
// Concurrency contract (both models): allocation MUST run inside the caller's
// transaction — Next takes a platform.DBTX and never begins/commits. Callers
// (sales/procurement CreateDoc) pass their JoinTx handle so the counter bump
// and the document insert commit atomically.
package number

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
)

// NumberModel is one pluggable numbering scheme.
type NumberModel interface {
	// Code is the registry key stored in ferp_config ("standard", "mercure", ...).
	Code() string
	// Next allocates the next reference inside the caller's tx and returns it.
	// It never begins or commits a transaction.
	Next(ctx context.Context, db platform.DBTX, entityID int64, docType string, at time.Time) (string, error)
	// Preview renders an example reference for cfg without touching the DB.
	Preview(cfg Config) string
}

// Config carries the inputs Preview renders without DB access.
type Config struct {
	// DocType is the document family ("invoice", "order", ...).
	DocType string
	// EntityID scopes mercure-style previews.
	EntityID int64
	// At selects the YYYYMM segment for date-scoped models; zero means 202601.
	At time.Time
	// Seq is the sequence number rendered; <=0 means 1.
	Seq int64
}

// DefaultCode is used when ferp_config holds no numbering.<doctype> key.
const DefaultCode = "standard"

// Key returns the ferp_config name for a doctype, e.g. "numbering.invoice".
func Key(docType string) string { return "numbering." + docType }

var (
	mu     sync.RWMutex
	models = map[string]NumberModel{}
)

// Register adds a model to the global registry. It panics on a nil model, an
// empty code, or a duplicate code (all programming errors caught in tests).
func Register(m NumberModel) {
	if m == nil || m.Code() == "" {
		panic("number: cannot register model with empty code")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := models[m.Code()]; dup {
		panic(fmt.Sprintf("number: duplicate model code %q", m.Code()))
	}
	models[m.Code()] = m
}

// ByCode returns the model for code or an error wrapping
// platform.ErrValidation so errors.Is works across package boundaries.
func ByCode(code string) (NumberModel, error) {
	mu.RLock()
	defer mu.RUnlock()
	if m, ok := models[code]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("number: unknown numbering model %q: %w", code, platform.ErrValidation)
}

// Codes lists registered model codes in sorted order (for diagnostics/tests).
func Codes() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(models))
	for c := range models {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ForEntity resolves the model for (entity, doctype) from ferp_config key
// numbering.<doctype>. Missing rows, empty values, and NULLs fall back to the
// "standard" model. A configured-but-unknown code is an error (fail loud, so
// a typo never silently renumbers documents).
func ForEntity(ctx context.Context, db platform.DBTX, entityID int64, docType string) (NumberModel, error) {
	if entityID == 0 {
		return nil, platform.ErrUnauthorized
	}
	if docType == "" {
		return nil, fmt.Errorf("number: empty doc type: %w", platform.ErrValidation)
	}
	var code string
	err := db.QueryRow(ctx, `SELECT value FROM ferp_config WHERE entity_id=$1 AND name=$2`, entityID, Key(docType)).Scan(&code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ByCode(DefaultCode)
		}
		return nil, err
	}
	if code == "" {
		return ByCode(DefaultCode)
	}
	return ByCode(code)
}

// Next resolves the model for (entity, doctype) and allocates one reference
// inside the caller's tx.
func Next(ctx context.Context, db platform.DBTX, entityID int64, docType string, at time.Time) (string, error) {
	m, err := ForEntity(ctx, db, entityID, docType)
	if err != nil {
		return "", err
	}
	return m.Next(ctx, db, entityID, docType, at)
}

// yearMonth renders the YYYYMM counter segment; zero time falls back to now.
func yearMonth(at time.Time) string {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return at.UTC().Format("200601")
}

// allocMonthly bumps ferp_doc_counters (entity, type, year_month) atomically
// inside the caller's tx and returns the allocated sequence (starting at 1).
// Single-statement INSERT..ON CONFLICT: safe under concurrency, gap-tolerant
// under rollback.
func allocMonthly(ctx context.Context, db platform.DBTX, entityID int64, docType, ym string) (int64, error) {
	var seq int64
	err := db.QueryRow(ctx, `INSERT INTO ferp_doc_counters (entity_id, type, year_month, next_seq)
		VALUES ($1,$2,$3,2) ON CONFLICT (entity_id, type, year_month)
		DO UPDATE SET next_seq=ferp_doc_counters.next_seq+1
		RETURNING next_seq-1`, entityID, docType, ym).Scan(&seq)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// standardModel reuses documents.NextRef: PREFIX-YYYYMM-####, monthly reset.
type standardModel struct{}

func (standardModel) Code() string { return "standard" }

func (standardModel) Next(ctx context.Context, db platform.DBTX, entityID int64, docType string, at time.Time) (string, error) {
	if entityID == 0 {
		return "", platform.ErrUnauthorized
	}
	if docType == "" {
		return "", fmt.Errorf("number: empty doc type: %w", platform.ErrValidation)
	}
	ym := yearMonth(at)
	seq, err := allocMonthly(ctx, db, entityID, docType, ym)
	if err != nil {
		return "", err
	}
	return documents.NextRef(documents.DocType(docType), ym, seq), nil
}

func (standardModel) Preview(cfg Config) string {
	ym := "202601"
	if !cfg.At.IsZero() {
		ym = cfg.At.UTC().Format("200601")
	}
	seq := cfg.Seq
	if seq <= 0 {
		seq = 1
	}
	t := documents.DocType(cfg.DocType)
	if cfg.DocType == "" {
		t = documents.TypeInvoice
	}
	return documents.NextRef(t, ym, seq)
}

// mercureBucket is the fixed year_month bucket holding the mercure running
// sequence in ferp_doc_counters (no new table/migration needed).
const mercureBucket = "*"

// mercureRef renders the Mercure-style reference: MC-<PREFIX>-<entity>-
// <seq>, e.g. MC-INV-0001-000042. No date segment: the sequence never resets.
func mercureRef(docType string, entityID, seq int64) string {
	return fmt.Sprintf("MC-%s-%04d-%06d", documents.DocType(docType).Prefix(), entityID, seq)
}

// mercureModel is the visibly different built-in: entity-scoped running
// sequence with prefix, monotonic per (entity, doctype) across months.
type mercureModel struct{}

func (mercureModel) Code() string { return "mercure" }

func (mercureModel) Next(ctx context.Context, db platform.DBTX, entityID int64, docType string, at time.Time) (string, error) {
	if entityID == 0 {
		return "", platform.ErrUnauthorized
	}
	if docType == "" {
		return "", fmt.Errorf("number: empty doc type: %w", platform.ErrValidation)
	}
	_ = at // mercure refs carry no date segment; at is accepted for interface symmetry.
	seq, err := allocMonthly(ctx, db, entityID, docType, mercureBucket)
	if err != nil {
		return "", err
	}
	return mercureRef(docType, entityID, seq), nil
}

func (mercureModel) Preview(cfg Config) string {
	seq := cfg.Seq
	if seq <= 0 {
		seq = 1
	}
	dt := cfg.DocType
	if dt == "" {
		dt = string(documents.TypeInvoice)
	}
	return mercureRef(dt, cfg.EntityID, seq)
}

func init() {
	Register(standardModel{})
	Register(mercureModel{})
}
