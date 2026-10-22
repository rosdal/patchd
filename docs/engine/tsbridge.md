# tsbridge

`engine/tsbridge` is the only package that touches the TypeScript compiler ([ADR 0005](../adr/0005-tsbridge-boundary.md)). It finds the projects in a repo, loads and type-checks them with typescript-go, and answers questions about their syntax, symbols, types and calls through a small Patchd-owned API ([ADR 0008](../adr/0008-tsbridge-api.md)). Every later engine layer (IR, dataflow, taint, call graph, rules) is built on it and never sees a compiler type.

typescript-go's internals are reached through generated shims ([ADR 0007](../adr/0007-tsbridge-shim.md), [`engine/tsbridge/shim/README.md`](../../engine/tsbridge/shim/README.md)); moving the pinned commit is [one command](tsgo-upgrades.md).

## Pipeline

1. **Discover** (`Discover`) walks the repo, skipping `node_modules`, VCS directories, build output (`dist`, `build`, `.next`, `out`, `coverage`, …) and everything `.gitignore` excludes. It collects `tsconfig.json`, `tsconfig.*.json` and `jsconfig.json`, detects workspace layouts (pnpm, npm and Yarn workspaces, Turborepo, Nx, Lerna), parses every config with typescript-go's own parser (following `extends`, including from npm packages, and `references`), and gives each source file to the **most specific** config that includes it: the deepest directory, then the config with fewer files. Configs that list `files` (Angular's `tsconfig.app.json`) also own the files their program reaches through imports. Base configs that only exist to be extended are not projects. Files no config covers go into a **synthesised project** per nearest `package.json`: `allowJs`, `checkJs: false`, `strict: false`, `target: es2024`, `module: nodenext`.
2. **Load** (`Load`) builds one program: the files the project owns plus every `.d.ts` its config includes, on a read-only file system (Patchd never writes into the analysed repo). It parses, binds and type-checks the root files in parallel and collects diagnostics without ever failing on type errors.
3. **LoadAll** runs projects in a worker pool (`Jobs`, default: CPU cores) inside a memory budget (`MaxMemory`, default 75% of available RAM; each project's cost is estimated from its source size), shares parsed `node_modules` declarations and lib files between programs, and closes each program after the caller's `visit` returns.

Options TypeScript 7 removed are translated so TypeScript 5-era configs keep working: `baseUrl` becomes `paths` relative to the same directory, and `moduleResolution: node`/`classic` resolve as `bundler` (TSB009). Without `types` in a config, every `@types` package is included, as TypeScript 5 did.

`patchd.yaml` can override discovery:

```yaml
typescript:
  projects: ["apps/web/tsconfig.json", "packages/api"] # use only these configs
  ignore: ["legacy/**", "**/*.generated.ts"] # gitignore-style patterns to skip
```

## API by example

```go
ws, err := tsbridge.Discover(ctx, repoRoot, tsbridge.DiscoverOptions{})
_, err = tsbridge.LoadAll(ctx, ws, tsbridge.LoadAllOptions{}, func(cfg tsbridge.ProjectConfig, p *tsbridge.Program, err error) error {
	if err != nil {
		return nil // the project failed to load (a *tsbridge.PanicError for TSB007); others continue
	}
	for _, id := range p.RootFiles() {
		f := p.File(id)
		p.Walk(id, func(n tsbridge.NodeID) bool {
			if !p.Kind(n).IsCallLike() {
				return true
			}
			callee, ok := p.ResolveCall(n)
			line, col := f.Position(p.Range(n).Pos) // 1-based line, UTF-16 column
			if ok {
				fmt.Printf("%s:%d:%d %s\n", f.Path, line, col, callee.QualifiedName) // express:Response.redirect
			} else {
				fmt.Printf("%s:%d:%d unresolved (%s)\n", f.Path, line, col, callee.Reason)
			}
			return true // false stops the walk
		})
	}
	return nil
})
```

The questions a program answers:

| Area                  | Methods                                                                                                                                         |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Files                 | `Files`, `RootFiles`, `File`, `FileByPath`, `Diagnostics`, `Warnings`, `Stats`, `Close`                                                         |
| Syntax                | `Root`, `Kind`, `Children`, `Parent`, `Range`, `Text`, `NodeAt`, `Walk`, `NodeCount`                                                            |
| Positions (on `File`) | `Position` (1-based, UTF-16: GitHub, SARIF), `LSPPosition` (0-based, UTF-16), `Offset`, `OffsetLSP`                                             |
| Symbols and types     | `SymbolOf`, `SymbolName`, `TypeOf`, `TypeString`, `TypeFlags`, `UnionMembers`, `IntersectionMembers`, `Properties`, `IsAssignable`, `Signature` |
| Resolution            | `ResolveCall`, `ResolveAlias`, `Declarations`, `QualifiedName`                                                                                  |

Handles (`FileID`, `NodeID`, `SymbolID`, `TypeID`) are small values valid only for their program. No method panics on bad input: it returns the zero value or `ok=false`. Methods are safe for concurrent use.

Positions are byte offsets. Lines end at `\n`, `\r\n` or a lone `\r`; columns count UTF-16 code units, a byte order mark is not a column, and a tab is one column. Ranges start at a node's first token, after its leading comments.

## QualifiedName

The stable name framework models match on (spec in [ADR 0008](../adr/0008-tsbridge-api.md#qualifiedname)):

| Kind          | Format                    | Examples                                                                                                         |
| ------------- | ------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| npm package   | `<package>:<export path>` | `express:Response.redirect`, `express:default`, `pg:Client.query`, `@prisma/client:PrismaClient.$queryRawUnsafe` |
| Node built-in | `node:<module>.<path>`    | `node:child_process.exec`, `node:fs.readFileSync`                                                                |
| Global        | `global:<path>`           | `global:JSON.parse`, `global:Date.now`, `global:console.log`, `global:encodeURIComponent`                        |
| Repo code     | `local:<file>#<path>`     | `local:src/db.ts#runQuery`, `local:src/db.ts#Repo.find`, `local:server.ts#(anonymous 150:32)`                    |

A method call is named after the receiver's type as the code sees it; a static or global receiver after its value.

## Warnings

tsbridge degrades instead of failing, and says where it is blind.

| Code     | Situation                                                                                | Behaviour                                                                                                                                                                 |
| -------- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TSB001` | `node_modules` missing, a package not installed, or `@types/node` missing for a built-in | Load anyway; imports from it are unknown; calls resolve with reason `package-not-installed`                                                                               |
| `TSB002` | A config is not valid JSON or can't be read                                              | Skip it; its files fall back to a synthesised project                                                                                                                     |
| `TSB003` | An `extends` target is not found                                                         | Use the config without the base                                                                                                                                           |
| `TSB004` | A file has syntax errors                                                                 | Keep the partial syntax tree; analyse what parsed                                                                                                                         |
| `TSB005` | A project has more files than `MaxFiles`                                                 | Analyse the first N by priority (`LoadOptions.Priority` first, then non-test source), report the rest                                                                     |
| `TSB006` | Loading or checking exceeds `Timeout`                                                    | Cancel; checking stops and types are still answered on demand. A timeout while parsing fails the project                                                                  |
| `TSB007` | A compiler panic                                                                         | Recovered at the tsbridge boundary: the project fails (`*PanicError`, with a stack in debug logs), other projects continue; in a query, the method returns its zero value |
| `TSB008` | `.vue`, `.svelte`, `.astro` files                                                        | Skipped with a note until framework support exists                                                                                                                        |
| `TSB009` | An option TypeScript 7 removed (`baseUrl`, `moduleResolution: node`)                     | Translated where the meaning is clear                                                                                                                                     |
| `TSB010` | Other problems the compiler reports in a config                                          | The config is used as far as it parses                                                                                                                                    |
| `TSB011` | Installed packages that ship no types                                                    | Their imports are `any`; calls resolve with reason `untyped-package`                                                                                                      |

Calls that don't resolve carry a reason: `any-receiver`, `dynamic-property`, `package-not-installed`, `untyped-package`, `unresolved-name`, `no-symbol`, `no-declaration`, `not-a-call`, `failed`. `patchd debug stats` counts them per repo, because they are where analysis is blind.

## Parallelism

Measured on the reference repos on a 16-thread laptop (Intel Core Ultra 7 255H, 32 GB, Windows 11), October 2026; each number is one run of `patchd debug stats`, and repeated runs differed by about 1%.

| Setting                                                                                | pnpm (519 projects) | vscode (165 projects, one of 10,659 files)                  |
| -------------------------------------------------------------------------------------- | ------------------- | ----------------------------------------------------------- |
| No shared parse cache, 16 jobs                                                         | 575 s, 4.5 GB heap  | –                                                           |
| Shared cache for lib and `node_modules` declarations, 16 jobs                          | 253 s, 3.1 GB       | 78 s, 12.9 GB                                               |
| Shared cache for every file                                                            | 221 s, 1.3 GB       | –                                                           |
| … plus cached `package.json`/config reads (module resolution re-reads them per import) | 51 s, 1.5 GB        | –                                                           |
| … with 4 jobs                                                                          | **42 s, 1.0 GB**    | –                                                           |
| 4 jobs, 1 / 4 / **8** / 16 checkers                                                    | 42 s at any count   | 72 s / 78 s / **52 s** / 54 s; 11.0 / 12.9 / 14.8 / 15.4 GB |

So the defaults are:

- **Jobs:** a quarter of the CPU cores. typescript-go already parses each program on all cores and checks it with several checkers; more programs at once only contend (16 jobs were slower than 4 on pnpm, juice-shop and create-t3-turbo).
- **Checkers:** 8 (at most half the cores) for projects of 2,000 or more files, else typescript-go's 4. Only very large projects gain from more, and each checker costs memory.
- **Shared parse cache and file reads:** on in `LoadAll`. Parsed files are immutable and bind once, as in typescript-go's language server.

Before these, imports into referenced projects resolved to their build output; resolving them to source (as editors do) raised pnpm's call resolution rate from 80% to 99%.

## Developer commands

`patchd debug` is hidden from the main help (`patchd debug --help` shows it). Every subcommand takes `--format json`, `--jobs`, `--max-memory`, `--timeout`, `--max-files`, `--no-check`, `--checkers`, `--no-shared-cache`, `--root` and `-v` (debug logs, including recovered compiler panics).

| Command                                  | Shows                                                                                                                          |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `patchd debug project [path]`            | Discovered projects, which config covers which files, uncovered files, warnings, time per phase (`--no-load` to discover only) |
| `patchd debug types <file>:<line>:<col>` | Node kind, symbol, qualified name, type, flags, declarations; `--batch positions.json` answers many (the oracle uses it)       |
| `patchd debug resolve <file>`            | Every call in the file with its qualified name, or why it didn't resolve                                                       |
| `patchd debug stats [path]`              | Files, nodes, call resolution rate by reason, type errors, warnings, timings, peak heap and RSS                                |

## Testing

- `go test ./engine/tsbridge/...` runs the fixture suites (part of `pnpm test`; CI also runs it under `-race`). Update golden files with `go test ./engine/tsbridge/... -update` and review the diff.
  - `testdata/workspaces/`: discovery, with `*.discover.golden.json`.
  - `testdata/positions/`: BOM, CRLF, tabs, emoji and CJK text (`-text` in `.gitattributes` keeps the bytes).
  - `testdata/types/`: `// ^? <type>` markers under expressions, as in the TypeScript playground.
  - `testdata/resolve/`: `// resolves-to: <qualified name>`, `// unresolved: <reason>`, `// declarations: N` on call lines.
  - `testdata/broken/`: one fixture per warning code.
- `go test ./engine/tsbridge -run '^$' -fuzz FuzzLoad -fuzztime 1h` before a release: random configs and sources, no panic may escape.
- `pnpm oracle` compares tsbridge with TypeScript 6 on 500 seeded positions per reference repo and writes `.patchd/oracle/<repo>.md`. Known TypeScript 6 vs 7 differences are listed, with reasons, in [`tools/tsoracle/expected.json`](../../tools/tsoracle/expected.json). Target: at least 98% real matches per repo.
- `pnpm bench` measures discover, load, full check and 10,000 `TypeOf` calls per reference repo; CI compares pull requests with `main` and fails on +15% time or +20% memory.

## Adding an API method safely

1. Write it on `*Program` (or as a function) with Patchd types only: add a type to `api.go` if you need one. Never return or accept a typescript-go type.
2. Lock `p.mu`, `defer p.recoverQuery("<Name>")`, and return zero values for bad handles and closed programs.
3. Use the node's file's checker (`p.checkerFor`). If you need a new compiler function, add it to the shim (`extra-shim.json` for unexported ones, with a reason) and run `pnpm gen`.
4. Add fixture coverage, and a `patchd debug` output if a developer would want to see the answer.
5. Document it here and in ADR 0008's list in the same PR. Changing the meaning of an existing method or the `QualifiedName` format needs a new ADR.
