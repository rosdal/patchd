# Patchd Code

Patchd reviews TypeScript and JavaScript pull requests. A dataflow engine built on typescript-go finds candidate bugs, agents confirm each one with evidence before it is posted, and patches are verified in a sandbox before Patchd suggests them. It is built for teams that want fewer, provable review comments, hosted in the EU or self-hosted.

**Status:** pre-alpha, v0.4 (milestone M1): first vulnerability findings. `patchd scan` finds command injection and SQL injection (and, opt-in, path traversal) where untrusted input reaches a dangerous call inside one function, and reports them in the terminal, as SARIF for GitHub code scanning, or as JSON, each with a step-by-step trace ([docs/cli/scan.md](docs/cli/scan.md), [docs/engine/taint.md](docs/engine/taint.md)). Underneath, `engine/tsbridge` loads and type-checks any TypeScript or JavaScript repo ([docs/engine/tsbridge.md](docs/engine/tsbridge.md)) and `engine/ir` lowers every function to a typed SSA control-flow graph ([docs/engine/ir.md](docs/engine/ir.md)). The review subcommands still return "not implemented yet".

## Try it

```sh
pnpm install && pnpm build
./bin/patchd scan path/to/your/app              # every rule on by default
./bin/patchd scan . --format sarif -o out.sarif # for GitHub code scanning
./bin/patchd rules list
```

On [DVNA](https://github.com/appsecco/dvna), a deliberately vulnerable Node app (`pnpm targets:fetch dvna`):

```text
$ ./bin/patchd scan .patchd/targets/dvna
core/appHandler.js

  CRITICAL ts/cmdi Command injection  39:2
  User input from `req.body.address` (line 39) reaches `exec` (line 39) without escaping.
    1. 39:22  read `req.body.address` (HTTP request body)
       │ exec('ping -c 2 ' + req.body.address, function (err, stdout, stderr) {
    2. 39:7  concatenated into a string
       │ exec('ping -c 2 ' + req.body.address, function (err, stdout, stderr) {
    3. 39:2  passed as argument 1 to `exec`
       │ exec('ping -c 2 ' + req.body.address, function (err, stdout, stderr) {

1 finding (1 critical) in 14 files, 300ms
```

## Prerequisites

| Tool    | Version                                                            |
| ------- | ------------------------------------------------------------------ |
| Git     | 2.40+                                                              |
| Node.js | the version in `.node-version` (24 LTS)                            |
| pnpm    | the version in `package.json` `packageManager` (`corepack enable`) |
| Go      | the version in `go.mod`                                            |

## Build and test

```sh
pnpm install
pnpm build          # bin/patchd plus the web app and VS Code extension
pnpm test           # go test ./... and the TypeScript tests
pnpm lint           # golangci-lint, oxlint, tsc, prettier --check
./bin/patchd version
```

Go tools (golangci-lint, gofumpt, buf and its Go plugins, sqlc, goose) are pinned in `tools/go.mod` and run through `go tool`, so there is nothing else to install.

pnpm is the only task runner, for Go as well as TypeScript (see [ADR 0003](docs/adr/0003-pnpm-task-runner.md)). `pnpm format` rewrites Go and TypeScript files; `pnpm fixtures:install` installs the types the engine's fixture suite needs (`pnpm test` runs it first); `pnpm eval:targets` and `pnpm eval:reference` score the scanner on the vulnerable targets and the reference repos (`pnpm targets:fetch` first). `pnpm gen` and `pnpm eval` are placeholders until code generation and the gold sets exist.

## Layout

| Path                                         | Contents                                                                     |
| -------------------------------------------- | ---------------------------------------------------------------------------- |
| `cmd/patchd`                                 | The single binary: server, workers, sandbox manager, local CLI, LSP, MCP     |
| `engine/`                                    | Analysis: tsbridge, IR, call graph, dataflow, taint, framework models, rules |
| `review/`, `agents/`, `trust/`               | Review pipeline, agent loop, filters that decide what gets posted            |
| `patches/`, `sandbox/`                       | Patch verification and delivery; isolated execution                          |
| `scm/`, `kb/`, `supplychain/`, `compliance/` | GitHub integration, code knowledge base, dependencies, CRA                   |
| `store/`, `jobs/`, `api/`                    | Postgres, River jobs, Connect API                                            |
| `web/`, `ide/vscode/`                        | Web app and VS Code extension                                                |
| `eval/`                                      | Gold sets and the scoring harness                                            |
| `internal/`                                  | Build info, git wrapper, telemetry, test helpers                             |
| `tools/scripts/`                             | Node scripts behind the root pnpm commands                                   |
| `docs/`                                      | Design docs and [Architecture Decision Records](docs/adr/)                   |

## Design docs

- [Technical design](docs/TECHNICAL_DESIGN.md)
- [Roadmap](docs/ROADMAP.md)
- [Product spec](docs/PRODUCT_SPEC.md)

See [CONTRIBUTING.md](CONTRIBUTING.md) to work on Patchd and [SECURITY.md](SECURITY.md) to report a vulnerability.
