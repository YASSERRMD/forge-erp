package sepa

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
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
