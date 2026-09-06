package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	h := Router(BuildInfo{Version: "test", Commit: "abc"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["version"] != "test" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestReadyzDegraded(t *testing.T) {
	h := HealthHandler(BuildInfo{}, true, func() error { return errors.New("db down") })
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz degraded = %d, want 503", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := Router(BuildInfo{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing X-Content-Type-Options")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing X-Frame-Options")
	}
}

func TestMemoryBus(t *testing.T) {
	bus := NewMemoryBus()
	var got []string
	unsub := bus.Subscribe("forgeerp.test.happened.v1", func(_ context.Context, e Event) {
		got = append(got, e.Entity)
	})
	_ = bus.Publish(context.Background(), Event{Subject: "forgeerp.test.happened.v1", Entity: "thing"})
	_ = bus.Publish(context.Background(), Event{Subject: "forgeerp.other.v1", Entity: "noise"})
	unsub()
	_ = bus.Publish(context.Background(), Event{Subject: "forgeerp.test.happened.v1", Entity: "late"})
	if len(got) != 1 || got[0] != "thing" {
		t.Fatalf("bus delivery = %v, want [thing]", got)
	}
}
