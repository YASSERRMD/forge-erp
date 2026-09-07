// Package inbound implements the email-to-ticket gateway (Dolibarr ticket
// email gateway + emailcollector config): inbound mailboxes with connection
// settings and a receive endpoint that files verified messages as helpdesk
// tickets. Live IMAP polling is a follow-up; the gateway contract (fetch log
// + ticket filing) is fully testable offline.
package inbound

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
)

// Mailbox is an inbound mail source (fetch config; passwords live in env/secrets).
type Mailbox struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	Code      string    `json:"code"` // unique per entity
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Username  string    `json:"username"`
	UseTLS    bool      `json:"use_tls"`
	Active    bool      `json:"active"`
	LastFetch *time.Time `json:"last_fetch"`
	LastError string    `json:"last_error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	RowVersion int64    `json:"row_version"`
}

// Validate checks mailbox invariants.
func (m Mailbox) Validate() error {
	if m.EntityID <= 0 {
		return errors.New("inbound: entity_id required")
	}
	if strings.TrimSpace(m.Code) == "" || strings.TrimSpace(m.Host) == "" {
		return errors.New("inbound: code and host required")
	}
	if m.Port <= 0 || m.Port > 65535 {
		return errors.New("inbound: bad port")
	}
	return nil
}

// Tickets abstracts ticket filing for received mail.
type Tickets interface {
	CreateTicket(ctx context.Context, t *services.Ticket) error
	AddMessage(ctx context.Context, m *services.TicketMessage) error
}

// Store is the persistence contract for mailboxes.
type Store interface {
	UpsertMailbox(ctx context.Context, m *Mailbox) error
	MailboxByCode(ctx context.Context, entityID int64, code string) (Mailbox, error)
	ListMailboxes(ctx context.Context, entityID int64) ([]Mailbox, error)
	RecordFetch(ctx context.Context, entityID int64, code string, at time.Time, fetchErr string) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const mailboxCols = `id, entity_id, code, host, port, username, use_tls, active, last_fetch, last_error, created_at, updated_at, row_version`

func scanMailbox(row pgx.Row) (Mailbox, error) {
	var m Mailbox
	err := row.Scan(&m.ID, &m.EntityID, &m.Code, &m.Host, &m.Port, &m.Username,
		&m.UseTLS, &m.Active, &m.LastFetch, &m.LastError,
		&m.CreatedAt, &m.UpdatedAt, &m.RowVersion)
	return m, mailboxErr(err)
}

func mailboxErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	return err
}

func (s *PGStore) UpsertMailbox(ctx context.Context, mb *Mailbox) error {
	if err := mb.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_mailboxes
		(entity_id, code, host, port, username, use_tls, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (entity_id, code) DO UPDATE SET
			host=EXCLUDED.host, port=EXCLUDED.port, username=EXCLUDED.username,
			use_tls=EXCLUDED.use_tls, active=EXCLUDED.active,
			updated_at=now(), row_version=ferp_mailboxes.row_version+1
		RETURNING id, row_version`,
		mb.EntityID, mb.Code, mb.Host, mb.Port, mb.Username, mb.UseTLS, mb.Active,
	).Scan(&mb.ID, &mb.RowVersion)
}

func (s *PGStore) MailboxByCode(ctx context.Context, entityID int64, code string) (Mailbox, error) {
	mb, err := scanMailbox(s.pool.QueryRow(ctx, `SELECT `+mailboxCols+` FROM ferp_mailboxes
		WHERE entity_id=$1 AND code=$2`, entityID, code))
	return mb, mailboxErr(err)
}

func (s *PGStore) ListMailboxes(ctx context.Context, entityID int64) ([]Mailbox, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM ferp_mailboxes WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mailbox
	for rows.Next() {
		mb, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mb)
	}
	return out, rows.Err()
}

func (s *PGStore) RecordFetch(ctx context.Context, entityID int64, code string, at time.Time, fetchErr string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_mailboxes SET last_fetch=$1, last_error=$2, updated_at=now()
		WHERE entity_id=$3 AND code=$4`, at, fetchErr, entityID, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

// MemoryStore is the in-process mailbox store (PG arrives with the poller;
// gateway behavior is fully covered here).
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	mailboxes map[int64]Mailbox
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{mailboxes: map[int64]Mailbox{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) UpsertMailbox(_ context.Context, mb *Mailbox) error {
	if err := mb.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.mailboxes {
		if e.EntityID == mb.EntityID && e.Code == mb.Code {
			mb.ID = id
			mb.RowVersion = e.RowVersion + 1
			m.mailboxes[id] = *mb
			return nil
		}
	}
	mb.ID = m.next()
	mb.RowVersion = 1
	m.mailboxes[mb.ID] = *mb
	return nil
}

func (m *MemoryStore) MailboxByCode(_ context.Context, entityID int64, code string) (Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.mailboxes {
		if e.EntityID == entityID && e.Code == code {
			return e, nil
		}
	}
	return Mailbox{}, identity.ErrNotFound
}

func (m *MemoryStore) ListMailboxes(_ context.Context, entityID int64) ([]Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Mailbox
	for _, e := range m.mailboxes {
		if e.EntityID == entityID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) RecordFetch(_ context.Context, entityID int64, code string, at time.Time, fetchErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.mailboxes {
		if e.EntityID == entityID && e.Code == code {
			e.LastFetch = &at
			e.LastError = fetchErr
			m.mailboxes[id] = e
			return nil
		}
	}
	return identity.ErrNotFound
}

// Deps wires handlers to mailbox state, ticket filing and the bus.
type Deps struct {
	Store   Store
	Tickets Tickets
	Bus     platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the inbound surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("inbound", "mailbox", "write")).Post("/inbound/mailboxes", h.UpsertMailbox)
	r.With(mw("inbound", "mailbox", "read")).Get("/inbound/mailboxes", h.ListMailboxes)
	r.With(mw("inbound", "message", "write")).Post("/inbound/messages", h.Receive)
}

// Handler implements the inbound surface.
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
	case err != nil && strings.Contains(err.Error(), "duplicate"):
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

func (h *Handler) publish(ctx context.Context, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, ID: id})
}

// UpsertMailbox registers or updates a fetch source.
func (h *Handler) UpsertMailbox(w http.ResponseWriter, r *http.Request) {
	var mb Mailbox
	if err := decode(r, &mb); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	mb.EntityID = entityOf(r)
	if err := h.deps.Store.UpsertMailbox(r.Context(), &mb); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mb)
}

// ListMailboxes lists fetch sources with last-fetch state.
func (h *Handler) ListMailboxes(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListMailboxes(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type inboundMessage struct {
	Mailbox string `json:"mailbox"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Receive files one fetched message as a ticket on a known active mailbox
// and stamps the fetch log (the live IMAP poller calls this per message).
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	var in inboundMessage
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if strings.TrimSpace(in.Mailbox) == "" || strings.TrimSpace(in.From) == "" ||
		strings.TrimSpace(in.Subject) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "inbound: mailbox, from and subject required")
		return
	}
	mb, err := h.deps.Store.MailboxByCode(r.Context(), entityOf(r), in.Mailbox)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if !mb.Active {
		writeErr(w, http.StatusUnprocessableEntity, "inbound: mailbox inactive")
		return
	}
	now := time.Now().UTC()
	ref := "MAIL-" + strconv.FormatInt(now.UnixNano(), 10)
	tk := &services.Ticket{EntityID: entityOf(r), Ref: ref,
		Subject: in.Subject, Priority: 2, Status: services.TicketOpen}
	if err := h.deps.Tickets.CreateTicket(r.Context(), tk); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	msg := &services.TicketMessage{EntityID: tk.EntityID, TicketID: tk.ID,
		Author: in.From, Body: in.Body}
	if err := h.deps.Tickets.AddMessage(r.Context(), msg); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	_ = h.deps.Store.RecordFetch(r.Context(), tk.EntityID, mb.Code, now, "")
	h.publish(r.Context(), "forgeerp.inbound.ticketed.v1", "ticket", tk.ID)
	writeJSON(w, http.StatusCreated, tk)
}
