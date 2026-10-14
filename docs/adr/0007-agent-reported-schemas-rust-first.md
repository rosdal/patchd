# 0007. Agent-reported schemas are generated from Rust types

Status: Accepted · 2026-10-14 · Amends [0004](0004-monorepo-layout.md)

## Context

ADR 0004 makes `protocol/openapi.yaml` the single source of truth and generates Rust types from it with `typify`. That fits the server's endpoints, which the server defines and the agent calls. The scan report is the opposite case. The agent produces it, its shape comes from parsing apt output, and v0.2 has the Rust types long before there is a server. Generated Rust types for it would be awkward to write parsers against: no custom newtypes such as `ProviderId`, no `jiff::Timestamp`, and no doc comments.

## Decision

- **Data the agent produces** (starting with `ScanReport`) is defined as Rust types in `patchd-core::model`. `cargo xtask schema` generates `protocol/schemas/scan-report.json` from them with `schemars`, and `openapi.yaml` references that file under `components.schemas`.
- **Endpoints the server owns** stay OpenAPI-first, as ADR 0004 says.
- CI regenerates the schema and fails if `protocol/` changed, so the committed schema cannot drift from the types.
- The schema has a `schema_version` field. Within one version, fields may only be added, and only as optional fields. Renaming or removing a field means a new `schema_version` (see `docs/protocol.md`).

## Consequences

- The agent's types are plain Rust with doc comments, and those comments become the schema's descriptions.
- The TypeScript server still gets its types from one file, `openapi.yaml`, which now pulls in the generated schema by reference.
- A field change to `ScanReport` shows up in the PR as a schema diff, and a test validates the CLI's JSON output against the committed schema.
- `protocol/` now has two kinds of file: `openapi.yaml`, which is written by hand, and `schemas/`, which is generated. The OpenAPI file marks the reference as generated.

## Alternatives considered

- **OpenAPI-first for everything (ADR 0004 unchanged).** Consistent, but the Rust side would be built on generated types it cannot extend, and the spec would be written before the data it describes is understood.
- **Hand-written schema next to the Rust types.** No tooling, but nothing catches drift between the two.
