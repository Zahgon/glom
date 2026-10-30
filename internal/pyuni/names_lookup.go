// Port of CPython 3.12's unicodedata / str character predicates (PSF License Version 2;
// Copyright (c) 2001-2024 Python Software Foundation).

package pyuni

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
)

var (
	namesOnce sync.Once
	byName    map[string]rune // upper-case names and aliases
	byCode    map[rune]string // primary names
)

func loadNames() {
	data, err := base64.StdEncoding.DecodeString(nameData)
	if err != nil {
		panic("pyuni: corrupt name table: " + err.Error())
	}
	words := strings.Split(nameWords, " ")
	byName = make(map[string]rune, nameCount)
	byCode = make(map[rune]string, nameCount)
	i := 0
	next := func() int {
		v, shift := 0, 0
		for {
			b := data[i]
			i++
			v |= int(b&0x7f) << shift
			if b < 0x80 {
				return v
			}
			shift += 7
		}
	}
	cp := rune(0)
	parts := make([]string, 0, 16)
	for i < len(data) {
		cp += rune(next())
		n := next()
		parts = parts[:0]
		for k := 0; k < n/2; k++ {
			parts = append(parts, words[next()])
		}
		name := strings.Join(parts, " ")
		byName[name] = cp
		if n&1 == 0 {
			byCode[cp] = name
		}
	}
}

// Hangul syllable composition, as in Modules/unicodedata.c.
const (
	hangulSBase  = 0xAC00
	hangulLCount = 19
	hangulVCount = 21
	hangulTCount = 28
	hangulNCount = hangulVCount * hangulTCount
	hangulSCount = hangulLCount * hangulNCount
)

var hangulJamo = [3][]string{
	{"G", "GG", "N", "D", "DD", "R", "M", "B", "BB", "S", "SS", "", "J", "JJ", "C", "K", "T", "P", "H"},
	{"A", "AE", "YA", "YAE", "EO", "E", "YEO", "YE", "O", "WA", "WAE", "OE", "YO", "U", "WEO", "WE", "WI",
		"YU", "EU", "YI", "I"},
	{"", "G", "GG", "GS", "N", "NJ", "NH", "D", "L", "LG", "LM", "LB", "LS", "LT", "LP", "LH", "M", "B", "BS",
		"S", "SS", "NG", "J", "C", "K", "T", "P", "H"},
}

// findSyllable is find_syllable: the longest jamo short name of the column that prefixes s.
func findSyllable(s string, column int) (length, pos int) {
	length, pos = -1, -1
	for i, j := range hangulJamo[column] {
		if len(j) <= length {
			continue
		}
		if strings.HasPrefix(s, j) {
			length, pos = len(j), i
		}
	}
	if length == -1 {
		length = 0
	}
	return length, pos
}

func isUnifiedIdeograph(r rune) bool {
	for _, rg := range cjkRanges {
		if rg[0] <= r && r <= rg[1] {
			return true
		}
	}
	return false
}

// Lookup is unicodedata.lookup(name) restricted to single code points, as \N{name} in a
// re pattern uses it: character names and name aliases match case-insensitively (ASCII), the
// algorithmic "HANGUL SYLLABLE ..." and "CJK UNIFIED IDEOGRAPH-XXXX" (4 or 5 upper-case hex
// digits) forms only in upper case. Named sequences are not found.
func Lookup(name string) (rune, bool) {
	if rest, ok := strings.CutPrefix(name, "HANGUL SYLLABLE "); ok {
		l, li := findSyllable(rest, 0)
		rest = rest[l:]
		v, vi := findSyllable(rest, 1)
		rest = rest[v:]
		t, ti := findSyllable(rest, 2)
		rest = rest[t:]
		if li >= 0 && vi >= 0 && ti >= 0 && rest == "" {
			return rune(hangulSBase + (li*hangulVCount+vi)*hangulTCount + ti), true
		}
		return 0, false
	}
	if hex, ok := strings.CutPrefix(name, "CJK UNIFIED IDEOGRAPH-"); ok {
		if len(hex) != 4 && len(hex) != 5 {
			return 0, false
		}
		v := rune(0)
		for i := 0; i < len(hex); i++ {
			c := hex[i]
			switch {
			case c >= '0' && c <= '9':
				v = v*16 + rune(c-'0')
			case c >= 'A' && c <= 'F':
				v = v*16 + rune(c-'A'+10)
			default:
				return 0, false
			}
		}
		if !isUnifiedIdeograph(v) {
			return 0, false
		}
		return v, true
	}
	namesOnce.Do(loadNames)
	up := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - 32
		}
		return r
	}, name)
	r, ok := byName[up]
	return r, ok
}

// Name is unicodedata.name(chr(r)); ok is false for code points without a name.
func Name(r rune) (string, bool) {
	if r >= hangulSBase && r < hangulSBase+hangulSCount {
		s := int(r - hangulSBase)
		return "HANGUL SYLLABLE " + hangulJamo[0][s/hangulNCount] + hangulJamo[1][s%hangulNCount/hangulTCount] +
			hangulJamo[2][s%hangulTCount], true
	}
	if isUnifiedIdeograph(r) {
		return fmt.Sprintf("CJK UNIFIED IDEOGRAPH-%X", r), true
	}
	namesOnce.Do(loadNames)
	s, ok := byCode[r]
	return s, ok
}
