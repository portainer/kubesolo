#!/usr/bin/env bash
# Shared helpers and configuration for the KubeSolo e2e harness.
#
# This file is sourced by boot.sh / smoke.sh / teardown.sh / run.sh. It never
# runs anything on its own.
#
# Tunables (all overridable from the environment):
#   IMAGE        container image under test (default: portainerci/kubesolo:develop)
#   KUBESOLOCTL  path to the kubesoloctl binary           (default: kubesoloctl on PATH)
#   KUBECTL      path to the kubectl binary               (default: kubectl on PATH)
#   KS_NAME      KubeSolo instance name / kube context    (default: kubesolo)
#   READY_TIMEOUT  seconds to wait for node/system readiness (default: 240)

set -euo pipefail

IMAGE="${IMAGE:-portainerci/kubesolo:develop}"
KUBESOLOCTL="${KUBESOLOCTL:-kubesoloctl}"
KUBECTL="${KUBECTL:-kubectl}"
KS_NAME="${KS_NAME:-kubesolo}"
KS_CONTEXT="${KS_CONTEXT:-}"   # resolved lazily on first kc() call
READY_TIMEOUT="${READY_TIMEOUT:-240}"

# _resolve_ctx finds the kube context kubesoloctl merged for this instance.
# kubesoloctl names it "kubernetes-admin@<container name>" (kubesolo, or
# kubesolo-<name>); older releases used "kubernetes-admin@<name>". We prefer
# those exact names, then fall back to any context whose name ends with
# "@<name>", then to the bare name. Resolved once, then cached.
_resolve_ctx() {
  [ -n "$KS_CONTEXT" ] && return
  local want
  for want in "kubernetes-admin@$(container_name)" "kubernetes-admin@$KS_NAME"; do
    if "$KUBECTL" config get-contexts -o name 2>/dev/null | grep -qx "$want"; then
      KS_CONTEXT="$want"
      return
    fi
  done
  # `|| true`: under `set -euo pipefail`, grep exiting non-zero on no match
  # would otherwise abort the script before the fallback below runs.
  KS_CONTEXT="$("$KUBECTL" config get-contexts -o name 2>/dev/null | grep -E "@${KS_NAME}\$" | head -1 || true)"
  [ -n "$KS_CONTEXT" ] || KS_CONTEXT="$KS_NAME"
}

# kc runs kubectl against the merged ~/.kube/config in the KubeSolo context.
kc() { _resolve_ctx; "$KUBECTL" --context "$KS_CONTEXT" "$@"; }

log()  { printf '\033[1;36m[e2e]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ ok]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[err]\033[0m %s\n' "$*" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

# wait_for <timeout-s> <interval-s> <desc> <cmd...> — poll until cmd succeeds or
# the timeout elapses. Uses the bash SECONDS builtin (portable; no /proc needed).
# Needed because `kubectl wait` errors immediately when a resource does not yet
# exist (e.g. the Node before the kubelet registers it).
wait_for() {
  local timeout="$1" interval="$2" desc="$3"; shift 3
  local start=$SECONDS
  until "$@"; do
    if [ $((SECONDS - start)) -ge "$timeout" ]; then
      log "timed out after ${timeout}s waiting for: $desc"
      return 1
    fi
    sleep "$interval"
  done
}

# container_name maps the instance name to the engine container name, matching
# kubesoloctl's ContainerNameFor (default instance keeps the bare name).
container_name() {
  if [ "$KS_NAME" = "kubesolo" ]; then echo "kubesolo"; else echo "kubesolo-$KS_NAME"; fi
}

# to_mib converts a "<num><unit>" docker/k8s size string to integer MiB.
to_mib() {
  awk -v s="$1" 'BEGIN{
    n=s+0; u=s; gsub(/[0-9.]/,"",u);
    if (u ~ /GiB|GB|G/) n*=1024; else if (u ~ /KiB|KB|kB|K/) n/=1024;
    printf "%d", (n+0.5)
  }'
}

# sample_rss_mib reports the whole-container memory usage at this instant, in MiB.
sample_rss_mib() {
  local raw; raw="$(docker stats --no-stream --format '{{.MemUsage}}' "$(container_name)" | awk '{print $1}')"
  to_mib "$raw"
}

# cpu_usage_usec reports the container cgroup's cumulative CPU time in µs. On
# cgroup v2 KubeSolo nests itself and its pods under the container's cgroup, so
# this covers the whole node, not only the kubesolo process.
cpu_usage_usec() {
  docker exec "$(container_name)" cat /sys/fs/cgroup/cpu.stat | awk '$1 == "usage_usec" {print $2}'
}

# measure_cpu_cores <seconds> reports the average cores the node used over the
# window, e.g. 0.031.
measure_cpu_cores() {
  local secs="$1" start end
  start="$(cpu_usage_usec)"
  sleep "$secs"
  end="$(cpu_usage_usec)"
  awk -v a="$start" -v b="$end" -v s="$secs" 'BEGIN{printf "%.3f", (b - a) / 1e6 / s}'
}

# api_request_total reports the API server's cumulative request count.
api_request_total() {
  kc get --raw /metrics 2>/dev/null | awk '/^apiserver_request_total\{/ {n += $NF} END {printf "%d", n}'
}

# goroutine_count reports the goroutines in the kubesolo process. The API server
# serves pprof for the whole process, since every component runs inside it.
goroutine_count() {
  kc get --raw '/debug/pprof/goroutine?debug=1' 2>/dev/null | awk 'NR == 1 {print $NF}'
}

# capture_profiles <dir> saves a 30s CPU profile, a heap profile and a goroutine
# dump of the kubesolo process, for diagnosing a failed performance gate.
capture_profiles() {
  local dir="$1"
  mkdir -p "$dir"
  log "capturing profiles into $dir"
  kc get --raw '/debug/pprof/profile?seconds=30' > "$dir/cpu.pprof" 2>/dev/null || true
  kc get --raw '/debug/pprof/heap' > "$dir/heap.pprof" 2>/dev/null || true
  kc get --raw '/debug/pprof/goroutine?debug=1' > "$dir/goroutines.txt" 2>/dev/null || true
}

# dump_diagnostics prints everything useful for debugging a failed run. Safe to
# call even when the cluster never came up.
dump_diagnostics() {
  log "──────── diagnostics ────────"
  local cname; cname="$(container_name)"
  echo "### container ($cname) logs (tail) ###"
  docker logs --tail 200 "$cname" 2>&1 || true
  echo "### nodes ###"
  kc get nodes -o wide 2>&1 || true
  echo "### all resources ###"
  kc get all --all-namespaces 2>&1 || true
  echo "### recent events ###"
  kc get events --all-namespaces --sort-by=.lastTimestamp 2>&1 | tail -40 || true
  log "─────────────────────────────"
}
