// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. pow rounded correctly: the C library CPython calls rounds it
// correctly in practice, and Go's math.Pow does not always, so it is computed in
// extended precision and rounded once.

package glom

import (
	"math"
	"math/big"
)

const precBits = 160

var (
	bigLn2 = lnSeries(new(big.Float).SetPrec(precBits).SetInt64(2))
)

func bf(x float64) *big.Float { return new(big.Float).SetPrec(precBits).SetFloat64(x) }

// lnSeries is ln(x) for x in [0.5, 2] by the atanh series:
// ln(x) = 2 * sum z^(2k+1)/(2k+1) with z = (x-1)/(x+1).
func lnSeries(x *big.Float) *big.Float {
	one := new(big.Float).SetPrec(precBits).SetInt64(1)
	num := new(big.Float).SetPrec(precBits).Sub(x, one)
	den := new(big.Float).SetPrec(precBits).Add(x, one)
	z := new(big.Float).SetPrec(precBits).Quo(num, den)
	z2 := new(big.Float).SetPrec(precBits).Mul(z, z)
	sum := new(big.Float).SetPrec(precBits)
	term := new(big.Float).SetPrec(precBits).Set(z)
	for k := 0; k < 400; k++ {
		t := new(big.Float).SetPrec(precBits).Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(2*k+1)))
		if t.Sign() == 0 {
			break
		}
		sum.Add(sum, t)
		if e := t.MantExp(nil) - sum.MantExp(nil); e < -precBits-4 {
			break
		}
		term.Mul(term, z2)
	}
	return sum.Mul(sum, new(big.Float).SetPrec(precBits).SetInt64(2))
}

// bigLnOf is ln(x) for x > 0.
func bigLnOf(x *big.Float) *big.Float {
	m := new(big.Float).SetPrec(precBits)
	e := x.MantExp(m) // x = m * 2^e, m in [0.5, 1)
	r := lnSeries(m)
	if e != 0 {
		r.Add(r, new(big.Float).SetPrec(precBits).Mul(bigLn2, new(big.Float).SetPrec(precBits).SetInt64(int64(e))))
	}
	return r
}

// bigExpOf is e**t, rounded to a float64 (an overflow gives +Inf).
func bigExpOf(t *big.Float) float64 {
	if t.Cmp(bf(710)) > 0 {
		return math.Inf(1)
	}
	if t.Cmp(bf(-746)) < 0 {
		return 0
	}
	kf := new(big.Float).SetPrec(precBits).Quo(t, bigLn2)
	k, _ := kf.Int64()
	r := new(big.Float).SetPrec(precBits).Sub(t, new(big.Float).SetPrec(precBits).Mul(bigLn2, new(big.Float).SetPrec(precBits).SetInt64(k)))
	// exp(r) with |r| < 1 by Taylor series on r/2^16, then squared 16 times.
	const halvings = 16
	r.SetMantExp(r, -halvings)
	sum := new(big.Float).SetPrec(precBits).SetInt64(1)
	term := new(big.Float).SetPrec(precBits).SetInt64(1)
	for n := 1; n < 60; n++ {
		term.Mul(term, r)
		term.Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(n)))
		if term.Sign() == 0 {
			break
		}
		sum.Add(sum, term)
	}
	for i := 0; i < halvings; i++ {
		sum.Mul(sum, sum)
	}
	sum.SetMantExp(sum, int(k))
	f, _ := sum.Float64()
	return f
}

// precisePow is pow(x, y) for x > 0 and finite y, correctly rounded.
func precisePow(x, y float64) float64 {
	if x == 1 || y == 0 {
		return 1
	}
	t := bigLnOf(bf(x))
	t.Mul(t, bf(y))
	return bigExpOf(t)
}

func finite(x float64) bool { return !math.IsInf(x, 0) && !math.IsNaN(x) }
