package dynprice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func testSvc() *Service {
	return NewService(NewMemoryStore(), platform.NewMemoryBus(), nil)
}

func TestRuleCRUDAndPriceForProduct(t *testing.T) {
	ctx := context.Background()
	svc := testSvc()
	r := &Rule{EntityID: 1, Code: "ten-off", Label: "10% off", Status: RuleActive,
		Expression: "max(base * 0.9, cost)"}
	if err := svc.CreateRule(ctx, r); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Org-specific assignment wins over the org-0 fallback.
	fallback := &Assignment{EntityID: 1, RuleID: r.ID, ProductID: 7, OrgID: 0}
	if err := svc.Store.Assign(ctx, nil, fallback); err != nil {
		t.Fatalf("assign fallback: %v", err)
	}
	// No assignment at all → passthrough, ok=false.
	if price, ok, err := svc.PriceForProduct(ctx, 1, 9, 3, Input{Base: 500}); err != nil || ok || price != 500 {
		t.Fatalf("passthrough=%d ok=%v err=%v want 500/false/nil", price, ok, err)
	}
	price, ok, err := svc.PriceForProduct(ctx, 1, 7, 3, Input{Base: 1000, Cost: 700})
	if err != nil || !ok || price != 900 {
		t.Fatalf("price=%d ok=%v err=%v want 900/true/nil", price, ok, err)
	}
	// Archived rules are skipped in PriceForProduct and 422 in EvaluateRule.
	r.Status = RuleArchived
	if err := svc.Store.UpdateRule(ctx, nil, r); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := svc.EvaluateRule(ctx, 1, r.ID, Input{Base: 1}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("evaluate archived err=%v want ErrValidation", err)
	}
	if _, ok, _ := svc.PriceForProduct(ctx, 1, 7, 3, Input{Base: 1000}); ok {
		t.Fatal("archived rule still prices")
	}
	// Cross-entity reads 404.
	if _, err := svc.Store.RuleByID(ctx, nil, 2, r.ID); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-entity err=%v want ErrNotFound", err)
	}
}

func TestRuleRoutes(t *testing.T) {
	svc := testSvc()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(map[string]any{"code": "half", "expression": "base / 2"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/price-rules", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var rule Rule
	_ = json.NewDecoder(rec.Body).Decode(&rule)

	raw, _ = json.Marshal(Input{Base: 1001})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/price-rules/1/evaluate", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out struct {
		Price int64 `json:"price"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if rec.Code != http.StatusOK || out.Price != 501 {
		t.Fatalf("evaluate API: code=%d price=%d want 200/501", rec.Code, out.Price)
	}
	// Unparseable expression is 422, never stored.
	raw, _ = json.Marshal(map[string]any{"code": "bad", "expression": "base +"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/price-rules", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad expression API: code=%d want 422", rec.Code)
	}
}
