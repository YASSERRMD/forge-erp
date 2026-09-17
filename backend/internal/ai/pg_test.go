package ai

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGAIRunLog(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)
	r := &Run{EntityID: 1, Model: "echo", PromptExcerpt: "hi", OutputExcerpt: "hello", DurationMs: 3}
	if err := st.Log(ctx, pool, r); err != nil {
		t.Fatalf("log: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("ID unset")
	}
	list, err := st.List(ctx, pool, 1, 50, 0)
	if err != nil || len(list) != 1 || list[0].Model != "echo" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}
