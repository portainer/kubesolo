package containerd

import (
	"strings"
	"testing"
)

func TestGenerateHostsTOML_PlainMirror(t *testing.T) {
	out := generateHostsTOML("docker.io", "https://mirror.corp")

	if !strings.Contains(out, `server = "https://registry-1.docker.io"`) {
		t.Errorf("expected well-known server line, got:\n%s", out)
	}
	if strings.Contains(out, "override_path") {
		t.Errorf("expected no override_path for plain hostname mirror, got:\n%s", out)
	}
	if !strings.Contains(out, `[host."https://mirror.corp"]`) {
		t.Errorf("expected host line, got:\n%s", out)
	}
}

func TestGenerateHostsTOML_HarborProxyCache(t *testing.T) {
	out := generateHostsTOML("docker.io", "https://harbor.corp/v2/docker.io")

	if !strings.Contains(out, `server = "https://registry-1.docker.io"`) {
		t.Errorf("expected well-known server line, got:\n%s", out)
	}
	if !strings.Contains(out, "override_path = true") {
		t.Errorf("expected override_path = true for path-based mirror, got:\n%s", out)
	}
}

func TestGenerateHostsTOML_Default(t *testing.T) {
	out := generateHostsTOML("_default", "https://airgap.internal")

	if strings.Contains(out, "server =") {
		t.Errorf("expected no server line for _default upstream, got:\n%s", out)
	}
	if !strings.Contains(out, `[host."https://airgap.internal"]`) {
		t.Errorf("expected host line, got:\n%s", out)
	}
}

func TestGenerateHostsTOML_UnknownUpstream(t *testing.T) {
	out := generateHostsTOML("myregistry.corp", "https://mirror.corp")

	if !strings.Contains(out, `server = "https://myregistry.corp"`) {
		t.Errorf("expected fallback server line for unknown upstream, got:\n%s", out)
	}
}

func TestHasNonRootPath(t *testing.T) {
	cases := []struct {
		url      string
		expected bool
	}{
		{"https://mirror.corp", false},
		{"https://mirror.corp/", false},
		{"https://harbor.corp/v2/docker.io", true},
		{"https://harbor.corp/docker.io", true},
	}

	for _, c := range cases {
		got := hasNonRootPath(c.url)
		if got != c.expected {
			t.Errorf("hasNonRootPath(%q) = %v, want %v", c.url, got, c.expected)
		}
	}
}
