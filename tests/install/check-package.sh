#!/usr/bin/env bash
# Installs the .deb or .rpm in this container and checks what it put where.
#
#   tests/install/check-package.sh <dir containing patchd_*.deb and/or patchd-*.rpm, at any depth>
#
# Run as root in a throwaway debian, ubuntu, fedora or almalinux container.
set -euo pipefail

dir=$1
fail() { echo "FAIL: $*" >&2; exit 1; }

if command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq "$(find "$dir" -name 'patchd_*.deb' | head -1 | xargs realpath)" >/dev/null
  files=$(dpkg -L patchd)
  conffiles=$(dpkg-query -W -f='${Conffiles}' patchd)
  echo "$conffiles" | grep -q '/etc/patchd/config.toml' || fail "config.toml is not a conffile"
else
  dnf install -y -q "$(find "$dir" -name 'patchd-*.rpm' | head -1 | xargs realpath)"
  files=$(rpm -ql patchd)
  rpm -q --configfiles patchd | grep -qx '/etc/patchd/config.toml' || fail "config.toml is not a config file"
fi

for f in /usr/bin/patchd /etc/patchd/config.toml \
  /usr/lib/systemd/system/patchd-apply.service /usr/lib/systemd/system/patchd-apply.timer; do
  echo "$files" | grep -qx "$f" || fail "$f is not in the package"
done

patchd --version
patchd config check
# Installed, not enabled.
[ ! -e /etc/systemd/system/timers.target.wants/patchd-apply.timer ] || fail "the timer was enabled on install"
echo "package OK"
