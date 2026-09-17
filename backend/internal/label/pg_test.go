package label

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGSheetPersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)
	sh := DefaultAvery65(1)
	if err := st.CreateSheet(ctx, pool, &sh); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sh.ID == 0 {
		t.Fatal("ID unset")
	}
	got, err := st.SheetByCode(ctx, pool, 1, "avery-65")
	if err != nil || got.LabelsPerSheet() != 65 {
		t.Fatalf("by-code=%+v err=%v", got, err)
	}
}
