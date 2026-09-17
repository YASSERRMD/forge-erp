package datapolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
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

func TestAnonymiseFieldCoverage(t *testing.T) {
	org := OrgPII{ID: 7, Name: "Acme SA", Email: "boss@acme.test",
		Phone: "+33123456789", Address: "12 rue Test", LedgerTotal: 123456}
	anon := AnonymizeOrg(org)
	for field, v := range map[string]string{
		"name": anon.Name, "email": anon.Email, "phone": anon.Phone, "address": anon.Address,
	} {
		if !Redacted(v) {
			t.Errorf("org %s=%q not redacted", field, v)
		}
	}
	for _, live := range []string{org.Name, org.Email, org.Phone, org.Address} {
		if live != "" && (anon.Name == live || anon.Email == live || anon.Address == live) {
			t.Errorf("live PII %q survived anonymisation", live)
		}
	}
	if anon.LedgerTotal != org.LedgerTotal {
		t.Errorf("ledger=%d want %d (amounts must survive)", anon.LedgerTotal, org.LedgerTotal)
	}

	m := MemberPII{ID: 9, FirstName: "Ada", LastName: "Lovelace", Company: "Acme",
		Email: "ada@example.com", Phone: "0600", LedgerTotal: 999}
	am := AnonymizeMember(m)
	for field, v := range map[string]string{
		"first": am.FirstName, "last": am.LastName, "company": am.Company,
		"email": am.Email, "phone": am.Phone,
	} {
		if !Redacted(v) {
			t.Errorf("member %s=%q not redacted", field, v)
		}
	}
	if am.LedgerTotal != m.LedgerTotal {
		t.Errorf("ledger=%d want %d", am.LedgerTotal, m.LedgerTotal)
	}
	bad := RetentionRule{EntityID: 1, Scope: "nope", RetainDays: 1, Action: ActionAnonymize}
	if bad.Validate() == nil {
		t.Error("unknown scope accepted")
	}
}

func TestDryRunMemory(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	svc := &Service{Store: m, Now: func() time.Time { return now },
		Subjects: func(_ context.Context, _ int64, _ string) ([]Subject, error) {
			return []Subject{
				{ID: 1, ClosedAt: now.AddDate(0, 0, -400)},             // due (365d rule)
				{ID: 2, ClosedAt: now.AddDate(0, 0, -10)},              // not due
				{ID: 3, ClosedAt: now.AddDate(0, 0, -900), Open: true}, // open, excluded
			}, nil
		}}
	if err := m.UpsertRule(ctx, nil, &RetentionRule{EntityID: 1, Scope: ScopeMembers, RetainDays: 365, Action: ActionAnonymize}); err != nil {
		t.Fatalf("rule: %v", err)
	}
	rep, err := svc.DryRun(ctx, 1, ScopeMembers)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(rep.Candidates) != 2 || rep.DueCount != 1 {
		t.Fatalf("report=%+v want 2 candidates, 1 due", rep)
	}
	if rep.Candidates[0].SubjectID != 1 || !rep.Candidates[0].Due {
		t.Fatalf("first candidate=%+v want subject 1 due", rep.Candidates[0])
	}
	if _, err := svc.DryRun(ctx, 1, ScopeOrgs); err == nil {
		t.Error("missing rule accepted")
	}
	e, err := svc.RequestErasure(ctx, 1, ScopeMembers, 1, "gdpr art.17")
	if err != nil || e.Status != ErasurePending {
		t.Fatalf("request=%+v err=%v", e, err)
	}
	done, err := svc.CompleteErasure(ctx, 1, e.ID)
	if err != nil || done.Status != ErasureDone {
		t.Fatalf("complete=%+v err=%v", done, err)
	}
	if _, err := svc.CompleteErasure(ctx, 1, e.ID); err == nil {
		t.Error("double-complete accepted")
	}
}

func TestDataPolicyAPI(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	put := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := put("/api/v1/data-policy/rules",
		map[string]any{"scope": "orgs", "retain_days": 30, "action": "anonymize"}); rec.Code != http.StatusOK {
		t.Fatalf("rule: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/data-policy/dry-run?scope=orgs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = post("/api/v1/data-policy/erasures", map[string]any{"scope": "orgs", "subject_id": 42})
	if rec.Code != http.StatusCreated {
		t.Fatalf("erasure: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var e ErasureRequest
	_ = json.NewDecoder(rec.Body).Decode(&e)
	rec = post(fmt.Sprintf("/api/v1/data-policy/erasures/%d/done", e.ID), map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("done: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPGDataPolicyFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	if err := st.UpsertRule(ctx, pool, &RetentionRule{EntityID: 1, Scope: ScopeOrgs, RetainDays: 90, Action: ActionAnonymize}); err != nil {
		t.Fatalf("rule: %v", err)
	}
	if err := st.UpsertRule(ctx, pool, &RetentionRule{EntityID: 1, Scope: ScopeOrgs, RetainDays: 120, Action: ActionAnonymize}); err != nil {
		t.Fatalf("rule update: %v", err)
	}
	rule, err := st.RuleByScope(ctx, pool, 1, ScopeOrgs)
	if err != nil || rule.RetainDays != 120 {
		t.Fatalf("rule=%+v err=%v want 120 days", rule, err)
	}
	e := &ErasureRequest{EntityID: 1, Scope: ScopeOrgs, SubjectID: 7, Reason: "test"}
	if err := st.LogErasure(ctx, pool, e); err != nil {
		t.Fatalf("log: %v", err)
	}
	list, err := st.ListErasures(ctx, pool, 1, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("erasures=%+v err=%v", list, err)
	}
	if _, err := st.MarkErasureDone(ctx, pool, 2, e.ID); err == nil {
		t.Error("cross-tenant MarkErasureDone accepted")
	}
	done, err := st.MarkErasureDone(ctx, pool, 1, e.ID)
	if err != nil || done.Status != ErasureDone {
		t.Fatalf("done=%+v err=%v", done, err)
	}
}
