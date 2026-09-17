package label

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

func TestSheetCRUDMemory(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	sh := DefaultAvery65(1)
	if err := st.CreateSheet(ctx, nil, &sh); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sh.ID == 0 {
		t.Fatal("ID unset")
	}
	got, err := st.SheetByCode(ctx, nil, 1, "avery-65")
	if err != nil || got.LabelsPerSheet() != 65 {
		t.Fatalf("by-code=%+v err=%v", got, err)
	}
	if _, err := st.SheetByID(ctx, nil, 2, sh.ID); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-entity err=%v want ErrNotFound", err)
	}
	sh.Name = "Renamed"
	if err := st.UpdateSheet(ctx, nil, &sh); err != nil {
		t.Fatalf("update: %v", err)
	}
	stale := sh
	stale.RowVersion--
	stale.Name = "Stale"
	if err := st.UpdateSheet(ctx, nil, &stale); !errors.Is(err, platform.ErrVersionConflict) {
		t.Fatalf("stale err=%v want ErrVersionConflict", err)
	}
	if err := st.DeleteSheet(ctx, nil, 1, sh.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestRenderDeterministic(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	sh := DefaultAvery65(1)
	if err := st.CreateSheet(ctx, nil, &sh); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, Bus: platform.NewMemoryBus()}
	rows := []LabelRow{{ProductRef: "SKU-1", ProductLabel: "Widget", PriceMinor: 1999, Currency: "EUR"}}
	a, ct, err := svc.RenderPDF(ctx, 1, "avery-65", rows)
	if err != nil || ct != "application/pdf" {
		t.Fatalf("render: %v %q", err, ct)
	}
	if len(a) == 0 || string(a[:5]) != "%PDF-" {
		t.Fatal("not a PDF")
	}
	b, _, err := svc.RenderPDF(ctx, 1, "avery-65", rows)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("identical subjects render different bytes")
	}
	if _, _, err := svc.RenderPDF(ctx, 1, "avery-65", nil); err == nil {
		t.Fatal("empty rows accepted")
	}
}

func TestLabelRoutes(t *testing.T) {
	st := NewMemoryStore()
	svc := &Service{Store: st, Bus: platform.NewMemoryBus()}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Store: st, Svc: svc}, passthrough) })

	raw, _ := json.Marshal(DefaultAvery65(0))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/label-sheets", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/label-sheets/1/pdf",
		bytes.NewReader([]byte(`{"rows":[{"product_ref":"A"}]}`)))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("pdf API: code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
