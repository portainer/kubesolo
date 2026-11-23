#!/usr/bin/env bash
# Soak / stability loop. Boots KubeSolo once, then runs the manifest tiers
# repeatedly, asserting the cluster stays healthy across iterations:
#   - the KubeSolo container never restarts,
#   - no kube-system pod is crash-looping,
#   - idle memory and goroutines do not grow unboundedly (leak detectors),
#   - the node goes back to idle CPU once the last iteration ends.
#
# Standalone nightly entrypoint (boots and tears down itself).
#
# Tunables:
#   SOAK_ITERATIONS    manifest-tier loops          (default: 5)
#   SOAK_DURATION      loop for this many seconds instead of SOAK_ITERATIONS
#   SOAK_RSS_GROWTH    allowed idle RSS growth after iteration 1, MiB (default: 64)
#   SOAK_GOROUTINE_GROWTH  allowed goroutine growth after iteration 1 (default: 200)
#   SOAK_MAX_RESTARTS  allowed kube-system restarts (default: 0)
#   SOAK_SAMPLES       per-iteration samples CSV    (default: test/e2e/soak-samples.csv)
#   IDLE_CPU_MAX       idle CPU ceiling at the end, cores (default: 0.25)
#   PROFILE_DIR        where profiles are saved when a gate fails (default: test/e2e/profiles)

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

SOAK_ITERATIONS="${SOAK_ITERATIONS:-5}"
SOAK_RSS_GROWTH="${SOAK_RSS_GROWTH:-64}"
SOAK_MAX_RESTARTS="${SOAK_MAX_RESTARTS:-0}"
SOAK_DURATION="${SOAK_DURATION:-}"
SOAK_GOROUTINE_GROWTH="${SOAK_GOROUTINE_GROWTH:-200}"
SOAK_SAMPLES="${SOAK_SAMPLES:-$HERE/soak-samples.csv}"
IDLE_CPU_MAX="${IDLE_CPU_MAX:-0.25}"
PROFILE_DIR="${PROFILE_DIR:-$HERE/profiles}"

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
goroutines0="$(goroutine_count)"
restarts0="$(container_restarts)"
log "baseline: idle RSS ${rss0} MiB, ${goroutines0} goroutines, container restarts ${restarts0}"
echo "iteration,elapsed_s,idle_rss_mib,goroutines,cpu_cores" > "$SOAK_SAMPLES"
echo "0,0,$rss0,$goroutines0," >> "$SOAK_SAMPLES"

# more_iterations decides whether to run iteration $1: by wall clock when
# SOAK_DURATION is set (the weekly long soak), else by count.
SOAK_START=$SECONDS
more_iterations() {
  if [ -n "$SOAK_DURATION" ]; then [ $((SECONDS - SOAK_START)) -lt "$SOAK_DURATION" ]
  else [ "$1" -le "$SOAK_ITERATIONS" ]; fi
}

i=0
while more_iterations $((i + 1)); do
  i=$((i + 1))
  log "── soak iteration $i ──"
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
  goroutines="$(goroutine_count)"
  cpu="$(measure_cpu_cores 20)"
  echo "$i,$((SECONDS - SOAK_START)),$rss,$goroutines,$cpu" >> "$SOAK_SAMPLES"
  log "iteration $i: idle RSS ${rss} MiB (Δ $((rss - rss0))), ${goroutines} goroutines (Δ $((goroutines - goroutines0))), ${cpu} cores"
  # The first pass warms caches and pulls images (~70 MiB on a 4 vCPU VM), so
  # growth is measured from the end of it: a leak keeps growing after warm-up.
  if [ "$i" -eq 1 ]; then rss_warm="$rss"; goroutines_warm="$goroutines"; fi
done

rss_final="$(settle_rss)"
growth=$((rss_final - rss_warm))
goroutines_final="$(goroutine_count)"
goroutine_growth=$((goroutines_final - goroutines_warm))
cpu_final="$(measure_cpu_cores 60)"
log "idle RSS: ${rss0} cold, ${rss_warm} after iteration 1, ${rss_final} MiB at the end of $i iterations (Δ since warm-up ${growth} MiB)"
log "goroutines: ${goroutines_warm} after iteration 1 → ${goroutines_final} (Δ ${goroutine_growth}); idle CPU ${cpu_final} cores"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### KubeSolo soak ($(uname -m), ${i} iterations)"
    echo ""
    echo "| metric | value |"
    echo "|--------|-------|"
    echo "| idle RSS cold | ${rss0} MiB |"
    echo "| idle RSS after iteration 1 | ${rss_warm} MiB |"
    echo "| idle RSS end | ${rss_final} MiB |"
    echo "| idle RSS growth since warm-up | ${growth} MiB (limit ${SOAK_RSS_GROWTH}) |"
    echo "| goroutine growth | ${goroutine_growth} (limit ${SOAK_GOROUTINE_GROWTH}) |"
    echo "| idle CPU at end | ${cpu_final} cores (limit ${IDLE_CPU_MAX}) |"
    echo "| container restarts | $(container_restarts) |"
  } >> "$GITHUB_STEP_SUMMARY"
fi

if [ "$growth" -gt "$SOAK_RSS_GROWTH" ]; then
  capture_profiles "$PROFILE_DIR"
  fail "idle RSS grew ${growth} MiB (> ${SOAK_RSS_GROWTH} MiB) — possible leak"
fi
if [ "$goroutine_growth" -gt "$SOAK_GOROUTINE_GROWTH" ]; then
  capture_profiles "$PROFILE_DIR"
  fail "goroutines grew by ${goroutine_growth} (> ${SOAK_GOROUTINE_GROWTH}) — possible leak"
fi
if awk -v c="$cpu_final" -v m="$IDLE_CPU_MAX" 'BEGIN{exit !(c > m)}'; then
  capture_profiles "$PROFILE_DIR"
  fail "idle CPU ${cpu_final} cores after soak (> ${IDLE_CPU_MAX})"
fi
ok "soak passed: stable across $i iterations, RSS growth ${growth} MiB, goroutine growth ${goroutine_growth}"
