package assets

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Finance abstracts the ledger posting used by depreciation and disposal.
type Finance interface {
	PostEntry(ctx context.Context, db platform.DBTX, e *finance.Entry) error
}

// DepService owns the depreciation transaction boundaries (Phase 2): each
// periodic posting (schedule advance + balanced ledger entry) and each
// disposal (gain/loss entry + asset retirement) commit atomically. A nil
// Pool runs the flow directly on the given db (memory stores in tests).
type DepService struct {
	Pool    *pgxpool.Pool
	Store   Store
	Finance Finance
	Bus     platform.Bus
}

// NewDepService wires a depreciation service (Pool may be nil in tests).
func NewDepService(pool *pgxpool.Pool, store Store, fin Finance, bus platform.Bus) *DepService {
	return &DepService{Pool: pool, Store: store, Finance: fin, Bus: bus}
}

// PostDepCmd posts one period's depreciation for a schedule.
type PostDepCmd struct {
	EntityID       int64
	ScheduleID     int64
	JournalID      int64
	ExpenseAccount int64 // debit: depreciation expense
	AccumAccount   int64 // credit: accumulated depreciation
	RowVersion     int64 // schedule row version
}

// PostResult is the posted period outcome.
type PostResult struct {
	Schedule AssetSchedule `json:"schedule"`
	Seq      int           `json:"seq"`
	Amount   int64         `json:"amount"`
}

// Post advances the schedule by one period and posts the balanced entry
// (debit depreciation expense, credit accumulated depreciation) atomically.
func (s *DepService) Post(ctx context.Context, cmd PostDepCmd) (PostResult, error) {
	if s.Pool == nil {
		out, err := s.postOn(ctx, nil, cmd)
		if err != nil {
			return PostResult{}, err
		}
		s.published(ctx, cmd.EntityID, "forgeerp.assets.depreciation.posted.v1", "asset_schedule", out.Schedule.ID)
		return out, nil
	}
	var out PostResult
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		out, err = s.postOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return PostResult{}, err
	}
	s.published(ctx, cmd.EntityID, "forgeerp.assets.depreciation.posted.v1", "asset_schedule", out.Schedule.ID)
	return out, nil
}

func (s *DepService) published(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

func (s *DepService) postOn(ctx context.Context, db platform.DBTX, cmd PostDepCmd) (PostResult, error) {
	if cmd.JournalID <= 0 || cmd.ExpenseAccount <= 0 || cmd.AccumAccount <= 0 {
		return PostResult{}, fmt.Errorf("assets: journal, expense and accumulated accounts required: %w", platform.ErrValidation)
	}
	sc, err := s.Store.ScheduleByID(ctx, db, cmd.EntityID, cmd.ScheduleID)
	if err != nil {
		return PostResult{}, err
	}
	plan, err := BuildSchedule(sc.Cost, sc.Method, sc.RateBps, sc.Periods)
	if err != nil {
		return PostResult{}, err
	}
	if sc.PostedPeriods >= len(plan) {
		return PostResult{}, fmt.Errorf("assets: all periods posted: %w", platform.ErrValidation)
	}
	line := plan[sc.PostedPeriods]
	entry := &finance.Entry{EntityID: sc.EntityID, JournalID: cmd.JournalID,
		Ref: fmt.Sprintf("DEP-%d-P%d", sc.AssetID, line.Seq), Date: time.Now().UTC(),
		Memo: fmt.Sprintf("Depreciation asset %d period %d", sc.AssetID, line.Seq),
		Lines: []finance.EntryLine{
			{AccountID: cmd.ExpenseAccount, Label: fmt.Sprintf("Depreciation P%d", line.Seq), Debit: line.Amount},
			{AccountID: cmd.AccumAccount, Label: fmt.Sprintf("Accum. depreciation P%d", line.Seq), Credit: line.Amount},
		}}
	if err := s.Finance.PostEntry(ctx, db, entry); err != nil {
		return PostResult{}, err
	}
	upd, err := s.Store.MarkSchedulePosted(ctx, db, cmd.EntityID, cmd.ScheduleID, line.Amount, cmd.RowVersion)
	if err != nil {
		return PostResult{}, err
	}
	return PostResult{Schedule: upd, Seq: line.Seq, Amount: line.Amount}, nil
}

// DisposeCmd sells/scraps an asset: proceeds vs net book value drives the
// gain/loss entry, and the asset retires atomically.
type DisposeCmd struct {
	EntityID     int64
	AssetID      int64
	JournalID    int64
	CashAccount  int64 // debit: proceeds (leg omitted when proceeds are zero)
	CostAccount  int64 // credit: asset cost (always required)
	AccumAccount int64 // debit: accumulated depreciation (leg omitted when zero)
	GainAccount  int64 // credit: disposal gain (required when proceeds exceed NBV)
	LossAccount  int64 // debit: disposal loss (required when NBV exceeds proceeds)
	Proceeds     int64 // minor units, >= 0
	RowVersion   int64 // asset row version
}

// Dispose posts the gain/loss entry and retires the asset atomically.
// Gain = proceeds - (cost - accumulated): a positive difference credits the
// gain account, a negative difference debits the loss account, and an exact
// NBV sale posts neither.
func (s *DepService) Dispose(ctx context.Context, cmd DisposeCmd) (Asset, error) {
	if s.Pool == nil {
		out, err := s.disposeOn(ctx, nil, cmd)
		if err != nil {
			return Asset{}, err
		}
		s.published(ctx, cmd.EntityID, "forgeerp.assets.disposed.v1", "asset", out.ID)
		return out, nil
	}
	var out Asset
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		out, err = s.disposeOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return Asset{}, err
	}
	s.published(ctx, cmd.EntityID, "forgeerp.assets.disposed.v1", "asset", out.ID)
	return out, nil
}

func (s *DepService) disposeOn(ctx context.Context, db platform.DBTX, cmd DisposeCmd) (Asset, error) {
	if cmd.JournalID <= 0 || cmd.CostAccount <= 0 {
		return Asset{}, fmt.Errorf("assets: journal and asset-cost accounts required: %w", platform.ErrValidation)
	}
	if cmd.Proceeds < 0 {
		return Asset{}, fmt.Errorf("assets: proceeds must be non-negative: %w", platform.ErrValidation)
	}
	a, err := s.Store.AssetByID(ctx, db, cmd.EntityID, cmd.AssetID)
	if err != nil {
		return Asset{}, err
	}
	if a.RowVersion != cmd.RowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	scs, err := s.Store.SchedulesOfAsset(ctx, db, cmd.EntityID, cmd.AssetID)
	if err != nil {
		return Asset{}, err
	}
	if len(scs) == 0 {
		return Asset{}, fmt.Errorf("assets: disposal requires a depreciation schedule: %w", platform.ErrValidation)
	}
	var cost, accumulated int64
	for _, sc := range scs {
		cost += sc.Cost
		accumulated += sc.Accumulated
	}
	nbv := cost - accumulated
	diff := cmd.Proceeds - nbv // >0 gain, <0 loss
	if cmd.Proceeds > 0 && cmd.CashAccount <= 0 {
		return Asset{}, fmt.Errorf("assets: cash account required for proceeds: %w", platform.ErrValidation)
	}
	if accumulated > 0 && cmd.AccumAccount <= 0 {
		return Asset{}, fmt.Errorf("assets: accumulated account required: %w", platform.ErrValidation)
	}
	if diff > 0 && cmd.GainAccount <= 0 {
		return Asset{}, fmt.Errorf("assets: gain account required: %w", platform.ErrValidation)
	}
	if diff < 0 && cmd.LossAccount <= 0 {
		return Asset{}, fmt.Errorf("assets: loss account required: %w", platform.ErrValidation)
	}
	var debits, credits []finance.EntryLine
	if cmd.Proceeds > 0 {
		debits = append(debits, finance.EntryLine{AccountID: cmd.CashAccount, Label: "Disposal proceeds", Debit: cmd.Proceeds})
	}
	if accumulated > 0 {
		debits = append(debits, finance.EntryLine{AccountID: cmd.AccumAccount, Label: "Accumulated depreciation", Debit: accumulated})
	}
	if diff < 0 {
		debits = append(debits, finance.EntryLine{AccountID: cmd.LossAccount, Label: "Loss on disposal", Debit: -diff})
	}
	credits = append(credits, finance.EntryLine{AccountID: cmd.CostAccount, Label: "Asset cost", Credit: cost})
	if diff > 0 {
		credits = append(credits, finance.EntryLine{AccountID: cmd.GainAccount, Label: "Gain on disposal", Credit: diff})
	}
	entry := &finance.Entry{EntityID: a.EntityID, JournalID: cmd.JournalID,
		Ref: fmt.Sprintf("DISP-%d", a.ID), Date: time.Now().UTC(),
		Memo:  fmt.Sprintf("Disposal asset %s", a.Code),
		Lines: append(debits, credits...)}
	if err := s.Finance.PostEntry(ctx, db, entry); err != nil {
		return Asset{}, err
	}
	return s.Store.SetAssetStatus(ctx, db, cmd.EntityID, cmd.AssetID, AssetRetired, cmd.RowVersion)
}
