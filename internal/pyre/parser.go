// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

package pyre

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mahmoud/glom/internal/pyuni"
)

// This file follows re/_parser.py of CPython 3.12: the same tokenizer, the same intermediate
// form and the same error messages and positions.

const (
	specialChars = ".\\[{()*+?^$|"
	repeatChars  = "*+?{"
	digits       = "0123456789"
	octDigits    = "01234567"
	hexDigits    = "0123456789abcdefABCDEF"
	asciiLetters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	whitespace   = " \t\n\r\v\f"
)

var escapes = map[string]rune{
	`\a`: '\a', `\b`: '\b', `\f`: '\f', `\n`: '\n', `\r`: '\r', `\t`: '\t', `\v`: '\v', `\\`: '\\',
}

var categories = map[string]*item{
	`\A`: {op: opAT, arg: atBEGINNING_STRING},
	`\b`: {op: opAT, arg: atBOUNDARY},
	`\B`: {op: opAT, arg: atNON_BOUNDARY},
	`\d`: {op: opIN, set: []*item{{op: opCATEGORY, arg: catDIGIT}}},
	`\D`: {op: opIN, set: []*item{{op: opCATEGORY, arg: catNOT_DIGIT}}},
	`\s`: {op: opIN, set: []*item{{op: opCATEGORY, arg: catSPACE}}},
	`\S`: {op: opIN, set: []*item{{op: opCATEGORY, arg: catNOT_SPACE}}},
	`\w`: {op: opIN, set: []*item{{op: opCATEGORY, arg: catWORD}}},
	`\W`: {op: opIN, set: []*item{{op: opCATEGORY, arg: catNOT_WORD}}},
	`\Z`: {op: opAT, arg: atEND_STRING},
}

// item is one (op, av) pair of the parser's intermediate form.
type item struct {
	op       int
	arg      int // literal code point, position or category code, group number, assertion direction
	lo, hi   int // range bounds, repeat counts
	set      []*item
	sub      *subPattern // repeat body, group body, assertion body, conditional yes-branch
	no       *subPattern // conditional no-branch, nil for none
	branches []*subPattern
	group    int // capturing group number, 0 for none
	addFlags int
	delFlags int
	words    []uint32 // CHARSET and BIGCHARSET payload
}

func (it *item) clone() *item {
	c := *it
	return &c
}

// equal is Python's == on two (op, av) tuples: sub-patterns compare by identity.
func (it *item) equal(o *item) bool {
	if it.op != o.op {
		return false
	}
	switch it.op {
	case opLITERAL, opNOT_LITERAL, opAT, opCATEGORY, opGROUPREF:
		return it.arg == o.arg
	case opANY, opNEGATE:
		return true
	case opRANGE:
		return it.lo == o.lo && it.hi == o.hi
	case opIN:
		if len(it.set) != len(o.set) {
			return false
		}
		for i := range it.set {
			if !it.set[i].equal(o.set[i]) {
				return false
			}
		}
		return true
	case opSUBPATTERN:
		return it.group == o.group && it.addFlags == o.addFlags && it.delFlags == o.delFlags && it.sub == o.sub
	case opMIN_REPEAT, opMAX_REPEAT, opPOSSESSIVE_REPEAT:
		return it.lo == o.lo && it.hi == o.hi && it.sub == o.sub
	case opBRANCH:
		if len(it.branches) != len(o.branches) {
			return false
		}
		for i := range it.branches {
			if it.branches[i] != o.branches[i] {
				return false
			}
		}
		return true
	case opASSERT, opASSERT_NOT:
		return it.arg == o.arg && it.sub == o.sub
	case opGROUPREF_EXISTS:
		return it.arg == o.arg && it.sub == o.sub && it.no == o.no
	case opATOMIC_GROUP:
		return it.sub == o.sub
	}
	return false
}

func (it *item) key() string { return fmt.Sprintf("%d:%d:%d:%d", it.op, it.arg, it.lo, it.hi) }

// uniq drops repeated set members, keeping the first of each.
func uniq(items []*item) []*item {
	seen := map[string]bool{}
	out := make([]*item, 0, len(items))
	for _, it := range items {
		k := it.key()
		if !seen[k] {
			seen[k] = true
			out = append(out, it)
		}
	}
	return out
}

type parseState struct {
	flags            int
	groupdict        map[string]int
	groupwidths      []*[2]uint64
	lookbehindgroups int // -1 for None
	grouprefpos      map[int]int
	grouprefOrder    []int
	groupOrder       []GroupName // groupdict in insertion order
}

func newState(flags int) *parseState {
	return &parseState{flags: flags, groupdict: map[string]int{}, groupwidths: []*[2]uint64{nil},
		lookbehindgroups: -1, grouprefpos: map[int]int{}}
}

func (s *parseState) groups() int { return len(s.groupwidths) }

func (s *parseState) opengroup(name string, named bool) (int, string) {
	gid := s.groups()
	s.groupwidths = append(s.groupwidths, nil)
	if s.groups() > maxGroups {
		return 0, "too many groups"
	}
	if named {
		if ogid, ok := s.groupdict[name]; ok {
			return 0, fmt.Sprintf("redefinition of group name %s as group %d; was group %d", strRepr(name), gid, ogid)
		}
		s.groupdict[name] = gid
		s.groupOrder = append(s.groupOrder, GroupName{Name: name, Index: gid})
	}
	return gid, ""
}

func (s *parseState) closegroup(gid int, p *subPattern) {
	w := p.getwidth()
	s.groupwidths[gid] = &w
}

func (s *parseState) checkgroup(gid int) bool {
	return gid < s.groups() && s.groupwidths[gid] != nil
}

func (s *parseState) checklookbehindgroup(gid int, src *tokenizer) {
	if s.lookbehindgroups >= 0 {
		if !s.checkgroup(gid) {
			panic(src.error("cannot refer to an open group", 0))
		}
		if gid >= s.lookbehindgroups {
			panic(src.error("cannot refer to group defined in the same lookbehind subpattern", 0))
		}
	}
}

type subPattern struct {
	state *parseState
	data  []*item
	width *[2]uint64
}

func newSub(state *parseState) *subPattern { return &subPattern{state: state} }

func (p *subPattern) append(it *item) { p.data = append(p.data, it) }

func addWidth(a, b uint64) uint64 {
	if a > maxWidth-b {
		return maxWidth
	}
	return a + b
}

func mulWidth(a, b uint64) uint64 {
	if a != 0 && b > maxWidth/a {
		return maxWidth
	}
	return a * b
}

// getwidth returns the (min, max) width of the pattern; maxWidth stands for "unbounded".
func (p *subPattern) getwidth() [2]uint64 {
	if p.width != nil {
		return *p.width
	}
	var lo, hi uint64
	for _, it := range p.data {
		switch it.op {
		case opBRANCH:
			i, j := maxWidth, uint64(0)
			for _, b := range it.branches {
				w := b.getwidth()
				i = min(i, w[0])
				j = max(j, w[1])
			}
			lo, hi = addWidth(lo, i), addWidth(hi, j)
		case opATOMIC_GROUP, opSUBPATTERN:
			w := it.sub.getwidth()
			lo, hi = addWidth(lo, w[0]), addWidth(hi, w[1])
		case opMIN_REPEAT, opMAX_REPEAT, opPOSSESSIVE_REPEAT:
			w := it.sub.getwidth()
			lo = addWidth(lo, mulWidth(w[0], uint64(it.lo)))
			if it.hi == maxRepeat && w[1] != 0 {
				hi = maxWidth
			} else {
				hi = addWidth(hi, mulWidth(w[1], uint64(it.hi)))
			}
		case opANY, opRANGE, opIN, opLITERAL, opNOT_LITERAL, opCATEGORY:
			lo, hi = addWidth(lo, 1), addWidth(hi, 1)
		case opGROUPREF:
			w := p.state.groupwidths[it.arg]
			lo, hi = addWidth(lo, w[0]), addWidth(hi, w[1])
		case opGROUPREF_EXISTS:
			w := it.sub.getwidth()
			i, j := w[0], w[1]
			if it.no != nil {
				n := it.no.getwidth()
				i = min(i, n[0])
				j = max(j, n[1])
			} else {
				i = 0
			}
			lo, hi = addWidth(lo, i), addWidth(hi, j)
		case opSUCCESS:
			goto done
		}
	}
done:
	p.width = &[2]uint64{lo, hi}
	return *p.width
}

// tokenizer splits a pattern into single code points and two-character escapes.
type tokenizer struct {
	pattern string
	runes   []rune
	index   int
	next    string // "" when the pattern is exhausted
	nextLen int
}

func newTokenizer(pattern string) *tokenizer {
	t := &tokenizer{pattern: pattern, runes: []rune(pattern)}
	t.advance()
	return t
}

func (t *tokenizer) advance() {
	index := t.index
	if index >= len(t.runes) {
		t.next, t.nextLen = "", 0
		return
	}
	c := t.runes[index]
	if c == '\\' {
		index++
		if index >= len(t.runes) {
			panic(newError("bad escape (end of pattern)", t.pattern, len(t.runes)-1))
		}
		t.next, t.nextLen = string([]rune{c, t.runes[index]}), 2
	} else {
		t.next, t.nextLen = string(c), 1
	}
	t.index = index + 1
}

func (t *tokenizer) match(s string) bool {
	if s == t.next {
		t.advance()
		return true
	}
	return false
}

func (t *tokenizer) get() string {
	this := t.next
	t.advance()
	return this
}

func (t *tokenizer) getwhile(n int, charset string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		c := t.next
		if c == "" || len([]rune(c)) != 1 || !strings.Contains(charset, c) {
			break
		}
		b.WriteString(c)
		t.advance()
	}
	return b.String()
}

func (t *tokenizer) getuntil(terminator, name string) string {
	var b strings.Builder
	n := 0
	for {
		c := t.next
		t.advance()
		if c == "" {
			if n == 0 {
				panic(t.error("missing "+name, 0))
			}
			panic(t.error(fmt.Sprintf("missing %s, unterminated name", terminator), n))
		}
		if c == terminator {
			if n == 0 {
				panic(t.error("missing "+name, 1))
			}
			break
		}
		b.WriteString(c)
		n += len([]rune(c))
	}
	return b.String()
}

func (t *tokenizer) tell() int { return t.index - t.nextLen }

func (t *tokenizer) seek(index int) {
	t.index = index
	t.advance()
}

func (t *tokenizer) error(msg string, offset int) *Error {
	return newError(msg, t.pattern, t.tell()-offset)
}

func (t *tokenizer) checkgroupname(name string, offset int) {
	if !pyuni.IsIdentifier(name) {
		panic(t.error("bad character in group name "+strRepr(name), runeLen(name)+offset))
	}
}

func isOneOf(tok, chars string) bool {
	return tok != "" && len([]rune(tok)) == 1 && strings.Contains(chars, tok)
}

func runeOf(tok string) rune {
	for _, r := range tok {
		return r
	}
	return 0
}

func runeAt(tok string, i int) string {
	rs := []rune(tok)
	if i < len(rs) {
		return string(rs[i])
	}
	return ""
}

func parseHex(s string) int {
	v, _ := strconv.ParseUint(s, 16, 64)
	return int(v)
}

func lit(c int) *item { return &item{op: opLITERAL, arg: c} }

// classEscape handles an escape inside a character set.
func classEscape(src *tokenizer, escape string) *item {
	if c, ok := escapes[escape]; ok {
		return lit(int(c))
	}
	if code, ok := categories[escape]; ok && code.op == opIN {
		return code.clone()
	}
	c := runeAt(escape, 1)
	bad := false
	switch {
	case c == "x":
		escape += src.getwhile(2, hexDigits)
		if runeLen(escape) != 4 {
			panic(src.error("incomplete escape "+escape, runeLen(escape)))
		}
		return lit(parseHex(escape[2:]))
	case c == "u":
		escape += src.getwhile(4, hexDigits)
		if runeLen(escape) != 6 {
			panic(src.error("incomplete escape "+escape, runeLen(escape)))
		}
		return lit(parseHex(escape[2:]))
	case c == "U":
		escape += src.getwhile(8, hexDigits)
		if runeLen(escape) != 10 {
			panic(src.error("incomplete escape "+escape, runeLen(escape)))
		}
		v := parseHex(escape[2:])
		if v > 0x10ffff {
			bad = true
			break
		}
		return lit(v)
	case c == "N":
		return namedEscape(src)
	case strings.Contains(octDigits, c) && c != "":
		escape += src.getwhile(2, octDigits)
		v, _ := strconv.ParseInt(escape[1:], 8, 64)
		if v > 0o377 {
			panic(src.error(fmt.Sprintf("octal escape value %s outside of range 0-0o377", escape), runeLen(escape)))
		}
		return lit(int(v))
	case strings.Contains(digits, c) && c != "":
		bad = true
	}
	if !bad && runeLen(escape) == 2 {
		if strings.Contains(asciiLetters, c) {
			panic(src.error("bad escape "+escape, runeLen(escape)))
		}
		return lit(int(runeOf(c)))
	}
	panic(src.error("bad escape "+escape, runeLen(escape)))
}

// namedEscape handles \N{name}: unicodedata.lookup of the name, which must be one code point.
func namedEscape(src *tokenizer) *item {
	if !src.match("{") {
		panic(src.error("missing {", 0))
	}
	charname := src.getuntil("}", "character name")
	if r, ok := pyuni.Lookup(charname); ok {
		return lit(int(r))
	}
	panic(src.error("undefined character name "+strRepr(charname), runeLen(charname)+len(`\N{}`)))
}

// parseEscape handles an escape outside a character set.
func parseEscape(src *tokenizer, escape string, state *parseState) *item {
	if code, ok := categories[escape]; ok {
		return code.clone()
	}
	if c, ok := escapes[escape]; ok {
		return lit(int(c))
	}
	c := runeAt(escape, 1)
	bad := false
	switch {
	case c == "x":
		escape += src.getwhile(2, hexDigits)
		if runeLen(escape) != 4 {
			panic(src.error("incomplete escape "+escape, runeLen(escape)))
		}
		return lit(parseHex(escape[2:]))
	case c == "u":
		escape += src.getwhile(4, hexDigits)
		if runeLen(escape) != 6 {
			panic(src.error("incomplete escape "+escape, runeLen(escape)))
		}
		return lit(parseHex(escape[2:]))
	case c == "U":
		escape += src.getwhile(8, hexDigits)
		if runeLen(escape) != 10 {
			panic(src.error("incomplete escape "+escape, runeLen(escape)))
		}
		v := parseHex(escape[2:])
		if v > 0x10ffff {
			bad = true
			break
		}
		return lit(v)
	case c == "N":
		return namedEscape(src)
	case c == "0":
		escape += src.getwhile(2, octDigits)
		v, _ := strconv.ParseInt(escape[1:], 8, 64)
		return lit(int(v))
	case c != "" && strings.Contains(digits, c):
		if isOneOf(src.next, digits) {
			escape += src.get()
			if strings.Contains(octDigits, runeAt(escape, 1)) && strings.Contains(octDigits, runeAt(escape, 2)) &&
				isOneOf(src.next, octDigits) {
				escape += src.get()
				v, _ := strconv.ParseInt(escape[1:], 8, 64)
				if v > 0o377 {
					panic(src.error(fmt.Sprintf("octal escape value %s outside of range 0-0o377", escape), runeLen(escape)))
				}
				return lit(int(v))
			}
		}
		group, _ := strconv.Atoi(escape[1:])
		if group < state.groups() {
			if !state.checkgroup(group) {
				panic(src.error("cannot refer to an open group", runeLen(escape)))
			}
			state.checklookbehindgroup(group, src)
			return &item{op: opGROUPREF, arg: group}
		}
		panic(src.error(fmt.Sprintf("invalid group reference %d", group), runeLen(escape)-1))
	}
	if !bad && runeLen(escape) == 2 {
		if strings.Contains(asciiLetters, c) {
			panic(src.error("bad escape "+escape, runeLen(escape)))
		}
		return lit(int(runeOf(c)))
	}
	panic(src.error("bad escape "+escape, runeLen(escape)))
}

// parseSub parses an alternation a|b|c.
func parseSub(src *tokenizer, state *parseState, verbose bool, nested int) *subPattern {
	var items []*subPattern
	for {
		items = append(items, parseSeq(src, state, verbose, nested+1, nested == 0 && len(items) == 0))
		if !src.match("|") {
			break
		}
		if nested == 0 {
			verbose = state.flags&VERBOSE != 0
		}
	}
	if len(items) == 1 {
		return items[0]
	}
	sub := newSub(state)
	for {
		var prefix *item
		common := true
		for _, it := range items {
			if len(it.data) == 0 {
				common = false
				break
			}
			if prefix == nil {
				prefix = it.data[0]
			} else if !it.data[0].equal(prefix) {
				common = false
				break
			}
		}
		if !common {
			break
		}
		for _, it := range items {
			it.data = it.data[1:]
		}
		sub.append(prefix)
	}
	var set []*item
	asSet := true
	for _, it := range items {
		if len(it.data) != 1 {
			asSet = false
			break
		}
		first := it.data[0]
		if first.op == opLITERAL {
			set = append(set, first)
		} else if first.op == opIN && first.set[0].op != opNEGATE {
			set = append(set, first.set...)
		} else {
			asSet = false
			break
		}
	}
	if asSet {
		sub.append(&item{op: opIN, set: uniq(set)})
		return sub
	}
	sub.append(&item{op: opBRANCH, branches: items})
	return sub
}

// parseSeq parses a simple pattern: a sequence without top-level alternation.
func parseSeq(src *tokenizer, state *parseState, verbose bool, nested int, first bool) *subPattern {
	sub := newSub(state)
	for {
		this := src.next
		if this == "" || this == "|" || this == ")" {
			break
		}
		src.get()
		if verbose {
			if isOneOf(this, whitespace) {
				continue
			}
			if this == "#" {
				for {
					this = src.get()
					if this == "" || this == "\n" {
						break
					}
				}
				continue
			}
		}
		switch {
		case runeOf(this) == '\\':
			sub.append(parseEscape(src, this, state))
		case !strings.Contains(specialChars, this):
			sub.append(lit(int(runeOf(this))))
		case this == "[":
			sub.append(parseSet(src))
		case strings.Contains(repeatChars, this):
			if !parseRepeat(src, sub, this) {
				continue
			}
		case this == ".":
			sub.append(&item{op: opANY})
		case this == "(":
			if it := parseGroup(src, state, sub, verbose, nested, first, &verbose); it != nil {
				sub.append(it)
			}
		case this == "^":
			sub.append(&item{op: opAT, arg: atBEGINNING})
		case this == "$":
			sub.append(&item{op: opAT, arg: atEND})
		}
	}
	// unpack non-capturing groups
	for i := len(sub.data) - 1; i >= 0; i-- {
		it := sub.data[i]
		if it.op == opSUBPATTERN && it.group == 0 && it.addFlags == 0 && it.delFlags == 0 {
			rest := append([]*item{}, sub.data[i+1:]...)
			sub.data = append(append(sub.data[:i], it.sub.data...), rest...)
		}
	}
	return sub
}

func parseSet(src *tokenizer) *item {
	here := src.tell() - 1
	var set []*item
	negate := src.match("^")
	for {
		this := src.get()
		if this == "" {
			panic(src.error("unterminated character set", src.tell()-here))
		}
		if this == "]" && len(set) > 0 {
			break
		}
		var code1 *item
		if runeOf(this) == '\\' {
			code1 = classEscape(src, this)
		} else {
			code1 = lit(int(runeOf(this)))
		}
		if src.match("-") {
			that := src.get()
			if that == "" {
				panic(src.error("unterminated character set", src.tell()-here))
			}
			if that == "]" {
				if code1.op == opIN {
					code1 = code1.set[0]
				}
				set = append(set, code1, lit('-'))
				break
			}
			var code2 *item
			if runeOf(that) == '\\' {
				code2 = classEscape(src, that)
			} else {
				code2 = lit(int(runeOf(that)))
			}
			msg := fmt.Sprintf("bad character range %s-%s", this, that)
			if code1.op != opLITERAL || code2.op != opLITERAL {
				panic(src.error(msg, runeLen(this)+1+runeLen(that)))
			}
			if code2.arg < code1.arg {
				panic(src.error(msg, runeLen(this)+1+runeLen(that)))
			}
			set = append(set, &item{op: opRANGE, lo: code1.arg, hi: code2.arg})
		} else {
			if code1.op == opIN {
				code1 = code1.set[0]
			}
			set = append(set, code1)
		}
	}
	set = uniq(set)
	if len(set) == 1 && set[0].op == opLITERAL {
		if negate {
			return &item{op: opNOT_LITERAL, arg: set[0].arg}
		}
		return set[0]
	}
	if negate {
		set = append([]*item{{op: opNEGATE}}, set...)
	}
	return &item{op: opIN, set: set}
}

// parseRepeat applies a quantifier to the last item. It returns false when "{" turned out to be
// a literal (already appended).
func parseRepeat(src *tokenizer, sub *subPattern, this string) bool {
	here := src.tell()
	var lo, hi int
	switch this {
	case "?":
		lo, hi = 0, 1
	case "*":
		lo, hi = 0, maxRepeat
	case "+":
		lo, hi = 1, maxRepeat
	case "{":
		if src.next == "}" {
			sub.append(lit('{'))
			return false
		}
		lo, hi = 0, maxRepeat
		var los, his string
		for isOneOf(src.next, digits) {
			los += src.get()
		}
		if src.match(",") {
			for isOneOf(src.next, digits) {
				his += src.get()
			}
		} else {
			his = los
		}
		if !src.match("}") {
			sub.append(lit('{'))
			src.seek(here)
			return false
		}
		if los != "" {
			lo = parseCount(los)
		}
		if his != "" {
			hi = parseCount(his)
			if hi < lo {
				panic(src.error("min repeat greater than max repeat", src.tell()-here))
			}
		}
	}
	var body *subPattern
	if len(sub.data) > 0 {
		body = &subPattern{state: sub.state, data: []*item{sub.data[len(sub.data)-1]}}
	}
	if body == nil || body.data[0].op == opAT {
		panic(src.error("nothing to repeat", src.tell()-here+runeLen(this)))
	}
	switch body.data[0].op {
	case opMIN_REPEAT, opMAX_REPEAT, opPOSSESSIVE_REPEAT:
		panic(src.error("multiple repeat", src.tell()-here+runeLen(this)))
	case opSUBPATTERN:
		g := body.data[0]
		if g.group == 0 && g.addFlags == 0 && g.delFlags == 0 {
			body = g.sub
		}
	}
	op := opMAX_REPEAT
	if src.match("?") {
		op = opMIN_REPEAT
	} else if src.match("+") {
		op = opPOSSESSIVE_REPEAT
	}
	sub.data[len(sub.data)-1] = &item{op: op, lo: lo, hi: hi, sub: body}
	return true
}

func parseCount(s string) int {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v >= maxRepeat {
		panic(&PyError{Class: "OverflowError", Msg: "the repetition number is too large"})
	}
	return int(v)
}

// parseGroup parses everything after "(". It returns the item to append, or nil when the group
// produced nothing (a comment, global flags) or appended its own item.
func parseGroup(src *tokenizer, state *parseState, sub *subPattern, verbose bool, nested int, first bool, outVerbose *bool) *item {
	start := src.tell() - 1
	capture := true
	atomic := false
	name, named := "", false
	addFlags, delFlags := 0, 0
	if src.match("?") {
		char := src.get()
		if char == "" {
			panic(src.error("unexpected end of pattern", 0))
		}
		switch {
		case char == "P":
			if src.match("<") {
				name = src.getuntil(">", "group name")
				named = true
				src.checkgroupname(name, 1)
			} else if src.match("=") {
				name = src.getuntil(")", "group name")
				src.checkgroupname(name, 1)
				gid, ok := state.groupdict[name]
				if !ok {
					panic(src.error("unknown group name "+strRepr(name), runeLen(name)+1))
				}
				if !state.checkgroup(gid) {
					panic(src.error("cannot refer to an open group", runeLen(name)+1))
				}
				state.checklookbehindgroup(gid, src)
				return &item{op: opGROUPREF, arg: gid}
			} else {
				char = src.get()
				if char == "" {
					panic(src.error("unexpected end of pattern", 0))
				}
				panic(src.error("unknown extension ?P"+char, runeLen(char)+2))
			}
		case char == ":":
			capture = false
		case char == "#":
			for {
				if src.next == "" {
					panic(src.error("missing ), unterminated comment", src.tell()-start))
				}
				if src.get() == ")" {
					break
				}
			}
			return nil
		case char == "=" || char == "!" || char == "<":
			dir := 1
			lookbehindgroups := -2
			if char == "<" {
				char = src.get()
				if char == "" {
					panic(src.error("unexpected end of pattern", 0))
				}
				if char != "=" && char != "!" {
					panic(src.error("unknown extension ?<"+char, runeLen(char)+2))
				}
				dir = -1
				lookbehindgroups = state.lookbehindgroups
				if lookbehindgroups < 0 {
					state.lookbehindgroups = state.groups()
				}
			}
			p := parseSub(src, state, verbose, nested+1)
			if dir < 0 && lookbehindgroups < 0 {
				state.lookbehindgroups = -1
			}
			if !src.match(")") {
				panic(src.error("missing ), unterminated subpattern", src.tell()-start))
			}
			if char == "=" {
				return &item{op: opASSERT, arg: dir, sub: p}
			}
			return &item{op: opASSERT_NOT, arg: dir, sub: p}
		case char == "(":
			condname := src.getuntil(")", "group name")
			var condgroup int
			if !(isASCIIDecimal(condname)) {
				src.checkgroupname(condname, 1)
				g, ok := state.groupdict[condname]
				if !ok {
					panic(src.error("unknown group name "+strRepr(condname), runeLen(condname)+1))
				}
				condgroup = g
			} else {
				v, err := strconv.ParseUint(condname, 10, 64)
				switch {
				case err == nil && v == 0:
					panic(src.error("bad group number", runeLen(condname)+1))
				case err != nil || v >= maxGroups:
					panic(src.error("invalid group reference "+strings.TrimLeft(condname, "0"), runeLen(condname)+1))
				}
				condgroup = int(v)
				if _, ok := state.grouprefpos[condgroup]; !ok {
					state.grouprefpos[condgroup] = src.tell() - runeLen(condname) - 1
					state.grouprefOrder = append(state.grouprefOrder, condgroup)
				}
			}
			state.checklookbehindgroup(condgroup, src)
			yes := parseSeq(src, state, verbose, nested+1, false)
			var no *subPattern
			if src.match("|") {
				no = parseSeq(src, state, verbose, nested+1, false)
				if src.next == "|" {
					panic(src.error("conditional backref with more than two branches", 0))
				}
			}
			if !src.match(")") {
				panic(src.error("missing ), unterminated subpattern", src.tell()-start))
			}
			return &item{op: opGROUPREF_EXISTS, arg: condgroup, sub: yes, no: no}
		case char == ">":
			capture = false
			atomic = true
		case char == "-" || isFlagLetter(char):
			add, del, global := parseFlags(src, state, char)
			if global {
				if !first || len(sub.data) > 0 {
					panic(src.error("global flags not at the start of the expression", src.tell()-start))
				}
				*outVerbose = state.flags&VERBOSE != 0
				return nil
			}
			addFlags, delFlags = add, del
			capture = false
		default:
			panic(src.error("unknown extension ?"+char, runeLen(char)+1))
		}
	}
	group := 0
	if capture {
		gid, msg := state.opengroup(name, named)
		if msg != "" {
			panic(src.error(msg, runeLen(name)+1))
		}
		group = gid
	}
	subVerbose := (verbose || addFlags&VERBOSE != 0) && delFlags&VERBOSE == 0
	p := parseSub(src, state, subVerbose, nested+1)
	if !src.match(")") {
		panic(src.error("missing ), unterminated subpattern", src.tell()-start))
	}
	if group != 0 {
		state.closegroup(group, p)
	}
	if atomic {
		return &item{op: opATOMIC_GROUP, sub: p}
	}
	return &item{op: opSUBPATTERN, group: group, addFlags: addFlags, delFlags: delFlags, sub: p}
}

func isASCIIDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isFlagLetter(s string) bool {
	if runeLen(s) != 1 {
		return false
	}
	_, ok := flagLetters[runeOf(s)]
	return ok
}

func isAlphaTok(s string) bool {
	return runeLen(s) == 1 && pyuni.IsAlpha(runeOf(s))
}

// parseFlags parses inline flags after "(?". global is true for "(?flags)".
func parseFlags(src *tokenizer, state *parseState, char string) (add, del int, global bool) {
	if char != "-" {
		for {
			flag := flagLetters[runeOf(char)]
			if char == "L" {
				panic(src.error("bad inline flags: cannot use 'L' flag with a str pattern", 0))
			}
			add |= flag
			if flag&typeFlags != 0 && add&typeFlags != flag {
				panic(src.error("bad inline flags: flags 'a', 'u' and 'L' are incompatible", 0))
			}
			char = src.get()
			if char == "" {
				panic(src.error("missing -, : or )", 0))
			}
			if char == ")" || char == "-" || char == ":" {
				break
			}
			if !isFlagLetter(char) {
				msg := "missing -, : or )"
				if isAlphaTok(char) {
					msg = "unknown flag"
				}
				panic(src.error(msg, runeLen(char)))
			}
		}
	}
	if char == ")" {
		state.flags |= add
		return 0, 0, true
	}
	if add&globalFlags != 0 {
		panic(src.error("bad inline flags: cannot turn on global flag", 1))
	}
	if char == "-" {
		char = src.get()
		if char == "" {
			panic(src.error("missing flag", 0))
		}
		if !isFlagLetter(char) {
			msg := "missing flag"
			if isAlphaTok(char) {
				msg = "unknown flag"
			}
			panic(src.error(msg, runeLen(char)))
		}
		for {
			flag := flagLetters[runeOf(char)]
			if flag&typeFlags != 0 {
				panic(src.error("bad inline flags: cannot turn off flags 'a', 'u' and 'L'", 0))
			}
			del |= flag
			char = src.get()
			if char == "" {
				panic(src.error("missing :", 0))
			}
			if char == ":" {
				break
			}
			if !isFlagLetter(char) {
				msg := "missing :"
				if isAlphaTok(char) {
					msg = "unknown flag"
				}
				panic(src.error(msg, runeLen(char)))
			}
		}
	}
	if del&globalFlags != 0 {
		panic(src.error("bad inline flags: cannot turn off global flag", 1))
	}
	if add&del != 0 {
		panic(src.error("bad inline flags: flag turned on and off", 1))
	}
	return add, del, false
}

func fixFlags(flags int) int {
	if flags&LOCALE != 0 {
		panic(&PyError{Class: "ValueError", Msg: "cannot use LOCALE flag with a str pattern"})
	}
	if flags&ASCII == 0 {
		flags |= UNICODE
	} else if flags&UNICODE != 0 {
		panic(&PyError{Class: "ValueError", Msg: "ASCII and UNICODE flags are incompatible"})
	}
	return flags
}

// parse turns a pattern into the intermediate form, panicking with *Error or *PyError.
func parse(pattern string, flags int) *subPattern {
	src := newTokenizer(pattern)
	state := newState(flags)
	p := parseSub(src, state, flags&VERBOSE != 0, 0)
	p.state.flags = fixFlags(p.state.flags)
	if src.next != "" {
		panic(src.error("unbalanced parenthesis", 0))
	}
	for _, g := range state.grouprefOrder {
		if g >= state.groups() {
			panic(newError(fmt.Sprintf("invalid group reference %d", g), pattern, state.grouprefpos[g]))
		}
	}
	return p
}
