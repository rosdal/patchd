# Patchd Code

Patchd reviews TypeScript and JavaScript pull requests. A dataflow engine built on typescript-go finds candidate bugs, agents confirm each one with evidence before it is posted, and patches are verified in a sandbox before Patchd suggests them. It is built for teams that want fewer, provable review comments, hosted in the EU or self-hosted.

**Status:** pre-alpha, v0.3: TypeScript lowered to Patchd IR. `engine/tsbridge` discovers, loads and type-checks any TypeScript or JavaScript repo ([docs/engine/tsbridge.md](docs/engine/tsbridge.md)), and `engine/ir` lowers every function to a typed control-flow graph in SSA form, the input of the analyses to come ([docs/engine/ir.md](docs/engine/ir.md)); inspect them with `patchd debug project|types|resolve|stats|ir|ir-coverage`. The review subcommands still return "not implemented yet".

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
