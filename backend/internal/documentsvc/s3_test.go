package documentsvc

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is a minimal in-memory S3 used to verify SigV4 + round trip.
type fakeS3 struct {
	mu   sync.Mutex
	objs map[string][]byte
	auth []string
}

func newFakeS3() (*fakeS3, *httptest.Server) {
	f := &fakeS3{objs: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		key := strings.TrimPrefix(r.URL.EscapedPath(), "/forgeerp/")
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			f.objs[key] = b
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			b, ok := f.objs[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		case http.MethodDelete:
			delete(f.objs, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	return f, srv
}

func testS3Config(srv *httptest.Server) S3Config {
	return S3Config{
		Endpoint:  strings.TrimPrefix(srv.URL, "http://"),
		Bucket:    "forgeerp",
		Region:    "us-east-1",
		AccessKey: "testkey",
		SecretKey: "testsecret",
		Timeout:   5 * time.Second,
	}
}

func TestS3RoundTrip(t *testing.T) {
	f, srv := newFakeS3()
	defer srv.Close()
	s := NewS3Storage(testS3Config(srv))
	s.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	body := []byte("hello documents")
	if err := s.Put(ctx, "invoices/INV-1.pdf", bytes.NewReader(body), int64(len(body)), "application/pdf"); err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := s.Get(ctx, "invoices/INV-1.pdf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("round trip mismatch: %q", got)
	}
	if _, err := s.Get(ctx, "missing.pdf"); err == nil {
		t.Error("missing get accepted")
	}
	if err := s.Delete(ctx, "invoices/INV-1.pdf"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(ctx, "missing.pdf"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.auth) == 0 {
		t.Fatal("no requests seen")
	}
	for _, a := range f.auth {
		if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=testkey/20260907/us-east-1/s3/aws4_request") {
			t.Fatalf("bad auth header: %q", a)
		}
		if !strings.Contains(a, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
			t.Fatalf("bad signed headers: %q", a)
		}
	}
}

func TestS3BackendSelection(t *testing.T) {
	t.Setenv("FERP_STORAGE_BACKEND", "s3")
	if !S3Enabled(func(k, d string) string { return d }) {
		t.Error("s3 backend not selected")
	}
	t.Setenv("FERP_STORAGE_BACKEND", "dir")
	if S3Enabled(func(k, d string) string { return d }) {
		t.Error("dir backend misdetected as s3")
	}
}
