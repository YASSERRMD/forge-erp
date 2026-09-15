// Barcode generation + label-sheet payloads (Phase 2). EAN-13 check-digit
// verification already lives in variant.go (CheckBarcode); this file adds the
// complementary direction plus the non-EAN symbologies as payload strings.
//
// Rendering images (PNG/SVG bars, QR matrices) is an explicit non-goal: it is
// a kernel-5 follow-up. Downstream print pipelines receive validated payload
// strings (LabelSheet JSON) and render them.
package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// ean13Check computes the EAN-13 check digit for exactly 12 decimal digits.
func ean13Check(first12 string) (byte, error) {
	if len(first12) != 12 {
		return 0, fmt.Errorf("catalog: EAN-13 base needs 12 digits, got %d", len(first12))
	}
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(first12[i] - '0')
		if d < 0 || d > 9 {
			return 0, fmt.Errorf("catalog: EAN-13 base needs digits, got %q", first12)
		}
		if i%2 == 1 {
			sum += 3 * d
		} else {
			sum += d
		}
	}
	return byte((10 - sum%10) % 10), nil
}

// CompleteEAN13 appends the valid check digit to a 12-digit base
// ("590123412345" → "5901234123457"). Known vectors: 400638133393→1,
// 978030640615→7.
func CompleteEAN13(first12 string) (string, error) {
	check, err := ean13Check(strings.TrimSpace(first12))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(first12) + string('0'+check), nil
}

// GenerateEAN13 builds a full EAN-13 from a shorter numeric prefix: the
// prefix is right-padded with zeros to 12 digits, then the check digit is
// appended. Uniqueness stays the caller's job (variant SKUs are unique per
// entity; generated codes should be recorded on the variant before reuse).
func GenerateEAN13(prefix string) (string, error) {
	p := strings.TrimSpace(prefix)
	if p == "" || len(p) > 12 {
		return "", fmt.Errorf("catalog: EAN-13 prefix needs 1-12 digits, got %q", prefix)
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return "", fmt.Errorf("catalog: EAN-13 prefix needs digits, got %q", prefix)
		}
	}
	return CompleteEAN13(p + strings.Repeat("0", 12-len(p)))
}

// Code128Payload validates a Code 128 data string and returns the print
// payload. Code 128 encodes full ASCII; control characters and DEL are
// rejected, everything else passes through verbatim (opaque, like
// CheckBarcode treats non-EAN codes). No image is rendered here.
func Code128Payload(data string) (string, error) {
	if data == "" {
		return "", errors.New("catalog: empty Code128 payload")
	}
	if len(data) > 80 {
		return "", fmt.Errorf("catalog: Code128 payload too long (%d > 80)", len(data))
	}
	for i := 0; i < len(data); i++ {
		if data[i] < 0x20 || data[i] == 0x7F {
			return "", fmt.Errorf("catalog: Code128 payload rejects control byte 0x%02x", data[i])
		}
	}
	return data, nil
}

// BuildProductQR returns the QR payload string identifying one sellable
// (entity + product SKU + optional variant SKU). Format is a stable URI-like
// tag so scanners and the kernel-5 renderer share one grammar:
// "FERP:P:{entity}:{product-sku}[:{variant-sku}]". No image is rendered here.
func BuildProductQR(entityID int64, productSKU, variantSKU string) (string, error) {
	if entityID <= 0 {
		return "", errors.New("catalog: QR needs an entity")
	}
	psku := strings.TrimSpace(productSKU)
	if psku == "" {
		return "", errors.New("catalog: QR needs a product SKU")
	}
	if strings.ContainsAny(psku, ": \t\n") {
		return "", fmt.Errorf("catalog: QR product SKU %q must not contain ':' or whitespace", productSKU)
	}
	out := fmt.Sprintf("FERP:P:%d:%s", entityID, psku)
	if v := strings.TrimSpace(variantSKU); v != "" {
		if strings.ContainsAny(v, ": \t\n") {
			return "", fmt.Errorf("catalog: QR variant SKU %q must not contain ':' or whitespace", variantSKU)
		}
		out += ":" + v
	}
	return out, nil
}

// Symbology names a barcode family for label sheets.
type Symbology string

const (
	SymEAN13   Symbology = "ean13"
	SymCode128 Symbology = "code128"
	SymQR      Symbology = "qr"
)

// LabelItem is one printable row: what to print and how many copies.
type LabelItem struct {
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	Barcode   string    `json:"barcode"`
	Symbology Symbology `json:"symbology"`
	Copies    int       `json:"copies"`
}

// Validate checks one label row (EAN-13 rows re-run the check-digit guard).
func (l LabelItem) Validate() error {
	if strings.TrimSpace(l.SKU) == "" {
		return errors.New("catalog: label needs a SKU")
	}
	if l.Copies <= 0 || l.Copies > 999 {
		return fmt.Errorf("catalog: label copies %d out of range 1-999", l.Copies)
	}
	switch l.Symbology {
	case SymEAN13:
		if err := CheckBarcode(l.Barcode); err != nil {
			return err
		}
		if len(strings.TrimSpace(l.Barcode)) != 13 {
			return fmt.Errorf("catalog: ean13 label needs a 13-digit code, got %q", l.Barcode)
		}
	case SymCode128:
		if _, err := Code128Payload(l.Barcode); err != nil {
			return err
		}
	case SymQR:
		if strings.TrimSpace(l.Barcode) == "" {
			return errors.New("catalog: QR label needs a payload")
		}
	default:
		return fmt.Errorf("catalog: unknown symbology %q", l.Symbology)
	}
	return nil
}

// LabelSheet is the print-pipeline payload: validated rows the kernel-5
// renderer turns into physical labels. TotalLabels is the copy-expanded count.
type LabelSheet struct {
	Items       []LabelItem `json:"items"`
	TotalLabels int         `json:"total_labels"`
}

// BuildLabelSheet validates every row and totals the copies.
func BuildLabelSheet(items []LabelItem) (LabelSheet, error) {
	if len(items) == 0 {
		return LabelSheet{}, errors.New("catalog: label sheet needs at least one item")
	}
	if len(items) > 500 {
		return LabelSheet{}, fmt.Errorf("catalog: label sheet too large (%d > 500)", len(items))
	}
	var total int
	for i := range items {
		if err := items[i].Validate(); err != nil {
			return LabelSheet{}, fmt.Errorf("catalog: label row %d: %w", i, err)
		}
		total += items[i].Copies
	}
	return LabelSheet{Items: items, TotalLabels: total}, nil
}
