package main

import "testing"

func TestNormaliseImageRef(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"docker.io/portainer/agent:lts", "docker.io/portainer/agent:lts"},
		{"portainer/agent:lts", "docker.io/portainer/agent:lts"},
		{"portainerci/agent:develop", "docker.io/portainerci/agent:develop"},
		{"portainerci/agent", "docker.io/portainerci/agent:latest"},
		{"ghcr.io/portainer/agent:1.0", "ghcr.io/portainer/agent:1.0"},
		{"localhost:5000/agent:dev", "localhost:5000/agent:dev"},
	}

	for _, tt := range tests {
		got, err := normaliseImageRef(tt.in)
		if err != nil {
			t.Errorf("normaliseImageRef(%q) returned error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("normaliseImageRef(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	if _, err := normaliseImageRef("NOT A REF"); err == nil {
		t.Errorf("normaliseImageRef should reject an invalid reference")
	}
}
