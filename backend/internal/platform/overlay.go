package platform

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OverlayFromDB applies the ferp_config overlay for entity 1 (Dolibarr llx_const
// equivalent). Environment FERP_* always wins for zero values here only when the
// DB holds a non-empty value for a recognized key; unknown keys are ignored.
// Best-effort: returns the number of applied keys (errors only on query failure).
func OverlayFromDB(ctx context.Context, pool *pgxpool.Pool, cfg *Config) (int, error) {
	rows, err := pool.Query(ctx, `SELECT name, value FROM ferp_config WHERE entity_id=1`)
	if err != nil {
		return 0, fmt.Errorf("config overlay: %w", err)
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return applied, err
		}
		if value == "" {
			continue
		}
		switch name {
		case "FERP_JWT_SECRET":
			cfg.JWTSecret = value
			applied++
		case "FERP_ADMIN_EMAIL":
			cfg.AdminEmail = value
			applied++
		default:
			log.Printf("platform: ignoring unknown config key %q", name)
		}
	}
	return applied, rows.Err()
}
