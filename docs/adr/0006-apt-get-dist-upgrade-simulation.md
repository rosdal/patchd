# 0006. apt updates come from an `apt-get -s dist-upgrade` simulation

Status: Accepted · 2026-10-14

## Context

The apt provider has to list pending updates on Debian, Ubuntu, Proxmox VE and Raspberry Pi OS without root, without taking the dpkg lock, and with a count that matches what an administrator sees from apt. apt offers several ways to ask, and they disagree.

## Decision

- **Use `apt-get` and `dpkg-query`, never `apt`.** `apt` prints "does not have a stable CLI interface" when its output is piped, and its output has changed between releases. `apt-get` and `dpkg-query` are the scriptable interfaces, and every command runs under `LC_ALL=C`.
- **Simulate `dist-upgrade`, not `upgrade`.** Proxmox VE requires `dist-upgrade` (or `full-upgrade`), and a plain `upgrade` holds back any package that needs a new dependency. On every distro that includes new kernel packages, so `upgrade` would undercount.
- **One dry run supplies everything.** `apt-get -s -q dist-upgrade` needs neither root nor the lock. Its `Inst` lines carry the old version, the new version and every origin, which is enough to classify security updates. A `[old]` field missing from an `Inst` line means a new dependency, and `Remv` lines are packages the upgrade would remove.
- **Inventory comes from `dpkg-query -W`** with an explicit tab-separated format, which is stable across all supported releases.

## Consequences

The dry run has two known gaps, and both are handled outside it:

- **Held packages.** `dist-upgrade` skips packages pinned with `apt-mark hold` and says nothing about them. Patchd runs `apt-mark showhold`, then `apt-cache policy` on the held packages, and reports each one that has a newer candidate as an update with `held: true`. Without this the count never matches `apt list --upgradable`.
- **Ubuntu phased updates.** apt defers upgrades whose rollout phase has not reached this machine. apt 2.5+ lists them under "deferred due to phasing" (apt 3: "Not upgrading yet due to phasing"); apt 2.4 on Ubuntu 22.04 lists them as kept back. Patchd reports them as a warning and does not count them. v0.3 has to respect phasing when it applies updates, and will revisit this.

A third difference showed up while testing: when `dist-upgrade` replaces a package (removes it and installs a successor), `apt list --upgradable` still lists the old one, while Patchd reports it under `removals`.

The simulation reads the package lists as they are, so a scan is only as fresh as the last `apt-get update`. Patchd reports the lists' age and warns when they are older than 24 hours; `patchd scan --refresh` runs `apt-get update` first, which is the only write v0.2 makes.

## Alternatives considered

- **`apt list --upgradable`.** The number people check by hand, but `apt` is not a stable interface, and the list has no origins, so security updates cannot be told apart.
- **python3-apt or libapt bindings.** An exact view of the resolver, but Python is not present on every target, and binding libapt-pkg ties the static binary to one apt ABI.
- **Parsing `/var/lib/apt/lists` directly.** No subprocess, but it reimplements pinning, phasing and dependency resolution, which ADR 0002 rules out.
