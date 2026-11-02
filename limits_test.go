package main

import (
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

// evalInt evaluates expr and returns its exact integer value, failing if the
// result is non-integral or errors. Used to compare the evaluator against
// known-good big.Int values.
func evalInt(t *testing.T, expr string) *big.Int {
	t.Helper()
	v, err := evaluate(expr)
	if err != nil {
		t.Fatalf("evaluate(%q): %v", expr, err)
	}
	if !v.IsInt() {
		t.Fatalf("evaluate(%q) = %v, want an integer", expr, v)
	}
	i, _ := v.Int(new(big.Int))
	return i
}

// evalFloat evaluates expr and returns the raw *big.Float result, failing on
// error or nil. Used where formatResult wants a *big.Float (not an *big.Int).
func evalFloat(t *testing.T, expr string) *big.Float {
	t.Helper()
	v, err := evaluate(expr)
	if err != nil {
		t.Fatalf("evaluate(%q): %v", expr, err)
	}
	if v == nil {
		t.Fatalf("evaluate(%q) returned nil", expr)
	}
	return v
}

// TestOutputCapBoundary pins the M4 output cap (maxResultDigits = 60000).
// Powers of two are exact at any magnitude (one-bit mantissa), so 2^199313
// is the smallest power of two with 60000 digits and 2^199316 the smallest
// with 60001 -- a real boundary, not a rounding artifact.
func TestOutputCapBoundary(t *testing.T) {
	// 60000 digits: emitted as-is.
	s, err := formatResult(evalFloat(t, "2^199313"))
	if err != nil {
		t.Fatalf("formatResult(2^199313): %v", err)
	}
	if len(s) != maxResultDigits {
		t.Fatalf("2^199313 = %d digits, want %d", len(s), maxResultDigits)
	}

	// 60001 digits: rejected by the cap.
	if _, err := formatResult(evalFloat(t, "2^199316")); !errors.Is(err, errResultTooLarge) {
		t.Fatalf("formatResult(2^199316) error = %v, want errResultTooLarge", err)
	}
}

// bigPow computes base^exp by repeated multiplication, avoiding big.Int.Pow
// (not present in this toolchain's math/big).
func bigPow(t *testing.T, base, exp int64) *big.Int {
	t.Helper()
	r := new(big.Int).SetInt64(1)
	b := new(big.Int).SetInt64(base)
	for e := exp; e > 0; e-- {
		r.Mul(r, b)
	}
	return r
}

// TestPrecisionBitLimit documents the other ceiling: the core is a single
// *big.Float at 1024-bit precision (evaluator.go prec). A result whose true
// value needs more than 1024 significant bits is rounded, so it prints as a
// large integer whose leading ~309 digits are right and the rest are not.
// 3^640 is 1015 bits (exact); 3^648 is 1028 bits (rounded).
func TestPrecisionBitLimit(t *testing.T) {
	true640 := bigPow(t, 3, 640)
	if got := evalInt(t, "3^640"); got.Cmp(true640) != 0 {
		t.Fatalf("3^640 is inexact: evaluator %s vs true %s", got, true640)
	}

	true648 := bigPow(t, 3, 648)
	if got := evalInt(t, "3^648"); got.Cmp(true648) == 0 {
		t.Fatalf("3^648 (1028 bits) came out exact; 1024-bit precision should round it")
	}
	// The rounded value is still a large, finite integer well under the output
	// cap, so it prints in full -- just not correctly past ~309 digits.
	if s, err := formatResult(evalFloat(t, "3^648")); err != nil || len(s) != 310 {
		t.Fatalf("formatResult(3^648) = %q (%d digits), %v", s, len(s), err)
	}
}

// evalWithin evaluates and formats expr in a goroutine and fails the test if
// that takes longer than limit. It returns the formatted result or the first
// error.
func evalWithin(t *testing.T, expr string, limit time.Duration) (string, error) {
	t.Helper()
	type res struct {
		s   string
		err error
	}
	done := make(chan res, 1)
	go func() {
		v, err := evaluate(expr)
		if err != nil {
			done <- res{"", err}
			return
		}
		s, err := formatResult(v)
		done <- res{s, err}
	}()
	select {
	case r := <-done:
		return r.s, r.err
	case <-time.After(limit):
		t.Fatalf("evaluate+format(%q) still running after %s", expr, limit)
		return "", nil
	}
}

// costLimit is how long any single expression may take. The bound exists to
// stop pathological inputs (seconds, minutes, forever); it is generous so it
// holds under -race on a slow CI runner.
const costLimit = 2 * time.Second

// TestMagnitudeBound pins the ±2^maxMagnitudeBits range: anything outside it
// is an error, reported quickly, and anything inside it still works, even
// when an intermediate is far past the output cap.
func TestMagnitudeBound(t *testing.T) {
	tests := []struct {
		expr    string
		want    string // exact expected output when wantErr is nil
		wantErr error
	}{
		// Far out of range: rejected before computing.
		{"2^2^24", "", errResultTooLarge},
		{"2^2^2^30", "", errResultTooLarge},
		{"10^10^10", "", errResultTooLarge},
		{"9^9^9^9", "", errResultTooLarge},
		{"2^-2^24", "", errResultTooSmall},
		{"0.5^2^30", "", errResultTooSmall},
		{"(0-3)^(2^40)", "", errResultTooLarge},
		{"(0-3)^(0-2^40)", "", errResultTooSmall},
		{"1.5^(10^300)", "", errResultTooLarge},
		{"(1+2^-1000)^(2^3000)", "", errResultTooLarge},
		{"2^0.5^(0-2000)", "", errResultTooLarge},

		// Just outside / inside the bound.
		{"2^1048576", "", errResultTooLarge},
		{"2^-1048577", "", errResultTooSmall},
		{"2^1048575/2^1048570", "32", nil},
		{"2^-1048576*2^1048575", "0.5", nil},

		// Intermediates past the output cap are fine.
		{"2^1000000/2^999990", "1024", nil},
		{"(2^500000-2^500000)+7", "7", nil},
		{"2^-1000000*2^1000000", "1", nil},

		// Products of in-range values that leave the range.
		{"2^1000000*2^1000000", "", errResultTooLarge},
		{"2^-1000000/2^1000000", "", errResultTooSmall},
		{"2^-1000000*2^-1000000", "", errResultTooSmall},

		// Bases of magnitude 1 and 0 never blow up, whatever the exponent.
		{"1^(10^300)", "1", nil},
		{"1^(0-10^300)", "1", nil},
		{"(0-1)^(10^300)", "1", nil},
		{"(0-1)^(10^300+1)", "-1", nil},
		{"(0-1)^(0-(10^300+1))", "-1", nil},
		{"0^(10^300)", "0", nil},
		{"1^0.5", "1", nil},

		// Ordinary powers are untouched.
		{"2^10", "1024", nil},
		{"2^-10", "0.0009765625", nil},
		{"(0-2)^(0-3)", "-0.125", nil},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := evalWithin(t, tt.expr, costLimit)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("%s: err = %v, want %v", tt.expr, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tt.expr, err)
			}
			if got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

// TestFormatHugeIntegerFast checks that an integer obviously over the output
// cap is rejected without converting it to decimal, and that the error
// still names the digit count.
func TestFormatHugeIntegerFast(t *testing.T) {
	for _, expr := range []string{"2^1048575", "3^600000", "(0-7)^300001"} {
		start := time.Now()
		_, err := evalWithin(t, expr, costLimit)
		if !errors.Is(err, errResultTooLarge) {
			t.Fatalf("%s: err = %v, want errResultTooLarge", expr, err)
		}
		if !strings.Contains(err.Error(), "digits") {
			t.Errorf("%s: error %q does not report the digit count", expr, err)
		}
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Errorf("%s: rejecting took %s", expr, d)
		}
	}
}

// TestFormatTiny checks tiny results print quickly in scientific notation and
// parse back to (very nearly) the exact value. Before the scaled formatter,
// 2^-200000 took over a second and 2^-1048576 about thirty.
func TestFormatTiny(t *testing.T) {
	tests := []struct {
		expr      string
		exp2      int    // the exact value is mant * 2^exp2
		mant      string // decimal mantissa; "" means 1
		wantShape string // substring the output must contain
	}{
		{"2^-200000", -200000, "", "e-60206"},
		{"2^-1048576", -1048576, "", "e-315653"},
		{"0-2^-200000", -200000, "-1", "e-60206"},
		{"3*2^-5000", -5000, "3", "e-1505"},
		{"1/3*2^-300000", -300000, "", "e-90310"},
		{"2^-1200", -1200, "", "e-362"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			start := time.Now()
			got, err := evalWithin(t, tt.expr, costLimit)
			if err != nil {
				t.Fatalf("%s: %v", tt.expr, err)
			}
			if d := time.Since(start); d > 200*time.Millisecond {
				t.Errorf("%s: took %s", tt.expr, d)
			}
			if !strings.Contains(got, tt.wantShape) {
				t.Errorf("%s = %q, want it to contain %q", tt.expr, got, tt.wantShape)
			}
			if len(got) > 400 {
				t.Errorf("%s: %d chars, want a compact mantissa+exponent", tt.expr, len(got))
			}

			// Compare against the exact value at extra precision.
			want := newBig().SetPrec(prec+64).SetMantExp(newBig().SetPrec(prec+64).SetInt64(1), tt.exp2)
			if tt.mant != "" {
				m, _ := newBig().SetPrec(prec + 64).SetString(tt.mant)
				want.Mul(want, m)
			}
			if strings.HasPrefix(tt.expr, "1/3") {
				want.Quo(want, newBig().SetPrec(prec+64).SetInt64(3))
			}
			parsed, ok := newBig().SetPrec(prec + 64).SetString(got)
			if !ok {
				t.Fatalf("%s: output %q does not parse", tt.expr, got)
			}
			relErr := newBig().SetPrec(prec+64).Sub(parsed, want)
			relErr.Quo(relErr, want).Abs(relErr)
			if relErr.Cmp(newBig().SetMantExp(newBig().SetInt64(1), -990)) > 0 {
				t.Errorf("%s = %s, relative error %s, want < 2^-990", tt.expr, got, relErr.Text('g', 5))
			}
		})
	}
}

// TestFormatTinyMatchesExact checks the scaled formatter against big.Float's
// exact conversion where the latter is still affordable.
func TestFormatTinyMatchesExact(t *testing.T) {
	for _, e := range []int{-1101, -1500, -4000, -12000} {
		for _, m := range []int64{1, 3, 7, 12345} {
			f := newBig().SetMantExp(newBig().SetInt64(m), e)
			f.Quo(f, newBig().SetInt64(3)) // full-length mantissa
			got := formatTiny(f)
			exact := f.Text('g', -1)
			gp, _ := newBig().SetString(got)
			if gp.Cmp(f) != 0 {
				// Same value to within the last couple of ulps.
				d := newBig().Sub(gp, f)
				d.Quo(d, f).Abs(d)
				if d.Cmp(newBig().SetMantExp(newBig().SetInt64(1), -1018)) > 0 {
					t.Errorf("m=%d e=%d: formatTiny=%s exact=%s (rel diff %s)", m, e, got, exact, d.Text('g', 3))
				}
			}
			// Same decimal exponent as the exact conversion.
			if got[strings.LastIndexByte(got, 'e'):] != exact[strings.LastIndexByte(exact, 'e'):] {
				t.Errorf("m=%d e=%d: exponent of %s differs from exact %s", m, e, got[strings.LastIndexByte(got, 'e'):], exact[strings.LastIndexByte(exact, 'e'):])
			}
		}
	}
}
