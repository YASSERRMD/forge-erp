package documentsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestShareRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	svc := &Service{Store: st, Storage: NewMemoryStorage()}
	d, err := svc.Upload(ctx, 1, "sales", 7, "inv.pdf", "application/pdf",
		bytes.NewReader([]byte("PDFDATA")), nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := MintToken()
	if err != nil || len(tok) != 64 {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	if err := st.CreateShare(ctx, &ShareToken{Token: tok, EntityID: 1,
		DocID: d.ID, ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatalf("share: %v", err)
	}
	got, err := st.ShareTarget(ctx, tok)
	if err != nil || got.DocID != d.ID {
		t.Fatalf("target=%+v err=%v", got, err)
	}
	expired := &ShareToken{Token: "old", EntityID: 1, DocID: d.ID,
		ExpiresAt: time.Now().UTC().Add(-time.Hour)}
	if err := st.CreateShare(ctx, expired); err != nil {
		t.Fatalf("expired share: %v", err)
	}
	if _, err := st.ShareTarget(ctx, "old"); err == nil {
		t.Error("expired token accepted")
	}
	// HTTP: share endpoint mints, public endpoint serves bytes.
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, svc, func(_, _, _ string) func(http.Handler) http.Handler {
			return func(next http.Handler) http.Handler { return next }
		})
	})
	r.Get("/public/share/{token}", PublicShare(svc))
	body, _ := json.Marshal(map[string]any{"expires_hours": 1})
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/share", d.ID),
		bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("share API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created ShareToken
	_ = json.NewDecoder(rec.Body).Decode(&created)
	req = httptest.NewRequest(http.MethodGet, "/public/share/"+created.Token, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "PDFDATA" {
		t.Fatalf("public: code=%d body=%q", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/public/share/nope", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bogus: code=%d want 404", rec.Code)
	}
}
