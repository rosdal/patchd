#!/usr/bin/env bash
# Prepares a throwaway Fedora or AlmaLinux container for the dnf e2e scenarios: installs
# the test tools, builds the fake repositories, and makes them the only enabled
# repositories, so nothing from the real distribution is ever planned.
#
#   tests/e2e/dnf/setup-container.sh
#
# Run as root, inside a container only: it disables the container's repositories.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
dnf install -y -q rpm-build createrepo_c jq procps-ng util-linux tar gzip >/dev/null
if ! command -v bats >/dev/null; then
  # Not packaged on AlmaLinux without EPEL.
  dnf install -y -q bats >/dev/null 2>&1 || {
    curl -fsSL https://github.com/bats-core/bats-core/archive/refs/tags/v1.11.1.tar.gz | tar -xz -C /tmp
    /tmp/bats-core-1.11.1/install.sh /usr/local >/dev/null
  }
fi

bash "$here/build-repo.sh" /srv/repo

mkdir -p /etc/yum.repos.d/disabled
mv /etc/yum.repos.d/*.repo /etc/yum.repos.d/disabled/ 2>/dev/null || true
for repo in base updates; do
  printf '[patchd-test-%s]\nname=Patchd test %s\nbaseurl=file:///srv/repo/%s\nenabled=1\ngpgcheck=0\nmetadata_expire=0\n' \
    "$repo" "$repo" "$repo"
done > /etc/yum.repos.d/patchd-test.repo
dnf -q makecache >/dev/null
