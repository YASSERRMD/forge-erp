// Package module is Kernel 1 of the Parity Build Order Phase 1: the module
// registry and rights catalogue.
//
// Today main.go hand-wires ~28 contexts (every Routes() call listed by hand).
// The registry replaces that with dependency-ordered activation: each bounded
// context implements Module, Register() records it, and Ordered() returns the
// boot order so dependents start after their dependencies. Per-entity
// enable/disable flags persist in ferp_modules (migration 0025); rights are
// granted into the existing ferp_rights table (migration 0002) on activation.
//
// Ground rules (inherited from the platform kernel): entityID scopes every
// repository method, failures use the platform sentinels (ErrNotFound,
// ErrConflict, ErrValidation, ErrUnauthorized) so handlers can map them with
// platform.ErrorCode, and there is no money here (int64 cents live in the
// finance/sales contexts, not the registry).
package module

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Family groups modules in SPA navigation (Dolibarr: $family in modX.class.php).
type Family string

// Module families (Dolibarr naming: crm, products, financial, hr, tools, portal).
const (
	FamilyCore      Family = "core"
	FamilyCRM       Family = "crm"
	FamilyCatalog   Family = "catalog"
	FamilyFinancial Family = "financial"
	FamilyHR        Family = "hr"
	FamilyTools     Family = "tools"
	FamilyPortal    Family = "portal"
)

// Right is one (module, entity, action) grant triple — the same shape the
// identity context enforces in Require (see identity/auth.go + domain.go).
// It is deliberately redeclared here instead of imported: module lives inside
// platform and identity already imports platform, so importing identity would
// be a cycle.
type Right struct {
	Module string `json:"module"` // e.g. "sales"
	Entity string `json:"entity"` // e.g. "invoice" or "*"
	Action string `json:"action"` // e.g. "read" | "write" | "delete" | "validate"
}

// Key returns the canonical triple string.
func (r Right) Key() string { return r.Module + "." + r.Entity + "." + r.Action }

// Validate checks naming rules shared by grants and Require() calls
// (mirrors identity.Right.Validate: no empties, no whitespace or dots).
func (r Right) Validate() error {
	for name, v := range map[string]string{"module": r.Module, "entity": r.Entity, "action": r.Action} {
		if v == "" {
			return fmt.Errorf("module: right %s is empty", name)
		}
		if strings.ContainsAny(v, " \t.") {
			return fmt.Errorf("module: right %s %q contains whitespace or dot", name, v)
		}
	}
	return nil
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in
// production; same alias shape as every context's handler.go).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Config scopes a module to one tenant at boot/route time.
type Config struct {
	EntityID int64
	// Disabled is the per-module kill switch (populated from ferp_modules by
	// the host; Enabled consults it).
	Disabled map[string]bool
	// Values carries free-form module settings; kernels 2-7 read their keys.
	Values map[string]string
}

// Hooks is the event-subscription surface handed to Module.Subscribe.
//
// FORWARD STUB for kernels 2-7: later kernels may add emit/declare helpers,
// but the Subscribe signature is frozen — it matches platform.Bus exactly so
// *platform.MemoryBus (and the NATS adapter) already satisfy it.
type Hooks interface {
	Subscribe(subject string, fn func(ctx context.Context, e platform.Event)) (unsubscribe func())
}

// --- Forward stubs for kernels 2-7 ---------------------------------------
// These types freeze the Module interface shape now so kernels 4 (numbering),
// 5 (documents) and 7 (dictionaries) can flesh them out later without breaking
// this API. Each is a minimal named struct with Code/Label-style fields on
// purpose: extension must add fields, never rename.

// NumberModel is a forward stub for kernel 4 (document numbering models).
type NumberModel struct {
	Code  string
	Label string
}

// DocModel is a forward stub for kernel 5 (document type models).
type DocModel struct {
	Type  string
	Label string
}

// Dict is a forward stub for kernel 7 (dictionary/lookup tables).
type Dict struct {
	Name  string
	Label string
}

// Module is the contract every bounded context implements to join the
// registry. DependsOn names other modules that must boot first (Ordered()
// topologically sorts on it).
type Module interface {
	Name() string
	Family() Family
	DependsOn() []string
	Rights() []Right
	Migrations() fs.FS
	Numbering() []NumberModel
	Documents() []DocModel
	Dictionaries() []Dict
	Subscribe(Hooks)
	Routes(chi.Router, Middleware)
	Enabled(Config) bool
}

// Base is an embeddable no-op Module: contexts promote it and override only
// Subscribe/Routes (and Migrations when they own schema files). Numbering,
// Documents and Dictionaries default to empty until kernels 4/5/7 land.
type Base struct {
	ModName   string
	ModFamily Family
	ModDeps   []string
	ModRights []Right
}

// Name returns the registry key (e.g. "sales").
func (b Base) Name() string { return b.ModName }

// Family returns the SPA nav group.
func (b Base) Family() Family { return b.ModFamily }

// DependsOn returns boot prerequisites.
func (b Base) DependsOn() []string { return append([]string(nil), b.ModDeps...) }

// Rights returns the module's rights catalogue.
func (b Base) Rights() []Right { return append([]Right(nil), b.ModRights...) }

// Migrations returns the module's schema files (nil until the context owns any).
func (b Base) Migrations() fs.FS { return nil }

// Numbering returns numbering models (empty until kernel 4).
func (b Base) Numbering() []NumberModel { return nil }

// Documents returns document models (empty until kernel 5).
func (b Base) Documents() []DocModel { return nil }

// Dictionaries returns dictionaries (empty until kernel 7).
func (b Base) Dictionaries() []Dict { return nil }

// Subscribe is a no-op (contexts override to bind triggers).
func (b Base) Subscribe(Hooks) {}

// Routes is a no-op (contexts override to mount their surface).
func (b Base) Routes(chi.Router, Middleware) {}

// Enabled honours the per-module kill switch (nil Disabled map = enabled).
func (b Base) Enabled(c Config) bool { return !c.Disabled[b.ModName] }

// Registry is the process-wide module catalogue. Register records modules
// (usually from init() or main); Ordered resolves boot order.
type Registry struct {
	mu   sync.RWMutex
	mods map[string]Module
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{mods: map[string]Module{}} }

// Register records m. Empty names fail with ErrValidation; re-registering a
// name fails with ErrConflict. Every declared right must Validate.
func (r *Registry) Register(m Module) error {
	if m == nil || m.Name() == "" {
		return fmt.Errorf("module: empty module name: %w", platform.ErrValidation)
	}
	for _, right := range m.Rights() {
		if err := right.Validate(); err != nil {
			return fmt.Errorf("module %q: %w: %w", m.Name(), err, platform.ErrValidation)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.mods[m.Name()]; dup {
		return fmt.Errorf("module %q already registered: %w", m.Name(), platform.ErrConflict)
	}
	r.mods[m.Name()] = m
	return nil
}

// Get returns the module registered under name.
func (r *Registry) Get(name string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.mods[name]
	return m, ok
}

// Names returns registered names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.mods))
	for name := range r.mods {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Ordered returns modules topologically sorted so every module boots after its
// DependsOn prerequisites (Kahn's algorithm, alphabetical tie-break for
// determinism). Unknown dependencies and cycles fail with ErrValidation.
func (r *Registry) Ordered() ([]Module, error) {
	r.mu.RLock()
	mods := make(map[string]Module, len(r.mods))
	for name, m := range r.mods {
		mods[name] = m
	}
	r.mu.RUnlock()

	for name, m := range mods {
		for _, dep := range m.DependsOn() {
			if _, ok := mods[dep]; !ok {
				return nil, fmt.Errorf("module %q depends on unknown module %q: %w",
					name, dep, platform.ErrValidation)
			}
			if dep == name {
				return nil, fmt.Errorf("module %q depends on itself: %w",
					name, platform.ErrValidation)
			}
		}
	}

	indeg := make(map[string]int, len(mods))
	dependents := make(map[string][]string, len(mods))
	for name, m := range mods {
		seen := map[string]bool{}
		for _, dep := range m.DependsOn() {
			if !seen[dep] {
				seen[dep] = true
				indeg[name]++
				dependents[dep] = append(dependents[dep], name)
			}
		}
	}
	var ready []string
	for name := range mods {
		if indeg[name] == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	var out []Module
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		out = append(out, mods[name])
		for _, dep := range dependents[name] {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
			}
		}
		sort.Strings(ready)
	}
	if len(out) != len(mods) {
		var cyclic []string
		for name, deg := range indeg {
			if deg > 0 {
				cyclic = append(cyclic, name)
			}
		}
		sort.Strings(cyclic)
		return nil, fmt.Errorf("module: dependency cycle among %s: %w",
			strings.Join(cyclic, ", "), platform.ErrValidation)
	}
	return out, nil
}
