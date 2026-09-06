package partners

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for the partners context.
type Store interface {
	CreateOrg(ctx context.Context, o *Organization) error
	OrgByID(ctx context.Context, id int64) (Organization, error)
	ListOrgs(ctx context.Context, entityID int64, limit, offset int) ([]Organization, error)
	UpdateOrg(ctx context.Context, o *Organization) error
	ParentOf(ctx context.Context, id int64) (*int64, bool)
	CreateContact(ctx context.Context, c *Contact) error
	ContactsOf(ctx context.Context, orgID int64) ([]Contact, error)
	CreateCategory(ctx context.Context, c *Category) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const orgCols = `id, entity_id, name, alias, ref_ext, parent_id, status, is_customer,
	is_supplier, is_prospect, customer_code, supplier_code, email, phone, address,
	acct_customer, acct_supplier, custom_fields, created_at, updated_at,
	created_by, updated_by, row_version`

func scanOrg(row pgx.Row) (Organization, error) {
	var o Organization
	var addr, custom []byte
	err := row.Scan(&o.ID, &o.EntityID, &o.Name, &o.Alias, &o.RefExt, &o.ParentID, &o.Status,
		&o.IsCustomer, &o.IsSupplier, &o.IsProspect, &o.CustomerCode, &o.SupplierCode,
		&o.Email, &o.Phone, &addr, &o.AcctCustomer, &o.AcctSupplier, &custom,
		&o.CreatedAt, &o.UpdatedAt, &o.CreatedBy, &o.UpdatedBy, &o.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, identity.ErrNotFound
	}
	if err != nil {
		return Organization{}, err
	}
	_ = json.Unmarshal(addr, &o.Address)
	_ = json.Unmarshal(custom, &o.CustomFields)
	return o, nil
}

// Omitted created/updated timestamps on the domain struct are tracked via RowVersion;
// full audit timestamps live in the row. (Timestamps intentionally minimal in domain.)

func (s *PGStore) CreateOrg(ctx context.Context, o *Organization) error {
	addr, _ := json.Marshal(o.Address)
	custom, _ := json.Marshal(nullableMap(o.CustomFields))
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_organizations
		(entity_id, name, alias, ref_ext, parent_id, status, is_customer, is_supplier, is_prospect,
		 customer_code, supplier_code, email, phone, address, acct_customer, acct_supplier,
		 custom_fields, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
			NULLIF($10,''),NULLIF($11,''),$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING id, row_version`,
		o.EntityID, o.Name, o.Alias, o.RefExt, o.ParentID, o.Status, o.IsCustomer, o.IsSupplier,
		o.IsProspect, o.CustomerCode, o.SupplierCode, o.Email, o.Phone, addr,
		o.AcctCustomer, o.AcctSupplier, custom, o.CreatedBy, o.UpdatedBy,
	).Scan(&o.ID, &o.RowVersion)
}

func nullableMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (s *PGStore) OrgByID(ctx context.Context, id int64) (Organization, error) {
	return scanOrg(s.pool.QueryRow(ctx, `SELECT `+orgCols+` FROM ferp_organizations WHERE id=$1`, id))
}

func (s *PGStore) ListOrgs(ctx context.Context, entityID int64, limit, offset int) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+orgCols+` FROM ferp_organizations
		WHERE entity_id=$1 ORDER BY name LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Organization
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateOrg(ctx context.Context, o *Organization) error {
	addr, _ := json.Marshal(o.Address)
	custom, _ := json.Marshal(nullableMap(o.CustomFields))
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_organizations SET name=$1, alias=$2, ref_ext=$3,
		parent_id=$4, status=$5, is_customer=$6, is_supplier=$7, is_prospect=$8,
		customer_code=NULLIF($9,''), supplier_code=NULLIF($10,''), email=$11, phone=$12,
		address=$13, acct_customer=$14, acct_supplier=$15, custom_fields=$16,
		updated_at=now(), updated_by=$17, row_version=row_version+1
		WHERE id=$18 AND row_version=$19`,
		o.Name, o.Alias, o.RefExt, o.ParentID, o.Status, o.IsCustomer, o.IsSupplier, o.IsProspect,
		o.CustomerCode, o.SupplierCode, o.Email, o.Phone, addr, o.AcctCustomer, o.AcctSupplier,
		custom, o.UpdatedBy, o.ID, o.RowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrVersionConflict
	}
	o.RowVersion++
	return nil
}

func (s *PGStore) ParentOf(ctx context.Context, id int64) (*int64, bool) {
	var parent *int64
	err := s.pool.QueryRow(ctx, `SELECT parent_id FROM ferp_organizations WHERE id=$1`, id).Scan(&parent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	return parent, true
}

const contactCols = `id, entity_id, org_id, first_name, last_name, email, phone, role,
	is_default, custom_fields, created_at, updated_at, created_by, updated_by, row_version`

func scanContact(row pgx.Row) (Contact, error) {
	var c Contact
	var custom []byte
	err := row.Scan(&c.ID, &c.EntityID, &c.OrgID, &c.FirstName, &c.LastName, &c.Email,
		&c.Phone, &c.Role, &c.IsDefault, &custom,
		&c.CreatedAt, &c.UpdatedAt, &c.CreatedBy, &c.UpdatedBy, &c.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, identity.ErrNotFound
	}
	if err != nil {
		return Contact{}, err
	}
	_ = json.Unmarshal(custom, &c.CustomFields)
	return c, nil
}

func (s *PGStore) CreateContact(ctx context.Context, c *Contact) error {
	custom, _ := json.Marshal(nullableMap(c.CustomFields))
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_contacts
		(entity_id, org_id, first_name, last_name, email, phone, role, is_default, custom_fields, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id, row_version`,
		c.EntityID, c.OrgID, c.FirstName, c.LastName, c.Email, c.Phone, c.Role,
		c.IsDefault, custom, c.CreatedBy, c.UpdatedBy,
	).Scan(&c.ID, &c.RowVersion)
}

func (s *PGStore) ContactsOf(ctx context.Context, orgID int64) ([]Contact, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contactCols+` FROM ferp_contacts WHERE org_id=$1 ORDER BY last_name, first_name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) CreateCategory(ctx context.Context, c *Category) error {
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_categories (entity_id, code, label, scope)
		VALUES ($1,$2,$3,$4) RETURNING id`, c.EntityID, c.Code, c.Label, c.Scope).Scan(&c.ID)
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	orgs     map[int64]Organization
	contacts map[int64]Contact
	cats     map[int64]Category
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{orgs: map[int64]Organization{}, contacts: map[int64]Contact{}, cats: map[int64]Category{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateOrg(_ context.Context, o *Organization) error {
	if err := o.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.orgs {
		if e.EntityID == o.EntityID && o.CustomerCode != "" && e.CustomerCode == o.CustomerCode {
			return errors.New("partners: duplicate customer code")
		}
		if e.EntityID == o.EntityID && o.SupplierCode != "" && e.SupplierCode == o.SupplierCode {
			return errors.New("partners: duplicate supplier code")
		}
	}
	o.ID = m.next()
	o.RowVersion = 1
	m.orgs[o.ID] = *o
	return nil
}

func (m *MemoryStore) OrgByID(_ context.Context, id int64) (Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return Organization{}, identity.ErrNotFound
	}
	return o, nil
}

func (m *MemoryStore) ListOrgs(_ context.Context, entityID int64, limit, offset int) ([]Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Organization
	for _, o := range m.orgs {
		if o.EntityID == entityID {
			out = append(out, o)
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

func (m *MemoryStore) UpdateOrg(_ context.Context, o *Organization) error {
	if err := o.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.orgs[o.ID]
	if !ok {
		return identity.ErrNotFound
	}
	if cur.RowVersion != o.RowVersion {
		return identity.ErrVersionConflict
	}
	o.RowVersion++
	m.orgs[o.ID] = *o
	return nil
}

func (m *MemoryStore) ParentOf(_ context.Context, id int64) (*int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return nil, false
	}
	return o.ParentID, true
}

func (m *MemoryStore) CreateContact(_ context.Context, c *Contact) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orgs[c.OrgID]; !ok {
		return errors.New("partners: organization not found")
	}
	c.ID = m.next()
	c.RowVersion = 1
	m.contacts[c.ID] = *c
	return nil
}

func (m *MemoryStore) ContactsOf(_ context.Context, orgID int64) ([]Contact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Contact
	for _, c := range m.contacts {
		if c.OrgID == orgID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateCategory(_ context.Context, c *Category) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID = m.next()
	m.cats[c.ID] = *c
	return nil
}
