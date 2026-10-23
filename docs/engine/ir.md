# Patchd IR

`engine/ir` turns every function, method, arrow, class initializer and module body of a TypeScript or JavaScript project into a typed control-flow graph in SSA form: the Patchd IR, which dataflow and taint analysis (v0.4+) run on. It sits on [`engine/tsbridge`](tsbridge.md) and never sees a compiler type. The design is [ADR 0009](../adr/0009-patchd-ir.md); every instruction is in the [op reference](ir-ops.md); adding syntax is [a recipe](adding-syntax.md).

## Design in one page

- **High level, close to source.** Property names stay on loads and stores, method calls keep their shape (`call v1.query(v2) this=v1`), template literals keep their static parts apart from their values, object literals keep their keys. A trace reads like the code.
- **SSA for locals only.** A local no other function refers to is a plain SSA value with `phi`s at merges. A local captured by another function is a **cell**, read and written with `load *c` / `store *c`. Module-level variables, imports and undeclared names are **globals** (`load global db`). Properties are memory.
- **Explicit exceptional edges.** Inside a `try`, every instruction that can throw ends its block, with an edge to the handler, so the handler's phis see each variable as it was at each throwing point.
- **Typed and located.** Every value carries a `tsbridge.TypeID`, every instruction its syntax node and source range.
- **Never panics.** Unsupported syntax becomes `unknown <Kind>`, counted by the coverage report; a function whose lowering panics becomes one `unknown` block and a warning.

## Using it

```go
mods := ir.BuildProgram(ctx, prog, ir.BuildOptions{})     // every root source file, in parallel
m, warnings := ir.BuildModule(prog, fileID)               // one file
for _, f := range m.Funcs {                               // m.Funcs[0] is m.Init, the module body
	for b := 1; b < len(f.Blocks); b++ {
		for _, id := range f.Blocks[b].Instrs {
			in := f.Instr(id)
			if in.Op == ir.OpCall && in.Aux.Flags.Has(ir.AuxMethod) && in.Aux.Name.String() == "query" {
				recv := f.Value(f.Args(in)[0])
				fmt.Println(prog.TypeString(recv.Type), in.Source) // pg.Pool …
			}
		}
	}
}
err := ir.ValidateModule(m) // the invariants; nil for anything the builder produces
ir.Print(os.Stdout, m, ir.PrintOptions{Types: true, Prog: prog})
```

From the terminal:

```sh
patchd debug ir src/routes/user.ts                 # the whole file: imports, exports, every function
patchd debug ir src/routes/user.ts:handler --types # one function, with TypeScript types
patchd debug ir src/routes/user.ts:handler --source --names
patchd debug ir src/routes/user.ts:f3 --dot | dot -Tsvg > cfg.svg
patchd debug ir-coverage .patchd/targets/vscode --verify   # share lowered, top unsupported kinds, verifier
```

## Data model

`engine/ir/ir.go`. Everything is referenced by small integer IDs into per-function slices; ID 0 is "none" and index 0 of each slice is a placeholder, so an ID is its own index.

| Type     | Holds                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `Module` | One file: `Init` (the module body, also `Funcs[0]`), `Funcs` (every function in source order, `Funcs[i].ID == i+1`), `Imports`, `Exports`, `Warnings`, `Coverage`, `Stats`.                                                                                                                                                                                                                                                                                        |
| `Func`   | `Name` (`handler`, `UserService.find`, `outer.inner`, `<anon@12:5>`, `<module>`), `Kind` (function, arrow, method, getter, setter, constructor, classinit, staticinit, module, namespace), `Flags` (async, generator, static, rest, implicit, derived, abstract), `Params`, `This` (none for arrows), `FreeVars`, `Parent`, `Type` (the function's type, e.g. `(req: Request) => Promise<void>`), `Source`, `Syntax`, and the `Blocks`, `Instrs`, `Values` slices. |
| `Block`  | `Instrs`, `Preds` (normal and exceptional), `Succs` (normal, in terminator order), `Handler` (the exceptional successor), `Comment` (`if.then`, `for.body`, `catch`…), `Dead` (dead code, lowered anyway).                                                                                                                                                                                                                                                         |
| `Instr`  | `Op`, `Block`, `Result`, `Aux` (`Name`, `Flags`, `Func`, parts), `Source`, `Syntax`; arguments through `Func.Args`.                                                                                                                                                                                                                                                                                                                                                |
| `Value`  | `Kind` (instr, param, this, arguments, new.target, self, free, global, intrinsic, undefined), `Type`, `Def`, `Name` (the source variable), `Syntax`.                                                                                                                                                                                                                                                                                                               |

Types come from the syntax a value stands for: an expression's type, a parameter's or variable's declared type, `await`'s awaited type. Values the builder makes up without syntax of their own (the `== undefined` of a `?.`, cells, iteration reads without a declaration, `undefined`) have type 0.

Accessors: `Func.Block/Instr/Value`, `Func.Args/Parts`, `Func.Terminator`, `Func.Uses()` (built on demand), `Func.PathOf(v)` (destructuring paths), `Func.Global(name)`, `Module.Func(id)`, `Module.Export(name)`. `Dominators(f)` gives the dominator tree (`Idom`, `Dominates`, `Children`, `Frontier`, `RPO`, `Live`).

Arguments, parts, block instruction lists and edges live in shared per-function slices, so a function is a handful of allocations however many instructions it has. Names are interned (`ir.Name`, Go's `unique`). On the reference repos the IR takes 12–20 bytes per byte of source (`Module.Bytes`).

## How the builder works

`engine/ir/builder.go`, `scan.go`, `ssa.go`, `lower_*.go`. Per file:

1. **Scan** (`scan.go`): one walk over the syntax (tsbridge's lock-free `Tree`) creates a `Func` for every function-like construct — function declarations and expressions, arrows, methods, accessors, constructors (implicit ones too), a `ClassInit` per class with instance fields, a `StaticInit` per class with static fields or static blocks, a function per namespace body, and the module body — and records which function and block scope each declaration and identifier is in.
2. **Resolve**: all identifiers go to `tsbridge.ValueSymbols` in one batch (one lock on the program per file). A declaration's symbol becomes a variable; a reference to a variable from another function marks it captured and adds it to the free variables of every function in between.
3. **Lower** each function: entry values, the function scope's cells, parameters, then the body, building SSA as it goes. Lowering can be done in any order because the scan already knows every closure's free variables.
4. **Finish** each function: seal all blocks, ignore phi operands from dead code, remove trivial phis, renumber blocks in emission order and values densely, lay slices out contiguously.
5. **Type** every value in one `tsbridge.TypesOf` batch, once per syntax node.

`BuildProgram` lowers a program's files on `Jobs` goroutines (default: one per CPU). `BenchmarkLower` (one project at a time, after tsbridge has loaded it; 16-thread laptop) measures:

| Repo       |     Lines | Lowering | Per 1,000 lines | IR kept | IR per source byte |
| ---------- | --------: | -------: | --------------: | ------: | -----------------: |
| pnpm       |   445,149 |    3.3 s |          7.4 ms |  236 MB |             13.1 B |
| vscode     | 4,933,832 |   54.7 s |         11.1 ms |  3.0 GB |             12.2 B |
| zod        |   103,619 |    0.8 s |          7.4 ms |   61 MB |             15.3 B |
| juice-shop |    65,541 |    0.4 s |          5.5 ms |   44 MB |             15.6 B |

About half of lowering is the checker answering type queries. The v0.3 budget for a 500k-line repo was under a minute, under 2 GB and under 50 bytes per source byte.

## How TypeScript lowers

Printed with `patchd debug ir` (types omitted). The [golden tests](../../engine/ir/testdata) have a case for every construct.

### Variables and control flow

```ts
function f(c: boolean) {
  let x = 0;
  if (c) x = 1;
  return x;
}
```

```text
b1.entry:
  v1 = param c
  v3 = const 0
  branch v1 b2.if.then b3.if.done
b2.if.then:  ; preds b1
  v4 = const 1
  jump b3.if.done
b3.if.done:  ; preds b1 b2
  v5 = phi [b1: v3, b2: v4]
  return v5
```

- `var` is function-scoped and hoisted; `let`/`const` are block-scoped; a `let` without an initializer is set to `undefined` where it is declared, so a binding inside a loop does not carry the last iteration's value. Function declarations become closures where their scope is entered.
- Loops: `while.head/body/done`, `do.body/test/done`, `for.head/body/update/done`, `forof.head/body/done`. A `for…of` or `for…in` head branches without a condition (`branch * b3 b4`); the body reads the next element with `load v[of]` (the key with `load v[in]`), awaited for `for await`.
- `break`/`continue` (labelled or not) jump to their target, running the `finally` bodies they leave. `switch` tests the cases in order with `===` and lays the bodies out in source order so they fall through; `default` is where the last failed test goes.
- Code after `return`, `throw` or `break` is still lowered, into blocks marked unreachable.

### Short-circuits and optional chains

`&&`, `||`, `??`, `?:`, `??=`/`||=`/`&&=`, default values and `?.` are control flow with a phi, never a `binop`, so dataflow sees both paths. A whole optional chain shares one short-circuit block:

```ts
const name = req?.body.user.name;
```

```text
  v3 = binop == v1 undefined
  branch v3 b3.opt.short b2.opt.cont
b2.opt.cont:  ; preds b1
  v4 = load v1.body
  v5 = load v4.user
  v6 = load v5.name
  jump b4.opt.done
b3.opt.short:  ; preds b1
  jump b4.opt.done
b4.opt.done:  ; preds b2 b3
  v7 = phi [b2: v6, b3: undefined]
```

### Templates and calls

```ts
export async function handler(req, res) {
  const rows = await db.query(`SELECT * FROM users WHERE id = ${req.query.id}`);
  res.json(rows);
}
```

```text
func f2 handler(req, res) function async in f1
b1.entry:
  v1 = param req
  v2 = param res
  v4 = load global db
  v5 = load v1.query
  v6 = load v5.id
  v7 = template ["SELECT * FROM users WHERE id = ", v6]
  v8 = call v4.query(v7) this=v4
  v9 = await v8
  v10 = call v2.json(v9) this=v2
  return undefined
```

Tagged templates are calls of the tag with a strings `const` first (`call v3.sql(v4, v1) this=v3 ; tagged`); JSX is `call %jsx(tag, props, children)`; `new`, `super(…)`, `super.m()`, `import()` and `require("x")` have their own shapes ([reference](ir-ops.md#calls-and-construction)).

### Closures

A variable captured by another function lives in a cell; the declaring function creates it and passes it in the closure instruction, and the inner function reads it through its free variable:

```ts
function counter() {
  let n = 0;
  return () => n++;
}
```

```text
func f2 counter() function in f1
b1.entry:
  v2 = cell n
  v3 = const 0
  store *v2 = v3
  v4 = closure f3 [v2]                    ; counter.<anon@3:10>
  return v4

func f3 counter.<anon@3:10>() arrow in f2
b1.entry:
  v1 = free n (cell)
  v2 = load *v1
  v3 = const 1
  v4 = binop + v2 v3
  store *v1 = v4
  return v2
```

- Variables no other function refers to never become cells. A captured parameter is copied into a cell at entry.
- `for (let i …)` gets a fresh cell per iteration (copied before the update), so `() => i` in the body keeps that iteration's value; a `var` loop variable shares one cell.
- Arrows have no `This`: they capture the enclosing function's `this` (and `arguments`, `new.target`) as free variables. A named function expression sees itself as a `self` value.
- Immediately invoked functions are a `closure` and a `call` flagged `iife`; nothing is inlined.

### Exceptions

Inside a `try`, every can-throw instruction ends its block with an exceptional edge to the handler:

```ts
let stage = "start";
try {
  stage = "read";
  read();
  stage = "parse";
  parse();
} catch {
  report(stage);
}
```

```text
b2.try:  ; preds b1
  v3 = const "read"
  v4 = load global read                   ; exceptional → b7.catch
  jump b3.try
b3.try:  ; preds b2
  v5 = call v4()                          ; exceptional → b7.catch
  …
b7.catch:  ; preds b2 b3 b4 b5
  v10 = phi [b2: v3, b3: v3, b4: v6, b5: v6]
  v11 = catchval
```

The `catch` block starts with `catchval`. Without a `catch`, exceptions go to a `finally.throw` block that runs the `finally` body and rethrows. `finally` is lowered by duplicating its body on every way out of the `try` — normal exit, `return`, each `break`/`continue` target, exception — which is simple and correct; on pnpm and vscode the extra copies add 0.4–0.5% of all instructions (`patchd debug ir-coverage` prints the figure). Outside a `try` an exception leaves the function and needs no edge.

### Objects and classes

Object literals keep their keys: `object {a: v1, [v2]: v3, ...v4, get b: v5, m(): v6, [[Prototype]]: v7}`. A class is its constructor's closure plus stores that attach its members, where the class is defined:

```ts
class Dog extends Animal {
  constructor(
    name: string,
    public breed: string,
  ) {
    super(name);
  }
  speak() {
    return super.speak() + " woof";
  }
}
```

```text
  v6 = closure f4 []                      ; Dog.constructor class derived
  store v6.__proto__ = v5                 ; proto
  store global Dog = v6
  v7 = load v6.prototype
  v8 = load v5.prototype
  store v7.__proto__ = v8                 ; proto
  v9 = closure f5 []                      ; Dog.speak
  store v7.speak = v9                     ; method

func f4 Dog.constructor(name, breed) constructor derived in f1
b1.entry:
  v1 = param name
  v2 = param breed
  v3 = this
  v4 = call super(v1) this=v3
  store v3.breed = v2
  return undefined
```

Instance field initializers (`#private` ones too) run in `ClassInit`, which the constructor calls at entry in a base class and right after `super()` in a derived one, after parameter properties. Static fields and static blocks run in `StaticInit`, called with the class as `this` where the class is defined. Decorators are calls applied in TypeScript's order, named after the decorator and the target (`decorator @Body param create#0`). Enums are objects with a store per member (and the reverse mapping for numeric ones); namespaces are a function called with the namespace object, whose exported members are stores on it.

### Destructuring

Destructuring is a sequence of loads with a default branch per default value; each extracted value records its path:

```ts
const {
  body: {
    user: {
      name,
      roles: [primary],
    },
  },
} = req;
```

```text
  v4 = load v1.body                       ; path v1.body
  v5 = load v4.user                       ; path v1.body.user
  v6 = load v5.name                       ; name, path v1.body.user.name
  v7 = load v5.roles                      ; path v1.body.user.roles
  v8 = spread v7
  v9 = load v8[0]                         ; primary, path v1.body.user.roles[0]
```

Array patterns iterate the value (`spread`, skipped for an array literal) and load by index; rest elements are `load v[i:]`, object rest is `object {...v1, -id, -secret}`. The same lowering serves declarations, assignments (`[a, b] = [b, a]`), parameters, `catch` clauses and `for…of` heads.

### Async and generators

`async` and generator functions are flagged; `await` and `yield` are instructions (`await` typed with the awaited type, and able to throw), not state machines. Promise chains are ordinary method calls with closure arguments.

### Modules

Imports are recorded in `Module.Imports` (specifier, imported name, local binding, kind, type-only, and the file or package tsbridge resolved it to) and their bindings are globals read where used. Exports are recorded in `Module.Exports` with the exported value where it is known. `require("x")` with a literal is an import too, and `module.exports = …`, `module.exports.x = …` and `exports.x = …` are exports; `module.exports = { a, b }` also exports `a` and `b`. A CommonJS module's `module.exports` is its `default` export, which is what `import x from "cjs"` reads.

```text
module require-basic.js
  import default as util from "./util" (require) → ./util.js
  export run = v11 (commonjs)
```

## Validation and coverage

`ir.Validate` checks every function's invariants (one terminator per block, consistent edges, definitions dominate uses, one phi operand per predecessor, no values from other functions, ranges inside the function); `ValidateModule` also checks closures against their functions' free variables. It runs in every IR test and, with `--verify`, on every function of the reference repos in the nightly `ir-coverage` workflow.

The coverage report counts syntax nodes with runtime meaning (no types, type-only declarations, ambient code, JSDoc, overload signatures) as lowered, unknown, or never reached (a builder bug: syntax silently dropped). The nightly workflow fails below 98% on any reference repo. At v0.3.0 all 10 reference repos are at 100% and every function validates.

## Deviations from the v0.3 plan

- `ir.BuildProgram(ctx, program, opts)` takes a context and options; `Module.Exports` is a slice of `Export` (ordered, and able to describe re-exports) rather than a map.
- Two additions to the instruction set: `cell`, which creates a captured variable's cell (a fresh one per loop iteration needs an instruction), and `branch` without a condition for iteration.
- Argument lists are ranges into one per-function slice rather than inline arrays: no per-instruction allocation either way, and no size limit.
- Array destructuring iterates every value that is not an array literal rather than consulting its type.
- A class decorator's return value does not replace the class binding.
