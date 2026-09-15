package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func depPassthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func depDoReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestDepreciationAPI exercises the schedule/disposal routes end to end on
// memory stores: create schedule → post one period → dispose with gain.
func TestDepreciationAPI(t *testing.T) {
	ctx := context.Background()
	fstore := finance.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Finance: fstore}, depPassthrough)
	})
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "681000", Label: "Depreciation", Type: "expense"},
		{EntityID: 1, Code: "281000", Label: "Accum. depreciation", Type: "asset"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
		{EntityID: 1, Code: "215000", Label: "Equipment cost", Type: "asset"},
		{EntityID: 1, Code: "775000", Label: "Disposal gains", Type: "revenue"},
		{EntityID: 1, Code: "675000", Label: "Disposal losses", Type: "expense"},
	} {
		a := a
		if err := fstore.CreateAccount(ctx, nil, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "OD", Label: "Operations"}
	if err := fstore.CreateJournal(ctx, nil, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	accts, _ := fstore.Accounts(ctx, nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}

	rec := depDoReq(t, r, http.MethodPost, "/api/v1/assets",
		map[string]any{"code": "API-1", "label": "API press", "kind": "equipment"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create asset: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var a Asset
	_ = json.NewDecoder(rec.Body).Decode(&a)

	rec = depDoReq(t, r, http.MethodPost, fmt.Sprintf("/api/v1/assets/%d/schedules", a.ID),
		map[string]any{"method": "linear", "cost": 6000,
			"start_date": "2026-01-01T00:00:00Z", "periods": 3})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create schedule: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sc AssetSchedule
	_ = json.NewDecoder(rec.Body).Decode(&sc)

	rec = depDoReq(t, r, http.MethodPost, fmt.Sprintf("/api/v1/assets/schedules/%d/post", sc.ID),
		map[string]any{"journal_id": j.ID, "expense_account_id": byCode["681000"],
			"accum_account_id": byCode["281000"], "row_version": sc.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("post: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var pres PostResult
	_ = json.NewDecoder(rec.Body).Decode(&pres)
	if pres.Amount != 2000 {
		t.Fatalf("amount=%d want 2000", pres.Amount)
	}

	// NBV = 4000; proceeds 5000 → gain 1000, asset retired.
	rec = depDoReq(t, r, http.MethodPost, fmt.Sprintf("/api/v1/assets/%d/dispose", a.ID),
		map[string]any{"journal_id": j.ID, "cash_account_id": byCode["512000"],
			"cost_account_id": byCode["215000"], "accum_account_id": byCode["281000"],
			"gain_account_id": byCode["775000"], "loss_account_id": byCode["675000"],
			"proceeds": 5000, "row_version": a.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("dispose: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var ret Asset
	_ = json.NewDecoder(rec.Body).Decode(&ret)
	if ret.Status != AssetRetired {
		t.Fatalf("status=%d want retired", ret.Status)
	}
	tb, _ := fstore.TrialBalance(ctx, nil, 1)
	if tb[byCode["775000"]] != [2]int64{0, 1000} {
		t.Fatalf("gain=%v want credit 1000", tb[byCode["775000"]])
	}
}
