# Taint analysis

`engine/taint` follows untrusted data from where it enters (sources) to where it is dangerous (sinks) inside one function, and records the path so every finding can explain itself. It runs on the [Patchd IR](ir.md) with the [dataflow framework](../../engine/dataflow) and is matched to code through [tsbridge](tsbridge.md) qualified names. The design is [ADR 0010](../adr/0010-taint-model.md); the rules that report its results are in [rules/](rules/).

v0.4 is **intraprocedural**: a source and a sink must be in the same function. A value that crosses a function call, a callback or a file is v0.5's job.

## Pipeline

```text
tsbridge.Discover → LoadAll → ir.BuildProgram → taint per function → rules → findings
```

`engine.Analyze` runs it; `patchd scan` is the command ([docs/cli/scan.md](../cli/scan.md)). Functions are independent, so they are analysed in parallel, and a function with no possible source (no read of a source property, no call to a source method) is skipped: about 7% of functions on the reference repos are analysed at all.

## Kinds and labels

A label is one strand of taint on a value: the source occurrence it came from (its **origin**), the **sink kinds** it can still reach (`sql`, `command`, `path`, `url`, `html`, `code`) and whether type pruning removed kinds on the way. A source produces a label that reaches every kind. A sanitizer clears specific kinds (`path.basename` clears `path` and nothing else). A sink of kind `k` reports a label that can still reach `k`.

Origins have a kind too: `user-input` (HTTP requests) or `environment` (`process.argv`, `process.env`). Environment sources are off unless `--include-environment` is passed, and their findings are low severity.

A value's fact is a set of labels, at most one per origin. Join is set union; bottom is the empty set.

## Propagation

| Op                                                            | v0.4                                                                                                                  |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `const`                                                       | Untainted                                                                                                             |
| `assign`, `phi`                                               | Union of the inputs                                                                                                   |
| `binop`                                                       | `+`: union of the operands. Everything else (comparisons, `in`, `instanceof`, arithmetic): untainted                  |
| `unop`                                                        | Untainted (`typeof`, `!`, `void`, `-`, `+`, `~`)                                                                      |
| `template`                                                    | Union of the expressions; the static parts stay apart for the rules                                                   |
| `load v.p` / `load v[k]`                                      | The object's taint (field-insensitive). A tainted key does not taint the result: it chooses among the object's values |
| `load` of a cell or module global                             | The values of the stores that reach it in the same function                                                           |
| `store v.p = x`                                               | `v` takes `x`'s taint (weak update)                                                                                   |
| `object`, `array`, `spread`, `await`, `unknown`               | Union of the inputs                                                                                                   |
| call to a source                                              | The result gets the source's label                                                                                    |
| call to a sanitizer                                           | Receiver and arguments flow to the result minus the cleared kinds                                                     |
| call to a propagator                                          | As the model says (`Array.push`: arguments → receiver)                                                                |
| `Number`, `parseInt`, `parseFloat`, `Boolean`, `Math.floor` … | Arguments flow to the result as numbers (see type pruning)                                                            |
| call to a function in the repo                                | Nothing: the flow crosses functions (v0.5)                                                                            |
| any other call or `new`                                       | Receiver and arguments → result (`trim`, `slice`, `join`, `path.join`, `JSON.parse`)                                  |
| `closure`, `cell`, `catchval`, `yield`, `delete`              | Untainted                                                                                                             |

**Type pruning.** A value whose TypeScript type is `number`, `boolean`, `bigint` or a union of only those (with `null`/`undefined`) can't reach `sql`, `command`, `path` or `url` sinks: `parseInt(req.query.id)` is safe in a query. The label keeps its origin, so traces still pass through, but loses those kinds and is marked numeric. A type annotation is not a check: `const id: number = req.body.id` is still a string at runtime, and since the IR types values by their expression, the annotation doesn't prune.

## Matching models to code

Models name code by qualified name (`express:Request.query`, `node:child_process.exec`, `pg:Pool.query`). Each call or property read is resolved, in order:

1. **Symbols:** `ResolveCall` for calls, `SymbolOf` + `QualifiedName` for reads.
2. **Receiver types:** `TypeQualifiedName(receiver) + "." + name`, for destructuring such as `({ params }: Request) => …`.
3. **Import provenance:** `require("child_process")`, what is read from it, `new` of what it exports, and module-level bindings initialised that way (`const exec = require("child_process").exec` read inside a handler).
4. **Untyped handlers:** in a function whose first parameter is `req`/`request` typed `any` (plain JavaScript) and whose second is `res`/`response`, the first stands for an Express `Request` and the second for a `Response`. Findings through this heuristic have medium confidence.

The v0.4 models are in [`engine/models/builtin_v04.go`](../../engine/models/builtin_v04.go): Express request properties and `req.get`/`header`/`param`; raw `http` `IncomingMessage.url` and `.headers`; `child_process`, `fs`, `fs/promises`, `res.sendFile`, `res.download` and `pg` `query` as sinks; `path.basename` as a sanitizer; `Array.push`/`unshift`/`splice` as propagators. They move to YAML in v0.6, and the spec types (`engine/taint/spec.go`) already serialise to it.

## Traces

For every candidate, the analysis finds the shortest chain of values from the origin to the sink argument along edges that carry the origin's label. The finding's trace turns it into readable steps: a chain of property reads becomes one step (`read req.query.id`), copies and phis disappear, repeated steps collapse, and each step gets a note and its source line.

## Known limitations

These are by design in v0.4 and each is tracked to the version that lifts it.

- **No flow across functions** (v0.5). A source in a route handler and a sink in a helper it calls, a value returned by a local helper, a value captured by a callback: all missed. Calls to functions in the repo pass no taint at all, so a helper that validates doesn't cause a false positive either.
- **Field-insensitive** (v0.5). An object is tainted as a whole: `{ id: req.params.id, table: "users" }.table` is tainted.
- **No path sensitivity** (v0.5). A check in a branch doesn't sanitize: `if (!allowed.includes(x)) return`, `if (!/^[a-z]+$/.test(x)) return`, `startsWith(base)` after `path.resolve`, and an allowlist ternary `list.includes(x) ? x : "default"` are all reported.
- **Exceptions don't carry taint.** `catchval` is untainted, so `try { … } catch (e) { exec(e.message) }` is missed.
- **Few models.** Express, raw `http`, Node core and `pg` only: Sequelize, Knex, Prisma, MongoDB, `res.render` and friends arrive in v0.6.
- **URL normalisation isn't modelled.** `path.join(root, new URL(req.url).pathname)` is reported even though the URL parser removed dot segments; treating `pathname` as a sanitizer would miss `decodeURIComponent(url.pathname)`.

## Testing

- `engine/taint/intra_test.go`: one case per row of the propagation table, on small snippets with a test spec.
- `engine/fixtures_test.go`: the fixture suite under `engine/testdata/fixtures/<rule>/{positive,negative}`, annotated with `// expect: <rule> [from=<line>]` and `// expect-none`; a finding on an unannotated line fails. 178 positive and 111 negative cases at v0.4, with a positive case for every built-in source, sink, sanitizer and propagator. Install its types with `pnpm fixtures:install`.
- `pnpm eval:targets` and `pnpm eval:reference`: the vulnerable targets and the reference repos, scored against `eval/targets/expected/` and `eval/triage/v0.4.yaml` ([reports](../../eval/reports)).
