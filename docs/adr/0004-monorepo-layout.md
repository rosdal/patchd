# 0004. Monorepo layout with a single protocol definition

Status: Accepted · 2026-10-14

## Context

The Rust agent and the TypeScript server have to agree on every message they exchange. If each side defines those types by hand, they drift, and the drift shows up as runtime errors on real machines. The two sides also tend to change together: a new field is added on both ends at once.

## Decision

Agent, server and protocol live in one repository:

```text
crates/       Rust workspace: patchd-core, patchd-cli (binary `patchd`), patchd-agent
protocol/     openapi.yaml, the single source of truth for the agent protocol
server/       TypeScript server and web UI (pnpm workspace, from v0.6)
packaging/    .deb/.rpm (from v0.3) and MSI (from v0.4)
docs/adr/     Architecture decision records
```

`protocol/openapi.yaml` is the only place protocol types are defined. Rust types are generated from it with `typify`, TypeScript types with `openapi-typescript`. CI lints the file on every change.

## Consequences

- A protocol change is one pull request that updates the spec, the agent and the server together, and CI checks all three.
- One issue tracker, one release history, one set of security settings.
- CI has to handle two ecosystems, and Dependabot watches both.
- The licence split (see [0003](0003-licence.md)) follows the directory layout, so the boundaries between directories must stay clear.

## Alternatives considered

- **Separate repositories for agent, server and protocol.** Cleaner ownership, but every protocol change becomes a coordinated multi-repo release.
- **Hand-written types on both sides.** Nothing to generate, but nothing catches drift.
