// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. The object model: every Python value glom touches is a
// pointer to an object, so identity (Python's `is`) is pointer equality, with
// CPython 3.12's shared objects: None, True, False, the ints from -5 to 256,
// the empty string and the one-character strings below U+0100.

package glom

import (
	"math/big"
	"unicode/utf8"
)

// Value is a Python value: data, a spec, a callable, a class or a sentinel.
// It is implemented only by the values this package creates.
type Value interface {
	class() *classObj
}

// ---------------------------------------------------------------- classes

// classObj is a Python class (a type object).
type classObj struct {
	name     string
	module   string // "builtins", "glom.core", "objects", ...
	qualname string
	bases    []*classObj
	mro      []*classObj

	// builtin is true for the classes the builtins module lists, which
	// bbrepr prints by name.
	builtin bool
	// iterable: instances define __iter__. hasDict: instances have a
	// __dict__ with keys(). Both feed glom's target registry.
	iterable bool
	hasDict  bool
	// exception classes: ownStr and ownRepr are the __str__ and __repr__
	// defined in this class's own namespace, getMessage its get_message.
	ownStr     func(e *excObj) (string, error)
	ownRepr    func(e *excObj) (string, error)
	getMessage func(e *excObj) (string, error)
	// construct is what calling the class does; nil for classes that
	// cannot be called from glom.
	construct func(args []Value, kw []kwArg) (Value, error)
	// methods are the attributes the class provides to its instances.
	methods map[string]*methodDescr
	// unhashable instances (__hash__ is None).
	unhashable bool
}

func (*classObj) class() *classObj { return typeClass }

func (c *classObj) isSubclass(of *classObj) bool {
	for _, k := range c.mro {
		if k == of {
			return true
		}
	}
	return false
}

// newClass makes a class and computes its MRO with C3 linearisation.
func newClass(name, module string, bases ...*classObj) *classObj {
	c := &classObj{name: name, module: module, qualname: name, bases: bases}
	c.mro = c3(c)
	return c
}

func c3(c *classObj) []*classObj {
	var seqs [][]*classObj
	for _, b := range c.bases {
		seqs = append(seqs, append([]*classObj(nil), b.mro...))
	}
	seqs = append(seqs, append([]*classObj(nil), c.bases...))
	out := []*classObj{c}
	for {
		empty := true
		for _, s := range seqs {
			if len(s) > 0 {
				empty = false
			}
		}
		if empty {
			return out
		}
		var cand *classObj
		for _, s := range seqs {
			if len(s) == 0 {
				continue
			}
			head := s[0]
			inTail := false
			for _, t := range seqs {
				if len(t) == 0 {
					continue
				}
				for _, x := range t[1:] {
					if x == head {
						inTail = true
					}
				}
			}
			if !inTail {
				cand = head
				break
			}
		}
		if cand == nil {
			panic("glom: inconsistent class hierarchy for " + c.name)
		}
		out = append(out, cand)
		for i, s := range seqs {
			if len(s) > 0 && s[0] == cand {
				seqs[i] = s[1:]
			}
		}
	}
}

var (
	objectClass = &classObj{name: "object", module: "builtins", qualname: "object", builtin: true}
	typeClass   = &classObj{name: "type", module: "builtins", qualname: "type", builtin: true, hasDict: true}
)

func builtinClass(name string, bases ...*classObj) *classObj {
	if len(bases) == 0 {
		bases = []*classObj{objectClass}
	}
	c := newClass(name, "builtins", bases...)
	c.builtin = true
	return c
}

func init() {
	objectClass.mro = []*classObj{objectClass}
	typeClass.bases = []*classObj{objectClass}
	typeClass.mro = []*classObj{typeClass, objectClass}
}

var (
	noneClass      = &classObj{name: "NoneType", module: "builtins", qualname: "NoneType"}
	intClass       = builtinClass("int")
	boolClass      = builtinClass("bool", intClass)
	floatClass     = builtinClass("float")
	strClass       = builtinClass("str")
	bytesClass     = builtinClass("bytes")
	listClass      = builtinClass("list")
	tupleClass     = builtinClass("tuple")
	dictClass      = builtinClass("dict")
	setClass       = builtinClass("set")
	frozensetClass = builtinClass("frozenset")
	sliceClass     = builtinClass("slice")
	builtinFnClass = &classObj{name: "builtin_function_or_method", module: "builtins", qualname: "builtin_function_or_method"}
	methodDescrCls = &classObj{name: "method_descriptor", module: "builtins", qualname: "method_descriptor"}
)

func init() {
	for _, c := range []*classObj{noneClass, builtinFnClass, methodDescrCls} {
		c.bases = []*classObj{objectClass}
		c.mro = []*classObj{c, objectClass}
	}
	for _, c := range []*classObj{listClass, tupleClass, dictClass, setClass, frozensetClass} {
		c.iterable = true
	}
	for _, c := range []*classObj{listClass, dictClass, setClass, sliceClass} {
		c.unhashable = true
	}
	sliceClass.unhashable = false // hashable since CPython 3.12
	strClass.iterable = true
	bytesClass.iterable = true
}

// iterClass returns the class of a C-level iterator such as "map" or "zip":
// iterable, no __dict__.
func iterClass(name string) *classObj {
	if c, ok := iterClasses[name]; ok {
		return c
	}
	c := &classObj{name: name, module: "builtins", qualname: name, iterable: true}
	c.bases = []*classObj{objectClass}
	c.mro = []*classObj{c, objectClass}
	iterClasses[name] = c
	return c
}

var iterClasses = map[string]*classObj{}

// ---------------------------------------------------------------- objects

type noneObj struct{}

type boolObj struct{ v bool }

type intObj struct{ v *big.Int }

type floatObj struct{ v float64 }

// strObj is a str; n is its length in code points.
type strObj struct {
	s string
	n int
}

type bytesObj struct{ b []byte }

type listObj struct{ items []Value }

type tupleObj struct{ items []Value }

type sliceObj struct{ start, stop, step Value }

// instObj is an instance of an Object class: a class name and attributes.
type instObj struct {
	cls   *classObj
	attrs *dictObj
}

func (*noneObj) class() *classObj   { return noneClass }
func (b *boolObj) class() *classObj { return boolClass }
func (*intObj) class() *classObj    { return intClass }
func (*floatObj) class() *classObj  { return floatClass }
func (*strObj) class() *classObj    { return strClass }
func (*bytesObj) class() *classObj  { return bytesClass }
func (*listObj) class() *classObj   { return listClass }
func (*tupleObj) class() *classObj  { return tupleClass }
func (*sliceObj) class() *classObj  { return sliceClass }
func (o *instObj) class() *classObj { return o.cls }

var (
	none     = &noneObj{}
	trueObj  = &boolObj{true}
	falseObj = &boolObj{false}
)

// The shared objects are built by variable initialisers, which run before any
// init function can make a value.
var (
	smallInts  = makeSmallInts()
	emptyStr   = &strObj{}
	latin1Strs = makeLatin1Strs()
	emptyTuple = &tupleObj{}
)

func makeSmallInts() (out [262]*intObj) {
	for i := range out {
		out[i] = &intObj{big.NewInt(int64(i - 5))}
	}
	return out
}

func makeLatin1Strs() (out [256]*strObj) {
	for i := range out {
		out[i] = &strObj{s: string(rune(i)), n: 1}
	}
	return out
}

func pyBool(b bool) Value {
	if b {
		return trueObj
	}
	return falseObj
}

// mkInt returns the int i, the shared object for -5..256.
func mkInt(i int64) *intObj {
	if i >= -5 && i <= 256 {
		return smallInts[i+5]
	}
	return &intObj{big.NewInt(i)}
}

// mkBig returns an int holding i (which it keeps), shared for -5..256.
func mkBig(i *big.Int) *intObj {
	if i.IsInt64() {
		if v := i.Int64(); v >= -5 && v <= 256 {
			return smallInts[v+5]
		}
	}
	return &intObj{i}
}

func mkFloat(f float64) *floatObj { return &floatObj{f} }

// mkStr returns a str, shared for "" and the one-character strings below U+0100.
func mkStr(s string) *strObj {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return emptyStr
	}
	if n == 1 {
		r, _ := utf8.DecodeRuneInString(s)
		if r < 256 {
			return latin1Strs[r]
		}
	}
	return &strObj{s: s, n: n}
}

func mkStrRunes(rs []rune) *strObj { return mkStr(string(rs)) }

func newList(items []Value) *listObj { return &listObj{items: items} }

// newTuple returns a tuple; the empty tuple is one shared object.
func newTuple(items []Value) *tupleObj {
	if len(items) == 0 {
		return emptyTuple
	}
	return &tupleObj{items: items}
}

func tup(items ...Value) *tupleObj { return newTuple(items) }

func (s *strObj) runes() []rune { return []rune(s.s) }

// ---------------------------------------------------------------- helpers

func typeName(v Value) string { return v.class().name }

func isNone(v Value) bool { return v == Value(none) }

func asStr(v Value) (string, bool) {
	if s, ok := v.(*strObj); ok {
		return s.s, true
	}
	return "", false
}

// asIntBig returns the integer value of an int or a bool.
func asIntBig(v Value) (*big.Int, bool) {
	switch x := v.(type) {
	case *intObj:
		return x.v, true
	case *boolObj:
		if x.v {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	}
	return nil, false
}

func isIntLike(v Value) bool {
	switch v.(type) {
	case *intObj, *boolObj:
		return true
	}
	return false
}

func isinstance(v Value, c *classObj) bool { return v.class().isSubclass(c) }

// kwArg is one keyword argument of a call.
type kwArg struct {
	name string
	val  Value
}
