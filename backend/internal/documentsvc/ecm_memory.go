package documentsvc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// --- MemoryStore: folders ---

func (m *MemoryStore) CreateFolder(_ context.Context, _ platform.DBTX, f *Folder) error {
	if err := f.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.folders {
		if e.EntityID == f.EntityID && folderEqual(e.ParentID, f.ParentID) && e.Name == f.Name {
			return fmt.Errorf("documentsvc: duplicate folder name: %w", platform.ErrConflict)
		}
	}
	m.folderSeq++
	f.ID = m.folderSeq
	f.CreatedAt = time.Now().UTC()
	m.folders[f.ID] = *f
	return nil
}

func (m *MemoryStore) FolderByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[id]
	if !ok || f.EntityID != entityID {
		return Folder{}, fmt.Errorf("documentsvc: folder not found: %w", platform.ErrNotFound)
	}
	return f, nil
}

func (m *MemoryStore) FindFolder(_ context.Context, _ platform.DBTX, entityID int64, parentID *int64, name string) (Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.folders {
		if f.EntityID == entityID && folderEqual(f.ParentID, parentID) && f.Name == name {
			return f, nil
		}
	}
	return Folder{}, fmt.Errorf("documentsvc: folder not found: %w", platform.ErrNotFound)
}

func (m *MemoryStore) ListFolders(_ context.Context, _ platform.DBTX, entityID int64, parentID *int64) ([]Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Folder
	for _, f := range m.folders {
		if f.EntityID == entityID && folderEqual(f.ParentID, parentID) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemoryStore) MoveFolder(_ context.Context, _ platform.DBTX, entityID int64, id int64, newParent *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[id]
	if !ok || f.EntityID != entityID {
		return fmt.Errorf("documentsvc: folder not found: %w", platform.ErrNotFound)
	}
	for _, e := range m.folders {
		if e.ID != id && e.EntityID == entityID && folderEqual(e.ParentID, newParent) && e.Name == f.Name {
			return fmt.Errorf("documentsvc: duplicate folder name: %w", platform.ErrConflict)
		}
	}
	f.ParentID = newParent
	m.folders[id] = f
	return nil
}

// --- MemoryStore: versioned files ---

func (m *MemoryStore) FindByFolderName(_ context.Context, _ platform.DBTX, entityID int64, folderID *int64, name string) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.docs {
		if d.EntityID == entityID && folderEqual(d.FolderID, folderID) && d.Name == name {
			return d, nil
		}
	}
	return Document{}, fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
}

func (m *MemoryStore) UpdateCurrent(_ context.Context, _ platform.DBTX, d *Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.docs[d.ID]
	if !ok || cur.EntityID != d.EntityID {
		return fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
	}
	m.docs[d.ID] = *d
	return nil
}

func (m *MemoryStore) CreateVersion(_ context.Context, _ platform.DBTX, v *FileVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.versions[v.FileID] {
		if e.Version == v.Version {
			return fmt.Errorf("documentsvc: duplicate version: %w", platform.ErrConflict)
		}
	}
	m.verSeq++
	v.ID = m.verSeq
	v.CreatedAt = time.Now().UTC()
	m.versions[v.FileID] = append(m.versions[v.FileID], *v)
	return nil
}

func (m *MemoryStore) VersionsByFile(_ context.Context, _ platform.DBTX, entityID int64, fileID int64) ([]FileVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[fileID]
	if !ok || d.EntityID != entityID {
		return nil, fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
	}
	out := append([]FileVersion(nil), m.versions[fileID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (m *MemoryStore) VersionByNumber(_ context.Context, _ platform.DBTX, entityID int64, fileID int64, version int) (FileVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[fileID]
	if !ok || d.EntityID != entityID {
		return FileVersion{}, fmt.Errorf("documentsvc: not found: %w", platform.ErrNotFound)
	}
	for _, v := range m.versions[fileID] {
		if v.Version == version {
			return v, nil
		}
	}
	return FileVersion{}, fmt.Errorf("documentsvc: version not found: %w", platform.ErrNotFound)
}

// --- MemoryStore: text index + search ---

func (m *MemoryStore) UpsertFileText(_ context.Context, _ platform.DBTX, fileID int64, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.texts[fileID] = content
	return nil
}

func (m *MemoryStore) SearchFiles(_ context.Context, _ platform.DBTX, entityID int64, query string, scope string, limit int) ([]SearchHit, error) {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return nil, fmt.Errorf("documentsvc: query required: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SearchHit
	for _, d := range m.docs {
		if d.EntityID != entityID || (scope != "" && d.Scope != scope) {
			continue
		}
		hay := strings.ToLower(m.texts[d.ID] + "\n" + d.Name)
		matched, nameHit := 0, false
		for _, tok := range tokens {
			if strings.Contains(hay, tok) {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		nameHit = strings.Contains(strings.ToLower(d.Name), tokens[0])
		rank := float64(matched) / float64(len(tokens))
		if nameHit {
			rank += 0.5
		}
		out = append(out, SearchHit{Document: d, Rank: rank})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].Document.ID > out[j].Document.ID
		}
		return out[i].Rank > out[j].Rank
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- MemoryStore: filing rules ---

func ruleKey(scope, objectType string) string { return scope + "\x00" + objectType }

func (m *MemoryStore) UpsertFilingRule(_ context.Context, _ platform.DBTX, r FilingRule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules[ruleKey(r.Scope, r.ObjectType)] = r
	return nil
}

func (m *MemoryStore) FilingRuleFor(_ context.Context, _ platform.DBTX, scope string, objectType string) (FilingRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rules[ruleKey(scope, objectType)]
	if !ok {
		return FilingRule{}, fmt.Errorf("documentsvc: no filing rule: %w", platform.ErrNotFound)
	}
	return r, nil
}

func (m *MemoryStore) ListFilingRules(_ context.Context, _ platform.DBTX) ([]FilingRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]FilingRule, 0, len(m.rules))
	for _, r := range m.rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope == out[j].Scope {
			return out[i].ObjectType < out[j].ObjectType
		}
		return out[i].Scope < out[j].Scope
	})
	return out, nil
}
