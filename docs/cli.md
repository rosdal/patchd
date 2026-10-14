# patchd CLI

Every command accepts `--config <path>` instead of `/etc/patchd/config.toml` (see [config.md](config.md)), and `-v`/`-vv` for debug or trace logging on stderr.

## `patchd scan`

Lists installed packages and pending updates on apt hosts (Debian, Ubuntu, Proxmox VE, Raspberry Pi OS) and dnf hosts (Fedora, RHEL, AlmaLinux, Rocky Linux). It is read-only: it never takes the dpkg lock and never changes the system, so it is safe to run on any host, as any user.

```text
patchd scan [--json | --quiet] [--provider apt|dnf] [--refresh] [--packages] [-v | -vv]
```

| Flag | Effect |
| --- | --- |
| *(none)* | A table of pending updates on stdout; warnings and errors on stderr |
| `--json` | The full scan report as one pretty-printed JSON object on stdout, nothing else (see [protocol.md](protocol.md)) |
| `--quiet`, `-q` | Prints nothing; only the exit code matters |
| `--provider apt\|dnf` | Runs only this provider. Fails with exit code 1 if it does not apply to the host |
| `--refresh` | Refreshes the package lists first (`apt-get update`, `dnf makecache`). Needs root; without root the scan stops with an error instead of reporting stale data. Fails while a `patchd apply` holds the run lock |
| `--packages` | Adds a table of every installed package |
| `-v`, `-vv` | Debug or trace logging to stderr. `RUST_LOG` overrides it. stdout stays clean, so `--json \| jq` always works |

Colour is used only when stdout is a terminal, and never when `NO_COLOR` is set.

### Output

```text
host  pve-elitedesk · Debian GNU/Linux 12 (bookworm) · x86_64 · lists 3h old

KIND      PACKAGE                             FROM             TO                 ORIGIN
security  libgnutls30                         3.7.9-2+deb12u3  3.7.9-2+deb12u4    Debian-Security:12/stable-security
minor     base-files                          12.4+deb12u7     12.4+deb12u8       Debian:12.8/stable
new       proxmox-kernel-6.8.12-4-pve-signed  —                6.8.12-4           Proxmox:bookworm/pve-no-subscription
held      zfsutils-linux                      2.2.6-pve1       2.2.7-pve1         http://download.proxmox.com/debian/pve bookworm/pve-no-subscription

3 updates (1 security) · 1 held · 0 removals
```

`KIND` is one of:

| Kind | Meaning |
| --- | --- |
| `security` | At least one origin is a security suite (`*-security`, or the `Debian-Security` label) |
| `major` | The upstream major version changes, such as `2.x` to `3.x` (usually backports) |
| `minor` | Any other upgrade |
| `new` | A package the upgrade installs as a new dependency, typically a new kernel |
| `held` | A newer version exists, but the package is pinned with `apt-mark hold`, so an upgrade skips it |

The scan also warns about anything that makes the numbers less trustworthy: package lists older than 24 hours, packages `dist-upgrade` would remove, Ubuntu phased updates being held back, packages in a broken dpkg state, and running inside a container.

### Exit codes

The exit code is the same in every output mode.

| Code | Meaning |
| --- | --- |
| 0 | Scan OK, nothing to update |
| 10 | Updates available, none of them security |
| 11 | Security updates available |
| 1 | A provider failed (the report still contains what succeeded), or no provider applies to this host |
| 2 | Usage error |

A provider failure wins over pending updates, so a partial scan never looks healthy. Held packages are listed but do not change the exit code: holding a package is a deliberate decision.

### Comparing with apt

`patchd scan` counts the same upgrades as `apt-get -s dist-upgrade`. Its count can differ from `apt list --upgradable` in three ways, each of which shows up in the report:

- Held packages are in both, as `held: true` updates in Patchd.
- Ubuntu phased updates are in `apt list --upgradable` but only appear as a warning in Patchd, because apt will not install them yet.
- A package that `dist-upgrade` replaces is "upgradable" to `apt list`, and a removal to Patchd.

### Monitoring examples

Cron mails only when security updates are pending:

```sh
# /etc/cron.d/patchd
0 7 * * * root patchd scan --quiet; [ $? -eq 11 ] && patchd scan
```

A Home Assistant command-line binary sensor that turns on when a host has pending security updates:

```yaml
command_line:
  - binary_sensor:
      name: pve1 security updates
      command: >-
        ssh pve1 patchd scan --quiet; [ $? -eq 11 ] && echo ON || echo OFF
      scan_interval: 3600
```

## `patchd plan`

Shows exactly what `patchd apply` would install. It is read-only and needs no root, so it is also the dry run: `apply` builds the same plan and installs nothing else ([ADR 0009](adr/0009-apply-exactly-the-plan.md)).

```text
patchd plan [--security | --all] [--json]
```

| Flag | Effect |
| --- | --- |
| *(none)* | Uses `filter` from the config (default `security`) |
| `--security` | Security updates only, plus the new packages they pull in |
| `--all` | Every pending update |
| `--json` | The plan as JSON (schema: [`protocol/schemas/plan.json`](../protocol/schemas/plan.json)) |

The table has the same columns as `scan`. Held packages, and packages matching an `exclude` glob, are listed as `held` or `excluded` and never installed. The footer counts them:

```text
will install 4, skip 1 held, 0 removals
```

Anything that will make `apply` refuse to run, such as a security update that needs a non-security upgrade outside the plan, is printed as a warning on stderr.

| Code | Meaning |
| --- | --- |
| 0 | The plan is empty |
| 10 | The plan installs something |
| 1 | Planning failed |

## `patchd apply`

Installs the plan. Needs root. Normally started by `patchd-apply.timer` (see [install-linux.md](install-linux.md)); run it by hand with `sudo patchd apply`.

```text
patchd apply [--security | --all] [--yes]
```

Without `--yes` it asks for confirmation, and it refuses when stdin is not a terminal.

A run goes through these steps. Everything before the install happens while no package has been touched:

1. **Lock.** Takes `/run/patchd/lock`. If another run holds it, exits 3 at once.
2. **Refresh** the package lists, when `refresh = true` (the default). A broken repository fails the run here.
3. **Plan**, exactly as `patchd plan` does.
4. **Pre-flight checks:** root; `dpkg --audit` (with `repair = true`, `dpkg --configure -a` gets one chance first); free space on `/` (1 GiB) and, when the plan touches the kernel or boot loader, on a separate `/boot` and `/boot/efi` (200 MiB); unattended-upgrades (a warning only); and a **simulation** of the exact install command, which must match the plan and remove nothing unless `allow_removals = true`. Any failed check stops the run with exit code 4.
5. **Download** everything. A network or repository failure stops the run here.
6. **Install**, with shutdown and sleep blocked by `systemd-inhibit`. dpkg and dnf run in their own process group, so SIGTERM and Ctrl-C never reach them: patchd lets the install finish, then stops.
7. **Verify** that every planned package is at its planned version and that `dpkg --audit` is clean.
8. **Reboot detection.** With `[reboot] mode = "if-required"`, a successful run that needs a reboot schedules one with `shutdown -r +5`.

Every step goes to the run log; see `patchd history`.

| Code | Meaning |
| --- | --- |
| 0 | Everything planned is installed, or there was nothing to install |
| 20 | Success, and a reboot is needed to finish (the unit treats it as success) |
| 1 | `partial` (the install ran, but not everything reached its planned version), `failed` (stopped before the install, such as at the download), or `interrupted` |
| 3 | Another patchd run is active |
| 4 | A pre-flight check refused the run; nothing was changed |
| 2 | Usage error |

## `patchd history`

Lists past runs from the run logs in `/var/log/patchd/runs`, which only root can read.

```text
patchd history [--limit 20] [--json]
patchd history <run-id> [--json]
```

```text
STARTED                  DURATION  FILTER    INSTALLED  RESULT                   REBOOT    RUN
2026-10-15 03:41:07 UTC  1m12s     security  3          success_reboot_required  required  01a14b2c-77d0
2026-10-14 03:38:51 UTC  4s        security  0          success                            01a145f0-1e2b
```

`RESULT` is `success`, `success_reboot_required`, `partial`, `failed`, `aborted_preflight`, `interrupted`, or `crashed` for a log that ends without a result because patchd died mid-run.

With a run ID (any unique prefix), it shows that run step by step: the pre-flight checks, the plan, every command with its exit code and the last lines of its output, the verification, and the result.

## `patchd status`

```text
last run   2026-10-15 03:41 UTC · success_reboot_required · 3 installed · 01a14b2c-77d0
next run   Thu 2026-10-16 03:47:12 CEST
reboot     required: kernel 6.8.12-5-pve installed, 6.8.12-4-pve running
pending    0 update(s), 0 security
```

The next run comes from `systemctl show patchd-apply.timer`, and the pending updates from a fresh read-only scan. `--json` prints the same as one object.

## `patchd config`

```text
patchd config check    # parse and validate; errors name the line; exit 0 or 1
patchd config show     # the effective config, after defaults and PATCHD_* overrides
```

See [config.md](config.md) for every key.
