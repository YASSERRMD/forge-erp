// Package bookmark implements per-user record bookmarks (Dolibarr
// bookmarks, lite): one bookmark per (user, scope, object) — re-adding is
// idempotent and toggle flips membership.
package bookmark

import (
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Bookmark pins one record for one user.
type Bookmark struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	UserLogin  string    `json:"user_login"`
	Scope      string    `json:"scope"`       // context name, e.g. "sales"
	ObjectType string    `json:"object_type"` // e.g. "invoice"
	ObjectID   int64     `json:"object_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// Validate checks bookmark invariants.
func (b Bookmark) Validate() error {
	if b.EntityID <= 0 {
		return fmt.Errorf("bookmark: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(b.UserLogin) == "" {
		return fmt.Errorf("bookmark: user_login required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(b.Scope) == "" || strings.TrimSpace(b.ObjectType) == "" {
		return fmt.Errorf("bookmark: scope and object_type required: %w", platform.ErrValidation)
	}
	if b.ObjectID <= 0 {
		return fmt.Errorf("bookmark: object_id required: %w", platform.ErrValidation)
	}
	return nil
}
