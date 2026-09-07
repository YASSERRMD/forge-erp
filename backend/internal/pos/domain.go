// Package pos implements point-of-sale lite (Dolibarr takepos): terminals,
// cashier sessions, and one-step checkout that posts a validated invoice,
// full payment and shipment stock moves through the existing contexts.
// Simplification: every sale needs a customer org (walk-in anonymous sales
// are a follow-up); refunds are void markers, not credit notes.
package pos

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Terminal status.
type TerminalStatus int16

const (
	TerminalActive   TerminalStatus = 1
	TerminalInactive TerminalStatus = 0
)

// Session status.
type SessionStatus int16

const (
	SessionOpen   SessionStatus = 0
	SessionClosed SessionStatus = 1
)

// Sale status.
type SaleStatus int16

const (
	SaleCompleted SaleStatus = 1
	SaleVoided    SaleStatus = -1
)

// Payment methods accepted at the till.
const (
	PayCash     = "cash"
	PayCard     = "card"
	PayTransfer = "transfer"
)

// Terminal is a till bound to the warehouse it sells from.
type Terminal struct {
	ID          int64          `json:"id"`
	EntityID    int64          `json:"entity_id"`
	Code        string         `json:"code"` // unique per entity
	Label       string         `json:"label"`
	WarehouseID int64          `json:"warehouse_id"`
	Status      TerminalStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	RowVersion  int64          `json:"row_version"`
}

// Validate checks terminal invariants.
func (t Terminal) Validate() error {
	if t.EntityID <= 0 {
		return errors.New("pos: entity_id required")
	}
	if strings.TrimSpace(t.Code) == "" {
		return errors.New("pos: code required")
	}
	if strings.TrimSpace(t.Label) == "" {
		return errors.New("pos: label required")
	}
	if t.WarehouseID <= 0 {
		return errors.New("pos: warehouse_id required")
	}
	return nil
}

// Session is a cashier shift on a terminal.
type Session struct {
	ID           int64         `json:"id"`
	EntityID     int64         `json:"entity_id"`
	TerminalID   int64         `json:"terminal_id"`
	Cashier      string        `json:"cashier"` // login
	OpeningFloat int64         `json:"opening_float"`
	Status       SessionStatus `json:"status"`
	OpenedAt     time.Time     `json:"opened_at"`
	ClosedAt     *time.Time    `json:"closed_at"`
	RowVersion   int64         `json:"row_version"`
}

// Validate checks session invariants.
func (s Session) Validate() error {
	if s.EntityID <= 0 || s.TerminalID <= 0 {
		return errors.New("pos: entity_id and terminal_id required")
	}
	if strings.TrimSpace(s.Cashier) == "" {
		return errors.New("pos: cashier required")
	}
	if s.OpeningFloat < 0 {
		return errors.New("pos: negative opening float")
	}
	return nil
}

// SaleLine is one till line (price snapshot taken at checkout).
type SaleLine struct {
	ProductID int64 `json:"product_id"`
	Qty       int64 `json:"qty"`
}

// Validate checks line invariants.
func (l SaleLine) Validate() error {
	if l.ProductID <= 0 {
		return errors.New("pos: product_id required")
	}
	if l.Qty <= 0 {
		return errors.New("pos: qty must be positive")
	}
	return nil
}

// Tender is one payment leg of a sale (multi-tender: cash + card, ...).
type Tender struct {
	Method string `json:"method"`
	Amount int64  `json:"amount"` // minor units, > 0
}

// Validate checks tender invariants.
func (t Tender) Validate() error {
	switch t.Method {
	case PayCash, PayCard, PayTransfer:
	default:
		return fmt.Errorf("pos: unknown payment method %q", t.Method)
	}
	if t.Amount <= 0 {
		return errors.New("pos: tender amount must be positive")
	}
	return nil
}

// Sale is a completed till transaction.
type Sale struct {
	ID          int64      `json:"id"`
	EntityID    int64      `json:"entity_id"`
	SessionID   int64      `json:"session_id"`
	Ref         string     `json:"ref"` // unique per entity (POS-xxxx)
	OrgID       int64      `json:"org_id"`
	Lines       []SaleLine `json:"lines"`
	TotalGross  int64      `json:"total_gross"` // minor units, server-computed
	Method      string     `json:"method"`
	Tendered    int64      `json:"tendered"`
	Change      int64      `json:"change"`
	Status      SaleStatus `json:"status"`
	InvoiceID   int64      `json:"invoice_id"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   *int64     `json:"created_by"`
}

// Validate checks sale invariants (totals/change recomputed server-side).
func (s Sale) Validate() error {
	if s.EntityID <= 0 || s.SessionID <= 0 {
		return errors.New("pos: entity_id and session_id required")
	}
	if s.OrgID <= 0 {
		return errors.New("pos: customer org required (no anonymous sales in lite scope)")
	}
	if len(s.Lines) == 0 {
		return errors.New("pos: sale requires at least one line")
	}
	for _, l := range s.Lines {
		if err := l.Validate(); err != nil {
			return err
		}
	}
	switch s.Method {
	case PayCash, PayCard, PayTransfer, "mixed":
	default:
		return fmt.Errorf("pos: unknown payment method %q", s.Method)
	}
	if s.Tendered <= 0 {
		return errors.New("pos: tendered must be positive")
	}
	return nil
}
