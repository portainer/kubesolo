package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
)

// flagTypes maps each KubeSolo flag to the JSON type of the setting behind it.
func flagTypes(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, d := range kubesoloconfig.Describe() {
		if d.Flag != "" {
			out[d.Flag] = d.Type
		}
	}
	return out
}

// assertFlagsUsable checks that every argument names a real KubeSolo setting and
// is written in a form the binary accepts.
//
// The second half is the part that matters. kingpin rejects --boolflag=value
// outright: booleans take the bare --flag or --no-flag form only. An installer
// emitting --debug=true produces a service that exits on every start, and
// nothing but running it would otherwise reveal that.
func assertFlagsUsable(t *testing.T, source string, args []string) {
	t.Helper()
	types := flagTypes(t)

	// Flags that are not settings, and so are absent from the registry.
	notSettings := map[string]bool{"config": true, "full": true, "print-config": true, "version": true}

	for _, arg := range args {
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		bare := strings.TrimPrefix(name, "no-")

		if notSettings[bare] {
			continue
		}

		kind, known := types[bare]
		if !known {
			t.Errorf("%s passes --%s, which is not a KubeSolo setting", source, name)
			continue
		}

		if kind == "boolean" && hasValue {
			t.Errorf("%s passes --%s=%s; kingpin rejects a value on a boolean flag — use --%s or --no-%s",
				source, name, value, bare, bare)
		}
		if kind != "boolean" && !hasValue {
			t.Errorf("%s passes a bare --%s, but it takes a %s value", source, name, kind)
		}
	}
}

// TestKubesoloFlagsAreUsable covers every setting kubesoloctl can pass through,
// with all of them populated at once.
func TestKubesoloFlagsAreUsable(t *testing.T) {
	cfg := &Config{
		Path:                    "/var/lib/kubesolo",
		APIServerExtraSANs:      "10.0.0.4,kubesolo.local",
		NodeIP:                  "10.0.0.5",
		MTU:                     "1400",
		PortainerEdgeID:         "edge-id",
		PortainerEdgeKey:        "edge-key",
		PortainerEdgeAsync:      true,
		PortainerEdgeImage:      "portainer/agent:lts",
		LocalStorage:            true,
		Debug:                   true,
		PprofServer:             true,
		D2K:                     true,
		D2KNamespace:            "workloads",
		CPUManagerPolicy:        "static",
		CPUManagerPolicyOptions: "full-pcpus-only=true",
		ReservedCPUs:            "0",
		SystemReserved:          "cpu=1,memory=500Mi",
	}
	assertFlagsUsable(t, "kubesoloctl", cfg.kubesoloFlags())
}

// TestInstallScriptFlagsAreUsable applies the same check to install.sh.
//
// The shell script is not covered by any Go test otherwise, and a flag it gets
// wrong only shows up as a service that will not start on a real machine.
func TestInstallScriptFlagsAreUsable(t *testing.T) {
	path := filepath.Join("..", "..", "..", "install.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Every flag the script appends to the kubesolo command line.
	pattern := regexp.MustCompile(`CMD_ARGS="[^"]*?((?:--[a-z0-9-]+(?:=[^ "]*)?[ ]?)+)"`)
	var args []string
	for _, m := range pattern.FindAllStringSubmatch(string(raw), -1) {
		args = append(args, strings.Fields(m[1])...)
	}
	if len(args) == 0 {
		t.Fatal("found no kubesolo flags in install.sh; the pattern needs updating")
	}
	t.Logf("checked %d flags from install.sh", len(args))

	assertFlagsUsable(t, "install.sh", args)
}

// TestToKubeSoloConfigMapsEverySetting checks that an installer setting reaches
// the configuration document, rather than being silently dropped between the two
// representations.
func TestToKubeSoloConfigMapsEverySetting(t *testing.T) {
	cfg := &Config{
		Path:                    "/opt/kubesolo",
		APIServerExtraSANs:      "10.0.0.4,kubesolo.local",
		NodeIP:                  "10.0.0.5",
		MTU:                     "1400",
		PortainerEdgeID:         "edge-id",
		PortainerEdgeKey:        "edge-key",
		PortainerEdgeAsync:      true,
		PortainerEdgeImage:      "portainerci/agent:develop",
		LocalStorage:            true,
		Debug:                   true,
		PprofServer:             true,
		D2K:                     true,
		D2KNamespace:            "workloads",
		CPUManagerPolicy:        "static",
		CPUManagerPolicyOptions: "full-pcpus-only=true",
		ReservedCPUs:            "0",
		SystemReserved:          "cpu=1,memory=500Mi",
	}

	doc, _, err := cfg.ToKubeSoloConfig()
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"path", doc.Path, "/opt/kubesolo"},
		{"nodeIP", doc.Network.NodeIP, "10.0.0.5"},
		{"mtu", doc.Network.MTU, 1400},
		{"extraSANs", strings.Join(doc.Kubernetes.APIServer.ExtraSANs, ","), "10.0.0.4,kubesolo.local"},
		{"edgeID", doc.Portainer.EdgeID, "edge-id"},
		{"edgeKey", doc.Portainer.EdgeKey, "edge-key"},
		{"async", doc.Portainer.Async, true},
		{"image", doc.Portainer.Image, "docker.io/portainerci/agent:develop"},
		{"localPath", doc.Storage.LocalPath.Enabled, true},
		{"debug", doc.Logging.Debug, true},
		{"pprof", doc.Logging.Pprof, true},
		{"d2k", doc.D2K.Enabled, true},
		{"d2kNamespace", doc.D2K.Namespace, "workloads"},
		{"cpuPolicy", doc.Kubernetes.Kubelet.CPUManager.Policy, types.CPUManagerPolicyStatic},
		{"reservedCPUs", doc.Kubernetes.Kubelet.CPUManager.ReservedCPUs, "0"},
		{"policyOptions", doc.Kubernetes.Kubelet.CPUManager.PolicyOptions["full-pcpus-only"], "true"},
		{"systemReserved", doc.Kubernetes.Kubelet.SystemReserved["memory"], "500Mi"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestCmdArgsCollapsesToConfigFlag pins the point of the whole exercise: once a
// configuration file exists, the service command line is one flag.
func TestCmdArgsCollapsesToConfigFlag(t *testing.T) {
	cfg := &Config{Path: "/var/lib/kubesolo", Debug: true, D2K: true}

	if got := cfg.CmdArgs(); len(got) == 1 {
		t.Fatalf("without ConfigFile the full flag list is expected, got %v", got)
	}

	cfg.ConfigFile = types.DefaultConfigFile
	want := []string{"--config=" + types.DefaultConfigFile}
	got := cfg.CmdArgs()
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("CmdArgs() = %v, want %v", got, want)
	}
}

// TestInstallScriptUsageMatchesItsParser checks install.sh's own --help output
// against the flags it actually accepts.
//
// This exists because a global search-and-replace over the script once rewrote
// "--portainer-edge-async=true|false" in the usage text while fixing the value
// passed to the binary, leaving a flag form the parser does not accept. Nothing
// caught it: the flag tests above only read the lines that build the kubesolo
// command line.
func TestInstallScriptUsageMatchesItsParser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)

	// The case labels of the script's own argument parser, e.g. `--path=*)`.
	accepted := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)(=\*)?\)`).FindAllStringSubmatch(script, -1) {
		accepted[m[1]] = true
	}
	if len(accepted) == 0 {
		t.Fatal("found no argument-parser cases in install.sh; the pattern needs updating")
	}

	// The flags advertised in the usage text.
	usage := regexp.MustCompile(`echo "\s+(--[a-z0-9-]+)(\S*)`)
	for _, m := range usage.FindAllStringSubmatch(script, -1) {
		flag, form := m[1], m[2]

		if !accepted[flag] {
			t.Errorf("install.sh --help advertises %s, which its parser does not accept", flag)
			continue
		}
		// A flag taking a value must be shown as --flag=... or --flag[=...] for
		// one where the value is optional. Anything else tells the reader to
		// type something the parser will not accept.
		if form != "" && !strings.HasPrefix(form, "=") && !strings.HasPrefix(form, "[=") {
			t.Errorf("install.sh --help shows %s%s; the parser expects %s=<value>", flag, form, flag)
		}
	}
}
