// Package ai implements the provider-agnostic assist endpoint (Phase 5
// PORT-LITE: Dolibarr Ai equivalent, scoped to an endpoint, not a feature
// surface): callers POST a prompt with record context, a registered Provider
// completes it, and the run is logged to ferp_ai_runs with excerpts only
// (full prompts/outputs never persist — operators keep their own audit).
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// AssistRequest is one assist call.
type AssistRequest struct {
	Scope      string `json:"scope"`
	ObjectType string `json:"object_type"`
	ObjectID   int64  `json:"object_id"`
	Prompt     string `json:"prompt"`
	Model      string `json:"model"` // hint only; the provider decides
}

// Validate checks request invariants.
func (a AssistRequest) Validate() error {
	if strings.TrimSpace(a.Prompt) == "" {
		return fmt.Errorf("ai: prompt required: %w", platform.ErrValidation)
	}
	if len(a.Prompt) > 8000 {
		return fmt.Errorf("ai: prompt too long (8000 max): %w", platform.ErrValidation)
	}
	return nil
}

// Run is one logged assist execution (excerpts capped at 500 chars).
type Run struct {
	ID            int64     `json:"id"`
	EntityID      int64     `json:"entity_id"`
	Model         string    `json:"model"`
	PromptExcerpt string    `json:"prompt_excerpt"`
	OutputExcerpt string    `json:"output_excerpt"`
	DurationMs    int64     `json:"duration_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

// excerpt caps stored text (run log keeps excerpts, never full I/O).
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// Provider completes a prompt. Deployments register their own (HTTP LLM,
// local model); the default EchoProvider answers offline without network.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req AssistRequest) (output string, err error)
}

// EchoProvider is the default offline provider: deterministic, no network.
// It answers with record context + prompt echo so integrations and the SPA
// work with zero configuration; real deployments replace it.
type EchoProvider struct{}

// Name implements Provider.
func (EchoProvider) Name() string { return "echo" }

// Complete implements Provider.
func (EchoProvider) Complete(_ context.Context, req AssistRequest) (string, error) {
	where := strings.TrimSpace(req.Scope + " " + req.ObjectType)
	if req.ObjectID > 0 {
		where += fmt.Sprintf(" #%d", req.ObjectID)
	}
	return fmt.Sprintf("[echo:%s] %s", strings.TrimSpace(where), excerpt(req.Prompt)), nil
}
