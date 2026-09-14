package sepa

import (
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/go-chi/chi/v5"
)

func isNotFound(err error) bool { return errors.Is(err, identity.ErrNotFound) }

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func TestIBANVectors(t *testing.T) {
	for _, good := range []string{
		"DE89370400440532013000",
		"FR1420041010050500013M02606",
		"GB29NWBK60161331926819",
		"de89 3704 0044 0532 0130 00",
	} {
		if err := CheckIBAN(good); err != nil {
			t.Errorf("valid IBAN %q rejected: %v", good, err)
		}
	}
	for _, bad := range []string{
		"DE89370400440532013001", // bad checksum
		"XX00",                   // too short
		"DE89!70400440532013000", // bad char
		"",
	} {
		if err := CheckIBAN(bad); err == nil {
			t.Errorf("bad IBAN %q accepted", bad)
		}
	}
	if err := CheckBIC("DEUTDEFF"); err != nil {
		t.Fatalf("BIC: %v", err)
	}
	if err := CheckBIC("BAD"); err == nil {
		t.Error("bad BIC accepted")
	}
}

func batchBody() map[string]any {
	return map[string]any{
		"ref": "SEPA-1", "creditor_name": "ForgeERP SARL",
		"creditor_iban": "FR1420041010050500013M02606", "creditor_bic": "AGRIFRPP",
		"creditor_id": "FR12ZZZ123456", "sequence": "RCUR",
		"requested_at": time.Now().UTC().Add(48 * time.Hour),
		"transactions": []map[string]any{{
			"debtor_name": "Client A", "iban": "DE89370400440532013000",
			"bic": "COBADEFFXXX", "amount": 2599,
			"remittance": "INV-1", "end_to_end_id": "E2E-1",
		}},
	}
}

func TestBatchXMLFlow(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/api/v1/sepa/batches", batchBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var b Batch
	_ = json.NewDecoder(rec.Body).Decode(&b)
	if b.Total() != 2599 {
		t.Fatalf("total=%d", b.Total())
	}
	// Draft export rejected.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sepa/batches/1/xml", nil)
	get := httptest.NewRecorder()
	// NOTE: id is dynamic; fetch it from the created batch.
	_ = get
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/sepa/batches/"+itoa(b.ID)+"/xml", nil)
	get = httptest.NewRecorder()
	r.ServeHTTP(get, req)
	if get.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft export: code=%d want 422", get.Code)
	}
	// Validate then export XML.
	rec = post("/api/v1/sepa/batches/"+itoa(b.ID)+"/status",
		map[string]any{"status": 1, "row_version": b.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: code=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sepa/batches/"+itoa(b.ID)+"/xml", nil)
	get = httptest.NewRecorder()
	r.ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("export: code=%d", get.Code)
	}
	xml := get.Body.String()
	for _, want := range []string{"pain.008.001.02", "SEPA-1", "25.99", "Client A",
		"DE89370400440532013000", "FR1420041010050500013M02606", "E2E-1"} {
		if !strings.Contains(xml, want) {
			t.Fatalf("xml missing %q", want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestPGBatchFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	b := &Batch{EntityID: 1, Ref: "PG-SEPA", CreditorName: "C",
		CreditorIBAN: "FR1420041010050500013M02606", CreditorBIC: "AGRIFRPP",
		CreditorID: "ID", Sequence: "RCUR", RequestedAt: time.Now().UTC().Add(24 * time.Hour),
		Transactions: []Transaction{{DebtorName: "D", IBAN: "DE89370400440532013000",
			Amount: 100, Remittance: "R", EndToEndID: "PG-E2E"}}}
	if err := st.CreateBatch(ctx, pool, b); err != nil {
		t.Fatalf("batch: %v", err)
	}
	upd, err := st.SetBatchStatus(ctx, pool, 1, b.ID, BatchValidated, b.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	_ = upd
	list, err := st.ListBatches(ctx, pool, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	b := &Batch{EntityID: 1, Ref: "SEPA-X", CreditorName: "C",
		CreditorIBAN: "FR1420041010050500013M02606", CreditorBIC: "AGRIFRPP",
		CreditorID: "ID", Sequence: "RCUR", RequestedAt: time.Now().UTC().Add(24 * time.Hour),
		Transactions: []Transaction{{DebtorName: "D", IBAN: "DE89370400440532013000",
			Amount: 100, Remittance: "R", EndToEndID: "X-E2E"}}}
	if err := m.CreateBatch(ctx, nil, b); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.BatchByID(ctx, nil, 2, b.ID); !isNotFound(err) {
		t.Fatalf("cross-tenant BatchByID err=%v want not-found", err)
	}
	if _, err := m.SetBatchStatus(ctx, nil, 2, b.ID, BatchValidated, b.RowVersion); !isNotFound(err) {
		t.Fatalf("cross-tenant SetBatchStatus err=%v want not-found", err)
	}
}
