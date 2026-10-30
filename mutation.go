// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom's mutation module: Assign, Delete, assign() and delete().

package glom

import "strings"

var (
	assignClass = glomClass("Assign", "glom.mutation", objectClass)
	deleteClass = glomClass("Delete", "glom.mutation", objectClass)
)

// mutationPath is the path argument of Assign and Delete as a Path.
func mutationPath(path Value) (*Path, error) {
	switch p := path.(type) {
	case *strObj:
		return pathFromText(p.s), nil
	case *TExpr:
		return newPathFrom(p)
	case *Path:
		return p, nil
	}
	return nil, typeErrf("path argument must be a .-delimited string, Path, T, or S")
}

// splitLast is path.items()[-1] and path[:-1].
func splitLast(p *Path) (Value, Value, *Path, error) {
	items := p.items()
	if len(items) == 0 {
		return nil, nil, nil, valueErrf("path must have at least one element")
	}
	last := items[len(items)-1].(*tupleObj)
	head, err := p.slice(none, mkInt(-1), none)
	if err != nil {
		return nil, nil, nil, err
	}
	return last.items[0], last.items[1], head, nil
}

// mutationOp checks self.op not in '[.P' and returns the operation.
func mutationOp(op Value, msg string) (string, error) {
	name, ok := opName(op)
	if !ok {
		return "", typeErrf("'in <string>' requires string as left operand, not " + typeName(op))
	}
	if !strings.Contains("[.P", name) {
		return "", valueErrf(msg)
	}
	return name, nil
}

// stars is TType.__stars__.
func stars(p *Path) int {
	n := 0
	for i := 1; i < len(p.pathT.ops); i += 2 {
		if name, _ := opName(p.pathT.ops[i]); name == "x" || name == "X" {
			n++
		}
	}
	return n
}

// applyForEach is _apply_for_each.
func applyForEach(fn func(Value) error, path *Path, val Value) error {
	layers := stars(path)
	if layers == 0 {
		return fn(val)
	}
	for i := 0; i < layers-1; i++ {
		flat, err := builtinSum([]Value{val, newList(nil)}, nil)
		if err != nil {
			return err
		}
		val = flat
	}
	items, err := toSlice(val)
	if err != nil {
		return err
	}
	for _, inner := range items {
		if err := fn(inner); err != nil {
			return err
		}
	}
	return nil
}

// assignObj is Assign.
type assignObj struct {
	op       string
	arg      Value
	origPath *Path
	path     *Path
	val      Value
	missing  Value // None when not given
}

func (*assignObj) class() *classObj { return assignClass }

func newAssign(path, val, missing Value) (*assignObj, error) {
	p, err := mutationPath(path)
	if err != nil {
		return nil, err
	}
	opv, arg, head, err := splitLast(p)
	if err != nil {
		return nil, err
	}
	op, err := mutationOp(opv, "last part of path must be setattr or setitem")
	if err != nil {
		return nil, err
	}
	if missing == nil {
		missing = none
	}
	if !isNone(missing) && !callable(missing) {
		r, err := pyRepr(missing)
		if err != nil {
			return nil, err
		}
		return nil, typeErrf("expected missing to be callable, not " + r)
	}
	return &assignObj{op: op, arg: arg, origPath: p, path: head, val: val, missing: missing}, nil
}

func startsWithS(p *Path) bool { return p.pathT.ops[0] == Value(S) }

func (a *assignObj) glomit(target Value, scope *scopeObj) (Value, error) {
	val, err := argVal(target, a.val, scope)
	if err != nil {
		return nil, err
	}
	op, arg, path := a.op, a.arg, a.path
	destTarget, destPath := target, a.path
	if startsWithS(a.path) {
		destTarget = scope.mustGet(sentUP)
		destPath = a.path.fromT()
	}
	dest, err := recurse(destTarget, destPath, scope)
	if err != nil {
		pae, ok := err.(*excObj)
		if !ok || !pae.cls.isSubclass(pathAccessClass) {
			return nil, err
		}
		if t, _ := truthy(a.missing); !t {
			return nil, err
		}
		idx := int(pae.g.partIdx.(*intObj).v.Int64())
		remaining, err := a.origPath.slice(mkInt(int64(idx+1)), none, none)
		if err != nil {
			return nil, err
		}
		fresh, err := callValue(a.missing, nil, nil)
		if err != nil {
			return nil, err
		}
		inner, err := newAssign(remaining, val, a.missing)
		if err != nil {
			return nil, err
		}
		if val, err = recurse(fresh, inner, scope); err != nil {
			return nil, err
		}
		items := a.origPath.items()
		if idx >= len(items) {
			return nil, tupleIndexErr()
		}
		item := items[idx].(*tupleObj)
		op, _ = opName(item.items[0])
		arg = item.items[1]
		if path, err = a.origPath.slice(none, mkInt(int64(idx)), none); err != nil {
			return nil, err
		}
		if dest, err = recurse(destTarget, path, scope); err != nil {
			return nil, err
		}
	}
	err = applyForEach(func(d Value) error {
		return assignOp(d, op, arg, val, path, scope)
	}, path, dest)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (a *assignObj) repr() (string, error) {
	p, err := pyRepr(a.origPath)
	if err != nil {
		return "", err
	}
	v, err := pyRepr(a.val)
	if err != nil {
		return "", err
	}
	if isNone(a.missing) {
		return "Assign(" + p + ", " + v + ")", nil
	}
	m, err := bbrepr(a.missing)
	return "Assign(" + p + ", " + v + ", missing=" + m + ")", err
}

// deleteObj is Delete.
type deleteObj struct {
	op            string
	arg           Value
	origPath      *Path
	path          *Path
	ignoreMissing bool
}

func (*deleteObj) class() *classObj { return deleteClass }

func newDelete(path Value, ignoreMissing bool) (*deleteObj, error) {
	p, err := mutationPath(path)
	if err != nil {
		return nil, err
	}
	opv, arg, head, err := splitLast(p)
	if err != nil {
		return nil, err
	}
	op, err := mutationOp(opv, "last part of path must be an attribute or index")
	if err != nil {
		return nil, err
	}
	return &deleteObj{op: op, arg: arg, origPath: p, path: head, ignoreMissing: ignoreMissing}, nil
}

func (d *deleteObj) delErr(err error, arg Value) error {
	if d.ignoreMissing {
		return nil
	}
	e := asExc(err)
	return newGlomErr(pathDeleteClass, &glomErrData{exc: e, path: d.path, destName: arg}, e, d.path, arg)
}

func (d *deleteObj) delOne(dest Value, op string, arg Value, scope *scopeObj) error {
	switch op {
	case "[":
		if err := delItem(dest, arg); err != nil {
			if isExcInstance(err, excIndexError) {
				return d.delErr(err, arg)
			}
			return err
		}
	case ".":
		name, ok := arg.(*strObj)
		if !ok {
			return typeErrf("attribute name must be string, not '" + typeName(arg) + "'")
		}
		if err := delAttr(dest, name.s); err != nil {
			if isExcInstance(err, excAttributeError) {
				return d.delErr(err, arg)
			}
			return err
		}
	case "P":
		h, err := scopeRegistry(scope).getHandler("delete", dest, nil)
		if err != nil {
			return err
		}
		if _, err := callHandler(h, dest, arg); err != nil {
			return d.delErr(err, arg)
		}
	}
	return nil
}

func (d *deleteObj) glomit(target Value, scope *scopeObj) (Value, error) {
	destTarget, destPath := target, d.path
	if startsWithS(d.path) {
		destTarget = scope.mustGet(sentUP)
		destPath = d.path.fromT()
	}
	dest, err := recurse(destTarget, destPath, scope)
	if err != nil {
		if isExcInstance(err, pathAccessClass) && d.ignoreMissing {
			return target, nil
		}
		return nil, err
	}
	err = applyForEach(func(v Value) error { return d.delOne(v, d.op, d.arg, scope) }, d.path, dest)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (d *deleteObj) repr() (string, error) {
	p, err := pyRepr(d.origPath)
	return "Delete(" + p + ")", err
}
