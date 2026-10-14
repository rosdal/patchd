#!/usr/bin/env bash
# Capture dnf fixtures for the dnf provider's parser tests.
#
#   scripts/capture-dnf-fixtures.sh <image> <name>
#
# Runs in a throwaway container (`fedora:latest`, `almalinux:10`), refreshes the
# metadata, and records the exact commands the dnf provider runs, under LC_ALL=C, into
# crates/patchd-core/tests/fixtures/dnf/<name>/. Each command's exit code goes into
# <file>.exit, because dnf exits 1 when `--assumeno` declines a transaction.
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <image> <name>" >&2
  exit 2
fi
image=$1
name=$2
out="$(cd "$(dirname "$0")/.." && pwd)/crates/patchd-core/tests/fixtures/dnf/$name"

remote=$(cat <<'EOF'
set -u
export LC_ALL=C LANG=C
dir=$(mktemp -d)
tab=$(printf '\t')
qf="%{name}${tab}%{epoch}:%{version}-%{release}${tab}%{arch}${tab}%{repoid}
"
rec() { # rec <file> <command…>
  local f=$1; shift
  "$@" > "$dir/$f" 2>/dev/null
  echo $? > "$dir/$f.exit"
}
dnf -q makecache >/dev/null 2>&1
cat /etc/os-release > "$dir/os-release"
rec dnf-version.txt dnf --version
rec rpm-qa.txt rpm -qa --qf "%{NAME}${tab}%{EPOCHNUM}:%{VERSION}-%{RELEASE}${tab}%{ARCH}
"
rec repoquery-upgrades.txt dnf -q repoquery --upgrades --latest-limit=1 --qf "$qf"
rec repoquery-security.txt dnf -q repoquery --upgrades --security --latest-limit=1 --qf "$qf"
rec upgrade-assumeno.txt dnf upgrade --assumeno
# The pinned form apply uses, for the first two security updates.
nevras=$(awk -F'\t' 'NF >= 3 { split($2, v, ":"); e = v[1]; sub(/^[^:]*:/, "", $2); print $1 "-" e ":" $2 "." $3 }' "$dir/repoquery-security.txt" | head -2 | tr '\n' ' ')
echo "$nevras" > "$dir/nevras.txt"
# shellcheck disable=SC2086
rec upgrade-assumeno-nevra.txt dnf upgrade --assumeno $nevras
rec needs-restarting.txt dnf needs-restarting -r
tar -C "$dir" -cf - .
EOF
)

mkdir -p "$out"
rm -f "$out"/*
MSYS_NO_PATHCONV=1 docker run --rm -i "$image" bash -c "$remote" | tar -C "$out" -xf -
echo "wrote $out"
ls "$out"
