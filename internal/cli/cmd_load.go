package cli

import (
	"fmt"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func loadCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "load IMAGE [IMAGE...]",
		Short: "Load images from the container engine into a KubeSolo container instance",
		Long: `Copy images from the container engine kubesoloctl uses into a KubeSolo
container instance, so pods can run them without a registry — what
kind load docker-image does for a kind node.

Pods refer to a loaded image by the name it has in the engine. An image tagged
:latest, or with no tag, defaults to imagePullPolicy: Always and is pulled again;
give it another tag, or set imagePullPolicy: IfNotPresent.

Examples:
  docker build -t my-app:dev .
  kubesoloctl load my-app:dev
  kubesoloctl load my-app:dev my-worker:dev --name dev`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLoad(name, args)
		},
	}
	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance to load the images into")
	return cmd
}

func runLoad(name string, images []string) error {
	p := ui.New()
	p.Header("load")

	cname := service.ContainerNameFor(name)
	if !service.ContainerExists(name) {
		return p.Fail("KubeSolo", fmt.Errorf("container %q not found — load works on container instances", cname))
	}

	p.Step(fmt.Sprintf("Loading %d image(s) into %s", len(images), cname))
	names, err := service.LoadImages(name, images)
	if err != nil {
		return p.Fail("load", err)
	}
	for _, n := range names {
		p.OK("Loaded", n)
	}
	p.Done(fmt.Sprintf("%d image(s) loaded into %s", len(names), cname))
	return nil
}
