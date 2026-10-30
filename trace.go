// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. The "Target-spec trace" glom adds to the errors glom()
// raises: _unpack_stack and format_target_spec_trace.

package glom

import (
	"strconv"
	"strings"
)

// traceWidth is TRACE_WIDTH: face's get_wrap_width(max_width=110) with no
// terminal and no ROWS/COLUMNS in the environment is 80 - 2.
const traceWidth = 78

type frame struct {
	scope    *scopeObj
	spec     Value
	target   Value
	err      Value // nil for None
	branches []*scopeObj
}

func listScopes(v Value) []*scopeObj {
	l, ok := v.(*listObj)
	if !ok {
		return nil
	}
	out := make([]*scopeObj, len(l.items))
	for i, it := range l.items {
		out[i] = it.(*scopeObj)
	}
	return out
}

// unpackStack is _unpack_stack(scope, only_errors=True).
func unpackStack(sc *scopeObj) []*frame {
	var stack []*frame
	m := sc.maps[0]
	cur := newScopeOf(m)
	get := func(d *dictObj, k Value) Value {
		v, _, _ := d.get(k)
		return v
	}
	for {
		lastV, ok, _ := m.get(sentLastChild)
		if !ok {
			stack = append(stack, &frame{scope: cur, spec: get(m, specClass), target: get(m, T), err: get(m, sentCurError)})
			break
		}
		child := lastV.(*scopeObj)
		branches := listScopes(get(m, sentChildErrors))
		if len(branches) == 1 && branches[0] == child {
			branches = nil
		}
		stack = append(stack, &frame{scope: cur, spec: get(m, specClass), target: get(m, T), err: get(m, sentCurError),
			branches: branches})
		covered := false
		for _, b := range branches {
			if b == child {
				covered = true
			}
		}
		if covered {
			break
		}
		m = child.maps[0]
		cur = newScopeOf(m)
	}
	for i := 0; i+1 < len(stack); i++ {
		if stack[i].err == stack[i+1].err {
			stack[i].err = nil
		}
	}
	for len(stack) > 1 && stack[len(stack)-1].err == nil {
		stack = stack[:len(stack)-1]
	}
	return stack
}

// formatTraceValue is _format_trace_value: bbrepr with escaped single
// quotes restored, cut to maxlen with "... (len=N)" or "...".
func formatTraceValue(v Value, maxlen int) (string, error) {
	s, err := bbrepr(v)
	if err != nil {
		return "", err
	}
	s = strings.ReplaceAll(s, `\'`, "'")
	rs := []rune(s)
	if len(rs) > maxlen {
		suffix := "..."
		if n, err := pyLen(v); err == nil {
			suffix = "... (len=" + strconv.Itoa(n) + ")"
		}
		cut := pySliceEnd(len(rs), maxlen-len([]rune(suffix)))
		s = string(rs[:cut]) + suffix
	}
	return s, nil
}

// pySliceEnd is the end index of s[:k] for a string of length n.
func pySliceEnd(n, k int) int {
	if k < 0 {
		k += n
		if k < 0 {
			k = 0
		}
	}
	if k > n {
		k = n
	}
	return k
}

// formatTargetSpecTrace is format_target_spec_trace.
func formatTargetSpecTrace(sc *scopeObj, rootErr Value, width, depth int, prevTarget Value, lastBranch bool) (string, error) {
	var segments []string
	indent := " " + strings.Repeat("|", depth)
	tick := "- "
	if depth > 0 {
		tick = "| "
	}
	mk := func(label, t string) func(Value) (string, error) {
		if t == "" {
			t = tick
		}
		pre := indent + t + label + ": "
		w := width - len([]rune(pre))
		return func(v Value) (string, error) {
			s, err := formatTraceValue(v, w)
			return pre + s, err
		}
	}
	fmtT, fmtS, fmtB := mk("Target", ""), mk("Spec", ""), mk("Spec", "+ ")
	lastLineError := false
	if prevTarget == nil {
		prevTarget = sentMissing
	}
	for _, f := range unpackStack(sc) {
		target := f.target
		if target == nil {
			target = none
		}
		spec := f.spec
		if spec == nil {
			spec = none
		}
		if target != prevTarget {
			s, err := fmtT(target)
			if err != nil {
				return "", err
			}
			segments = append(segments, s)
		}
		prevTarget = target
		if len(f.branches) > 0 {
			s, err := fmtB(spec)
			if err != nil {
				return "", err
			}
			segments = append(segments, s)
			for i, b := range f.branches {
				last := false
				if i == len(f.branches)-1 {
					last = lastBranch
				}
				r, err := formatTargetSpecTrace(b, rootErr, width, depth+1, prevTarget, last)
				if err != nil {
					return "", err
				}
				segments = append(segments, r)
			}
		} else {
			s, err := fmtS(spec)
			if err != nil {
				return "", err
			}
			segments = append(segments, s)
		}
		if f.err != nil && f.err != rootErr {
			lastLineError = true
			segments = append(segments, indent+tick+formatExceptionOnly(f.err.(*excObj)))
		} else {
			lastLineError = false
		}
	}
	if depth > 0 && len(segments) > 0 {
		remark := func(s, m string) string {
			rs := []rune(s)
			a := min(depth+1, len(rs))
			b := min(depth+2, len(rs))
			return string(rs[:a]) + m + string(rs[b:])
		}
		segments[0] = remark(segments[0], "\\")
		if !lastBranch || lastLineError {
			segments[len(segments)-1] = remark(segments[len(segments)-1], "X")
		}
	}
	return strings.Join(segments, "\n"), nil
}

func strItoa(i int64) string { return strconv.FormatInt(i, 10) }
