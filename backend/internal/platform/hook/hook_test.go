package hook

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func TestPriorityOrdering(t *testing.T) {
	b := NewBus()
	var order []int
	for _, p := range []int{10, -5, 0, 5} {
		p := p
		b.Register("test.order", p, func(context.Context, pgx.Tx, Context, any) (Result, error) {
			order = append(order, p)
			return Result{}, nil
		})
	}
	if _, err := b.Execute(context.Background(), nil, "test.order", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []int{-5, 0, 5, 10}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("order=%v want %v", order, want)
	}
}

func TestEqualPriorityKeepsRegistrationOrder(t *testing.T) {
	b := NewBus()
	var order []string
	for _, name := range []string{"a", "b", "c"} {
		name := name
		b.Register("test.stable", 0, func(context.Context, pgx.Tx, Context, any) (Result, error) {
			order = append(order, name)
			return Result{}, nil
		})
	}
	if _, err := b.Execute(context.Background(), nil, "test.stable", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fmt.Sprint(order) != fmt.Sprint([]string{"a", "b", "c"}) {
		t.Fatalf("order=%v want [a b c]", order)
	}
}

func TestVetoAbortsChain(t *testing.T) {
	b := NewBus()
	veto := fmt.Errorf("hook: loyalty block: %w", platform.ErrValidation)
	var secondRan bool
	b.Register("test.veto", 0, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		return Result{Veto: veto, Data: map[string]any{"hook_tag": "loyalty-veto"}}, nil
	})
	b.Register("test.veto", 1, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		secondRan = true
		return Result{}, nil
	})
	res, err := b.Execute(context.Background(), nil, "test.veto", nil)
	if !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("err=%v want platform.ErrValidation", err)
	}
	if !errors.Is(err, veto) {
		t.Fatalf("err=%v want veto identity", err)
	}
	if secondRan {
		t.Fatal("second handler ran after veto")
	}
	if res.Data["hook_tag"] != "loyalty-veto" {
		t.Fatalf("data=%v want hook_tag merged on veto path", res.Data)
	}
}

func TestMutateAppliesToSubject(t *testing.T) {
	type subj struct{ OrgID int64 }
	b := NewBus()
	b.Register("test.mutate", 0, func(_ context.Context, _ pgx.Tx, _ Context, s any) (Result, error) {
		return Result{
			Mutate: func(v any) error {
				v.(*subj).OrgID = 7
				return nil
			},
			Data: map[string]any{"a": 1},
		}, nil
	})
	b.Register("test.mutate", 1, func(_ context.Context, _ pgx.Tx, _ Context, s any) (Result, error) {
		if s.(*subj).OrgID != 7 {
			return Result{}, fmt.Errorf("hook: mutate not visible to later handler: %w", platform.ErrValidation)
		}
		return Result{Data: map[string]any{"b": 2}}, nil
	})
	s := &subj{}
	res, err := b.Execute(context.Background(), nil, "test.mutate", s)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if s.OrgID != 7 {
		t.Fatalf("subject=%+v want OrgID 7", s)
	}
	if res.Data["a"] != 1 || res.Data["b"] != 2 {
		t.Fatalf("data=%v want {a:1 b:2} merged", res.Data)
	}
}

func TestHandlerErrorAborts(t *testing.T) {
	b := NewBus()
	boom := fmt.Errorf("hook: pricing offline: %w", platform.ErrValidation)
	var secondRan bool
	b.Register("test.err", 0, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		return Result{}, boom
	})
	b.Register("test.err", 1, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		secondRan = true
		return Result{}, nil
	})
	if _, err := b.Execute(context.Background(), nil, "test.err", nil); !errors.Is(err, boom) {
		t.Fatalf("err=%v want boom", err)
	}
	if secondRan {
		t.Fatal("second handler ran after error")
	}
}

func TestMutateErrorAborts(t *testing.T) {
	b := NewBus()
	mutateErr := fmt.Errorf("hook: bad mutate: %w", platform.ErrValidation)
	b.Register("test.merr", 0, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		return Result{Mutate: func(any) error { return mutateErr }}, nil
	})
	if _, err := b.Execute(context.Background(), nil, "test.merr", nil); !errors.Is(err, mutateErr) {
		t.Fatalf("err=%v want mutate error", err)
	}
}

func TestContextsAreIsolated(t *testing.T) {
	b := NewBus()
	var ran bool
	b.Register("test.a", 0, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		ran = true
		return Result{}, nil
	})
	if _, err := b.Execute(context.Background(), nil, "test.b", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ran {
		t.Fatal("handler for test.a ran on test.b")
	}
}

func TestNilBusIsNoOp(t *testing.T) {
	var b *Bus
	res, err := b.Execute(context.Background(), nil, POSCheckoutValidate, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data == nil {
		t.Fatal("data map is nil")
	}
}

func TestCancelledContextAborts(t *testing.T) {
	b := NewBus()
	b.Register("test.cancel", 0, func(context.Context, pgx.Tx, Context, any) (Result, error) {
		return Result{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Execute(ctx, nil, "test.cancel", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}
