# Patchd Code

Find it, prove it, patch it. Patchd reviews TypeScript pull requests with a
dataflow engine built on typescript-go, has agents confirm each finding before
it's posted, and verifies patches in a sandbox before suggesting them.

Status: pre-alpha.

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

## Development

Requires Go 1.27+, Node 24 with pnpm 10, [Task](https://taskfile.dev), buf and sqlc.

```sh
pnpm install
task gen     # protobuf + sqlc
task dev     # server + web app
task test
```

Design docs live in `docs/`.
