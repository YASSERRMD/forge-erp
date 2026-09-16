package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
)

func passMW(module, entity, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func portalRouter(d Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, d, passMW) })
	return r
}

func staffReq(t *testing.T, r *chi.Mux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(platform.ContextWithEntity(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func custReq(r *chi.Mux, credential, method, path, body string) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func lines() []documents.Line {
	return []documents.Line{{ProductID: 1, Label: "widget", Qty: 2, UnitNet: 1000, VATRateBps: 2000}}
}

// fixture seeds two customers (org 100 / org 200) with invoices + quotes and
// returns the router, bearer credentials, and customer-A's invoice/quote/draft ids.
func fixture(t *testing.T) (*chi.Mux, string, string, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	salesMem := sales.NewMemoryStore()
	svcMem := services.NewMemoryStore()
	tokens := NewMemoryStore()
	d := Deps{Store: tokens, Sales: salesMem, Services: svcMem}
	r := portalRouter(d)
	svc := &Service{Store: tokens, Sales: salesMem, Services: svcMem}

	mkDoc := func(typ documents.DocType, org int64, status int16) int64 {
		doc := &sales.Document{EntityID: 1, Type: typ, OrgID: org,
			Currency: "USD", RateToBase: 1000000, Lines: lines()}
		if err := salesMem.CreateDoc(ctx, nil, doc, "202609"); err != nil {
			t.Fatal(err)
		}
		if status != 0 {
			upd, err := salesMem.SetStatus(ctx, nil, 1, doc.ID, status)
			if err != nil {
				t.Fatalf("status %d: %v", status, err)
			}
			*doc = upd
		}
		return doc.ID
	}
	invA := mkDoc(documents.TypeInvoice, 100, sales.InvoiceValidated)
	mkDoc(documents.TypeInvoice, 200, sales.InvoiceValidated)
	quoteA := mkDoc(documents.TypeProposal, 100, sales.ProposalValidated)
	draftA := mkDoc(documents.TypeProposal, 100, 0)
	mkDoc(documents.TypeProposal, 200, sales.ProposalValidated)

	// Other customer's ticket (must stay invisible to A).
	org200 := int64(200)
	other := &services.Ticket{EntityID: 1, OrgID: &org200, Ref: "TICK-OTHER",
		Subject: "other issue", Priority: 2}
	if err := svcMem.CreateTicket(ctx, nil, other); err != nil {
		t.Fatal(err)
	}

	_, credA, err := svc.MintToken(ctx, 1, 100, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, credB, err := svc.MintToken(ctx, 1, 200, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Raw secret must not be recoverable from storage (hash-only).
	for _, tok := range tokens.tokens {
		if strings.Contains(credA, tok.TokenHash) || strings.Contains(credB, tok.TokenHash) {
			t.Fatal("hash equals bearer material")
		}
	}
	_ = draftA
	return r, credA, credB, invA, quoteA, draftA
}

func TestPortalInvoicesIsolation(t *testing.T) {
	r, credA, _, invA, _, _ := fixture(t)

	rec := custReq(r, credA, http.MethodGet, "/api/v1/portal/invoices", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"org_id":200`) {
		t.Fatalf("cross-customer invoice leaked: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"org_id":100`) {
		t.Fatalf("own invoice missing: %s", rec.Body.String())
	}
	// Own invoice readable.
	rec = custReq(r, credA, http.MethodGet, "/api/v1/portal/invoices/"+itoa(invA), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get own: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Other customer's invoice → 404 (not 403: IDs undiscoverable).
	rec = custReq(r, credA, http.MethodGet, "/api/v1/portal/invoices/"+itoa(invA+1), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-customer get: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPortalQuotesAccept(t *testing.T) {
	r, credA, credB, _, quoteA, draftA := fixture(t)

	rec := custReq(r, credA, http.MethodGet, "/api/v1/portal/quotes", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"org_id":200`) {
		t.Fatalf("quotes isolation: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Accept own validated quote → signed.
	rec = custReq(r, credA, http.MethodPost, "/api/v1/portal/quotes/"+itoa(quoteA)+"/accept", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":2`) {
		t.Fatalf("accept: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Re-accept (now signed) → 422.
	rec = custReq(r, credA, http.MethodPost, "/api/v1/portal/quotes/"+itoa(quoteA)+"/accept", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-accept: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Other customer's quote → 404.
	rec = custReq(r, credB, http.MethodPost, "/api/v1/portal/quotes/"+itoa(quoteA)+"/accept", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-customer accept: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Draft quote cannot be accepted → 422.
	rec = custReq(r, credA, http.MethodPost, "/api/v1/portal/quotes/"+itoa(draftA)+"/accept", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft accept: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPortalTickets(t *testing.T) {
	r, credA, _, _, _, _ := fixture(t)

	rec := custReq(r, credA, http.MethodPost, "/api/v1/portal/tickets",
		`{"subject":"portal broken","priority":3}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"org_id":100`) {
		t.Fatalf("open: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = custReq(r, credA, http.MethodGet, "/api/v1/portal/tickets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "other issue") {
		t.Fatalf("cross-customer ticket leaked: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "portal broken") {
		t.Fatalf("own ticket missing: %s", rec.Body.String())
	}
	// Empty subject → 400.
	rec = custReq(r, credA, http.MethodPost, "/api/v1/portal/tickets", `{"subject":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty subject: code=%d", rec.Code)
	}
}

func TestPortalAuth(t *testing.T) {
	r, credA, _, _, _, _ := fixture(t)

	// No bearer → 401.
	if rec := custReq(r, "", http.MethodGet, "/api/v1/portal/invoices", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: code=%d", rec.Code)
	}
	// Garbage → 401.
	if rec := custReq(r, "bogus", http.MethodGet, "/api/v1/portal/invoices", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage: code=%d", rec.Code)
	}
	// Wrong secret, right salt → 401.
	salt, _ := SplitBearer(credA)
	if rec := custReq(r, salt+".deadbeef", http.MethodGet, "/api/v1/portal/invoices", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: code=%d", rec.Code)
	}
	// Staff mint endpoint needs the platform entity (RBAC context).
	rec := staffReq(t, r, http.MethodPost, "/api/v1/portal/tokens", `{"org_id":300}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("staff mint: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Staff mint without entity → 401.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/tokens", strings.NewReader(`{"org_id":300}`))
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("mint w/o entity: code=%d", rec2.Code)
	}
}

func TestTokenHashing(t *testing.T) {
	raw, err := MintToken()
	if err != nil || len(raw) != 64 {
		t.Fatalf("mint: %q %v", raw, err)
	}
	salt, err := MintSalt()
	if err != nil {
		t.Fatal(err)
	}
	row := PortalToken{EntityID: 1, OrgID: 5, Salt: salt,
		TokenHash: HashToken(salt, raw), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if !VerifyToken(row, raw) {
		t.Fatal("valid token rejected")
	}
	if VerifyToken(row, "wrong") {
		t.Fatal("wrong token accepted")
	}
	// Salts differentiate identical secrets.
	if HashToken("a", raw) == HashToken("b", raw) {
		t.Fatal("salt ignored")
	}
	// Expiry/revocation end life.
	now := time.Now().UTC()
	if !row.Live(now) {
		t.Fatal("live token reported dead")
	}
	expired := row
	expired.ExpiresAt = now.Add(-time.Second)
	if expired.Live(now) {
		t.Fatal("expired token reported live")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
