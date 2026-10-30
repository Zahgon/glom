// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. Python's ==, <, truth, len() and iteration for the values
// glom handles, with CPython 3.12's rules and error messages.

package glom

import (
	"math"
	"math/big"
)

// pyEq is a == b.
func pyEq(a, b Value) (bool, error) {
	if isMLike(a) || isMLike(b) {
		// M's __eq__ builds an _MExpr, which is truthy.
		return true, nil
	}
	if na, ok := numOf(a); ok {
		if nb, ok := numOf(b); ok {
			return numCmp(na, nb) == 0 && !na.isNaN() && !nb.isNaN(), nil
		}
		return false, nil
	}
	switch x := a.(type) {
	case *strObj:
		y, ok := b.(*strObj)
		return ok && x.s == y.s, nil
	case *bytesObj:
		y, ok := b.(*bytesObj)
		return ok && string(x.b) == string(y.b), nil
	case *listObj:
		y, ok := b.(*listObj)
		if !ok {
			return false, nil
		}
		return seqEq(x.items, y.items)
	case *tupleObj:
		y, ok := b.(*tupleObj)
		if !ok {
			return false, nil
		}
		return seqEq(x.items, y.items)
	case *dictObj:
		y, ok := b.(*dictObj)
		if !ok || x.n != y.n {
			return false, nil
		}
		for _, e := range x.items() {
			v, found, err := y.get(e.key)
			if err != nil {
				return false, err
			}
			if !found {
				return false, nil
			}
			eq, err := eqKey(e.val, v)
			if err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	case *setObj:
		y, ok := b.(*setObj)
		if !ok || x.used != y.used {
			return false, nil
		}
		for _, k := range x.items() {
			in, err := y.contains(k)
			if err != nil || !in {
				return false, err
			}
		}
		return true, nil
	case *sliceObj:
		y, ok := b.(*sliceObj)
		if !ok {
			return false, nil
		}
		return seqEq([]Value{x.start, x.stop, x.step}, []Value{y.start, y.stop, y.step})
	case *Path:
		return pathEq(x, b)
	case *TExpr:
		if p, ok := b.(*Path); ok {
			return pathEq(p, a)
		}
	case *viewObj:
		y, ok := b.(*viewObj)
		if ok && x.kind == "keys" && y.kind == "keys" {
			return pyEq(setOfIter(x), setOfIter(y))
		}
	}
	return a == b, nil
}

func setOfIter(v *viewObj) Value {
	s := newSet(true)
	_ = s.update(v)
	return s
}

func seqEq(a, b []Value) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for i := range a {
		eq, err := eqKey(a[i], b[i])
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

// num is an int, bool or float seen as a number.
type num struct {
	i       *big.Int
	f       float64
	isFloat bool
}

func (n num) isNaN() bool { return n.isFloat && math.IsNaN(n.f) }

func numOf(v Value) (num, bool) {
	switch x := v.(type) {
	case *intObj:
		return num{i: x.v}, true
	case *boolObj:
		if x.v {
			return num{i: big.NewInt(1)}, true
		}
		return num{i: big.NewInt(0)}, true
	case *floatObj:
		return num{f: x.v, isFloat: true}, true
	}
	return num{}, false
}

// numCmp compares two numbers exactly (-1, 0, 1); NaN compares as 2.
func numCmp(a, b num) int {
	if a.isNaN() || b.isNaN() {
		return 2
	}
	switch {
	case !a.isFloat && !b.isFloat:
		return a.i.Cmp(b.i)
	case a.isFloat && b.isFloat:
		switch {
		case a.f < b.f:
			return -1
		case a.f > b.f:
			return 1
		}
		return 0
	case a.isFloat:
		return floatIntCmp(a.f, b.i)
	}
	return -floatIntCmp(b.f, a.i)
}

func floatIntCmp(f float64, i *big.Int) int {
	if math.IsInf(f, 1) {
		return 1
	}
	if math.IsInf(f, -1) {
		return -1
	}
	bf := new(big.Float).SetPrec(0).SetFloat64(f)
	bi := new(big.Float).SetPrec(0).SetInt(i)
	return bf.Cmp(bi)
}

var cmpSymbols = map[string]bool{"<": true, "<=": true, ">": true, ">=": true}

// pyCompare is a <op> b for "<", "<=", ">" and ">=".
func pyCompare(op string, a, b Value) (bool, error) {
	if na, ok := numOf(a); ok {
		if nb, ok := numOf(b); ok {
			c := numCmp(na, nb)
			if c == 2 {
				return false, nil
			}
			return cmpResult(op, c), nil
		}
	}
	switch x := a.(type) {
	case *strObj:
		if y, ok := b.(*strObj); ok {
			return cmpResult(op, cmpStrings(x.s, y.s)), nil
		}
	case *bytesObj:
		if y, ok := b.(*bytesObj); ok {
			return cmpResult(op, cmpStrings(string(x.b), string(y.b))), nil
		}
	case *listObj:
		if y, ok := b.(*listObj); ok {
			return seqCompare(op, x.items, y.items)
		}
	case *tupleObj:
		if y, ok := b.(*tupleObj); ok {
			return seqCompare(op, x.items, y.items)
		}
	case *setObj:
		if y, ok := b.(*setObj); ok {
			return setCompare(op, x, y)
		}
	}
	return false, newExc(excTypeError, mkStr("'"+op+"' not supported between instances of '"+
		typeName(a)+"' and '"+typeName(b)+"'"))
}

func cmpStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpResult(op string, c int) bool {
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	}
	return c >= 0
}

func seqCompare(op string, a, b []Value) (bool, error) {
	for i := 0; i < len(a) && i < len(b); i++ {
		eq, err := eqKey(a[i], b[i])
		if err != nil {
			return false, err
		}
		if !eq {
			return pyCompare(op, a[i], b[i])
		}
	}
	return cmpResult(op, cmpInts(len(a), len(b))), nil
}

func cmpInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func setCompare(op string, a, b *setObj) (bool, error) {
	subset := func(x, y *setObj) (bool, error) {
		if x.used > y.used {
			return false, nil
		}
		for _, k := range x.items() {
			in, err := y.contains(k)
			if err != nil || !in {
				return false, err
			}
		}
		return true, nil
	}
	switch op {
	case "<=":
		return subset(a, b)
	case ">=":
		return subset(b, a)
	case "<":
		if a.used >= b.used {
			return false, nil
		}
		return subset(a, b)
	}
	if b.used >= a.used {
		return false, nil
	}
	return subset(b, a)
}

// pyLess is a < b, the comparison sorted(), min() and max() use.
func pyLess(a, b Value) (bool, error) { return pyCompare("<", a, b) }

// truthy is bool(v).
func truthy(v Value) (bool, error) {
	switch x := v.(type) {
	case *noneObj:
		return false, nil
	case *boolObj:
		return x.v, nil
	case *intObj:
		return x.v.Sign() != 0, nil
	case *floatObj:
		return x.v != 0, nil
	case *strObj:
		return x.n != 0, nil
	case *bytesObj:
		return len(x.b) != 0, nil
	case *listObj:
		return len(x.items) != 0, nil
	case *tupleObj:
		return len(x.items) != 0, nil
	case *dictObj:
		return x.n != 0, nil
	case *setObj:
		return x.used != 0, nil
	case *sentinelObj:
		return false, nil
	case *Path:
		return x.length() != 0, nil
	case *viewObj:
		return x.d.n != 0, nil
	}
	return true, nil
}

// pyLen is len(v).
func pyLen(v Value) (int, error) {
	switch x := v.(type) {
	case *strObj:
		return x.n, nil
	case *bytesObj:
		return len(x.b), nil
	case *listObj:
		return len(x.items), nil
	case *tupleObj:
		return len(x.items), nil
	case *dictObj:
		return x.n, nil
	case *setObj:
		return x.used, nil
	case *viewObj:
		return x.d.n, nil
	case *Path:
		return x.length(), nil
	case *scopeObj:
		return len(x.keyList()), nil
	}
	return 0, newExc(excTypeError, mkStr("object of type '"+typeName(v)+"' has no len()"))
}

// ---------------------------------------------------------------- iteration

// iterator is a Python iterator: next returns the item and true, or false at
// the end.
type iterator interface {
	next() (Value, bool, error)
}

type funcIter func() (Value, bool, error)

func (f funcIter) next() (Value, bool, error) { return f() }

func sliceIter(items []Value) iterator {
	i := 0
	return funcIter(func() (Value, bool, error) {
		if i >= len(items) {
			return nil, false, nil
		}
		i++
		return items[i-1], true, nil
	})
}

// pyIter is iter(v).
func pyIter(v Value) (iterator, error) {
	switch x := v.(type) {
	case *listObj:
		i := 0
		return funcIter(func() (Value, bool, error) {
			if i >= len(x.items) {
				i = len(x.items) + 1<<30
				return nil, false, nil
			}
			i++
			return x.items[i-1], true, nil
		}), nil
	case *tupleObj:
		return sliceIter(x.items), nil
	case *strObj:
		rs := x.runes()
		i := 0
		return funcIter(func() (Value, bool, error) {
			if i >= len(rs) {
				return nil, false, nil
			}
			i++
			return mkStr(string(rs[i-1])), true, nil
		}), nil
	case *bytesObj:
		i := 0
		return funcIter(func() (Value, bool, error) {
			if i >= len(x.b) {
				return nil, false, nil
			}
			i++
			return mkInt(int64(x.b[i-1])), true, nil
		}), nil
	case *dictObj:
		return dictIter(x, func(e dictEntry) Value { return e.key }), nil
	case *viewObj:
		return x.iter(), nil
	case *setObj:
		size, i := x.used, 0
		return funcIter(func() (Value, bool, error) {
			if x.used != size {
				return nil, false, newExc(excRuntimeError, mkStr("Set changed size during iteration"))
			}
			for i < len(x.table) {
				e := x.table[i]
				i++
				if e.live() {
					return e.key, true, nil
				}
			}
			return nil, false, nil
		}), nil
	case *iterObj:
		return x, nil
	case *scopeVarsObj:
		return dictIter(x.d, func(e dictEntry) Value { return tup(e.key, e.val) }), nil
	case *scopeObj:
		return sliceIter(x.keyList()), nil
	case *Path:
		i := 0
		return funcIter(func() (Value, bool, error) {
			p, err := x.index(i)
			if err != nil {
				return nil, false, nil
			}
			i++
			return p, true, nil
		}), nil
	}
	return nil, newExc(excTypeError, mkStr("'"+typeName(v)+"' object is not iterable"))
}

func dictIter(d *dictObj, f func(dictEntry) Value) iterator {
	size, i := d.n, 0
	return funcIter(func() (Value, bool, error) {
		if d.n != size {
			return nil, false, newExc(excRuntimeError, mkStr("dictionary changed size during iteration"))
		}
		for i < len(d.entries) {
			e := d.entries[i]
			i++
			if e.live {
				return f(e), true, nil
			}
		}
		return nil, false, nil
	})
}

// toSlice is list(v)'s items.
func toSlice(v Value) ([]Value, error) {
	switch x := v.(type) {
	case *listObj:
		return append([]Value(nil), x.items...), nil
	case *tupleObj:
		return append([]Value(nil), x.items...), nil
	}
	it, err := pyIter(v)
	if err != nil {
		return nil, err
	}
	var out []Value
	for {
		item, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, item)
	}
}

// isIterable is boltons.iterutils.is_iterable: iter(v) does not raise TypeError.
func isIterable(v Value) bool {
	switch v.(type) {
	case *TExpr:
		return true // T has __getitem__
	}
	_, err := pyIter(v)
	return err == nil
}

// pyContains is item in container.
func pyContains(container, item Value) (bool, error) {
	switch c := container.(type) {
	case *listObj:
		return seqContains(c.items, item)
	case *tupleObj:
		return seqContains(c.items, item)
	case *dictObj:
		_, ok, err := c.get(item)
		return ok, err
	case *setObj:
		return c.contains(item)
	case *strObj:
		s, ok := item.(*strObj)
		if !ok {
			return false, newExc(excTypeError, mkStr("'in <string>' requires string as left operand, not "+typeName(item)))
		}
		return containsStr(c.s, s.s), nil
	}
	it, err := pyIter(container)
	if err != nil {
		return false, newExc(excTypeError, mkStr("argument of type '"+typeName(container)+"' is not iterable"))
	}
	for {
		v, ok, err := it.next()
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		eq, err := eqKey(v, item)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
}

func seqContains(items []Value, item Value) (bool, error) {
	for _, v := range items {
		eq, err := eqKey(v, item)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
	return false, nil
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
