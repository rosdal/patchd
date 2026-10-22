package tsbridge

import (
	"sort"
	"unicode/utf8"
)

// Positions are byte offsets into a file's text (Pos), the same offsets the
// compiler uses. Output converts them to lines and columns the way users see
// them:
//
//   - Position: 1-based line, 1-based column in UTF-16 code units. GitHub and
//     SARIF (whose default column kind is utf16CodeUnits) use this.
//   - LSPPosition: 0-based line, 0-based UTF-16 character, the LSP default.
//
// Lines end at "\n", "\r\n" or a lone "\r", as in editors and the LSP spec;
// U+2028 and U+2029 do not end lines. A byte order mark is not a column. A tab
// is one column. Invalid UTF-8 counts one column per byte.

const bom = "\xef\xbb\xbf" // U+FEFF in UTF-8

// lineMap holds the byte offset at which each line starts.
type lineMap []int

func computeLineMap(text string) lineMap {
	starts := lineMap{0}
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			starts = append(starts, i+1)
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			starts = append(starts, i+1)
		}
	}
	return starts
}

func (f *File) lineMap() lineMap {
	f.linesOnce.Do(func() { f.lines = computeLineMap(f.Text) })
	return f.lines
}

// LineCount returns the number of lines in the file.
func (f *File) LineCount() int { return len(f.lineMap()) }

// Position converts a byte offset to a 1-based line and 1-based UTF-16 column.
// Offsets outside the file are clamped to it.
func (f *File) Position(p Pos) (line, column int) {
	l, c := f.LSPPosition(p)
	return l + 1, c + 1
}

// LSPPosition converts a byte offset to a 0-based line and 0-based UTF-16 character.
func (f *File) LSPPosition(p Pos) (line, character int) {
	off := min(max(int(p), 0), len(f.Text))
	lines := f.lineMap()
	line = sort.Search(len(lines), func(i int) bool { return lines[i] > off }) - 1
	start := lines[line]
	if line == 0 && len(f.Text) >= len(bom) && f.Text[:len(bom)] == bom {
		start = min(len(bom), off)
	}
	return line, utf16Len(f.Text[start:off])
}

// Offset converts a 1-based line and 1-based UTF-16 column to a byte offset.
// A column past the end of the line clamps to the line's end; ok is false for
// a line outside the file or a column below 1.
func (f *File) Offset(line, column int) (Pos, bool) {
	return f.OffsetLSP(line-1, column-1)
}

// OffsetLSP converts a 0-based line and 0-based UTF-16 character to a byte offset.
func (f *File) OffsetLSP(line, character int) (Pos, bool) {
	lines := f.lineMap()
	if line < 0 || line >= len(lines) || character < 0 {
		return 0, false
	}
	start, end := lines[line], len(f.Text)
	if line+1 < len(lines) {
		end = lines[line+1]
	}
	if line == 0 && len(f.Text) >= len(bom) && f.Text[:len(bom)] == bom {
		start = len(bom)
	}
	// Exclude the line break from the line's columns.
	for end > start && (f.Text[end-1] == '\n' || f.Text[end-1] == '\r') {
		end--
	}
	off, units := start, 0
	for off < end && units < character {
		r, size := utf8.DecodeRuneInString(f.Text[off:end])
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		off += size
	}
	return Pos(off), true
}

// utf16Len counts UTF-16 code units in s; each invalid UTF-8 byte counts one.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}
