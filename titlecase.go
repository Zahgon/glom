// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. The titlecase mappings of Unicode 15.0 (SpecialCasing.txt
// and UnicodeData.txt) that differ from the simple uppercase, as CPython 3.12's
// str.title and str.capitalize apply them.

package glom

var pyTitleExc = map[rune]string{
	0xdf: "Ss", 0x149: "\u02bcN", 0x19b: "\u019b", 0x1f0: "J\u030c",
	0x264: "\u0264", 0x390: "\u0399\u0308\u0301", 0x3b0: "\u03a5\u0308\u0301", 0x587: "\u0535\u0582",
	0x1c8a: "\u1c8a", 0x1e96: "H\u0331", 0x1e97: "T\u0308", 0x1e98: "W\u030a",
	0x1e99: "Y\u030a", 0x1e9a: "A\u02be", 0x1f50: "\u03a5\u0313", 0x1f52: "\u03a5\u0313\u0300",
	0x1f54: "\u03a5\u0313\u0301", 0x1f56: "\u03a5\u0313\u0342", 0x1fb2: "\u1fba\u0345", 0x1fb4: "\u0386\u0345",
	0x1fb6: "\u0391\u0342", 0x1fb7: "\u0391\u0342\u0345", 0x1fc2: "\u1fca\u0345", 0x1fc4: "\u0389\u0345",
	0x1fc6: "\u0397\u0342", 0x1fc7: "\u0397\u0342\u0345", 0x1fd2: "\u0399\u0308\u0300", 0x1fd3: "\u0399\u0308\u0301",
	0x1fd6: "\u0399\u0342", 0x1fd7: "\u0399\u0308\u0342", 0x1fe2: "\u03a5\u0308\u0300", 0x1fe3: "\u03a5\u0308\u0301",
	0x1fe4: "\u03a1\u0313", 0x1fe6: "\u03a5\u0342", 0x1fe7: "\u03a5\u0308\u0342", 0x1ff2: "\u1ffa\u0345",
	0x1ff4: "\u038f\u0345", 0x1ff6: "\u03a9\u0342", 0x1ff7: "\u03a9\u0342\u0345", 0xa7cd: "\ua7cd",
	0xa7cf: "\ua7cf", 0xa7d3: "\ua7d3", 0xa7d5: "\ua7d5", 0xa7db: "\ua7db",
	0xfb00: "Ff", 0xfb01: "Fi", 0xfb02: "Fl", 0xfb03: "Ffi",
	0xfb04: "Ffl", 0xfb05: "St", 0xfb06: "St", 0xfb13: "\u0544\u0576",
	0xfb14: "\u0544\u0565", 0xfb15: "\u0544\u056b", 0xfb16: "\u054e\u0576", 0xfb17: "\u0544\u056d",
	0x10d70: "\U00010d70", 0x10d71: "\U00010d71", 0x10d72: "\U00010d72", 0x10d73: "\U00010d73",
	0x10d74: "\U00010d74", 0x10d75: "\U00010d75", 0x10d76: "\U00010d76", 0x10d77: "\U00010d77",
	0x10d78: "\U00010d78", 0x10d79: "\U00010d79", 0x10d7a: "\U00010d7a", 0x10d7b: "\U00010d7b",
	0x10d7c: "\U00010d7c", 0x10d7d: "\U00010d7d", 0x10d7e: "\U00010d7e", 0x10d7f: "\U00010d7f",
	0x10d80: "\U00010d80", 0x10d81: "\U00010d81", 0x10d82: "\U00010d82", 0x10d83: "\U00010d83",
	0x10d84: "\U00010d84", 0x10d85: "\U00010d85", 0x16ebb: "\U00016ebb", 0x16ebc: "\U00016ebc",
	0x16ebd: "\U00016ebd", 0x16ebe: "\U00016ebe", 0x16ebf: "\U00016ebf", 0x16ec0: "\U00016ec0",
	0x16ec1: "\U00016ec1", 0x16ec2: "\U00016ec2", 0x16ec3: "\U00016ec3", 0x16ec4: "\U00016ec4",
	0x16ec5: "\U00016ec5", 0x16ec6: "\U00016ec6", 0x16ec7: "\U00016ec7", 0x16ec8: "\U00016ec8",
	0x16ec9: "\U00016ec9", 0x16eca: "\U00016eca", 0x16ecb: "\U00016ecb", 0x16ecc: "\U00016ecc",
	0x16ecd: "\U00016ecd", 0x16ece: "\U00016ece", 0x16ecf: "\U00016ecf", 0x16ed0: "\U00016ed0",
	0x16ed1: "\U00016ed1", 0x16ed2: "\U00016ed2", 0x16ed3: "\U00016ed3",
}
