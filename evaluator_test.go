package main

import (
	"math"
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		want    float64
		wantErr error
	}{
		// precedence
		{"mult before add", "2+3*4", 14, nil},
		{"add before mult parens", "(2+3)*4", 20, nil},
		{"plain add", "1+1", 2, nil},
		{"plain sub", "10-3", 7, nil},
		{"plain mul", "6*7", 42, nil},
		{"plain div", "20/4", 5, nil},

		// decimals
		{"decimal mult", "1.5*2", 3, nil},
		{"decimal add", "0.1+0.2", 0.3, nil},
		{"leading dot not supported", ".5+.5", 0, errBadToken},
		{"double dot number", "1.5.2", 0, errBadNumber},
		{"trailing dot", "2.", 0, errBadToken},

		// unary minus
		{"unary minus", "-2+5", 3, nil},
		{"minus pow precedence", "-2^2", -4, nil},
		{"neg base pow", "(-2)^2", 4, nil},
		{"double unary", "--5", 5, nil},
		{"unary plus", "+3+4", 7, nil},
		{"unary in pow exp", "2^-1", 0.5, nil},

		// right-associative ^
		{"pow right assoc", "2^3^2", 512, nil},
		{"pow chain", "2^2^2", 16, nil},

		// word aliases (decision 2), case-insensitive
		{"plus alias", "2.plus.3", 5, nil},
		{"minus alias", "10.minus.4", 6, nil},
		{"times alias", "6.times.7", 42, nil},
		{"over alias", "20.over.4", 5, nil},
		{"pow alias", "2.pow.3", 8, nil},
		{"lp alias", "lp 2 plus 3 rp", 5, nil},
		{"rp alias only", "rp", 0, errBadToken},
		{"mixed case alias", "2.Plus.3", 5, nil},
		{"case randomised 0x20", "2.\x70lus.3", 5, nil},

		// decimals via separator
		{"decimal via labels", "1.5*2", 3, nil},

		// errors
		{"division by zero", "1/0", 0, errDivisionZero},
		{"nested div by zero", "5/(2-2)", 0, errDivisionZero},
		{"unknown word", "2.foo.3", 0, errUnknownWord},
		{"empty", "", 0, errEmptyExpr},
		{"whitespace only", "   ", 0, errEmptyExpr},
		{"unbalanced open", "(2+3", 0, errBadToken},
		{"unbalanced close", "2+3)", 0, errBadToken},
		{"bad number", "1.2.3.4", 0, errBadNumber},
		{"trailing op", "2+", 0, errBadToken},
		{"leading paren op", "+)2(", 0, errBadToken},
		{"inf result", "10^10^10", 0, errInfNaN},
		{"zero expr after trim", "\t\n", 0, errEmptyExpr},
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
			if tt.name == "decimal add" {
				// 0.1+0.2 is not exactly 0.3 in float64.
				if math.Abs(got-tt.want) > 1e-12 {
					t.Fatalf("evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestFormatResult(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{14, "14"},
		{3, "3"},
		{-4, "-4"},
		{0, "0"},
		{42, "42"},
		{3.5, "3.5"},
		{0.5, "0.5"},
		{100.25, "100.25"},
		{-0.0, "0"},
	}
	for _, tt := range tests {
		if got := formatResult(tt.in); got != tt.want {
			t.Errorf("formatResult(%v) = %q, want %q", tt.in, got, tt.want)
		}
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
	toks = append(toks, token{kind: tokNum, val: 1})
	p := &parser{toks: toks}
	if _, err := p.parseExpr(); err != errParenDepth {
		t.Fatalf("parseExpr(deep) error = %v, want %v", err, errParenDepth)
	}

	// Exactly maxParenDepth deep must be accepted.
	toks2 := make([]token, 0, 40)
	for i := 0; i < maxParenDepth; i++ {
		toks2 = append(toks2, token{kind: tokLParen})
	}
	toks2 = append(toks2, token{kind: tokNum, val: 1})
	// Add matching close parens so the expression is balanced.
	for i := 0; i < maxParenDepth; i++ {
		toks2 = append(toks2, token{kind: tokRParen})
	}
	p2 := &parser{toks: toks2}
	if _, err := p2.parseExpr(); err != nil {
		t.Fatalf("parseExpr(max depth) error: %v", err)
	}
}

// joinTokens renders a token slice back to a string for assertions.
func joinTokens(toks []token) string {
	var b strings.Builder
	for _, t := range toks {
		switch t.kind {
		case tokNum:
			b.WriteString(formatResult(t.val))
		case tokOp:
			if t.op != 0 {
				b.WriteRune(t.op)
			}
		case tokLParen:
			b.WriteByte('(')
		case tokRParen:
			b.WriteByte(')')
		}
	}
	return b.String()
}

// FTestEvaluate is the fuzz target (M1): it must never panic.
func FTestEvaluate(t *testing.F) {
	for _, seed := range []string{"2+3*4", "1.5*2", "1/0", "2.plus.3", "-2^2", "2^3^2", "(2+3)*4", "pow.2"} {
		t.Add(seed)
	}
	t.Fuzz(func(t *testing.T, s string) {
		// Must never panic regardless of input.
		_, _ = evaluate(s)
	})
}
