# Adding syntax to the IR builder

When `patchd debug ir-coverage` lists a syntax kind as unknown or never reached, or a new TypeScript release adds syntax, this is how to lower it. Work test first: the golden file is the specification, and the verifier catches most mistakes before review does.

## 1. Find the gap

```sh
patchd debug ir-coverage .patchd/targets/<repo> --verify
```

The report lists the top unsupported kinds with example locations:

```text
top unsupported kinds:
  WithStatement      3 unknown      0 never reached   src/legacy.js:12:3, …
```

- **unknown**: the builder hit syntax it has no case for and emitted `unknown <Kind>` (and warning `IR001`).
- **never reached**: the builder handled a parent but dropped this child, so the syntax silently disappeared from the IR. Treat these as bugs first: they are usually a missing `fb.mark(n)` or a child the parent's case forgot to lower.

`patchd debug ir <file>:<function>` shows what the builder made of the example.

## 2. Write the golden case first

Add the smallest program that uses the syntax to the area it belongs to, `engine/ir/testdata/<area>/<case>.ts` (`.tsx`, `.js` as needed): `statements`, `expressions`, `functions`, `classes`, `destructuring`, `async`, `modules`, `commonjs`, `types` (printed with types), `regressions` (bugs found on real code), `realworld` (snippets from the reference repos, with their source and licence in a header). Cover the edge cases next to the plain one: empty bodies, nesting, the construct inside a `try`, inside a loop, inside a closure.

Run the golden test to see today's output:

```sh
go test ./engine/ir -run 'TestGolden/<area>/<case>' -update
```

## 3. Decide the lowering

Read [ir.md](ir.md) and [ir-ops.md](ir-ops.md) first and express the construct with the existing ops; [ADR 0009](../adr/0009-patchd-ir.md) explains why the set is small. Rules of thumb:

- Short-circuit behaviour becomes control flow and a phi (`fb.shortCircuit`, `fb.merge`), never an operator.
- Anything that reads or writes a variable goes through `readVar`/`writeVar` (or `readIdent`/`writeIdent`): they know whether the variable is SSA, a cell, a global or a namespace member.
- Keep what rules match on: property names, operators, call shapes, template parts.
- Syntax with no runtime effect (types, ambient declarations) belongs in `skipped` in `scan.go`, so lowering and coverage both ignore it.
- A new op is a last resort and needs an ADR amendment: every op is a case in every analysis.

If the construct declares variables or creates a function, the scan (`scan.go`) must see it too: declarations go through `scanner.declare` with the right scope, new function-like constructs get a `Func` from `builder.newFn`, and identifiers in new positions must be walked (or skipped, if they are names).

## 4. Lower it

Add the case to `lowerStmt` or `lowerExpr` (or the `lower_*.go` file of its area). Use `fb.elems(n)` for a node's runtime children, `fb.t.Role(c)` where children can't be told apart by kind, and `fb.mark(n)` for every node consumed without being passed to `lowerExpr`/`lowerStmt` (they mark what they lower). Emit with `fb.emitTyped` when the result is the value of an expression node (it gets that node's type), `fb.emit` otherwise. Inside a `try`, can-throw instructions end their block automatically; terminators go through `fb.terminate`, `fb.jump` or `fb.branch`, and blocks are sealed with `fb.seal` once all their predecessors exist.

## 5. Check

```sh
go test ./engine/ir -run TestGolden -update   # regenerate, then read the diff of every .ir.golden file
go test ./engine/ir                           # verifier on every function, unit tests
patchd debug ir-coverage .patchd/targets/<repo> --verify
```

Review the golden diff line by line: wrong IR that validates is still wrong. Then confirm the kind is gone from the coverage report and nothing became invalid, describe the lowering in [ir.md](ir.md) if it is a new kind of construct, and document any new flag or part in [ir-ops.md](ir-ops.md).
