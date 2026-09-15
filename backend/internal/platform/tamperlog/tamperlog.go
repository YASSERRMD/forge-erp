// Package tamperlog generalizes the hash-chained append-only log (blockedlog
// equivalent): Append chains each record's hash over its predecessor plus an
// arbitrary payload, Verify recomputes every link. Finance keeps its own
// entry chain (finance.Chain/VerifyChain) unchanged; new consumers should
// build on this package instead of rolling a bespoke chain.
package tamperlog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// Genesis anchors new logs (no predecessor).
const Genesis = "tamperlog-genesis"

// Record is one chained log entry. Payload is copied on Append so later
// caller mutation cannot invalidate the stored hash.
type Record struct {
	Seq     uint64 `json:"seq"`
	Prev    string `json:"prev"`
	Hash    string `json:"hash"`
	Payload []byte `json:"payload"`
}

// Hash commits prev and payload to a chain link.
func Hash(prev string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{0})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Log is an in-process append-only chained log.
type Log struct {
	mu      sync.Mutex
	records []Record
}

// Append chains payload to the head and returns the stored record.
func (l *Log) Append(payload []byte) Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := Genesis
	if n := len(l.records); n > 0 {
		prev = l.records[n-1].Hash
	}
	cp := append([]byte(nil), payload...)
	rec := Record{Seq: uint64(len(l.records)), Prev: prev, Hash: Hash(prev, cp), Payload: cp}
	l.records = append(l.records, rec)
	return rec
}

// Records returns a copy of the chain in append order.
func (l *Log) Records() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Record(nil), l.records...)
}

// Verify recomputes every link from genesis; empty logs verify clean.
func (l *Log) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := Genesis
	for i, r := range l.records {
		if r.Prev != prev {
			return fmt.Errorf("tamperlog: chain break at seq %d", i)
		}
		if want := Hash(r.Prev, r.Payload); want != r.Hash {
			return fmt.Errorf("tamperlog: tampered record at seq %d", i)
		}
		prev = r.Hash
	}
	return nil
}
