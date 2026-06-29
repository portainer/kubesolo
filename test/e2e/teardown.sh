#!/usr/bin/env bash
# Tear down the KubeSolo container and its data. Best-effort and idempotent —
# safe to run even if install never succeeded. Never fails the run.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh" || true

log "tearing down KubeSolo ($KS_NAME)"
"$KUBESOLOCTL" uninstall --name "$KS_NAME" --purge --remove-kubeconfig >/dev/null 2>&1 || true

# Fallback: force-remove the container directly in case kubesoloctl is unhappy.
cname="$KS_NAME"
[ "$KS_NAME" != "kubesolo" ] && cname="kubesolo-$KS_NAME"
docker rm -f "$cname" >/dev/null 2>&1 || true

ok "teardown complete"
