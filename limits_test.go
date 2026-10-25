package main

import (
	"errors"
	"math/big"
	"testing"
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
