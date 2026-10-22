package tsbridge

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Resolve fixtures mark calls with a trailing comment on the call's line:
//
//	res.redirect(url) // resolves-to: express:Response.redirect
//	anything.run()    // unresolved: any-receiver
//	either.save()     // declarations: 2
//
// The marked call is the outermost call-like node that starts on that line.
var resolveMarker = regexp.MustCompile(`//\s*(resolves-to|unresolved|declarations):\s*(\S+)\s*$`)

func TestResolveFixtures(t *testing.T) {
	ctx := context.Background()
	ws, err := Discover(ctx, filepath.Join("testdata", "resolve"), DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, cfg := range ws.Projects {
		p, err := Load(ctx, cfg, LoadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		for _, id := range p.RootFiles() {
			f := p.File(id)
			calls := map[int]NodeID{} // 1-based line -> outermost call starting there
			p.Walk(id, func(n NodeID) bool {
				if p.Kind(n).IsCallLike() {
					line, _ := f.Position(p.Range(n).Pos)
					if _, ok := calls[line]; !ok {
						calls[line] = n
					}
				}
				return true
			})
			for i, text := range strings.Split(f.Text, "\n") {
				m := resolveMarker.FindStringSubmatch(text)
				if m == nil {
					continue
				}
				checked++
				where := shortPath(f.Path) + ":" + strconv.Itoa(i+1)
				call, ok := calls[i+1]
				if !ok {
					t.Errorf("%s: no call on the marked line", where)
					continue
				}
				callee, resolved := p.ResolveCall(call)
				switch m[1] {
				case "resolves-to":
					if !resolved || callee.QualifiedName != m[2] {
						t.Errorf("%s %s: resolved=%v %q (reason %q), want %s", where, p.Text(call), resolved, callee.QualifiedName, callee.Reason, m[2])
					}
					if resolved && len(callee.Declarations) == 0 {
						t.Errorf("%s: resolved without declarations", where)
					}
				case "unresolved":
					if resolved || string(callee.Reason) != m[2] {
						t.Errorf("%s %s: resolved=%v %q reason %q, want unresolved %s", where, p.Text(call), resolved, callee.QualifiedName, callee.Reason, m[2])
					}
				case "declarations":
					if want, _ := strconv.Atoi(m[2]); !resolved || len(callee.Declarations) != want {
						t.Errorf("%s %s: %d declarations (resolved=%v), want %d", where, p.Text(call), len(callee.Declarations), resolved, want)
					}
				}
			}
		}
	}
	if checked < 25 {
		t.Fatalf("only %d markers checked", checked)
	}
}

func TestResolveDeclarationPackage(t *testing.T) {
	p := loadFixture(t, "resolve", LoadOptions{})
	f := fileNamed(t, p, "db.ts")
	var call NodeID
	p.Walk(f.ID, func(n NodeID) bool {
		if p.Kind(n) == KindCallExpression && strings.HasPrefix(p.Text(n), "client.query") {
			call = n
			return false
		}
		return true
	})
	callee, ok := p.ResolveCall(call)
	if !ok || len(callee.Declarations) != 1 {
		t.Fatalf("client.query: %+v", callee)
	}
	d := callee.Declarations[0]
	if d.Package == nil || d.Package.Name != "@types/pg" || d.Package.Version != "8.11.0" || !strings.HasSuffix(d.Path, "/@types/pg/index.d.ts") || d.Line != 3 {
		t.Errorf("declaration = %+v (package %+v)", d, d.Package)
	}
}

func TestResolveNotACall(t *testing.T) {
	p := loadFixture(t, "resolve", LoadOptions{})
	if c, ok := p.ResolveCall(p.Root(p.RootFiles()[0])); ok || c.Reason != ReasonNotACall {
		t.Errorf("ResolveCall(SourceFile) = %+v %v", c, ok)
	}
	if c, ok := p.ResolveCall(0); ok || c.Reason != ReasonNotACall {
		t.Errorf("ResolveCall(0) = %+v %v", c, ok)
	}
}

func TestPublicPackageName(t *testing.T) {
	for in, want := range map[string]string{
		"@types/express":                   "express",
		"@types/babel__core":               "@babel/core",
		"@types/express-serve-static-core": "express",
		"pg":                               "pg",
		"@prisma/client":                   "@prisma/client",
	} {
		if got := publicPackageName(in); got != want {
			t.Errorf("publicPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}
