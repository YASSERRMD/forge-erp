// Package documentsvc implements the document/ECM context (Dolibarr ecm +
// documents/ storage + document_model generators): metadata registry with a
// Storage backend interface (local filesystem today, MinIO/S3 tomorrow via the
// same Put/Get/Delete contract).
package documentsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Document is stored-file metadata (Dolibarr llx_ecm_files equivalent).
type Document struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Scope      string    `json:"scope"`     // e.g. "sales", "purchase", "partners"
	ObjectID   int64     `json:"object_id"` // linked business object
	Name       string    `json:"name"`
	MIME       string    `json:"mime"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
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

// Store is the metadata persistence contract.
type Store interface {
	Create(ctx context.Context, d *Document) error
	ByID(ctx context.Context, id int64) (Document, error)
	List(ctx context.Context, entityID int64, scope string, objectID int64) ([]Document, error)
}

// MemoryStore is the in-process metadata fake.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	docs   map[int64]Document
	shares map[string]ShareToken
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{docs: map[int64]Document{}, shares: map[string]ShareToken{}}
}

// Service couples metadata with byte storage.
type Service struct {
	Store   Store
	Storage Storage
}

// Upload stores bytes + metadata (hash computed server-side).
func (s *Service) Upload(ctx context.Context, entityID int64, scope string, objectID int64,
	name, mime string, r io.Reader, createdBy *int64) (Document, error) {
	b, err := io.ReadAll(io.LimitReader(r, 64<<20)) // 64 MiB cap (Phase 10 hardens limits)
	if err != nil {
		return Document{}, err
	}
	sum := sha256.Sum256(b)
	d := Document{EntityID: entityID, Scope: scope, ObjectID: objectID, Name: name,
		MIME: mime, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:]), CreatedBy: createdBy}
	if err := d.Validate(); err != nil {
		return Document{}, err
	}
	d.StorageKey = fmt.Sprintf("e%d/%s/%d-%s", entityID, scope, time.Now().UnixNano(), name)
	if err := s.Storage.Put(ctx, d.StorageKey, bytes.NewReader(b), d.Size, mime); err != nil {
		return Document{}, err
	}
	if err := s.Store.Create(ctx, &d); err != nil {
		_ = s.Storage.Delete(ctx, d.StorageKey)
		return Document{}, err
	}
	return d, nil
}

func (m *MemoryStore) Create(_ context.Context, d *Document) error {
	if err := d.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	d.ID = m.seq
	d.CreatedAt = time.Now().UTC()
	m.docs[d.ID] = *d
	return nil
}

func (m *MemoryStore) ByID(_ context.Context, id int64) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok {
		return Document{}, errors.New("documentsvc: not found")
	}
	return d, nil
}

func (m *MemoryStore) List(_ context.Context, entityID int64, scope string, objectID int64) ([]Document, error) {
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
