// Package fx implements currency rates and conversion (Dolibarr
// multicurrency rate tables): per-entity board rates to the base currency and
// a convert endpoint. Document snapshots (rate_to_base) remain the audit
// source; this service holds the current board rate.
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Rate is one board rate to the base currency (scaled ×1e6 like documents).
type Rate struct {
	EntityID   int64     `json:"entity_id"`
	Code       string    `json:"code"` // ISO-4217
	RateToBase int64     `json:"rate_to_base"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Validate checks a rate.
func (r Rate) Validate() error {
	if r.EntityID <= 0 {
		return errors.New("fx: entity_id required")
	}
	if len(strings.TrimSpace(r.Code)) != 3 {
		return fmt.Errorf("fx: bad currency code %q", r.Code)
	}
	if r.RateToBase <= 0 {
		return errors.New("fx: rate must be positive")
	}
	return nil
}

// Convert translates minor units between currencies via board rates.
func Convert(amount int64, from, to Rate) int64 {
	if from.Code == to.Code {
		return amount
	}
	return amount * from.RateToBase / to.RateToBase
}

// Store is the persistence contract for rates.
type Store interface {
	SetRate(ctx context.Context, r *Rate) error
	RateByCode(ctx context.Context, entityID int64, code string) (Rate, error)
	ListRates(ctx context.Context, entityID int64) ([]Rate, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) SetRate(ctx context.Context, r *Rate) error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.Code = strings.ToUpper(strings.TrimSpace(r.Code))
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_fx_rates (entity_id, code, rate_to_base)
		VALUES ($1,$2,$3)
		ON CONFLICT (entity_id, code) DO UPDATE SET rate_to_base=EXCLUDED.rate_to_base, updated_at=now()
		RETURNING updated_at`, r.EntityID, r.Code, r.RateToBase).Scan(&r.UpdatedAt)
}

func (s *PGStore) RateByCode(ctx context.Context, entityID int64, code string) (Rate, error) {
	var r Rate
	err := s.pool.QueryRow(ctx, `SELECT entity_id, code, rate_to_base, updated_at
		FROM ferp_fx_rates WHERE entity_id=$1 AND code=$2`,
		entityID, strings.ToUpper(strings.TrimSpace(code))).Scan(
		&r.EntityID, &r.Code, &r.RateToBase, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rate{}, identity.ErrNotFound
	}
	return r, err
}

func (s *PGStore) ListRates(ctx context.Context, entityID int64) ([]Rate, error) {
	rows, err := s.pool.Query(ctx, `SELECT entity_id, code, rate_to_base, updated_at
		FROM ferp_fx_rates WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rate
	for rows.Next() {
		var r Rate
		if err := rows.Scan(&r.EntityID, &r.Code, &r.RateToBase, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process rate board.
type MemoryStore struct {
	mu    sync.Mutex
	rates map[string]Rate // entity/code -> rate
}

// NewMemoryStore builds an empty board (USD base preloaded for entity 1).
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rates: map[string]Rate{
		"1/USD": {EntityID: 1, Code: "USD", RateToBase: 1000000},
	}}
}

func rateKey(entityID int64, code string) string {
	return fmt.Sprintf("%d/%s", entityID, strings.ToUpper(strings.TrimSpace(code)))
}

func (m *MemoryStore) SetRate(_ context.Context, r *Rate) error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.Code = strings.ToUpper(strings.TrimSpace(r.Code))
	r.UpdatedAt = time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rates[rateKey(r.EntityID, r.Code)] = *r
	return nil
}

func (m *MemoryStore) RateByCode(_ context.Context, entityID int64, code string) (Rate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rates[rateKey(entityID, code)]
	if !ok {
		return Rate{}, identity.ErrNotFound
	}
	return r, nil
}

func (m *MemoryStore) ListRates(_ context.Context, entityID int64) ([]Rate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Rate
	for k, r := range m.rates {
		if strings.HasPrefix(k, fmt.Sprintf("%d/", entityID)) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Deps wires handlers to the rate board.
type Deps struct {
	Store Store
	Bus   platform.Bus
}

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the fx surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("fx", "rate", "read")).Get("/fx/rates", h.ListRates)
	r.With(mw("fx", "rate", "write")).Post("/fx/rates", h.SetRate)
	r.With(mw("fx", "rate", "read")).Get("/fx/convert", h.Convert)
}

// Handler implements the fx surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

func storeErrorCode(err error) int {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusUnprocessableEntity
	}
}

// ListRates lists board rates.
func (h *Handler) ListRates(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListRates(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetRate upserts a board rate.
func (h *Handler) SetRate(w http.ResponseWriter, r *http.Request) {
	var rate Rate
	if err := decode(r, &rate); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rate.EntityID = entityOf(r)
	if err := h.deps.Store.SetRate(r.Context(), &rate); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rate)
}

// Convert translates an amount between board currencies.
func (h *Handler) Convert(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	amount, err := strconv.ParseInt(q.Get("amount"), 10, 64)
	if err != nil || amount < 0 {
		writeErr(w, http.StatusBadRequest, "amount required")
		return
	}
	from, err1 := h.deps.Store.RateByCode(r.Context(), entityOf(r), q.Get("from"))
	to, err2 := h.deps.Store.RateByCode(r.Context(), entityOf(r), q.Get("to"))
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusUnprocessableEntity, "fx: unknown currency")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"amount": amount, "from": from.Code, "to": to.Code,
		"result": Convert(amount, from, to),
	})
}
