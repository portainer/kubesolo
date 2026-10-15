#!/usr/bin/env bash
# Generates labelled triage cases by breaking a KubeSolo node on purpose.
#
# Each fault is injected, held until KubeSolo records and triages an incident,
# and then undone; the incident is saved as a test case labelled with the
# action the fault calls for. The label comes from the fault, not from the
# model, so the model's own diagnosis is kept beside it only for reference.
#
# It runs on the node, as root, and BREAKS IT: run it only on a test node
# with the assistant and the API enabled.
#
#   generate.sh OUTDIR RUNS FAULT...
#
# Odd runs add noise workloads (a crash-looping pod and an unpullable image),
# so each fault is seen with and without unrelated warnings around it.
#
# Faults: kubelet-down kine-blocked runtime-unreachable killed disk-full
#         coredns-firewall db-corrupt


set -euo pipefail

OUT=${1:?usage: generate.sh OUTDIR RUNS FAULT...}
RUNS=${2:?usage: generate.sh OUTDIR RUNS FAULT...}
shift 2

DATA=/var/lib/kubesolo
SOCK=$DATA/config.sock
export KUBECONFIG=$DATA/pki/admin/admin.kubeconfig

# HOLD outlasts the watcher's 5-minute threshold and its 30-second checks.
HOLD=${HOLD:-420}
INCIDENT_TIMEOUT=${INCIDENT_TIMEOUT:-1500}

log() { echo "$(date -u +%T) $*" >&2; }
api() { curl -sf --unix-socket "$SOCK" "http://localhost$1"; }

wait_api() {
  local deadline=$((SECONDS + 600))
  until api /healthz >/dev/null 2>&1; do
    [ $SECONDS -lt $deadline ] || { log "the API did not come up"; return 1; }
    sleep 3
  done
}

healthy() { api /api/v1/assistant/report | jq -e '[.components[]] | all(. == "ok")' >/dev/null 2>&1; }

# wait_healthy waits until every component has reported ok for STEADY
# seconds: the watcher ends an outage only at a check that finds everything
# healthy, and a fault injected before then would join the previous outage
# instead of being an incident of its own.
STEADY=${STEADY:-90}
wait_healthy() {
  local deadline=$((SECONDS + 900)) since=""
  wait_api
  while [ $SECONDS -lt $deadline ]; do
    if healthy; then
      since=${since:-$SECONDS}
      [ $((SECONDS - since)) -ge "$STEADY" ] && return 0
    else
      since=""
    fi
    sleep 10
  done
  log "the node did not stay healthy"
  return 1
}

latest_id() { api /api/v1/assistant/incidents | jq -r 'last | .id // ""'; }

# wait_incident waits for a served (so triaged) incident after the cursor.
wait_incident() {
  local cursor=$1 deadline=$((SECONDS + INCIDENT_TIMEOUT)) id
  while [ $SECONDS -lt $deadline ]; do
    id=$(api "/api/v1/assistant/incidents?since=$cursor" 2>/dev/null | jq -r 'first | .id // ""' || true)
    [ -n "$id" ] && { echo "$id"; return; }
    sleep 15
  done
  return 1
}

save_case() {
  local id=$1 fault=$2 run=$3 actions=$4 mentions=$5
  mkdir -p "$OUT"
  api "/api/v1/assistant/incidents/$id" | jq \
    --arg fault "$fault" --argjson run "$run" --arg node "$(hostname)" \
    --argjson actions "$actions" --argjson mentions "$mentions" \
    '{incident: {trigger, time, snapshot},
      expect: {actions: $actions, mentions: $mentions},
      source: {fault: $fault, run: $run, node: $node, triage}}' >"$OUT/$fault-$run.json"
  log "saved $OUT/$fault-$run.json: $(jq -r '.source.triage | "\(.status) \(.action // "-") \(.rating // "-")"' "$OUT/$fault-$run.json")"
}

# A crash within 15 minutes of the last one counts as the same crash loop and
# is not recorded; each fault here is an incident of its own, and some faults
# end in a crash.
forget_last_crash() { rm -f "$DATA/assistant/last-crash"; }

noise_on() {
  kubectl create namespace inject-noise --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n inject-noise run crashloop --image=busybox:1.36 --restart=Always -- sh -c 'echo failing; exit 1' >/dev/null 2>&1 || true
  kubectl -n inject-noise run unpullable --image=registry.invalid/inject/missing:1 >/dev/null 2>&1 || true
}
# noise_off retries: right after a restart the API server is still starting.
noise_off() {
  local deadline=$((SECONDS + 600))
  until kubectl delete namespace inject-noise --ignore-not-found --wait=false >/dev/null 2>&1; do
    [ $SECONDS -lt $deadline ] || { log "could not remove the noise workloads"; return 0; }
    sleep 10
  done
}

# --- faults -----------------------------------------------------------------
# Each has inject_<fault> and restore_<fault>; restore must be safe to repeat.
# expect_<fault> prints the acceptable actions and the words the diagnosis
# should name, as two JSON arrays.

expect_kubelet_down() { echo '["restart_component"] ["kubelet"]'; }
inject_kubelet_down() { iptables -I INPUT -i lo -p tcp --dport 10248 -j REJECT; sleep "$HOLD"; }
restore_kubelet_down() { while iptables -D INPUT -i lo -p tcp --dport 10248 -j REJECT 2>/dev/null; do :; done; }

expect_kine_blocked() { echo '["restart_component"] ["kine"]'; }
inject_kine_blocked() { iptables -I INPUT -i lo -p tcp --dport 2379 -j REJECT; sleep "$HOLD"; }
restore_kine_blocked() { while iptables -D INPUT -i lo -p tcp --dport 2379 -j REJECT 2>/dev/null; do :; done; }

expect_runtime_unreachable() { echo '["restart_component"] ["containerd","runtime"]'; }
inject_runtime_unreachable() { mv "$DATA/containerd/containerd.sock" "$DATA/containerd/containerd.sock.inject"; sleep "$HOLD"; }
restore_runtime_unreachable() {
  [ -e "$DATA/containerd/containerd.sock.inject" ] && mv "$DATA/containerd/containerd.sock.inject" "$DATA/containerd/containerd.sock"
  return 0
}

expect_killed() { echo '["report_only"] []'; }
inject_killed() {
  kill -9 "$(systemctl show -p MainPID --value kubesolo)"
  sleep 5
  systemctl start kubesolo
}
restore_killed() { systemctl start kubesolo; }

expect_disk_full() { echo '["free_disk_space"] ["disk"]'; }
inject_disk_full() {
  local free
  free=$(df --output=avail -B1 "$DATA" | tail -1)
  fallocate -l $((free - 32 * 1024 * 1024)) "$DATA/inject-fill"
  sleep "$HOLD"
}
# The kubelet evicts pods under disk pressure and their replacements are
# evicted in turn; thousands of failed pods are left behind, held in the API
# server's memory, unless they are deleted.
restore_disk_full() {
  rm -f "$DATA/inject-fill"
  wait_api
  kubectl delete pods -A --field-selector=status.phase=Failed --wait=false >/dev/null 2>&1 || true
}

expect_coredns_firewall() { echo '["report_only"] ["firewall","network","api"]'; }
inject_coredns_firewall() {
  local selector
  selector=$(kubectl -n kube-system get deployment coredns -o jsonpath='{.spec.selector.matchLabels}' |
    jq -r 'to_entries | map("\(.key)=\(.value)") | join(",")')
  iptables -I INPUT -s 10.42.0.0/16 -p tcp --dport 6443 -j REJECT
  # A running CoreDNS keeps serving; a new one cannot sync and stays unready.
  kubectl -n kube-system delete pod -l "$selector" --wait=false >/dev/null
  sleep "$HOLD"
}
restore_coredns_firewall() { while iptables -D INPUT -s 10.42.0.0/16 -p tcp --dport 6443 -j REJECT 2>/dev/null; do :; done; }

# db-corrupt damages the datastore, lets KubeSolo fail on it, then puts the
# saved copy back: the incident is the crash, triaged once a run stays up.
expect_db_corrupt() { echo '["repair_database"] ["database","kine"]'; }
inject_db_corrupt() {
  systemctl stop kubesolo
  cp -a "$DATA/kine/db" "$DATA/kine/db.inject"
  local size
  size=$(stat -c %s "$DATA/kine/db/state.db")
  dd if=/dev/urandom of="$DATA/kine/db/state.db" bs=4096 seek=$((size / 4096 / 2)) count=16 conv=notrunc status=none
  systemctl start kubesolo
  sleep 120
  systemctl stop kubesolo
  rm -rf "$DATA/kine/db" && mv "$DATA/kine/db.inject" "$DATA/kine/db"
  systemctl start kubesolo
}
restore_db_corrupt() {
  if [ -d "$DATA/kine/db.inject" ]; then
    systemctl stop kubesolo
    rm -rf "$DATA/kine/db" && mv "$DATA/kine/db.inject" "$DATA/kine/db"
  fi
  systemctl start kubesolo
}

# --- driver -----------------------------------------------------------------

current=""
cleanup() {
  [ -n "$current" ] && "restore_${current//-/_}" || true
  noise_off
}
trap cleanup EXIT

for run in $(seq 1 "$RUNS"); do
  for fault in "$@"; do
    fn=${fault//-/_}
    declare -F "inject_$fn" >/dev/null || { log "unknown fault $fault"; exit 2; }
    read -r actions mentions < <("expect_$fn")

    log "run $run, $fault: waiting for a healthy node"
    wait_healthy
    if [ $((run % 2)) -eq 1 ]; then noise_on; sleep 60; fi
    cursor=$(latest_id)

    log "run $run, $fault: injecting"
    forget_last_crash
    current=$fault
    "inject_$fn"
    "restore_$fn"
    current=""
    noise_off

    log "run $run, $fault: waiting for the incident"
    if id=$(wait_incident "$cursor"); then
      save_case "$id" "$fault" "$run" "$actions" "$mentions"
    else
      log "run $run, $fault: no incident within ${INCIDENT_TIMEOUT}s"
    fi
  done
done
