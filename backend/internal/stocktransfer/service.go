package stocktransfer

import (
	"context"
	"fmt"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// StockLedger is the catalog seam validation posts through (paired
// out/in movements). *catalog.PGStore satisfies it in production.
type StockLedger interface {
	Level(ctx context.Context, db platform.DBTX, productID, warehouseID int64) (catalog.StockLevel, error)
	AppendMovement(ctx context.Context, db platform.DBTX, m *catalog.StockMovement, allowNegative bool) (catalog.StockLevel, error)
}

// Service owns transfer use cases (validation posts stock atomically
// through the caller's transaction).
type Service struct {
	Store  Store
	Ledger StockLedger
	Bus    platform.Bus
	DB     platform.DBTX
	Now    func() time.Time
}

// NewService builds a Service.
func NewService(s Store, ledger StockLedger, bus platform.Bus, db platform.DBTX) *Service {
	return &Service{Store: s, Ledger: ledger, Bus: bus, DB: db}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) yearMonth() string { return s.now().Format("200601") }

func (s *Service) publish(ctx context.Context, entityID, id int64, subject string) {
	if s == nil || s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: "transfer", EntityID: entityID, ID: id})
}

// Create mints the TRF reference and persists the draft.
func (s *Service) Create(ctx context.Context, t *Transfer) error {
	if err := s.Store.Create(ctx, s.DB, t, s.yearMonth()); err != nil {
		return err
	}
	s.publish(ctx, t.EntityID, t.ID, "forgeerp.stocktransfer.transfer.created.v1")
	return nil
}

// ValidateTransfer posts paired movements and flips draft→validated.
// UnitCost snapshots the source PMP average (half-up) when the line leaves
// it zero; insufficient source stock fails validation (422, no partial post).
func (s *Service) ValidateTransfer(ctx context.Context, entityID, id int64, actor *int64) (Transfer, error) {
	cur, err := s.Store.TransferByID(ctx, s.DB, entityID, id)
	if err != nil {
		return Transfer{}, err
	}
	if cur.Status != StatusDraft {
		return Transfer{}, fmt.Errorf("stocktransfer %d: only drafts validate: %w", id, platform.ErrValidation)
	}
	lines, err := s.Store.LinesOf(ctx, s.DB, entityID, id)
	if err != nil {
		return Transfer{}, err
	}
	if len(lines) == 0 {
		return Transfer{}, fmt.Errorf("stocktransfer %d: no lines: %w", id, platform.ErrValidation)
	}
	for _, l := range lines {
		cost := l.UnitCost
		if cost == 0 {
			lvl, err := s.Ledger.Level(ctx, s.DB, l.ProductID, cur.SourceWarehouseID)
			if err != nil {
				return Transfer{}, err
			}
			if lvl.Qty > 0 {
				cost = lvl.PMP()
			}
			if err := s.Store.SetLineCost(ctx, s.DB, entityID, l.ID, cost); err != nil {
				return Transfer{}, err
			}
		}
		out := &catalog.StockMovement{
			EntityID: entityID, ProductID: l.ProductID, WarehouseID: cur.SourceWarehouseID,
			Qty: -l.Qty, UnitCost: cost, Reason: catalog.ReasonTransferOut,
			Ref: cur.Ref, CreatedBy: actor,
		}
		if _, err := s.Ledger.AppendMovement(ctx, s.DB, out, false); err != nil {
			return Transfer{}, err
		}
		in := &catalog.StockMovement{
			EntityID: entityID, ProductID: l.ProductID, WarehouseID: cur.DestWarehouseID,
			Qty: l.Qty, UnitCost: cost, Reason: catalog.ReasonTransferIn,
			Ref: cur.Ref, CreatedBy: actor,
		}
		if _, err := s.Ledger.AppendMovement(ctx, s.DB, in, false); err != nil {
			return Transfer{}, err
		}
	}
	done, err := s.Store.SetStatus(ctx, s.DB, entityID, id, StatusValidated, cur.RowVersion)
	if err != nil {
		return Transfer{}, err
	}
	s.publish(ctx, entityID, id, "forgeerp.stocktransfer.transfer.validated.v1")
	return done, nil
}
