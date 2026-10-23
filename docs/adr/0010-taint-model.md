# 0010. Intraprocedural taint as sets of kinded, origin-carrying labels over SSA values

- **Status:** Proposed
- **Date:** 2026-10-23

## Context

v0.4 (milestone M1) reports the first vulnerabilities: command injection, path traversal and SQL injection where untrusted data reaches a dangerous call inside one function. The taint model decided here is what v0.5 (interprocedural, field-sensitive) and v0.6 (YAML models) extend, and what every finding's trace is built from, so its shape matters more than the three rules that first use it.

Forces:

- **Precision is the product.** A tool that cries wolf gets switched off. Each rule must reach 80% true positives on real, non-vulnerable-by-design code, or ship disabled.
- **Sanitizers are specific.** `encodeURIComponent` makes a value safe for a URL, not for SQL; `path.basename` makes it safe for a path, not for a shell. A single "tainted" bit can't say that.
- **Traces are mandatory.** Every finding explains itself step by step, from the source to the sink, so taint has to remember where it came from.
- **The IR is SSA for locals** ([ADR 0009](0009-patchd-ir.md)): most facts belong to values, not program points. Captured variables (cells), module variables (globals) and properties are memory.
- **Real code is often untyped.** DVNA and NodeGoat are plain JavaScript; their handlers' `req` is `any`, and without `@types/node` a `require("child_process").exec` call does not resolve. Typed code resolves cleanly through `tsbridge.QualifiedName`.
- **Unknown calls are everywhere.** Most calls in a function are to code no model describes.

Reading that informed this decision: CodeQL's JavaScript taint tracking (flow labels distinguish "tainted" from "partially sanitized"; sanitizer guards; additional taint steps through string methods), Semgrep's taint mode (sources, sinks, sanitizers and propagators as the user-facing vocabulary; propagation by default through unknown functions), and the Go `x/tools` SSA analyses (sparse propagation along def-use chains).

## Decision

### Taint kinds

A label has an **origin kind**, the kind of source it came from (`user-input`, or `environment` for `process.argv`/`process.env`), and a set of **sink kinds** it can still reach: `sql`, `command`, `path`, `url`, `html`, `code`. A source produces a label that can reach every sink kind. A sanitizer clears specific sink kinds (`path.basename` clears `path`); the label keeps the rest. A sink of kind `k` fires only for labels that can still reach `k`.

`environment` labels are reported at low severity and only when enabled (`--include-environment`), so CLI tools don't flood results.

### Lattice

Each SSA value maps to a set of labels. A label is `(origin, kinds, numeric)`: `origin` is the source occurrence (the instruction, its source location and the model entry that matched), `kinds` the reachable sink kinds, and `numeric` whether type pruning removed kinds on the way. The set holds at most one label per origin; join is set union, merging labels of the same origin by uniting their kinds. Bottom is the empty set. The lattice has finite height (origins and kinds are finite per function), so the solver terminates.

For traces, the analysis also records, for every value that gains a label, the operand it gained it from (its **predecessor**). A trace is the shortest predecessor chain from a sink argument back to a source.

### Sound where cheap, precise where it matters

- A call to something no model describes passes the union of its receiver's and arguments' taint to its result (`trim`, `slice`, `join`, `replace`, `path.join`, `path.resolve` and any local helper). It never makes taint from nothing.
- Operators that can't carry an injection produce untainted values: comparisons, `in`, `instanceof`, `typeof`, `!`, `void`, unary `-`/`+`/`~`.
- **Type pruning.** A value whose TypeScript type is `number`, `boolean`, `bigint` or a union of only those, and the results of `Number()`, `parseInt`, `parseFloat` and `Boolean()`, can't reach `sql`, `command`, `path` or `url` sinks. Their labels keep the origin (traces still pass through them) but lose those kinds and are marked numeric.
- `catchval` is untainted, and so is a `closure` value (its captured cells carry their own taint).

### Memory

- **Cells and module globals** inside one function: a load sees the stores that reach it (a forward reaching-stores analysis per function, keyed by the cell value or the global's name; a store to the same key kills earlier ones). At function entry, cells and globals are untainted: v0.4 doesn't follow taint across functions.
- **Properties are field-insensitive.** An object is tainted as a whole: `load o.p` and `load o[k]` get the object's taint (plus a tainted key's), and `store o.p = x` adds `x`'s taint to `o` (a weak update). One tainted property taints every other property of the object. This is a known source of false positives, fixed by field sensitivity in v0.5.
- Known mutators move taint into their receiver: `Array.prototype.push`, `unshift` and `splice` (a propagator from the arguments to the receiver).

### Reporting rule

A finding needs a concrete chain of values from a source to a dangerous argument of a sink, with a label that can still reach the sink's kind (no sanitizer on the chain cleared it, and no type pruning removed it). Rules may refine this: `ts/sqli` requires that the tainted part was concatenated or interpolated into the query text, `ts/cmdi` ignores the argument array of `spawn`/`execFile` without `shell: true`.

### Matching models to code

Models name code by `tsbridge` qualified names ([ADR 0008](0008-tsbridge-api.md)): `express:Request.query`, `node:child_process.exec`, `pg:Pool.query`. The analysis resolves an instruction to a name in this order, and stops at the first answer:

1. **Symbols.** `ResolveCall` for calls, `SymbolOf` + `QualifiedName` for property reads (`req.query` is `express:Request.query`).
2. **Receiver types.** A property read whose syntax has no member symbol (destructuring: `({ params }: Request) => …`) is named after its receiver's type, `TypeQualifiedName(receiver) + "." + name`. This is a new `tsbridge` method, documented in [docs/engine/tsbridge.md](../engine/tsbridge.md).
3. **Import provenance.** Values that come from an import or a literal `require` carry the module and path they came from: `const { execSync } = require("child_process")`, `const cp = require("child_process"); cp.exec(…)`, and module-level bindings initialised that way (`const exec = require("child_process").exec` at the top of a file, read inside a handler). Following a module-level `const` to its initialiser is name resolution, not dataflow: no taint crosses functions.
4. **Untyped Express handlers.** In a function whose first parameter is named `req` or `request` and typed `any` (or untyped JavaScript) and whose second is named `res` or `response`, the first parameter stands for an Express `Request` (its property reads and `req.get`/`req.header`/`req.param` calls are sources) and the second, when untyped too, for an Express `Response` (so `res.sendFile` is a sink). Findings whose source matched through this heuristic have medium confidence.

Typed handlers need nothing special: any read of `query`, `body`, `params`, … on a value typed `express.Request` matches, whether the handler is an inline arrow, a named function or a router method, without a call graph.

### Engine shape

- `engine/dataflow`: a generic `Lattice[T]`, a sparse solver that propagates facts along def-use chains to a fixed point (with a dense per-block fallback for memory), reaching stores, and a per-function iteration cap (50 passes by default) that stops with a warning instead of looping.
- `engine/taint`: the spec types (sources, sinks, sanitizers, propagators, matchers; designed to serialise to YAML in v0.6), the transfer function and the per-function analysis.
- `engine/models`: the built-in v0.4 models in Go.
- `engine/rules`: rules own their sinks, metadata and refinements and register themselves.

### Options that lost

- **One tainted bit.** Simple, but every sanitizer would either clear everything (false negatives: `encodeURIComponent` before a SQL query) or nothing (false positives: `path.basename` before `sendFile`).
- **Kinds without origins.** Smaller facts, but traces would have to be rebuilt by a second search, and deduplicating several sources into one finding needs origins anyway.
- **Dense per-program-point facts for every value.** What a textbook solver does; on SSA it repeats the same fact at every point after a definition. Sparse propagation touches each def-use edge a bounded number of times.
- **Requiring types for everything.** Precise and simple, but blind on plain JavaScript, which includes two of the three integration targets.
- **Following module-level bindings with interprocedural dataflow.** The v0.5 machinery; for `const exec = require(…).exec` name resolution is enough.

## Consequences

- Sanitizers and sinks compose by kind: a v0.6 XSS rule (`html`) and an SSRF rule (`url`) reuse the same propagation with new models.
- Field-insensitivity will cause false positives where one tainted field shares an object with safe ones (`{ id: req.params.id, table: "users" }`); each one found in triage becomes a negative fixture to flip when v0.5 adds field sensitivity.
- Validation through branches (`if (!allowed.includes(x)) return`, `if (!/^[a-z]+$/.test(x))`, `startsWith(base)` after `path.resolve`) doesn't sanitize: that needs path-sensitive analysis. These are documented as known false positives per rule.
- The untyped-handler heuristic can mistake a non-Express `req` for a request; it is limited to the `(req, res)` shape and marked medium confidence. Revisit if triage shows false positives from it.
- Revisit the model if v0.5's summaries need labels to carry more than one origin per value, or if traces need the sanitizer steps a value passed (today a cleared kind is simply gone).
