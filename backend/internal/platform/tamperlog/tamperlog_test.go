package tamperlog

import "testing"

func TestAppendVerify(t *testing.T) {
	var l Log
	if err := l.Verify(); err != nil {
		t.Fatalf("empty log: %v", err)
	}
	r1 := l.Append([]byte("first"))
	r2 := l.Append([]byte("second"))
	if r1.Prev != Genesis || r2.Prev != r1.Hash {
		t.Fatal("links not chained")
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestTamperDetected(t *testing.T) {
	var l Log
	l.Append([]byte("a"))
	l.Append([]byte("b"))
	recs := l.Records()
	recs[0].Payload[0] = 'X'
	l.mu.Lock()
	l.records[0] = recs[0]
	l.mu.Unlock()
	if err := l.Verify(); err == nil {
		t.Error("tampered payload passed")
	}
}

func TestBreakDetected(t *testing.T) {
	var l Log
	l.Append([]byte("a"))
	r := l.Append([]byte("b"))
	r.Prev = Genesis
	l.mu.Lock()
	l.records[1] = r
	l.mu.Unlock()
	if err := l.Verify(); err == nil {
		t.Error("broken link passed")
	}
}

func TestPayloadCopied(t *testing.T) {
	var l Log
	p := []byte("mutable")
	l.Append(p)
	p[0] = 'X'
	if err := l.Verify(); err != nil {
		t.Fatalf("caller mutation invalidated log: %v", err)
	}
}
