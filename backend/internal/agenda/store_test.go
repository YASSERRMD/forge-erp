package agenda

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestReminderDueLogic(t *testing.T) {
	now := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	e := Event{Status: EventScheduled, ReminderMin: 30,
		StartAt: now.Add(20 * time.Minute), EndAt: now.Add(time.Hour)}
	if !e.ReminderDue(now) {
		t.Error("due reminder not detected")
	}
	e2 := Event{Status: EventScheduled, ReminderMin: 30,
		StartAt: now.Add(2 * time.Hour), EndAt: now.Add(3 * time.Hour)}
	if e2.ReminderDue(now) {
		t.Error("early reminder detected")
	}
	e3 := Event{Status: EventCanceled, ReminderMin: 30,
		StartAt: now.Add(time.Minute), EndAt: now.Add(time.Hour)}
	if e3.ReminderDue(now) {
		t.Error("canceled reminder detected")
	}
	if (Event{Status: EventDone}.CanTransition(EventScheduled)) {
		t.Error("done→scheduled accepted")
	}
}

func TestReminderDispatchOnce(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	e := &Event{EntityID: 1, Title: "Demo", OwnerLogin: "ada",
		StartAt: now.Add(10 * time.Minute), EndAt: now.Add(time.Hour), ReminderMin: 30}
	if err := m.CreateEvent(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	due, err := m.DueReminders(ctx, 1, now, 50)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%d err=%v", len(due), err)
	}
	if err := m.MarkReminded(ctx, e.ID); err != nil {
		t.Fatalf("mark: %v", err)
	}
	due, _ = m.DueReminders(ctx, 1, now, 50)
	if len(due) != 0 {
		t.Fatalf("re-notified: %d", len(due))
	}
	if err := m.MarkReminded(ctx, e.ID); err == nil {
		t.Error("double mark accepted")
	}
}

func TestAgendaAPI(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := do(http.MethodPost, "/api/v1/agenda/events", map[string]any{
		"title": "Kickoff", "owner_login": "ada",
		"start_at": now.Add(time.Hour), "end_at": now.Add(2 * time.Hour), "reminder_min": 15,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var e Event
	_ = json.NewDecoder(rec.Body).Decode(&e)
	// Bad window → 422.
	rec = do(http.MethodPost, "/api/v1/agenda/events", map[string]any{
		"title": "Bad", "owner_login": "ada",
		"start_at": now.Add(2 * time.Hour), "end_at": now.Add(time.Hour),
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad window: code=%d want 422", rec.Code)
	}
	// Dispatch endpoint drains the due reminder.
	rec = do(http.MethodPost, "/api/v1/agenda/reminders/dispatch", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dispatched":0`) {
		t.Fatalf("dispatch early: code=%d body=%s", rec.Code, rec.Body.String())
	}
}
