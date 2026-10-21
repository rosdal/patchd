# Upgrading typescript-go

tsbridge is built against one typescript-go commit, recorded in [`engine/tsbridge/TSGO_VERSION`](../../engine/tsbridge/TSGO_VERSION) and in the `go.mod` of the root module and every shim module. The mechanism is in [ADR 0007](../adr/0007-tsbridge-shim.md); this page is the procedure.

## The one command

```bash
pnpm tsgo:update <commit|tag>            # e.g. pnpm tsgo:update typescript/v7.0.3
pnpm tsgo:update <commit> --npm typescript@7.0.3   # when the commit has no release tag
```

It runs steps 1–3 below and stops at the first failure. Prefer a `typescript/v7.x.y` tag: the oracle and the CHANGELOG line up with a release.

## Steps

1. **Move the pin.** In every module under `engine/tsbridge/shim/` and in the root: `go get github.com/microsoft/typescript-go@<version>` and `go mod tidy`. `TSGO_VERSION` gets the commit SHA, module version, tag and matching npm version.
2. **Regenerate** (`pnpm gen`): the shims (`engine/tsbridge/shim/*/shim.go`) and the syntax kind table (`engine/tsbridge/kind_gen.go`). Generated files are committed exactly as generated.
3. **Build and test**: `go build ./engine/...` and `go test ./engine/tsbridge/...`.
4. **Oracle and benchmarks** on all reference repos: `pnpm targets:fetch`, `pnpm oracle`, `pnpm bench`. CI runs both on the PR.
5. **Open the PR** titled `chore(engine): bump typescript-go to <version>`, with the before/after oracle match rates and benchmark table in the description.

## When something breaks

| Symptom                               | Where                        | What to do                                                                                                                                                                                 |
| ------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A shim doesn't compile                | `engine/tsbridge/shim/<pkg>` | Usually a private-field mirror or an unexported function listed in `extra-shim.json` that changed or disappeared. Drop or update the entry; every remaining entry needs a reason.          |
| The generator fails on a config entry | `extra-shim.json`            | The generator refuses entries it can't find. Remove or rename the entry.                                                                                                                   |
| `TestKindTableCoversCompiler` fails   | `kind_gen.go`                | The compiler gained syntax kinds. `pnpm gen` adds them as `KindOther` or a group kind; if v0.3 lowering needs the new syntax, give it a Patchd kind in `tools/scripts/gen-kinds.mjs`.      |
| tsbridge code doesn't compile         | `engine/tsbridge/*.go`       | A compiler signature changed (in the 7.0.2 → August 2026 trial, `compiler.NewCompilerHost` gained a parameter). Fix it there and only there: nothing outside `engine/tsbridge` may change. |
| `TestShimTypeChecksProject` fails     | `shim_test.go`               | Loading or checking through the shims is broken; don't merge. If `go:linkname` access is the cause, see the fallback in ADR 0007.                                                          |
| Oracle match rate drops               | `.patchd/oracle/<repo>.md`   | Classify the new mismatches. A TypeScript 7 behaviour change becomes an expected mismatch in `tools/tsoracle/expected.json`; anything else is a bug with an issue.                         |
| Benchmarks regress past the alarm     | `bench-results.json`         | Investigate before merging; the `perf-accepted` label with a reason overrides the alarm.                                                                                                   |

## Shims we add

To expose another compiler package, add it to `packagesToShim` in `engine/tsbridge/shim/gen/main.go`, create the module directory with a `go.mod` like its siblings, add a `require` and `replace` for it to the root `go.mod`, run `pnpm gen`, and list it with its reason in [`engine/tsbridge/shim/README.md`](../../engine/tsbridge/shim/README.md).
