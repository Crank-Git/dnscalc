package main

// Recursive-descent parser over *big.Float (M4).
//
// Grammar (precedence, lowest to highest):
//
//	expr   := term   (('+' | '-') term)*
//	term   := power  (('*' | '/') power)*
//	power  := tet    ('^' power)?          // right-associative
//	tet    := unary  ('tet' tet)?          // right-associative, binds tighter than '^'
//	unary  := ('+' | '-') power | primary // unary binds looser than tet
//	primary := number | '(' expr ')'
//
// Tetration (M4a) binds tighter than '^', which binds tighter than unary
// minus, so "-2 tet 2" parses as -(2 tet 2) = -4. Tetration is
// right-associative, so "2 tet 2 tet 2" parses as 2 tet (2 tet 2) =
// 2 tet 4 = 65536.
//
// Powers are computed by powBig: an integer exponent uses binary
// exponentiation (exact for powers of small integers, e.g. 2^80000000); a
// non-integer exponent falls back to float64 math.Pow widened to *big.Float.

import (
	"math"
	"math/big"
)

type parser struct {
	toks  []token
	pos   int
	depth int
}

// tetOp is the internal rune for tetration (M4a), produced by resolving the
// "tet" word alias. It is not a binary operator, so binaryOp ignores it.
const tetOp rune = 'ᛏ'

// resolve converts word-alias tokens into their canonical operator/paren
// tokens. Unknown words are an error.
func resolve(toks []token) ([]token, error) {
	out := make([]token, 0, len(toks))
	for _, t := range toks {
		if t.kind != tokOp || t.word == "" {
			out = append(out, t)
			continue
		}
		switch t.word {
		case "plus":
			out = append(out, token{kind: tokOp, op: '+'})
		case "minus":
			out = append(out, token{kind: tokOp, op: '-'})
		case "times":
			out = append(out, token{kind: tokOp, op: '*'})
		case "over":
			out = append(out, token{kind: tokOp, op: '/'})
		case "pow":
			out = append(out, token{kind: tokOp, op: '^'})
		case "tet":
			out = append(out, token{kind: tokOp, op: tetOp})
		case "lp":
			out = append(out, token{kind: tokLParen})
		case "rp":
			out = append(out, token{kind: tokRParen})
		default:
			return nil, errUnknownWord
		}
	}
	return out, nil
}

func (p *parser) cur() token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token{kind: tokRParen} // EOF sentinel
}

func (p *parser) advance() token {
	t := p.toks[p.pos]
	p.pos++
	return t
}

// binaryOp reports whether t is a binary operator and returns it.
func (p *parser) binaryOp(t token) (rune, bool) {
	if t.kind == tokOp && t.op != 0 {
		switch t.op {
		case '+', '-', '*', '/', '^':
			return t.op, true
		}
	}
	return 0, false
}

func (p *parser) parseExpr() (*big.Float, error) {
	v, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		op, ok := p.binaryOp(t)
		if !ok {
			break
		}
		p.advance()
		rhs, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		switch op {
		case '+':
			v.Add(v, rhs)
		case '-':
			v.Sub(v, rhs)
		case '*':
			v.Mul(v, rhs)
		case '/':
			if rhs.Sign() == 0 {
				return nil, errDivisionZero
			}
			v.Quo(v, rhs)
		case '^':
			v.Set(powBig(v, rhs))
		}
	}
	return v, nil
}

func (p *parser) parseTerm() (*big.Float, error) {
	v, err := p.parsePower()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		op, ok := p.binaryOp(t)
		if !ok || (op != '*' && op != '/') {
			break
		}
		p.advance()
		rhs, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		switch op {
		case '*':
			v.Mul(v, rhs)
		case '/':
			if rhs.Sign() == 0 {
				return nil, errDivisionZero
			}
			v.Quo(v, rhs)
		}
	}
	return v, nil
}

func (p *parser) parsePower() (*big.Float, error) {
	base, err := p.parseTet()
	if err != nil {
		return nil, err
	}
	t := p.cur()
	op, ok := p.binaryOp(t)
	if ok && op == '^' {
		p.advance()
		exp, err := p.parsePower() // right-associative
		if err != nil {
			return nil, err
		}
		return powBig(base, exp), nil
	}
	return base, nil
}

// parseTet implements tetration (M4a): a tet b is iterated exponentiation,
// right-associative. It parses a unary base, and if the next token is tet,
// parses the height with a recursive parseTet (which gives right-associativity)
// and evaluates the tower via tetration.
func (p *parser) parseTet() (*big.Float, error) {
	base, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	t := p.cur()
	if t.kind == tokOp && t.op == tetOp {
		p.advance()
		height, err := p.parseTet() // right-associative
		if err != nil {
			return nil, err
		}
		return tetration(base, height)
	}
	return base, nil
}

func (p *parser) parseUnary() (*big.Float, error) {
	t := p.cur()
	op, ok := p.binaryOp(t)
	if ok && (op == '+' || op == '-') {
		p.advance()
		// Recurse into parsePower (not parseUnary) so that unary minus
		// binds looser than '^': -2^2 = -(2^2) = -4.
		v, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		if op == '-' {
			return newBig().Neg(v), nil
		}
		return v, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*big.Float, error) {
	t := p.cur()
	if t.kind == tokRParen {
		return nil, errBadToken
	}
	switch t.kind {
	case tokNum:
		p.advance()
		return t.val, nil
	case tokLParen:
		p.advance()
		p.depth++
		if p.depth > maxParenDepth {
			return nil, errParenDepth
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		// p.cur() returns an EOF sentinel of kind tokRParen, so guard the
		// index explicitly to catch an unbalanced '('.
		if p.pos >= len(p.toks) || p.cur().kind != tokRParen {
			return nil, errBadToken
		}
		p.advance()
		p.depth--
		return v, nil
	default:
		return nil, errBadToken
	}
}

// powBig computes base^exp at fixed precision. An integer exponent uses
// binary exponentiation (exact for powers of small integers, e.g. 2^80000000
// is one significant bit); a non-integer exponent falls back to float64
// math.Pow widened to *big.Float.
func powBig(base, exp *big.Float) *big.Float {
	if exp.IsInt() {
		ei, _ := exp.Int(nil)
		if ei.Sign() >= 0 {
			return powIntExp(base, ei)
		}
		// Negative integer exponent: 1 / base^|exp|.
		inv := powIntExp(base, new(big.Int).Neg(ei))
		one := newBig().SetInt64(1)
		return one.Quo(one, inv)
	}
	// Non-integer exponent: float64 precision, widened to *big.Float.
	b, _ := base.Float64()
	e, _ := exp.Float64()
	return newBig().SetFloat64(math.Pow(b, e))
}

// powIntExp computes base^exp for a non-negative integer exp via binary
// exponentiation. Powers of small integers stay exact because each squaring
// of a one-bit mantissa remains a one-bit mantissa.
func powIntExp(base *big.Float, exp *big.Int) *big.Float {
	result := newBig().SetInt64(1)
	b := newBig().Set(base)
	for i := 0; i < exp.BitLen(); i++ {
		if exp.Bit(i) == 1 {
			result.Mul(result, b)
		}
		b.Mul(b, b)
	}
	return result
}

// tetration computes a tet b (M4a) by iterated power: result starts at 1 and
// is replaced b times by a ^ result, using powBig. The height b must be a
// non-negative integer. Before each power step the running result is checked
// against the base-independent too-large bound (200000): if it already exceeds
// the bound, errResultTooLarge is returned immediately without computing the
// power, so an exploding tower never runs to completion.
func tetration(a, b *big.Float) (*big.Float, error) {
	if !b.IsInt() || b.Sign() < 0 {
		return nil, errTetHeight
	}
	bi, _ := b.Int(nil)
	result := newBig().SetInt64(1)
	one := new(big.Int).SetInt64(1)
	for i := new(big.Int); i.Cmp(bi) < 0; i.Add(i, one) {
		if result.Cmp(newBig().SetInt64(tetThreshold)) > 0 {
			return nil, errResultTooLarge
		}
		result = powBig(a, result)
	}
	return result, nil
}
