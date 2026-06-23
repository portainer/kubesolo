package service

import (
	"testing"

	"github.com/docker/go-connections/nat"
)

func TestParseContainerPorts(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		wantErr bool
		// containerPort/proto -> expected host port
		check map[nat.Port]string
	}{
		{name: "empty", spec: "", check: map[nat.Port]string{}},
		{name: "bare same-port", spec: "9001", check: map[nat.Port]string{"9001/tcp": "9001"}},
		{name: "explicit host:container", spec: "8080:80", check: map[nat.Port]string{"80/tcp": "8080"}},
		{name: "udp protocol", spec: "53/udp", check: map[nat.Port]string{"53/udp": "53"}},
		{name: "multi", spec: "9001,8080:80", check: map[nat.Port]string{"9001/tcp": "9001", "80/tcp": "8080"}},
		{name: "range maps 1:1", spec: "9000-9002", check: map[nat.Port]string{"9000/tcp": "9000", "9002/tcp": "9002"}},
		{name: "whitespace tolerated", spec: " 9001 , 8080:80 ", check: map[nat.Port]string{"9001/tcp": "9001", "80/tcp": "8080"}},
		{name: "bad protocol", spec: "53/sctp", wantErr: true},
		{name: "bad port", spec: "notaport", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, bindings, err := ParseContainerPorts(c.spec)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got none", c.spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.spec, err)
			}
			for port, wantHost := range c.check {
				b, ok := bindings[port]
				if !ok {
					t.Fatalf("%q: expected binding for %s, got none (bindings=%v)", c.spec, port, bindings)
				}
				if len(b) != 1 || b[0].HostPort != wantHost {
					t.Fatalf("%q: port %s mapped to %+v, want host %s", c.spec, port, b, wantHost)
				}
			}
		})
	}
}
