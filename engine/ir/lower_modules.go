package ir

import (
	"github.com/patchd/patchd/engine/tsbridge"
)

// Modules: ES module imports and exports, and CommonJS require and
// module.exports. Imports are recorded in Module.Imports and their bindings
// are globals of the module, read with load global where used. Exports are
// recorded in Module.Exports with the value exported where it is known.

func (fb *fnBuilder) lowerModuleBody() {
	root := fb.info.node
	fb.lowerStmts(fb.t.Children(root))
}

// addImport records an import; specNode is the module specifier literal.
func (fb *fnBuilder) addImport(imp Import, specNode, at tsbridge.NodeID) int {
	imp.Syntax = at
	imp.Source = fb.t.Range(at)
	if specNode != 0 {
		fb.mark(specNode)
		if target, ok := fb.b.prog.ResolveModule(specNode); ok {
			imp.Target, imp.Resolved = target, true
		}
	}
	fb.b.mod.Imports = append(fb.b.mod.Imports, imp)
	return len(fb.b.mod.Imports)
}

// moduleSpecifier returns the string literal child of an import or export
// declaration.
func (fb *fnBuilder) moduleSpecifier(n tsbridge.NodeID) (tsbridge.NodeID, string) {
	for _, c := range fb.t.Children(n) {
		if fb.t.Kind(c) == tsbridge.KindStringLiteral {
			return c, fb.t.Value(c)
		}
		if fb.t.Kind(c) == tsbridge.KindExternalModuleReference {
			fb.mark(c)
			if s := fb.first(c); s != 0 {
				return s, fb.t.Value(s)
			}
		}
	}
	return 0, ""
}

func (fb *fnBuilder) lowerImport(n tsbridge.NodeID) {
	t := fb.t
	specNode, spec := fb.moduleSpecifier(n)
	for _, c := range t.Children(n) {
		if t.Kind(c) == tsbridge.KindImportAttributes {
			fb.mark(c)
			for _, a := range t.Children(c) {
				fb.mark(a)
			}
		}
	}
	var clause tsbridge.NodeID
	for _, c := range t.Children(n) {
		if t.Kind(c) == tsbridge.KindImportClause {
			clause = c
		}
	}
	if clause == 0 {
		fb.addImport(Import{Specifier: spec, Kind: ImportESM}, specNode, n)
		return
	}
	fb.mark(clause)
	typeOnly := t.Flags(clause)&tsbridge.FlagTypeOnly != 0
	for _, c := range t.Children(clause) {
		fb.mark(c)
		switch t.Kind(c) {
		case tsbridge.KindIdentifier:
			fb.addImport(Import{Specifier: spec, Imported: "default", Local: t.Value(c), Kind: ImportESM, TypeOnly: typeOnly}, specNode, n)
		case tsbridge.KindNamespaceImport:
			for _, id := range t.Children(c) {
				fb.mark(id)
				fb.addImport(Import{Specifier: spec, Imported: "*", Local: t.Value(id), Kind: ImportESM, TypeOnly: typeOnly}, specNode, n)
			}
		case tsbridge.KindNamedImports:
			for _, s := range t.Children(c) {
				fb.mark(s)
				var prop, name string
				for _, sc := range t.Children(s) {
					fb.mark(sc)
					switch t.Role(sc) {
					case tsbridge.RolePropertyName:
						prop = t.Value(sc)
					case tsbridge.RoleName:
						name = t.Value(sc)
					}
				}
				if prop == "" {
					prop = name
				}
				fb.addImport(Import{
					Specifier: spec, Imported: prop, Local: name, Kind: ImportESM,
					TypeOnly: typeOnly || t.Flags(s)&tsbridge.FlagTypeOnly != 0,
				}, specNode, n)
			}
		}
	}
}

// lowerImportEquals lowers import x = require("m") (an import) and
// import x = N.y (an alias of a namespace member).
func (fb *fnBuilder) lowerImportEquals(n tsbridge.NodeID) {
	t := fb.t
	var name tsbridge.NodeID
	var ref tsbridge.NodeID
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindIdentifier:
			if name == 0 {
				name = c
			} else {
				ref = c
			}
		case tsbridge.KindQualifiedName, tsbridge.KindExternalModuleReference:
			ref = c
		}
	}
	fb.mark(name)
	typeOnly := t.Flags(n)&tsbridge.FlagTypeOnly != 0
	if t.Kind(ref) == tsbridge.KindExternalModuleReference {
		specNode, spec := fb.moduleSpecifier(n)
		fb.addImport(Import{Specifier: spec, Imported: "*", Local: t.Value(name), Kind: ImportEquals, TypeOnly: typeOnly}, specNode, n)
		return
	}
	if typeOnly || ref == 0 {
		return
	}
	v := fb.lowerEntityName(ref)
	if vi := fb.b.idVar[name]; vi > 0 {
		fb.writeVar(vi-1, v, name)
		fb.exportDecl(vi-1, v)
	}
}

// lowerEntityName lowers N.x.y in import x = N.x.y.
func (fb *fnBuilder) lowerEntityName(n tsbridge.NodeID) ValueID {
	t := fb.t
	fb.mark(n)
	if t.Kind(n) == tsbridge.KindQualifiedName {
		kids := t.Children(n)
		if len(kids) == 2 {
			obj := fb.lowerEntityName(kids[0])
			fb.mark(kids[1])
			return fb.emitTyped(OpLoad, n, Aux{Name: fb.name(t.Value(kids[1]))}, obj)
		}
	}
	return fb.lowerExpr(n)
}

// lowerExportDecl lowers export { a as b }, export { a } from "m",
// export * from "m" and export * as ns from "m".
func (fb *fnBuilder) lowerExportDecl(n tsbridge.NodeID) {
	t := fb.t
	specNode, spec := fb.moduleSpecifier(n)
	typeOnly := t.Flags(n)&tsbridge.FlagTypeOnly != 0
	src := fb.t.Range(n)
	var named tsbridge.NodeID
	var nsExport tsbridge.NodeID
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindNamedExports:
			named = c
		case tsbridge.KindNamespaceExport:
			nsExport = c
		case tsbridge.KindImportAttributes:
			fb.mark(c)
		}
	}
	if typeOnly {
		fb.markTree(n)
		return
	}
	switch {
	case named == 0 && nsExport == 0: // export * from "m"
		imp := fb.addImport(Import{Specifier: spec, Imported: "*", Kind: ImportExport}, specNode, n)
		fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: "*", Import: imp, Kind: ExportReexport, Source: src})
	case nsExport != 0: // export * as ns from "m"
		fb.mark(nsExport)
		name := ""
		for _, c := range t.Children(nsExport) {
			fb.mark(c)
			name = t.Value(c)
		}
		imp := fb.addImport(Import{Specifier: spec, Imported: "*", Kind: ImportExport}, specNode, n)
		fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: name, Import: imp, Kind: ExportReexport, Source: src})
	default:
		fb.mark(named)
		for _, s := range t.Children(named) {
			fb.mark(s)
			if t.Flags(s)&tsbridge.FlagTypeOnly != 0 {
				fb.markTree(s)
				continue
			}
			var prop, name tsbridge.NodeID
			for _, sc := range t.Children(s) {
				fb.mark(sc)
				switch t.Role(sc) {
				case tsbridge.RolePropertyName:
					prop = sc
				case tsbridge.RoleName:
					name = sc
				}
			}
			local := prop
			if local == 0 {
				local = name
			}
			if specNode != 0 {
				imp := fb.addImport(Import{Specifier: spec, Imported: t.Value(local), Kind: ImportExport}, specNode, n)
				fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: t.Value(name), Import: imp, Kind: ExportReexport, Source: src})
				continue
			}
			e := Export{Name: t.Value(name), Local: t.Value(local), Kind: ExportNamed, Source: src}
			if fb.f == fb.b.mod.Init {
				if vi := fb.b.idVar[local]; vi > 0 && !fb.b.vars[vi-1].global {
					e.Value = fb.readVar(vi-1, local)
				}
				if imp := fb.importIndex(t.Value(local)); imp > 0 && fb.b.idVar[local] > 0 && fb.b.vars[fb.b.idVar[local]-1].kind == declImport {
					e.Import = imp
				}
			}
			fb.b.mod.Exports = append(fb.b.mod.Exports, e)
		}
	}
}

// importIndex returns index+1 of the import binding a local name.
func (fb *fnBuilder) importIndex(local string) int {
	for i, imp := range fb.b.mod.Imports {
		if imp.Local == local && imp.Kind != ImportExport {
			return i + 1
		}
	}
	return 0
}

// markTree marks n and everything under it lowered: syntax that is
// deliberately not lowered because it has no runtime effect.
func (fb *fnBuilder) markTree(n tsbridge.NodeID) {
	fb.mark(n)
	for _, c := range fb.t.Children(n) {
		fb.markTree(c)
	}
}

// lowerExportAssignment lowers export default <expr> and export = <expr>.
func (fb *fnBuilder) lowerExportAssignment(n tsbridge.NodeID) {
	t := fb.t
	v := fb.lowerExpr(fb.first(n))
	kind := ExportDefault
	if t.Flags(n)&tsbridge.FlagExportEquals != 0 {
		kind = ExportAssignment
	}
	if fb.f != fb.b.mod.Init {
		v = NoValue
	}
	fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: "default", Value: v, Kind: kind, Source: t.Range(n)})
}

// exportDecl records an export written as a modifier on a declaration
// (export function f, export const x, export default class), once per
// binding.
func (fb *fnBuilder) exportDecl(v int32, val ValueID) {
	vi := &fb.b.vars[v]
	if !vi.exported || !vi.global || fb.f != fb.b.mod.Init {
		return
	}
	if fb.exports == nil {
		fb.exports = map[int32]bool{}
	}
	if fb.exports[v] {
		return
	}
	fb.exports[v] = true
	name := vi.name
	decl := fb.declOf(vi.decl)
	if fb.t.Flags(decl)&tsbridge.FlagDefault != 0 {
		name = "default"
	}
	fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: name, Local: vi.name, Value: val, Kind: ExportDeclaration, Source: fb.t.Range(decl)})
}

// exportAnonymousDefault records export default function () {} and
// export default class {}.
func (fb *fnBuilder) exportAnonymousDefault(n tsbridge.NodeID, v ValueID) {
	if fb.f != fb.b.mod.Init || fb.t.Flags(n)&tsbridge.FlagDefault == 0 {
		return
	}
	fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: "default", Value: v, Kind: ExportDeclaration, Source: fb.t.Range(n)})
}

// declOf returns the declaration statement a declared name belongs to.
func (fb *fnBuilder) declOf(name tsbridge.NodeID) tsbridge.NodeID {
	t := fb.t
	for n := t.Parent(name); n != 0; n = t.Parent(n) {
		switch t.Kind(n) {
		case tsbridge.KindVariableStatement, tsbridge.KindFunctionDeclaration, tsbridge.KindClassDeclaration,
			tsbridge.KindEnumDeclaration, tsbridge.KindModuleDeclaration, tsbridge.KindImportEqualsDeclaration:
			return n
		case tsbridge.KindSourceFile:
			return name
		}
	}
	return name
}

// commonJSExport recognises module.exports = v, module.exports.x = v and
// exports.x = v.
func (fb *fnBuilder) commonJSExport(r *ref, v ValueID, at tsbridge.NodeID) {
	t := fb.t
	if r.kind != tsbridge.KindPropertyAccessExpression || r.super {
		return
	}
	e := fb.elems(r.node)
	if len(e) < 2 {
		return
	}
	obj := fb.unwrap(e[0])
	val := v
	if fb.f != fb.b.mod.Init {
		val = NoValue
	}
	add := func(name string) {
		fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: name, Value: val, Kind: ExportCommonJS, Source: t.Range(at)})
	}
	switch {
	case r.name == "exports" && fb.isFreeName(obj, "module"):
		add("default")
		// module.exports = { a, b }: each static key is also a named export.
		if d := fb.f.Value(v); d != nil && d.Kind == ValueInstr && fb.f.Instrs[d.Def].Op == OpObject && val != NoValue {
			in := &fb.f.Instrs[d.Def]
			args := fb.f.Args(in)
			ai := 0
			for _, p := range fb.f.Parts(in) {
				switch p.Kind {
				case PartKey, PartMethod:
					if ai < len(args) {
						fb.b.mod.Exports = append(fb.b.mod.Exports, Export{Name: p.Name.String(), Value: args[ai], Kind: ExportCommonJS, Source: t.Range(at)})
					}
					ai++
				case PartComputed:
					ai += 2
				default:
					ai++
				}
			}
		}
	case fb.isFreeName(obj, "exports"):
		add(r.name)
	case t.Kind(obj) == tsbridge.KindPropertyAccessExpression:
		oe := fb.elems(obj)
		if len(oe) == 2 && t.Value(oe[1]) == "exports" && fb.isFreeName(fb.unwrap(oe[0]), "module") {
			add(r.name)
		}
	}
}

// requireBinding returns the variable a require call initializes directly
// (const x = require("m")), if any.
func (fb *fnBuilder) requireBinding(call tsbridge.NodeID) string {
	t := fb.t
	p := t.Parent(call)
	if t.Kind(p) != tsbridge.KindVariableDeclaration {
		return ""
	}
	if e := fb.elems(p); len(e) == 2 && e[1] == call && t.Kind(e[0]) == tsbridge.KindIdentifier {
		return t.Value(e[0])
	}
	return ""
}

// isFreeName reports an identifier with the given name that is not a local
// binding of the file (module, exports, require in CommonJS).
func (fb *fnBuilder) isFreeName(n tsbridge.NodeID, name string) bool {
	return fb.t.Kind(n) == tsbridge.KindIdentifier && fb.t.Value(n) == name && fb.b.idVar[n] == 0
}
