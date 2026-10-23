package ir

import "unsafe"

// Bytes estimates the memory a module's IR holds: its functions' slices,
// imports, exports and coverage counters. Interned names are shared across
// modules and not counted. Used for the bytes-per-source-byte budget.
func (m *Module) Bytes() int {
	n := int(unsafe.Sizeof(*m))
	n += cap(m.Funcs) * int(unsafe.Sizeof((*Func)(nil)))
	for _, f := range m.Funcs {
		n += f.Bytes()
	}
	n += cap(m.Imports) * int(unsafe.Sizeof(Import{}))
	n += cap(m.Exports) * int(unsafe.Sizeof(Export{}))
	n += cap(m.Warnings) * int(unsafe.Sizeof(Warning{}))
	n += len(m.Coverage.Kinds) * (int(unsafe.Sizeof(KindCoverage{})) + 64)
	return n
}

// Bytes estimates the memory a function's IR holds.
func (f *Func) Bytes() int {
	n := int(unsafe.Sizeof(*f))
	n += cap(f.Blocks) * int(unsafe.Sizeof(Block{}))
	n += cap(f.Instrs) * int(unsafe.Sizeof(Instr{}))
	n += cap(f.Values) * int(unsafe.Sizeof(Value{}))
	n += cap(f.args) * int(unsafe.Sizeof(ValueID(0)))
	n += cap(f.parts) * int(unsafe.Sizeof(Part{}))
	n += cap(f.edges) * int(unsafe.Sizeof(BlockID(0)))
	n += cap(f.order) * int(unsafe.Sizeof(InstrID(0)))
	n += cap(f.Params) * int(unsafe.Sizeof(ValueID(0)))
	n += cap(f.FreeVars) * int(unsafe.Sizeof(FreeVar{}))
	for _, p := range f.paths {
		n += int(unsafe.Sizeof(p)) + len(p.Path)
	}
	n += cap(f.cells) * int(unsafe.Sizeof(cellVar{}))
	n += len(f.byName) * 48 // map entry: key, value and bucket overhead
	return n
}
