package ir

import (
	"unique"

	"github.com/patchd/patchd/engine/tsbridge"
)

// IDs are small integers indexing a module's functions and a function's
// blocks, instructions and values. Zero is always "none": index 0 of each
// slice is an unused placeholder, so an ID is also its index.

// FuncID identifies a function of a module.
type FuncID uint32

// BlockID identifies a block of a function.
type BlockID uint32

// InstrID identifies an instruction of a function.
type InstrID uint32

// ValueID identifies a value of a function.
type ValueID uint32

// The "none" IDs.
const (
	NoFunc  FuncID  = 0
	NoBlock BlockID = 0
	NoInstr InstrID = 0
	NoValue ValueID = 0
)

// Name is an interned string: property names, identifiers, operators and
// literal text are stored once per process however many instructions use
// them, and compare by pointer. The zero Name is "".
type Name struct{ h unique.Handle[string] }

// Intern returns the Name for s.
func Intern(s string) Name {
	if s == "" {
		return Name{}
	}
	return Name{unique.Make(s)}
}

func (n Name) String() string {
	if n == (Name{}) {
		return ""
	}
	return n.h.Value()
}

// Module is the IR of one source file.
type Module struct {
	File     tsbridge.FileID
	Path     string
	Init     *Func   // the module body (ModuleInit); also Funcs[0]
	Funcs    []*Func // every function in the file, including nested ones and Init, in source order; Funcs[i].ID == i+1
	Exports  []Export
	Imports  []Import
	Warnings []Warning
	Coverage Coverage
	Stats    Stats
}

// Stats are counters about how a module was lowered.
type Stats struct {
	// FinallyCopies counts the extra copies of finally bodies (one per way
	// out of a try beyond the first) and FinallyInstrs the instructions they
	// add: the code-size cost of lowering finally by duplication.
	FinallyCopies int `json:"finally_copies"`
	FinallyInstrs int `json:"finally_instrs"`
}

// Func returns the function with an ID; nil for NoFunc or a bad ID.
func (m *Module) Func(id FuncID) *Func {
	if id == NoFunc || int(id) > len(m.Funcs) {
		return nil
	}
	return m.Funcs[id-1]
}

// Export returns the export with a name.
func (m *Module) Export(name string) (Export, bool) {
	for _, e := range m.Exports {
		if e.Name == name {
			return e, true
		}
	}
	return Export{}, false
}

// Export is one name a module exports. Value is the exported value in Init
// at the point of export for declarations and default expressions; Local is
// the module-level binding for live bindings (export let x, export { x });
// Import is set for re-exports.
type Export struct {
	Name   string         // the exported name; "default" for default exports and module.exports; "*" for export * from
	Local  string         // the module-level binding it exports, if any
	Value  ValueID        // the exported value in Init, if known at the export site
	Import int            // index+1 into Module.Imports of the import it re-exports; 0 if none
	Kind   ExportKind     // how it was written
	Source tsbridge.Range // the export syntax
}

// ExportKind says how an export was written.
type ExportKind uint8

// Export kinds.
const (
	ExportDeclaration ExportKind = iota // export function f / export const x / export class C
	ExportNamed                         // export { a as b }
	ExportDefault                       // export default …
	ExportReexport                      // export { a } from / export * from / export * as ns from
	ExportAssignment                    // export = e
	ExportCommonJS                      // module.exports = … / exports.x = …
)

// Import is one binding a module imports, or one module it loads for side
// effects or re-exports.
type Import struct {
	Specifier string                // "express", "./db"
	Imported  string                // "default", the imported name, "*" for namespaces, "" for side-effect imports
	Local     string                // the local binding (a global of the module); "" if none
	Kind      ImportKind            // how it was written
	TypeOnly  bool                  // import type: no runtime value
	Target    tsbridge.ModuleTarget // what the specifier resolved to, if Resolved
	Resolved  bool
	Source    tsbridge.Range
	Syntax    tsbridge.NodeID
}

// ImportKind says how an import was written.
type ImportKind uint8

// Import kinds.
const (
	ImportESM     ImportKind = iota // import … from
	ImportRequire                   // require("x") with a literal specifier
	ImportDynamic                   // import("x") with a literal specifier
	ImportEquals                    // import x = require("x")
	ImportExport                    // the source of a re-export: export … from "x"
)

// Func is one function's control-flow graph. Values, instructions and blocks
// live in the function's own slices and are referenced by ID.
type Func struct {
	ID       FuncID
	Name     string   // "handler", "UserService.find", "outer.inner", "<anon@12:5>", "<module>"
	Kind     FuncKind // what syntax it came from
	Flags    FuncFlags
	Params   []ValueID // in order; a rest parameter is the last one, flagged FuncRest
	This     ValueID   // NoValue for arrows (they capture this) and module bodies
	FreeVars []FreeVar // captured from enclosing functions, in the order the closure instruction passes them
	Parent   FuncID    // enclosing function, or NoFunc for the module body
	Entry    BlockID
	Source   tsbridge.Range
	Syntax   tsbridge.NodeID // the function-like node; the class for ClassInit/StaticInit and implicit constructors; the SourceFile for ModuleInit
	Type     tsbridge.TypeID // the function's type, e.g. (req: Request) => Promise<void>; 0 for synthetic functions

	Blocks []Block // index = BlockID; Blocks[0] is unused
	Instrs []Instr // index = InstrID; Instrs[0] is unused
	Values []Value // index = ValueID; Values[0] is unused
	args   []ValueID
	parts  []Part
	edges  []BlockID  // backing store for Block.Instrs/Preds/Succs after finish
	order  []InstrID  // backing store for Block.Instrs after finish
	paths  []PathLink // destructuring paths, sorted by Value
	cells  []cellVar  // the variable each cell instruction allocates, by result value (printing only)
	byName map[string]ValueID
}

// FuncKind is the syntax a Func came from.
type FuncKind uint8

// Function kinds.
const (
	FuncDeclaration FuncKind = iota // function f() {} and function expressions
	FuncArrow
	FuncMethod
	FuncGetter
	FuncSetter
	FuncConstructor
	FuncClassInit  // instance field initializers, run by the constructor
	FuncStaticInit // static fields and static blocks, run where the class is defined
	FuncModuleInit // top-level statements
	FuncNamespace  // the body of a namespace declaration, called with the namespace object
)

var funcKindNames = [...]string{"function", "arrow", "method", "getter", "setter", "constructor", "classinit", "staticinit", "module", "namespace"}

func (k FuncKind) String() string {
	if int(k) < len(funcKindNames) {
		return funcKindNames[k]
	}
	return "?"
}

// FuncFlags are facts about a function.
type FuncFlags uint16

// Function flags.
const (
	FuncAsync     FuncFlags = 1 << iota
	FuncGenerator           // function* or async function*
	FuncStatic              // a static method or accessor
	FuncRest                // the last parameter is a rest parameter
	FuncImplicit            // synthesised: an implicit constructor
	FuncDerived             // a constructor of a class with extends
	FuncAbstract            // an abstract member: no body, recorded for the call graph
	FuncPanicked            // lowering panicked; the body is a single unknown
)

var funcFlagNames = []string{"async", "generator", "static", "rest", "implicit", "derived", "abstract", "panicked"}

// FreeVar is a variable a function captures from an enclosing one.
type FreeVar struct {
	Name  string
	Kind  FreeVarKind
	Value ValueID // the value standing for it inside this function
}

// FreeVarKind says what a free variable carries.
type FreeVarKind uint8

// Free variable kinds.
const (
	FreeCell      FreeVarKind = iota // a captured variable's cell: read with load *v, written with store *v
	FreeThis                         // the enclosing function's this (arrows, class field initializers)
	FreeArguments                    // the enclosing function's arguments object
	FreeNewTarget                    // the enclosing function's new.target
)

var freeVarKindNames = [...]string{"cell", "this", "arguments", "new.target"}

// Block is a basic block: instructions ending in exactly one terminator.
type Block struct {
	ID      BlockID
	Instrs  []InstrID
	Preds   []BlockID // normal and exceptional predecessors
	Succs   []BlockID // normal successors, in terminator order (branch: true, false)
	Handler BlockID   // exceptional successor of the block's can-throw instruction, or NoBlock
	Comment string    // "entry", "if.then", "for.body", "catch"…, for printing
	Dead    bool      // lowered from code after return/throw/break: unreachable from entry
}

// Instr is one instruction.
type Instr struct {
	ID     InstrID
	Op     Op
	Block  BlockID
	Result ValueID // NoValue if the instruction produces nothing
	Aux    Aux
	Source tsbridge.Range
	Syntax tsbridge.NodeID
	argOff uint32
	argLen uint32
}

// Aux is an instruction's op-specific data; see docs/engine/ir-ops.md.
type Aux struct {
	Name    Name     // property name, operator, literal text, callee hint, global name, unknown syntax kind
	Flags   AuxFlags // literal kind, access shape, call flags…
	Func    FuncID   // closure: the function it creates
	partOff uint32
	partLen uint32
}

// Value is an SSA value: a parameter, this, a free variable, a global, the
// result of an instruction, or undefined.
type Value struct {
	ID     ValueID
	Kind   ValueKind
	Type   tsbridge.TypeID
	Def    InstrID         // defining instruction for Kind=ValueInstr
	Name   string          // source variable name, parameter name, global name; "" if none
	Syntax tsbridge.NodeID // the node the value stands for (a parameter, the expression an instruction lowers); 0 if synthesised
}

// ValueKind says where a value comes from.
type ValueKind uint8

// Value kinds.
const (
	ValueInstr     ValueKind = iota // the result of Def
	ValueParam                      // a parameter
	ValueThis                       // the function's this
	ValueArguments                  // the function's arguments object
	ValueNewTarget                  // the function's new.target
	ValueSelf                       // the function itself (the name of a named function expression, inside it)
	ValueFreeVar                    // a FreeVar
	ValueGlobal                     // a module-level binding, import or undeclared global, by Name; read and written with load/store global
	ValueIntrinsic                  // a synthetic function: %jsx, %import, %Fragment; Name holds it
	ValueUndefined                  // the undefined value
)

var valueKindNames = [...]string{"instr", "param", "this", "arguments", "new.target", "self", "free", "global", "intrinsic", "undefined"}

func (k ValueKind) String() string {
	if int(k) < len(valueKindNames) {
		return valueKindNames[k]
	}
	return "?"
}

// Part is one entry of an instruction's part list: an object literal key, a
// template literal's static text, an excluded key of a rest pattern.
type Part struct {
	Kind PartKind
	Name Name
}

// PartKind says what a Part is.
type PartKind uint8

// Part kinds.
const (
	PartKey      PartKind = iota // object: a static key taking the next argument
	PartComputed                 // object: a computed key; takes two arguments, key then value
	PartSpread                   // object: ...arg
	PartGetter                   // object: get key() {}; the argument is the getter's closure
	PartSetter                   // object: set key(v) {}
	PartMethod                   // object: key() {}
	PartProto                    // object: __proto__: arg, which sets the prototype
	PartExclude                  // object rest pattern: a key the rest object leaves out
	PartString                   // template and template-strings const: a static string
	PartTarget                   // decorator: what it decorates ("class", "method m", "param m#0", "field x")
)

var partKindNames = [...]string{"key", "computed", "spread", "get", "set", "method", "proto", "exclude", "string", "target"}

func (k PartKind) String() string {
	if int(k) < len(partKindNames) {
		return partKindNames[k]
	}
	return "?"
}

// PathLink records that a value was extracted by destructuring: Value is
// Base followed by Path (".body.user.name", "[0].id").
type PathLink struct {
	Value ValueID
	Base  ValueID
	Path  string
}

type cellVar struct {
	value ValueID
	name  string
}

// Warning is something the builder could not lower precisely.
type Warning struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Path    string         `json:"path,omitempty"`
	Range   tsbridge.Range `json:"range"`
}

// Warning codes.
const (
	WarnUnknown = "IR001" // syntax lowered to unknown
	WarnWith    = "IR002" // a with statement: names in its body may read the object's properties
	WarnPanic   = "IR003" // lowering a function panicked; it is a single unknown block
	WarnEval    = "IR004" // direct eval: it can read and write the function's locals
)

// Block returns the block with an ID; nil for NoBlock or a bad ID.
func (f *Func) Block(id BlockID) *Block {
	if id == NoBlock || int(id) >= len(f.Blocks) {
		return nil
	}
	return &f.Blocks[id]
}

// Instr returns the instruction with an ID; nil for NoInstr or a bad ID.
func (f *Func) Instr(id InstrID) *Instr {
	if id == NoInstr || int(id) >= len(f.Instrs) {
		return nil
	}
	return &f.Instrs[id]
}

// Value returns the value with an ID; nil for NoValue or a bad ID.
func (f *Func) Value(id ValueID) *Value {
	if id == NoValue || int(id) >= len(f.Values) {
		return nil
	}
	return &f.Values[id]
}

// Args returns an instruction's arguments. The slice is shared: callers must
// not modify it. NoValue marks a hole in an array literal.
func (f *Func) Args(in *Instr) []ValueID {
	return f.args[in.argOff : in.argOff+in.argLen : in.argOff+in.argLen]
}

// Parts returns an instruction's part list (object keys, template strings).
func (f *Func) Parts(in *Instr) []Part {
	return f.parts[in.Aux.partOff : in.Aux.partOff+in.Aux.partLen : in.Aux.partOff+in.Aux.partLen]
}

// NumBlocks returns the number of blocks; valid BlockIDs are 1..NumBlocks.
func (f *Func) NumBlocks() int { return len(f.Blocks) - 1 }

// NumInstrs returns the number of instructions.
func (f *Func) NumInstrs() int { return len(f.Instrs) - 1 }

// NumValues returns the number of values.
func (f *Func) NumValues() int { return len(f.Values) - 1 }

// Terminator returns a block's last instruction, or nil if it has none.
func (f *Func) Terminator(b *Block) *Instr {
	if len(b.Instrs) == 0 {
		return nil
	}
	in := f.Instr(b.Instrs[len(b.Instrs)-1])
	if in == nil || !in.Op.IsTerminator() {
		return nil
	}
	return in
}

// PathOf returns how a destructured value was extracted.
func (f *Func) PathOf(v ValueID) (PathLink, bool) {
	lo, hi := 0, len(f.paths)
	for lo < hi {
		m := (lo + hi) / 2
		switch {
		case f.paths[m].Value == v:
			return f.paths[m], true
		case f.paths[m].Value < v:
			lo = m + 1
		default:
			hi = m
		}
	}
	return PathLink{}, false
}

// Paths returns every destructuring path, sorted by value.
func (f *Func) Paths() []PathLink { return f.paths }

// Uses returns, for every value, the instructions that use it as an
// argument, in instruction order. It is built on demand: an instruction that
// uses a value twice is listed twice.
func (f *Func) Uses() [][]InstrID {
	uses := make([][]InstrID, len(f.Values))
	for i := 1; i < len(f.Instrs); i++ {
		in := &f.Instrs[i]
		for _, a := range f.Args(in) {
			if a != NoValue {
				uses[a] = append(uses[a], in.ID)
			}
		}
	}
	return uses
}

// Global returns the value standing for a global name in this function, if
// the function refers to it.
func (f *Func) Global(name string) (ValueID, bool) {
	v, ok := f.byName[name]
	return v, ok
}
