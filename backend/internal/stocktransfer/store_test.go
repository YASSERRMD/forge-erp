package stocktransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

// fakeLedger is an in-process StockLedger: levels per (product, warehouse),
// movements appended without PMP revaluation (cost passthrough).
type fakeLedger struct {
	levels map[[2]int64]int64
	moves  []catalog.StockMovement
}

func (f *fakeLedger) Level(_ context.Context, _ platform.DBTX, productID, warehouseID int64) (catalog.StockLevel, error) {
	return catalog.StockLevel{ProductID: productID, WarehouseID: warehouseID,
		Qty:        f.levels[[2]int64{productID, warehouseID}],
		TotalValue: f.levels[[2]int64{productID, warehouseID}] * 100}, nil
}

func (f *fakeLedger) AppendMovement(_ context.Context, _ platform.DBTX, m *catalog.StockMovement, allowNegative bool) (catalog.StockLevel, error) {
	if err := m.Validate(); err != nil {
		return catalog.StockLevel{}, err
	}
	k := [2]int64{m.ProductID, m.WarehouseID}
	if f.levels[k]+m.Qty < 0 && !allowNegative {
		return catalog.StockLevel{}, errors.Join(errors.New("catalog: insufficient stock"), platform.ErrValidation)
	}
	f.levels[k] += m.Qty
	f.moves = append(f.moves, *m)
	return catalog.StockLevel{ProductID: m.ProductID, WarehouseID: m.WarehouseID, Qty: f.levels[k]}, nil
}

func testSvc() (*Service, *fakeLedger) {
	ledger := &fakeLedger{levels: map[[2]int64]int64{}}
	svc := &Service{Store: NewMemoryStore(), Ledger: ledger, Bus: platform.NewMemoryBus(),
		Now: func() time.Time { return time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC) }}
	return svc, ledger
}

func TestTransferValidatePostsPairedMovements(t *testing.T) {
	ctx := context.Background()
	svc, ledger := testSvc()
	ledger.levels[[2]int64{5, 1}] = 10 // product 5, warehouse 1: 10 units @ 100
	tr := &Transfer{EntityID: 1, SourceWarehouseID: 1, DestWarehouseID: 2, Note: "replenish"}
	if err := svc.Create(ctx, tr); err != nil {
		t.Fatalf("create: %v", err)
	}
	if tr.Ref != "TRF-202609-0001" {
		t.Fatalf("ref=%q want TRF-202609-0001", tr.Ref)
	}
	if err := svc.Store.AddLine(ctx, nil, 1, tr.ID, &TransferLine{ProductID: 5, Qty: 4}); err != nil {
		t.Fatalf("line: %v", err)
	}
	done, err := svc.ValidateTransfer(ctx, 1, tr.ID, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if done.Status != StatusValidated {
		t.Fatalf("status=%d want validated", done.Status)
	}
	if len(ledger.moves) != 2 {
		t.Fatalf("moves=%d want 2", len(ledger.moves))
	}
	if ledger.moves[0].Qty != -4 || ledger.moves[0].WarehouseID != 1 ||
		ledger.moves[0].Reason != catalog.ReasonTransferOut {
		t.Fatalf("out move=%+v", ledger.moves[0])
	}
	if ledger.moves[1].Qty != 4 || ledger.moves[1].WarehouseID != 2 ||
		ledger.moves[1].Reason != catalog.ReasonTransferIn {
		t.Fatalf("in move=%+v", ledger.moves[1])
	}
	if ledger.moves[0].UnitCost != 100 {
		t.Fatalf("PMP snapshot=%d want 100", ledger.moves[0].UnitCost)
	}
	// Validated is terminal: re-validate and cancel both 422.
	if _, err := svc.ValidateTransfer(ctx, 1, tr.ID, nil); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("re-validate err=%v want ErrValidation", err)
	}
	cur, _ := svc.Store.TransferByID(ctx, nil, 1, tr.ID)
	if _, err := svc.Store.SetStatus(ctx, nil, 1, tr.ID, StatusCanceled, cur.RowVersion); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("cancel-validated err=%v want ErrValidation", err)
	}
}

func TestTransferValidateNeedsStock(t *testing.T) {
	ctx := context.Background()
	svc, _ := testSvc() // empty ledger: 0 on hand
	tr := &Transfer{EntityID: 1, SourceWarehouseID: 1, DestWarehouseID: 2}
	if err := svc.Create(ctx, tr); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Store.AddLine(ctx, nil, 1, tr.ID, &TransferLine{ProductID: 5, Qty: 3}); err != nil {
		t.Fatalf("line: %v", err)
	}
	if _, err := svc.ValidateTransfer(ctx, 1, tr.ID, nil); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("validate err=%v want ErrValidation (insufficient stock)", err)
	}
}

func TestTransferSameWarehouseRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := testSvc()
	tr := &Transfer{EntityID: 1, SourceWarehouseID: 1, DestWarehouseID: 1}
	if err := svc.Create(ctx, tr); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("create err=%v want ErrValidation", err)
	}
}

func TestTransferRoutes(t *testing.T) {
	svc, _ := testSvc()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(map[string]any{"source_warehouse_id": 1, "dest_warehouse_id": 2})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stock-transfers", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var tr Transfer
	_ = json.NewDecoder(rec.Body).Decode(&tr)

	raw, _ = json.Marshal(map[string]any{"product_id": 5, "qty": 2})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/stock-transfers/1/lines", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("line API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = tr
}
