package sepa

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store  Store
	DB     platform.DBTX
	Bus    platform.Bus
	Pool   *pgxpool.Pool // transaction source for the R-transaction service (nil in tests)
	Ledger Ledger        // nil disables reversal posting (memory path)
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the sepa surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d, svc: NewService(d.Pool, d.Store, d.Ledger, d.Bus)}
	r.With(mw("sepa", "batch", "write")).Post("/sepa/batches", h.CreateBatch)
	r.With(mw("sepa", "batch", "read")).Get("/sepa/batches", h.ListBatches)
	r.With(mw("sepa", "batch", "validate")).Post("/sepa/batches/{id}/status", h.SetBatchStatus)
	r.With(mw("sepa", "batch", "read")).Get("/sepa/batches/{id}/xml", h.ExportXML)
	r.With(mw("sepa", "batch", "write")).Post("/sepa/batches/{id}/rtransactions", h.RecordRTransaction)
	r.With(mw("sepa", "batch", "read")).Get("/sepa/batches/{id}/rtransactions", h.ListRTransactions)
	r.With(mw("sepa", "mandate", "write")).Post("/sepa/mandates", h.CreateMandate)
	r.With(mw("sepa", "mandate", "read")).Get("/sepa/mandates", h.ListMandates)
	r.With(mw("sepa", "mandate", "read")).Get("/sepa/mandates/{id}", h.MandateByID)
	r.With(mw("sepa", "mandate", "validate")).Post("/sepa/mandates/{id}/sign", h.SignMandate)
	r.With(mw("sepa", "mandate", "write")).Post("/sepa/mandates/{id}/amend", h.AmendMandate)
	r.With(mw("sepa", "mandate", "validate")).Post("/sepa/mandates/{id}/cancel", h.CancelMandate)
	r.With(mw("sepa", "transfer", "write")).Post("/sepa/transfers", h.CreateTransfer)
	r.With(mw("sepa", "transfer", "read")).Get("/sepa/transfers", h.ListTransfers)
	r.With(mw("sepa", "transfer", "read")).Get("/sepa/transfers/{id}", h.TransferByID)
	r.With(mw("sepa", "transfer", "validate")).Post("/sepa/transfers/{id}/status", h.SetTransferStatus)
	r.With(mw("sepa", "transfer", "read")).Get("/sepa/transfers/{id}/xml", h.ExportPain001)
}

// Handler implements the sepa HTTP surface.
type Handler struct {
	deps Deps
	svc  *Service
}

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

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateBatch opens a draft collection batch (IBANs validated).
func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var b Batch
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	b.ID = 0
	b.EntityID = entityID
	b.Status = BatchDraft
	if err := h.deps.Store.CreateBatch(r.Context(), h.deps.DB, &b); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

// ListBatches lists batches.
func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListBatches(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetBatchStatus moves a batch along its lifecycle.
func (h *Handler) SetBatchStatus(w http.ResponseWriter, r *http.Request) {
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
	b, err := h.deps.Store.SetBatchStatus(r.Context(), h.deps.DB, entityID, id, BatchStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.sepa.batch.status.v1", "batch", b.ID)
	writeJSON(w, http.StatusOK, b)
}

// ExportXML serves the pain.008 document for validated batches.
func (h *Handler) ExportXML(w http.ResponseWriter, r *http.Request) {
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
	b, err := h.deps.Store.BatchByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	raw, err := ExportXML(b, time.Now().UTC())
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", `attachment; filename="`+b.Ref+`.xml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// CreateMandate opens a draft mandate (IBAN mod-97 validated, UMR unique).
func (h *Handler) CreateMandate(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var m Mandate
	if err := decode(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m.ID = 0
	m.EntityID = entityID
	m.Status = MandateDraft
	if err := h.deps.Store.CreateMandate(r.Context(), h.deps.DB, &m); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// ListMandates lists mandates.
func (h *Handler) ListMandates(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListMandates(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// MandateByID fetches one mandate.
func (h *Handler) MandateByID(w http.ResponseWriter, r *http.Request) {
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
	m, err := h.deps.Store.MandateByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type signIn struct {
	SignedAt   time.Time `json:"signed_at"`
	RowVersion int64     `json:"row_version"`
}

// SignMandate moves a draft mandate to signed/active.
func (h *Handler) SignMandate(w http.ResponseWriter, r *http.Request) {
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
	var in signIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m, err := h.deps.Store.SignMandate(r.Context(), h.deps.DB, entityID, id, in.SignedAt, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.sepa.mandate.signed.v1", "mandate", m.ID)
	writeJSON(w, http.StatusOK, m)
}

type amendIn struct {
	DebtorName string `json:"debtor_name"`
	IBAN       string `json:"iban"`
	BIC        string `json:"bic"`
	RowVersion int64  `json:"row_version"`
}

// AmendMandate updates debtor details on a signed mandate (stays active).
func (h *Handler) AmendMandate(w http.ResponseWriter, r *http.Request) {
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
	var in amendIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m, err := h.deps.Store.AmendMandate(r.Context(), h.deps.DB, entityID, id, in.DebtorName, in.IBAN, in.BIC, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.sepa.mandate.amended.v1", "mandate", m.ID)
	writeJSON(w, http.StatusOK, m)
}

type versionIn struct {
	RowVersion int64 `json:"row_version"`
}

// CancelMandate cancels a draft or signed mandate.
func (h *Handler) CancelMandate(w http.ResponseWriter, r *http.Request) {
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
	var in versionIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m, err := h.deps.Store.CancelMandate(r.Context(), h.deps.DB, entityID, id, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.sepa.mandate.canceled.v1", "mandate", m.ID)
	writeJSON(w, http.StatusOK, m)
}

type rIn struct {
	EndToEndID        string `json:"end_to_end_id"`
	Kind              RKind  `json:"kind"`
	Reason            string `json:"reason"`
	JournalID         int64  `json:"journal_id"`
	ReceivableAccount int64  `json:"receivable_account"`
	BankAccount       int64  `json:"bank_account"`
}

// RecordRTransaction records an R-handling (reject/return/refund) against a
// collected batch item and posts its ledger reversal atomically.
func (h *Handler) RecordRTransaction(w http.ResponseWriter, r *http.Request) {
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
	var in rIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := h.svc.RecordR(r.Context(), RCmd{
		EntityID: entityID, BatchID: id, EndToEndID: in.EndToEndID,
		Kind: in.Kind, Reason: in.Reason, JournalID: in.JournalID,
		ReceivableAccount: in.ReceivableAccount, BankAccount: in.BankAccount,
	})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ListRTransactions lists R-handlings for a batch.
func (h *Handler) ListRTransactions(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.deps.Store.BatchByID(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	list, err := h.deps.Store.ListRTransactions(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateTransfer opens a draft outbound credit-transfer batch.
func (h *Handler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var t Transfer
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityID
	t.Status = TransferDraft
	if err := h.deps.Store.CreateTransfer(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// ListTransfers lists credit-transfer batches.
func (h *Handler) ListTransfers(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListTransfers(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// TransferByID fetches one credit-transfer batch.
func (h *Handler) TransferByID(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Store.TransferByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// SetTransferStatus moves a credit-transfer batch along its lifecycle.
func (h *Handler) SetTransferStatus(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Store.SetTransferStatus(r.Context(), h.deps.DB, entityID, id, TransferStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.sepa.transfer.status.v1", "transfer", t.ID)
	writeJSON(w, http.StatusOK, t)
}

// ExportPain001 serves the pain.001 document for validated transfers.
func (h *Handler) ExportPain001(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Store.TransferByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	raw, err := ExportPain001(t, time.Now().UTC())
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", `attachment; filename="`+t.Ref+`.xml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
