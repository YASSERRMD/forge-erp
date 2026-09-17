package dynprice

import (
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

// Expression grammar (whitespace-insensitive):
//
//	expr   := term { ("+" | "-") term }
//	term   := factor { ("*" | "/") factor }
//	factor := ["-"] ( number | var | func | "(" expr ")" )
//	func   := ("max" | "min") "(" expr "," expr ")"
//	var    := "base" | "qty" | "cost"
//	number := digits ["." digits]   (decimal, exact rational)
//
// All arithmetic is *big.Rat (exact); Eval rounds half-up away from zero to
// int64. Division by zero and unknown names are errors, never panics.

// Node is one parsed expression node.
type Node struct {
	op       byte // 0=const, 'v'=var, '+','-','*','/','m'(max),'n'(min),'g'(neg)
	val      *big.Rat
	name     string
	left     *Node
	right    *Node
	Decimals int // source decimal places (documentation only)
}

// Parse compiles an expression (validation errors wrap platform.ErrValidation
// at the domain layer; here plain errors keep expr testable standalone).
func Parse(src string) (*Node, error) {
	p := &parser{s: src}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("dynprice: unexpected %q", p.s[p.pos:])
	}
	return n, nil
}

type parser struct {
	s   string
	pos int
}

func (p *parser) skip() {
	for p.pos < len(p.s) && unicode.IsSpace(rune(p.s[p.pos])) {
		p.pos++
	}
}

func (p *parser) parseExpr() (*Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		p.skip()
		if p.pos >= len(p.s) || (p.s[p.pos] != '+' && p.s[p.pos] != '-') {
			return left, nil
		}
		op := p.s[p.pos]
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &Node{op: op, left: left, right: right}
	}
}

func (p *parser) parseTerm() (*Node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		p.skip()
		if p.pos >= len(p.s) || (p.s[p.pos] != '*' && p.s[p.pos] != '/') {
			return left, nil
		}
		op := p.s[p.pos]
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &Node{op: op, left: left, right: right}
	}
}

func (p *parser) parseFactor() (*Node, error) {
	p.skip()
	if p.pos >= len(p.s) {
		return nil, fmt.Errorf("dynprice: unexpected end of expression")
	}
	if p.s[p.pos] == '-' {
		p.pos++
		inner, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &Node{op: 'g', left: inner}, nil
	}
	c := p.s[p.pos]
	switch {
	case c == '(':
		p.pos++
		n, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skip()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return nil, fmt.Errorf("dynprice: missing closing paren")
		}
		p.pos++
		return n, nil
	case c >= '0' && c <= '9' || c == '.':
		return p.parseNumber()
	case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_':
		return p.parseName()
	default:
		return nil, fmt.Errorf("dynprice: unexpected character %q", string(c))
	}
}

func (p *parser) parseNumber() (*Node, error) {
	start := p.pos
	dots := 0
	for p.pos < len(p.s) && (p.s[p.pos] >= '0' && p.s[p.pos] <= '9' || p.s[p.pos] == '.') {
		if p.s[p.pos] == '.' {
			dots++
		}
		p.pos++
	}
	lit := p.s[start:p.pos]
	if lit == "" || lit == "." || dots > 1 {
		return nil, fmt.Errorf("dynprice: bad number %q", lit)
	}
	r := new(big.Rat)
	if _, ok := r.SetString(lit); !ok {
		return nil, fmt.Errorf("dynprice: bad number %q", lit)
	}
	dec := 0
	if i := strings.IndexByte(lit, '.'); i >= 0 {
		dec = len(lit) - i - 1
	}
	return &Node{val: r, Decimals: dec}, nil
}

func (p *parser) parseName() (*Node, error) {
	start := p.pos
	for p.pos < len(p.s) && (p.s[p.pos] >= 'a' && p.s[p.pos] <= 'z' ||
		p.s[p.pos] >= 'A' && p.s[p.pos] <= 'Z' ||
		p.s[p.pos] >= '0' && p.s[p.pos] <= '9' || p.s[p.pos] == '_') {
		p.pos++
	}
	name := strings.ToLower(p.s[start:p.pos])
	switch name {
	case "base", "qty", "cost":
		return &Node{op: 'v', name: name}, nil
	case "max", "min":
		p.skip()
		if p.pos >= len(p.s) || p.s[p.pos] != '(' {
			return nil, fmt.Errorf("dynprice: %s needs (a, b)", name)
		}
		p.pos++
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skip()
		if p.pos >= len(p.s) || p.s[p.pos] != ',' {
			return nil, fmt.Errorf("dynprice: %s needs two arguments", name)
		}
		p.pos++
		b, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skip()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return nil, fmt.Errorf("dynprice: missing closing paren for %s", name)
		}
		p.pos++
		op := byte('m')
		if name == "min" {
			op = 'n'
		}
		return &Node{op: op, left: a, right: b}, nil
	default:
		return nil, fmt.Errorf("dynprice: unknown name %q (want base, qty, cost, max, min)", name)
	}
}

// eval computes the exact rational value.
func (n *Node) eval(in Input) (*big.Rat, error) {
	switch n.op {
	case 0:
		return new(big.Rat).Set(n.val), nil
	case 'v':
		switch n.name {
		case "base":
			return big.NewRat(in.Base, 1), nil
		case "qty":
			return big.NewRat(in.Qty, 1), nil
		case "cost":
			return big.NewRat(in.Cost, 1), nil
		}
		return nil, fmt.Errorf("dynprice: unknown variable %q", n.name)
	case 'g':
		v, err := n.left.eval(in)
		if err != nil {
			return nil, err
		}
		return v.Neg(v), nil
	case '+', '-', '*':
		a, err := n.left.eval(in)
		if err != nil {
			return nil, err
		}
		b, err := n.right.eval(in)
		if err != nil {
			return nil, err
		}
		switch n.op {
		case '+':
			return a.Add(a, b), nil
		case '-':
			return a.Sub(a, b), nil
		default:
			return a.Mul(a, b), nil
		}
	case '/':
		a, err := n.left.eval(in)
		if err != nil {
			return nil, err
		}
		b, err := n.right.eval(in)
		if err != nil {
			return nil, err
		}
		if b.Sign() == 0 {
			return nil, fmt.Errorf("dynprice: division by zero")
		}
		return a.Quo(a, b), nil
	case 'm', 'n':
		a, err := n.left.eval(in)
		if err != nil {
			return nil, err
		}
		b, err := n.right.eval(in)
		if err != nil {
			return nil, err
		}
		cmp := a.Cmp(b)
		if (n.op == 'm' && cmp >= 0) || (n.op == 'n' && cmp <= 0) {
			return a, nil
		}
		return b, nil
	default:
		return nil, fmt.Errorf("dynprice: bad node")
	}
}

// roundHalfUp converts an exact rational to int64, halves away from zero.
func roundHalfUp(r *big.Rat) (int64, error) {
	num := r.Num()
	den := r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// |2*rem| >= den rounds the magnitude up.
	twice := new(big.Int).Abs(new(big.Int).Mul(rem, big.NewInt(2)))
	if twice.Cmp(new(big.Int).Abs(den)) >= 0 {
		if r.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("dynprice: result out of int64 range")
	}
	return q.Int64(), nil
}

// Eval parses and evaluates an expression over in (half-up int64 result).
func Eval(expression string, in Input) (int64, error) {
	n, err := Parse(expression)
	if err != nil {
		return 0, err
	}
	r, err := n.eval(in)
	if err != nil {
		return 0, err
	}
	return roundHalfUp(r)
}
