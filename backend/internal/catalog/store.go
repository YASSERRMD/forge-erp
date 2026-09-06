package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for the catalog context.
type Store interface {
	CreateProduct(ctx context.Context, p *Product) error
	ProductByID(ctx context.Context, id int64) (Product, error)
	ListProducts(ctx context.Context, entityID int64, limit, offset int) ([]Product, error)
	CreateWarehouse(ctx context.Context, w *Warehouse) error
	WarehouseByID(ctx context.Context, id int64) (Warehouse, error)
	// AppendMovement validates, appends the ledger line, and advances the level
	// atomically (PG) — the negative-stock guard lives in Apply.
	AppendMovement(ctx context.Context, m *StockMovement, allowNegative bool) (StockLevel, error)
	Level(ctx context.Context, productID, warehouseID int64) (StockLevel, error)
	CreateLot(ctx context.Context, l *Lot) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const productCols = `id, entity_id, sku, name, type, unit, net_price, vat_rate_bps,
	status, stock_tracked, custom_fields, created_at, updated_at, created_by, updated_by, row_version`

func scanProduct(row pgx.Row) (Product, error) {
	var p Product
	var custom []byte
	err := row.Scan(&p.ID, &p.EntityID, &p.SKU, &p.Name, &p.Type, &p.Unit, &p.NetPrice,
		&p.VATRateBps, &p.Status, &p.StockTracked, &custom,
		&p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy, &p.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, identity.ErrNotFound
	}
	if err != nil {
		return Product{}, err
	}
	_ = json.Unmarshal(custom, &p.CustomFields)
	return p, nil
}

func (s *PGStore) CreateProduct(ctx context.Context, p *Product) error {
	custom, _ := json.Marshal(nullMap(p.CustomFields))
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_products
		(entity_id, sku, name, type, unit, net_price, vat_rate_bps, status, stock_tracked,
		 custom_fields, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id, row_version`,
		p.EntityID, p.SKU, p.Name, p.Type, p.Unit, p.NetPrice, p.VATRateBps, p.Status,
		p.StockTracked, custom, p.CreatedBy, p.UpdatedBy,
	).Scan(&p.ID, &p.RowVersion)
}

func nullMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (s *PGStore) ProductByID(ctx context.Context, id int64) (Product, error) {
	return scanProduct(s.pool.QueryRow(ctx, `SELECT `+productCols+` FROM ferp_products WHERE id=$1`, id))
}

func (s *PGStore) ListProducts(ctx context.Context, entityID int64, limit, offset int) ([]Product, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+productCols+` FROM ferp_products
		WHERE entity_id=$1 ORDER BY name LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PGStore) CreateWarehouse(ctx context.Context, w *Warehouse) error {
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_warehouses (entity_id, code, label, status)
		VALUES ($1,$2,$3,$4) RETURNING id, created_at, updated_at`,
		w.EntityID, w.Code, w.Label, w.Status).Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt)
}

func (s *PGStore) WarehouseByID(ctx context.Context, id int64) (Warehouse, error) {
	var w Warehouse
	err := s.pool.QueryRow(ctx, `SELECT id, entity_id, code, label, status, created_at, updated_at
		FROM ferp_warehouses WHERE id=$1`, id).Scan(
		&w.ID, &w.EntityID, &w.Code, &w.Label, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Warehouse{}, identity.ErrNotFound
	}
	return w, err
}

func (s *PGStore) Level(ctx context.Context, productID, warehouseID int64) (StockLevel, error) {
	var l StockLevel
	err := s.pool.QueryRow(ctx, `SELECT product_id, warehouse_id, qty, total_value
		FROM ferp_stock_levels WHERE product_id=$1 AND warehouse_id=$2`,
		productID, warehouseID).Scan(&l.ProductID, &l.WarehouseID, &l.Qty, &l.TotalValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return StockLevel{ProductID: productID, WarehouseID: warehouseID}, nil
	}
	return l, err
}

func (s *PGStore) AppendMovement(ctx context.Context, m *StockMovement, allowNegative bool) (StockLevel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StockLevel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cur StockLevel
	err = tx.QueryRow(ctx, `SELECT product_id, warehouse_id, qty, total_value FROM ferp_stock_levels
		WHERE product_id=$1 AND warehouse_id=$2 FOR UPDATE`, m.ProductID, m.WarehouseID).
		Scan(&cur.ProductID, &cur.WarehouseID, &cur.Qty, &cur.TotalValue)
	if errors.Is(err, pgx.ErrNoRows) {
		cur = StockLevel{ProductID: m.ProductID, WarehouseID: m.WarehouseID}
	} else if err != nil {
		return StockLevel{}, err
	}
	next, err := Apply(cur, *m, allowNegative)
	if err != nil {
		return StockLevel{}, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO ferp_stock_movements
		(entity_id, product_id, warehouse_id, lot_id, qty, unit_cost, reason, ref, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, created_at`,
		m.EntityID, m.ProductID, m.WarehouseID, m.LotID, m.Qty, m.UnitCost, m.Reason, m.Ref, m.CreatedBy,
	).Scan(&m.ID, &m.CreatedAt); err != nil {
		return StockLevel{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ferp_stock_levels (product_id, warehouse_id, qty, total_value, updated_at)
		VALUES ($1,$2,$3,$4,now())
		ON CONFLICT (product_id, warehouse_id) DO UPDATE
		SET qty=$3, total_value=$4, updated_at=now()`,
		next.ProductID, next.WarehouseID, next.Qty, next.TotalValue); err != nil {
		return StockLevel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StockLevel{}, err
	}
	return next, nil
}

func (s *PGStore) CreateLot(ctx context.Context, l *Lot) error {
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_lots (entity_id, product_id, number, expires_at)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		l.EntityID, l.ProductID, l.Number, l.ExpiresAt).Scan(&l.ID)
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	products map[int64]Product
	bySKU    map[string]int64
	houses   map[int64]Warehouse
	levels   map[[2]int64]StockLevel
	moves    []StockMovement
	lots     map[int64]Lot
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		products: map[int64]Product{}, bySKU: map[string]int64{},
		houses: map[int64]Warehouse{}, levels: map[[2]int64]StockLevel{},
		lots: map[int64]Lot{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateProduct(_ context.Context, p *Product) error {
	if err := p.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := skuKey(p.EntityID, p.SKU)
	if _, dup := m.bySKU[k]; dup {
		return errors.New("catalog: duplicate SKU")
	}
	p.ID = m.next()
	p.RowVersion = 1
	m.products[p.ID] = *p
	m.bySKU[k] = p.ID
	return nil
}

func skuKey(entityID int64, sku string) string { return fmt.Sprintf("%d\x00%s", entityID, sku) }

func (m *MemoryStore) ProductByID(_ context.Context, id int64) (Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.products[id]
	if !ok {
		return Product{}, identity.ErrNotFound
	}
	return p, nil
}

func (m *MemoryStore) ListProducts(_ context.Context, entityID int64, limit, offset int) ([]Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Product
	for _, p := range m.products {
		if p.EntityID == entityID {
			out = append(out, p)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) CreateWarehouse(_ context.Context, w *Warehouse) error {
	if err := w.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w.ID = m.next()
	m.houses[w.ID] = *w
	return nil
}

func (m *MemoryStore) WarehouseByID(_ context.Context, id int64) (Warehouse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.houses[id]
	if !ok {
		return Warehouse{}, identity.ErrNotFound
	}
	return w, nil
}

func (m *MemoryStore) AppendMovement(_ context.Context, mov *StockMovement, allowNegative bool) (StockLevel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [2]int64{mov.ProductID, mov.WarehouseID}
	next, err := Apply(m.levels[k], *mov, allowNegative)
	if err != nil {
		return StockLevel{}, err
	}
	mov.ID = m.next()
	m.moves = append(m.moves, *mov)
	m.levels[k] = next
	return next, nil
}

func (m *MemoryStore) Level(_ context.Context, productID, warehouseID int64) (StockLevel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.levels[[2]int64{productID, warehouseID}], nil
}

func (m *MemoryStore) CreateLot(_ context.Context, l *Lot) error {
	if err := l.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l.ID = m.next()
	m.lots[l.ID] = *l
	return nil
}
