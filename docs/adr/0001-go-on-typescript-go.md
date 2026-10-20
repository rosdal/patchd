# 0001. Build the engine and backend in Go on typescript-go

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

Taint analysis in TypeScript is only precise with real type information: which `query` method a call resolves to, what a generic instantiates to, which overload applies. The best TypeScript type checker is now Go code: TypeScript 7.0, the native port of the compiler, reached general availability on 8 July 2026 and builds 8–12× faster than the JavaScript compiler. It has no stable programmatic API yet; Microsoft targets 7.1 for one, several months out. Oxc's tsgolint already builds programs with typescript-go and runs type-aware rules on them in production, by linking its internal packages through a shim.

## Decision

The analysis engine, workers, API and CLI are written in Go and call the TypeScript 7 checker in-process through `github.com/microsoft/typescript-go`, pinned to an exact commit. The web app and VS Code extension stay in TypeScript.

Options that lost:

- **TypeScript on the TS 6 API (`@typescript/typescript6`)**: full types, but single-threaded and slow on large repos, and TS 6 is the last JavaScript release, effectively frozen. Kept as the fallback.
- **Rust on Oxc**: fastest parsing, but no type checker; we would call out to tsgo for types and run two processes in two languages.
- **Wait for the TS 7.1 API**: right long term, but blocks all engine work for months.

## Consequences

- One language for engine, backend and CLI, and one static binary for Self-Hosted.
- We depend on compiler internals with no compatibility promise. [ADR 0005](0005-tsbridge-boundary.md) contains that to one package; the shim mechanism is decided in [ADR 0007](0007-tsbridge-shim.md).
- **TS 7.1 migration plan:** when 7.1 ships its public API, `engine/tsbridge` moves onto it behind the same Patchd-owned interface, gated by the full engine test suite. If the public API lacks what taint analysis needs (for example checker internals for call resolution), `tsbridge` keeps a narrow shim for the missing parts only. Nothing outside `tsbridge` changes either way.
- Revisit if typescript-go's internals churn faster than one engineer-day per upgrade, or if the 7.1 API plans are withdrawn.
