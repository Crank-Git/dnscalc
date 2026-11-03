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
// non-integer exponent is e^(exp·ln base) at full precision (bigmath.go).

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
		if !ok || (op != '+' && op != '-') {
			break
		}
		p.advance()
		rhs, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		if op == '+' {
			v.Add(v, rhs)
		} else {
			v.Sub(v, rhs)
		}
		if err := checkRange(v); err != nil {
			return nil, err
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
		if err := checkRange(v); err != nil {
			return nil, err
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
		return powBig(base, exp)
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

// checkRange rejects a value outside ±2^maxMagnitudeBits as soon as an
// operation produces it. Keeping every intermediate in range bounds the cost
// of the next operation and of formatting, and means no value ever overflows
// to ±Inf (big.Float panics on undefined operations such as 0*Inf).
//
// A value v = m·2^e with 0.5 <= |m| < 1 is in range when
// 2^-maxMagnitudeBits <= |v| < 2^maxMagnitudeBits, i.e.
// -maxMagnitudeBits < e <= maxMagnitudeBits.
func checkRange(v *big.Float) error {
	if v.IsInf() {
		return errInfNaN
	}
	if v.Sign() == 0 {
		return nil
	}
	switch e := v.MantExp(nil); {
	case e > maxMagnitudeBits:
		return errResultTooLarge
	case e <= -maxMagnitudeBits:
		return errResultTooSmall
	}
	return nil
}

// powBig computes base^exp at fixed precision. An integer exponent uses
// binary exponentiation (exact for powers of small integers, e.g. 2^80000000
// is one significant bit); a non-integer exponent is e^(exp·ln base),
// computed at workPrec and rounded to prec.
//
// Undefined cases are errors rather than NaN: a zero base with a negative
// exponent is a division by zero, and a negative base with a non-integer
// exponent has no real result.
//
// Bases 0 and ±1 are answered directly, whatever the exponent. For any other
// base the size of the result is estimated first, and a result far outside
// the magnitude range is rejected without computing it.
func powBig(base, exp *big.Float) (*big.Float, error) {
	return powBigLn(base, exp, nil)
}

// powBigLn is powBig with a cache for ln base: if lnBase is non-nil, a
// non-integer power uses *lnBase, computing and storing it on first use.
// tetration raises one base to thousands of exponents and computes ln only
// once.
func powBigLn(base, exp *big.Float, lnBase **big.Float) (*big.Float, error) {
	if base.Sign() == 0 {
		switch exp.Sign() {
		case -1:
			return nil, errDivisionZero
		case 0:
			return newBig().SetInt64(1), nil
		}
		return newBig(), nil
	}
	if base.Sign() < 0 && !exp.IsInt() {
		return nil, errNotReal
	}
	one := newBig().SetInt64(1)
	if newBig().Abs(base).Cmp(one) == 0 {
		if base.Sign() < 0 && isOdd(exp) {
			return newBig().SetInt64(-1), nil
		}
		return one, nil
	}
	if err := estimatePow(base, exp); err != nil {
		return nil, err
	}
	if exp.IsInt() {
		ei, _ := exp.Int(nil)
		if ei.Sign() >= 0 {
			r := powIntExp(base, ei)
			return r, checkRange(r)
		}
		// Negative integer exponent: 1 / base^|exp|.
		inv := powIntExp(base, new(big.Int).Neg(ei))
		r := one.Quo(one, inv)
		return r, checkRange(r)
	}
	// Non-integer exponent (base > 0). estimatePow bounded |exp·ln base|.
	var ln *big.Float
	switch {
	case lnBase == nil:
		ln = bigLn(base)
	case *lnBase == nil:
		ln = bigLn(base)
		*lnBase = ln
	default:
		ln = *lnBase
	}
	v := newBig().Set(bigExp(newWork().Mul(exp, ln)))
	return v, checkRange(v)
}

// estimatePow rejects base^exp when log2|base^exp| = exp·log2|base| is far
// outside ±maxMagnitudeBits. The estimate is float64 and only needs to be
// within a factor of two: anything it lets through is checked exactly by
// checkRange after computing. base must not be 0 or ±1.
//
// Because |log2|base|| >= ~2^-1024 for any 1024-bit base other than ±1, a
// passing exponent has at most ~1050 bits, which bounds the binary
// exponentiation loop.
func estimatePow(base, exp *big.Float) error {
	mant := newBig()
	e2 := base.MantExp(mant)
	m, _ := mant.Abs(mant).Float64()
	lg := float64(e2) + math.Log2(m)
	if lg == 0 {
		// |base| is within float64 rounding of 1 but not 1: keep the sign
		// of log2|base| so a huge exponent still estimates as huge.
		lg = 0x1p-1030
		if newBig().Abs(base).Cmp(newBig().SetInt64(1)) < 0 {
			lg = -lg
		}
	}
	x, _ := exp.Float64() // ±Inf for a huge exponent; lg != 0, so no NaN
	switch est := x * lg; {
	case est > 2*maxMagnitudeBits:
		return errResultTooLarge
	case est < -2*maxMagnitudeBits:
		return errResultTooSmall
	}
	return nil
}

// isOdd reports whether the integer x is odd.
func isOdd(x *big.Float) bool {
	i, _ := x.Int(nil)
	return i.Bit(0) == 1
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

// tetration computes a tet b (M4a, DECISIONS.md): x starts at 1 and is
// replaced b times by a^x, using powBig, so every step is subject to the
// magnitude range and a step that would leave it fails before it is computed.
// The height b must be a non-negative integer.
//
// A tower can stop early, for any height:
//   - a step that leaves x unchanged has reached a fixed point, and every
//     later step gives the same x (1 tet (10^300) = 1);
//   - a step that returns x to its value two steps back has entered a
//     2-cycle, and the answer is whichever of the two values the parity of
//     the remaining height lands on (0 tet n alternates 1, 0, 1, ...).
//
// A tower that has done none of these after maxTetSteps steps is
// errTetSteps: bases near e^(1/e) or e^-e settle (or blow up) too slowly to
// follow.
func tetration(a, b *big.Float) (*big.Float, error) {
	if !b.IsInt() || b.Sign() < 0 {
		return nil, errTetHeight
	}
	h, _ := b.Int(nil)
	x := newBig().SetInt64(1) // x = a tet i
	var prev *big.Float       // a tet (i-1)
	var lnA *big.Float        // ln a, computed on first use
	for i := int64(0); ; i++ {
		if h.Cmp(big.NewInt(i)) == 0 {
			return x, nil
		}
		if i == maxTetSteps {
			return nil, errTetSteps
		}
		next, err := powBigLn(a, x, &lnA)
		if err != nil {
			return nil, err
		}
		if next.Cmp(x) == 0 {
			return x, nil
		}
		if prev != nil && next.Cmp(prev) == 0 {
			// a tet (i+1) == a tet (i-1): from i-1 on the values alternate
			// next, x, next, ... so a tet h is next when h-(i+1) is even.
			if new(big.Int).Sub(h, big.NewInt(i+1)).Bit(0) == 0 {
				return next, nil
			}
			return x, nil
		}
		prev, x = x, next
	}
}
