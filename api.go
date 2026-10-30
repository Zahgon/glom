// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause

// Package glom is a Go port of glom, the Python library for declarative
// access to nested data and its restructuring. Targets and specs are Python
// values built with the constructors below, and glom's behaviour, including
// the CPython 3.12 semantics it inherits (reprs, exceptions and their
// messages, equality and ordering), is reproduced exactly.
package glom

import (
	"math/big"
)

// None returns Python's None.
func None() Value { return none }

// Bool returns True or False.
func Bool(b bool) Value { return pyBool(b) }

// Int returns an int.
func Int(i int64) Value { return mkInt(i) }

// BigInt returns an int of any size. The argument is copied.
func BigInt(i *big.Int) Value { return mkBig(new(big.Int).Set(i)) }

// Float returns a float.
func Float(f float64) Value { return mkFloat(f) }

// String returns a str holding s, which must be valid UTF-8.
func String(s string) Value { return mkStr(s) }

// Bytes returns a bytes object. The argument is copied.
func Bytes(b []byte) Value { return &bytesObj{append([]byte(nil), b...)} }

// List returns a new list holding the items.
func List(items ...Value) Value { return newList(append([]Value(nil), items...)) }

// Tuple returns a tuple holding the items.
func Tuple(items ...Value) Value { return newTuple(append([]Value(nil), items...)) }

// NewDict returns a dict built as dict(zip(keys, values)) is, or the
// TypeError Python raises for an unhashable key.
func NewDict(keys, values []Value) (Value, error) {
	d := newDict()
	for i := range keys {
		if err := d.set(keys[i], values[i]); err != nil {
			return nil, toError(err)
		}
	}
	return d, nil
}

// NewSet returns set(items), or the TypeError for an unhashable item.
func NewSet(items ...Value) (Value, error) {
	s := newSet(false)
	for _, it := range items {
		if err := s.add(it); err != nil {
			return nil, toError(err)
		}
	}
	return s, nil
}

// NewFrozenSet returns frozenset(items), or the TypeError for an unhashable
// item.
func NewFrozenSet(items ...Value) (Value, error) {
	s := newSet(true)
	for _, it := range items {
		if err := s.add(it); err != nil {
			return nil, toError(err)
		}
	}
	return s, nil
}

// NewObject returns an instance of the Object class named class (one class
// per name) with the given attributes, in order.
func NewObject(class string, names []string, values []Value) Value {
	o := &instObj{cls: objectClassNamed(class), attrs: newDict()}
	for i, n := range names {
		o.attrs.setStr(n, values[i])
	}
	return o
}

var objectClasses = map[string]*classObj{}

func objectClassNamed(name string) *classObj {
	if c, ok := objectClasses[name]; ok {
		return c
	}
	c := newClass(name, "objects", objectClass)
	c.hasDict = true
	objectClasses[name] = c
	return c
}

// Slice returns slice(start, stop, step); pass None() for an omitted part.
func Slice(start, stop, step Value) Value { return &sliceObj{start, stop, step} }

// Builtin returns the callable or class with the given name: a builtin such
// as "len" or "KeyError", "operator.add", a method descriptor such as
// "str.upper", or one of glom's error classes such as "glom.PathAccessError".
func Builtin(name string) (Value, error) {
	if v, ok := builtins[name]; ok {
		return v, nil
	}
	return nil, toError(valueErrf("unknown builtin " + strRepr(name)))
}

// Repr returns repr(v), or the exception repr() raises (a ValueError for an
// int of more than 4300 digits).
func Repr(v Value) (string, error) {
	s, err := pyRepr(v)
	return s, toError(err)
}

// BBRepr returns glom.core.bbrepr(v), or the exception it raises.
func BBRepr(v Value) (string, error) {
	s, err := bbrepr(v)
	return s, toError(err)
}

// Sentinels: glom's SKIP, STOP, UP, ROOT and MODE.
var (
	SKIP Value = sentSKIP
	STOP Value = sentSTOP
	UP   Value = sentUP
	ROOT Value = sentROOT
	MODE Value = sentMODE
)

// Error is an exception raised by glom or by a spec constructor.
type Error struct {
	// Class is the name of the exception's class, type(exc).__name__, such
	// as "PathAccessError" or "GlomError.wrap(ValueError)".
	Class string
	// Message is the exception's message before glom() adds its trace:
	// str() of the exception glom copied or wrapped, or str(exc) for one
	// glom did not raise.
	Message string
	// Str is str(exc), the whole text.
	Str string
}

// Error returns Str.
func (e *Error) Error() string { return e.Str }

func toError(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	x := asExc(err)
	str, serr := excStr(x)
	if serr != nil {
		str = "<exception str() failed>"
	}
	msg := str
	if x.g != nil && x.g.wrapped != nil {
		if w, ok := x.g.wrapped.(*excObj); ok {
			if m, err := excStr(w); err == nil {
				msg = m
			}
		}
	}
	return &Error{Class: x.cls.name, Message: msg, Str: str}
}

// KW is a keyword argument.
type KW struct {
	Name  string
	Value Value
}

func kwArgs(kw []KW) []kwArg {
	out := make([]kwArg, len(kw))
	for i, k := range kw {
		out[i] = kwArg{k.Name, k.Value}
	}
	return out
}

func nilOr(v Value, def Value) Value {
	if v == nil {
		return def
	}
	return v
}

// ---------------------------------------------------------------- T, S, A

// Attr is t.name.
func (t *TExpr) Attr(name string) (*TExpr, error) {
	v, err := t.attr(name)
	if err != nil {
		return nil, toError(err)
	}
	return v.(*TExpr), nil
}

// Dunder is t.__(name), the attribute "__" + name.
func (t *TExpr) Dunder(name string) (*TExpr, error) {
	return wrapT(tChild(t, opV("."), mkStr("__"+name)))
}

// Item is t[key].
func (t *TExpr) Item(key Value) (*TExpr, error) { return wrapT(tChild(t, opV("["), key)) }

// Call is t(*args, **kwargs).
func (t *TExpr) Call(args []Value, kwargs []KW) (*TExpr, error) {
	v, err := t.call(args, kwArgs(kwargs))
	if err != nil {
		return nil, toError(err)
	}
	return v.(*TExpr), nil
}

// Star is t.__star__().
func (t *TExpr) Star() (*TExpr, error) { return wrapT(tChild(t, opV("x"), none)) }

// StarStar is t.__starstar__().
func (t *TExpr) StarStar() (*TExpr, error) { return wrapT(tChild(t, opV("X"), none)) }

// Add is t + v.
func (t *TExpr) Add(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("+"), v)) }

// Sub is t - v.
func (t *TExpr) Sub(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("-"), v)) }

// Mul is t * v.
func (t *TExpr) Mul(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("*"), v)) }

// FloorDiv is t // v.
func (t *TExpr) FloorDiv(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("#"), v)) }

// TrueDiv is t / v.
func (t *TExpr) TrueDiv(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("/"), v)) }

// Mod is t % v.
func (t *TExpr) Mod(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("%"), v)) }

// Pow is t ** v.
func (t *TExpr) Pow(v Value) (*TExpr, error) { return wrapT(tChild(t, opV(":"), v)) }

// And is t & v.
func (t *TExpr) And(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("&"), v)) }

// Or is t | v.
func (t *TExpr) Or(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("|"), v)) }

// Xor is t ^ v.
func (t *TExpr) Xor(v Value) (*TExpr, error) { return wrapT(tChild(t, opV("^"), v)) }

// Invert is ~t.
func (t *TExpr) Invert() (*TExpr, error) { return wrapT(tChild(t, opV("~"), none)) }

// Neg is -t.
func (t *TExpr) Neg() (*TExpr, error) { return wrapT(tChild(t, opV("_"), none)) }

func wrapT(t *TExpr, err error) (*TExpr, error) {
	if err != nil {
		return nil, toError(err)
	}
	return t, nil
}

// ---------------------------------------------------------------- Path

// NewPath is Path(*parts).
func NewPath(parts ...Value) (*Path, error) {
	p, err := newPathFrom(parts...)
	if err != nil {
		return nil, toError(err)
	}
	return p, nil
}

// PathFromText is Path.from_text(text).
func PathFromText(text string) *Path { return pathFromText(text) }

// Values is path.values().
func (p *Path) Values() Value { return newTuple(p.values()) }

// Items is path.items().
func (p *Path) Items() Value { return newTuple(p.items()) }

// Len is len(path).
func (p *Path) Len() int { return p.length() }

// Index is path[i].
func (p *Path) Index(i int) (*Path, error) {
	q, err := p.index(i)
	if err != nil {
		return nil, toError(err)
	}
	return q, nil
}

// Slice is path[start:stop:step]; pass None() for an omitted part.
func (p *Path) Slice(start, stop, step Value) (*Path, error) {
	q, err := p.slice(start, stop, step)
	if err != nil {
		return nil, toError(err)
	}
	return q, nil
}

// StartsWith is path.startswith(other).
func (p *Path) StartsWith(other Value) (bool, error) {
	ok, err := p.startswith(other)
	return ok, toError(err)
}

// FromT is path.from_t().
func (p *Path) FromT() *Path { return p.fromT() }

// ---------------------------------------------------------------- core specs

// Spec is Spec(spec, scope=scope); scope nil is not passing it.
func Spec(spec Value, scope Value) Value {
	s := &specObj{spec: spec, scope: newDict()}
	if d, ok := scope.(*dictObj); ok {
		s.scope = d
	}
	return s
}

// Val is Val(value).
func Val(value Value) Value { return &valObj{value} }

// Vars is Vars(base, **kwargs); base nil is not passing it.
func Vars(base Value, kwargs []KW) (Value, error) {
	b := nilOr(base, emptyTuple)
	if err := dictUpdateFrom(newDict(), b); err != nil {
		return nil, toError(err)
	}
	return &varsObj{base: b, defaults: kwArgs(kwargs)}, nil
}

// Ref is Ref(name, subspec); subspec nil is not passing it.
func Ref(name string, subspec Value) Value {
	return &refObj{name: mkStr(name), sub: subspec, hasSub: subspec != nil}
}

// Auto is Auto(spec); spec nil is Auto().
func Auto(spec Value) Value { return &modeSpec{name: "Auto", spec: nilOr(spec, none)} }

// Fill is Fill(spec); spec nil is Fill().
func Fill(spec Value) Value { return &modeSpec{name: "Fill", spec: nilOr(spec, none)} }

// Pipe is Pipe(*steps).
func Pipe(steps ...Value) Value { return &pipeObj{steps: newTuple(append([]Value(nil), steps...))} }

// Call is Call(func, args, kwargs); a nil argument is not passing it.
func Call(fn, args, kwargs Value) (Value, error) {
	c, err := newCall(fn, args, kwargs)
	if err != nil {
		return nil, toError(err)
	}
	return c, nil
}

// Invoke is glom's Invoke.
type Invoke struct{ inv *invokeObj }

func (*Invoke) class() *classObj { return invokeClass }

func (i *Invoke) glomit(target Value, scope *scopeObj) (Value, error) {
	return i.inv.glomit(target, scope)
}

func (i *Invoke) repr() (string, error) { return i.inv.repr() }

// NewInvoke is Invoke(func).
func NewInvoke(fn Value) (*Invoke, error) {
	inv, err := newInvoke(fn)
	if err != nil {
		return nil, toError(err)
	}
	return &Invoke{inv}, nil
}

// InvokeSpecfunc is Invoke.specfunc(spec).
func InvokeSpecfunc(spec Value) *Invoke {
	inv, _ := newInvoke(&specObj{spec: spec, scope: newDict()})
	return &Invoke{inv}
}

// Constants is invoke.constants(*args, **kwargs).
func (i *Invoke) Constants(args []Value, kwargs []KW) *Invoke {
	return &Invoke{i.inv.add(&invokeStep{op: "C", args: append([]Value(nil), args...), kw: kwArgs(kwargs)})}
}

// Specs is invoke.specs(*args, **kwargs).
func (i *Invoke) Specs(args []Value, kwargs []KW) *Invoke {
	return &Invoke{i.inv.add(&invokeStep{op: "S", args: append([]Value(nil), args...), kw: kwArgs(kwargs)})}
}

// Star is invoke.star(args=args, kwargs=kwargs); nil is not passing one.
func (i *Invoke) Star(args, kwargs Value) (*Invoke, error) {
	if args == nil && kwargs == nil {
		return nil, toError(typeErrf("expected one or both of args/kwargs to be passed"))
	}
	return &Invoke{i.inv.add(&invokeStep{op: "*", star: [2]Value{args, kwargs}})}, nil
}

// CoalesceOptions are Coalesce's keyword arguments; nil fields are not
// passed.
type CoalesceOptions struct {
	Default        Value
	DefaultFactory Value
	Skip           Value
	SkipExc        Value
}

// Coalesce is Coalesce(*subspecs, **options).
func Coalesce(subspecs []Value, opts CoalesceOptions) (Value, error) {
	var kw []kwArg
	add := func(name string, v Value) {
		if v != nil {
			kw = append(kw, kwArg{name, v})
		}
	}
	add("default", opts.Default)
	add("default_factory", opts.DefaultFactory)
	add("skip", opts.Skip)
	add("skip_exc", opts.SkipExc)
	c, err := newCoalesce(append([]Value(nil), subspecs...), kw)
	if err != nil {
		return nil, toError(err)
	}
	return c, nil
}

// Options are glom()'s keyword arguments; nil fields are not passed.
type Options struct {
	Default Value
	SkipExc Value
	Scope   Value
}

// Glom is glom(target, spec, **options).
func Glom(target, spec Value, opts Options) (Value, error) {
	v, err := glomTop(target, spec, glomOptions{def: opts.Default, skipExc: opts.SkipExc, scope: opts.Scope}, defaultScopeMap)
	if err != nil {
		return nil, toError(err)
	}
	return v, nil
}

// Glommer is glom's Glommer.
type Glommer struct {
	scope *dictObj
}

// NewGlommer is Glommer(register_default_types=registerDefaultTypes).
func NewGlommer(registerDefaultTypes bool) *Glommer {
	return &Glommer{scope: defaultScope(newRegistry(registerDefaultTypes, false))}
}

// Glom is glommer.glom(target, spec, **options).
func (g *Glommer) Glom(target, spec Value, opts Options) (Value, error) {
	if opts.Scope != nil {
		return nil, toError(typeErrf("glom() got multiple values for keyword argument 'scope'"))
	}
	v, err := glomTop(target, spec, glomOptions{def: opts.Default, skipExc: opts.SkipExc, scope: g.scope}, defaultScopeMap)
	if err != nil {
		return nil, toError(err)
	}
	return v, nil
}

// ---------------------------------------------------------------- matching

// Match is glom's Match.
type Match struct{ m *matchObj }

func (*Match) class() *classObj { return matchClass }

func (m *Match) glomit(target Value, scope *scopeObj) (Value, error) {
	return m.m.glomit(target, scope)
}

func (m *Match) repr() (string, error) { return m.m.repr() }

// NewMatch is Match(spec, default=defaultValue); nil is not passing it.
func NewMatch(spec, defaultValue Value) *Match {
	return &Match{&matchObj{spec: spec, def: nilOr(defaultValue, matchMissing)}}
}

// Matches is match.matches(target).
func (m *Match) Matches(target Value) (bool, error) {
	_, err := glomTop(target, m, glomOptions{}, defaultScopeMap)
	if err != nil {
		if isExcInstance(err, glomErrorClass) {
			return false, nil
		}
		return false, toError(err)
	}
	return true, nil
}

// Verify is match.verify(target).
func (m *Match) Verify(target Value) (Value, error) {
	v, err := glomTop(target, m, glomOptions{}, defaultScopeMap)
	if err != nil {
		return nil, toError(err)
	}
	return v, nil
}

// Regex is Regex(pattern, flags=flags, func=fn), fn being "" (None),
// "match", "search" or "fullmatch".
func Regex(pattern string, flags int, fn string) (Value, error) {
	r, err := newRegex(pattern, flags, fn)
	if err != nil {
		return nil, toError(err)
	}
	return r, nil
}

// MSub is M(spec).
func MSub(spec Value) (Value, error) {
	if _, ok := spec.(*TExpr); !ok {
		return nil, toError(typeErrf("M() only accepts T-style specs, not " + typeName(spec)))
	}
	return &mSubspec{spec}, nil
}

// MCompare is lhs <op> rhs where one side is M or an M(...) subspec; op is
// "==", "!=", ">", "<", ">=" or "<=".
func MCompare(lhs Value, op string, rhs Value) (Value, error) {
	v, err := mCompare(lhs, op, rhs)
	return v, toError(err)
}

// And is And(*children, default=defaultValue); nil is not passing it.
func And(children []Value, defaultValue Value) (Value, error) {
	b, err := newBool("&", append([]Value(nil), children...), defaultValue)
	if err != nil {
		return nil, toError(err)
	}
	return b, nil
}

// Or is Or(*children, default=defaultValue); nil is not passing it.
func Or(children []Value, defaultValue Value) (Value, error) {
	b, err := newBool("|", append([]Value(nil), children...), defaultValue)
	if err != nil {
		return nil, toError(err)
	}
	return b, nil
}

// Not is Not(child).
func Not(child Value) Value { return &notObj{child} }

// BitAnd is a & b.
func BitAnd(a, b Value) (Value, error) {
	v, err := bitAnd(a, b)
	return v, toError(err)
}

// BitOr is a | b.
func BitOr(a, b Value) (Value, error) {
	v, err := bitOr(a, b)
	return v, toError(err)
}

// Invert is ~a.
func Invert(a Value) (Value, error) {
	v, err := invertSpec(a)
	return v, toError(err)
}

// Optional is Optional(key, default=defaultValue); nil is not passing it.
func Optional(key, defaultValue Value) (Value, error) {
	o, err := newOptional(key, defaultValue)
	if err != nil {
		return nil, toError(err)
	}
	return o, nil
}

// Required is Required(key).
func Required(key Value) (Value, error) {
	r, err := newRequired(key)
	if err != nil {
		return nil, toError(err)
	}
	return r, nil
}

// Switch is Switch(cases, default=defaultValue); nil is not passing it.
func Switch(cases, defaultValue Value) (Value, error) {
	s, err := newSwitch(cases, defaultValue)
	if err != nil {
		return nil, toError(err)
	}
	return s, nil
}

// CheckOptions are Check's keyword arguments; nil fields are not passed.
type CheckOptions struct {
	Type       Value
	InstanceOf Value
	EqualTo    Value
	OneOf      Value
	Validate   Value
	Default    Value
}

// Check is Check(spec, **options); spec nil is not passing it.
func Check(spec Value, opts CheckOptions) (Value, error) {
	var kw []kwArg
	add := func(name string, v Value) {
		if v != nil {
			kw = append(kw, kwArg{name, v})
		}
	}
	add("type", opts.Type)
	add("instance_of", opts.InstanceOf)
	add("equal_to", opts.EqualTo)
	add("one_of", opts.OneOf)
	add("validate", opts.Validate)
	add("default", opts.Default)
	c, err := newCheck(spec, kw)
	if err != nil {
		return nil, toError(err)
	}
	return c, nil
}

// ---------------------------------------------------------------- mutation

// Assign is Assign(path, val, missing=missing); missing nil is not passing it.
func Assign(path, val, missing Value) (Value, error) {
	a, err := newAssign(path, val, missing)
	if err != nil {
		return nil, toError(err)
	}
	return a, nil
}

// Delete is Delete(path, ignore_missing=ignoreMissing).
func Delete(path Value, ignoreMissing bool) (Value, error) {
	d, err := newDelete(path, ignoreMissing)
	if err != nil {
		return nil, toError(err)
	}
	return d, nil
}

// AssignFn is the function assign(obj, path, val, missing=missing).
func AssignFn(obj, path, val, missing Value) (Value, error) {
	a, err := newAssign(path, val, missing)
	if err != nil {
		return nil, toError(err)
	}
	v, err := glomTop(obj, a, glomOptions{}, defaultScopeMap)
	return v, toError(err)
}

// DeleteFn is the function delete(obj, path, ignore_missing=ignoreMissing).
func DeleteFn(obj, path Value, ignoreMissing bool) (Value, error) {
	d, err := newDelete(path, ignoreMissing)
	if err != nil {
		return nil, toError(err)
	}
	v, err := glomTop(obj, d, glomOptions{}, defaultScopeMap)
	return v, toError(err)
}

// ---------------------------------------------------------------- streaming

// Iter is glom's Iter.
type Iter struct{ s *iterSpec }

func (*Iter) class() *classObj { return iterSpecClass }

func (i *Iter) glomit(target Value, scope *scopeObj) (Value, error) { return i.s.glomit(target, scope) }

func (i *Iter) repr() (string, error) { return i.s.repr() }

// NewIter is Iter(subspec, sentinel=sentinel); nil is not passing one.
func NewIter(subspec, sentinel Value) *Iter { return &Iter{newIterSpec(subspec, sentinel)} }

// Map is iter.map(subspec).
func (i *Iter) Map(subspec Value) *Iter { return &Iter{i.s.mapStep(subspec)} }

// Filter is iter.filter(key); nil is filter().
func (i *Iter) Filter(key Value) *Iter {
	s, _ := i.s.filterStep(key)
	return &Iter{s}
}

// Chunked is iter.chunked(size, fill=fill); fill nil is not passing it.
func (i *Iter) Chunked(size, fill Value) *Iter { return &Iter{i.s.chunkedStep(size, fill)} }

// Windowed is iter.windowed(size).
func (i *Iter) Windowed(size Value) *Iter { return &Iter{i.s.windowedStep(size)} }

// Split is iter.split(sep, maxsplit); nil is None.
func (i *Iter) Split(sep, maxsplit Value) *Iter { return &Iter{i.s.splitStep(sep, maxsplit)} }

// Flatten is iter.flatten().
func (i *Iter) Flatten() *Iter { return &Iter{i.s.flattenStep()} }

// Unique is iter.unique(key); nil is unique().
func (i *Iter) Unique(key Value) *Iter { return &Iter{i.s.uniqueStep(key)} }

// Slice is iter.slice(*args).
func (i *Iter) Slice(args ...Value) (*Iter, error) {
	s, err := i.s.sliceStep(append([]Value(nil), args...))
	if err != nil {
		return nil, toError(err)
	}
	return &Iter{s}, nil
}

// Limit is iter.limit(count).
func (i *Iter) Limit(count Value) *Iter { return &Iter{i.s.limitStep(count)} }

// Takewhile is iter.takewhile(key); nil is takewhile().
func (i *Iter) Takewhile(key Value) *Iter { return &Iter{i.s.whileStep("takewhile", key)} }

// Dropwhile is iter.dropwhile(key); nil is dropwhile().
func (i *Iter) Dropwhile(key Value) *Iter { return &Iter{i.s.whileStep("dropwhile", key)} }

// All is iter.all().
func (i *Iter) All() Value { return &pipeObj{steps: tup(i, listClass)} }

// First is iter.first(key, default=defaultValue); nil is not passing one.
func (i *Iter) First(key, defaultValue Value) Value { return tup(i, newStreamFirst(key, defaultValue)) }

// IterFirst is glom.streaming.First(key, default=defaultValue); nil is not
// passing one.
func IterFirst(key, defaultValue Value) Value { return newStreamFirst(key, defaultValue) }

// ---------------------------------------------------------------- reduction

// Fold is Fold(subspec, init, op=op); op nil is not passing it.
func Fold(subspec, init, op Value) (Value, error) {
	f, err := newFold("Fold", subspec, init, op)
	if err != nil {
		return nil, toError(err)
	}
	return f, nil
}

// Sum is Sum(subspec, init); nil is not passing one.
func Sum(subspec, init Value) (Value, error) {
	f, err := newFold("Sum", subspec, init, nil)
	if err != nil {
		return nil, toError(err)
	}
	return f, nil
}

// Flatten is Flatten(subspec, init); nil is not passing one (init may be
// String("lazy")).
func Flatten(subspec, init Value) (Value, error) {
	f, err := newFold("Flatten", subspec, init, nil)
	if err != nil {
		return nil, toError(err)
	}
	return f, nil
}

// Merge is Merge(subspec, init, op); nil is not passing one.
func Merge(subspec, init, op Value) (Value, error) {
	f, err := newFold("Merge", subspec, init, op)
	if err != nil {
		return nil, toError(err)
	}
	return f, nil
}

// FlattenFn is the function flatten(target, spec=spec, init=init,
// levels=levels); nil is not passing one.
func FlattenFn(target, spec, init, levels Value) (Value, error) {
	v, err := flattenFn(target, spec, init, levels)
	return v, toError(err)
}

// MergeFn is the function merge(target, spec=spec, init=init, op=op); nil is
// not passing one.
func MergeFn(target, spec, init, op Value) (Value, error) {
	v, err := mergeFn(target, spec, init, op)
	return v, toError(err)
}

// ---------------------------------------------------------------- grouping

// Group is glom.grouping.Group(spec).
func Group(spec Value) Value { return &groupObj{spec} }

// GroupFirst is glom.grouping.First().
func GroupFirst() Value { return &gAgg{"First"} }

// Avg is glom.grouping.Avg().
func Avg() Value { return &gAgg{"Avg"} }

// Max is glom.grouping.Max().
func Max() Value { return &gAgg{"Max"} }

// Min is glom.grouping.Min().
func Min() Value { return &gAgg{"Min"} }

// Limit is glom.grouping.Limit(n, subspec); subspec nil is not passing it.
func Limit(n, subspec Value) Value {
	if subspec == nil {
		subspec = newList([]Value{T})
	}
	return &limitObj{n: n, subspec: subspec}
}
