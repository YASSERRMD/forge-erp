package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Searcher is the query contract (MemorySearcher or OpenSearcher).
type Searcher interface {
	Search(ctx context.Context, entityID int64, q string, scopes []string) ([]Result, error)
}

// OSConfig carries OpenSearch settings (env FERP_SEARCH_*). The compose
// service runs single-node without the security plugin, matching defaults.
type OSConfig struct {
	URL     string
	Index   string
	Timeout time.Duration
}

// LoadOSConfig reads FERP_SEARCH_* env with compose defaults.
func LoadOSConfig(get func(key, def string) string) OSConfig {
	return OSConfig{
		URL:     get("FERP_SEARCH_URL", "http://localhost:9200"),
		Index:   get("FERP_SEARCH_INDEX", "forgeerp"),
		Timeout: 10 * time.Second,
	}
}

// OpenSearchEnabled reports whether FERP_SEARCH_BACKEND=opensearch.
func OpenSearchEnabled(get func(key, def string) string) bool {
	if v := os.Getenv("FERP_SEARCH_BACKEND"); v != "" {
		return v == "opensearch"
	}
	return get("FERP_SEARCH_BACKEND", "memory") == "opensearch"
}

// OpenSearcher queries OpenSearch with provider fallback: OpenSearch serves
// ranked hits; any transport/query failure falls back to the in-process
// providers so search never hard-fails (and stays fresh between reindexes).
type OpenSearcher struct {
	cfg       OSConfig
	client    *http.Client
	providers map[string]Provider
}

// NewOpenSearcher builds an OpenSearch-backed searcher over providers.
func NewOpenSearcher(cfg OSConfig, providers map[string]Provider) *OpenSearcher {
	return &OpenSearcher{cfg: cfg,
		client:    &http.Client{Timeout: cfg.Timeout},
		providers: providers}
}

// doc is the indexed shape.
type doc struct {
	Scope    string `json:"scope"`
	EntityID int64  `json:"entity_id"`
	RefID    int64  `json:"ref_id"`
	Label    string `json:"label"`
	Ref      string `json:"ref"`
}

// Document is the exported indexed shape (alias for cross-package indexing).
type Document = doc

// MakeDocument builds an indexable document.
func MakeDocument(scope string, entityID, id int64, label, ref string) Document {
	return Document{Scope: scope, EntityID: entityID, RefID: id, Label: label, Ref: ref}
}

// EnsureIndex creates the index with mappings (400 = already exists, fine).
func (s *OpenSearcher) EnsureIndex(ctx context.Context) error {
	body := `{"mappings":{"properties":{` +
		`"scope":{"type":"keyword"},"entity_id":{"type":"long"},` +
		`"ref_id":{"type":"long"},"label":{"type":"text"},"ref":{"type":"keyword"}}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		s.cfg.URL+"/"+s.cfg.Index, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("search: ensure index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("search: ensure index status %d", resp.StatusCode)
	}
	return nil
}

// IndexOne upserts one document.
func (s *OpenSearcher) IndexOne(ctx context.Context, d doc) error {
	raw, _ := json.Marshal(d)
	id := fmt.Sprintf("%s-%d", d.Scope, d.RefID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("%s/%s/_doc/%s", s.cfg.URL, s.cfg.Index, id), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("search: index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("search: index status %d", resp.StatusCode)
	}
	return nil
}

// Reindex pulls every provider scope into the index (startup/refresh path).
func (s *OpenSearcher) Reindex(ctx context.Context, entityID int64) (int, error) {
	if err := s.EnsureIndex(ctx); err != nil {
		return 0, err
	}
	n := 0
	for scope, p := range s.providers {
		all, err := p(ctx, entityID)
		if err != nil {
			return n, err
		}
		for _, r := range all {
			if err := s.IndexOne(ctx, doc{Scope: scope, EntityID: entityID,
				RefID: r.ID, Label: r.Label, Ref: r.Ref}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// Search queries OpenSearch, falling back to providers on any failure.
func (s *OpenSearcher) Search(ctx context.Context, entityID int64, q string, scopes []string) ([]Result, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	hits, err := s.query(ctx, entityID, q, scopes)
	if err != nil {
		return fallbackSearch(ctx, s.providers, entityID, q, scopes)
	}
	return hits, nil
}

type osHit struct {
	Source doc `json:"_source"`
}

type osResp struct {
	Hits struct {
		Hits []osHit `json:"hits"`
	} `json:"hits"`
}

// query runs a filtered multi-match against OpenSearch.
func (s *OpenSearcher) query(ctx context.Context, entityID int64, q string, scopes []string) ([]Result, error) {
	must := []any{map[string]any{"term": map[string]any{"entity_id": entityID}}}
	if len(scopes) > 0 {
		must = append(must, map[string]any{"terms": map[string]any{"scope": scopes}})
	}
	must = append(must, map[string]any{"multi_match": map[string]any{
		"query": q, "fields": []string{"label^2", "ref"}}})
	raw, _ := json.Marshal(map[string]any{
		"size":  50,
		"query": map[string]any{"bool": map[string]any{"must": must}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.URL+"/"+s.cfg.Index+"/_search", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("search: query status %d", resp.StatusCode)
	}
	var parsed osResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		out = append(out, Result{Scope: h.Source.Scope, ID: h.Source.RefID,
			Label: h.Source.Label, Ref: h.Source.Ref})
	}
	return out, nil
}

// fallbackSearch mirrors MemorySearcher semantics over providers.
func fallbackSearch(ctx context.Context, providers map[string]Provider, entityID int64, q string, scopes []string) ([]Result, error) {
	if len(scopes) == 0 {
		for scope := range providers {
			scopes = append(scopes, scope)
		}
	}
	var out []Result
	for _, scope := range scopes {
		p, ok := providers[scope]
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
