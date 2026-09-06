package search

import (
	"context"
	"testing"
)

func TestMemorySearcher(t *testing.T) {
	ctx := context.Background()
	s := NewMemorySearcher()
	s.Register("organization", func(_ context.Context, _ int64) ([]Result, error) {
		return []Result{{ID: 1, Label: "Acme Industries", Ref: "ACME-001"}}, nil
	})
	s.Register("product", func(_ context.Context, _ int64) ([]Result, error) {
		return []Result{{ID: 2, Label: "Standard widget", Ref: "WID-001"}}, nil
	})
	hits, err := s.Search(ctx, 1, "acme", nil)
	if err != nil || len(hits) != 1 || hits[0].Scope != "organization" {
		t.Fatalf("hits = %+v %v", hits, err)
	}
	hits, _ = s.Search(ctx, 1, "WID", []string{"product"})
	if len(hits) != 1 {
		t.Fatalf("scoped hits = %+v", hits)
	}
	none, _ := s.Search(ctx, 1, "zzz-no-match", nil)
	if len(none) != 0 {
		t.Fatalf("hits = %+v", none)
	}
	if empty, _ := s.Search(ctx, 1, "  ", nil); len(empty) != 0 {
		t.Fatal("blank query should return nothing")
	}
}
