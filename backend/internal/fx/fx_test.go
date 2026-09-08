package fx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestConvertMath(t *testing.T) {
	eur := Rate{Code: "EUR", RateToBase: 1080000}
	usd := Rate{Code: "USD", RateToBase: 1000000}
	if got := Convert(10000, eur, usd); got != 10800 {
		t.Fatalf("eur->usd=%d want 10800", got)
	}
	if got := Convert(10800, usd, eur); got != 10000 {
		t.Fatalf("usd->eur=%d want 10000", got)
	}
	if got := Convert(123, usd, usd); got != 123 {
		t.Fatalf("identity=%d", got)
	}
	if err := (&Rate{EntityID: 1, Code: "US", RateToBase: 1}).Validate(); err == nil {
		t.Error("bad code accepted")
	}
}

func TestBoardAPI(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := do(http.MethodPost, "/api/v1/fx/rates", `{"code":"eur","rate_to_base":1080000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = do(http.MethodGet, "/api/v1/fx/convert?amount=10000&from=EUR&to=USD", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("convert: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Result int64 `json:"result"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.Result != 10800 {
		t.Fatalf("result=%d want 10800", out.Result)
	}
	rec = do(http.MethodGet, "/api/v1/fx/convert?amount=1&from=XX&to=USD", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown: code=%d want 422", rec.Code)
	}
	rec = do(http.MethodGet, "/api/v1/fx/rates", "")
	var list []Rate
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 2 {
		t.Fatalf("rates=%d want 2 (USD seed + EUR)", len(list))
	}
}
