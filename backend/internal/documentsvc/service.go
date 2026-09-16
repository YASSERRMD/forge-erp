// Package documentsvc implements the document/ECM context (Dolibarr ecm +
// documents/ storage + document_model generators): metadata registry with a
// Storage backend interface (local filesystem today, MinIO/S3 tomorrow via the
// same Put/Get/Delete contract).
package documentsvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Document is stored-file metadata (Dolibarr llx_ecm_files equivalent).
// FolderID points into the per-entity folder tree (nil = entity root);
// Version tracks the current revision (history lives in FileVersion rows).
type Document struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Scope      string    `json:"scope"`     // e.g. "sales", "purchase", "partners"
	ObjectID   int64     `json:"object_id"` // linked business object
	FolderID   *int64    `json:"folder_id,omitempty"`
	Name       string    `json:"name"`
	MIME       string    `json:"mime"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	Version    int       `json:"version"`
	StorageKey string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  *int64    `json:"created_by"`
}

// Validate metadata rules.
func (d Document) Validate() error {
	if d.Scope == "" || d.Name == "" {
		return errors.New("documentsvc: scope and name required")
	}
	if d.Size < 0 {
		return errors.New("documentsvc: negative size")
	}
	return nil
}

// Storage is the byte backend contract: DirStorage (local), S3Storage
// (MinIO/S3 via SigV4), MemoryStorage (tests).
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// MemoryStorage is the test fake.
type MemoryStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

// NewMemoryStorage builds an empty fake.
func NewMemoryStorage() *MemoryStorage { return &MemoryStorage{data: map[string][]byte{}} }

func (s *MemoryStorage) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = b
	return nil
}

func (s *MemoryStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("documentsvc: %s not found", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *MemoryStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// DirStorage stores bytes under a root directory (dev/single-node backend;
// production MinIO adapter will implement Storage against S3).
type DirStorage struct {
	Root string
}

// NewDirStorage builds a directory backend (creates root).
func NewDirStorage(root string) (*DirStorage, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &DirStorage{Root: root}, nil
}

func (s *DirStorage) path(key string) string { return filepath.Join(s.Root, filepath.Clean("/"+key)) }

func (s *DirStorage) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (s *DirStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(s.path(key))
}

func (s *DirStorage) Delete(_ context.Context, key string) error {
	err := os.Remove(s.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Store is the metadata persistence contract (documents + ECM tree).
type Store interface {
	Create(ctx context.Context, db platform.DBTX, d *Document) error
	ByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Document, error)
	List(ctx context.Context, db platform.DBTX, entityID int64, scope string, objectID int64) ([]Document, error)
	FindByFolderName(ctx context.Context, db platform.DBTX, entityID int64, folderID *int64, name string) (Document, error)
	UpdateCurrent(ctx context.Context, db platform.DBTX, d *Document) error
	CreateFolder(ctx context.Context, db platform.DBTX, f *Folder) error
	FolderByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Folder, error)
	FindFolder(ctx context.Context, db platform.DBTX, entityID int64, parentID *int64, name string) (Folder, error)
	ListFolders(ctx context.Context, db platform.DBTX, entityID int64, parentID *int64) ([]Folder, error)
	MoveFolder(ctx context.Context, db platform.DBTX, entityID int64, id int64, newParent *int64) error
	CreateVersion(ctx context.Context, db platform.DBTX, v *FileVersion) error
	VersionsByFile(ctx context.Context, db platform.DBTX, entityID int64, fileID int64) ([]FileVersion, error)
	VersionByNumber(ctx context.Context, db platform.DBTX, entityID int64, fileID int64, version int) (FileVersion, error)
	UpsertFileText(ctx context.Context, db platform.DBTX, fileID int64, content string) error
	SearchFiles(ctx context.Context, db platform.DBTX, entityID int64, query string, scope string, limit int) ([]SearchHit, error)
	UpsertFilingRule(ctx context.Context, db platform.DBTX, r FilingRule) error
	FilingRuleFor(ctx context.Context, db platform.DBTX, scope string, objectType string) (FilingRule, error)
	ListFilingRules(ctx context.Context, db platform.DBTX) ([]FilingRule, error)
}

// MemoryStore is the in-process metadata fake.
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	docs      map[int64]Document
	shares    map[string]ShareToken
	folderSeq int64
	folders   map[int64]Folder
	verSeq    int64
	versions  map[int64][]FileVersion // by file id
	texts     map[int64]string        // by file id
	rules     map[string]FilingRule   // by scope + "\x00" + object_type
}

// NewMemoryStore builds an empty fake (seeded with the sales/invoice rule).
func NewMemoryStore() *MemoryStore {
	m := &MemoryStore{docs: map[int64]Document{}, shares: map[string]ShareToken{},
		folders: map[int64]Folder{}, versions: map[int64][]FileVersion{},
		texts: map[int64]string{}, rules: map[string]FilingRule{}}
	m.rules["sales\x00invoice"] = FilingRule{Scope: "sales", ObjectType: "invoice",
		PathTemplate: "/sales/invoices/{entity}"}
	return m
}

// Service couples metadata with byte storage.
type Service struct {
	Store   Store
	Storage Storage
	DB      platform.DBTX
}

// Upload stores bytes + metadata (hash computed server-side).
// Re-uploading the same name at the entity root appends a new version.
func (s *Service) Upload(ctx context.Context, entityID int64, scope string, objectID int64,
	name, mime string, r io.Reader, createdBy *int64) (Document, error) {
	return s.UploadEx(ctx, entityID, UploadInput{Scope: scope, ObjectID: objectID,
		Name: name, MIME: mime, Body: r, CreatedBy: createdBy})
}

func (m *MemoryStore) Create(_ context.Context, _ platform.DBTX, d *Document) error {
	if err := d.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.docs {
		if e.EntityID == d.EntityID && folderEqual(e.FolderID, d.FolderID) && e.Name == d.Name {
			return fmt.Errorf("documentsvc: duplicate file name: %w", platform.ErrConflict)
		}
	}
	m.seq++
	d.ID = m.seq
	if d.Version <= 0 {
		d.Version = 1
	}
	if d.MIME == "" {
		d.MIME = "application/octet-stream"
	}
	d.CreatedAt = time.Now().UTC()
	m.docs[d.ID] = *d
	return nil
}

func (m *MemoryStore) ByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok || d.EntityID != entityID {
		return Document{}, errors.New("documentsvc: not found")
	}
	return d, nil
}

func (m *MemoryStore) List(_ context.Context, _ platform.DBTX, entityID int64, scope string, objectID int64) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Document
	for _, d := range m.docs {
		if d.EntityID == entityID && (scope == "" || d.Scope == scope) &&
			(objectID == 0 || d.ObjectID == objectID) {
			out = append(out, d)
		}
	}
	return out, nil
}
