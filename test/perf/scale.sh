#!/usr/bin/env bash
# Datastore-scale test for KubeSolo. Grows kine's SQLite database with many
# objects and a long revision history, then checks the node still idles, LISTs
# stay fast and a restart on the large database still comes up in time. A real
# edge node accumulates millions of revisions; a fresh test cluster has almost
# none, so this is where bugs that scale with history show up.
#
# Usage:
#   IMAGE=portainerci/kubesolo:develop KUBESOLOCTL=./dist/kubesoloctl test/perf/scale.sh
#
# Tunables:
#   OUT                    result JSON path (default: test/perf/scale.<arch>.json)
#   LOADGEN                loadgen binary; built from ./loadgen when unset
#   SCALE_OBJECTS          distinct ConfigMaps (default: 2000)
#   SCALE_REVISIONS        total writes, creates included (default: 100000)
#   SCALE_SIZE             ConfigMap payload bytes (default: 2048)
#   CPU_WINDOW             CPU sampling window, seconds (default: 60)
#   IDLE_CPU_MAX           idle CPU ceiling with the large database (default: 0.25 cores)
#   LIST_P99_MAX_MS        LIST of every object p99 ceiling (default: 2000)
#   RESTART_READY_MAX_S    restart → node Ready ceiling (default: 120)
#   PROFILE_DIR            where profiles are saved when a gate fails (default: test/perf/profiles)

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

OUT="${OUT:-$HERE/scale.$ARCH.json}"
SCALE_OBJECTS="${SCALE_OBJECTS:-2000}"
SCALE_REVISIONS="${SCALE_REVISIONS:-100000}"
SCALE_SIZE="${SCALE_SIZE:-2048}"
CPU_WINDOW="${CPU_WINDOW:-60}"
IDLE_CPU_MAX="${IDLE_CPU_MAX:-0.25}"
LIST_P99_MAX_MS="${LIST_P99_MAX_MS:-2000}"
RESTART_READY_MAX_S="${RESTART_READY_MAX_S:-120}"
NS=perf-scale
DB=/var/lib/kubesolo/kine/db/state.db

require_cmd jq
trap 'bash "$HERE/../e2e/teardown.sh"' EXIT
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

ensure_loadgen

bash "$HERE/../e2e/boot.sh"
_resolve_ctx

# db_mib reports the SQLite database plus its WAL, in MiB.
db_mib() {
  docker exec "$(container_name)" sh -c "du -ck $DB $DB-wal 2>/dev/null | tail -1" | awk '{printf "%d", $1 / 1024}'
}

kc create namespace "$NS" >/dev/null
log "filling the datastore: $SCALE_OBJECTS objects, $SCALE_REVISIONS writes"
fill="$(lg fill -objects "$SCALE_OBJECTS" -revisions "$SCALE_REVISIONS" -size "$SCALE_SIZE")" \
  || { dump_diagnostics; fail "fill failed"; }
log "  $fill"
db_after_fill="$(db_mib)"

log "timing LISTs of every object"
list="$(lg list)" || { dump_diagnostics; fail "list failed"; }
log "  $list"

log "sampling idle CPU with the large database"
sleep 30
cpu_large="$(measure_cpu_cores "$CPU_WINDOW")"
rss_large="$(sample_rss_mib)"
log "  ${cpu_large} cores, ${rss_large} MiB, database ${db_after_fill} MiB"

# Restart on the large database: startup repair checks, kine's first compaction
# and the API server's cache warm-up all read the whole history.
log "restarting KubeSolo on the large database"
node_ready() {
  kc get nodes -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null | grep -qx True
}
docker restart "$(container_name)" >/dev/null
SECONDS=0
# The node object keeps its old Ready condition until the kubelet reports
# again, so wait for the API server to drop out first.
wait_for 30 1 "API server to go down" sh -c "! $KUBECTL --context $KS_CONTEXT get --raw /readyz >/dev/null 2>&1" || true
wait_for "$RESTART_READY_MAX_S" 2 "node Ready after restart" node_ready \
  || { dump_diagnostics; fail "node not Ready ${RESTART_READY_MAX_S}s after restart"; }
restart_ready_s=$SECONDS
log "  node Ready ${restart_ready_s}s after restart"

sleep 30
cpu_restart="$(measure_cpu_cores "$CPU_WINDOW")"
db_after_restart="$(db_mib)"
log "  after restart: ${cpu_restart} cores, database ${db_after_restart} MiB"

mkdir -p "$(dirname "$OUT")"
jq -n --arg arch "$ARCH" --arg image "$IMAGE" --argjson fill "$fill" --argjson list "$list" \
  --argjson db_fill "$db_after_fill" --argjson db_restart "$db_after_restart" \
  --argjson cpu_large "$cpu_large" --argjson rss_large "$rss_large" \
  --argjson restart "$restart_ready_s" --argjson cpu_restart "$cpu_restart" \
  '{arch: $arch, image: $image, fill: $fill, list: $list,
    db_mib_after_fill: $db_fill, db_mib_after_restart: $db_restart,
    idle_cpu_cores: $cpu_large, idle_rss_mib: $rss_large,
    restart_to_ready_s: $restart, idle_cpu_cores_after_restart: $cpu_restart}' > "$OUT"
log "wrote $OUT"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  jq -r '"### KubeSolo datastore scale (\(.arch))\n",
    "| metric | value |", "|---|---|",
    "| fill | \(.fill.writes) writes at \(.fill.writes_per_s|floor)/s, \(.fill.errors) errors |",
    "| database | \(.db_mib_after_fill) MiB after fill, \(.db_mib_after_restart) MiB after restart |",
    "| LIST \(.list.items) objects | p50 \(.list.p50_ms) ms, p99 \(.list.p99_ms) ms |",
    "| idle with large db | \(.idle_cpu_cores) cores, \(.idle_rss_mib) MiB |",
    "| restart → node Ready | \(.restart_to_ready_s)s |",
    "| idle after restart | \(.idle_cpu_cores_after_restart) cores |"' "$OUT" >> "$GITHUB_STEP_SUMMARY"
fi

log "checking scale gates"
check fill.errors "$(jq .fill.errors "$OUT")" 0
check list.p99_ms "$(jq .list.p99_ms "$OUT")" "$LIST_P99_MAX_MS"
check idle_cpu_cores "$cpu_large" "$IDLE_CPU_MAX"
check restart_to_ready_s "$restart_ready_s" "$RESTART_READY_MAX_S"
check idle_cpu_cores_after_restart "$cpu_restart" "$IDLE_CPU_MAX"

finish_checks "scale gates"
