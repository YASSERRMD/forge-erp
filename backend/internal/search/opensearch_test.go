package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeOS is a minimal OpenSearch: stores docs, serves multi-match-ish search.
type fakeOS struct {
	mu   sync.Mutex
	docs map[string]doc
	fail bool
}

func newFakeOS() (*fakeOS, *httptest.Server) {
	f := &fakeOS{docs: map[string]doc{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		path := r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodPut && !strings.Contains(path, "_doc"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"acknowledged":true}`))
		case strings.Contains(path, "_doc"):
			id := path[strings.LastIndex(path, "/")+1:]
			var d doc
			_ = json.NewDecoder(r.Body).Decode(&d)
			f.docs[id] = d
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":"created"}`))
		case strings.Contains(path, "_search"):
			var q struct {
				Query struct {
					Bool struct {
						Must []map[string]any `json:"must"`
					} `json:"bool"`
				} `json:"query"`
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &q)
			term := ""
			for _, m := range q.Query.Bool.Must {
				if mm, ok := m["multi_match"].(map[string]any); ok {
					term, _ = mm["query"].(string)
				}
			}
			term = strings.ToLower(term)
			type hit struct {
				Source doc `json:"_source"`
			}
			var hits []hit
			for _, d := range f.docs {
				if strings.Contains(strings.ToLower(d.Label), term) ||
					strings.Contains(strings.ToLower(d.Ref), term) {
					hits = append(hits, hit{Source: d})
				}
			}
			if hits == nil {
				hits = []hit{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	return f, srv
}

func testProviders() map[string]Provider {
	return map[string]Provider{
		"organization": func(_ context.Context, _ int64) ([]Result, error) {
			return []Result{{ID: 1, Label: "Acme Industries", Ref: "ACME-001"}}, nil
		},
		"product": func(_ context.Context, _ int64) ([]Result, error) {
			return []Result{{ID: 2, Label: "Standard widget", Ref: "WID-001"}}, nil
		},
	}
}

func TestOpenSearchRoundTrip(t *testing.T) {
	_, srv := newFakeOS()
	defer srv.Close()
	s := NewOpenSearcher(OSConfig{URL: srv.URL, Index: "forgeerp"}, testProviders())
	ctx := context.Background()
	n, err := s.Reindex(ctx, 1)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if n != 2 {
		t.Fatalf("indexed=%d want 2", n)
	}
	hits, err := s.Search(ctx, 1, "acme", nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Scope != "organization" || hits[0].ID != 1 {
		t.Fatalf("hits=%+v", hits)
	}
	hits, err = s.Search(ctx, 1, "WID", []string{"product"})
	if err != nil || len(hits) != 1 || hits[0].Ref != "WID-001" {
		t.Fatalf("scoped hits=%+v err=%v", hits, err)
	}
	if hits, err := s.Search(ctx, 1, "zzz", nil); err != nil || len(hits) != 0 {
		t.Fatalf("empty=%v err=%v", hits, err)
	}
}

func TestOpenSearchFallback(t *testing.T) {
	f, srv := newFakeOS()
	defer srv.Close()
	s := NewOpenSearcher(OSConfig{URL: srv.URL, Index: "forgeerp"}, testProviders())
	ctx := context.Background()
	f.fail = true // every request 500s → fallback must serve provider data
	hits, err := s.Search(ctx, 1, "widget", nil)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if len(hits) != 1 || hits[0].Label != "Standard widget" {
		t.Fatalf("fallback hits=%+v", hits)
	}
	if _, err := s.Reindex(ctx, 1); err == nil {
		t.Fatal("reindex against failing server accepted")
	}
}
