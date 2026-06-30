#!/usr/bin/env bash
# End-to-end entrypoint: boot KubeSolo, run smoke checks, always tear down.
#
# Usage:
#   IMAGE=portainerci/kubesolo:pr-12 test/e2e/run.sh
#   IMAGE=portainer/kubesolo:dev KUBESOLOCTL=./dist/kubesoloctl test/e2e/run.sh

IMAGE="${IMAGE:-portainerci/kubesolo:develop}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

# Always tear down, whatever happens.
trap 'bash "$HERE/teardown.sh"' EXIT

bash "$HERE/boot.sh"
bash "$HERE/smoke.sh"
# Manifest deployment tiers (skip with E2E_SKIP_MANIFESTS=1 for a fast smoke-only run).
if [ "${E2E_SKIP_MANIFESTS:-0}" != "1" ]; then
  bash "$HERE/manifests.sh"
fi

ok "e2e run succeeded"
