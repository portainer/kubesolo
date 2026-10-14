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
	port, err := service.GetContainerAPIPort(name)
	if err != nil {
		return "", nil, err
	}
	apiAddr = fmt.Sprintf("127.0.0.1:%d", port)

	kc, err = kubeconfig.WaitForContainerKubeconfig(service.ContainerNameFor(name), "")
	if err != nil {
		return apiAddr, nil, err
	}
	if err := kubeconfig.WaitForAPIServer(apiAddr, 120*time.Second); err != nil {
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
