// Package label implements sticker-sheet definitions (sheet layouts:
// rows×cols, label size in mm, fields from product/org) rendered through
// the docgen registry to PDF. Ships an Avery-compatible 65-up default.
package label

import (
	"errors"
	"fmt"
	"strings"
)

// Label fields selectable from product/org rows.
const (
	FieldProductRef   = "product_ref"
	FieldProductLabel = "product_label"
	FieldOrgName      = "org_name"
	FieldPrice        = "price"
)

// ValidFields enumerates renderable fields.
var ValidFields = map[string]bool{
	FieldProductRef: true, FieldProductLabel: true, FieldOrgName: true, FieldPrice: true,
}

// SheetDef is one sticker-sheet layout (all lengths in millimetres).
type SheetDef struct {
	ID           int64    `json:"id"`
	EntityID     int64    `json:"entity_id"`
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Rows         int64    `json:"rows"`
	Cols         int64    `json:"cols"`
	LabelWMM     float64  `json:"label_w_mm"`
	LabelHMM     float64  `json:"label_h_mm"`
	MarginTopMM  float64  `json:"margin_top_mm"`
	MarginLeftMM float64  `json:"margin_left_mm"`
	GapXMM       float64  `json:"gap_x_mm"`
	GapYMM       float64  `json:"gap_y_mm"`
	Fields       []string `json:"fields"`
	RowVersion   int64    `json:"row_version"`
}

// Validate checks sheet invariants.
func (s SheetDef) Validate() error {
	if s.EntityID <= 0 {
		return errors.New("label: entity_id required")
	}
	if strings.TrimSpace(s.Code) == "" || strings.TrimSpace(s.Name) == "" {
		return errors.New("label: code and name required")
	}
	if s.Rows < 1 || s.Rows > 20 || s.Cols < 1 || s.Cols > 20 {
		return fmt.Errorf("label: rows×cols %dx%d out of range 1..20", s.Rows, s.Cols)
	}
	if s.LabelWMM <= 0 || s.LabelHMM <= 0 {
		return errors.New("label: label size must be positive")
	}
	if s.MarginTopMM < 0 || s.MarginLeftMM < 0 || s.GapXMM < 0 || s.GapYMM < 0 {
		return errors.New("label: margins and gaps must be non-negative")
	}
	if len(s.Fields) == 0 {
		return errors.New("label: at least one field required")
	}
	for _, f := range s.Fields {
		if !ValidFields[f] {
			return fmt.Errorf("label: unknown field %q", f)
		}
	}
	return nil
}

// LabelsPerSheet is the sheet capacity (rows × cols).
func (s SheetDef) LabelsPerSheet() int64 { return s.Rows * s.Cols }

// CellXY maps a 0-based label index (across pages) to its page (0-based)
// and top-left corner in mm. Pure layout math — fully unit-testable.
func (s SheetDef) CellXY(index int64) (page int64, xMM, yMM float64, err error) {
	if index < 0 {
		return 0, 0, 0, errors.New("label: negative index")
	}
	per := s.LabelsPerSheet()
	if per <= 0 {
		return 0, 0, 0, errors.New("label: empty sheet")
	}
	page = index / per
	pos := index % per
	row := pos / s.Cols
	col := pos % s.Cols
	xMM = s.MarginLeftMM + float64(col)*(s.LabelWMM+s.GapXMM)
	yMM = s.MarginTopMM + float64(row)*(s.LabelHMM+s.GapYMM)
	return page, xMM, yMM, nil
}

// PagesFor reports how many pages n labels need on this sheet.
func (s SheetDef) PagesFor(n int64) int64 {
	if n <= 0 {
		return 0
	}
	per := s.LabelsPerSheet()
	return (n + per - 1) / per
}

// DefaultAvery65 returns the Avery-compatible 65-up default (5 cols × 13
// rows of 38×21.2mm labels on A4 — L7651-family geometry).
func DefaultAvery65(entityID int64) SheetDef {
	return SheetDef{
		EntityID: entityID, Code: "avery-65", Name: "Avery 65-up (38x21.2mm)",
		Rows: 13, Cols: 5,
		LabelWMM: 38, LabelHMM: 21.2,
		MarginTopMM: 10, MarginLeftMM: 10, GapXMM: 2.5, GapYMM: 0,
		Fields: []string{FieldProductRef, FieldProductLabel, FieldPrice},
	}
}

// LabelRow is one printable label (fields projected from product/org rows
// by the caller seam; money in int64 minor units).
type LabelRow struct {
	ProductRef   string `json:"product_ref"`
	ProductLabel string `json:"product_label"`
	OrgName      string `json:"org_name"`
	PriceMinor   int64  `json:"price_minor"`
	Currency     string `json:"currency"`
}

// Lines renders the row's selected fields as text lines.
func (r LabelRow) Lines(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		switch f {
		case FieldProductRef:
			out = append(out, r.ProductRef)
		case FieldProductLabel:
			out = append(out, r.ProductLabel)
		case FieldOrgName:
			out = append(out, r.OrgName)
		case FieldPrice:
			out = append(out, formatMoney(r.PriceMinor, r.Currency))
		}
	}
	return out
}

func formatMoney(minor int64, currency string) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := fmt.Sprintf("%d.%02d %s", minor/100, minor%100, strings.ToUpper(strings.TrimSpace(currency)))
	s = strings.TrimSpace(s)
	if neg {
		return "-" + s
	}
	return s
}

// LabelSubject is the docgen renderer input for the label family.
type LabelSubject struct {
	Sheet SheetDef
	Rows  []LabelRow
	Title string
}
