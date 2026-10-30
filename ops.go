// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. Python's arithmetic and bitwise operators on the values
// glom's T expressions and reducers reach, with CPython 3.12's results and
// exception messages.

package glom

import (
	"math"
	"math/big"
	"strings"
)

func unsupported(op string, a, b Value) error {
	return newExc(excTypeError, mkStr("unsupported operand type(s) for "+op+": '"+typeName(a)+"' and '"+typeName(b)+"'"))
}

func zeroDiv(msg string) error { return newExc(excZeroDivision, mkStr(msg)) }

// toFloat converts an int, bool or float to a float as Python's mixed
// arithmetic does.
func toFloat(v Value) (float64, bool, error) {
	switch x := v.(type) {
	case *floatObj:
		return x.v, true, nil
	case *intObj, *boolObj:
		i, _ := asIntBig(v)
		f, err := bigToFloat(i)
		return f, true, err
	}
	return 0, false, nil
}

func bigToFloat(i *big.Int) (float64, error) {
	f, _ := new(big.Float).SetInt(i).Float64()
	if math.IsInf(f, 0) {
		return 0, newExc(excOverflowError, mkStr("int too large to convert to float"))
	}
	// big.Float.Float64 rounds to nearest even, as CPython's int.__float__.
	return f, nil
}

// binaryOp is a <op> b for "+", "-", "*", "/", "//", "%", "**", "&", "|", "^".
func binaryOp(op string, a, b Value) (Value, error) {
	_, aInt := a.(*intObj)
	_, aBool := a.(*boolObj)
	_, bInt := b.(*intObj)
	_, bBool := b.(*boolObj)
	if (aInt || aBool) && (bInt || bBool) {
		if aBool && bBool && (op == "&" || op == "|" || op == "^") {
			x, y := a.(*boolObj).v, b.(*boolObj).v
			switch op {
			case "&":
				return pyBool(x && y), nil
			case "|":
				return pyBool(x || y), nil
			}
			return pyBool(x != y), nil
		}
		x, _ := asIntBig(a)
		y, _ := asIntBig(b)
		return intOp(op, x, y)
	}
	_, aF := a.(*floatObj)
	_, bF := b.(*floatObj)
	if (aF || aInt || aBool) && (bF || bInt || bBool) && (aF || bF) {
		switch op {
		case "&", "|", "^":
			return nil, unsupported(op, a, b)
		}
		x, _, err := toFloat(a)
		if err != nil {
			return nil, err
		}
		y, _, err := toFloat(b)
		if err != nil {
			return nil, err
		}
		return floatOp(op, x, y)
	}
	switch op {
	case "%":
		switch a.(type) {
		case *strObj, *bytesObj:
			return percentFormat(a, b)
		}
	case "+":
		return seqAdd(a, b)
	case "*":
		if n, ok := asIntBig(b); ok {
			if r, ok, err := seqRepeat(a, n); ok {
				return r, err
			}
		}
		if n, ok := asIntBig(a); ok {
			if r, ok, err := seqRepeat(b, n); ok {
				return r, err
			}
		}
		if isSequence(a) {
			return nil, newExc(excTypeError, mkStr("can't multiply sequence by non-int of type '"+typeName(b)+"'"))
		}
		if isSequence(b) {
			return nil, newExc(excTypeError, mkStr("can't multiply sequence by non-int of type '"+typeName(a)+"'"))
		}
	case "|":
		if x, ok := a.(*dictObj); ok {
			if y, ok := b.(*dictObj); ok {
				r := x.copy()
				for _, e := range y.items() {
					if err := r.set(e.key, e.val); err != nil {
						return nil, err
					}
				}
				return r, nil
			}
		}
		if x, ok := a.(*setObj); ok {
			if y, ok := b.(*setObj); ok {
				r := x.copyAs(x.frozen)
				err := r.merge(y)
				return r, err
			}
		}
	case "-":
		if x, ok := a.(*setObj); ok {
			if y, ok := b.(*setObj); ok {
				r := newSet(x.frozen)
				for _, k := range x.items() {
					in, err := y.contains(k)
					if err != nil {
						return nil, err
					}
					if !in {
						if err := r.add(k); err != nil {
							return nil, err
						}
					}
				}
				return r, nil
			}
		}
	}
	sym := op
	if op == "**" {
		sym = "** or pow()"
	}
	return nil, unsupported(sym, a, b)
}

func isSequence(v Value) bool {
	switch v.(type) {
	case *strObj, *listObj, *tupleObj, *bytesObj:
		return true
	}
	return false
}

func seqAdd(a, b Value) (Value, error) {
	switch x := a.(type) {
	case *strObj:
		if y, ok := b.(*strObj); ok {
			if x.s == "" {
				return y, nil
			}
			if y.s == "" {
				return x, nil
			}
			return mkStr(x.s + y.s), nil
		}
		return nil, newExc(excTypeError, mkStr(`can only concatenate str (not "`+typeName(b)+`") to str`))
	case *listObj:
		if y, ok := b.(*listObj); ok {
			items := append(append([]Value(nil), x.items...), y.items...)
			return newList(items), nil
		}
		return nil, newExc(excTypeError, mkStr(`can only concatenate list (not "`+typeName(b)+`") to list`))
	case *tupleObj:
		if y, ok := b.(*tupleObj); ok {
			if len(y.items) == 0 {
				return x, nil
			}
			if len(x.items) == 0 {
				return y, nil
			}
			return newTuple(append(append([]Value(nil), x.items...), y.items...)), nil
		}
		return nil, newExc(excTypeError, mkStr(`can only concatenate tuple (not "`+typeName(b)+`") to tuple`))
	case *bytesObj:
		if y, ok := b.(*bytesObj); ok {
			return &bytesObj{append(append([]byte(nil), x.b...), y.b...)}, nil
		}
		return nil, newExc(excTypeError, mkStr("can't concat "+typeName(b)+" to bytes"))
	}
	return nil, unsupported("+", a, b)
}

// seqRepeat is seq * n; ok is false when a is not a sequence.
func seqRepeat(a Value, n *big.Int) (Value, bool, error) {
	if !isSequence(a) {
		return nil, false, nil
	}
	if !n.IsInt64() {
		return nil, true, newExc(excOverflowError, mkStr("cannot fit 'int' into an index-sized integer"))
	}
	k := n.Int64()
	if k < 0 {
		k = 0
	}
	switch x := a.(type) {
	case *strObj:
		if k == 1 {
			return x, true, nil
		}
		return mkStr(strings.Repeat(x.s, int(k))), true, nil
	case *bytesObj:
		return &bytesObj{[]byte(strings.Repeat(string(x.b), int(k)))}, true, nil
	case *listObj:
		var items []Value
		for i := int64(0); i < k; i++ {
			items = append(items, x.items...)
		}
		return newList(items), true, nil
	case *tupleObj:
		if k == 1 {
			return x, true, nil
		}
		var items []Value
		for i := int64(0); i < k; i++ {
			items = append(items, x.items...)
		}
		return newTuple(items), true, nil
	}
	return nil, false, nil
}

func intOp(op string, x, y *big.Int) (Value, error) {
	r := new(big.Int)
	switch op {
	case "+":
		return mkBig(r.Add(x, y)), nil
	case "-":
		return mkBig(r.Sub(x, y)), nil
	case "*":
		return mkBig(r.Mul(x, y)), nil
	case "/":
		if y.Sign() == 0 {
			return nil, zeroDiv("division by zero")
		}
		q, _ := new(big.Rat).SetFrac(x, y).Float64()
		if math.IsInf(q, 0) {
			return nil, newExc(excOverflowError, mkStr("integer division result too large for a float"))
		}
		return mkFloat(q), nil
	case "//":
		if y.Sign() == 0 {
			return nil, zeroDiv("integer division or modulo by zero")
		}
		q, _ := floorDivMod(x, y)
		return mkBig(q), nil
	case "%":
		if y.Sign() == 0 {
			return nil, zeroDiv("integer modulo by zero")
		}
		_, m := floorDivMod(x, y)
		return mkBig(m), nil
	case "**":
		if y.Sign() < 0 {
			if x.Sign() == 0 {
				return nil, zeroDiv("0.0 cannot be raised to a negative power")
			}
			fx, err := bigToFloat(x)
			if err != nil {
				return nil, err
			}
			fy, err := bigToFloat(y)
			if err != nil {
				return nil, err
			}
			return floatOp("**", fx, fy)
		}
		return mkBig(r.Exp(x, y, nil)), nil
	case "&":
		return mkBig(r.And(x, y)), nil
	case "|":
		return mkBig(r.Or(x, y)), nil
	case "^":
		return mkBig(r.Xor(x, y)), nil
	}
	return nil, newExc(excTypeError, mkStr("bad operator "+op))
}

// floorDivMod is Python's divmod for ints: the quotient rounded toward
// negative infinity and a remainder with the divisor's sign.
func floorDivMod(x, y *big.Int) (*big.Int, *big.Int) {
	q, m := new(big.Int).QuoRem(x, y, new(big.Int))
	if m.Sign() != 0 && (m.Sign() < 0) != (y.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
		m.Add(m, y)
	}
	return q, m
}

func floatOp(op string, x, y float64) (Value, error) {
	switch op {
	case "+":
		return mkFloat(x + y), nil
	case "-":
		return mkFloat(x - y), nil
	case "*":
		return mkFloat(x * y), nil
	case "/":
		if y == 0 {
			return nil, zeroDiv("float division by zero")
		}
		return mkFloat(x / y), nil
	case "//":
		if y == 0 {
			return nil, zeroDiv("float floor division by zero")
		}
		q, _ := floatDivMod(x, y)
		return mkFloat(q), nil
	case "%":
		if y == 0 {
			return nil, zeroDiv("float modulo")
		}
		_, m := floatDivMod(x, y)
		return mkFloat(m), nil
	case "**":
		return floatPow(x, y)
	}
	return nil, newExc(excTypeError, mkStr("bad operator "+op))
}

// floatDivMod is float_divmod from floatobject.c.
func floatDivMod(vx, wx float64) (float64, float64) {
	mod := math.Mod(vx, wx)
	div := (vx - mod) / wx
	if mod != 0 {
		if (wx < 0) != (mod < 0) {
			mod += wx
			div -= 1.0
		}
	} else {
		mod = math.Copysign(0, wx)
	}
	var floordiv float64
	if div != 0 {
		floordiv = math.Floor(div)
		if div-floordiv > 0.5 {
			floordiv += 1.0
		}
	} else {
		floordiv = math.Copysign(0, vx/wx)
	}
	return floordiv, mod
}

// floatPow is float_pow from floatobject.c, with a correctly rounded power
// for integral exponents.
func floatPow(iv, iw float64) (Value, error) {
	if iw == 0 {
		return mkFloat(1.0), nil
	}
	if math.IsNaN(iv) {
		if iw == 0 {
			return mkFloat(1.0), nil
		}
		return mkFloat(iv), nil
	}
	if math.IsNaN(iw) {
		if iv == 1.0 {
			return mkFloat(1.0), nil
		}
		return mkFloat(iw), nil
	}
	if math.IsInf(iw, 0) {
		av := math.Abs(iv)
		switch {
		case av == 1.0:
			return mkFloat(1.0), nil
		case (iw > 0) == (av > 1.0):
			return mkFloat(math.Inf(1)), nil
		}
		return mkFloat(0.0), nil
	}
	if math.IsInf(iv, 0) {
		oddInt := math.Mod(math.Abs(iw), 2.0) == 1.0
		if iw > 0 {
			if oddInt {
				return mkFloat(iv), nil
			}
			return mkFloat(math.Abs(iv)), nil
		}
		if oddInt {
			return mkFloat(math.Copysign(0.0, iv)), nil
		}
		return mkFloat(0.0), nil
	}
	if iv == 0 {
		oddInt := math.Mod(math.Abs(iw), 2.0) == 1.0
		if iw < 0 {
			return nil, zeroDiv("0.0 cannot be raised to a negative power")
		}
		if oddInt {
			return mkFloat(iv), nil
		}
		return mkFloat(0.0), nil
	}
	negResult := false
	if iv < 0 {
		if iw != math.Floor(iw) {
			return nil, newExc(excValueError, mkStr("negative number cannot be raised to a fractional power"))
		}
		iv = -iv
		negResult = math.Mod(math.Abs(iw), 2.0) == 1.0
	}
	if iv == 1.0 {
		if negResult {
			return mkFloat(-1.0), nil
		}
		return mkFloat(1.0), nil
	}
	var ix float64
	if iw == math.Trunc(iw) && math.Abs(iw) <= 1<<20 {
		ix = exactPow(iv, int64(iw))
	} else {
		ix = precisePow(iv, iw)
	}
	if math.IsInf(ix, 0) {
		return nil, newExc(excOverflowError, mkStr("(34, 'Numerical result out of range')"))
	}
	if negResult {
		ix = -ix
	}
	return mkFloat(ix), nil
}

// exactPow is x**n rounded once to the nearest float64.
func exactPow(x float64, n int64) float64 {
	neg := n < 0
	if neg {
		n = -n
	}
	prec := uint(64 + 64*bitsLen(n))
	if prec > 1<<16 {
		return math.Pow(x, float64(map[bool]int64{true: -n, false: n}[neg]))
	}
	base := new(big.Float).SetPrec(prec).SetFloat64(x)
	r := new(big.Float).SetPrec(prec).SetFloat64(1)
	for n > 0 {
		if n&1 == 1 {
			r.Mul(r, base)
		}
		base.Mul(base, base)
		n >>= 1
	}
	if neg {
		r.Quo(new(big.Float).SetPrec(prec).SetFloat64(1), r)
	}
	f, _ := r.Float64()
	return f
}

func bitsLen(n int64) int {
	l := 0
	for n > 0 {
		l++
		n >>= 1
	}
	return l
}

// inplaceAdd is operator.iadd(a, b): list += extends a in place with any
// iterable; everything else is a + b.
func inplaceAdd(a, b Value) (Value, error) {
	if l, ok := a.(*listObj); ok {
		items, err := toSlice(b)
		if err != nil {
			return nil, err
		}
		l.items = append(l.items, items...)
		return l, nil
	}
	r, err := binaryOp("+", a, b)
	if err != nil {
		if e, ok := err.(*excObj); ok && e.cls == excTypeError {
			if s, _ := excStr(e); strings.HasPrefix(s, "unsupported operand type(s) for +:") {
				return nil, unsupported("+=", a, b)
			}
		}
	}
	return r, err
}

// unaryOp is -v ("neg") or ~v ("invert").
func unaryOp(op string, v Value) (Value, error) {
	sym := "-"
	if op == "invert" {
		sym = "~"
	}
	switch x := v.(type) {
	case *intObj, *boolObj:
		i, _ := asIntBig(x)
		if op == "neg" {
			return mkBig(new(big.Int).Neg(i)), nil
		}
		return mkBig(new(big.Int).Not(i)), nil
	case *floatObj:
		if op == "neg" {
			return mkFloat(-x.v), nil
		}
	}
	return nil, newExc(excTypeError, mkStr("bad operand type for unary "+sym+": '"+typeName(v)+"'"))
}
