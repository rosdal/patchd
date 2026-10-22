package tsbridge

import (
	"slices"
	"testing"
)

func TestPositions(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	tests := []struct {
		file, needle string
		nth          int
		line, col    int // 1-based, UTF-16 columns
	}{
		{"bom.ts", "bom", 0, 1, 14}, // the BOM is not a column
		{"bom.ts", "two", 0, 2, 14},
		{"crlf.ts", "a = 1", 0, 2, 14}, // "\r\n" is one line break
		{"crlf.ts", "b", 0, 4, 14},     // the empty CRLF line counts
		{"tabs.ts", "return", 0, 2, 3}, // a tab is one column
		{"emoji.ts", ";", 0, 1, 24},    // each emoji is two UTF-16 code units
		{"emoji.ts", "after", 0, 2, 14},
		{"cjk.ts", "x =", 0, 2, 36},   // CJK characters are one code unit, three bytes
		{"cjk.ts", "export", 0, 2, 1}, // after a CJK comment line
	}
	for _, tt := range tests {
		f := fileNamed(t, p, tt.file)
		pos := offsetOf(t, f, tt.needle, tt.nth)
		line, col := f.Position(pos)
		if line != tt.line || col != tt.col {
			t.Errorf("%s %q: Position = %d:%d, want %d:%d", tt.file, tt.needle, line, col, tt.line, tt.col)
		}
		if l, c := f.LSPPosition(pos); l != tt.line-1 || c != tt.col-1 {
			t.Errorf("%s %q: LSPPosition = %d:%d, want %d:%d", tt.file, tt.needle, l, c, tt.line-1, tt.col-1)
		}
		back, ok := f.Offset(tt.line, tt.col)
		if !ok || back != pos {
			t.Errorf("%s %q: Offset(%d, %d) = %d %v, want %d", tt.file, tt.needle, tt.line, tt.col, back, ok, pos)
		}
		if back, ok := f.OffsetLSP(tt.line-1, tt.col-1); !ok || back != pos {
			t.Errorf("%s %q: OffsetLSP = %d %v, want %d", tt.file, tt.needle, back, ok, pos)
		}
	}
}

func TestOffsetEdges(t *testing.T) {
	f := &File{Text: "ab\r\ncd\n"}
	if _, ok := f.Offset(0, 1); ok {
		t.Error("line 0 must be rejected")
	}
	if _, ok := f.Offset(9, 1); ok {
		t.Error("a line past the end must be rejected")
	}
	if _, ok := f.Offset(1, 0); ok {
		t.Error("column 0 must be rejected")
	}
	// A column past the end of the line clamps to the line's end, before the break.
	if pos, ok := f.Offset(1, 99); !ok || pos != 2 {
		t.Errorf("Offset(1, 99) = %d %v, want 2", pos, ok)
	}
	// Offsets outside the file clamp.
	if l, c := f.Position(-5); l != 1 || c != 1 {
		t.Errorf("Position(-5) = %d:%d", l, c)
	}
	if l, c := f.Position(1000); l != 3 || c != 1 {
		t.Errorf("Position(1000) = %d:%d, want 3:1", l, c)
	}
}

func TestLineMap(t *testing.T) {
	tests := []struct {
		text string
		want lineMap
	}{
		{"", lineMap{0}},
		{"a\nb", lineMap{0, 2}},
		{"a\r\nb", lineMap{0, 3}},
		{"a\rb", lineMap{0, 2}},        // a lone CR ends a line, as in editors and LSP
		{"a\xe2\x80\xa8b", lineMap{0}}, // U+2028 does not
		{"a\n\n", lineMap{0, 2, 3}},
	}
	for _, tt := range tests {
		if got := computeLineMap(tt.text); !slices.Equal(got, tt.want) {
			t.Errorf("%q: %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestUTF16LenInvalidUTF8(t *testing.T) {
	if n := utf16Len("a\xffb"); n != 3 {
		t.Errorf("utf16Len with an invalid byte = %d, want 3", n)
	}
}
