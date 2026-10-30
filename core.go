// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom's core module: glom() and its scope, the AUTO, FILL and
// argument modes, T/S/A expressions and their evaluation, Path, and the core
// specifier types.

package glom

import (
	"sort"
	"strings"
)

// glomiter is implemented by the specifier types (they have a glomit method).
type glomiter interface {
	glomit(target Value, scope *scopeObj) (Value, error)
}

var defaultRegistry = newRegistry(true, true)

// ---------------------------------------------------------------- T

// TExpr is a T, S or A expression: glom's TType. ops is its __ops__ tuple:
// the root (T, S or A) followed by operation and argument pairs, where an
// operation is one of '.', '[', 'P', '(', 'x', 'X', '+', '-', '*', '#', '/',
// '%', ':', '&', '|', '^', '~' and '_'. A Path slice can leave an odd or
// misaligned tuple, which glom only trips over when it is used.
type TExpr struct {
	ops []Value
}

var tTypeClass = glomClass("TType", "glom.core", objectClass)

func init() { tTypeClass.hasDict = false }

func (*TExpr) class() *classObj { return tTypeClass }

// T, S and A are glom's T (the target), S (the scope) and A (assignment to
// the scope).
var (
	T = &TExpr{}
	S = &TExpr{}
	A = &TExpr{}
)

func init() {
	T.ops = []Value{T}
	S.ops = []Value{S}
	A.ops = []Value{A}
}

var (
	tStar     *TExpr
	tStarStar *TExpr
)

func init() {
	tStar = tChildMust(T, opV("x"), none)
	tStarStar = tChildMust(T, opV("X"), none)
}

// opV is an operation code as the str glom stores in __ops__.
func opV(op string) Value { return mkStr(op) }

// opName is an operation code as text, or "" when it is not a str.
func opName(v Value) (string, bool) {
	s, ok := v.(*strObj)
	if !ok {
		return "", false
	}
	return s.s, true
}

func (t *TExpr) root() Value { return t.ops[0] }

func tChildMust(p *TExpr, op Value, arg Value) *TExpr {
	t, _ := tChild(p, op, arg)
	return t
}

// tChild is _t_child.
func tChild(parent *TExpr, op Value, arg Value) (*TExpr, error) {
	if parent.ops[0] == Value(A) {
		name, _ := opName(op)
		if name != "." && name != "[" && name != "P" {
			return nil, newBadSpec("operation not allowed on A assignment path")
		}
	}
	ops := make([]Value, len(parent.ops), len(parent.ops)+2)
	copy(ops, parent.ops)
	return &TExpr{ops: append(ops, op, arg)}, nil
}

func (t *TExpr) attr(name string) (Value, error) {
	if strings.HasPrefix(name, "__") {
		return nil, newExc(excAttributeError, mkStr(`T instances reserve dunder attributes. To access the "`+name+
			`" attribute, use T.__("`+name[2:]+`")`))
	}
	return tChild(t, opV("."), mkStr(name))
}

func (t *TExpr) item(key Value) (Value, error) { return tChild(t, opV("["), key) }

func (t *TExpr) call(args []Value, kw []kwArg) (Value, error) {
	if t == S {
		if len(args) > 0 {
			r, _ := pyRepr(newTuple(args))
			return nil, typeErrf("S() takes no positional arguments, got: " + r)
		}
		if len(kw) == 0 {
			return nil, typeErrf("S() expected at least one kwarg, got none")
		}
	}
	d := newDict()
	for _, k := range kw {
		d.setStr(k.name, k.val)
	}
	return tChild(t, opV("("), tup(newTuple(append([]Value(nil), args...)), d))
}

func (t *TExpr) repr() (string, error) { return formatT(t.ops[1:], t.ops[0]) }

func rootName(r Value) string {
	switch r {
	case Value(S):
		return "S"
	case Value(A):
		return "A"
	}
	return "T"
}

// formatSlice is _format_slice.
func formatSlice(x Value) (string, error) {
	s, ok := x.(*sliceObj)
	if !ok {
		return bbrepr(x)
	}
	f := func(v Value) (string, error) {
		if isNone(v) {
			return "", nil
		}
		return bbrepr(v)
	}
	a, err := f(s.start)
	if err != nil {
		return "", err
	}
	b, err := f(s.stop)
	if err != nil {
		return "", err
	}
	if isNone(s.step) {
		return a + ":" + b, nil
	}
	c, err := f(s.step)
	return a + ":" + b + ":" + c, err
}

func tupleIndexErr() error { return newExc(excIndexError, mkStr("tuple index out of range")) }

var arithOps = "+-/%:&|^~_"

// containsArith is any([o in path for o in '+-/%:&|^~_']).
func containsArith(path []Value) bool {
	for _, o := range arithOps {
		for _, p := range path {
			if name, ok := opName(p); ok && name == string(o) {
				return true
			}
		}
	}
	return false
}

// formatT is _format_t over a flat list of operations and arguments.
func formatT(path []Value, root Value) (string, error) {
	prepr := []string{rootName(root)}
	for i := 0; i < len(path); i += 2 {
		if i+1 >= len(path) {
			return "", tupleIndexErr()
		}
		op, arg := path[i], path[i+1]
		name, _ := opName(op)
		switch name {
		case ".":
			s, ok := arg.(*strObj)
			if !ok {
				return "", typeErrf(`can only concatenate str (not "` + typeName(arg) + `") to str`)
			}
			prepr = append(prepr, "."+s.s)
		case "[":
			var index string
			if tu, ok := arg.(*tupleObj); ok {
				parts := make([]string, len(tu.items))
				for j, x := range tu.items {
					s, err := formatSlice(x)
					if err != nil {
						return "", err
					}
					parts[j] = s
				}
				index = strings.Join(parts, ", ")
			} else {
				s, err := formatSlice(arg)
				if err != nil {
					return "", err
				}
				index = s
			}
			prepr = append(prepr, "["+index+"]")
		case "(":
			a, ok := arg.(*tupleObj)
			if !ok || len(a.items) != 2 {
				return "", typeErrf("cannot unpack non-iterable " + typeName(arg) + " object")
			}
			args, _ := toSlice(a.items[0])
			d, _ := a.items[1].(*dictObj)
			var kw []kwArg
			if d != nil {
				kw = kwOfDict(d)
			}
			s, err := formatInvocation("", args, kw, true)
			if err != nil {
				return "", err
			}
			prepr = append(prepr, s)
		case "P":
			return formatPath(path)
		case "x":
			prepr = append(prepr, ".__star__()")
		case "X":
			prepr = append(prepr, ".__starstar__()")
		case "_", "~":
			if containsArith(path[:i]) {
				prepr = append(append([]string{"("}, prepr...), ")")
			}
			sym := name
			if sym == "_" {
				sym = "-"
			}
			prepr = append([]string{sym}, prepr...)
		default:
			fa, err := bbrepr(arg)
			if err != nil {
				return "", err
			}
			if at, ok := arg.(*TExpr); ok && containsArith(at.ops) {
				fa = "(" + fa + ")"
			}
			if _, isStr := op.(*strObj); !isStr {
				return "", typeErrf(`can only concatenate str (not "` + typeName(op) + `") to str`)
			}
			sym := name
			if sym == ":" {
				sym = "**"
			}
			prepr = append(prepr, " "+sym+" ", fa)
		}
	}
	return strings.Join(prepr, ""), nil
}

func kwOfDict(d *dictObj) []kwArg {
	var out []kwArg
	for _, e := range d.items() {
		k, _ := asStr(e.key)
		out = append(out, kwArg{k, e.val})
	}
	return out
}

// formatInvocation is glom's format_invocation(name, args, kwargs,
// repr=bbrepr); sortKw is true when kwargs was a dict (printed sorted).
func formatInvocation(name string, args []Value, kw []kwArg, sortKw bool) (string, error) {
	parts := make([]string, 0, len(args)+len(kw))
	for _, a := range args {
		s, err := bbrepr(a)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	items := kw
	if sortKw {
		items = append([]kwArg(nil), kw...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].name < items[j].name })
	}
	for _, k := range items {
		s, err := bbrepr(k.val)
		if err != nil {
			return "", err
		}
		parts = append(parts, k.name+"="+s)
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

// ---------------------------------------------------------------- Path

// Path is glom's Path.
type Path struct {
	pathT *TExpr
}

func (*Path) class() *classObj { return pathClass }

func init() { pathClass.unhashable = true }

// newPathFrom is Path(*parts).
func newPathFrom(parts ...Value) (*Path, error) {
	if len(parts) == 0 {
		return &Path{pathT: T}, nil
	}
	pathT := T
	offset := 0
	if t, ok := parts[0].(*TExpr); ok {
		pathT = t
		offset = 1
	}
	for _, part := range parts[offset:] {
		if p, ok := part.(*Path); ok {
			part = p.pathT
		}
		if t, ok := part.(*TExpr); ok {
			if t.ops[0] != Value(T) {
				r, _ := pyRepr(t.ops[0])
				return nil, valueErrf("path segment must be path from T, not " + r)
			}
			for i := 1; i < len(t.ops); i += 2 {
				if i+1 >= len(t.ops) {
					return nil, tupleIndexErr()
				}
				var err error
				if pathT, err = tChild(pathT, t.ops[i], t.ops[i+1]); err != nil {
					return nil, err
				}
			}
		} else {
			var err error
			if pathT, err = tChild(pathT, opV("P"), part); err != nil {
				return nil, err
			}
		}
	}
	return &Path{pathT: pathT}, nil
}

var pathCache = map[string]*Path{}

// pathFromText is Path.from_text (with '*' and '**' as star segments).
func pathFromText(text string) *Path {
	if p, ok := pathCache[text]; ok {
		return p
	}
	segs := strings.Split(text, ".")
	parts := make([]Value, len(segs))
	for i, s := range segs {
		switch s {
		case "*":
			parts[i] = tStar
		case "**":
			parts[i] = tStarStar
		default:
			parts[i] = mkStr(s)
		}
	}
	p, _ := newPathFrom(parts...)
	if len(pathCache) <= 10000 {
		pathCache[text] = p
	}
	return p
}

func (p *Path) glomit(target Value, scope *scopeObj) (Value, error) {
	return tEval(target, p.pathT, scope)
}

// length is len(path): (len(__ops__) - 1) // 2.
func (p *Path) length() int { return (len(p.pathT.ops) - 1) / 2 }

// values is __ops__[2::2].
func (p *Path) values() []Value {
	var out []Value
	for i := 2; i < len(p.pathT.ops); i += 2 {
		out = append(out, p.pathT.ops[i])
	}
	return out
}

// items is tuple(zip(__ops__[1::2], __ops__[2::2])).
func (p *Path) items() []Value {
	var out []Value
	for i := 1; i+1 < len(p.pathT.ops); i += 2 {
		out = append(out, tup(p.pathT.ops[i], p.pathT.ops[i+1]))
	}
	return out
}

// startswith is Path.startswith.
func (p *Path) startswith(other Value) (bool, error) {
	if s, ok := other.(*strObj); ok {
		q, err := newPathFrom(s)
		if err != nil {
			return false, err
		}
		other = q
	}
	if q, ok := other.(*Path); ok {
		other = q.pathT
	}
	t, ok := other.(*TExpr)
	if !ok {
		return false, typeErrf("can only check if Path starts with string, Path or T")
	}
	a, b := p.pathT.ops, t.ops
	if len(b) > len(a) {
		return false, nil
	}
	return seqEq(a[:len(b)], b)
}

// fromT is Path.from_t.
func (p *Path) fromT() *Path {
	if p.pathT.ops[0] == Value(S) {
		return &Path{pathT: &TExpr{ops: append([]Value{T}, p.pathT.ops[1:]...)}}
	}
	return p
}

// index is path[i].
func (p *Path) index(i int) (*Path, error) {
	ops := p.pathT.ops
	n := len(ops)
	start := i*2 + 1
	if i < 0 {
		start = i*2 + n
	}
	if start < 0 || start >= n {
		return nil, newExc(excIndexError, mkStr("Path index "+itoa(i)+" out of range for Path of length "+
			itoa((n-1)/2)))
	}
	stop := (i+1)*2 + 1
	if i < 0 {
		stop = (i+1)*2 + n
	}
	sub, err := getItem(newTuple(ops), &sliceObj{mkInt(int64(start)), mkInt(int64(stop)), none})
	if err != nil {
		return nil, err
	}
	return &Path{pathT: &TExpr{ops: append([]Value{ops[0]}, sub.(*tupleObj).items...)}}, nil
}

// slice is path[start:stop:step], following Path.__getitem__'s arithmetic on
// the flat __ops__ tuple.
func (p *Path) slice(startV, stopV, stepV Value) (*Path, error) {
	flat := p.pathT.ops
	ln := int64(len(flat))
	var start int64
	if !isNone(startV) {
		s, ok := asIntBig(startV)
		if !ok {
			return nil, typeErrf("unsupported operand type(s) for *: '" + typeName(startV) + "' and 'int'")
		}
		start = s.Int64()
	}
	if start >= 0 {
		start = start*2 + 1
	} else {
		start = start*2 + ln
	}
	var stop Value = none
	if !isNone(stopV) {
		s, ok := asIntBig(stopV)
		if !ok {
			return nil, typeErrf("unsupported operand type(s) for *: '" + typeName(stopV) + "' and 'int'")
		}
		v := s.Int64()
		if v >= 0 {
			v = v*2 + 1
		} else {
			v = v*2 + ln
		}
		stop = mkInt(v)
	}
	sub, err := getItem(newTuple(flat), &sliceObj{mkInt(start), stop, none})
	if err != nil {
		return nil, err
	}
	np := sub.(*tupleObj).items
	if st, ok := asIntBig(stepV); !isNone(stepV) && !(ok && st.Sign() == 1 && st.IsInt64() && st.Int64() == 1) {
		var pairs []Value
		for i := 0; i+1 < len(np); i += 2 {
			pairs = append(pairs, tup(np[i], np[i+1]))
		}
		sel, err := getItem(newTuple(pairs), &sliceObj{none, none, stepV})
		if err != nil {
			return nil, err
		}
		np = nil
		for _, pr := range sel.(*tupleObj).items {
			np = append(np, pr.(*tupleObj).items...)
		}
	}
	return &Path{pathT: &TExpr{ops: append([]Value{flat[0]}, np...)}}, nil
}

func (p *Path) repr() (string, error) { return formatPath(p.pathT.ops[1:]) }

// pathEq is Path.__eq__.
func pathEq(p *Path, other Value) (bool, error) {
	switch o := other.(type) {
	case *Path:
		return seqEq(p.pathT.ops, o.pathT.ops)
	case *TExpr:
		return seqEq(p.pathT.ops, o.ops)
	}
	return false, nil
}

// formatPath is _format_path over a flat list of operations and arguments.
func formatPath(tPath []Value) (string, error) {
	type part struct {
		val Value
		t   []Value
	}
	var parts []part
	var cur []Value
	for i := 0; i < len(tPath); i += 2 {
		if i+1 >= len(tPath) {
			return "", tupleIndexErr()
		}
		op, arg := tPath[i], tPath[i+1]
		if name, _ := opName(op); name == "P" {
			if len(cur) > 0 {
				parts = append(parts, part{t: cur})
				cur = nil
			}
			parts = append(parts, part{val: arg})
		} else {
			cur = append(cur, op, arg)
		}
	}
	if len(parts) > 0 && len(cur) > 0 {
		parts = append(parts, part{t: cur})
	}
	if len(parts) > 0 || len(cur) == 0 {
		strs := make([]string, len(parts))
		for i, pt := range parts {
			var err error
			if pt.t != nil {
				strs[i], err = formatT(pt.t, T)
			} else {
				strs[i], err = pyRepr(pt.val)
			}
			if err != nil {
				return "", err
			}
		}
		return "Path(" + strings.Join(strs, ", ") + ")", nil
	}
	return formatT(cur, T)
}

// ---------------------------------------------------------------- glom()

var (
	autoMode  = &modeFunc{name: "AUTO"}
	fillMode  = &modeFunc{name: "FILL"}
	matchMode = &modeFunc{name: "_glom_match"}
	groupMode = &modeFunc{name: "GROUP"}
)

func init() {
	autoMode.fn = autoFn
	fillMode.fn = fillFn
	matchMode.fn = glomMatch
	groupMode.fn = groupFn
}

// glomOptions are glom()'s keyword arguments; nil fields were not passed.
type glomOptions struct {
	def, skipExc, scope Value
}

func defaultScope(reg *registry) *dictObj {
	d := newDict()
	_ = d.set(glomFuncKey, glomFuncKey)
	_ = d.set(registryClass, &registryObj{reg})
	return d
}

// registryObj holds a registry in the scope.
type registryObj struct{ r *registry }

func (*registryObj) class() *classObj { return registryClass }

var defaultScopeMap = defaultScope(defaultRegistry)

// glomTop is the module-level glom(target, spec, **kwargs), given the base
// scope (the default scope, or a Glommer's).
func glomTop(target, spec Value, o glomOptions, base *dictObj) (Value, error) {
	var def Value = sentMissing
	if o.def != nil {
		def = o.def
	} else if o.skipExc != nil {
		def = none
	}
	var skipExc Value = emptyTuple
	if o.skipExc != nil {
		skipExc = o.skipExc
	} else if def != Value(sentMissing) {
		skipExc = glomErrorClass
	}
	root := newDict()
	_ = root.set(pathClass, newList(nil))
	_ = root.set(inspectClass, none)
	_ = root.set(sentMODE, autoMode)
	_ = root.set(sentMinMode, none)
	_ = root.set(sentChildErrors, newList(nil))
	_ = root.set(globalsKey, newScopeVars(newDict(), nil))
	scope := newScopeOf(root, base)
	scope.set(sentUP, scope)
	scope.set(sentROOT, scope)
	scope.set(T, target)
	if o.scope != nil {
		if err := scope.update(o.scope); err != nil {
			return nil, err
		}
	}
	ret, err := glomInner(target, spec, scope)
	if err != nil && excMatches(err, skipExc) {
		if def == Value(sentMissing) {
			// re-raised below
		} else {
			ret, err = def, nil
		}
	}
	if err == nil {
		return ret, nil
	}
	e := asExc(err)
	var out *excObj
	if e.cls.isSubclass(glomErrorClass) {
		out = copyGlomErr(e)
		out.g.wrapped = e
	} else {
		out = wrapExc(e)
	}
	last, _, _ := scope.maps[0].get(sentLastChild)
	finalize(out, e, last.(*scopeObj))
	return nil, out
}

// finalize is GlomError._finalize: the traceback's last lines (the raised
// exception's own lines) and the scope.
func finalize(out, raised *excObj, scope *scopeObj) {
	text := strings.TrimRight(formatExceptionOnly(raised), " \t\n\r\x0b\x0c")
	lines := pySplitlines(text)
	if len(lines) > 0 && onlyCaretChars(lines[0]) {
		lines = lines[1:]
	}
	out.g.tbLines = lines
	out.g.scope = scope
	out.g.finalized = nil
}

func onlyCaretChars(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '^' && r != '~' {
			return false
		}
	}
	return true
}

// glomInner is _glom.
func glomInner(target, spec Value, parent *scopeObj) (Value, error) {
	pmap := parent.maps[0]
	m := newDict()
	_ = m.set(T, target)
	_ = m.set(specClass, spec)
	_ = m.set(sentUP, parent)
	_ = m.set(sentChildErrors, newList(nil))
	mode, _, _ := pmap.get(sentMODE)
	minMode, _, _ := pmap.get(sentMinMode)
	if mode == nil {
		return nil, newExc(excKeyError, sentMODE)
	}
	if minMode == nil {
		return nil, newExc(excKeyError, sentMinMode)
	}
	_ = m.set(sentMODE, mode)
	_ = m.set(sentMinMode, minMode)
	scope := parent.newChild(m)
	_ = pmap.set(sentLastChild, scope)

	ret, err := glomDispatch(target, spec, scope)
	if err != nil {
		if errs, ok, _ := scope.maps[1].get(sentChildErrors); ok {
			l := errs.(*listObj)
			l.items = append(l.items, scope)
		}
		e := asExc(err)
		_ = scope.maps[0].set(sentCurError, e)
		if _, ok, _ := scope.maps[1].get(sentNoPyframe); ok {
			cur := scope.mustGet(sentUP).(*scopeObj)
			for {
				if _, ok, _ := cur.maps[0].get(sentNoPyframe); !ok {
					break
				}
				if len(cur.maps) < 2 {
					// cur_scope.maps[1] raises inside the except clause and
					// replaces the error (a scope flattened by Spec.glom).
					return nil, newExc(excIndexError, mkStr("list index out of range"))
				}
				if errs, ok, _ := cur.maps[1].get(sentChildErrors); ok {
					l := errs.(*listObj)
					l.items = append(l.items, cur)
				}
				_ = cur.maps[0].set(sentCurError, e)
				cur = cur.mustGet(sentUP).(*scopeObj)
			}
		}
		return nil, e
	}
	return ret, nil
}

func glomDispatch(target, spec Value, scope *scopeObj) (Value, error) {
	if t, ok := spec.(*TExpr); ok {
		scope.set(sentMinMode, none)
		return tEval(target, t, scope)
	}
	if g, ok := spec.(glomiter); ok {
		scope.set(sentMinMode, none)
		return g.glomit(target, scope)
	}
	mm, _, _ := scope.maps[0].get(sentMinMode)
	mode, _, _ := scope.maps[0].get(sentMODE)
	fn := mode
	if t, _ := truthy(mm); t {
		fn = mm
	}
	if mf, ok := fn.(*modeFunc); ok {
		return mf.fn(target, spec, scope)
	}
	return callValue(fn, []Value{target, spec, scope}, nil)
}

func recurse(target, spec Value, scope *scopeObj) (Value, error) {
	return glomInner(target, spec, scope)
}

// autoFn is AUTO.
func autoFn(target, spec Value, scope *scopeObj) (Value, error) {
	switch s := spec.(type) {
	case *strObj:
		return tEval(target, pathFromText(s.s).pathT, scope)
	case *dictObj:
		return handleDict(target, s, scope)
	case *listObj:
		return handleList(target, s, scope)
	case *tupleObj:
		return handleTuple(target, s.items, scope)
	}
	if callable(spec) {
		return callValue(spec, []Value{target}, nil)
	}
	r, err := pyRepr(spec)
	if err != nil {
		return nil, err
	}
	return nil, typeErrf("expected spec to be dict, list, tuple, callable, string, or other Spec-like type, not: " + r)
}

func handleDict(target Value, spec *dictObj, scope *scopeObj) (Value, error) {
	ret := newDict()
	for _, e := range spec.items() {
		val, err := recurse(target, e.val, scope)
		if err != nil {
			return nil, err
		}
		if val == Value(sentSKIP) {
			continue
		}
		field := e.key
		switch field.(type) {
		case *TExpr, *specObj:
			if field, err = recurse(target, field, scope); err != nil {
				return nil, err
			}
		}
		if err := ret.set(field, val); err != nil {
			return nil, err
		}
	}
	return ret, nil
}

func scopePath(scope *scopeObj) *listObj {
	v := scope.mustGet(pathClass)
	l, _ := v.(*listObj)
	if l == nil {
		l = newList(nil)
	}
	return l
}

func scopeRegistry(scope *scopeObj) *registry {
	return scope.mustGet(registryClass).(*registryObj).r
}

// iterTarget is the 'iterate' lookup and call shared by list specs, Iter,
// Group and Fold.
func iterTarget(target Value, scope *scopeObj) (iterator, error) {
	path := scopePath(scope)
	h, err := scopeRegistry(scope).getHandler("iterate", target, path)
	if err != nil {
		return nil, err
	}
	it, err := callHandler(h, target)
	if err != nil {
		p, perr := newPathFrom(path.items...)
		var pr string
		if perr == nil {
			pr, _ = pyRepr(p)
		}
		tn, _ := pyRepr(mkStr(typeName(target)))
		er, _ := pyRepr(asExc(err))
		return nil, typeErrf("failed to iterate on instance of type " + tn + " at " + pr + " (got " + er + ")")
	}
	return it.(*iterObj), nil
}

func handleList(target Value, spec *listObj, scope *scopeObj) (Value, error) {
	if len(spec.items) == 0 {
		return nil, newExc(excIndexError, mkStr("list index out of range"))
	}
	subspec := spec.items[0]
	it, err := iterTarget(target, scope)
	if err != nil {
		return nil, err
	}
	ret := newList(nil)
	base := scopePath(scope)
	for i := 0; ; i++ {
		t, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		scope.set(pathClass, newList(append(append([]Value(nil), base.items...), mkInt(int64(i)))))
		val, err := recurse(t, subspec, scope)
		if err != nil {
			return nil, err
		}
		if val == Value(sentSKIP) {
			continue
		}
		if val == Value(sentSTOP) {
			break
		}
		ret.items = append(ret.items, val)
	}
	return ret, nil
}

// pathName is getattr(subspec, '__name__', subspec).
func pathName(subspec Value) Value {
	if n, ok := funcName(subspec); ok {
		return mkStr(n)
	}
	return subspec
}

func handleTuple(target Value, spec []Value, scope *scopeObj) (Value, error) {
	res := target
	for _, subspec := range spec {
		scope = chainChild(scope)
		nxt, err := recurse(res, subspec, scope)
		if err != nil {
			return nil, err
		}
		if nxt == Value(sentSKIP) {
			continue
		}
		if nxt == Value(sentSTOP) {
			break
		}
		res = nxt
		if _, isList := subspec.(*listObj); !isList {
			l := scopePath(scope)
			l.items = append(l.items, pathName(subspec))
			scope.set(pathClass, l)
		}
	}
	return res, nil
}

// chainChild is chain_child.
func chainChild(scope *scopeObj) *scopeObj {
	last, ok, _ := scope.maps[0].get(sentLastChild)
	if !ok {
		return scope
	}
	nxt := scope.mustGet(sentLastChild).(*scopeObj)
	_ = last
	_ = nxt.maps[0].set(sentNoPyframe, trueObj)
	if errs, ok, _ := nxt.maps[0].get(sentChildErrors); ok {
		errs.(*listObj).items = nil
	}
	return nxt
}

// fillFn is FILL.
func fillFn(target, spec Value, scope *scopeObj) (Value, error) {
	switch s := spec.(type) {
	case *dictObj:
		out := newDict()
		for _, e := range s.items() {
			k, err := recurse(target, e.key, scope)
			if err != nil {
				return nil, err
			}
			v, err := recurse(target, e.val, scope)
			if err != nil {
				return nil, err
			}
			if err := out.set(k, v); err != nil {
				return nil, err
			}
		}
		return out, nil
	case *listObj, *tupleObj, *setObj:
		items, _ := toSlice(spec)
		result := make([]Value, len(items))
		for i, it := range items {
			v, err := recurse(target, it, scope)
			if err != nil {
				return nil, err
			}
			result[i] = v
		}
		switch x := spec.(type) {
		case *listObj:
			return newList(result), nil
		case *tupleObj:
			return newTuple(result), nil
		case *setObj:
			out := newSet(x.frozen)
			for _, v := range result {
				if err := out.add(v); err != nil {
					return nil, err
				}
			}
			return out, nil
		}
	}
	if callable(spec) {
		return callValue(spec, []Value{target}, nil)
	}
	return spec, nil
}

// argValuator is _ArgValuator: its mode keeps a cache of the lists and dicts
// it has rebuilt.
type argValuator struct {
	cache map[Value]Value
}

func newArgMode() *modeFunc {
	av := &argValuator{cache: map[Value]Value{}}
	return &modeFunc{name: "mode", fn: av.mode}
}

func (av *argValuator) mode(target, spec Value, scope *scopeObj) (Value, error) {
	switch s := spec.(type) {
	case *listObj:
		if r, ok := av.cache[spec]; ok {
			return r, nil
		}
		res := newList(nil)
		av.cache[spec] = res
		var vals []Value
		for _, it := range s.items {
			v, err := recurse(target, it, scope)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
		res.items = append(res.items, vals...)
		return res, nil
	case *dictObj:
		if r, ok := av.cache[spec]; ok {
			return r, nil
		}
		res := newDict()
		av.cache[spec] = res
		tmp := newDict()
		for _, e := range s.items() {
			k, err := recurse(target, e.key, scope)
			if err != nil {
				return nil, err
			}
			v, err := recurse(target, e.val, scope)
			if err != nil {
				return nil, err
			}
			if err := tmp.set(k, v); err != nil {
				return nil, err
			}
		}
		for _, e := range tmp.items() {
			_ = res.set(e.key, e.val)
		}
		return res, nil
	case *tupleObj:
		vals := make([]Value, len(s.items))
		for i, it := range s.items {
			v, err := recurse(target, it, scope)
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		return newTuple(vals), nil
	case *setObj:
		var vals []Value
		for _, it := range s.items() {
			v, err := recurse(target, it, scope)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
		out := newSet(s.frozen)
		for _, v := range vals {
			if err := out.add(v); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return spec, nil
}

// argVal is arg_val: the argument evaluated in argument mode. MIN_MODE is not
// restored when the evaluation raises, as in the original.
func argVal(target, arg Value, scope *scopeObj) (Value, error) {
	mode := scope.mustGet(sentMinMode)
	scope.set(sentMinMode, newArgMode())
	res, err := recurse(target, arg, scope)
	if err != nil {
		return nil, err
	}
	scope.set(sentMinMode, mode)
	return res, nil
}

// ---------------------------------------------------------------- T evaluation

// sFirstMagic is _s_first_magic.
func sFirstMagic(scope *scopeObj, key Value, t *TExpr) (Value, error) {
	v, ok := scope.get(key)
	if !ok {
		p, _ := newPathFrom(t)
		return nil, newPAE(newExc(excKeyError, key), p, 0)
	}
	return v, nil
}

func pathOf(t *TExpr) *Path {
	p, _ := newPathFrom(t)
	return p
}

// tEval is _t_eval over the flat __ops__ tuple.
func tEval(target Value, t *TExpr, scope *scopeObj) (Value, error) {
	tPath := t.ops
	i := 1
	fetchTill := len(tPath)
	root := tPath[0]
	var cur Value
	switch root {
	case Value(T):
		cur = target
	case Value(S), Value(A):
		if root == Value(A) {
			fetchTill -= 2
			if fetchTill < 1 {
				return nil, newBadSpec("cannot assign without destination")
			}
		}
		cur = scope
		first, _ := opName(tPath[min(1, len(tPath)-1)])
		if fetchTill > 1 && (first == "." || first == "P") {
			v, err := sFirstMagic(scope, tPath[2], t)
			if err != nil {
				return nil, err
			}
			cur = v
			i += 2
		} else if root == Value(S) && fetchTill > 1 && first == "(" {
			kw, ok := tPath[2].(*tupleObj)
			if !ok || len(kw.items) != 2 {
				return nil, typeErrf("cannot unpack non-iterable " + typeName(tPath[2]) + " object")
			}
			d, ok := kw.items[1].(*dictObj)
			if !ok {
				return nil, attrErr(kw.items[1], "items")
			}
			for _, e := range d.items() {
				v, err := argVal(target, e.val, scope)
				if err != nil {
					return nil, err
				}
				scope.set(e.key, v)
			}
			return target, nil
		}
	default:
		return nil, valueErrf("TType instance with invalid root")
	}
	for i < fetchTill {
		if i+1 >= len(tPath) {
			return nil, tupleIndexErr()
		}
		opv := tPath[i]
		arg, err := argVal(target, tPath[i+1], scope)
		if err != nil {
			return nil, err
		}
		op, isStr := opName(opv)
		partIdx := i / 2
		var pae error
		switch {
		case isStr && op == ".":
			name, ok := arg.(*strObj)
			if !ok {
				return nil, typeErrf("attribute name must be string, not '" + typeName(arg) + "'")
			}
			v, err := getAttr(cur, name.s)
			if err != nil {
				if !isExcInstance(err, excAttributeError) {
					return nil, err
				}
				pae = newPAE(err, pathOf(t), partIdx)
			} else {
				cur = v
			}
		case isStr && op == "[":
			v, err := getItem(cur, arg)
			if err != nil {
				if !(isExcInstance(err, excKeyError) || isExcInstance(err, excIndexError) || isExcInstance(err, excTypeError)) {
					return nil, err
				}
				pae = newPAE(err, pathOf(t), partIdx)
			} else {
				cur = v
			}
		case isStr && op == "P":
			var vals []Value
			for k := 2; k < i+2 && k < len(tPath); k += 2 {
				vals = append(vals, tPath[k])
			}
			h, err := scopeRegistry(scope).getHandler("get", cur, newTuple(vals))
			if err != nil {
				return nil, err
			}
			v, err := callHandler(h, cur, arg)
			if err != nil {
				pae = newPAE(err, pathOf(t), partIdx)
			} else {
				cur = v
			}
		case !isStr:
			return nil, typeErrf("'in <string>' requires string as left operand, not " + typeName(opv))
		case strings.Contains("xX", op):
			var nxt []Value
			reg := scopeRegistry(scope)
			nxt = extendChildren(nxt, cur, reg)
			if op == "X" {
				seen := map[Value]bool{}
				for k := 0; k < len(nxt); k++ {
					item := nxt[k]
					if !seen[item] {
						seen[item] = true
						nxt = extendChildren(nxt, item, reg)
					}
				}
				nxt = append([]Value{cur}, nxt...)
			}
			out := newList(nil)
			todo := &TExpr{ops: append([]Value{root}, tPath[i+2:]...)}
			for _, child := range nxt {
				v, err := tEval(child, todo, scope)
				if err != nil {
					if isExcInstance(err, pathAccessClass) {
						continue
					}
					return nil, err
				}
				out.items = append(out.items, v)
			}
			cur = out
			i = fetchTill
			continue
		case op == "(":
			a, ok := arg.(*tupleObj)
			if !ok || len(a.items) != 2 {
				return nil, typeErrf("cannot unpack non-iterable " + typeName(arg) + " object")
			}
			var vals []Value
			for k := 2; k < i+2 && k < len(tPath); k += 2 {
				vals = append(vals, tPath[k])
			}
			l := scopePath(scope)
			l.items = append(l.items, vals...)
			scope.set(pathClass, l)
			c, err := newCall(cur, a.items[0], a.items[1])
			if err != nil {
				return nil, err
			}
			v, err := recurse(target, c, scope)
			if err != nil {
				return nil, err
			}
			cur = v
		default:
			var v Value
			switch op {
			case "+", "-", "*", "/", "%", "&", "|", "^":
				v, err = binaryOp(op, cur, arg)
			case ":":
				v, err = binaryOp("**", cur, arg)
			case "~":
				v, err = unaryOp("invert", cur)
			case "_":
				v, err = unaryOp("neg", cur)
			default:
				// '#' (floor division) and unknown operations leave the target as it is.
				v = cur
			}
			if err != nil {
				if !(isExcInstance(err, excTypeError) || isExcInstance(err, excZeroDivision)) {
					return nil, err
				}
				pae = newPAE(err, pathOf(t), partIdx)
			} else {
				cur = v
			}
		}
		if pae != nil {
			return nil, pae
		}
		i += 2
	}
	if root == Value(A) {
		n := len(tPath)
		op, arg := tPath[n-2], tPath[n-1]
		if cur == Value(scope) {
			op = opV("[")
		}
		name, _ := opName(op)
		if err := assignOp(cur, name, arg, target, t, scope); err != nil {
			return nil, err
		}
		return target, nil
	}
	return cur, nil
}

// assignOp is _assign_op.
func assignOp(dest Value, op string, arg, val Value, path Value, scope *scopeObj) error {
	switch op {
	case "[":
		return setItem(dest, arg, val)
	case ".":
		name, ok := arg.(*strObj)
		if !ok {
			return typeErrf("attribute name must be string, not '" + typeName(arg) + "'")
		}
		return setAttr(dest, name.s, val)
	case "P":
		h, err := scopeRegistry(scope).getHandler("assign", dest, nil)
		if err != nil {
			return err
		}
		if _, err := callHandler(h, dest, arg, val); err != nil {
			pv := path
			if t, ok := path.(*TExpr); ok {
				pv = t
			}
			return newGlomErr(pathAssignClass, &glomErrData{exc: asExc(err), path: pv, destName: arg},
				asExc(err), pv, arg)
		}
		return nil
	}
	return valueErrf("unsupported T operation for assignment")
}

// extendChildren is _extend_children.
func extendChildren(children []Value, item Value, reg *registry) []Value {
	kh, err1 := reg.getHandler("keys", item, nil)
	var gh handlerKind
	var err2 error
	if err1 == nil {
		gh, err2 = reg.getHandler("get", item, nil)
	}
	if err1 != nil || err2 != nil {
		ih, err := reg.getHandler("iterate", item, nil)
		if err != nil {
			return children
		}
		it, err := callHandler(ih, item)
		if err != nil {
			return children
		}
		items, err := toSlice(it)
		if err != nil {
			return append(children, items...)
		}
		return append(children, items...)
	}
	keys, err := callHandler(kh, item)
	if err != nil {
		return children
	}
	it, err := pyIter(keys)
	if err != nil {
		return children
	}
	for {
		k, ok, err := it.next()
		if err != nil || !ok {
			return children
		}
		v, err := callHandler(gh, item, k)
		if err == nil {
			children = append(children, v)
		}
	}
}

// ---------------------------------------------------------------- Spec, Val, ...

// specObj is glom's Spec.
type specObj struct {
	spec  Value
	scope *dictObj
}

func (*specObj) class() *classObj { return specClass }

func (s *specObj) glomit(target Value, scope *scopeObj) (Value, error) {
	for _, e := range s.scope.items() {
		scope.set(e.key, e.val)
	}
	return recurse(target, s.spec, scope)
}

// glomCall is Spec.glom(target, scope=scope): the spec's scope and the
// given scope flattened into one map, evaluated with _glom.
func (s *specObj) glomCall(target Value, scope Value) (Value, error) {
	m := s.scope.copy()
	if sc, ok := scope.(*scopeObj); ok {
		for _, k := range sc.keyList() {
			_ = m.set(k, sc.mustGet(k))
		}
	} else if d, ok := scope.(*dictObj); ok {
		for _, e := range d.items() {
			_ = m.set(e.key, e.val)
		}
	}
	return glomInner(target, s.spec, newScopeOf(m))
}

func (s *specObj) repr() (string, error) {
	sp, err := bbrepr(s.spec)
	if err != nil {
		return "", err
	}
	if s.scope.n > 0 {
		sc, err := pyRepr(s.scope)
		return "Spec(" + sp + ", scope=" + sc + ")", err
	}
	return "Spec(" + sp + ")", nil
}

// valObj is Val.
type valObj struct{ value Value }

var valClass = glomClass("Val", "glom.core", objectClass)

func (*valObj) class() *classObj { return valClass }

func (v *valObj) glomit(target Value, scope *scopeObj) (Value, error) { return v.value, nil }

func (v *valObj) repr() (string, error) {
	s, err := bbrepr(v.value)
	return "Val(" + s + ")", err
}

// scopeVarsObj is ScopeVars.
type scopeVarsObj struct{ d *dictObj }

var scopeVarsClass = func() *classObj {
	c := glomClass("ScopeVars", "glom.core", objectClass)
	c.iterable = true
	return c
}()

func (*scopeVarsObj) class() *classObj { return scopeVarsClass }

func newScopeVars(base *dictObj, defaults []kwArg) *scopeVarsObj {
	d := base.copy()
	for _, k := range defaults {
		d.setStr(k.name, k.val)
	}
	return &scopeVarsObj{d: d}
}

func (s *scopeVarsObj) repr() (string, error) {
	r, err := bbrepr(s.d)
	return "ScopeVars(" + r + ")", err
}

// varsObj is Vars.
type varsObj struct {
	base     Value
	defaults []kwArg
}

var varsClass = glomClass("Vars", "glom.core", objectClass)

func (*varsObj) class() *classObj { return varsClass }

func (v *varsObj) glomit(target Value, scope *scopeObj) (Value, error) {
	d := newDict()
	if err := dictUpdateFrom(d, v.base); err != nil {
		return nil, err
	}
	return newScopeVars(d, v.defaults), nil
}

func (v *varsObj) repr() (string, error) {
	var args []Value
	if t, _ := truthy(v.base); t {
		args = []Value{v.base}
	}
	return formatInvocation("Vars", args, v.defaults, true)
}

// refObj is Ref.
type refObj struct {
	name   Value
	sub    Value
	hasSub bool
}

func (*refObj) class() *classObj { return refClass }

func (r *refObj) glomit(target Value, scope *scopeObj) (Value, error) {
	key := tup(refClass, r.name)
	sub := r.sub
	if !r.hasSub {
		v, ok := scope.get(key)
		if !ok {
			return nil, newExc(excKeyError, key)
		}
		sub = v
	} else {
		scope.set(key, sub)
	}
	return recurse(target, sub, scope)
}

func (r *refObj) repr() (string, error) {
	if !r.hasSub {
		s, err := bbrepr(r.name)
		return "Ref(" + s + ")", err
	}
	s, err := bbrepr(tup(r.name, r.sub))
	if err != nil {
		return "", err
	}
	rs := []rune(s)
	if len(rs) >= 2 {
		rs = rs[1 : len(rs)-1]
	}
	return "Ref(" + string(rs) + ")", nil
}

// modeSpec is Auto or Fill.
type modeSpec struct {
	name string
	spec Value
}

var (
	autoClass = glomClass("Auto", "glom.core", objectClass)
	fillClass = glomClass("Fill", "glom.core", objectClass)
)

func (m *modeSpec) class() *classObj {
	if m.name == "Fill" {
		return fillClass
	}
	return autoClass
}

func (m *modeSpec) glomit(target Value, scope *scopeObj) (Value, error) {
	if m.name == "Fill" {
		scope.set(sentMODE, fillMode)
	} else {
		scope.set(sentMODE, autoMode)
	}
	return recurse(target, m.spec, scope)
}

func (m *modeSpec) repr() (string, error) {
	if isNone(m.spec) {
		return m.name + "()", nil
	}
	s, err := bbrepr(m.spec)
	return m.name + "(" + s + ")", err
}

// pipeObj is Pipe.
type pipeObj struct{ steps *tupleObj }

var pipeClass = glomClass("Pipe", "glom.core", objectClass)

func (*pipeObj) class() *classObj { return pipeClass }

func (p *pipeObj) glomit(target Value, scope *scopeObj) (Value, error) {
	return handleTuple(target, p.steps.items, scope)
}

func (p *pipeObj) repr() (string, error) {
	s, err := bbrepr(p.steps)
	return "Pipe" + s, err
}

// callObj is Call.
type callObj struct {
	fn, args, kwargs Value
}

var callClass = glomClass("Call", "glom.core", objectClass)

func (*callObj) class() *classObj { return callClass }

// newCall is Call(func, args, kwargs); nil arguments were not passed.
func newCall(fn, args, kwargs Value) (*callObj, error) {
	if fn == nil || isNone(fn) {
		fn = T
	}
	_, isSpec := fn.(*specObj)
	if !(callable(fn) || isSpec) {
		r, err := pyRepr(fn)
		if err != nil {
			return nil, err
		}
		return nil, typeErrf("expected func to be a callable or T expression, not: " + r)
	}
	if args == nil || isNone(args) {
		args = emptyTuple
	}
	if kwargs == nil || isNone(kwargs) {
		kwargs = newDict()
	}
	return &callObj{fn: fn, args: args, kwargs: kwargs}, nil
}

func (c *callObj) glomit(target Value, scope *scopeObj) (Value, error) {
	fn, err := argVal(target, c.fn, scope)
	if err != nil {
		return nil, err
	}
	args, err := argVal(target, c.args, scope)
	if err != nil {
		return nil, err
	}
	kwargs, err := argVal(target, c.kwargs, scope)
	if err != nil {
		return nil, err
	}
	pos, err := starArgs(fn, args)
	if err != nil {
		return nil, err
	}
	kw, err := starKwargs(fn, kwargs)
	if err != nil {
		return nil, err
	}
	return callValue(fn, pos, kw)
}

// starArgs is the *args of a call.
func starArgs(fn, args Value) ([]Value, error) {
	items, err := toSlice(args)
	if err != nil {
		name := callableDesc(fn)
		return nil, typeErrf(name + " argument after * must be an iterable, not " + typeName(args))
	}
	return items, nil
}

// starKwargs is the **kwargs of a call.
func starKwargs(fn, kwargs Value) ([]kwArg, error) {
	d, ok := kwargs.(*dictObj)
	if !ok {
		return nil, typeErrf(callableDesc(fn) + " argument after ** must be a mapping, not " + typeName(kwargs))
	}
	var out []kwArg
	for _, e := range d.items() {
		k, ok := e.key.(*strObj)
		if !ok {
			return nil, typeErrf("keywords must be strings")
		}
		out = append(out, kwArg{k.s, e.val})
	}
	return out, nil
}

// callableDesc is how CPython names a callable in call errors.
func callableDesc(fn Value) string {
	switch f := fn.(type) {
	case *builtinFunc:
		if f.self != nil {
			return typeName(f.self) + "." + f.name + "()"
		}
		return f.name + "()"
	case *methodDescr:
		return f.owner.name + "." + f.name + "()"
	case *classObj:
		return f.name + "()"
	}
	return typeName(fn) + " object"
}

func (c *callObj) repr() (string, error) {
	f, err := bbrepr(c.fn)
	if err != nil {
		return "", err
	}
	a, err := pyRepr(c.args)
	if err != nil {
		return "", err
	}
	k, err := pyRepr(c.kwargs)
	return "Call(" + f + ", args=" + a + ", kwargs=" + k + ")", err
}

// invokeObj is Invoke.
type invokeObj struct {
	fn   Value
	args []invokeStep
	cur  map[string]*invokeStep // _cur_kwargs: the step that last set each kwarg
}

type invokeStep struct {
	op   string // "C", "S", "*"
	args []Value
	kw   []kwArg
	star [2]Value // star args and kwargs specs (nil when not passed)
}

var invokeClass = glomClass("Invoke", "glom.core", objectClass)

func (*invokeObj) class() *classObj { return invokeClass }

func newInvoke(fn Value) (*invokeObj, error) {
	_, isSpec := fn.(*specObj)
	if !callable(fn) && !isSpec {
		r, err := pyRepr(fn)
		if err != nil {
			return nil, err
		}
		return nil, typeErrf("expected func to be a callable or Spec instance, not: " + r)
	}
	return &invokeObj{fn: fn, cur: map[string]*invokeStep{}}, nil
}

func (inv *invokeObj) add(step *invokeStep) *invokeObj {
	n := &invokeObj{fn: inv.fn, cur: map[string]*invokeStep{}}
	n.args = append(append([]invokeStep(nil), inv.args...), *step)
	for k, v := range inv.cur {
		n.cur[k] = v
	}
	last := &n.args[len(n.args)-1]
	// The steps are copied, so _cur_kwargs points at the copies by index.
	for i := range n.args[:len(n.args)-1] {
		for k, v := range inv.cur {
			if v == &inv.args[i] {
				n.cur[k] = &n.args[i]
			}
		}
	}
	if step.op != "*" {
		for _, k := range step.kw {
			n.cur[k.name] = last
		}
	}
	return n
}

func (inv *invokeObj) glomit(target Value, scope *scopeObj) (Value, error) {
	var allArgs []Value
	allKw := newDict()
	fn := inv.fn
	if s, ok := fn.(*specObj); ok {
		v, err := recurse(target, s, scope)
		if err != nil {
			return nil, err
		}
		fn = v
	}
	for i := range inv.args {
		st := &inv.args[i]
		switch st.op {
		case "C":
			allArgs = append(allArgs, st.args...)
			for _, k := range st.kw {
				if inv.cur[k.name] == st {
					allKw.setStr(k.name, k.val)
				}
			}
		case "S":
			for _, a := range st.args {
				v, err := recurse(target, a, scope)
				if err != nil {
					return nil, err
				}
				allArgs = append(allArgs, v)
			}
			for _, k := range st.kw {
				if inv.cur[k.name] == st {
					v, err := recurse(target, k.val, scope)
					if err != nil {
						return nil, err
					}
					allKw.setStr(k.name, v)
				}
			}
		case "*":
			if st.star[0] != nil {
				v, err := recurse(target, st.star[0], scope)
				if err != nil {
					return nil, err
				}
				items, err := toSlice(v)
				if err != nil {
					return nil, err
				}
				allArgs = append(allArgs, items...)
			}
			if st.star[1] != nil {
				v, err := recurse(target, st.star[1], scope)
				if err != nil {
					return nil, err
				}
				if err := dictUpdateFrom(allKw, v); err != nil {
					return nil, err
				}
			}
		}
	}
	kw, err := starKwargs(fn, allKw)
	if err != nil {
		return nil, err
	}
	return callValue(fn, allArgs, kw)
}

func (inv *invokeObj) repr() (string, error) {
	base := "Invoke"
	var arg Value = inv.fn
	if s, ok := inv.fn.(*specObj); ok {
		base += ".specfunc"
		arg = s.spec
	}
	first, err := formatInvocation(base, []Value{arg}, nil, false)
	if err != nil {
		return "", err
	}
	chunks := []string{first}
	names := map[string]string{"C": "constants", "S": "specs", "*": "star"}
	for i := range inv.args {
		st := &inv.args[i]
		var s string
		if st.op == "*" {
			var kw []kwArg
			if st.star[0] != nil {
				kw = append(kw, kwArg{"args", st.star[0]})
			}
			if st.star[1] != nil {
				kw = append(kw, kwArg{"kwargs", st.star[1]})
			}
			s, err = formatInvocation(names[st.op], nil, kw, true)
		} else {
			var kw []kwArg
			for _, k := range st.kw {
				if inv.cur[k.name] == st {
					kw = append(kw, k)
				}
			}
			s, err = formatInvocation(names[st.op], st.args, kw, false)
		}
		if err != nil {
			return "", err
		}
		chunks = append(chunks, "."+s)
	}
	return strings.Join(chunks, ""), nil
}

// coalesceObj is Coalesce.
type coalesceObj struct {
	subspecs   []Value
	origKw     []kwArg
	def        Value
	defFactory Value
	skip       Value
	skipExc    Value
}

var coalesceClass = glomClass("Coalesce", "glom.core", objectClass)

func (*coalesceObj) class() *classObj { return coalesceClass }

func newCoalesce(subspecs []Value, kw []kwArg) (*coalesceObj, error) {
	c := &coalesceObj{subspecs: subspecs, origKw: kw, def: sentMissing, defFactory: sentMissing,
		skip: sentMissing, skipExc: glomErrorClass}
	for _, k := range kw {
		switch k.name {
		case "default":
			c.def = k.val
		case "default_factory":
			c.defFactory = k.val
		case "skip":
			c.skip = k.val
		case "skip_exc":
			c.skipExc = k.val
		}
	}
	a, err := truthy(c.def)
	if err != nil {
		return nil, err
	}
	if a {
		b, err := truthy(c.defFactory)
		if err != nil {
			return nil, err
		}
		if b {
			return nil, valueErrf(`expected one of "default" or "default_factory", not both`)
		}
	}
	return c, nil
}

func (c *coalesceObj) skipped(v Value) (bool, error) {
	switch {
	case c.skip == Value(sentMissing):
		return false, nil
	case callable(c.skip):
		r, err := callValue(c.skip, []Value{v}, nil)
		if err != nil {
			return false, err
		}
		return truthy(r)
	}
	if t, ok := c.skip.(*tupleObj); ok {
		return seqContains(t.items, v)
	}
	r, err := pyEq(v, c.skip)
	return r, err
}

func (c *coalesceObj) glomit(target Value, scope *scopeObj) (Value, error) {
	skipped := newList(nil)
	for _, sub := range c.subspecs {
		ret, err := recurse(target, sub, scope)
		if err != nil {
			if excMatches(err, c.skipExc) {
				skipped.items = append(skipped.items, asExc(err))
				continue
			}
			return nil, err
		}
		skip, err := c.skipped(ret)
		if err != nil {
			// skip runs inside the same try as the subspec.
			if excMatches(err, c.skipExc) {
				skipped.items = append(skipped.items, asExc(err))
				continue
			}
			return nil, err
		}
		if !skip {
			return ret, nil
		}
		skipped.items = append(skipped.items, ret)
	}
	if c.def != Value(sentMissing) {
		return argVal(target, c.def, scope)
	}
	if c.defFactory != Value(sentMissing) {
		return callValue(c.defFactory, nil, nil)
	}
	return nil, newGlomErr(coalesceErrorClass, &glomErrData{coal: c, skipped: skipped, path: scope.mustGet(pathClass)},
		c, skipped, scope.mustGet(pathClass))
}

func (c *coalesceObj) repr() (string, error) {
	return formatInvocation("Coalesce", c.subspecs, c.origKw, true)
}

func coalesceMessage(e *excObj) (string, error) {
	c := e.g.coal
	missed, err := pyRepr(newTuple(c.subspecs))
	if err != nil {
		return "", err
	}
	var vals []string
	for _, v := range e.g.skipped.items {
		if isinstanceSpec(v, c.skipExc) {
			vals = append(vals, v.class().name)
		} else {
			vals = append(vals, "<skipped "+v.class().name+">")
		}
	}
	msg := "no valid values found. Tried " + missed + " and got (" + strings.Join(vals, ", ") + ")"
	if c.skip != Value(sentMissing) {
		s, err := pyRepr(c.skip)
		if err != nil {
			return "", err
		}
		msg += ", skip set to " + s
	}
	if c.skipExc != Value(glomErrorClass) {
		s, err := pyRepr(c.skipExc)
		if err != nil {
			return "", err
		}
		msg += ", skip_exc set to " + s
	}
	if !isNone(e.g.path) {
		s, err := pyRepr(e.g.path)
		if err != nil {
			return "", err
		}
		msg += " (at path " + s + ")"
	}
	return msg, nil
}

func itoa(i int) string { return strItoa(int64(i)) }
