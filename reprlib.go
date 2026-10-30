// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. bbrepr: glom's _BBRepr, a reprlib.Repr (CPython 3.12)
// with every length limit raised to 1024 that prints builtins by name, wrapped
// in reprlib.recursive_repr; and bbformat, string.Formatter with !r through
// bbrepr.

package glom

import (
	"strings"
)

const bbLimit = 1024

// bbreprActive holds the objects bbrepr is printing, for recursive_repr.
var bbreprActive []Value

// bbrepr is glom.core.bbrepr.
func bbrepr(v Value) (string, error) {
	for _, a := range bbreprActive {
		if a == v {
			return "...", nil
		}
	}
	bbreprActive = append(bbreprActive, v)
	defer func() { bbreprActive = bbreprActive[:len(bbreprActive)-1] }()
	return bbRepr1(v, bbLimit)
}

// bbRepr1 is _BBRepr.repr1: Repr.repr1, then the builtins' names for reprs
// starting with "<".
func bbRepr1(v Value, level int) (string, error) {
	s, err := reprlibRepr1(v, level)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(s, "<") {
		return s, nil
	}
	if name, ok := builtinName(v); ok {
		return name, nil
	}
	return s, nil
}

// builtinName returns the name under which the builtins module holds v.
func builtinName(v Value) (string, bool) {
	switch x := v.(type) {
	case *classObj:
		if x.builtin && x.module == "builtins" {
			return x.name, true
		}
	case *builtinFunc:
		if x.self == nil && x.module == "builtins" {
			return x.name, true
		}
	}
	return "", false
}

func reprlibRepr1(v Value, level int) (string, error) {
	if v.class().module == "builtins" {
		switch x := v.(type) {
		case *tupleObj:
			return bbIterable(x.items, level, "(", ")", ",")
		case *listObj:
			return bbIterable(x.items, level, "[", "]", "")
		case *setObj:
			if x.used == 0 {
				if x.frozen {
					return "frozenset()", nil
				}
				return "set()", nil
			}
			items := possiblySorted(x.items())
			if x.frozen {
				return bbIterable(items, level, "frozenset({", "})", "")
			}
			return bbIterable(items, level, "{", "}", "")
		case *dictObj:
			return bbDict(x, level)
		case *strObj:
			return bbStr(x), nil
		case *intObj:
			s, err := intString(x.v)
			if err != nil {
				return "", err
			}
			return middleTrunc(s), nil
		}
	}
	return bbInstance(v)
}

// middleTrunc cuts s to 1024 code points with "..." in the middle, as
// repr_int and repr_instance do.
func middleTrunc(s string) string {
	rs := []rune(s)
	if len(rs) <= bbLimit {
		return s
	}
	i := (bbLimit - 3) / 2
	j := bbLimit - 3 - i
	return string(rs[:i]) + "..." + string(rs[len(rs)-j:])
}

func bbInstance(v Value) (string, error) {
	s, err := pyRepr(v)
	if err != nil {
		return "<" + typeName(v) + " instance at " + addr(v) + ">", nil
	}
	return middleTrunc(s), nil
}

func bbIterable(items []Value, level int, left, right, trail string) (string, error) {
	n := len(items)
	var s string
	if level <= 0 && n > 0 {
		s = "..."
	} else {
		var pieces []string
		for i, it := range items {
			if i >= bbLimit {
				break
			}
			p, err := bbRepr1(it, level-1)
			if err != nil {
				return "", err
			}
			pieces = append(pieces, p)
		}
		if n > bbLimit {
			pieces = append(pieces, "...")
		}
		s = strings.Join(pieces, ", ")
		if n == 1 && trail != "" {
			right = trail + right
		}
	}
	return left + s + right, nil
}

func bbDict(d *dictObj, level int) (string, error) {
	n := d.n
	if n == 0 {
		return "{}", nil
	}
	if level <= 0 {
		return "{...}", nil
	}
	var pieces []string
	for i, k := range possiblySorted(d.keys()) {
		if i >= bbLimit {
			break
		}
		ks, err := bbRepr1(k, level-1)
		if err != nil {
			return "", err
		}
		val, _, err := d.get(k)
		if err != nil {
			return "", err
		}
		vs, err := bbRepr1(val, level-1)
		if err != nil {
			return "", err
		}
		pieces = append(pieces, ks+": "+vs)
	}
	if n > bbLimit {
		pieces = append(pieces, "...")
	}
	return "{" + strings.Join(pieces, ", ") + "}", nil
}

// bbStr is Repr.repr_str with maxstring 1024.
func bbStr(x *strObj) string {
	rs := x.runes()
	head := rs
	if len(head) > bbLimit {
		head = head[:bbLimit]
	}
	s := strRepr(string(head))
	if len([]rune(s)) > bbLimit {
		i := (bbLimit - 3) / 2
		j := bbLimit - 3 - i
		cut := append(append([]rune(nil), rs[:i]...), rs[len(rs)-j:]...)
		sr := []rune(strRepr(string(cut)))
		s = string(sr[:i]) + "..." + string(sr[len(sr)-j:])
	}
	return s
}

// possiblySorted is reprlib._possibly_sorted: sorted(x), or x as it is when
// sorting raises.
func possiblySorted(items []Value) []Value {
	a := make([]sortItem, len(items))
	for i, it := range items {
		a[i] = sortItem{it, it}
	}
	if err := sortBy(a, pyLess); err != nil {
		return items
	}
	out := make([]Value, len(a))
	for i, it := range a {
		out[i] = it.v
	}
	return out
}

// bbformat is glom's _BBReprFormatter().format: str.format field syntax with
// automatic or explicit indexes, attribute lookups, and !r through bbrepr with
// escaped single quotes restored.
func bbformat(format string, args ...Value) (string, error) {
	var b strings.Builder
	auto := 0
	rs := []rune(format)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '{' {
			if i+1 < len(rs) && rs[i+1] == '{' {
				b.WriteByte('{')
				i++
				continue
			}
			j := i + 1
			for j < len(rs) && rs[j] != '}' {
				j++
			}
			field := string(rs[i+1 : j])
			i = j
			conv := ""
			if k := strings.IndexByte(field, '!'); k >= 0 {
				conv = field[k+1:]
				field = field[:k]
			}
			name, attrs := field, []string(nil)
			if k := strings.IndexByte(field, '.'); k >= 0 {
				name = field[:k]
				attrs = strings.Split(field[k+1:], ".")
			}
			idx := auto
			if name == "" {
				auto++
			} else {
				idx = atoiSimple(name)
			}
			v := args[idx]
			for _, a := range attrs {
				var err error
				v, err = getAttr(v, a)
				if err != nil {
					return "", err
				}
			}
			var s string
			var err error
			switch conv {
			case "r":
				s, err = bbrepr(v)
				s = strings.ReplaceAll(s, `\'`, "'")
			case "s":
				s, err = pyStr(v)
			default:
				s, err = pyStr(v)
			}
			if err != nil {
				return "", err
			}
			b.WriteString(s)
			continue
		}
		if c == '}' && i+1 < len(rs) && rs[i+1] == '}' {
			i++
		}
		b.WriteRune(c)
	}
	return b.String(), nil
}

func atoiSimple(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
