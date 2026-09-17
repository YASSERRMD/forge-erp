package ldap

import (
	"context"
	"fmt"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Dialer abstracts the directory connection. Production would dial
// Config.URL over LDAP(S); tests and offline operators use StaticDialer.
// The interface keeps the context server-free: without FERP_LDAP_URL the
// service refuses before any dialer is touched.
type Dialer interface {
	// SearchUsers returns every user entry under the configured base DN.
	SearchUsers(ctx context.Context) ([]LDAPUser, error)
}

// StaticDialer is a fake-friendly test double: it replays canned entries.
type StaticDialer struct {
	Users []LDAPUser
	Err   error
}

// SearchUsers replays the canned entries.
func (d StaticDialer) SearchUsers(_ context.Context) ([]LDAPUser, error) {
	if d.Err != nil {
		return nil, d.Err
	}
	return append([]LDAPUser(nil), d.Users...), nil
}

// Service syncs directory entries into the local cache.
type Service struct {
	Store  Store
	Dialer Dialer
	Config Config
	Bus    platform.Bus
}

// NewService builds a Service.
func NewService(s Store, d Dialer, c Config, bus platform.Bus) *Service {
	return &Service{Store: s, Dialer: d, Config: c, Bus: bus}
}

// SyncResult counts mirrored entries.
type SyncResult struct {
	Synced int `json:"synced"`
}

// Sync pulls directory entries via the dialer and upserts them for entityID.
// Refused with ErrValidation while the FERP_LDAP_URL gate is unset.
func (s *Service) Sync(ctx context.Context, db platform.DBTX, entityID int64) (SyncResult, error) {
	if !s.Config.Enabled() {
		return SyncResult{}, fmt.Errorf("ldap: FERP_LDAP_URL unset, sync disabled: %w", platform.ErrValidation)
	}
	if entityID == 0 {
		return SyncResult{}, fmt.Errorf("ldap: entity required: %w", platform.ErrUnauthorized)
	}
	if s.Dialer == nil {
		return SyncResult{}, fmt.Errorf("ldap: no dialer configured: %w", platform.ErrValidation)
	}
	users, err := s.Dialer.SearchUsers(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	var res SyncResult
	for _, u := range users {
		if err := u.Validate(); err != nil {
			return res, err
		}
		if err := s.Store.UpsertUser(ctx, db, entityID, u); err != nil {
			return res, err
		}
		res.Synced++
	}
	if s.Bus != nil {
		_ = s.Bus.Publish(ctx, platform.Event{Subject: "forgeerp.ldap.sync.v1", Entity: "user", EntityID: entityID})
	}
	return res, nil
}
