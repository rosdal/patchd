# Patchd

[![CI](https://github.com/rosdal/patchd/actions/workflows/ci.yml/badge.svg)](https://github.com/rosdal/patchd/actions/workflows/ci.yml)

Self-hosted patch management for Linux and Windows. A small Rust agent drives each machine's own update tools (apt, dnf, Windows Update, winget) and reports which CVEs it is still exposed to; an optional server adds fleet policy, scheduling and vulnerability context.

**Status: pre-alpha.** `patchd scan` lists installed packages and pending updates on Debian, Ubuntu, Proxmox VE and Raspberry Pi OS. It is read-only; nothing installs updates yet.

## Quick start

1. Download the archive for your machine from the [latest release](https://github.com/rosdal/patchd/releases) and unpack it: `tar xzf patchd-*-x86_64-unknown-linux-musl.tar.gz` (or `aarch64` for a Pi).
2. Run `./patchd scan`. No root needed.
3. Read the table: one row per pending update, security updates first. The exit code is 11 when security updates are pending, so it works in cron and monitoring; see [docs/cli.md](docs/cli.md).

## Layout

```text
crates/       Rust workspace: patchd-core, patchd-cli (binary `patchd`), patchd-agent
protocol/     openapi.yaml for the agent protocol, plus schemas generated from Rust (cargo xtask schema)
server/       TypeScript server and web UI (from v0.6)
packaging/    .deb/.rpm (from v0.3) and MSI (from v0.4)
docs/adr/     Architecture decision records
scripts/      Fixture capture and other maintenance scripts
xtask/        Repository tasks run with `cargo xtask`
```

## Development

You need Rust (the version is pinned in `rust-toolchain.toml`), [`just`](https://github.com/casey/just), [`cargo-deny`](https://github.com/EmbarkStudios/cargo-deny) and pnpm. `just ci` runs the same checks as CI. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

- The agent, CLI and shared crates (`crates/`) and the protocol definition (`protocol/`) are licensed under [Apache-2.0](LICENSE-APACHE).
- The server (`server/`) is licensed under [AGPL-3.0-only](LICENSE-AGPL).

See [ADR 0003](docs/adr/0003-licence.md) for why.
