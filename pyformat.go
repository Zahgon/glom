// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. printf-style formatting (str % args and bytes % args), as
// CPython 3.12's PyUnicode_Format and _PyBytes_FormatEx consume their
// arguments: %s, %r, %a, %d, %i and %% with width and the - flag.

package glom

import (
	"strconv"
	"strings"
)

// hasMappingSubscript is PyMapping_Check: the type has mp_subscript.
func hasMappingSubscript(v Value) bool {
	switch v.(type) {
	case *dictObj, *listObj, *tupleObj, *strObj, *bytesObj, *scopeObj:
		return true
	}
	return false
}

// percentFormat is fmt % args for a str or bytes format.
func percentFormat(format Value, args Value) (Value, error) {
	isBytes := false
	var text string
	switch f := format.(type) {
	case *strObj:
		text = f.s
	case *bytesObj:
		text, isBytes = string(f.b), true
	}
	kind := "string"
	if isBytes {
		kind = "bytes"
	}
	var items []Value
	argidx, arglen := -2, -1
	if t, ok := args.(*tupleObj); ok {
		items, argidx, arglen = t.items, 0, len(t.items)
	}
	var dict Value
	_, isTuple := args.(*tupleObj)
	_, isStr := args.(*strObj)
	// PyUnicode_Format excludes tuples and str; _PyBytes_FormatEx also bytes.
	if hasMappingSubscript(args) && !isTuple && !isStr && !(isBytes && isBytesLike(args)) {
		dict = args
	}
	next := func() (Value, error) {
		if argidx < arglen {
			argidx++
			if arglen < 0 {
				return args, nil
			}
			return items[argidx-1], nil
		}
		return nil, typeErrf("not enough arguments for format string")
	}
	var b strings.Builder
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '%' {
			b.WriteRune(rs[i])
			continue
		}
		i++
		if i >= len(rs) {
			return nil, valueErrf("incomplete format")
		}
		var arg Value
		if rs[i] == '(' {
			if dict == nil {
				return nil, typeErrf("format requires a mapping")
			}
			j := i + 1
			for j < len(rs) && rs[j] != ')' {
				j++
			}
			key := Value(mkStr(string(rs[i+1 : j])))
			if isBytes {
				key = &bytesObj{[]byte(string(rs[i+1 : j]))}
			}
			v, err := getItem(dict, key)
			if err != nil {
				return nil, err
			}
			arg = v
			arglen, argidx = -1, -2
			i = j + 1
			if i >= len(rs) {
				return nil, valueErrf("incomplete format")
			}
		}
		left := false
		for i < len(rs) && strings.ContainsRune("-+ #0", rs[i]) {
			if rs[i] == '-' {
				left = true
			}
			i++
		}
		width := 0
		for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
			width = width*10 + int(rs[i]-'0')
			i++
		}
		if i >= len(rs) {
			return nil, valueErrf("incomplete format")
		}
		c := rs[i]
		if c == '%' {
			b.WriteByte('%')
			continue
		}
		if arg == nil {
			v, err := next()
			if err != nil {
				return nil, err
			}
			arg = v
		}
		var s string
		var err error
		switch c {
		case 's':
			s, err = pyStr(arg)
		case 'r', 'a':
			s, err = pyRepr(arg)
		case 'd', 'i', 'u':
			n, ok := numOf(arg)
			if !ok {
				return nil, typeErrf("%" + string(c) + " format: a real number is required, not " + typeName(arg))
			}
			if n.isFloat {
				iv, ierr := toInt(arg)
				if ierr != nil {
					return nil, ierr
				}
				s, err = pyRepr(iv)
			} else {
				s = n.i.String()
			}
		default:
			return nil, valueErrf("unsupported format character '" + string(c) + "' (0x" +
				strconv.FormatInt(int64(c), 16) + ") at index " + strconv.Itoa(i))
		}
		if err != nil {
			return nil, err
		}
		for pad := width - len([]rune(s)); pad > 0; pad-- {
			if left {
				s += " "
			} else {
				s = " " + s
			}
		}
		b.WriteString(s)
	}
	if argidx < arglen && dict == nil {
		return nil, typeErrf("not all arguments converted during " + kind + " formatting")
	}
	if isBytes {
		return &bytesObj{[]byte(b.String())}, nil
	}
	return mkStr(b.String()), nil
}

func isBytesLike(v Value) bool {
	_, ok := v.(*bytesObj)
	return ok
}
