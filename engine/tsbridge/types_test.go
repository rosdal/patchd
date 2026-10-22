package tsbridge

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Type fixtures mark expressions the way the TypeScript playground's
// twoslash queries do: a comment line "//    ^? <type>" under the line it
// queries, with the caret under the expression.
var queryMarker = regexp.MustCompile(`^(\s*//\s*)\^\?\s*(.*)$`)

type typeQuery struct {
	file      *File
	line      int // 0-based line of the queried expression
	character int
	want      string
}

func typeQueries(t *testing.T, p *Program) []typeQuery {
	t.Helper()
	var out []typeQuery
	for _, id := range p.RootFiles() {
		f := p.File(id)
		for i, line := range strings.Split(strings.ReplaceAll(f.Text, "\r\n", "\n"), "\n") {
			m := queryMarker.FindStringSubmatchIndex(line)
			if m == nil {
				continue
			}
			caret := strings.Index(line, "^?")
			out = append(out, typeQuery{file: f, line: i - 1, character: caret, want: line[m[4]:m[5]]})
		}
	}
	if len(out) == 0 {
		t.Fatal("no ^? queries found")
	}
	return out
}

func TestTypeFixtures(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	for _, d := range p.Diagnostics() {
		if d.Category == DiagnosticError {
			t.Errorf("fixture has a type error: %s:%d:%d %s", d.Path, d.Line, d.Column, d.Message)
		}
	}
	for _, q := range typeQueries(t, p) {
		pos, ok := q.file.OffsetLSP(q.line, q.character)
		if !ok {
			t.Fatalf("%s:%d: bad query position", q.file.Path, q.line+1)
		}
		n := p.NodeAt(q.file.ID, pos)
		got := p.TypeString(p.TypeOf(n))
		if got != q.want {
			t.Errorf("%s:%d:%d %q: type %q, want %q", shortPath(q.file.Path), q.line+1, q.character+1, p.Text(n), got, q.want)
		}
	}
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/testdata/"); i >= 0 {
		return p[i+len("/testdata/"):]
	}
	return p
}

// nodeAtText returns the innermost node at the nth occurrence of needle.
func nodeAtText(t *testing.T, p *Program, file, needle string, nth int) NodeID {
	t.Helper()
	f := fileNamed(t, p, file)
	return p.NodeAt(f.ID, offsetOf(t, f, needle, nth))
}

// callAt returns the innermost call-like node containing the nth needle.
func callAt(t *testing.T, p *Program, file, needle string, nth int) NodeID {
	t.Helper()
	for n := nodeAtText(t, p, file, needle, nth); n != 0; n = p.Parent(n) {
		if p.Kind(n).IsCallLike() {
			return n
		}
	}
	t.Fatalf("no call around %q in %s", needle, file)
	return 0
}

func TestTypeFlags(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	tests := []struct {
		file, needle string
		has          TypeFlags
	}{
		{"generics.ts", "n = identity", TypeNumber | TypeLiteral},
		{"generics.ts", "s = identity", TypeString | TypeLiteral},
		{"generics.ts", "widened = identity", TypeNumber},
		{"generics.ts", "f = first", TypeString | TypeLiteral},
		{"mapped.ts", "id = partial", TypeUnion}, // a union's flags are its own; see UnionMembers
		{"mapped.ts", "getName = getters", TypeFunction | TypeObject},
		{"generics.ts", "value: T", TypeTypeParameter},
	}
	for _, tt := range tests {
		got := p.TypeFlags(p.TypeOf(nodeAtText(t, p, tt.file, tt.needle, 0)))
		if got&tt.has != tt.has {
			t.Errorf("%s %q: flags %v, want at least %v", tt.file, tt.needle, got.Strings(), tt.has.Strings())
		}
	}
}

func TestUnionMembersAndAssignability(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	idType := p.TypeOf(nodeAtText(t, p, "mapped.ts", "id = partial", 0))
	members := p.UnionMembers(idType)
	var names []string
	for _, m := range members {
		names = append(names, p.TypeString(m))
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "number,undefined" {
		t.Errorf("union members = %v", names)
	}
	num := p.TypeOf(nodeAtText(t, p, "mapped.ts", "id = partial", 0)) // number | undefined
	var number TypeID
	for _, m := range members {
		if p.TypeString(m) == "number" {
			number = m
		}
	}
	if !p.IsAssignable(number, num) {
		t.Error("number should be assignable to number | undefined")
	}
	if p.IsAssignable(num, number) {
		t.Error("number | undefined should not be assignable to number")
	}
	if p.UnionMembers(number) != nil || p.IntersectionMembers(number) != nil {
		t.Error("number has no union or intersection members")
	}
	if p.IsAssignable(0, number) || p.TypeString(0) != "" || p.TypeFlags(0) != 0 {
		t.Error("TypeID 0 must answer with zero values")
	}
}

func TestPropertiesIncludeInheritedAndMerged(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	names := func(file, needle string) []string {
		var out []string
		for _, s := range p.Properties(p.TypeOf(nodeAtText(t, p, file, needle, 0))) {
			out = append(out, p.SymbolName(s))
		}
		return out
	}
	if got := names("merging.ts", "box.height"); !slices.Contains(got, "width") || !slices.Contains(got, "height") {
		t.Errorf("merged interface properties = %v", got)
	}
	if got := names("classes.ts", "built ="); !slices.Contains(got, "add") || !slices.Contains(got, "shout") || !slices.Contains(got, "build") {
		t.Errorf("inherited class properties = %v", got)
	}
	// Apparent type: a string has String's members.
	if got := names("classes.ts", "out ="); !slices.Contains(got, "toUpperCase") {
		t.Errorf("string properties = %v", got)
	}
}

func TestSignature(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	sig, ok := p.Signature(callAt(t, p, "overloads.ts", `parse(1)`, 0))
	if !ok {
		t.Fatal("no signature for parse(1)")
	}
	if p.TypeString(sig.Return) != "string" || len(sig.Parameters) != 1 || p.TypeString(sig.Parameters[0].Type) != "number" {
		t.Errorf("parse(1) chose %q (return %s)", sig.Text, p.TypeString(sig.Return))
	}
	if sig.Declaration == nil || sig.Declaration.Line != 3 {
		t.Errorf("parse(1) declaration = %+v, want the second overload on line 3", sig.Declaration)
	}
	// Generic instantiation.
	sig, ok = p.Signature(callAt(t, p, "generics.ts", `identity("hi")`, 0))
	if !ok || p.TypeString(sig.Parameters[0].Type) != `"hi"` || p.TypeString(sig.Return) != `"hi"` {
		t.Errorf("identity(\"hi\") = %+v", sig)
	}
	if _, ok := p.Signature(nodeAtText(t, p, "generics.ts", "value", 0)); ok {
		t.Error("Signature on a non-call must fail")
	}
}

func TestSymbolOf(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	tests := []struct {
		file, needle string
		nth          int
		want         string
	}{
		{"classes.ts", "parts.push", 0, "parts"},       // property access name
		{"classes.ts", "this.parts", 0, "Builder"},     // this
		{"generics.ts", "identity(42)", 0, "identity"}, // identifier
		{"merging.ts", "Shapes.sides", 0, "Shapes"},
		{"merging.ts", "height;", 0, "height"}, // a declaration's own name
	}
	for _, tt := range tests {
		s, ok := p.SymbolOf(nodeAtText(t, p, tt.file, tt.needle, tt.nth))
		if !ok || p.SymbolName(s) != tt.want {
			t.Errorf("%s %q: symbol %q (ok=%v), want %q", tt.file, tt.needle, p.SymbolName(s), ok, tt.want)
		}
	}
}

func TestQueryCaching(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	n := nodeAtText(t, p, "generics.ts", "n = identity", 0)
	before := p.Stats()
	a, b := p.TypeOf(n), p.TypeOf(n)
	p.SymbolOf(n)
	p.SymbolOf(n)
	after := p.Stats()
	if a != b {
		t.Fatal("TypeOf is not stable")
	}
	if after.TypeOfHits-before.TypeOfHits < 1 || after.SymbolOfHits-before.SymbolOfHits < 1 {
		t.Errorf("no cache hits: %+v -> %+v", before, after)
	}
}
