package events

import (
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func TestEventCapacityFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	e := &OrgEvent{EntityID: 1, Title: "Conf", StartsAt: now.Add(24 * time.Hour),
		EndsAt: now.Add(48 * time.Hour), Capacity: 2}
	if err := m.CreateEvent(ctx, nil, e); err != nil {
		t.Fatalf("event: %v", err)
	}
	// Draft events reject registration.
	if err := m.Register(ctx, nil, &Registration{EntityID: 1, EventID: e.ID, Name: "a"}); err == nil {
		t.Error("draft registration accepted")
	}
	upd, err := m.SetEventStatus(ctx, nil, 1, e.ID, OrgEventPublished, e.RowVersion)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = upd
	for _, n := range []string{"a", "b"} {
		if err := m.Register(ctx, nil, &Registration{EntityID: 1, EventID: e.ID, Name: n}); err != nil {
			t.Fatalf("register %s: %v", n, err)
		}
	}
	if err := m.Register(ctx, nil, &Registration{EntityID: 1, EventID: e.ID, Name: "c"}); err == nil {
		t.Error("over-capacity accepted")
	}
}

func TestHiringPipeline(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	p := &Position{EntityID: 1, Code: "DEV-1", Title: "Go dev"}
	if err := m.CreatePosition(ctx, nil, p); err != nil {
		t.Fatalf("position: %v", err)
	}
	if err := m.Apply(ctx, nil, &Application{EntityID: 1, PositionID: p.ID, Name: "x"}); err == nil {
		t.Error("application on draft accepted")
	}
	upd, err := m.SetPositionStatus(ctx, nil, 1, p.ID, PositionOpen, p.RowVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = upd
	a := &Application{EntityID: 1, PositionID: p.ID, Name: "Ada"}
	if err := m.Apply(ctx, nil, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// received → interview skips screening → rejected.
	if _, err := m.SetApplicationStatus(ctx, nil, 1, a.ID, AppInterview, a.RowVersion); err == nil {
		t.Error("stage skip accepted")
	}
	cur := *a
	for _, st := range []ApplicationStatus{AppScreening, AppInterview, AppOffer, AppHired} {
		nx, err := m.SetApplicationStatus(ctx, nil, 1, a.ID, st, cur.RowVersion)
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

func TestPGEventHiring(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	e := &OrgEvent{EntityID: 1, Title: "PG Conf", StartsAt: now.Add(24 * time.Hour),
		EndsAt: now.Add(48 * time.Hour), Capacity: 1}
	if err := st.CreateEvent(ctx, pool, e); err != nil {
		t.Fatalf("event: %v", err)
	}
	upd, err := st.SetEventStatus(ctx, pool, 1, e.ID, OrgEventPublished, e.RowVersion)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = upd
	if err := st.Register(ctx, pool, &Registration{EntityID: 1, EventID: e.ID, Name: "a"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := st.Register(ctx, pool, &Registration{EntityID: 1, EventID: e.ID, Name: "b"}); err == nil {
		t.Error("over-capacity accepted on PG")
	}
	p := &Position{EntityID: 1, Code: "PG-D", Title: "Dev"}
	if err := st.CreatePosition(ctx, pool, p); err != nil {
		t.Fatalf("position: %v", err)
	}
}

func TestCrossTenantEventIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	e := &OrgEvent{EntityID: 1, Title: "Tenant A Conf", StartsAt: now.Add(24 * time.Hour),
		EndsAt: now.Add(48 * time.Hour), Capacity: 10}
	if err := m.CreateEvent(ctx, nil, e); err != nil {
		t.Fatalf("event: %v", err)
	}
	// ByID under entity B must miss.
	if _, err := m.EventByID(ctx, nil, 2, e.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant EventByID should be not-found, got %v", err)
	}
	// Status mutation under entity B must miss.
	if _, err := m.SetEventStatus(ctx, nil, 2, e.ID, OrgEventPublished, e.RowVersion); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant SetEventStatus should be not-found, got %v", err)
	}
	// Own-tenant lookup still works.
	if _, err := m.EventByID(ctx, nil, 1, e.ID); err != nil {
		t.Fatalf("own-tenant EventByID failed: %v", err)
	}
	// Position isolation.
	p := &Position{EntityID: 1, Code: "X-1", Title: "Dev"}
	if err := m.CreatePosition(ctx, nil, p); err != nil {
		t.Fatalf("position: %v", err)
	}
	if _, err := m.SetPositionStatus(ctx, nil, 2, p.ID, PositionOpen, p.RowVersion); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant SetPositionStatus should be not-found, got %v", err)
	}
}
