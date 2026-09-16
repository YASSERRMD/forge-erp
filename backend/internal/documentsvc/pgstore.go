package documentsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore persists document metadata in ferp_files.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) Create(ctx context.Context, db platform.DBTX, d *Document) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if d.Version <= 0 {
		d.Version = 1
	}
	err := db.QueryRow(ctx, `INSERT INTO ferp_files
		(entity_id, scope, object_id, folder_id, name, mime, size, sha256, version, storage_key, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id, created_at`,
		d.EntityID, d.Scope, d.ObjectID, d.FolderID, d.Name, d.MIME, d.Size, d.SHA256,
		d.Version, d.StorageKey, d.CreatedBy,
	).Scan(&d.ID, &d.CreatedAt)
	return mapUnique(err)
}

// scanDoc scans the canonical ferp_files column list (with folder/version).
func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.EntityID, &d.Scope, &d.ObjectID, &d.FolderID,
		&d.Name, &d.MIME, &d.Size, &d.SHA256, &d.Version,
		&d.StorageKey, &d.CreatedAt, &d.CreatedBy)
	return d, err
}

func (s *PGStore) ByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Document, error) {
	d, err := scanDoc(db.QueryRow(ctx, `SELECT id, entity_id, scope, object_id, folder_id,
		name, mime, size, sha256, version, storage_key, created_at, created_by
		FROM ferp_files WHERE id=$1 AND entity_id=$2`, id, entityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
	}
	return d, err
}

func (s *PGStore) List(ctx context.Context, db platform.DBTX, entityID int64, scope string, objectID int64) ([]Document, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, scope, object_id, folder_id,
		name, mime, size, sha256, version, storage_key, created_at, created_by FROM ferp_files
		WHERE entity_id=$1 AND ($2='' OR scope=$2) AND ($3=0 OR object_id=$3) ORDER BY id DESC LIMIT 200`,
		entityID, scope, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
