package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/spf13/cobra"
)

func rollbackCmd() *cobra.Command {
	var (
		name          string
		yes           bool
		detach        bool
		healthTimeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Restore the version, datastore and configuration from before the last upgrade",
		Long: `Undo the last upgrade: restore the previous KubeSolo binary, and the datastore
and configuration file as they were the moment it was replaced.

Everything written to the cluster since that upgrade is lost — deployments
created or changed, secrets, the lot. Workloads are restarted.

A rollback is only possible to the version immediately before the running one,
and only until the next upgrade replaces the backup.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollback(name, yes, detach, healthTimeout)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	f.BoolVar(&detach, "detach", false, "Return once the rollback has started instead of following it")
	f.DurationVar(&healthTimeout, "health-timeout", upgrade.DefaultHealthTimeout,
		"How long the restored version has to become healthy")
	f.StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance to roll back (container mode)")
	return cmd
}

func runRollback(name string, yes, detach bool, healthTimeout time.Duration) error {
	p := ui.New()
	p.Header("rollback")

	if err := upgrade.ValidateHealthTimeout(int(healthTimeout / time.Second)); err != nil {
		return p.Fail("--health-timeout", err)
	}
	if containerModeActive(name) {
		if detach {
			return p.Fail("rollback", fmt.Errorf("--detach applies to host installs only"))
		}
		return runContainerRollback(p, name, yes, healthTimeout)
	}
	if err := preflight.CheckRoot(); err != nil {
		return p.Fail("root check", err)
	}
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	running, err := installedVersion()
	if err != nil {
		return p.Fail("installed version", err)
	}
	target, err := upgrade.RollbackTarget(h.layout, running)
	if err != nil {
		return p.Fail("rollback", err)
	}

	p.Warn(fmt.Sprintf("this restores KubeSolo %s and the datastore as it was at %s, when %s was installed",
		target.From, target.Created.Local().Format(time.RFC1123), target.To))
	p.Warn("every change made to the cluster since then is lost")
	if !yes && !confirm("Roll back?") {
		p.Info("rollback cancelled")
		return nil
	}

	var id string
	if h.api != nil {
		accepted, err := h.api.Rollback(int(healthTimeout / time.Second))
		if err != nil {
			return p.Fail("rollback request", err)
		}
		id = accepted.ID
	} else {
		info, err := detect.Detect()
		if err != nil {
			return p.Fail("system detection", err)
		}
		// The rollback runs from the installed binary, the newer of the two:
		// the one being restored may predate the executor.
		if !supportsExecutor(upgrade.BinaryPath) {
			return p.Fail("rollback", fmt.Errorf("the installed KubeSolo %s cannot run a rollback", running))
		}
		id = upgrade.NewRunID()
		job := upgrade.Job{
			ID: id, Operation: upgrade.OpRollback,
			Request: upgrade.Request{Version: target.From, HealthTimeoutSeconds: int(healthTimeout / time.Second)},
			DataDir: h.layout.DataDir, ConfigFile: h.layout.ConfigFile, From: running,
		}
		if err := startLocally(h, job, upgrade.BinaryPath, info); err != nil {
			return p.Fail("rollback", err)
		}
	}
	p.OK("Rollback started", "run "+id)
	if detach {
		p.Info("follow it with: kubesoloctl status")
		return nil
	}

	p.Step("Rolling back")
	run, err := follow(p, h.layout, id)
	if err != nil {
		return p.Fail("rollback", err)
	}
	if err := reportRun(p, run); err != nil {
		return err
	}
	p.Done(fmt.Sprintf("KubeSolo rolled back from %s to %s", run.From, run.To))
	return nil
}

// runContainerRollback restores the container and datastore the last upgrade of
// a container instance replaced.
func runContainerRollback(p *ui.Printer, name string, yes bool, healthTimeout time.Duration) error {
	prev, err := service.InspectInstance(name)
	if err != nil {
		return p.Fail("KubeSolo", fmt.Errorf("container %q not found — is KubeSolo installed? (%w)", service.ContainerNameFor(name), err))
	}
	target, err := service.ReadRollbackTarget(name)
	if err != nil {
		return p.Fail("rollback", err)
	}
	if target == nil {
		return p.Fail("rollback", fmt.Errorf("there is no upgrade to roll back: %s has not been upgraded since it was installed or reset", service.ContainerNameFor(name)))
	}

	from := target.Image
	if target.Version != "" {
		from = target.Version + " (" + target.Image + ")"
	}
	p.Warn(fmt.Sprintf("this restores %s and the datastore as it was at %s, when %s was installed",
		from, target.Created.Local().Format(time.RFC1123), prev.Config.Image))
	p.Warn("every change made to the cluster since then is lost")
	if !yes && !confirm("Roll back?") {
		p.Info("rollback cancelled")
		return nil
	}

	p.Step("Rolling back to " + from)
	if err := service.PullImage(target.Image); err != nil {
		p.Warn("nothing was changed")
		return p.Fail("image", err)
	}
	if err := service.StopContainer(name); err != nil {
		return p.Fail("rollback", err)
	}
	if err := service.RestoreDatastore(name, target.Image, false); err != nil {
		if startErr := service.StartContainer(name); startErr == nil {
			p.Warn("nothing was changed: the container was started again")
		}
		return p.Fail("datastore restore", err)
	}
	if err := service.ReplaceContainer(name, &config.Config{Name: name, ContainerImage: target.Image}, target.Args, nil, prev); err != nil {
		return p.Fail("rollback", err)
	}
	apiAddr, data, err := awaitHealthy(name, healthTimeout)
	if err != nil {
		return containerFailed(p, name, err)
	}
	p.OK("Node ready", "https://"+apiAddr)
	if err := kubeconfig.MergeContainerKubeconfig(data, service.ContainerNameFor(name), "https://"+apiAddr); err != nil {
		p.Warn("kubeconfig not updated — " + err.Error())
	}
	p.Done("KubeSolo rolled back to " + from)
	return nil
}

func confirm(question string) bool {
	fmt.Printf("  %s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func statusCmd() *cobra.Command {
	var asJSON bool
	var name string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the running version, health, and the last upgrade or rollback",
		RunE: func(cmd *cobra.Command, args []string) error {
			if containerModeActive(name) {
				return runContainerStatus(name, asJSON)
			}
			return runStatus(asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the status as JSON")
	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance (container mode)")
	return cmd
}

// containerStatus is the status of a container instance.
type containerStatus struct {
	Container      string                  `json:"container"`
	Image          string                  `json:"image"`
	Version        string                  `json:"version,omitempty"`
	State          string                  `json:"state"`
	Restarts       int                     `json:"restarts"`
	APIServer      string                  `json:"apiServer,omitempty"`
	Healthy        bool                    `json:"healthy"`
	Health         string                  `json:"health"`
	RollbackTarget *service.RollbackTarget `json:"rollbackTarget,omitempty"`
}

func runContainerStatus(name string, asJSON bool) error {
	p := ui.New()
	cname := service.ContainerNameFor(name)
	insp, err := service.InspectInstance(name)
	if err != nil {
		return p.Fail("KubeSolo", fmt.Errorf("container %q not found — is KubeSolo installed? (%w)", cname, err))
	}

	st := containerStatus{Container: cname, Image: insp.Config.Image, State: insp.State.Status, Restarts: insp.RestartCount}
	st.Health = "unknown — the container is not running"
	if insp.State.Running {
		if v, err := service.ContainerVersion(name); err == nil {
			st.Version = v
		}
		port, err := service.GetContainerAPIPort(name)
		var kc []byte
		if err == nil {
			st.APIServer = fmt.Sprintf("https://127.0.0.1:%d", port)
			kc, err = kubeconfig.GetFromContainer(cname, "")
		}
		var ready bool
		if err == nil {
			ready, err = kubeconfig.NodeReady(kc, fmt.Sprintf("127.0.0.1:%d", port))
		}
		switch {
		case err != nil:
			st.Health = "unknown — " + err.Error()
		case ready:
			st.Healthy, st.Health = true, "healthy"
		default:
			st.Health = "unhealthy — the node is not Ready"
		}
	}
	if st.RollbackTarget, err = service.ReadRollbackTarget(name); err != nil {
		return p.Fail("rollback target", err)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}

	p.Header("status")
	p.Label("Instance", cname)
	if st.Version != "" {
		p.Label("Version", st.Version)
	}
	p.Label("Image", st.Image)
	state := st.State
	if st.Restarts > 0 {
		state = fmt.Sprintf("%s (%d restarts)", st.State, st.Restarts)
	}
	p.Label("Container", state)
	if st.APIServer != "" {
		p.Label("API server", st.APIServer)
	}
	p.Label("Health", st.Health)
	if t := st.RollbackTarget; t != nil {
		to := t.Image
		if t.Version != "" {
			to = t.Version + " (" + t.Image + ")"
		}
		p.Label("Rollback to", fmt.Sprintf("%s (backup of %s)", to, t.Created.Local().Format(time.RFC1123)))
	} else {
		p.Label("Rollback to", "none — no upgrade since the instance was installed or reset")
	}
	return nil
}

func runStatus(asJSON bool) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}

	var st *upgrade.Status
	if h.api != nil {
		if st, err = h.api.Status(); err != nil {
			return p.Fail("status", err)
		}
	} else {
		// Without the API, everything but health can still be read from disk.
		version, err := installedVersion()
		if err != nil {
			return p.Fail("installed version", err)
		}
		st = &upgrade.Status{Version: version, Upgrade: upgrade.ReadUpgradeStatus(h.layout, version)}
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}

	p.Header("status")
	p.Label("Version", st.Version)
	if h.api != nil {
		health := "healthy"
		if !st.Health.Healthy {
			var failing []string
			for _, name := range sortedKeys(st.Health.Checks) {
				if v := st.Health.Checks[name]; v != "ok" {
					failing = append(failing, name+": "+v)
				}
			}
			health = "unhealthy — " + strings.Join(failing, "; ")
		}
		p.Label("Health", health)
	} else {
		p.Label("Health", "unknown (the KubeSolo API is not enabled)")
	}
	if a := st.Agent; a != nil {
		agent := a.RunningImage
		if !a.InSync {
			agent = fmt.Sprintf("%s (configured: %s) %s", a.RunningImage, a.ConfiguredImage, a.Detail)
		}
		p.Label("Agent", agent)
	}

	u := st.Upgrade
	if u.InFlight && u.Current != nil {
		p.Label("In progress", fmt.Sprintf("%s %s → %s, %s (run %s)", u.Current.Operation, u.Current.From, u.Current.To, u.Current.Phase, u.Current.ID))
	}
	if r := u.Interrupted; r != nil {
		p.Label("Interrupted", fmt.Sprintf("%s %s → %s stopped at %s (run %s); KubeSolo closes it once healthy", r.Operation, r.From, r.To, r.Phase, r.ID))
	}
	if u.PendingVerify != "" {
		p.Label("Verifying", fmt.Sprintf("%s, started %d time(s)", u.PendingVerify, u.BootAttempts))
	}
	if r := u.Last; r != nil {
		line := fmt.Sprintf("%s %s → %s: %s, %s", r.Operation, r.From, r.To, r.Result, r.Finished.Local().Format(time.RFC1123))
		if r.Error != "" {
			line += " — " + r.Error
		}
		p.Label("Last run", line)
	}
	if t := u.RollbackTarget; t != nil {
		p.Label("Rollback to", fmt.Sprintf("%s (backup of %s)", t.From, t.Created.Local().Format(time.RFC1123)))
	} else {
		p.Label("Rollback to", "none — "+u.RollbackUnavailable)
	}
	if n := len(u.GuardEvents); n > 0 {
		p.Label("Boot guard", fmt.Sprintf("%s (%d restore(s) recorded)", u.GuardEvents[n-1], n))
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
