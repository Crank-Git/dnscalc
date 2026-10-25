package main

// Recursive-descent parser over float64 (decision 3).
//
// Grammar (precedence, lowest to highest):
//
//	expr   := term   (('+' | '-') term)*
//	term   := power  (('*' | '/') power)*
//	power  := unary  ('^' power)?          // right-associative
//	unary  := ('+' | '-') power | primary // unary binds looser than '^'
//	primary := number | '(' expr ')'
//
// Power binds tighter than unary minus, and is right-associative, so
// "-2^2" parses as -(2^2) = -4 and "2^3^2" parses as 2^(3^2) = 512.

import "math"

type parser struct {
	toks  []token
	pos   int
	depth int
}

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

func (p *parser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
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
			return 0, err
		}
		switch op {
		case '+':
			v += rhs
		case '-':
			v -= rhs
		case '*':
			v *= rhs
		case '/':
			if rhs == 0 {
				return 0, errDivisionZero
			}
			v /= rhs
		case '^':
			v = math.Pow(v, rhs)
		}
	}
	return v, nil
}

func (p *parser) parseTerm() (float64, error) {
	v, err := p.parsePower()
	if err != nil {
		return 0, err
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
			return 0, err
		}
		switch op {
		case '*':
			v *= rhs
		case '/':
			if rhs == 0 {
				return 0, errDivisionZero
			}
			v /= rhs
		}
	}
	return v, nil
}

func (p *parser) parsePower() (float64, error) {
	base, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	t := p.cur()
	op, ok := p.binaryOp(t)
	if ok && op == '^' {
		p.advance()
		exp, err := p.parsePower() // right-associative
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *parser) parseUnary() (float64, error) {
	t := p.cur()
	op, ok := p.binaryOp(t)
	if ok && (op == '+' || op == '-') {
		p.advance()
		// Recurse into parsePower (not parseUnary) so that unary minus
		// binds looser than '^': -2^2 = -(2^2) = -4.
		v, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		if op == '-' {
			return -v, nil
		}
		return v, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (float64, error) {
	t := p.cur()
	if t.kind == tokRParen {
		return 0, errBadToken
	}
	switch t.kind {
	case tokNum:
		p.advance()
		return t.val, nil
	case tokLParen:
		p.advance()
		p.depth++
		if p.depth > maxParenDepth {
			return 0, errParenDepth
		}
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		// p.cur() returns an EOF sentinel of kind tokRParen, so guard the
		// index explicitly to catch an unbalanced '('.
		if p.pos >= len(p.toks) || p.cur().kind != tokRParen {
			return 0, errBadToken
		}
		p.advance()
		p.depth--
		return v, nil
	default:
		return 0, errBadToken
	}
}
