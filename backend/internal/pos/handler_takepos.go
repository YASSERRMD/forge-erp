package pos

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/docgen"
)

func registryOf(h *Handler) *docgen.Registry {
	if h.deps.DocModels != nil {
		return h.deps.DocModels
	}
	return docgen.DefaultRegistry()
}

// Receipt renders a completed sale through the Kernel-5 registry:
// GET /pos/sales/{id}/receipt?format=text|pdf&locale=&model=. The template
// code comes from ?model= when given, else "pos_receipt". Plain text streams
// as text/plain (same builder as the ESC/POS feed); PDF goes through the
// registered DocModel as an attachment.
func (h *Handler) Receipt(w http.ResponseWriter, r *http.Request) {
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
	sub, err := h.svc.ReceiptData(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" || format == "text" {
		body := ReceiptText(sub)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sub.Ref+".txt"))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
		return
	}
	if format != "pdf" {
		writeErr(w, http.StatusBadRequest, "format must be text or pdf")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("model"))
	if code == "" {
		code = ReceiptCode
	}
	m, err := registryOf(h).Lookup(code)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if !m.Applies(ReceiptCode) {
		writeErr(w, http.StatusUnprocessableEntity, "document model does not apply to "+ReceiptCode)
		return
	}
	locale := strings.TrimSpace(r.URL.Query().Get("locale"))
	if locale == "" {
		locale = "en"
	}
	rd, contentType, err := m.Render(r.Context(), sub, locale)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	body, err := io.ReadAll(rd)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sub.Ref+".pdf"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ESCPos downloads the receipt as ESC/POS printer bytes (no hardware calls):
// GET /pos/sales/{id}/escpos?kick=1&cut=full|partial. The client forwards the
// bytes to its own spooler.
func (h *Handler) ESCPos(w http.ResponseWriter, r *http.Request) {
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
	sub, err := h.svc.ReceiptData(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	q := r.URL.Query()
	kick := true
	if v := strings.TrimSpace(q.Get("kick")); v == "0" || strings.EqualFold(v, "false") {
		kick = false
	}
	full := true
	if v := strings.ToLower(strings.TrimSpace(q.Get("cut"))); v == "partial" {
		full = false
	} else if v != "" && v != "full" {
		writeErr(w, http.StatusBadRequest, "cut must be full or partial")
		return
	}
	job := ESCPosJob(ReceiptText(sub), kick, full)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sub.Ref+".escpos.bin"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(job)
}

type enqueueIn struct {
	SessionID      int64      `json:"session_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	OrgID          int64      `json:"org_id"`
	Lines          []SaleLine `json:"lines"`
	Method         string     `json:"method"`
	Tendered       int64      `json:"tendered"`
	Payments       []Tender   `json:"payments"`
}

// EnqueueOffline stores an offline till payload (idempotent on key).
func (h *Handler) EnqueueOffline(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in enqueueIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	q := &QueuedSale{EntityID: entityID, SessionID: in.SessionID,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey), OrgID: in.OrgID,
		Lines: in.Lines, Method: in.Method, Tendered: in.Tendered, Payments: in.Payments}
	if err := h.deps.Store.EnqueueOffline(r.Context(), h.deps.DB, q); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

// ListQueue lists a session's offline payloads (?session_id= required,
// ?status= queued|sent|failed optional).
func (h *Handler) ListQueue(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	sessionID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("session_id")), 10, 64)
	if err != nil || sessionID <= 0 {
		writeErr(w, http.StatusBadRequest, "session_id required")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "", string(QueueQueued), string(QueueSent), string(QueueFailed):
	default:
		writeErr(w, http.StatusBadRequest, "bad status")
		return
	}
	list, err := h.deps.Store.QueueList(r.Context(), h.deps.DB, entityID, sessionID, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	if list == nil {
		list = []QueuedSale{}
	}
	writeJSON(w, http.StatusOK, list)
}

// ReplayQueue drains a session's queued payloads through the checkout
// service (idempotent: double replay is a no-op).
func (h *Handler) ReplayQueue(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in struct {
		SessionID int64 `json:"session_id"`
	}
	if err := decode(r, &in); err != nil || in.SessionID <= 0 {
		writeErr(w, http.StatusBadRequest, "session_id required")
		return
	}
	out, err := h.svc.ReplayQueue(r.Context(), entityID, in.SessionID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if out.SaleIDs == nil {
		out.SaleIDs = []int64{}
	}
	if out.QueueIDs == nil {
		out.QueueIDs = []int64{}
	}
	if out.Failed == nil {
		out.Failed = []QueueFailure{}
	}
	writeJSON(w, http.StatusOK, out)
}

type payoutIn struct {
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

// RecordPayout books cash out of the drawer's session.
func (h *Handler) RecordPayout(w http.ResponseWriter, r *http.Request) {
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
	var in payoutIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if _, err := h.deps.Store.SessionByID(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	p := &Payout{EntityID: entityID, SessionID: id, Amount: in.Amount, Reason: in.Reason}
	if err := h.deps.Store.RecordPayout(r.Context(), h.deps.DB, p); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

type countIn struct {
	CountedCash int64 `json:"counted_cash"`
}

// RecordCount snapshots the drawer (counted vs expected → variance).
func (h *Handler) RecordCount(w http.ResponseWriter, r *http.Request) {
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
	var in countIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c, err := h.svc.RecordCashCount(r.Context(), entityID, id, in.CountedCash)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// XReport snapshots a session mid-shift (no reset, till keeps selling).
func (h *Handler) XReport(w http.ResponseWriter, r *http.Request) {
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
	rep, err := h.svc.XReport(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// ZReport closes the session and returns the closing report (the close is
// the counter reset — a closed session rejects further checkouts).
func (h *Handler) ZReport(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		RowVersion int64 `json:"row_version"`
	}
	if err := decode(r, &in); err != nil || in.RowVersion <= 0 {
		writeErr(w, http.StatusBadRequest, "row_version required")
		return
	}
	rep, err := h.svc.ZReport(r.Context(), entityID, id, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
