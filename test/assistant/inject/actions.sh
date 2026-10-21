#!/usr/bin/env bash
# Checks that the assistant carries out approved actions, and refuses those
# whose preconditions no longer hold, on a real node.
#
# It runs on the node, as root, and BREAKS IT: run it only on a test node with
# the assistant and the API enabled, kubectl and crictl installed.
#
#   actions.sh SCENARIO...
#
# Scenarios: restart-fails restart-succeeds refused repair prune orphans auto
#
# Except for restarts, which come from a real fault, each scenario writes an
# incident whose triage calls for its action: a datastore damaged while
# KubeSolo keeps running, say, cannot be produced on demand.

set -euo pipefail

DATA=/var/lib/kubesolo
SOCK=$DATA/config.sock
INCIDENTS=$DATA/assistant/incidents
export KUBECONFIG=$DATA/pki/admin/admin.kubeconfig
CRICTL=${CRICTL:-crictl --runtime-endpoint unix://$DATA/containerd/containerd.sock}

log() { echo "$(date -u +%T) $*" >&2; }
api() { curl -sf --unix-socket "$SOCK" "http://localhost$1"; }
fail() { log "FAIL: $*"; exit 1; }

wait_api() { until api /healthz >/dev/null 2>&1; do sleep 3; done; }
healthy() { api /api/v1/assistant/report | jq -e '[.components | to_entries[] | select(.key != "disk" and .key != "memory") | .value] | all(. == "ok")' >/dev/null 2>&1; }
wait_healthy() { wait_api; until healthy; do sleep 5; done; }

# synthetic writes a triaged incident calling for $1, rated high, and prints
# its ID.
synthetic() {
  local id
  id=$(date -u +%Y%m%dT%H%M%S.%NZ)
  mkdir -p "$INCIDENTS"
  jq -n --arg id "$id" --arg action "$1" --arg now "$(date -u +%FT%TZ)" '{
    id: $id, time: $now, kind: "kubesolo", trigger: ("actions.sh: " + $action),
    snapshot: {host: {diskUsedPercent: 0, memoryAvailableMiB: 0, uptimeSeconds: 0}},
    triage: {status: "triaged", time: $now, summary: ("a test of " + $action), cause: "actions.sh",
             action: $action, actionProbability: 0.95, rating: "high"}}' >"$INCIDENTS/$id.json"
  echo "$id"
}

# action_run waits until incident $1's action is no longer running, and prints
# its actionRun.
action_run() {
  local deadline=$((SECONDS + ${2:-1200})) run
  while [ $SECONDS -lt $deadline ]; do
    run=$(api "/api/v1/assistant/incidents/$1" 2>/dev/null | jq -c '.actionRun // empty' || true)
    if [ -n "$run" ] && [ "$(jq -r .status <<<"$run")" != running ]; then
      echo "$run"
      return
    fi
    sleep 10
  done
  fail "incident $1: the action did not finish"
}

expect_status() {
  local run=$1 want=$2
  [ "$(jq -r .status <<<"$run")" = "$want" ] || fail "want $want, got $run"
  log "ok: $want: $(jq -r '.reason // ""' <<<"$run")"
}

coredns_uid() { kubectl -n kube-system get pods -l k8s-app=coredns -o jsonpath='{.items[0].metadata.uid}'; }

# kubelet_incident blocks the kubelet's health port until the watcher records
# an incident and it is triaged, and prints its ID. The port stays blocked.
kubelet_incident() {
  local cursor id
  cursor=$(api /api/v1/assistant/incidents | jq -r 'last | .id // ""')
  iptables -I INPUT -i lo -p tcp --dport 10248 -j REJECT
  for _ in $(seq 150); do
    id=$(api "/api/v1/assistant/incidents?since=$cursor" | jq -r 'map(select(.kind == "kubesolo")) | first | .id // ""')
    [ -n "$id" ] && { echo "$id"; return; }
    sleep 10
  done
  fail "no kubelet incident"
}
unblock_kubelet() { while iptables -D INPUT -i lo -p tcp --dport 10248 -j REJECT 2>/dev/null; do :; done; }

approve() { kubesoloctl assistant approve "$1" --note "actions.sh" >&2 || fail "could not approve $1"; }

# wait_for runs its arguments until they succeed, or fails after $1 seconds.
wait_for() {
  local deadline=$((SECONDS + $1)); shift
  until "$@"; do
    [ $SECONDS -lt $deadline ] || fail "timed out waiting for: $*"
    sleep 2
  done
}
stopped() { ! systemctl is-active --quiet kubesolo; }
auto_started() { api "/api/v1/assistant/incidents/$1" 2>/dev/null | jq -e '.actionRun.auto == true' >/dev/null; }
kubelet_failing() { api /api/v1/assistant/report | jq -e '.components.kubelet != "ok"' >/dev/null 2>&1; }

# kubelet_down blocks the kubelet's health port until the probe fails, so that
# a restart's precondition holds.
kubelet_down() {
  iptables -I INPUT -i lo -p tcp --dport 10248 -j REJECT
  wait_for 120 kubelet_failing
}

scenario_restart_fails() {
  wait_healthy
  kubelet_down
  local id
  id=$(synthetic restart_component)
  approve "$id"
  # The kubelet stays unreachable: KubeSolo restarts but does not come back healthy.
  expect_status "$(action_run "$id" 1500)" failed
  unblock_kubelet
}

scenario_restart_succeeds() {
  wait_healthy
  local id before
  before=$(coredns_uid)
  kubelet_down
  id=$(synthetic restart_component)
  approve "$id"
  # The restart is what fixes it: undo the fault as soon as KubeSolo stops.
  wait_for 300 stopped
  unblock_kubelet
  wait_api
  expect_status "$(action_run "$id")" succeeded
  [ "$(coredns_uid)" = "$before" ] || fail "CoreDNS was recreated: pods did not survive the restart"
  log "ok: pods kept running across the restart"
}

scenario_refused() {
  wait_healthy
  local id
  id=$(synthetic restart_component)
  approve "$id"
  expect_status "$(action_run "$id")" refused
}

scenario_repair() {
  wait_healthy
  local id
  # A sound datastore is never repaired: repairing drops its latest writes.
  id=$(synthetic repair_database)
  approve "$id"
  expect_status "$(action_run "$id")" refused

  # The repair itself, as an approval of a damaged datastore starts it: a
  # restart job for the upgrade executor that repairs the datastore while
  # KubeSolo is stopped. A datastore damaged while KubeSolo runs cannot be
  # made on demand: SQLite serves its latest pages from the WAL.
  cp -a "$DATA/kine/db" /root/actions-db-backup
  local job version
  job=$(date -u +%Y%m%dT%H%M%SZ)-repair-test
  version=$(/usr/local/bin/kubesolo --version 2>&1 | jq -r 'select(.version) | .version' | head -1)
  mkdir -p "$DATA/upgrade"
  jq -n --arg id "$job" --arg v "$version" --arg data "$DATA" '{id: $id, operation: "restart",
    request: {version: $v, repairDatastore: true}, dataDir: $data, configFile: "/etc/kubesolo/config.yaml", from: $v}' >"$DATA/upgrade/job-$job.json"
  systemd-run --unit="kubesolo-upgrade-$job" --collect --quiet --property=KillMode=process \
    /usr/local/bin/kubesolo --upgrade-executor="$DATA/upgrade/job-$job.json"
  local deadline=$((SECONDS + 900)) result=""
  while [ $SECONDS -lt $deadline ]; do
    result=$(jq -r --arg id "$job" 'select(.last.id == $id) | .last.result' "$DATA/upgrade/state.json" 2>/dev/null || true)
    [ -n "$result" ] && break
    sleep 10
  done
  [ "$result" = succeeded ] || fail "the repair restart ended: ${result:-not at all}"
  [ -f "$DATA/upgrade/repair-$job/state.db" ] || fail "no backup of the datastore was kept"
  log "ok: repaired: the datastore was backed up to upgrade/repair-$job, and KubeSolo came back healthy"

  # Put back the writes the repair dropped.
  systemctl stop kubesolo
  rm -rf "$DATA/kine/db" && mv /root/actions-db-backup "$DATA/kine/db"
  rm -rf "$DATA/upgrade/repair-$job"
  systemctl start kubesolo
}

scenario_prune() {
  wait_healthy
  $CRICTL pull docker.io/library/nginx:1.27-alpine >/dev/null
  local before
  before=$($CRICTL images -q | wc -l)
  local free
  free=$(df --output=avail -B1 "$DATA" | tail -1)
  local total
  total=$(df --output=size -B1 "$DATA" | tail -1)
  # Fill to 92% used.
  fallocate -l $((free - total * 8 / 100)) "$DATA/actions-fill"
  sleep 35 # the disk check runs on demand; let the API settle
  local id
  id=$(synthetic prune_images)
  approve "$id"
  local run
  run=$(action_run "$id")
  rm -f "$DATA/actions-fill"
  expect_status "$run" succeeded
  $CRICTL images | grep -q nginx && fail "the unused image is still there"
  [ "$($CRICTL images -q | wc -l)" -lt "$before" ] || fail "no image was removed"
  $CRICTL images | grep -q coredns || fail "an image in use was removed"
  log "ok: the unused image was pruned, those in use kept"
}

scenario_orphans() {
  wait_healthy
  # A container cgroup the runtime does not run, holding a process, in a live
  # pod: what a restart that lost track of a container leaves running beside
  # the copy the kubelet started instead. The kubelet reaps whole pods it does
  # not know, but not this.
  local uid pod dir
  uid=$(coredns_uid)
  pod=$(find /sys/fs/cgroup/kubepods.slice -maxdepth 3 -type d -name "*pod${uid//-/_}.slice")
  [ -n "$pod" ] || fail "no cgroup for the CoreDNS pod"
  dir=$pod/cri-containerd-$(printf '0a%.0s' $(seq 32)).scope
  mkdir -p "$dir"
  sleep 3600 &
  local pid=$!
  echo "$pid" >"$dir/cgroup.procs"
  local id
  id=$(synthetic clean_orphaned_containers)
  approve "$id"
  expect_status "$(action_run "$id")" succeeded
  wait "$pid" 2>/dev/null || true
  kill -0 "$pid" 2>/dev/null && fail "the orphaned process is still running"
  rmdir "$dir" 2>/dev/null || true
  [ "$(coredns_uid)" = "$uid" ] || fail "the CoreDNS pod was recreated"
  api /api/v1/assistant/report | jq -e '.components.coredns == "ok"' >/dev/null || fail "CoreDNS is unhealthy"
  log "ok: the orphaned container's process was killed, its pod kept"
}

scenario_auto() {
  grep -q "autoRestart: true" /etc/kubesolo/config.yaml || fail "set assistant.autoRestart: true first"
  wait_healthy
  local id
  id=$(kubelet_incident)
  local triage
  triage=$(api "/api/v1/assistant/incidents/$id" | jq -r '"\(.triage.action) \(.triage.rating)"')
  if [ "$triage" != "restart_component high" ]; then
    unblock_kubelet; log "skipped: the model triaged it $triage, not a high-rated restart"; return
  fi
  # The restart starts as the incident is triaged, and KubeSolo is down for
  # seconds: wait for its record rather than for the stop.
  wait_for 300 auto_started "$id"
  unblock_kubelet
  wait_api
  local run
  run=$(action_run "$id")
  expect_status "$run" succeeded
  [ "$(jq -r .auto <<<"$run")" = true ] || fail "not marked automatic: $run"
  log "ok: restarted without approval"
}

trap 'unblock_kubelet; rm -f "$DATA/actions-fill"' EXIT
for s in "$@"; do
  log "=== $s"
  "scenario_${s//-/_}"
done
