package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestReqLoggerCorrelation(t *testing.T) {
	// Exercise the real chi RequestID middleware: the ID it injects must be
	// visible through RequestIDOf / ReqLogger (the correlation contract).
	var seen string
	var loggerOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDOf(r)
		loggerOK = ReqLogger(r) != nil
	})
	req := httptest.NewRequest("GET", "/", nil)
	middleware.RequestID(next).ServeHTTP(httptest.NewRecorder(), req)
	if seen == "" {
		t.Fatal("RequestIDOf empty behind chi RequestID middleware")
	}
	if !loggerOK {
		t.Fatal("nil logger")
	}
	// No RequestID → empty correlation, still usable.
	req2 := httptest.NewRequest("GET", "/", nil)
	if got := RequestIDOf(req2); got != "" {
		t.Fatalf("RequestIDOf w/o middleware = %q, want empty", got)
	}
	if NewLogger() == nil || Default() == nil {
		t.Fatal("nil process logger")
	}
}
