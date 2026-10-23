// Naming IR instructions for models.

package taint

import (
	"strings"
	"sync"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/tsbridge"
)

// resolver names calls and property reads with tsbridge qualified names, in
// the order ADR 0010 sets: symbols, receiver types, import provenance, and
// the untyped-handler heuristic. One resolver serves one program; it is safe
// for concurrent use.
type resolver struct {
	prog *tsbridge.Program

	mu      sync.Mutex
	globals map[*ir.Module]map[string]string // module-level bindings initialised from an import: name -> provenance
}

func newResolver(prog *tsbridge.Program) *resolver {
	return &resolver{prog: prog, globals: map[*ir.Module]map[string]string{}}
}

// name is a resolved qualified name and how it was found.
type name struct {
	qn        string
	heuristic bool // through the untyped-handler heuristic: medium confidence
}

// funcNames resolves names within one function; it memoises provenance.
type funcNames struct {
	r       *resolver
	m       *ir.Module
	f       *ir.Func
	prov    map[ir.ValueID]name // values that stand for a module, an export of one, or an Express request
	visited map[ir.ValueID]bool
	partial map[string]string // module body only: the module globals resolved so far, in source order
}

func (r *resolver) forFunc(m *ir.Module, f *ir.Func) *funcNames {
	return &funcNames{r: r, m: m, f: f, prov: map[ir.ValueID]name{}, visited: map[ir.ValueID]bool{}}
}

// call returns the qualified name a call or new resolves to.
func (n *funcNames) call(in *ir.Instr) (name, bool) {
	if c, ok := n.r.prog.ResolveCall(in.Syntax); ok && c.QualifiedName != "" && !strings.HasPrefix(c.QualifiedName, "local:") {
		return name{qn: c.QualifiedName}, true
	}
	args := n.f.Args(in)
	if len(args) == 0 {
		return name{}, false
	}
	// A method call: the receiver's provenance or type names the owner.
	if in.Op == ir.OpCall && in.Aux.Flags.Has(ir.AuxMethod) && !in.Aux.Flags.Has(ir.AuxDynamic) && !in.Aux.Flags.Has(ir.AuxSuper) {
		return n.member(args[0], in.Aux.Name.String())
	}
	if in.Aux.Flags.Has(ir.AuxSuper) || in.Aux.Flags.Has(ir.AuxSuperCall) {
		return name{}, false
	}
	// A plain call or new: the callee's provenance.
	if p, ok := n.provenance(args[0]); ok {
		return p, true
	}
	return name{}, false
}

// property returns the qualified name of a static property read
// (load v.name).
func (n *funcNames) property(in *ir.Instr) (name, bool) {
	if s, ok := n.r.prog.SymbolOf(in.Syntax); ok {
		if qn := n.r.prog.QualifiedName(s); qn != "" && !strings.HasPrefix(qn, "local:") {
			return name{qn: qn}, true
		}
	}
	args := n.f.Args(in)
	if len(args) == 0 {
		return name{}, false
	}
	return n.member(args[0], in.Aux.Name.String())
}

// member names recv.prop through the receiver's type or provenance.
func (n *funcNames) member(recv ir.ValueID, prop string) (name, bool) {
	if v := n.f.Value(recv); v != nil && v.Type != 0 {
		if owner := n.r.prog.TypeQualifiedName(v.Type); owner != "" && !strings.HasPrefix(owner, "local:") {
			return name{qn: owner + "." + prop}, true
		}
	}
	if p, ok := n.provenance(recv); ok {
		return name{qn: joinMember(p.qn, prop), heuristic: p.heuristic}, true
	}
	return name{}, false
}

// joinMember appends a member to a provenance: node:fs + readFile is
// node:fs.readFile, pg: + Pool is pg:Pool, express:Request + query is
// express:Request.query.
func joinMember(base, prop string) string {
	if strings.HasSuffix(base, ":") {
		return base + prop
	}
	return base + "." + prop
}

// provenance says what module, module export or request a value stands for,
// when the code shows it without types: require("x") and what is read from
// it, module-level bindings initialised that way, undeclared Node globals
// (process), and the first parameter of an untyped (req, res) handler.
func (n *funcNames) provenance(v ir.ValueID) (name, bool) {
	if p, ok := n.prov[v]; ok {
		return p, p.qn != ""
	}
	if n.visited[v] {
		return name{}, false
	}
	n.visited[v] = true
	p := n.computeProvenance(v)
	n.prov[v] = p
	return p, p.qn != ""
}

func (n *funcNames) computeProvenance(v ir.ValueID) name {
	val := n.f.Value(v)
	if val == nil {
		return name{}
	}
	switch val.Kind {
	case ir.ValueParam:
		if qn := untypedHandlerParam(n.r.prog, n.f, v); qn != "" {
			return name{qn: qn, heuristic: true}
		}
		return name{}
	case ir.ValueInstr:
	default:
		return name{}
	}
	in := n.f.Instr(val.Def)
	if in == nil {
		return name{}
	}
	args := n.f.Args(in)
	switch in.Op {
	case ir.OpAssign, ir.OpAwait:
		return n.provOf(args[0])
	case ir.OpNew:
		// An instance is named after its class: new Pool() from
		// require("pg") is a pg:Pool, so pool.query is pg:Pool.query.
		return n.provOf(args[0])
	case ir.OpCall:
		if in.Aux.Flags.Has(ir.AuxRequire) && len(args) >= 2 {
			if spec, ok := constString(n.f, args[1]); ok {
				return name{qn: moduleRoot(spec)}
			}
		}
		if in.Aux.Flags.Has(ir.AuxImport) && len(args) >= 2 {
			if spec, ok := constString(n.f, args[1]); ok {
				return name{qn: moduleRoot(spec)}
			}
		}
	case ir.OpLoad:
		switch {
		case in.Aux.Flags.Has(ir.AuxGlobal):
			return n.globalProvenance(in.Aux.Name.String())
		case in.Aux.Flags&(ir.AuxDynamic|ir.AuxCell|ir.AuxSuper|ir.AuxIterKey|ir.AuxIterValue|ir.AuxRest) == 0 && len(args) > 0:
			if base, ok := n.provenance(args[0]); ok {
				if base.heuristic {
					// Only the request itself is a pseudo-type; what is read
					// from it is plain data.
					return name{}
				}
				return name{qn: joinMember(base.qn, in.Aux.Name.String())}
			}
		}
	}
	return name{}
}

func (n *funcNames) provOf(v ir.ValueID) name {
	p, _ := n.provenance(v)
	return p
}

// globalProvenance resolves a module-level binding to the import it was
// initialised from, or an undeclared Node global to its global name.
func (n *funcNames) globalProvenance(g string) name {
	for _, imp := range n.m.Imports {
		if imp.Local != g || imp.TypeOnly {
			continue
		}
		root := moduleRoot(imp.Specifier)
		switch imp.Imported {
		case "default", "*":
			if strings.HasSuffix(root, ":") { // an npm package's default export
				return name{qn: root + "default"}
			}
			return name{qn: root}
		case "":
			return name{}
		default:
			return name{qn: joinMember(root, imp.Imported)}
		}
	}
	if qn, ok := n.r.moduleGlobals(n.m)[g]; ok {
		return name{qn: qn}
	}
	if nodeGlobals[g] {
		return name{qn: "global:" + g}
	}
	return name{}
}

// nodeGlobals are Node.js globals models refer to, named when the code has
// no @types/node to resolve them.
var nodeGlobals = map[string]bool{"process": true, "Buffer": true, "require": true}

// moduleGlobals returns, for a module, the module-level bindings its body
// initialises from an import: `const exec = require("child_process").exec`
// gives exec -> node:child_process.exec. A binding written more than once is
// left out.
func (r *resolver) moduleGlobals(m *ir.Module) map[string]string {
	r.mu.Lock()
	g, ok := r.globals[m]
	r.mu.Unlock()
	if ok {
		return g
	}
	g = map[string]string{}
	writes := map[string]int{}
	init := m.Init
	if init != nil {
		// initProvenance resolves the module body's own global loads
		// through imports only, so this never recurses.
		names := &funcNames{r: r, m: m, f: init, partial: g}
		for i := 1; i < len(init.Instrs); i++ {
			in := &init.Instrs[i]
			if in.Op != ir.OpStore || !in.Aux.Flags.Has(ir.AuxGlobal) {
				continue
			}
			gname := in.Aux.Name.String()
			writes[gname]++
			args := init.Args(in)
			if p := names.initProvenance(args[len(args)-1]); p != "" {
				g[gname] = p
			}
		}
	}
	for k, n := range writes {
		if n > 1 {
			delete(g, k)
		}
	}
	r.mu.Lock()
	if old, ok := r.globals[m]; ok {
		g = old
	} else {
		r.globals[m] = g
	}
	r.mu.Unlock()
	return g
}

// initProvenance is provenance inside a module body, without consulting
// module globals (which are being computed).
func (n *funcNames) initProvenance(v ir.ValueID) string {
	val := n.f.Value(v)
	if val == nil || val.Kind != ir.ValueInstr {
		return ""
	}
	in := n.f.Instr(val.Def)
	args := n.f.Args(in)
	switch in.Op {
	case ir.OpCall:
		if (in.Aux.Flags.Has(ir.AuxRequire) || in.Aux.Flags.Has(ir.AuxImport)) && len(args) >= 2 {
			if spec, ok := constString(n.f, args[1]); ok {
				return moduleRoot(spec)
			}
		}
	case ir.OpAwait, ir.OpAssign, ir.OpNew:
		// new Pool() is named after its class, like in provenance.
		return n.initProvenance(args[0])
	case ir.OpLoad:
		if in.Aux.Flags.Has(ir.AuxGlobal) {
			for _, imp := range n.m.Imports {
				if imp.Local == in.Aux.Name.String() && !imp.TypeOnly && imp.Imported != "" {
					root := moduleRoot(imp.Specifier)
					if imp.Imported == "default" || imp.Imported == "*" {
						return root
					}
					return joinMember(root, imp.Imported)
				}
			}
			return n.partial[in.Aux.Name.String()]
		}
		if in.Aux.Flags&(ir.AuxDynamic|ir.AuxCell|ir.AuxSuper|ir.AuxIterKey|ir.AuxIterValue|ir.AuxRest) == 0 && len(args) > 0 {
			if base := n.initProvenance(args[0]); base != "" {
				return joinMember(base, in.Aux.Name.String())
			}
		}
	}
	return ""
}

// moduleRoot names a module specifier: a Node built-in is node:<module>
// (members joined with "."), an npm package is <package>: (members joined
// directly), relative specifiers are not named.
func moduleRoot(spec string) string {
	bare := strings.TrimPrefix(spec, "node:")
	if nodeBuiltins[bare] || strings.HasPrefix(spec, "node:") {
		return "node:" + bare
	}
	if spec == "" || strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		return ""
	}
	// Package name: @scope/pkg or pkg, without a subpath.
	parts := strings.Split(spec, "/")
	pkg := parts[0]
	if strings.HasPrefix(pkg, "@") && len(parts) > 1 {
		pkg += "/" + parts[1]
	}
	return pkg + ":"
}

// nodeBuiltins are the Node.js core modules models name.
var nodeBuiltins = map[string]bool{
	"assert": true, "buffer": true, "child_process": true, "cluster": true, "crypto": true, "dgram": true,
	"dns": true, "events": true, "fs": true, "fs/promises": true, "http": true, "http2": true, "https": true,
	"net": true, "os": true, "path": true, "path/posix": true, "path/win32": true, "querystring": true,
	"readline": true, "stream": true, "string_decoder": true, "timers": true, "tls": true, "url": true,
	"util": true, "vm": true, "worker_threads": true, "zlib": true,
}

// constString returns the text of a string const value.
func constString(f *ir.Func, v ir.ValueID) (string, bool) {
	val := f.Value(v)
	if val == nil || val.Kind != ir.ValueInstr {
		return "", false
	}
	in := f.Instr(val.Def)
	if in == nil || in.Op != ir.OpConst || in.Aux.Flags.Lit() != ir.LitString {
		return "", false
	}
	return in.Aux.Name.String(), true
}

// untypedHandlerParam names the parameters of an untyped Express-style
// handler (ADR 0010): a function whose first parameter is named req or
// request and typed any (or untyped JavaScript), and whose second is named
// res or response. The first stands for express:Request, the second (when
// it is untyped too) for express:Response; anything else is "".
func untypedHandlerParam(prog *tsbridge.Program, f *ir.Func, v ir.ValueID) string {
	if len(f.Params) < 2 || (f.Params[0] != v && f.Params[1] != v) {
		return ""
	}
	p0, p1 := f.Value(f.Params[0]), f.Value(f.Params[1])
	if p0 == nil || p1 == nil || (p0.Name != "req" && p0.Name != "request") || (p1.Name != "res" && p1.Name != "response") {
		return ""
	}
	untyped := func(p *ir.Value) bool {
		return p.Type == 0 || prog.TypeFlags(p.Type)&(tsbridge.TypeAny|tsbridge.TypeUnknown) != 0
	}
	if !untyped(p0) {
		return ""
	}
	if v == f.Params[0] {
		return "express:Request"
	}
	if untyped(p1) {
		return "express:Response"
	}
	return ""
}
