#!/usr/bin/env bash
# Soak / stability loop. Boots KubeSolo once, then runs the manifest tiers
# repeatedly, asserting the cluster stays healthy across iterations:
#   - the KubeSolo container never restarts,
#   - no kube-system pod is crash-looping,
#   - idle memory does not grow unboundedly (a leak detector).
#
# Standalone nightly entrypoint (boots and tears down itself).
#
# Tunables:
#   SOAK_ITERATIONS    manifest-tier loops          (default: 5)
#   SOAK_RSS_GROWTH    allowed idle RSS growth, MiB (default: 64)
#   SOAK_MAX_RESTARTS  allowed kube-system restarts (default: 0)

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

SOAK_ITERATIONS="${SOAK_ITERATIONS:-5}"
SOAK_RSS_GROWTH="${SOAK_RSS_GROWTH:-64}"
SOAK_MAX_RESTARTS="${SOAK_MAX_RESTARTS:-0}"

trap 'bash "$HERE/teardown.sh"' EXIT
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

bash "$HERE/boot.sh"

settle_rss() { sleep 10; sample_rss_mib; }

# container restart count (engine-level): KubeSolo crashing would bump this.
container_restarts() { docker inspect -f '{{.RestartCount}}' "$(container_name)" 2>/dev/null || echo 0; }

# highest restartCount across kube-system pods (crash-loop detector).
system_pod_restarts() {
  kc -n kube-system get pods -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{"\n"}{end}' 2>/dev/null \
    | tr ' ' '\n' | sort -rn | head -1 || echo 0
}

rss0="$(settle_rss)"
restarts0="$(container_restarts)"
log "baseline: idle RSS ${rss0} MiB, container restarts ${restarts0}"

for i in $(seq 1 "$SOAK_ITERATIONS"); do
  log "── soak iteration $i/$SOAK_ITERATIONS ──"
  bash "$HERE/manifests.sh" || { dump_diagnostics; fail "manifest tiers failed on iteration $i"; }

  cr="$(container_restarts)"
  if [ "$((cr - restarts0))" -gt "$SOAK_MAX_RESTARTS" ]; then
    dump_diagnostics; fail "KubeSolo container restarted during soak (count $cr)"
  fi

  spr="$(system_pod_restarts)"; spr="${spr:-0}"
  if [ "$spr" -gt "$SOAK_MAX_RESTARTS" ]; then
    dump_diagnostics; fail "kube-system pod crash-looping (max restartCount $spr)"
  fi

  rss="$(settle_rss)"
  log "iteration $i: idle RSS ${rss} MiB (Δ from baseline $((rss - rss0)) MiB)"
done

rss_final="$(settle_rss)"
growth=$((rss_final - rss0))
log "idle RSS: ${rss0} → ${rss_final} MiB over $SOAK_ITERATIONS iterations (Δ ${growth} MiB)"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### KubeSolo soak ($(uname -m), ${SOAK_ITERATIONS} iterations)"
    echo ""
    echo "| metric | value |"
    echo "|--------|-------|"
    echo "| idle RSS start | ${rss0} MiB |"
    echo "| idle RSS end | ${rss_final} MiB |"
    echo "| idle RSS growth | ${growth} MiB (limit ${SOAK_RSS_GROWTH}) |"
    echo "| container restarts | $(container_restarts) |"
  } >> "$GITHUB_STEP_SUMMARY"
fi

if [ "$growth" -gt "$SOAK_RSS_GROWTH" ]; then
  fail "idle RSS grew ${growth} MiB (> ${SOAK_RSS_GROWTH} MiB) — possible leak"
fi
ok "soak passed: stable across $SOAK_ITERATIONS iterations, RSS growth ${growth} MiB"
