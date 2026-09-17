package label

import (
	"testing"
)

func goodSheet() SheetDef {
	s := DefaultAvery65(1)
	return s
}

func TestSheetValidation(t *testing.T) {
	if err := goodSheet().Validate(); err != nil {
		t.Fatalf("default rejected: %v", err)
	}
	bad := goodSheet()
	bad.Rows = 0
	if err := bad.Validate(); err == nil {
		t.Error("zero rows accepted")
	}
	bad = goodSheet()
	bad.Fields = []string{"product_ref", "nope"}
	if err := bad.Validate(); err == nil {
		t.Error("unknown field accepted")
	}
	bad = goodSheet()
	bad.GapXMM = -1
	if err := bad.Validate(); err == nil {
		t.Error("negative gap accepted")
	}
	bad = goodSheet()
	bad.Fields = nil
	if err := bad.Validate(); err == nil {
		t.Error("empty fields accepted")
	}
}

func TestCellLayout(t *testing.T) {
	s := SheetDef{EntityID: 1, Code: "t", Name: "t", Rows: 2, Cols: 3,
		LabelWMM: 10, LabelHMM: 20, MarginTopMM: 5, MarginLeftMM: 7,
		GapXMM: 1, GapYMM: 2, Fields: []string{FieldProductRef}}
	if got := s.LabelsPerSheet(); got != 6 {
		t.Fatalf("per-sheet=%d want 6", got)
	}
	// Index 0: page 0, top-left corner at the margins.
	page, x, y, err := s.CellXY(0)
	if err != nil || page != 0 || x != 7 || y != 5 {
		t.Fatalf("cell0=(%d,%v,%v) err=%v", page, x, y, err)
	}
	// Index 4: row 1, col 1 → x=7+1*(10+1)=18, y=5+1*(20+2)=27.
	page, x, y, err = s.CellXY(4)
	if err != nil || page != 0 || x != 18 || y != 27 {
		t.Fatalf("cell4=(%d,%v,%v) err=%v", page, x, y, err)
	}
	// Index 6 spills to page 1.
	if page, _, _, err := s.CellXY(6); err != nil || page != 1 {
		t.Fatalf("cell6 page=%d err=%v want 1", page, err)
	}
	if _, _, _, err := s.CellXY(-1); err == nil {
		t.Error("negative index accepted")
	}
	if got := s.PagesFor(13); got != 3 {
		t.Fatalf("pages=%d want 3", got)
	}
	if got := s.PagesFor(0); got != 0 {
		t.Fatalf("pages(0)=%d want 0", got)
	}
}

func TestRowLines(t *testing.T) {
	r := LabelRow{ProductRef: "SKU-1", ProductLabel: "Widget", OrgName: "Acme",
		PriceMinor: 1999, Currency: "eur"}
	lines := r.Lines([]string{FieldProductRef, FieldPrice})
	if len(lines) != 2 || lines[0] != "SKU-1" || lines[1] != "19.99 EUR" {
		t.Fatalf("lines=%q", lines)
	}
	if got := formatMoney(-50, "usd"); got != "-0.50 USD" {
		t.Fatalf("neg=%q", got)
	}
}
