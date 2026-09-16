package documentsvc

// Phase 2 ECM depth: folder tree, file versioning, automatic filing rules,
// full-text search sidecar.
//
// Text extraction policy: text/plain bodies are indexed verbatim plus the
// filename; every other MIME type indexes the filename only. NO binary
// parsers exist — PDFs/ODTs are searchable by name until a parser lands.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Folder is one node of the per-entity folder tree (ferp_folders).
// Root folders carry a nil ParentID; (entity, parent, name) is unique.
type Folder struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	ParentID  *int64    `json:"parent_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate folder naming rules (no path separators, no dot segments).
func (f Folder) Validate() error {
	if f.Name == "" || strings.Contains(f.Name, "/") || f.Name == "." || f.Name == ".." {
		return fmt.Errorf("documentsvc: bad folder name %q: %w", f.Name, platform.ErrValidation)
	}
	if len(f.Name) > 255 {
		return fmt.Errorf("documentsvc: folder name too long: %w", platform.ErrValidation)
	}
	return nil
}

// FileVersion is one immutable revision of a file's bytes (ferp_file_versions).
// History rows are never mutated: restores insert a NEW version copying old bytes.
type FileVersion struct {
	ID         int64     `json:"id"`
	FileID     int64     `json:"file_id"`
	Version    int       `json:"version"`
	StorageKey string    `json:"-"`
	MIME       string    `json:"mime"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  *int64    `json:"created_by"`
}

// FilingRule maps (scope, object_type) to a folder path template
// (e.g. sales/invoice -> /sales/invoices/{entity}).
type FilingRule struct {
	Scope        string `json:"scope"`
	ObjectType   string `json:"object_type"`
	PathTemplate string `json:"path_template"`
}

// Validate filing rule fields.
func (r FilingRule) Validate() error {
	if r.Scope == "" || !strings.HasPrefix(r.PathTemplate, "/") {
		return fmt.Errorf("documentsvc: bad filing rule: %w", platform.ErrValidation)
	}
	return nil
}

// SearchHit pairs a document with its full-text rank.
type SearchHit struct {
	Document Document `json:"document"`
	Rank     float64  `json:"rank"`
}

// UploadInput carries the extended upload parameters (folder targeting +
// automatic filing). Zero FolderID + empty FolderPath + AutoFile=false files
// at the entity root (backwards compatible with Service.Upload).
type UploadInput struct {
	Scope      string
	ObjectType string
	ObjectID   int64
	Name       string
	MIME       string
	Body       io.Reader
	CreatedBy  *int64
	FolderID   *int64 // explicit target folder (nil = root)
	FolderPath string // explicit path, auto-created (takes precedence over AutoFile)
	AutoFile   bool   // resolve target via filing rules, auto-creating folders
}

// SplitPath splits "/a/b/c" into ["a","b","c"] ("" and "/" -> empty).
// Rejects dot segments and over-long names.
func SplitPath(p string) ([]string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return nil, nil
	}
	raw := strings.Split(strings.Trim(p, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, seg := range raw {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." || strings.Contains(seg, "/") {
			return nil, fmt.Errorf("documentsvc: bad path segment %q: %w", seg, platform.ErrValidation)
		}
		if len(seg) > 255 {
			return nil, fmt.Errorf("documentsvc: path segment too long: %w", platform.ErrValidation)
		}
		out = append(out, seg)
	}
	return out, nil
}

// FilingPath renders a rule template. Supported placeholders: {entity},
// {entity_id}, {object_id}, {object_type}, {scope}.
func FilingPath(template string, entityID, objectID int64, objectType, scope string) string {
	r := strings.NewReplacer(
		"{entity}", fmt.Sprintf("%d", entityID),
		"{entity_id}", fmt.Sprintf("%d", entityID),
		"{object_id}", fmt.Sprintf("%d", objectID),
		"{object_type}", objectType,
		"{scope}", scope,
	)
	return r.Replace(template)
}

// extractText derives indexable text: text/* bodies verbatim, everything else
// falls back to the filename (NO binary parsers by design).
func extractText(name, mime string, body []byte) string {
	if strings.HasPrefix(strings.ToLower(mime), "text/") {
		return strings.TrimSpace(string(body) + "\n" + name)
	}
	return name
}

// folderEqual reports whether two nullable folder ids match (NULL-safe).
func folderEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// --- Service: folders ---

// CreateFolder inserts a folder under parentID (nil = root) within the entity.
func (s *Service) CreateFolder(ctx context.Context, entityID int64, parentID *int64, name string) (Folder, error) {
	f := &Folder{EntityID: entityID, ParentID: parentID, Name: strings.TrimSpace(name)}
	if err := f.Validate(); err != nil {
		return Folder{}, err
	}
	if parentID != nil {
		if _, err := s.Store.FolderByID(ctx, s.DB, entityID, *parentID); err != nil {
			return Folder{}, err // missing / foreign parent -> not found
		}
	}
	if err := s.Store.CreateFolder(ctx, s.DB, f); err != nil {
		return Folder{}, err
	}
	return *f, nil
}

// MoveFolder reparents a folder (nil newParent = root) with a cycle guard:
// a folder can never move into itself or one of its own descendants.
func (s *Service) MoveFolder(ctx context.Context, entityID, id int64, newParent *int64) (Folder, error) {
	f, err := s.Store.FolderByID(ctx, s.DB, entityID, id)
	if err != nil {
		return Folder{}, err
	}
	if newParent != nil {
		if *newParent == id {
			return Folder{}, fmt.Errorf("documentsvc: cannot move folder into itself: %w", platform.ErrValidation)
		}
		if _, err := s.Store.FolderByID(ctx, s.DB, entityID, *newParent); err != nil {
			return Folder{}, err
		}
		// Walk the new parent chain: hitting id means id is an ancestor -> cycle.
		for cur := newParent; cur != nil; {
			if *cur == id {
				return Folder{}, fmt.Errorf("documentsvc: cannot move folder into own descendant: %w", platform.ErrValidation)
			}
			p, err := s.Store.FolderByID(ctx, s.DB, entityID, *cur)
			if err != nil {
				return Folder{}, err
			}
			cur = p.ParentID
		}
	}
	if folderEqual(f.ParentID, newParent) {
		return f, nil // no-op
	}
	if err := s.Store.MoveFolder(ctx, s.DB, entityID, id, newParent); err != nil {
		return Folder{}, err
	}
	f.ParentID = newParent
	return f, nil
}

// FolderPath resolves the absolute path of a folder ("/a/b/c", root child "/a").
func (s *Service) FolderPath(ctx context.Context, entityID, id int64) (string, error) {
	var segs []string
	for cur := &id; cur != nil; {
		f, err := s.Store.FolderByID(ctx, s.DB, entityID, *cur)
		if err != nil {
			return "", err
		}
		segs = append([]string{f.Name}, segs...)
		cur = f.ParentID
		if len(segs) > 256 {
			return "", fmt.Errorf("documentsvc: folder depth exceeded: %w", platform.ErrValidation)
		}
	}
	return "/" + strings.Join(segs, "/"), nil
}

// EnsureFolderPath creates missing segments of path and returns the leaf id
// (nil for root "" / "/").
func (s *Service) EnsureFolderPath(ctx context.Context, entityID int64, path string) (*int64, error) {
	segs, err := SplitPath(path)
	if err != nil {
		return nil, err
	}
	var parent *int64
	for _, seg := range segs {
		existing, err := s.Store.FindFolder(ctx, s.DB, entityID, parent, seg)
		if err == nil {
			parent = &existing.ID
			continue
		}
		if !errors.Is(err, platform.ErrNotFound) {
			return nil, err
		}
		f := &Folder{EntityID: entityID, ParentID: parent, Name: seg}
		if err := s.Store.CreateFolder(ctx, s.DB, f); err != nil {
			if errors.Is(err, platform.ErrConflict) {
				// Lost a create race: re-read.
				existing, rerr := s.Store.FindFolder(ctx, s.DB, entityID, parent, seg)
				if rerr != nil {
					return nil, rerr
				}
				parent = &existing.ID
				continue
			}
			return nil, err
		}
		parent = &f.ID
	}
	return parent, nil
}

// --- Service: filing rules ---

// FilingRuleFor resolves the rule for (scope, object_type).
func (s *Service) FilingRuleFor(ctx context.Context, scope, objectType string) (FilingRule, error) {
	return s.Store.FilingRuleFor(ctx, s.DB, scope, objectType)
}

// UpsertFilingRule creates or replaces a filing rule.
func (s *Service) UpsertFilingRule(ctx context.Context, r FilingRule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return s.Store.UpsertFilingRule(ctx, s.DB, r)
}

// --- Service: versioned upload / restore / search ---

// UploadEx stores bytes with folder targeting + automatic filing. Re-uploads
// to the same (entity, folder, name) append a new version row (byte history is
// append-only); the ferp_files row tracks the current revision pointer.
func (s *Service) UploadEx(ctx context.Context, entityID int64, in UploadInput) (Document, error) {
	body, err := io.ReadAll(io.LimitReader(in.Body, 64<<20)) // 64 MiB cap (Phase 10 hardens limits)
	if err != nil {
		return Document{}, err
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Scope) == "" {
		return Document{}, fmt.Errorf("documentsvc: scope and name required: %w", platform.ErrValidation)
	}
	folderID := in.FolderID
	switch {
	case in.FolderPath != "":
		folderID, err = s.EnsureFolderPath(ctx, entityID, in.FolderPath)
		if err != nil {
			return Document{}, err
		}
	case in.AutoFile:
		rule, rerr := s.Store.FilingRuleFor(ctx, s.DB, in.Scope, in.ObjectType)
		if rerr != nil {
			return Document{}, fmt.Errorf("documentsvc: no filing rule for %s/%s: %w",
				in.Scope, in.ObjectType, platform.ErrValidation)
		}
		folderID, err = s.EnsureFolderPath(ctx, entityID,
			FilingPath(rule.PathTemplate, entityID, in.ObjectID, in.ObjectType, in.Scope))
		if err != nil {
			return Document{}, err
		}
	}
	if folderID != nil {
		if _, err := s.Store.FolderByID(ctx, s.DB, entityID, *folderID); err != nil {
			return Document{}, err
		}
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	mime := in.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}

	existing, err := s.Store.FindByFolderName(ctx, s.DB, entityID, folderID, in.Name)
	if err != nil && !errors.Is(err, platform.ErrNotFound) {
		return Document{}, err
	}
	if errors.Is(err, platform.ErrNotFound) {
		d := Document{EntityID: entityID, Scope: in.Scope, ObjectID: in.ObjectID,
			FolderID: folderID, Name: in.Name, MIME: mime, Size: int64(len(body)),
			SHA256: sha, Version: 1, CreatedBy: in.CreatedBy}
		d.StorageKey = fmt.Sprintf("e%d/%s/%d-%s", entityID, in.Scope, time.Now().UnixNano(), in.Name)
		if err := s.Storage.Put(ctx, d.StorageKey, bytes.NewReader(body), d.Size, mime); err != nil {
			return Document{}, err
		}
		if err := s.Store.Create(ctx, s.DB, &d); err != nil {
			_ = s.Storage.Delete(ctx, d.StorageKey)
			return Document{}, err
		}
		v := &FileVersion{FileID: d.ID, Version: 1, StorageKey: d.StorageKey,
			MIME: mime, Size: d.Size, SHA256: sha, CreatedBy: in.CreatedBy}
		if err := s.Store.CreateVersion(ctx, s.DB, v); err != nil {
			return Document{}, err
		}
		if err := s.Store.UpsertFileText(ctx, s.DB, d.ID, extractText(in.Name, mime, body)); err != nil {
			return Document{}, err
		}
		return d, nil
	}
	// New revision of an existing logical file.
	next := existing.Version + 1
	key := fmt.Sprintf("e%d/%s/%d-v%d-%s", entityID, existing.Scope, existing.ID, next, existing.Name)
	if err := s.Storage.Put(ctx, key, bytes.NewReader(body), int64(len(body)), mime); err != nil {
		return Document{}, err
	}
	v := &FileVersion{FileID: existing.ID, Version: next, StorageKey: key,
		MIME: mime, Size: int64(len(body)), SHA256: sha, CreatedBy: in.CreatedBy}
	if err := s.Store.CreateVersion(ctx, s.DB, v); err != nil {
		_ = s.Storage.Delete(ctx, key)
		return Document{}, err
	}
	existing.MIME = mime
	existing.Size = int64(len(body))
	existing.SHA256 = sha
	existing.StorageKey = key
	existing.Version = next
	if err := s.Store.UpdateCurrent(ctx, s.DB, &existing); err != nil {
		return Document{}, err
	}
	if err := s.Store.UpsertFileText(ctx, s.DB, existing.ID, extractText(existing.Name, mime, body)); err != nil {
		return Document{}, err
	}
	return existing, nil
}

// Versions returns the immutable history of a file (oldest first).
func (s *Service) Versions(ctx context.Context, entityID, fileID int64) ([]FileVersion, error) {
	if _, err := s.Store.ByID(ctx, s.DB, entityID, fileID); err != nil {
		return nil, err
	}
	return s.Store.VersionsByFile(ctx, s.DB, entityID, fileID)
}

// RestoreVersion copies the bytes of a historical version into a NEW current
// version. History rows are never mutated.
func (s *Service) RestoreVersion(ctx context.Context, entityID, fileID int64, version int, by *int64) (Document, error) {
	if version <= 0 {
		return Document{}, fmt.Errorf("documentsvc: bad version: %w", platform.ErrValidation)
	}
	d, err := s.Store.ByID(ctx, s.DB, entityID, fileID)
	if err != nil {
		return Document{}, err
	}
	src, err := s.Store.VersionByNumber(ctx, s.DB, entityID, fileID, version)
	if err != nil {
		return Document{}, err
	}
	if version == d.Version {
		return d, nil // already current: no-op
	}
	rc, err := s.Storage.Get(ctx, src.StorageKey)
	if err != nil {
		return Document{}, fmt.Errorf("documentsvc: version bytes missing: %w", platform.ErrNotFound)
	}
	body, err := io.ReadAll(io.LimitReader(rc, 64<<20))
	_ = rc.Close()
	if err != nil {
		return Document{}, err
	}
	next := d.Version + 1
	key := fmt.Sprintf("e%d/%s/%d-v%d-%s", entityID, d.Scope, d.ID, next, d.Name)
	if err := s.Storage.Put(ctx, key, bytes.NewReader(body), int64(len(body)), src.MIME); err != nil {
		return Document{}, err
	}
	sum := sha256.Sum256(body)
	v := &FileVersion{FileID: d.ID, Version: next, StorageKey: key, MIME: src.MIME,
		Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), CreatedBy: by}
	if err := s.Store.CreateVersion(ctx, s.DB, v); err != nil {
		_ = s.Storage.Delete(ctx, key)
		return Document{}, err
	}
	d.MIME = v.MIME
	d.Size = v.Size
	d.SHA256 = v.SHA256
	d.StorageKey = key
	d.Version = next
	if err := s.Store.UpdateCurrent(ctx, s.DB, &d); err != nil {
		return Document{}, err
	}
	if err := s.Store.UpsertFileText(ctx, s.DB, d.ID, extractText(d.Name, d.MIME, body)); err != nil {
		return Document{}, err
	}
	return d, nil
}

// Search full-text searches indexed file content entity-scoped (ranked).
func (s *Service) Search(ctx context.Context, entityID int64, query, scope string, limit int) ([]SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("documentsvc: query required: %w", platform.ErrValidation)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.Store.SearchFiles(ctx, s.DB, entityID, query, scope, limit)
}
