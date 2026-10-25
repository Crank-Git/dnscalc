package main

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

// mustFormat formats a result and fails the test if formatting errors (e.g.
// the output cap). It exists so TestEvaluate compares rendered strings.
func mustFormat(t *testing.T, v *big.Float) string {
	t.Helper()
	s, err := formatResult(v)
	if err != nil {
		t.Fatalf("formatResult(%v): %v", v, err)
	}
	return s
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		want    string
		wantErr error
	}{
		// precedence
		{"mult before add", "2+3*4", "14", nil},
		{"add before mult parens", "(2+3)*4", "20", nil},
		{"plain add", "1+1", "2", nil},
		{"plain sub", "10-3", "7", nil},
		{"plain mul", "6*7", "42", nil},
		{"plain div", "20/4", "5", nil},

		// decimals
		{"decimal mult", "1.5*2", "3", nil},
		{"leading dot not supported", ".5+.5", "", errBadToken},
		{"double dot number", "1.5.2", "", errBadNumber},
		{"trailing dot", "2.", "", errBadToken},

		// unary minus
		{"unary minus", "-2+5", "3", nil},
		{"minus pow precedence", "-2^2", "-4", nil},
		{"neg base pow", "(-2)^2", "4", nil},
		{"double unary", "--5", "5", nil},
		{"unary plus", "+3+4", "7", nil},
		{"unary in pow exp", "2^-1", "0.5", nil},

		// right-associative ^
		{"pow right assoc", "2^3^2", "512", nil},
		{"pow chain", "2^2^2", "16", nil},

		// word aliases (decision 2), case-insensitive
		{"plus alias", "2.plus.3", "5", nil},
		{"minus alias", "10.minus.4", "6", nil},
		{"times alias", "6.times.7", "42", nil},
		{"over alias", "20.over.4", "5", nil},
		{"pow alias", "2.pow.3", "8", nil},
		{"lp alias", "lp 2 plus 3 rp", "5", nil},
		{"rp alias only", "rp", "", errBadToken},
		{"mixed case alias", "2.Plus.3", "5", nil},
		{"case randomised 0x20", "2.\x70lus.3", "5", nil},

		// decimals via separator
		{"decimal via labels", "1.5*2", "3", nil},

		// M4: big precision and the output cap
		{"power of two exact", "2^40", "1099511627776", nil},
		{"large exact power", "3^500", "36360291795869936842385267079543319118023385026001623040346035832580600191583895484198508262979388783308179702534403855752855931517013066142992430916562025780021771247847643450125342836565813209972590371590152578728008385990139795377610001", nil},
		{"inf result", "10^10^10", "", errInfNaN},

		// errors
		{"division by zero", "1/0", "", errDivisionZero},
		{"nested div by zero", "5/(2-2)", "", errDivisionZero},
		{"unknown word", "2.foo.3", "", errUnknownWord},
		{"empty", "", "", errEmptyExpr},
		{"whitespace only", "   ", "", errEmptyExpr},
		{"unbalanced open", "(2+3", "", errBadToken},
		{"unbalanced close", "2+3)", "", errBadToken},
		{"bad number", "1.2.3.4", "", errBadNumber},
		{"trailing op", "2+", "", errBadToken},
		{"leading paren op", "+)2(", "", errBadToken},
		{"zero expr after trim", "\t\n", "", errEmptyExpr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluate(tt.expr)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("evaluate(%q) = nil error, want %v", tt.expr, tt.wantErr)
				}
				if err != tt.wantErr {
					t.Fatalf("evaluate(%q) error = %v, want %v", tt.expr, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("evaluate(%q) unexpected error: %v", tt.expr, err)
			}
			want, _ := newBig().SetString(tt.want)
			if got.Cmp(want) != 0 {
				t.Fatalf("evaluate(%q) = %s, want %s", tt.expr, mustFormat(t, got), tt.want)
			}
		})
	}
}

func TestFormatResult(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"14", "14"},
		{"3", "3"},
		{"-4", "-4"},
		{"0", "0"},
		{"42", "42"},
		{"3.5", "3.5"},
		{"0.5", "0.5"},
		{"100.25", "100.25"},
		{"-0", "0"},
	}
	for _, tt := range tests {
		f, _ := newBig().SetString(tt.in)
		if got := mustFormat(t, f); got != tt.want {
			t.Errorf("formatResult(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestOutputCap verifies the M4 output cap end-to-end: evaluate() returns the
// value, and formatResult() rejects it when the decimal form exceeds
// maxResultDigits. 2^200000 (~60206 digits) is exact (one-bit mantissa) and
// computes in milliseconds via binary exponentiation, so this stays fast.
func TestOutputCap(t *testing.T) {
	v, err := evaluate("2^200000")
	if err != nil {
		t.Fatalf("evaluate(2^200000) error: %v", err)
	}
	_, err = formatResult(v)
	if !errors.Is(err, errResultTooLarge) {
		t.Fatalf("formatResult(2^200000) error = %v, want errResultTooLarge", err)
	}
}

// TestTokenizerDotRule pins the "." tokenizer rule (decision 2).
func TestTokenizerDotRule(t *testing.T) {
	// The dot rule is already exercised indirectly by TestEvaluate
	// (decimals vs. word aliases). Here we pin the token structure:
	// a dot between digits yields one number token; a dot between a
	// digit and a letter is a separator that joins two tokens.
	tests := []struct {
		expr       string
		numTokens  int
		firstIsNum bool
	}{
		{"1.5*2", 3, true},    // [1.5, *, 2]
		{"2.plus.3", 3, true}, // [2, plus, 3]
		{"1.5.2", 0, false},   // error: bad number
	}
	for _, tt := range tests {
		toks, err := tokenize(tt.expr)
		if tt.expr == "1.5.2" {
			if err == nil {
				t.Fatalf("tokenize(%q) = nil error, want bad number", tt.expr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("tokenize(%q) error: %v", tt.expr, err)
		}
		if len(toks) != tt.numTokens {
			t.Errorf("tokenize(%q) produced %d tokens, want %d", tt.expr, len(toks), tt.numTokens)
		}
	}
}

func TestMaxTokens(t *testing.T) {
	// "-1" repeated 32 times = 64 tokens (32 ones + 32 minuses), a valid
	// expression that must be accepted.
	okExpr := strings.Repeat("-1", 32)
	if _, err := evaluate(okExpr); err != nil {
		t.Fatalf("evaluate(64 tokens) error: %v", err)
	}
	// 33 repetitions = 66 tokens, over the limit.
	overExpr := strings.Repeat("-1", 33)
	if _, err := evaluate(overExpr); err != errTokenLimit {
		t.Fatalf("evaluate(66 tokens) error = %v, want %v", err, errTokenLimit)
	}
}

// TestParenDepthGuard exercises the paren-depth limit directly on the
// parser, bypassing tokenize (a 33-deep expression needs >64 tokens, so
// the token limit would fire first through evaluate).
func TestParenDepthGuard(t *testing.T) {
	toks := make([]token, 0, 40)
	for i := 0; i < maxParenDepth+1; i++ {
		toks = append(toks, token{kind: tokLParen})
	}
	toks = append(toks, token{kind: tokNum, val: newBig().SetInt64(1)})
	p := &parser{toks: toks}
	if _, err := p.parseExpr(); err != errParenDepth {
		t.Fatalf("parseExpr(deep) error = %v, want %v", err, errParenDepth)
	}

	// Exactly maxParenDepth deep must be accepted.
	toks2 := make([]token, 0, 40)
	for i := 0; i < maxParenDepth; i++ {
		toks2 = append(toks2, token{kind: tokLParen})
	}
	toks2 = append(toks2, token{kind: tokNum, val: newBig().SetInt64(1)})
	// Add matching close parens so the expression is balanced.
	for i := 0; i < maxParenDepth; i++ {
		toks2 = append(toks2, token{kind: tokRParen})
	}
	p2 := &parser{toks: toks2}
	if _, err := p2.parseExpr(); err != nil {
		t.Fatalf("parseExpr(max depth) error: %v", err)
	}
}

// FTestEvaluate is the fuzz target (M1): it must never panic.
func FTestEvaluate(t *testing.F) {
	for _, seed := range []string{"2+3*4", "1.5*2", "1/0", "2.plus.3", "-2^2", "2^3^2", "(2+3)*4", "pow.2"} {
		t.Add(seed)
	}
	t.Fuzz(func(t *testing.T, s string) {
		// Must never panic regardless of input.
		v, err := evaluate(s)
		if err == nil {
			_ = mustFormat(t, v)
		}
	})
}
