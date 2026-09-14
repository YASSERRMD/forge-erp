package pos

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// Service owns the checkout transaction boundary (Phase 0 task 4): invoice,
// payment, stock movements and the till sale commit atomically. A nil Pool
// runs the flow directly on the given db (memory stores in handler tests).
type Service struct {
	Pool      *pgxpool.Pool
	Store     Store
	Catalog   Catalog
	Sales     Sales
	WalkinOrg int64 // default customer for anonymous sales (0 = require org)
	Bus       platform.Bus
}

// NewService wires a checkout service (Pool may be nil in tests).
func NewService(pool *pgxpool.Pool, store Store, cat Catalog, sal Sales, walkinOrg int64, bus platform.Bus) *Service {
	return &Service{Pool: pool, Store: store, Catalog: cat, Sales: sal, WalkinOrg: walkinOrg, Bus: bus}
}

// CheckoutCmd is a validated checkout request with the caller's tenant.
type CheckoutCmd struct {
	EntityID  int64
	SessionID int64
	OrgID     int64
	Lines     []SaleLine
	Method    string
	Tendered  int64
	Payments  []Tender
}

// Checkout rings a sale atomically: validated invoice + full payment,
// tracked-goods decrements, and the till record. Publishes
// forgeerp.pos.sale.completed.v1 after commit.
func (s *Service) Checkout(ctx context.Context, cmd CheckoutCmd) (Sale, error) {
	if s.Pool == nil {
		out, err := s.checkoutOn(ctx, nil, cmd)
		if err != nil {
			return Sale{}, err
		}
		s.published(ctx, cmd.EntityID, out.ID)
		return out, nil
	}
	var out Sale
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		out, err = s.checkoutOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return Sale{}, err
	}
	s.published(ctx, cmd.EntityID, out.ID)
	return out, nil
}

func (s *Service) published(ctx context.Context, entityID, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{
		Subject: "forgeerp.pos.sale.completed.v1", Entity: "sale", EntityID: entityID, ID: id})
}

func (s *Service) checkoutOn(ctx context.Context, db platform.DBTX, cmd CheckoutCmd) (Sale, error) {
	entity := cmd.EntityID
	se, err := s.Store.SessionByID(ctx, db, entity, cmd.SessionID)
	if err != nil {
		return Sale{}, err
	}
	if se.Status != SessionOpen {
		return Sale{}, errors.New("pos: session closed")
	}
	term, err := s.Store.TerminalByID(ctx, db, entity, se.TerminalID)
	if err != nil {
		return Sale{}, err
	}
	if term.Status != TerminalActive {
		return Sale{}, errors.New("pos: terminal inactive")
	}
	dlines := make([]documents.Line, 0, len(cmd.Lines))
	type need struct {
		productID int64
		qty       int64
	}
	var needs []need
	for _, l := range cmd.Lines {
		if err := l.Validate(); err != nil {
			return Sale{}, err
		}
		p, err := s.Catalog.ProductByID(ctx, db, entity, l.ProductID)
		if err != nil {
			return Sale{}, err
		}
		if p.Status != catalog.ProductActive {
			return Sale{}, errors.New("pos: product not sellable")
		}
		dlines = append(dlines, documents.Line{ProductID: p.ID, Label: p.Name,
			Qty: l.Qty, UnitNet: p.NetPrice, VATRateBps: int(p.VATRateBps)})
		if p.Type == catalog.ProductGoods && p.StockTracked {
			lvl, err := s.Catalog.Level(ctx, db, p.ID, term.WarehouseID)
			if err != nil {
				return Sale{}, err
			}
			if lvl.Qty < l.Qty {
				return Sale{}, errors.New("pos: insufficient stock")
			}
			needs = append(needs, need{productID: p.ID, qty: l.Qty})
		}
	}
	tot, err := documents.Sum(dlines)
	if err != nil {
		return Sale{}, err
	}
	method := cmd.Method
	if method == "" {
		method = PayCash
	}
	tendered := cmd.Tendered
	legs := []Tender{{Method: method, Amount: tendered}}
	if len(cmd.Payments) > 0 {
		for _, tg := range cmd.Payments {
			if err := tg.Validate(); err != nil {
				return Sale{}, err
			}
		}
		tendered = 0
		seen := map[string]bool{}
		for _, tg := range cmd.Payments {
			tendered += tg.Amount
			seen[tg.Method] = true
		}
		legs = cmd.Payments
		method = cmd.Payments[0].Method
		if len(seen) > 1 {
			method = "mixed"
		}
	}
	orgID := cmd.OrgID
	if orgID == 0 {
		if s.WalkinOrg == 0 {
			return Sale{}, errors.New("pos: customer org required (no anonymous sales in lite scope)")
		}
		orgID = s.WalkinOrg
	}
	sale := Sale{EntityID: entity, SessionID: se.ID, OrgID: orgID,
		Lines: cmd.Lines, Method: method, Tendered: tendered}
	if err := sale.Validate(); err != nil {
		return Sale{}, err
	}
	if tendered < tot.Gross {
		return Sale{}, errors.New("pos: tendered below total")
	}
	ym := time.Now().UTC().Format("200601")
	inv := &sales.Document{EntityID: entity, Type: documents.TypeInvoice, OrgID: orgID,
		Currency: "USD", RateToBase: 1000000, Lines: dlines}
	if err := s.Sales.CreateDoc(ctx, db, inv, ym); err != nil {
		return Sale{}, err
	}
	validated, err := s.Sales.SetStatus(ctx, db, entity, inv.ID, sales.InvoiceValidated)
	if err != nil {
		return Sale{}, err
	}
	inv = &validated
	// Split the gross across tender legs in order (change stays on the till).
	remaining := tot.Gross
	for _, leg := range legs {
		alloc := leg.Amount
		if alloc > remaining {
			alloc = remaining
		}
		if alloc <= 0 {
			continue
		}
		pay := &sales.Payment{EntityID: entity, OrgID: orgID, Amount: alloc,
			Currency: "USD", Method: leg.Method, PaidAt: time.Now().UTC()}
		if _, err := s.Sales.RecordPayment(ctx, db, pay, []int64{inv.ID}, ym); err != nil {
			return Sale{}, err
		}
		remaining -= alloc
	}
	for _, n := range needs {
		if _, err := s.Catalog.AppendMovement(ctx, db, &catalog.StockMovement{
			EntityID: entity, ProductID: n.productID, WarehouseID: term.WarehouseID,
			Qty: -n.qty, Reason: catalog.ReasonShipment, Ref: inv.Ref}, false); err != nil {
			return Sale{}, err
		}
	}
	rec := Sale{EntityID: entity, SessionID: se.ID, Ref: inv.Ref, OrgID: orgID,
		Lines: cmd.Lines, TotalGross: tot.Gross, Method: method, Tendered: tendered,
		Change: tendered - tot.Gross, Status: SaleCompleted, InvoiceID: inv.ID}
	if err := s.Store.CreateSale(ctx, db, &rec); err != nil {
		return Sale{}, err
	}
	return rec, nil
}
