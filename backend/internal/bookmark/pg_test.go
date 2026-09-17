package bookmark

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGBookmarkPersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore()
	b := &Bookmark{EntityID: 1, UserLogin: "ada", Scope: "sales", ObjectType: "invoice", ObjectID: 11}
	if err := st.Add(ctx, pool, b); err != nil {
		t.Fatalf("add: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("ID unset")
	}
	list, err := st.ListForUser(ctx, pool, 1, "ada")
	if err != nil || len(list) != 1 || list[0].ObjectID != 11 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := st.Remove(ctx, pool, 1, b.ID, "ada"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	list, _ = st.ListForUser(ctx, pool, 1, "ada")
	if len(list) != 0 {
		t.Fatalf("list after remove=%d want 0", len(list))
	}
}
