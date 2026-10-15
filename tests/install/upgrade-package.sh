#!/usr/bin/env bash
# Upgrades from the previous release's package to this build's, and checks that a local
# config edit and the timer's enabled state survive (docs/v0.3.md §11).
#
#   tests/install/upgrade-package.sh <dir with the previous packages> <dir with the new ones>
#
# Run as root in a throwaway debian, ubuntu, fedora or almalinux container. The new
# package must carry a higher version than the previous one.
set -euo pipefail

old=$1
new=$2
fail() { echo "FAIL: $*" >&2; exit 1; }
pick() { find "$1" -name "$2" | head -1 | xargs realpath; }

if command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  # systemctl, to enable the timer; it works without a running systemd.
  apt-get install -y -qq systemd >/dev/null
  apt-get install -y -qq "$(pick "$old" 'patchd_*.deb')" >/dev/null
  upgrade() { apt-get install -y -qq "$(pick "$new" 'patchd_*.deb')" >/dev/null; }
else
  dnf install -y -q systemd >/dev/null
  dnf install -y -q "$(pick "$old" 'patchd-*.rpm')"
  upgrade() { dnf upgrade -y -q "$(pick "$new" 'patchd-*.rpm')"; }
fi

before=$(patchd --version)
sed -i 's/^filter = "security"/filter = "all"/' /etc/patchd/config.toml
grep -q '^filter = "all"' /etc/patchd/config.toml || fail "could not edit the config"
systemctl enable patchd-apply.timer
[ -e /etc/systemd/system/timers.target.wants/patchd-apply.timer ] || fail "enabling the timer did nothing"

upgrade

after=$(patchd --version)
echo "upgraded: $before -> $after"
[ "$before" != "$after" ] || fail "the version did not change"
grep -q '^filter = "all"' /etc/patchd/config.toml || fail "the local config edit was lost"
[ -e /etc/systemd/system/timers.target.wants/patchd-apply.timer ] || fail "the upgrade disabled the timer"
patchd config check
echo "upgrade OK"
