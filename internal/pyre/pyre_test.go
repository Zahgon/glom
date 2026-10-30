// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

package pyre

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The expected values below were produced by CPython 3.12.15 in the pinned image.

func errString(err error) string {
	switch e := err.(type) {
	case nil:
		return "OK"
	case *Error:
		return "error: " + e.Message()
	case *PyError:
		return e.Class + ": " + e.Msg
	}
	return "go: " + err.Error()
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		pattern string
		flags   int
		want    string
	}{
		{"a(b", 0, "error: missing ), unterminated subpattern at position 1"},
		{"a**", 0, "error: multiple repeat at position 2"},
		{"*a", 0, "error: nothing to repeat at position 0"},
		{"[z-a]", 0, "error: bad character range z-a at position 1"},
		{`\N{BOGUS}`, 0, "error: undefined character name 'BOGUS' at position 0"},
		{"(?<=a+)b", 0, "error: look-behind requires fixed-width pattern"},
		{"(?P<1>a)", 0, "error: bad character in group name '1' at position 4"},
		{"a{4294967295}", 0, "OverflowError: the repetition number is too large"},
		{"a", LOCALE, "ValueError: cannot use LOCALE flag with a str pattern"},
		{"a", ASCII | UNICODE, "ValueError: ASCII and UNICODE flags are incompatible"},
		{"(?au:x)", 0, "error: bad inline flags: flags 'a', 'u' and 'L' are incompatible at position 4"},
		{"a\n(b", 0, "error: missing ), unterminated subpattern at position 2 (line 2, column 1)"},
		{`\x4`, 0, `error: incomplete escape \x4 at position 0`},
		{"(?(2)a|b)", 0, "error: invalid group reference 2 at position 3"},
		{"(?P=n)", 0, "error: unknown group name 'n' at position 4"},
		{`(a)\2`, 0, "error: invalid group reference 2 at position 4"},
		{"a(?i)b", 0, "error: global flags not at the start of the expression at position 1"},
		{"(?x) a # c\n (", 0, "error: missing ), unterminated subpattern at position 12 (line 2, column 2)"},
		{`\N{EM DASH}`, 0, "OK"},
		{"(?<!ab|c)", 0, "error: look-behind requires fixed-width pattern"},
		{`[a-\d]`, 0, `error: bad character range a-\d at position 1`},
		{"(?P<a>x)(?P<a>y)", 0, "error: redefinition of group name 'a' as group 2; was group 1 at position 12"},
		{"a{2,1}", 0, "error: min repeat greater than max repeat at position 2"},
		{"x{99999999999999999999}", 0, "OverflowError: the repetition number is too large"},
		{")", 0, "error: unbalanced parenthesis at position 0"},
		{"(?", 0, "error: unexpected end of pattern at position 2"},
		{"a{3", 0, "OK"},
		{"(?#x", 0, "error: missing ), unterminated comment at position 0"},
		{"a", TEMPLATE, "OK"},
		{"a*", TEMPLATE, "error: internal: unsupported template operator MAX_REPEAT"},
		{`\`, 0, "error: bad escape (end of pattern) at position 0"},
		{"(?i:a)(?L)", 0, "error: bad inline flags: cannot use 'L' flag with a str pattern at position 9"},
	}
	for _, c := range cases {
		_, err := Compile(c.pattern, c.flags)
		if got := errString(err); got != c.want {
			t.Errorf("Compile(%q, %d): %s, want %s", c.pattern, c.flags, got, c.want)
		}
	}
	_, err := Compile("a\n(b", 0)
	if e, ok := err.(*Error); !ok || e.Msg != "missing ), unterminated subpattern" || e.Pos != 2 {
		t.Errorf("error fields %#v", err)
	} else if l, c := e.LineCol(); l != 2 || c != 1 {
		t.Errorf("LineCol = %d, %d", l, c)
	}
}

func span(m *Match) string {
	if m == nil {
		return "None"
	}
	return fmt.Sprintf("(%d, %d)", m.Start(0), m.End(0))
}

func TestCaseAndClasses(t *testing.T) {
	cases := []struct{ pattern, text, want string }{
		{"(?i)ſ", "S", "(0, 1)"}, {"(?i)ſ", "s", "(0, 1)"}, {"(?i)k", "K", "(0, 1)"},
		{"(?i)[k-l]", "K", "(0, 1)"}, {"(?i)ß", "ẞ", "(0, 1)"}, {"(?i)İ", "i", "(0, 1)"},
		{"(?i)σ", "ς", "(0, 1)"}, {"(?i)[^σ]", "ς", "None"}, {`\w`, "౜", "None"},
		{`\d`, "٣", "(0, 1)"}, {`(?a)\d`, "٣", "None"}, {`(?i)\U00010400`, "\U00010428", "(0, 1)"},
		{`(?i)[\U00010400-\U00010401]`, "\U00010429", "(0, 1)"}, {`\s`, "\x1c", "(0, 1)"},
		{`(?a)\s`, "\x1c", "None"}, {`\b`, "౜", "None"}, {`(?i)(a)\1`, "aA", "(0, 2)"},
		{"(?i)ͅ", "ι", "(0, 1)"}, {"(?i)ι", "Ι", "(0, 1)"},
	}
	for _, c := range cases {
		m := MustCompile(c.pattern, 0).Search([]rune(c.text), 0)
		if got := span(m); got != c.want {
			t.Errorf("search(%q, %q) = %s, want %s", c.pattern, c.text, got, c.want)
		}
	}
}

func TestMatchObjects(t *testing.T) {
	if got := MustCompile("a*", 0).Match([]rune(strings.Repeat("a", 60)), 0).Repr(); got !=
		"<re.Match object; span=(0, 60), match='"+strings.Repeat("a", 49)+">" {
		t.Errorf("long repr %s", got)
	}
	if got := MustCompile(`".`, 0).Search([]rune("\"'\x7f"), 0).Repr(); got != `<re.Match object; span=(0, 2), match='"\''>` {
		t.Errorf("repr %s", got)
	}
	if got := span(MustCompile(`(?<=a)b`, 0).Search([]rune("ab"), 1)); got != "(1, 2)" {
		t.Errorf("lookbehind before pos: %s", got)
	}
	if got := span(MustCompile(``, 0).MatchRange([]rune("abc"), 3, 1)); got != "(3, 3)" {
		t.Errorf("pos > endpos: %s", got)
	}
	var fi []string
	for _, m := range MustCompile("x*", 0).FindIter([]rune("axbxx"), 0, 5) {
		fi = append(fi, span(m))
	}
	if got := strings.Join(fi, " "); got != "(0, 0) (1, 2) (2, 2) (3, 5) (5, 5)" {
		t.Errorf("finditer %s", got)
	}
	show := func(ps []*string) string {
		var out []string
		for _, p := range ps {
			if p == nil {
				out = append(out, "None")
			} else {
				out = append(out, "'"+*p+"'")
			}
		}
		return strings.Join(out, ", ")
	}
	if got := show(MustCompile("x*", 0).Split([]rune("axbxx"), 0)); got != "'', 'a', '', 'b', '', ''" {
		t.Errorf("split %s", got)
	}
	if got := show(MustCompile("(x)|y", 0).Split([]rune("axbyc"), 0)); got != "'a', 'x', 'b', None, 'c'" {
		t.Errorf("split groups %s", got)
	}
	p := MustCompile(`(?P<first>a)(b)?(?P<last>c)?`, 0)
	m := p.Match([]rune("ac"), 0)
	if got := show(m.Groups()); got != "'a', None, 'c'" {
		t.Errorf("groups %s", got)
	}
	gd := m.GroupDict()
	if len(gd) != 2 || gd[0].Name != "first" || *gd[0].Value != "a" || gd[1].Name != "last" || *gd[1].Value != "c" {
		t.Errorf("groupdict %v", gd)
	}
	if lg, _ := m.LastGroup(); m.LastIndex() != 3 || lg != "last" {
		t.Errorf("lastindex %d %s", m.LastIndex(), lg)
	}
	if !reflect.DeepEqual(p.GroupIndex(), []GroupName{{"first", 1}, {"last", 3}}) || p.Groups() != 3 {
		t.Errorf("groupindex %v", p.GroupIndex())
	}
	m = MustCompile(`((a)|b)+`, 0).Match([]rune("ab"), 0)
	if got := show(m.Groups()); m.LastIndex() != 1 || got != "'b', 'a'" {
		t.Errorf("lastindex %d groups %s", m.LastIndex(), got)
	}
	if m := MustCompile(`(?>a+)ab`, 0).Match([]rune("aaab"), 0); m != nil {
		t.Errorf("atomic group backtracked")
	}
	if got := span(MustCompile(`a++b`, 0).Match([]rune("aab"), 0)); got != "(0, 3)" {
		t.Errorf("possessive %s", got)
	}
	if s, e := MustCompile(`(a+)+?b`, 0).Match([]rune("aab"), 0).Span(1); s != 0 || e != 2 {
		t.Errorf("lazy group span %d %d", s, e)
	}
	cond := MustCompile(`(a)?(?(1)b|c)`, 0)
	if span(cond.Match([]rune("ab"), 0)) != "(0, 2)" || span(cond.Match([]rune("c"), 0)) != "(0, 1)" {
		t.Errorf("conditional")
	}
	named := MustCompile(`\N{LATIN SMALL LETTER A}\N{latin capital letter gha}\N{CJK UNIFIED IDEOGRAPH-4E00}`, 0)
	if got := named.Match([]rune("aƢ一"), 0).Repr(); got != "<re.Match object; span=(0, 3), match='aƢ一'>" {
		t.Errorf("named escapes %s", got)
	}
}

func TestSubAndTemplates(t *testing.T) {
	p := MustCompile(`(?P<first>[ab])`, 0)
	cases := []struct{ repl, want string }{
		{`<\g<0>>`, "x<a><b>y"}, {`\1\1`, "xaabby"}, {`\g<first>`, "xaby"},
		{`\g<zz>`, "IndexError: unknown group name 'zz'"}, {`\q`, `error: bad escape \q at position 0`},
		{`\g<3`, "error: missing >, unterminated name at position 3"},
		{`\`, "error: bad escape (end of pattern) at position 0"},
		{`\9`, "error: invalid group reference 9 at position 1"}, {`\x`, `error: bad escape \x at position 0`},
		{`\-\n\0`, "x\\-\n\x00\\-\n\x00y"}, {`\g<1a>`, "error: bad character in group name '1a' at position 3"},
		{`\400`, `error: octal escape value \400 outside of range 0-0o377 at position 0`}, {`\07`, "x\a\ay"},
	}
	for _, c := range cases {
		got, err := p.Sub(c.repl, []rune("xaby"), 0)
		if err != nil {
			got = errString(err)
		}
		if got != c.want {
			t.Errorf("sub(%q) = %q, want %q", c.repl, got, c.want)
		}
	}
	if s, n, _ := MustCompile("a|", 0).Subn("-", []rune("bab"), 0); s != "-b--b-" || n != 4 {
		t.Errorf("subn %q %d", s, n)
	}
	if s, _ := MustCompile("x*", 0).Sub("-", []rune("abxd"), 0); s != "-a-b--d-" {
		t.Errorf("sub empty matches %q", s)
	}
	if s, _ := MustCompile("a", 0).Sub("b", []rune("aaa"), -1); s != "aaa" {
		t.Errorf("negative count %q", s)
	}
	m := p.Search([]rune("xaby"), 0)
	if s, err := m.Expand(`[\g<first>|\1]`); err != nil || s != "[a|a]" {
		t.Errorf("expand %q %v", s, err)
	}
	if got := Escape("a.b*c\t-é #&~"); got != "a\\.b\\*c\\\t\\-é\\ \\#\\&\\~" {
		t.Errorf("escape %q", got)
	}
	flags := []int{MustCompile("(?i)a", 0).Flags(), MustCompile("a", ASCII).Flags(),
		MustCompile("(?x)a", IGNORECASE|MULTILINE).Flags()}
	if !reflect.DeepEqual(flags, []int{34, 256, 106}) {
		t.Errorf("flags %v", flags)
	}
}

// TestLongSubjects checks that the matcher's backtracking state lives on the heap: CPython
// handles a million-character subject with a capturing repeat, and so must the port.
func TestLongSubjects(t *testing.T) {
	text := []rune(strings.Repeat("ab", 500000))
	for _, pat := range []string{`(a|b)*`, `((a)|b)+`, `(?:a|b)*?$`, `(ab|a)*+`, `(?>a|b)*`, `(.)*`} {
		m := MustCompile(pat, 0).Match(text, 0)
		if m == nil || m.End(0) != len(text) {
			t.Errorf("%s: %v", pat, m)
		}
	}
	if m := MustCompile(`(?:a|b)*c`, 0).Match(text[:100000], 0); m != nil {
		t.Errorf("unexpected match")
	}
}
