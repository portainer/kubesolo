package apiserver

import (
	"testing"

	"k8s.io/kubernetes/cmd/kube-apiserver/app"
)

// The apiserver dials the kubelet for logs, exec and attach. kubernetes.nodeName
// need not resolve in DNS, so the node's IP must come before its host name, or
// those calls fail with "lookup <node>: server misbehaving".
func TestKubeletPreferredAddressTypesPutsInternalIPFirst(t *testing.T) {
	command := app.NewAPIServerCommand(nil)
	if err := (&service{}).configureAPIServerFlags(command); err != nil {
		t.Fatal(err)
	}

	const name = "kubelet-preferred-address-types"

	flag := command.Flags().Lookup(name)
	if flag == nil {
		t.Fatalf("%s: flag not found on the kube-apiserver command", name)
	}

	if got, want := flag.Value.String(), "[InternalIP,ExternalIP,Hostname]"; got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}
