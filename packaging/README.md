# Packaging

The `.deb` and `.rpm` are built in CI from the release binaries, never on a workstation:
`cargo deb` and `cargo generate-rpm` read their metadata from `crates/patchd-cli/Cargo.toml`.

| Path | What |
| --- | --- |
| `config.toml` | The commented example installed as `/etc/patchd/config.toml` (conffile / `noreplace`). A unit test checks it parses to the defaults |
| `systemd/patchd-apply.service` | Runs `patchd apply --yes`; read the comment above its hardening block before changing it |
| `systemd/patchd-apply.timer` | The nightly schedule; hosts override it with a drop-in |
| `debian/` | Maintainer scripts; cargo-deb appends its systemd handling at `#DEBHELPER#` |

Both packages install the units without enabling them ([ADR 0008](../docs/adr/0008-systemd-timer-before-daemon.md)), and neither restarts the unit on upgrade: patchd can upgrade itself during a run.

`tests/install/check-package.sh` installs a package in a container and checks its contents; CI runs it on every supported distribution. MSI packaging arrives in v0.4.
