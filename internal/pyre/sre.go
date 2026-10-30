// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

package pyre

import "github.com/mahmoud/glom/internal/pyuni"

// This file is the backtracking matcher of CPython's _sre module (sre_lib.h): the same opcode
// semantics, the same order of alternatives, the same capture bookkeeping on backtracking and
// the same zero-width-repeat protection. Every choice point recurses into the rest of the
// pattern, as the C version does with its context stack.

type repeatCtx struct {
	count   int
	pc      int // index of the REPEAT skip word
	lastPtr int
	prev    *repeatCtx
}

type machine struct {
	code        []uint32
	text        []rune
	start       int // where the current match attempt began (state->start)
	end         int // endpos (state->end)
	ptr         int // state->ptr
	marks       []int
	lastmark    int
	lastindex   int
	repeat      *repeatCtx
	mustAdvance bool
	matchAll    bool

	frames []*frame // the context stack (sre's match contexts), heap allocated
	pool   []*frame
	block  []frame // frames not handed out yet
	data   []int   // saved marks (sre's data stack)
	retval bool
	cpc    int  // pending call: pattern position
	ctop   bool // pending call: toplevel
}

func newMachine(code []uint32, text []rune, groups int, end int) *machine {
	marks := make([]int, 2*groups)
	for i := range marks {
		marks[i] = -1
	}
	return &machine{code: code, text: text, end: end, marks: marks, lastmark: -1, lastindex: -1}
}

func lowerASCII(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

func isASCIIWord(c rune) bool {
	return c <= 'z' && (c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_')
}

func isUniWord(c rune) bool { return pyuni.IsAlnum(c) || c == '_' }

func category(code uint32, c rune) bool {
	switch code {
	case catDIGIT:
		return c >= '0' && c <= '9'
	case catNOT_DIGIT:
		return !(c >= '0' && c <= '9')
	case catSPACE:
		return c == ' ' || c >= '\t' && c <= '\r'
	case catNOT_SPACE:
		return !(c == ' ' || c >= '\t' && c <= '\r')
	case catWORD, catLOC_WORD:
		return isASCIIWord(c)
	case catNOT_WORD, catLOC_NOT_WORD:
		return !isASCIIWord(c)
	case catLINEBREAK:
		return c == '\n'
	case catNOT_LINEBREAK:
		return c != '\n'
	case catUNI_DIGIT:
		return pyuni.IsDecimal(c)
	case catUNI_NOT_DIGIT:
		return !pyuni.IsDecimal(c)
	case catUNI_SPACE:
		return pyuni.IsSpace(c)
	case catUNI_NOT_SPACE:
		return !pyuni.IsSpace(c)
	case catUNI_WORD:
		return isUniWord(c)
	case catUNI_NOT_WORD:
		return !isUniWord(c)
	case catUNI_LINEBREAK:
		return pyuni.IsLineBreak(c)
	case catUNI_NOT_LINEBREAK:
		return !pyuni.IsLineBreak(c)
	}
	return false
}

// charset tests membership of c in the set program starting at pc.
func (m *machine) charset(pc int, c rune) bool {
	code := m.code
	ok := true
	u := uint32(c)
	for {
		op := code[pc]
		pc++
		switch op {
		case opFAILURE:
			return !ok
		case opLITERAL:
			if u == code[pc] {
				return ok
			}
			pc++
		case opCATEGORY:
			if category(code[pc], c) {
				return ok
			}
			pc++
		case opCHARSET:
			if u < 256 && code[pc+int(u/32)]&(1<<(u&31)) != 0 {
				return ok
			}
			pc += 8
		case opRANGE:
			if code[pc] <= u && u <= code[pc+1] {
				return ok
			}
			pc += 2
		case opRANGE_UNI_IGNORE:
			if code[pc] <= u && u <= code[pc+1] {
				return ok
			}
			up := uint32(pyuni.SimpleUpper(c))
			if code[pc] <= up && up <= code[pc+1] {
				return ok
			}
			pc += 2
		case opNEGATE:
			ok = !ok
		case opBIGCHARSET:
			count := int(code[pc])
			pc++
			block := -1
			if u < 0x10000 {
				k := int(u >> 8)
				block = int(code[pc+k/4]>>(8*(k%4))) & 0xff
			}
			pc += 64
			if block >= 0 {
				bit := block*256 + int(u&255)
				if code[pc+bit/32]&(1<<(bit%32)) != 0 {
					return ok
				}
			}
			pc += count * 8
		default:
			return false
		}
	}
}

func (m *machine) at(ptr int, code uint32) bool {
	text := m.text
	switch code {
	case atBEGINNING, atBEGINNING_STRING:
		return ptr == 0
	case atBEGINNING_LINE:
		return ptr == 0 || text[ptr-1] == '\n'
	case atEND:
		return (m.end-ptr == 1 && text[ptr] == '\n') || ptr == m.end
	case atEND_LINE:
		return ptr == m.end || (ptr < len(text) && text[ptr] == '\n')
	case atEND_STRING:
		return ptr == m.end
	case atBOUNDARY, atNON_BOUNDARY, atLOC_BOUNDARY, atLOC_NON_BOUNDARY, atUNI_BOUNDARY, atUNI_NON_BOUNDARY:
		if m.end == 0 {
			return false
		}
		word := isASCIIWord
		if code == atUNI_BOUNDARY || code == atUNI_NON_BOUNDARY {
			word = isUniWord
		}
		that := ptr > 0 && word(text[ptr-1])
		this := ptr < m.end && word(text[ptr])
		if code == atBOUNDARY || code == atLOC_BOUNDARY || code == atUNI_BOUNDARY {
			return this != that
		}
		return this == that
	}
	return false
}

// count returns how many times the single-character item at pc matches from m.ptr, at most
// maxcount times.
func (m *machine) count(pc int, maxcount uint32) int {
	code, text := m.code, m.text
	ptr := m.ptr
	end := m.end
	if maxcount != maxRepeat && int(maxcount) < end-ptr {
		end = ptr + int(maxcount)
	}
	switch code[pc] {
	case opIN:
		for ptr < end && m.charset(pc+2, text[ptr]) {
			ptr++
		}
	case opANY:
		for ptr < end && text[ptr] != '\n' {
			ptr++
		}
	case opANY_ALL:
		ptr = end
	case opLITERAL:
		for ptr < end && uint32(text[ptr]) == code[pc+1] {
			ptr++
		}
	case opLITERAL_IGNORE:
		for ptr < end && uint32(lowerASCII(text[ptr])) == code[pc+1] {
			ptr++
		}
	case opLITERAL_UNI_IGNORE:
		for ptr < end && uint32(pyuni.SimpleLower(text[ptr])) == code[pc+1] {
			ptr++
		}
	case opNOT_LITERAL:
		for ptr < end && uint32(text[ptr]) != code[pc+1] {
			ptr++
		}
	case opNOT_LITERAL_IGNORE:
		for ptr < end && uint32(lowerASCII(text[ptr])) != code[pc+1] {
			ptr++
		}
	case opNOT_LITERAL_UNI_IGNORE:
		for ptr < end && uint32(pyuni.SimpleLower(text[ptr])) != code[pc+1] {
			ptr++
		}
	default:
		start := m.ptr
		for m.ptr < end {
			if !m.run(pc, false) {
				break
			}
		}
		return m.ptr - start
	}
	return ptr - m.ptr
}

// The matcher below is SRE(match) of Modules/_sre/sre_lib.h: every DO_JUMP pushes a context
// on a heap-allocated stack instead of recursing, so arbitrarily long subjects only cost
// memory, as in CPython. A context remembers where to continue (jump) when its child returns.

const (
	jNone = iota
	jBranch
	jRepeatOne1
	jRepeatOne2
	jMinRepeatOne
	jRepeat
	jMaxUntil1
	jMaxUntil2
	jMaxUntil3
	jMinUntil1
	jMinUntil2
	jMinUntil3
	jPossRepeat1
	jPossRepeat2
	jAtomic
	jAssert
	jAssertNot
)

type action int

const (
	aCall   action = iota // push a child context (m.cpc, m.ptr, m.ctop)
	aReturn               // return m.retval to the parent context
	aExec                 // continue executing the current context at f.pc, f.ptr
)

// frame is one match context (SRE(match_context)).
type frame struct {
	pc, ptr             int
	toplevel            bool
	jump                int
	lastmark, lastindex int
	count               int
	markBase, markN     int // saved marks in m.data; markBase < 0 when nothing was pushed
	rep                 *repeatCtx
	savedLast           int
	chr                 uint32
	ctxPtr              int
	next                int
}

func (m *machine) push(pc, ptr int, toplevel bool) *frame {
	var f *frame
	if n := len(m.pool); n > 0 {
		f = m.pool[n-1]
		m.pool = m.pool[:n-1]
		*f = frame{}
	} else {
		if len(m.block) == 0 {
			m.block = make([]frame, min(max(4, len(m.frames)), 256))
		}
		f = &m.block[0]
		m.block = m.block[1:]
	}
	f.pc, f.ptr, f.toplevel, f.markBase = pc, ptr, toplevel, -1
	m.frames = append(m.frames, f)
	return f
}

func (m *machine) pop() {
	n := len(m.frames) - 1
	f := m.frames[n]
	if f.markBase >= 0 && len(m.data) > f.markBase {
		m.data = m.data[:f.markBase]
	}
	m.frames = m.frames[:n]
	m.pool = append(m.pool, f)
}

func (m *machine) call(f *frame, pc int, toplevel bool, jump int) action {
	f.jump = jump
	m.cpc, m.ctop = pc, toplevel
	return aCall
}

func (m *machine) ret(v bool) action {
	m.retval = v
	return aReturn
}

// lastmarkSave is LASTMARK_SAVE.
func (m *machine) lastmarkSave(f *frame) { f.lastmark, f.lastindex = m.lastmark, m.lastindex }

// lastmarkRestore is LASTMARK_RESTORE.
func (m *machine) lastmarkRestore(f *frame) { m.lastmark, m.lastindex = f.lastmark, f.lastindex }

// markPush is MARK_PUSH(ctx->lastmark).
func (m *machine) markPush(f *frame) {
	f.markBase = len(m.data)
	f.markN = 0
	if f.lastmark >= 0 {
		f.markN = f.lastmark + 1
		m.data = append(m.data, m.marks[:f.markN]...)
	}
}

// markPopKeep is MARK_POP_KEEP: restore the saved marks and keep them saved.
func (m *machine) markPopKeep(f *frame) {
	if f.markBase >= 0 {
		copy(m.marks[:f.markN], m.data[f.markBase:f.markBase+f.markN])
	}
}

// markPop is MARK_POP: restore the saved marks and drop them.
func (m *machine) markPop(f *frame) {
	m.markPopKeep(f)
	m.markDiscard(f)
}

// markDiscard is MARK_POP_DISCARD.
func (m *machine) markDiscard(f *frame) {
	if f.markBase >= 0 {
		m.data = m.data[:f.markBase]
		f.markBase = -1
	}
}

// run matches the pattern from pc at m.ptr through to the next SUCCESS (SRE(match)). On
// success m.ptr is where the match ended.
func (m *machine) run(pc int, toplevel bool) bool {
	base := len(m.frames)
	f := m.push(pc, m.ptr, toplevel)
	act := m.exec(f)
	for {
		switch act {
		case aCall:
			f = m.push(m.cpc, m.ptr, m.ctop)
			act = m.exec(f)
		case aReturn:
			m.pop()
			if len(m.frames) == base {
				return m.retval
			}
			f = m.frames[len(m.frames)-1]
			act = m.resume(f, m.retval)
		case aExec:
			act = m.exec(f)
		}
	}
}

// resume continues a context after its child returned v.
func (m *machine) resume(f *frame, v bool) action {
	code := m.code
	switch f.jump {
	case jBranch:
		if v {
			if m.repeat != nil {
				m.markDiscard(f)
			}
			return m.ret(true)
		}
		if m.repeat != nil {
			m.markPopKeep(f)
		}
		m.lastmarkRestore(f)
		f.pc += int(code[f.pc])
		return m.branchNext(f)

	case jRepeatOne1, jRepeatOne2:
		if v {
			if m.repeat != nil {
				m.markDiscard(f)
			}
			return m.ret(true)
		}
		if m.repeat != nil {
			m.markPopKeep(f)
		}
		m.lastmarkRestore(f)
		f.ptr--
		f.count--
		if f.jump == jRepeatOne1 {
			return m.repeatOneLiteral(f)
		}
		return m.repeatOneGeneral(f)

	case jMinRepeatOne:
		if v {
			if m.repeat != nil {
				m.markDiscard(f)
			}
			return m.ret(true)
		}
		if m.repeat != nil {
			m.markPopKeep(f)
		}
		m.lastmarkRestore(f)
		m.ptr = f.ptr
		if m.count(f.pc+3, 1) == 0 {
			if m.repeat != nil {
				m.markDiscard(f)
			}
			return m.ret(false)
		}
		f.ptr++
		f.count++
		return m.minRepeatOneNext(f)

	case jRepeat:
		m.repeat = f.rep.prev
		return m.ret(v)

	case jMaxUntil1, jMinUntil1:
		if v {
			return m.ret(true)
		}
		f.rep.count = f.count - 1
		m.ptr = f.ptr
		return m.ret(false)

	case jMaxUntil2:
		f.rep.lastPtr = f.savedLast
		if v {
			m.markDiscard(f)
			return m.ret(true)
		}
		m.markPop(f)
		m.lastmarkRestore(f)
		f.rep.count = f.count - 1
		m.ptr = f.ptr
		m.repeat = f.rep.prev
		return m.call(f, f.pc, f.toplevel, jMaxUntil3)

	case jMaxUntil3:
		m.repeat = f.rep
		if v {
			return m.ret(true)
		}
		m.ptr = f.ptr
		return m.ret(false)

	case jMinUntil2:
		repeatOfTail := m.repeat
		m.repeat = f.rep
		if v {
			if repeatOfTail != nil {
				m.markDiscard(f)
			}
			return m.ret(true)
		}
		if repeatOfTail != nil {
			m.markPop(f)
		}
		m.lastmarkRestore(f)
		m.ptr = f.ptr
		mx := code[f.rep.pc+2]
		if (f.count >= int(mx) && mx != maxRepeat) || m.ptr == f.rep.lastPtr {
			return m.ret(false)
		}
		f.rep.count = f.count
		f.savedLast = f.rep.lastPtr
		f.rep.lastPtr = m.ptr
		return m.call(f, f.rep.pc+3, f.toplevel, jMinUntil3)

	case jMinUntil3:
		f.rep.lastPtr = f.savedLast
		if v {
			return m.ret(true)
		}
		f.rep.count = f.count - 1
		m.ptr = f.ptr
		return m.ret(false)

	case jPossRepeat1:
		if v {
			f.count++
			return m.possMin(f)
		}
		m.ptr = f.ptr
		m.repeat = f.rep.prev
		return m.ret(false)

	case jPossRepeat2:
		if v {
			m.markDiscard(f)
			f.count++
			return m.possLoop(f)
		}
		m.markPop(f)
		m.lastmarkRestore(f)
		m.ptr = f.ctxPtr
		return m.possDone(f)

	case jAtomic:
		if !v {
			m.ptr = f.ptr
			return m.ret(false)
		}
		f.pc += int(code[f.pc])
		f.ptr = m.ptr
		return aExec

	case jAssert:
		if !v {
			return m.ret(false)
		}
		f.pc += int(code[f.pc])
		return aExec

	case jAssertNot:
		if v {
			if m.repeat != nil {
				m.markDiscard(f)
			}
			return m.ret(false)
		}
		if m.repeat != nil {
			m.markPop(f)
		}
		m.lastmarkRestore(f)
		f.pc += int(code[f.pc])
		return aExec
	}
	panic("pyre: bad jump")
}

// branchNext tries the alternatives of a BRANCH from the one at f.pc.
func (m *machine) branchNext(f *frame) action {
	code, text, end := m.code, m.text, m.end
	for ; code[f.pc] != 0; f.pc += int(code[f.pc]) {
		if code[f.pc+1] == opLITERAL && (f.ptr >= end || uint32(text[f.ptr]) != code[f.pc+2]) {
			continue
		}
		if code[f.pc+1] == opIN && (f.ptr >= end || !m.charset(f.pc+3, text[f.ptr])) {
			continue
		}
		m.ptr = f.ptr
		return m.call(f, f.pc+1, f.toplevel, jBranch)
	}
	if m.repeat != nil {
		m.markDiscard(f)
	}
	return m.ret(false)
}

// repeatOneLiteral is REPEAT_ONE's backtracking loop when the tail starts with a literal.
func (m *machine) repeatOneLiteral(f *frame) action {
	mn := int(m.code[f.pc+1])
	for f.count >= mn && (f.ptr >= m.end || uint32(m.text[f.ptr]) != f.chr) {
		f.ptr--
		f.count--
	}
	if f.count < mn {
		if m.repeat != nil {
			m.markDiscard(f)
		}
		return m.ret(false)
	}
	m.ptr = f.ptr
	return m.call(f, f.next, f.toplevel, jRepeatOne1)
}

// repeatOneGeneral is REPEAT_ONE's general backtracking loop.
func (m *machine) repeatOneGeneral(f *frame) action {
	if f.count >= int(m.code[f.pc+1]) {
		m.ptr = f.ptr
		return m.call(f, f.next, f.toplevel, jRepeatOne2)
	}
	if m.repeat != nil {
		m.markDiscard(f)
	}
	return m.ret(false)
}

func (m *machine) minRepeatOneNext(f *frame) action {
	mx := m.code[f.pc+2]
	if mx == maxRepeat || f.count <= int(mx) {
		m.ptr = f.ptr
		return m.call(f, f.next, f.toplevel, jMinRepeatOne)
	}
	if m.repeat != nil {
		m.markDiscard(f)
	}
	return m.ret(false)
}

func (m *machine) possMin(f *frame) action {
	if f.count < int(m.code[f.pc+1]) {
		return m.call(f, f.pc+3, false, jPossRepeat1)
	}
	f.ctxPtr = -1
	return m.possLoop(f)
}

func (m *machine) possLoop(f *frame) action {
	mx := m.code[f.pc+2]
	if (f.count < int(mx) || mx == maxRepeat) && m.ptr != f.ctxPtr {
		m.lastmarkSave(f)
		m.markPush(f)
		f.ctxPtr = m.ptr
		return m.call(f, f.pc+3, false, jPossRepeat2)
	}
	return m.possDone(f)
}

func (m *machine) possDone(f *frame) action {
	m.repeat = f.rep.prev
	f.pc += int(m.code[f.pc]) + 1
	f.ptr = m.ptr
	return aExec
}

// exec runs the context's pattern from f.pc at f.ptr until it returns or calls a child.
func (m *machine) exec(f *frame) action {
	code, text := m.code, m.text
	end := m.end
	pc, ptr := f.pc, f.ptr
	for {
		op := code[pc]
		pc++
		switch op {
		case opFAILURE:
			return m.ret(false)

		case opSUCCESS:
			if f.toplevel && ((m.matchAll && ptr != m.end) || (m.mustAdvance && ptr == m.start)) {
				return m.ret(false)
			}
			m.ptr = ptr
			return m.ret(true)

		case opMARK:
			i := int(code[pc])
			if i&1 != 0 {
				m.lastindex = i/2 + 1
			}
			if i > m.lastmark {
				for j := m.lastmark + 1; j < i; j++ {
					m.marks[j] = -1
				}
				m.lastmark = i
			}
			m.marks[i] = ptr
			pc++

		case opLITERAL:
			if ptr >= end || uint32(text[ptr]) != code[pc] {
				return m.ret(false)
			}
			pc++
			ptr++

		case opNOT_LITERAL:
			if ptr >= end || uint32(text[ptr]) == code[pc] {
				return m.ret(false)
			}
			pc++
			ptr++

		case opLITERAL_IGNORE, opLITERAL_LOC_IGNORE:
			if ptr >= end || uint32(lowerASCII(text[ptr])) != code[pc] {
				return m.ret(false)
			}
			pc++
			ptr++

		case opLITERAL_UNI_IGNORE:
			if ptr >= end || uint32(pyuni.SimpleLower(text[ptr])) != code[pc] {
				return m.ret(false)
			}
			pc++
			ptr++

		case opNOT_LITERAL_IGNORE, opNOT_LITERAL_LOC_IGNORE:
			if ptr >= end || uint32(lowerASCII(text[ptr])) == code[pc] {
				return m.ret(false)
			}
			pc++
			ptr++

		case opNOT_LITERAL_UNI_IGNORE:
			if ptr >= end || uint32(pyuni.SimpleLower(text[ptr])) == code[pc] {
				return m.ret(false)
			}
			pc++
			ptr++

		case opAT:
			if !m.at(ptr, code[pc]) {
				return m.ret(false)
			}
			pc++

		case opCATEGORY:
			if ptr >= end || !category(code[pc], text[ptr]) {
				return m.ret(false)
			}
			pc++
			ptr++

		case opANY:
			if ptr >= end || text[ptr] == '\n' {
				return m.ret(false)
			}
			ptr++

		case opANY_ALL:
			if ptr >= end {
				return m.ret(false)
			}
			ptr++

		case opIN:
			if ptr >= end || !m.charset(pc+1, text[ptr]) {
				return m.ret(false)
			}
			pc += int(code[pc])
			ptr++

		case opIN_IGNORE, opIN_LOC_IGNORE:
			if ptr >= end || !m.charset(pc+1, lowerASCII(text[ptr])) {
				return m.ret(false)
			}
			pc += int(code[pc])
			ptr++

		case opIN_UNI_IGNORE:
			if ptr >= end || !m.charset(pc+1, pyuni.SimpleLower(text[ptr])) {
				return m.ret(false)
			}
			pc += int(code[pc])
			ptr++

		case opJUMP, opINFO:
			pc += int(code[pc])

		case opBRANCH:
			m.lastmarkSave(f)
			if m.repeat != nil {
				m.markPush(f)
			}
			f.pc, f.ptr = pc, ptr
			return m.branchNext(f)

		case opREPEAT_ONE:
			mn, mx := int(code[pc+1]), code[pc+2]
			if mn > end-ptr {
				return m.ret(false)
			}
			m.ptr = ptr
			count := m.count(pc+3, mx)
			ptr += count
			if count < mn {
				return m.ret(false)
			}
			next := pc + int(code[pc])
			if code[next] == opSUCCESS && ptr == m.end && !(f.toplevel && m.mustAdvance && ptr == m.start) {
				m.ptr = ptr
				return m.ret(true)
			}
			m.lastmarkSave(f)
			if m.repeat != nil {
				m.markPush(f)
			}
			f.pc, f.ptr, f.count, f.next = pc, ptr, count, next
			if code[next] == opLITERAL {
				f.chr = code[next+1]
				return m.repeatOneLiteral(f)
			}
			return m.repeatOneGeneral(f)

		case opMIN_REPEAT_ONE:
			mn := int(code[pc+1])
			if mn > end-ptr {
				return m.ret(false)
			}
			m.ptr = ptr
			count := 0
			if mn != 0 {
				n := m.count(pc+3, uint32(mn))
				if n < mn {
					return m.ret(false)
				}
				count = n
				ptr += count
			}
			next := pc + int(code[pc])
			if code[next] == opSUCCESS &&
				!(f.toplevel && ((m.matchAll && ptr != m.end) || (m.mustAdvance && ptr == m.start))) {
				m.ptr = ptr
				return m.ret(true)
			}
			m.lastmarkSave(f)
			if m.repeat != nil {
				m.markPush(f)
			}
			f.pc, f.ptr, f.count, f.next = pc, ptr, count, next
			return m.minRepeatOneNext(f)

		case opPOSSESSIVE_REPEAT_ONE:
			mn, mx := int(code[pc+1]), code[pc+2]
			if ptr+mn > end {
				return m.ret(false)
			}
			m.ptr = ptr
			count := m.count(pc+3, mx)
			ptr += count
			if count < mn {
				return m.ret(false)
			}
			pc += int(code[pc])
			if code[pc] == opSUCCESS && ptr == m.end && !(f.toplevel && m.mustAdvance && ptr == m.start) {
				m.ptr = ptr
				return m.ret(true)
			}

		case opREPEAT:
			rep := &repeatCtx{count: -1, pc: pc, lastPtr: -1, prev: m.repeat}
			m.repeat = rep
			f.rep = rep
			f.pc, f.ptr = pc, ptr
			m.ptr = ptr
			return m.call(f, pc+int(code[pc]), f.toplevel, jRepeat)

		case opMAX_UNTIL:
			rep := m.repeat
			if rep == nil {
				return m.ret(false)
			}
			m.ptr = ptr
			f.pc, f.ptr, f.rep = pc, ptr, rep
			f.count = rep.count + 1
			mn, mx := int(code[rep.pc+1]), code[rep.pc+2]
			if f.count < mn {
				rep.count = f.count
				return m.call(f, rep.pc+3, f.toplevel, jMaxUntil1)
			}
			if (f.count < int(mx) || mx == maxRepeat) && m.ptr != rep.lastPtr {
				rep.count = f.count
				m.lastmarkSave(f)
				m.markPush(f)
				f.savedLast = rep.lastPtr
				rep.lastPtr = m.ptr
				return m.call(f, rep.pc+3, f.toplevel, jMaxUntil2)
			}
			m.repeat = rep.prev
			return m.call(f, pc, f.toplevel, jMaxUntil3)

		case opMIN_UNTIL:
			rep := m.repeat
			if rep == nil {
				return m.ret(false)
			}
			m.ptr = ptr
			f.pc, f.ptr, f.rep = pc, ptr, rep
			f.count = rep.count + 1
			if f.count < int(code[rep.pc+1]) {
				rep.count = f.count
				return m.call(f, rep.pc+3, f.toplevel, jMinUntil1)
			}
			m.repeat = rep.prev
			m.lastmarkSave(f)
			if m.repeat != nil {
				m.markPush(f)
			}
			return m.call(f, pc, f.toplevel, jMinUntil2)

		case opPOSSESSIVE_REPEAT:
			m.ptr = ptr
			f.pc, f.ptr = pc, ptr
			rep := &repeatCtx{count: -1, pc: -1, lastPtr: -1, prev: m.repeat}
			m.repeat = rep
			f.rep = rep
			f.count = 0
			return m.possMin(f)

		case opATOMIC_GROUP:
			m.ptr = ptr
			f.pc, f.ptr = pc, ptr
			return m.call(f, pc+1, false, jAtomic)

		case opGROUPREF, opGROUPREF_IGNORE, opGROUPREF_LOC_IGNORE, opGROUPREF_UNI_IGNORE:
			g := int(code[pc]) * 2
			if g >= m.lastmark {
				return m.ret(false)
			}
			p, e := m.marks[g], m.marks[g+1]
			if p < 0 || e < 0 || e < p {
				return m.ret(false)
			}
			for ; p < e; p++ {
				if ptr >= end {
					return m.ret(false)
				}
				a, b := text[ptr], text[p]
				switch op {
				case opGROUPREF_IGNORE, opGROUPREF_LOC_IGNORE:
					a, b = lowerASCII(a), lowerASCII(b)
				case opGROUPREF_UNI_IGNORE:
					a, b = pyuni.SimpleLower(a), pyuni.SimpleLower(b)
				}
				if a != b {
					return m.ret(false)
				}
				ptr++
			}
			pc++

		case opGROUPREF_EXISTS:
			g := int(code[pc]) * 2
			if g >= m.lastmark || m.marks[g] < 0 || m.marks[g+1] < 0 || m.marks[g+1] < m.marks[g] {
				pc += int(code[pc+1])
			} else {
				pc += 2
			}

		case opASSERT:
			back := int(code[pc+1])
			if ptr < back {
				return m.ret(false)
			}
			m.ptr = ptr - back
			f.pc, f.ptr = pc, ptr
			return m.call(f, pc+2, false, jAssert)

		case opASSERT_NOT:
			back := int(code[pc+1])
			if ptr >= back {
				m.ptr = ptr - back
				m.lastmarkSave(f)
				if m.repeat != nil {
					m.markPush(f)
				}
				f.pc, f.ptr = pc, ptr
				return m.call(f, pc+2, false, jAssertNot)
			}
			pc += int(code[pc])

		default:
			return m.ret(false)
		}
	}
}
