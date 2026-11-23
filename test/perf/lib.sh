#!/usr/bin/env bash
# Shared helpers for the performance suite, layered on the e2e harness. Sourced
# by bench.sh / load.sh / scale.sh; never runs anything on its own.

PERF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../e2e/lib.sh
source "$PERF_DIR/../e2e/lib.sh"

ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac
PROFILE_DIR="${PROFILE_DIR:-$PERF_DIR/profiles}"

# check <name> <current> <max> records a ceiling breach without stopping, so a
# run reports every breached gate at once. finish_checks fails if any did.
checks_failed=0
check() {
  if awk -v c="$2" -v m="$3" 'BEGIN{exit !(c > m)}'; then
    printf '\033[1;31m  FAIL %s: %s > %s\033[0m\n' "$1" "$2" "$3"
    checks_failed=1
  else
    printf '  ok %s: %s (max %s)\n' "$1" "$2" "$3"
  fi
}

# finish_checks <what> saves profiles of the still-running node and fails when
# any check breached, so the evidence is collected before teardown.
finish_checks() {
  if [ "$checks_failed" -ne 0 ]; then
    capture_profiles "$PROFILE_DIR"
    fail "$1 failed"
  fi
  ok "$1 passed"
}

# ensure_loadgen builds the load generator unless LOADGEN already points at one.
ensure_loadgen() {
  [ -n "${LOADGEN:-}" ] && return
  require_cmd go
  LOADGEN="$(mktemp -d)/loadgen"
  log "building loadgen"
  (cd "$PERF_DIR/loadgen" && go build -o "$LOADGEN" .)
}

# lg <scenario> [flags] runs loadgen against the instance in its own namespace.
lg() {
  _resolve_ctx
  "$LOADGEN" "$1" -kubeconfig "$KUBECONFIG" -context "$KS_CONTEXT" -namespace "$NS" "${@:2}"
}
