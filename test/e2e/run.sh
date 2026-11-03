#!/usr/bin/env bash
# End-to-end entrypoint: boot KubeSolo, run smoke and authentication checks, always tear down.
#
# Usage:
#   IMAGE=portainerci/kubesolo:pr-12 test/e2e/run.sh
#   IMAGE=portainer/kubesolo:dev KUBESOLOCTL=./dist/kubesoloctl test/e2e/run.sh

IMAGE="${IMAGE:-portainerci/kubesolo:develop}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

# Always tear down, whatever happens — including fixture build failures.
trap 'bash "$HERE/teardown.sh"' EXIT

# Host directory bind-mounted into the container. authentication.sh fills it
# after the cluster is up; boot only needs the directory to exist.
E2E_AUTH_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kubesolo-e2e-auth.XXXXXX")"
export E2E_AUTH_DIR

bash "$HERE/boot.sh"
bash "$HERE/smoke.sh"
# Manifest deployment tiers (skip with E2E_SKIP_MANIFESTS=1 for a fast smoke-only run).
if [ "${E2E_SKIP_MANIFESTS:-0}" != "1" ]; then
  bash "$HERE/manifests.sh"
fi
bash "$HERE/authentication.sh"

ok "e2e run succeeded"
