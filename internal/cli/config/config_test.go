package config

import (
	"testing"
)

func TestCmdArgs_Minimal(t *testing.T) {
	cfg := &Config{Path: "/var/lib/kubesolo"}
	args := cfg.CmdArgs()
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d: %v", len(args), args)
	}
	if args[0] != "--path=/var/lib/kubesolo" {
		t.Errorf("unexpected arg: %q", args[0])
	}
}

func TestCmdArgs_AllFlags(t *testing.T) {
	cfg := &Config{
		Path:               "/data/kubesolo",
		APIServerExtraSANs: "192.168.1.1,my.host.local",
		PortainerEdgeID:    "edge-id-123",
		PortainerEdgeKey:   "edge-key-abc",
		PortainerEdgeAsync: true,
		LocalStorage:       true,
		Debug:              true,
		PprofServer:        true,
	}
	args := cfg.CmdArgs()

	want := map[string]bool{
		"--path=/data/kubesolo":                            true,
		"--apiserver-extra-sans=192.168.1.1,my.host.local": true,
		"--portainer-edge-id=edge-id-123":                  true,
		"--portainer-edge-key=edge-key-abc":                true,
		"--portainer-edge-async":                           true,
		"--local-storage":                                  true,
		"--debug":                                          true,
		"--pprof-server":                                   true,
	}

	if len(args) != len(want) {
		t.Fatalf("expected %d args, got %d: %v", len(want), len(args), args)
	}
	for _, a := range args {
		if !want[a] {
			t.Errorf("unexpected arg: %q", a)
		}
	}
}

func TestCmdArgs_FalseFieldsOmitted(t *testing.T) {
	cfg := &Config{
		Path:               "/var/lib/kubesolo",
		PortainerEdgeAsync: false,
		LocalStorage:       false,
		Debug:              false,
		PprofServer:        false,
	}
	args := cfg.CmdArgs()
	for _, a := range args {
		switch a {
		case "--portainer-edge-async", "--local-storage", "--debug", "--pprof-server":
			t.Errorf("false flag should be omitted, got: %q", a)
		}
	}
}

func TestCmdArgs_ContainerModeAddsFull(t *testing.T) {
	cfg := &Config{Path: "/var/lib/kubesolo", RunMode: RunModeContainer}
	if !hasArg(cfg.CmdArgs(), "--full") {
		t.Errorf("container mode must always pass --full, got: %v", cfg.CmdArgs())
	}
}

func TestCmdArgs_NonContainerModeOmitsFull(t *testing.T) {
	for _, mode := range []string{"", RunModeService, RunModeDaemon, RunModeForeground} {
		cfg := &Config{Path: "/var/lib/kubesolo", RunMode: mode}
		if hasArg(cfg.CmdArgs(), "--full") {
			t.Errorf("run mode %q must not pass --full, got: %v", mode, cfg.CmdArgs())
		}
	}
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestCmdArgs_EmptyStringsOmitted(t *testing.T) {
	cfg := &Config{
		Path:               "/var/lib/kubesolo",
		APIServerExtraSANs: "",
		PortainerEdgeID:    "",
		PortainerEdgeKey:   "",
	}
	args := cfg.CmdArgs()
	for _, a := range args {
		switch {
		case len(a) > len("--apiserver-extra-sans=") && a[:len("--apiserver-extra-sans=")] == "--apiserver-extra-sans=":
			t.Errorf("empty APIServerExtraSANs should be omitted, got: %q", a)
		case len(a) > len("--portainer-edge-id=") && a[:len("--portainer-edge-id=")] == "--portainer-edge-id=":
			t.Errorf("empty PortainerEdgeID should be omitted, got: %q", a)
		}
	}
}
