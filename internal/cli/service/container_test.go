package service

import (
	"testing"

	"github.com/portainer/kubesolo/internal/cli/config"
)

// An upgrade re-creates the container from the previous command line and a
// fresh config that has no --d2k of its own, so the command line decides.
func TestPublishD2K(t *testing.T) {
	cases := []struct {
		name string
		d2k  bool
		args []string
		want bool
	}{
		{"install with --d2k", true, []string{"--path=/var/lib/kubesolo", "--d2k"}, true},
		{"upgrade of a d2k container", false, []string{"--path=/var/lib/kubesolo", "--d2k", "--d2k-namespace=d2k"}, true},
		{"no d2k", false, []string{"--path=/var/lib/kubesolo"}, false},
		{"d2k namespace alone", false, []string{"--d2k-namespace=d2k"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := publishD2K(&config.Config{D2K: c.d2k}, c.args); got != c.want {
				t.Errorf("publishD2K = %v, want %v", got, c.want)
			}
		})
	}
}
