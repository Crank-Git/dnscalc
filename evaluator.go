package main

import (
	"errors"
	"math"
	"strconv"
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
// Tokens: number (float64), operator (+ - * / ^), paren ( ) and word
// alias. Unknown words are an error.

var (
	errTokenLimit   = errors.New("too many tokens (max 64)")
	errParenDepth   = errors.New("parenthesis depth exceeds 32")
	errDivisionZero = errors.New("division by zero")
	errBadNumber    = errors.New("bad number")
	errBadToken     = errors.New("unexpected token")
	errEmptyExpr    = errors.New("empty expression")
	errInfNaN       = errors.New("result is not a finite number")
	errUnknownWord  = errors.New("unknown word")
)

// Limits from decision 4.
const (
	maxTokens     = 64
	maxParenDepth = 32
)

// evaluate parses and evaluates a math expression over float64.
// It supports + - * / ^ ( ), unary minus, and the word aliases
// plus minus times over pow lp rp (case-insensitive).
func evaluate(expr string) (float64, error) {
	if strings.TrimSpace(expr) == "" {
		return 0, errEmptyExpr
	}

	toks, err := tokenize(expr)
	if err != nil {
		return 0, err
	}
	toks, err = resolve(toks)
	if err != nil {
		return 0, err
	}
	if len(toks) > maxTokens {
		return 0, errTokenLimit
	}

	p := &parser{toks: toks}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.toks) {
		return 0, errBadToken
	}
	if !isFinite(v) {
		return 0, errInfNaN
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
	val  float64 // for tokNum
	op   rune    // for tokOp
	word string  // for word aliases (resolved by parser)
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

// readNumber reads a numeric literal starting at i. It accepts an optional
// leading sign, digits, and a single fractional part. Returns the parsed
// value and the number of bytes consumed.
func readNumber(s string, i int) (float64, int, error) {
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
		return 0, 0, errBadNumber
	}
	// Fractional part: a '.' followed by at least one digit.
	if j+1 < n && s[j] == '.' && isDigit(s[j+1]) {
		j++
		j++
		for j < n && isDigit(s[j]) {
			j++
		}
	}
	v, err := strconv.ParseFloat(s[i:j], 64)
	if err != nil {
		return 0, 0, errBadNumber
	}
	return v, j - i, nil
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
// without a decimal point ("14", "-4"); everything else uses the shortest
// round-tripping representation (strconv 'g', precision -1).
func formatResult(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func isFinite(f float64) bool {
	return f == f && !math.IsInf(f, 0) && !math.IsNaN(f)
}
