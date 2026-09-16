package documentsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// mapUnique converts a PG unique violation (23505) to ErrConflict so the
// platform error kernel renders 409 via errors.Is.
func mapUnique(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("documentsvc: duplicate: %w", platform.ErrConflict)
	}
	return err
}

// --- PGStore: folders ---

func (s *PGStore) CreateFolder(ctx context.Context, db platform.DBTX, f *Folder) error {
	if err := f.Validate(); err != nil {
		return err
	}
	err := db.QueryRow(ctx, `INSERT INTO ferp_folders (entity_id, parent_id, name)
		VALUES ($1,$2,$3) RETURNING id, created_at`,
		f.EntityID, f.ParentID, f.Name).Scan(&f.ID, &f.CreatedAt)
	return mapUnique(err)
}

func scanFolder(row pgx.Row) (Folder, error) {
	var f Folder
	err := row.Scan(&f.ID, &f.EntityID, &f.ParentID, &f.Name, &f.CreatedAt)
	return f, err
}

func (s *PGStore) FolderByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Folder, error) {
	f, err := scanFolder(db.QueryRow(ctx, `SELECT id, entity_id, parent_id, name, created_at
		FROM ferp_folders WHERE id=$1 AND entity_id=$2`, id, entityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, fmt.Errorf("documentsvc: folder not found: %w", platform.ErrNotFound)
	}
	return f, err
}

func (s *PGStore) FindFolder(ctx context.Context, db platform.DBTX, entityID int64, parentID *int64, name string) (Folder, error) {
	f, err := scanFolder(db.QueryRow(ctx, `SELECT id, entity_id, parent_id, name, created_at
		FROM ferp_folders WHERE entity_id=$1 AND parent_id IS NOT DISTINCT FROM $2 AND name=$3`,
		entityID, parentID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, fmt.Errorf("documentsvc: folder not found: %w", platform.ErrNotFound)
	}
	return f, err
}

func (s *PGStore) ListFolders(ctx context.Context, db platform.DBTX, entityID int64, parentID *int64) ([]Folder, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, parent_id, name, created_at
		FROM ferp_folders WHERE entity_id=$1 AND parent_id IS NOT DISTINCT FROM $2
		ORDER BY name ASC LIMIT 500`, entityID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *PGStore) MoveFolder(ctx context.Context, db platform.DBTX, entityID int64, id int64, newParent *int64) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_folders SET parent_id=$1
		WHERE id=$2 AND entity_id=$3`, newParent, id, entityID)
	if err != nil {
		return mapUnique(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("documentsvc: folder not found: %w", platform.ErrNotFound)
	}
	return nil
}

// --- PGStore: versioned files ---

func (s *PGStore) FindByFolderName(ctx context.Context, db platform.DBTX, entityID int64, folderID *int64, name string) (Document, error) {
	d, err := scanDoc(db.QueryRow(ctx, `SELECT id, entity_id, scope, object_id, folder_id,
		name, mime, size, sha256, version, storage_key, created_at, created_by
		FROM ferp_files WHERE entity_id=$1 AND folder_id IS NOT DISTINCT FROM $2 AND name=$3`,
		entityID, folderID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
	}
	return d, err
}

func (s *PGStore) UpdateCurrent(ctx context.Context, db platform.DBTX, d *Document) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_files SET mime=$1, size=$2, sha256=$3,
		storage_key=$4, version=$5 WHERE id=$6 AND entity_id=$7`,
		d.MIME, d.Size, d.SHA256, d.StorageKey, d.Version, d.ID, d.EntityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
	}
	return nil
}

func (s *PGStore) CreateVersion(ctx context.Context, db platform.DBTX, v *FileVersion) error {
	err := db.QueryRow(ctx, `INSERT INTO ferp_file_versions
		(file_id, version, storage_key, mime, size, sha256, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`,
		v.FileID, v.Version, v.StorageKey, v.MIME, v.Size, v.SHA256, v.CreatedBy,
	).Scan(&v.ID, &v.CreatedAt)
	return mapUnique(err)
}

func (s *PGStore) VersionsByFile(ctx context.Context, db platform.DBTX, entityID int64, fileID int64) ([]FileVersion, error) {
	rows, err := db.Query(ctx, `SELECT v.id, v.file_id, v.version, v.storage_key, v.mime,
		v.size, v.sha256, v.created_at, v.created_by
		FROM ferp_file_versions v JOIN ferp_files f ON f.id=v.file_id
		WHERE v.file_id=$1 AND f.entity_id=$2 ORDER BY v.version ASC`,
		fileID, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileVersion
	for rows.Next() {
		var v FileVersion
		if err := rows.Scan(&v.ID, &v.FileID, &v.Version, &v.StorageKey, &v.MIME,
			&v.Size, &v.SHA256, &v.CreatedAt, &v.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) VersionByNumber(ctx context.Context, db platform.DBTX, entityID int64, fileID int64, version int) (FileVersion, error) {
	var v FileVersion
	err := db.QueryRow(ctx, `SELECT v.id, v.file_id, v.version, v.storage_key, v.mime,
		v.size, v.sha256, v.created_at, v.created_by
		FROM ferp_file_versions v JOIN ferp_files f ON f.id=v.file_id
		WHERE v.file_id=$1 AND v.version=$2 AND f.entity_id=$3`,
		fileID, version, entityID).Scan(&v.ID, &v.FileID, &v.Version, &v.StorageKey,
		&v.MIME, &v.Size, &v.SHA256, &v.CreatedAt, &v.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileVersion{}, fmt.Errorf("documentsvc: version not found: %w", platform.ErrNotFound)
	}
	return v, err
}

// --- PGStore: text index + search ---

func (s *PGStore) UpsertFileText(ctx context.Context, db platform.DBTX, fileID int64, content string) error {
	_, err := db.Exec(ctx, `INSERT INTO ferp_file_texts (file_id, content)
		VALUES ($1,$2) ON CONFLICT (file_id) DO UPDATE SET content=EXCLUDED.content`,
		fileID, content)
	return err
}

func (s *PGStore) SearchFiles(ctx context.Context, db platform.DBTX, entityID int64, query string, scope string, limit int) ([]SearchHit, error) {
	rows, err := db.Query(ctx, `SELECT f.id, f.entity_id, f.scope, f.object_id, f.folder_id,
		f.name, f.mime, f.size, f.sha256, f.version, f.storage_key, f.created_at, f.created_by,
		ts_rank(t.tsv, plainto_tsquery('english', $2)) AS rank
		FROM ferp_files f JOIN ferp_file_texts t ON t.file_id=f.id
		WHERE f.entity_id=$1 AND ($3='' OR f.scope=$3)
		  AND t.tsv @@ plainto_tsquery('english', $2)
		ORDER BY rank DESC, f.id DESC LIMIT $4`,
		entityID, query, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var d Document
		if err := rows.Scan(&d.ID, &d.EntityID, &d.Scope, &d.ObjectID, &d.FolderID,
			&d.Name, &d.MIME, &d.Size, &d.SHA256, &d.Version, &d.StorageKey,
			&d.CreatedAt, &d.CreatedBy, &h.Rank); err != nil {
			return nil, err
		}
		h.Document = d
		out = append(out, h)
	}
	return out, rows.Err()
}

// --- PGStore: filing rules ---

func (s *PGStore) UpsertFilingRule(ctx context.Context, db platform.DBTX, r FilingRule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `INSERT INTO ferp_filing_rules (scope, object_type, path_template)
		VALUES ($1,$2,$3) ON CONFLICT (scope, object_type)
		DO UPDATE SET path_template=EXCLUDED.path_template`,
		r.Scope, r.ObjectType, r.PathTemplate)
	return err
}

func (s *PGStore) FilingRuleFor(ctx context.Context, db platform.DBTX, scope string, objectType string) (FilingRule, error) {
	var r FilingRule
	err := db.QueryRow(ctx, `SELECT scope, object_type, path_template FROM ferp_filing_rules
		WHERE scope=$1 AND object_type=$2`, scope, objectType).
		Scan(&r.Scope, &r.ObjectType, &r.PathTemplate)
	if errors.Is(err, pgx.ErrNoRows) {
		return FilingRule{}, fmt.Errorf("documentsvc: no filing rule: %w", platform.ErrNotFound)
	}
	return r, err
}

func (s *PGStore) ListFilingRules(ctx context.Context, db platform.DBTX) ([]FilingRule, error) {
	rows, err := db.Query(ctx, `SELECT scope, object_type, path_template FROM ferp_filing_rules
		ORDER BY scope, object_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FilingRule
	for rows.Next() {
		var r FilingRule
		if err := rows.Scan(&r.Scope, &r.ObjectType, &r.PathTemplate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
