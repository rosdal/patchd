# Contributing

## Setup

Install the tools listed in the [README](README.md#prerequisites), then:

```sh
pnpm install        # also installs the lefthook git hooks
pnpm build && pnpm test && pnpm lint
```

## Commands

| Command       | Does                                                          |
| ------------- | ------------------------------------------------------------- |
| `pnpm build`  | Builds `bin/patchd`, the web app and the VS Code extension    |
| `pnpm test`   | `go test ./...` plus every workspace package's tests          |
| `pnpm lint`   | golangci-lint, oxlint, type checks and `prettier --check`     |
| `pnpm format` | gofumpt and Prettier, in place                                |
| `pnpm dev`    | Web app dev server (the API joins once `patchd serve` exists) |
| `pnpm clean`  | Removes build output                                          |

## Commits and branches

Commits follow [Conventional Commits](https://www.conventionalcommits.org), for example `feat(engine): lower optional chaining`. The commit-msg hook and CI both run commitlint; PRs are squash-merged, so the PR title must pass too.

Name branches `type/short-slug`, for example `feat/tsbridge-loader` or `fix/version-date`. Spikes live on `spike/<topic>` and are not merged; only their ADR and reusable setup are.

Don't bypass the hooks with `--no-verify`. If a hook fails, fix the cause.

## Rules of the road

- Only `engine/tsbridge` may import typescript-go; depguard enforces this in `pnpm lint`. The shims under `engine/tsbridge/shim/` are generated; to expose more of the compiler or move the typescript-go pin, follow [ADR 0007](docs/adr/0007-tsbridge-shim.md#upgrade-cost).
- Don't edit generated code in `api/gen/`, `store/gen/` or `web/src/api/gen/`; regenerate it with `pnpm gen`.
- Decisions that are hard to reverse get an ADR in `docs/adr/` (copy `0000-template.md`). Accepted ADRs are never edited, only superseded.

## Adding an engine fixture

Engine changes need a fixture in `engine/testdata/fixtures/<rule>/`: a small `.ts` file with `// expect: <rule> from <source>` on the sink line, or `// expect-none` for code that must stay silent. Every reported false positive or false negative becomes a fixture. The fixtures are deliberately vulnerable; they are marked `linguist-vendored` and excluded from linting.
