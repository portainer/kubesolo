#!/usr/bin/env bash
# Tiered manifest deployment suite. Runs after the smoke suite against an
# already-running KubeSolo cluster. Each tier applies its manifests, gates on
# readiness, asserts behaviour, then tears its namespace(s) down.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
MANIFESTS="$HERE/manifests"
WAIT="${READY_TIMEOUT}s"

# delete_ns_wait removes namespaces and BLOCKS until they are fully gone. The
# soak loop re-applies the same tiers each iteration, so a fire-and-forget delete
# would race the next apply ("namespace is being terminated"). Waiting makes the
# suite safely re-runnable.
ns_absent() { ! kc get namespace "$1" >/dev/null 2>&1; }
delete_ns_wait() {
  local ns
  for ns in "$@"; do
    kc delete namespace "$ns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
  for ns in "$@"; do
    wait_for 120 2 "namespace $ns to terminate" ns_absent "$ns" \
      || { dump_diagnostics; fail "namespace $ns stuck terminating"; }
  done
}

# lb_ingress_ip <ns> <svc> — echo the assigned EXTERNAL-IP, polling until it
# appears. Returns 1 on timeout. Same loop tier 5 uses inline, factored out
# because tier 6 needs it for two Services.
lb_ingress_ip() {
  local ns="$1" svc="$2" ip="" i=0
  while [ "$i" -lt 24 ]; do
    ip=$(kc -n "$ns" get svc "$svc" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
    [ -n "$ip" ] && { echo "$ip"; return 0; }
    i=$((i + 1)); sleep 5
  done
  return 1
}

# assert_webhook_rules checks the KS-75 rule split: services match CREATE+UPDATE
# while pods/PVCs/jobs stay CREATE-only. The split is load-bearing, not
# cosmetic — pod spec.nodeName and job spec.template are immutable after
# creation, so a webhook returning those patches on an UPDATE would make the
# apiserver reject the request. go-template rather than jq: the harness only
# assumes kubectl and awk.
assert_webhook_rules() {
  local rules
  rules=$(kc get mutatingwebhookconfiguration webhook.kubesolo.io -o go-template='{{range .webhooks}}{{range .rules}}{{range .resources}}{{.}},{{end}}={{range .operations}}{{.}},{{end}}{{"\n"}}{{end}}{{end}}') \
    || { dump_diagnostics; fail "could not read the kubesolo MutatingWebhookConfiguration"; }

  printf '%s\n' "$rules" | grep -q '^services,=CREATE,UPDATE,$' \
    || { log "rules: $rules"; dump_diagnostics; fail "services rule is not CREATE+UPDATE"; }
  # Matched exactly rather than with a wildcard: a loose pattern would still
  # pass if PVCs or jobs were dropped from the rule, which is the other half of
  # what this guards.
  printf '%s\n' "$rules" | grep -q '^pods,persistentvolumeclaims,jobs,=CREATE,$' \
    || { log "rules: $rules"; dump_diagnostics; fail "pods/PVCs/jobs rule is not exactly those three, CREATE-only (immutable-field hazard)"; }
}

# tier1: a Deployment is reachable via its ClusterIP and a NodePort is allocated.
tier1_workload() {
  log "tier 1 — workload & networking"
  kc apply -f "$MANIFESTS/01-workload/workload.yaml" >/dev/null
  kc -n tier1-workload rollout status deployment/web --timeout="$WAIT"

  # ClusterIP routing, exercised from inside the cluster. The client pod is
  # one-shot: it reaches Succeeded only if the request returned nginx's page.
  kc -n tier1-workload run curl --image=busybox:1.36 --restart=Never \
    --command -- sh -c 'wget -q -T 10 -O- http://web-clusterip/ | grep -qi nginx' >/dev/null
  kc -n tier1-workload wait --for=jsonpath='{.status.phase}'=Succeeded pod/curl --timeout=120s \
    || { dump_diagnostics; fail "ClusterIP routing failed"; }

  # NodePort is allocated by the apiserver.
  local np
  np=$(kc -n tier1-workload get svc web-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
  [ -n "$np" ] || { dump_diagnostics; fail "NodePort was not allocated"; }
  ok "tier 1 passed (ClusterIP routes, NodePort=$np)"
  delete_ns_wait tier1-workload
}

# tier2: data written by one pod survives into a second pod via a local-path PVC.
tier2_storage() {
  log "tier 2 — storage persistence"
  kc apply -f "$MANIFESTS/02-storage/pvc.yaml" >/dev/null
  kc apply -f "$MANIFESTS/02-storage/writer.yaml" >/dev/null
  kc -n tier2-storage wait --for=condition=complete job/writer --timeout="$WAIT" \
    || { dump_diagnostics; fail "writer job did not complete"; }
  kc -n tier2-storage delete job/writer --wait=true >/dev/null

  kc apply -f "$MANIFESTS/02-storage/reader.yaml" >/dev/null
  # The reader only stays Ready if the marker persisted (see its command).
  kc -n tier2-storage wait --for=condition=Ready pod/reader --timeout="$WAIT" \
    || { dump_diagnostics; fail "data did not persist across pods"; }
  ok "tier 2 passed (PVC data persisted)"
  delete_ns_wait tier2-storage
}

# tier3: a pod sees its ConfigMap env, mounted Secret, and projected SA token.
tier3_config() {
  log "tier 3 — config & identity"
  kc apply -f "$MANIFESTS/03-config/config.yaml" >/dev/null
  kc -n tier3-config wait --for=condition=Ready pod/consumer --timeout="$WAIT" \
    || { dump_diagnostics; fail "consumer pod not Ready"; }

  kc -n tier3-config exec consumer -- sh -c '[ "$GREETING" = hello-kubesolo ]' \
    || { dump_diagnostics; fail "ConfigMap env not injected"; }
  kc -n tier3-config exec consumer -- sh -c 'grep -q s3cr3t-value /etc/secret/token' \
    || { dump_diagnostics; fail "Secret not mounted"; }
  # Projected SA token — the mount-propagation path container mode relies on.
  kc -n tier3-config exec consumer -- sh -c 'test -s /var/run/secrets/tokens/sa-token' \
    || { dump_diagnostics; fail "projected ServiceAccount token missing"; }
  ok "tier 3 passed (ConfigMap, Secret, projected token)"
  delete_ns_wait tier3-config
}

# tier4: Job completes, DaemonSet+StatefulSet roll out, CronJob is schedulable.
tier4_controllers() {
  log "tier 4 — controllers"
  kc apply -f "$MANIFESTS/04-controllers/controllers.yaml" >/dev/null

  kc -n tier4-controllers wait --for=condition=complete job/oneshot --timeout="$WAIT" \
    || { dump_diagnostics; fail "Job did not complete"; }
  kc -n tier4-controllers rollout status daemonset/agent --timeout="$WAIT" \
    || { dump_diagnostics; fail "DaemonSet did not roll out"; }
  kc -n tier4-controllers rollout status statefulset/stateful --timeout="$WAIT" \
    || { dump_diagnostics; fail "StatefulSet did not roll out"; }

  # Trigger the CronJob immediately rather than waiting for the schedule.
  kc -n tier4-controllers create job --from=cronjob/periodic periodic-manual >/dev/null
  kc -n tier4-controllers wait --for=condition=complete job/periodic-manual --timeout="$WAIT" \
    || { dump_diagnostics; fail "CronJob-derived job did not complete"; }
  ok "tier 4 passed (Job, CronJob, DaemonSet, StatefulSet)"
  delete_ns_wait tier4-controllers
}

# tier5: cross-namespace DNS resolves, and the LoadBalancer webhook assigns an IP.
tier5_dns_lb() {
  log "tier 5 — DNS & LoadBalancer"
  kc apply -f "$MANIFESTS/05-dns-lb/dns-lb.yaml" >/dev/null
  kc -n tier5-a rollout status deployment/web --timeout="$WAIT"
  kc -n tier5-b wait --for=condition=Ready pod/dns-client --timeout="$WAIT"

  # Cross-namespace service DNS (client in tier5-b resolves a service in tier5-a).
  kc -n tier5-b exec dns-client -- nslookup web.tier5-a.svc.cluster.local >/dev/null 2>&1 \
    || { dump_diagnostics; fail "cross-namespace DNS resolution failed"; }

  # The LoadBalancer webhook patches status with the node IP.
  local ip="" i=0
  while [ "$i" -lt 24 ]; do
    ip=$(kc -n tier5-a get svc web-lb -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
    [ -n "$ip" ] && break
    i=$((i + 1)); sleep 5
  done
  [ -n "$ip" ] || { dump_diagnostics; fail "LoadBalancer was not assigned an IP"; }
  ok "tier 5 passed (cross-ns DNS; LoadBalancer IP=$ip)"
  delete_ns_wait tier5-a tier5-b
}

# tier6: a Service changed to type LoadBalancer after creation gets an
# EXTERNAL-IP [KS-75]; a server dry run does not mutate real state; and the
# webhook rule split leaves pods/PVCs/jobs on CREATE only.
tier6_lb_update() {
  log "tier 6 — LoadBalancer UPDATE path [KS-75]"
  kc apply -f "$MANIFESTS/06-lb-update/lb-update.yaml" >/dev/null
  kc -n tier6-lb rollout status deployment/web --timeout="$WAIT"

  # Control: the CREATE path still assigns an IP.
  local born
  born=$(lb_ingress_ip tier6-lb born-lb) \
    || { dump_diagnostics; fail "born-lb (CREATE path) never got an EXTERNAL-IP"; }

  # Precondition: while still ClusterIP, flip-me has no ingress IP.
  [ -z "$(kc -n tier6-lb get svc flip-me -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null)" ] \
    || { dump_diagnostics; fail "flip-me had an EXTERNAL-IP before being flipped"; }

  # A server-side dry run must not mutate real state. The Service path patches
  # status out of band, and the webhook declares sideEffects: None, so the
  # apiserver does invoke it for dry-run requests.
  kc apply --dry-run=server -f "$MANIFESTS/06-lb-update/flip-to-lb.yaml" >/dev/null
  [ "$(kc -n tier6-lb get svc flip-me -o jsonpath='{.spec.type}')" = ClusterIP ] \
    || { dump_diagnostics; fail "server dry-run mutated spec.type"; }
  [ -z "$(kc -n tier6-lb get svc flip-me -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null)" ] \
    || { dump_diagnostics; fail "server dry-run patched LoadBalancer status"; }

  # The regression itself: ClusterIP -> LoadBalancer via apply, as helm would.
  kc apply -f "$MANIFESTS/06-lb-update/flip-to-lb.yaml" >/dev/null
  local flipped
  flipped=$(lb_ingress_ip tier6-lb flip-me) \
    || { dump_diagnostics; fail "flipped Service never got an EXTERNAL-IP [KS-75]"; }
  [ "$flipped" = "$born" ] \
    || { dump_diagnostics; fail "flipped IP ($flipped) does not match created IP ($born)"; }

  assert_webhook_rules

  # Jobs are still mutated on CREATE and still accept metadata updates.
  kc -n tier6-lb wait --for=condition=complete job/guard --timeout="$WAIT" \
    || { dump_diagnostics; fail "guard job did not complete"; }
  [ -n "$(kc -n tier6-lb get job guard -o jsonpath='{.spec.template.spec.nodeSelector.kubernetes\.io/hostname}')" ] \
    || { dump_diagnostics; fail "job nodeSelector was not injected on CREATE"; }
  kc -n tier6-lb label job/guard ks75=touched --overwrite >/dev/null \
    || { dump_diagnostics; fail "job UPDATE was rejected — webhook is matching job updates"; }

  ok "tier 6 passed (flipped=$flipped, created=$born; rules scoped correctly)"
  delete_ns_wait tier6-lb
}

tier1_workload
tier2_storage
tier3_config
tier4_controllers
tier5_dns_lb
tier6_lb_update

ok "all manifest tiers passed"
