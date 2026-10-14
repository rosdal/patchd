#!/usr/bin/env bash
# Builds the fake dnf repositories the e2e scenarios install from.
#
#   tests/e2e/dnf/build-repo.sh [/srv/repo]
#
# Two repositories of tiny noarch RPMs:
#
#   base/      version 1 of everything
#   updates/   version 2 of everything, with an updateinfo.xml that marks pt-sec 2 as a
#              security advisory, so `repoquery --security` sees it
#
# Needs rpmbuild (rpm-build), createrepo_c and modifyrepo_c.
set -euo pipefail

out=${1:-/srv/repo}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# mkrpm <repo> <name> <version> [preamble lines] [%post body]
mkrpm() {
  local repo=$1 name=$2 version=$3 extra=${4:-} post=${5:-}
  local spec="$work/$name-$version.spec"
  {
    echo "Name: $name"
    echo "Version: $version"
    echo "Release: 1"
    echo "Summary: Patchd e2e test package"
    echo "License: Apache-2.0"
    echo "BuildArch: noarch"
    [ -n "$extra" ] && printf '%s\n' "$extra"
    echo "%description"
    echo "Patchd e2e test package."
    echo "%install"
    echo "mkdir -p %{buildroot}/usr/share/$name"
    echo "echo $version > %{buildroot}/usr/share/$name/version"
    echo "%files"
    echo "/usr/share/$name/version"
    if [ -n "$post" ]; then
      echo "%post"
      printf '%s\n' "$post"
    fi
  } > "$spec"
  rpmbuild -bb --quiet --define "_topdir $work/rpmbuild" --undefine source_date_epoch_from_changelog "$spec"
  mkdir -p "$out/$repo"
  mv "$work"/rpmbuild/RPMS/noarch/"$name-$version"-1.noarch.rpm "$out/$repo/"
}

rm -rf "$out"
for p in pt-sec pt-reg pt-other pt-conflict pt-fail; do
  mkrpm base "$p" 1
done

mkrpm updates pt-sec 2
mkrpm updates pt-reg 2
# 7: conflicts with an installed package.
mkrpm updates pt-conflict 2 "Conflicts: pt-other"
# 8: %post fails the first time it runs.
mkrpm updates pt-fail 2 "" 'if [ ! -e /var/lib/pt-fail-once ]; then
  touch /var/lib/pt-fail-once
  echo "pt-fail: failing on purpose" >&2
  exit 1
fi'

createrepo_c -q "$out/base"
createrepo_c -q "$out/updates"
cat > "$work/updateinfo.xml" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<updates>
  <update from="tests@example.invalid" status="stable" type="security" version="1">
    <id>PATCHD-2026-0001</id>
    <title>pt-sec security update</title>
    <issued date="2026-10-14 00:00:00"/>
    <severity>Important</severity>
    <description>Patchd e2e security advisory.</description>
    <pkglist>
      <collection short="patchd">
        <name>patchd</name>
        <package name="pt-sec" version="2" release="1" epoch="0" arch="noarch">
          <filename>pt-sec-2-1.noarch.rpm</filename>
        </package>
      </collection>
    </pkglist>
  </update>
</updates>
EOF
modifyrepo_c --mdtype=updateinfo "$work/updateinfo.xml" "$out/updates/repodata" >/dev/null
echo "built $(find "$out" -name '*.rpm' | wc -l) packages in $out"
