// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause

package glom_test

// recorded_test.go is generated from the probe kit by gold_testdata.py: it
// builds each recorded case through the public API only.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"

	glom "github.com/mahmoud/glom"
)

type obj = map[string]any

var (
	_ = hex.DecodeString
	_ = big.NewInt
	_ = strconv.Itoa
)

// pyQuote is json.dumps(s, ensure_ascii=True).
func pyQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r >= 0x7f && r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tags are the keys that name a node of the case tree.
var tags = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`let ref i f b l t d set fs obj fn slice sent T Path PathText PathIndex PathSlice
		PathFromT Spec Val Vars Ref Auto Fill Pipe Call Invoke Coalesce Match Regex And Or Not M MSub MCmp BitAnd
		BitOr Invert Optional Required Switch Check Assign Delete Iter Fold Sum Flatten Merge Group GFirst IFirst
		Avg Max Min Limit`) {
		tags[t] = true
	}
}

func tagOf(m obj) string {
	for k := range m {
		if tags[k] {
			return k
		}
	}
	return ""
}

// builder turns the case tree into glom values; named nodes are shared.
type builder struct{ named map[string]glom.Value }

func newBuilder() *builder { return &builder{named: map[string]glom.Value{}} }

func (bd *builder) build(x any) (glom.Value, error) {
	switch v := x.(type) {
	case nil:
		return glom.None(), nil
	case bool:
		return glom.Bool(v), nil
	case string:
		return glom.String(v), nil
	case obj:
		tag := tagOf(v)
		switch tag {
		case "let":
			val, err := bd.build(v["v"])
			if err != nil {
				return nil, err
			}
			bd.named[v["let"].(string)] = val
			return val, nil
		case "ref":
			return bd.named[v["ref"].(string)], nil
		case "i":
			n, ok := new(big.Int).SetString(v["i"].(string), 10)
			if !ok {
				return nil, fmt.Errorf("bad int %v", v["i"])
			}
			return glom.BigInt(n), nil
		case "l":
			items, err := bd.list(v["l"])
			if err != nil {
				return nil, err
			}
			return glom.List(items...), nil
		case "t":
			items, err := bd.list(v["t"])
			if err != nil {
				return nil, err
			}
			return glom.Tuple(items...), nil
		case "d":
			var keys, vals []glom.Value
			for _, p := range v["d"].([]any) {
				pair := p.([]any)
				k, err := bd.build(pair[0])
				if err != nil {
					return nil, err
				}
				val, err := bd.build(pair[1])
				if err != nil {
					return nil, err
				}
				keys, vals = append(keys, k), append(vals, val)
			}
			return glom.NewDict(keys, vals)
		}
		return bd.buildTag(tag, v)
	}
	return nil, fmt.Errorf("bad node %v", x)
}

func (bd *builder) list(x any) ([]glom.Value, error) {
	var out []glom.Value
	for _, it := range x.([]any) {
		v, err := bd.build(it)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// opt builds m[key] when present, else returns nil (not passed).
func (bd *builder) opt(m obj, key string) (glom.Value, error) {
	x, ok := m[key]
	if !ok {
		return nil, nil
	}
	return bd.build(x)
}

func errParts(err error) (string, string, string) {
	var e *glom.Error
	if errors.As(err, &e) {
		return e.Class, e.Message, e.Str
	}
	return "?", err.Error(), err.Error()
}

func errLines(err error, trace bool, out *[]string) {
	class, msg, str := errParts(err)
	*out = append(*out, "E "+class+" "+pyQuote(msg))
	if trace {
		*out = append(*out, "X "+pyQuote(str))
	}
}

func reprLine(prefix string, v glom.Value, trace bool, out *[]string) {
	s, err := glom.Repr(v)
	if err != nil {
		errLines(err, trace, out)
		return
	}
	*out = append(*out, prefix+pyQuote(s))
}

func buildErr(err error, out *[]string) {
	class, msg, _ := errParts(err)
	*out = append(*out, "C "+class+" "+pyQuote(msg))
}

func glomOptions(bd *builder, step obj) (glom.Options, error) {
	var o glom.Options
	m, _ := step["opts"].(obj)
	var err error
	if o.Default, err = bd.opt(m, "default"); err != nil {
		return o, err
	}
	if o.SkipExc, err = bd.opt(m, "skip_exc"); err != nil {
		return o, err
	}
	if o.Scope, err = bd.opt(m, "scope"); err != nil {
		return o, err
	}
	return o, nil
}

func opGlom(step obj, out *[]string) {
	bd := newBuilder()
	target, err := bd.build(step["target"])
	if err != nil {
		buildErr(err, out)
		return
	}
	spec, err := bd.build(step["spec"])
	if err != nil {
		buildErr(err, out)
		return
	}
	o, err := glomOptions(bd, step)
	if err != nil {
		buildErr(err, out)
		return
	}
	trace, _ := step["trace"].(bool)
	res, err := glom.Glom(target, spec, o)
	if err != nil {
		errLines(err, trace, out)
	} else {
		reprLine("R ", res, trace, out)
	}
	if st, _ := step["show_target"].(bool); st {
		reprLine("T ", target, false, out)
	}
}

func (bd *builder) kwargs(x any) ([]glom.KW, error) {
	var out []glom.KW
	if x == nil {
		return nil, nil
	}
	for _, p := range x.([]any) {
		pair := p.([]any)
		v, err := bd.build(pair[1])
		if err != nil {
			return nil, err
		}
		out = append(out, glom.KW{Name: pair[0].(string), Value: v})
	}
	return out, nil
}

func tagF(bd *builder, m obj) (glom.Value, error) {
	f, err := strconv.ParseFloat(m["f"].(string), 64)
	if err != nil {
		return nil, err
	}
	return glom.Float(f), nil
}

func tagB(bd *builder, m obj) (glom.Value, error) {
	b, err := hex.DecodeString(m["b"].(string))
	if err != nil {
		return nil, err
	}
	return glom.Bytes(b), nil
}

func tagSet(bd *builder, m obj) (glom.Value, error) {
	items, err := bd.list(m["set"])
	if err != nil {
		return nil, err
	}
	return glom.NewSet(items...)
}

func tagFs(bd *builder, m obj) (glom.Value, error) {
	items, err := bd.list(m["fs"])
	if err != nil {
		return nil, err
	}
	return glom.NewFrozenSet(items...)
}

func tagObj(bd *builder, m obj) (glom.Value, error) {
	var names []string
	var vals []glom.Value
	for _, p := range m["a"].([]any) {
		pair := p.([]any)
		v, err := bd.build(pair[1])
		if err != nil {
			return nil, err
		}
		names, vals = append(names, pair[0].(string)), append(vals, v)
	}
	return glom.NewObject(m["obj"].(string), names, vals), nil
}

func tagFn(bd *builder, m obj) (glom.Value, error) { return glom.Builtin(m["fn"].(string)) }

func tagSlice(bd *builder, m obj) (glom.Value, error) {
	parts, err := bd.list(m["slice"])
	if err != nil {
		return nil, err
	}
	for len(parts) < 3 {
		parts = append(parts, glom.None())
	}
	return glom.Slice(parts[0], parts[1], parts[2]), nil
}

func tagSent(bd *builder, m obj) (glom.Value, error) {
	switch m["sent"].(string) {
	case "SKIP":
		return glom.SKIP, nil
	case "STOP":
		return glom.STOP, nil
	case "UP":
		return glom.UP, nil
	case "ROOT":
		return glom.ROOT, nil
	}
	return glom.MODE, nil
}

func tagT(bd *builder, m obj) (glom.Value, error) {
	t := glom.T
	switch m["T"].(string) {
	case "S":
		t = glom.S
	case "A":
		t = glom.A
	}
	ops, _ := m["ops"].([]any)
	for _, o := range ops {
		op := o.([]any)
		var err error
		arg := func() (glom.Value, error) { return bd.build(op[1]) }
		switch op[0].(string) {
		case "attr":
			t, err = t.Attr(op[1].(string))
		case "dunder":
			t, err = t.Dunder(op[1].(string))
		case "item":
			var a glom.Value
			if a, err = arg(); err == nil {
				t, err = t.Item(a)
			}
		case "call":
			var args []glom.Value
			var kw []glom.KW
			if args, err = bd.list(op[1]); err == nil {
				if kw, err = bd.kwargs(op[2]); err == nil {
					t, err = t.Call(args, kw)
				}
			}
		case "star":
			t, err = t.Star()
		case "starstar":
			t, err = t.StarStar()
		case "invert":
			t, err = t.Invert()
		case "neg":
			t, err = t.Neg()
		default:
			var a glom.Value
			if a, err = arg(); err == nil {
				switch op[0].(string) {
				case "add":
					t, err = t.Add(a)
				case "sub":
					t, err = t.Sub(a)
				case "mul":
					t, err = t.Mul(a)
				case "floordiv":
					t, err = t.FloorDiv(a)
				case "truediv":
					t, err = t.TrueDiv(a)
				case "mod":
					t, err = t.Mod(a)
				case "pow":
					t, err = t.Pow(a)
				case "and":
					t, err = t.And(a)
				case "or":
					t, err = t.Or(a)
				case "xor":
					t, err = t.Xor(a)
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

func tagPath(bd *builder, m obj) (glom.Value, error) {
	parts, err := bd.list(m["Path"])
	if err != nil {
		return nil, err
	}
	return glom.NewPath(parts...)
}

func tagPathText(bd *builder, m obj) (glom.Value, error) {
	return glom.PathFromText(m["PathText"].(string)), nil
}

func tagPathIndex(bd *builder, m obj) (glom.Value, error) {
	a := m["PathIndex"].(obj)
	p, err := bd.build(a["path"])
	if err != nil {
		return nil, err
	}
	n, _ := a["i"].(json.Number).Int64()
	return p.(*glom.Path).Index(int(n))
}

func tagPathSlice(bd *builder, m obj) (glom.Value, error) {
	a := m["PathSlice"].(obj)
	p, err := bd.build(a["path"])
	if err != nil {
		return nil, err
	}
	parts, err := bd.list(a["s"])
	if err != nil {
		return nil, err
	}
	for len(parts) < 3 {
		parts = append(parts, glom.None())
	}
	return p.(*glom.Path).Slice(parts[0], parts[1], parts[2])
}

func tagPathFromT(bd *builder, m obj) (glom.Value, error) {
	p, err := bd.build(m["PathFromT"])
	if err != nil {
		return nil, err
	}
	return p.(*glom.Path).FromT(), nil
}

func tagSpec(bd *builder, m obj) (glom.Value, error) {
	a := m["Spec"].(obj)
	spec, err := bd.build(a["spec"])
	if err != nil {
		return nil, err
	}
	scope, err := bd.opt(a, "scope")
	if err != nil {
		return nil, err
	}
	return glom.Spec(spec, scope), nil
}

func tagVal(bd *builder, m obj) (glom.Value, error) {
	v, err := bd.build(m["Val"])
	if err != nil {
		return nil, err
	}
	return glom.Val(v), nil
}

func tagVars(bd *builder, m obj) (glom.Value, error) {
	a := m["Vars"].(obj)
	base, err := bd.opt(a, "base")
	if err != nil {
		return nil, err
	}
	kw, err := bd.kwargs(a["kw"])
	if err != nil {
		return nil, err
	}
	return glom.Vars(base, kw)
}

func tagRef(bd *builder, m obj) (glom.Value, error) {
	a := m["Ref"].(obj)
	sub, err := bd.opt(a, "sub")
	if err != nil {
		return nil, err
	}
	return glom.Ref(a["name"].(string), sub), nil
}

func tagAuto(bd *builder, m obj) (glom.Value, error) {
	spec, err := bd.opt(m["Auto"].(obj), "spec")
	if err != nil {
		return nil, err
	}
	return glom.Auto(spec), nil
}

func tagFill(bd *builder, m obj) (glom.Value, error) {
	spec, err := bd.opt(m["Fill"].(obj), "spec")
	if err != nil {
		return nil, err
	}
	return glom.Fill(spec), nil
}

func tagPipe(bd *builder, m obj) (glom.Value, error) {
	steps, err := bd.list(m["Pipe"])
	if err != nil {
		return nil, err
	}
	return glom.Pipe(steps...), nil
}

func tagCall(bd *builder, m obj) (glom.Value, error) {
	a := m["Call"].(obj)
	fn, err := bd.opt(a, "func")
	if err != nil {
		return nil, err
	}
	args, err := bd.opt(a, "args")
	if err != nil {
		return nil, err
	}
	kwargs, err := bd.opt(a, "kwargs")
	if err != nil {
		return nil, err
	}
	return glom.Call(fn, args, kwargs)
}

func tagInvoke(bd *builder, m obj) (glom.Value, error) {
	a := m["Invoke"].(obj)
	fn, err := bd.build(a["func"])
	if err != nil {
		return nil, err
	}
	var inv *glom.Invoke
	if sf, _ := a["specfunc"].(bool); sf {
		inv = glom.InvokeSpecfunc(fn)
	} else if inv, err = glom.NewInvoke(fn); err != nil {
		return nil, err
	}
	chain, _ := a["chain"].([]any)
	for _, s := range chain {
		st := s.([]any)
		switch st[0].(string) {
		case "C", "S":
			args, err := bd.list(st[1])
			if err != nil {
				return nil, err
			}
			kw, err := bd.kwargs(st[2])
			if err != nil {
				return nil, err
			}
			if st[0].(string) == "C" {
				inv = inv.Constants(args, kw)
			} else {
				inv = inv.Specs(args, kw)
			}
		default:
			o := st[1].(obj)
			sa, err := bd.opt(o, "args")
			if err != nil {
				return nil, err
			}
			sk, err := bd.opt(o, "kwargs")
			if err != nil {
				return nil, err
			}
			if inv, err = inv.Star(sa, sk); err != nil {
				return nil, err
			}
		}
	}
	return inv, nil
}

func tagCoalesce(bd *builder, m obj) (glom.Value, error) {
	a := m["Coalesce"].(obj)
	subs, err := bd.list(a["subs"])
	if err != nil {
		return nil, err
	}
	kw, err := bd.kwargs(a["kw"])
	if err != nil {
		return nil, err
	}
	var o glom.CoalesceOptions
	for _, k := range kw {
		switch k.Name {
		case "default":
			o.Default = k.Value
		case "default_factory":
			o.DefaultFactory = k.Value
		case "skip":
			o.Skip = k.Value
		case "skip_exc":
			o.SkipExc = k.Value
		}
	}
	return glom.Coalesce(subs, o)
}

func tagMatch(bd *builder, m obj) (glom.Value, error) {
	a := m["Match"].(obj)
	spec, err := bd.build(a["spec"])
	if err != nil {
		return nil, err
	}
	def, err := bd.opt(a, "default")
	if err != nil {
		return nil, err
	}
	return glom.NewMatch(spec, def), nil
}

func tagRegex(bd *builder, m obj) (glom.Value, error) {
	a := m["Regex"].(obj)
	flags := 0
	if f, ok := a["flags"].(json.Number); ok {
		n, _ := f.Int64()
		flags = int(n)
	}
	fn, _ := a["func"].(string)
	return glom.Regex(a["pattern"].(string), flags, fn)
}

func tagAnd(bd *builder, m obj) (glom.Value, error) {
	a := m["And"].(obj)
	ch, err := bd.list(a["ch"])
	if err != nil {
		return nil, err
	}
	def, err := bd.opt(a, "default")
	if err != nil {
		return nil, err
	}
	return glom.And(ch, def)
}

func tagOr(bd *builder, m obj) (glom.Value, error) {
	a := m["Or"].(obj)
	ch, err := bd.list(a["ch"])
	if err != nil {
		return nil, err
	}
	def, err := bd.opt(a, "default")
	if err != nil {
		return nil, err
	}
	return glom.Or(ch, def)
}

func tagNot(bd *builder, m obj) (glom.Value, error) {
	c, err := bd.build(m["Not"])
	if err != nil {
		return nil, err
	}
	return glom.Not(c), nil
}

func tagM(bd *builder, m obj) (glom.Value, error) { return glom.M, nil }

func tagMSub(bd *builder, m obj) (glom.Value, error) {
	s, err := bd.build(m["MSub"])
	if err != nil {
		return nil, err
	}
	return glom.MSub(s)
}

func tagMCmp(bd *builder, m obj) (glom.Value, error) {
	a := m["MCmp"].([]any)
	l, err := bd.build(a[0])
	if err != nil {
		return nil, err
	}
	r, err := bd.build(a[2])
	if err != nil {
		return nil, err
	}
	return glom.MCompare(l, a[1].(string), r)
}

func tagBitAnd(bd *builder, m obj) (glom.Value, error) {
	a := m["BitAnd"].([]any)
	l, err := bd.build(a[0])
	if err != nil {
		return nil, err
	}
	r, err := bd.build(a[1])
	if err != nil {
		return nil, err
	}
	return glom.BitAnd(l, r)
}

func tagBitOr(bd *builder, m obj) (glom.Value, error) {
	a := m["BitOr"].([]any)
	l, err := bd.build(a[0])
	if err != nil {
		return nil, err
	}
	r, err := bd.build(a[1])
	if err != nil {
		return nil, err
	}
	return glom.BitOr(l, r)
}

func tagInvert(bd *builder, m obj) (glom.Value, error) {
	v, err := bd.build(m["Invert"])
	if err != nil {
		return nil, err
	}
	return glom.Invert(v)
}

func tagOptional(bd *builder, m obj) (glom.Value, error) {
	a := m["Optional"].(obj)
	k, err := bd.build(a["key"])
	if err != nil {
		return nil, err
	}
	def, err := bd.opt(a, "default")
	if err != nil {
		return nil, err
	}
	return glom.Optional(k, def)
}

func tagRequired(bd *builder, m obj) (glom.Value, error) {
	k, err := bd.build(m["Required"])
	if err != nil {
		return nil, err
	}
	return glom.Required(k)
}

func tagSwitch(bd *builder, m obj) (glom.Value, error) {
	a := m["Switch"].(obj)
	cases, err := bd.build(a["cases"])
	if err != nil {
		return nil, err
	}
	def, err := bd.opt(a, "default")
	if err != nil {
		return nil, err
	}
	return glom.Switch(cases, def)
}

func tagCheck(bd *builder, m obj) (glom.Value, error) {
	a := m["Check"].(obj)
	spec, err := bd.opt(a, "spec")
	if err != nil {
		return nil, err
	}
	kw, err := bd.kwargs(a["kw"])
	if err != nil {
		return nil, err
	}
	var o glom.CheckOptions
	for _, k := range kw {
		switch k.Name {
		case "type":
			o.Type = k.Value
		case "instance_of":
			o.InstanceOf = k.Value
		case "equal_to":
			o.EqualTo = k.Value
		case "one_of":
			o.OneOf = k.Value
		case "validate":
			o.Validate = k.Value
		case "default":
			o.Default = k.Value
		}
	}
	return glom.Check(spec, o)
}

func tagAssign(bd *builder, m obj) (glom.Value, error) {
	a := m["Assign"].(obj)
	p, err := bd.build(a["path"])
	if err != nil {
		return nil, err
	}
	v, err := bd.build(a["val"])
	if err != nil {
		return nil, err
	}
	missing, err := bd.opt(a, "missing")
	if err != nil {
		return nil, err
	}
	return glom.Assign(p, v, missing)
}

func tagDelete(bd *builder, m obj) (glom.Value, error) {
	a := m["Delete"].(obj)
	p, err := bd.build(a["path"])
	if err != nil {
		return nil, err
	}
	ignore, _ := a["ignore_missing"].(bool)
	return glom.Delete(p, ignore)
}

func tagIter(bd *builder, m obj) (glom.Value, error) {
	a := m["Iter"].(obj)
	sub, err := bd.opt(a, "sub")
	if err != nil {
		return nil, err
	}
	sent, err := bd.opt(a, "sentinel")
	if err != nil {
		return nil, err
	}
	it := glom.NewIter(sub, sent)
	chain, _ := a["chain"].([]any)
	for _, s := range chain {
		st := s.([]any)
		o := obj{}
		if len(st) > 1 {
			o = st[1].(obj)
		}
		opt := func(k string) glom.Value {
			v, e := bd.opt(o, k)
			if e != nil && err == nil {
				err = e
			}
			return v
		}
		switch st[0].(string) {
		case "map":
			it = it.Map(opt("spec"))
		case "filter":
			it = it.Filter(opt("key"))
		case "chunked":
			it = it.Chunked(opt("size"), opt("fill"))
		case "windowed":
			it = it.Windowed(opt("size"))
		case "split":
			it = it.Split(opt("sep"), opt("maxsplit"))
		case "flatten":
			it = it.Flatten()
		case "unique":
			it = it.Unique(opt("key"))
		case "slice":
			args, e := bd.list(o["args"])
			if e != nil {
				return nil, e
			}
			if it, err = it.Slice(args...); err != nil {
				return nil, err
			}
		case "limit":
			it = it.Limit(opt("count"))
		case "takewhile":
			it = it.Takewhile(opt("key"))
		case "dropwhile":
			it = it.Dropwhile(opt("key"))
		}
		if err != nil {
			return nil, err
		}
	}
	switch term := a["term"].(type) {
	case string:
		return it.All(), nil
	case obj:
		k, err := bd.opt(term, "key")
		if err != nil {
			return nil, err
		}
		d, err := bd.opt(term, "default")
		if err != nil {
			return nil, err
		}
		return it.First(k, d), nil
	}
	return it, nil
}

func tagIFirst(bd *builder, m obj) (glom.Value, error) {
	a := m["IFirst"].(obj)
	k, err := bd.opt(a, "key")
	if err != nil {
		return nil, err
	}
	d, err := bd.opt(a, "default")
	if err != nil {
		return nil, err
	}
	return glom.IterFirst(k, d), nil
}

func foldArgs(bd *builder, a obj) (glom.Value, glom.Value, glom.Value, error) {
	sub, err := bd.opt(a, "sub")
	if err != nil {
		return nil, nil, nil, err
	}
	init, err := bd.opt(a, "init")
	if err != nil {
		return nil, nil, nil, err
	}
	op, err := bd.opt(a, "op")
	return sub, init, op, err
}

func tagFold(bd *builder, m obj) (glom.Value, error) {
	sub, init, op, err := foldArgs(bd, m["Fold"].(obj))
	if err != nil {
		return nil, err
	}
	return glom.Fold(sub, init, op)
}

func tagSum(bd *builder, m obj) (glom.Value, error) {
	sub, init, _, err := foldArgs(bd, m["Sum"].(obj))
	if err != nil {
		return nil, err
	}
	return glom.Sum(sub, init)
}

func tagFlatten(bd *builder, m obj) (glom.Value, error) {
	sub, init, _, err := foldArgs(bd, m["Flatten"].(obj))
	if err != nil {
		return nil, err
	}
	return glom.Flatten(sub, init)
}

func tagMerge(bd *builder, m obj) (glom.Value, error) {
	sub, init, op, err := foldArgs(bd, m["Merge"].(obj))
	if err != nil {
		return nil, err
	}
	return glom.Merge(sub, init, op)
}

func tagGroup(bd *builder, m obj) (glom.Value, error) {
	s, err := bd.build(m["Group"])
	if err != nil {
		return nil, err
	}
	return glom.Group(s), nil
}

func tagGFirst(bd *builder, m obj) (glom.Value, error) { return glom.GroupFirst(), nil }

func tagAvg(bd *builder, m obj) (glom.Value, error) { return glom.Avg(), nil }

func tagMax(bd *builder, m obj) (glom.Value, error) { return glom.Max(), nil }

func tagMin(bd *builder, m obj) (glom.Value, error) { return glom.Min(), nil }

func tagLimit(bd *builder, m obj) (glom.Value, error) {
	a := m["Limit"].(obj)
	n, err := bd.build(a["n"])
	if err != nil {
		return nil, err
	}
	sub, err := bd.opt(a, "sub")
	if err != nil {
		return nil, err
	}
	return glom.Limit(n, sub), nil
}

func opGlommer(step obj, out *[]string) {
	bd := newBuilder()
	target, err := bd.build(step["target"])
	if err != nil {
		buildErr(err, out)
		return
	}
	spec, err := bd.build(step["spec"])
	if err != nil {
		buildErr(err, out)
		return
	}
	o, err := glomOptions(bd, step)
	if err != nil {
		buildErr(err, out)
		return
	}
	defaults := true
	if d, ok := step["defaults"].(bool); ok {
		defaults = d
	}
	trace, _ := step["trace"].(bool)
	res, err := glom.NewGlommer(defaults).Glom(target, spec, o)
	if err != nil {
		errLines(err, trace, out)
	} else {
		reprLine("R ", res, trace, out)
	}
	if st, _ := step["show_target"].(bool); st {
		reprLine("T ", target, false, out)
	}
}

func opRepr(step obj, out *[]string) {
	spec, err := newBuilder().build(step["spec"])
	if err != nil {
		buildErr(err, out)
		return
	}
	reprLine("P ", spec, false, out)
}

func opBBRepr(step obj, out *[]string) {
	spec, err := newBuilder().build(step["spec"])
	if err != nil {
		buildErr(err, out)
		return
	}
	s, err := glom.BBRepr(spec)
	if err != nil {
		errLines(err, false, out)
		return
	}
	*out = append(*out, "B "+pyQuote(s))
}

func opPathm(step obj, out *[]string) {
	bd := newBuilder()
	pv, err := bd.build(step["path"])
	if err != nil {
		buildErr(err, out)
		return
	}
	args, err := bd.list(step["args"])
	if err != nil {
		buildErr(err, out)
		return
	}
	p := pv.(*glom.Path)
	switch step["m"].(string) {
	case "values":
		reprLine("V ", p.Values(), false, out)
	case "items":
		reprLine("V ", p.Items(), false, out)
	case "len":
		*out = append(*out, "V "+pyQuote(strconv.Itoa(p.Len())))
	case "startswith":
		ok, err := p.StartsWith(args[0])
		if err != nil {
			errLines(err, false, out)
			return
		}
		s := "False"
		if ok {
			s = "True"
		}
		*out = append(*out, "V "+pyQuote(s))
	}
}

func opMatches(step obj, out *[]string) {
	bd := newBuilder()
	mv, err := bd.build(step["spec"])
	if err != nil {
		buildErr(err, out)
		return
	}
	target, err := bd.build(step["target"])
	if err != nil {
		buildErr(err, out)
		return
	}
	ok, err := mv.(*glom.Match).Matches(target)
	if err != nil {
		errLines(err, false, out)
		return
	}
	s := "False"
	if ok {
		s = "True"
	}
	*out = append(*out, "V "+pyQuote(s))
}

func opVerify(step obj, out *[]string) {
	bd := newBuilder()
	mv, err := bd.build(step["spec"])
	if err != nil {
		buildErr(err, out)
		return
	}
	target, err := bd.build(step["target"])
	if err != nil {
		buildErr(err, out)
		return
	}
	trace, _ := step["trace"].(bool)
	res, err := mv.(*glom.Match).Verify(target)
	if err != nil {
		errLines(err, trace, out)
		return
	}
	reprLine("V ", res, false, out)
}

func fnArgs(bd *builder, step obj) (obj, error) {
	out := obj{}
	args, _ := step["args"].([]any)
	for _, p := range args {
		pair := p.([]any)
		v, err := bd.build(pair[1])
		if err != nil {
			return nil, err
		}
		out[pair[0].(string)] = v
	}
	return out, nil
}

func val(a obj, k string) glom.Value {
	if v, ok := a[k]; ok {
		return v.(glom.Value)
	}
	return nil
}

func opFns(step obj, out *[]string) {
	bd := newBuilder()
	target, err := bd.build(step["target"])
	if err != nil {
		buildErr(err, out)
		return
	}
	a, err := fnArgs(bd, step)
	if err != nil {
		buildErr(err, out)
		return
	}
	trace, _ := step["trace"].(bool)
	var res glom.Value
	switch step["op"].(string) {
	case "assignfn":
		res, err = glom.AssignFn(target, val(a, "path"), val(a, "val"), val(a, "missing"))
	case "deletefn":
		ignore := false
		if v, ok := a["ignore_missing"]; ok {
			s, _ := glom.Repr(v.(glom.Value))
			ignore = s == "True"
		}
		res, err = glom.DeleteFn(target, val(a, "path"), ignore)
	case "flattenfn":
		res, err = glom.FlattenFn(target, val(a, "spec"), val(a, "init"), val(a, "levels"))
	case "mergefn":
		res, err = glom.MergeFn(target, val(a, "spec"), val(a, "init"), val(a, "op"))
	}
	if err != nil {
		errLines(err, trace, out)
	} else {
		reprLine("R ", res, trace, out)
	}
	if st, _ := step["show_target"].(bool); st {
		reprLine("T ", target, false, out)
	}
}

func runOp(step obj, out *[]string) {
	switch step["op"] {
	case "assignfn":
		opFns(step, out)
	case "bbrepr":
		opBBRepr(step, out)
	case "deletefn":
		opFns(step, out)
	case "flattenfn":
		opFns(step, out)
	case "glom":
		opGlom(step, out)
	case "glommer":
		opGlommer(step, out)
	case "matches":
		opMatches(step, out)
	case "mergefn":
		opFns(step, out)
	case "pathm":
		opPathm(step, out)
	case "repr":
		opRepr(step, out)
	case "verify":
		opVerify(step, out)
	}
}

func (bd *builder) buildTag(tag string, m obj) (glom.Value, error) {
	switch tag {
	case "And":
		return tagAnd(bd, m)
	case "Assign":
		return tagAssign(bd, m)
	case "Auto":
		return tagAuto(bd, m)
	case "Avg":
		return tagAvg(bd, m)
	case "BitAnd":
		return tagBitAnd(bd, m)
	case "BitOr":
		return tagBitOr(bd, m)
	case "Call":
		return tagCall(bd, m)
	case "Check":
		return tagCheck(bd, m)
	case "Coalesce":
		return tagCoalesce(bd, m)
	case "Delete":
		return tagDelete(bd, m)
	case "Fill":
		return tagFill(bd, m)
	case "Flatten":
		return tagFlatten(bd, m)
	case "Fold":
		return tagFold(bd, m)
	case "GFirst":
		return tagGFirst(bd, m)
	case "Group":
		return tagGroup(bd, m)
	case "IFirst":
		return tagIFirst(bd, m)
	case "Invert":
		return tagInvert(bd, m)
	case "Invoke":
		return tagInvoke(bd, m)
	case "Iter":
		return tagIter(bd, m)
	case "Limit":
		return tagLimit(bd, m)
	case "M":
		return tagM(bd, m)
	case "MCmp":
		return tagMCmp(bd, m)
	case "MSub":
		return tagMSub(bd, m)
	case "Match":
		return tagMatch(bd, m)
	case "Max":
		return tagMax(bd, m)
	case "Merge":
		return tagMerge(bd, m)
	case "Min":
		return tagMin(bd, m)
	case "Not":
		return tagNot(bd, m)
	case "Optional":
		return tagOptional(bd, m)
	case "Or":
		return tagOr(bd, m)
	case "Path":
		return tagPath(bd, m)
	case "PathFromT":
		return tagPathFromT(bd, m)
	case "PathIndex":
		return tagPathIndex(bd, m)
	case "PathSlice":
		return tagPathSlice(bd, m)
	case "PathText":
		return tagPathText(bd, m)
	case "Pipe":
		return tagPipe(bd, m)
	case "Ref":
		return tagRef(bd, m)
	case "Regex":
		return tagRegex(bd, m)
	case "Required":
		return tagRequired(bd, m)
	case "Spec":
		return tagSpec(bd, m)
	case "Sum":
		return tagSum(bd, m)
	case "Switch":
		return tagSwitch(bd, m)
	case "T":
		return tagT(bd, m)
	case "Val":
		return tagVal(bd, m)
	case "Vars":
		return tagVars(bd, m)
	case "b":
		return tagB(bd, m)
	case "f":
		return tagF(bd, m)
	case "fn":
		return tagFn(bd, m)
	case "fs":
		return tagFs(bd, m)
	case "obj":
		return tagObj(bd, m)
	case "sent":
		return tagSent(bd, m)
	case "set":
		return tagSet(bd, m)
	case "slice":
		return tagSlice(bd, m)
	}
	return nil, fmt.Errorf("unknown tag %q", tag)
}

// TestRecordedCases replays cases recorded from the original glom under
// CPython 3.12 (testdata/recorded.jsonl) through the public API and compares
// every line the case prints.
func TestRecordedCases(t *testing.T) {
	data, err := os.ReadFile("testdata/recorded.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			Case  json.RawMessage `json:"case"`
			Lines []string        `json:"lines"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(strings.NewReader(string(rec.Case)))
		dec.UseNumber()
		var c obj
		if err := dec.Decode(&c); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range c["steps"].([]any) {
			runOp(s.(obj), &got)
		}
		id, _ := c["id"].(string)
		if len(got) != len(rec.Lines) {
			t.Errorf("%s: printed %d lines, want %d: %q", id, len(got), len(rec.Lines), got)
			continue
		}
		for i := range got {
			if got[i] != rec.Lines[i] {
				t.Errorf("%s: line %d:\n got  %s\n want %s", id, i, got[i], rec.Lines[i])
			}
		}
		n++
	}
	if n == 0 {
		t.Fatal("no recorded cases")
	}
}
