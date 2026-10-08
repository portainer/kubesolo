package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/ui"
	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/pkg/components/configapi"
	"github.com/portainer/kubesolo/types"
)

// host is a host-mode KubeSolo as kubesoloctl finds it: where its files are,
// and its API, if it is serving one.
type host struct {
	layout upgrade.Layout

	// api is non-nil when KubeSolo answered on its socket with the lifecycle
	// endpoints, which a release before them lacks.
	api *configapi.Client
}

// findHost locates the installed KubeSolo.
func findHost() (*host, error) {
	if _, err := os.Stat(upgrade.BinaryPath); err != nil {
		return nil, fmt.Errorf("KubeSolo is not installed (%s: %w)", upgrade.BinaryPath, err)
	}

	cfg := kubesoloconfig.Defaults()
	if _, err := os.Stat(types.DefaultConfigFile); err == nil {
		if _, _, err := kubesoloconfig.Read(types.DefaultConfigFile, cfg); err != nil {
			return nil, err
		}
	} else if p := runningPathFlag(); p != "" {
		// A flag-based install from before the configuration file.
		cfg.Path = p
	}

	h := &host{layout: upgrade.NewLayout(cfg.Path, types.DefaultConfigFile)}
	if cfg.API.Enabled {
		socket := cfg.API.SocketPath
		if socket == "" {
			socket = filepath.Join(cfg.Path, types.DefaultAPISocketName)
		}
		client := configapi.NewClient(socket)
		if client.Available() {
			if _, err := client.Status(); err == nil {
				h.api = client
			}
		}
	}
	return h, nil
}

// runningPathFlag returns the --path the running KubeSolo was started with.
func runningPathFlag() string {
	for _, pid := range upgrade.MainPIDs(upgrade.BinaryPath) {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		args := strings.Split(string(raw), "\x00")
		for i, a := range args {
			if v, ok := strings.CutPrefix(a, "--path="); ok {
				return v
			}
			if a == "--path" && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

// supportsExecutor reports whether a kubesolo binary can run the upgrade
// executor. Releases before it reject the flag.
func supportsExecutor(binary string) bool {
	out, _ := exec.Command(binary, upgrade.ExecutorFlag+"=/nonexistent").CombinedOutput()
	return !strings.Contains(string(out), "unknown long flag")
}

// startLocally runs job with the executor in binary, without the API: the
// running KubeSolo has none, or is too old to have the lifecycle endpoints.
// The lock is taken here, so a concurrent API request or kubesoloctl is
// refused just as the API would refuse it.
func startLocally(h *host, job upgrade.Job, executorBinary string, info *detect.SystemInfo) error {
	lock, err := upgrade.TryLock(h.layout)
	if err != nil {
		return err
	}
	defer lock.Unlock()

	state, err := upgrade.LoadState(h.layout)
	if err != nil {
		return err
	}
	if state.InFlight() {
		return fmt.Errorf("%w (run %s, %s)", upgrade.ErrBusy, state.Current.ID, state.Current.Phase)
	}

	jobFile, err := upgrade.WriteJob(h.layout, job)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	run := &upgrade.Run{
		ID: job.ID, Operation: job.Operation, From: job.From, To: job.Request.Version,
		Phase: upgrade.PhaseQueued, Started: now, Updated: now,
	}
	run.Log("accepted by kubesoloctl")
	previous := state.Current
	state.Current = run
	if err := upgrade.SaveState(h.layout, state); err != nil {
		return err
	}
	if err := upgrade.SpawnExecutor(executorBinary, jobFile, info.InitSystem); err != nil {
		state.Current = previous
		_ = upgrade.SaveState(h.layout, state)
		_ = os.Remove(jobFile)
		return err
	}
	return nil
}

// follow prints a run's progress until it finishes and returns how it ended.
//
// It reads the state file rather than the API: the API goes away while KubeSolo
// restarts, and comes back as a different process — possibly an older one,
// after a rollback.
func follow(p *ui.Printer, l upgrade.Layout, id string) (*upgrade.Run, error) {
	printed := 0
	phase := upgrade.Phase("")
	lastProgress := time.Now()
	for {
		state, err := upgrade.LoadState(l)
		if err != nil {
			return nil, err
		}

		var run *upgrade.Run
		switch {
		case state.Current != nil && state.Current.ID == id:
			run = state.Current
		case state.Last != nil && state.Last.ID == id:
			run = state.Last
		}

		if run != nil {
			if run.Phase != phase {
				phase = run.Phase
				lastProgress = time.Now()
			}
			for ; printed < len(run.Messages); printed++ {
				p.Info(stripTimestamp(run.Messages[printed]))
				lastProgress = time.Now()
			}
			if !run.Finished.IsZero() {
				return run, nil
			}
			if run.PID > 0 && !state.InFlight() {
				return run, errors.New("the upgrade executor stopped without finishing; see `kubesoloctl status`")
			}
		} else if time.Since(lastProgress) > 2*time.Minute {
			return nil, fmt.Errorf("run %s never started; see `kubesoloctl status`", id)
		}
		time.Sleep(time.Second)
	}
}

func stripTimestamp(msg string) string {
	if _, rest, ok := strings.Cut(msg, " "); ok && len(msg) > 20 && msg[4] == '-' {
		return rest
	}
	return msg
}

// reportRun prints how a run ended and turns a failure into an error.
func reportRun(p *ui.Printer, run *upgrade.Run) error {
	switch run.Result {
	case upgrade.ResultSucceeded:
		return nil
	case upgrade.ResultAborted:
		p.Warn("nothing was changed: KubeSolo " + run.From + " kept running throughout")
		return p.Fail(string(run.Operation)+" aborted", errors.New(run.Error))
	case upgrade.ResultRolledBack:
		p.Warn("KubeSolo " + run.From + " was restored, with its datastore and configuration, and is running")
		return p.Fail(string(run.Operation)+" rolled back", errors.New(run.Error))
	default:
		p.Warn("KubeSolo needs attention: run `kubesoloctl status` and check the service logs")
		return p.Fail(string(run.Operation)+" failed", errors.New(run.Error))
	}
}

// installedVersion asks the installed binary for its version.
func installedVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return upgrade.BinaryVersion(ctx, upgrade.BinaryPath)
}
