package network

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// The bridge and IPAM state of the embedded CNI network; see
// internal/core/embedded/config.go.
const (
	cniBridge  = "cni0"
	cniIPAMDir = "/var/lib/cni/networks/kubesolo-net"

	firewallDialTimeout = 3 * time.Second
)

// firewallHelp is shown whenever the firewall check fails.
var firewallHelp = fmt.Sprintf(`The host firewall is blocking Kubernetes traffic. KubeSolo needs it to accept
traffic from the pod network %[1]s to this host (INPUT) and to and from
the pod network when forwarded (FORWARD). Add these rules to your firewall,
make them persistent, then run the installer again:

  firewalld:  firewall-cmd --permanent --zone=trusted --add-source=%[1]s
              firewall-cmd --reload
  iptables:   iptables -I INPUT -s %[1]s -j ACCEPT
              iptables -I FORWARD -s %[1]s -j ACCEPT
              iptables -I FORWARD -d %[1]s -j ACCEPT
              then save them, e.g. service iptables save or netfilter-persistent save
  nftables:   add "ip saddr %[1]s accept" to the input chain and
              "ip saddr %[1]s accept" and "ip daddr %[1]s accept"
              to the forward chain in /etc/nftables.conf, then reload nftables
  ufw:        ufw allow from %[1]s
              ufw route allow from %[1]s
              ufw route allow to %[1]s

Other firewalls need equivalent rules.`, types.DefaultPodCIDR)

// CheckFirewall verifies that the host firewall lets pods reach the API server
// on the node IP and reach each other. It attaches two throwaway pods to the
// CNI bridge, as the bridge plugin would, and lets the kernel decide: every
// firewall frontend (iptables, nftables, firewalld, ufw) is evaluated as it
// will be for real pod traffic. nodeIPOverride is resolved as KubeSolo does.
func CheckFirewall(nodeIPOverride string) error {
	nodeIP, _, err := ResolveNodeIP(nodeIPOverride)
	if err != nil {
		return err
	}

	// Without br_netfilter, bridged traffic bypasses the firewall and the
	// pod-to-pod check would pass when it should not. KubeSolo loads it anyway.
	_ = exec.Command("modprobe", "br_netfilter").Run()

	gw, podNet, err := net.ParseCIDR(types.DefaultPodCIDR)
	if err != nil {
		return err
	}
	gw = gw.To4()
	gw[3] = 1

	bridge, created, err := ensureBridge(gw, podNet.Mask)
	if err != nil {
		return fmt.Errorf("could not set up the firewall check: %w", err)
	}
	if created {
		defer func() { _ = netlink.LinkDel(bridge) }()
	}

	ips := freePodIPs(podNet.IP.To4(), 2)
	podA, err := addPod(bridge, "kspf-a", ips[0], podNet.Mask, gw)
	if err != nil {
		return fmt.Errorf("could not set up the firewall check: %w", err)
	}
	defer podA.remove()
	podB, err := addPod(bridge, "kspf-b", ips[1], podNet.Mask, gw)
	if err != nil {
		return fmt.Errorf("could not set up the firewall check: %w", err)
	}
	defer podB.remove()

	// INPUT: pods reach the API server through the kubernetes service, which
	// kube-proxy translates to the node IP. When KubeSolo is already running it
	// owns the port, and the connection reaches it instead.
	apiAddr := net.JoinHostPort(nodeIP, "6443")
	if l, err := net.Listen("tcp", apiAddr); err == nil {
		defer func() { _ = l.Close() }()
	}
	if err := podA.dial(apiAddr); err != nil {
		return fmt.Errorf("pods cannot reach the API server at %s: %w\n\n%s", apiAddr, err, firewallHelp)
	}

	// FORWARD: pod to pod, on the DNS port CoreDNS serves.
	dnsAddr := net.JoinHostPort(ips[1].String(), "53")
	l, err := podB.listen(dnsAddr)
	if err != nil {
		return fmt.Errorf("could not set up the firewall check: %w", err)
	}
	defer func() { _ = l.Close() }()
	if err := podA.dial(dnsAddr); err != nil {
		return fmt.Errorf("pods cannot reach each other (%s to %s): %w\n\n%s", ips[0], dnsAddr, err, firewallHelp)
	}
	return nil
}

// ensureBridge returns the CNI bridge, creating it with the gateway address the
// bridge plugin gives it when it does not exist yet.
func ensureBridge(gw net.IP, mask net.IPMask) (netlink.Link, bool, error) {
	if br, err := netlink.LinkByName(cniBridge); err == nil {
		return br, false, nil
	}
	br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: cniBridge}}
	if err := netlink.LinkAdd(br); err != nil {
		return nil, false, err
	}
	if err := netlink.AddrAdd(br, &netlink.Addr{IPNet: &net.IPNet{IP: gw, Mask: mask}}); err != nil {
		_ = netlink.LinkDel(br)
		return nil, false, err
	}
	if err := netlink.LinkSetUp(br); err != nil {
		_ = netlink.LinkDel(br)
		return nil, false, err
	}
	return br, true, nil
}

// freePodIPs returns n addresses from the top of the /16 pod network that the
// CNI has not handed to a pod.
func freePodIPs(network net.IP, n int) []net.IP {
	var ips []net.IP
	for i := 254; len(ips) < n; i-- {
		ip := net.IPv4(network[0], network[1], 255, byte(i)).To4()
		if _, err := os.Stat(filepath.Join(cniIPAMDir, ip.String())); err == nil {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}

// probePod is a network namespace attached to the CNI bridge by a veth pair.
type probePod struct {
	ns   netns.NsHandle
	veth netlink.Link
}

func addPod(bridge netlink.Link, name string, ip net.IP, mask net.IPMask, gw net.IP) (*probePod, error) {
	// A run that was killed part way leaves its veth behind.
	if old, err := netlink.LinkByName(name); err == nil {
		_ = netlink.LinkDel(old)
	}

	ns, err := newNS()
	if err != nil {
		return nil, err
	}
	p := &probePod{ns: ns}

	p.veth = &netlink.Veth{
		LinkAttrs:     netlink.LinkAttrs{Name: name, MasterIndex: bridge.Attrs().Index},
		PeerName:      "eth0",
		PeerNamespace: netlink.NsFd(ns),
	}
	if err := netlink.LinkAdd(p.veth); err != nil {
		_ = ns.Close()
		return nil, err
	}
	if err := netlink.LinkSetUp(p.veth); err != nil {
		p.remove()
		return nil, err
	}

	h, err := netlink.NewHandleAt(ns)
	if err != nil {
		p.remove()
		return nil, err
	}
	defer h.Close()
	eth0, err := h.LinkByName("eth0")
	if err == nil {
		err = h.AddrAdd(eth0, &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: mask}})
	}
	if err == nil {
		err = h.LinkSetUp(eth0)
	}
	if err == nil {
		err = h.RouteAdd(&netlink.Route{LinkIndex: eth0.Attrs().Index, Gw: gw})
	}
	if err != nil {
		p.remove()
		return nil, err
	}
	return p, nil
}

func (p *probePod) remove() {
	_ = netlink.LinkDel(p.veth)
	_ = p.ns.Close()
}

// dial opens a TCP connection to addr from inside the pod.
func (p *probePod) dial(addr string) error {
	return inNS(p.ns, func() error {
		c, err := net.DialTimeout("tcp", addr, firewallDialTimeout)
		if err != nil {
			return err
		}
		return c.Close()
	})
}

// listen opens a TCP listener on addr inside the pod. The kernel completes the
// handshake without an Accept.
func (p *probePod) listen(addr string) (l net.Listener, err error) {
	err = inNS(p.ns, func() error {
		l, err = net.Listen("tcp", addr)
		return err
	})
	return l, err
}

// newNS creates a network namespace without leaving the calling thread in it.
func newNS() (netns.NsHandle, error) {
	runtime.LockOSThread()
	host, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		return netns.None(), err
	}
	defer func() { _ = host.Close() }()
	ns, err := netns.New()
	if err != nil {
		runtime.UnlockOSThread()
		return netns.None(), err
	}
	if err := switchBack(host); err != nil {
		_ = ns.Close()
		return netns.None(), err
	}
	return ns, nil
}

// inNS runs fn on a thread switched into ns. Sockets keep the namespace they
// were created in, so a connection or listener made by fn stays in the pod.
func inNS(ns netns.NsHandle, fn func() error) error {
	runtime.LockOSThread()
	host, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		return err
	}
	defer func() { _ = host.Close() }()
	if err := netns.Set(ns); err != nil {
		runtime.UnlockOSThread()
		return err
	}
	fnErr := fn()
	if err := switchBack(host); err != nil {
		return err
	}
	return fnErr
}

// switchBack returns the locked thread to host and unlocks it. A thread that
// cannot be returned stays locked, so Go discards it with the goroutine rather
// than reusing it in the wrong namespace.
func switchBack(host netns.NsHandle) error {
	if err := netns.Set(host); err != nil {
		return fmt.Errorf("could not return to the host network namespace: %w", err)
	}
	runtime.UnlockOSThread()
	return nil
}
