// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause

package glom_test

import (
	"errors"
	"strings"
	"testing"

	glom "github.com/mahmoud/glom"
)

func mustDict(t *testing.T, kv ...glom.Value) glom.Value {
	t.Helper()
	var keys, vals []glom.Value
	for i := 0; i+1 < len(kv); i += 2 {
		keys, vals = append(keys, kv[i]), append(vals, kv[i+1])
	}
	d, err := glom.NewDict(keys, vals)
	if err != nil {
		t.Fatalf("NewDict: %v", err)
	}
	return d
}

func repr(t *testing.T, v glom.Value) string {
	t.Helper()
	s, err := glom.Repr(v)
	if err != nil {
		t.Fatalf("Repr: %v", err)
	}
	return s
}

func asError(t *testing.T, err error) *glom.Error {
	t.Helper()
	var e *glom.Error
	if !errors.As(err, &e) {
		t.Fatalf("got %v (%T), want a *glom.Error", err, err)
	}
	return e
}

func TestDeepGet(t *testing.T) {
	target := mustDict(t, glom.String("a"), mustDict(t, glom.String("b"), glom.List(glom.Int(1), glom.Int(2))))
	got, err := glom.Glom(target, glom.String("a.b.1"), glom.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := repr(t, got); r != "2" {
		t.Errorf("got %s, want 2", r)
	}
}

func TestPathAccessError(t *testing.T) {
	target := mustDict(t, glom.String("a"), mustDict(t, glom.String("b"), glom.String("c")))
	_, err := glom.Glom(target, glom.String("a.b.d"), glom.Options{})
	e := asError(t, err)
	if e.Class != "PathAccessError" {
		t.Errorf("class %q, want PathAccessError", e.Class)
	}
	wantMsg := `could not access 'd', part 2 of Path('a', 'b', 'd'), got error: AttributeError("'str' object has no attribute 'd'")`
	if e.Message != wantMsg {
		t.Errorf("message\n got  %s\n want %s", e.Message, wantMsg)
	}
	wantStr := "error raised while processing, details below.\n Target-spec trace (most recent last):\n" +
		" - Target: {'a': {'b': 'c'}}\n - Spec: 'a.b.d'\nglom.core.PathAccessError: " + wantMsg
	if e.Str != wantStr {
		t.Errorf("str\n got  %q\n want %q", e.Str, wantStr)
	}
}

func TestDefaultAndCoalesce(t *testing.T) {
	target := mustDict(t, glom.String("c"), glom.String("d"))
	spec, err := glom.Coalesce([]glom.Value{glom.String("a"), glom.String("b"), glom.String("c")}, glom.CoalesceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := glom.Glom(target, spec, glom.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := repr(t, got); r != "'d'" {
		t.Errorf("got %s, want 'd'", r)
	}
	if r := repr(t, spec); r != "Coalesce('a', 'b', 'c')" {
		t.Errorf("repr %s", r)
	}
	missing, _ := glom.Coalesce([]glom.Value{glom.String("x")}, glom.CoalesceOptions{})
	_, err = glom.Glom(target, missing, glom.Options{})
	e := asError(t, err)
	want := "no valid values found. Tried ('x',) and got (PathAccessError) (at path [])"
	if e.Class != "CoalesceError" || e.Message != want {
		t.Errorf("got %s %q, want CoalesceError %q", e.Class, e.Message, want)
	}
	got, err = glom.Glom(target, glom.String("zz"), glom.Options{Default: glom.String("dflt")})
	if err != nil || repr(t, got) != "'dflt'" {
		t.Errorf("default: got %v, %v", got, err)
	}
}

func TestTExpressionRepr(t *testing.T) {
	expr, err := glom.T.Item(glom.String("a"))
	if err == nil {
		expr, err = expr.Attr("b")
	}
	if err == nil {
		expr, err = expr.Call([]glom.Value{glom.Int(1)}, []glom.KW{{Name: "x", Value: glom.Int(2)}})
	}
	if err == nil {
		expr, err = expr.Item(glom.Slice(glom.Int(1), glom.Int(3), glom.None()))
	}
	if err == nil {
		expr, err = expr.Add(glom.Int(1))
	}
	if err != nil {
		t.Fatal(err)
	}
	if r := repr(t, expr); r != "T['a'].b(1, x=2)[1:3] + 1" {
		t.Errorf("got %s", r)
	}
	if _, err := glom.A.Add(glom.Int(1)); err == nil || asError(t, err).Class != "BadSpec" {
		t.Errorf("A + 1: got %v, want BadSpec", err)
	}
	if _, err := glom.T.Attr("__x"); err == nil || asError(t, err).Class != "AttributeError" {
		t.Errorf("T.__x: got %v, want AttributeError", err)
	}
}

func TestBBReprSortsKeys(t *testing.T) {
	d := mustDict(t, glom.String("b"), glom.Int(1), glom.String("a"), glom.List(glom.Int(3)))
	s, err := glom.BBRepr(d)
	if err != nil {
		t.Fatal(err)
	}
	if s != "{'a': [3], 'b': 1}" {
		t.Errorf("bbrepr %s", s)
	}
	if r := repr(t, d); r != "{'b': 1, 'a': [3]}" {
		t.Errorf("repr %s", r)
	}
	long := glom.String(strings.Repeat("x", 2000))
	s, _ = glom.BBRepr(long)
	if n := len([]rune(s)); n != 1024 || !strings.Contains(s, "...") {
		t.Errorf("long str bbrepr has %d characters: %.40s", n, s)
	}
	lenFn, _ := glom.Builtin("len")
	if s, _ := glom.BBRepr(lenFn); s != "len" {
		t.Errorf("bbrepr(len) = %s", s)
	}
}

func TestMatch(t *testing.T) {
	intT, _ := glom.Builtin("int")
	strT, _ := glom.Builtin("str")
	m := glom.NewMatch(mustDict(t, glom.String("id"), intT, glom.String("name"), strT), nil)
	ok, err := m.Matches(mustDict(t, glom.String("id"), glom.Int(1), glom.String("name"), glom.String("x")))
	if err != nil || !ok {
		t.Errorf("Matches: %v, %v", ok, err)
	}
	_, err = m.Verify(mustDict(t, glom.String("id"), glom.String("a"), glom.String("name"), glom.String("x")))
	e := asError(t, err)
	if e.Class != "TypeMatchError" || e.Message != "expected type int, not str" {
		t.Errorf("got %s %q", e.Class, e.Message)
	}
}

func TestAssignAndDelete(t *testing.T) {
	target := mustDict(t, glom.String("a"), mustDict(t))
	dictT, _ := glom.Builtin("dict")
	if _, err := glom.AssignFn(target, glom.String("a.b.c"), glom.String("hi"), dictT); err != nil {
		t.Fatal(err)
	}
	if r := repr(t, target); r != "{'a': {'b': {'c': 'hi'}}}" {
		t.Errorf("after assign: %s", r)
	}
	if _, err := glom.DeleteFn(target, glom.String("a.b"), false); err != nil {
		t.Fatal(err)
	}
	if r := repr(t, target); r != "{'a': {}}" {
		t.Errorf("after delete: %s", r)
	}
	_, err := glom.DeleteFn(target, glom.String("a.zz"), false)
	if e := asError(t, err); e.Class != "PathDeleteError" {
		t.Errorf("got %s", e.Class)
	}
}

func TestIterAndGroup(t *testing.T) {
	mustT := func(e *glom.TExpr, err error) *glom.TExpr {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	nums := glom.List(glom.Int(3), glom.Int(1), glom.Int(3), glom.Int(2))
	it := glom.NewIter(nil, nil).Unique(nil).Map(mustT(glom.T.Mul(glom.Int(10))))
	got, err := glom.Glom(nums, it.All(), glom.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := repr(t, got); r != "[30, 10, 20]" {
		t.Errorf("Iter: %s", r)
	}
	if r := repr(t, it); r != "Iter().unique(T).map(T * 10)" {
		t.Errorf("Iter repr: %s", r)
	}
	rows := glom.List(mustDict(t, glom.String("k"), glom.String("a"), glom.String("v"), glom.Int(1)),
		mustDict(t, glom.String("k"), glom.String("b"), glom.String("v"), glom.Int(2)),
		mustDict(t, glom.String("k"), glom.String("a"), glom.String("v"), glom.Int(3)))
	key := mustT(glom.T.Item(glom.String("k")))
	val := mustT(glom.T.Item(glom.String("v")))
	got, err = glom.Glom(rows, glom.Group(mustDict(t, key, glom.List(val))), glom.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := repr(t, got); r != "{'a': [1, 3], 'b': [2]}" {
		t.Errorf("Group: %s", r)
	}
}
