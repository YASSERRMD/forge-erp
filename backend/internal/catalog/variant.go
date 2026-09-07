// Package catalog variant support: sellable product variations with
// attribute sets, price deltas and validated barcodes (Dolibarr variants +
// barcode modules). EAN-13 check digits are verified; other symbologies pass
// through as opaque strings.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Variant is one sellable product variation.
type Variant struct {
	ID         int64          `json:"id"`
	EntityID   int64          `json:"entity_id"`
	ProductID  int64          `json:"product_id"`
	SKU        string         `json:"sku"` // unique per entity (combination ref)
	Attributes map[string]any `json:"attributes"`
	PriceDelta int64          `json:"price_delta"` // minor units vs base price
	Barcode    string         `json:"barcode"`
	RowVersion int64          `json:"row_version"`
}

// Validate checks variant invariants.
func (v Variant) Validate() error {
	if v.EntityID <= 0 || v.ProductID <= 0 {
		return errors.New("catalog: entity_id and product_id required")
	}
	if strings.TrimSpace(v.SKU) == "" || !skuPattern.MatchString(v.SKU) {
		return fmt.Errorf("catalog: bad variant SKU %q", v.SKU)
	}
	if v.Barcode != "" {
		if err := CheckBarcode(v.Barcode); err != nil {
			return err
		}
	}
	return nil
}

// CheckBarcode verifies EAN-13 check digits for 13-digit codes; other
// symbologies (CODE128 etc.) pass through as opaque strings.
func CheckBarcode(code string) error {
	c := strings.TrimSpace(code)
	if c == "" {
		return errors.New("catalog: empty barcode")
	}
	if len(c) == 13 {
		sum := 0
		for i := 0; i < 12; i++ {
			d := int(c[i] - '0')
			if d < 0 || d > 9 {
				return fmt.Errorf("catalog: bad EAN-13 %q", code)
			}
			if i%2 == 1 {
				sum += 3 * d
			} else {
				sum += d
			}
		}
		if check := (10 - sum%10) % 10; check != int(c[12]-'0') {
			return fmt.Errorf("catalog: bad EAN-13 check digit in %q", code)
		}
	}
	return nil
}

// VariantStore is the persistence contract for variants.
type VariantStore interface {
	CreateVariant(ctx context.Context, v *Variant) error
	VariantsOf(ctx context.Context, productID int64) ([]Variant, error)
}

const variantCols = `id, entity_id, product_id, sku, attributes, price_delta, barcode, row_version`

func scanVariant(row pgx.Row) (Variant, error) {
	var v Variant
	var attrs []byte
	err := row.Scan(&v.ID, &v.EntityID, &v.ProductID, &v.SKU, &attrs,
		&v.PriceDelta, &v.Barcode, &v.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Variant{}, identity.ErrNotFound
	}
	if err != nil {
		return Variant{}, err
	}
	_ = json.Unmarshal(attrs, &v.Attributes)
	return v, nil
}

// CreateVariant persists a variant on the PG store.
func (s *PGStore) CreateVariant(ctx context.Context, v *Variant) error {
	if err := v.Validate(); err != nil {
		return err
	}
	attrs, _ := json.Marshal(v.Attributes)
	if attrs == nil {
		attrs = []byte("{}")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_product_variants
		(entity_id, product_id, sku, attributes, price_delta, barcode)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')) RETURNING id, row_version`,
		v.EntityID, v.ProductID, v.SKU, attrs, v.PriceDelta, v.Barcode,
	).Scan(&v.ID, &v.RowVersion)
}

// VariantsOf lists a product's variants on the PG store.
func (s *PGStore) VariantsOf(ctx context.Context, productID int64) ([]Variant, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+variantCols+` FROM ferp_product_variants
		WHERE product_id=$1 ORDER BY sku`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Variant
	for rows.Next() {
		v, err := scanVariant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateVariant persists a variant on the memory fake.
func (m *MemoryStore) CreateVariant(_ context.Context, v *Variant) error {
	if err := v.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.variants {
		if e.EntityID == v.EntityID && e.SKU == v.SKU {
			return errors.New("catalog: duplicate variant SKU")
		}
	}
	v.ID = m.next()
	v.RowVersion = 1
	m.variants[v.ID] = *v
	return nil
}

// VariantsOf lists a product's variants on the memory fake.
func (m *MemoryStore) VariantsOf(_ context.Context, productID int64) ([]Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Variant
	for _, v := range m.variants {
		if v.ProductID == productID {
			out = append(out, v)
		}
	}
	return out, nil
}
