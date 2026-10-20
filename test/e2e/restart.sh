#!/usr/bin/env bash
# End to end test of POST /api/v1/restart on a host install, driven the way the
# Portainer agent drives it: from inside a pod, through the agent's API socket.
#
#   1. A configuration change is applied, and every pod keeps running.
#   2. A Portainer agent image that cannot run is reverted.
#   3. A configuration KubeSolo cannot run on is reverted.
#   4. The Portainer agent's set-once settings are refused.
#   5. Agent settings overridden from the environment are not mistaken for
#      changes to the configuration file.
#   6. An agent already crash-looping does not fail the restart.
#
# Run it as root on a host where KubeSolo is installed (not in container mode)
# with the API enabled and an edge agent configured. It changes the
# configuration and restarts KubeSolo several times, so do not run it on a host
# that matters.
#
# It runs itself as a transient systemd unit. A dropped SSH session then cannot
# stop it halfway: the cleanup trap, which puts the configuration back, runs in
# the unit whatever happens to the terminal. Follow a detached run with
# `journalctl -fu kubesolo-e2e-restart`.

set -euo pipefail

CONFIG="${KUBESOLO_CONFIG:-/etc/kubesolo/config.yaml}"
DATA="${KUBESOLO_PATH:-/var/lib/kubesolo}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"
RUN_TIMEOUT="${RUN_TIMEOUT:-900}"
NS="e2e-restart"
PROBE_IMAGE="${PROBE_IMAGE:-curlimages/curl:8.11.1}"
ENV_IMAGE="${ENV_IMAGE:-docker.io/portainer/agent:sts}"
DROPIN=/etc/systemd/system/kubesolo.service.d/zz-e2e-restart.conf
SECRET_BACKUP=/root/e2e-restart-agent-secret.json

log()  { printf '\033[1;36m[e2e]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ ok]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[err]\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || fail "run as root"
for cmd in kubectl python3 systemd-run curl; do
  command -v "$cmd" >/dev/null || fail "$cmd is required"
done

# The unit's output goes to the journal, not through this terminal: a write to
# a terminal that has gone would kill the test before its cleanup ran.
if [ -z "${INVOCATION_ID:-}" ]; then
  journalctl -fu kubesolo-e2e-restart -o cat -n 0 &
  follower=$!
  rc=0
  systemd-run --unit=kubesolo-e2e-restart --description="KubeSolo restart e2e" \
    --collect --wait --quiet \
    --setenv=KUBESOLO_CONFIG="$CONFIG" --setenv=KUBESOLO_PATH="$DATA" \
    --setenv=HEALTH_TIMEOUT="$HEALTH_TIMEOUT" --setenv=RUN_TIMEOUT="$RUN_TIMEOUT" \
    --setenv=PROBE_IMAGE="$PROBE_IMAGE" --setenv=ENV_IMAGE="$ENV_IMAGE" \
    bash "$(readlink -f "$0")" "$@" || rc=$?
  sleep 2
  kill "$follower" 2>/dev/null
  exit "$rc"
fi

k() { kubectl --kubeconfig "$DATA/pki/admin/admin.kubeconfig" "$@"; }

# agent_api calls the API the way the agent does: from a pod, through the
# socket directory KubeSolo mounts into the agent's pod.
agent_api() { k -n "$NS" exec probe -- curl -sS -m 10 --unix-socket /agent-api/kubesolo.sock "$@"; }

json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

# pods lists every pod with what a restart must not change.
pods() {
  k get pods -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} uid={.metadata.uid} container={.status.containerStatuses[0].containerID} restarts={.status.containerStatuses[0].restartCount}{"\n"}{end}' | sort
}

setting() { agent_api http://localhost/api/v1/config | json "d['config']$1"; }

# restart starts a restart through the agent socket and waits for it to end,
# printing its result. The socket is gone while KubeSolo restarts, and kubectl
# exec with it, so failed polls are expected.
restart() {
  local body="$1" resp id deadline st
  resp=$(agent_api -X POST -d "$body" http://localhost/api/v1/restart)
  id=$(echo "$resp" | json "d.get('id','')")
  [ -n "$id" ] || fail "restart not accepted: $resp"
  log "restart $id accepted" >&2
  deadline=$((SECONDS + RUN_TIMEOUT))
  while [ $SECONDS -lt $deadline ]; do
    sleep 5
    st=$(agent_api http://localhost/api/v1/status 2>/dev/null) || continue
    if echo "$st" | json "d['upgrade'].get('last',{}).get('id')" | grep -qx "$id"; then
      echo "$st" | json "d['upgrade']['last']['result'] + ' ' + d['upgrade']['last'].get('error','')"
      return
    fi
  done
  fail "restart $id did not finish within ${RUN_TIMEOUT}s"
}

# wait_known_good waits for KubeSolo to record the configuration file as
# known-good, which it does once it has run healthy on it for a minute.
wait_known_good() {
  local deadline=$((SECONDS + 300))
  while [ $SECONDS -lt $deadline ]; do
    cmp -s "$CONFIG" "$DATA/upgrade/config.good.yaml" && return
    sleep 5
  done
  fail "KubeSolo did not record the configuration as known-good"
}

# restart_service restarts KubeSolo the way an operator would, and waits for it.
restart_service() {
  local deadline=$((SECONDS + RUN_TIMEOUT))
  systemctl daemon-reload
  systemctl restart kubesolo
  sleep 5
  until k get --raw /readyz >/dev/null 2>&1 && [ -S "$DATA/agent-api/kubesolo.sock" ]; do
    [ $SECONDS -lt $deadline ] || fail "KubeSolo did not come back"
    sleep 5
  done
}

# wait_agent_image waits for KubeSolo to move the agent to $1 and for it to run.
wait_agent_image() {
  local deadline=$((SECONDS + 300))
  until [ "$(k -n portainer get deploy portainer-agent -o jsonpath='{.spec.template.spec.containers[0].image}')" = "$1" ]; do
    [ $SECONDS -lt $deadline ] || fail "the agent was not moved to $1"
    sleep 5
  done
  k -n portainer rollout status deploy/portainer-agent --timeout=300s >/dev/null || fail "the agent on $1 did not become ready"
  # The rollout is done once the new pod is ready; the old one is still going.
  until [ "$(k -n portainer get pods -l app=portainer-agent --no-headers | wc -l)" = 1 ]; do
    [ $SECONDS -lt $deadline ] || fail "the previous agent pod did not go"
    sleep 2
  done
}

restore_agent_secret() {
  k -n portainer replace -f "$SECRET_BACKUP" >/dev/null && rm -f "$SECRET_BACKUP"
  k -n portainer delete pod -l app=portainer-agent --wait=false >/dev/null
}

expect_same_pods() {
  local before="$1" after
  after=$(pods)
  if [ "$before" != "$after" ]; then
    diff <(echo "$before") <(echo "$after") >&2 || true
    fail "$2"
  fi
}

# ── setup and cleanup ───────────────────────────────────────────────────────
BACKUP=$(mktemp)
cp -p "$CONFIG" "$BACKUP"
FAILED_BEFORE=$(ls "$CONFIG".failed-* 2>/dev/null || true)

cleanup() {
  local rc=$?
  set +e
  # A run still going finishes first: changing the file under it would have
  # it revert to, or verify, the wrong configuration. Only the kubesolo binary
  # itself is matched: a shell whose command line mentions the flag is not one.
  local deadline=$((SECONDS + RUN_TIMEOUT))
  while pgrep -f -- '^/usr/local/bin/kubesolo --upgrade-executor=' >/dev/null && [ $SECONDS -lt $deadline ]; do sleep 5; done
  [ -e "$SECRET_BACKUP" ] && restore_agent_secret
  if [ -e "$DROPIN" ] || ! cmp -s "$CONFIG" "$BACKUP"; then
    log "putting the original configuration back"
    rm -f "$DROPIN"
    cp -p "$BACKUP" "$CONFIG"
    systemctl daemon-reload
    systemctl restart kubesolo
    deadline=$((SECONDS + RUN_TIMEOUT))
    until k get --raw /readyz >/dev/null 2>&1 || [ $SECONDS -ge $deadline ]; do sleep 5; done
  fi
  for f in "$CONFIG".failed-*; do
    [ -e "$f" ] && ! grep -qxF "$f" <<<"$FAILED_BEFORE" && rm -f "$f"
  done
  k delete namespace "$NS" --ignore-not-found --wait=false >/dev/null 2>&1
  rm -f "$BACKUP"
  exit $rc
}
trap cleanup EXIT INT TERM

deadline=$((SECONDS + RUN_TIMEOUT))
until k get --raw /readyz >/dev/null 2>&1; do
  [ $SECONDS -lt $deadline ] || fail "the Kubernetes API server is not ready"
  sleep 5
done
[ -S "$DATA/agent-api/kubesolo.sock" ] || fail "no agent API socket: enable the API and configure an edge agent"

# A previous run's namespace may still be going, and a new one has no service
# account until the controller manager creates it.
k wait --for=delete namespace/"$NS" --timeout=300s >/dev/null 2>&1 || true
k create namespace "$NS" >/dev/null
deadline=$((SECONDS + 120))
until k -n "$NS" get serviceaccount default >/dev/null 2>&1; do
  [ $SECONDS -lt $deadline ] || fail "namespace $NS has no default service account"
  sleep 2
done
k -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: probe
spec:
  securityContext:
    runAsUser: 0
  containers:
    - name: curl
      image: $PROBE_IMAGE
      command: ["sleep", "infinity"]
      volumeMounts:
        - name: agent-api
          mountPath: /agent-api
  volumes:
    - name: agent-api
      hostPath:
        path: $DATA/agent-api
        type: Directory
YAML
k -n "$NS" wait --for=condition=Ready pod/probe --timeout=180s >/dev/null || fail "probe pod did not become Ready"
log "waiting for the current configuration to be known-good"
wait_known_good
ok "setup: probe pod running, configuration known-good"

# ── 1. a change is applied, and nothing is restarted ────────────────────────
metrics=$(setting "['metrics']['enabled']")
want=$([ "$metrics" = True ] && echo false || echo true)
log "1: changing metrics.enabled to $want through the agent socket"
agent_api -X PATCH -H 'Content-Type: application/merge-patch+json' -d "{\"metrics\":{\"enabled\":$want}}" http://localhost/api/v1/config >/dev/null
before=$(pods)
result=$(restart '{}')
[[ "$result" == succeeded* ]] || fail "1: restart ended: $result"
expect_same_pods "$before" "1: a pod or container changed across the restart"
# The restart is judged once the restarted KubeSolo has finished starting, not
# on the node and pod statuses the previous one left behind.
finished=$(date -d "$(agent_api http://localhost/api/v1/status | json "d['upgrade']['last']['finished']" | sed 's/\.[0-9]*//')" +%s)
[ "$(cat "$DATA/upgrade/started")" = "$(systemctl show -p MainPID --value kubesolo)" ] || fail "1: the running KubeSolo did not record that it started"
[ "$finished" -ge "$(stat -c %Y "$DATA/upgrade/started")" ] || fail "1: the restart was judged before KubeSolo finished starting"
code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:9105/metrics || true)
if [ "$want" = true ]; then [ "$code" = 200 ] || fail "1: metrics not served after enabling them ($code)"
else [ "$code" != 200 ] || fail "1: metrics still served after disabling them"; fi
ok "1: metrics.enabled=$want applied; every pod kept its container"
wait_known_good

# ── 2. an agent image that cannot run is reverted ───────────────────────────
image=$(setting "['portainer']['image']")
log "2: configuring an agent image that does not exist"
agent_api -X PATCH -H 'Content-Type: application/merge-patch+json' -d '{"portainer":{"image":"docker.io/portainer/agent:e2e-does-not-exist"}}' http://localhost/api/v1/config >/dev/null
before=$(pods | grep -v '^portainer/')
result=$(restart "{\"healthTimeoutSeconds\":$HEALTH_TIMEOUT}")
[[ "$result" == rolled-back*"Portainer agent"* ]] || fail "2: restart ended: $result"
[ "$(setting "['portainer']['image']")" = "$image" ] || fail "2: portainer.image was not put back"
k -n portainer rollout status deploy/portainer-agent --timeout=180s >/dev/null || fail "2: the agent did not come back"
after=$(pods | grep -v '^portainer/')
[ "$before" = "$after" ] || fail "2: a pod other than the agent's changed"
ok "2: reverted ($result); agent running $image again"

# ── 3. a configuration KubeSolo cannot run on is reverted ───────────────────
log "3: pointing runtime.endpoint at a socket that does not exist"
agent_api -X PATCH -H 'Content-Type: application/merge-patch+json' -d '{"runtime":{"endpoint":"unix:///run/e2e-no-such-runtime.sock"}}' http://localhost/api/v1/config >/dev/null
before=$(pods)
result=$(restart "{\"healthTimeoutSeconds\":$HEALTH_TIMEOUT}")
[[ "$result" == rolled-back*"did not become healthy"* ]] || fail "3: restart ended: $result"
[ "$(setting "['runtime']['endpoint']")" = "" ] || fail "3: runtime.endpoint was not put back"
expect_same_pods "$before" "3: a pod or container changed across the reverted restart"
ok "3: reverted ($result); every pod kept its container"

# ── 4. the agent's set-once settings are refused, not ignored ──────────────
log "4: changing portainer.edgeKey through the agent socket"
saved=$(mktemp); cp -p "$CONFIG" "$saved"
code=$(agent_api -o /dev/null -w '%{http_code}' -X PATCH -H 'Content-Type: application/merge-patch+json' -d '{"portainer":{"edgeKey":"e2e-other-key"}}' http://localhost/api/v1/config)
[ "$code" = 409 ] || fail "4: changing portainer.edgeKey answered $code, want 409"
cmp -s "$CONFIG" "$saved" || fail "4: a refused change modified the configuration file"
log "4: changing portainer.edgeID by hand, then asking for a restart"
sed -i 's|^  edgeID: .*|  edgeID: e2e-other-id|' "$CONFIG"
code=$(agent_api -o /dev/null -w '%{http_code}' -X POST http://localhost/api/v1/restart)
cp -p "$saved" "$CONFIG"; rm -f "$saved"
[ "$code" = 409 ] || fail "4: restart after a hand edit of portainer.edgeID answered $code, want 409"
ok "4: set-once agent settings refused through the API and at restart"

# ── 5. settings from the environment are not mistaken for changes ─────────
# KubeSolo applies flags and environment variables over the file. The agent it
# runs then differs from what the file says, which a restart must not take for
# a change to the file.
log "5: overriding the agent's image and edge ID from the environment"
mkdir -p "$(dirname "$DROPIN")"
printf '[Service]\nEnvironment=KUBESOLO_PORTAINER_EDGE_IMAGE=%s\nEnvironment=KUBESOLO_PORTAINER_EDGE_ID=e2e-env-edge-id\n' "$ENV_IMAGE" > "$DROPIN"
restart_service
wait_agent_image "$ENV_IMAGE"
wait_known_good
before=$(pods)
result=$(restart "{\"healthTimeoutSeconds\":$HEALTH_TIMEOUT}")
[[ "$result" == succeeded* ]] || fail "5: restart ended: $result"
expect_same_pods "$before" "5: a pod or container changed across the restart"
rm -f "$DROPIN"
restart_service
wait_agent_image "$image"
ok "5: restart with the agent's settings from the environment succeeded; every pod kept its container"
wait_known_good

# ── 6. an agent already crash-looping does not fail the restart ───────────
log "6: making the agent crash-loop before the restart"
k -n portainer get secret portainer-agent-edge-key -o json |
  python3 -c "import json,sys; d=json.load(sys.stdin); [d['metadata'].pop(x,None) for x in ('resourceVersion','uid','creationTimestamp','managedFields')]; print(json.dumps(d))" > "$SECRET_BACKUP"
k -n portainer patch secret portainer-agent-edge-key -p '{"stringData":{"edge.key":"e2e-not-a-key"}}' >/dev/null
k -n portainer delete pod -l app=portainer-agent --wait=true >/dev/null
deadline=$((SECONDS + 240))
until [ "$(k -n portainer get pods -l app=portainer-agent -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}')" -ge 1 ] 2>/dev/null; do
  [ $SECONDS -lt $deadline ] || fail "6: the agent did not crash with a bad edge key"
  sleep 5
done
result=$(restart "{\"healthTimeoutSeconds\":$HEALTH_TIMEOUT}")
[[ "$result" == succeeded* ]] || fail "6: restart ended: $result"
restore_agent_secret
k -n portainer rollout status deploy/portainer-agent --timeout=300s >/dev/null || fail "6: the agent did not come back"
ok "6: restart with a crash-looping agent succeeded"

ok "restart e2e passed"
