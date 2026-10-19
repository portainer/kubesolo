package network

import (
	"net"
	"testing"
)

// The probe pods' addresses come from the top of whatever pod network is
// configured, below its broadcast address and above the bridge's.
func TestFreePodIPs(t *testing.T) {
	for _, tc := range []struct {
		cidr string
		want []string
	}{
		{"10.42.0.0/16", []string{"10.42.255.254", "10.42.255.253"}},
		{"10.42.7.0/24", []string{"10.42.7.254", "10.42.7.253"}},
		{"192.168.50.0/30", []string{"192.168.50.2"}},
		{"192.168.50.0/31", nil},
	} {
		_, network, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, ip := range freePodIPs(network, 2) {
			got = append(got, ip.String())
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.cidr, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", tc.cidr, got, tc.want)
			}
		}
	}

	_, network, _ := net.ParseCIDR("10.42.7.0/24")
	if gw := ipv4At(network, 1).String(); gw != "10.42.7.1" {
		t.Errorf("gateway = %s, want 10.42.7.1", gw)
	}
}
