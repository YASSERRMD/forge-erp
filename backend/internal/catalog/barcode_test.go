package catalog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompleteEAN13Vectors(t *testing.T) {
	// Known check digits (GS1 reference values).
	for base, want := range map[string]string{
		"590123412345": "5901234123457",
		"400638133393": "4006381333931",
		"978030640615": "9780306406157",
		"200000000000": "2000000000008",
	} {
		got, err := CompleteEAN13(base)
		if err != nil {
			t.Fatalf("base %s: %v", base, err)
		}
		if got != want {
			t.Errorf("base %s = %s want %s", base, got, want)
		}
		// Round-trip: generated codes pass the existing validator.
		if err := CheckBarcode(got); err != nil {
			t.Errorf("generated %s rejected: %v", got, err)
		}
	}
	for _, bad := range []string{"", "123", "1234567890123", "59012341234X", "59012341234 "} {
		if _, err := CompleteEAN13(bad); err == nil {
			t.Errorf("accepted base %q", bad)
		}
	}
}

func TestGenerateEAN13FromPrefix(t *testing.T) {
	full, err := GenerateEAN13("200")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "200") || len(full) != 13 {
		t.Fatalf("prefix build = %q", full)
	}
	if err := CheckBarcode(full); err != nil {
		t.Fatalf("generated prefix code rejected: %v", err)
	}
	if _, err := GenerateEAN13(""); err == nil {
		t.Error("empty prefix accepted")
	}
	if _, err := GenerateEAN13("ABC"); err == nil {
		t.Error("non-digit prefix accepted")
	}
	if _, err := GenerateEAN13("1234567890123"); err == nil {
		t.Error("overlong prefix accepted")
	}
}

func TestCode128Payload(t *testing.T) {
	if _, err := Code128Payload("CODE128-XYZ-001"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "has\x01control", "del\x7fhere", strings.Repeat("x", 81)} {
		if _, err := Code128Payload(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestBuildProductQR(t *testing.T) {
	got, err := BuildProductQR(1, "WID-001", "WID-001-L")
	if err != nil {
		t.Fatal(err)
	}
	if got != "FERP:P:1:WID-001:WID-001-L" {
		t.Fatalf("qr = %q", got)
	}
	got, err = BuildProductQR(2, "WID-001", "")
	if err != nil || got != "FERP:P:2:WID-001" {
		t.Fatalf("qr bare = %q err=%v", got, err)
	}
	for _, tc := range [][3]string{{"0", "SKU", ""}, {"1", "", ""}, {"1", "has space", ""}, {"1", "SKU", "bad:variant"}} {
		var entity int64 = 1
		if tc[0] == "0" {
			entity = 0
		}
		if _, err := BuildProductQR(entity, tc[1], tc[2]); err == nil {
			t.Errorf("accepted %+v", tc)
		}
	}
}

func TestBuildLabelSheet(t *testing.T) {
	sheet, err := BuildLabelSheet([]LabelItem{
		{SKU: "WID-001", Name: "Widget", Barcode: "5901234123457", Symbology: SymEAN13, Copies: 2},
		{SKU: "WID-002", Name: "Gadget", Barcode: "CODE128-X", Symbology: SymCode128, Copies: 3},
		{SKU: "WID-003", Name: "Thing", Barcode: "FERP:P:1:WID-003", Symbology: SymQR, Copies: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sheet.TotalLabels != 6 {
		t.Fatalf("total = %d want 6", sheet.TotalLabels)
	}
	if _, err := BuildLabelSheet(nil); err == nil {
		t.Error("empty sheet accepted")
	}
	if _, err := BuildLabelSheet([]LabelItem{
		{SKU: "X", Barcode: "5901234123450", Symbology: SymEAN13, Copies: 1},
	}); err == nil {
		t.Error("bad check digit accepted on sheet")
	}
	if _, err := BuildLabelSheet([]LabelItem{
		{SKU: "X", Barcode: "CODE128-X", Symbology: "datamatrix", Copies: 1},
	}); err == nil {
		t.Error("unknown symbology accepted")
	}
}

func TestLabelSheetEndpoint(t *testing.T) {
	h := testRouter()
	raw, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"sku": "WID-001", "name": "Widget", "barcode": "5901234123457",
			"symbology": "ean13", "copies": 2},
		{"sku": "WID-002", "name": "Gadget", "barcode": "CODE128-X",
			"symbology": "code128", "copies": 1},
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/barcode/labels", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("labels: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sheet LabelSheet
	_ = json.NewDecoder(rec.Body).Decode(&sheet)
	if sheet.TotalLabels != 3 {
		t.Fatalf("total=%d want 3", sheet.TotalLabels)
	}
	// Bad check digit → 422.
	raw, _ = json.Marshal(map[string]any{"items": []map[string]any{
		{"sku": "X", "barcode": "5901234123450", "symbology": "ean13", "copies": 1},
	}})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/barcode/labels", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad barcode: code=%d want 422", rec.Code)
	}
}
