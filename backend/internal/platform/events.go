package platform

import (
	"context"
	"sync"
)

// Event is the unit of cross-module communication.
// Subject convention: forgeerp.<context>.<event>.v1 (MASTER §3).
type Event struct {
	Subject string
	Entity  string
	ID      int64
	ActorID *int64
	Payload map[string]any
}

// Bus is the publish/subscribe contract. The in-process MemoryBus implements it
// today; a NATS JetStream adapter can replace it without touching modules.
type Bus interface {
	Publish(ctx context.Context, e Event) error
	Subscribe(subject string, fn func(ctx context.Context, e Event)) (unsubscribe func())
}

// MemoryBus is a goroutine-safe synchronous fan-out bus for the monolith stage.
type MemoryBus struct {
	mu   sync.RWMutex
	subs map[string][]func(ctx context.Context, e Event)
}

// NewMemoryBus builds an empty bus.
func NewMemoryBus() *MemoryBus { return &MemoryBus{subs: map[string][]func(ctx context.Context, e Event){}} }

// Publish delivers e to all subscribers of its exact subject, in order.
func (b *MemoryBus) Publish(ctx context.Context, e Event) error {
	b.mu.RLock()
	subs := append([]func(ctx context.Context, e Event){}, b.subs[e.Subject]...)
	b.mu.RUnlock()
	for _, fn := range subs {
		fn(ctx, e)
	}
	return nil
}

// Subscribe registers fn for subject; the returned func removes the registration.
func (b *MemoryBus) Subscribe(subject string, fn func(ctx context.Context, e Event)) func() {
	b.mu.Lock()
	b.subs[subject] = append(b.subs[subject], fn)
	idx := len(b.subs[subject]) - 1
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		s := b.subs[subject]
		if idx < len(s) {
			b.subs[subject] = append(s[:idx], s[idx+1:]...)
		}
	}
}
