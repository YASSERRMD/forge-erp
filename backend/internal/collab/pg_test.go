package collab

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGCommentPersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)
	c := &Comment{EntityID: 1, Scope: "sales", ObjectType: "invoice", ObjectID: 11,
		Author: "ada", Body: "pg note"}
	if err := st.Add(ctx, pool, c); err != nil {
		t.Fatalf("add: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("ID unset")
	}
	list, err := st.ListForObject(ctx, pool, 1, "sales", "invoice", 11, "", 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := st.Remove(ctx, pool, 1, c.ID, "ada"); err != nil {
		t.Fatalf("remove: %v", err)
	}
}
