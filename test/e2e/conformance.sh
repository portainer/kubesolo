#!/usr/bin/env bash
# Conformance-lite: run a focused subset of the upstream Kubernetes e2e
# [Conformance] suite via Sonobuoy — only the namespaced-API behaviours that are
# meaningful on a single node. Multi-node / Serial / Disruptive / Slow tests are
# skipped (they assume >1 schedulable node or are too heavy for an edge node).
#
# Standalone nightly entrypoint (boots and tears down itself). Requires the
# `sonobuoy` binary on PATH in addition to docker/kubectl/kubesoloctl.
#
# Tunables:
#   E2E_FOCUS  ginkgo focus regex  (default: single-node-safe Conformance areas)
#   E2E_SKIP   ginkgo skip regex   (default: multi-node / Serial / Disruptive / Slow)
#   CONF_TIMEOUT  sonobuoy run timeout seconds (default: 3600)

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

# Ginkgo focus is a single RE2 regex matched against the full test name, which
# is "<topic> … [Conformance]" — topic FIRST, tag LAST. So the keyword must come
# before \[Conformance\] (RE2 has no lookahead, so "both in any order" isn't
# expressible in one regex).
FOCUS="${E2E_FOCUS:-(ConfigMap|Secret|Pods|Services|Deployment|ReplicaSet|DNS|Projected|Downward|EmptyDir).*\\[Conformance\\]}"
SKIP="${E2E_SKIP:-\\[Serial\\]|\\[Disruptive\\]|\\[Slow\\]|\\[Flaky\\]|two nodes|multiple nodes|more than one node}"
CONF_TIMEOUT="${CONF_TIMEOUT:-3600}"

require_cmd docker
require_cmd "$KUBECTL"
require_cmd sonobuoy
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

cleanup() {
  _resolve_ctx 2>/dev/null || true
  sonobuoy delete --wait --kubeconfig "$KUBECONFIG" --context "$KS_CONTEXT" >/dev/null 2>&1 || true
  bash "$HERE/teardown.sh"
}
trap cleanup EXIT

bash "$HERE/boot.sh"

# boot.sh runs in a subshell, so resolve the real context here too (KubeSolo names
# it kubernetes-admin@<name>, not <name>). sonobuoy needs the actual context.
_resolve_ctx
log "running focused conformance subset via sonobuoy (context: $KS_CONTEXT)"
log "  focus: $FOCUS"
log "  skip:  $SKIP"
sonobuoy run --wait --wait-output=silent \
  --kubeconfig "$KUBECONFIG" --context "$KS_CONTEXT" \
  --plugin e2e \
  --plugin-env e2e.E2E_FOCUS="$FOCUS" \
  --plugin-env e2e.E2E_SKIP="$SKIP" \
  --timeout "$CONF_TIMEOUT" \
  || { dump_diagnostics; fail "sonobuoy run did not complete"; }

results="$(sonobuoy retrieve --kubeconfig "$KUBECONFIG" --context "$KS_CONTEXT")"
[ -n "$results" ] && [ -f "$results" ] || fail "could not retrieve sonobuoy results"

log "results:"
summary="$(sonobuoy results "$results")"
echo "$summary"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  { echo "### Conformance-lite"; echo ""; echo '```'; echo "$summary"; echo '```'; } >> "$GITHUB_STEP_SUMMARY"
fi

failed="$(printf '%s\n' "$summary" | awk -F': ' '/^Failed:/ {print $2; exit}')"
failed="${failed:-unknown}"
[ "$failed" = "0" ] || { dump_diagnostics; fail "conformance-lite reported failures: $failed"; }
ok "conformance-lite passed (0 failures)"
