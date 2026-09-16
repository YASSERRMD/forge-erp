package documentsvc

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func testSvc() *Service {
	return &Service{Store: NewMemoryStore(), Storage: NewMemoryStorage()}
}

func TestFolderTreeOps(t *testing.T) {
	ctx := context.Background()
	svc := testSvc()
	root, err := svc.CreateFolder(ctx, 1, nil, "sales")
	if err != nil {
		t.Fatal(err)
	}
	child, err := svc.CreateFolder(ctx, 1, &root.ID, "invoices")
	if err != nil {
		t.Fatal(err)
	}
	// Duplicate name under same parent rejected.
	if _, err := svc.CreateFolder(ctx, 1, &root.ID, "invoices"); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("dup folder err=%v", err)
	}
	// Same name under a different parent is fine.
	if _, err := svc.CreateFolder(ctx, 1, nil, "archive"); err != nil {
		t.Fatalf("sibling name: %v", err)
	}
	// Cross-entity isolation.
	if _, err := svc.Store.FolderByID(ctx, nil, 2, root.ID); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-entity folder err=%v", err)
	}
	// Path resolution.
	p, err := svc.FolderPath(ctx, 1, child.ID)
	if err != nil || p != "/sales/invoices" {
		t.Fatalf("path=%q err=%v", p, err)
	}
	// Move: child to root.
	moved, err := svc.MoveFolder(ctx, 1, child.ID, nil)
	if err != nil || moved.ParentID != nil {
		t.Fatalf("move=%+v err=%v", moved, err)
	}
	if p, _ := svc.FolderPath(ctx, 1, child.ID); p != "/invoices" {
		t.Fatalf("moved path=%q", p)
	}
}

func TestFolderCycleRejection(t *testing.T) {
	ctx := context.Background()
	svc := testSvc()
	a, _ := svc.CreateFolder(ctx, 1, nil, "a")
	b, _ := svc.CreateFolder(ctx, 1, &a.ID, "b")
	c, _ := svc.CreateFolder(ctx, 1, &b.ID, "c")
	// Move into self.
	if _, err := svc.MoveFolder(ctx, 1, a.ID, &a.ID); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("self-move err=%v", err)
	}
	// Move into own descendant (a -> c, c is under a).
	if _, err := svc.MoveFolder(ctx, 1, a.ID, &c.ID); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("descendant-move err=%v", err)
	}
	// Move into grandchild (b -> c).
	if _, err := svc.MoveFolder(ctx, 1, b.ID, &c.ID); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("grandchild-move err=%v", err)
	}
	// Legal move (c -> root) still works.
	if _, err := svc.MoveFolder(ctx, 1, c.ID, nil); err != nil {
		t.Fatalf("legal move: %v", err)
	}
	// Foreign parent rejected.
	if _, err := svc.MoveFolder(ctx, 2, c.ID, nil); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("foreign move err=%v", err)
	}
}

func TestVersioningRestore(t *testing.T) {
	ctx := context.Background()
	svc := testSvc()
	put := func(body string) Document {
		t.Helper()
		d, err := svc.Upload(ctx, 1, "sales", 9, "note.txt", "text/plain",
			bytes.NewReader([]byte(body)), nil)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	v1 := put("hello version one")
	if v1.Version != 1 {
		t.Fatalf("v1=%+v", v1)
	}
	v2 := put("hello version two")
	if v2.Version != 2 || v2.ID != v1.ID {
		t.Fatalf("v2=%+v want same id new version", v2)
	}
	if v2.StorageKey == v1.StorageKey {
		t.Fatal("versioned keys must differ")
	}
	hist, err := svc.Versions(ctx, 1, v1.ID)
	if err != nil || len(hist) != 2 || hist[0].Version != 1 || hist[1].Version != 2 {
		t.Fatalf("hist=%+v err=%v", hist, err)
	}
	// Restore v1 -> creates v3, history untouched.
	v3, err := svc.RestoreVersion(ctx, 1, v1.ID, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v3.Version != 3 {
		t.Fatalf("restored=%+v", v3)
	}
	hist, _ = svc.Versions(ctx, 1, v1.ID)
	if len(hist) != 3 || hist[0].StorageKey == hist[2].StorageKey {
		t.Fatalf("history mutated: %+v", hist)
	}
	rc, err := svc.Storage.Get(ctx, v3.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rc)
	if buf.String() != "hello version one" {
		t.Fatalf("restored bytes=%q", buf.String())
	}
	// Restoring the current version is a no-op.
	same, err := svc.RestoreVersion(ctx, 1, v1.ID, 3, nil)
	if err != nil || same.Version != 3 {
		t.Fatalf("noop restore=%+v err=%v", same, err)
	}
	if _, err := svc.RestoreVersion(ctx, 1, v1.ID, 99, nil); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("bogus version err=%v", err)
	}
}

func TestFilingPathComputation(t *testing.T) {
	got := FilingPath("/sales/invoices/{entity}", 7, 42, "invoice", "sales")
	if got != "/sales/invoices/7" {
		t.Fatalf("path=%q", got)
	}
	got = FilingPath("/{scope}/{object_type}/{entity_id}/{object_id}", 1, 2, "order", "sales")
	if got != "/sales/order/1/2" {
		t.Fatalf("path=%q", got)
	}
	if segs, err := SplitPath("/sales/invoices/7"); err != nil || len(segs) != 3 {
		t.Fatalf("segs=%v err=%v", segs, err)
	}
	if _, err := SplitPath("/a/../b"); err == nil {
		t.Fatal("dot segment accepted")
	}
}

func TestAutoFilingEndToEndMemory(t *testing.T) {
	ctx := context.Background()
	svc := testSvc()
	d, err := svc.UploadEx(ctx, 3, UploadInput{Scope: "sales", ObjectType: "invoice",
		ObjectID: 11, Name: "inv-11.pdf", MIME: "application/pdf",
		Body: bytes.NewReader([]byte("%PDF-fake")), AutoFile: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.FolderID == nil {
		t.Fatal("auto-filed doc has no folder")
	}
	p, err := svc.FolderPath(ctx, 3, *d.FolderID)
	if err != nil || p != "/sales/invoices/3" {
		t.Fatalf("path=%q err=%v", p, err)
	}
	// Unknown rule rejected.
	if _, err := svc.UploadEx(ctx, 3, UploadInput{Scope: "nope", ObjectType: "x",
		Name: "a.txt", MIME: "text/plain", Body: bytes.NewReader([]byte("a")),
		AutoFile: true}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("rule-less autofile err=%v", err)
	}
}

func TestSearchMemory(t *testing.T) {
	ctx := context.Background()
	svc := testSvc()
	if _, err := svc.Upload(ctx, 1, "sales", 1, "contract.txt", "text/plain",
		bytes.NewReader([]byte("framework agreement arbitration clause")), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(ctx, 1, "sales", 2, "report.pdf", "application/pdf",
		bytes.NewReader([]byte("%PDF-binary")), nil); err != nil {
		t.Fatal(err)
	}
	hits, err := svc.Search(ctx, 1, "arbitration", "", 10)
	if err != nil || len(hits) != 1 || hits[0].Document.Name != "contract.txt" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	// PDF indexed by filename fallback.
	hits, err = svc.Search(ctx, 1, "report", "", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("filename fallback hits=%+v err=%v", hits, err)
	}
	// Entity isolation.
	hits, _ = svc.Search(ctx, 2, "arbitration", "", 10)
	if len(hits) != 0 {
		t.Fatal("cross-entity search leak")
	}
	if _, err := svc.Search(ctx, 1, "  ", "", 10); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty query err=%v", err)
	}
}

func TestExtractTextPolicy(t *testing.T) {
	if got := extractText("a.txt", "text/plain", []byte("body")); !strings.Contains(got, "body") {
		t.Fatalf("text not indexed: %q", got)
	}
	if got := extractText("scan.pdf", "application/pdf", []byte("%PDF-1")); got != "scan.pdf" {
		t.Fatalf("binary indexed beyond name: %q", got)
	}
}
