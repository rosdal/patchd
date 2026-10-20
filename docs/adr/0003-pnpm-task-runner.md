# 0003. Use pnpm root scripts as the single task runner

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

The repo mixes one Go module with a pnpm workspace (`web`, `ide/*`, `tools/*`). Developers and CI need one set of commands for both languages. The initial skeleton used Task (`Taskfile.yml`), one more tool to install next to pnpm, which everyone needs anyway.

## Decision

Root `package.json` scripts are the task runner for the whole repo: `pnpm build`, `test`, `lint`, `format`, `dev`, `gen`, `eval` and `clean`. Go commands are called directly (`go test ./...`, `golangci-lint run`), or through small Node scripts in `tools/scripts/` when they need logic, such as injecting version data with `-ldflags`. Scripts are composed with `npm-run-all2`. CI runs the same commands developers run.

Rejected: Task and Make (an extra tool, and Make is awkward on Windows), and Nx or Turborepo (build caching we don't need yet, for a large dependency).

## Consequences

- `pnpm install` also installs the git hooks, through the `prepare` script.
- Node scripts avoid shell quirks, so commands behave the same on Linux, macOS and Windows.
- Go-only contributors need Node and pnpm installed.
- `Taskfile.yml` is removed.
