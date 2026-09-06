package documentsvc

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestUploadRoundtripMemory(t *testing.T) {
	ctx := context.Background()
	svc := &Service{Store: NewMemoryStore(), Storage: NewMemoryStorage()}
	d, err := svc.Upload(ctx, 1, "sales", 42, "quote.pdf", "application/pdf",
		bytes.NewReader([]byte("%PDF-hello")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Size != 10 || d.SHA256 == "" || d.StorageKey == "" {
		t.Fatalf("doc: %+v", d)
	}
	rc, err := svc.Storage.Get(ctx, d.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "%PDF-hello" {
		t.Fatalf("bytes = %q", b)
	}
	list, err := svc.Store.List(ctx, 1, "sales", 42)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	// Other entity isolated.
	empty, _ := svc.Store.List(ctx, 2, "sales", 42)
	if len(empty) != 0 {
		t.Fatal("cross-entity leak")
	}
}

func TestUploadValidation(t *testing.T) {
	ctx := context.Background()
	svc := &Service{Store: NewMemoryStore(), Storage: NewMemoryStorage()}
	if _, err := svc.Upload(ctx, 1, "", 0, "x", "text/plain", bytes.NewReader([]byte("hi")), nil); err == nil {
		t.Fatal("scope-less upload accepted")
	}
}

func TestDirStorageRoundtrip(t *testing.T) {
	ctx := context.Background()
	st, err := NewDirStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "a/b.bin", bytes.NewReader([]byte("data")), 4, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	rc, err := st.Get(ctx, "a/b.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "data" {
		t.Fatalf("bytes = %q", b)
	}
	if err := st.Delete(ctx, "a/b.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "a/b.bin"); err == nil {
		t.Fatal("deleted file readable")
	}
}
