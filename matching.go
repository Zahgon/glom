// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom's matching module: Match, Regex, And, Or, Not, M, Optional,
// Required, Switch and Check, and the match mode.

package glom

import (
	"strconv"
	"strings"

	"github.com/mahmoud/glom/internal/pyre"
)

// ---------------------------------------------------------------- Match

type matchObj struct {
	spec Value
	def  Value // sentinel when unset
}

var matchClass = glomClass("Match", "glom.matching", objectClass)

func (*matchObj) class() *classObj { return matchClass }

func (m *matchObj) glomit(target Value, scope *scopeObj) (Value, error) {
	scope.set(sentMODE, matchMode)
	ret, err := recurse(target, m.spec, scope)
	if err != nil {
		if !isExcInstance(err, glomErrorClass) || m.def == Value(matchMissing) {
			return nil, err
		}
		return argVal(target, m.def, scope)
	}
	return ret, nil
}

func (m *matchObj) repr() (string, error) {
	s, err := bbrepr(m.spec)
	return "Match(" + s + ")", err
}

// ---------------------------------------------------------------- Regex

type regexObj struct {
	pattern string
	flags   int
	fn      string
	re      *pyre.Pattern
}

var regexClass = func() *classObj {
	c := glomClass("Regex", "glom.matching", objectClass)
	c.hasDict = false
	return c
}()

func (*regexObj) class() *classObj { return regexClass }

func newRegex(pattern string, flags int, fn string) (*regexObj, error) {
	switch fn {
	case "", "match", "search", "fullmatch":
	default:
		return nil, valueErrf("'func' must be one of None, fullmatch, match, search")
	}
	p, err := pyre.Compile(pattern, flags)
	if err != nil {
		switch e := err.(type) {
		case *pyre.Error:
			return nil, newExc(excReError, mkStr(e.Message()))
		case *pyre.PyError:
			cls := map[string]*classObj{"OverflowError": excOverflowError, "ValueError": excValueError,
				"IndexError": excIndexError}[e.Class]
			if cls == nil {
				cls = excValueError
			}
			return nil, newExc(cls, mkStr(e.Msg))
		}
		return nil, err
	}
	return &regexObj{pattern: pattern, flags: flags, fn: fn, re: p}, nil
}

func (r *regexObj) glomit(target Value, scope *scopeObj) (Value, error) {
	var text []rune
	switch t := target.(type) {
	case *strObj:
		text = t.runes()
	case *bytesObj:
		return nil, typeErrf("cannot use a string pattern on a bytes-like object")
	default:
		return nil, newMatchError("{0!r} not valid as a Regex target -- expected {1!r}", target.class(), tup(strClass, bytesClass))
	}
	var m *pyre.Match
	switch r.fn {
	case "match":
		m = r.re.Match(text, 0)
	case "search":
		m = r.re.Search(text, 0)
	default:
		m = r.re.FullMatch(text, 0, len(text))
	}
	if m == nil {
		return nil, newMatchError("target did not match pattern {0!r}", mkStr(r.pattern))
	}
	for _, g := range m.GroupDict() {
		var v Value = none
		if g.Value != nil {
			v = mkStr(*g.Value)
		}
		scope.set(mkStr(g.Name), v)
	}
	return target, nil
}

func (r *regexObj) repr() (string, error) {
	s := "(" + strRepr(r.pattern)
	if r.flags != 0 {
		s += ", flags=" + strconv.Itoa(r.flags)
	}
	if r.fn != "" {
		s += ", func=" + r.fn
	}
	return "Regex" + s + ")", nil
}

// ---------------------------------------------------------------- And, Or, Not

type boolSpec struct {
	op       string // "&" (And) or "|" (Or)
	children []Value
	def      Value // matchMissing when unset
}

var (
	andClass = glomClass("And", "glom.matching", objectClass)
	orClass  = glomClass("Or", "glom.matching", objectClass)
	notClass = glomClass("Not", "glom.matching", objectClass)
)

func (b *boolSpec) class() *classObj {
	if b.op == "&" {
		return andClass
	}
	return orClass
}

func (b *boolSpec) name() string {
	if b.op == "&" {
		return "And"
	}
	return "Or"
}

func newBool(op string, children []Value, def Value) (*boolSpec, error) {
	b := &boolSpec{op: op, children: children, def: matchMissing}
	if len(children) == 0 {
		return nil, valueErrf("need at least one operand for " + b.name())
	}
	if def != nil {
		b.def = def
	}
	return b, nil
}

func (b *boolSpec) glomit(target Value, scope *scopeObj) (Value, error) {
	ret, err := b.glomitInner(target, scope)
	if err != nil {
		if isExcInstance(err, glomErrorClass) && b.def != Value(matchMissing) {
			return argVal(target, b.def, scope)
		}
		return nil, err
	}
	return ret, nil
}

func (b *boolSpec) glomitInner(target Value, scope *scopeObj) (Value, error) {
	if b.op == "&" {
		result := target
		for _, c := range b.children {
			r, err := recurse(target, c, scope)
			if err != nil {
				return nil, err
			}
			result = r
		}
		return result, nil
	}
	for _, c := range b.children[:len(b.children)-1] {
		r, err := recurse(target, c, scope)
		if err == nil {
			return r, nil
		}
		if !isExcInstance(err, glomErrorClass) {
			return nil, err
		}
	}
	return recurse(target, b.children[len(b.children)-1], scope)
}

func mRepr(v Value) bool {
	switch x := v.(type) {
	case *mType, *mExpr:
		return true
	case *boolSpec:
		return mRepr(x.children[0])
	case *notObj:
		return mRepr(x.child)
	}
	return false
}

func boolChildRepr(c Value) (string, error) {
	if c == Value(M) {
		return "M", nil
	}
	if _, ok := c.(*mExpr); ok {
		s, err := bbrepr(c)
		return "(" + s + ")", err
	}
	return bbrepr(c)
}

func (b *boolSpec) repr() (string, error) {
	parts := make([]string, len(b.children))
	for i, c := range b.children {
		s, err := boolChildRepr(c)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	if mRepr(b.children[0]) && b.def == Value(matchMissing) {
		return strings.Join(parts, " "+b.op+" "), nil
	}
	if b.def != Value(matchMissing) {
		s, err := pyRepr(b.def)
		if err != nil {
			return "", err
		}
		parts = append(parts, "default="+s)
	}
	return b.name() + "(" + strings.Join(parts, ", ") + ")", nil
}

type notObj struct{ child Value }

func (*notObj) class() *classObj { return notClass }

func (n *notObj) glomit(target Value, scope *scopeObj) (Value, error) {
	_, err := recurse(target, n.child, scope)
	if err != nil {
		if isExcInstance(err, glomErrorClass) {
			return target, nil
		}
		return nil, err
	}
	return nil, newGlomErr(glomErrorClass, &glomErrData{}, mkStr("child shouldn't have passed"), n.child)
}

func (n *notObj) repr() (string, error) {
	if n.child == Value(M) {
		return "~M", nil
	}
	s, err := bbrepr(n.child)
	if err != nil {
		return "", err
	}
	if mRepr(n.child) {
		return "~(" + s + ")", nil
	}
	return "Not(" + s + ")", nil
}

// ---------------------------------------------------------------- M

type mType struct{}

var (
	mTypeClass    = glomClass("_MType", "glom.matching", objectClass)
	mSubClass     = glomClass("_MSubspec", "glom.matching", objectClass)
	mExprClass    = glomClass("_MExpr", "glom.matching", objectClass)
	optionalClass = glomClass("Optional", "glom.matching", objectClass)
	requiredClass = glomClass("Required", "glom.matching", objectClass)
	switchClass   = glomClass("Switch", "glom.matching", objectClass)
	checkClass    = glomClass("Check", "glom.matching", objectClass)
)

func init() {
	for _, c := range []*classObj{mTypeClass, mSubClass, mExprClass, optionalClass, requiredClass} {
		c.hasDict = false
	}
	// _MType and _MSubspec define __eq__, which leaves __hash__ None.
	mTypeClass.unhashable = true
	mSubClass.unhashable = true
}

var mSingleton = &mType{}

// M is glom's M.
var M Value = mSingleton

func (*mType) class() *classObj { return mTypeClass }

func (*mType) repr() (string, error) { return "M", nil }

func (*mType) glomit(target Value, scope *scopeObj) (Value, error) {
	t, err := truthy(target)
	if err != nil {
		return nil, err
	}
	if t {
		return target, nil
	}
	return nil, newMatchError("{0!r} not truthy", target)
}

type mSubspec struct{ spec Value }

func (*mSubspec) class() *classObj { return mSubClass }

func (m *mSubspec) repr() (string, error) {
	s, err := bbrepr(m.spec)
	return "M(" + s + ")", err
}

func (m *mSubspec) glomit(target Value, scope *scopeObj) (Value, error) {
	match, err := recurse(target, m.spec, scope)
	if err != nil {
		return nil, err
	}
	t, err := truthy(match)
	if err != nil {
		return nil, err
	}
	if t {
		return target, nil
	}
	return nil, newMatchError("expected truthy value from {0!r}, got {1!r}", m.spec, match)
}

func isMLike(v Value) bool {
	switch v.(type) {
	case *mType, *mSubspec:
		return true
	}
	return false
}

type mExpr struct {
	lhs Value
	op  string // '=', '!', '>', '<', 'g', 'l'
	rhs Value
}

func (*mExpr) class() *classObj { return mExprClass }

var mOpMap = map[string]string{"=": "==", "!": "!=", "g": ">=", "l": "<="}

func mOpName(op string) string {
	if s, ok := mOpMap[op]; ok {
		return s
	}
	return op
}

func (m *mExpr) glomit(target Value, scope *scopeObj) (Value, error) {
	lhs, rhs := m.lhs, m.rhs
	if lhs == Value(M) {
		lhs = target
	}
	if rhs == Value(M) {
		rhs = target
	}
	var err error
	if s, ok := lhs.(*mSubspec); ok {
		if lhs, err = recurse(target, s.spec, scope); err != nil {
			return nil, err
		}
	}
	if s, ok := rhs.(*mSubspec); ok {
		if rhs, err = recurse(target, s.spec, scope); err != nil {
			return nil, err
		}
	}
	var matched bool
	switch m.op {
	case "=":
		matched, err = pyEq(lhs, rhs)
	case "!":
		var eq bool
		eq, err = pyEq(lhs, rhs)
		matched = !eq
		if isMLike(lhs) || isMLike(rhs) {
			matched = true
		}
	default:
		matched, err = pyCompare(mOpName(m.op), lhs, rhs)
	}
	if err != nil {
		return nil, err
	}
	if matched {
		return target, nil
	}
	return nil, newMatchError("{0!r} {1} {2!r}", lhs, mkStr(mOpName(m.op)), rhs)
}

func (m *mExpr) repr() (string, error) {
	l, err := pyRepr(m.lhs)
	if err != nil {
		return "", err
	}
	r, err := pyRepr(m.rhs)
	return l + " " + mOpName(m.op) + " " + r, err
}

// mCompare builds lhs <op> rhs for M or an M(...) subspec on the left.
func mCompare(lhs Value, op string, rhs Value) (Value, error) {
	code := map[string]string{"==": "=", "!=": "!", ">": ">", "<": "<", ">=": "g", "<=": "l"}[op]
	if code == "" {
		return nil, valueErrf("unknown comparison " + op)
	}
	switch lhs.(type) {
	case *mType, *mSubspec:
		return &mExpr{lhs: lhs, op: code, rhs: rhs}, nil
	}
	switch rhs.(type) {
	case *mType, *mSubspec:
		// the reflected comparison
		ref := map[string]string{"=": "=", "!": "!", ">": "<", "<": ">", "g": "l", "l": "g"}[code]
		return &mExpr{lhs: rhs, op: ref, rhs: lhs}, nil
	}
	return nil, typeErrf("'" + op + "' is not an M comparison")
}

// bitAnd is a & b for glom's spec objects.
func bitAnd(a, b Value) (Value, error) {
	switch x := a.(type) {
	case *TExpr:
		return tChild(x, opV("&"), b)
	case *mType, *mExpr, *notObj:
		return newBool("&", []Value{a, b}, nil)
	case *boolSpec:
		if x.op == "&" {
			return newBool("&", append(append([]Value(nil), x.children...), b), nil)
		}
		return newBool("&", []Value{a, b}, nil)
	}
	switch b.(type) {
	case *mType, *mExpr:
		return newBool("&", []Value{b, a}, nil)
	}
	return binaryOpErr("&", a, b)
}

// bitOr is a | b for glom's spec objects.
func bitOr(a, b Value) (Value, error) {
	switch x := a.(type) {
	case *TExpr:
		return tChild(x, opV("|"), b)
	case *mType, *mExpr, *notObj:
		return newBool("|", []Value{a, b}, nil)
	case *boolSpec:
		if x.op == "|" {
			return newBool("|", append(append([]Value(nil), x.children...), b), nil)
		}
		return newBool("|", []Value{a, b}, nil)
	}
	return binaryOpErr("|", a, b)
}

func binaryOpErr(op string, a, b Value) (Value, error) {
	if _, ok := a.(*TExpr); !ok {
		if _, ok := b.(*TExpr); !ok {
			if r, err := binaryOp(op, a, b); err == nil {
				return r, nil
			}
		}
	}
	return nil, unsupported(op, a, b)
}

// invertSpec is ~a for glom's spec objects.
func invertSpec(a Value) (Value, error) {
	switch x := a.(type) {
	case *TExpr:
		return tChild(x, opV("~"), none)
	case *mType, *mExpr, *boolSpec, *notObj:
		return &notObj{child: a}, nil
	}
	return unaryOp("invert", a)
}

// ---------------------------------------------------------------- Optional, Required

type optionalObj struct {
	key, def Value
}

func (*optionalObj) class() *classObj { return optionalClass }

func newOptional(key, def Value) (*optionalObj, error) {
	switch key.(type) {
	case *optionalObj, *requiredObj:
		return nil, typeErrf("double wrapping of Optional")
	}
	if _, err := pyHash(key); err != nil {
		return nil, err
	}
	if precedence(key) != 0 {
		r, err := pyRepr(key)
		if err != nil {
			return nil, err
		}
		return nil, valueErrf("Optional() keys must be == match constants, not " + r)
	}
	if def == nil {
		def = matchMissing
	}
	return &optionalObj{key: key, def: def}, nil
}

func (o *optionalObj) glomit(target Value, scope *scopeObj) (Value, error) {
	eq, err := pyEq(target, o.key)
	if err != nil {
		return nil, err
	}
	if !eq {
		return nil, newMatchError("target {0} != spec {1}", target, o.key)
	}
	return target, nil
}

func (o *optionalObj) repr() (string, error) {
	s, err := bbrepr(o.key)
	return "Optional(" + s + ")", err
}

type requiredObj struct{ key Value }

func (*requiredObj) class() *classObj { return requiredClass }

func newRequired(key Value) (*requiredObj, error) {
	switch key.(type) {
	case *optionalObj, *requiredObj:
		return nil, typeErrf("double wrapping of Required")
	}
	if _, err := pyHash(key); err != nil {
		return nil, err
	}
	if precedence(key) == 0 {
		s, err := bbrepr(key)
		if err != nil {
			return nil, err
		}
		return nil, valueErrf("== match constants are already required: " + s)
	}
	return &requiredObj{key: key}, nil
}

func (r *requiredObj) repr() (string, error) {
	s, err := bbrepr(r.key)
	return "Required(" + s + ")", err
}

// hasGlomit is hasattr(v, "glomit").
func hasGlomit(v Value) bool {
	switch v.(type) {
	case *TExpr:
		return true
	case glomiter:
		return true
	}
	return false
}

// precedence is _precedence.
func precedence(match Value) int {
	switch x := match.(type) {
	case *requiredObj:
		match = x.key
	case *optionalObj:
		match = x.key
	}
	switch x := match.(type) {
	case *tupleObj:
		if len(x.items) == 0 {
			return 0
		}
		best := 0
		for _, it := range x.items {
			best = max(best, precedence(it))
		}
		return best
	case *setObj:
		if x.frozen {
			if x.used == 0 {
				return 0
			}
			best := 0
			for _, it := range x.items() {
				best = max(best, precedence(it))
			}
			return best
		}
	case *classObj:
		return 2
	}
	if hasGlomit(match) {
		return 1
	}
	return 0
}

// matchDict is matching._handle_dict.
func matchDict(target Value, spec *dictObj, scope *scopeObj) (Value, error) {
	td, ok := target.(*dictObj)
	if !ok {
		return nil, newTypeMatchError(target.class(), dictClass)
	}
	specKeys := spec.keys()
	var required []Value
	for _, k := range specKeys {
		_, isOpt := k.(*optionalObj)
		_, isReq := k.(*requiredObj)
		if precedence(k) == 0 && !isOpt || isReq {
			required = append(required, k)
		}
	}
	defaults := newDict()
	for _, k := range specKeys {
		if o, ok := k.(*optionalObj); ok && o.def != Value(matchMissing) {
			_ = defaults.set(o.key, o.def)
		}
	}
	result := newDict()
	for _, e := range td.items() {
		key, val := e.key, e.val
		matched := false
		for _, maybe := range specKeys {
			specKey := maybe
			if r, ok := maybe.(*requiredObj); ok {
				specKey = r.key
			}
			k, err := recurse(key, specKey, scope)
			if err != nil {
				if isExcInstance(err, glomErrorClass) {
					continue
				}
				return nil, err
			}
			key = k
			sv, _, _ := spec.get(maybe)
			v, err := recurse(val, sv, chainChild(scope))
			if err != nil {
				return nil, err
			}
			if err := result.set(key, v); err != nil {
				return nil, err
			}
			for i, r := range required {
				if r == maybe {
					required = append(required[:i], required[i+1:]...)
					break
				}
			}
			matched = true
			break
		}
		if !matched {
			return nil, newMatchError("key {0!r} didn't match any of {1!r}", key, spec)
		}
	}
	for _, e := range defaults.items() {
		if _, ok, _ := result.get(e.key); ok {
			continue
		}
		v, err := argVal(target, e.val, scope)
		if err != nil {
			return nil, err
		}
		if err := result.set(e.key, v); err != nil {
			return nil, err
		}
	}
	if len(required) > 0 {
		parts := make([]string, len(required))
		for i, r := range required {
			s, err := bbrepr(r)
			if err != nil {
				return nil, err
			}
			parts[i] = s
		}
		return nil, newMatchError("target missing expected keys: {0}", mkStr(strings.Join(parts, ", ")))
	}
	return result, nil
}

// glomMatch is _glom_match, the match mode.
func glomMatch(target, spec Value, scope *scopeObj) (Value, error) {
	switch s := spec.(type) {
	case *classObj:
		if !isinstanceSpec(target, s) {
			return nil, newTypeMatchError(target.class(), s)
		}
		return target, nil
	case *dictObj:
		return matchDict(target, s, scope)
	case *listObj, *setObj:
		if !isinstance(target, spec.class()) {
			return nil, newTypeMatchError(target.class(), spec.class())
		}
		children, _ := toSlice(spec)
		var result []Value
		it, err := pyIter(target)
		if err != nil {
			return nil, err
		}
		for {
			item, ok, err := it.next()
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			var lastErr error
			done := false
			for _, child := range children {
				r, err := recurse(item, child, scope)
				if err != nil {
					if !isExcInstance(err, glomErrorClass) {
						return nil, err
					}
					lastErr = err
					continue
				}
				result = append(result, r)
				done = true
				break
			}
			if !done {
				if t, _ := truthy(target); t && len(children) == 0 {
					return nil, newMatchError("{0!r} does not match empty {1}", target, mkStr(spec.class().name))
				}
				if lastErr == nil {
					return nil, newExc(excRuntimeError, mkStr("No active exception to reraise"))
				}
				return nil, lastErr
			}
		}
		if ss, ok := spec.(*setObj); ok {
			out := newSet(ss.frozen)
			for _, r := range result {
				if err := out.add(r); err != nil {
					return nil, err
				}
			}
			return out, nil
		}
		return newList(result), nil
	case *tupleObj:
		tt, ok := target.(*tupleObj)
		if !ok {
			return nil, newTypeMatchError(target.class(), tupleClass)
		}
		if len(tt.items) != len(s.items) {
			return nil, newMatchError("{0!r} does not match {1!r}", target, spec)
		}
		out := make([]Value, len(s.items))
		for i := range s.items {
			r, err := recurse(tt.items[i], s.items[i], scope)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return newTuple(out), nil
	}
	if callable(spec) {
		name, _ := funcName(spec)
		r, err := callValue(spec, []Value{target}, nil)
		var t bool
		if err == nil {
			t, err = truthy(r)
		}
		if err != nil {
			return nil, newMatchError("{0}({1!r}) did not validate (got exception {2!r})", mkStr(name), target, asExc(err))
		}
		if t {
			return target, nil
		}
		return nil, newMatchError("{0}({1!r}) did not validate (non truthy return)", mkStr(name), target)
	}
	eq, err := pyEq(target, spec)
	if err != nil {
		return nil, err
	}
	if !eq {
		return nil, newMatchError("{0!r} does not match {1!r}", target, spec)
	}
	return target, nil
}

// ---------------------------------------------------------------- Switch

type switchObj struct {
	cases *listObj
	def   Value
}

func (*switchObj) class() *classObj { return switchClass }

func newSwitch(cases, def Value) (*switchObj, error) {
	if d, ok := cases.(*dictObj); ok {
		var items []Value
		for _, e := range d.items() {
			items = append(items, tup(e.key, e.val))
		}
		cases = newList(items)
	}
	l, ok := cases.(*listObj)
	if !ok {
		r, _ := pyRepr(cases.class())
		return nil, typeErrf("expected cases argument to be of format {keyspec: valspec} or [(keyspec, valspec)] not: " + r)
	}
	if def == nil {
		def = matchMissing
	}
	if len(l.items) == 0 {
		r, _ := pyRepr(l)
		return nil, valueErrf("expected at least one case in Switch, got: " + r)
	}
	return &switchObj{cases: l, def: def}, nil
}

func unpack2(v Value) (Value, Value, error) {
	items, err := toSlice(v)
	if err != nil {
		return nil, nil, typeErrf("cannot unpack non-iterable " + typeName(v) + " object")
	}
	if len(items) > 2 {
		return nil, nil, valueErrf("too many values to unpack (expected 2)")
	}
	if len(items) < 2 {
		return nil, nil, valueErrf("not enough values to unpack (expected 2, got " + strconv.Itoa(len(items)) + ")")
	}
	return items[0], items[1], nil
}

func (s *switchObj) glomit(target Value, scope *scopeObj) (Value, error) {
	for _, c := range s.cases.items {
		keyspec, valspec, err := unpack2(c)
		if err != nil {
			return nil, err
		}
		if _, err := recurse(target, keyspec, scope); err != nil {
			if isExcInstance(err, glomErrorClass) {
				continue
			}
			return nil, err
		}
		return recurse(target, valspec, chainChild(scope))
	}
	if s.def != Value(matchMissing) {
		return argVal(target, s.def, scope)
	}
	return nil, newMatchError("no matches for target in Switch")
}

func (s *switchObj) repr() (string, error) {
	r, err := bbrepr(s.cases)
	return "Switch(" + r + ")", err
}

// ---------------------------------------------------------------- Check

type checkObj struct {
	spec       Value
	origKw     []kwArg
	def        Value
	validators []Value
	instanceOf Value // the class or iterable given, or an empty tuple
	types      Value
	vals       Value
}

func (*checkObj) class() *classObj { return checkClass }

var truthyFunc = &builtinFunc{name: "truthy", module: "glom.matching", fn: func(args []Value, kw []kwArg) (Value, error) {
	if len(args) != 1 {
		return nil, typeErrf("Check.__init__.<locals>.truthy() takes 1 positional argument but " +
			strconv.Itoa(len(args)) + " were given")
	}
	t, err := truthy(args[0])
	return pyBool(t), err
}}

func newCheck(spec Value, kw []kwArg) (*checkObj, error) {
	if spec == nil {
		spec = T
	}
	c := &checkObj{spec: spec, origKw: kw, def: sentRAISE}
	opts := map[string]Value{}
	for _, k := range kw {
		opts[k.name] = k.val
	}
	if d, ok := opts["default"]; ok {
		c.def = d
		delete(opts, "default")
	}
	argVals := func(name, cond string, ok func(Value) bool, val Value, canBeEmpty bool) (Value, error) {
		if val == nil {
			return emptyTuple, nil
		}
		if !isIterable(val) {
			val = tup(val)
		} else if t, err := truthy(val); err == nil && !t && !canBeEmpty {
			r, _ := pyRepr(val)
			return nil, valueErrf("expected '" + name + "' argument to contain at least one value, not: " + r)
		}
		items, err := toSlice(val)
		if err != nil {
			return nil, err
		}
		for _, v := range items {
			if !ok(v) {
				r, _ := pyRepr(v)
				return nil, valueErrf("expected '" + name + "' argument to be " + cond + ", not: " + r)
			}
		}
		return val, nil
	}
	validate, hasValidate := opts["validate"]
	delete(opts, "validate")
	if !hasValidate {
		validate = nil
		if len(opts) == 0 {
			validate = truthyFunc
		}
	}
	typeArg := opts["type"]
	instanceOf := opts["instance_of"]
	equalTo, hasEqual := opts["equal_to"]
	oneOf, hasOneOf := opts["one_of"]
	delete(opts, "type")
	delete(opts, "instance_of")
	delete(opts, "equal_to")
	delete(opts, "one_of")
	if len(opts) > 0 {
		var names []string
		for _, k := range kw {
			if _, ok := opts[k.name]; ok {
				names = append(names, strRepr(k.name))
			}
		}
		return nil, typeErrf("unexpected keyword arguments: dict_keys([" + strings.Join(names, ", ") + "])")
	}
	isType := func(v Value) bool { _, ok := v.(*classObj); return ok }
	v, err := argVals("validate", "callable", callable, validate, true)
	if err != nil {
		return nil, err
	}
	c.validators, _ = toSlice(v)
	if c.instanceOf, err = argVals("instance_of", "a type", isType, instanceOf, false); err != nil {
		return nil, err
	}
	if c.types, err = argVals("type", "a type", isType, typeArg, false); err != nil {
		return nil, err
	}
	switch {
	case hasEqual:
		c.vals = tup(equalTo)
		if hasOneOf {
			return nil, typeErrf(`expected "one_of" argument to be unset when "equal_to" argument is passed`)
		}
	case hasOneOf:
		if !isIterable(oneOf) {
			r, _ := pyRepr(oneOf)
			return nil, valueErrf(`expected "one_of" argument to be iterable , not: ` + r)
		}
		if t, _ := truthy(oneOf); !t {
			r, _ := pyRepr(oneOf)
			return nil, valueErrf(`expected "one_of" to contain at least one value, not: ` + r)
		}
		c.vals = oneOf
	default:
		c.vals = emptyTuple
	}
	return c, nil
}

func typeNames(types Value) (Value, error) {
	items, err := toSlice(types)
	if err != nil {
		return nil, err
	}
	if len(items) == 1 {
		n, _ := funcName(items[0])
		return mkStr(n), nil
	}
	var names []Value
	for _, t := range items {
		n, _ := funcName(t)
		names = append(names, mkStr(n))
	}
	return newTuple(names), nil
}

func (c *checkObj) glomit(target Value, scope *scopeObj) (Value, error) {
	ret := target
	var errs []Value
	if c.spec != Value(T) {
		t, err := recurse(target, c.spec, scope)
		if err != nil {
			return nil, err
		}
		target = t
	}
	if t, _ := truthy(c.types); t {
		in, err := pyContains(c.types, target.class())
		if err != nil {
			return nil, err
		}
		if !in {
			if c.def != Value(sentRAISE) {
				return argVal(target, c.def, scope)
			}
			want, err := typeNames(c.types)
			if err != nil {
				return nil, err
			}
			msg, err := fmtR("expected type to be %r, found type %r", want, mkStr(target.class().name))
			if err != nil {
				return nil, err
			}
			errs = append(errs, mkStr(msg))
		}
	}
	if t, _ := truthy(c.vals); t {
		in, err := pyContains(c.vals, target)
		if err != nil {
			return nil, err
		}
		if !in {
			if c.def != Value(sentRAISE) {
				return argVal(target, c.def, scope)
			}
			n, _ := pyLen(c.vals)
			ts, err := pyStr(target)
			if err != nil {
				return nil, err
			}
			if n == 1 {
				first, err := getItem(c.vals, mkInt(0))
				if err != nil {
					return nil, err
				}
				fs, err := pyStr(first)
				if err != nil {
					return nil, err
				}
				errs = append(errs, mkStr("expected "+fs+", found "+ts))
			} else {
				vs, err := pyStr(c.vals)
				if err != nil {
					return nil, err
				}
				errs = append(errs, mkStr("expected one of "+vs+", found "+ts))
			}
		}
	}
	for _, v := range c.validators {
		res, err := callValue(v, []Value{target}, nil)
		failed := err == nil && res == Value(falseObj)
		if err == nil && !failed {
			continue
		}
		var name Value = none
		if n, ok := funcName(v); ok {
			name = mkStr(n)
		}
		msg, _ := fmtR("expected %r check to validate target", name)
		if failed {
			if c.def != Value(sentRAISE) {
				return c.def, nil
			}
		} else {
			r, rerr := pyRepr(asExc(err))
			if rerr != nil {
				return nil, rerr
			}
			msg += " (got exception: " + r + ")"
		}
		errs = append(errs, mkStr(msg))
	}
	if t, _ := truthy(c.instanceOf); t {
		var spec Value
		switch x := c.instanceOf.(type) {
		case *tupleObj, *classObj:
			spec = x
		default:
			return nil, typeErrf("isinstance() arg 2 must be a type, a tuple of types, or a union")
		}
		if !isinstanceSpec(target, spec) {
			if c.def != Value(sentRAISE) {
				return argVal(target, c.def, scope)
			}
			want, err := typeNames(c.instanceOf)
			if err != nil {
				return nil, err
			}
			msg, err := fmtR("expected instance of %r, found instance of %r", want, mkStr(target.class().name))
			if err != nil {
				return nil, err
			}
			errs = append(errs, mkStr(msg))
		}
	}
	if len(errs) > 0 {
		msgs := newList(errs)
		path := scope.mustGet(pathClass)
		return nil, newGlomErr(checkErrorClass, &glomErrData{msgs: msgs, check: c, path: path}, msgs, c, path)
	}
	return ret, nil
}

func (c *checkObj) repr() (string, error) {
	var args []Value
	if c.spec != Value(T) {
		args = []Value{c.spec}
	}
	return formatInvocation("Check", args, c.origKw, true)
}

func checkMessage(e *excObj) (string, error) {
	p, err := pyStr(e.g.path)
	if err != nil {
		return "", err
	}
	msg := "target at path " + p + " failed check,"
	if e.g.check.spec != Value(T) {
		s, err := pyRepr(e.g.check.spec)
		if err != nil {
			return "", err
		}
		msg += " subtarget at " + s
	}
	if len(e.g.msgs.items) == 1 {
		s, err := pyRepr(e.g.msgs.items[0])
		if err != nil {
			return "", err
		}
		msg += " got error: " + s
	} else {
		s, err := pyRepr(e.g.msgs)
		if err != nil {
			return "", err
		}
		msg += " got " + strconv.Itoa(len(e.g.msgs.items)) + " errors: " + s
	}
	return msg, nil
}
