// Package trigger is the typed event catalogue for the ForgeERP bus
// (Parity Build Order Phase 1, Kernel 3).
//
// Events used to be ad-hoc subject strings published from handlers and
// services with no registry. This package ports the Dolibarr trigger names
// (BILL_VALIDATE, MEMBER_SUBSCRIPTION, ...) as the baseline catalogue: every
// event has a Definition (name, subject, documentation-grade schema,
// version), a Go struct implementing Event, and a durable outbox row in
// ferp_outbox (see outbox.go) so a crash between commit and publish still
// delivers.
package trigger

import (
	"fmt"
	"sort"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Definition describes one catalogue event.
type Definition struct {
	// Name is the Dolibarr-style trigger name, e.g. BILL_VALIDATE.
	Name string
	// Subject is the bus subject, forgeerp.<context>.<event>.v1.
	Subject string
	// Entity is the platform.Event Entity scope, e.g. "expense".
	Entity string
	// Schema maps payload field -> type, documentation grade.
	Schema map[string]string
	// Version is the event schema version (1 matches the .v1 subject suffix).
	Version int
}

// Validate checks definition invariants.
func (d Definition) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("trigger: event name required: %w", platform.ErrValidation)
	}
	if d.Subject == "" {
		return fmt.Errorf("trigger: subject required for %s: %w", d.Name, platform.ErrValidation)
	}
	if d.Version <= 0 {
		return fmt.Errorf("trigger: version must be positive for %s: %w", d.Name, platform.ErrValidation)
	}
	if d.Schema == nil {
		return fmt.Errorf("trigger: schema required for %s: %w", d.Name, platform.ErrValidation)
	}
	if _, ok := d.Schema["entity_id"]; !ok {
		return fmt.Errorf("trigger: schema must document entity_id for %s: %w", d.Name, platform.ErrValidation)
	}
	return nil
}

// Registry is the process-local event catalogue.
type Registry struct {
	mu   sync.RWMutex
	defs map[string]Definition
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{defs: map[string]Definition{}} }

// Register adds def (duplicate names conflict).
func (r *Registry) Register(def Definition) error {
	if err := def.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs[def.Name]; ok {
		return fmt.Errorf("trigger: duplicate event %s: %w", def.Name, platform.ErrAlreadyExists)
	}
	r.defs[def.Name] = def
	return nil
}

// ByName returns the definition for name (unknown names are not found).
func (r *Registry) ByName(name string) (Definition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[name]
	if !ok {
		return Definition{}, fmt.Errorf("trigger: unknown event %s: %w", name, platform.ErrNotFound)
	}
	return def, nil
}

// All returns every definition sorted by name.
func (r *Registry) All() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Definition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// List returns every event name sorted (the API surface enumerates these later).
func (r *Registry) List() []string {
	all := r.All()
	names := make([]string, 0, len(all))
	for _, d := range all {
		names = append(names, d.Name)
	}
	return names
}

// Catalogue returns the baseline definitions (~20, Dolibarr trigger names
// ported across sales/hr/members/pos/procurement). The Def* vars in
// events.go are the single source of truth.
func Catalogue() []Definition {
	return []Definition{
		DefProposalValidate,
		DefProposalClose,
		DefOrderValidate,
		DefOrderClose,
		DefBillValidate,
		DefBillPayed,
		DefPaymentCreated,
		DefLeaveSubmitted,
		DefLeaveApproved,
		DefExpenseReportSubmitted,
		DefExpensePaid,
		DefSalaryValidated,
		DefMemberValidate,
		DefMemberSubscription,
		DefDonationCreated,
		DefPOSSaleCompleted,
		DefPOSSaleReturned,
		DefSupplierOrderValidate,
		DefSupplierOrderReceived,
		DefReceptionReceived,
	}
}

var defaultRegistry = func() *Registry {
	r := NewRegistry()
	for _, d := range Catalogue() {
		if err := r.Register(d); err != nil {
			panic(err)
		}
	}
	return r
}()

// Default returns the process catalogue (all baseline definitions registered).
func Default() *Registry { return defaultRegistry }

// List enumerates every catalogue event name, sorted (API surface later).
func List() []string { return defaultRegistry.List() }

// ByName looks up name in the default catalogue.
func ByName(name string) (Definition, error) { return defaultRegistry.ByName(name) }
