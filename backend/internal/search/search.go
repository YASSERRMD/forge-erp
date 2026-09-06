// Package search provides cross-entity search (Dolibarr per-module search +
// global search equivalent). The Searcher interface backs OpenSearch in
// production; MemorySearcher serves development and tests.
package search

import (
	"context"
	"strings"
)

// Result is one hit.
type Result struct {
	Scope string `json:"scope"` // organization | product | document | ...
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Ref   string `json:"ref"`
}

// Provider indexes one scope on demand (list-all iterators from owning stores).
type Provider func(ctx context.Context, entityID int64) ([]Result, error)

// MemorySearcher fans out to registered providers and filters substring matches.
type MemorySearcher struct {
	providers map[string]Provider
}

// NewMemorySearcher builds an empty searcher.
func NewMemorySearcher() *MemorySearcher { return &MemorySearcher{providers: map[string]Provider{}} }

// Register adds a scope provider.
func (s *MemorySearcher) Register(scope string, p Provider) { s.providers[scope] = p }

// Search returns matches for q (case-insensitive substring over label+ref).
func (s *MemorySearcher) Search(ctx context.Context, entityID int64, q string, scopes []string) ([]Result, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	if len(scopes) == 0 {
		for scope := range s.providers {
			scopes = append(scopes, scope)
		}
	}
	var out []Result
	for _, scope := range scopes {
		p, ok := s.providers[scope]
		if !ok {
			continue
		}
		all, err := p(ctx, entityID)
		if err != nil {
			return nil, err
		}
		for _, r := range all {
			if strings.Contains(strings.ToLower(r.Label), q) || strings.Contains(strings.ToLower(r.Ref), q) {
				r.Scope = scope
				out = append(out, r)
			}
		}
	}
	return out, nil
}
