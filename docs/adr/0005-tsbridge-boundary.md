# 0005. Only engine/tsbridge may import typescript-go

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

[ADR 0001](0001-go-on-typescript-go.md) puts the engine on typescript-go internals, which have no compatibility promise and will be replaced by the TS 7.1 public API. If compiler types leak into the IR, the analyses or the rules, every typescript-go upgrade and the eventual API migration touch the whole engine.

## Decision

`engine/tsbridge` is the only package that imports `github.com/microsoft/typescript-go`; its `shim` subpackage holds the re-exports of internals. `tsbridge` exposes a small Patchd-owned API: load a project from `tsconfig.json`, walk syntax trees, get symbols, types and signatures, and resolve calls. Everything else uses those types.

golangci-lint's `depguard` enforces the rule (`typescript-go-only-in-tsbridge` in `.golangci.yml`) in `pnpm lint` and CI.

## Consequences

- A typescript-go upgrade, or the move to the 7.1 API, changes one package, gated by the engine test suite.
- `tsbridge` grows deliberately: a new engine need means a new `tsbridge` function, never a direct import.
- Some data is copied across the boundary instead of shared; the v0.2 benchmarks will measure what that costs.
