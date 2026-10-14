# 0008. A systemd timer schedules runs until the agent needs its own scheduler

Status: Accepted · 2026-10-14

## Context

v0.3 is the first version that installs updates on its own. Something has to start a run every night, survive reboots and missed nights, and keep a log of what happened. The roadmap planned a `patchd-agent` daemon with its own scheduler for this, but on Linux every job that daemon would do in v0.3 is already done by systemd: start a process at a time, catch up after downtime, record its output, and stop it cleanly at shutdown. The daemon only earns its keep once there is something systemd cannot do: Windows (v0.4), maintenance windows with time zones and reboot policy (v0.5), and a connection to the server (v0.6).

## Decision

`patchd apply` runs as a oneshot service, `patchd-apply.service`, started by `patchd-apply.timer`. The schedule is the timer's `OnCalendar`, and nothing else: the config file has no schedule key in v0.3. A host gets its own schedule through a timer drop-in, not by editing the unit.

`ExecStart` runs `patchd` itself, so patchd is the unit's main process and receives the SIGTERM when the unit stops. patchd wraps only the install step in `systemd-inhibit`, so shutdown and sleep are blocked while dpkg runs and at no other time.

The package installs both units but enables neither. Enabling them is a deliberate act, done by Ansible or `systemctl enable --now patchd-apply.timer`.

The `patchd-agent` daemon moves to v0.4/v0.5.

## Consequences

- No scheduler code to write, test or debug. `Persistent=true` runs a missed night's job after the machine comes back, and `RandomizedDelaySec` spreads hosts out.
- Logs land in journald for free (`journalctl -u patchd-apply`), next to the run log files.
- The timer is Linux-only. Windows gets its scheduler with the agent in v0.4, so the two platforms schedule differently for at least one release.
- When the agent arrives, the timer becomes a fallback or goes away. The `apply` code path stays the same: the agent will call it rather than replace it.

## Alternatives considered

- **The daemon now, as the roadmap said.** A long-running process that sleeps until 03:30 and then applies. It needs its own catch-up logic, its own logging, a reload path for the config, and a story for what happens when it crashes. All of that is code systemd already ships.
- **cron.** Present everywhere, but no catch-up after downtime, no `RandomizedDelaySec`, output goes to mail, and no clean way to stop a run that is in progress.
- **`ExecStart=systemd-inhibit … patchd apply`.** Holds the inhibitor for the whole run with no code, but makes `systemd-inhibit` the main process. With `KillMode=mixed`, systemd would send SIGTERM to `systemd-inhibit` rather than patchd, then SIGKILL everything left, dpkg included.
