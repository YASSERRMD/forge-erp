package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

// failProvider always errors (provider-outage path).
type failProvider struct{}

func (failProvider) Name() string { return "failing" }

func (failProvider) Complete(_ context.Context, _ AssistRequest) (string, error) {
	return "", errors.New("ai: provider unreachable")
}

func TestAssistEchoAndLog(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore(), nil, platform.NewMemoryBus(), nil)
	res, err := svc.Assist(ctx, 1, AssistRequest{Scope: "sales",
		ObjectType: "invoice", ObjectID: 7, Prompt: "summarize"})
	if err != nil {
		t.Fatalf("assist: %v", err)
	}
	if res.Model != "echo" || res.RunID == 0 {
		t.Fatalf("result=%+v", res)
	}
	if !strings.Contains(res.Output, "summarize") {
		t.Fatalf("output=%q missing prompt", res.Output)
	}
	runs, err := svc.Store.List(ctx, nil, 1, 50, 0)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if runs[0].PromptExcerpt != "summarize" || runs[0].OutputExcerpt == "" {
		t.Fatalf("run=%+v", runs[0])
	}
	// Other entities see no runs.
	other, _ := svc.Store.List(ctx, nil, 2, 50, 0)
	if len(other) != 0 {
		t.Fatalf("cross-entity runs=%d want 0", len(other))
	}
	// Long prompts are excerpted, never stored whole.
	long := strings.Repeat("x", 900)
	res, err = svc.Assist(ctx, 1, AssistRequest{Prompt: long})
	if err != nil {
		t.Fatalf("long assist: %v", err)
	}
	runs, _ = svc.Store.List(ctx, nil, 1, 50, 0)
	if len(runs[0].PromptExcerpt) > 500 {
		t.Fatalf("excerpt=%d chars want <=500", len(runs[0].PromptExcerpt))
	}
	// Empty prompt is 422-shaped; provider failure surfaces unwrapped.
	if _, err := svc.Assist(ctx, 1, AssistRequest{}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty prompt err=%v want ErrValidation", err)
	}
	bad := NewService(NewMemoryStore(), failProvider{}, platform.NewMemoryBus(), nil)
	if _, err := bad.Assist(ctx, 1, AssistRequest{Prompt: "hi"}); err == nil {
		t.Fatal("failing provider accepted")
	}
	// Failing runs are not logged.
	if runs, _ := bad.Store.List(ctx, nil, 1, 50, 0); len(runs) != 0 {
		t.Fatalf("failed runs logged=%d want 0", len(runs))
	}
}

func TestAssistRoutes(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil, platform.NewMemoryBus(), nil)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(map[string]any{"prompt": "hello", "scope": "sales"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/assist", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assist API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var res AssistResult
	_ = json.NewDecoder(rec.Body).Decode(&res)
	if res.Output == "" || res.RunID == 0 {
		t.Fatalf("result=%+v", res)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var runs []Run
	_ = json.NewDecoder(rec.Body).Decode(&runs)
	if len(runs) != 1 {
		t.Fatalf("runs=%d want 1", len(runs))
	}
}
