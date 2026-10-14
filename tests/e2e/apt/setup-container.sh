#!/usr/bin/env bash
# Prepares a throwaway Debian or Ubuntu container for the apt e2e scenarios: installs
# the test tools, builds the fake repositories, and makes them the only apt sources, so
# nothing from the real archive is ever planned.
#
#   tests/e2e/apt/setup-container.sh
#
# Run as root, inside a container only: it deletes the container's apt sources.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq --no-install-recommends apt-utils bats jq util-linux procps debconf >/dev/null

bash "$here/build-repo.sh" /srv/repo

rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
for repo in base security updates; do
  echo "deb [trusted=yes] file:/srv/repo patchd-test-$repo main"
done > /etc/apt/sources.list.d/patchd-test.list
apt-get update -qq
