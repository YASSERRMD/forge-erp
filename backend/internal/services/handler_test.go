package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore()}, passthrough)
	})
	return r
}

func doReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProjectTaskTimeFlow(t *testing.T) {
	h := testRouter()
	rec := doReq(t, h, http.MethodPost, "/api/v1/services/projects",
		map[string]any{"ref": "PRJ-9", "label": "Intranet"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p Project
	_ = json.NewDecoder(rec.Body).Decode(&p)

	// Duplicate ref → 409.
	rec = doReq(t, h, http.MethodPost, "/api/v1/services/projects",
		map[string]any{"ref": "PRJ-9", "label": "dup"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: code=%d want 409", rec.Code)
	}

	// Illegal jump draft→closed → 422.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/projects/%d/status", p.ID),
		map[string]any{"status": 3, "row_version": p.RowVersion})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("illegal transition: code=%d want 422 body=%s", rec.Code, rec.Body.String())
	}

	// Activate.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/projects/%d/status", p.ID),
		map[string]any{"status": 1, "row_version": p.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("activate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&p)

	// Add task + time.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/projects/%d/tasks", p.ID),
		map[string]any{"label": "Setup"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var tk Task
	_ = json.NewDecoder(rec.Body).Decode(&tk)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/tasks/%d/time", tk.ID),
		map[string]any{"project_id": p.ID, "author": "ada", "hours": 200, "entry_date": "2026-09-06T00:00:00Z"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add time: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/services/projects/%d/hours", p.ID), nil)
	var hours map[string]int64
	_ = json.NewDecoder(rec.Body).Decode(&hours)
	if hours["hours"] != 200 {
		t.Fatalf("hours=%d want 200 (%s)", hours["hours"], rec.Body.String())
	}

	// Unknown project → 404.
	rec = doReq(t, h, http.MethodGet, "/api/v1/services/projects/9999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing: code=%d want 404", rec.Code)
	}
}

func TestTicketAndContractFlow(t *testing.T) {
	h := testRouter()
	rec := doReq(t, h, http.MethodPost, "/api/v1/services/tickets",
		map[string]any{"ref": "TCK-1", "subject": "VPN down", "priority": 4})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ticket: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var tk Ticket
	_ = json.NewDecoder(rec.Body).Decode(&tk)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/tickets/%d/messages", tk.ID),
		map[string]any{"author": "ops", "body": "looking"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("message: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/tickets/%d/status", tk.ID),
		map[string]any{"status": 3, "row_version": tk.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("close: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&tk)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/tickets/%d/messages", tk.ID),
		map[string]any{"author": "ops", "body": "late"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("message on closed: code=%d want 422", rec.Code)
	}

	// Contract with bad dates → 422; good → 201 + activate + list by org.
	rec = doReq(t, h, http.MethodPost, "/api/v1/services/contracts",
		map[string]any{"ref": "CTR-1", "org_id": 5, "label": "bad",
			"start_date": "2026-09-06T00:00:00Z", "end_date": "2026-09-05T00:00:00Z"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad dates: code=%d want 422", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/services/contracts",
		map[string]any{"ref": "CTR-1", "org_id": 5, "label": "Support"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contract: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var c ServiceContract
	_ = json.NewDecoder(rec.Body).Decode(&c)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/contracts/%d/status", c.ID),
		map[string]any{"status": 1, "row_version": c.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("activate contract: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/services/organizations/5/contracts", nil)
	var list []ServiceContract
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("contracts of org=%d want 1", len(list))
	}

	// Intervention scheduled→done must go through inprogress.
	rec = doReq(t, h, http.MethodPost, "/api/v1/services/interventions",
		map[string]any{"ref": "INT-1", "org_id": 5, "label": "Onsite"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create intervention: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var in Intervention
	_ = json.NewDecoder(rec.Body).Decode(&in)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/services/interventions/%d/status", in.ID),
		map[string]any{"status": 2, "row_version": in.RowVersion})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("skip inprogress: code=%d want 422", rec.Code)
	}
}

func TestContractInterventionLists(t *testing.T) {
	h := testRouter()
	rec := doReq(t, h, http.MethodPost, "/api/v1/services/contracts",
		map[string]any{"ref": "CTR-L", "org_id": 3, "label": "Support"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("contract: code=%d", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/services/contracts?limit=50", nil)
	var contracts []ServiceContract
	_ = json.NewDecoder(rec.Body).Decode(&contracts)
	if len(contracts) != 1 {
		t.Fatalf("contracts=%d", len(contracts))
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/services/interventions",
		map[string]any{"ref": "INT-L", "org_id": 3, "label": "Onsite"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("intervention: code=%d", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/services/interventions?limit=50", nil)
	var intervs []Intervention
	_ = json.NewDecoder(rec.Body).Decode(&intervs)
	if len(intervs) != 1 {
		t.Fatalf("interventions=%d", len(intervs))
	}
}
