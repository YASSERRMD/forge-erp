package platform

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

// testServer starts an embedded NATS server with JetStream.
func testServer(t *testing.T) string {
	t.Helper()
	opts := &natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

func TestNATSBusRoundTrip(t *testing.T) {
	url := testServer(t)
	bus, err := ConnectNATS(NATSConfig{Backend: "nats", URL: url, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer bus.Close()
	var got atomic.Int64
	unsub := bus.Subscribe("forgeerp.test.event.v1", func(_ context.Context, e Event) {
		if e.Entity == "thing" && e.ID == 7 {
			got.Add(1)
		}
	})
	defer unsub()
	time.Sleep(300 * time.Millisecond) // let the consumer attach
	if err := bus.Publish(context.Background(), Event{Subject: "forgeerp.test.event.v1", Entity: "thing", ID: 7}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for got.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got.Load() != 1 {
		t.Fatalf("deliveries=%d want 1", got.Load())
	}
}

func TestNATSConnectFailure(t *testing.T) {
	if _, err := ConnectNATS(NATSConfig{Backend: "nats", URL: "nats://127.0.0.1:1", Timeout: time.Second}); err == nil {
		t.Error("bad URL accepted")
	}
	if _, err := ConnectNATS(NATSConfig{URL: ""}); err == nil {
		t.Error("empty URL accepted")
	}
	// MemoryBus still satisfies Bus (fallback path keeps its contract).
	var _ Bus = NewMemoryBus()
	var _ Bus = (*NATSBus)(nil)
}
