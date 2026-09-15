package assets

import (
	"net/http"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

type scheduleIn struct {
	Method    string    `json:"method"`
	Cost      int64     `json:"cost"`
	RateBps   int64     `json:"rate_bps"`
	StartDate time.Time `json:"start_date"`
	Periods   int       `json:"periods"`
}

// CreateSchedule opens a depreciation schedule for an asset (one per asset).
func (h *Handler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	aid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in scheduleIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sc := AssetSchedule{EntityID: entityID, AssetID: aid, Method: in.Method,
		Cost: in.Cost, RateBps: in.RateBps, StartDate: in.StartDate, Periods: in.Periods}
	if err := h.deps.Store.CreateSchedule(r.Context(), h.deps.DB, &sc); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.assets.schedule.created.v1", "asset_schedule", sc.ID)
	writeJSON(w, http.StatusCreated, sc)
}

// ListSchedules lists an asset's schedules with the generated plan preview.
func (h *Handler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	aid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.SchedulesOfAsset(r.Context(), h.deps.DB, entityID, aid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	type preview struct {
		AssetSchedule
		Plan []DepLine `json:"plan"`
	}
	out := make([]preview, 0, len(list))
	for _, sc := range list {
		plan, perr := BuildSchedule(sc.Cost, sc.Method, sc.RateBps, sc.Periods)
		if perr != nil {
			writeErr(w, http.StatusInternalServerError, "plan failed")
			return
		}
		out = append(out, preview{AssetSchedule: sc, Plan: plan})
	}
	writeJSON(w, http.StatusOK, out)
}

type postDepIn struct {
	JournalID      int64 `json:"journal_id"`
	ExpenseAccount int64 `json:"expense_account_id"`
	AccumAccount   int64 `json:"accum_account_id"`
	RowVersion     int64 `json:"row_version"`
}

// PostDepreciation posts one period's depreciation for a schedule.
func (h *Handler) PostDepreciation(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	sid, ok := pathID(r, "sid")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if h.deps.Finance == nil {
		writeErr(w, http.StatusServiceUnavailable, "assets: finance posting not wired")
		return
	}
	var in postDepIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.JournalID <= 0 || in.ExpenseAccount <= 0 || in.AccumAccount <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "assets: journal, expense and accumulated accounts required")
		return
	}
	res, err := h.dep.Post(r.Context(), PostDepCmd{EntityID: entityID, ScheduleID: sid,
		JournalID: in.JournalID, ExpenseAccount: in.ExpenseAccount,
		AccumAccount: in.AccumAccount, RowVersion: in.RowVersion})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type disposeIn struct {
	JournalID    int64 `json:"journal_id"`
	CashAccount  int64 `json:"cash_account_id"`
	CostAccount  int64 `json:"cost_account_id"`
	AccumAccount int64 `json:"accum_account_id"`
	GainAccount  int64 `json:"gain_account_id"`
	LossAccount  int64 `json:"loss_account_id"`
	Proceeds     int64 `json:"proceeds"`
	RowVersion   int64 `json:"row_version"`
}

// DisposeAsset sells/scraps an asset with a gain/loss entry and retires it.
func (h *Handler) DisposeAsset(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	aid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if h.deps.Finance == nil {
		writeErr(w, http.StatusServiceUnavailable, "assets: finance posting not wired")
		return
	}
	var in disposeIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.JournalID <= 0 || in.CostAccount <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "assets: journal and asset-cost accounts required")
		return
	}
	a, err := h.dep.Dispose(r.Context(), DisposeCmd{EntityID: entityID, AssetID: aid,
		JournalID: in.JournalID, CashAccount: in.CashAccount, CostAccount: in.CostAccount,
		AccumAccount: in.AccumAccount, GainAccount: in.GainAccount, LossAccount: in.LossAccount,
		Proceeds: in.Proceeds, RowVersion: in.RowVersion})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
