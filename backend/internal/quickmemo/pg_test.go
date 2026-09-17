package quickmemo

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGMemoPersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore()
	m := &Memo{EntityID: 1, UserLogin: "ada", Title: "PG note", Body: "persist me"}
	if err := st.Create(ctx, pool, m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 || m.RowVersion != 1 {
		t.Fatalf("bad insert: %+v", m)
	}
	got, err := st.MemoByID(ctx, pool, 1, m.ID, "ada")
	if err != nil || got.Title != "PG note" {
		t.Fatalf("read: %+v err=%v", got, err)
	}
	upd, err := st.Update(ctx, pool, 1, m.ID, "ada", "PG note v2", "still here", m.RowVersion)
	if err != nil || upd.RowVersion != 2 {
		t.Fatalf("update: %+v err=%v", upd, err)
	}
	if err := st.Delete(ctx, pool, 1, m.ID, "ada"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
