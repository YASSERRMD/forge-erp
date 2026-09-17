package dynprice

import (
	"testing"
)

func TestEvalTable(t *testing.T) {
	cases := []struct {
		expr string
		in   Input
		want int64
	}{
		{"base", Input{Base: 1000}, 1000},
		{"base * 0.9", Input{Base: 1000}, 900},
		{"max(base * 0.9, cost)", Input{Base: 1000, Cost: 950}, 950},
		{"max(base * 0.9, cost)", Input{Base: 1000, Cost: 700}, 900},
		{"min(base, cost * 2)", Input{Base: 1000, Cost: 400}, 800},
		{"base * qty", Input{Base: 250, Qty: 4}, 1000},
		{"(base - cost) * qty", Input{Base: 300, Cost: 100, Qty: 2}, 400},
		{"base / 3", Input{Base: 1000}, 333}, // 333.33… rounds down
		{"base / 6", Input{Base: 1000}, 167}, // 166.66… rounds up
		{"-base", Input{Base: 100}, -100},
		{"base * 0.333", Input{Base: 1000}, 333},
		{"max(min(base, 500), 100)", Input{Base: 1000}, 500},
		{"  max( base , cost ) ", Input{Base: 10, Cost: 20}, 20},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, c.in)
		if err != nil {
			t.Errorf("Eval(%q): %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %d want %d", c.expr, got, c.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	for _, expr := range []string{
		"", "base +", "base / 0", "bogus", "price", "max(base)",
		"max(base, cost, qty)", "base * (1 + 2", "1.2.3", "base ${x}",
	} {
		if _, err := Eval(expr, Input{Base: 1}); err == nil {
			t.Errorf("Eval(%q) accepted", expr)
		}
	}
	// Division by zero through a variable, not just a literal.
	if _, err := Eval("base / qty", Input{Base: 100, Qty: 0}); err == nil {
		t.Error("division by variable zero accepted")
	}
}

func TestRuleValidation(t *testing.T) {
	bad := Rule{EntityID: 1, Code: "x", Expression: "nope"}
	if bad.Validate() == nil {
		t.Error("unknown name accepted")
	}
	good := Rule{EntityID: 1, Code: "ten-off", Label: "10% off",
		Expression: "max(base * 0.9, cost)"}
	if err := good.Validate(); err != nil {
		t.Errorf("valid rule rejected: %v", err)
	}
}
