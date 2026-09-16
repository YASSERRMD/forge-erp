package pos

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// TakePOS depth orchestration (Phase 2 Order 2): offline-queue replay,
// session cash reports (X/Z) and receipt subjects. All orchestration lives
// on the SERVICE (not the handler): handlers decode/encode HTTP only.

func (s *Service) db() platform.DBTX {
	if s.Pool != nil {
		return s.Pool
	}
	return nil
}

// ReceiptData builds the print subject for a completed sale: sale totals
// plus terminal/cashier context and catalog-resolved line labels/prices.
func (s *Service) ReceiptData(ctx context.Context, entityID, saleID int64) (ReceiptSubject, error) {
	db := s.db()
	sa, err := s.Store.SaleByID(ctx, db, entityID, saleID)
	if err != nil {
		return ReceiptSubject{}, err
	}
	se, err := s.Store.SessionByID(ctx, db, entityID, sa.SessionID)
	if err != nil {
		return ReceiptSubject{}, err
	}
	term, err := s.Store.TerminalByID(ctx, db, entityID, se.TerminalID)
	if err != nil {
		return ReceiptSubject{}, err
	}
	lines := make([]ReceiptLine, 0, len(sa.Lines))
	for _, l := range sa.Lines {
		p, err := s.Catalog.ProductByID(ctx, db, entityID, l.ProductID)
		if err != nil {
			return ReceiptSubject{}, err
		}
		dl := documents.Line{ProductID: p.ID, Label: p.Name,
			Qty: l.Qty, UnitNet: p.NetPrice, VATRateBps: int(p.VATRateBps)}
		unit := documents.Line{ProductID: p.ID, Qty: 1,
			UnitNet: p.NetPrice, VATRateBps: int(p.VATRateBps)}
		unitGross := unit.Net() + unit.VAT()
		lines = append(lines, ReceiptLine{
			Label: p.Name, Qty: l.Qty,
			UnitGross: unitGross, LineGross: dl.Net() + dl.VAT(),
		})
	}
	issued := ""
	if !sa.CreatedAt.IsZero() {
		issued = sa.CreatedAt.UTC().Format("2006-01-02")
	}
	return ReceiptSubject{
		Ref: sa.Ref, Terminal: term.Code + " " + term.Label, Cashier: se.Cashier,
		IssuedOn: issued, Method: sa.Method, Currency: "USD", Lines: lines,
		TotalGross: sa.TotalGross, Tendered: sa.Tendered, Change: sa.Change,
	}, nil
}

// QueueFailure is one replayed payload that did not become a sale.
type QueueFailure struct {
	QueueID int64  `json:"queue_id"`
	Key     string `json:"key"`
	Error   string `json:"error"`
}

// ReplayResult is the outcome of one replay drain.
type ReplayResult struct {
	SaleIDs  []int64        `json:"sale_ids"`  // sales created, in drain order
	QueueIDs []int64        `json:"queue_ids"` // queue rows flipped queued → sent
	Failed   []QueueFailure `json:"failed"`
}

// ReplayQueue drains a session's queued payloads in FIFO order through the
// checkout service. Each payload commits atomically with its queued → sent
// flip; failures flip to failed (attempts++, last_error kept) and never
// block later payloads. Idempotent on key: sent/failed rows are never
// re-drained, so a double replay is a no-op, and a retried sync enqueues
// onto the existing row (see EnqueueOffline).
func (s *Service) ReplayQueue(ctx context.Context, entityID, sessionID int64) (ReplayResult, error) {
	var out ReplayResult
	entries, err := s.Store.QueuedDrain(ctx, s.db(), entityID, sessionID)
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		sale, err := s.replayOne(ctx, entityID, e)
		if err != nil {
			_, _ = s.Store.MarkQueue(ctx, s.db(), entityID, e.ID, QueueFailed, err.Error())
			out.Failed = append(out.Failed, QueueFailure{QueueID: e.ID, Key: e.IdempotencyKey, Error: err.Error()})
			continue
		}
		out.SaleIDs = append(out.SaleIDs, sale.ID)
		out.QueueIDs = append(out.QueueIDs, e.ID)
	}
	return out, nil
}

func (s *Service) replayOne(ctx context.Context, entityID int64, e QueuedSale) (Sale, error) {
	cmd := e.CheckoutCmd()
	if s.Pool == nil {
		sale, err := s.checkoutOn(ctx, nil, cmd)
		if err != nil {
			return Sale{}, err
		}
		if _, err := s.Store.MarkQueue(ctx, nil, entityID, e.ID, QueueSent, ""); err != nil {
			return Sale{}, err
		}
		s.published(ctx, entityID, sale.ID)
		return sale, nil
	}
	var sale Sale
	err := platform.TxEntity(ctx, s.Pool, entityID, func(tx pgx.Tx) error {
		var err error
		sale, err = s.checkoutOn(ctx, tx, cmd)
		if err != nil {
			return err
		}
		_, err = s.Store.MarkQueue(ctx, tx, entityID, e.ID, QueueSent, "")
		return err
	})
	if err != nil {
		return Sale{}, err
	}
	s.published(ctx, entityID, sale.ID)
	return sale, nil
}

// SessionTotals aggregates one cashier shift. Voided and fully-returned sales
// are excluded (their money left the drawer); partial returns keep the sale
// completed (credit-note settlement happens out-of-band — see DIFFERENCES).
// cashGross feeds the drawer expectation; card/transfer/mixed legs never sit
// in the drawer, so mixed-sale card portions are excluded (see DIFFERENCES).
type SessionTotals struct {
	SaleCount     int64            `json:"sale_count"`
	GrossTotal    int64            `json:"gross_total"`
	ByMethod      map[string]int64 `json:"by_method"`
	CashGross     int64            `json:"cash_gross"`
	PayoutTotal   int64            `json:"payout_total"`
	ExpectedCash  int64            `json:"expected_cash"`
	CountedCash   *int64           `json:"counted_cash,omitempty"`
	Variance      *int64           `json:"variance,omitempty"`
	VoidCount     int64            `json:"void_count"`
	ReturnedCount int64            `json:"returned_count"`
}

// SessionReport is an X (snapshot) or Z (closing) shift report.
type SessionReport struct {
	Kind        string        `json:"kind"` // "X" | "Z"
	Session     Session       `json:"session"`
	Terminal    Terminal      `json:"terminal"`
	Totals      SessionTotals `json:"totals"`
	GeneratedAt time.Time     `json:"generated_at"`
}

func (s *Service) reportOn(ctx context.Context, db platform.DBTX, se Session, term Terminal, kind string) (SessionReport, error) {
	sales, err := s.Store.SalesOfSession(ctx, db, se.ID)
	if err != nil {
		return SessionReport{}, err
	}
	tot := SessionTotals{ByMethod: map[string]int64{}}
	for _, sa := range sales {
		switch sa.Status {
		case SaleCompleted:
			tot.SaleCount++
			tot.GrossTotal += sa.TotalGross
			tot.ByMethod[sa.Method] += sa.TotalGross
			if sa.Method == PayCash {
				tot.CashGross += sa.Tendered - sa.Change
			}
		case SaleVoided:
			tot.VoidCount++
		case SaleReturned:
			tot.ReturnedCount++
		}
	}
	payouts, err := s.Store.PayoutsOfSession(ctx, db, se.EntityID, se.ID)
	if err != nil {
		return SessionReport{}, err
	}
	for _, p := range payouts {
		tot.PayoutTotal += p.Amount
	}
	// Drawer expectation: opening float + cash sales − payouts.
	tot.ExpectedCash = se.OpeningFloat + tot.CashGross - tot.PayoutTotal
	counts, err := s.Store.CountsOfSession(ctx, db, se.EntityID, se.ID)
	if err != nil {
		return SessionReport{}, err
	}
	if len(counts) > 0 {
		last := counts[0]
		for _, c := range counts[1:] {
			if c.ID > last.ID {
				last = c
			}
		}
		tot.CountedCash = &last.CountedCash
		tot.Variance = &last.Variance
	}
	return SessionReport{
		Kind: kind, Session: se, Terminal: term,
		Totals: tot, GeneratedAt: time.Now().UTC(),
	}, nil
}

// XReport snapshots a session mid-shift: totals without reset, session stays
// open and the till keeps selling.
func (s *Service) XReport(ctx context.Context, entityID, sessionID int64) (SessionReport, error) {
	db := s.db()
	se, err := s.Store.SessionByID(ctx, db, entityID, sessionID)
	if err != nil {
		return SessionReport{}, err
	}
	term, err := s.Store.TerminalByID(ctx, db, entityID, se.TerminalID)
	if err != nil {
		return SessionReport{}, err
	}
	return s.reportOn(ctx, db, se, term, "X")
}

// ZReport closes the session and returns the closing report. The close IS
// the counter reset: a closed session rejects further checkouts, so the
// reported expected cash is final.
func (s *Service) ZReport(ctx context.Context, entityID, sessionID int64, rowVersion int64) (SessionReport, error) {
	if s.Pool == nil {
		se, err := s.Store.CloseSession(ctx, nil, entityID, sessionID, rowVersion)
		if err != nil {
			return SessionReport{}, err
		}
		term, err := s.Store.TerminalByID(ctx, nil, entityID, se.TerminalID)
		if err != nil {
			return SessionReport{}, err
		}
		return s.reportOn(ctx, nil, se, term, "Z")
	}
	var rep SessionReport
	err := platform.TxEntity(ctx, s.Pool, entityID, func(tx pgx.Tx) error {
		se, err := s.Store.CloseSession(ctx, tx, entityID, sessionID, rowVersion)
		if err != nil {
			return err
		}
		term, err := s.Store.TerminalByID(ctx, tx, entityID, se.TerminalID)
		if err != nil {
			return err
		}
		rep, err = s.reportOn(ctx, tx, se, term, "Z")
		return err
	})
	if err != nil {
		return SessionReport{}, err
	}
	return rep, nil
}

// RecordCashCount snapshots the drawer: expected = opening float + cash
// sales − payouts; variance = counted − expected (negative = short).
func (s *Service) RecordCashCount(ctx context.Context, entityID, sessionID, counted int64) (CashCount, error) {
	db := s.db()
	se, err := s.Store.SessionByID(ctx, db, entityID, sessionID)
	if err != nil {
		return CashCount{}, err
	}
	term, err := s.Store.TerminalByID(ctx, db, entityID, se.TerminalID)
	if err != nil {
		return CashCount{}, err
	}
	rep, err := s.reportOn(ctx, db, se, term, "X")
	if err != nil {
		return CashCount{}, err
	}
	c := &CashCount{
		EntityID: entityID, SessionID: sessionID,
		CountedCash: counted, ExpectedCash: rep.Totals.ExpectedCash,
		Variance: counted - rep.Totals.ExpectedCash,
	}
	if err := s.Store.RecordCount(ctx, db, c); err != nil {
		return CashCount{}, err
	}
	return *c, nil
}
