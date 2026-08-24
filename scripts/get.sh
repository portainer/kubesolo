#!/bin/sh
# KubeSolo bootstrap script.
# Detects the host architecture, downloads the correct kubesoloctl binary, and
# runs it. Requires curl or wget and a POSIX-compatible sh.
#
# Standard install:
#   curl -sfL https://get.kubesolo.io | sudo sh
#
# Pin a specific version:
#   curl -sfL https://get.kubesolo.io | KUBESOLO_VERSION=v1.2.0 sudo sh
#
# Pass flags to kubesoloctl (note the -s -- separator):
#   curl -sfL https://get.kubesolo.io | sudo sh -s -- --install-prereqs
#   curl -sfL https://get.kubesolo.io | sudo sh -s -- \
#       --portainer-edge-id=ID --portainer-edge-key=KEY
#
# Override the base URL (internal mirror, staging server, air-gap host):
#   KUBESOLO_INSTALLER_BASE_URL=https://files.example.com/kubesolo \
#   KUBESOLO_FLAT_URLS=1 curl -sfL ... | sudo sh -s -- --install-prereqs
#
# URL layouts:
#   versioned (default): <base>/<version>/kubesoloctl-linux-<arch>
#   flat (KUBESOLO_FLAT_URLS=1): <base>/kubesoloctl-linux-<arch>
set -e

KUBESOLO_VERSION="${KUBESOLO_VERSION:-v1.2.0}"
DEFAULT_BASE_URL="https://github.com/portainer/kubesolo/releases/download"
BASE_URL="${KUBESOLO_INSTALLER_BASE_URL:-$DEFAULT_BASE_URL}"
FLAT_URLS="${KUBESOLO_FLAT_URLS:-0}"

# detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)              ARCH=amd64   ;;
  aarch64)             ARCH=arm64   ;;
  *)
    printf 'error: unsupported architecture: %s\n' "$ARCH" >&2
    printf 'supported: x86_64 (amd64), aarch64 (arm64)\n' >&2
    exit 1
    ;;
esac

# resolve download URL
if [ "$FLAT_URLS" = "1" ]; then
  URL="${BASE_URL}/kubesoloctl-linux-${ARCH}"
else
  URL="${BASE_URL}/${KUBESOLO_VERSION}/kubesoloctl-linux-${ARCH}"
fi

# download
TMP=$(mktemp /tmp/kubesoloctl-XXXXXX)
trap 'rm -f "$TMP"' EXIT INT TERM HUP

printf 'downloading kubesoloctl %s (%s)...\n' "$KUBESOLO_VERSION" "$ARCH"

if command -v curl > /dev/null 2>&1; then
  curl -fsSL "$URL" -o "$TMP"
elif command -v wget > /dev/null 2>&1; then
  wget -qO "$TMP" "$URL"
else
  printf 'error: curl or wget is required to download kubesoloctl\n' >&2
  exit 1
fi

# run
# Run as a child (not exec) so the EXIT trap still fires and removes the
# downloaded binary afterwards — exec would replace this shell and skip cleanup.
# set -e propagates a non-zero exit status from kubesoloctl.
chmod +x "$TMP"
"$TMP" "$@"
