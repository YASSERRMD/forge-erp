// Package collab implements record comments (Phase 5 PORT-LITE: Dolibarr
// Collab equivalent, scoped down to comments — no collaborative editing):
// threaded notes attached to any (scope, object) by author login.
package collab

import (
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Comment is one note on a record. Thread groups follow-ups ("" = top level).
type Comment struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Scope      string    `json:"scope"`       // context name, e.g. "sales"
	ObjectType string    `json:"object_type"` // e.g. "invoice"
	ObjectID   int64     `json:"object_id"`
	Thread     string    `json:"thread"`
	Author     string    `json:"author"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	RowVersion int64     `json:"row_version"`
}

// Validate checks comment invariants.
func (c Comment) Validate() error {
	if c.EntityID <= 0 {
		return fmt.Errorf("collab: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(c.Scope) == "" || strings.TrimSpace(c.ObjectType) == "" {
		return fmt.Errorf("collab: scope and object_type required: %w", platform.ErrValidation)
	}
	if c.ObjectID <= 0 {
		return fmt.Errorf("collab: object_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(c.Author) == "" {
		return fmt.Errorf("collab: author required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(c.Body) == "" {
		return fmt.Errorf("collab: body required: %w", platform.ErrValidation)
	}
	return nil
}
