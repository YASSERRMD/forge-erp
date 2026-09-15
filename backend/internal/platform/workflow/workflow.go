// Package workflow implements Kernel-adjacent configurable automatic
// actions on status change (Parity Build Order Phase 2): a rule table in
// code plus per-entity overrides — on {context, from → to} do
// {webhook | hook | field-set} — replacing fixed kernel transition tables
// where adopted (first adoption: members subscriptions).
//
// The engine is pure: Fire matches rules and returns the actions without
// performing I/O. Callers execute effects themselves: hook actions publish
// platform.Bus events (or run hook-bus handlers), webhook actions POST via
// Deliver, field-set actions merge into the caller's payload via ApplyFields.
package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ActionKind selects the effect a fired rule performs.
type ActionKind string

const (
	// ActionHook publishes/fires a named hook (platform.Bus subject or
	// hook-bus context, chosen by the adopting context).
	ActionHook ActionKind = "hook"
	// ActionWebhook POSTs the transition JSON to Target (an https URL).
	ActionWebhook ActionKind = "webhook"
	// ActionFieldSet merges Fields into the caller's payload map.
	ActionFieldSet ActionKind = "field-set"
)

// Action is one effect of a fired rule.
type Action struct {
	Kind   ActionKind     `json:"kind"`
	Target string         `json:"target,omitempty"` // hook subject or webhook URL
	Fields map[string]any `json:"fields,omitempty"` // field-set payload
}

// Validate checks action invariants.
func (a Action) Validate() error {
	switch a.Kind {
	case ActionHook:
		if strings.TrimSpace(a.Target) == "" {
			return errors.New("workflow: hook action needs a target")
		}
	case ActionWebhook:
		u := strings.TrimSpace(a.Target)
		if !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("workflow: webhook target must be https, got %q", a.Target)
		}
	case ActionFieldSet:
		if len(a.Fields) == 0 {
			return errors.New("workflow: field-set action needs fields")
		}
	default:
		return fmt.Errorf("workflow: unknown action kind %q", a.Kind)
	}
	return nil
}

// Rule fires Actions when an entity in Context moves From → To.
// From or To may be "*" to match any side (but not both — that would fire
// on every transition).
type Rule struct {
	Context string   `json:"context"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Actions []Action `json:"actions"`
}

// Validate checks rule invariants.
func (r Rule) Validate() error {
	if strings.TrimSpace(r.Context) == "" {
		return errors.New("workflow: rule needs a context")
	}
	if r.From == "*" && r.To == "*" {
		return errors.New("workflow: rule cannot wildcard both sides")
	}
	if len(r.Actions) == 0 {
		return errors.New("workflow: rule needs at least one action")
	}
	for i := range r.Actions {
		if err := r.Actions[i].Validate(); err != nil {
			return fmt.Errorf("workflow: rule %s %s→%s action %d: %w",
				r.Context, r.From, r.To, i, err)
		}
	}
	return nil
}

// matches reports whether the rule fires for a transition.
func (r Rule) matches(context, from, to string) bool {
	if r.Context != context {
		return false
	}
	if r.From != "*" && r.From != from {
		return false
	}
	if r.To != "*" && r.To != to {
		return false
	}
	return true
}

// Transition is one status change presented to the engine.
type Transition struct {
	Context  string         `json:"context"`
	EntityID int64          `json:"entity_id"`
	ID       int64          `json:"id"`
	From     string         `json:"from"`
	To       string         `json:"to"`
	Payload  map[string]any `json:"payload,omitempty"`
}

// Engine holds the code rule table plus per-entity overrides. An entity
// override for a context replaces the code rules for that context (explicit
// beats default; no silent merging). Safe for concurrent use.
type Engine struct {
	mu      sync.RWMutex
	rules   []Rule
	configs map[int64]map[string][]Rule // entity → context → rules
}

// New builds an engine over the code rule table.
func New(rules ...Rule) (*Engine, error) {
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return nil, err
		}
	}
	return &Engine{rules: append([]Rule(nil), rules...), configs: map[int64]map[string][]Rule{}}, nil
}

// Add appends code rules (validated; invalid rules are rejected).
func (e *Engine) Add(rules ...Rule) error {
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, rules...)
	return nil
}

// SetEntityRules installs a per-entity override for one context: while set,
// Fire for (entity, context) uses only these rules. Pass nil/empty to clear
// the override and fall back to the code table.
func (e *Engine) SetEntityRules(entityID int64, context string, rules []Rule) error {
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return err
		}
		if rules[i].Context != context {
			return fmt.Errorf("workflow: entity rule context %q != %q", rules[i].Context, context)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(rules) == 0 {
		if m := e.configs[entityID]; m != nil {
			delete(m, context)
		}
		return nil
	}
	if e.configs[entityID] == nil {
		e.configs[entityID] = map[string][]Rule{}
	}
	e.configs[entityID][context] = append([]Rule(nil), rules...)
	return nil
}

// Fire returns the actions for a transition, in rule order. No match
// returns nil (callers treat that as "no automatic actions").
func (e *Engine) Fire(t Transition) []Action {
	e.mu.RLock()
	defer e.mu.RUnlock()
	table := e.rules
	if m := e.configs[t.EntityID]; m != nil {
		if over, ok := m[t.Context]; ok {
			table = over
		}
	}
	var out []Action
	for _, r := range table {
		if r.matches(t.Context, t.From, t.To) {
			out = append(out, r.Actions...)
		}
	}
	return out
}

// ApplyFields merges every field-set action of actions into payload (later
// actions win per key) and returns the merged map.
func ApplyFields(payload map[string]any, actions []Action) map[string]any {
	out := map[string]any{}
	for k, v := range payload {
		out[k] = v
	}
	for _, a := range actions {
		if a.Kind != ActionFieldSet {
			continue
		}
		for k, v := range a.Fields {
			out[k] = v
		}
	}
	return out
}

// WebhookClient posts transition JSON to webhook targets.
type WebhookClient struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// Deliver POSTs t (plus the action) as JSON to every webhook action's
// target. Delivery is best-effort per target: the first error aborts the
// batch and is returned (callers decide retry policy; the engine itself
// stays side-effect free).
func (c *WebhookClient) Deliver(ctx context.Context, t Transition, actions []Action) error {
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	for _, a := range actions {
		if a.Kind != ActionWebhook {
			continue
		}
		body, err := json.Marshal(map[string]any{"target": a.Target, "transition": t})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Target, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("workflow: webhook %s: %w", a.Target, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("workflow: webhook %s: status %d", a.Target, resp.StatusCode)
		}
	}
	return nil
}
