package cli

import (
	"strings"
	"testing"
)

func TestContainerUpgradeImage(t *testing.T) {
	cases := []struct {
		current, explicit, want, wantErr string
	}{
		{current: "portainer/kubesolo:v1.2.1", want: "portainer/kubesolo:v1.2.2"},
		{current: "docker.io/portainer/kubesolo:v1.2.1", want: "portainer/kubesolo:v1.2.2"},
		{current: "localhost:5099/kubesolo:custom", wantErr: "--image"},
		{current: "portainerci/kubesolo:pr-42", wantErr: "--image"},
		{current: "localhost:5099/kubesolo:custom", explicit: "localhost:5099/kubesolo:next", want: "localhost:5099/kubesolo:next"},
	}
	for _, c := range cases {
		got, err := containerUpgradeImage(c.current, c.explicit, "v1.2.2")
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want one mentioning %s", c.current, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.current, got, err, c.want)
		}
	}
}

func TestCheckContainerVersion(t *testing.T) {
	if err := checkContainerVersion("v1.2.1", "v1.2.2", false); err != nil {
		t.Errorf("upgrade: %v", err)
	}
	for _, target := range []string{"v1.2.2", "v1.2.1"} {
		err := checkContainerVersion("v1.2.2", target, false)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Errorf("%s over v1.2.2: err = %v, want a --force hint", target, err)
		}
		if err := checkContainerVersion("v1.2.2", target, true); err != nil {
			t.Errorf("%s with --force: %v", target, err)
		}
	}
}
