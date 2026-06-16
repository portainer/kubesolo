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
)

var (
	Version   = "dev"
	BuildDate = "unknown"
	Commit    = "unknown"
)

func main() {
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
