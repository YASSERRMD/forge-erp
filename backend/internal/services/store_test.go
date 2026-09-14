package services

import (
	"context"
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

func TestProjectTransitions(t *testing.T) {
	p := Project{Status: ProjectDraft}
	if !p.CanTransition(ProjectActive) || !p.CanTransition(ProjectCanceled) {
		t.Error("draft must allow active/canceled")
	}
	if p.CanTransition(ProjectClosed) {
		t.Error("draft must not jump to closed")
	}
	p.Status = ProjectActive
	if !p.CanTransition(ProjectOnHold) || p.CanTransition(ProjectDraft) {
		t.Error("active transitions wrong")
	}
	p.Status = ProjectClosed
	if p.CanTransition(ProjectActive) {
		t.Error("closed is terminal")
	}
}

func TestTaskAndContractTransitions(t *testing.T) {
	tk := Task{Status: TaskTodo}
	if !tk.CanTransition(TaskDoing) || tk.CanTransition(TaskTodo) {
		t.Error("todo transitions wrong")
	}
	c := ServiceContract{Status: ContractActive}
	if !c.CanTransition(ContractSuspended) || c.CanTransition(ContractDraft) {
		t.Error("active contract transitions wrong")
	}
	in := Intervention{Status: InterventionScheduled}
	if !in.CanTransition(InterventionInProgress) || in.CanTransition(InterventionDone) {
		t.Error("scheduled must go through inprogress")
	}
	tk2 := Ticket{Status: TicketResolved, Priority: 2}
	if !tk2.CanTransition(TicketClosed) || !tk2.CanTransition(TicketOpen) {
		t.Error("resolved must allow close/reopen")
	}
	if (Ticket{Priority: 0}.Validate() == nil) || (Ticket{EntityID: 1, Ref: "T", Subject: "s", Priority: 5}.Validate() == nil) {
		t.Error("priority range not enforced")
	}
}

func TestTimeValidation(t *testing.T) {
	if (TimeEntry{Hours: 0}.Validate() == nil) {
		t.Error("zero hours accepted")
	}
	if (ServiceContract{EntityID: 1, Ref: "C", OrgID: 1, Label: "x"}.Validate() != nil) {
		t.Error("valid contract rejected")
	}
}

func TestMemoryLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	p := &Project{EntityID: 1, Ref: "PRJ-1", Label: "Website"}
	if err := m.CreateProject(ctx, nil, p); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := m.CreateProject(ctx, nil, &Project{EntityID: 1, Ref: "PRJ-1", Label: "dup"}); err == nil {
		t.Error("duplicate ref accepted")
	}
	if _, err := m.SetProjectStatus(ctx, nil, 1, p.ID, ProjectClosed, p.RowVersion); err == nil {
		t.Error("draft→closed accepted")
	}
	pActive, err := m.SetProjectStatus(ctx, nil, 1, p.ID, ProjectActive, p.RowVersion)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	*p = pActive
	tk := &Task{EntityID: 1, ProjectID: p.ID, Label: "Design"}
	if err := m.CreateTask(ctx, nil, tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := m.AddTime(ctx, nil, &TimeEntry{EntityID: 1, ProjectID: p.ID, TaskID: tk.ID,
		Author: "ada", Hours: 250, EntryDate: now}); err != nil {
		t.Fatalf("add time: %v", err)
	}
	if err := m.AddTime(ctx, nil, &TimeEntry{EntityID: 1, ProjectID: p.ID, TaskID: tk.ID,
		Author: "ada", Hours: 150, EntryDate: now}); err != nil {
		t.Fatalf("add time 2: %v", err)
	}
	if h, _ := m.TaskHours(ctx, nil, tk.ID); h != 400 {
		t.Errorf("task hours=%d want 400", h)
	}
	if h, _ := m.ProjectHours(ctx, nil, p.ID); h != 400 {
		t.Errorf("project hours=%d want 400", h)
	}
	tk2, err := m.SetTaskStatus(ctx, nil, 1, tk.ID, TaskDone, tk.RowVersion)
	if err != nil {
		t.Fatalf("finish task: %v", err)
	}
	if err := m.AddTime(ctx, nil, &TimeEntry{EntityID: 1, ProjectID: p.ID, TaskID: tk.ID,
		Author: "ada", Hours: 10, EntryDate: now}); err == nil {
		t.Error("time on done task accepted")
	}
	_ = tk2
	pClosed, err := m.SetProjectStatus(ctx, nil, 1, p.ID, ProjectClosed, p.RowVersion)
	if err != nil {
		t.Fatalf("close project: %v", err)
	}
	*p = pClosed
	if err := m.CreateTask(ctx, nil, &Task{EntityID: 1, ProjectID: p.ID, Label: "late"}); err == nil {
		t.Error("task on closed project accepted")
	}
	if err := m.AddTime(ctx, nil, &TimeEntry{EntityID: 1, ProjectID: p.ID, TaskID: tk.ID,
		Author: "ada", Hours: 10, EntryDate: now}); err == nil {
		t.Error("time on closed project accepted")
	}
}

func TestMemoryTicketFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	tk := &Ticket{EntityID: 1, Ref: "TCK-1", Subject: "Login broken", Priority: 3}
	if err := m.CreateTicket(ctx, nil, tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if err := m.AddMessage(ctx, nil, &TicketMessage{EntityID: 1, TicketID: tk.ID, Author: "bob", Body: "seen"}); err != nil {
		t.Fatalf("add message: %v", err)
	}
	upd, err := m.SetTicketStatus(ctx, nil, 1, tk.ID, TicketResolved, tk.RowVersion)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	upd, err = m.SetTicketStatus(ctx, nil, 1, tk.ID, TicketClosed, upd.RowVersion)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := m.AddMessage(ctx, nil, &TicketMessage{EntityID: 1, TicketID: tk.ID, Author: "bob", Body: "late"}); err == nil {
		t.Error("message on closed ticket accepted")
	}
	upd, err = m.SetTicketStatus(ctx, nil, 1, tk.ID, TicketOpen, upd.RowVersion)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if msgs, _ := m.MessagesOf(ctx, nil, tk.ID); len(msgs) != 1 {
		t.Errorf("messages=%d want 1", len(msgs))
	}
	_ = upd
}

func TestMemoryContractDates(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	start := time.Now().UTC()
	end := start.Add(-time.Hour)
	if err := m.CreateContract(ctx, nil, &ServiceContract{EntityID: 1, Ref: "CTR-1", OrgID: 7,
		Label: "bad", StartDate: &start, EndDate: &end}); err == nil {
		t.Error("end before start accepted")
	}
	if err := m.CreateContract(ctx, nil, &ServiceContract{EntityID: 1, Ref: "CTR-1", OrgID: 7, Label: "ok"}); err != nil {
		t.Fatalf("create contract: %v", err)
	}
	if orgs, _ := m.ContractsOfOrg(ctx, nil, 7); len(orgs) != 1 {
		t.Errorf("contracts of org=%d want 1", len(orgs))
	}
}

func TestPGProjectLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	p := &Project{EntityID: 1, Ref: "PG-P1", Label: "PG Project"}
	if err := st.CreateProject(ctx, pool, p); err != nil {
		t.Fatalf("project: %v", err)
	}
	tk := &Task{EntityID: 1, ProjectID: p.ID, Label: "PG Task"}
	if err := st.CreateTask(ctx, pool, tk); err != nil {
		t.Fatalf("task: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.AddTime(ctx, pool, &TimeEntry{EntityID: 1, ProjectID: p.ID, TaskID: tk.ID,
		Author: "ada", Hours: 120, EntryDate: now}); err != nil {
		t.Fatalf("time: %v", err)
	}
	if h, _ := st.ProjectHours(ctx, pool, p.ID); h != 120 {
		t.Fatalf("hours=%d", h)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	p := &Project{EntityID: 1, Ref: "X-1", Label: "X"}
	if err := m.CreateProject(ctx, nil, p); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := m.ProjectByID(ctx, nil, 2, p.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant ProjectByID err=%v want ErrNotFound", err)
	}
	if _, err := m.SetProjectStatus(ctx, nil, 2, p.ID, ProjectActive, p.RowVersion); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant SetProjectStatus err=%v want ErrNotFound", err)
	}
	tk := &Ticket{EntityID: 1, Ref: "X-T1", Subject: "s", Priority: 2}
	if err := m.CreateTicket(ctx, nil, tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if _, err := m.TicketByID(ctx, nil, 2, tk.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant TicketByID err=%v want ErrNotFound", err)
	}

	// Handler GET under another entity (row seeded under entity 2,
	// test request carries entity 1 via the passthrough tenant) must 404.
	m2 := NewMemoryStore()
	p2 := &Project{EntityID: 2, Ref: "X-2", Label: "foreign"}
	if err := m2.CreateProject(ctx, nil, p2); err != nil {
		t.Fatalf("seed foreign project: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: m2}, passthrough)
	})
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/services/projects/%d", p2.ID), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("handler cross-tenant GET code=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}
