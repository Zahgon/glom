// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom's reduction and grouping modules: Fold, Sum, Flatten,
// Merge, flatten() and merge(); Group with First, Avg, Max, Min and Limit.

package glom

var (
	foldClass    = glomClass("Fold", "glom.reduction", objectClass)
	sumClass     = glomClass("Sum", "glom.reduction", foldClass)
	flattenClass = glomClass("Flatten", "glom.reduction", foldClass)
	mergeClass   = glomClass("Merge", "glom.reduction", foldClass)
	groupClass   = glomClass("Group", "glom.grouping", objectClass)
	gFirstClass  = glomClass("First", "glom.grouping", objectClass)
	avgClass     = glomClass("Avg", "glom.grouping", objectClass)
	maxClass     = glomClass("Max", "glom.grouping", objectClass)
	minClass     = glomClass("Min", "glom.grouping", objectClass)
	limitClass   = glomClass("Limit", "glom.grouping", objectClass)
)

func init() {
	for _, c := range []*classObj{gFirstClass, avgClass, maxClass, minClass, limitClass} {
		c.hasDict = false
	}
}

func iaddFn() Value { return builtins["operator.iadd"] }

// foldObj is Fold and its subtypes Sum, Flatten and Merge.
type foldObj struct {
	kind    string // "Fold", "Sum", "Flatten", "Merge"
	subspec Value
	init    Value
	op      Value
	lazy    bool
}

func (f *foldObj) class() *classObj {
	switch f.kind {
	case "Sum":
		return sumClass
	case "Flatten":
		return flattenClass
	case "Merge":
		return mergeClass
	}
	return foldClass
}

func foldCheck(kind string, init, op Value) error {
	if !callable(op) {
		r, err := pyRepr(op)
		if err != nil {
			return err
		}
		return typeErrf("expected callable for " + kind + " op param, not: " + r)
	}
	if !callable(init) {
		r, err := pyRepr(init)
		if err != nil {
			return err
		}
		return typeErrf("expected callable for " + kind + " init param, not: " + r)
	}
	return nil
}

func newFold(kind string, subspec, init, op Value) (*foldObj, error) {
	f := &foldObj{kind: kind, subspec: subspec, init: init, op: op}
	if f.subspec == nil {
		f.subspec = T
	}
	switch kind {
	case "Fold":
		if f.op == nil {
			f.op = iaddFn()
		}
	case "Sum":
		if f.init == nil {
			f.init = intClass
		}
		f.op = iaddFn()
	case "Flatten":
		if f.init == nil {
			f.init = listClass
		}
		if s, ok := f.init.(*strObj); ok && s.s == "lazy" {
			f.lazy = true
			f.init = listClass
		}
		f.op = iaddFn()
	case "Merge":
		if f.init == nil {
			f.init = dictClass
		}
		if f.op == nil || isNone(f.op) {
			f.op = mkStr("update")
		}
		if name, ok := f.op.(*strObj); ok {
			test, err := callValue(f.init, nil, nil)
			if err != nil {
				return nil, err
			}
			f.op = none
			if m, ok := test.class().methods[name.s]; ok {
				f.op = m
			}
		}
		if !callable(f.op) {
			a, err := pyRepr(f.op)
			if err != nil {
				return nil, err
			}
			b, err := pyRepr(f.init)
			if err != nil {
				return nil, err
			}
			return nil, valueErrf(`expected callable "op" arg or an "init" with an .update() method not ` + a + " and " + b)
		}
	}
	if err := foldCheck(kind, f.init, f.op); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *foldObj) glomit(target Value, scope *scopeObj) (Value, error) {
	isAgg := false
	if scope.mustGet(sentMODE) == Value(groupMode) {
		if cur, ok := scope.get(sentCurAgg); !ok || isNone(cur) {
			scope.set(sentCurAgg, f)
			isAgg = true
		}
	}
	if f.subspec != Value(T) {
		t, err := recurse(target, f.subspec, scope)
		if err != nil {
			return nil, err
		}
		target = t
	}
	if isAgg {
		return f.agg(target, scope.mustGet(sentAccTree).(*dictObj))
	}
	it, err := iterTarget(target, scope)
	if err != nil {
		if e, ok := err.(*excObj); ok && e.cls.isSubclass(unregisteredClass) {
			s, serr := excStr(e)
			if serr != nil {
				return nil, serr
			}
			return nil, newGlomErr(foldErrorClass, &glomErrData{}, mkStr("can only "+f.kind+" on iterable targets, not "+
				typeName(target)+" type ("+s+")"))
		}
		return nil, err
	}
	return f.fold(it)
}

func (f *foldObj) fold(it iterator) (Value, error) {
	if f.lazy {
		var inner iterator
		return newIterObj("chain", funcIter(func() (Value, bool, error) {
			for {
				if inner != nil {
					v, ok, err := inner.next()
					if err != nil {
						return nil, false, err
					}
					if ok {
						return v, true, nil
					}
					inner = nil
				}
				t, ok, err := it.next()
				if err != nil || !ok {
					return nil, false, err
				}
				if inner, err = pyIter(t); err != nil {
					return nil, false, err
				}
			}
		})), nil
	}
	ret, err := callValue(f.init, nil, nil)
	if err != nil {
		return nil, err
	}
	for {
		v, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return ret, nil
		}
		r, err := callValue(f.op, []Value{ret, v}, nil)
		if err != nil {
			return nil, err
		}
		if f.kind != "Merge" {
			ret = r
		}
	}
}

func (f *foldObj) agg(target Value, tree *dictObj) (Value, error) {
	cur, ok, err := tree.get(f)
	if err != nil {
		return nil, err
	}
	if !ok {
		if cur, err = callValue(f.init, nil, nil); err != nil {
			return nil, err
		}
		_ = tree.set(f, cur)
	}
	r, err := callValue(f.op, []Value{cur, target}, nil)
	if err != nil {
		return nil, err
	}
	if f.kind == "Merge" {
		return cur, nil
	}
	_ = tree.set(f, r)
	return r, nil
}

func (f *foldObj) repr() (string, error) {
	switch f.kind {
	case "Sum", "Flatten":
		var args []Value
		if f.subspec != Value(T) {
			args = []Value{f.subspec}
		}
		var kw []kwArg
		if f.kind == "Sum" && f.init != Value(intClass) {
			kw = []kwArg{{"init", f.init}}
		}
		if f.kind == "Flatten" {
			if f.lazy {
				kw = []kwArg{{"init", mkStr("lazy")}}
			} else if f.init != Value(listClass) {
				kw = []kwArg{{"init", f.init}}
			}
		}
		return formatInvocation(f.kind, args, kw, true)
	}
	kw := []kwArg{{"init", f.init}}
	if f.op != iaddFn() {
		kw = append(kw, kwArg{"op", f.op})
	}
	return formatInvocation(f.kind, []Value{f.subspec}, kw, true)
}

// flattenFn is flatten(target, spec=..., init=..., levels=...).
func flattenFn(target, spec, init, levels Value) (Value, error) {
	if spec == nil {
		spec = T
	}
	if init == nil {
		init = listClass
	}
	if levels == nil {
		levels = mkInt(1)
	}
	if eq, err := pyEq(levels, mkInt(0)); err != nil {
		return nil, err
	} else if eq {
		return target, nil
	}
	neg, err := pyCompare("<", levels, mkInt(0))
	if err != nil {
		return nil, err
	}
	if neg {
		r, err := pyRepr(levels)
		if err != nil {
			return nil, err
		}
		return nil, valueErrf("expected levels >= 0, not " + r)
	}
	steps := []Value{spec}
	n, ok := asIntBig(levels)
	if !ok {
		return nil, typeErrf("can't multiply sequence by non-int of type '" + typeName(levels) + "'")
	}
	if n.Int64() > 1 {
		lazy, err := newFold("Flatten", nil, mkStr("lazy"), nil)
		if err != nil {
			return nil, err
		}
		for i := int64(0); i < n.Int64()-1; i++ {
			steps = append(steps, lazy)
		}
	}
	last, err := newFold("Flatten", nil, init, nil)
	if err != nil {
		return nil, err
	}
	steps = append(steps, last)
	return glomTop(target, newTuple(steps), glomOptions{}, defaultScopeMap)
}

// mergeFn is merge(target, spec=..., init=..., op=...).
func mergeFn(target, spec, init, op Value) (Value, error) {
	m, err := newFold("Merge", spec, init, op)
	if err != nil {
		return nil, err
	}
	return glomTop(target, m, glomOptions{}, defaultScopeMap)
}

// ---------------------------------------------------------------- grouping

type groupObj struct{ spec Value }

func (*groupObj) class() *classObj { return groupClass }

func (g *groupObj) glomit(target Value, scope *scopeObj) (Value, error) {
	scope.set(sentMODE, groupMode)
	scope.set(sentCurAgg, none)
	scope.set(sentAccTree, newDict())
	var ret Value = none
	switch g.spec.(type) {
	case *dictObj:
		ret = newDict()
	case *listObj:
		ret = newList(nil)
	}
	it, err := iterTarget(target, scope)
	if err != nil {
		return nil, err
	}
	for {
		t, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return ret, nil
		}
		last := ret
		if ret, err = recurse(t, g.spec, scope); err != nil {
			return nil, err
		}
		if ret == Value(sentSTOP) {
			return last, nil
		}
	}
}

func (g *groupObj) repr() (string, error) {
	r, err := pyRepr(g.spec)
	return "Group(" + r + ")", err
}

// specID is id(spec) as an ACC_TREE key.
type specID struct{ spec Value }

func (*specID) class() *classObj { return intClass }

var specIDs = map[Value]*specID{}

func idKey(spec Value) *specID {
	if k, ok := specIDs[spec]; ok {
		return k
	}
	k := &specID{spec}
	specIDs[spec] = k
	return k
}

// aggregator is implemented by First, Avg, Max and Min (the agg method).
type aggregator interface {
	agg(target Value, tree *dictObj) (Value, error)
}

// groupFn is GROUP, the group mode.
func groupFn(target, spec Value, scope *scopeObj) (Value, error) {
	tree := scope.mustGet(sentAccTree).(*dictObj)
	if a, ok := spec.(aggregator); ok {
		return a.agg(target, tree)
	}
	if callable(spec) {
		return callValue(spec, []Value{target}, nil)
	}
	switch s := spec.(type) {
	case *dictObj:
		accV, ok, _ := tree.get(idKey(spec))
		if !ok {
			accV = newDict()
			_ = tree.set(idKey(spec), accV)
		}
		acc := accV.(*dictObj)
		done := true
		for _, e := range s.items() {
			keyspec, valspec := e.key, e.val
			if v, ok, err := tree.get(keyspec); err != nil {
				return nil, err
			} else if ok && v == Value(sentSTOP) {
				continue
			}
			key, err := recurse(target, keyspec, scope)
			if err != nil {
				return nil, err
			}
			if key == Value(sentSKIP) {
				done = false
				continue
			}
			if key == Value(sentSTOP) {
				if err := tree.set(keyspec, sentSTOP); err != nil {
					return nil, err
				}
				continue
			}
			_, in, err := acc.get(key)
			if err != nil {
				return nil, err
			}
			if !in {
				if err := tree.set(key, newDict()); err != nil {
					return nil, err
				}
			}
			sub, _, err := tree.get(key)
			if err != nil {
				return nil, err
			}
			scope.set(sentAccTree, sub)
			result, err := recurse(target, valspec, scope)
			if err != nil {
				return nil, err
			}
			if result == Value(sentSTOP) {
				if err := tree.set(keyspec, sentSTOP); err != nil {
					return nil, err
				}
				continue
			}
			done = false
			if result != Value(sentSKIP) {
				if err := acc.set(key, result); err != nil {
					return nil, err
				}
			}
		}
		if done {
			return sentSTOP, nil
		}
		return acc, nil
	case *listObj:
		accV, ok, _ := tree.get(idKey(spec))
		if !ok {
			accV = newList(nil)
			_ = tree.set(idKey(spec), accV)
		}
		acc := accV.(*listObj)
		for _, valspec := range s.items {
			if _, isDict := valspec.(*dictObj); isDict {
				r, err := pyRepr(spec)
				if err != nil {
					return nil, err
				}
				return nil, newBadSpec("dicts within lists are not allowed while in Group mode: " + r)
			}
			result, err := recurse(target, valspec, scope)
			if err != nil {
				return nil, err
			}
			if result == Value(sentSTOP) {
				return sentSTOP, nil
			}
			if result != Value(sentSKIP) {
				acc.items = append(acc.items, result)
			}
		}
		return acc, nil
	}
	r, err := pyRepr(spec)
	if err != nil {
		return nil, err
	}
	return nil, newBadSpec("Group mode expected dict, list, callable, or aggregator, not: " + r)
}

// gAgg is grouping's First, Avg, Max or Min.
type gAgg struct{ kind string }

func (a *gAgg) class() *classObj {
	switch a.kind {
	case "Avg":
		return avgClass
	case "Max":
		return maxClass
	case "Min":
		return minClass
	}
	return gFirstClass
}

func (a *gAgg) repr() (string, error) { return a.kind + "()", nil }

func (a *gAgg) agg(target Value, tree *dictObj) (Value, error) {
	cur, in, err := tree.get(a)
	if err != nil {
		return nil, err
	}
	switch a.kind {
	case "First":
		if !in {
			_ = tree.set(a, sentSTOP)
			return target, nil
		}
		return sentSTOP, nil
	case "Avg":
		if !in {
			cur = newList([]Value{mkFloat(0), mkInt(0)})
			_ = tree.set(a, cur)
		}
		acc := cur.(*listObj)
		s, err := inplaceAddNum(acc.items[0], target)
		if err != nil {
			return nil, err
		}
		acc.items[0] = s
		n, _ := binaryOp("+", acc.items[1], mkInt(1))
		acc.items[1] = n
		return binaryOp("/", acc.items[0], acc.items[1])
	}
	op := ">"
	if a.kind == "Min" {
		op = "<"
	}
	replace := !in
	if in {
		if replace, err = pyCompare(op, target, cur); err != nil {
			return nil, err
		}
	}
	if replace {
		_ = tree.set(a, target)
		return target, nil
	}
	return cur, nil
}

// inplaceAddNum is a += b for the accumulators of Avg.
func inplaceAddNum(a, b Value) (Value, error) {
	r, err := binaryOp("+", a, b)
	if err != nil {
		if e, ok := err.(*excObj); ok && e.cls == excTypeError {
			if s, _ := excStr(e); len(s) > 0 && s[:11] == "unsupported" {
				return nil, unsupported("+=", a, b)
			}
		}
	}
	return r, err
}

// limitObj is Limit.
type limitObj struct {
	n, subspec Value
}

func (*limitObj) class() *classObj { return limitClass }

func (l *limitObj) glomit(target Value, scope *scopeObj) (Value, error) {
	if scope.mustGet(sentMODE) != Value(groupMode) {
		return nil, newBadSpec("Limit() only valid in Group mode")
	}
	tree := scope.mustGet(sentAccTree).(*dictObj)
	cur, in, err := tree.get(l)
	if err != nil {
		return nil, err
	}
	if !in {
		cur = newList([]Value{mkInt(0), newDict()})
		_ = tree.set(l, cur)
	}
	st := cur.(*listObj)
	scope.set(sentAccTree, st.items[1])
	c, _ := binaryOp("+", st.items[0], mkInt(1))
	st.items[0] = c
	over, err := pyCompare(">", c, l.n)
	if err != nil {
		return nil, err
	}
	if over {
		return sentSTOP, nil
	}
	return recurse(target, l.subspec, scope)
}

func (l *limitObj) repr() (string, error) {
	n, err := pyRepr(l.n)
	if err != nil {
		return "", err
	}
	s, err := pyRepr(l.subspec)
	return "Limit(" + n + ", " + s + ")", err
}
