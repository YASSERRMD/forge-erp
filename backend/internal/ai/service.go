package ai

import (
	"context"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns assist use cases.
type Service struct {
	Store    Store
	Provider Provider
	Bus      platform.Bus
	DB       platform.DBTX
	Now      func() time.Time
}

// NewService builds a Service (nil provider → EchoProvider).
func NewService(s Store, p Provider, bus platform.Bus, db platform.DBTX) *Service {
	if p == nil {
		p = EchoProvider{}
	}
	return &Service{Store: s, Provider: p, Bus: bus, DB: db}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// AssistResult is one completed assist call.
type AssistResult struct {
	Output     string `json:"output"`
	Model      string `json:"model"`
	DurationMs int64  `json:"duration_ms"`
	RunID      int64  `json:"run_id"`
}

// Assist validates, completes through the provider, and logs the run.
// Provider failures are 500-shaped (the run is NOT logged — nothing ran).
func (s *Service) Assist(ctx context.Context, entityID int64, req AssistRequest) (AssistResult, error) {
	if err := req.Validate(); err != nil {
		return AssistResult{}, err
	}
	start := s.now()
	output, err := s.Provider.Complete(ctx, req)
	if err != nil {
		return AssistResult{}, err
	}
	elapsed := s.now().Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	run := &Run{EntityID: entityID, Model: s.Provider.Name(),
		PromptExcerpt: excerpt(req.Prompt), OutputExcerpt: excerpt(output),
		DurationMs: elapsed.Milliseconds()}
	if err := s.Store.Log(ctx, s.DB, run); err != nil {
		return AssistResult{}, err
	}
	if s.Bus != nil {
		_ = s.Bus.Publish(ctx, platform.Event{Subject: "forgeerp.ai.assist.completed.v1",
			Entity: "run", EntityID: entityID, ID: run.ID})
	}
	return AssistResult{Output: output, Model: run.Model,
		DurationMs: run.DurationMs, RunID: run.ID}, nil
}
