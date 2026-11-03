package main

// Natural logarithm and exponential over *big.Float, for non-integer powers:
// base^exp = e^(exp·ln base). Both work at workPrec (prec plus guard bits)
// and return values at workPrec; powBig rounds the final result to prec.

import (
	"math"
	"math/big"
)

// workPrec is the working precision for bigLn and bigExp. The 64 guard bits
// absorb the error of argument reduction (up to ~22 bits for the largest
// exponents powBig lets through) with room to spare.
const workPrec = prec + 64

func newWork() *big.Float { return new(big.Float).SetPrec(workPrec) }

// ln2 is ln 2 at workPrec: ln 2 = 2·atanh(1/3).
var ln2 = func() *big.Float {
	third := newWork().Quo(newWork().SetInt64(1), newWork().SetInt64(3))
	return atanh2(third)
}()

// atanh2 returns 2·atanh(z) = ln((1+z)/(1-z)) for |z| < 1 by its Taylor
// series 2·(z + z^3/3 + z^5/5 + ...). It converges fast only for small |z|.
func atanh2(z *big.Float) *big.Float {
	z2 := newWork().Mul(z, z)
	term := newWork().Set(z)
	sum := newWork().Set(z)
	t := newWork()
	k := newWork()
	for n := int64(3); ; n += 2 {
		term.Mul(term, z2)
		t.Quo(term, k.SetInt64(n))
		if t.Sign() == 0 || t.MantExp(nil) < sum.MantExp(nil)-workPrec {
			break
		}
		sum.Add(sum, t)
	}
	return sum.SetMantExp(sum, 1)
}

// bigLn returns ln x for x > 0.
//
// x = m·2^e with m in [1/√2, √2), so ln x = ln m + e·ln 2. If m is not
// already close to 1 it is pulled toward 1 by square roots
// (ln m = 2^k·ln m^(1/2^k)) until |m-1| < 2^-8, and then
// ln m = 2·atanh((m-1)/(m+1)). A value already near 1 skips the square
// roots, so m-1 is exact and ln(1+tiny) keeps full relative precision.
func bigLn(x *big.Float) *big.Float {
	m := newWork()
	e := x.MantExp(m)   // m in [0.5, 1)
	m.SetPrec(workPrec) // MantExp copies x's precision into m
	if m.Cmp(newWork().SetFloat64(math.Sqrt2/2)) < 0 {
		m.SetMantExp(m, 1)
		e--
	}
	one := newWork().SetInt64(1)
	d := newWork()
	k := 0
	for {
		d.Sub(m, one)
		if d.Sign() == 0 || d.MantExp(nil) <= -8 { // |m-1| < 2^-8
			break
		}
		m.Sqrt(m)
		k++
	}
	z := newWork().Quo(newWork().Sub(m, one), newWork().Add(m, one))
	r := atanh2(z)
	r.SetMantExp(r, k)
	return r.Add(r, newWork().Mul(newWork().SetInt64(int64(e)), ln2))
}

// bigExp returns e^r. powBig only calls it with |r| < ~2^22.
//
// r = n·ln 2 + s with |s| <= ln2/2, so e^r = 2^n·e^s; e^s is computed as
// (e^(s/2^k))^(2^k), where the Taylor series for the tiny s/2^k converges in
// a few dozen terms.
func bigExp(r *big.Float) *big.Float {
	q, _ := newWork().Quo(r, ln2).Float64()
	n := int64(math.Round(q))
	s := newWork().Sub(r, newWork().Mul(newWork().SetInt64(n), ln2))

	const k = 16
	s.SetMantExp(s, -k)
	sum := newWork().SetInt64(1)
	term := newWork().SetInt64(1)
	d := newWork()
	for i := int64(1); ; i++ {
		term.Mul(term, s)
		term.Quo(term, d.SetInt64(i))
		if term.Sign() == 0 || term.MantExp(nil) < -workPrec-2 {
			break
		}
		sum.Add(sum, term)
	}
	for i := 0; i < k; i++ {
		sum.Mul(sum, sum)
	}
	return sum.SetMantExp(sum, int(n))
}
