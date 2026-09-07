package inbound

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestGatewayFlow(t *testing.T) {
	tickets := services.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Tickets: tickets}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/api/v1/inbound/mailboxes", map[string]any{
		"code": "support", "host": "mail.example.com", "port": 993, "active": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mailbox: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Unknown mailbox → 404.
	rec = post("/api/v1/inbound/messages", map[string]any{
		"mailbox": "nope", "from": "a@b.c", "subject": "hi", "body": "x",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown mailbox: code=%d want 404", rec.Code)
	}
	// Known mailbox → ticket + message, fetch stamped.
	rec = post("/api/v1/inbound/messages", map[string]any{
		"mailbox": "support", "from": "client@x.io", "subject": "VPN down", "body": "help!",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("receive: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var tk services.Ticket
	_ = json.NewDecoder(rec.Body).Decode(&tk)
	if tk.Status != services.TicketOpen {
		t.Fatalf("ticket status=%d", tk.Status)
	}
	msgs, err := tickets.MessagesOf(t.Context(), tk.ID)
	if err != nil || len(msgs) != 1 || msgs[0].Author != "client@x.io" {
		t.Fatalf("messages=%+v err=%v", msgs, err)
	}
}
