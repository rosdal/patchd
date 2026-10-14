# 0010. Patchd replaces unattended-upgrades

Status: Accepted · 2026-10-14

## Context

Debian and Ubuntu install `unattended-upgrades` by default on many images, triggered by `apt-daily-upgrade.timer`. It does the same job as `patchd apply`: install security updates at night. Running both means two tools competing for the dpkg lock in the same hours, and when one fails because the other held the lock or half-configured a package, the failure gets blamed on the wrong tool.

## Decision

Patchd replaces unattended-upgrades on hosts where `patchd-apply.timer` is enabled; it never runs alongside it.

- `patchd apply` detects it and logs a warning when all three hold: unattended-upgrades is installed, `apt-daily-upgrade.timer` is enabled, and `APT::Periodic::Unattended-Upgrade` is non-zero in `/etc/apt/apt.conf.d` (read as apt reads it: last setting wins, ignored file names skipped, both syntaxes). The timer alone is not enough: apt enables it everywhere, and with the setting at its default of `0` it installs nothing. The warning never aborts the run, which still works because apt waits for the lock (`DPkg::Lock::Timeout=600`).
- The Ansible role disables `apt-daily-upgrade.timer` and sets `APT::Periodic::Unattended-Upgrade "0"`.
- `apt-daily.timer`, which only refreshes package lists, stays enabled. It is harmless and keeps `patchd scan` fresh.

The package does not change unattended-upgrades on install. Turning off another tool is a decision for whoever enables the timer.

## Consequences

- One tool owns night-time package changes, and its run log is the full record.
- Hosts where Patchd is installed but its timer is not enabled keep their existing setup.
- A host where someone enables both gets a warning on every run, not a silent conflict.

## Alternatives considered

- **Coexist, and let the dpkg lock sort it out.** Works most nights. The nights it doesn't are the ones that need a manual `dpkg --configure -a`.
- **Disable unattended-upgrades from the package's postinst.** Surprising for anyone who installs the package to try `patchd scan`, and hard to undo cleanly on removal.
- **Abort when unattended-upgrades is active.** Safe, but turns a configuration oversight into missed security updates.
