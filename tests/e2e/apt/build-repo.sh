#!/usr/bin/env bash
# Builds the fake apt repositories the e2e scenarios install from.
#
#   tests/e2e/apt/build-repo.sh [/srv/repo]
#
# Three suites of tiny `Architecture: all` packages, in the standard `dists/` layout so
# apt sees a real suite name everywhere, including `apt-cache policy`:
#
#   patchd-test-base       version 1 of everything
#   patchd-test-security   pt-sec 2 (and the new pt-sec-dep it needs)
#   patchd-test-updates    version 2 of everything else, each with one failure mode
#
# The suite names make v0.2's security-origin rule see the security suite as one. Use:
#
#   deb [trusted=yes] file:/srv/repo patchd-test-security main
#
# Needs dpkg-deb and apt-ftparchive (apt-utils).
set -euo pipefail

out=${1:-/srv/repo}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# mkdeb <repo> <name> <version> [control lines] — the package's files come from
# $work/files/<name>-<version>/ and its maintainer scripts from $work/scripts/<name>-<version>/.
mkdeb() {
  local repo=$1 name=$2 version=$3 extra=${4:-}
  local dir="$work/build/$name-$version"
  rm -rf "$dir"
  mkdir -p "$dir/DEBIAN" "$out/pool/$repo"
  if [ -d "$work/files/$name-$version" ]; then
    cp -a "$work/files/$name-$version/." "$dir/"
  fi
  if [ -d "$work/scripts/$name-$version" ]; then
    cp -a "$work/scripts/$name-$version/." "$dir/DEBIAN/"
    for s in preinst postinst prerm postrm config; do
      [ -e "$dir/DEBIAN/$s" ] && chmod 0755 "$dir/DEBIAN/$s"
    done
  fi
  {
    echo "Package: $name"
    echo "Version: $version"
    echo "Architecture: all"
    echo "Maintainer: Patchd tests <tests@example.invalid>"
    echo "Section: misc"
    echo "Priority: optional"
    [ -n "$extra" ] && printf '%s\n' "$extra"
    echo "Description: Patchd e2e test package"
  } > "$dir/DEBIAN/control"
  if [ -d "$dir/etc" ]; then
    (cd "$dir" && find etc -type f | sed 's|^|/|') > "$dir/DEBIAN/conffiles"
  fi
  dpkg-deb --root-owner-group -Zgzip --build "$dir" "$out/pool/$repo/${name}_${version}_all.deb" >/dev/null
}

script() { # script <name>-<version> <maintscript> <body>
  mkdir -p "$work/scripts/$1"
  printf '#!/bin/sh\nset -e\n%s\n' "$3" > "$work/scripts/$1/$2"
}

file() { # file <name>-<version> <path> <content>
  mkdir -p "$work/files/$1$(dirname "$2")"
  printf '%s\n' "$3" > "$work/files/$1$2"
}

rm -rf "$out"

# --- base: version 1 ---
for p in pt-sec pt-reg pt-other pt-debconf pt-fail pt-slow pt-conflict; do
  mkdeb base "$p" 1
done
mkdeb base pt-meta 1 "Depends: pt-sec"
file pt-conf-1 /etc/pt-conf.conf "setting = 1"
mkdeb base pt-conf 1

# --- security: pt-sec 2 needs a new package ---
mkdeb security pt-sec-dep 1
mkdeb security pt-sec 2 "Depends: pt-sec-dep"

# --- updates: version 2, one failure mode each ---
mkdeb updates pt-reg 2
mkdeb updates pt-meta 2 "Depends: pt-sec"
# 5: a changed conffile; a locally edited copy must win.
file pt-conf-2 /etc/pt-conf.conf "setting = 2"
mkdeb updates pt-conf 2
# 6: a debconf question; noninteractive must take the default.
mkdir -p "$work/scripts/pt-debconf-2"
cat > "$work/scripts/pt-debconf-2/templates" <<'EOF'
Template: pt-debconf/answer
Type: boolean
Default: true
Description: Answer this question?
 Patchd's e2e tests ask this to prove nothing waits for an answer.
EOF
script pt-debconf-2 config '. /usr/share/debconf/confmodule
db_input high pt-debconf/answer || true
db_go || true'
script pt-debconf-2 postinst '. /usr/share/debconf/confmodule
db_get pt-debconf/answer
echo "$RET" > /var/lib/pt-debconf-answer'
mkdeb updates pt-debconf 2 "Depends: debconf"
# 7: conflicts with an installed package, so the install would remove it.
mkdeb updates pt-conflict 2 "Conflicts: pt-other"
# 8: postinst fails the first time it runs, then succeeds (so a repair can fix it).
script pt-fail-2 postinst 'if [ ! -e /var/lib/pt-fail-once ]; then
  touch /var/lib/pt-fail-once
  echo "pt-fail: failing on purpose" >&2
  exit 1
fi'
mkdeb updates pt-fail 2
# 11: a slow postinst, to deliver SIGTERM in the middle of it.
script pt-slow-2 postinst 'touch /var/lib/pt-slow-started
sleep 20
touch /var/lib/pt-slow-finished'
mkdeb updates pt-slow 2

for repo in base security updates; do
  suite="patchd-test-$repo"
  index="$out/dists/$suite/main/binary-all"
  mkdir -p "$index"
  (cd "$out" && apt-ftparchive packages "pool/$repo") > "$index/Packages"
  gzip -9 -k "$index/Packages"
  label=Patchd
  [ "$repo" = security ] && label=Patchd-Security
  apt-ftparchive     -o APT::FTPArchive::Release::Origin=Patchd     -o APT::FTPArchive::Release::Label="$label"     -o APT::FTPArchive::Release::Suite="$suite"     -o APT::FTPArchive::Release::Codename="$suite"     -o APT::FTPArchive::Release::Components=main     -o "APT::FTPArchive::Release::Architectures=all amd64 arm64"     release "$out/dists/$suite" > "$out/dists/$suite/Release.tmp"
  mv "$out/dists/$suite/Release.tmp" "$out/dists/$suite/Release"
done
echo "built $(find "$out" -name '*.deb' | wc -l) packages in $out"
