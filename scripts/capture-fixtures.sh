#!/usr/bin/env bash
# Capture apt fixtures for the parser tests.
#
#   scripts/capture-fixtures.sh <image|ssh-host> <name>
#
# A target containing ':' is a container image (`debian:13`): the script runs
# `apt-get update` inside a throwaway container first, since images ship without package
# lists. Anything else is an ssh host (`pve1`, `pi@raspberrypi`), which is only read: run
# `sudo apt-get update` there yourself beforehand if the lists are stale.
#
# Writes crates/patchd-core/tests/fixtures/apt/<name>/ with the exact commands the apt
# provider runs, under LC_ALL=C. On ssh hosts the hostname is replaced with `example`.
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <image|ssh-host> <name>" >&2
  exit 2
fi
target=$1
name=$2
out="$(cd "$(dirname "$0")/.." && pwd)/crates/patchd-core/tests/fixtures/apt/$name"

# Runs on the target; prints a tar of the captured files on stdout.
remote=$(cat <<'EOF'
set -eu
export LC_ALL=C LANG=C
unset LANGUAGE || true
[ "${PATCHD_REFRESH:-0}" = 1 ] && apt-get update -q >/dev/null
dir=$(mktemp -d)
cat /etc/os-release > "$dir/os-release" 2>/dev/null || cat /usr/lib/os-release > "$dir/os-release"
dpkg-query -W -f='${binary:Package}\t${Version}\t${Architecture}\t${db:Status-Abbrev}\n' > "$dir/dpkg-query.txt"
apt-get -s -q dist-upgrade > "$dir/apt-get-s-dist-upgrade.txt"
held=$(apt-mark showhold)
if [ -n "$held" ]; then
  printf '%s\n' "$held" > "$dir/apt-mark-showhold.txt"
  # shellcheck disable=SC2086
  apt-cache policy $held > "$dir/apt-cache-policy.txt"
fi
hostname > "$dir/.hostname"
tar -C "$dir" -cf - .
rm -rf "$dir"
EOF
)

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

case $target in
  *:*)
    printf '%s
' "$remote" | docker run --rm -i -e PATCHD_REFRESH=1 "$target" sh -s | tar -C "$tmp" -xf -
    ;;
  *)
    printf '%s
' "$remote" | ssh "$target" sh -s | tar -C "$tmp" -xf -
    host=$(cat "$tmp/.hostname")
    if [ -n "$host" ]; then
      for f in "$tmp"/*; do
        sed -i "s/$host/example/g" "$f"
      done
    fi
    ;;
esac
rm -f "$tmp/.hostname"

mkdir -p "$out"
cp "$tmp"/* "$out/"
echo "wrote $out:"
ls -l "$out"
