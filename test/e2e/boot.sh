#!/usr/bin/env bash
# Boot KubeSolo as a container via kubesoloctl and wait until the cluster is
# ready to accept workloads.
#
# kubesoloctl does the heavy lifting: it pulls the image, runs a privileged
# container, waits for the API server, and merges a host-reachable kubeconfig
# (rewritten to the random published 127.0.0.1 port) into ~/.kube/config.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

[ -n "$IMAGE" ] || fail "IMAGE is required (e.g. IMAGE=portainerci/kubesolo:pr-12)"
require_cmd docker
require_cmd "$KUBECTL"
command -v "$KUBESOLOCTL" >/dev/null 2>&1 || [ -x "$KUBESOLOCTL" ] || fail "kubesoloctl not found: $KUBESOLOCTL"

log "installing KubeSolo container from $IMAGE (name: $KS_NAME)"
"$KUBESOLOCTL" install \
  --run-mode=container \
  --image "$IMAGE" \
  --name "$KS_NAME" \
  --local-storage=true

# install merges the kubeconfig into ~/.kube/config; make sure we target it.
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
_resolve_ctx
log "using kube context: $KS_CONTEXT"
kc cluster-info >/dev/null 2>&1 \
  || { dump_diagnostics; fail "kube context '$KS_CONTEXT' is not reachable after install"; }
ok "API server reachable"

# The kubelet registers the Node a few seconds after the API server is up, and
# CoreDNS / local-path are deployed later still — so wait for each object to
# EXIST before waiting on its condition (kubectl wait errors on a missing resource).
node_exists() { [ -n "$(kc get nodes -o name 2>/dev/null)" ]; }
deploy_exists() { kc -n "$1" get deploy "$2" >/dev/null 2>&1; }

log "waiting for the node to register and become Ready (timeout ${READY_TIMEOUT}s)"
wait_for "$READY_TIMEOUT" 3 "node registration" node_exists \
  || { dump_diagnostics; fail "no node registered with the API server"; }
kc wait --for=condition=Ready nodes --all --timeout="${READY_TIMEOUT}s" \
  || { dump_diagnostics; fail "node did not become Ready"; }
ok "node Ready"

log "waiting for CoreDNS"
wait_for "$READY_TIMEOUT" 3 "coredns deployment" deploy_exists kube-system coredns \
  || { dump_diagnostics; fail "CoreDNS deployment was never created"; }
kc -n kube-system rollout status deployment/coredns --timeout="${READY_TIMEOUT}s" \
  || { dump_diagnostics; fail "CoreDNS did not roll out"; }
ok "CoreDNS ready"

# local-path provisioner (deployed when --local-storage, which we pass).
log "waiting for local-path provisioner"
if wait_for 60 3 "local-path deployment" deploy_exists local-path-storage local-path-provisioner; then
  kc -n local-path-storage rollout status deployment/local-path-provisioner --timeout="${READY_TIMEOUT}s" \
    || { dump_diagnostics; fail "local-path provisioner did not roll out"; }
  ok "local-path provisioner ready"
else
  log "local-path provisioner not present after 60s — skipping (is --local-storage enabled?)"
fi

ok "cluster is up and ready for workloads"
