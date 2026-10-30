// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom's streaming module (Iter and its First), with the lazy
// iterators of itertools and boltons.iterutils it builds on.

package glom

import (
	"math/big"
	"strconv"
)

var (
	iterSpecClass  = glomClass("Iter", "glom.streaming", objectClass)
	streamFirstCls = glomClass("First", "glom.streaming", objectClass)
	firstFn        = &builtinFunc{name: "first", module: "boltons.iterutils"}
	partialFn      = partialClass
)

func init() {
	streamFirstCls.hasDict = false
	firstFn.fn = boltonsFirst
	partialClass.construct = func(args []Value, kw []kwArg) (Value, error) {
		if len(args) == 0 {
			return nil, typeErrf("type 'partial' takes at least one argument")
		}
		return &partialObj{fn: args[0], args: append([]Value(nil), args[1:]...), kw: kw}, nil
	}
}

// boltonsFirst is boltons.iterutils.first(iterable, default=None, key=None):
// next(filter(key, iterable), default).
func boltonsFirst(args []Value, kw []kwArg) (Value, error) {
	var iterable, def, key Value = nil, none, none
	if len(args) > 0 {
		iterable = args[0]
	}
	for _, k := range kw {
		switch k.name {
		case "default":
			def = k.val
		case "key":
			key = k.val
		}
	}
	it, err := pyIter(iterable)
	if err != nil {
		return nil, err
	}
	for {
		item, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return def, nil
		}
		v := item
		if !isNone(key) {
			if v, err = callValue(key, []Value{item}, nil); err != nil {
				return nil, err
			}
		}
		t, err := truthy(v)
		if err != nil {
			return nil, err
		}
		if t {
			return item, nil
		}
	}
}

// iterStep is one method call recorded on an Iter.
type iterStep struct {
	name     string
	args     []Value
	callback func(it *iterObj, scope *scopeObj) (*iterObj, error)
}

// iterSpec is Iter.
type iterSpec struct {
	subspec  Value
	sentinel Value
	steps    []iterStep // in the order the methods were called
}

func (*iterSpec) class() *classObj { return iterSpecClass }

// add is Iter._add_op, which builds the new Iter without passing the
// sentinel along, so it is STOP again.
func (s *iterSpec) add(step iterStep) *iterSpec {
	return &iterSpec{subspec: s.subspec, sentinel: sentSTOP, steps: append(append([]iterStep(nil), s.steps...), step)}
}

func (s *iterSpec) glomit(target Value, scope *scopeObj) (Value, error) {
	it := s.iterate(target, scope)
	for _, st := range s.steps {
		var err error
		if it, err = st.callback(it, scope); err != nil {
			return nil, err
		}
	}
	return it, nil
}

// iterate is Iter._iterate, a generator.
func (s *iterSpec) iterate(target Value, scope *scopeObj) *iterObj {
	var src iterator
	started, done := false, false
	var base *listObj
	i := 0
	return newIterObj("generator", funcIter(func() (Value, bool, error) {
		if done {
			return nil, false, nil
		}
		if !started {
			started = true
			it, err := iterTarget(target, scope)
			if err != nil {
				done = true
				return nil, false, err
			}
			src = it
			base = scopePath(scope)
		}
		for {
			t, ok, err := src.next()
			if err != nil {
				done = true
				return nil, false, err
			}
			if !ok {
				done = true
				return nil, false, nil
			}
			scope.set(pathClass, newList(append(append([]Value(nil), base.items...), mkInt(int64(i)))))
			i++
			yld := t
			if s.subspec != Value(T) {
				if yld, err = recurse(t, s.subspec, scope); err != nil {
					done = true
					return nil, false, err
				}
			}
			if yld == Value(sentSKIP) {
				continue
			}
			if yld == s.sentinel || yld == Value(sentSTOP) {
				done = true
				return nil, false, nil
			}
			return yld, true, nil
		}
	}))
}

// argNames are the parameter names of each Iter method, for its repr.
var iterArgNames = map[string][]string{
	"map": {"subspec"}, "filter": {"key"}, "chunked": {"size", "fill"}, "windowed": {"size"},
	"split": {"sep", "maxsplit"}, "flatten": nil, "unique": {"key"}, "slice": nil, "limit": {"count"},
	"takewhile": {"key"}, "dropwhile": {"key"},
}

func (s *iterSpec) repr() (string, error) {
	var base []Value
	ne, err := pyEq(s.subspec, T)
	if err != nil {
		return "", err
	}
	if !ne || isMLike(s.subspec) {
		base = []Value{s.subspec}
	}
	out, err := formatInvocation("Iter", base, nil, false)
	if err != nil {
		return "", err
	}
	for _, st := range s.steps {
		args := st.args
		var kw []kwArg
		names := iterArgNames[st.name]
		if len(args) > 1 && len(names) > 0 {
			for i, a := range args {
				if i < len(names) {
					kw = append(kw, kwArg{names[i], a})
				}
			}
			args = nil
		}
		c, err := formatInvocation(st.name, args, kw, false)
		if err != nil {
			return "", err
		}
		out += "." + c
	}
	return out, nil
}

func newIterSpec(subspec, sentinel Value) *iterSpec {
	if subspec == nil {
		subspec = T
	}
	if sentinel == nil {
		sentinel = sentSTOP
	}
	return &iterSpec{subspec: subspec, sentinel: sentinel}
}

func (s *iterSpec) mapStep(sub Value) *iterSpec {
	return s.add(iterStep{name: "map", args: []Value{sub}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		return newIterObj("map", funcIter(func() (Value, bool, error) {
			t, ok, err := it.next()
			if err != nil || !ok {
				return nil, false, err
			}
			v, err := recurse(t, sub, scope)
			if err != nil {
				return nil, false, err
			}
			return v, true, nil
		})), nil
	}})
}

func (s *iterSpec) filterStep(key Value) (*iterSpec, error) {
	if key == nil {
		key = T
	}
	check, ok := key.(*checkObj)
	if !ok {
		c, err := newCheck(key, []kwArg{{"default", sentSKIP}})
		if err != nil {
			return nil, err
		}
		check = c
	}
	return s.add(iterStep{name: "filter", args: []Value{key}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		return newIterObj("filter", funcIter(func() (Value, bool, error) {
			for {
				t, ok, err := it.next()
				if err != nil || !ok {
					return nil, false, err
				}
				r, err := recurse(t, check, scope)
				if err != nil {
					return nil, false, err
				}
				if r != Value(sentSKIP) {
					return t, true, nil
				}
			}
		})), nil
	}}), nil
}

func validatePositiveInt(v Value, name string) (int, error) {
	i, err := toInt(v)
	if err != nil {
		return 0, err
	}
	n := i.(*intObj).v
	if n.Sign() <= 0 {
		return 0, valueErrf("expected a positive integer " + name)
	}
	if !n.IsInt64() || n.Int64() > 1<<40 {
		return 1 << 40, nil
	}
	return int(n.Int64()), nil
}

func (s *iterSpec) chunkedStep(size, fill Value) *iterSpec {
	args := []Value{size}
	if fill != nil {
		args = append(args, fill)
	}
	return s.add(iterStep{name: "chunked", args: args, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		started, done := false, false
		n := 0
		return newIterObj("generator", funcIter(func() (Value, bool, error) {
			if done {
				return nil, false, nil
			}
			if !started {
				started = true
				var err error
				if n, err = validatePositiveInt(size, "chunk size"); err != nil {
					done = true
					return nil, false, err
				}
			}
			var chunk []Value
			for len(chunk) < n {
				t, ok, err := it.next()
				if err != nil {
					done = true
					return nil, false, err
				}
				if !ok {
					break
				}
				chunk = append(chunk, t)
			}
			if len(chunk) == 0 {
				done = true
				return nil, false, nil
			}
			if len(chunk) < n && fill != nil {
				for len(chunk) < n {
					chunk = append(chunk, fill)
				}
			}
			return newList(chunk), true, nil
		})), nil
	}})
}

func (s *iterSpec) windowedStep(size Value) *iterSpec {
	return s.add(iterStep{name: "windowed", args: []Value{size}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		n, ok := asIntBig(size)
		if !ok {
			return nil, typeErrf("'" + typeName(size) + "' object cannot be interpreted as an integer")
		}
		if n.Sign() < 0 {
			return nil, valueErrf("n must be >= 0")
		}
		k := int(n.Int64())
		// itertools.tee buffers the source; the i-th tee skips i items now.
		var buf []Value
		srcDone := false
		pull := func() error {
			t, ok, err := it.next()
			if err != nil {
				return err
			}
			if !ok {
				srcDone = true
				return nil
			}
			buf = append(buf, t)
			return nil
		}
		for i := 0; i < k; i++ {
			for len(buf) < i && !srcDone {
				if err := pull(); err != nil {
					return nil, err
				}
			}
			if len(buf) < i {
				return newIterObj("zip", funcIter(func() (Value, bool, error) { return nil, false, nil })), nil
			}
		}
		if k == 0 {
			return newIterObj("zip", funcIter(func() (Value, bool, error) { return nil, false, nil })), nil
		}
		pos := 0
		done := false
		return newIterObj("zip", funcIter(func() (Value, bool, error) {
			if done {
				return nil, false, nil
			}
			for len(buf) < pos+k && !srcDone {
				if err := pull(); err != nil {
					done = true
					return nil, false, err
				}
			}
			if len(buf) < pos+k {
				done = true
				return nil, false, nil
			}
			w := append([]Value(nil), buf[pos:pos+k]...)
			pos++
			return newTuple(w), true, nil
		})), nil
	}})
}

func (s *iterSpec) splitStep(sep, maxsplit Value) *iterSpec {
	if sep == nil {
		sep = none
	}
	if maxsplit == nil {
		maxsplit = none
	}
	return s.add(iterStep{name: "split", args: []Value{sep, maxsplit}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		started, done := false, false
		hasMax, max := false, 0
		var sepFunc func(Value) (bool, error)
		var cur []Value
		count := 0
		finalPending := false
		return newIterObj("generator", funcIter(func() (Value, bool, error) {
			if done {
				return nil, false, nil
			}
			if !started {
				started = true
				if !isNone(maxsplit) {
					m, err := toInt(maxsplit)
					if err != nil {
						done = true
						return nil, false, err
					}
					mi := m.(*intObj).v
					if mi.Sign() == 0 {
						done = true
						return newList([]Value{it}), true, nil
					}
					hasMax = true
					max = int(mi.Int64())
					if !mi.IsInt64() {
						max = 1 << 62
					}
				}
				switch {
				case callable(sep):
					sepFunc = func(x Value) (bool, error) {
						r, err := callValue(sep, []Value{x}, nil)
						if err != nil {
							return false, err
						}
						return truthy(r)
					}
				case isIterable(sep) && !isStrOrBytes(sep):
					fs := newSet(true)
					if err := fs.update(sep); err != nil {
						done = true
						return nil, false, err
					}
					sepFunc = func(x Value) (bool, error) { return fs.contains(x) }
				default:
					sepFunc = func(x Value) (bool, error) { return pyEq(x, sep) }
				}
			}
			if finalPending {
				done = true
				return nil, false, nil
			}
			for {
				t, ok, err := it.next()
				if err != nil {
					done = true
					return nil, false, err
				}
				if !ok {
					finalPending = true
					if len(cur) > 0 || !isNone(sep) {
						out := newList(cur)
						cur = nil
						return out, true, nil
					}
					done = true
					return nil, false, nil
				}
				isSep := false
				if !(hasMax && count >= max) {
					if isSep, err = sepFunc(t); err != nil {
						done = true
						return nil, false, err
					}
				}
				if isSep {
					if isNone(sep) && len(cur) == 0 {
						continue
					}
					count++
					out := newList(cur)
					cur = nil
					return out, true, nil
				}
				cur = append(cur, t)
			}
		})), nil
	}})
}

func isStrOrBytes(v Value) bool {
	switch v.(type) {
	case *strObj, *bytesObj:
		return true
	}
	return false
}

func (s *iterSpec) flattenStep() *iterSpec {
	return s.add(iterStep{name: "flatten", callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
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
	}})
}

func (s *iterSpec) uniqueStep(key Value) *iterSpec {
	if key == nil {
		key = T
	}
	return s.add(iterStep{name: "unique", args: []Value{key}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		seen := newSet(false)
		return newIterObj("generator", funcIter(func() (Value, bool, error) {
			for {
				t, ok, err := it.next()
				if err != nil || !ok {
					return nil, false, err
				}
				k, err := recurse(t, key, scope)
				if err != nil {
					return nil, false, err
				}
				in, err := seen.contains(k)
				if err != nil {
					return nil, false, err
				}
				if !in {
					if err := seen.add(k); err != nil {
						return nil, false, err
					}
					return t, true, nil
				}
			}
		})), nil
	}})
}

// isliceArgs is islice_new's argument handling: start, stop (-1 for None)
// and step, with CPython 3.12's errors.
func isliceArgs(args []Value) (int64, int64, int64, error) {
	if len(args) == 0 {
		return 0, 0, 0, typeErrf("islice expected at least 2 arguments, got 1")
	}
	if len(args) > 3 {
		return 0, 0, 0, typeErrf("islice expected at most 4 arguments, got " + strconv.Itoa(len(args)+1))
	}
	// asSsize is PyNumber_AsSsize_t with the error cleared: -1 on failure.
	asSsize := func(v Value) int64 {
		i, ok := asIntBig(v)
		if !ok || !i.IsInt64() {
			return -1
		}
		return i.Int64()
	}
	stopErr := valueErrf("Stop argument for islice() must be None or an integer: 0 <= x <= sys.maxsize.")
	var start, stop, step int64 = 0, -1, 1
	if len(args) == 1 {
		if !isNone(args[0]) {
			if stop = asSsize(args[0]); stop == -1 {
				return 0, 0, 0, stopErr
			}
		}
	} else {
		if !isNone(args[0]) {
			start = asSsize(args[0])
		}
		if !isNone(args[1]) {
			if stop = asSsize(args[1]); stop == -1 {
				return 0, 0, 0, stopErr
			}
		}
	}
	if start < 0 || stop < -1 {
		return 0, 0, 0, valueErrf("Indices for islice() must be None or an integer: 0 <= x <= sys.maxsize.")
	}
	if len(args) == 3 && !isNone(args[2]) {
		step = asSsize(args[2])
	}
	if step < 1 {
		return 0, 0, 0, valueErrf("Step for islice() must be a positive integer or None.")
	}
	return start, stop, step, nil
}

// isliceIter is islice_next.
func isliceIter(it iterator, start, stop, step int64) *iterObj {
	next, cnt := start, int64(0)
	done := false
	return newIterObj("islice", funcIter(func() (Value, bool, error) {
		if done {
			return nil, false, nil
		}
		for cnt < next {
			_, ok, err := it.next()
			if err != nil {
				done = true
				return nil, false, err
			}
			if !ok {
				done = true
				return nil, false, nil
			}
			cnt++
		}
		if stop != -1 && cnt >= stop {
			done = true
			return nil, false, nil
		}
		v, ok, err := it.next()
		if err != nil {
			done = true
			return nil, false, err
		}
		if !ok {
			done = true
			return nil, false, nil
		}
		cnt++
		old := next
		next += step
		if next < old || (stop != -1 && next > stop) {
			next = stop
		}
		return v, true, nil
	}))
}

func (s *iterSpec) sliceStep(args []Value) (*iterSpec, error) {
	if _, _, _, err := isliceArgs(args); err != nil {
		if isExcInstance(err, excTypeError) {
			r, rerr := pyRepr(newTuple(args))
			if rerr != nil {
				return nil, rerr
			}
			return nil, typeErrf("invalid slice arguments: " + r)
		}
		return nil, err
	}
	return s.add(iterStep{name: "slice", args: args, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		start, stop, step, err := isliceArgs(args)
		if err != nil {
			return nil, err
		}
		return isliceIter(it, start, stop, step), nil
	}}), nil
}

func (s *iterSpec) limitStep(count Value) *iterSpec {
	return s.add(iterStep{name: "limit", args: []Value{count}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		start, stop, step, err := isliceArgs([]Value{count})
		if err != nil {
			return nil, err
		}
		return isliceIter(it, start, stop, step), nil
	}})
}

func (s *iterSpec) whileStep(name string, key Value) *iterSpec {
	if key == nil {
		key = T
	}
	return s.add(iterStep{name: name, args: []Value{key}, callback: func(it *iterObj, scope *scopeObj) (*iterObj, error) {
		done, dropping := false, true
		return newIterObj(name, funcIter(func() (Value, bool, error) {
			if done {
				return nil, false, nil
			}
			for {
				t, ok, err := it.next()
				if err != nil || !ok {
					return nil, false, err
				}
				if name == "dropwhile" && !dropping {
					return t, true, nil
				}
				r, err := recurse(t, key, scope)
				if err != nil {
					return nil, false, err
				}
				tr, err := truthy(r)
				if err != nil {
					return nil, false, err
				}
				if name == "takewhile" {
					if tr {
						return t, true, nil
					}
					done = true
					return nil, false, nil
				}
				if !tr {
					dropping = false
					return t, true, nil
				}
			}
		})), nil
	}})
}

// streamFirst is streaming.First.
type streamFirst struct {
	key, def Value
	call     *callObj
}

func (*streamFirst) class() *classObj { return streamFirstCls }

func newStreamFirst(key, def Value) *streamFirst {
	if key == nil {
		key = T
	}
	if def == nil {
		def = none
	}
	inner, _ := newCall(partialFn, tup(&boundSpecGlom{&specObj{spec: key, scope: newDict()}}), dictOf("scope", S))
	specGlom := &specObj{spec: inner, scope: newDict()}
	c, _ := newCall(firstFn, tup(T), dictOf("default", def, "key", specGlom))
	return &streamFirst{key: key, def: def, call: c}
}

// dictOf builds a dict from alternating keys (Go strings or Values) and values.
func dictOf(kv ...any) *dictObj {
	d := newDict()
	for i := 0; i+1 < len(kv); i += 2 {
		var k Value
		switch x := kv[i].(type) {
		case string:
			k = mkStr(x)
		case Value:
			k = x
		}
		_ = d.set(k, kv[i+1].(Value))
	}
	return d
}

func (f *streamFirst) glomit(target Value, scope *scopeObj) (Value, error) {
	return f.call.glomit(target, scope)
}

func (f *streamFirst) repr() (string, error) {
	k, err := bbrepr(f.key)
	if err != nil {
		return "", err
	}
	if isNone(f.def) {
		return "First(" + k + ")", nil
	}
	d, err := bbrepr(f.def)
	return "First(" + k + ", default=" + d + ")", err
}

// boundSpecGlom is the bound method Spec(key).glom.
type boundSpecGlom struct{ spec *specObj }

var methodClass = func() *classObj {
	c := newClass("method", "builtins", objectClass)
	return c
}()

func (*boundSpecGlom) class() *classObj { return methodClass }

func (b *boundSpecGlom) call(args []Value, kw []kwArg) (Value, error) {
	var scope Value = newDict()
	for _, k := range kw {
		if k.name == "scope" {
			scope = k.val
		}
	}
	if len(args) != 1 {
		return nil, typeErrf("Spec.glom() missing 1 required positional argument: 'target'")
	}
	return b.spec.glomCall(args[0], scope)
}

func (s *iterSpec) all() Value { return &pipeObj{steps: tup(s, listClass)} }

func (s *iterSpec) first(key, def Value) Value { return tup(s, newStreamFirst(key, def)) }

var _ = big.NewInt
