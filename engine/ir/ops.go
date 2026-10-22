package ir

// Op is an instruction's operation. Semantics: docs/engine/ir-ops.md.
type Op uint8

// Operations.
const (
	OpInvalid Op = iota
	OpConst
	OpAssign
	OpBinOp
	OpUnOp
	OpTemplate
	OpLoad
	OpStore
	OpDelete
	OpCall
	OpNew
	OpObject
	OpArray
	OpClosure
	OpCell
	OpSpread
	OpAwait
	OpYield
	OpPhi
	OpCatchVal
	OpUnknown
	OpJump
	OpBranch
	OpReturn
	OpThrow
	OpUnreachable
	numOps
)

// opInfo is the per-op properties table.
type opInfo struct {
	name       string
	terminator bool
	canThrow   bool
	hasResult  bool
}

var opTable = [numOps]opInfo{
	OpInvalid:     {name: "invalid"},
	OpConst:       {name: "const", hasResult: true},
	OpAssign:      {name: "assign", hasResult: true},
	OpBinOp:       {name: "binop", canThrow: true, hasResult: true},
	OpUnOp:        {name: "unop", hasResult: true},
	OpTemplate:    {name: "template", canThrow: true, hasResult: true},
	OpLoad:        {name: "load", canThrow: true, hasResult: true},
	OpStore:       {name: "store", canThrow: true},
	OpDelete:      {name: "delete", canThrow: true, hasResult: true},
	OpCall:        {name: "call", canThrow: true, hasResult: true},
	OpNew:         {name: "new", canThrow: true, hasResult: true},
	OpObject:      {name: "object", canThrow: true, hasResult: true},
	OpArray:       {name: "array", canThrow: true, hasResult: true},
	OpClosure:     {name: "closure", hasResult: true},
	OpCell:        {name: "cell", hasResult: true},
	OpSpread:      {name: "spread", canThrow: true, hasResult: true},
	OpAwait:       {name: "await", canThrow: true, hasResult: true},
	OpYield:       {name: "yield", canThrow: true, hasResult: true},
	OpPhi:         {name: "phi", hasResult: true},
	OpCatchVal:    {name: "catchval", hasResult: true},
	OpUnknown:     {name: "unknown", canThrow: true, hasResult: true},
	OpJump:        {name: "jump", terminator: true},
	OpBranch:      {name: "branch", terminator: true},
	OpReturn:      {name: "return", terminator: true},
	OpThrow:       {name: "throw", terminator: true, canThrow: true},
	OpUnreachable: {name: "unreachable", terminator: true},
}

func (o Op) String() string {
	if o < numOps {
		return opTable[o].name
	}
	return "op?"
}

// IsTerminator reports whether the op ends a block.
func (o Op) IsTerminator() bool { return o < numOps && opTable[o].terminator }

// CanThrow reports whether the op can raise an exception, and so gets an
// exceptional edge inside a try.
func (o Op) CanThrow() bool { return o < numOps && opTable[o].canThrow }

// HasResult reports whether the op produces a value.
func (o Op) HasResult() bool { return o < numOps && opTable[o].hasResult }

// Ops returns every valid op.
func Ops() []Op {
	out := make([]Op, 0, numOps-1)
	for o := OpInvalid + 1; o < numOps; o++ {
		out = append(out, o)
	}
	return out
}

// AuxFlags are op-specific facts in Aux. The low bits hold a const's literal
// kind; the rest are independent flags.
type AuxFlags uint32

// Literal kinds of a const (Flags & AuxLitMask).
const (
	LitString AuxFlags = iota
	LitNumber
	LitBigInt
	LitBool
	LitNull
	LitUndefined
	LitRegex
	LitStrings          // the strings array of a tagged template: Parts are its PartString entries
	AuxLitMask AuxFlags = 7
)

// Flags.
const (
	AuxDynamic   AuxFlags = 1 << (iota + 3) // load/store/delete/call: the key is an argument (v1[v2]), not Name
	AuxGlobal                               // load/store: a global by Name; the argument is the global value
	AuxCell                                 // load/store: through a cell (load *v1)
	AuxSuper                                // load/call: super.x / super.m() with this as the argument
	AuxIterValue                            // load: the next element of a for…of (load v1[of])
	AuxIterKey                              // load: the next key of a for…in (load v1[in])
	AuxRest                                 // load: an array rest pattern, elements from Name on (load v1[2:])
	AuxProto                                // store: sets the prototype (class extends)
	AuxGetter                               // store: defines a getter; closure: a getter
	AuxSetter                               // store: defines a setter; closure: a setter
	AuxMethod                               // store: defines a method; call: callee is receiver + Name (v1.m())
	AuxOptional                             // call/load: written with ?. (the short-circuit is explicit control flow)
	AuxTagged                               // call: a tagged template; the first argument is the strings const
	AuxSuperCall                            // call: super(…) in a derived constructor
	AuxJSX                                  // call: a JSX element, %jsx(tag, props, children)
	AuxDecorator                            // call: applies a decorator; Name is the decorator's name, Parts its target
	AuxRequire                              // call: require("x") with a literal specifier
	AuxImport                               // call: dynamic import(x)
	AuxEval                                 // call: direct eval(x)
	AuxDelegate                             // yield: yield*
	AuxClass                                // closure: a class constructor
	AuxDerived                              // closure: a class with extends
	AuxStatic                               // store: defines a static member
	AuxIIFE                                 // call: an immediately invoked function expression
	AuxWithThis                             // call: the second argument is this (call v1() this=v2)
)

var auxFlagNames = []string{
	"dynamic", "global", "cell", "super", "iter-value", "iter-key", "rest", "proto", "get", "set", "method",
	"optional", "tagged", "super-call", "jsx", "decorator", "require", "import", "eval", "delegate", "class",
	"derived", "static", "iife", "with-this",
}

// Lit returns a const's literal kind.
func (f AuxFlags) Lit() AuxFlags { return f & AuxLitMask }

// Has reports whether all of flag are set.
func (f AuxFlags) Has(flag AuxFlags) bool { return f&flag == flag }
