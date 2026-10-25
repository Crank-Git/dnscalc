package main

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Tokenizer rule (decision 2):
//
// The expression is the query name with the zone suffix stripped, so its
// labels are joined with ".". We must turn that back into a token stream.
//
//   - "." is a SEPARATOR that joins two *word* tokens (e.g. "2" "." "plus"
//     -> "2.plus"). It is NOT a separator between digits: "1" "." "5*2"
//     must become "1.5*2", i.e. a decimal point.
//
// Concretely, a "." is a decimal point only when the character before it
// AND the character after it are both digits; otherwise (when either
// neighbour is a letter) it is a separator that joins two tokens. So:
//
//	"1.5*2" -> ["1.5", "*", "2"]      (dot between digits -> decimal)
//	"2.plus.3" -> ["2", "plus", "3"]  (dot between digit and letter -> sep)
//
// Word aliases are case-insensitive and resolved after lowercasing.
// Resolvers may randomise case with 0x20 (per RFC 4343), so we lowercase
// the whole expression before tokenising.
//
// Tokens: number (*big.Float), operator (+ - * / ^), paren ( ) and word
// alias. Unknown words are an error.

// Value type (M4): a single *big.Float at fixed 1024-bit precision. All
// arithmetic and powers use it; there is no float64 path in the core.
const prec = 1024

// maxResultDigits is the output cap (M4): a decimal result longer than this
// is rejected instead of being emitted. The bound is set by the DNS message
// size itself: every DNS packet is length-prefixed to 16 bits, so the whole
// message (header + question + answer) must stay under 65535 bytes regardless
// of record type. We cap the result string at 60000 characters, leaving
// headroom for that overhead; anything larger (e.g. 2^80000000, ~24M digits)
// is reported as an error rather than producing an undeliverable packet.
const maxResultDigits = 60000

// tetThreshold is the base-independent too-large bound for tetration (M4a):
// before each iterated power step, an already-exceeding running result is
// rejected with errResultTooLarge instead of computing the next power.
const tetThreshold = 200000

var (
	errTokenLimit     = errors.New("too many tokens (max 64)")
	errParenDepth     = errors.New("parenthesis depth exceeds 32")
	errDivisionZero   = errors.New("division by zero")
	errBadNumber      = errors.New("bad number")
	errBadToken       = errors.New("unexpected token")
	errEmptyExpr      = errors.New("empty expression")
	errInfNaN         = errors.New("result is not a finite number")
	errUnknownWord    = errors.New("unknown word")
	errResultTooLarge = errors.New("result too large")
	errTetHeight      = errors.New("tetration height must be a non-negative integer")
)

// Limits from decision 4.
const (
	maxTokens     = 64
	maxParenDepth = 32
)

func newBig() *big.Float { return new(big.Float).SetPrec(prec) }

// evaluate parses and evaluates a math expression over *big.Float.
// It supports + - * / ^ ( ), unary minus, and the word aliases
// plus minus times over pow lp rp (case-insensitive).
func evaluate(expr string) (*big.Float, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errEmptyExpr
	}

	toks, err := tokenize(expr)
	if err != nil {
		return nil, err
	}
	toks, err = resolve(toks)
	if err != nil {
		return nil, err
	}
	if len(toks) > maxTokens {
		return nil, errTokenLimit
	}

	p := &parser{toks: toks}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, errBadToken
	}
	if v.IsInf() {
		return nil, errInfNaN
	}
	return v, nil
}

type tokenKind int

const (
	tokNum tokenKind = iota
	tokOp
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	val  *big.Float // for tokNum
	op   rune       // for tokOp
	word string     // for word aliases (resolved by parser)
}

func tokenize(expr string) ([]token, error) {
	s := strings.ToLower(expr)
	var toks []token
	i, n := 0, len(s)

	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '^':
			toks = append(toks, token{kind: tokOp, op: rune(c)})
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen})
			i++
		case isDigit(c):
			num, adv, err := readNumber(s, i)
			if err != nil {
				return nil, errBadNumber
			}
			toks = append(toks, token{kind: tokNum, val: num})
			i += adv
		case isWordChar(c):
			w, adv := readWord(s, i)
			toks = append(toks, token{kind: tokOp, word: w})
			i += adv
		case c == '.':
			switch {
			case isSepDot(s, i):
				// Separator between two word/number tokens: drop it.
				i++
			case isDecimalDot(s, i):
				// A number that begins with a dot, e.g. ".5".
				num, adv, err := readNumber(s, i)
				if err != nil {
					return nil, errBadNumber
				}
				toks = append(toks, token{kind: tokNum, val: num})
				i += adv
			default:
				return nil, errBadToken
			}
		default:
			return nil, errBadToken
		}
	}
	return toks, nil
}

// isSepDot reports whether the '.' at index i joins two word/number tokens
// (a separator) rather than forming a decimal point. It is a separator when
// both neighbours are alphanumeric and at least one of them is a letter.
func isSepDot(s string, i int) bool {
	if i == 0 || i+1 >= len(s) {
		return false
	}
	a, b := s[i-1], s[i+1]
	alNum := isWordOrDigit(a) && isWordOrDigit(b)
	letOrDig := isDigit(a) && isDigit(b)
	return alNum && !letOrDig
}

// isDecimalDot reports whether the '.' at index i is a decimal point, i.e.
// both neighbours are digits.
func isDecimalDot(s string, i int) bool {
	if i == 0 || i+1 >= len(s) {
		return false
	}
	return isDigit(s[i-1]) && isDigit(s[i+1])
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// isWordOrDigit reports whether c is a letter, digit, or underscore.
func isWordOrDigit(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c) || c == '_'
}

// isWordChar reports whether c can begin a word alias.
func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// readNumber reads a numeric literal starting at i and returns it as a
// *big.Float. The literal is parsed directly from source (not via float64)
// so decimal values keep full 1024-bit precision.
func readNumber(s string, i int) (*big.Float, int, error) {
	n := len(s)
	j := i
	if j < n && (s[j] == '+' || s[j] == '-') {
		j++
	}
	start := j
	for j < n && isDigit(s[j]) {
		j++
	}
	if j == start {
		return nil, 0, errBadNumber
	}
	// Fractional part: a '.' followed by at least one digit.
	if j+1 < n && s[j] == '.' && isDigit(s[j+1]) {
		j++
		j++
		for j < n && isDigit(s[j]) {
			j++
		}
	}
	f, ok := newBig().SetString(s[i:j])
	if !ok {
		return nil, 0, errBadNumber
	}
	return f, j - i, nil
}

// readWord reads a word alias starting at i and returns it and the bytes
// consumed.
func readWord(s string, i int) (string, int) {
	n := len(s)
	j := i
	for j < n && isWordChar(s[j]) {
		j++
	}
	return s[i:j], j - i
}

// formatResult renders a result per decision 5: integral values print
// without a decimal point ("14", "-4") via (*big.Float).Int(); everything
// else uses the shortest round-tripping representation (big.Float 'g', -1).
//
// It also enforces the M4 output cap: a decimal result longer than
// maxResultDigits characters returns errResultTooLarge instead of the number.
func formatResult(f *big.Float) (string, error) {
	var s string
	if f.IsInt() {
		i, _ := f.Int(new(big.Int))
		s = i.String()
	} else {
		s = f.Text('g', -1)
	}
	if len(s) > maxResultDigits {
		return "", fmt.Errorf("%w (%d digits)", errResultTooLarge, len(s))
	}
	return s, nil
}
