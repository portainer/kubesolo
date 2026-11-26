#!/usr/bin/env bash
# Installs KubeSolo on the host, waits for its system pods, runs
# uninstall.sh --remove-data and checks that nothing of the pod network stays:
# no bridge, veth link, route to the pod CIDR or pod network namespace, and
# nothing of the network in /var/lib/cni or /var/lib/kubelet. The state of
# another CNI network on the host, whose name starts with KubeSolo's, must stay.
#
# Usage, as root on a host that can be thrown away:
#   KUBESOLO=./dist/kubesolo test/uninstall/run.sh
#
# Needs kubectl. Docker must not be installed: install.sh refuses to run.
set -euo pipefail

KUBESOLO="$(realpath "${KUBESOLO:?set KUBESOLO}")"
READY_TIMEOUT="${READY_TIMEOUT:-300}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DATA=/var/lib/kubesolo
POD_CIDR=10.42.0.0/16
NETWORK=kubesolo-net
RESULTS=/var/lib/cni/results
NETWORKS=/var/lib/cni/networks

[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 2; }
command -v kubectl >/dev/null || { echo "kubectl is required" >&2; exit 2; }

# Another CNI network on the same host. Its name starts with KubeSolo's, so
# only the networkName in its result tells the two apart.
OTHER_NETWORK=kubesolo-net-2
OTHER_ID=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
OTHER_NETNS=e2e-other
OTHER_RESULT="$RESULTS/$OTHER_NETWORK-$OTHER_ID-eth0"
OTHER_LOOPBACK="$RESULTS/cni-loopback-$OTHER_ID-lo"
OTHER_ADDRESS="$NETWORKS/$OTHER_NETWORK/10.99.0.2"

cleanup() {
  ip netns delete "$OTHER_NETNS" 2>/dev/null || true
  rm -rf "$OTHER_RESULT" "$OTHER_LOOPBACK" "${NETWORKS:?}/${OTHER_NETWORK:?}"
  rmdir "$NETWORKS" "$RESULTS" /var/lib/cni 2>/dev/null || true
}
trap cleanup EXIT

k() { kubectl --kubeconfig "$DATA/pki/admin/admin.kubeconfig" "$@"; }

# running NAMESPACE PREFIX is true when a pod whose name starts with PREFIX
# is Running in NAMESPACE.
running() {
  k -n "$1" get pods --no-headers 2>/dev/null | awk -v p="$2" 'index($1, p) == 1 && $3 == "Running" { found = 1 } END { exit !found }'
}

wait_running() {
  local start=$SECONDS
  until running "$1" "$2"; do
    if [ $((SECONDS - start)) -ge "$READY_TIMEOUT" ]; then
      echo "FAIL  $1/$2 is not Running after ${READY_TIMEOUT}s"
      k get pods -A -o wide 2>&1 || true
      journalctl -u kubesolo --no-pager -n 100 2>&1 || true
      exit 1
    fi
    sleep 3
  done
  echo "ok    $1/$2 is Running"
}

failed=0

# check DESCRIPTION COMMAND... passes when COMMAND succeeds.
check() {
  local desc="$1"; shift
  if "$@"; then
    echo "ok    $desc"
  else
    echo "FAIL  $desc"
    failed=1
  fi
}

none() { [ -z "$("$@" 2>/dev/null)" ]; }

echo "Installing KubeSolo from $KUBESOLO"
sh "$ROOT/install.sh" --offline-install="$KUBESOLO"

wait_running kube-system coredns
wait_running local-path-storage local-path-provisioner

# The test means nothing unless the pods left what it checks for.
ip link show cni0 >/dev/null 2>&1 || { echo "FAIL  cni0 does not exist before uninstall"; exit 1; }
ls "$RESULTS/$NETWORK"-* >/dev/null 2>&1 || { echo "FAIL  no $NETWORK results before uninstall"; exit 1; }

ip netns add "$OTHER_NETNS"
mkdir -p "$NETWORKS/$OTHER_NETWORK"
echo "$OTHER_ID" > "$OTHER_ADDRESS"
printf '{"kind":"cniCacheV1","containerId":"%s","config":"e30=","ifName":"eth0","networkName":"%s","netns":"/var/run/netns/%s","result":{"cniVersion":"1.0.0"}}' \
  "$OTHER_ID" "$OTHER_NETWORK" "$OTHER_NETNS" > "$OTHER_RESULT"
printf '{"kind":"cniCacheV1","containerId":"%s","config":"e30=","ifName":"lo","networkName":"cni-loopback","netns":"/var/run/netns/%s","result":{"cniVersion":"1.0.0"}}' \
  "$OTHER_ID" "$OTHER_NETNS" > "$OTHER_LOOPBACK"

echo "Running uninstall.sh --remove-data"
echo y | sh "$ROOT/uninstall.sh" --remove-data

check "no cni0 link" none ip -o link show cni0
check "no veth links" none ip -o link show type veth
check "no route to $POD_CIDR" none ip route show "$POD_CIDR"
check "no cni-* network namespaces" none sh -c "ip netns list | grep '^cni-'"
check "no $NETWORKS/$NETWORK" test ! -e "$NETWORKS/$NETWORK"
check "no $NETWORK results" none sh -c "ls -d $RESULTS/$NETWORK-*-eth0 2>/dev/null | grep -v '/$OTHER_NETWORK-'"
check "no loopback results of KubeSolo's pods" none sh -c "ls $RESULTS/cni-loopback-* 2>/dev/null | grep -v '$OTHER_ID'"
check "no /var/lib/kubelet" test ! -e /var/lib/kubelet
check "no $DATA" test ! -e "$DATA"

check "$OTHER_NETWORK's result stays" test -f "$OTHER_RESULT"
check "$OTHER_NETWORK's loopback result stays" test -f "$OTHER_LOOPBACK"
check "$OTHER_NETWORK's addresses stay" test -f "$OTHER_ADDRESS"
check "$OTHER_NETWORK's network namespace stays" test -e "/run/netns/$OTHER_NETNS"

if [ "$failed" -ne 0 ]; then
  echo "Left on the host:"
  ip -br link || true
  ip route || true
  ip netns list || true
  ls -laR /var/lib/cni /var/lib/kubelet 2>/dev/null || true
fi
exit "$failed"
