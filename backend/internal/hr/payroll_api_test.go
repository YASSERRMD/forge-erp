package hr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/go-chi/chi/v5"
)

// TestPayrollRunAPI exercises the payroll routes end to end on memory stores:
// create run → add line → post (balanced ledger entry via finance).
func TestPayrollRunAPI(t *testing.T) {
	fstore := finance.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Finance: fstore}, passthrough)
	})
	ctx := context.Background()
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "641000", Label: "Salaries", Type: "expense"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
		{EntityID: 1, Code: "431000", Label: "Payroll payable", Type: "liability"},
	} {
		a := a
		if err := fstore.CreateAccount(ctx, nil, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "PAY", Label: "Payroll"}
	if err := fstore.CreateJournal(ctx, nil, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	accts, _ := fstore.Accounts(ctx, nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}

	rec := doReq(t, r, http.MethodPost, "/api/v1/hr/payroll/runs", map[string]any{
		"label": "SEP-2026", "period_start": "2026-09-01T00:00:00Z", "period_end": "2026-09-30T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create run: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var run PayrollRun
	_ = json.NewDecoder(rec.Body).Decode(&run)

	// Unbalanced line → 422 (amounts recorded, never computed).
	rec = doReq(t, r, http.MethodPost, fmt.Sprintf("/api/v1/hr/payroll/runs/%d/lines", run.ID),
		map[string]any{"gross": 1000, "charges": 200, "net": 700})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbalanced line: code=%d want 422", rec.Code)
	}
	rec = doReq(t, r, http.MethodPost, fmt.Sprintf("/api/v1/hr/payroll/runs/%d/lines", run.ID),
		map[string]any{"gross": 500000, "charges": 110000, "net": 390000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, r, http.MethodPost, fmt.Sprintf("/api/v1/hr/payroll/runs/%d/post", run.ID),
		map[string]any{"journal_id": j.ID, "expense_account_id": byCode["641000"],
			"bank_account_id": byCode["512000"], "payable_account_id": byCode["431000"],
			"row_version": run.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("post: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var posted PayrollRun
	_ = json.NewDecoder(rec.Body).Decode(&posted)
	if posted.Status != PayrollRunPosted {
		t.Fatalf("status=%d want posted", posted.Status)
	}
	tb, _ := fstore.TrialBalance(ctx, nil, 1)
	if tb[byCode["641000"]] != [2]int64{500000, 0} || tb[byCode["512000"]] != [2]int64{0, 390000} ||
		tb[byCode["431000"]] != [2]int64{0, 110000} {
		t.Fatalf("trial=%v", tb)
	}
}
