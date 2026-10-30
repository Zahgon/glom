// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. repr() and str() of the values glom handles, as CPython
// 3.12 prints them.

package glom

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mahmoud/glom/internal/pyuni"
)

// reprer is implemented by the objects that define __repr__ (glom's spec types).
type reprer interface {
	repr() (string, error)
}

// reprState tracks the containers being printed, as Py_ReprEnter does.
type reprState struct{ active []Value }

func (st *reprState) enter(v Value) bool {
	for _, a := range st.active {
		if a == v {
			return false
		}
	}
	st.active = append(st.active, v)
	return true
}

func (st *reprState) leave() { st.active = st.active[:len(st.active)-1] }

var reprStack = &reprState{}

// pyRepr is repr(v).
func pyRepr(v Value) (string, error) {
	switch x := v.(type) {
	case *noneObj:
		return "None", nil
	case *boolObj:
		if x.v {
			return "True", nil
		}
		return "False", nil
	case *intObj:
		return intString(x.v)
	case *floatObj:
		return floatRepr(x.v), nil
	case *strObj:
		return strRepr(x.s), nil
	case *bytesObj:
		return bytesRepr(x.b), nil
	case *listObj:
		if !reprStack.enter(v) {
			return "[...]", nil
		}
		defer reprStack.leave()
		return seqRepr("[", "]", x.items, false)
	case *tupleObj:
		if !reprStack.enter(v) {
			return "(...)", nil
		}
		defer reprStack.leave()
		return seqRepr("(", ")", x.items, true)
	case *dictObj:
		if !reprStack.enter(v) {
			return "{...}", nil
		}
		defer reprStack.leave()
		return dictRepr(x)
	case *setObj:
		return setRepr(x)
	case *sliceObj:
		parts, err := reprAll([]Value{x.start, x.stop, x.step})
		if err != nil {
			return "", err
		}
		return "slice(" + strings.Join(parts, ", ") + ")", nil
	case *instObj:
		var parts []string
		for _, e := range x.attrs.items() {
			s, err := pyRepr(e.val)
			if err != nil {
				return "", err
			}
			parts = append(parts, e.key.(*strObj).s+"="+s)
		}
		return x.cls.name + "(" + strings.Join(parts, ", ") + ")", nil
	case *classObj:
		if x.module == "builtins" {
			return "<class '" + x.qualname + "'>", nil
		}
		return "<class '" + x.module + "." + x.qualname + "'>", nil
	case *excObj:
		return excRepr(x)
	case reprer:
		return x.repr()
	}
	return "<" + typeName(v) + " object at " + addr(v) + ">", nil
}

func addr(v Value) string { return "0x" + strconv.FormatUint(0x7f0000000000+objID(v), 16) }

func reprAll(items []Value) ([]string, error) {
	out := make([]string, len(items))
	for i, it := range items {
		s, err := pyRepr(it)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

func seqRepr(open, close string, items []Value, tuple bool) (string, error) {
	parts, err := reprAll(items)
	if err != nil {
		return "", err
	}
	if tuple && len(items) == 1 {
		return open + parts[0] + ",)", nil
	}
	return open + strings.Join(parts, ", ") + close, nil
}

func dictRepr(d *dictObj) (string, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, e := range d.items() {
		if i > 0 {
			b.WriteString(", ")
		}
		k, err := pyRepr(e.key)
		if err != nil {
			return "", err
		}
		v, err := pyRepr(e.val)
		if err != nil {
			return "", err
		}
		b.WriteString(k + ": " + v)
	}
	b.WriteByte('}')
	return b.String(), nil
}

func setRepr(s *setObj) (string, error) {
	name := "set"
	if s.frozen {
		name = "frozenset"
	}
	if s.used == 0 {
		return name + "()", nil
	}
	if !reprStack.enter(s) {
		return name + "(...)", nil
	}
	defer reprStack.leave()
	parts, err := reprAll(s.items())
	if err != nil {
		return "", err
	}
	inner := "{" + strings.Join(parts, ", ") + "}"
	if s.frozen {
		return "frozenset(" + inner + ")", nil
	}
	return inner, nil
}

// maxStrDigits is sys.get_int_max_str_digits() under CPython 3.12.
const maxStrDigits = 4300

// intString is str(int), refusing more than 4300 digits as CPython does.
func intString(i *big.Int) (string, error) {
	s := i.String()
	n := len(s)
	if i.Sign() < 0 {
		n--
	}
	if n > maxStrDigits {
		return "", newExc(excValueError, mkStr("Exceeds the limit (4300 digits) for integer string conversion; "+
			"use sys.set_int_max_str_digits() to increase the limit"))
	}
	return s, nil
}

// floatRepr is repr(float): the shortest round-tripping digits, in fixed
// notation for decimal exponents from -4 to 15, else in exponent form.
func floatRepr(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	epos := strings.IndexByte(s, 'e')
	mant, exp := s[:epos], s[epos+1:]
	e, _ := strconv.Atoi(exp)
	digits := strings.Replace(mant, ".", "", 1)
	decpt := e + 1
	var out string
	if decpt <= -4 || decpt > 16 {
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		ee := decpt - 1
		sign := "+"
		if ee < 0 {
			sign = "-"
			ee = -ee
		}
		es := strconv.Itoa(ee)
		if len(es) < 2 {
			es = "0" + es
		}
		out += "e" + sign + es
	} else if decpt <= 0 {
		out = "0." + strings.Repeat("0", -decpt) + digits
	} else if decpt >= len(digits) {
		out = digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	} else {
		out = digits[:decpt] + "." + digits[decpt:]
	}
	if neg {
		return "-" + out
	}
	return out
}

// strRepr is repr(str).
func strRepr(s string) string {
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x` + hexN(int(r), 2))
		case r < 0x7f:
			b.WriteRune(r)
		case pyuni.IsPrintable(r):
			b.WriteRune(r)
		case r <= 0xff:
			b.WriteString(`\x` + hexN(int(r), 2))
		case r <= 0xffff:
			b.WriteString(`\u` + hexN(int(r), 4))
		default:
			b.WriteString(`\U` + hexN(int(r), 8))
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// bytesRepr is repr(bytes).
func bytesRepr(bs []byte) string {
	s := string(bs)
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	var b strings.Builder
	b.WriteString("b")
	b.WriteByte(quote)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == quote || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c >= 0x7f:
			b.WriteString(`\x` + hexN(int(c), 2))
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

func hexN(n, w int) string {
	s := strconv.FormatInt(int64(n), 16)
	for len(s) < w {
		s = "0" + s
	}
	return s
}

// pyStr is str(v).
func pyStr(v Value) (string, error) {
	switch x := v.(type) {
	case *strObj:
		return x.s, nil
	case *excObj:
		return excStr(x)
	}
	return pyRepr(v)
}
