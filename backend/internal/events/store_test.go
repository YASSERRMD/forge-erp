package events

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

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestEventCapacityFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	e := &OrgEvent{EntityID: 1, Title: "Conf", StartsAt: now.Add(24 * time.Hour),
		EndsAt: now.Add(48 * time.Hour), Capacity: 2}
	if err := m.CreateEvent(ctx, e); err != nil {
		t.Fatalf("event: %v", err)
	}
	// Draft events reject registration.
	if err := m.Register(ctx, &Registration{EntityID: 1, EventID: e.ID, Name: "a"}); err == nil {
		t.Error("draft registration accepted")
	}
	upd, err := m.SetEventStatus(ctx, e.ID, OrgEventPublished, e.RowVersion)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = upd
	for _, n := range []string{"a", "b"} {
		if err := m.Register(ctx, &Registration{EntityID: 1, EventID: e.ID, Name: n}); err != nil {
			t.Fatalf("register %s: %v", n, err)
		}
	}
	if err := m.Register(ctx, &Registration{EntityID: 1, EventID: e.ID, Name: "c"}); err == nil {
		t.Error("over-capacity accepted")
	}
}

func TestHiringPipeline(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	p := &Position{EntityID: 1, Code: "DEV-1", Title: "Go dev"}
	if err := m.CreatePosition(ctx, p); err != nil {
		t.Fatalf("position: %v", err)
	}
	if err := m.Apply(ctx, &Application{EntityID: 1, PositionID: p.ID, Name: "x"}); err == nil {
		t.Error("application on draft accepted")
	}
	upd, err := m.SetPositionStatus(ctx, p.ID, PositionOpen, p.RowVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = upd
	a := &Application{EntityID: 1, PositionID: p.ID, Name: "Ada"}
	if err := m.Apply(ctx, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// received → interview skips screening → rejected.
	if _, err := m.SetApplicationStatus(ctx, a.ID, AppInterview, a.RowVersion); err == nil {
		t.Error("stage skip accepted")
	}
	cur := *a
	for _, st := range []ApplicationStatus{AppScreening, AppInterview, AppOffer, AppHired} {
		nx, err := m.SetApplicationStatus(ctx, a.ID, st, cur.RowVersion)
		if err != nil {
			t.Fatalf("stage %d: %v", st, err)
		}
		cur = nx
	}
}

func TestEventsAPI(t *testing.T) {
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
	rec := post("/api/v1/positions", map[string]any{"code": "D1", "title": "Dev"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("position: code=%d", rec.Code)
	}
	var p Position
	_ = json.NewDecoder(rec.Body).Decode(&p)
	rec = post(fmt.Sprintf("/api/v1/positions/%d/status", p.ID),
		map[string]any{"status": 1, "row_version": p.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("open: code=%d", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&p)
	rec = post(fmt.Sprintf("/api/v1/positions/%d/applications", p.ID),
		map[string]any{"name": "Grace"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var a Application
	_ = json.NewDecoder(rec.Body).Decode(&a)
	rec = post(fmt.Sprintf("/api/v1/applications/%d/status", a.ID),
		map[string]any{"status": -1, "row_version": a.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("reject: code=%d", rec.Code)
	}
}
