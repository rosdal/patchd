# 0007. Reach typescript-go internals through generated go:linkname shims

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

[ADR 0001](0001-go-on-typescript-go.md) puts the engine on the TypeScript 7 checker, and [ADR 0005](0005-tsbridge-boundary.md) confines it to `engine/tsbridge`. typescript-go keeps its compiler in `internal/` packages, which Go refuses to import from another module. The v0.1 spike had to answer one question before any engine code is written: can Go code load a real TypeScript project and query the checker in-process, and what does it cost to keep that working?

**How tsgolint does it** (studied at `oxc-project/tsgolint` main, October 2026):

- typescript-go is a git submodule, pinned at the TypeScript 7.0.2 commit, with a stack of 6 local patches applied by `git am`, used through `go.work`.
- Each internal package it needs gets a small module named `github.com/microsoft/typescript-go/shim/<pkg>`. Because that path sits under typescript-go's own module path, Go's `internal` rule allows it to import `github.com/microsoft/typescript-go/internal/<pkg>`.
- A generator (`tools/gen_shims`, built on `golang.org/x/tools/go/packages`) writes each `shim.go`: type aliases and constants for exported declarations, and `//go:linkname` declarations for functions and methods, including chosen unexported ones listed in `extra-shim.json`. Private struct fields are reached through generated mirror structs.
- The root `go.mod` maps every shim module to its directory with `replace`.
- Upgrades are a script: move the submodule, re-apply the patches, `go get` the new commit in every shim module, regenerate.

## Decision

Use the same shim mechanism, without the submodule and the patch stack:

- **Pin:** `github.com/microsoft/typescript-go` at `v0.0.0-20260708042240-2bd066d87f5b`, the `typescript/v7.0.2` tag (TypeScript 7.0.2), fetched from the Go module proxy. The pin lives in the root `go.mod` and in each shim module's `go.mod`.
- **Shims:** 11 modules under `engine/tsbridge/shim/` (`ast`, `bundled`, `checker`, `compiler`, `core`, `scanner`, `tsoptions`, `tspath`, `vfs`, `vfs/cachedvfs`, `vfs/osvfs`), each named `github.com/microsoft/typescript-go/shim/<pkg>` and wired up with `replace` in the root `go.mod`. `go.work` is removed; the `replace` directives do its job, and a workspace file makes commands inside the nested modules awkward.
- **Generator:** `engine/tsbridge/shim/gen`, adapted from tsgolint's (MIT, attributed in the file). `extra-shim.json` files start as tsgolint's, minus the private-field mirror of `Checker` (see the upgrade below). Unlike tsgolint's, ours gofmts its output and fails on any config entry or package it can't find, so generated `shim.go` files are committed exactly as generated and formatters leave them alone.
- **Guard:** `engine/tsbridge/shim_test.go` loads a fixture `tsconfig.json` through the shims, type-checks it and resolves a call. It runs in `pnpm test` and CI on Linux and macOS, so a broken shim fails the build, not v0.2 work.
- **No patches.** None of tsgolint's patches were needed to load, check and query projects. If we need one later, we vendor typescript-go then, not before.

Options that lost:

- **Submodule plus patch stack, as tsgolint does.** Lets us patch the compiler, but costs a recursive clone, a patch rebase on every upgrade, and `go.work` in every checkout. Not needed yet.
- **Depending on tsgolint's shim modules.** They aren't published as modules, they track tsgolint's needs and release schedule, and they would make an unrelated project our compiler gatekeeper.
- **`@typescript/typescript6` in a Node sidecar.** Remains the fallback (below). Slower, single-threaded, a second runtime, and a frozen TypeScript version.

## Measurements

Spike code (`cmd/spike-tsgo`, `engine/tsbridge/spike.go`) is on the `spike/tsgo` branch and not merged. For each project it loads the `tsconfig.json`, type-checks every project file (semantic diagnostics, in parallel), then visits every call expression and prints the callee symbol, the resolved declaration's file and line, and the type of the first argument.

Machine: Windows 11, Intel Core Ultra 7 255H (16 threads), 31.5 GB RAM, Go 1.27.0. Peak memory is the process's peak working set, sampled every 100 ms. Dependencies were installed with lifecycle scripts disabled.

| Project                                           | Files (project / program) | Calls (resolved)             | Wall      | Load / check / walk         | Peak memory  | Errors |
| ------------------------------------------------- | ------------------------- | ---------------------------- | --------- | --------------------------- | ------------ | ------ |
| OWASP NodeGoat `c5cb68a`, Express, plain JS       | 29 / 185                  | 2,837 (985, 35%)             | 0.6 s     | 0.33 s / – / 0.08 s         | 72 MB        | 0      |
| vercel/commerce `3761e52`, Next.js 15, as shipped | 65 / 938                  | 376 (298, 79%)               | 3.0 s     | 2.5 s / 0.36 s / 0.02 s     | 269 MB       | 114    |
| vercel/commerce, TS 7 module settings             | 65 / 938                  | 376 (374, 99.5%)             | 3.0 s     | 2.5 s / 0.30 s / 0.01 s     | 269 MB       | 1      |
| microsoft/vscode `2de56745`, `src/tsconfig.json`  | 10,659 / 11,514           | 1,241,877 (1,239,746, 99.8%) | 107–145 s | 15–23 s / 32–46 s / 59–76 s | 10.6–11.4 GB | 386    |
| microsoft/vscode, type-check only                 | 10,659 / 11,514           | –                            | 62 s      | 20 s / 41 s / –             | 8.8 GB       | 386    |

What the numbers say:

- **Loading and checking work on all three, with no panics, hangs or unsupported-config crashes.** Ranges are across two runs.
- **NodeGoat** is untyped JS (`checkJs` off), so there is nothing to check. The 65% of calls left unresolved are calls on `any`: the untyped mongodb 2.x API and callback parameters. Adding `@types/node` raised resolution only from 955 to 985 calls. For JavaScript, the engine's points-to analysis and framework models carry the weight, not the checker. In JS files, `require(...)` resolves to Node's `require` signature, so module identity has to come from the module resolver, not the call's declaration.
- **commerce's 114 errors** come from `baseUrl` plus `"moduleResolution": "node"`. TypeScript 7 removed both, and it reports no config diagnostic: imports like `lib/utils` just fail to resolve. Rewriting them to `"paths": { "*": ["./*"] }` and `"moduleResolution": "bundler"` leaves 1 error and 374 of 376 calls resolved. Many real repos still ship TS 5-era configs, so v0.2's `tsbridge` must detect removed options, translate them where the meaning is clear, and warn.
- **All 386 vscode errors** trace to Electron's type definitions, which vscode's install scripts download and the spike skipped: missing `Electron` namespace and `electron` module, implicit `any` in Electron callbacks, and `process.noAsar`. Not a checker problem.
- **vscode is the cost to watch.** Type-checking 10,659 files takes about 1 minute and 9 GB; the call walk adds 1–1.3 minutes, a large part of it formatting 1.24 million type strings and writing them out. Memory is the binding constraint: about 9 GB on the largest repo means the worker size and the per-review cache matter from v0.2 on. Both numbers come from one Windows machine. Linux and macOS runs are a v0.2 benchmark task.

**Accuracy:** 20 resolved calls were sampled across the three projects (7 NodeGoat, 7 commerce, 6 vscode): library methods, generic functions, overloads such as `Math.min`, re-exports such as Next's `unstable_cacheLife` imported as `cacheLife`, and calls into other files. Each was compared with VS Code's Go to Definition, which for TypeScript is the language service's `getDefinitionAtPosition`. That was called directly, on the callee identifier, with each project's own TypeScript (5.8.2 for NodeGoat and commerce, 6.0.3 for vscode), and the spike's declaration file and line had to be among the definitions it returned. For bundled lib files, whose line numbers differ between TypeScript versions, the declaration text had to match instead. **20 of 20 match.** For `require`, Go to Definition returns two declarations in `@types/node`, and the spike's is one of them.

## Upgrade cost

Moving the pin from 7.0.2 (`2bd066d8`, 2026-07-08) to the newest typescript-go commit (`89d5d5b`, 2026-08-20, six weeks later):

1. `go get` the new commit in each shim module, then regenerate. The regenerated shims differ by +112/−63 lines over 6 of 11 files: new kinds, renamed emit helpers, changed signatures.
2. **One break in the shims:** the generated mirror of the private `Checker` struct referenced two new unexported generic types (`nodeLinkStore`, `symbolArenaLinkStore`) and no longer compiled. We don't use those private fields, so the fix was deleting the `Checker` entry from `extra-shim.json`. Private-field mirrors are the most fragile part of the mechanism; we use none on `main`, and each one added later needs a reason in review.
3. **One break in our code:** `compiler.NewCompilerHost` gained a trailing `contentmapper.Project` parameter. One call site, passing `nil`.
4. Output on commerce and NodeGoat was byte-for-byte identical before and after.

Total: under an hour. The upgrade procedure, until it becomes a script:

1. Pick the target commit (a `typescript/v7.x.y` tag where possible).
2. In each module under `engine/tsbridge/shim/`: `go get github.com/microsoft/typescript-go@<commit>`.
3. In `engine/tsbridge/shim/gen`: `go run .`
4. In the root: `go get github.com/microsoft/typescript-go@<commit>`, then `go mod tidy`.
5. Fix compile errors inside `engine/tsbridge` only, then `pnpm test`. The engine test suite is the gate, as ADR 0001 requires.

## Fallback

If the shims become unworkable (linkname access is blocked, or upgrades cost more than about a day each):

1. Move to the TypeScript 7.1 public API when it ships. `tsbridge`'s Patchd-owned interface is designed to make that a one-package change.
2. Until then, run `@typescript/typescript6` (the TS 6 API) in a Node sidecar that `tsbridge` talks to over a pipe. That costs speed, a second runtime and TS 7 parity, but keeps the engine unchanged.

## Consequences

- **Go for v0.2.** `tsbridge` builds on these shims: project loading, syntax, symbols, types and call resolution all work in-process on real projects.
- v0.2 has to handle removed TS 5-era options (`baseUrl`, `moduleResolution: node`/`node10`), projects with missing or partial `node_modules`, and untyped JavaScript, where most calls resolve to `any`.
- The largest reference repo needs about 9 GB to type-check. Memory and time per reference repo go into CI tracking in v0.2.
- `go:linkname` access to another module's unexported functions works today, but Go has been tightening linkname rules for the standard library since 1.23. A Go release that extends them to other modules would force the fallback; the smoke test would catch it on the first CI run with that Go version.
- typescript-go is a 55 MB module download, cached by `actions/setup-go` in CI.
