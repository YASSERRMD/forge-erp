package main

// TestOpenAPIRouterMatchesSpec makes the OpenAPI claim true: the served chi
// route set and api/openapi.yaml must agree in BOTH directions. A route
// missing from the spec, or a spec path with no route, fails the test.
// Reconcile drift with the generator (//go:generate in main.go), never by
// hand-editing api/openapi.yaml.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// openAPIHTTPMethods are the OpenAPI operation keys we compare.
var openAPIHTTPMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true,
	"delete": true, "head": true, "options": true, "trace": true,
}

// walkRouteSet builds the served METHOD+path set from the real router with
// stub (database-free) wiring. Registration only captures handlers, so nil
// stores are never touched.
func walkRouteSet() (map[string]bool, error) {
	base := platform.Router(platform.BuildInfo{Version: "test"}, nil)
	mux, ok := base.(chi.Router)
	if !ok {
		return nil, errors.New("platform router is not a chi router")
	}
	mountAPIRoutes(mux, stubWiring())
	out := map[string]bool{}
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[strings.ToUpper(method)+" "+normalizeChiPattern(route)] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// normalizeChiPattern maps chi route patterns onto OpenAPI path-template
// syntax: "{name:regex}" becomes "{name}" (plain "{name}" already matches).
func normalizeChiPattern(route string) string {
	var b strings.Builder
	for i := 0; i < len(route); i++ {
		if route[i] == '{' {
			end := strings.IndexByte(route[i:], '}')
			if end < 0 {
				b.WriteString(route[i:])
				break
			}
			inner := route[i+1 : i+end]
			if cut := strings.IndexByte(inner, ':'); cut >= 0 {
				inner = inner[:cut]
			}
			b.WriteByte('{')
			b.WriteString(inner)
			b.WriteByte('}')
			i += end
			continue
		}
		b.WriteByte(route[i])
	}
	return b.String()
}

// specRouteSet parses api/openapi.yaml and returns the METHOD+path set,
// merging duplicate path blocks (their method maps union) and reporting
// duplicate path keys (which silently drop methods for naive YAML readers).
func specRouteSet(t *testing.T) (map[string]bool, []string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	specPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "api", "openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	paths := findMapping(doc.Content, "paths")
	if paths == nil {
		t.Fatal("spec has no top-level paths mapping")
	}
	out := map[string]bool{}
	seen := map[string]int{}
	for i := 0; i+1 < len(paths.Content); i += 2 {
		p := paths.Content[i].Value
		if !strings.HasPrefix(p, "/") {
			continue
		}
		seen[p]++
		ops := paths.Content[i+1]
		if ops.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(ops.Content); j += 2 {
			m := strings.ToLower(ops.Content[j].Value)
			if openAPIHTTPMethods[m] {
				out[strings.ToUpper(m)+" "+p] = true
			}
		}
	}
	var dups []string
	for p, n := range seen {
		if n > 1 {
			dups = append(dups, p)
		}
	}
	sort.Strings(dups)
	return out, dups
}

// findMapping returns the value node for key under a document/mapping node.
func findMapping(content []*yaml.Node, key string) *yaml.Node {
	for _, n := range content {
		if n.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
	}
	return nil
}

func diffKeys(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestOpenAPIRouterMatchesSpec(t *testing.T) {
	routes, err := walkRouteSet()
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("walked zero routes; router construction is broken")
	}
	spec, dups := specRouteSet(t)

	missing := diffKeys(routes, spec) // served but undocumented
	orphans := diffKeys(spec, routes) // documented but not served

	if len(dups) > 0 {
		t.Errorf("spec has %d duplicate path keys (later blocks hide earlier methods):\n  %s",
			len(dups), strings.Join(dups, "\n  "))
	}
	if len(missing) > 0 {
		t.Errorf("router serves %d routes missing from api/openapi.yaml:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(orphans) > 0 {
		t.Errorf("spec declares %d paths with no router route:\n  %s",
			len(orphans), strings.Join(orphans, "\n  "))
	}
	if len(dups)+len(missing)+len(orphans) > 0 {
		t.Logf("reconcile via the spec generator, never by hand-editing api/openapi.yaml")
	}
}
