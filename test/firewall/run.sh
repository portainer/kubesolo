#!/usr/bin/env bash
# Runs the host firewall pre-flight check against a real firewall in a
# privileged container. Both installers' checks must fail with the firewall as
# the distro sets it up, and pass once the rules the check asks for are added.
#
# Usage:
#   KUBESOLOCTL=./dist/kubesoloctl KUBESOLO=./dist/kubesolo test/firewall/run.sh SCENARIO
#
# Needs jq on the machine running it.
#
# SCENARIO: rocky-iptables, rocky-firewalld, ubuntu-ufw, debian-nftables
set -euo pipefail

scenario="${1:?usage: $0 SCENARIO}"
KUBESOLOCTL="$(realpath "${KUBESOLOCTL:?set KUBESOLOCTL}")"
KUBESOLO="$(realpath "${KUBESOLO:?set KUBESOLO}")"
POD_CIDR=10.42.0.0/16

case "$scenario" in
  rocky-iptables)
    image=rockylinux/rockylinux:10
    # The rules iptables-services installs and loads at boot.
    setup='dnf install -y -q iptables-services && iptables-restore /etc/sysconfig/iptables'
    fix="iptables -I INPUT -s $POD_CIDR -j ACCEPT && iptables -I FORWARD -s $POD_CIDR -j ACCEPT && iptables -I FORWARD -d $POD_CIDR -j ACCEPT"
    ;;
  rocky-firewalld)
    image=rockylinux/rockylinux:10
    # No systemd in the container, so dbus and firewalld are started by hand.
    setup='dnf install -y -q firewalld dbus-daemon && mkdir -p /run/dbus && dbus-daemon --system --fork &&
      { firewalld --nofork --nopid >/tmp/firewalld.log 2>&1 & } &&
      for _ in $(seq 30); do firewall-cmd --state >/dev/null 2>&1 && exit 0; sleep 1; done; cat /tmp/firewalld.log; exit 1'
    fix="firewall-cmd --permanent --zone=trusted --add-source=$POD_CIDR && firewall-cmd --reload"
    ;;
  ubuntu-ufw)
    image=ubuntu:24.04
    setup='apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ufw >/dev/null && ufw --force enable'
    fix="ufw allow from $POD_CIDR && ufw route allow from $POD_CIDR && ufw route allow to $POD_CIDR"
    ;;
  debian-nftables)
    image=debian:13
    # iptables as well: KubeSolo needs it, and without it kubesoloctl check stops
    # at the netfilter tools check before it reaches the firewall.
    setup='apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nftables iptables >/dev/null && nft -f - <<EOF
table inet filter {
  chain input { type filter hook input priority 0; policy drop; ct state established,related accept; iif lo accept; }
  chain forward { type filter hook forward priority 0; policy drop; }
}
EOF'
    fix="nft add rule inet filter input ip saddr $POD_CIDR accept && nft add rule inet filter forward ip saddr $POD_CIDR accept && nft add rule inet filter forward ip daddr $POD_CIDR accept"
    ;;
  *)
    echo "unknown scenario: $scenario" >&2
    exit 2
    ;;
esac

name="kubesolo-firewall-$scenario"
docker rm -f "$name" >/dev/null 2>&1 || true
docker run -d --privileged --name "$name" \
  -v /lib/modules:/lib/modules:ro \
  -v "$KUBESOLOCTL":/usr/local/bin/kubesoloctl:ro \
  -v "$KUBESOLO":/usr/local/bin/kubesolo:ro \
  "$image" sleep infinity >/dev/null
trap 'docker rm -f "$name" >/dev/null' EXIT

failed=0

# expect WANT COMMAND runs COMMAND in the container and checks it passes or
# fails as WANT says, failing for the firewall rather than anything else.
expect() {
  local want="$1" cmd="$2" out rc=0
  out="$(docker exec "$name" sh -c "$cmd" 2>&1)" || rc=$?
  if { [ "$want" = pass ] && [ "$rc" -eq 0 ]; } ||
     { [ "$want" = fail ] && [ "$rc" -ne 0 ] && grep -q "pods cannot reach" <<<"$out"; }; then
    echo "ok    $scenario: $cmd ($want)"
  else
    echo "FAIL  $scenario: $cmd should ${want} (exit $rc):"
    sed 's/^/      /' <<<"$out"
    failed=1
  fi
}

# expect_json WANT runs kubesoloctl check --json and checks it prints one
# report whose firewall result is WANT, with the firewall's help when it fails,
# and exits non-zero only when it fails.
expect_json() {
  local want="$1" out rc=0 got msg
  out="$(docker exec "$name" kubesoloctl check --json 2>/dev/null)" || rc=$?
  got="$(jq -r '.checks[] | select(.name == "host firewall") | .result' <<<"$out" 2>/dev/null)"
  msg="$(jq -r '.checks[] | select(.name == "host firewall") | .message // ""' <<<"$out" 2>/dev/null)"
  if [ "$got" = "$want" ] &&
     { { [ "$want" = pass ] && [ "$rc" -eq 0 ]; } ||
       { [ "$want" = fail ] && [ "$rc" -ne 0 ] && grep -q "pods cannot reach" <<<"$msg"; }; }; then
    echo "ok    $scenario: kubesoloctl check --json ($want)"
  else
    echo "FAIL  $scenario: kubesoloctl check --json should report the firewall as $want (exit $rc, got '$got'):"
    sed 's/^/      /' <<<"$out"
    failed=1
  fi
}

docker exec "$name" sh -c "$setup"

expect fail "kubesoloctl check"
expect_json fail
expect fail "kubesolo --check-firewall"

docker exec "$name" sh -c "$fix"

expect pass "kubesoloctl check"
expect_json pass
expect pass "kubesolo --check-firewall"

exit "$failed"
