// Package pgtest gives repository tests a real PostgreSQL database:
// per-test isolated schemas, migrated from backend/migrations, dropped on
// cleanup. Skips when TEST_DATABASE_URL is unset (local runs stay offline);
// CI sets it (see .github/workflows/ci.yml) and every repository test runs
// against real Postgres there.
package pgtest

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/migrations"
)

var schemaSeq atomic.Int64

// Pool connects to TEST_DATABASE_URL, migrates a private schema, and returns
// a pool pinned to it via search_path. The schema is dropped on cleanup so
// parallel packages never collide.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL unset: PG-backed test runs in CI only")
	}
	admin, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	schema := fmt.Sprintf("pgtest_%d_%d", os.Getpid(), schemaSeq.Add(1))
	if _, err := admin.Exec(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("pgtest: create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pgtest: parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pgtest: schema pool: %v", err)
	}
	if err := platform.Migrate(context.Background(), pool, migrations.FS); err != nil {
		pool.Close()
		t.Fatalf("pgtest: migrate: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return pool
}

// NewEntity inserts an extra tenant and returns its id (cross-tenant tests).
func NewEntity(t *testing.T, pool *pgxpool.Pool, code string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO ferp_entities (code, label) VALUES ($1,$2) RETURNING id`,
		code, code).Scan(&id); err != nil {
		t.Fatalf("pgtest: new entity: %v", err)
	}
	return id
}
