// Package quickmemo implements per-user memos: short private notes with
// optimistic-locking edits. Memos are strictly per-user: reads and writes
// filter on (entity, user_login) so users never see each other's notes.
package quickmemo

import (
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Memo is one private note.
type Memo struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	UserLogin  string    `json:"user_login"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	RowVersion int64     `json:"row_version"`
}

// Validate checks memo invariants.
func (m Memo) Validate() error {
	if m.EntityID <= 0 {
		return fmt.Errorf("quickmemo: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(m.UserLogin) == "" {
		return fmt.Errorf("quickmemo: user_login required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("quickmemo: title required: %w", platform.ErrValidation)
	}
	return nil
}
