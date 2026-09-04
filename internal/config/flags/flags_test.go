package flags

import "testing"

// TestTrackedFlagsHaveNoDefault pins the invariant the layered configuration
// depends on. Defaults live in internal/config.Defaults(). If a flag also
// carried one, kingpin would populate it before parsing and the loader could not
// tell a value the user typed from one it was born with — so the config file
// would never win over anything.
func TestTrackedFlagsHaveNoDefault(t *testing.T) {
	for _, m := range Application.Model().Flags {
		if _, ok := setByUser[m.Name]; !ok {
			continue // untracked: --version, --full, --config and kingpin's own
		}
		if len(m.Default) > 0 {
			t.Errorf("--%s declares default %q; defaults belong in internal/config.Defaults()", m.Name, m.Default)
		}
	}
}

// TestEveryConfigurableFlagIsTracked catches a flag added without the tracked()
// wrapper, which would silently never reach the config loader.
func TestEveryConfigurableFlagIsTracked(t *testing.T) {
	untracked := map[string]bool{"full": true, "config": true}

	for _, m := range Application.Model().Flags {
		if m.Envar == "" || untracked[m.Name] {
			continue
		}
		if _, ok := setByUser[m.Name]; !ok {
			t.Errorf("--%s carries %s but is not wrapped in tracked()", m.Name, m.Envar)
		}
	}
}

// TestSetByUserAndValues covers the two accessors the loader consumes.
func TestSetByUserAndValues(t *testing.T) {
	if _, err := Application.Parse([]string{"--node-ip=1.2.3.4", "--debug"}); err != nil {
		t.Fatal(err)
	}

	set := SetByUser()
	if !set["node-ip"] {
		t.Error("node-ip was given on the command line")
	}
	if !set["debug"] {
		t.Error("debug was given on the command line")
	}
	if set["mtu"] {
		t.Error("mtu was not given on the command line")
	}

	values := Values()
	if got := values["node-ip"]; got != "1.2.3.4" {
		t.Errorf("values[node-ip] = %q, want 1.2.3.4", got)
	}
	if got := values["debug"]; got != "true" {
		t.Errorf("values[debug] = %q, want true", got)
	}
}
