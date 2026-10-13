// cmd/kubesoloctl is the management CLI for KubeSolo — a single-node
// Kubernetes distribution for constrained edge environments.
//
// It is built with CGO_ENABLED=0 so a single binary per architecture covers
// both glibc and musl systems.
package main

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/internal/cli"
	"github.com/portainer/kubesolo/internal/syspath"
)

var (
	Version   = "dev"
	BuildDate = "unknown"
	Commit    = "unknown"
)

func main() {
	// Pre-flight looks tools up on PATH, and KubeSolo inherits it when started as
	// a daemon or in the foreground; a non-login root shell leaves sbin off it.
	syspath.Ensure()

	cli.SetVersionInfo(Version, Commit, BuildDate)
	if err := cli.Execute(); err != nil {
		// ui.Printer.Fail returns an error with an empty message (already
		// displayed). Only print if there is an actual message to show.
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}
