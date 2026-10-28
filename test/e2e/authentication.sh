#!/usr/bin/env bash
# Authentication checks against the container cluster run.sh already booted.
# Mirrors upstream's reload cases (test/integration/apiserver/oidc):
#
#   1. Before the file is set, an issuer token is rejected.
#   2. The file is enabled with one docker restart and maps the token's user.
#   3. A token for another audience is rejected.
#   4. A new valid file is loaded without a restart.
#   5. An invalid file is rejected and the last valid one stays in use.
#   6. Correcting the file loads it, still without a restart.
# Requires go (host GOHOSTOS/GOHOSTARCH), curl, docker, kubectl, and kubesoloctl.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

RELOAD_TIMEOUT="${RELOAD_TIMEOUT:-180}"
AUTH_IN_CONTAINER="/etc/kubesolo/e2e-auth/authentication.yaml"
AUDIENCE="kubesolo-e2e"
issuer_pid=""

[ -n "${E2E_AUTH_DIR:-}" ] || fail "E2E_AUTH_DIR is required (run.sh creates it)"
[ -d "$E2E_AUTH_DIR" ] || fail "E2E_AUTH_DIR does not exist: $E2E_AUTH_DIR"
require_cmd go
require_cmd curl
require_cmd docker
require_cmd "$KUBECTL"
command -v "$KUBESOLOCTL" >/dev/null 2>&1 || [ -x "$KUBESOLOCTL" ] || fail "kubesoloctl not found: $KUBESOLOCTL"

cleanup() {
  local rc=$?
  trap - EXIT
  if [ "$rc" -ne 0 ]; then
    dump_diagnostics || true
    if [ -f "$E2E_AUTH_DIR/issuer.log" ]; then
      cat "$E2E_AUTH_DIR/issuer.log" >&2
    fi
  fi
  if [ -n "$issuer_pid" ]; then
    kill "$issuer_pid" 2>/dev/null || true
    wait "$issuer_pid" 2>/dev/null || true
  fi
  exit "$rc"
}
trap cleanup EXIT

runtime_stamp() {
  docker inspect -f '{{.State.Pid}} {{.State.StartedAt}} {{.RestartCount}}' "$(container_name)"
}

# write_config <username expression> replaces the file atomically, as upstream
# does, so kube-apiserver never reads a partial file.
write_config() {
  cat >"$E2E_AUTH_DIR/.authentication.yaml.tmp" <<EOF
apiVersion: apiserver.config.k8s.io/v1
kind: AuthenticationConfiguration
jwt:
- issuer:
    url: $issuer
    audiences:
    - $AUDIENCE
    certificateAuthority: |
$(sed 's/^/      /' "$E2E_AUTH_DIR/ca.crt")
  claimMappings:
    username:
      expression: "$1"
EOF
  mv "$E2E_AUTH_DIR/.authentication.yaml.tmp" "$E2E_AUTH_DIR/authentication.yaml"
}

# token_status [audience] prints the HTTP status the API server answers a fresh
# issuer token with. 401 means the token was not accepted.
token_status() {
  local token
  token="$(curl -fsS --cacert "$E2E_AUTH_DIR/ca.crt" "$issuer/token${1:+?aud=$1}")" || return 1
  curl -sS -o /dev/null -w '%{http_code}' --cacert "$E2E_AUTH_DIR/cluster-ca.crt" \
    -H "Authorization: Bearer $token" "$server/api"
}

# reload_failures sums kube-apiserver's failed authentication file reloads.
reload_failures() {
  kc get --raw /metrics | awk '/^apiserver_authentication_config_controller_automatic_reloads_total\{.*status="failure"/ { n += $NF } END { print n + 0 }'
}

reload_failed_since() { [ "$(reload_failures)" -gt "$1" ]; }

# whoami_is <username> sends a fresh issuer token with no client certificate.
whoami_is() {
  local token user
  token="$(curl -fsS --cacert "$E2E_AUTH_DIR/ca.crt" "$issuer/token")" || return 1
  user="$(KUBECONFIG=/dev/null "$KUBECTL" --server "$server" --certificate-authority "$E2E_AUTH_DIR/cluster-ca.crt" \
    --token "$token" auth whoami -o jsonpath='{.status.userInfo.username}' 2>/dev/null)" || return 1
  [ "$user" = "$1" ]
}

log "building authentication fixture"
# Single-file build: the fixture is stdlib-only and must be the host binary.
CGO_ENABLED=0 GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" \
  go build -o "$E2E_AUTH_DIR/oidc" "$HERE/oidc/main.go"

gateway="$(docker inspect -f '{{range $name, $net := .NetworkSettings.Networks}}{{println $net.Gateway}}{{end}}' "$(container_name)" \
  | awk 'NF { print; exit }')"
[ -n "$gateway" ] || fail "container network gateway is empty ($(container_name))"
log "issuer advertise address: $gateway"

"$E2E_AUTH_DIR/oidc" "$E2E_AUTH_DIR" "$gateway" "$AUDIENCE" >"$E2E_AUTH_DIR/issuer.log" 2>&1 &
issuer_pid=$!
wait_for 30 1 "issuer URL" test -s "$E2E_AUTH_DIR/issuer-url" \
  || fail "issuer did not become ready (see $E2E_AUTH_DIR/issuer.log)"
issuer="$(cat "$E2E_AUTH_DIR/issuer-url")"

server="$(kc config view --minify --raw -o jsonpath='{.clusters[0].cluster.server}')"
kc config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' \
  | base64 --decode >"$E2E_AUTH_DIR/cluster-ca.crt"

status="$(token_status)"
[ "$status" = 401 ] || fail "1: a JWT was answered $status before an authentication file was set, want 401"
ok "1: JWT rejected without an authentication file"

write_config "'k8s-' + claims.sub"
"$KUBESOLOCTL" config set --name "$KS_NAME" kubernetes.apiServer.authenticationConfigFile "$AUTH_IN_CONTAINER"
log "restarting $(container_name) once so the authentication file is read"
docker restart "$(container_name)" >/dev/null
wait_for "$READY_TIMEOUT" 3 "API readiness after authentication restart" kc get --raw /readyz >/dev/null \
  || fail "API did not become ready after authentication restart"
runtime="$(runtime_stamp)"

wait_for "$RELOAD_TIMEOUT" 3 "JWT user k8s-john_doe" whoami_is k8s-john_doe \
  || fail "2: JWT authentication did not map k8s-john_doe"
ok "2: authentication file applied: k8s-john_doe"

status="$(token_status other-audience)"
[ "$status" = 401 ] || fail "3: a JWT for another audience was answered $status, want 401"
ok "3: JWT for another audience rejected"

write_config "'panda-' + claims.sub"
wait_for "$RELOAD_TIMEOUT" 3 "JWT user panda-john_doe" whoami_is panda-john_doe \
  || fail "4: authentication file reload did not map panda-john_doe"
ok "4: authentication file reloaded: panda-john_doe"

failures="$(reload_failures)"
# The username expression does not compile.
write_config "claims.("
wait_for "$RELOAD_TIMEOUT" 3 "a failed authentication file reload" reload_failed_since "$failures" \
  || fail "5: kube-apiserver did not report a failed reload of the invalid file"
whoami_is panda-john_doe || fail "5: the last valid authentication file is no longer in use"
ok "5: invalid authentication file refused; panda-john_doe still mapped"

write_config "'k8s-' + claims.sub"
wait_for "$RELOAD_TIMEOUT" 3 "JWT user k8s-john_doe" whoami_is k8s-john_doe \
  || fail "6: corrected authentication file did not map k8s-john_doe"
[ "$(runtime_stamp)" = "$runtime" ] || fail "container restarted during authentication reloads"
ok "6: corrected authentication file loaded; no restart since it was enabled"
