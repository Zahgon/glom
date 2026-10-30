// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. The scope (a collections.ChainMap of dicts, as glom keeps
// it) and glom's target registry (TargetRegistry with the default types and
// the 'assign' and 'delete' operations the mutation module registers).

package glom

import (
	"sort"
	"strings"
)

// scopeObj is a collections.ChainMap; maps[0] is the innermost map.
type scopeObj struct{ maps []*dictObj }

var chainMapClass = func() *classObj {
	c := newClass("ChainMap", "collections", objectClass)
	c.iterable, c.hasDict, c.unhashable = true, true, true
	return c
}()

func (*scopeObj) class() *classObj { return chainMapClass }

func newScopeOf(maps ...*dictObj) *scopeObj { return &scopeObj{maps: maps} }

// get is ChainMap.__getitem__ without __missing__.
func (s *scopeObj) get(key Value) (Value, bool) {
	for _, m := range s.maps {
		if v, ok, _ := m.get(key); ok {
			return v, true
		}
	}
	return nil, false
}

func (s *scopeObj) mustGet(key Value) Value {
	v, _ := s.get(key)
	return v
}

func (s *scopeObj) set(key, val Value) { _ = s.maps[0].set(key, val) }

func (s *scopeObj) has(key Value) bool {
	_, ok := s.get(key)
	return ok
}

func (s *scopeObj) newChild(m *dictObj) *scopeObj {
	return &scopeObj{maps: append([]*dictObj{m}, s.maps...)}
}

// keyList is iter(ChainMap): the keys of all maps, outermost first.
func (s *scopeObj) keyList() []Value {
	seen := newDict()
	for i := len(s.maps) - 1; i >= 0; i-- {
		for _, k := range s.maps[i].keys() {
			_ = seen.set(k, none)
		}
	}
	return seen.keys()
}

// update is MutableMapping.update(src) for a dict or another ChainMap.
func (s *scopeObj) update(src Value) error {
	switch x := src.(type) {
	case *dictObj:
		for _, e := range x.items() {
			s.set(e.key, e.val)
		}
		return nil
	case *scopeObj:
		for _, k := range x.keyList() {
			s.set(k, x.mustGet(k))
		}
		return nil
	}
	return dictUpdateFrom(s.maps[0], src)
}

func (s *scopeObj) repr() (string, error) {
	parts := make([]string, len(s.maps))
	for i, m := range s.maps {
		r, err := pyRepr(m)
		if err != nil {
			return "", err
		}
		parts[i] = r
	}
	return "ChainMap(" + strings.Join(parts, ", ") + ")", nil
}

// ---------------------------------------------------------------- sentinels

// sentinelObj is a boltons make_sentinel() object: falsy, printed as
// Sentinel('NAME'), each of its own class.
type sentinelObj struct {
	name string
	cls  *classObj
}

func (s *sentinelObj) class() *classObj { return s.cls }

func (s *sentinelObj) repr() (string, error) { return "Sentinel(" + strRepr(s.name) + ")", nil }

func newSentinel(name string) *sentinelObj {
	c := newClass("Sentinel", "boltons.typeutils", objectClass)
	c.qualname = "make_sentinel.<locals>.Sentinel"
	c.hasDict = true
	return &sentinelObj{name: name, cls: c}
}

var (
	sentMissing     = newSentinel("_MISSING")
	sentSKIP        = newSentinel("SKIP")
	sentSTOP        = newSentinel("STOP")
	sentLastChild   = newSentinel("LAST_CHILD_SCOPE")
	sentNoPyframe   = newSentinel("NO_PYFRAME")
	sentMODE        = newSentinel("MODE")
	sentMinMode     = newSentinel("MIN_MODE")
	sentChildErrors = newSentinel("CHILD_ERRORS")
	sentCurError    = newSentinel("CUR_ERROR")
	sentUP          = newSentinel("UP")
	sentROOT        = newSentinel("ROOT")
	sentRAISE       = newSentinel("RAISE")
	sentAccTree     = newSentinel("ACC_TREE")
	sentCurAgg      = newSentinel("CUR_AGG")
	matchMissing    = newSentinel("_MISSING")
	reduceMissing   = newSentinel("_MISSING")
)

// Scope keys that are glom classes or functions.
var (
	specClass     = glomClass("Spec", "glom.core", objectClass)
	pathClass     = glomClass("Path", "glom.core", objectClass)
	inspectClass  = glomClass("Inspect", "glom.core", objectClass)
	registryClass = glomClass("TargetRegistry", "glom.core", objectClass)
	refClass      = glomClass("Ref", "glom.core", objectClass)
	glomFuncKey   = &modeFunc{name: "glom"}
	globalsKey    = mkStr("globals")
)

// modeFunc is one of glom's mode functions (AUTO, FILL, _glom_match, GROUP,
// an _ArgValuator's mode) or the glom function itself as a scope key.
type modeFunc struct {
	name string
	fn   func(target, spec Value, scope *scopeObj) (Value, error)
}

var functionClass = func() *classObj {
	c := newClass("function", "builtins", objectClass)
	c.hasDict = true
	return c
}()

func (*modeFunc) class() *classObj { return functionClass }

// ---------------------------------------------------------------- registry

type handlerKind string

const (
	hNone    handlerKind = ""
	hGetattr handlerKind = "getattr"
	hGetitem handlerKind = "getitem"
	hSeqGet  handlerKind = "_get_sequence_item"
	hIter    handlerKind = "iter"
	hKeys    handlerKind = "keys"
	hObjKeys handlerKind = "get_keys"
	hSetattr handlerKind = "setattr"
	hSetitem handlerKind = "setitem"
	hSeqSet  handlerKind = "_set_sequence_item"
	hDelattr handlerKind = "delattr"
	hDelitem handlerKind = "delitem"
	hSeqDel  handlerKind = "_del_sequence_item"
)

// typeHandler is one entry of a type map: a registered (possibly abstract)
// type and its handler (hNone is False).
type typeHandler struct {
	t string
	h handlerKind
}

// typeTree is a registry type tree: ordered children under each type.
type typeTree struct {
	t        string
	children []*typeTree
}

type opTable struct {
	handlers []typeHandler
	tree     []*typeTree
}

// registry is a TargetRegistry.
type registry struct {
	ops map[string]*opTable
}

func tree(t string, children ...*typeTree) *typeTree { return &typeTree{t: t, children: children} }

func getTree() []*typeTree {
	return []*typeTree{tree("object",
		tree("_AbstractIterable",
			tree("dict", tree("dict", tree("OrderedDict", tree("OrderedDict")))),
			tree("list"), tree("tuple")),
		tree("_ObjStyleKeys"))}
}

func mutateTree() []*typeTree {
	return []*typeTree{tree("object",
		tree("_ObjStyleKeys"),
		tree("_AbstractIterable", tree("tuple"), tree("dict", tree("OrderedDict")), tree("list")))}
}

// newRegistry is TargetRegistry(register_default_types=...), plus the
// mutation module's operations for the module-level default registry.
func newRegistry(defaults, mutation bool) *registry {
	r := &registry{ops: map[string]*opTable{}}
	if !defaults {
		r.ops["iterate"] = &opTable{}
		r.ops["get"] = &opTable{}
		return r
	}
	r.ops["get"] = &opTable{handlers: []typeHandler{{"object", hGetattr}, {"dict", hGetitem}, {"list", hSeqGet},
		{"tuple", hSeqGet}, {"OrderedDict", hGetitem}, {"_AbstractIterable", hGetattr}, {"_ObjStyleKeys", hGetattr}},
		tree: getTree()}
	r.ops["iterate"] = &opTable{handlers: []typeHandler{{"object", hNone}, {"dict", hIter}, {"list", hIter},
		{"tuple", hIter}, {"OrderedDict", hIter}, {"_AbstractIterable", hIter}, {"_ObjStyleKeys", hNone}},
		tree: getTree()}
	r.ops["keys"] = &opTable{handlers: []typeHandler{{"dict", hKeys}, {"OrderedDict", hKeys}, {"_ObjStyleKeys", hObjKeys}},
		tree: []*typeTree{tree("dict", tree("OrderedDict")), tree("_ObjStyleKeys")}}
	if mutation {
		r.ops["assign"] = &opTable{handlers: []typeHandler{{"OrderedDict", hSetitem}, {"_AbstractIterable", hSetattr},
			{"_ObjStyleKeys", hSetattr}, {"dict", hSetitem}, {"list", hSeqSet}, {"object", hSetattr}, {"tuple", hNone}},
			tree: mutateTree()}
		r.ops["delete"] = &opTable{handlers: []typeHandler{{"OrderedDict", hDelitem}, {"_AbstractIterable", hDelattr},
			{"_ObjStyleKeys", hDelattr}, {"dict", hDelitem}, {"list", hSeqDel}, {"object", hDelattr}, {"tuple", hNone}},
			tree: mutateTree()}
	}
	return r
}

// isAbstractInstance is isinstance(v, t) for the registry's type names.
func isAbstractInstance(v Value, t string) bool {
	switch t {
	case "object":
		return true
	case "dict":
		_, ok := v.(*dictObj)
		return ok
	case "list":
		_, ok := v.(*listObj)
		return ok
	case "tuple":
		_, ok := v.(*tupleObj)
		return ok
	case "OrderedDict":
		return false
	case "_AbstractIterable":
		c := v.class()
		return c.iterable && c != strClass && c != bytesClass
	case "_ObjStyleKeys":
		return v.class().hasDict
	}
	return false
}

// instanceOfABC is isinstance against glom's abstract classes when a spec
// names them; only the builtin classes appear in specs, so it is false.
func instanceOfABC(v Value, c *classObj) bool { return false }

func exactTypeName(v Value) string {
	switch v.(type) {
	case *dictObj:
		return "dict"
	case *listObj:
		return "list"
	case *tupleObj:
		return "tuple"
	case *plainObj:
		return "object"
	}
	return ""
}

func closestType(v Value, trees []*typeTree) string {
	for _, t := range trees {
		if isAbstractInstance(v, t.t) {
			if sub := closestType(v, t.children); sub != "" {
				return sub
			}
			return t.t
		}
	}
	return ""
}

// getHandler is TargetRegistry.get_handler(op, obj, path=path).
func (r *registry) getHandler(op string, obj Value, path Value) (handlerKind, error) {
	tbl := r.ops[op]
	var handlers []typeHandler
	if tbl != nil {
		handlers = tbl.handlers
	}
	ret := hNone
	if len(handlers) > 0 {
		found := false
		if et := exactTypeName(obj); et != "" {
			for _, h := range handlers {
				if h.t == et {
					ret, found = h.h, true
					break
				}
			}
		}
		if !found {
			if closest := closestType(obj, tbl.tree); closest != "" {
				for _, h := range handlers {
					if h.t == closest {
						ret = h.h
					}
				}
			}
		}
	}
	if ret == hNone {
		if path == nil {
			path = none
		}
		return hNone, newGlomErr(unregisteredClass, &glomErrData{op: op, targetType: obj.class(), typeMap: handlers,
			utPath: path}, mkStr(op), obj.class(), typeMapValue(handlers), path)
	}
	return ret, nil
}

func typeMapValue(handlers []typeHandler) Value {
	d := newDict()
	for _, h := range handlers {
		_ = d.set(mkStr(h.t), mkStr(string(h.h)))
	}
	return d
}

func typeMapRepr(handlers []typeHandler) (string, error) {
	return pyRepr(typeMapValue(handlers))
}

func unregisteredMessage(e *excObj) (string, error) {
	g := e.g
	if len(g.typeMap) == 0 {
		return "glom() called without registering any types for operation '" + g.op +
			"'. see glom.register() or Glommer's constructor for details.", nil
	}
	var names []string
	for _, h := range g.typeMap {
		if h.h != hNone {
			names = append(names, h.t)
		}
	}
	sort.Strings(names)
	regs := "()"
	if len(names) > 0 {
		regs = "(" + strings.Join(names, ", ") + ")"
	}
	tn, _ := pyRepr(mkStr(g.targetType.name))
	msg := "target type " + tn + " not registered for '" + g.op + "', expected one of registered types: " + regs
	if t, _ := truthy(g.utPath); t {
		p, err := pyRepr(g.utPath)
		if err != nil {
			return "", err
		}
		msg += " (at " + p + ")"
	}
	return msg, nil
}

// callHandler runs a registry handler on target with the given arguments.
func callHandler(h handlerKind, target Value, args ...Value) (Value, error) {
	switch h {
	case hGetattr:
		name, ok := args[0].(*strObj)
		if !ok {
			return nil, typeErrf("attribute name must be string, not '" + typeName(args[0]) + "'")
		}
		return getAttr(target, name.s)
	case hGetitem:
		return getItem(target, args[0])
	case hSeqGet:
		i, err := toInt(args[0])
		if err != nil {
			return nil, err
		}
		return getItem(target, i)
	case hIter:
		it, err := pyIter(target)
		if err != nil {
			return nil, err
		}
		return &iterObj{cls: iterClassOf(target), it: it}, nil
	case hKeys:
		return &viewObj{kind: "keys", d: target.(*dictObj)}, nil
	case hObjKeys:
		d := objDict(target)
		if d == nil {
			return nil, attrErr(target, "__dict__")
		}
		return &viewObj{kind: "keys", d: d}, nil
	case hSetattr:
		name, ok := args[0].(*strObj)
		if !ok {
			return nil, typeErrf("attribute name must be string, not '" + typeName(args[0]) + "'")
		}
		return none, setAttr(target, name.s, args[1])
	case hSetitem:
		return none, setItem(target, args[0], args[1])
	case hSeqSet:
		i, err := toInt(args[0])
		if err != nil {
			return nil, err
		}
		return none, setItem(target, i, args[1])
	case hDelattr:
		name, ok := args[0].(*strObj)
		if !ok {
			return nil, typeErrf("attribute name must be string, not '" + typeName(args[0]) + "'")
		}
		return none, delAttr(target, name.s)
	case hDelitem:
		return none, delItem(target, args[0])
	case hSeqDel:
		i, err := toInt(args[0])
		if err != nil {
			return nil, err
		}
		return none, delItem(target, i)
	}
	return nil, typeErrf("'bool' object is not callable")
}

// objDict is the __dict__ of an object that has one with keys.
func objDict(v Value) *dictObj {
	switch x := v.(type) {
	case *instObj:
		return x.attrs
	case *scopeVarsObj:
		return x.d
	case *sentinelObj:
		d := newDict()
		d.setStr("name", mkStr(x.name))
		d.setStr("var_name", none)
		return d
	}
	return newDict()
}

// iterClassOf is the class of iter(v).
func iterClassOf(v Value) *classObj {
	switch v.(type) {
	case *listObj:
		return iterClass("list_iterator")
	case *tupleObj:
		return iterClass("tuple_iterator")
	case *dictObj, *viewObj:
		return iterClass("dict_keyiterator")
	case *setObj:
		return iterClass("set_iterator")
	case *strObj:
		return iterClass("str_ascii_iterator")
	case *iterObj:
		return v.class()
	}
	return iterClass("iterator")
}

// iterObj is an iterator object: a generator, map, filter, zip, ...
type iterObj struct {
	cls *classObj
	it  iterator
}

func (i *iterObj) class() *classObj { return i.cls }

func (i *iterObj) next() (Value, bool, error) { return i.it.next() }

func newIterObj(name string, it iterator) *iterObj { return &iterObj{cls: iterClass(name), it: it} }
