#!/usr/bin/env bats
# dnf apply scenarios against the fake repositories from build-repo.sh: the numbers match
# the apt scenarios in tests/e2e/apt/apply.bats.
#
# Run as root in a container prepared by setup-container.sh:
#   PATCHD=/path/to/patchd bats tests/e2e/dnf

PATCHD=${PATCHD:-patchd}
APPLY_TIMEOUT=300

setup() {
  pkgs=$(rpm -qa --qf '%{NAME}\n' 'pt-*' || true)
  if [ -n "$pkgs" ]; then
    # shellcheck disable=SC2086
    rpm -e --nodeps --noscripts $pkgs
  fi
  rm -f /var/lib/pt-*
  rm -rf /run/patchd /var/log/patchd
  dnf -q clean packages >/dev/null
  config=$(mktemp)
  printf '[apply]\nrefresh = true\n' > "$config"
}

teardown() {
  rm -f "$config"
}

install_v1() {
  dnf install -y -q "$@" >/dev/null
}

apply() {
  run timeout "$APPLY_TIMEOUT" "$PATCHD" --config "$config" apply --yes "$@"
}

version_of() {
  rpm -q --qf '%{VERSION}' "$1"
}

# Success is 0, or 20 when needs-restarting reports a reboot: in a container it often
# does, because setting the container up upgraded core libraries.
succeeded() {
  [ "$status" -eq 0 ] || [ "$status" -eq 20 ]
}

last_result() {
  "$PATCHD" history --json --limit 1 | jq -r '.[0].result'
}

rpmdb_sum() {
  rpm -qa | sort | sha256sum | cut -d' ' -f1
}

@test "1: security filter upgrades only the security update" {
  install_v1 pt-sec-1 pt-reg-1
  apply --security
  echo "$output"
  succeeded
  [ "$(version_of pt-sec)" = 2 ]
  [ "$(version_of pt-reg)" = 1 ]
  [[ "$(last_result)" == success* ]]
}

@test "2: all filter upgrades both" {
  install_v1 pt-sec-1 pt-reg-1
  apply --all
  echo "$output"
  succeeded
  [ "$(version_of pt-sec)" = 2 ]
  [ "$(version_of pt-reg)" = 2 ]
}

@test "7: a conflict aborts at the simulation with nothing installed" {
  install_v1 pt-conflict-1 pt-other-1
  before=$(rpmdb_sum)
  apply --all
  echo "$output"
  [ "$status" -eq 4 ]
  [ "$(rpmdb_sum)" = "$before" ]
  [ "$(last_result)" = aborted_preflight ]
}

@test "8: a failing %post makes the run partial" {
  install_v1 pt-fail-1
  apply --all
  echo "$output"
  [ "$status" -eq 1 ]
  [ "$(last_result)" = partial ]
  # rpm installs the package anyway; the next run has nothing to do.
  [ "$(version_of pt-fail)" = 2 ]
  apply --all
  succeeded
}

@test "9: a held lock exits 3 and runs nothing" {
  install_v1 pt-sec-1
  mkdir -p /run/patchd
  flock /run/patchd/lock sleep 30 &
  holder=$!
  sleep 1
  before=$(rpmdb_sum)
  apply --all
  kill "$holder"
  echo "$output"
  [ "$status" -eq 3 ]
  [[ "$output" == *"another patchd run is active"* ]]
  [ "$(rpmdb_sum)" = "$before" ]
}
