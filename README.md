# Patchd

Self-hosted patch management for Linux and Windows. A small Rust agent drives each machine's own update tools (apt, dnf, Windows Update, winget) and reports which CVEs it is still exposed to; an optional server adds fleet policy, scheduling and vulnerability context.

**Status: pre-alpha, does nothing useful yet.**

## Licence

- The agent, CLI and shared crates (`crates/`) and the protocol definition (`protocol/`) are licensed under [Apache-2.0](LICENSE-APACHE).
- The server (`server/`) is licensed under [AGPL-3.0-only](LICENSE-AGPL).
