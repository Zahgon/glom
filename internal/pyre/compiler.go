// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

package pyre

import (
	"bytes"
	"fmt"

	"github.com/mahmoud/glom/internal/pyuni"
)

// This file follows re/_compiler.py of CPython 3.12. The optional INFO block that CPython
// prepends is a search optimisation only and is not generated.

var repeatingCodes = map[int][3]int{
	opMIN_REPEAT:        {opREPEAT, opMIN_UNTIL, opMIN_REPEAT_ONE},
	opMAX_REPEAT:        {opREPEAT, opMAX_UNTIL, opREPEAT_ONE},
	opPOSSESSIVE_REPEAT: {opPOSSESSIVE_REPEAT, opSUCCESS, opPOSSESSIVE_REPEAT_ONE},
}

var opIgnore = map[int]int{opLITERAL: opLITERAL_IGNORE, opNOT_LITERAL: opNOT_LITERAL_IGNORE}
var opLocaleIgnore = map[int]int{opLITERAL: opLITERAL_LOC_IGNORE, opNOT_LITERAL: opNOT_LITERAL_LOC_IGNORE}
var opUnicodeIgnore = map[int]int{opLITERAL: opLITERAL_UNI_IGNORE, opNOT_LITERAL: opNOT_LITERAL_UNI_IGNORE}

func combineFlags(flags, add, del int) int {
	if add&typeFlags != 0 {
		flags &^= typeFlags
	}
	return (flags | add) &^ del
}

func asciiIsCased(c int) bool {
	return c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
}

func asciiToLower(c int) int {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

func uniIsCased(c int) bool { return pyuni.IsSreCased(rune(c)) }
func uniToLower(c int) int  { return int(pyuni.SimpleLower(rune(c))) }

func extraCases(c int) []rune { return pyuni.ExtraCases(rune(c)) }

type compiler struct {
	code []uint32
}

func (c *compiler) emit(v ...int) {
	for _, x := range v {
		c.code = append(c.code, uint32(x))
	}
}

func (c *compiler) mark() int {
	c.code = append(c.code, 0)
	return len(c.code) - 1
}

func (c *compiler) patch(skip int, extra int) { c.code[skip] = uint32(len(c.code) - skip + extra) }

func (c *compiler) compile(items []*item, flags int) {
	var iscased func(int) bool
	var tolower func(int) int
	fixes := false
	if flags&IGNORECASE != 0 && flags&LOCALE == 0 {
		if flags&UNICODE != 0 {
			iscased, tolower, fixes = uniIsCased, uniToLower, true
		} else {
			iscased, tolower = asciiIsCased, asciiToLower
		}
	}
	for _, it := range items {
		switch it.op {
		case opLITERAL, opNOT_LITERAL:
			switch {
			case flags&IGNORECASE == 0:
				c.emit(it.op, it.arg)
			case flags&LOCALE != 0:
				c.emit(opLocaleIgnore[it.op], it.arg)
			case !iscased(it.arg):
				c.emit(it.op, it.arg)
			default:
				lo := tolower(it.arg)
				switch {
				case !fixes:
					c.emit(opIgnore[it.op], lo)
				case extraCases(lo) == nil:
					c.emit(opUnicodeIgnore[it.op], lo)
				default:
					c.emit(opIN_UNI_IGNORE)
					skip := c.mark()
					if it.op == opNOT_LITERAL {
						c.emit(opNEGATE)
					}
					c.emit(opLITERAL, lo)
					for _, k := range extraCases(lo) {
						c.emit(opLITERAL, int(k))
					}
					c.emit(opFAILURE)
					c.patch(skip, 0)
				}
			}
		case opIN:
			charset, hascased := optimizeCharset(it.set, iscased, tolower, fixes)
			switch {
			case flags&IGNORECASE != 0 && flags&LOCALE != 0:
				c.emit(opIN_LOC_IGNORE)
			case !hascased:
				c.emit(opIN)
			case !fixes:
				c.emit(opIN_IGNORE)
			default:
				c.emit(opIN_UNI_IGNORE)
			}
			skip := c.mark()
			c.compileCharset(charset, flags)
			c.patch(skip, 0)
		case opANY:
			if flags&DOTALL != 0 {
				c.emit(opANY_ALL)
			} else {
				c.emit(opANY)
			}
		case opMIN_REPEAT, opMAX_REPEAT, opPOSSESSIVE_REPEAT:
			if flags&TEMPLATE != 0 {
				panic(&Error{Msg: "internal: unsupported template operator " + opNames[it.op]})
			}
			codes := repeatingCodes[it.op]
			if simple(it.sub) {
				c.emit(codes[2])
				skip := c.mark()
				c.emit(it.lo, it.hi)
				c.compile(it.sub.data, flags)
				c.emit(opSUCCESS)
				c.patch(skip, 0)
			} else {
				c.emit(codes[0])
				skip := c.mark()
				c.emit(it.lo, it.hi)
				c.compile(it.sub.data, flags)
				c.patch(skip, 0)
				c.emit(codes[1])
			}
		case opSUBPATTERN:
			if it.group != 0 {
				c.emit(opMARK, (it.group-1)*2)
			}
			c.compile(it.sub.data, combineFlags(flags, it.addFlags, it.delFlags))
			if it.group != 0 {
				c.emit(opMARK, (it.group-1)*2+1)
			}
		case opATOMIC_GROUP:
			c.emit(opATOMIC_GROUP)
			skip := c.mark()
			c.compile(it.sub.data, flags)
			c.emit(opSUCCESS)
			c.patch(skip, 0)
		case opSUCCESS, opFAILURE:
			c.emit(it.op)
		case opASSERT, opASSERT_NOT:
			c.emit(it.op)
			skip := c.mark()
			if it.arg >= 0 {
				c.emit(0)
			} else {
				w := it.sub.getwidth()
				if w[0] > maxCode {
					panic(&Error{Msg: "looks too much behind"})
				}
				if w[0] != w[1] {
					panic(&Error{Msg: "look-behind requires fixed-width pattern"})
				}
				c.emit(int(w[0]))
			}
			c.compile(it.sub.data, flags)
			c.emit(opSUCCESS)
			c.patch(skip, 0)
		case opAT:
			av := it.arg
			if flags&MULTILINE != 0 {
				switch av {
				case atBEGINNING:
					av = atBEGINNING_LINE
				case atEND:
					av = atEND_LINE
				}
			}
			if flags&LOCALE != 0 {
				switch av {
				case atBOUNDARY:
					av = atLOC_BOUNDARY
				case atNON_BOUNDARY:
					av = atLOC_NON_BOUNDARY
				}
			} else if flags&UNICODE != 0 {
				switch av {
				case atBOUNDARY:
					av = atUNI_BOUNDARY
				case atNON_BOUNDARY:
					av = atUNI_NON_BOUNDARY
				}
			}
			c.emit(opAT, av)
		case opBRANCH:
			c.emit(opBRANCH)
			var tails []int
			for _, b := range it.branches {
				skip := c.mark()
				c.compile(b.data, flags)
				c.emit(opJUMP)
				tails = append(tails, c.mark())
				c.patch(skip, 0)
			}
			c.emit(opFAILURE)
			for _, t := range tails {
				c.patch(t, 0)
			}
		case opCATEGORY:
			av := it.arg
			if flags&LOCALE != 0 {
				av = chLocale[av]
			} else if flags&UNICODE != 0 {
				av = chUnicode[av]
			}
			c.emit(opCATEGORY, av)
		case opGROUPREF:
			switch {
			case flags&IGNORECASE == 0:
				c.emit(opGROUPREF)
			case flags&LOCALE != 0:
				c.emit(opGROUPREF_LOC_IGNORE)
			case !fixes:
				c.emit(opGROUPREF_IGNORE)
			default:
				c.emit(opGROUPREF_UNI_IGNORE)
			}
			c.emit(it.arg - 1)
		case opGROUPREF_EXISTS:
			c.emit(opGROUPREF_EXISTS, it.arg-1)
			skipyes := c.mark()
			c.compile(it.sub.data, flags)
			if it.no != nil && len(it.no.data) > 0 {
				c.emit(opJUMP)
				skipno := c.mark()
				c.patch(skipyes, 1)
				c.compile(it.no.data, flags)
				c.patch(skipno, 0)
			} else {
				c.patch(skipyes, 1)
			}
		default:
			panic(&Error{Msg: fmt.Sprintf("internal: unsupported operand type %s", opNames[it.op])})
		}
	}
}

func (c *compiler) compileCharset(charset []*item, flags int) {
	for _, it := range charset {
		c.emit(it.op)
		switch it.op {
		case opNEGATE:
		case opLITERAL:
			c.emit(it.arg)
		case opRANGE, opRANGE_UNI_IGNORE:
			c.emit(it.lo, it.hi)
		case opCHARSET, opBIGCHARSET:
			c.code = append(c.code, it.words...)
		case opCATEGORY:
			switch {
			case flags&LOCALE != 0:
				c.emit(chLocale[it.arg])
			case flags&UNICODE != 0:
				c.emit(chUnicode[it.arg])
			default:
				c.emit(it.arg)
			}
		default:
			panic(&Error{Msg: fmt.Sprintf("internal: unsupported set operator %s", opNames[it.op])})
		}
	}
	c.emit(opFAILURE)
}

// optimizeCharset is _optimize_charset: it folds literals and ranges into the shortest of a
// literal/range list, a 256-bit map or a chunked 65536-bit map. Members that do not fit the map
// are kept as they are.
func optimizeCharset(charset []*item, iscased func(int) bool, fixup func(int) int, fixes bool) ([]*item, bool) {
	var out, tail []*item
	charmap := make([]byte, 256)
	hascased := false
	set := func(i int) bool {
		if i >= len(charmap) {
			return false
		}
		charmap[i] = 1
		return true
	}
	for _, orig := range charset {
		op, av := orig.op, orig.arg
		for {
			ok := true
			switch op {
			case opLITERAL:
				if fixup != nil {
					av = fixup(av)
					ok = set(av)
					if ok && fixes {
						for _, k := range extraCases(av) {
							if ok = set(int(k)); !ok {
								break
							}
						}
					}
					if ok && !hascased && iscased(av) {
						hascased = true
					}
				} else {
					ok = set(av)
				}
			case opRANGE:
				if fixup != nil {
					for i := orig.lo; i <= orig.hi && ok; i++ {
						v := fixup(i)
						if ok = set(v); ok && fixes {
							for _, k := range extraCases(v) {
								if ok = set(int(k)); !ok {
									break
								}
							}
						}
					}
					if ok && !hascased {
						for i := orig.lo; i <= orig.hi; i++ {
							if iscased(i) {
								hascased = true
								break
							}
						}
					}
				} else {
					for i := orig.lo; i <= orig.hi && ok; i++ {
						ok = set(i)
					}
				}
			case opNEGATE:
				out = append(out, orig)
			default:
				tail = append(tail, orig)
			}
			if ok {
				break
			}
			if len(charmap) == 256 {
				charmap = append(charmap, make([]byte, 0xff00)...)
				continue
			}
			t := &item{op: op, arg: av, lo: orig.lo, hi: orig.hi}
			if fixup != nil {
				if op == opRANGE {
					if fixes {
						t.op = opRANGE_UNI_IGNORE
					}
					hascased = true
				} else if !hascased && iscased(av) {
					hascased = true
				}
			}
			tail = append(tail, t)
			break
		}
	}

	var runs [][2]int
	useRuns := true
	q := 0
	for {
		p := indexFrom(charmap, 1, q)
		if p < 0 {
			break
		}
		if len(runs) >= 2 {
			useRuns = false
			break
		}
		q = indexFrom(charmap, 0, p)
		if q < 0 {
			runs = append(runs, [2]int{p, len(charmap)})
			break
		}
		runs = append(runs, [2]int{p, q})
	}
	if useRuns {
		for _, r := range runs {
			if r[1]-r[0] == 1 {
				out = append(out, lit(r[0]))
			} else {
				out = append(out, &item{op: opRANGE, lo: r[0], hi: r[1] - 1})
			}
		}
		out = append(out, tail...)
		if hascased || len(out) < len(charset) {
			return out, hascased
		}
		return charset, hascased
	}

	if len(charmap) == 256 {
		out = append(out, &item{op: opCHARSET, words: mkBitmap(charmap)})
		return append(out, tail...), hascased
	}

	comps := map[string]int{}
	mapping := make([]byte, 256)
	block := 0
	var data []byte
	for i := 0; i < 65536; i += 256 {
		chunk := string(charmap[i : i+256])
		if b, ok := comps[chunk]; ok {
			mapping[i/256] = byte(b)
		} else {
			mapping[i/256] = byte(block)
			comps[chunk] = block
			block++
			data = append(data, chunk...)
		}
	}
	words := []uint32{uint32(block)}
	for i := 0; i < 256; i += 4 {
		words = append(words, uint32(mapping[i])|uint32(mapping[i+1])<<8|uint32(mapping[i+2])<<16|uint32(mapping[i+3])<<24)
	}
	words = append(words, mkBitmap(data)...)
	out = append(out, &item{op: opBIGCHARSET, words: words})
	return append(out, tail...), hascased
}

func indexFrom(b []byte, c byte, from int) int {
	if from >= len(b) {
		return -1
	}
	i := bytes.IndexByte(b[from:], c)
	if i < 0 {
		return -1
	}
	return from + i
}

// mkBitmap packs a 0/1 byte map into 32-bit words, least significant bit first.
func mkBitmap(bits []byte) []uint32 {
	words := make([]uint32, len(bits)/32)
	for i, b := range bits {
		if b != 0 {
			words[i/32] |= 1 << (i % 32)
		}
	}
	return words
}

// simple reports whether a repeat body is a single one-character operation.
func simple(p *subPattern) bool {
	if len(p.data) != 1 {
		return false
	}
	it := p.data[0]
	if it.op == opSUBPATTERN {
		return it.group == 0 && simple(it.sub)
	}
	switch it.op {
	case opLITERAL, opNOT_LITERAL, opANY, opIN:
		return true
	}
	return false
}

// compileCode is _code: the pattern's code followed by SUCCESS.
func compileCode(p *subPattern, flags int) []uint32 {
	flags |= p.state.flags
	c := &compiler{}
	c.compile(p.data, flags)
	c.emit(opSUCCESS)
	return c.code
}
