// Port of CPython 3.12's re module (Lib/re/_parser.py, _compiler.py, Modules/_sre/sre.c); PSF License Version 2, Copyright (c) 2001-2024 Python Software Foundation.

package pyre

const (
	typeFlags   = ASCII | LOCALE | UNICODE
	globalFlags = DEBUG | TEMPLATE

	// maxRepeat is _sre.MAXREPEAT, the "unbounded" repeat count.
	maxRepeat = 4294967295
	// maxGroups is _sre.MAXGROUPS.
	maxGroups = 1073741823
	// maxCode is the largest value one code word holds.
	maxCode = 1<<32 - 1
)

// maxWidth stands for the parser's MAXWIDTH (1 << 64): any width at or above it.
const maxWidth = ^uint64(0)

// Opcodes, numbered as in re/_constants.py.
const (
	opFAILURE = iota
	opSUCCESS
	opANY
	opANY_ALL
	opASSERT
	opASSERT_NOT
	opAT
	opBRANCH
	opCATEGORY
	opCHARSET
	opBIGCHARSET
	opGROUPREF
	opGROUPREF_EXISTS
	opIN
	opINFO
	opJUMP
	opLITERAL
	opMARK
	opMAX_UNTIL
	opMIN_UNTIL
	opNOT_LITERAL
	opNEGATE
	opRANGE
	opREPEAT
	opREPEAT_ONE
	opSUBPATTERN
	opMIN_REPEAT_ONE
	opATOMIC_GROUP
	opPOSSESSIVE_REPEAT
	opPOSSESSIVE_REPEAT_ONE
	opGROUPREF_IGNORE
	opIN_IGNORE
	opLITERAL_IGNORE
	opNOT_LITERAL_IGNORE
	opGROUPREF_LOC_IGNORE
	opIN_LOC_IGNORE
	opLITERAL_LOC_IGNORE
	opNOT_LITERAL_LOC_IGNORE
	opGROUPREF_UNI_IGNORE
	opIN_UNI_IGNORE
	opLITERAL_UNI_IGNORE
	opNOT_LITERAL_UNI_IGNORE
	opRANGE_UNI_IGNORE
	// parser-only operators
	opMIN_REPEAT
	opMAX_REPEAT
)

var opNames = [...]string{
	"FAILURE", "SUCCESS", "ANY", "ANY_ALL", "ASSERT", "ASSERT_NOT", "AT", "BRANCH", "CATEGORY",
	"CHARSET", "BIGCHARSET", "GROUPREF", "GROUPREF_EXISTS", "IN", "INFO", "JUMP", "LITERAL", "MARK",
	"MAX_UNTIL", "MIN_UNTIL", "NOT_LITERAL", "NEGATE", "RANGE", "REPEAT", "REPEAT_ONE", "SUBPATTERN",
	"MIN_REPEAT_ONE", "ATOMIC_GROUP", "POSSESSIVE_REPEAT", "POSSESSIVE_REPEAT_ONE", "GROUPREF_IGNORE",
	"IN_IGNORE", "LITERAL_IGNORE", "NOT_LITERAL_IGNORE", "GROUPREF_LOC_IGNORE", "IN_LOC_IGNORE",
	"LITERAL_LOC_IGNORE", "NOT_LITERAL_LOC_IGNORE", "GROUPREF_UNI_IGNORE", "IN_UNI_IGNORE",
	"LITERAL_UNI_IGNORE", "NOT_LITERAL_UNI_IGNORE", "RANGE_UNI_IGNORE", "MIN_REPEAT", "MAX_REPEAT",
}

// Position codes for opAT.
const (
	atBEGINNING = iota
	atBEGINNING_LINE
	atBEGINNING_STRING
	atBOUNDARY
	atNON_BOUNDARY
	atEND
	atEND_LINE
	atEND_STRING
	atLOC_BOUNDARY
	atLOC_NON_BOUNDARY
	atUNI_BOUNDARY
	atUNI_NON_BOUNDARY
)

// Category codes for opCATEGORY.
const (
	catDIGIT = iota
	catNOT_DIGIT
	catSPACE
	catNOT_SPACE
	catWORD
	catNOT_WORD
	catLINEBREAK
	catNOT_LINEBREAK
	catLOC_WORD
	catLOC_NOT_WORD
	catUNI_DIGIT
	catUNI_NOT_DIGIT
	catUNI_SPACE
	catUNI_NOT_SPACE
	catUNI_WORD
	catUNI_NOT_WORD
	catUNI_LINEBREAK
	catUNI_NOT_LINEBREAK
)

var chLocale = map[int]int{
	catDIGIT: catDIGIT, catNOT_DIGIT: catNOT_DIGIT, catSPACE: catSPACE, catNOT_SPACE: catNOT_SPACE,
	catWORD: catLOC_WORD, catNOT_WORD: catLOC_NOT_WORD, catLINEBREAK: catLINEBREAK,
	catNOT_LINEBREAK: catNOT_LINEBREAK,
}

var chUnicode = map[int]int{
	catDIGIT: catUNI_DIGIT, catNOT_DIGIT: catUNI_NOT_DIGIT, catSPACE: catUNI_SPACE,
	catNOT_SPACE: catUNI_NOT_SPACE, catWORD: catUNI_WORD, catNOT_WORD: catUNI_NOT_WORD,
	catLINEBREAK: catUNI_LINEBREAK, catNOT_LINEBREAK: catUNI_NOT_LINEBREAK,
}

var flagLetters = map[rune]int{
	'i': IGNORECASE, 'L': LOCALE, 'm': MULTILINE, 's': DOTALL, 'x': VERBOSE, 'a': ASCII,
	't': TEMPLATE, 'u': UNICODE,
}
