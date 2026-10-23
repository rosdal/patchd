# 0009. A high-level, SSA-for-locals IR with explicit exceptional edges

- **Status:** Accepted
- **Date:** 2026-10-23

## Context

v0.3 turns every function, method, arrow, class initializer and module body of a TypeScript or JavaScript project into a control-flow graph that dataflow and taint analysis (v0.4+) run on. The IR is the contract between the TypeScript-specific front end (`engine/ir`'s builder over `engine/tsbridge`) and every language-neutral layer after it; changing its shape later touches every analysis, so it is decided here.

Forces:

- **Traces must read like the code.** A finding says "`req.query.id` reaches `db.query` through a template literal on line 14". An IR that desugars templates into `+` chains, or method calls into `load` + `call`, makes traces harder to map back and loses facts rules match on (the static parts of a SQL template, the name of the method being called).
- **Dataflow wants SSA.** Reaching definitions, taint propagation and constant tracking are simple and fast when every local has one definition and merges are explicit `phi`s.
- **JavaScript scoping is not SSA-friendly.** Closures capture variables by reference, `var` is hoisted, `for (let …)` creates a binding per iteration, module variables are shared across functions, and properties alias freely.
- **Exceptions are control flow.** `try { await db.query(x) } catch (e) { res.send(e.message) }` is a taint path through a rejected promise; an IR that ignores exceptional edges loses it, and variables modified inside `try` must merge correctly into `catch` and `finally`.
- **Real repos contain everything.** `with`, decorators, JSX, CommonJS, syntax errors and constructs nobody planned for. Lowering must degrade, never crash, and say where it is blind.
- **Scale.** A 500k-line repo is millions of instructions; v0.6 caches IR per file hash, so the representation must be compact and pointer-free.
- **A second language (v2.0)** should reuse dataflow, taint and rules over the same IR.

Reading that informed this decision:

- **Go's `golang.org/x/tools/go/ssa`.** The closest prior art: a high-level SSA IR built for analysis, not compilation. Taken from it: SSA only for locals whose address is never taken; everything else (fields, globals, captured variables) is memory accessed through explicit loads and stores; closures are a `MakeClosure` instruction listing captured cells, and a function's free variables are explicit; every instruction keeps a source position; a function's blocks carry a comment (`if.then`, `for.body`) for readable dumps; blocks have a recover/handler concept rather than modelling every panic site.
- **Braun et al., "Simple and Efficient Construction of Static Single Assignment Form" (CC 2013).** Builds SSA directly while lowering the syntax tree in one pass: `writeVariable`/`readVariable` per block, operand-less phis in unsealed blocks completed when the block is sealed, and recursive trivial-phi removal. No dominance frontiers or liveness needed, which fits a builder that emits blocks in syntax order. This is the algorithm Step 10 implements.
- **Cytron et al. (1991).** Dominance-frontier phi placement, kept for comparison and because later passes (control dependence for sanitizer checks, loop detection) want the dominator tree and frontiers anyway. The dominator tree uses the Cooper–Harvey–Kennedy iterative algorithm, which is simpler than Lengauer–Tarjan and as fast on CFGs of the size functions produce.
- **CodeQL's JavaScript data-flow libraries (public docs).** They model flow through captured variables as flow through a shared "closure variable" node, through promises as content (`.then` callbacks receive the promise's resolved content, `await` reads it), and through properties as field contents (`req.body.user.name` is a path of content steps). The IR therefore must make captured variables explicit (cells), keep `await` as an instruction distinct from a call, keep property names on every load and store, and keep destructuring paths so field-sensitive taint can be added in v0.5 without re-lowering.

## Decision

### Level: high-level, close to source

The IR keeps what source code says: property names on `load`/`store` (`load v1.query`), call shapes (`call v6.query(v5) this=v6`, with the syntactic callee in `Aux`), template literals with static and dynamic parts kept apart (`template ["SELECT * FROM t WHERE id = ", v4]`), object literals with their keys, and the operator of every `binop`. It does not lower to bytecode, stack slots or a register machine.

### SSA for locals only; everything else is memory

- A local variable never referenced from another function is a plain SSA value: no instruction for `x = y`, `phi` at merges.
- A local referenced from another function (a closure, a class field initializer) is a **cell**: the declaring function creates it with a `cell` instruction where the binding comes into existence (once per iteration for `for (let …)` bindings) and reads and writes it with `load *v` / `store *v = x`. The inner function receives it through its `FreeVars` and the `closure` instruction that creates the function value.
- Module-level variables, imports and unresolved names (`window`, `process`) are **globals**, read and written with `load global x` / `store global x = v`.
- Properties and elements are memory: `load`/`store` with a static name, or a dynamic key as an argument.
- `this`, `arguments`, `new.target` and `super`'s home object never change within a function, so arrows and class initializers capture them by value as free variables.

### Exceptions as explicit edges

Every instruction that can throw (the "can throw" column of [docs/engine/ir-ops.md](../engine/ir-ops.md)) inside a `try` ends its block, and that block's `Handler` names the nearest handler: the `catch` block, or a synthetic `finally.throw` block when there is no `catch`. Handler blocks count those blocks as predecessors, so their phis merge each variable as it was at each throwing point. Outside a `try` an exception leaves the function and needs no edge. `finally` is lowered by duplicating its body for each way out of the `try` (normal exit, `return`, each `break`/`continue` target, exception); simplest correct, and the growth is measured in [docs/engine/ir.md](../engine/ir.md).

### Every value is typed and located

Every instruction records its syntax node (`tsbridge.NodeID`) and source `Range`; every value records its `tsbridge.TypeID` (from `TypeOf` on the node that produced it) and, when it is a source variable, the variable's name.

### A language-neutral core with a few TypeScript ops

The core (`const`, `assign`, `binop`, `unop`, `load`, `store`, `delete`, `call`, `new`, `object`, `array`, `closure`, `cell`, `phi`, `catchval`, terminators, `unknown`) means the same in any dynamic language. TypeScript-specific behaviour gets a small set of ops of its own (`template`, `spread`, `await`, `yield`) rather than being desugared, because taint needs to see them. Promise chains, `call`/`apply`/`bind`, JSX and decorators stay ordinary `call`s for v0.5's models to interpret.

### Never panic, never silently omit

Syntax the builder does not support lowers to `unknown <SyntaxKind>`, which analyses treat as "could be anything", and is counted per kind by the coverage report. A panic in the builder is recovered per function and turns that function into a single `unknown` block plus a warning.

### Representation

Values, instructions and blocks live in per-function slices and are referenced by small integer IDs (`ValueID`, `InstrID`, `BlockID`, `FuncID`), never pointers; instruction arguments are ranges into one per-function argument slice; property names and identifiers are interned. Each block ends in exactly one terminator (`jump`, `branch`, `return`, `throw`, `unreachable`). `ir.Validate` checks the invariants (one terminator per block, consistent edges, definitions dominate uses, one phi operand per predecessor, no cross-function values except free variables, ranges inside the function) and runs in every test.

### Options that lost

- **Low-level, bytecode-like IR** (V8 Ignition, Hermes IR). Precise semantics, but traces stop mapping cleanly to code and rules lose template parts and call shapes.
- **SSA for everything via memory SSA** (versioned heap). Dataflow over fields would be uniform, but construction is much harder, and v0.5's field-sensitive taint works on access paths, which `load`/`store` with names already give.
- **Implicit exceptions** (any instruction may jump to the handler, as in Go's `Recover` block). Fewer blocks, but variables modified inside `try` would merge wrongly into `catch`.
- **Desugaring `async`/generators into state machines.** What compilers do; it hides `await` from taint and multiplies blocks.
- **Dominance-frontier SSA construction (Cytron).** Needs a complete CFG and liveness first; Braun builds SSA in the same pass as lowering.

### Changes made during implementation

Recorded at acceptance, after lowering the 10 reference repos:

- **A `cell` instruction.** Captured variables need a cell created at a point in the CFG (a fresh one per iteration of a `for (let …)` loop), which a value kind can't express. The cell pointer itself is an SSA value, so per-iteration cells merge with ordinary phis.
- **`branch` without a condition** for `for…of`/`for…in` heads, where the iteration protocol decides; the element is `load v[of]` (the key `load v[in]`).
- **Dead code stays out of live dominance.** Code after `return`/`throw` is lowered into blocks marked dead; edges from them back into live code are ignored by the dominator tree, and phi operands arriving over them don't count.
- **Exports are a list, not a map**, so re-exports, `export *` and CommonJS exports keep their shape and order.
- **Destructuring iterates** every array-pattern source but an array literal (a `spread`), rather than asking for its type.

## Consequences

- Dataflow and taint (v0.4) see one definition per local, explicit merges, explicit captured state and explicit exceptional edges; they never need to know TypeScript syntax.
- Field-sensitive taint (v0.5) and framework models (v0.6) match on property names and call shapes the IR keeps.
- Duplicating `finally` bodies grows code; functions with large `finally` blocks and many exits pay most. Revisit with a shared `finally` and an exit-kind switch if growth shows up in the cache size.
- Ending a block at every can-throw instruction inside `try` multiplies blocks there. If this costs too much, a handler could instead receive one phi per variable from a block summary, at the price of a more complex SSA builder.
- New syntax (a future TypeScript release) never breaks lowering; it shows up as `unknown` in the coverage report until someone adds a case ([docs/engine/adding-syntax.md](../engine/adding-syntax.md)).
- A second language needs its own builder; analyses keep working as long as it maps onto the same ops.
