#!/usr/bin/env bash
# Performance & footprint baseline for KubeSolo. Boots the container image,
# measures the metrics that matter for an edge distro, emits JSON + a GitHub
# job-summary table, and (if a baseline exists) fails on relative regression.
#
# Usage:
#   IMAGE=portainerci/kubesolo:develop KUBESOLOCTL=./dist/kubesoloctl test/perf/bench.sh
#
# Tunables:
#   OUT            result JSON path        (default: test/perf/result.<arch>.json)
#   BASELINE       baseline JSON to gate against (default: test/perf/baseline.<arch>.json)
#   TOLERANCE      allowed relative regression, fraction (default: 0.20 = +20%)
#   DENSITY_MAX    max pods to attempt      (default: 50; 0 disables the density probe)
#   DENSITY_STEP   density scale increment  (default: 10)
#   SETTLE         idle settle seconds before RSS sample (default: 25)

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../e2e/lib.sh
source "$HERE/../e2e/lib.sh"

ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac
OUT="${OUT:-$HERE/result.$ARCH.json}"
BASELINE="${BASELINE:-$HERE/baseline.$ARCH.json}"
TOLERANCE="${TOLERANCE:-0.20}"
DENSITY_MAX="${DENSITY_MAX:-50}"
DENSITY_STEP="${DENSITY_STEP:-10}"
SETTLE="${SETTLE:-25}"

[ -n "$IMAGE" ] || fail "IMAGE is required"
require_cmd docker
require_cmd "$KUBECTL"

trap 'bash "$HERE/../e2e/teardown.sh"' EXIT
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

node_exists() { [ -n "$(kc get nodes -o name 2>/dev/null)" ]; }

log "booting $IMAGE for measurement"
SECONDS=0
"$KUBESOLOCTL" install --run-mode=container --image "$IMAGE" --name "$KS_NAME" --local-storage=true
boot_to_api=$SECONDS

# The kubelet registers the Node a few seconds after the API server is up, so
# wait for it to exist before waiting on its Ready condition (kubectl wait errors
# on a missing resource).
wait_for "$READY_TIMEOUT" 3 "node registration" node_exists \
  || { dump_diagnostics; fail "no node registered with the API server"; }
kc wait --for=condition=Ready nodes --all --timeout="${READY_TIMEOUT}s" \
  || { dump_diagnostics; fail "node did not become Ready"; }
boot_to_node_ready=$SECONDS

log "measuring time to first running pod"
kc create namespace perf >/dev/null 2>&1 || true
kc -n perf run probe --image=busybox:1.36 --restart=Never --command -- sh -c 'sleep 3600' >/dev/null
kc -n perf wait --for=condition=Ready pod/probe --timeout="${READY_TIMEOUT}s" \
  || { dump_diagnostics; fail "first pod did not run"; }
boot_to_first_pod=$SECONDS

log "settling ${SETTLE}s before sampling idle memory"
sleep "$SETTLE"
idle_rss_mib="$(sample_rss_mib)"

image_bytes="$(docker image inspect "$IMAGE" -f '{{.Size}}' 2>/dev/null || echo 0)"
image_size_mib=$(( image_bytes / 1024 / 1024 ))

# ── Pod density: scale a tiny Deployment until a step fails to fully schedule ──
max_pods=0
if [ "$DENSITY_MAX" -gt 0 ]; then
  log "probing pod density (up to $DENSITY_MAX)"
  kc -n perf create deployment density --image=busybox:1.36 -- sh -c 'sleep 3600' >/dev/null
  n=0
  while [ "$n" -lt "$DENSITY_MAX" ]; do
    next=$((n + DENSITY_STEP)); [ "$next" -gt "$DENSITY_MAX" ] && next="$DENSITY_MAX"
    kc -n perf scale deployment/density --replicas="$next" >/dev/null
    if kc -n perf rollout status deployment/density --timeout=120s >/dev/null 2>&1; then
      max_pods="$next"; n="$next"
    else
      log "density plateaued: $next replicas did not all become Ready"
      break
    fi
  done
fi
ok "measurement complete"

mkdir -p "$(dirname "$OUT")"
cat > "$OUT" <<JSON
{
  "arch": "$ARCH",
  "image": "$IMAGE",
  "boot_to_api_s": $boot_to_api,
  "boot_to_node_ready_s": $boot_to_node_ready,
  "boot_to_first_pod_s": $boot_to_first_pod,
  "idle_rss_mib": $idle_rss_mib,
  "image_size_mib": $image_size_mib,
  "max_pods": $max_pods
}
JSON
log "wrote $OUT"

# ── Job summary ─────────────────────────────────────────────────────────────
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### KubeSolo performance ($ARCH)"
    echo ""
    echo "| metric | value |"
    echo "|--------|-------|"
    echo "| boot → API ready | ${boot_to_api}s |"
    echo "| boot → node Ready | ${boot_to_node_ready}s |"
    echo "| boot → first pod Running | ${boot_to_first_pod}s |"
    echo "| idle RSS | ${idle_rss_mib} MiB |"
    echo "| image size | ${image_size_mib} MiB |"
    echo "| max pods scheduled | ${max_pods} |"
  } >> "$GITHUB_STEP_SUMMARY"
fi

printf '\n%s\n' "$(cat "$OUT")"

# ── Regression gate (relative, vs committed baseline) ─────────────────────────
if [ ! -f "$BASELINE" ]; then
  log "no baseline at $BASELINE — reporting only (commit $OUT as the baseline once trusted)"
  exit 0
fi

log "comparing against baseline $BASELINE (tolerance +$(awk -v t="$TOLERANCE" 'BEGIN{printf "%d", t*100}')%)"
regressed=0
gate() { # name  current  lower-is-better
  local key="$1" cur="$2"
  local base; base="$(awk -F'[:,]' -v k="\"$key\"" '$1 ~ k {gsub(/[ ]/,"",$2); print $2+0; exit}' "$BASELINE")"
  [ -z "$base" ] || [ "$base" = "0" ] && { log "  $key: no baseline value, skipping"; return; }
  local limit; limit="$(awk -v b="$base" -v t="$TOLERANCE" 'BEGIN{printf "%.2f", b*(1+t)}')"
  if awk -v c="$cur" -v l="$limit" 'BEGIN{exit !(c>l)}'; then
    printf '\033[1;31m  REGRESSION %s: %s > %s (baseline %s)\033[0m\n' "$key" "$cur" "$limit" "$base"
    regressed=1
  else
    printf '  ok %s: %s (baseline %s, limit %s)\n' "$key" "$cur" "$base" "$limit"
  fi
}
gate boot_to_api_s "$boot_to_api"
gate boot_to_node_ready_s "$boot_to_node_ready"
gate boot_to_first_pod_s "$boot_to_first_pod"
gate idle_rss_mib "$idle_rss_mib"
gate image_size_mib "$image_size_mib"

[ "$regressed" -eq 0 ] || fail "performance regressed beyond tolerance"
ok "within performance tolerances"
