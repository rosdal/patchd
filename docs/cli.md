# patchd CLI

## `patchd scan`

Lists installed packages and pending updates. It is read-only: it never takes the dpkg lock and never changes the system, so it is safe to run on any host, as any user.

```text
patchd scan [--json | --quiet] [--provider apt] [--refresh] [--packages] [-v | -vv]
```

| Flag | Effect |
| --- | --- |
| *(none)* | A table of pending updates on stdout; warnings and errors on stderr |
| `--json` | The full scan report as one pretty-printed JSON object on stdout, nothing else (see [protocol.md](protocol.md)) |
| `--quiet`, `-q` | Prints nothing; only the exit code matters |
| `--provider apt` | Runs only this provider. Fails with exit code 1 if it does not apply to the host |
| `--refresh` | Runs `apt-get update` first. Needs root; without root the scan stops with an error instead of reporting stale data |
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
