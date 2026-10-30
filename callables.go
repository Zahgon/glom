// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. The callables a glom spec can name (Python builtins, a few
// operator functions and method descriptors), bound methods, and Python's
// attribute and item access, each with CPython 3.12's results and messages.

package glom

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mahmoud/glom/internal/pyuni"
)

// builtinFunc is a builtin function or a bound builtin method.
type builtinFunc struct {
	name   string
	module string // "builtins", "operator", "functools", ...
	self   Value  // the bound object, nil for a plain function
	fn     func(args []Value, kw []kwArg) (Value, error)
}

func (*builtinFunc) class() *classObj { return builtinFnClass }

func (f *builtinFunc) repr() (string, error) {
	if f.self != nil {
		return "<built-in method " + f.name + " of " + typeName(f.self) + " object at " + addr(f.self) + ">", nil
	}
	return "<built-in function " + f.name + ">", nil
}

// methodDescr is a method of a builtin class (str.upper, dict.update, ...).
type methodDescr struct {
	owner *classObj
	name  string
	fn    func(self Value, args []Value, kw []kwArg) (Value, error)
}

func (*methodDescr) class() *classObj { return methodDescrCls }

func (m *methodDescr) repr() (string, error) {
	return "<method '" + m.name + "' of '" + m.owner.name + "' objects>", nil
}

func (m *methodDescr) bind(self Value) *builtinFunc {
	return &builtinFunc{name: m.name, module: "builtins", self: self, fn: func(args []Value, kw []kwArg) (Value, error) {
		return m.fn(self, args, kw)
	}}
}

// partialObj is functools.partial(func, *args, **kw).
type partialObj struct {
	fn   Value
	args []Value
	kw   []kwArg
}

var partialClass = func() *classObj {
	c := newClass("partial", "functools", objectClass)
	c.hasDict = true
	return c
}()

func (*partialObj) class() *classObj { return partialClass }

// callable is Python's callable().
func callable(v Value) bool {
	switch x := v.(type) {
	case *builtinFunc, *methodDescr, *partialObj, *TExpr, *mType, *modeFunc, *boundSpecGlom:
		return true
	case *classObj:
		return x.construct != nil || x == objectClass || x.isSubclass(excBase)
	}
	return false
}

// callValue calls fn(*args, **kw).
func callValue(fn Value, args []Value, kw []kwArg) (Value, error) {
	switch f := fn.(type) {
	case *builtinFunc:
		return f.fn(args, kw)
	case *methodDescr:
		if len(args) == 0 {
			return nil, newExc(excTypeError, mkStr("unbound method "+f.owner.name+"."+f.name+"() needs an argument"))
		}
		if !isinstance(args[0], f.owner) {
			return nil, newExc(excTypeError, mkStr("descriptor '"+f.name+"' for '"+f.owner.name+
				"' objects doesn't apply to a '"+typeName(args[0])+"' object"))
		}
		return f.fn(args[0], args[1:], kw)
	case *partialObj:
		kws := append(append([]kwArg(nil), f.kw...), kw...)
		return callValue(f.fn, append(append([]Value(nil), f.args...), args...), kws)
	case *classObj:
		if f.construct != nil {
			return f.construct(args, kw)
		}
		if f == objectClass {
			if len(args) > 0 || len(kw) > 0 {
				return nil, newExc(excTypeError, mkStr("object() takes no arguments"))
			}
			return &plainObj{}, nil
		}
	case *TExpr:
		return f.call(args, kw)
	case *mType:
		if len(args) != 1 || len(kw) != 0 {
			return nil, newExc(excTypeError, mkStr("_MType.__call__() takes 2 positional arguments but "+
				strconv.Itoa(len(args)+1)+" were given"))
		}
		return MSub(args[0])
	case *boundSpecGlom:
		return f.call(args, kw)
	case *modeFunc:
		return nil, newExc(excTypeError, mkStr("mode functions cannot be called from a spec"))
	}
	return nil, newExc(excTypeError, mkStr("'"+typeName(fn)+"' object is not callable"))
}

// plainObj is an instance of object.
type plainObj struct{}

func (*plainObj) class() *classObj { return objectClass }

// funcName is getattr(f, '__name__', None).
func funcName(v Value) (string, bool) {
	switch f := v.(type) {
	case *builtinFunc:
		return f.name, true
	case *methodDescr:
		return f.name, true
	case *classObj:
		return f.name, true
	case *modeFunc:
		return f.name, true
	}
	return "", false
}

// ---------------------------------------------------------------- argument checks

func typeErrf(msg string) error { return newExc(excTypeError, mkStr(msg)) }

func valueErrf(msg string) error { return newExc(excValueError, mkStr(msg)) }

// methO checks a METH_O function: exactly one positional argument.
func methO(name string, args []Value, kw []kwArg) error {
	if len(kw) > 0 {
		return typeErrf(name + "() takes no keyword arguments")
	}
	if len(args) != 1 {
		return typeErrf(name + "() takes exactly one argument (" + strconv.Itoa(len(args)) + " given)")
	}
	return nil
}

// noArgs checks a METH_NOARGS method.
func noArgs(qual string, args []Value, kw []kwArg) error {
	if len(kw) > 0 {
		return typeErrf(qual + "() takes no keyword arguments")
	}
	if len(args) > 0 {
		return typeErrf(qual + "() takes no arguments (" + strconv.Itoa(len(args)) + " given)")
	}
	return nil
}

// posArgs checks an Argument Clinic positional-only signature.
func posArgs(name string, minN, maxN int, args []Value, kw []kwArg) error {
	if len(kw) > 0 {
		return typeErrf(name + "() takes no keyword arguments")
	}
	if len(args) < minN || len(args) > maxN {
		return argCountErr(name, minN, maxN, len(args))
	}
	return nil
}

func argCountErr(name string, minN, maxN, got int) error {
	plural := func(n int) string {
		if n == 1 {
			return "argument"
		}
		return "arguments"
	}
	switch {
	case minN == maxN:
		return typeErrf(name + " expected " + strconv.Itoa(minN) + " " + plural(minN) + ", got " + strconv.Itoa(got))
	case got < minN:
		return typeErrf(name + " expected at least " + strconv.Itoa(minN) + " " + plural(minN) + ", got " + strconv.Itoa(got))
	}
	return typeErrf(name + " expected at most " + strconv.Itoa(maxN) + " " + plural(maxN) + ", got " + strconv.Itoa(got))
}

// clinicArgs binds Argument Clinic positional-or-keyword parameters: the
// values by position or name (nil when not given), or CPython's errors.
func clinicArgs(name string, params []string, args []Value, kw []kwArg) ([]Value, error) {
	if n := len(args) + len(kw); n > len(params) {
		return nil, typeErrf(name + "() takes at most " + strconv.Itoa(len(params)) + " arguments (" +
			strconv.Itoa(n) + " given)")
	}
	vals := make([]Value, len(params))
	copy(vals, args)
	for _, k := range kw {
		found := false
		for i, p := range params {
			if p == k.name {
				if vals[i] != nil {
					return nil, typeErrf("argument for " + name + "() given by name ('" + p + "') and position (" +
						strconv.Itoa(i+1) + ")")
				}
				vals[i], found = k.val, true
			}
		}
		if !found {
			return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for " + name + "()")
		}
	}
	return vals, nil
}

// ---------------------------------------------------------------- builtins

var builtins = map[string]Value{}

func defBuiltin(name string, fn func(args []Value, kw []kwArg) (Value, error)) {
	builtins[name] = &builtinFunc{name: name, module: "builtins", fn: fn}
}

func init() {
	for _, c := range []*classObj{excBase, excException, excArithmetic, excLookup, excKeyError, excIndexError,
		excAttributeError, excTypeError, excValueError, excZeroDivision, excOverflowError, excRuntimeError} {
		builtins[c.name] = c
	}
	builtins["object"] = objectClass
	for _, c := range []*classObj{intClass, boolClass, floatClass, strClass, bytesClass, listClass, tupleClass,
		dictClass, setClass, frozensetClass} {
		builtins[c.name] = c
	}
	defBuiltin("len", func(args []Value, kw []kwArg) (Value, error) {
		if err := methO("len", args, kw); err != nil {
			return nil, err
		}
		n, err := pyLen(args[0])
		if err != nil {
			return nil, err
		}
		return mkInt(int64(n)), nil
	})
	defBuiltin("abs", func(args []Value, kw []kwArg) (Value, error) {
		if err := methO("abs", args, kw); err != nil {
			return nil, err
		}
		switch x := args[0].(type) {
		case *intObj:
			if x.v.Sign() >= 0 {
				return x, nil
			}
			return mkBig(new(big.Int).Neg(x.v)), nil
		case *boolObj:
			i, _ := asIntBig(x)
			return mkBig(i), nil
		case *floatObj:
			return mkFloat(math.Abs(x.v)), nil
		}
		return nil, typeErrf("bad operand type for abs(): '" + typeName(args[0]) + "'")
	})
	defBuiltin("repr", func(args []Value, kw []kwArg) (Value, error) {
		if err := methO("repr", args, kw); err != nil {
			return nil, err
		}
		s, err := pyRepr(args[0])
		if err != nil {
			return nil, err
		}
		return mkStr(s), nil
	})
	for _, name := range []string{"all", "any"} {
		name := name
		defBuiltin(name, func(args []Value, kw []kwArg) (Value, error) {
			if err := methO(name, args, kw); err != nil {
				return nil, err
			}
			it, err := pyIter(args[0])
			if err != nil {
				return nil, err
			}
			for {
				v, ok, err := it.next()
				if err != nil {
					return nil, err
				}
				if !ok {
					return pyBool(name == "all"), nil
				}
				t, err := truthy(v)
				if err != nil {
					return nil, err
				}
				if name == "any" && t {
					return trueObj, nil
				}
				if name == "all" && !t {
					return falseObj, nil
				}
			}
		})
	}
	defBuiltin("sorted", builtinSorted)
	defBuiltin("sum", builtinSum)
	defBuiltin("round", builtinRound)
	defBuiltin("max", minMax("max", ">"))
	defBuiltin("min", minMax("min", "<"))

	intClass.construct = builtinInt
	boolClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		if len(kw) > 0 {
			return nil, typeErrf("bool() takes no keyword arguments")
		}
		if len(args) > 1 {
			return nil, typeErrf("bool expected at most 1 argument, got " + strconv.Itoa(len(args)))
		}
		if len(args) == 0 {
			return falseObj, nil
		}
		t, err := truthy(args[0])
		return pyBool(t), err
	}
	floatClass.construct = builtinFloat
	strClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		vals, err := clinicArgs("str", []string{"object", "encoding", "errors"}, args, kw)
		if err != nil {
			return nil, err
		}
		if vals[1] != nil || vals[2] != nil {
			for i, name := range []string{"encoding", "errors"} {
				if v := vals[i+1]; v != nil {
					if _, ok := v.(*strObj); !ok {
						return nil, typeErrf("str() argument '" + name + "' must be str, not " + typeName(v))
					}
				}
			}
			if vals[0] == nil {
				return emptyStr, nil
			}
			b, ok := vals[0].(*bytesObj)
			if !ok {
				return nil, typeErrf("decoding to str: need a bytes-like object, " + typeName(vals[0]) + " found")
			}
			return mkStr(string(b.b)), nil
		}
		args = nil
		if vals[0] != nil {
			args = []Value{vals[0]}
		}
		if len(args) == 0 {
			return emptyStr, nil
		}
		if s, ok := args[0].(*strObj); ok {
			return s, nil
		}
		s, err := pyStr(args[0])
		if err != nil {
			return nil, err
		}
		return mkStr(s), nil
	}
	bytesClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		vals, err := clinicArgs("bytes", []string{"source", "encoding", "errors"}, args, kw)
		if err != nil {
			return nil, err
		}
		if vals[1] != nil || vals[2] != nil {
			for i, name := range []string{"encoding", "errors"} {
				if v := vals[i+1]; v != nil {
					if _, ok := v.(*strObj); !ok {
						return nil, typeErrf("bytes() argument '" + name + "' must be str, not " + typeName(v))
					}
				}
			}
			s, ok := vals[0].(*strObj)
			if !ok {
				return nil, typeErrf("encoding without a string argument")
			}
			return &bytesObj{[]byte(s.s)}, nil
		}
		args = nil
		if vals[0] != nil {
			args = []Value{vals[0]}
		}
		if len(args) == 0 {
			return &bytesObj{}, nil
		}
		switch x := args[0].(type) {
		case *bytesObj:
			return x, nil
		case *strObj:
			return nil, typeErrf("string argument without an encoding")
		case *intObj, *boolObj:
			n, _ := asIntBig(x)
			if n.Sign() < 0 {
				return nil, valueErrf("negative count")
			}
			return &bytesObj{make([]byte, n.Int64())}, nil
		}
		items, err := toSlice(args[0])
		if err != nil {
			return nil, typeErrf("cannot convert '" + typeName(args[0]) + "' object to bytes")
		}
		out := make([]byte, len(items))
		for i, it := range items {
			n, ok := asIntBig(it)
			if !ok {
				return nil, typeErrf("'" + typeName(it) + "' object cannot be interpreted as an integer")
			}
			if n.Sign() < 0 || n.Cmp(big.NewInt(255)) > 0 {
				return nil, valueErrf("bytes must be in range(0, 256)")
			}
			out[i] = byte(n.Int64())
		}
		return &bytesObj{out}, nil
	}
	listClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		if len(kw) > 0 {
			return nil, typeErrf("list() takes no keyword arguments")
		}
		if len(args) > 1 {
			return nil, typeErrf("list expected at most 1 argument, got " + strconv.Itoa(len(args)))
		}
		if len(args) == 0 {
			return newList(nil), nil
		}
		items, err := toSlice(args[0])
		return newList(items), err
	}
	tupleClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		if len(kw) > 0 {
			return nil, typeErrf("tuple() takes no keyword arguments")
		}
		if len(args) > 1 {
			return nil, typeErrf("tuple expected at most 1 argument, got " + strconv.Itoa(len(args)))
		}
		if len(args) == 0 {
			return emptyTuple, nil
		}
		if t, ok := args[0].(*tupleObj); ok {
			return t, nil
		}
		items, err := toSlice(args[0])
		return newTuple(items), err
	}
	for _, c := range []*classObj{setClass, frozensetClass} {
		c := c
		c.construct = func(args []Value, kw []kwArg) (Value, error) {
			if len(kw) > 0 {
				return nil, typeErrf(c.name + "() takes no keyword arguments")
			}
			if len(args) > 1 {
				return nil, typeErrf(c.name + " expected at most 1 argument, got " + strconv.Itoa(len(args)))
			}
			frozen := c == frozensetClass
			if len(args) == 1 {
				if fs, ok := args[0].(*setObj); ok && frozen && fs.frozen {
					return fs, nil
				}
			}
			s := newSet(frozen)
			if len(args) == 1 {
				if err := s.update(args[0]); err != nil {
					return nil, err
				}
			}
			return s, nil
		}
	}
	dictClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		if len(args) > 1 {
			return nil, typeErrf("dict expected at most 1 argument, got " + strconv.Itoa(len(args)))
		}
		d := newDict()
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
		return d, nil
	}
	sliceClass.construct = nil

	defOperator("add", 2, func(a []Value) (Value, error) { return binaryOp("+", a[0], a[1]) })
	defOperator("iadd", 2, func(a []Value) (Value, error) { return inplaceAdd(a[0], a[1]) })
	defOperator("mul", 2, func(a []Value) (Value, error) { return binaryOp("*", a[0], a[1]) })
	defOperator("or_", 2, func(a []Value) (Value, error) { return binaryOp("|", a[0], a[1]) })
	defOperator("getitem", 2, func(a []Value) (Value, error) { return getItem(a[0], a[1]) })
	defOperator("not_", 1, func(a []Value) (Value, error) {
		t, err := truthy(a[0])
		return pyBool(!t), err
	})
	defOperator("neg", 1, func(a []Value) (Value, error) { return unaryOp("neg", a[0]) })

	for _, m := range []string{"upper", "lower", "strip", "title", "split"} {
		builtins["str."+m] = strClass.methods[m]
	}
	builtins["list.append"] = listClass.methods["append"]
	builtins["list.extend"] = listClass.methods["extend"]
	builtins["dict.update"] = dictClass.methods["update"]
	builtins["set.update"] = setClass.methods["update"]
	builtins["set.union"] = setClass.methods["union"]
	builtins["frozenset.union"] = frozensetClass.methods["union"]
	for _, c := range []*classObj{glomErrorClass, pathAccessClass, pathAssignClass, pathDeleteClass,
		coalesceErrorClass, unregisteredClass, badSpecClass, matchErrorClass, typeMatchClass, checkErrorClass,
		foldErrorClass} {
		builtins["glom."+c.name] = c
	}
}

func defOperator(name string, n int, fn func(a []Value) (Value, error)) {
	builtins["operator."+name] = &builtinFunc{name: name, module: "operator", fn: func(args []Value, kw []kwArg) (Value, error) {
		if len(kw) > 0 {
			return nil, typeErrf("_operator." + name + "() takes no keyword arguments")
		}
		if len(args) != n {
			if n == 1 {
				return nil, typeErrf("_operator." + name + "() takes exactly one argument (" + strconv.Itoa(len(args)) + " given)")
			}
			return nil, argCountErr(name, n, n, len(args))
		}
		return fn(args)
	}}
}

func dictUpdateFrom(d *dictObj, src Value) error {
	if o, ok := src.(*dictObj); ok {
		for _, e := range o.items() {
			if err := d.set(e.key, e.val); err != nil {
				return err
			}
		}
		return nil
	}
	if sv, ok := src.(*scopeVarsObj); ok {
		_ = sv
		return typeErrf("'ScopeVars' object is not iterable")
	}
	it, err := pyIter(src)
	if err != nil {
		return err
	}
	for i := 0; ; i++ {
		item, ok, err := it.next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		pair, err := toSlice(item)
		if err != nil {
			return typeErrf("cannot convert dictionary update sequence element #" + strconv.Itoa(i) + " to a sequence")
		}
		if len(pair) != 2 {
			return valueErrf("dictionary update sequence element #" + strconv.Itoa(i) + " has length " +
				strconv.Itoa(len(pair)) + "; 2 is required")
		}
		if err := d.set(pair[0], pair[1]); err != nil {
			return err
		}
	}
}

func builtinInt(args []Value, kw []kwArg) (Value, error) {
	var x, base Value
	if n := len(args) + len(kw); n > 2 {
		return nil, typeErrf("int() takes at most 2 arguments (" + strconv.Itoa(n) + " given)")
	}
	if len(args) > 0 {
		x = args[0]
	}
	if len(args) > 1 {
		base = args[1]
	}
	for _, k := range kw {
		switch k.name {
		case "base":
			if base != nil {
				return nil, typeErrf("argument for int() given by name ('base') and position (2)")
			}
			base = k.val
		default:
			return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for int()")
		}
	}
	if x == nil {
		if base != nil {
			return nil, typeErrf("int() missing string argument")
		}
		return mkInt(0), nil
	}
	if base != nil {
		b, ok := asIntBig(base)
		if !ok {
			return nil, typeErrf("'" + typeName(base) + "' object cannot be interpreted as an integer")
		}
		s, isStr := x.(*strObj)
		if !isStr {
			if bs, ok := x.(*bytesObj); ok {
				return parseIntText(string(bs.b), int(b.Int64()), x)
			}
			return nil, typeErrf("int() can't convert non-string with explicit base")
		}
		return parseIntText(s.s, int(b.Int64()), x)
	}
	return toInt(x)
}

// toInt is int(x) with no base.
func toInt(x Value) (Value, error) {
	switch v := x.(type) {
	case *intObj:
		return v, nil
	case *boolObj:
		i, _ := asIntBig(v)
		return mkBig(i), nil
	case *floatObj:
		if math.IsInf(v.v, 0) {
			return nil, newExc(excOverflowError, mkStr("cannot convert float infinity to integer"))
		}
		if math.IsNaN(v.v) {
			return nil, valueErrf("cannot convert float NaN to integer")
		}
		i, _ := new(big.Float).SetFloat64(math.Trunc(v.v)).Int(nil)
		return mkBig(i), nil
	case *strObj:
		return parseIntText(v.s, 10, x)
	case *bytesObj:
		return parseIntText(string(v.b), 10, x)
	}
	return nil, typeErrf("int() argument must be a string, a bytes-like object or a real number, not '" + typeName(x) + "'")
}

func parseIntText(s string, base int, obj Value) (Value, error) {
	if base != 0 && base < 2 || base > 36 {
		return nil, valueErrf("int() base must be >= 2 and <= 36, or 0")
	}
	buf := toASCIIDigits(s)
	full := true
	if i := strings.IndexByte(string(buf), 0); i >= 0 {
		buf, full = buf[:i], false
	}
	v := intFromString(buf, base)
	if v == nil || !full {
		r, _ := pyRepr(obj)
		rs := []rune(r)
		if len(rs) > 200 {
			r = string(rs[:200])
		}
		return nil, valueErrf("invalid literal for int() with base " + strconv.Itoa(base) + ": " + r)
	}
	return mkBig(v), nil
}

// toASCIIDigits is _PyUnicode_TransformDecimalAndSpaceToASCII.
func toASCIIDigits(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x7f:
			out = append(out, byte(r))
		case pyuni.IsSpace(r):
			out = append(out, ' ')
		default:
			d, ok := pyuni.IsDecimalValue(r)
			if !ok {
				return append(out, '?')
			}
			out = append(out, byte('0'+d))
		}
	}
	return out
}

func cSpace(c byte) bool { return c == ' ' || c >= '\t' && c <= '\r' }

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 37
}

// intFromString is PyLong_FromString on an ASCII buffer; nil when malformed.
func intFromString(b []byte, base int) *big.Int {
	at := func(i int) byte {
		if i < len(b) {
			return b[i]
		}
		return 0
	}
	p := 0
	for p < len(b) && cSpace(b[p]) {
		p++
	}
	neg := false
	switch at(p) {
	case '+':
		p++
	case '-':
		p++
		neg = true
	}
	errorIfNonzero := false
	if base == 0 {
		switch {
		case at(p) != '0':
			base = 10
		case at(p+1) == 'x' || at(p+1) == 'X':
			base = 16
		case at(p+1) == 'o' || at(p+1) == 'O':
			base = 8
		case at(p+1) == 'b' || at(p+1) == 'B':
			base = 2
		default:
			errorIfNonzero = true
			base = 10
		}
	}
	if at(p) == '0' {
		c := at(p + 1)
		if base == 16 && (c == 'x' || c == 'X') || base == 8 && (c == 'o' || c == 'O') ||
			base == 2 && (c == 'b' || c == 'B') {
			p += 2
			if at(p) == '_' {
				p++
			}
		}
	}
	if at(p) == '_' {
		return nil
	}
	start := p
	var prev byte
	for digitValue(at(p)) < base || at(p) == '_' {
		if at(p) == '_' && prev == '_' {
			return nil
		}
		prev = at(p)
		p++
	}
	if prev == '_' || p == start {
		return nil
	}
	end := p
	for p < len(b) && cSpace(b[p]) {
		p++
	}
	if p != len(b) {
		return nil
	}
	v, ok := new(big.Int).SetString(strings.ReplaceAll(string(b[start:end]), "_", ""), base)
	if !ok {
		return nil
	}
	if errorIfNonzero && v.Sign() != 0 {
		return nil
	}
	if neg {
		v.Neg(v)
	}
	return v
}

func builtinFloat(args []Value, kw []kwArg) (Value, error) {
	if len(kw) > 0 {
		return nil, typeErrf("float() takes no keyword arguments")
	}
	if len(args) > 1 {
		return nil, typeErrf("float expected at most 1 argument, got " + strconv.Itoa(len(args)))
	}
	if len(args) == 0 {
		return mkFloat(0), nil
	}
	switch x := args[0].(type) {
	case *floatObj:
		return x, nil
	case *intObj, *boolObj:
		f, _, err := toFloat(x)
		if err != nil {
			return nil, err
		}
		return mkFloat(f), nil
	case *strObj:
		f, ok := parseFloatText(toASCIIDigits(x.s))
		if !ok {
			r, _ := pyRepr(x)
			return nil, valueErrf("could not convert string to float: " + r)
		}
		return mkFloat(f), nil
	case *bytesObj:
		f, ok := parseFloatText(x.b)
		if !ok {
			r, _ := pyRepr(x)
			return nil, valueErrf("could not convert string to float: " + r)
		}
		return mkFloat(f), nil
	}
	return nil, typeErrf("float() argument must be a string or a real number, not '" + typeName(args[0]) + "'")
}

func parseFloatText(b []byte) (float64, bool) {
	if strings.IndexByte(string(b), 0) >= 0 {
		return 0, false
	}
	if strings.IndexByte(string(b), '_') >= 0 {
		isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
		var prev byte
		clean := make([]byte, 0, len(b))
		for _, c := range b {
			if c == '_' {
				if !isDigit(prev) {
					return 0, false
				}
			} else {
				clean = append(clean, c)
				if prev == '_' && !isDigit(c) {
					return 0, false
				}
			}
			prev = c
		}
		if prev == '_' {
			return 0, false
		}
		b = clean
	}
	i, j := 0, len(b)
	for i < j && cSpace(b[i]) {
		i++
	}
	for j > i && cSpace(b[j-1]) {
		j--
	}
	s := string(b[i:j])
	body := s
	neg := false
	if body != "" && (body[0] == '+' || body[0] == '-') {
		neg = body[0] == '-'
		body = body[1:]
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case "nan":
		if neg {
			return math.Copysign(math.NaN(), -1), true
		}
		return math.NaN(), true
	}
	if !isDecimalFloat(body) {
		return 0, false
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f, true
}

func isDecimalFloat(s string) bool {
	i := 0
	digits := func() int {
		n := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n++
		}
		return n
	}
	n := digits()
	if i < len(s) && s[i] == '.' {
		i++
		n += digits()
	}
	if n == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(s)
}

func builtinSorted(args []Value, kw []kwArg) (Value, error) {
	if len(args) != 1 {
		return nil, typeErrf("sorted expected 1 argument, got " + strconv.Itoa(len(args)))
	}
	items, err := toSlice(args[0])
	if err != nil {
		return nil, err
	}
	l := newList(items)
	var key Value = none
	reverse := false
	for _, k := range kw {
		switch k.name {
		case "key":
			key = k.val
		case "reverse":
			n, ok := asIntBig(k.val)
			if !ok {
				if _, isF := k.val.(*floatObj); isF {
					return nil, typeErrf("'float' object cannot be interpreted as an integer")
				}
				return nil, typeErrf("'" + typeName(k.val) + "' object cannot be interpreted as an integer")
			}
			reverse = n.Sign() != 0
		default:
			return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for sort()")
		}
	}
	return l, listSort(l, key, reverse)
}

// listSort is list.sort(key=key, reverse=reverse).
func listSort(l *listObj, key Value, reverse bool) error {
	a := make([]sortItem, len(l.items))
	for i, it := range l.items {
		k := it
		if !isNone(key) {
			var err error
			if k, err = callValue(key, []Value{it}, nil); err != nil {
				return err
			}
		}
		a[i] = sortItem{k, it}
	}
	if reverse {
		sortReverse(a)
	}
	err := sortBy(a, pyLess)
	if reverse {
		sortReverse(a)
	}
	for i := range a {
		l.items[i] = a[i].v
	}
	return err
}

func builtinSum(args []Value, kw []kwArg) (Value, error) {
	if n := len(args) + len(kw); n > 2 {
		return nil, typeErrf("sum() takes at most 2 arguments (" + strconv.Itoa(n) + " given)")
	}
	for _, k := range kw {
		if k.name != "start" {
			return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for sum()")
		}
	}
	if len(args) < 1 {
		return nil, typeErrf("sum() takes at least 1 positional argument (" + strconv.Itoa(len(args)) + " given)")
	}
	var result Value = mkInt(0)
	if len(args) == 2 {
		result = args[1]
	}
	for _, k := range kw {
		result = k.val
	}
	switch result.(type) {
	case *strObj:
		return nil, typeErrf("sum() can't sum strings [use ''.join(seq) instead]")
	case *bytesObj:
		return nil, typeErrf("sum() can't sum bytes [use b''.join(seq) instead]")
	}
	it, err := pyIter(args[0])
	if err != nil {
		return nil, err
	}
	if _, ok := result.(*intObj); ok {
		for {
			item, ok, err := it.next()
			if err != nil {
				return nil, err
			}
			if !ok {
				return result, nil
			}
			if isIntLike(item) {
				result, _ = binaryOp("+", result, item)
				continue
			}
			if result, err = binaryOp("+", result, item); err != nil {
				return nil, err
			}
			break
		}
	}
	if f, ok := result.(*floatObj); ok {
		fr, comp := f.v, 0.0
		for {
			item, ok, err := it.next()
			if err != nil {
				return nil, err
			}
			if !ok {
				if comp != 0 && !math.IsInf(comp, 0) && !math.IsNaN(comp) {
					fr += comp
				}
				return mkFloat(fr), nil
			}
			if x, isF := item.(*floatObj); isF {
				t := fr + x.v
				if math.Abs(fr) >= math.Abs(x.v) {
					comp += (fr - t) + x.v
				} else {
					comp += (x.v - t) + fr
				}
				fr = t
				continue
			}
			if i, isInt := item.(*intObj); isInt && i.v.IsInt64() {
				fr += float64(i.v.Int64())
				continue
			}
			if comp != 0 && !math.IsInf(comp, 0) && !math.IsNaN(comp) {
				fr += comp
			}
			if result, err = binaryOp("+", mkFloat(fr), item); err != nil {
				return nil, err
			}
			break
		}
	}
	for {
		item, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return result, nil
		}
		if result, err = binaryOp("+", result, item); err != nil {
			return nil, err
		}
	}
}

func builtinRound(args []Value, kw []kwArg) (Value, error) {
	var num, nd Value
	if len(args) > 2 {
		return nil, typeErrf("round() takes at most 2 arguments (" + strconv.Itoa(len(args)) + " given)")
	}
	if len(args) > 0 {
		num = args[0]
	}
	if len(args) > 1 {
		nd = args[1]
	}
	for _, k := range kw {
		switch k.name {
		case "number":
			num = k.val
		case "ndigits":
			nd = k.val
		default:
			return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for round()")
		}
	}
	if num == nil {
		return nil, typeErrf("round() missing required argument 'number' (pos 1)")
	}
	if nd != nil && isNone(nd) {
		nd = nil
	}
	switch x := num.(type) {
	case *intObj, *boolObj:
		i, _ := asIntBig(x)
		if nd == nil {
			return mkBig(new(big.Int).Set(i)), nil
		}
		n, ok := asIntBig(nd)
		if !ok {
			return nil, typeErrf("'" + typeName(nd) + "' object cannot be interpreted as an integer")
		}
		if n.Sign() >= 0 {
			return mkBig(new(big.Int).Set(i)), nil
		}
		return mkBig(roundIntHalfEven(i, new(big.Int).Neg(n))), nil
	case *floatObj:
		f := x.v
		if nd == nil {
			if math.IsInf(f, 0) {
				return nil, newExc(excOverflowError, mkStr("cannot convert float infinity to integer"))
			}
			if math.IsNaN(f) {
				return nil, valueErrf("cannot convert float NaN to integer")
			}
			r, _ := big.NewFloat(math.RoundToEven(f)).Int(nil)
			return mkBig(r), nil
		}
		n, ok := asIntBig(nd)
		if !ok {
			return nil, typeErrf("'" + typeName(nd) + "' object cannot be interpreted as an integer")
		}
		return mkFloat(roundFloat(f, n)), nil
	}
	return nil, typeErrf("type " + typeName(num) + " doesn't define __round__ method")
}

func roundIntHalfEven(i, digits *big.Int) *big.Int {
	if digits.BitLen() > 20 {
		return big.NewInt(0)
	}
	pow := new(big.Int).Exp(big.NewInt(10), digits, nil)
	q, r := floorDivMod(i, pow)
	twice := new(big.Int).Lsh(r, 1)
	if c := twice.Cmp(pow); c > 0 || c == 0 && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	return q.Mul(q, pow)
}

func roundFloat(x float64, nd *big.Int) float64 {
	if math.IsInf(x, 0) || math.IsNaN(x) || x == 0 {
		return x
	}
	if nd.Cmp(big.NewInt(323)) > 0 {
		return x
	}
	if nd.Cmp(big.NewInt(-308)) < 0 {
		return 0.0 * x
	}
	n := int(nd.Int64())
	if n >= 0 {
		r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', n, 64), 64)
		if r == 0 {
			return math.Copysign(0, x)
		}
		return r
	}
	exact, _ := new(big.Float).SetPrec(2000).SetFloat64(x).Int(nil)
	rem := new(big.Float).SetPrec(2000).Sub(new(big.Float).SetPrec(2000).SetFloat64(x), new(big.Float).SetPrec(2000).SetInt(exact))
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n)), nil)
	q, r := floorDivMod(exact, pow)
	twice := new(big.Float).SetPrec(2000).SetInt(r)
	twice.Add(twice, rem)
	twice.Mul(twice, big.NewFloat(2))
	c := twice.Cmp(new(big.Float).SetPrec(2000).SetInt(pow))
	if c > 0 || c == 0 && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	res, _ := new(big.Float).SetInt(q.Mul(q, pow)).Float64()
	if res == 0 {
		return math.Copysign(0, x)
	}
	return res
}

func minMax(name, op string) func(args []Value, kw []kwArg) (Value, error) {
	return func(args []Value, kw []kwArg) (Value, error) {
		var key, def Value
		for _, k := range kw {
			switch k.name {
			case "key":
				key = k.val
			case "default":
				def = k.val
			default:
				return nil, typeErrf("'" + k.name + "' is an invalid keyword argument for " + name + "()")
			}
		}
		if len(args) == 0 {
			return nil, typeErrf(name + " expected at least 1 argument, got 0")
		}
		var items []Value
		if len(args) == 1 {
			var err error
			if items, err = toSlice(args[0]); err != nil {
				return nil, err
			}
		} else {
			if def != nil {
				return nil, typeErrf("Cannot specify a default for " + name + "() with multiple positional arguments")
			}
			items = args
		}
		if key != nil && isNone(key) {
			key = nil
		}
		var best, bestKey Value
		for _, it := range items {
			k := it
			if key != nil {
				var err error
				if k, err = callValue(key, []Value{it}, nil); err != nil {
					return nil, err
				}
			}
			if best == nil {
				best, bestKey = it, k
				continue
			}
			better, err := pyCompare(op, k, bestKey)
			if err != nil {
				return nil, err
			}
			if better {
				best, bestKey = it, k
			}
		}
		if best == nil {
			if def != nil {
				return def, nil
			}
			return nil, valueErrf(name + "() iterable argument is empty")
		}
		return best, nil
	}
}

// ---------------------------------------------------------------- attributes

// getAttr is getattr(v, name).
func getAttr(v Value, name string) (Value, error) {
	switch x := v.(type) {
	case *instObj:
		if val, ok := x.attrs.getStr(name); ok {
			return val, nil
		}
		switch name {
		case "__class__":
			return x.cls, nil
		case "__dict__":
			return x.attrs, nil
		}
		return nil, attrErr(v, name)
	case *scopeVarsObj:
		if val, ok := x.d.getStr(name); ok {
			return val, nil
		}
		if name == "__dict__" {
			return x.d, nil
		}
	case *sentinelObj:
		switch name {
		case "name":
			return mkStr(x.name), nil
		case "var_name":
			return none, nil
		}
	case *classObj:
		switch name {
		case "__name__":
			return mkStr(x.name), nil
		case "__qualname__":
			return mkStr(x.qualname), nil
		case "__module__":
			return mkStr(x.module), nil
		}
		if m, ok := x.methods[name]; ok {
			return m, nil
		}
		return nil, newExc(excAttributeError, mkStr("type object '"+x.qualname+"' has no attribute '"+name+"'"))
	case *builtinFunc:
		switch name {
		case "__name__", "__qualname__":
			return mkStr(x.name), nil
		}
	case *methodDescr:
		switch name {
		case "__name__":
			return mkStr(x.name), nil
		case "__qualname__":
			return mkStr(x.owner.name + "." + x.name), nil
		}
	case *excObj:
		if name == "args" {
			return newTuple(x.args), nil
		}
	case *intObj, *boolObj:
		i, _ := asIntBig(v)
		switch name {
		case "real", "numerator":
			return mkBig(new(big.Int).Set(i)), nil
		case "imag":
			return mkInt(0), nil
		case "denominator":
			return mkInt(1), nil
		}
	case *floatObj:
		switch name {
		case "real":
			return x, nil
		case "imag":
			return mkFloat(0), nil
		}
	case *TExpr:
		return x.attr(name)
	}
	if name == "__class__" {
		return v.class(), nil
	}
	if m := findMethod(v.class(), name); m != nil {
		return m.bind(v), nil
	}
	return nil, attrErr(v, name)
}

// findMethod looks a method up along the class's MRO.
func findMethod(c *classObj, name string) *methodDescr {
	for _, k := range c.mro {
		if m, ok := k.methods[name]; ok {
			return m
		}
	}
	return nil
}

func attrErr(v Value, name string) error {
	return newExc(excAttributeError, mkStr("'"+typeName(v)+"' object has no attribute '"+name+"'"))
}

// setAttr is setattr(v, name, val).
func setAttr(v Value, name string, val Value) error {
	switch x := v.(type) {
	case *instObj:
		x.attrs.setStr(name, val)
		return nil
	case *scopeVarsObj:
		x.d.setStr(name, val)
		return nil
	case *classObj:
		if x.module == "builtins" {
			return typeErrf("cannot set '" + name + "' attribute of immutable type '" + x.name + "'")
		}
	}
	if findMethod(v.class(), name) != nil {
		return newExc(excAttributeError, mkStr("'"+typeName(v)+"' object attribute '"+name+"' is read-only"))
	}
	return attrErr(v, name)
}

// delAttr is delattr(v, name).
func delAttr(v Value, name string) error {
	switch x := v.(type) {
	case *instObj:
		if ok, _ := x.attrs.del(mkStr(name)); ok {
			return nil
		}
		return attrErr(v, name)
	case *scopeVarsObj:
		if ok, _ := x.d.del(mkStr(name)); ok {
			return nil
		}
		return attrErr(v, name)
	}
	if findMethod(v.class(), name) != nil {
		return newExc(excAttributeError, mkStr("'"+typeName(v)+"' object attribute '"+name+"' is read-only"))
	}
	return attrErr(v, name)
}

// ---------------------------------------------------------------- items

func seqKind(v Value) string {
	switch v.(type) {
	case *listObj:
		return "list"
	case *tupleObj:
		return "tuple"
	}
	return ""
}

// asIndex is PyNumber_AsSsize_t for an index: ok false when v is not an int.
func asIndex(v Value) (int64, bool, error) {
	i, ok := asIntBig(v)
	if !ok {
		return 0, false, nil
	}
	if !i.IsInt64() {
		return 0, true, newExc(excIndexError, mkStr("cannot fit 'int' into an index-sized integer"))
	}
	return i.Int64(), true, nil
}

// getItem is v[key].
func getItem(v, key Value) (Value, error) {
	switch x := v.(type) {
	case *dictObj:
		val, ok, err := x.get(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, newExc(excKeyError, key)
		}
		return val, nil
	case *listObj, *tupleObj:
		items := seqItems(v)
		kind := seqKind(v)
		if s, ok := key.(*sliceObj); ok {
			start, _, step, n, err := sliceIndices(s, len(items))
			if err != nil {
				return nil, err
			}
			out := make([]Value, 0, n)
			for i, k := start, 0; k < n; i, k = i+step, k+1 {
				out = append(out, items[i])
			}
			if kind == "list" {
				return newList(out), nil
			}
			if t, ok := v.(*tupleObj); ok && step == 1 && n == len(t.items) {
				return t, nil
			}
			return newTuple(out), nil
		}
		i, ok, err := asIndex(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, typeErrf(kind + " indices must be integers or slices, not " + typeName(key))
		}
		if i < 0 {
			i += int64(len(items))
		}
		if i < 0 || i >= int64(len(items)) {
			return nil, newExc(excIndexError, mkStr(kind+" index out of range"))
		}
		return items[i], nil
	case *strObj:
		rs := x.runes()
		if s, ok := key.(*sliceObj); ok {
			start, _, step, n, err := sliceIndices(s, len(rs))
			if err != nil {
				return nil, err
			}
			if step == 1 && n == len(rs) {
				return x, nil
			}
			out := make([]rune, 0, n)
			for i, k := start, 0; k < n; i, k = i+step, k+1 {
				out = append(out, rs[i])
			}
			return mkStrRunes(out), nil
		}
		i, ok, err := asIndex(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, typeErrf("string indices must be integers, not '" + typeName(key) + "'")
		}
		if i < 0 {
			i += int64(len(rs))
		}
		if i < 0 || i >= int64(len(rs)) {
			return nil, newExc(excIndexError, mkStr("string index out of range"))
		}
		return mkStr(string(rs[i])), nil
	case *bytesObj:
		if s, ok := key.(*sliceObj); ok {
			start, _, step, n, err := sliceIndices(s, len(x.b))
			if err != nil {
				return nil, err
			}
			out := make([]byte, 0, n)
			for i, k := start, 0; k < n; i, k = i+step, k+1 {
				out = append(out, x.b[i])
			}
			return &bytesObj{out}, nil
		}
		i, ok, err := asIndex(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, typeErrf("byte indices must be integers or slices, not " + typeName(key))
		}
		if i < 0 {
			i += int64(len(x.b))
		}
		if i < 0 || i >= int64(len(x.b)) {
			return nil, newExc(excIndexError, mkStr("index out of range"))
		}
		return mkInt(int64(x.b[i])), nil
	case *scopeObj:
		val, ok := x.get(key)
		if !ok {
			return nil, newExc(excKeyError, key)
		}
		return val, nil
	case *Path:
		if s, ok := key.(*sliceObj); ok {
			return x.slice(s.start, s.stop, s.step)
		}
		i, ok, err := asIndex(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, typeErrf("'" + typeName(key) + "' object is not subscriptable")
		}
		return x.index(int(i))
	case *TExpr:
		return x.item(key)
	case *classObj:
		return nil, typeErrf("type '" + x.name + "' is not subscriptable")
	}
	return nil, typeErrf("'" + typeName(v) + "' object is not subscriptable")
}

func seqItems(v Value) []Value {
	switch x := v.(type) {
	case *listObj:
		return x.items
	case *tupleObj:
		return x.items
	}
	return nil
}

// sliceIndices is PySlice_Unpack with PySlice_AdjustIndices: start, stop,
// step and the number of items.
func sliceIndices(s *sliceObj, length int) (int, int, int, int, error) {
	const maxI = math.MaxInt64
	get := func(v Value, def int64) (int64, error) {
		if isNone(v) {
			return def, nil
		}
		i, ok := asIntBig(v)
		if !ok {
			return 0, typeErrf("slice indices must be integers or None or have an __index__ method")
		}
		if !i.IsInt64() {
			if i.Sign() > 0 {
				return maxI, nil
			}
			return -maxI, nil
		}
		return i.Int64(), nil
	}
	step, err := get(s.step, 1)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if step == 0 {
		return 0, 0, 0, 0, valueErrf("slice step cannot be zero")
	}
	if step < -maxI {
		step = -maxI
	}
	var defStart, defStop int64 = 0, maxI
	if step < 0 {
		defStart, defStop = maxI, -maxI-1
	}
	start, err := get(s.start, defStart)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	stop, err := get(s.stop, defStop)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	n := int64(length)
	adjust := func(i int64) int64 {
		if i < 0 {
			i += n
			if i < 0 {
				if step < 0 {
					return -1
				}
				return 0
			}
		} else if i >= n {
			if step < 0 {
				return n - 1
			}
			return n
		}
		return i
	}
	start, stop = adjust(start), adjust(stop)
	var count int64
	if step < 0 {
		if stop < start {
			count = (start-stop-1)/(-step) + 1
		}
	} else if start < stop {
		count = (stop-start-1)/step + 1
	}
	return int(start), int(stop), int(step), int(count), nil
}

// setItem is v[key] = val.
func setItem(v, key, val Value) error {
	switch x := v.(type) {
	case *dictObj:
		return x.set(key, val)
	case *listObj:
		i, ok, err := asIndex(key)
		if err != nil {
			return err
		}
		if !ok {
			return typeErrf("list indices must be integers or slices, not " + typeName(key))
		}
		if i < 0 {
			i += int64(len(x.items))
		}
		if i < 0 || i >= int64(len(x.items)) {
			return newExc(excIndexError, mkStr("list assignment index out of range"))
		}
		x.items[i] = val
		return nil
	case *scopeObj:
		x.set(key, val)
		return nil
	}
	return typeErrf("'" + typeName(v) + "' object does not support item assignment")
}

// delItem is del v[key].
func delItem(v, key Value) error {
	switch x := v.(type) {
	case *dictObj:
		ok, err := x.del(key)
		if err != nil {
			return err
		}
		if !ok {
			return newExc(excKeyError, key)
		}
		return nil
	case *listObj:
		i, ok, err := asIndex(key)
		if err != nil {
			return err
		}
		if !ok {
			return typeErrf("list indices must be integers or slices, not " + typeName(key))
		}
		if i < 0 {
			i += int64(len(x.items))
		}
		if i < 0 || i >= int64(len(x.items)) {
			return newExc(excIndexError, mkStr("list assignment index out of range"))
		}
		x.items = append(x.items[:i:i], x.items[i+1:]...)
		return nil
	}
	return typeErrf("'" + typeName(v) + "' object doesn't support item deletion")
}
