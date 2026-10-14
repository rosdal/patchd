# apt fixtures

Each directory is one host, recorded with `scripts/capture-fixtures.sh` under `LC_ALL=C`:

| File | Command |
| --- | --- |
| `os-release` | `cat /etc/os-release` |
| `dpkg-query.txt` | `dpkg-query -W -f='${binary:Package}\t${Version}\t${Architecture}\t${db:Status-Abbrev}\n'` |
| `apt-get-s-dist-upgrade.txt` | `apt-get -s -q dist-upgrade` |
| `apt-mark-showhold.txt` | `apt-mark showhold` (only when something is held) |
| `apt-cache-policy.txt` | `apt-cache policy <held packages>` (only when something is held) |

`dpkg-query.txt` lines end in a space (`ii `); keep it when editing by hand.

## Where each one came from

| Directory | Source |
| --- | --- |
| `ubuntu-22.04`, `ubuntu-24.04`, `ubuntu-26.04`, `debian-12`, `debian-13` | Captured from the official container images |
| `ubuntu-24.04-held`, `debian-13-held` | Captured from the images after `apt-mark hold` on a package with a pending update |
| `raspios-trixie-arm64` | Captured from the official Raspberry Pi OS Lite arm64 image (2026-10-06), unpacked into an arm64 container |
| `edge-cases` | Hand-written (Proxmox-shaped): a removal, a new kernel dependency, an epoch, a `:i386` copy, a held package, a backports major bump, a half-configured package |
| `ubuntu-24.04-phased` | `ubuntu-24.04` plus a hand-written "deferred due to phasing" section |
| `no-updates`, `broken` | Hand-written for the CLI exit-code tests (0 and 1) |

Debian 13 and Ubuntu 26.04 ship apt 3.x, so their fixtures are what prove the parser survives the apt 3 output changes.
