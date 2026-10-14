# Patchd

[![CI](https://github.com/rosdal/patchd/actions/workflows/ci.yml/badge.svg)](https://github.com/rosdal/patchd/actions/workflows/ci.yml)

Self-hosted patch management for Linux and Windows. A small Rust agent drives each machine's own update tools (apt, dnf, Windows Update, winget) and reports which CVEs it is still exposed to; an optional server adds fleet policy, scheduling and vulnerability context.

**Status: pre-alpha.** On Debian, Ubuntu, Proxmox VE, Raspberry Pi OS, Fedora and RHEL-family hosts, `patchd scan` lists pending updates and `patchd plan` shows what would be installed; both are read-only.

**`patchd apply` changes your system: it installs updates.** It is built to fail safe rather than to install at any cost ([ADR 0009](docs/adr/0009-apply-exactly-the-plan.md)):

- **Plan-exact.** It installs exactly the versions `patchd plan` shows. If the package manager would do anything else, the run stops before touching a package.
- **Simulate, download, then install.** Pre-flight checks, a simulation of the exact command and the full download all happen first; a refused run changes nothing.
- **No removals by default**, security updates only by default, and no reboots unless you opt in.
- **Never interrupts dpkg.** Stopping the service lets an install in progress finish.
- **Every run is logged**, step by step (`patchd history`).

## Quick start

1. Install the `.deb` or `.rpm` from the [latest release](https://github.com/rosdal/patchd/releases); see [docs/install-linux.md](docs/install-linux.md). Nothing runs until you enable the timer.
2. Run `patchd scan`, then `patchd plan`. No root needed.
3. When the plan looks right: `sudo patchd apply` once by hand, then `sudo systemctl enable --now patchd-apply.timer` for a nightly run.

`patchd scan` exits 11 when security updates are pending, so it also works in cron and monitoring; see [docs/cli.md](docs/cli.md). The config file is documented in [docs/config.md](docs/config.md).

## Layout

```text
crates/       Rust workspace: patchd-core, patchd-cli (binary `patchd`), patchd-agent
protocol/     openapi.yaml for the agent protocol, plus schemas generated from Rust (cargo xtask schema)
server/       TypeScript server and web UI (from v0.6)
packaging/    systemd units, the example config, and .deb maintainer scripts (MSI from v0.4)
docs/adr/     Architecture decision records
scripts/      Fixture capture and other maintenance scripts
tests/        End-to-end tests against fake apt and dnf repositories, and package install tests
xtask/        Repository tasks run with `cargo xtask`
```

## Development

You need Rust (the version is pinned in `rust-toolchain.toml`), [`just`](https://github.com/casey/just), [`cargo-deny`](https://github.com/EmbarkStudios/cargo-deny) and pnpm. `just ci` runs the same checks as CI. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

- The agent, CLI and shared crates (`crates/`) and the protocol definition (`protocol/`) are licensed under [Apache-2.0](LICENSE-APACHE).
- The server (`server/`) is licensed under [AGPL-3.0-only](LICENSE-AGPL).

See [ADR 0003](docs/adr/0003-licence.md) for why.
