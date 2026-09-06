package manufacturing

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for the manufacturing context.
type Store interface {
	CreateBOM(ctx context.Context, b *BOM) error
	BOMByID(ctx context.Context, id int64) (BOM, error)
	ListBOMs(ctx context.Context, entityID int64, limit, offset int) ([]BOM, error)
	SetBOMStatus(ctx context.Context, id int64, to BOMStatus, rowVersion int64) (BOM, error)
	AddLine(ctx context.Context, l *BOMLine) error
	LinesOf(ctx context.Context, bomID int64) ([]BOMLine, error)
	CreateMO(ctx context.Context, m *ManufacturingOrder) error
	MOByID(ctx context.Context, id int64) (ManufacturingOrder, error)
	SetMOStatus(ctx context.Context, id int64, to MOStatus, rowVersion int64) (ManufacturingOrder, error)
	MarkProduced(ctx context.Context, id int64, rowVersion int64) (ManufacturingOrder, error)
}

// Ledger abstracts the catalog stock postings used at produce time.
type Ledger interface {
	Level(ctx context.Context, productID, warehouseID int64) (catalog.StockLevel, error)
	AppendMovement(ctx context.Context, m *catalog.StockMovement, allowNegative bool) (catalog.StockLevel, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const bomCols = `id, entity_id, ref, product_id, label, revision, status, created_at, updated_at, row_version`

func scanBOM(row pgx.Row) (BOM, error) {
	var b BOM
	err := row.Scan(&b.ID, &b.EntityID, &b.Ref, &b.ProductID, &b.Label, &b.Revision,
		&b.Status, &b.CreatedAt, &b.UpdatedAt, &b.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return BOM{}, identity.ErrNotFound
	}
	return b, err
}

func (s *PGStore) CreateBOM(ctx context.Context, b *BOM) error {
	if err := b.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_boms
		(entity_id, ref, product_id, label, revision, status)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, row_version`,
		b.EntityID, b.Ref, b.ProductID, b.Label, b.Revision, b.Status,
	).Scan(&b.ID, &b.RowVersion)
}

func (s *PGStore) BOMByID(ctx context.Context, id int64) (BOM, error) {
	return scanBOM(s.pool.QueryRow(ctx, `SELECT `+bomCols+` FROM ferp_boms WHERE id=$1`, id))
}

func (s *PGStore) ListBOMs(ctx context.Context, entityID int64, limit, offset int) ([]BOM, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+bomCols+` FROM ferp_boms
		WHERE entity_id=$1 ORDER BY ref LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BOM
	for rows.Next() {
		b, err := scanBOM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) SetBOMStatus(ctx context.Context, id int64, to BOMStatus, rowVersion int64) (BOM, error) {
	b, err := s.BOMByID(ctx, id)
	if err != nil {
		return BOM{}, err
	}
	if b.RowVersion != rowVersion {
		return BOM{}, identity.ErrVersionConflict
	}
	if !b.CanTransition(to) {
		return BOM{}, errors.New("manufacturing: illegal BOM transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_boms SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return BOM{}, err
	}
	if tag.RowsAffected() == 0 {
		return BOM{}, identity.ErrVersionConflict
	}
	b.Status = to
	b.RowVersion++
	return b, nil
}

func (s *PGStore) AddLine(ctx context.Context, l *BOMLine) error {
	b, err := s.BOMByID(ctx, l.BOMID)
	if err != nil {
		return err
	}
	if err := l.Validate(b.ProductID); err != nil {
		return err
	}
	if b.Status == BOMObsolete {
		return errors.New("manufacturing: BOM obsolete, lines frozen")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_bom_lines
		(entity_id, bom_id, component_id, qty, position)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		l.EntityID, l.BOMID, l.ComponentID, l.Qty, l.Position,
	).Scan(&l.ID)
}

func (s *PGStore) LinesOf(ctx context.Context, bomID int64) ([]BOMLine, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, entity_id, bom_id, component_id, qty, position
		FROM ferp_bom_lines WHERE bom_id=$1 ORDER BY position, id`, bomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BOMLine
	for rows.Next() {
		var l BOMLine
		if err := rows.Scan(&l.ID, &l.EntityID, &l.BOMID, &l.ComponentID, &l.Qty, &l.Position); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

const moCols = `id, entity_id, ref, bom_id, product_id, warehouse_id, qty, status, created_at, updated_at, row_version`

func scanMO(row pgx.Row) (ManufacturingOrder, error) {
	var m ManufacturingOrder
	err := row.Scan(&m.ID, &m.EntityID, &m.Ref, &m.BOMID, &m.ProductID, &m.WarehouseID,
		&m.Qty, &m.Status, &m.CreatedAt, &m.UpdatedAt, &m.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManufacturingOrder{}, identity.ErrNotFound
	}
	return m, err
}

func (s *PGStore) CreateMO(ctx context.Context, m *ManufacturingOrder) error {
	if err := m.Validate(); err != nil {
		return err
	}
	b, err := s.BOMByID(ctx, m.BOMID)
	if err != nil {
		return err
	}
	if b.ProductID != m.ProductID {
		return errors.New("manufacturing: MO product must match BOM product")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_mos
		(entity_id, ref, bom_id, product_id, warehouse_id, qty, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		m.EntityID, m.Ref, m.BOMID, m.ProductID, m.WarehouseID, m.Qty, m.Status,
	).Scan(&m.ID, &m.RowVersion)
}

func (s *PGStore) MOByID(ctx context.Context, id int64) (ManufacturingOrder, error) {
	return scanMO(s.pool.QueryRow(ctx, `SELECT `+moCols+` FROM ferp_mos WHERE id=$1`, id))
}

func (s *PGStore) SetMOStatus(ctx context.Context, id int64, to MOStatus, rowVersion int64) (ManufacturingOrder, error) {
	m, err := s.MOByID(ctx, id)
	if err != nil {
		return ManufacturingOrder{}, err
	}
	if m.RowVersion != rowVersion {
		return ManufacturingOrder{}, identity.ErrVersionConflict
	}
	if !m.CanTransition(to) {
		return ManufacturingOrder{}, errors.New("manufacturing: illegal MO transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_mos SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return ManufacturingOrder{}, err
	}
	if tag.RowsAffected() == 0 {
		return ManufacturingOrder{}, identity.ErrVersionConflict
	}
	m.Status = to
	m.RowVersion++
	return m, nil
}

// PostProduce validates availability, posts consume + produce moves through the
// ledger, and returns the posting plan. The caller flips the MO to produced
// via MarkProduced after a successful posting.
func PostProduce(ctx context.Context, mo ManufacturingOrder, lines []BOMLine, ledger Ledger) (ProducePlan, error) {
	plan, err := PlanProduce(mo, lines)
	if err != nil {
		return ProducePlan{}, err
	}
	for _, c := range plan.Consumes {
		lvl, err := ledger.Level(ctx, c.ComponentID, mo.WarehouseID)
		if err != nil {
			return ProducePlan{}, err
		}
		if lvl.Qty < -c.Qty {
			return ProducePlan{}, errors.New("manufacturing: insufficient component stock")
		}
	}
	for _, c := range plan.Consumes {
		qty := c.Qty
		if _, err := ledger.AppendMovement(ctx, &catalog.StockMovement{
			EntityID: mo.EntityID, ProductID: c.ComponentID, WarehouseID: mo.WarehouseID,
			Qty: qty, Reason: catalog.ReasonConsume, Ref: mo.Ref}, false); err != nil {
			return ProducePlan{}, err
		}
	}
	if _, err := ledger.AppendMovement(ctx, &catalog.StockMovement{
		EntityID: mo.EntityID, ProductID: plan.Produce.ProductID, WarehouseID: mo.WarehouseID,
		Qty: plan.Produce.Qty, Reason: catalog.ReasonProduce, Ref: mo.Ref}, false); err != nil {
		return ProducePlan{}, err
	}
	return plan, nil
}

func (s *PGStore) MarkProduced(ctx context.Context, id int64, rowVersion int64) (ManufacturingOrder, error) {
	return s.SetMOStatus(ctx, id, MOProduced, rowVersion)
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu    sync.Mutex
	seq   int64
	boms  map[int64]BOM
	lines map[int64]BOMLine
	mos   map[int64]ManufacturingOrder
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{boms: map[int64]BOM{}, lines: map[int64]BOMLine{}, mos: map[int64]ManufacturingOrder{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateBOM(_ context.Context, b *BOM) error {
	if err := b.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.boms {
		if e.EntityID == b.EntityID && e.Ref == b.Ref {
			return errors.New("manufacturing: duplicate BOM ref")
		}
	}
	b.ID = m.next()
	b.RowVersion = 1
	m.boms[b.ID] = *b
	return nil
}

func (m *MemoryStore) BOMByID(_ context.Context, id int64) (BOM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boms[id]
	if !ok {
		return BOM{}, identity.ErrNotFound
	}
	return b, nil
}

func (m *MemoryStore) ListBOMs(_ context.Context, entityID int64, limit, offset int) ([]BOM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BOM
	for _, b := range m.boms {
		if b.EntityID == entityID {
			out = append(out, b)
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

func (m *MemoryStore) SetBOMStatus(_ context.Context, id int64, to BOMStatus, rowVersion int64) (BOM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boms[id]
	if !ok {
		return BOM{}, identity.ErrNotFound
	}
	if b.RowVersion != rowVersion {
		return BOM{}, identity.ErrVersionConflict
	}
	if !b.CanTransition(to) {
		return BOM{}, errors.New("manufacturing: illegal BOM transition")
	}
	b.Status = to
	b.RowVersion++
	m.boms[id] = b
	return b, nil
}

func (m *MemoryStore) AddLine(_ context.Context, l *BOMLine) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boms[l.BOMID]
	if !ok {
		return errors.New("manufacturing: BOM not found")
	}
	if err := l.Validate(b.ProductID); err != nil {
		return err
	}
	if b.Status == BOMObsolete {
		return errors.New("manufacturing: BOM obsolete, lines frozen")
	}
	for _, e := range m.lines {
		if e.BOMID == l.BOMID && e.ComponentID == l.ComponentID {
			return errors.New("manufacturing: duplicate component")
		}
	}
	l.ID = m.next()
	m.lines[l.ID] = *l
	return nil
}

func (m *MemoryStore) LinesOf(_ context.Context, bomID int64) ([]BOMLine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BOMLine
	for _, l := range m.lines {
		if l.BOMID == bomID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateMO(_ context.Context, mo *ManufacturingOrder) error {
	if err := mo.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boms[mo.BOMID]
	if !ok {
		return errors.New("manufacturing: BOM not found")
	}
	if b.ProductID != mo.ProductID {
		return errors.New("manufacturing: MO product must match BOM product")
	}
	for _, e := range m.mos {
		if e.EntityID == mo.EntityID && e.Ref == mo.Ref {
			return errors.New("manufacturing: duplicate MO ref")
		}
	}
	mo.ID = m.next()
	mo.RowVersion = 1
	m.mos[mo.ID] = *mo
	return nil
}

func (m *MemoryStore) MOByID(_ context.Context, id int64) (ManufacturingOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mo, ok := m.mos[id]
	if !ok {
		return ManufacturingOrder{}, identity.ErrNotFound
	}
	return mo, nil
}

func (m *MemoryStore) SetMOStatus(_ context.Context, id int64, to MOStatus, rowVersion int64) (ManufacturingOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mo, ok := m.mos[id]
	if !ok {
		return ManufacturingOrder{}, identity.ErrNotFound
	}
	if mo.RowVersion != rowVersion {
		return ManufacturingOrder{}, identity.ErrVersionConflict
	}
	if !mo.CanTransition(to) {
		return ManufacturingOrder{}, errors.New("manufacturing: illegal MO transition")
	}
	mo.Status = to
	mo.RowVersion++
	m.mos[id] = mo
	return mo, nil
}

func (m *MemoryStore) MarkProduced(_ context.Context, id int64, rowVersion int64) (ManufacturingOrder, error) {
	return m.SetMOStatus(context.Background(), id, MOProduced, rowVersion)
}
