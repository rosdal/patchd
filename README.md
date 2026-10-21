# Patchd Code

Patchd reviews TypeScript and JavaScript pull requests. A dataflow engine built on typescript-go finds candidate bugs, agents confirm each one with evidence before it is posted, and patches are verified in a sandbox before Patchd suggests them. It is built for teams that want fewer, provable review comments, hosted in the EU or self-hosted.

**Status:** pre-alpha, v0.1 (repo foundation). There is no analysis code yet; every subcommand except `version` returns "not implemented yet".

## Prerequisites

| Tool          | Version                                                            |
| ------------- | ------------------------------------------------------------------ |
| Git           | 2.40+                                                              |
| Node.js       | the version in `.node-version` (24 LTS)                            |
| pnpm          | the version in `package.json` `packageManager` (`corepack enable`) |
| Go            | the version in `go.mod`                                            |
| golangci-lint | v2 (CI pins the exact version)                                     |
| gofumpt       | latest, for `pnpm format`                                          |

## Build and test

```sh
pnpm install
pnpm build          # bin/patchd plus the web app and VS Code extension
pnpm test           # go test ./... and the TypeScript tests
pnpm lint           # golangci-lint, oxlint, tsc, prettier --check
./bin/patchd version
```

pnpm is the only task runner, for Go as well as TypeScript (see [ADR 0003](docs/adr/0003-pnpm-task-runner.md)). `pnpm format` rewrites Go and TypeScript files; `pnpm gen` and `pnpm eval` are placeholders until code generation and the eval harness exist.

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
