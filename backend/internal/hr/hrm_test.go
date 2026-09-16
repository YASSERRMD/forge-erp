package hr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func mustEstablishment(t *testing.T, m *MemoryStore, code string) Establishment {
	t.Helper()
	e := &Establishment{EntityID: 1, Code: code, Label: code + " site"}
	if err := m.CreateEstablishment(context.Background(), nil, e); err != nil {
		t.Fatalf("establishment %s: %v", code, err)
	}
	return *e
}

func TestMemoryEmployeeCRUDTransitions(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	est := mustEstablishment(t, m, "HQ")
	if err := m.CreateEstablishment(ctx, nil, &Establishment{EntityID: 1, Code: "HQ", Label: "dup"}); err == nil {
		t.Error("duplicate establishment code accepted")
	}
	hire := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	e := &Employee{EntityID: 1, Code: "EMP-1", Name: "Ada Lovelace",
		EstablishmentID: est.ID, JobTitle: "Engineer", HireDate: hire}
	svc := NewHRMService(nil, m, nil)
	if err := svc.Hire(ctx, nil, 1, e); err != nil {
		t.Fatalf("hire: %v", err)
	}
	if e.Status != EmployeeActive {
		t.Fatalf("status=%d want active", e.Status)
	}
	if err := m.CreateEmployee(ctx, nil, &Employee{EntityID: 1, Code: "EMP-1", Name: "dup",
		EstablishmentID: est.ID, JobTitle: "X", HireDate: hire}); err == nil {
		t.Error("duplicate employee code accepted")
	}
	// Active → suspended → active → terminated; terminated terminal.
	susp, err := m.SetEmployeeStatus(ctx, nil, 1, e.ID, EmployeeSuspended, e.RowVersion)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	back, err := m.SetEmployeeStatus(ctx, nil, 1, e.ID, EmployeeActive, susp.RowVersion)
	if err != nil {
		t.Fatalf("reinstate: %v", err)
	}
	term, err := svc.Terminate(ctx, nil, 1, e.ID, back.RowVersion)
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if _, err := m.SetEmployeeStatus(ctx, nil, 1, e.ID, EmployeeActive, term.RowVersion); err == nil {
		t.Error("terminated→active accepted")
	}
	// Cross-tenant isolation.
	if _, err := m.SetEmployeeStatus(ctx, nil, 2, e.ID, EmployeeSuspended, term.RowVersion); err == nil {
		t.Error("cross-tenant transition accepted")
	}
	// Validation vectors.
	if err := m.CreateEmployee(ctx, nil, &Employee{EntityID: 1, Code: " ", Name: "x",
		EstablishmentID: est.ID, JobTitle: "X", HireDate: hire}); err == nil {
		t.Error("blank code accepted")
	}
	if err := m.CreateEmployee(ctx, nil, &Employee{EntityID: 1, Code: "EMP-9", Name: "x",
		EstablishmentID: 999, JobTitle: "X", HireDate: hire}); err == nil {
		t.Error("unknown establishment accepted")
	}
	if err := m.AddSkill(ctx, nil, &EmployeeSkill{EntityID: 1, EmployeeID: e.ID, Skill: "go", Level: 9}); err == nil {
		t.Error("skill level 9 accepted")
	}
	sk := &EmployeeSkill{EntityID: 1, EmployeeID: e.ID, Skill: "go", Level: 4}
	if err := m.AddSkill(ctx, nil, sk); err != nil {
		t.Fatalf("skill: %v", err)
	}
	if ss, _ := m.SkillsOf(ctx, nil, 1, e.ID); len(ss) != 1 {
		t.Fatalf("skills=%d want 1", len(ss))
	}
	if err := m.AddEvaluation(ctx, nil, &Evaluation{EntityID: 1, EmployeeID: e.ID, Period: "2026-13", Rating: 3}); err == nil {
		t.Error("bad evaluation period accepted")
	}
	if err := m.AddEvaluation(ctx, nil, &Evaluation{EntityID: 1, EmployeeID: e.ID, Period: "2026-09", Rating: 0}); err == nil {
		t.Error("rating 0 accepted")
	}
	if err := m.AddEvaluation(ctx, nil, &Evaluation{EntityID: 1, EmployeeID: e.ID, Period: "2026-09", Rating: 5, Notes: "great"}); err != nil {
		t.Fatalf("evaluation: %v", err)
	}
	if evs, _ := m.EvaluationsOf(ctx, nil, 1, e.ID); len(evs) != 1 {
		t.Fatalf("evaluations=%d want 1", len(evs))
	}
}

func TestEmployeeAPI(t *testing.T) {
	h := testRouter()
	rec := doReq(t, h, http.MethodPost, "/api/v1/hr/establishments",
		map[string]any{"code": "LYON", "label": "Lyon plant"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("establishment: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var est Establishment
	_ = json.NewDecoder(rec.Body).Decode(&est)
	rec = doReq(t, h, http.MethodPost, "/api/v1/hr/employees", map[string]any{
		"code": "E-1", "name": "Ada", "establishment_id": est.ID,
		"job_title": "Engineer", "hire_date": "2026-03-01T00:00:00Z"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("hire: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var e Employee
	_ = json.NewDecoder(rec.Body).Decode(&e)
	// Suspend then terminate via dedicated endpoint.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/employees/%d/status", e.ID),
		map[string]any{"status": 1, "row_version": e.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&e)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/employees/%d/terminate", e.ID),
		map[string]any{"status": 2, "row_version": e.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("terminate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// status endpoint refuses direct terminate.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/employees/%d/status", e.ID),
		map[string]any{"status": 2, "row_version": e.RowVersion})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("direct terminate: code=%d want 422", rec.Code)
	}
	// Skill + evaluation round-trip.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/employees/%d/skills", e.ID),
		map[string]any{"skill": "welding", "level": 3})
	if rec.Code != http.StatusCreated {
		t.Fatalf("skill: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/employees/%d/evaluations", e.ID),
		map[string]any{"period": "2026-09", "rating": 4, "notes": "solid"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("evaluation: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/hr/employees/%d/evaluations", e.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list evaluations: code=%d", rec.Code)
	}
}

func TestPGEmployeePersist(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	est := &Establishment{EntityID: 1, Code: "PGHQ", Label: "PG HQ"}
	if err := st.CreateEstablishment(ctx, pool, est); err != nil {
		t.Fatalf("establishment: %v", err)
	}
	hire := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	e := &Employee{EntityID: 1, Code: "PG-EMP-1", Name: "Grace", EstablishmentID: est.ID,
		JobTitle: "Tech", HireDate: hire, Status: EmployeeActive}
	if err := st.CreateEmployee(ctx, pool, e); err != nil {
		t.Fatalf("employee: %v", err)
	}
	got, err := st.EmployeeByID(ctx, pool, 1, e.ID)
	if err != nil || got.Code != "PG-EMP-1" {
		t.Fatalf("byid=%+v err=%v", got, err)
	}
	sk := &EmployeeSkill{EntityID: 1, EmployeeID: e.ID, Skill: "soldering", Level: 2}
	if err := st.AddSkill(ctx, pool, sk); err != nil {
		t.Fatalf("skill: %v", err)
	}
	ev := &Evaluation{EntityID: 1, EmployeeID: e.ID, Period: "2026-09", Rating: 4}
	if err := st.AddEvaluation(ctx, pool, ev); err != nil {
		t.Fatalf("evaluation: %v", err)
	}
	if _, err := st.SetEmployeeStatus(ctx, pool, 1, e.ID, EmployeeSuspended, e.RowVersion); err != nil {
		t.Fatalf("suspend: %v", err)
	}
}
