package booking

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

func window(day int, h1, h2 int) (time.Time, time.Time) {
	s := time.Date(2026, 9, day, h1, 0, 0, 0, time.UTC)
	e := time.Date(2026, 9, day, h2, 0, 0, 0, time.UTC)
	return s, e
}

func TestOverlapAndCapacity(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	r := &Resource{EntityID: 1, Code: "ROOM-A", Label: "Room A", Capacity: 4, Status: ResourceActive}
	if err := m.CreateResource(ctx, r); err != nil {
		t.Fatalf("resource: %v", err)
	}
	s1, e1 := window(10, 9, 11)
	b1 := &Booking{EntityID: 1, ResourceID: r.ID, UserLogin: "ada", StartAt: s1, EndAt: e1, Seats: 3}
	if err := m.CreateBooking(ctx, b1); err != nil {
		t.Fatalf("booking 1: %v", err)
	}
	// Overlapping 10-12 with 2 seats exceeds capacity 4 (3 used).
	s2, e2 := window(10, 10, 12)
	if err := m.CreateBooking(ctx, &Booking{EntityID: 1, ResourceID: r.ID,
		UserLogin: "bob", StartAt: s2, EndAt: e2, Seats: 2}); err == nil {
		t.Error("over-capacity accepted")
	}
	// Same window with 1 seat fits.
	if err := m.CreateBooking(ctx, &Booking{EntityID: 1, ResourceID: r.ID,
		UserLogin: "bob", StartAt: s2, EndAt: e2, Seats: 1}); err != nil {
		t.Fatalf("fitting booking: %v", err)
	}
	// Adjacent window (12-13) overlaps nothing.
	s3, e3 := window(10, 12, 13)
	if err := m.CreateBooking(ctx, &Booking{EntityID: 1, ResourceID: r.ID,
		UserLogin: "cid", StartAt: s3, EndAt: e3, Seats: 4}); err != nil {
		t.Fatalf("adjacent booking: %v", err)
	}
	// Cancel the first booking frees capacity for a 4-seat overlap.
	upd, err := m.SetBookingStatus(ctx, b1.ID, BookingCanceled, b1.RowVersion)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_ = upd
	// Zero-length window rejected.
	if err := m.CreateBooking(ctx, &Booking{EntityID: 1, ResourceID: r.ID,
		UserLogin: "x", StartAt: s1, EndAt: s1, Seats: 1}); err == nil {
		t.Error("zero-length accepted")
	}
	// Inactive resource rejects.
	r2 := &Resource{EntityID: 1, Code: "ROOM-B", Label: "B", Capacity: 2, Status: ResourceInactive}
	if err := m.CreateResource(ctx, r2); err != nil {
		t.Fatalf("resource B: %v", err)
	}
	if err := m.CreateBooking(ctx, &Booking{EntityID: 1, ResourceID: r2.ID,
		UserLogin: "x", StartAt: s1, EndAt: e1, Seats: 1}); err == nil {
		t.Error("inactive resource accepted")
	}
	// Illegal transition.
	if _, err := m.SetBookingStatus(ctx, b1.ID, BookingCompleted, upd.RowVersion); err == nil {
		t.Error("canceled→completed accepted")
	}
}

func TestBookingAPI(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	ctx := context.Background()
	res := &Resource{EntityID: 1, Code: "R1", Label: "R", Capacity: 1, Status: ResourceActive}
	if err := st.CreateResource(ctx, res); err != nil {
		t.Fatalf("resource: %v", err)
	}
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	s, e := window(11, 9, 10)
	rec := post("/api/v1/bookings", map[string]any{"resource_id": res.ID,
		"user_login": "ada", "start_at": s, "end_at": e, "seats": 1})
	if rec.Code != http.StatusCreated {
		t.Fatalf("book: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var b Booking
	_ = json.NewDecoder(rec.Body).Decode(&b)
	// Double-book same slot → 422.
	rec = post("/api/v1/bookings", map[string]any{"resource_id": res.ID,
		"user_login": "bob", "start_at": s, "end_at": e, "seats": 1})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("double-book: code=%d want 422", rec.Code)
	}
	// Check in then complete.
	rec = post(fmt.Sprintf("/api/v1/bookings/%d/status", b.ID),
		map[string]any{"status": 1, "row_version": b.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("checkin: code=%d", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&b)
	rec = post(fmt.Sprintf("/api/v1/bookings/%d/status", b.ID),
		map[string]any{"status": 2, "row_version": b.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: code=%d", rec.Code)
	}
}
