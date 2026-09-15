package members

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/workflow"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
	// Ledger posts loan disbursement/repayment entries via finance
	// PostEntry (nil → posting endpoints answer 501).
	Ledger LedgerPoster
	// Workflow fires automatic actions on subscription status changes
	// (nil → default SubscriptionWorkflow table).
	Workflow *workflow.Engine
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the members surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("members", "type", "write")).Post("/member-types", h.CreateType)
	r.With(mw("members", "type", "read")).Get("/member-types", h.ListTypes)
	r.With(mw("members", "member", "write")).Post("/members", h.CreateMember)
	r.With(mw("members", "member", "read")).Get("/members", h.ListMembers)
	r.With(mw("members", "member", "validate")).Post("/members/{id}/status", h.SetMemberStatus)
	r.With(mw("members", "subscription", "write")).Post("/members/{id}/subscriptions", h.CreateSubscription)
	r.With(mw("members", "subscription", "read")).Get("/members/{id}/subscriptions", h.ListSubscriptions)
	r.With(mw("members", "subscription", "validate")).Post("/subscriptions/{id}/status", h.SetSubscriptionStatus)
	r.With(mw("members", "donation", "write")).Post("/donations", h.CreateDonation)
	r.With(mw("members", "donation", "read")).Get("/donations", h.ListDonations)
	r.With(mw("members", "donation", "validate")).Post("/donations/{id}/status", h.SetDonationStatus)
	r.With(mw("members", "donation", "read")).Get("/donations/{id}/receipt", h.DonationReceipt)
	r.With(mw("members", "loan", "write")).Post("/member-loans", h.CreateMemberLoan)
	r.With(mw("members", "loan", "read")).Get("/member-loans/{id}/schedule", h.MemberLoanSchedule)
	r.With(mw("members", "loan", "write")).Post("/member-loans/{id}/disburse", h.DisburseMemberLoan)
	r.With(mw("members", "loan", "write")).Post("/member-loans/{id}/repay", h.RepayMemberLoan)
}

// Handler implements the members HTTP surface.
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

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func page(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// authActor returns the authenticated user id, or nil outside identity
// middleware (handler tests, service paths).
func authActor(r *http.Request) *int64 {
	if u, ok := identity.AuthUser(r); ok {
		return &u.ID
	}
	return nil
}

func errValidation(msg string) error {
	return &codedError{msg: msg, code: platform.ErrValidation}
}

func errNotFound(msg string) error {
	return &codedError{msg: msg, code: platform.ErrNotFound}
}

// codedError carries a message with a platform sentinel so errors.Is and
// platform.ErrorCode keep working across the handler boundary.
type codedError struct {
	msg  string
	code error
}

func (e *codedError) Error() string { return e.msg }
func (e *codedError) Unwrap() error { return e.code }

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateType registers a membership class.
func (h *Handler) CreateType(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var t MemberType
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityID
	if err := h.deps.Store.CreateType(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// ListTypes lists membership classes.
func (h *Handler) ListTypes(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListTypes(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateMember registers a draft member.
func (h *Handler) CreateMember(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var m Member
	if err := decode(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m.ID = 0
	m.EntityID = entityID
	m.Status = MemberDraft
	if err := h.deps.Store.CreateMember(r.Context(), h.deps.DB, &m); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.members.created.v1", "member", m.ID)
	writeJSON(w, http.StatusCreated, m)
}

// ListMembers pages members.
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset := page(r)
	list, err := h.deps.Store.ListMembers(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetMemberStatus moves a member along its lifecycle.
func (h *Handler) SetMemberStatus(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m, err := h.deps.Store.SetMemberStatus(r.Context(), h.deps.DB, entityID, id, MemberStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// CreateSubscription opens a draft yearly subscription.
func (h *Handler) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	mid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var s Subscription
	if err := decode(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	s.ID = 0
	s.EntityID = entityID
	s.MemberID = mid
	s.Status = SubDraft
	if err := h.deps.Store.CreateSubscription(r.Context(), h.deps.DB, &s); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

// ListSubscriptions lists a member's subscriptions.
func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	mid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.SubscriptionsOf(r.Context(), h.deps.DB, entityID, mid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetSubscriptionStatus moves a subscription along its flow.
func (h *Handler) SetSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	pre, err := h.deps.Store.SubscriptionByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	s, err := h.deps.Store.SetSubscriptionStatus(r.Context(), h.deps.DB, entityID, id, SubscriptionStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	// Workflow proof adoption: fire automatic actions for the legal move.
	// Hook actions publish with the field-set-merged payload; other kinds
	// are returned for the caller (webhook delivery stays out of band).
	base := map[string]any{"subscription_id": s.ID, "member_id": s.MemberID,
		"year": s.Year, "amount": s.Amount}
	actions, payload := fireSubscription(h.deps.Workflow, entityID, s.ID, pre.Status, s.Status, base)
	if h.deps.Bus != nil {
		for _, a := range actions {
			if a.Kind != workflow.ActionHook {
				continue
			}
			_ = h.deps.Bus.Publish(r.Context(), platform.Event{
				Subject: a.Target, Entity: "subscription",
				EntityID: entityID, ID: s.ID, Payload: payload})
		}
	}
	writeJSON(w, http.StatusOK, s)
}

// CreateDonation records a promised donation.
func (h *Handler) CreateDonation(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var d Donation
	if err := decode(r, &d); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	d.ID = 0
	d.EntityID = entityID
	d.Status = DonationPromised
	if err := h.deps.Store.CreateDonation(r.Context(), h.deps.DB, &d); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.donation.created.v1", "donation", d.ID)
	writeJSON(w, http.StatusCreated, d)
}

// ListDonations pages donations.
func (h *Handler) ListDonations(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset := page(r)
	list, err := h.deps.Store.ListDonations(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetDonationStatus settles or cancels a donation.
func (h *Handler) SetDonationStatus(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	d, err := h.deps.Store.SetDonationStatus(r.Context(), h.deps.DB, entityID, id, DonationStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// DonationReceipt renders a paid donation's PDF receipt via the platform
// docgen registry (422 unless paid).
func (h *Handler) DonationReceipt(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	d, err := h.deps.Store.DonationByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	reader, contentType, err := RenderDonationReceipt(r.Context(), d, r.URL.Query().Get("locale"))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

type memberLoanRequest struct {
	Label     string    `json:"label"`
	Principal int64     `json:"principal"`
	RateBps   int       `json:"rate_bps"`
	Start     time.Time `json:"start"`
	Periods   int       `json:"periods"`
}

// CreateMemberLoan books a loan and persists its French-amortisation schedule.
func (h *Handler) CreateMemberLoan(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req memberLoanRequest
	if err := decode(r, &req); err != nil || req.Principal <= 0 || req.Periods <= 0 {
		writeErr(w, http.StatusBadRequest, "label, principal and periods required")
		return
	}
	if req.Start.IsZero() {
		req.Start = time.Now().UTC()
	}
	l := &MemberLoan{EntityID: entityID, Label: req.Label, Principal: req.Principal,
		RateBps: req.RateBps, Start: req.Start, Periods: req.Periods}
	if err := h.deps.Store.CreateMemberLoan(r.Context(), h.deps.DB, l); err != nil {
		platform.WriteError(w, err)
		return
	}
	sched, err := h.deps.Store.MemberLoanSchedule(r.Context(), h.deps.DB, entityID, l.ID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"loan": l, "schedule": sched})
}

// MemberLoanSchedule lists a loan's installments.
func (h *Handler) MemberLoanSchedule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	sched, err := h.deps.Store.MemberLoanSchedule(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

type disburseRequest struct {
	JournalID         int64 `json:"journal_id"`
	ReceivableAccount int64 `json:"receivable_account"`
	BankAccount       int64 `json:"bank_account"`
	RowVersion        int64 `json:"row_version"`
}

// DisburseMemberLoan posts the disbursement entry via finance and marks the
// loan disbursed (money first, then status; a status failure after a posted
// entry surfaces as 500 with the entry id in the error detail).
func (h *Handler) DisburseMemberLoan(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req disburseRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if h.deps.Ledger == nil {
		writeErr(w, http.StatusNotImplemented, "ledger not configured")
		return
	}
	loan, err := h.deps.Store.MemberLoanByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if loan.Status != MemberLoanDraft {
		platform.WriteError(w, errValidation("members: loan not in draft"))
		return
	}
	createdBy := authActor(r)
	entry, err := PostLoanDisbursement(r.Context(), h.deps.DB, h.deps.Ledger,
		entityID, req.JournalID, loan, req.ReceivableAccount, req.BankAccount, time.Now().UTC(), createdBy)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	upd, err := h.deps.Store.SetMemberLoanStatus(r.Context(), h.deps.DB, entityID, id, MemberLoanDisbursed, req.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"loan": upd, "entry_id": entry.ID})
}

type repayRequest struct {
	Seq               int   `json:"seq"`
	JournalID         int64 `json:"journal_id"`
	BankAccount       int64 `json:"bank_account"`
	ReceivableAccount int64 `json:"receivable_account"`
	InterestAccount   int64 `json:"interest_account"`
}

// RepayMemberLoan posts one installment via finance and flags it paid;
// when the last open installment closes, the loan moves to repaid.
func (h *Handler) RepayMemberLoan(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req repayRequest
	if err := decode(r, &req); err != nil || req.Seq <= 0 {
		writeErr(w, http.StatusBadRequest, "seq and accounts required")
		return
	}
	if h.deps.Ledger == nil {
		writeErr(w, http.StatusNotImplemented, "ledger not configured")
		return
	}
	loan, err := h.deps.Store.MemberLoanByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if loan.Status != MemberLoanDisbursed {
		platform.WriteError(w, errValidation("members: loan not disbursed"))
		return
	}
	sched, err := h.deps.Store.MemberLoanSchedule(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	var line *MemberLoanLine
	for i := range sched {
		if sched[i].Seq == req.Seq {
			line = &sched[i]
			break
		}
	}
	if line == nil {
		platform.WriteError(w, errNotFound("members: installment not found"))
		return
	}
	createdBy := authActor(r)
	entry, err := PostLoanRepayment(r.Context(), h.deps.DB, h.deps.Ledger,
		entityID, req.JournalID, loan, *line,
		req.BankAccount, req.ReceivableAccount, req.InterestAccount, time.Now().UTC(), createdBy)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if _, err := h.deps.Store.MarkLoanLinePaid(r.Context(), h.deps.DB, entityID, id, req.Seq, time.Now().UTC()); err != nil {
		platform.WriteError(w, err)
		return
	}
	sched, err = h.deps.Store.MemberLoanSchedule(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	open := false
	for _, l := range sched {
		if !l.Paid {
			open = true
			break
		}
	}
	if !open {
		fresh, err := h.deps.Store.MemberLoanByID(r.Context(), h.deps.DB, entityID, id)
		if err != nil {
			platform.WriteError(w, err)
			return
		}
		if loan, err = h.deps.Store.SetMemberLoanStatus(r.Context(), h.deps.DB, entityID, id, MemberLoanRepaid, fresh.RowVersion); err != nil {
			platform.WriteError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"loan": loan, "entry_id": entry.ID, "schedule": sched})
}
