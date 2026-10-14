# 0011. Detect reboots, act only when asked

Status: Accepted · 2026-10-14

## Context

A kernel, libc or systemd update does not take effect until the machine reboots. Two of the three hosts Patchd first runs on are Proxmox nodes with VMs on them, where an unannounced reboot takes down Home Assistant or a media server in the middle of the night. A pending kernel is a known, visible risk; a surprise reboot is an outage. A full reboot policy (windows, notify-then-force) needs maintenance windows, which arrive in v0.5.

There is also no single reliable signal. `/run/reboot-required` is an Ubuntu convention that plain Debian and Proxmox do not reliably create.

## Decision

- **Detect, layered.** `/run/reboot-required` and its `.pkgs` file; `needrestart -b` when installed (kernel status and services to restart); the running kernel compared with the newest one in `/lib/modules`; `dnf needs-restarting -r` on dnf systems. Every check that fires adds a reason, so the report says *why* a reboot is needed.
- **Report.** In `patchd status`, in the run log, and as exit code 20 from `apply` (success, reboot required).
- **Act only when configured.** `[reboot] mode = "never"` is the default. `mode = "if-required"` runs `shutdown -r +5` after a successful run that needs one, which warns logged-in users and can be cancelled with `shutdown -c`. A failed or partial run never reboots.

## Consequences

- The Proxmox hosts can be patched nightly and rebooted by hand when convenient, with Patchd telling you when that is due and why.
- The Pi, which is quick to recover, can opt in to automatic reboots and acts as the canary.
- Exit code 20 counts as success for systemd (`SuccessExitStatus=20`), so a pending reboot is not a failed unit.
- Kernel detection compares directory names in `/lib/modules`, which is a heuristic. It falls back to mtimes when version comparison cannot decide, and is tested with Proxmox and Raspberry Pi kernel names.

## Alternatives considered

- **Reboot whenever required.** What many tools default to. Wrong for hypervisors, and the reason most people turn automatic patching off.
- **Only `/run/reboot-required`.** Simple, and misses kernel updates on Debian and Proxmox entirely.
- **Full reboot policy now.** Depends on maintenance windows and notifications, both v0.5.
