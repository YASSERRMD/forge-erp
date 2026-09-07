package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func passthrough(_ string, _ string, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() (http.Handler, *MemoryStore) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Store: st}, passthrough) })
	return r, st
}

func post(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPostAndTrialBalanceHTTP(t *testing.T) {
	h, _ := testRouter()
	ctx := context.Background()
	_ = ctx

	mk := func(code, label, typ string) int64 {
		rec := post(t, h, "/api/v1/finance/accounts", map[string]any{
			"code": code, "label": label, "type": typ})
		if rec.Code != http.StatusCreated {
			t.Fatalf("account %s: code=%d", code, rec.Code)
		}
		var a Account
		_ = json.NewDecoder(rec.Body).Decode(&a)
		return a.ID
	}
	cust, rev, vat := mk("411000", "Customers", "asset"), mk("707000", "Sales", "revenue"), mk("445700", "VAT", "liability")
	jrec := post(t, h, "/api/v1/finance/journals", map[string]any{"code": "VEN", "label": "Sales"})
	var j Journal
	_ = json.NewDecoder(jrec.Body).Decode(&j)

	// Balanced post → 201.
	ok := post(t, h, "/api/v1/finance/entries", map[string]any{
		"journal_id": j.ID, "ref": "VEN-1", "memo": "sale",
		"lines": []map[string]any{
			{"account_id": cust, "label": "c", "debit": 1200},
			{"account_id": rev, "label": "s", "credit": 1000},
			{"account_id": vat, "label": "v", "credit": 200},
		}})
	if ok.Code != http.StatusCreated {
		t.Fatalf("post: code=%d body=%s", ok.Code, ok.Body.String())
	}
	// Unbalanced → 422.
	bad := post(t, h, "/api/v1/finance/entries", map[string]any{
		"journal_id": j.ID, "ref": "VEN-2",
		"lines": []map[string]any{
			{"account_id": cust, "debit": 100},
			{"account_id": rev, "credit": 99},
		}})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbalanced: code=%d want 422", bad.Code)
	}
	// Trial balance zero-sums.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/finance/trial-balance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var tb struct {
		TotalDebit  int64 `json:"total_debit"`
		TotalCredit int64 `json:"total_credit"`
		Balanced    bool  `json:"balanced"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&tb)
	if !tb.Balanced || tb.TotalDebit != 1200 || tb.TotalCredit != 1200 {
		t.Fatalf("trial: %+v", tb)
	}
	// Chain verifies.
	creq := httptest.NewRequest(http.MethodGet, "/api/v1/finance/chain-verify?journal_id="+itoa(j.ID), nil)
	crec := httptest.NewRecorder()
	h.ServeHTTP(crec, creq)
	var cv struct {
		Valid   bool `json:"valid"`
		Entries int  `json:"entries"`
	}
	_ = json.NewDecoder(crec.Body).Decode(&cv)
	if !cv.Valid || cv.Entries != 1 {
		t.Fatalf("chain: %+v", cv)
	}
}

func TestPostInvoiceConsumer(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	cust := &Account{EntityID: 1, Code: "411", Label: "C", Type: "asset"}
	rev := &Account{EntityID: 1, Code: "707", Label: "R", Type: "revenue"}
	vat := &Account{EntityID: 1, Code: "4457", Label: "V", Type: "liability"}
	for _, a := range []*Account{cust, rev, vat} {
		_ = st.CreateAccount(ctx, a)
	}
	j := &Journal{EntityID: 1, Code: "VEN", Label: "V"}
	_ = st.CreateJournal(ctx, j)
	// Sales invoice INV gross 1200 (net 1000 + vat 200) auto-posts balanced.
	e, err := PostInvoice(ctx, st, 1, j.ID, "INV-1", time.Now().UTC(),
		cust.ID, rev.ID, vat.ID, 1000, 200, "auto", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != EntryPosted {
		t.Fatalf("status = %d", e.Status)
	}
	tb, _ := st.TrialBalance(ctx, 1)
	var dr, cr int64
	for _, s := range tb {
		dr += s[0]
		cr += s[1]
	}
	if dr != cr || dr == 0 {
		t.Fatalf("trial: dr=%d cr=%d", dr, cr)
	}
}

func TestListAccountsHTTP(t *testing.T) {
	h, _ := testRouter()
	rec := post(t, h, "/api/v1/finance/accounts",
		map[string]any{"code": "707000", "label": "Sales", "type": "revenue"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/finance/accounts", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d", rec.Code)
	}
	var list []Account
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].Code != "707000" {
		t.Fatalf("accounts=%+v", list)
	}
}
