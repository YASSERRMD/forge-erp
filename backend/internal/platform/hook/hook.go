// Package hook implements Kernel 2 (hook bus) from the Parity Build Order
// Phase 1: synchronous, in-transaction extension points for bounded contexts.
//
// Hooks differ from platform.Bus events: events are async post-commit fan-out
// (MemoryBus/NATS); hooks run synchronously INSIDE the caller's transaction so
// an upstream module can validate, adjust, or veto an operation before any
// write commits. A non-nil Result.Veto aborts the operation and the caller
// returns the veto error so platform.Tx / platform.TxEntity rolls back.
//
// Lifecycle contract for handler authors:
//   - Read-only inspection by default; adjust via Result.Mutate, which the bus
//     applies to the subject pointer (e.g. *pos.CheckoutHookSubject).
//   - Extra outputs go in Result.Data; the bus merges every handler's Data
//     into the returned Result (later handlers win on key collisions).
//   - Handlers run ordered by registered priority, ascending (lower first);
//     ties keep registration order. A handler that returns a non-nil error or
//     a non-nil Veto stops the chain: later handlers do not run.
//   - Veto errors SHOULD wrap a platform sentinel (usually
//     platform.ErrValidation) with %w so errors.Is and platform.ErrorCode keep
//     working across package boundaries.
//   - The bus never opens its own transaction: it passes ctx and the caller's
//     pgx.Tx through untouched (entityID threading preserved). tx may be nil
//     in memory-fake paths (Pool == nil); handlers that need SQL must tolerate
//     a nil tx or only register on PG-backed services.
//   - Money is never converted here: amounts stay int64 minor units throughout;
//     this package performs no arithmetic on monetary values.
//
// The ~10 Context constants below are the kernel's well-known extension
// points. The bus carries well past thirty contexts as the parity surface
// grows — new extension points land HERE as Context constants, not as new
// event subjects or new service seams.
package hook

import (
	"context"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Context names a synchronous extension point.
type Context string

// Well-known hook contexts (Kernel 2 lifecycle surface).
const (
	// SalesDocumentCreate runs before a sales document row is inserted.
	SalesDocumentCreate Context = "sales.document.create"
	// SalesDocumentValidate runs before a sales document is validated.
	SalesDocumentValidate Context = "sales.document.validate"
	// SalesDocumentConvert runs before a sales document converts type
	// (e.g. quote → invoice).
	SalesDocumentConvert Context = "sales.document.convert"
	// SalesDocumentCancel runs before a sales document is cancelled/voided.
	SalesDocumentCancel Context = "sales.document.cancel"
	// POSCheckoutValidate runs inside pos checkoutOn after the session and
	// terminal are loaded but BEFORE any writes. The subject is a
	// *pos.CheckoutHookSubject pointer so Mutate can adjust the command.
	POSCheckoutValidate Context = "pos.checkout.validate"
	// PricingLine runs before a document line price snapshot is taken.
	PricingLine Context = "pricing.line"
	// NumberingNext runs before the next document number is issued.
	NumberingNext Context = "numbering.next"
	// DocRender runs before a document is rendered (PDF/print payload).
	DocRender Context = "doc.render"
	// ListFilter runs before a list query executes; Mutate may narrow filters.
	ListFilter Context = "list.filter"
	// DetailEnrich runs before a detail payload is returned; Data merges into
	// the response extras.
	DetailEnrich Context = "detail.enrich"
)

// Result is one handler's vote on the in-flight operation.
type Result struct {
	// Veto, when non-nil, aborts the chain: Execute returns it and later
	// handlers do not run. Wrap a platform sentinel with %w.
	Veto error
	// Mutate, when non-nil, is applied by the bus to the subject pointer
	// immediately after the handler returns (even on the vetoing handler).
	Mutate func(any) error
	// Data merges into Execute's accumulated result (later wins per key).
	Data map[string]any
}

// Handler inspects (and optionally votes on) subject inside the caller's
// transaction. subject is always a pointer chosen by the calling context.
type Handler func(ctx context.Context, tx pgx.Tx, c Context, subject any) (Result, error)

type registration struct {
	priority int
	seq      int
	h        Handler
}

// Bus is the synchronous hook registry. The zero value is unusable;
// build one with NewBus. It is safe for concurrent use.
type Bus struct {
	mu       sync.RWMutex
	handlers map[Context][]registration
	seq      int
}

// NewBus builds an empty hook bus.
func NewBus() *Bus { return &Bus{handlers: map[Context][]registration{}} }

// Register adds h for context c. Lower priority values run first; equal
// priorities run in registration order. A nil h is ignored.
func (b *Bus) Register(c Context, priority int, h Handler) {
	if b == nil || h == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.handlers[c] = append(b.handlers[c], registration{priority: priority, seq: b.seq, h: h})
}

// Execute runs c's handlers in priority order inside the caller's
// transaction (ctx and tx pass through untouched; tx may be nil on
// memory-fake paths). Each handler's Mutate applies to subject and its Data
// merges into the returned Result. A handler error or non-nil Veto aborts the
// chain and is returned; the caller's transaction helper rolls back when the
// caller propagates the error. A nil bus is a no-op returning empty Data.
func (b *Bus) Execute(ctx context.Context, tx pgx.Tx, c Context, subject any) (Result, error) {
	out := Result{Data: map[string]any{}}
	if b == nil {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	b.mu.RLock()
	regs := append([]registration(nil), b.handlers[c]...)
	b.mu.RUnlock()
	sort.SliceStable(regs, func(i, j int) bool {
		if regs[i].priority != regs[j].priority {
			return regs[i].priority < regs[j].priority
		}
		return regs[i].seq < regs[j].seq
	})
	for _, r := range regs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res, err := r.h(ctx, tx, c, subject)
		for k, v := range res.Data {
			out.Data[k] = v
		}
		if err != nil {
			return out, err
		}
		if res.Mutate != nil {
			if err := res.Mutate(subject); err != nil {
				return out, err
			}
		}
		if res.Veto != nil {
			out.Veto = res.Veto
			return out, res.Veto
		}
	}
	return out, nil
}
