// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

package pyre

import (
	"fmt"
	"strconv"
	"strings"
)

// template is a parsed replacement string: literal pieces with group references between them
// (re._parser.parse_template followed by _sre.template).
type template struct {
	literals []string // len(groups)+1 pieces
	groups   []int
}

func (t *template) expand(m *Match) string {
	var b strings.Builder
	b.WriteString(t.literals[0])
	for i, g := range t.groups {
		if s, ok := m.Group(g); ok {
			b.WriteString(s)
		}
		b.WriteString(t.literals[i+1])
	}
	return b.String()
}

// compileTemplate is re._compile_template(pattern, repl).
func (p *Pattern) compileTemplate(repl string) (t *template, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *Error:
				t, err = nil, e
			case *PyError:
				t, err = nil, e
			default:
				panic(r)
			}
		}
	}()
	return p.parseTemplate(repl), nil
}

func (p *Pattern) parseTemplate(source string) *template {
	s := newTokenizer(source)
	t := &template{}
	var literal strings.Builder
	addgroup := func(index, pos int) {
		if index > p.groups {
			panic(s.error(fmt.Sprintf("invalid group reference %d", index), pos))
		}
		t.literals = append(t.literals, literal.String())
		literal.Reset()
		t.groups = append(t.groups, index)
	}
	for {
		this := s.get()
		if this == "" {
			break
		}
		if runeOf(this) != '\\' {
			literal.WriteString(this)
			continue
		}
		c := runeAt(this, 1)
		switch {
		case c == "g":
			if !s.match("<") {
				panic(s.error("missing <", 0))
			}
			name := s.getuntil(">", "group name")
			var index int
			if !isASCIIDecimal(name) {
				s.checkgroupname(name, 1)
				g, ok := p.groupMap[name]
				if !ok {
					panic(&PyError{Class: "IndexError", Msg: "unknown group name " + strRepr(name)})
				}
				index = g
			} else {
				v, err := strconv.ParseUint(name, 10, 64)
				if err != nil || v >= maxGroups {
					panic(s.error("invalid group reference "+trimZeros(name), runeLen(name)+1))
				}
				index = int(v)
			}
			addgroup(index, runeLen(name)+1)
		case c == "0":
			if isOneOf(s.next, octDigits) {
				this += s.get()
				if isOneOf(s.next, octDigits) {
					this += s.get()
				}
			}
			v, _ := strconv.ParseInt(this[1:], 8, 64)
			literal.WriteRune(rune(v & 0xff))
		case c != "" && strings.Contains(digits, c):
			isoctal := false
			if isOneOf(s.next, digits) {
				this += s.get()
				if strings.Contains(octDigits, c) && strings.Contains(octDigits, runeAt(this, 2)) &&
					isOneOf(s.next, octDigits) {
					this += s.get()
					isoctal = true
					v, _ := strconv.ParseInt(this[1:], 8, 64)
					if v > 0o377 {
						panic(s.error(fmt.Sprintf("octal escape value %s outside of range 0-0o377", this), runeLen(this)))
					}
					literal.WriteRune(rune(v))
				}
			}
			if !isoctal {
				v, _ := strconv.Atoi(this[1:])
				addgroup(v, runeLen(this)-1)
			}
		default:
			if r, ok := escapes[this]; ok {
				literal.WriteRune(r)
			} else {
				if strings.Contains(asciiLetters, c) {
					panic(s.error("bad escape "+this, runeLen(this)))
				}
				literal.WriteString(this)
			}
		}
	}
	t.literals = append(t.literals, literal.String())
	return t
}

// trimZeros formats a decimal digit string as Python's %d of int(s) does.
func trimZeros(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}
