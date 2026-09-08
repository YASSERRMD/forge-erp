package documentsvc

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore persists document metadata in ferp_files.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) Create(ctx context.Context, d *Document) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_files
		(entity_id, scope, object_id, name, mime, size, sha256, storage_key, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, created_at`,
		d.EntityID, d.Scope, d.ObjectID, d.Name, d.MIME, d.Size, d.SHA256, d.StorageKey, d.CreatedBy,
	).Scan(&d.ID, &d.CreatedAt)
}

func (s *PGStore) ByID(ctx context.Context, id int64) (Document, error) {
	var d Document
	err := s.pool.QueryRow(ctx, `SELECT id, entity_id, scope, object_id, name, mime, size,
		sha256, storage_key, created_at, created_by FROM ferp_files WHERE id=$1`, id).
		Scan(&d.ID, &d.EntityID, &d.Scope, &d.ObjectID, &d.Name, &d.MIME, &d.Size,
			&d.SHA256, &d.StorageKey, &d.CreatedAt, &d.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, errors.New("documentsvc: not found")
	}
	return d, err
}

func (s *PGStore) List(ctx context.Context, entityID int64, scope string, objectID int64) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, entity_id, scope, object_id, name, mime, size,
		sha256, storage_key, created_at, created_by FROM ferp_files
		WHERE entity_id=$1 AND ($2='' OR scope=$2) AND ($3=0 OR object_id=$3) ORDER BY id DESC LIMIT 200`,
		entityID, scope, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.EntityID, &d.Scope, &d.ObjectID, &d.Name, &d.MIME,
			&d.Size, &d.SHA256, &d.StorageKey, &d.CreatedAt, &d.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
