#!/usr/bin/env bash
# Control-plane load test for KubeSolo. Boots the container image, measures the
# idle baseline, drives API churn, a watch storm and pod churn with loadgen,
# then checks the node returns to idle. Emits JSON + a GitHub job-summary table
# and fails when a scenario breaches its ceiling.
#
# Usage:
#   IMAGE=portainerci/kubesolo:develop KUBESOLOCTL=./dist/kubesoloctl test/perf/load.sh
#
# Tunables:
#   OUT                    result JSON path (default: test/perf/load.<arch>.json)
#   LOADGEN                loadgen binary; built from ./loadgen when unset
#   LOAD_DURATION          churn and watch duration (default: 120s)
#   CHURN_QPS              churn writes per second (default: 50)
#   WATCHERS               concurrent watches (default: 100)
#   WATCH_QPS              writes per second under watch (default: 10)
#   POD_WAVES / POD_REPLICAS  pod churn shape (default: 3 waves of 20)
#   RECOVERY_SETTLE        idle seconds after load before re-measuring (default: 120)
#   CPU_WINDOW             CPU sampling window, seconds (default: 60)
#   IDLE_CPU_MAX           idle CPU ceiling, before and after load (default: 0.25 cores)
#   RECOVERY_RSS_GROWTH    allowed idle RSS growth across the run (default: 256 MiB)
#   RECOVERY_GOROUTINE_GROWTH  allowed goroutine growth across the run (default: 200)
#   CHURN_P99_MAX_MS       churn p99 latency ceiling (default: 1000)
#   WATCH_LAG_P99_MAX_MS   watch delivery p99 lag ceiling (default: 2000)
#   POD_STARTUP_P99_MAX_S  pod creation → Ready p99 ceiling (default: 60)
#   PROFILE_DIR            where profiles are saved when a gate fails (default: test/perf/profiles)

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

OUT="${OUT:-$HERE/load.$ARCH.json}"
LOAD_DURATION="${LOAD_DURATION:-120s}"
CHURN_QPS="${CHURN_QPS:-50}"
WATCHERS="${WATCHERS:-100}"
WATCH_QPS="${WATCH_QPS:-10}"
POD_WAVES="${POD_WAVES:-3}"
POD_REPLICAS="${POD_REPLICAS:-20}"
RECOVERY_SETTLE="${RECOVERY_SETTLE:-120}"
CPU_WINDOW="${CPU_WINDOW:-60}"
IDLE_CPU_MAX="${IDLE_CPU_MAX:-0.25}"
# RSS stays ~100 MiB above the baseline after a healthy run (retained Go heap,
# page cache of the database), so its gate only catches gross leaks; goroutines
# return to within a handful and are the sharper leak signal.
RECOVERY_RSS_GROWTH="${RECOVERY_RSS_GROWTH:-256}"
RECOVERY_GOROUTINE_GROWTH="${RECOVERY_GOROUTINE_GROWTH:-200}"
CHURN_P99_MAX_MS="${CHURN_P99_MAX_MS:-1000}"
WATCH_LAG_P99_MAX_MS="${WATCH_LAG_P99_MAX_MS:-2000}"
POD_STARTUP_P99_MAX_S="${POD_STARTUP_P99_MAX_S:-60}"
NS=perf-load

require_cmd jq
trap 'bash "$HERE/../e2e/teardown.sh"' EXIT
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

ensure_loadgen

bash "$HERE/../e2e/boot.sh"
_resolve_ctx

log "settling ${CPU_WINDOW}s, then sampling the idle baseline"
sleep "$CPU_WINDOW"
rss_before="$(sample_rss_mib)"
cpu_before="$(measure_cpu_cores "$CPU_WINDOW")"
goroutines_before="$(goroutine_count)"
log "baseline: ${cpu_before} cores, ${rss_before} MiB, ${goroutines_before} goroutines"

kc create namespace "$NS" >/dev/null

# Each scenario's CPU is sampled from the cgroup around the loadgen call, so it
# reflects the work the load caused, not only the loadgen's own view.
# Logs go to stderr: stdout is the scenario's JSON, captured by the caller.
run_scenario() { # name  loadgen args...
  local name="$1"; shift
  log "scenario: $name" >&2
  local u0 u1 t0 t1 json
  u0="$(cpu_usage_usec)"; t0="$(date +%s)"
  json="$(lg "$@")" || { dump_diagnostics >&2; fail "loadgen $name failed"; }
  u1="$(cpu_usage_usec)"; t1="$(date +%s)"
  json="$(jq -c --argjson cores "$(awk -v a="$u0" -v b="$u1" -v s="$((t1 - t0))" 'BEGIN{printf "%.3f", (b - a) / 1e6 / (s > 0 ? s : 1)}')" \
    '. + {cpu_cores: $cores}' <<<"$json")"
  log "  $json" >&2
  printf '%s' "$json"
}

churn="$(run_scenario churn churn -duration "$LOAD_DURATION" -qps "$CHURN_QPS")"
watch="$(run_scenario watch watch -duration "$LOAD_DURATION" -watchers "$WATCHERS" -qps "$WATCH_QPS")"
pods="$(run_scenario pods pods -waves "$POD_WAVES" -replicas "$POD_REPLICAS")"

# Recovery: a loop that keeps spinning after the burst ends, or memory that never
# comes back, only shows once the load is gone (issue #254 was exactly that).
kc delete namespace "$NS" --wait=true --timeout=300s >/dev/null || true
log "settling ${RECOVERY_SETTLE}s after load"
sleep "$RECOVERY_SETTLE"
rss_after="$(sample_rss_mib)"
cpu_after="$(measure_cpu_cores "$CPU_WINDOW")"
goroutines_after="$(goroutine_count)"
log "after load: ${cpu_after} cores, ${rss_after} MiB, ${goroutines_after} goroutines"

mkdir -p "$(dirname "$OUT")"
jq -n --arg arch "$ARCH" --arg image "$IMAGE" \
  --argjson churn "$churn" --argjson watch "$watch" --argjson pods "$pods" \
  --argjson cpu_before "$cpu_before" --argjson cpu_after "$cpu_after" \
  --argjson rss_before "$rss_before" --argjson rss_after "$rss_after" \
  --argjson g_before "${goroutines_before:-0}" --argjson g_after "${goroutines_after:-0}" \
  '{arch: $arch, image: $image,
    idle_before: {cpu_cores: $cpu_before, rss_mib: $rss_before, goroutines: $g_before},
    churn: $churn, watch: $watch, pods: $pods,
    idle_after: {cpu_cores: $cpu_after, rss_mib: $rss_after, goroutines: $g_after}}' > "$OUT"
log "wrote $OUT"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  jq -r '"### KubeSolo load (\(.arch))\n",
    "| scenario | result |", "|---|---|",
    "| idle before | \(.idle_before.cpu_cores) cores, \(.idle_before.rss_mib) MiB |",
    "| churn | \(.churn.ops_per_s|floor) ops/s, p50 \(.churn.p50_ms) ms, p99 \(.churn.p99_ms) ms, \(.churn.errors) errors, \(.churn.cpu_cores) cores |",
    "| watch | \(.watch.watchers) watchers, lag p99 \(.watch.lag_p99_ms) ms, missed \(.watch.missed), \(.watch.cpu_cores) cores |",
    "| pods | \(.pods.waves)×\(.pods.replicas), startup p50 \(.pods.startup_p50_s)s, p99 \(.pods.startup_p99_s)s |",
    "| idle after | \(.idle_after.cpu_cores) cores, \(.idle_after.rss_mib) MiB |"' "$OUT" >> "$GITHUB_STEP_SUMMARY"
fi

# ── Gates ─────────────────────────────────────────────────────────────────────
log "checking load gates"
check idle_before.cpu_cores "$cpu_before" "$IDLE_CPU_MAX"
check churn.errors "$(jq .churn.errors "$OUT")" 0
check churn.p99_ms "$(jq .churn.p99_ms "$OUT")" "$CHURN_P99_MAX_MS"
check watch.write_errors "$(jq .watch.write_errors "$OUT")" 0
check watch.missed "$(jq .watch.missed "$OUT")" 0
check watch.lag_p99_ms "$(jq .watch.lag_p99_ms "$OUT")" "$WATCH_LAG_P99_MAX_MS"
check pods.startup_p99_s "$(jq .pods.startup_p99_s "$OUT")" "$POD_STARTUP_P99_MAX_S"
check idle_after.cpu_cores "$cpu_after" "$IDLE_CPU_MAX"
check idle_after.rss_growth_mib "$((rss_after - rss_before))" "$RECOVERY_RSS_GROWTH"
check idle_after.goroutine_growth "$((goroutines_after - goroutines_before))" "$RECOVERY_GOROUTINE_GROWTH"

finish_checks "load gates"
