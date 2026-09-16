package pos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/docgen"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// memService seeds one sellable good + till and returns a checkout service
// over memory stores (Pool nil → direct, no transactions).
func memService(t *testing.T) (*Service, *MemoryStore, Session) {
	t.Helper()
	ctx := context.Background()
	ledger := catalog.NewMemoryStore()
	p := &catalog.Product{EntityID: 1, SKU: "TP-001", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 2000,
		Status: catalog.ProductActive, StockTracked: true}
	if err := ledger.CreateProduct(ctx, nil, p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := ledger.AppendMovement(ctx, nil, &catalog.StockMovement{EntityID: 1,
		ProductID: p.ID, WarehouseID: 1, Qty: 100,
		Reason: catalog.ReasonReceipt, Ref: "OPEN"}, false); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	store := NewMemoryStore()
	term := &Terminal{EntityID: 1, Code: "TP-TILL", Label: "Till",
		WarehouseID: 1, Status: TerminalActive}
	if err := store.CreateTerminal(ctx, nil, term); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada", OpeningFloat: 5000}
	if err := store.OpenSession(ctx, nil, se); err != nil {
		t.Fatalf("session: %v", err)
	}
	return NewService(nil, store, ledger, sales.NewMemoryStore(), 0, nil), store, *se
}

// cashCheckout rings 2 x 1000 net + 20% VAT = 2400 gross, tendered 3000.
func cashCheckout(t *testing.T, svc *Service, se Session) Sale {
	t.Helper()
	sa, err := svc.Checkout(context.Background(), CheckoutCmd{EntityID: 1,
		SessionID: se.ID, OrgID: 7,
		Lines: []SaleLine{{ProductID: 1, Qty: 2}}, Method: PayCash, Tendered: 3000})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if sa.TotalGross != 2400 || sa.Change != 600 {
		t.Fatalf("sale=%+v want gross 2400 change 600", sa)
	}
	return sa
}

func TestReceiptTextAndPDF(t *testing.T) {
	svc, _, se := memService(t)
	sa := cashCheckout(t, svc, se)
	sub, err := svc.ReceiptData(context.Background(), 1, sa.ID)
	if err != nil {
		t.Fatalf("receipt data: %v", err)
	}
	if sub.Ref == "" || len(sub.Lines) != 1 || sub.Lines[0].Label != "Widget" {
		t.Fatalf("subject=%+v", sub)
	}
	if sub.Lines[0].LineGross != 2400 {
		t.Fatalf("line gross=%d want 2400", sub.Lines[0].LineGross)
	}
	txt := ReceiptText(sub)
	for _, want := range []string{sa.Ref, "TOTAL:", "24.00 USD", "CHANGE:", "6.00 USD", "ada"} {
		if !strings.Contains(txt, want) {
			t.Errorf("receipt text missing %q:\n%s", want, txt)
		}
	}
	for _, ln := range strings.Split(strings.TrimRight(txt, "\n"), "\n") {
		if len(ln) > receiptWidth {
			t.Errorf("line too wide (%d): %q", len(ln), ln)
		}
	}

	// Through the Kernel-5 registry: PDF, deterministic, rejects garbage.
	m, err := docgen.DefaultRegistry().Lookup(ReceiptCode)
	if err != nil {
		t.Fatalf("lookup receipt model: %v", err)
	}
	if !m.Applies(ReceiptCode) || m.Applies("invoice") {
		t.Fatal("receipt model applies mismatch")
	}
	render := func() []byte {
		rd, ct, err := m.Render(context.Background(), sub, "en")
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if ct != "application/pdf" {
			t.Fatalf("content type = %q", ct)
		}
		b, _ := io.ReadAll(rd)
		return b
	}
	a := render()
	if !bytes.HasPrefix(a, []byte("%PDF")) {
		t.Fatal("output is not a PDF")
	}
	for i := 0; i < 10; i++ {
		if b := render(); !bytes.Equal(a, b) {
			t.Fatalf("render %d differs", i)
		}
	}
	if _, _, err := m.Render(context.Background(), "nope", "en"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad subject err=%v want ErrValidation", err)
	}
	empty := sub
	empty.Lines = nil
	if _, _, err := m.Render(context.Background(), empty, "en"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty lines err=%v want ErrValidation", err)
	}
	// Cross-tenant sale is invisible.
	if _, err := svc.ReceiptData(context.Background(), 2, sa.ID); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-tenant receipt err=%v want ErrNotFound", err)
	}
}

func TestESCPosBytes(t *testing.T) {
	if got := DrawerKickBytes(); !bytes.Equal(got, []byte{0x1B, 0x70, 0x00, 0x19, 0xFA}) {
		t.Fatalf("kick=% x", got)
	}
	if got := CutBytes(true); !bytes.Equal(got, []byte{0x1D, 0x56, 0x00}) {
		t.Fatalf("full cut=% x", got)
	}
	if got := CutBytes(false); !bytes.Equal(got, []byte{0x1D, 0x56, 0x01}) {
		t.Fatalf("partial cut=% x", got)
	}
	if got := string(EncodeReceiptText("caf\u00e9 \u20ac12 \u65e5\u672c\r\nok")); got != "cafE E12 ??\nok" {
		t.Fatalf("encoded=%q", got)
	}
	job := ESCPosJob("hi", true, true)
	if !bytes.HasPrefix(job, InitBytes()) {
		t.Fatal("job must start with INIT")
	}
	if !bytes.Contains(job, DrawerKickBytes()) {
		t.Fatal("kick requested but missing")
	}
	if !bytes.HasSuffix(job, CutBytes(true)) {
		t.Fatal("job must end with full cut")
	}
	quiet := ESCPosJob("hi", false, false)
	if bytes.Contains(quiet, DrawerKickBytes()) {
		t.Fatal("kick not requested but present")
	}
	if !bytes.HasSuffix(quiet, CutBytes(false)) {
		t.Fatal("job must end with partial cut")
	}
	if !bytes.Contains(quiet, []byte("hi")) {
		t.Fatal("text missing from job")
	}
}

func TestOfflineQueueReplayIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, store, se := memService(t)

	q1 := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "k1",
		OrgID: 7, Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCash, Tendered: 1200}
	if err := store.EnqueueOffline(ctx, nil, q1); err != nil {
		t.Fatalf("enqueue k1: %v", err)
	}
	q2 := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "k2",
		OrgID: 7, Lines: []SaleLine{{ProductID: 1, Qty: 2}}, Method: PayCash, Tendered: 3000}
	if err := store.EnqueueOffline(ctx, nil, q2); err != nil {
		t.Fatalf("enqueue k2: %v", err)
	}
	// Retried sync with the same key collapses onto the existing row.
	dup := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "k1",
		OrgID: 7, Lines: []SaleLine{{ProductID: 1, Qty: 9}}, Method: PayCash, Tendered: 99999}
	if err := store.EnqueueOffline(ctx, nil, dup); err != nil {
		t.Fatalf("re-enqueue k1: %v", err)
	}
	if dup.ID != q1.ID || len(dup.Lines) != 1 || dup.Lines[0].Qty != 1 {
		t.Fatalf("dup=%+v want original k1 row", dup)
	}
	// Same key under another entity is a distinct payload.
	other := &QueuedSale{EntityID: 2, SessionID: se.ID, IdempotencyKey: "k1",
		OrgID: 7, Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCash, Tendered: 1200}
	if err := store.EnqueueOffline(ctx, nil, other); err != nil {
		t.Fatalf("cross-entity key: %v", err)
	}

	res, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.SaleIDs) != 2 || len(res.Failed) != 0 {
		t.Fatalf("replay=%+v want 2 sales, no failures", res)
	}
	for _, qid := range res.QueueIDs {
		found := false
		for _, want := range []int64{q1.ID, q2.ID} {
			if qid == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("queue id %d not enqueued", qid)
		}
	}
	if got, _ := store.SalesOfSession(ctx, nil, se.ID); len(got) != 2 {
		t.Fatalf("sales=%d want 2", len(got))
	}

	// Double replay is a no-op: sent rows never re-drain.
	res2, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay2: %v", err)
	}
	if len(res2.SaleIDs) != 0 || len(res2.Failed) != 0 {
		t.Fatalf("replay2=%+v want empty", res2)
	}
	if got, _ := store.SalesOfSession(ctx, nil, se.ID); len(got) != 2 {
		t.Fatalf("sales after double replay=%d want 2", len(got))
	}

	// A poison payload fails without blocking siblings enqueued later.
	bad := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "bad",
		OrgID: 7, Lines: []SaleLine{{ProductID: 999, Qty: 1}}, Method: PayCash, Tendered: 5000}
	if err := store.EnqueueOffline(ctx, nil, bad); err != nil {
		t.Fatalf("enqueue bad: %v", err)
	}
	good := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "k3",
		OrgID: 7, Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCash, Tendered: 1200}
	if err := store.EnqueueOffline(ctx, nil, good); err != nil {
		t.Fatalf("enqueue k3: %v", err)
	}
	res3, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay3: %v", err)
	}
	if len(res3.SaleIDs) != 1 || len(res3.Failed) != 1 {
		t.Fatalf("replay3=%+v want 1 sale + 1 failure", res3)
	}
	if res3.Failed[0].Key != "bad" || res3.Failed[0].Error == "" {
		t.Fatalf("failure=%+v", res3.Failed[0])
	}
	stored, err := store.QueueByKey(ctx, nil, 1, "bad")
	if err != nil || stored.Status != QueueFailed || stored.Attempts != 1 || stored.LastError == "" {
		t.Fatalf("bad row=%+v err=%v", stored, err)
	}
	// Failed rows never re-drain either.
	res4, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay4: %v", err)
	}
	if len(res4.SaleIDs) != 0 || len(res4.Failed) != 0 {
		t.Fatalf("replay4=%+v want empty", res4)
	}

	// Validation + tenant isolation on the queue surface.
	if err := store.EnqueueOffline(ctx, nil, &QueuedSale{EntityID: 1, SessionID: se.ID}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("blank key err=%v want ErrValidation", err)
	}
	if _, err := store.QueueByKey(ctx, nil, 2, "k2"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-tenant key err=%v want ErrNotFound", err)
	}
	if _, err := store.MarkQueue(ctx, nil, 2, q1.ID, QueueSent, ""); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-tenant mark err=%v want ErrNotFound", err)
	}
	if _, err := store.MarkQueue(ctx, nil, 1, q1.ID, "bogus", ""); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad status err=%v want ErrValidation", err)
	}
}

func TestCashVarianceAndXZ(t *testing.T) {
	ctx := context.Background()
	svc, store, se := memService(t)
	cashCheckout(t, svc, se) // 2400 gross cash, tendered 3000

	// A card sale rings the invoice but never sits in the drawer.
	if _, err := svc.Checkout(ctx, CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: 7,
		Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCard, Tendered: 1200}); err != nil {
		t.Fatalf("card checkout: %v", err)
	}

	// Payout validation.
	if err := store.RecordPayout(ctx, nil, &Payout{EntityID: 1, SessionID: se.ID}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("zero payout err=%v want ErrValidation", err)
	}
	if err := store.RecordPayout(ctx, nil, &Payout{EntityID: 1, SessionID: se.ID, Amount: 500, Reason: "stamps"}); err != nil {
		t.Fatalf("payout: %v", err)
	}

	// expected = 5000 + 2400 − 500 = 6900; counted 7000 → +100.
	c, err := svc.RecordCashCount(ctx, 1, se.ID, 7000)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if c.ExpectedCash != 6900 || c.Variance != 100 {
		t.Fatalf("count=%+v want expected 6900 variance 100", c)
	}
	if err := store.RecordCount(ctx, nil, &CashCount{EntityID: 1, SessionID: se.ID, CountedCash: -1}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("negative count err=%v want ErrValidation", err)
	}

	x, err := svc.XReport(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("X: %v", err)
	}
	if x.Kind != "X" {
		t.Fatalf("kind=%q want X", x.Kind)
	}
	tot := x.Totals
	if tot.SaleCount != 2 || tot.GrossTotal != 3600 || tot.CashGross != 2400 || tot.PayoutTotal != 500 || tot.ExpectedCash != 6900 {
		t.Fatalf("totals=%+v", tot)
	}
	if tot.ByMethod[PayCash] != 2400 || tot.ByMethod[PayCard] != 1200 {
		t.Fatalf("by_method=%v", tot.ByMethod)
	}
	if tot.CountedCash == nil || *tot.CountedCash != 7000 || tot.Variance == nil || *tot.Variance != 100 {
		t.Fatalf("totals=%+v want counted 7000 variance 100", tot)
	}
	// X never closes: the till keeps selling.
	if se2, err := store.SessionByID(ctx, nil, 1, se.ID); err != nil || se2.Status != SessionOpen {
		t.Fatalf("X closed the session: %+v %v", se2, err)
	}

	z, err := svc.ZReport(ctx, 1, se.ID, se.RowVersion)
	if err != nil {
		t.Fatalf("Z: %v", err)
	}
	if z.Kind != "Z" || z.Session.Status != SessionClosed || z.Session.ClosedAt == nil {
		t.Fatalf("z=%+v", z)
	}
	if z.Totals.ExpectedCash != 6900 {
		t.Fatalf("z expected=%d want 6900", z.Totals.ExpectedCash)
	}
	// Closed session: Z again conflicts, checkout is rejected.
	if _, err := svc.ZReport(ctx, 1, se.ID, se.RowVersion); err == nil {
		t.Fatal("double Z accepted")
	}
	if _, err := svc.Checkout(ctx, CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: 7,
		Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCash, Tendered: 1200}); err == nil {
		t.Fatal("checkout on closed session accepted")
	}
	// Unknown session reports 404.
	if _, err := svc.XReport(ctx, 1, 9999); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("X ghost err=%v want ErrNotFound", err)
	}
}

func TestTakeposHTTP(t *testing.T) {
	h, ledger := testRouter()
	seedGoods(t, ledger)
	se := openTill(t, h)

	rec := doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7, "method": "cash", "tendered": 3000,
		"lines": []map[string]any{{"product_id": 1, "qty": 2}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sa Sale
	_ = json.NewDecoder(rec.Body).Decode(&sa)

	// Receipt text + PDF + bad format.
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sales/%d/receipt", sa.ID), nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("receipt text: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), sa.Ref) {
		t.Fatalf("receipt text missing ref:\n%s", rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sales/%d/receipt?format=pdf", sa.ID), nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("receipt pdf: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF")) {
		t.Fatal("receipt pdf is not a PDF")
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sales/%d/receipt?format=docx", sa.ID), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad format: code=%d want 400", rec.Code)
	}

	// ESC/POS download: INIT-first, cut-last; quiet variant without kick.
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sales/%d/escpos", sa.ID), nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("escpos: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	job := rec.Body.Bytes()
	if !bytes.HasPrefix(job, InitBytes()) || !bytes.HasSuffix(job, CutBytes(true)) || !bytes.Contains(job, DrawerKickBytes()) {
		t.Fatalf("escpos job framing wrong: % x", job)
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sales/%d/escpos?kick=0&cut=partial", sa.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("escpos quiet: code=%d", rec.Code)
	}
	quiet := rec.Body.Bytes()
	if bytes.Contains(quiet, DrawerKickBytes()) || !bytes.HasSuffix(quiet, CutBytes(false)) {
		t.Fatalf("escpos quiet framing wrong: % x", quiet)
	}

	// Offline queue: enqueue → list → replay → replay (no-op).
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/offline/queue", map[string]any{
		"session_id": se.ID, "idempotency_key": "till-1", "org_id": 7,
		"method": "cash", "tendered": 1200,
		"lines": []map[string]any{{"product_id": 1, "qty": 1}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("enqueue: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/offline/queue?session_id=%d", se.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list queue: code=%d", rec.Code)
	}
	var queued []QueuedSale
	_ = json.NewDecoder(rec.Body).Decode(&queued)
	if len(queued) != 1 {
		t.Fatalf("queued=%d want 1", len(queued))
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/offline/replay",
		map[string]any{"session_id": se.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var rep ReplayResult
	_ = json.NewDecoder(rec.Body).Decode(&rep)
	if len(rep.SaleIDs) != 1 || len(rep.Failed) != 0 {
		t.Fatalf("replay=%+v want 1 sale", rep)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/offline/replay",
		map[string]any{"session_id": se.ID})
	_ = json.NewDecoder(rec.Body).Decode(&rep)
	if len(rep.SaleIDs) != 0 {
		t.Fatalf("double replay=%+v want empty", rep)
	}

	// Payout + count: expected = 5000 + 2400 + 1200 − 600 = 8000.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/pos/sessions/%d/payouts", se.ID),
		map[string]any{"amount": 600, "reason": "change run"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payout: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/pos/sessions/%d/count", se.ID),
		map[string]any{"counted_cash": 8000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("count: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var counted CashCount
	_ = json.NewDecoder(rec.Body).Decode(&counted)
	if counted.ExpectedCash != 8000 || counted.Variance != 0 {
		t.Fatalf("count=%+v want expected 8000 variance 0", counted)
	}

	// X snapshot keeps the session open; Z closes it.
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sessions/%d/x", se.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("X: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var x SessionReport
	_ = json.NewDecoder(rec.Body).Decode(&x)
	if x.Kind != "X" || x.Totals.SaleCount != 2 || x.Totals.ExpectedCash != 8000 {
		t.Fatalf("x=%+v", x)
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/pos/sessions/%d", se.ID), nil)
	var cur Session
	_ = json.NewDecoder(rec.Body).Decode(&cur)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/pos/sessions/%d/z", se.ID),
		map[string]any{"row_version": cur.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("Z: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var z SessionReport
	_ = json.NewDecoder(rec.Body).Decode(&z)
	if z.Kind != "Z" || z.Session.Status != SessionClosed {
		t.Fatalf("z=%+v", z)
	}
}
