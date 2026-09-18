// Entity locale defaults (Phase 4): ferp_entities.default_locale feeds the
// resolution chain (user preference → Accept-Language → entity default →
// English). Empty at every level means "unset" — resolution falls through.
package locale

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Tags lists shipped catalogue tags, sorted ("ar", "en", "fr").
func Tags() []string {
	cats, err := loadEmbedded()
	if err != nil {
		return []string{DefaultLocale}
	}
	out := make([]string, 0, len(cats))
	for t := range cats {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// EntityDefault returns an entity's configured default locale ("" = unset).
// Unknown entities read as unset (ErrNotFound swallowed): a missing entity
// row must not break resolution, the chain simply falls to English.
func EntityDefault(ctx context.Context, db platform.DBTX, entityID int64) (string, error) {
	if db == nil || entityID <= 0 {
		return "", nil
	}
	var v string
	err := db.QueryRow(ctx, `SELECT default_locale FROM ferp_entities WHERE id=$1`, entityID).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// CatalogueForTest builds a loader with no entity default (handler tests).
func CatalogueForTest(userLocale string) (Catalogue, string, error) {
	l, err := NewLoader("")
	if err != nil {
		return Catalogue{}, "", err
	}
	c, tag := l.Catalogue(userLocale)
	return c, tag, nil
}
