# 0008. A small, handle-based tsbridge API with stable qualified names

- **Status:** Proposed
- **Date:** 2026-10-22

## Context

[ADR 0005](0005-tsbridge-boundary.md) makes `engine/tsbridge` the only package that touches typescript-go and asks it for a small Patchd-owned API. Every later layer builds on that API: the IR (v0.3) walks syntax and asks for types, taint analysis (v0.4) asks thousands of questions per file, call graphs (v0.5) resolve calls, and framework models (v0.6) match on names like "`express.Response.redirect` is a sink". Whatever crosses the boundary now is hard to change later.

Forces:

- Compiler types (`*ast.Node`, `*checker.Type`) must not leak, or the TypeScript 7.1 public API migration touches the whole engine.
- Later layers store node, symbol and type references by the million, in maps; they need cheap values, not pointers to compiler objects.
- typescript-go type-checks a program with several checkers in parallel, and types from two checkers can't be mixed.
- Real repos are messy. A query on bad input must not take down a review.
- Framework models need a name for "the `redirect` method of Express's `Response`" that survives refactors, re-exports and `.d.ts` reshuffles between package versions.

## Decision

### Shape

The API lives in `engine/tsbridge/api.go` (types) and the methods on `*Program`. Implementations are unexported.

```go
func Discover(ctx, root string, opts DiscoverOptions) (*Workspace, error) // projects, no type-checking
func Load(ctx, cfg ProjectConfig, opts LoadOptions) (*Program, error)     // parse, bind, check one project
func LoadAll(ctx, ws *Workspace, opts LoadAllOptions, visit func(ProjectConfig, *Program, error) error) (LoadAllStats, error)

// Program: files and results
Files() []FileID; RootFiles() []FileID; File(FileID) *File; FileByPath(string) *File
Diagnostics() []Diagnostic; Warnings() []Warning; Stats() Stats; Config() ProjectConfig; Close()

// Syntax
Root(FileID) NodeID; Kind(NodeID) Kind; Children(NodeID) []NodeID; Parent(NodeID) NodeID
Range(NodeID) Range; Text(NodeID) string; NodeAt(FileID, Pos) NodeID; Walk(FileID, func(NodeID) bool)

// Symbols and types
SymbolOf(NodeID) (SymbolID, bool); SymbolName(SymbolID) string; TypeOf(NodeID) TypeID
TypeString(TypeID) string; TypeFlags(TypeID) TypeFlags; UnionMembers(TypeID) []TypeID
IntersectionMembers(TypeID) []TypeID; Properties(TypeID) []SymbolID
IsAssignable(from, to TypeID) bool; Signature(call NodeID) (Signature, bool)

// Resolution
Declarations(SymbolID) []Location; ResolveCall(NodeID) (Callee, bool)
ResolveAlias(SymbolID) SymbolID; QualifiedName(SymbolID) string

// Positions, on *File
Position(Pos) (line, column int)        // 1-based, UTF-16 columns: GitHub, SARIF
LSPPosition(Pos) (line, character int)  // 0-based, UTF-16: LSP
Offset(line, column int) (Pos, bool); OffsetLSP(line, character int) (Pos, bool)
```

### Rules

1. **No method takes or returns a typescript-go type.** Not as a parameter, result, field or interface. Review rejects any exception.
2. **Handles are small values valid for one `Program`.** `FileID` and `SymbolID` and `TypeID` are `uint32` indexes, `NodeID` is a `uint64` (file in the high 32 bits, preorder index in the low). Zero is always invalid. Handles from one program mean nothing in another, and nothing after `Close`.
3. **No method panics on bad input.** It returns the zero value, `ok=false` or an error. Every method that calls into the compiler recovers panics at the boundary, records them as warning `TSB007`, and logs the stack at debug level.
4. **Context.** `Discover`, `Load` and `LoadAll` take a `context.Context` and stop when it is done. Query methods derive the program's context (cancelled by `Close`) rather than take one each, because they are called millions of times and never block.
5. **Degrade, never die.** Problems that leave something usable are `Warning`s with a code (`TSB001`–`TSB011`, listed in [docs/engine/tsbridge.md](../engine/tsbridge.md)), not errors.
6. **One checker per answer.** A query runs on the checker typescript-go assigned to the node's file: the one that already checked it. A `TypeID` remembers its checker; `IsAssignable` on types from two checkers returns false. In practice both sides of a comparison come from the same file.
7. **Concurrency.** `Program` methods are safe for concurrent use and serialised on the program; `LoadAll` gets parallelism across projects.

### Extending the API

A new engine need means a new method, never a compiler import outside `tsbridge`. A new method:

- follows the rules above, and returns Patchd types only (add a type to `api.go` if needed);
- gets fixture coverage under `engine/tsbridge/testdata/` and is used by a `patchd debug` command if it answers a question a developer would ask;
- is added to [docs/engine/tsbridge.md](../engine/tsbridge.md) in the same PR;
- never changes the meaning of an existing method or the `QualifiedName` format below; those changes need a new ADR.

### QualifiedName

`QualifiedName` is the string framework models (v0.6) match on, so its format is part of this decision:

```text
<package>:<export path>          npm package or Node built-in
local:<file>#<path>              code in the repo
global:<path>                    globals from lib.*.d.ts and global declarations
```

- **Package.** The npm package that declares the symbol, from the nearest `package.json` above the declaration file. Types packages are named after the package they type: `@types/express` is `express`, `@types/babel__core` is `@babel/core`. Types packages that only hold another package's internals take that package's name (`express-serve-static-core` is `express`); the list lives in `internalTypesPackages` in `resolve.go`. An ambient `declare module "m"` names package `m`.
- **Node built-ins** are `node:<module>.<path>`, whether declared as `"fs"` or `"node:fs"` in `@types/node`: `node:child_process.exec`, `node:fs.readFileSync`, `node:fs/promises.readFile`.
- **Export path.** The symbol's name, prefixed by its containers up to the module: `Client.query` for a method of exported class `Client`, `ns.fn` for a function in a namespace. Default exports are `default`. For a module written `export = e`, `e` is the module itself: `e`'s members are top-level (`express:Response`) and `e` is `default` (`express:default`).
- **Members reached through a receiver.** For a method call, the owner is the type of the receiver as the code sees it, not the interface that happens to declare the member. `res.redirect()` on an Express `Response` is `express:Response.redirect`, even though `@types/express-serve-static-core` declares `redirect`. When the receiver is itself a named static value (a class, enum, namespace, or a global variable such as `Date`, `JSON`, `console`), the value names the owner: `Date.now()` is `global:Date.now`, not `global:DateConstructor.now`.
- **Local code** is `local:<path relative to the workspace root>#<export path>`: `local:src/db.ts#runQuery`, `local:src/db.ts#Db.query`.
- **Globals** are `global:<path>`: `global:encodeURIComponent`, `global:JSON.parse`, `global:console.log`, `global:Array.map` (a method on an array value).
- Compiler-internal names (the object of `module.exports = {…}`) are not part of a path; an anonymous function or class is named by its position, `local:src/a.ts#(anonymous 12:5)`.

Options that lost:

- **Exposing compiler nodes behind interfaces.** Cheaper to write, but every interface method is a promise about compiler behaviour, and interfaces over pointers can't be map keys without keeping the compiler's objects alive.
- **One checker for every query.** Types would always be comparable, but every file not assigned to that checker would be type-checked twice, and most of a project's files aren't.
- **Declaration-based names only** (`express-serve-static-core:Response.redirect`). Stable but wrong for models: package internals decide the name, and they change between versions.
- **Fully resolved export paths through re-exports** (`pg:lib/client.Client.query`). Precise, but internal file layout leaks into names models match on.

## Consequences

- Later layers can store `NodeID`/`SymbolID`/`TypeID` in maps and slices for free, and v0.3's IR keeps a `NodeID` per value for source ranges.
- Moving to the TypeScript 7.1 public API changes `engine/tsbridge` only.
- Framework models match on `QualifiedName`. When a package moves a declaration between files or packages, the name only changes if the receiver type or export path does.
- Answers that need types from two files (comparing an argument in one file with a parameter type in another) must come from the call site's file. If a later layer needs cross-checker comparison, add a method that re-derives both types from one checker.
- `Program` serialises queries. If v0.4 taint analysis is bound by it, the fix is per-checker locking inside `tsbridge`, not a change to the API.
