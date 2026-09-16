package documentsvc

// PG-gated ECM tests: folders persist, filing end-to-end, tsvector search.
// Skipped when TEST_DATABASE_URL is unset (CI sets it).

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pgSvc(t *testing.T) (*Service, *PGStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	return &Service{Store: st, Storage: NewMemoryStorage(), DB: pool}, st, pool
}

func TestPGFoldersPersist(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := pgSvc(t)
	root, err := svc.CreateFolder(ctx, 1, nil, "sales")
	if err != nil {
		t.Fatal(err)
	}
	child, err := svc.CreateFolder(ctx, 1, &root.ID, "invoices")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFolder(ctx, 1, &root.ID, "invoices"); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("dup folder err=%v", err)
	}
	p, err := svc.FolderPath(ctx, 1, child.ID)
	if err != nil || p != "/sales/invoices" {
		t.Fatalf("path=%q err=%v", p, err)
	}
	kids, err := svc.Store.ListFolders(ctx, svc.DB, 1, &root.ID)
	if err != nil || len(kids) != 1 || kids[0].Name != "invoices" {
		t.Fatalf("kids=%+v err=%v", kids, err)
	}
	if _, err := svc.MoveFolder(ctx, 1, root.ID, &child.ID); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("cycle err=%v", err)
	}
	if _, err := svc.MoveFolder(ctx, 1, child.ID, nil); err != nil {
		t.Fatalf("move: %v", err)
	}
}

func TestPGFilingEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := pgSvc(t)
	d, err := svc.UploadEx(ctx, 1, UploadInput{Scope: "sales", ObjectType: "invoice",
		ObjectID: 5, Name: "inv-5.txt", MIME: "text/plain",
		Body: bytes.NewReader([]byte("invoice five total 100")), AutoFile: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || d.FolderID == nil {
		t.Fatalf("doc=%+v", d)
	}
	p, err := svc.FolderPath(ctx, 1, *d.FolderID)
	if err != nil || p != "/sales/invoices/1" {
		t.Fatalf("path=%q err=%v", p, err)
	}
	// Re-upload same folder+name -> version 2.
	d2, err := svc.UploadEx(ctx, 1, UploadInput{Scope: "sales", ObjectType: "invoice",
		ObjectID: 5, Name: "inv-5.txt", MIME: "text/plain",
		Body: bytes.NewReader([]byte("invoice five total 120")), AutoFile: true})
	if err != nil {
		t.Fatal(err)
	}
	if d2.ID != d.ID || d2.Version != 2 {
		t.Fatalf("re-upload=%+v", d2)
	}
	hist, err := svc.Versions(ctx, 1, d.ID)
	if err != nil || len(hist) != 2 {
		t.Fatalf("hist=%+v err=%v", hist, err)
	}
	// Restore v1 -> v3 with original bytes.
	d3, err := svc.RestoreVersion(ctx, 1, d.ID, 1, nil)
	if err != nil || d3.Version != 3 {
		t.Fatalf("restored=%+v err=%v", d3, err)
	}
}

func TestPGTsvectorSearch(t *testing.T) {
	ctx := context.Background()
	svc, _, pool := pgSvc(t)
	if _, err := svc.Upload(ctx, 1, "sales", 1, "contract.txt", "text/plain",
		bytes.NewReader([]byte("framework agreement arbitration clause")), nil); err != nil {
		t.Fatal(err)
	}
	hits, err := svc.Search(ctx, 1, "arbitration", "", 10)
	if err != nil || len(hits) == 0 || hits[0].Document.Name != "contract.txt" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	if hits[0].Rank <= 0 {
		t.Fatalf("rank=%v", hits[0].Rank)
	}
	// Scope filter.
	hits, err = svc.Search(ctx, 1, "arbitration", "purchase", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("scope leak: %+v err=%v", hits, err)
	}
	// Entity isolation: a second tenant's search sees nothing.
	other := pgtest.NewEntity(t, pool, "ecm-search-other")
	hits, err = svc.Search(ctx, other, "arbitration", "", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("entity leak: %+v err=%v", hits, err)
	}
}
