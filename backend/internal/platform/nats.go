package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

// NATS stream/consumer settings for the ForgeERP event backbone.
const (
	natsStream   = "forgeerp"
	natsSubjects = "forgeerp.>"
)

// NATSConfig carries event-bus settings (env FERP_BUS_*/FERP_NATS_*).
type NATSConfig struct {
	Backend string // memory | nats
	URL     string
	Timeout time.Duration
}

// LoadNATSConfig reads bus settings (defaults: in-process memory bus).
func LoadNATSConfig(get func(key, def string) string) NATSConfig {
	if v := os.Getenv("FERP_BUS_BACKEND"); v != "" {
		return NATSConfig{Backend: v, URL: os.Getenv("FERP_NATS_URL"), Timeout: 5 * time.Second}
	}
	url := get("FERP_NATS_URL", "nats://localhost:4222")
	if url == "" {
		url = "nats://localhost:4222"
	}
	return NATSConfig{Backend: get("FERP_BUS_BACKEND", "memory"), URL: url, Timeout: 5 * time.Second}
}

// NATSBus implements Bus over NATS JetStream: published events persist in
// the forgeerp stream; subscribers consume from it with explicit acks.
// Exactly-once is NOT guaranteed (at-least-once: handlers must be idempotent
// — all state transitions in this repo are, via status guards + row versions).
type NATSBus struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// ConnectNATS dials NATS, ensures the forgeerp stream, and returns the bus.
// Callers fall back to NewMemoryBus when this errors (broker unreachable).
func ConnectNATS(cfg NATSConfig) (*NATSBus, error) {
	if cfg.URL == "" {
		return nil, errors.New("platform: empty NATS URL")
	}
	nc, err := nats.Connect(cfg.URL, nats.Timeout(cfg.Timeout),
		nats.MaxReconnects(5), nats.ReconnectWait(time.Second))
	if err != nil {
		return nil, fmt.Errorf("platform: nats dial: %w", err)
	}
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("platform: jetstream: %w", err)
	}
	_, err = js.AddStream(&nats.StreamConfig{Name: natsStream, Subjects: []string{natsSubjects}})
	if err != nil && !errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
		nc.Close()
		return nil, fmt.Errorf("platform: ensure stream: %w", err)
	}
	return &NATSBus{nc: nc, js: js}, nil
}

// Close drains and closes the connection.
func (b *NATSBus) Close() {
	if b == nil || b.nc == nil {
		return
	}
	_ = b.nc.Drain()
	b.nc.Close()
}

// Publish persists the event to the stream (subject = event subject).
func (b *NATSBus) Publish(ctx context.Context, e Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = b.js.Publish(e.Subject, raw, nats.Context(ctx))
	return err
}

// Subscribe consumes new messages on subject, acking after fn returns.
func (b *NATSBus) Subscribe(subject string, fn func(ctx context.Context, e Event)) (unsubscribe func()) {
	sub, err := b.js.Subscribe(subject, func(msg *nats.Msg) {
		var e Event
		if err := json.Unmarshal(msg.Data, &e); err != nil {
			_ = msg.Nak()
			return
		}
		fn(context.Background(), e)
		_ = msg.Ack()
	}, nats.DeliverNew(), nats.AckExplicit(), nats.ManualAck())
	if err != nil {
		return func() {}
	}
	return func() {
		_ = sub.Unsubscribe()
	}
}
