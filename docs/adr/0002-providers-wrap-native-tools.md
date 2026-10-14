# 0002. Providers wrap native update tools

Status: Accepted · 2026-10-14

## Context

Every supported platform already has a mature update mechanism: apt and dnf on Linux, the Windows Update Agent and WinGet on Windows. They handle dependency resolution, signature checks, configuration files and transactions. Reimplementing any of that would be a large, risky project.

## Decision

Patchd drives the platform's own tools instead of reimplementing package management. Each update source is a **provider** behind one `Provider` trait (detect, inventory, available updates, apply, reboot required). The agent core is a scheduler over a set of providers, so adding a source never touches the core.

## Consequences

- Patchd gets each tool's dependency handling, signing and transactions for free, and its changes match what an administrator would get from running the tool by hand.
- Patchd also inherits each tool's quirks: apt's interactive prompts, WinGet's limited visibility under SYSTEM, Windows Update's opaque HRESULTs. These are handled per provider.
- The `Provider` trait has to fit very different tools; anything source-specific stays inside its provider.

## Alternatives considered

- **Install packages directly** (download .deb/.rpm/.msi files and install them). Rejected: it duplicates the package managers and bypasses their safety checks.
- **Use a configuration-management tool** (Ansible, Salt) as the engine. Rejected: a heavy dependency on every machine, and weak coverage of Windows Update.
