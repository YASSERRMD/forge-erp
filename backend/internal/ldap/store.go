package ldap

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for the mirrored directory cache.
// LDAP has no tables: the memory cache is the store (db args are accepted
// for signature parity and ignored).
type Store interface {
	UpsertUser(ctx context.Context, db platform.DBTX, entityID int64, u LDAPUser) error
	UserByLogin(ctx context.Context, db platform.DBTX, entityID int64, login string) (LDAPUser, error)
	ListUsers(ctx context.Context, db platform.DBTX, entityID int64) ([]LDAPUser, error)
}

// MemoryStore is the in-process directory cache.
type MemoryStore struct {
	mu    sync.Mutex
	users map[string]LDAPUser // key: entityID + "\x00" + login
}

// NewMemoryStore builds an empty cache.
func NewMemoryStore() *MemoryStore { return &MemoryStore{users: map[string]LDAPUser{}} }

func userKey(entityID int64, login string) string { return fmt.Sprintf("%d\x00%s", entityID, login) }

// UpsertUser inserts or replaces one mirrored entry.
func (m *MemoryStore) UpsertUser(_ context.Context, _ platform.DBTX, entityID int64, u LDAPUser) error {
	if entityID == 0 {
		return fmt.Errorf("ldap: entity required: %w", platform.ErrUnauthorized)
	}
	if err := u.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[userKey(entityID, u.Login)] = u
	return nil
}

// UserByLogin returns one mirrored entry (ErrNotFound when absent).
func (m *MemoryStore) UserByLogin(_ context.Context, _ platform.DBTX, entityID int64, login string) (LDAPUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userKey(entityID, login)]
	if !ok {
		return LDAPUser{}, fmt.Errorf("ldap: user %q: %w", login, platform.ErrNotFound)
	}
	return u, nil
}

// ListUsers returns every mirrored entry for one entity, sorted by login.
func (m *MemoryStore) ListUsers(_ context.Context, _ platform.DBTX, entityID int64) ([]LDAPUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LDAPUser
	for key, u := range m.users {
		sep := strings.Index(key, "\x00")
		eid, _ := strconv.ParseInt(key[:sep], 10, 64)
		if eid == entityID {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Login < out[j].Login })
	return out, nil
}
