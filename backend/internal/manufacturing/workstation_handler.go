package manufacturing

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// CreateWorkstation registers a production resource with a daily capacity.
func (h *Handler) CreateWorkstation(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var ws Workstation
	if err := decode(r, &ws); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	ws.ID = 0
	ws.EntityID = entityID
	ws.Status = WorkstationActive
	if err := h.deps.Store.CreateWorkstation(r.Context(), h.deps.DB, &ws); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.manufacturing.workstation.created.v1", "workstation", ws.ID)
	writeJSON(w, http.StatusCreated, ws)
}

// ListWorkstations pages workstations within the caller's entity.
func (h *Handler) ListWorkstations(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListWorkstations(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetWorkstation fetches one workstation.
func (h *Handler) GetWorkstation(w http.ResponseWriter, r *http.Request) {
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
	ws, err := h.deps.Store.WorkstationByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

type bomOperationIn struct {
	Seq               int32 `json:"seq"` // 0 = append after the current max
	WorkstationID     int64 `json:"workstation_id"`
	RunMinutesPerUnit int64 `json:"run_minutes_per_unit"`
	SetupMinutes      int64 `json:"setup_minutes"`
}

// AddBOMOperation appends a routing step to a BOM (seq auto-assigned when 0).
func (h *Handler) AddBOMOperation(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	bid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in bomOperationIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	o := BOMOperation{EntityID: entityID, BOMID: bid, Seq: in.Seq,
		WorkstationID: in.WorkstationID, RunMinutesPerUnit: in.RunMinutesPerUnit, SetupMinutes: in.SetupMinutes}
	if err := h.deps.Store.AddBOMOperation(r.Context(), h.deps.DB, &o); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

// ListBOMOperations lists a BOM's routing steps in seq order.
func (h *Handler) ListBOMOperations(w http.ResponseWriter, r *http.Request) {
	bid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.BOMOperations(r.Context(), h.deps.DB, bid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type scheduleIn struct {
	Start string `json:"start"` // YYYY-MM-DD, default today UTC
}

// ScheduleMO forward-schedules the MO's routing x order qty, replacing any
// pending schedule. Overload is flagged on operations, not resolved.
func (h *Handler) ScheduleMO(w http.ResponseWriter, r *http.Request) {
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
	var in scheduleIn
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
	}
	var start time.Time
	if in.Start != "" {
		var err error
		start, err = time.Parse("2006-01-02", in.Start)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad start date")
			return
		}
	}
	ops, err := h.svc.Schedule(r.Context(), ScheduleCmd{EntityID: entityID, MOID: id, Start: start})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ops)
}

// ListMOOperations lists an MO's scheduled operations in seq order.
func (h *Handler) ListMOOperations(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.MOOperations(r.Context(), h.deps.DB, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type completeOperationIn struct {
	ActualMinutes int64 `json:"actual_minutes"`
}

// CompleteMOOperation completes one scheduled step in seq order: it records
// actual minutes, posts the step's consume share, and on the final step posts
// the receipt and flips the MO to produced. 422 on insufficient stock.
func (h *Handler) CompleteMOOperation(w http.ResponseWriter, r *http.Request) {
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
	seq, err := strconv.ParseInt(chi.URLParam(r, "seq"), 10, 32)
	if err != nil || seq <= 0 {
		writeErr(w, http.StatusBadRequest, "bad seq")
		return
	}
	var in completeOperationIn
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
	}
	res, err := h.svc.CompleteOperation(r.Context(), CompleteOperationCmd{
		EntityID: entityID, MOID: id, Seq: int32(seq), ActualMinutes: in.ActualMinutes})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// Capacity reports per-day load vs capacity for one workstation (or all of
// the entity's when workstation_id is 0) over [from, to] inclusive
// (YYYY-MM-DD; defaults to today + 13 days, max 93 days).
func (h *Handler) Capacity(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	q := r.URL.Query()
	var wsID int64
	if s := q.Get("workstation_id"); s != "" {
		var err error
		wsID, err = strconv.ParseInt(s, 10, 64)
		if err != nil || wsID < 0 {
			writeErr(w, http.StatusBadRequest, "bad workstation_id")
			return
		}
	}
	today := time.Now().UTC()
	from := midnightUTC(today)
	to := from.AddDate(0, 0, 13)
	if s := q.Get("from"); s != "" {
		var err error
		from, err = time.Parse("2006-01-02", s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad from date")
			return
		}
	}
	if s := q.Get("to"); s != "" {
		var err error
		to, err = time.Parse("2006-01-02", s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad to date")
			return
		}
	}
	from = midnightUTC(from)
	to = midnightUTC(to)
	if to.Before(from) || to.Sub(from).Hours()/24 > 93 {
		writeErr(w, http.StatusBadRequest, "bad range")
		return
	}
	ctx := r.Context()
	var stations []Workstation
	if wsID != 0 {
		ws, err := h.deps.Store.WorkstationByID(ctx, h.deps.DB, entityID, wsID)
		if err != nil {
			platform.WriteError(w, err)
			return
		}
		stations = []Workstation{ws}
	} else {
		var err error
		stations, err = h.deps.Store.ListWorkstations(ctx, h.deps.DB, entityID, 200, 0)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "list failed")
			return
		}
	}
	var out []CapacityDay
	for _, ws := range stations {
		ops, err := h.deps.Store.OperationsByWorkstation(ctx, h.deps.DB, entityID, ws.ID, from, to)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "load failed")
			return
		}
		out = append(out, CapacityView(ops, ws.ID, ws.DailyCapacityMin, from, to)...)
	}
	writeJSON(w, http.StatusOK, out)
}
