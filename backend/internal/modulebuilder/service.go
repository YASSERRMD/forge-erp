package modulebuilder

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// ScaffoldSpec names the module to generate (family = SPA nav group).
type ScaffoldSpec struct {
	Name   string `json:"name"`
	Family string `json:"family"`
}

// Service wires scaffolding to activation persistence.
type Service struct {
	Store Store
	Bus   platform.Bus
}

// NewService builds a Service.
func NewService(s Store, bus platform.Bus) *Service { return &Service{Store: s, Bus: bus} }

func (s *Service) publish(ctx context.Context, entityID int64, subject string, id int64) {
	if s == nil || s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: "module", EntityID: entityID, ID: id})
}

// Validate checks a manifest structurally (no writes).
func (s *Service) Validate(_ context.Context, m Manifest) error { return m.Validate() }

// Install validates the manifest then records activation in ferp_modules.
func (s *Service) Install(ctx context.Context, db platform.DBTX, entityID int64, m Manifest) error {
	if entityID == 0 {
		return fmt.Errorf("modulebuilder: entity required: %w", platform.ErrUnauthorized)
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if err := s.Store.SetEnabled(ctx, db, entityID, m.Name, true, m.Version); err != nil {
		return err
	}
	s.publish(ctx, entityID, "forgeerp.modulebuilder.install.v1", 0)
	return nil
}

// Uninstall records deactivation (rights grants are left intact so
// re-install restores access without re-granting).
func (s *Service) Uninstall(ctx context.Context, db platform.DBTX, entityID int64, name, version string) error {
	if entityID == 0 {
		return fmt.Errorf("modulebuilder: entity required: %w", platform.ErrUnauthorized)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("modulebuilder: invalid module name %q: %w", name, platform.ErrValidation)
	}
	if err := s.Store.SetEnabled(ctx, db, entityID, name, false, version); err != nil {
		return err
	}
	s.publish(ctx, entityID, "forgeerp.modulebuilder.uninstall.v1", 0)
	return nil
}

// Scaffold emits the 4-file context skeleton plus a migration stub and a
// Routes wiring snippet. Keys are file paths relative to the new context dir
// (or helper artifacts); values are file contents.
func (s *Service) Scaffold(_ context.Context, spec ScaffoldSpec) (map[string]string, error) {
	name := strings.ToLower(strings.TrimSpace(spec.Name))
	family := strings.TrimSpace(spec.Family)
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("modulebuilder: invalid module name %q: %w", spec.Name, platform.ErrValidation)
	}
	if family == "" {
		return nil, fmt.Errorf("modulebuilder: family required: %w", platform.ErrValidation)
	}
	title := strings.ToUpper(name[:1]) + name[1:]
	files := map[string]string{
		"domain.go":  scaffoldDomain(name, title, family),
		"store.go":   scaffoldStore(name, title),
		"service.go": scaffoldService(name, title),
		"handler.go": scaffoldHandler(name, title),
		"migration.stub.sql": fmt.Sprintf(`-- pending: %s tables (centrally renumbered on merge).
CREATE TABLE ferp_%s (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
`, name, name),
		"routes.snippet.txt": fmt.Sprintf(`// Paste into mountAPIRoutes (backend/cmd/api/main.go):
//   %s.Routes(r, w.%s, idH.Require)
%s.Routes(r, w.%s, idH.Require)
`, name, title, name, title),
	}
	return files, nil
}

// ScaffoldKeys returns artifact names in sorted order (stable goldens).
func ScaffoldKeys(files map[string]string) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func scaffoldDomain(name, title, family string) string {
	return fmt.Sprintf(`// Package %s implements the %s bounded context (family %s).
package %s

import (
	"fmt"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// %s is the aggregate root (int64 minor units for any money; extend here).
type %s struct {
	ID       int64  `+"`json:\"id\"`"+`
	EntityID int64  `+"`json:\"entity_id\"`"+`
	Name     string `+"`json:\"name\"`"+`
}

// Validate checks %s invariants.
func (e %s) Validate() error {
	if e.EntityID <= 0 {
		return fmt.Errorf("%s: entity_id required: %%w", platform.ErrValidation)
	}
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("%s: name required: %%w", platform.ErrValidation)
	}
	return nil
}
`, name, title, family, name, title, title, title, title, name, name)
}

func scaffoldStore(name, title string) string {
	return fmt.Sprintf(`package %s

import (
	"context"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for %s.
type Store interface {
	Create(ctx context.Context, db platform.DBTX, e *%s) error
	Get(ctx context.Context, db platform.DBTX, entityID int64, id int64) (%s, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{}

// NewPGStore builds a PGStore.
func NewPGStore() *PGStore { return &PGStore{} }

func (s *PGStore) Create(ctx context.Context, db platform.DBTX, e *%s) error {
	if err := e.Validate(); err != nil {
		return err
	}
	return db.QueryRow(ctx, `+"`INSERT INTO ferp_%s (entity_id) VALUES ($1) RETURNING id`"+`, e.EntityID).Scan(&e.ID)
}

func (s *PGStore) Get(ctx context.Context, db platform.DBTX, entityID int64, id int64) (%s, error) {
	var e %s
	if err := db.QueryRow(ctx, `+"`SELECT id, entity_id FROM ferp_%s WHERE id=$1 AND entity_id=$2`"+`,
		id, entityID).Scan(&e.ID, &e.EntityID); err != nil {
		return %s{}, platform.ErrNotFound
	}
	return e, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu  sync.Mutex
	seq int64
	rows map[int64]%s
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[int64]%s{}} }
`, name, title, title, title, title, title, name, title, name, title, title, title)
}

func scaffoldService(name, title string) string {
	return fmt.Sprintf(`package %s

import (
	"context"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service orchestrates %s use cases.
type Service struct {
	Store Store
	Bus   platform.Bus
}

// NewService builds a Service.
func NewService(s Store, bus platform.Bus) *Service { return &Service{Store: s, Bus: bus} }

// Create validates and persists one %s.
func (s *Service) Create(ctx context.Context, db platform.DBTX, e *%s) error {
	return s.Store.Create(ctx, db, e)
}
`, name, title, title, title)
}

func scaffoldHandler(name, title string) string {
	return fmt.Sprintf(`package %s

import (
	"encoding/json"
	"net/http"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to the service layer.
type Deps struct {
	Svc *Service
	DB  platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the %s surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("%s", "record", "write")).Post("/%s", h.Create)
}

type Handler struct{ deps Deps }

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var e %s
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	e.EntityID = entityID
	if err := h.deps.Svc.Create(r.Context(), h.deps.DB, &e); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(e)
}
`, name, title, name, name, title)
}
