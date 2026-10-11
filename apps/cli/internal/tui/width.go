package tui

import "unicode"

// runeWidth is the number of terminal columns r takes.
func runeWidth(r rune) int {
	switch {
	case r < 0x300:
		return 1
	case r == 0xfe0f: // VS16 often widens the emoji before it to two columns
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case unicode.Is(wide, r):
		return 2
	}
	return 1
}

// strWidth is the number of terminal columns s takes.
func strWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// cutWidth returns the longest prefix of s that fits in w columns.
func cutWidth(s string, w int) string {
	n := 0
	for i, r := range s {
		if n += runeWidth(r); n > w {
			return s[:i]
		}
	}
	return s
}

// wide holds the East Asian Wide and Fullwidth characters of Unicode 16,
// with small gaps merged in: counting a narrow character as wide only ends
// a row early, while counting a wide one as narrow overflows the row.
var wide = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x1100, 0x115f, 1}, {0x231a, 0x231b, 1}, {0x2329, 0x232a, 1}, {0x23e9, 0x23ec, 1},
		{0x23f0, 0x23f0, 1}, {0x23f3, 0x23f3, 1}, {0x25fd, 0x25fe, 1}, {0x2614, 0x2615, 1},
		{0x2630, 0x2637, 1}, {0x2648, 0x2653, 1}, {0x267f, 0x267f, 1}, {0x268a, 0x268f, 1},
		{0x2693, 0x2693, 1}, {0x26a1, 0x26a1, 1}, {0x26aa, 0x26ab, 1}, {0x26bd, 0x26be, 1},
		{0x26c4, 0x26c5, 1}, {0x26ce, 0x26ce, 1}, {0x26d4, 0x26d4, 1}, {0x26ea, 0x26ea, 1},
		{0x26f2, 0x26f3, 1}, {0x26f5, 0x26f5, 1}, {0x26fa, 0x26fa, 1}, {0x26fd, 0x26fd, 1},
		{0x2705, 0x2705, 1}, {0x270a, 0x270b, 1}, {0x2728, 0x2728, 1}, {0x274c, 0x274c, 1},
		{0x274e, 0x274e, 1}, {0x2753, 0x2755, 1}, {0x2757, 0x2757, 1}, {0x2795, 0x2797, 1},
		{0x27b0, 0x27b0, 1}, {0x27bf, 0x27bf, 1}, {0x2b1b, 0x2b1c, 1}, {0x2b50, 0x2b50, 1},
		{0x2b55, 0x2b55, 1}, {0x2e80, 0xa4cf, 1}, {0xa960, 0xa97c, 1}, {0xac00, 0xd7a3, 1},
		{0xf900, 0xfaff, 1}, {0xfe10, 0xfe19, 1}, {0xfe30, 0xfe6f, 1}, {0xff00, 0xff60, 1},
		{0xffe0, 0xffe6, 1},
	},
	R32: []unicode.Range32{
		{0x16fe0, 0x18d08, 1}, {0x1aff0, 0x1b2fb, 1}, {0x1d300, 0x1d376, 1},
		{0x1f000, 0x1faff, 1}, {0x20000, 0x3fffd, 1},
	},
}
