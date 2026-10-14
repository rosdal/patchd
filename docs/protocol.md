# Protocol

The JSON that `patchd scan --json` prints is the scan report. From v0.6 the agent sends the same document to the server's `/agent/inventory` endpoint. Its JSON Schema is [`protocol/schemas/scan-report.json`](../protocol/schemas/scan-report.json), generated from the Rust types in `crates/patchd-core/src/model.rs` by `cargo xtask schema` ([ADR 0007](adr/0007-agent-reported-schemas-rust-first.md)). Never edit the schema by hand; CI fails if it differs from the types.

## ScanReport

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | `1`. See the compatibility rule below |
| `agent_version` | string | The build that produced the report, such as `0.2.0 (1a2b3c4d5e x86_64-unknown-linux-musl)` |
| `scanned_at` | string | RFC 3339 timestamp, UTC |
| `host` | HostInfo | The scanned machine |
| `warnings` | string[] | Host-level warnings, such as running inside a container |
| `providers` | ProviderReport[] | One entry per provider that applies to the host |

**HostInfo:** `hostname`, `arch` (machine architecture, `x86_64` or `aarch64`), `kernel` (release, or `null`), and `os`: `id` (`ubuntu`, `debian`; Proxmox VE and Raspberry Pi OS report `debian`), `id_like`, `version_id`, `codename`, `pretty_name`, all from `/etc/os-release`.

**ProviderReport:**

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | `apt` |
| `packages` | Package[] | Everything installed |
| `updates` | Update[] | Everything that can be updated, held packages included |
| `removals` | string[] | Packages the upgrade would remove |
| `index_age_secs` | integer or null | Seconds since the package lists were refreshed |
| `warnings` | string[] | Problems that make the result less reliable |
| `error` | string or null | Set when the provider failed. Whatever it did collect is still reported, and the other providers are unaffected |

**Package:** `provider`, `name` (multi-arch copies keep their suffix, such as `libc6:i386`), `version`, `arch`, `held`.

**Update:** `provider`, `name`, `arch`, `from_version` (`null` for a new dependency), `to_version`, `kind` (`security`, `minor` or `major`), `origins` (where the new version comes from, such as `Ubuntu:24.04/noble-security`), `held`.

## Compatibility rule

Within one `schema_version`, a change may only **add** fields, and every added field must be optional, so a consumer built against an older report keeps working. Renaming a field, removing one, changing its type or meaning, or making an optional field required all mean `schema_version: 2`. Consumers must ignore fields they do not know.
