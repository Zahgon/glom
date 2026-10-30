// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. The methods of builtin types that glom specs call, with
// CPython 3.12's results and argument errors, and dict views.

package glom

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/mahmoud/glom/internal/pyuni"
)

var methodsInstalled = installMethods()

func def(c *classObj, name string, fn func(self Value, args []Value, kw []kwArg) (Value, error)) {
	if c.methods == nil {
		c.methods = map[string]*methodDescr{}
	}
	c.methods[name] = &methodDescr{owner: c, name: name, fn: fn}
}

func installMethods() bool {
	strMethods()
	dictMethods()
	listMethods()
	setMethods()
	def(intClass, "bit_length", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs(typeName(self)+".bit_length", args, kw); err != nil {
			return nil, err
		}
		i, _ := asIntBig(self)
		return mkInt(int64(i.BitLen())), nil
	})
	def(intClass, "is_integer", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs(typeName(self)+".is_integer", args, kw); err != nil {
			return nil, err
		}
		return trueObj, nil
	})
	def(floatClass, "is_integer", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs(typeName(self)+".is_integer", args, kw); err != nil {
			return nil, err
		}
		f := self.(*floatObj).v
		return pyBool(!math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)), nil
	})
	return true
}

func strSelf(v Value) string { return v.(*strObj).s }

// strArg is a str argument of a method: "<what> must be str, not X" otherwise.
func strArg(v Value, what string) (string, error) {
	if s, ok := v.(*strObj); ok {
		return s.s, nil
	}
	return "", typeErrf(what + "must be str, not " + typeName(v))
}

func pyIsSpaceStr(r rune) bool { return pyuni.IsSpace(r) }

func strMethods() {
	caseFn := func(name string, f func(string) string) {
		def(strClass, name, func(self Value, args []Value, kw []kwArg) (Value, error) {
			if err := noArgs(typeName(self)+"."+name, args, kw); err != nil {
				return nil, err
			}
			return mkStr(f(strSelf(self))), nil
		})
	}
	caseFn("upper", pyuni.Upper)
	caseFn("lower", pyuni.Lower)
	caseFn("title", pyTitle)
	caseFn("capitalize", pyCapitalize)
	caseFn("swapcase", pySwapcase)
	isFn := func(name string, f func(rune) bool) {
		def(strClass, name, func(self Value, args []Value, kw []kwArg) (Value, error) {
			if err := noArgs(typeName(self)+"."+name, args, kw); err != nil {
				return nil, err
			}
			s := strSelf(self)
			if s == "" {
				return falseObj, nil
			}
			for _, r := range s {
				if !f(r) {
					return falseObj, nil
				}
			}
			return trueObj, nil
		})
	}
	isFn("isdigit", pyuni.IsDigit)
	isFn("isalpha", pyuni.IsAlpha)
	isFn("isalnum", pyuni.IsAlnum)
	isFn("isspace", pyuni.IsSpace)
	def(strClass, "isupper", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs("str.isupper", args, kw); err != nil {
			return nil, err
		}
		return pyBool(casedCheck(strSelf(self), pyuni.IsUpper, pyuni.IsLower)), nil
	})
	def(strClass, "islower", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs("str.islower", args, kw); err != nil {
			return nil, err
		}
		return pyBool(casedCheck(strSelf(self), pyuni.IsLower, pyuni.IsUpper)), nil
	})
	for _, name := range []string{"strip", "lstrip", "rstrip"} {
		name := name
		def(strClass, name, func(self Value, args []Value, kw []kwArg) (Value, error) {
			if err := posArgs(name, 0, 1, args, kw); err != nil {
				return nil, err
			}
			s := self.(*strObj)
			var chars []rune
			useChars := false
			if len(args) == 1 && !isNone(args[0]) {
				cs, ok := args[0].(*strObj)
				if !ok {
					return nil, typeErrf(name + " arg must be None or str")
				}
				chars, useChars = cs.runes(), true
			}
			strip := func(r rune) bool {
				if !useChars {
					return pyuni.IsSpace(r)
				}
				for _, c := range chars {
					if c == r {
						return true
					}
				}
				return false
			}
			rs := s.runes()
			i, j := 0, len(rs)
			if name != "rstrip" {
				for i < j && strip(rs[i]) {
					i++
				}
			}
			if name != "lstrip" {
				for j > i && strip(rs[j-1]) {
					j--
				}
			}
			if i == 0 && j == len(rs) {
				return s, nil
			}
			return mkStrRunes(rs[i:j]), nil
		})
	}
	def(strClass, "split", func(self Value, args []Value, kw []kwArg) (Value, error) {
		return strSplit(self, args, kw, "split")
	})
	def(strClass, "rsplit", func(self Value, args []Value, kw []kwArg) (Value, error) {
		return strSplit(self, args, kw, "rsplit")
	})
	def(strClass, "join", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := methO(typeName(self)+".join", args, kw); err != nil {
			return nil, err
		}
		items, err := toSlice(args[0])
		if err != nil {
			return nil, typeErrf("can only join an iterable")
		}
		parts := make([]string, len(items))
		for i, it := range items {
			s, ok := it.(*strObj)
			if !ok {
				return nil, typeErrf("sequence item " + strconv.Itoa(i) + ": expected str instance, " + typeName(it) + " found")
			}
			parts[i] = s.s
		}
		if len(items) == 1 {
			return items[0], nil
		}
		return mkStr(strings.Join(parts, strSelf(self))), nil
	})
	def(strClass, "replace", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := posArgs("replace", 2, 3, args, kw); err != nil {
			return nil, err
		}
		old, ok := args[0].(*strObj)
		if !ok {
			return nil, typeErrf("replace() argument 1 must be str, not " + typeName(args[0]))
		}
		nw, ok := args[1].(*strObj)
		if !ok {
			return nil, typeErrf("replace() argument 2 must be str, not " + typeName(args[1]))
		}
		count := -1
		if len(args) == 3 {
			n, ok := asIntBig(args[2])
			if !ok {
				return nil, typeErrf("'" + typeName(args[2]) + "' object cannot be interpreted as an integer")
			}
			count = int(n.Int64())
		}
		s := self.(*strObj)
		r := pyReplace(s.s, old.s, nw.s, count)
		if r == s.s {
			return s, nil
		}
		return mkStr(r), nil
	})
	for _, name := range []string{"startswith", "endswith"} {
		name := name
		def(strClass, name, func(self Value, args []Value, kw []kwArg) (Value, error) {
			if len(kw) > 0 {
				return nil, typeErrf(name + "() takes no keyword arguments")
			}
			if err := parseTupleCount(name, 1, 3, len(args)); err != nil {
				return nil, err
			}
			rs := self.(*strObj).runes()
			start, end, err := subRange(args[1:], len(rs))
			if err != nil {
				return nil, err
			}
			test := func(p string) bool {
				pr := []rune(p)
				if start > len(rs) {
					return false
				}
				seg := rs[start:max(start, end)]
				if len(pr) > len(seg) {
					return false
				}
				if name == "startswith" {
					return string(seg[:len(pr)]) == p
				}
				return string(seg[len(seg)-len(pr):]) == p
			}
			switch p := args[0].(type) {
			case *strObj:
				return pyBool(test(p.s)), nil
			case *tupleObj:
				for _, it := range p.items {
					s, ok := it.(*strObj)
					if !ok {
						return nil, typeErrf("tuple for " + name + " must only contain str, not " + typeName(it))
					}
					if test(s.s) {
						return trueObj, nil
					}
				}
				return falseObj, nil
			}
			return nil, typeErrf(name + " first arg must be str or a tuple of str, not " + typeName(args[0]))
		})
	}
	for _, name := range []string{"find", "count"} {
		name := name
		def(strClass, name, func(self Value, args []Value, kw []kwArg) (Value, error) {
			if len(kw) > 0 {
				return nil, typeErrf(name + "() takes no keyword arguments")
			}
			if err := parseTupleCount(name, 1, 3, len(args)); err != nil {
				return nil, err
			}
			sub, ok := args[0].(*strObj)
			if !ok {
				return nil, typeErrf("must be str, not " + typeName(args[0]))
			}
			rs := self.(*strObj).runes()
			start, end, err := subRange(args[1:], len(rs))
			if err != nil {
				return nil, err
			}
			sr := sub.runes()
			if name == "find" {
				if end-start < len(sr) {
					return mkInt(-1), nil
				}
				for i := start; i+len(sr) <= end; i++ {
					if string(rs[i:i+len(sr)]) == sub.s {
						return mkInt(int64(i)), nil
					}
				}
				return mkInt(-1), nil
			}
			if end-start < len(sr) {
				return mkInt(0), nil
			}
			if len(sr) == 0 {
				return mkInt(int64(end - start + 1)), nil
			}
			n := 0
			for i := start; i+len(sr) <= end; {
				if string(rs[i:i+len(sr)]) == sub.s {
					n++
					i += len(sr)
				} else {
					i++
				}
			}
			return mkInt(int64(n)), nil
		})
	}
	def(strClass, "zfill", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := methO(typeName(self)+".zfill", args, kw); err != nil {
			return nil, err
		}
		w, ok := asIntBig(args[0])
		if !ok {
			return nil, typeErrf("'" + typeName(args[0]) + "' object cannot be interpreted as an integer")
		}
		s := self.(*strObj)
		rs := s.runes()
		if !w.IsInt64() || w.Int64() <= int64(len(rs)) {
			return s, nil
		}
		fill := int(w.Int64()) - len(rs)
		pad := []rune(strings.Repeat("0", fill))
		if len(rs) > 0 && (rs[0] == '+' || rs[0] == '-') {
			return mkStrRunes(append(append([]rune{rs[0]}, pad...), rs[1:]...)), nil
		}
		return mkStrRunes(append(pad, rs...)), nil
	})
}

// parseTupleCount is PyArg_ParseTuple's argument-count check.
func parseTupleCount(name string, minN, maxN, got int) error {
	plural := func(n int) string {
		if n == 1 {
			return "argument"
		}
		return "arguments"
	}
	if got < minN {
		return typeErrf(name + "() takes at least " + strconv.Itoa(minN) + " " + plural(minN) + " (" +
			strconv.Itoa(got) + " given)")
	}
	if got > maxN {
		return typeErrf(name + "() takes at most " + strconv.Itoa(maxN) + " " + plural(maxN) + " (" +
			strconv.Itoa(got) + " given)")
	}
	return nil
}

// isTitlecaseChar is Py_UNICODE_ISTITLE: a titlecase letter (pyuni.IsTitle is
// the one-character str.istitle(), which uppercase letters also satisfy).
func isTitlecaseChar(r rune) bool { return pyuni.IsTitle(r) && !pyuni.IsUpper(r) }

func casedCheck(s string, want, reject func(rune) bool) bool {
	cased := false
	for _, r := range s {
		if reject(r) || isTitlecaseChar(r) {
			return false
		}
		if want(r) {
			cased = true
		}
	}
	return cased
}

// subRange is the optional start and end arguments of find, count,
// startswith and endswith, adjusted like a slice.
func subRange(args []Value, n int) (int, int, error) {
	start, end := 0, n
	conv := func(v Value, def int) (int, error) {
		if isNone(v) {
			return def, nil
		}
		i, ok := asIntBig(v)
		if !ok {
			return 0, typeErrf("slice indices must be integers or None or have an __index__ method")
		}
		x := i.Int64()
		if x < 0 {
			x += int64(n)
			if x < 0 {
				x = 0
			}
		}
		return int(x), nil
	}
	var err error
	if len(args) > 0 {
		if start, err = conv(args[0], 0); err != nil {
			return 0, 0, err
		}
	}
	if len(args) > 1 {
		if end, err = conv(args[1], n); err != nil {
			return 0, 0, err
		}
	}
	if end > n {
		end = n
	}
	return start, end, nil
}

func pyReplace(s, old, nw string, count int) string {
	if count < 0 {
		count = -1
	}
	if old == "" {
		rs := []rune(s)
		var b strings.Builder
		n := 0
		for i := 0; i <= len(rs); i++ {
			if count < 0 || n < count {
				b.WriteString(nw)
				n++
			}
			if i < len(rs) {
				b.WriteRune(rs[i])
			}
		}
		return b.String()
	}
	return strings.Replace(s, old, nw, count)
}

func strSplit(self Value, args []Value, kw []kwArg, name string) (Value, error) {
	var sep, maxs Value = none, mkInt(-1)
	if len(args) > 2 {
		return nil, typeErrf(name + "() takes at most 2 arguments (" + strconv.Itoa(len(args)) + " given)")
	}
	if len(args) > 0 {
		sep = args[0]
	}
	if len(args) > 1 {
		maxs = args[1]
	}
	for _, k := range kw {
		switch k.name {
		case "sep":
			if len(args) > 0 {
				return nil, typeErrf("argument for " + name + "() given by name ('sep') and position (1)")
			}
			sep = k.val
		case "maxsplit":
			if len(args) > 1 {
				return nil, typeErrf("argument for " + name + "() given by name ('maxsplit') and position (2)")
			}
			maxs = k.val
		default:
			return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for " + name + "()")
		}
	}
	mi, ok := asIntBig(maxs)
	if !ok {
		return nil, typeErrf("'" + typeName(maxs) + "' object cannot be interpreted as an integer")
	}
	maxsplit := -1
	if mi.IsInt64() && mi.Int64() >= 0 && mi.Int64() < 1<<31 {
		maxsplit = int(mi.Int64())
	}
	s := self.(*strObj)
	rs := s.runes()
	var parts []Value
	if isNone(sep) {
		if name == "split" {
			i := 0
			for {
				for i < len(rs) && pyuni.IsSpace(rs[i]) {
					i++
				}
				if i >= len(rs) {
					break
				}
				if maxsplit >= 0 && len(parts) == maxsplit {
					j := len(rs)
					for j > i && pyuni.IsSpace(rs[j-1]) {
						j--
					}
					parts = append(parts, mkStrRunes(rs[i:j]))
					break
				}
				j := i
				for j < len(rs) && !pyuni.IsSpace(rs[j]) {
					j++
				}
				parts = append(parts, mkStrRunes(rs[i:j]))
				i = j
			}
			if len(parts) == 1 && parts[0].(*strObj).s == s.s {
				parts[0] = s
			}
			return newList(parts), nil
		}
		j := len(rs)
		for {
			for j > 0 && pyuni.IsSpace(rs[j-1]) {
				j--
			}
			if j <= 0 {
				break
			}
			if maxsplit >= 0 && len(parts) == maxsplit {
				i := 0
				for i < j && pyuni.IsSpace(rs[i]) {
					i++
				}
				parts = append(parts, mkStrRunes(rs[i:j]))
				break
			}
			i := j
			for i > 0 && !pyuni.IsSpace(rs[i-1]) {
				i--
			}
			parts = append(parts, mkStrRunes(rs[i:j]))
			j = i
		}
		for a, b := 0, len(parts)-1; a < b; a, b = a+1, b-1 {
			parts[a], parts[b] = parts[b], parts[a]
		}
		return newList(parts), nil
	}
	sp, ok := sep.(*strObj)
	if !ok {
		return nil, typeErrf("must be str or None, not " + typeName(sep))
	}
	if sp.s == "" {
		return nil, valueErrf("empty separator")
	}
	var pieces []string
	if name == "split" {
		pieces = strings.SplitN(s.s, sp.s, maxsplit+1)
		if maxsplit < 0 {
			pieces = strings.Split(s.s, sp.s)
		}
	} else {
		all := strings.Split(s.s, sp.s)
		if maxsplit < 0 || maxsplit >= len(all)-1 {
			pieces = all
		} else {
			head := strings.Join(all[:len(all)-maxsplit], sp.s)
			pieces = append([]string{head}, all[len(all)-maxsplit:]...)
		}
	}
	for _, p := range pieces {
		parts = append(parts, mkStr(p))
	}
	if len(parts) == 1 {
		parts[0] = s
	}
	return newList(parts), nil
}

func pyTitle(s string) string {
	var b strings.Builder
	prevCased := false
	for _, r := range s {
		if prevCased {
			b.WriteString(string(pyuni.LowerRune(r)))
		} else {
			b.WriteString(titleRune(r))
		}
		prevCased = pyuni.IsCased(r)
	}
	return b.String()
}

func titleRune(r rune) string {
	if s, ok := pyTitleExc[r]; ok {
		return s
	}
	t := unicode.ToTitle(r)
	if t != r || !pyuni.IsLower(r) {
		if t == r {
			return string(pyuni.UpperRune(r))
		}
		return string(t)
	}
	return string(pyuni.UpperRune(r))
}

func pyCapitalize(s string) string {
	rs := []rune(s)
	if len(rs) == 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(titleRune(rs[0]))
	b.WriteString(string(pyuni.LowerRunes(rs[1:])))
	return b.String()
}

func pySwapcase(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case pyuni.IsUpper(r):
			b.WriteString(string(pyuni.LowerRune(r)))
		case pyuni.IsLower(r):
			b.WriteString(string(pyuni.UpperRune(r)))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- dict

// viewObj is dict_keys, dict_values or dict_items.
type viewObj struct {
	kind string
	d    *dictObj
}

var viewClasses = map[string]*classObj{}

func init() {
	for _, k := range []string{"keys", "values", "items"} {
		c := &classObj{name: "dict_" + k, module: "builtins", qualname: "dict_" + k, iterable: true}
		c.bases = []*classObj{objectClass}
		c.mro = []*classObj{c, objectClass}
		viewClasses[k] = c
	}
}

func (v *viewObj) class() *classObj { return viewClasses[v.kind] }

func (v *viewObj) iter() iterator {
	return dictIter(v.d, func(e dictEntry) Value {
		switch v.kind {
		case "keys":
			return e.key
		case "values":
			return e.val
		}
		return tup(e.key, e.val)
	})
}

func (v *viewObj) repr() (string, error) {
	items, err := toSlice(v)
	if err != nil {
		return "", err
	}
	s, err := pyRepr(newList(items))
	return "dict_" + v.kind + "(" + s + ")", err
}

func dictMethods() {
	for _, k := range []string{"keys", "values", "items"} {
		k := k
		def(dictClass, k, func(self Value, args []Value, kw []kwArg) (Value, error) {
			if err := noArgs(typeName(self)+"."+k, args, kw); err != nil {
				return nil, err
			}
			return &viewObj{kind: k, d: self.(*dictObj)}, nil
		})
	}
	def(dictClass, "get", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := posArgs("get", 1, 2, args, kw); err != nil {
			return nil, err
		}
		v, ok, err := self.(*dictObj).get(args[0])
		if err != nil {
			return nil, err
		}
		if ok {
			return v, nil
		}
		if len(args) == 2 {
			return args[1], nil
		}
		return none, nil
	})
	def(dictClass, "copy", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs(typeName(self)+".copy", args, kw); err != nil {
			return nil, err
		}
		return self.(*dictObj).copy(), nil
	})
	def(dictClass, "update", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if len(args) > 1 {
			return nil, typeErrf("update expected at most 1 argument, got " + strconv.Itoa(len(args)))
		}
		d := self.(*dictObj)
		if len(args) == 1 {
			if err := dictUpdateFrom(d, args[0]); err != nil {
				return nil, err
			}
		}
		for _, k := range kw {
			if err := d.set(mkStr(k.name), k.val); err != nil {
				return nil, err
			}
		}
		return none, nil
	})
	def(dictClass, "pop", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := posArgs("pop", 1, 2, args, kw); err != nil {
			return nil, err
		}
		d := self.(*dictObj)
		v, ok, err := d.get(args[0])
		if err != nil {
			return nil, err
		}
		if !ok {
			if len(args) == 2 {
				return args[1], nil
			}
			return nil, newExc(excKeyError, args[0])
		}
		_, _ = d.del(args[0])
		return v, nil
	})
}

// ---------------------------------------------------------------- list

func listMethods() {
	def(listClass, "append", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := methO(typeName(self)+".append", args, kw); err != nil {
			return nil, err
		}
		l := self.(*listObj)
		l.items = append(l.items, args[0])
		return none, nil
	})
	def(listClass, "extend", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := methO(typeName(self)+".extend", args, kw); err != nil {
			return nil, err
		}
		items, err := toSlice(args[0])
		if err != nil {
			return nil, err
		}
		l := self.(*listObj)
		l.items = append(l.items, items...)
		return none, nil
	})
	def(listClass, "copy", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := noArgs(typeName(self)+".copy", args, kw); err != nil {
			return nil, err
		}
		return newList(append([]Value(nil), self.(*listObj).items...)), nil
	})
	def(listClass, "count", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := methO(typeName(self)+".count", args, kw); err != nil {
			return nil, err
		}
		n := 0
		for _, it := range self.(*listObj).items {
			eq, err := eqKey(it, args[0])
			if err != nil {
				return nil, err
			}
			if eq {
				n++
			}
		}
		return mkInt(int64(n)), nil
	})
	def(listClass, "index", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := posArgs("index", 1, 3, args, kw); err != nil {
			return nil, err
		}
		items := self.(*listObj).items
		start, end, err := subRange(args[1:], len(items))
		if err != nil {
			return nil, err
		}
		for i := start; i < end && i < len(items); i++ {
			eq, err := eqKey(items[i], args[0])
			if err != nil {
				return nil, err
			}
			if eq {
				return mkInt(int64(i)), nil
			}
		}
		r, err := pyRepr(args[0])
		if err != nil {
			return nil, err
		}
		return nil, valueErrf(r + " is not in list")
	})
}

// ---------------------------------------------------------------- set

func setMethods() {
	def(setClass, "add", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if err := methO(typeName(self)+".add", args, kw); err != nil {
			return nil, err
		}
		return none, self.(*setObj).add(args[0])
	})
	def(setClass, "update", func(self Value, args []Value, kw []kwArg) (Value, error) {
		if len(kw) > 0 {
			return nil, typeErrf("set.update() takes no keyword arguments")
		}
		s := self.(*setObj)
		for _, a := range args {
			if err := s.update(a); err != nil {
				return nil, err
			}
		}
		return none, nil
	})
	for _, c := range []*classObj{setClass, frozensetClass} {
		c := c
		def(c, "union", func(self Value, args []Value, kw []kwArg) (Value, error) {
			if len(kw) > 0 {
				return nil, typeErrf(c.name + ".union() takes no keyword arguments")
			}
			s := self.(*setObj)
			if s.frozen && len(args) == 0 {
				return s, nil
			}
			r := s.copyAs(s.frozen)
			for _, a := range args {
				if err := r.update(a); err != nil {
					return nil, err
				}
			}
			return r, nil
		})
	}
}

var _ = big.NewInt
