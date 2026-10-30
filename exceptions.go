// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. Python exceptions: the builtin classes glom meets and
// glom's own error classes, with CPython 3.12's str(), repr() and traceback
// formatting.

package glom

import (
	"strings"
)

// excObj is a Python exception instance.
type excObj struct {
	cls  *classObj
	args []Value
	g    *glomErrData
}

func (e *excObj) class() *classObj { return e.cls }

// Error returns str(e).
func (e *excObj) Error() string {
	s, err := excStr(e)
	if err != nil {
		return typeName(e)
	}
	return s
}

// glomErrData holds the attributes glom's error classes set.
type glomErrData struct {
	exc      Value
	path     Value
	partIdx  Value
	destName Value

	coal    *coalesceObj
	skipped *listObj

	op         string
	targetType *classObj
	typeMap    []typeHandler
	utPath     Value

	msgs  *listObj
	check *checkObj

	// finalisation by glom()
	wrapped   Value
	scope     *scopeObj
	tbLines   []string
	finalized *string
}

func newExc(cls *classObj, args ...Value) *excObj {
	return &excObj{cls: cls, args: args}
}

func excClass(name string, bases ...*classObj) *classObj {
	c := builtinClass(name, bases...)
	c.hasDict = true
	return c
}

// excConstructor is what calling a builtin exception class does.
func excConstructor(c *classObj) func(args []Value, kw []kwArg) (Value, error) {
	return func(args []Value, kw []kwArg) (Value, error) {
		if len(kw) > 0 {
			return nil, newExc(excTypeError, mkStr(c.name+"() takes no keyword arguments"))
		}
		return newExc(c, args...), nil
	}
}

var (
	excBase           = excClass("BaseException")
	excException      = excClass("Exception", excBase)
	excArithmetic     = excClass("ArithmeticError", excException)
	excLookup         = excClass("LookupError", excException)
	excKeyError       = excClass("KeyError", excLookup)
	excIndexError     = excClass("IndexError", excLookup)
	excAttributeError = excClass("AttributeError", excException)
	excTypeError      = excClass("TypeError", excException)
	excValueError     = excClass("ValueError", excException)
	excZeroDivision   = excClass("ZeroDivisionError", excArithmetic)
	excOverflowError  = excClass("OverflowError", excArithmetic)
	excRuntimeError   = excClass("RuntimeError", excException)
	excRecursionError = excClass("RecursionError", excRuntimeError)
	excStopIteration  = excClass("StopIteration", excException)
	excNotImplemented = excClass("NotImplementedError", excRuntimeError)
	excReError        = &classObj{name: "error", module: "re", qualname: "error"}
)

func init() {
	for _, c := range []*classObj{excBase, excException, excArithmetic, excLookup, excKeyError, excIndexError,
		excAttributeError, excTypeError, excValueError, excZeroDivision, excOverflowError, excRuntimeError,
		excRecursionError, excStopIteration, excNotImplemented} {
		c.construct = excConstructor(c)
	}
	excBase.ownStr = baseExcStr
	excBase.ownRepr = baseExcRepr
	excKeyError.ownStr = func(e *excObj) (string, error) {
		if len(e.args) == 1 {
			return pyRepr(e.args[0])
		}
		return baseExcStr(e)
	}
	excAttributeError.ownStr = baseExcStr
	excReError.bases = []*classObj{excException}
	excReError.mro = c3(excReError)
	excReError.hasDict = true
}

// baseExcStr is BaseException.__str__.
func baseExcStr(e *excObj) (string, error) {
	switch len(e.args) {
	case 0:
		return "", nil
	case 1:
		return pyStr(e.args[0])
	}
	return pyRepr(newTuple(e.args))
}

// baseExcRepr is BaseException.__repr__, which names the class by the part
// of its name after the last dot.
func baseExcRepr(e *excObj) (string, error) {
	name := e.cls.name
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if len(e.args) == 1 {
		s, err := pyRepr(e.args[0])
		return name + "(" + s + ")", err
	}
	s, err := pyRepr(newTuple(e.args))
	return name + s, err
}

// excStr is str(e): the first __str__ in the class's MRO.
func excStr(e *excObj) (string, error) {
	return excStrFrom(e, 0)
}

func excStrFrom(e *excObj, start int) (string, error) {
	for _, c := range e.cls.mro[start:] {
		if c.ownStr != nil {
			return c.ownStr(e)
		}
	}
	return baseExcStr(e)
}

func excRepr(e *excObj) (string, error) {
	for _, c := range e.cls.mro {
		if c.ownRepr != nil {
			return c.ownRepr(e)
		}
	}
	return baseExcRepr(e)
}

// excTypeQual is how traceback names an exception's class: module and
// qualified name, without the module for builtins.
func excTypeQual(c *classObj) string {
	if c.module == "builtins" || c.module == "__main__" {
		return c.qualname
	}
	return c.module + "." + c.qualname
}

// formatExceptionOnly is "".join(traceback.format_exception_only(type(e), e))
// without the final newline.
func formatExceptionOnly(e *excObj) string {
	s, err := excStr(e)
	if err != nil {
		return excTypeQual(e.cls) + ": <exception str() failed>"
	}
	if s == "" {
		return excTypeQual(e.cls)
	}
	return excTypeQual(e.cls) + ": " + s
}

// asExc turns a Go error into the Python exception it stands for.
func asExc(err error) *excObj {
	if e, ok := err.(*excObj); ok {
		return e
	}
	return newExc(excRuntimeError, mkStr(err.Error()))
}

func isExcInstance(err error, c *classObj) bool {
	e, ok := err.(*excObj)
	return ok && e.cls.isSubclass(c)
}

// excMatches is `except spec`: spec is an exception class or a tuple of them.
func excMatches(err error, spec Value) bool {
	e, ok := err.(*excObj)
	if !ok {
		return false
	}
	switch s := spec.(type) {
	case *classObj:
		return e.cls.isSubclass(s)
	case *tupleObj:
		for _, it := range s.items {
			if excMatches(err, it) {
				return true
			}
		}
	}
	return false
}

// isinstanceSpec is isinstance(v, spec) for a class or a tuple of classes.
func isinstanceSpec(v Value, spec Value) bool {
	switch s := spec.(type) {
	case *classObj:
		if s == objectClass {
			return true
		}
		return isinstance(v, s) || instanceOfABC(v, s)
	case *tupleObj:
		for _, it := range s.items {
			if isinstanceSpec(v, it) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- glom's errors

func glomClass(name, module string, bases ...*classObj) *classObj {
	c := newClass(name, module, bases...)
	c.hasDict = true
	return c
}

var (
	glomErrorClass     = glomClass("GlomError", "glom.core", excException)
	pathAccessClass    = glomClass("PathAccessError", "glom.core", glomErrorClass, excAttributeError, excKeyError, excIndexError)
	pathAssignClass    = glomClass("PathAssignError", "glom.core", glomErrorClass)
	coalesceErrorClass = glomClass("CoalesceError", "glom.core", glomErrorClass)
	badSpecClass       = glomClass("BadSpec", "glom.core", glomErrorClass, excTypeError)
	unregisteredClass  = glomClass("UnregisteredTarget", "glom.core", glomErrorClass)
	pathDeleteClass    = glomClass("PathDeleteError", "glom.mutation", pathAssignClass)
	matchErrorClass    = glomClass("MatchError", "glom.matching", glomErrorClass)
	typeMatchClass     = glomClass("TypeMatchError", "glom.matching", matchErrorClass, excTypeError)
	checkErrorClass    = glomClass("CheckError", "glom.matching", glomErrorClass)
	foldErrorClass     = glomClass("FoldError", "glom.reduction", glomErrorClass)
	validationErrClass = glomClass("_ValidationError", "glom.matching", excException)
)

func init() {
	validationErrClass.qualname = "Check._ValidationError"
	glomErrorClass.ownStr = glomErrorStr
	for _, c := range []*classObj{glomErrorClass, pathAccessClass, pathAssignClass, coalesceErrorClass,
		badSpecClass, unregisteredClass, pathDeleteClass, matchErrorClass, typeMatchClass, checkErrorClass,
		foldErrorClass} {
		c := c
		c.construct = func(args []Value, kw []kwArg) (Value, error) {
			return newExc(c, args...), nil
		}
	}
	pathAccessClass.getMessage = paeMessage
	pathAccessClass.ownRepr = func(e *excObj) (string, error) {
		return reprJoin(e.cls.name+"(", ")", e.g.exc, e.g.path, e.g.partIdx)
	}
	pathAssignClass.getMessage = func(e *excObj) (string, error) {
		return fmtR("could not assign %r on object at %r, got error: %r", e.g.destName, e.g.path, e.g.exc)
	}
	pathAssignClass.ownRepr = func(e *excObj) (string, error) {
		return reprJoin(e.cls.name+"(", ")", e.g.exc, e.g.path, e.g.destName)
	}
	pathDeleteClass.getMessage = func(e *excObj) (string, error) {
		return fmtR("could not delete %r on object at %r, got error: %r", e.g.destName, e.g.path, e.g.exc)
	}
	coalesceErrorClass.getMessage = coalesceMessage
	coalesceErrorClass.ownRepr = func(e *excObj) (string, error) {
		return reprJoin(e.cls.name+"(", ")", e.g.coal, e.g.skipped, e.g.path)
	}
	unregisteredClass.getMessage = unregisteredMessage
	unregisteredClass.ownRepr = func(e *excObj) (string, error) {
		op, _ := pyRepr(mkStr(e.g.op))
		tn, _ := pyRepr(mkStr(e.g.targetType.name))
		tm, err := typeMapRepr(e.g.typeMap)
		if err != nil {
			return "", err
		}
		p, err := pyRepr(e.g.utPath)
		return e.cls.name + "(" + op + ", <type " + tn + ">, " + tm + ", " + p + ")", err
	}
	matchErrorClass.getMessage = func(e *excObj) (string, error) {
		f, _ := asStr(e.args[0])
		return bbformat(f, e.args[1:]...)
	}
	checkErrorClass.getMessage = checkMessage
	checkErrorClass.ownRepr = func(e *excObj) (string, error) {
		return reprJoin(e.cls.name+"(", ")", e.g.msgs, e.g.check, e.g.path)
	}
}

func reprJoin(open, close string, vals ...Value) (string, error) {
	parts, err := reprAll(vals)
	if err != nil {
		return "", err
	}
	return open + strings.Join(parts, ", ") + close, nil
}

// fmtR fills %r and %s slots with repr() and str() of the arguments.
func fmtR(format string, args ...Value) (string, error) {
	var b strings.Builder
	ai := 0
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) {
			var s string
			var err error
			switch format[i+1] {
			case 'r':
				s, err = pyRepr(args[ai])
			case 's':
				s, err = pyStr(args[ai])
			case 'd':
				s, err = pyStr(args[ai])
			case '%':
				b.WriteByte('%')
				i++
				continue
			default:
				b.WriteByte(format[i])
				continue
			}
			if err != nil {
				return "", err
			}
			b.WriteString(s)
			ai++
			i++
			continue
		}
		b.WriteByte(format[i])
	}
	return b.String(), nil
}

// glomErrorStr is GlomError.__str__.
func glomErrorStr(e *excObj) (string, error) {
	if e.g != nil && e.g.finalized != nil && *e.g.finalized != "" {
		return *e.g.finalized, nil
	}
	if e.g != nil && e.g.scope != nil {
		trace, err := formatTargetSpecTrace(e.g.scope, e.g.wrapped, traceWidth, 0, nil, true)
		if err != nil {
			return "", err
		}
		parts := []string{"error raised while processing, details below.",
			" Target-spec trace (most recent last):", trace}
		parts = append(parts, e.g.tbLines...)
		s := strings.Join(parts, "\n")
		e.g.finalized = &s
		return s, nil
	}
	for _, c := range e.cls.mro {
		if c.getMessage != nil {
			return c.getMessage(e)
		}
	}
	// super().__str__ from GlomError's place in the MRO
	for i, c := range e.cls.mro {
		if c == glomErrorClass {
			return excStrFrom(e, i+1)
		}
	}
	return baseExcStr(e)
}

func newGlomErr(cls *classObj, g *glomErrData, args ...Value) *excObj {
	return &excObj{cls: cls, args: args, g: g}
}

func newPAE(exc error, path *Path, partIdx int) *excObj {
	ev := Value(asExc(exc))
	return newGlomErr(pathAccessClass, &glomErrData{exc: ev, path: path, partIdx: mkInt(int64(partIdx))},
		ev, path, mkInt(int64(partIdx)))
}

func paeMessage(e *excObj) (string, error) {
	var part Value = e.g.path
	if p, ok := e.g.path.(*Path); ok {
		vals := p.values()
		if idx, ok := e.g.partIdx.(*intObj); ok && idx.v.IsInt64() {
			i := idx.v.Int64()
			if i < 0 {
				i += int64(len(vals))
			}
			if i >= 0 && i < int64(len(vals)) {
				part = vals[i]
			}
		}
	}
	return fmtR("could not access %r, part %r of %r, got error: %r", part, e.g.partIdx, e.g.path, e.g.exc)
}

func newBadSpec(msg string) *excObj {
	return newGlomErr(badSpecClass, &glomErrData{}, mkStr(msg))
}

func newMatchError(format string, args ...Value) *excObj {
	return newGlomErr(matchErrorClass, &glomErrData{}, append([]Value{mkStr(format)}, args...)...)
}

func newTypeMatchError(actual, expected Value) *excObj {
	return newGlomErr(typeMatchClass, &glomErrData{}, mkStr("expected type {0.__name__}, not {1.__name__}"),
		expected, actual)
}

// copyGlomErr is copy.copy() of one of glom's errors.
func copyGlomErr(e *excObj) *excObj {
	if e.cls == typeMatchClass {
		// TypeMatchError.__copy__ rebuilds it from its args.
		return newTypeMatchError(e.args[2], e.args[1])
	}
	n := &excObj{cls: e.cls, args: e.args}
	if e.g != nil {
		g := *e.g
		n.g = &g
	} else {
		n.g = &glomErrData{}
	}
	return n
}

// wrapExc is GlomError.wrap(exc): a new class deriving from exc's class and
// GlomError, instantiated with exc's args; nil when that cannot be done.
func wrapExc(e *excObj) *excObj {
	bases := []*classObj{e.cls, glomErrorClass}
	if glomErrorClass.isSubclass(e.cls) {
		bases = []*classObj{glomErrorClass}
	}
	name := "GlomError.wrap(" + e.cls.name + ")"
	c := newClass(name, "glom.core", bases...)
	c.hasDict = true
	return &excObj{cls: c, args: e.args, g: &glomErrData{wrapped: e}}
}

// pySplitlines is str.splitlines().
func pySplitlines(s string) []string {
	var out []string
	rs := []rune(s)
	start := 0
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\r', '\x0b', '\x0c', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ':
			out = append(out, string(rs[start:i]))
			if rs[i] == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}
