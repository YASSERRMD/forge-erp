package dict

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Dictionary is one reference list (e.g. "country", "vat_rate").
type Dictionary struct {
	Code      string    `json:"code"`
	Label     string    `json:"label"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Entry is one row inside a dictionary. LocaleOverrides maps a locale tag
// ("fr", "ar", ...) to the display label in that locale; Extra carries the
// per-dictionary columns (rate_bps, ISO codes, days, ...) as JSON scalars.
type Entry struct {
	ID              int64             `json:"id"`
	Dictionary      string            `json:"dictionary"`
	Code            string            `json:"code"`
	Label           string            `json:"label"`
	Sort            int               `json:"sort"`
	Active          bool              `json:"active"`
	IsCore          bool              `json:"is_core"`
	LocaleOverrides map[string]string `json:"locale_overrides,omitempty"`
	Extra           map[string]any    `json:"extra,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ResolvedLabel returns the locale-merged display label: the override wins
// when locale is non-empty and present, otherwise the base label.
func (e Entry) ResolvedLabel(locale string) string {
	if locale != "" && e.LocaleOverrides != nil {
		if v, ok := e.LocaleOverrides[locale]; ok && v != "" {
			return v
		}
	}
	return e.Label
}

// Store is the persistence contract for reference dictionaries.
type Store interface {
	CreateDictionary(ctx context.Context, db platform.DBTX, d *Dictionary) error
	GetDictionary(ctx context.Context, db platform.DBTX, code string) (Dictionary, error)
	ListDictionaries(ctx context.Context, db platform.DBTX) ([]Dictionary, error)
	CreateEntry(ctx context.Context, db platform.DBTX, e *Entry) error
	GetEntry(ctx context.Context, db platform.DBTX, dictionary, code string) (Entry, error)
	UpdateEntry(ctx context.Context, db platform.DBTX, e *Entry) error
	// DeleteEntry deactivates (active = FALSE), including core rows.
	DeleteEntry(ctx context.Context, db platform.DBTX, dictionary, code string) error
	// HardDeleteEntry physically removes non-core rows; core rows fail
	// with a platform.ErrValidation wrap (deactivate-only).
	HardDeleteEntry(ctx context.Context, db platform.DBTX, dictionary, code string) error
	// List returns entries of one dictionary ordered by (sort, code),
	// with locale overrides merged into Label; activeOnly filters to
	// active rows.
	List(ctx context.Context, db platform.DBTX, dictionary, locale string, activeOnly bool) ([]Entry, error)
}

func validateDictionary(d *Dictionary) error {
	if strings.TrimSpace(d.Code) == "" {
		return errors.New("dict: dictionary code required")
	}
	if strings.TrimSpace(d.Label) == "" {
		return errors.New("dict: dictionary label required")
	}
	return nil
}

func validateEntry(e *Entry) error {
	if strings.TrimSpace(e.Dictionary) == "" {
		return errors.New("dict: dictionary required")
	}
	if strings.TrimSpace(e.Code) == "" {
		return errors.New("dict: entry code required")
	}
	if strings.TrimSpace(e.Label) == "" {
		return errors.New("dict: entry label required")
	}
	return nil
}

func cloneEntry(e Entry) Entry {
	out := e
	if e.LocaleOverrides != nil {
		out.LocaleOverrides = make(map[string]string, len(e.LocaleOverrides))
		for k, v := range e.LocaleOverrides {
			out.LocaleOverrides[k] = v
		}
	}
	if e.Extra != nil {
		out.Extra = make(map[string]any, len(e.Extra))
		for k, v := range e.Extra {
			out.Extra[k] = v
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// PostgreSQL store
// ---------------------------------------------------------------------------

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const dictCols = `code, label, scope, created_at, updated_at`

func scanDictionary(row pgx.Row) (Dictionary, error) {
	var d Dictionary
	if err := row.Scan(&d.Code, &d.Label, &d.Scope, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Dictionary{}, platform.ErrNotFound
		}
		return Dictionary{}, err
	}
	return d, nil
}

const entryCols = `id, dictionary, code, label, sort, active, is_core, locale_overrides, extra, created_at, updated_at`

func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	var overrides, extra []byte
	if err := row.Scan(&e.ID, &e.Dictionary, &e.Code, &e.Label, &e.Sort,
		&e.Active, &e.IsCore, &overrides, &extra, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, platform.ErrNotFound
		}
		return Entry{}, err
	}
	e.LocaleOverrides = map[string]string{}
	if len(overrides) > 0 {
		if err := json.Unmarshal(overrides, &e.LocaleOverrides); err != nil {
			return Entry{}, fmt.Errorf("dict: decode locale_overrides: %w", err)
		}
	}
	e.Extra = map[string]any{}
	if len(extra) > 0 {
		if err := json.Unmarshal(extra, &e.Extra); err != nil {
			return Entry{}, fmt.Errorf("dict: decode extra: %w", err)
		}
	}
	return e, nil
}

func marshalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func isUniqueViolation(err error) bool {
	if pgCode(err) == "23505" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}

func (s *PGStore) CreateDictionary(ctx context.Context, db platform.DBTX, d *Dictionary) error {
	if err := validateDictionary(d); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if d.Scope == "" {
		d.Scope = "global"
	}
	err := db.QueryRow(ctx, `INSERT INTO ferp_dictionaries (code, label, scope)
		VALUES ($1,$2,$3) RETURNING created_at, updated_at`,
		d.Code, d.Label, d.Scope).Scan(&d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("dict: duplicate dictionary %q: %w", d.Code, platform.ErrConflict)
		}
		return err
	}
	return nil
}

func (s *PGStore) GetDictionary(ctx context.Context, db platform.DBTX, code string) (Dictionary, error) {
	return scanDictionary(db.QueryRow(ctx,
		`SELECT `+dictCols+` FROM ferp_dictionaries WHERE code=$1`, code))
}

func (s *PGStore) ListDictionaries(ctx context.Context, db platform.DBTX) ([]Dictionary, error) {
	rows, err := db.Query(ctx, `SELECT `+dictCols+` FROM ferp_dictionaries ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dictionary
	for rows.Next() {
		d, err := scanDictionary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *PGStore) CreateEntry(ctx context.Context, db platform.DBTX, e *Entry) error {
	if err := validateEntry(e); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	overrides, err := marshalJSON(e.LocaleOverrides)
	if err != nil {
		return fmt.Errorf("dict: encode locale_overrides: %w: %w", err, platform.ErrValidation)
	}
	extra, err := marshalJSON(e.Extra)
	if err != nil {
		return fmt.Errorf("dict: encode extra: %w: %w", err, platform.ErrValidation)
	}
	err = db.QueryRow(ctx, `INSERT INTO ferp_dictionary_entries
		(dictionary, code, label, sort, active, is_core, locale_overrides, extra)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb)
		RETURNING id, created_at, updated_at`,
		e.Dictionary, e.Code, e.Label, e.Sort, e.Active, e.IsCore,
		string(overrides), string(extra)).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("dict: duplicate entry %q/%q: %w", e.Dictionary, e.Code, platform.ErrConflict)
		}
		if pgCode(err) == "23503" {
			return fmt.Errorf("dict: unknown dictionary %q: %w", e.Dictionary, platform.ErrValidation)
		}
		return err
	}
	return nil
}

func (s *PGStore) GetEntry(ctx context.Context, db platform.DBTX, dictionary, code string) (Entry, error) {
	return scanEntry(db.QueryRow(ctx, `SELECT `+entryCols+`
		FROM ferp_dictionary_entries WHERE dictionary=$1 AND code=$2`, dictionary, code))
}

func (s *PGStore) UpdateEntry(ctx context.Context, db platform.DBTX, e *Entry) error {
	if err := validateEntry(e); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	cur, err := s.GetEntry(ctx, db, e.Dictionary, e.Code)
	if err != nil {
		return err
	}
	if cur.IsCore != e.IsCore {
		return fmt.Errorf("dict: is_core is immutable: %w", platform.ErrValidation)
	}
	overrides, err := marshalJSON(e.LocaleOverrides)
	if err != nil {
		return fmt.Errorf("dict: encode locale_overrides: %w: %w", err, platform.ErrValidation)
	}
	extra, err := marshalJSON(e.Extra)
	if err != nil {
		return fmt.Errorf("dict: encode extra: %w: %w", err, platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_dictionary_entries
		SET label=$3, sort=$4, active=$5, locale_overrides=$6::jsonb, extra=$7::jsonb, updated_at=now()
		WHERE dictionary=$1 AND code=$2`, e.Dictionary, e.Code, e.Label, e.Sort,
		e.Active, string(overrides), string(extra))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// DeleteEntry deactivates the row (core-safe): core seed rows must never be
// physically removed, so Delete is deactivate-only for every row.
func (s *PGStore) DeleteEntry(ctx context.Context, db platform.DBTX, dictionary, code string) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_dictionary_entries
		SET active=FALSE, updated_at=now()
		WHERE dictionary=$1 AND code=$2`, dictionary, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// HardDeleteEntry physically removes a non-core row. Core rows are
// deactivate-only: attempting to hard-delete one fails with ErrValidation.
func (s *PGStore) HardDeleteEntry(ctx context.Context, db platform.DBTX, dictionary, code string) error {
	cur, err := s.GetEntry(ctx, db, dictionary, code)
	if err != nil {
		return err
	}
	if cur.IsCore {
		return fmt.Errorf("dict: core entry %q/%q is deactivate-only: %w",
			dictionary, code, platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `DELETE FROM ferp_dictionary_entries
		WHERE dictionary=$1 AND code=$2`, dictionary, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// List returns entries ordered by (sort, code) with the requested locale's
// override merged into Label.
func (s *PGStore) List(ctx context.Context, db platform.DBTX, dictionary, locale string, activeOnly bool) ([]Entry, error) {
	q := `SELECT ` + entryCols + ` FROM ferp_dictionary_entries WHERE dictionary=$1`
	args := []any{dictionary}
	if activeOnly {
		q += ` AND active`
	}
	q += ` ORDER BY sort, code`
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		if locale != "" {
			e.Label = e.ResolvedLabel(locale)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// In-memory store (handler tests)
// ---------------------------------------------------------------------------

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	dicts  map[string]Dictionary
	entrys map[string]Entry // key: dictionary + "\x00" + code
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{dicts: map[string]Dictionary{}, entrys: map[string]Entry{}}
}

func entryKey(dictionary, code string) string { return dictionary + "\x00" + code }

func (m *MemoryStore) CreateDictionary(_ context.Context, _ platform.DBTX, d *Dictionary) error {
	if err := validateDictionary(d); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if d.Scope == "" {
		d.Scope = "global"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.dicts[d.Code]; ok {
		return fmt.Errorf("dict: duplicate dictionary %q: %w", d.Code, platform.ErrConflict)
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	m.dicts[d.Code] = *d
	return nil
}

func (m *MemoryStore) GetDictionary(_ context.Context, _ platform.DBTX, code string) (Dictionary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dicts[code]
	if !ok {
		return Dictionary{}, platform.ErrNotFound
	}
	return d, nil
}

func (m *MemoryStore) ListDictionaries(_ context.Context, _ platform.DBTX) ([]Dictionary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Dictionary, 0, len(m.dicts))
	for _, d := range m.dicts {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

func (m *MemoryStore) CreateEntry(_ context.Context, _ platform.DBTX, e *Entry) error {
	if err := validateEntry(e); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.dicts[e.Dictionary]; !ok {
		return fmt.Errorf("dict: unknown dictionary %q: %w", e.Dictionary, platform.ErrValidation)
	}
	k := entryKey(e.Dictionary, e.Code)
	if _, ok := m.entrys[k]; ok {
		return fmt.Errorf("dict: duplicate entry %q/%q: %w", e.Dictionary, e.Code, platform.ErrConflict)
	}
	m.seq++
	e.ID = m.seq
	now := time.Now().UTC()
	e.CreatedAt, e.UpdatedAt = now, now
	m.entrys[k] = cloneEntry(*e)
	return nil
}

func (m *MemoryStore) GetEntry(_ context.Context, _ platform.DBTX, dictionary, code string) (Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entrys[entryKey(dictionary, code)]
	if !ok {
		return Entry{}, platform.ErrNotFound
	}
	return cloneEntry(e), nil
}

func (m *MemoryStore) UpdateEntry(_ context.Context, _ platform.DBTX, e *Entry) error {
	if err := validateEntry(e); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := entryKey(e.Dictionary, e.Code)
	cur, ok := m.entrys[k]
	if !ok {
		return platform.ErrNotFound
	}
	if cur.IsCore != e.IsCore {
		return fmt.Errorf("dict: is_core is immutable: %w", platform.ErrValidation)
	}
	upd := cloneEntry(*e)
	upd.ID = cur.ID
	upd.CreatedAt = cur.CreatedAt
	upd.UpdatedAt = time.Now().UTC()
	m.entrys[k] = upd
	*e = cloneEntry(upd)
	return nil
}

func (m *MemoryStore) DeleteEntry(_ context.Context, _ platform.DBTX, dictionary, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := entryKey(dictionary, code)
	e, ok := m.entrys[k]
	if !ok {
		return platform.ErrNotFound
	}
	e.Active = false
	e.UpdatedAt = time.Now().UTC()
	m.entrys[k] = e
	return nil
}

func (m *MemoryStore) HardDeleteEntry(_ context.Context, _ platform.DBTX, dictionary, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := entryKey(dictionary, code)
	e, ok := m.entrys[k]
	if !ok {
		return platform.ErrNotFound
	}
	if e.IsCore {
		return fmt.Errorf("dict: core entry %q/%q is deactivate-only: %w",
			dictionary, code, platform.ErrValidation)
	}
	delete(m.entrys, k)
	return nil
}

func (m *MemoryStore) List(_ context.Context, _ platform.DBTX, dictionary, locale string, activeOnly bool) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, e := range m.entrys {
		if e.Dictionary != dictionary {
			continue
		}
		if activeOnly && !e.Active {
			continue
		}
		e = cloneEntry(e)
		if locale != "" {
			e.Label = e.ResolvedLabel(locale)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].Code < out[j].Code
	})
	return out, nil
}
