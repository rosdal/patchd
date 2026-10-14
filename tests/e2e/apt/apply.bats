#!/usr/bin/env bats
# apt apply scenarios against the fake repositories from build-repo.sh.
#
# Run as root in a container prepared by setup-container.sh:
#   PATCHD=/path/to/patchd bats tests/e2e/apt
#
# Each test starts from the same state: every pt-* package purged and every marker
# removed, so the order of the tests does not matter.

PATCHD=${PATCHD:-patchd}
# A hung prompt must fail the test, not the CI job.
APPLY_TIMEOUT=300

setup() {
  export DEBIAN_FRONTEND=noninteractive
  pkgs=$(dpkg-query -W -f='${Package}\n' 'pt-*' 2>/dev/null || true)
  if [ -n "$pkgs" ]; then
    # shellcheck disable=SC2086
    apt-mark unhold $pkgs >/dev/null
    # shellcheck disable=SC2086
    dpkg --purge --force-all $pkgs >/dev/null 2>&1 || true
  fi
  rm -f /var/lib/pt-* /etc/pt-conf.conf*
  rm -rf /run/patchd /var/log/patchd
  # A cached .deb would hide a download failure.
  apt-get clean
  [ -d /srv/repo ] || { echo "run setup-container.sh first" >&2; return 1; }
  apt-get update -qq
  config=$(mktemp)
  printf '[apply]\nrefresh = true\n' > "$config"
}

teardown() {
  rm -f "$config"
}

install_v1() {
  apt-get install -y -qq "$@" >/dev/null
}

# apply [args…]: runs patchd apply with the test config; sets $status and $output.
apply() {
  run timeout "$APPLY_TIMEOUT" "$PATCHD" --config "$config" apply --yes "$@"
}

version_of() {
  dpkg-query -W -f='${Version}' "$1"
}

last_result() {
  "$PATCHD" history --json --limit 1 | jq -r '.[0].result'
}

status_sum() {
  sha256sum /var/lib/dpkg/status | cut -d' ' -f1
}

@test "1: security filter upgrades only the security update" {
  install_v1 pt-sec=1 pt-reg=1
  apply --security
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(version_of pt-sec)" = 2 ]
  [ "$(version_of pt-sec-dep)" = 1 ]
  [ "$(version_of pt-reg)" = 1 ]
  [ "$(last_result)" = success ]
}

@test "2: all filter upgrades both" {
  install_v1 pt-sec=1 pt-reg=1
  apply --all
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(version_of pt-sec)" = 2 ]
  [ "$(version_of pt-reg)" = 2 ]
}

@test "3: an automatically installed package stays automatic" {
  install_v1 pt-meta=1 pt-sec=1
  apt-mark manual pt-meta >/dev/null
  apt-mark auto pt-sec >/dev/null
  apply --security
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(version_of pt-sec)" = 2 ]
  apt-mark showauto | grep -qx pt-sec
}

@test "4: a held package is skipped and listed as held" {
  install_v1 pt-sec=1
  apt-mark hold pt-sec >/dev/null
  run "$PATCHD" --config "$config" plan --security
  echo "$output"
  [ "$status" -eq 0 ]
  held_row='held +pt-sec'
  [[ "$output" =~ $held_row ]]
  [[ "$output" == *"skip 1 held"* ]]
  apply --security
  [ "$status" -eq 0 ]
  [ "$(version_of pt-sec)" = 1 ]
}

@test "5: a locally edited conffile is kept, the new one written as .dpkg-dist" {
  install_v1 pt-conf=1
  echo "setting = local" > /etc/pt-conf.conf
  apply --all
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(version_of pt-conf)" = 2 ]
  [ "$(cat /etc/pt-conf.conf)" = "setting = local" ]
  [ "$(cat /etc/pt-conf.conf.dpkg-dist)" = "setting = 2" ]
}

@test "6: a debconf question takes its default without waiting" {
  install_v1 pt-debconf=1
  apply --all
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(version_of pt-debconf)" = 2 ]
  [ "$(cat /var/lib/pt-debconf-answer)" = true ]
}

@test "7: a conflict aborts at the simulation with dpkg untouched" {
  install_v1 pt-conflict=1 pt-other=1
  before=$(status_sum)
  apply --all
  echo "$output"
  [ "$status" -eq 4 ]
  [ "$(status_sum)" = "$before" ]
  [ "$(last_result)" = aborted_preflight ]
  [[ "$output" == *"would remove pt-other"* ]]
}

@test "8: a failing postinst is partial, and the next run needs repair" {
  install_v1 pt-fail=1
  apply --all
  echo "$output"
  [ "$status" -eq 1 ]
  [ "$(last_result)" = partial ]

  apply --all
  echo "$output"
  [ "$status" -eq 4 ]
  [[ "$output" == *"repair = true"* ]]

  printf '[apply]\nrefresh = true\nrepair = true\n' > "$config"
  apply --all
  echo "$output"
  [ "$status" -eq 0 ]
  [ -z "$(dpkg --audit)" ]
}

@test "9: a held lock exits 3 and runs nothing" {
  install_v1 pt-sec=1
  mkdir -p /run/patchd
  flock /run/patchd/lock sleep 30 &
  holder=$!
  sleep 1
  before=$(status_sum)
  apply --all
  kill "$holder"
  echo "$output"
  [ "$status" -eq 3 ]
  [[ "$output" == *"another patchd run is active"* ]]
  [ "$(status_sum)" = "$before" ]
  [ ! -d /var/log/patchd/runs ] || [ -z "$(ls /var/log/patchd/runs)" ]
}

@test "10: a repository gone after planning aborts at download with dpkg untouched" {
  install_v1 pt-sec=1
  printf '[apply]\nrefresh = false\n' > "$config"
  apt-get update -qq
  # The indexes stay, the packages go: the plan and simulation still see version 2.
  find /srv/repo -name '*.deb' -exec rm {} +
  before=$(status_sum)
  apply --all
  bash "$(dirname "$BATS_TEST_FILENAME")/build-repo.sh" /srv/repo >/dev/null
  echo "$output"
  [ "$status" -eq 1 ]
  [ "$(status_sum)" = "$before" ]
  [ "$(last_result)" = failed ]
  [[ "$output" == *"download failed"* ]]
}

@test "11: SIGTERM during a slow postinst lets dpkg finish" {
  install_v1 pt-slow=1
  timeout "$APPLY_TIMEOUT" "$PATCHD" --config "$config" apply --yes --all > /tmp/apply.out 2>&1 &
  pid=$!
  for _ in $(seq 120); do
    [ -e /var/lib/pt-slow-started ] && break
    sleep 0.5
  done
  [ -e /var/lib/pt-slow-started ]
  pkill -TERM -x patchd
  wait "$pid" || code=$?
  cat /tmp/apply.out
  [ "${code:-0}" -eq 1 ]
  [ -e /var/lib/pt-slow-finished ]
  [ "$(version_of pt-slow)" = 2 ]
  [ -z "$(dpkg --audit)" ]
  [ "$(last_result)" = interrupted ]
}

@test "history and status read the run back" {
  install_v1 pt-sec=1
  apply --security
  [ "$status" -eq 0 ]
  run "$PATCHD" history
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"success"* ]]
  id=$("$PATCHD" history --json | jq -r '.[0].run_id')
  run "$PATCHD" history "${id:0:8}"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"step install: exit 0"* ]]
  run "$PATCHD" status
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"last run"*"success"* ]]
}

@test "run log files are private and one JSON object per line" {
  install_v1 pt-sec=1
  apply --security
  [ "$status" -eq 0 ]
  [ "$(stat -c %a /var/log/patchd/runs)" = 750 ]
  f=$(ls /var/log/patchd/runs/*.jsonl)
  [ "$(stat -c %a "$f")" = 640 ]
  jq -e -s 'all(.[]; has("run_id") and has("ts") and has("event"))' "$f"
  [ "$(jq -r -s '.[0].event' "$f")" = run_started ]
  [ "$(jq -r -s '.[-1].event' "$f")" = run_finished ]
}
