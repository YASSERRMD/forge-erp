//go:build ignore

// Spec generator for api/openapi.yaml (Phase 0 task 9).
//
// Ground rule: api/openapi.yaml is NEVER hand-maintained. This script walks
// the real chi router and reconciles the spec:
//
//   - duplicate path keys are merged (method maps union; on a same-method
//     conflict the FIRST block wins as the original curated content),
//   - walked routes missing from the spec get minimal-but-valid stub entries,
//   - spec paths with no router route are removed as orphans.
//
// The file is edited TEXTUALLY (block move/append/delete honoring the
// 2-space layout) so every untouched byte — comments, flow-map spacing,
// folded scalars — survives verbatim. A YAML round-trip would reformat the
// whole document and is deliberately avoided.
//
// Run from backend/cmd/api:
//
//	go run genspec_openapi.go
//
// or via the //go:generate directive in main.go. The route list below mirrors
// mountAPIRoutes (main.go); TestOpenAPIRouterMatchesSpec (openapi_test.go) is
// authoritative — if this mirror drifts, the test fails loudly.
//
// NOTE: this file carries the "ignore" build tag and is excluded from normal
// builds/tests.
package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/agenda"
	"github.com/YASSERRMD/forge-erp/backend/internal/assets"
	"github.com/YASSERRMD/forge-erp/backend/internal/booking"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/dataio"
	"github.com/YASSERRMD/forge-erp/backend/internal/documentsvc"
	"github.com/YASSERRMD/forge-erp/backend/internal/events"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/fx"
	"github.com/YASSERRMD/forge-erp/backend/internal/hr"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/inbound"
	"github.com/YASSERRMD/forge-erp/backend/internal/kb"
	"github.com/YASSERRMD/forge-erp/backend/internal/manufacturing"
	"github.com/YASSERRMD/forge-erp/backend/internal/members"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/payments"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/cron"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/module"
	"github.com/YASSERRMD/forge-erp/backend/internal/portal"
	"github.com/YASSERRMD/forge-erp/backend/internal/pos"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/reporting"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/YASSERRMD/forge-erp/backend/internal/search"
	"github.com/YASSERRMD/forge-erp/backend/internal/sepa"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
	"github.com/YASSERRMD/forge-erp/backend/internal/survey"
	"github.com/go-chi/chi/v5"
)

var genMethods = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

func genMethodSet() map[string]bool {
	m := map[string]bool{}
	for _, k := range genMethods {
		m[k] = true
	}
	return m
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genspec:", err)
	os.Exit(1)
}

// specRouter mirrors mountAPIRoutes (main.go) with database-free stub deps.
// Registration only captures handlers, so nil stores are never touched.
func specRouter() chi.Router {
	base := platform.Router(platform.BuildInfo{Version: "genspec"}, nil)
	mux, ok := base.(chi.Router)
	if !ok {
		panic("platform router is not a chi router")
	}
	limiter := platform.NewRateLimiter(20, 40)
	idH := identity.NewHandler(identity.Deps{})
	mux.With(limiter.Limit).Route("/api/v1", func(r chi.Router) {
		identity.Routes(r, identity.Deps{})
		partners.Routes(r, partners.Deps{}, idH.Require)
		catalog.Routes(r, catalog.Deps{}, idH.Require)
		sales.Routes(r, sales.Deps{}, idH.Require)
		procurement.Routes(r, procurement.Deps{}, idH.Require)
		finance.Routes(r, finance.Deps{}, idH.Require)
		services.Routes(r, services.Deps{}, idH.Require)
		manufacturing.Routes(r, manufacturing.Deps{}, idH.Require)
		hr.Routes(r, hr.Deps{}, idH.Require)
		pos.Routes(r, pos.Deps{}, idH.Require)
		reporting.Routes(r, reporting.Deps{}, idH.Require)
		payments.Routes(r, payments.Deps{Providers: payments.NewRegistry(), Bus: platform.NewMemoryBus()}, idH.Require)
		booking.Routes(r, booking.Deps{}, idH.Require)
		survey.Routes(r, survey.Deps{}, idH.Require)
		members.Routes(r, members.Deps{}, idH.Require)
		assets.Routes(r, assets.Deps{}, idH.Require)
		kb.Routes(r, kb.Deps{}, idH.Require)
		events.Routes(r, events.Deps{}, idH.Require)
		dataio.Routes(r, dataio.Deps{}, idH.Require)
		fx.Routes(r, fx.Deps{}, idH.Require)
		sepa.Routes(r, sepa.Deps{}, idH.Require)
		inbound.Routes(r, inbound.Deps{}, idH.Require)
		agenda.Routes(r, agenda.Deps{}, idH.Require)
		module.Routes(r, module.Deps{Registry: module.NewRegistry()}, idH.Require)
		portal.Routes(r, portal.Deps{}, idH.Require)
		cron.Routes(r, cron.Deps{}, idH.Require)
		documentsvc.Routes(r, &documentsvc.Service{}, idH.Require)
		search.Routes(r, search.NewMemorySearcher(), idH.Require)
	})
	mux.With(limiter.Limit).Get("/public/share/{token}", documentsvc.PublicShare(&documentsvc.Service{}))
	return mux
}

// normalizePattern maps chi route patterns onto OpenAPI path-template syntax.
func normalizePattern(route string) string {
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

// stubOperationID derives a deterministic placeholder operationId, e.g.
// GET /api/v1/manufacturing/boms/{id}/lines -> getStubManufacturingBomsIdLines.
// The leading api/v1 segments carry no signal and are dropped.
func stubOperationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	b.WriteString("Stub")
	var segs []string
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	for len(segs) >= 2 && segs[0] == "api" && segs[1] == "v1" {
		segs = segs[2:]
	}
	for _, seg := range segs {
		seg = strings.Trim(seg, "{}")
		seg = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return ' '
		}, seg)
		for _, w := range strings.Fields(seg) {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

// pathLine reports whether line is a 2-space OpenAPI path key under paths:.
func pathLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
		return "", false
	}
	rest := strings.TrimSpace(line[2:])
	if !strings.HasPrefix(rest, "/") || !strings.HasSuffix(rest, ":") {
		return "", false
	}
	return strings.TrimSuffix(rest, ":"), true
}

// methodLine reports whether line is a 4-space HTTP method key.
func methodLine(line string, methods map[string]bool) (string, bool) {
	if !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "     ") {
		return "", false
	}
	rest := strings.TrimSpace(line[4:])
	if !strings.HasSuffix(rest, ":") {
		return "", false
	}
	m := strings.ToLower(strings.TrimSuffix(rest, ":"))
	if !methods[m] {
		return "", false
	}
	return m, true
}

func main() {
	methods := genMethodSet()

	// 0. Walk the real router.
	mux := specRouter()
	walked := map[string]bool{} // "METHOD path"
	if err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		walked[strings.ToUpper(method)+" "+normalizePattern(route)] = true
		return nil
	}); err != nil {
		fail(err)
	}

	specPath := filepath.Join("..", "..", "..", "api", "openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		fail(err)
	}
	text := strings.TrimSuffix(string(raw), "\n")
	lines := strings.Split(text, "\n")

	// Locate the top-level "paths:" key; everything before it is untouched.
	pathsAt := -1
	for i, l := range lines {
		if l == "paths:" {
			pathsAt = i
			break
		}
	}
	if pathsAt < 0 {
		fail(fmt.Errorf("no top-level paths: key"))
	}

	type block struct {
		path     string
		start    int               // path key line
		end      int               // exclusive: next path line or EOF
		meths    map[string][2]int // method -> [start,end) line range of its sub-block
		methList []string          // in file order
	}
	var blocks []*block
	for i := pathsAt + 1; i < len(lines); {
		p, ok := pathLine(lines[i])
		if !ok {
			i++
			continue
		}
		b := &block{path: p, start: i, meths: map[string][2]int{}}
		j := i + 1
		for j < len(lines) {
			if _, ok := pathLine(lines[j]); ok {
				break
			}
			if m, ok := methodLine(lines[j], methods); ok {
				ms := j
				j++
				for j < len(lines) {
					if _, ok := pathLine(lines[j]); ok {
						break
					}
					if _, ok := methodLine(lines[j], methods); ok {
						break
					}
					j++
				}
				b.meths[m] = [2]int{ms, j}
				b.methList = append(b.methList, m)
				continue
			}
			j++
		}
		b.end = j
		blocks = append(blocks, b)
		i = j
	}

	// 1. Merge duplicate path blocks into the first occurrence (textual move of
	// method sub-blocks; same-method conflict keeps the FIRST block).
	first := map[string]*block{}
	var reportMerge []string
	type edit struct {
		at       int      // line index in first block after which to insert
		blob     []string // method sub-block lines to insert
		delStart int      // dup block path-key line
		delEnd   int      // dup block end (exclusive)
		note     string
	}
	var edits []edit
	for _, b := range blocks {
		f, seen := first[b.path]
		if !seen {
			first[b.path] = b
			continue
		}
		for _, m := range b.methList {
			r := b.meths[m]
			if _, has := f.meths[m]; has {
				reportMerge = append(reportMerge, fmt.Sprintf("%s %s (kept first block)", strings.ToUpper(m), b.path))
				continue
			}
			edits = append(edits, edit{at: f.end - 1, blob: append([]string{}, lines[r[0]:r[1]]...), note: fmt.Sprintf("%s %s (unioned from duplicate block)", strings.ToUpper(m), b.path)})
			reportMerge = append(reportMerge, fmt.Sprintf("%s %s (unioned from duplicate block)", strings.ToUpper(m), b.path))
		}
		edits = append(edits, edit{delStart: b.start, delEnd: b.end, note: ""})
	}
	// Apply insertions (descending line order so indexes stay valid), then deletions.
	type insertion struct {
		at   int
		blob []string
	}
	var inserts []insertion
	for _, e := range edits {
		if len(e.blob) > 0 {
			inserts = append(inserts, insertion{e.at, e.blob})
		}
	}
	sort.Slice(inserts, func(a, b int) bool {
		if inserts[a].at != inserts[b].at {
			return inserts[a].at > inserts[b].at
		}
		return len(inserts[a].blob) < len(inserts[b].blob)
	})
	for _, in := range inserts {
		lines = append(lines[:in.at+1], append(append([]string{}, in.blob...), lines[in.at+1:]...)...)
	}
	// Recompute block ranges after insertions by rescanning (simplest correct).
	blocks = nil
	for i := pathsAt + 1; i < len(lines); {
		p, ok := pathLine(lines[i])
		if !ok {
			i++
			continue
		}
		b := &block{path: p, start: i, meths: map[string][2]int{}}
		j := i + 1
		for j < len(lines) {
			if _, ok := pathLine(lines[j]); ok {
				break
			}
			if m, ok := methodLine(lines[j], methods); ok {
				ms := j
				j++
				for j < len(lines) {
					if _, ok := pathLine(lines[j]); ok {
						break
					}
					if _, ok := methodLine(lines[j], methods); ok {
						break
					}
					j++
				}
				b.meths[m] = [2]int{ms, j}
				b.methList = append(b.methList, m)
				continue
			}
			j++
		}
		b.end = j
		blocks = append(blocks, b)
		i = j
	}
	// Delete duplicate blocks (all but first occurrence), descending order.
	seenPath := map[string]bool{}
	type span struct{ s, e int }
	var spans []span
	for _, b := range blocks {
		if seenPath[b.path] {
			spans = append(spans, span{b.start, b.end})
			continue
		}
		seenPath[b.path] = true
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a].s > spans[b].s })
	for _, s := range spans {
		lines = append(lines[:s.s], lines[s.e:]...)
	}

	// Rescan for the orphan/stub passes.
	blocks = nil
	for i := pathsAt + 1; i < len(lines); {
		p, ok := pathLine(lines[i])
		if !ok {
			i++
			continue
		}
		b := &block{path: p, start: i, meths: map[string][2]int{}}
		j := i + 1
		for j < len(lines) {
			if _, ok := pathLine(lines[j]); ok {
				break
			}
			if m, ok := methodLine(lines[j], methods); ok {
				ms := j
				j++
				for j < len(lines) {
					if _, ok := pathLine(lines[j]); ok {
						break
					}
					if _, ok := methodLine(lines[j], methods); ok {
						break
					}
					j++
				}
				b.meths[m] = [2]int{ms, j}
				b.methList = append(b.methList, m)
				continue
			}
			j++
		}
		b.end = j
		blocks = append(blocks, b)
		i = j
	}
	specSet := map[string]bool{}
	for _, b := range blocks {
		for _, m := range b.methList {
			specSet[strings.ToUpper(m)+" "+b.path] = true
		}
	}

	// 2. Remove orphan spec methods (no router route); drop path blocks left empty.
	var orphans []string
	var delSpans []span
	for _, b := range blocks {
		var methodSpans []span
		for _, m := range b.methList {
			key := strings.ToUpper(m) + " " + b.path
			if walked[key] {
				continue
			}
			orphans = append(orphans, key)
			r := b.meths[m]
			methodSpans = append(methodSpans, span{r[0], r[1]})
		}
		if len(methodSpans) == len(b.methList) && len(b.methList) > 0 {
			delSpans = append(delSpans, span{b.start, b.end})
		} else {
			delSpans = append(delSpans, methodSpans...)
		}
	}
	sort.Slice(delSpans, func(a, b int) bool { return delSpans[a].s > delSpans[b].s })
	for _, s := range delSpans {
		lines = append(lines[:s.s], lines[s.e:]...)
	}

	// 3. Add stubs for walked routes missing from the spec (sorted). A stub
	// whose path already has a block is inserted into that block; otherwise
	// a new path block is appended at EOF (never a duplicate path key).
	specSet2 := map[string]bool{}
	{
		// recompute cheaply from remaining blocks is overkill; reuse specSet minus orphans
		for k := range specSet {
			dropped := false
			for _, o := range orphans {
				if o == k {
					dropped = true
					break
				}
			}
			if !dropped {
				specSet2[k] = true
			}
		}
	}
	type stub struct{ method, path string }
	var stubs []stub
	for key := range walked {
		if !specSet2[key] {
			m, p, _ := strings.Cut(key, " ")
			stubs = append(stubs, stub{m, p})
		}
	}
	sort.Slice(stubs, func(a, b int) bool {
		if stubs[a].path != stubs[b].path {
			return stubs[a].path < stubs[b].path
		}
		return stubs[a].method < stubs[b].method
	})
	// path -> end (exclusive) line index of its block, rescanned post-deletion.
	pathEnd := map[string]int{}
	for i := pathsAt + 1; i < len(lines); {
		p, ok := pathLine(lines[i])
		if !ok {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) {
			if _, ok := pathLine(lines[j]); ok {
				break
			}
			j++
		}
		pathEnd[p] = j
		i = j
	}
	stubMethodLines := func(s stub) []string {
		code := "200"
		if s.method == "POST" {
			code = "201"
		}
		return []string{
			"    " + strings.ToLower(s.method) + ":",
			"      operationId: " + stubOperationID(s.method, s.path),
			"      summary: STUB (generated) - route is served but undocumented; fill in contract details",
			"      responses:",
			"        '" + code + "': { description: OK }",
		}
	}
	type lineInsert struct {
		at   int
		blob []string
	}
	// Group stub method blocks per existing path (stubs arrive sorted, so
	// same-path blobs stay adjacent), then insert once per path.
	var stubInserts []lineInsert
	for i := 0; i < len(stubs); {
		j := i
		var blob []string
		for j < len(stubs) && stubs[j].path == stubs[i].path {
			blob = append(blob, stubMethodLines(stubs[j])...)
			j++
		}
		if end, ok := pathEnd[stubs[i].path]; ok {
			stubInserts = append(stubInserts, lineInsert{at: end - 1, blob: blob})
		} else {
			lines = append(lines, append([]string{"  " + stubs[i].path + ":"}, blob...)...)
			pathEnd[stubs[i].path] = len(lines)
		}
		i = j
	}
	// Insert into existing blocks in descending line order.
	sort.Slice(stubInserts, func(a, b int) bool { return stubInserts[a].at > stubInserts[b].at })
	for _, in := range stubInserts {
		lines = append(lines[:in.at+1], append(append([]string{}, in.blob...), lines[in.at+1:]...)...)
	}

	out := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(specPath, []byte(out), 0o644); err != nil {
		fail(err)
	}

	sort.Strings(reportMerge)
	sort.Strings(orphans)
	fmt.Println("genspec: merged duplicate blocks:")
	for _, m := range reportMerge {
		fmt.Println("  merge:", m)
	}
	fmt.Println("genspec: added stubs:")
	for _, s := range stubs {
		fmt.Println("  stub: ", s.method, s.path)
	}
	fmt.Println("genspec: removed orphans:")
	for _, o := range orphans {
		fmt.Println("  orphan:", o)
	}
}
