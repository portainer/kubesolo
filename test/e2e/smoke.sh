#!/usr/bin/env bash
# Core smoke assertions against a running KubeSolo cluster. Assumes boot.sh has
# already brought the cluster up and merged the kubeconfig.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
NS="e2e-smoke"

cleanup_ns() { kc delete namespace "$NS" --ignore-not-found --wait=false >/dev/null 2>&1 || true; }
trap cleanup_ns EXIT

kc create namespace "$NS" >/dev/null 2>&1 || true

# ── 1. A trivial pod schedules, pulls and runs ──────────────────────────────
log "scheduling a workload pod"
kc -n "$NS" apply -f - >/dev/null <<'YAML'
apiVersion: v1
kind: Pod
metadata:
  name: smoke-busybox
spec:
  containers:
    - name: busybox
      image: busybox:1.36
      command: ["sh", "-c", "sleep 3600"]
  restartPolicy: Never
YAML
kc -n "$NS" wait --for=condition=Ready pod/smoke-busybox --timeout=120s \
  || { dump_diagnostics; fail "workload pod did not become Ready"; }
ok "workload pod Running"

# ── 2. In-cluster DNS resolves the kubernetes service ───────────────────────
log "checking in-cluster DNS"
if kc -n "$NS" exec smoke-busybox -- nslookup kubernetes.default.svc.cluster.local >/dev/null 2>&1; then
  ok "CoreDNS resolves kubernetes.default"
else
  dump_diagnostics; fail "in-cluster DNS resolution failed"
fi

# ── 3. Pod egress works (masquerade / SNAT is wired) ────────────────────────
log "checking pod egress"
if kc -n "$NS" exec smoke-busybox -- sh -c 'wget -q -T 10 -O /dev/null https://1.1.1.1 || nslookup cloudflare.com' >/dev/null 2>&1; then
  ok "pod egress works"
else
  dump_diagnostics; fail "pod egress failed (masquerade not working?)"
fi

ok "all smoke checks passed"
