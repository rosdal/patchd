package tsbridge

import (
	"strings"
	"testing"
)

// findNode returns the first node of kind k whose text starts with prefix.
func findNode(t *testing.T, tr *Tree, k Kind, prefix string) NodeID {
	t.Helper()
	for i := range tr.Len() {
		n := tr.Node(i)
		if tr.Kind(n) == k && strings.HasPrefix(tr.Text(n), prefix) {
			return n
		}
	}
	t.Fatalf("no %v starting with %q", k, prefix)
	return 0
}

func TestTreeMatchesProgram(t *testing.T) {
	p := loadFixture(t, "tree", LoadOptions{})
	f := fileNamed(t, p, "syntax.ts")
	tr := p.Tree(f.ID)
	if tr == nil || tr.File() != f || tr.Root() != p.Root(f.ID) || tr.Len() != p.NodeCount(f.ID) {
		t.Fatal("Tree does not describe the file")
	}
	for i := range tr.Len() {
		n := tr.Node(i)
		if tr.Index(n) != i {
			t.Fatalf("Index(Node(%d)) = %d", i, tr.Index(n))
		}
		if tr.Kind(n) != p.Kind(n) || tr.Parent(n) != p.Parent(n) || tr.Range(n) != p.Range(n) ||
			tr.Text(n) != p.Text(n) || tr.CompilerKind(n) != p.CompilerKind(n) {
			t.Fatalf("node %d: Tree and Program disagree", i)
		}
		pk, tk := p.Children(n), tr.Children(n)
		if len(pk) != len(tk) {
			t.Fatalf("node %d: %d children vs %d", i, len(tk), len(pk))
		}
	}
	if p.Tree(0) != nil || tr.Kind(0) != KindInvalid || tr.Index(nodeID(f.ID+1, 0)) != -1 {
		t.Error("bad handles are not rejected")
	}
}

func TestTreeValuesOperatorsFlags(t *testing.T) {
	p := loadFixture(t, "tree", LoadOptions{})
	tr := p.Tree(fileNamed(t, p, "syntax.ts").ID)
	values := []struct {
		kind   Kind
		prefix string
		want   string
	}{
		{KindStringLiteral, `"a\tb"`, "a\tb"},
		{KindRegularExpressionLiteral, "/ab", "/ab+c/gi"},
		{KindTemplateHead, "`x", "x"},
		{KindTemplateTail, "}y", "y"},
		{KindPrivateIdentifier, "#secret", "#secret"},
		{KindIdentifier, "counter", "counter"},
	}
	for _, v := range values {
		if got := tr.Value(findNode(t, tr, v.kind, v.prefix)); got != v.want {
			t.Errorf("Value(%v %q) = %q, want %q", v.kind, v.prefix, got, v.want)
		}
	}
	ops := []struct {
		kind   Kind
		prefix string
		want   string
	}{
		{KindBinaryExpression, "a + counter", "+"},
		{KindPostfixUnaryExpression, "counter++", "++"},
		{KindPrefixUnaryExpression, "++counter", "++"},
		{KindHeritageClause, "extends", "extends"},
	}
	for _, o := range ops {
		if got := tr.Operator(findNode(t, tr, o.kind, o.prefix)); got != o.want {
			t.Errorf("Operator(%v %q) = %q, want %q", o.kind, o.prefix, got, o.want)
		}
	}
	flags := []struct {
		kind   Kind
		prefix string
		want   SyntaxFlags
	}{
		{KindVariableDeclarationList, "let counter", FlagLet},
		{KindVariableDeclarationList, "const label", FlagConst},
		{KindPropertyAccessExpression, "o?.b.c", FlagOptionalChain},
		{KindPropertyAccessExpression, "o?.b", FlagOptionalChain},
		{KindMethodDeclaration, "async *items", FlagAsync | FlagGenerator},
		{KindPropertyDeclaration, "static count", FlagStatic},
		{KindYieldExpression, "yield*", FlagGenerator},
		{KindForOfStatement, "for await", FlagAwait},
		{KindClassDeclaration, "export class", FlagExport},
		{KindImportClause, "type", FlagTypeOnly},
	}
	for _, fl := range flags {
		if got := tr.Flags(findNode(t, tr, fl.kind, fl.prefix)); got&fl.want != fl.want {
			t.Errorf("Flags(%v %q) = %b, want %b set", fl.kind, fl.prefix, got, fl.want)
		}
	}
	// Only the a?.b link is written with ?.; the outer .c is in the chain.
	outer := findNode(t, tr, KindPropertyAccessExpression, "o?.b.c")
	if tr.Flags(outer)&FlagQuestionDot != 0 || tr.Flags(tr.Children(outer)[0])&FlagQuestionDot == 0 {
		t.Error("FlagQuestionDot is on the wrong link")
	}
}

func TestTreeRoles(t *testing.T) {
	p := loadFixture(t, "tree", LoadOptions{})
	tr := p.Tree(fileNamed(t, p, "syntax.ts").ID)
	roles := func(n NodeID) []Role {
		var out []Role
		for _, c := range tr.Children(n) {
			if r := tr.Role(c); r != RoleNone {
				out = append(out, r)
			}
		}
		return out
	}
	eq := func(a, b []Role) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	cases := []struct {
		kind   Kind
		prefix string
		want   []Role
	}{
		{KindForStatement, "for (let i", []Role{RoleInitializer, RoleCondition, RoleIncrementor, RoleBody}},
		{KindBindingElement, "p: q = 1", []Role{RolePropertyName, RoleName, RoleInitializer}},
		{KindBindingElement, "r", []Role{RoleName}},
		{KindImportSpecifier, "shared as s", []Role{RolePropertyName, RoleName}},
		{KindExportSpecifier, "s", []Role{RoleName}},
	}
	for _, c := range cases {
		if got := roles(findNode(t, tr, c.kind, c.prefix)); !eq(got, c.want) {
			t.Errorf("roles of %v %q = %v, want %v", c.kind, c.prefix, got, c.want)
		}
	}
}

func TestValueSymbols(t *testing.T) {
	p := loadFixture(t, "tree", LoadOptions{})
	tr := p.Tree(fileNamed(t, p, "syntax.ts").ID)
	var aParam, aShorthand, sImport, sExport, mystery NodeID
	for i := range tr.Len() {
		n := tr.Node(i)
		if tr.Kind(n) != KindIdentifier {
			continue
		}
		switch parent := tr.Parent(n); {
		case tr.Value(n) == "a" && tr.Kind(parent) == KindParameter:
			aParam = n
		case tr.Value(n) == "a" && tr.Kind(parent) == KindShorthandPropertyAssignment:
			aShorthand = n
		case tr.Value(n) == "s" && tr.Kind(parent) == KindImportSpecifier:
			sImport = n
		case tr.Value(n) == "s" && tr.Kind(parent) == KindExportSpecifier:
			sExport = n
		case tr.Value(n) == "mystery":
			mystery = n
		}
	}
	syms := p.ValueSymbols([]NodeID{aParam, aShorthand, sImport, sExport, mystery, tr.Root()})
	if syms[0] == 0 || syms[1] != syms[0] {
		t.Errorf("shorthand {a} names symbol %d, the parameter is %d", syms[1], syms[0])
	}
	if syms[2] == 0 || syms[3] != syms[2] {
		t.Errorf("export {s} names symbol %d, the import binding is %d", syms[3], syms[2])
	}
	if syms[4] != 0 || syms[5] != 0 {
		t.Errorf("an undeclared name or a non-identifier has a symbol: %v", syms[4:])
	}
}

func TestTypesOfMatchesTypeOf(t *testing.T) {
	p := loadFixture(t, "tree", LoadOptions{})
	tr := p.Tree(fileNamed(t, p, "syntax.ts").ID)
	var nodes []NodeID
	for i := range tr.Len() {
		if n := tr.Node(i); tr.Kind(n) == KindIdentifier || tr.Kind(n) == KindCallExpression {
			nodes = append(nodes, n)
		}
	}
	got := p.TypesOf(nodes)
	for i, n := range nodes {
		if want := p.TypeOf(n); p.TypeString(got[i]) != p.TypeString(want) {
			t.Errorf("TypesOf(%q) = %s, TypeOf = %s", tr.Text(n), p.TypeString(got[i]), p.TypeString(want))
		}
	}
}

func TestResolveModule(t *testing.T) {
	p := loadFixture(t, "tree", LoadOptions{})
	tr := p.Tree(fileNamed(t, p, "syntax.ts").ID)
	lib := findNode(t, tr, KindStringLiteral, `"./lib"`)
	target, ok := p.ResolveModule(lib)
	if !ok || !strings.HasSuffix(target.Path, "/lib.ts") || target.File == 0 || target.Package != nil {
		t.Errorf("ResolveModule(./lib) = %+v, %v", target, ok)
	}
	if _, ok := p.ResolveModule(findNode(t, tr, KindStringLiteral, `"./nowhere"`)); ok {
		t.Error("a missing module resolved")
	}
	if _, ok := p.ResolveModule(tr.Root()); ok {
		t.Error("a non-literal resolved")
	}
}
