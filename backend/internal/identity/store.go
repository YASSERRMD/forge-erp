package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("identity: not found")

// ErrVersionConflict is returned on optimistic-locking mismatch.
var ErrVersionConflict = errors.New("identity: row version conflict")

// Store is the persistence contract for the identity context.
// PGStore implements it against PostgreSQL; MemoryStore is the test fake.
type Store interface {
	CreateUser(ctx context.Context, u *User) error
	UserByID(ctx context.Context, id int64) (User, error)
	UserByLogin(ctx context.Context, entityID int64, login string) (User, error)
	UpdateUser(ctx context.Context, u *User) error
	CreateGroup(ctx context.Context, g *Group) error
	AddMember(ctx context.Context, groupID, userID int64) error
	UserGroups(ctx context.Context, userID int64) ([]Group, error)
	Grant(ctx context.Context, entityID int64, userID, groupID *int64, r Right) error
	DirectRights(ctx context.Context, userID int64) ([]Right, error)
	InheritedRights(ctx context.Context, userID int64) ([]Right, error)
	ResolveRights(ctx context.Context, u User) (direct, inherited []Right, err error)
	CreateSession(ctx context.Context, userID int64, tokenHash string, expires time.Time) error
	RevokeSession(ctx context.Context, tokenHash string) error
	SessionUser(ctx context.Context, tokenHash string, now time.Time) (User, error)
}

// PGStore is the PostgreSQL implementation.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) CreateUser(ctx context.Context, u *User) error {
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_users
		(entity_id, login, email, first_name, last_name, status, password_hash, is_admin, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at, row_version`,
		u.EntityID, u.Login, u.Email, u.FirstName, u.LastName, u.Status, u.PasswordHash, u.IsAdmin, u.CreatedBy, u.UpdatedBy,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt, &u.RowVersion)
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.EntityID, &u.Login, &u.Email, &u.FirstName, &u.LastName,
		&u.Status, &u.PasswordHash, &u.IsAdmin, &u.FailedAttempts, &u.LockedUntil,
		&u.CreatedAt, &u.UpdatedAt, &u.CreatedBy, &u.UpdatedBy, &u.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

const userCols = `id, entity_id, login, email, first_name, last_name, status, password_hash,
	is_admin, failed_attempts, locked_until, created_at, updated_at, created_by, updated_by, row_version`

func (s *PGStore) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM ferp_users WHERE id=$1`, id))
}

func (s *PGStore) UserByLogin(ctx context.Context, entityID int64, login string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM ferp_users WHERE entity_id=$1 AND login=$2`, entityID, login))
}

func (s *PGStore) UpdateUser(ctx context.Context, u *User) error {
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_users SET email=$1, first_name=$2, last_name=$3,
		status=$4, password_hash=$5, is_admin=$6, failed_attempts=$7, locked_until=$8,
		updated_at=now(), updated_by=$9, row_version=row_version+1
		WHERE id=$10 AND row_version=$11`,
		u.Email, u.FirstName, u.LastName, u.Status, u.PasswordHash, u.IsAdmin,
		u.FailedAttempts, u.LockedUntil, u.UpdatedBy, u.ID, u.RowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	u.RowVersion++
	return nil
}

func (s *PGStore) CreateGroup(ctx context.Context, g *Group) error {
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_groups (entity_id, code, label)
		VALUES ($1,$2,$3) RETURNING id, created_at, updated_at`,
		g.EntityID, g.Code, g.Label).Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
}

func (s *PGStore) AddMember(ctx context.Context, groupID, userID int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ferp_group_members (group_id, user_id)
		VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, userID)
	return err
}

func (s *PGStore) UserGroups(ctx context.Context, userID int64) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.id, g.entity_id, g.code, g.label, g.created_at, g.updated_at
		FROM ferp_groups g JOIN ferp_group_members m ON m.group_id=g.id WHERE m.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.EntityID, &g.Code, &g.Label, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *PGStore) Grant(ctx context.Context, entityID int64, userID, groupID *int64, r Right) error {
	if err := r.Validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO ferp_rights (entity_id, user_id, group_id, module, entity, action)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
		entityID, userID, groupID, r.Module, r.Entity, r.Action)
	return err
}

func scanRights(rows pgx.Rows) ([]Right, error) {
	defer rows.Close()
	var out []Right
	for rows.Next() {
		var r Right
		if err := rows.Scan(&r.Module, &r.Entity, &r.Action); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) DirectRights(ctx context.Context, userID int64) ([]Right, error) {
	rows, err := s.pool.Query(ctx, `SELECT module, entity, action FROM ferp_rights WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	return scanRights(rows)
}

func (s *PGStore) InheritedRights(ctx context.Context, userID int64) ([]Right, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.module, r.entity, r.action FROM ferp_rights r
		JOIN ferp_group_members m ON m.group_id=r.group_id WHERE m.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	return scanRights(rows)
}

// ResolveRights loads both grant sets for Can().
func (s *PGStore) ResolveRights(ctx context.Context, u User) ([]Right, []Right, error) {
	direct, err := s.DirectRights(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	inherited, err := s.InheritedRights(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	return direct, inherited, nil
}

func (s *PGStore) CreateSession(ctx context.Context, userID int64, tokenHash string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ferp_sessions (user_id, token_hash, expires_at) VALUES ($1,$2,$3)`,
		userID, tokenHash, expires)
	return err
}

func (s *PGStore) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE ferp_sessions SET revoked_at=now() WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *PGStore) SessionUser(ctx context.Context, tokenHash string, now time.Time) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM ferp_users u
		JOIN ferp_sessions s ON s.user_id=u.id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>$2`, tokenHash, now))
}

// MemoryStore is an in-process fake for handler tests (no database required).
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	users    map[int64]User
	byLogin  map[string]int64
	groups   map[int64]Group
	members  map[int64]map[int64]bool
	direct   map[int64][]Right
	ggrants  map[int64][]Right
	sessions map[string]int64
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users: map[int64]User{}, byLogin: map[string]int64{},
		groups: map[int64]Group{}, members: map[int64]map[int64]bool{},
		direct: map[int64][]Right{}, ggrants: map[int64][]Right{},
		sessions: map[string]int64{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateUser(_ context.Context, u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u.ID = m.next()
	u.CreatedAt = time.Now().UTC()
	u.UpdatedAt = u.CreatedAt
	u.RowVersion = 1
	m.users[u.ID] = *u
	m.byLogin[key(u.EntityID, u.Login)] = u.ID
	return nil
}

func key(entityID int64, login string) string { return fmt.Sprintf("%d\x00%s", entityID, login) }

func (m *MemoryStore) UserByID(_ context.Context, id int64) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *MemoryStore) UserByLogin(_ context.Context, entityID int64, login string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byLogin[key(entityID, login)]
	if !ok {
		return User{}, ErrNotFound
	}
	return m.users[id], nil
}

func (m *MemoryStore) UpdateUser(_ context.Context, u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.users[u.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.RowVersion != u.RowVersion {
		return ErrVersionConflict
	}
	u.RowVersion++
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = *u
	return nil
}

func (m *MemoryStore) CreateGroup(_ context.Context, g *Group) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g.ID = m.next()
	g.CreatedAt = time.Now().UTC()
	g.UpdatedAt = g.CreatedAt
	m.groups[g.ID] = *g
	return nil
}

func (m *MemoryStore) AddMember(_ context.Context, groupID, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[groupID] == nil {
		m.members[groupID] = map[int64]bool{}
	}
	m.members[groupID][userID] = true
	return nil
}

func (m *MemoryStore) UserGroups(_ context.Context, userID int64) ([]Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Group
	for gid, set := range m.members {
		if set[userID] {
			out = append(out, m.groups[gid])
		}
	}
	return out, nil
}

func (m *MemoryStore) Grant(_ context.Context, _ int64, userID, groupID *int64, r Right) error {
	if err := r.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if userID != nil {
		m.direct[*userID] = append(m.direct[*userID], r)
	} else {
		m.ggrants[*groupID] = append(m.ggrants[*groupID], r)
	}
	return nil
}

func (m *MemoryStore) DirectRights(_ context.Context, userID int64) ([]Right, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Right(nil), m.direct[userID]...), nil
}

func (m *MemoryStore) InheritedRights(_ context.Context, userID int64) ([]Right, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Right
	for gid, set := range m.members {
		if set[userID] {
			out = append(out, m.ggrants[gid]...)
		}
	}
	return out, nil
}

func (m *MemoryStore) ResolveRights(ctx context.Context, u User) ([]Right, []Right, error) {
	d, err := m.DirectRights(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	inh, err := m.InheritedRights(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	return d, inh, nil
}

func (m *MemoryStore) CreateSession(_ context.Context, userID int64, tokenHash string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[tokenHash] = userID
	return nil
}

func (m *MemoryStore) RevokeSession(_ context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, tokenHash)
	return nil
}

func (m *MemoryStore) SessionUser(_ context.Context, tokenHash string, _ time.Time) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.sessions[tokenHash]
	if !ok {
		return User{}, ErrNotFound
	}
	return m.users[id], nil
}
