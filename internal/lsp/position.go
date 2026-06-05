package lsp

import "unicode/utf8"

// Position conversions between cgp's token.Pos (1-based line/col, 0-based byte
// offset) and LSP positions (0-based line, UTF-16 code-unit character offset).
//
// The two coordinate systems disagree on two axes: cgp counts from 1 and
// measures columns in bytes, LSP counts from 0 and measures characters in
// UTF-16 code units. We always recompute the LSP character from the byte offset
// (never from token.Pos.Col) so multi-byte runes are handled correctly.

// lineStarts returns the byte offset at which each line begins; line i starts at
// the returned slice's element i (line 0 = offset 0).
func lineStarts(src string) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// utf16Len returns the number of UTF-16 code units needed to encode s. Runes
// outside the Basic Multilingual Plane take two units (a surrogate pair).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// utf16Char returns the 0-based UTF-16 character offset within its line for the
// byte offset off, given the precomputed line starts. line0 is the 0-based line.
func utf16Char(src string, starts []int, line0, off int) int {
	if line0 < 0 || line0 >= len(starts) {
		return 0
	}
	ls := starts[line0]
	if off < ls {
		off = ls
	}
	if off > len(src) {
		off = len(src)
	}
	return utf16Len(src[ls:off])
}

// offsetForPosition converts an LSP position (0-based line, UTF-16 character) to
// a byte offset into src. Used to find the token under the cursor for hover.
func offsetForPosition(src string, starts []int, pos Position) int {
	if pos.Line < 0 || pos.Line >= len(starts) {
		return len(src)
	}
	off := starts[pos.Line]
	units := 0
	for off < len(src) && src[off] != '\n' && units < pos.Character {
		r, size := utf8.DecodeRuneInString(src[off:])
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
		off += size
	}
	return off
}
