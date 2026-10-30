// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

// Package pyre reproduces CPython 3.12's re module for str patterns: the same pattern syntax,
// the same re.error messages and positions, the same compiled program and a backtracking
// matcher with the same match results, plus re.sub templates, Match.expand and re.escape.
//
// Texts are []rune and every position is a code point index, as in Python. Character
// properties (\w, \d, \s, \b, IGNORECASE folding) come from internal/pyuni, which describes
// Unicode 15.0.0 exactly as CPython 3.12 does.
package pyre

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Error is re.error: an invalid pattern or replacement template.
type Error struct {
	Msg     string // the bare message (re.error.msg)
	Pattern string // the pattern or template the error is about
	Pos     int    // re.error.pos, meaningful when HasPos
	HasPos  bool   // false for errors raised without a position ("look-behind requires fixed-width pattern")
}

func newError(msg, pattern string, pos int) *Error {
	return &Error{Msg: msg, Pattern: pattern, Pos: pos, HasPos: true}
}

// Message is str(re.error): the message, " at position N" and, for a pattern containing a
// newline, " (line L, column C)".
func (e *Error) Message() string {
	if !e.HasPos {
		return e.Msg
	}
	msg := fmt.Sprintf("%s at position %d", e.Msg, e.Pos)
	if strings.Contains(e.Pattern, "\n") {
		lineno, colno := e.LineCol()
		msg += fmt.Sprintf(" (line %d, column %d)", lineno, colno)
	}
	return msg
}

// LineCol returns re.error.lineno and re.error.colno (valid when HasPos).
func (e *Error) LineCol() (int, int) {
	rs := []rune(e.Pattern)
	// pattern.count('\n', 0, pos) and pattern.rfind('\n', 0, pos) with Python slice bounds
	stop := e.Pos
	if stop < 0 {
		stop = max(stop+len(rs), 0)
	}
	stop = min(stop, len(rs))
	lineno, last := 1, -1
	for i := 0; i < stop; i++ {
		if rs[i] == '\n' {
			lineno++
			last = i
		}
	}
	return lineno, e.Pos - last
}

// Error returns str(re.error), as Message does.
func (e *Error) Error() string { return e.Message() }

// PyError is a Python exception other than re.error: OverflowError for a too large repeat
// count, ValueError for conflicting flags, IndexError for an unknown group name in a
// replacement template.
type PyError struct {
	Class string // the exception class name, e.g. "OverflowError"
	Msg   string // str(exc)
}

// Error returns "Class: message".
func (e *PyError) Error() string { return e.Class + ": " + e.Msg }

// Flag values, identical to Python's re module.
const (
	TEMPLATE   = 1
	IGNORECASE = 2
	LOCALE     = 4
	MULTILINE  = 8
	DOTALL     = 16
	UNICODE    = 32
	VERBOSE    = 64
	DEBUG      = 128
	ASCII      = 256
)

// GroupName is one entry of Pattern.groupindex.
type GroupName struct {
	Name  string
	Index int
}

// NamedGroup is one entry of Match.groupdict(); Value is nil when the group did not take part.
type NamedGroup struct {
	Name  string
	Value *string
}

// Pattern is a compiled regular expression (re.Pattern).
type Pattern struct {
	pattern    string
	flags      int
	groups     int
	groupIndex []GroupName
	groupMap   map[string]int
	code       []uint32
}

// Compile is re.compile(pattern, flags). The error is a *Error (re.error) or a *PyError
// (OverflowError for a repeat count >= 2**32-1, ValueError for LOCALE or ASCII|UNICODE).
// Unlike CPython, whose parser is recursive Python code, nesting depth is not limited (no
// RecursionError), and DEBUG (128) prints nothing; it is only kept in Flags.
func Compile(pattern string, flags int) (p *Pattern, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *Error:
				p, err = nil, e
			case *PyError:
				p, err = nil, e
			default:
				panic(r)
			}
		}
	}()
	sp := parse(pattern, flags)
	code := compileCode(sp, flags)
	st := sp.state
	return &Pattern{
		pattern:    pattern,
		flags:      flags | st.flags,
		groups:     st.groups() - 1,
		groupIndex: append([]GroupName(nil), st.groupOrder...),
		groupMap:   st.groupdict,
		code:       code,
	}, nil
}

// MustCompile is Compile for patterns known to be valid; it panics otherwise.
func MustCompile(pattern string, flags int) *Pattern {
	p, err := Compile(pattern, flags)
	if err != nil {
		panic(err)
	}
	return p
}

// Pattern is re.Pattern.pattern.
func (p *Pattern) Pattern() string { return p.pattern }

// Flags is re.Pattern.flags: the flags argument combined with the inline flags and UNICODE
// (added unless ASCII is set).
func (p *Pattern) Flags() int { return p.flags }

// Groups is re.Pattern.groups, the number of capturing groups.
func (p *Pattern) Groups() int { return p.groups }

// GroupIndex is re.Pattern.groupindex in definition order.
func (p *Pattern) GroupIndex() []GroupName { return append([]GroupName(nil), p.groupIndex...) }

// GroupIndexOf returns the number of the named group.
func (p *Pattern) GroupIndexOf(name string) (int, bool) {
	i, ok := p.groupMap[name]
	return i, ok
}

// Match is a successful match (re.Match).
type Match struct {
	re        *Pattern
	text      []rune
	pos       int
	endpos    int
	lastindex int
	spans     []int
}

func clampRange(n, pos, endpos int) (int, int) {
	pos = min(max(pos, 0), n)
	endpos = min(max(endpos, 0), n)
	return pos, endpos
}

func (p *Pattern) result(m *machine, pos, endpos int) *Match {
	spans := make([]int, 2*(p.groups+1))
	spans[0], spans[1] = m.start, m.ptr
	for i, j := 0, 0; i < p.groups; i, j = i+1, j+2 {
		if j+1 <= m.lastmark && m.marks[j] >= 0 && m.marks[j+1] >= 0 {
			spans[j+2], spans[j+3] = m.marks[j], m.marks[j+1]
		} else {
			spans[j+2], spans[j+3] = -1, -1
		}
	}
	return &Match{re: p, text: m.text, pos: pos, endpos: endpos, lastindex: m.lastindex, spans: spans}
}

// Match is re.Pattern.match(text, pos): a match starting exactly at pos, or nil.
func (p *Pattern) Match(text []rune, pos int) *Match { return p.MatchRange(text, pos, len(text)) }

// MatchRange is re.Pattern.match(text, pos, endpos); pos and endpos are clamped as Python
// does.
func (p *Pattern) MatchRange(text []rune, pos, endpos int) *Match {
	return p.matchAt(text, pos, endpos, false)
}

// FullMatch is re.Pattern.fullmatch(text, pos, endpos).
func (p *Pattern) FullMatch(text []rune, pos, endpos int) *Match {
	return p.matchAt(text, pos, endpos, true)
}

func (p *Pattern) matchAt(text []rune, pos, endpos int, all bool) *Match {
	pos, endpos = clampRange(len(text), pos, endpos)
	m := newMachine(p.code, text, p.groups, endpos)
	m.start, m.ptr = pos, pos
	m.matchAll = all
	if !m.run(0, true) {
		return nil
	}
	return p.result(m, pos, endpos)
}

// search is SRE(search) from start; mustAdvance forbids an empty match at start.
func (p *Pattern) search(text []rune, start, endpos int, mustAdvance bool, pos int) *Match {
	if start > endpos {
		return nil
	}
	m := newMachine(p.code, text, p.groups, endpos)
	ptr := start
	m.start, m.ptr = ptr, ptr
	m.mustAdvance = mustAdvance
	ok := m.run(0, true)
	m.mustAdvance = false
	if !ok && p.code[0] == opAT && (p.code[1] == atBEGINNING || p.code[1] == atBEGINNING_STRING) {
		return nil
	}
	for !ok && ptr < endpos {
		ptr++
		m.lastmark, m.lastindex = -1, -1
		m.start, m.ptr = ptr, ptr
		ok = m.run(0, false)
	}
	if !ok {
		return nil
	}
	return p.result(m, pos, endpos)
}

// Search is re.Pattern.search(text, pos): the first match at or after pos, or nil.
func (p *Pattern) Search(text []rune, pos int) *Match { return p.SearchRange(text, pos, len(text)) }

// SearchRange is re.Pattern.search(text, pos, endpos).
func (p *Pattern) SearchRange(text []rune, pos, endpos int) *Match {
	pos, endpos = clampRange(len(text), pos, endpos)
	return p.search(text, pos, endpos, false, pos)
}

// FindIter is list(re.Pattern.finditer(text, pos, endpos)).
func (p *Pattern) FindIter(text []rune, pos, endpos int) []*Match {
	pos, endpos = clampRange(len(text), pos, endpos)
	var out []*Match
	start, mustAdvance := pos, false
	for {
		m := p.search(text, start, endpos, mustAdvance, pos)
		if m == nil {
			return out
		}
		out = append(out, m)
		mustAdvance = m.spans[1] == m.spans[0]
		start = m.spans[1]
	}
}

// FindAll is re.Pattern.findall(text, pos, endpos): each item is the whole match (no groups),
// the single group, or all groups; groups that did not take part give "".
func (p *Pattern) FindAll(text []rune, pos, endpos int) [][]string {
	var out [][]string
	for _, m := range p.FindIter(text, pos, endpos) {
		var item []string
		if p.groups == 0 {
			s, _ := m.Group(0)
			item = []string{s}
		} else {
			for g := 1; g <= p.groups; g++ {
				s, _ := m.Group(g)
				item = append(item, s)
			}
		}
		out = append(out, item)
	}
	return out
}

// Split is re.Pattern.split(text, maxsplit). Groups that did not take part come back as nil.
func (p *Pattern) Split(text []rune, maxsplit int) []*string {
	var out []*string
	str := func(a, b int) *string { s := string(text[a:b]); return &s }
	last, start, mustAdvance := 0, 0, false
	n := 0
	for maxsplit <= 0 || n < maxsplit {
		if maxsplit < 0 {
			break
		}
		m := p.search(text, start, len(text), mustAdvance, 0)
		if m == nil {
			break
		}
		out = append(out, str(last, m.spans[0]))
		for g := 1; g <= p.groups; g++ {
			if m.spans[2*g] >= 0 {
				out = append(out, str(m.spans[2*g], m.spans[2*g+1]))
			} else {
				out = append(out, nil)
			}
		}
		n++
		mustAdvance = m.spans[1] == m.spans[0]
		last, start = m.spans[1], m.spans[1]
	}
	return append(out, str(last, len(text)))
}

// Sub is re.Pattern.sub(repl, text, count) for a replacement template string: backslash
// escapes and group references (\1, \g<1>, \g<name>) are expanded. count <= 0 replaces every
// match (a negative count, as in Python, replaces none). Template errors are *Error (re.error)
// or *PyError (IndexError for an unknown group name), reported even when nothing matches.
func (p *Pattern) Sub(repl string, text []rune, count int) (string, error) {
	s, _, err := p.Subn(repl, text, count)
	return s, err
}

// Subn is re.Pattern.subn(repl, text, count): the new text and the number of replacements.
func (p *Pattern) Subn(repl string, text []rune, count int) (string, int, error) {
	if !strings.Contains(repl, `\`) {
		return p.subFunc(func(*Match) (string, error) { return repl, nil }, text, count)
	}
	t, err := p.compileTemplate(repl)
	if err != nil {
		return "", 0, err
	}
	return p.subFunc(func(m *Match) (string, error) { return t.expand(m), nil }, text, count)
}

// SubFunc is re.Pattern.sub(fn, text, count) with a callable replacement. An error returned by
// fn stops the substitution and is returned.
func (p *Pattern) SubFunc(fn func(*Match) (string, error), text []rune, count int) (string, error) {
	s, _, err := p.subFunc(fn, text, count)
	return s, err
}

func (p *Pattern) subFunc(fn func(*Match) (string, error), text []rune, count int) (string, int, error) {
	var b strings.Builder
	i, n := 0, 0
	start, mustAdvance := 0, false
	for count == 0 || n < count {
		m := p.search(text, start, len(text), mustAdvance, 0)
		if m == nil {
			break
		}
		b.WriteString(string(text[i:m.spans[0]]))
		s, err := fn(m)
		if err != nil {
			return "", n, err
		}
		b.WriteString(s)
		i = m.spans[1]
		n++
		mustAdvance = m.spans[1] == m.spans[0]
		start = m.spans[1]
	}
	b.WriteString(string(text[i:]))
	return b.String(), n, nil
}

// Re is Match.re.
func (m *Match) Re() *Pattern { return m.re }

// Text is Match.string.
func (m *Match) Text() []rune { return m.text }

// Pos is Match.pos.
func (m *Match) Pos() int { return m.pos }

// EndPos is Match.endpos.
func (m *Match) EndPos() int { return m.endpos }

func (m *Match) valid(g int) bool { return g >= 0 && 2*g+1 < len(m.spans) }

// Start is Match.start(g): -1 when the group did not take part (or does not exist).
func (m *Match) Start(g int) int {
	if !m.valid(g) {
		return -1
	}
	return m.spans[2*g]
}

// End is Match.end(g): -1 when the group did not take part (or does not exist).
func (m *Match) End(g int) int {
	if !m.valid(g) {
		return -1
	}
	return m.spans[2*g+1]
}

// Span is Match.span(g).
func (m *Match) Span(g int) (int, int) { return m.Start(g), m.End(g) }

// Group is Match.group(g); ok is false when the group did not take part (Python's None) or
// does not exist.
func (m *Match) Group(g int) (string, bool) {
	if !m.valid(g) || m.spans[2*g] < 0 {
		return "", false
	}
	return string(m.text[m.spans[2*g]:m.spans[2*g+1]]), true
}

// GroupByName is Match.group(name).
func (m *Match) GroupByName(name string) (string, bool) {
	g, ok := m.re.groupMap[name]
	if !ok {
		return "", false
	}
	return m.Group(g)
}

// Groups is Match.groups(): one entry per capturing group, nil for a group that did not take
// part.
func (m *Match) Groups() []*string {
	out := make([]*string, m.re.groups)
	for g := 1; g <= m.re.groups; g++ {
		if s, ok := m.Group(g); ok {
			out[g-1] = &s
		}
	}
	return out
}

// GroupDict is Match.groupdict() in group definition order.
func (m *Match) GroupDict() []NamedGroup {
	out := make([]NamedGroup, 0, len(m.re.groupIndex))
	for _, gn := range m.re.groupIndex {
		var v *string
		if s, ok := m.Group(gn.Index); ok {
			v = &s
		}
		out = append(out, NamedGroup{Name: gn.Name, Value: v})
	}
	return out
}

// LastIndex is Match.lastindex, or -1 for None.
func (m *Match) LastIndex() int { return m.lastindex }

// LastGroup is Match.lastgroup: the name of the last closed group, if it has one.
func (m *Match) LastGroup() (string, bool) {
	if m.lastindex < 0 {
		return "", false
	}
	for _, gn := range m.re.groupIndex {
		if gn.Index == m.lastindex {
			return gn.Name, true
		}
	}
	return "", false
}

// Expand is Match.expand(template).
func (m *Match) Expand(template string) (string, error) {
	t, err := m.re.compileTemplate(template)
	if err != nil {
		return "", err
	}
	return t.expand(m), nil
}

// Repr is repr(match): <re.Match object; span=(0, 3), match='abc'>. As in CPython the repr of
// the matched text is cut to 50 characters (%.50R).
func (m *Match) Repr() string {
	s, _ := m.Group(0)
	r := []rune(StrRepr(s))
	if len(r) > 50 {
		r = r[:50]
	}
	return fmt.Sprintf("<re.Match object; span=(%d, %d), match=%s>", m.spans[0], m.spans[1], string(r))
}

// Escape is re.escape(s).
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && strings.ContainsRune("()[]{}?*+-|^$\\.&~# \t\n\r\v\f", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// Code returns the compiled program as _sre receives it, without the INFO block CPython
// prepends (an optimisation only). It is meant for differential testing.
func (p *Pattern) Code() []uint32 { return append([]uint32(nil), p.code...) }
