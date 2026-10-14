package cli

import (
	"fmt"
	"time"

	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
)

// awaitContainer waits for a KubeSolo container that was just created to come
// up, and returns the address its API server is published on and its admin
// kubeconfig. It fails as soon as KubeSolo exits, and when the API server does
// not answer within two minutes.
func awaitContainer(name string) (apiAddr string, kc []byte, err error) {
	return awaitContainerFor(name, 120*time.Second)
}

// awaitHealthy waits, up to timeout, for a container that replaced another to
// come up with its node Ready, the health an upgrade or rollback requires.
func awaitHealthy(name string, timeout time.Duration) (apiAddr string, kc []byte, err error) {
	deadline := time.Now().Add(timeout)
	if apiAddr, kc, err = awaitContainerFor(name, timeout); err != nil {
		return apiAddr, kc, err
	}
	return apiAddr, kc, kubeconfig.WaitForNodeReady(kc, apiAddr, time.Until(deadline))
}

func awaitContainerFor(name string, timeout time.Duration) (apiAddr string, kc []byte, err error) {
	// The kubeconfig first: its wait notices KubeSolo exiting, and a container
	// that is not running has no published port to look up.
	kc, err = kubeconfig.WaitForContainerKubeconfig(service.ContainerNameFor(name), "")
	if err != nil {
		return "", nil, err
	}
	port, err := service.GetContainerAPIPort(name)
	if err != nil {
		return "", kc, err
	}
	apiAddr = fmt.Sprintf("127.0.0.1:%d", port)
	if err := kubeconfig.WaitForAPIServer(apiAddr, timeout); err != nil {
		return apiAddr, kc, err
	}
	return apiAddr, kc, nil
}

// containerFailed reports a KubeSolo container that did not come up, with the
// last lines it logged. The container is left in place to be inspected.
func containerFailed(p *ui.Printer, name string, err error) error {
	cname := service.ContainerNameFor(name)
	if lines, logErr := service.ContainerLogTail(name, 15); logErr == nil {
		p.Info("Last lines logged by " + cname + ":")
		for _, l := range lines {
			p.Info("  " + l)
		}
	}
	p.Info(fmt.Sprintf("The container is left in place; see the full log with:  docker logs %s", cname))
	return p.Fail("KubeSolo did not start", err)
}
