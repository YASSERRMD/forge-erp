package platform

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustNets(t *testing.T, csv string) []net.IPNet {
	t.Helper()
	nets, err := ParseTrustedProxies(csv)
	if err != nil {
		t.Fatal(err)
	}
	return nets
}

func TestRealIPFromTrustedProxy(t *testing.T) {
	trusted := mustNets(t, "10.0.0.0/8")
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.RemoteAddr))
	})
	h := RealIPFromTrusted(trusted)(echo)

	// Trusted peer: leftmost XFF entry is the client.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.2.3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "203.0.113.9" {
		t.Fatalf("trusted proxy: client = %q want 203.0.113.9", rec.Body.String())
	}

	// Untrusted peer: spoofed XFF ignored, peer stands.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.7:4567"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "198.51.100.7" {
		t.Fatalf("untrusted peer: client = %q want 198.51.100.7", rec.Body.String())
	}

	// Chain: spoofed prefix before the real client is ignored.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.9.9.9:4567"
	req.Header.Set("X-Forwarded-For", "192.0.2.99, 203.0.113.9, 10.9.9.9")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "203.0.113.9" {
		t.Fatalf("chain: client = %q want 203.0.113.9", rec.Body.String())
	}
}

func TestParseTrustedProxiesBareIP(t *testing.T) {
	nets, err := ParseTrustedProxies("10.0.0.5, 2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 2 {
		t.Fatalf("nets = %d want 2", len(nets))
	}
	if _, err := ParseTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("garbage CIDR accepted")
	}
}
