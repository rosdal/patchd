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
| `id` | string | `apt` or `dnf` |
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

## Plan

`patchd plan --json` prints a plan, and every run log records the plan it applied. Schema: [`protocol/schemas/plan.json`](../protocol/schemas/plan.json). v0.7's signed jobs will carry exactly this document.

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | A UUIDv7, so IDs sort by creation time |
| `created_at` | string | RFC 3339 timestamp, UTC |
| `filter` | string | `security` or `all` |
| `providers` | ProviderPlan[] | One per provider that applies to the host |

**ProviderPlan:** `id`, `updates` (Update[], each pinned to its `to_version`), `held_skipped` (each an Update plus `reason`: `held` or `excluded`), `removals` (packages the install would remove), `warnings`.

## Run log

Each run writes one JSON-lines file, `/var/log/patchd/runs/<started_at>_<run_id>.jsonl`. Every line is an object with `run_id`, `ts` (RFC 3339) and `event`; the other fields depend on the event. Schema: [`protocol/schemas/run-event.json`](../protocol/schemas/run-event.json). The v0.6 agent will upload these events unchanged, so the compatibility rule above applies to them too.

| `event` | Fields |
| --- | --- |
| `run_started` | `agent_version`, `host`, `filter`, `command` |
| `preflight` | `checks`, each with `name`, `status` (`pass`, `warn` or `fail`) and `detail` |
| `plan` | `plan` |
| `step_started` | `step`, `command` |
| `step_output` | `step`, `stream` (`stdout` or `stderr`), `lines` (batched) |
| `step_finished` | `step`, `command`, `exit_code`, `duration_ms`, `error` |
| `verify` | `ok`, `missed` (Change[]), `audit` (lines from `dpkg --audit`) |
| `reboot_status` | `status`: `required`, `reasons`, `services_to_restart` |
| `warning` | `message` |
| `run_finished` | `result`, `installed` and `failed` (Change[]), `reboot_required`, `duration_ms`, `message` |

**Change:** `name`, `from` (`null` for a new package), `to`.

A file is written with `O_APPEND` and flushed after every event, so a crash leaves a readable file that ends without `run_finished`; `patchd history` shows such a run as `crashed`.
