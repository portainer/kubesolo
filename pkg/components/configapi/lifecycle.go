package configapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/types"
)

// Lifecycle is what the upgrade, rollback and status endpoints need from the
// running KubeSolo. The functions are fields so tests can replace them; main
// supplies the real ones.
type Lifecycle struct {
	// Version and Commit identify the running binary.
	Version string
	Commit  string

	// DataDir is KubeSolo's data directory.
	DataDir string

	// ContainerMode is set when KubeSolo runs in a container, which cannot
	// replace its own image.
	ContainerMode bool

	// Spawn starts the upgrade executor on a job file, detached from the
	// KubeSolo service.
	Spawn func(jobFile string) error

	// Health reports whether KubeSolo is working.
	Health func(ctx context.Context) upgrade.Health

	// AgentImage returns the image the Portainer agent Deployment runs, or ""
	// when there is no such Deployment.
	AgentImage func(ctx context.Context) (string, error)
}

func (s *Service) layout() upgrade.Layout {
	return upgrade.NewLayout(s.opts.Lifecycle.DataDir, s.opts.ConfigPath)
}

// handleUpgrade accepts an upgrade and hands it to the executor.
//
// It cannot do the work itself: stopping KubeSolo stops the process serving
// this request. So it checks what it can cheaply — the request, the lock, the
// version — and returns 202. Everything slower, from the download to the
// datastore dry-run, runs in the executor before anything is stopped, and a
// failure there is reported by /api/v1/status as an abort that changed nothing.
func (s *Service) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	lc := s.opts.Lifecycle
	if lc.ContainerMode {
		writeErrorFor(w, r, http.StatusNotImplemented, ErrorResponse{Error: upgrade.ErrContainerMode.Error()})
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req upgrade.Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: "body is not a valid upgrade request: " + err.Error()})
		return
	}
	if err := req.Validate(); err != nil {
		writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	if req.Source != "" {
		if fi, err := os.Stat(req.Source); err != nil || !fi.Mode().IsRegular() {
			writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Field: "source", Error: fmt.Sprintf("%s is not a file on this host", req.Source)})
			return
		}
	}
	if err := versionAllowed(lc.Version, req); err != nil {
		writeErrorFor(w, r, http.StatusConflict, ErrorResponse{Field: "version", Error: err.Error()})
		return
	}

	s.accept(w, r, upgrade.Job{
		ID: upgrade.NewRunID(), Operation: upgrade.OpUpgrade, Request: req,
		DataDir: lc.DataDir, ConfigFile: s.opts.ConfigPath, From: lc.Version,
	}, nil)
}

// versionAllowed refuses what is not an upgrade unless the caller insists.
func versionAllowed(running string, req upgrade.Request) error {
	if req.Force {
		return nil
	}
	target, _ := upgrade.ParseVersion(req.Version)
	current, err := upgrade.ParseVersion(running)
	switch {
	case err != nil:
		return fmt.Errorf("the running version %q is not a release; set force to replace it", running)
	case target.Compare(current) == 0:
		return fmt.Errorf("%s is already running", running)
	case target.Compare(current) < 0:
		return fmt.Errorf("%s is older than the running %s; a downgrade needs force. To undo the last upgrade, use POST /api/v1/rollback", req.Version, running)
	}
	return nil
}

// rollbackRequest is the optional body of POST /api/v1/rollback.
type rollbackRequest struct {
	HealthTimeoutSeconds int `json:"healthTimeoutSeconds,omitempty"`
}

// handleRollback restores the version, datastore and configuration file from
// before the last upgrade. Anything written to the cluster since is lost; the
// response and the run log say so.
func (s *Service) handleRollback(w http.ResponseWriter, r *http.Request) {
	lc := s.opts.Lifecycle
	if lc.ContainerMode {
		writeErrorFor(w, r, http.StatusNotImplemented, ErrorResponse{Error: upgrade.ErrContainerMode.Error()})
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var rb rollbackRequest
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rb); err != nil || rb.HealthTimeoutSeconds < 0 {
			writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: "body is not a valid rollback request"})
			return
		}
	}

	target, err := upgrade.RollbackTarget(s.layout(), lc.Version)
	if err != nil {
		writeErrorFor(w, r, http.StatusConflict, ErrorResponse{Error: err.Error()})
		return
	}

	s.accept(w, r, upgrade.Job{
		ID: upgrade.NewRunID(), Operation: upgrade.OpRollback,
		Request: upgrade.Request{Version: target.From, HealthTimeoutSeconds: rb.HealthTimeoutSeconds},
		DataDir: lc.DataDir, ConfigFile: s.opts.ConfigPath, From: lc.Version,
	}, target)
}

// accept records the run, starts the executor and answers 202. Taking the lock
// is what makes this the only writer: a second request, or kubesoloctl, finds
// it held or finds the run recorded and in flight.
func (s *Service) accept(w http.ResponseWriter, r *http.Request, job upgrade.Job, target *upgrade.Manifest) {
	l := s.layout()
	lock, err := upgrade.TryLock(l)
	if errors.Is(err, upgrade.ErrBusy) {
		writeErrorFor(w, r, http.StatusConflict, ErrorResponse{Error: upgrade.ErrBusy.Error()})
		return
	}
	if err != nil {
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	defer lock.Unlock()

	state, err := upgrade.LoadState(l)
	if err != nil {
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if state.InFlight() {
		writeErrorFor(w, r, http.StatusConflict, ErrorResponse{
			Error: fmt.Sprintf("%s (run %s, %s to %s, %s)", upgrade.ErrBusy, state.Current.ID, state.Current.From, state.Current.To, state.Current.Phase),
		})
		return
	}

	jobFile, err := upgrade.WriteJob(l, job)
	if err != nil {
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	now := time.Now().UTC()
	run := &upgrade.Run{
		ID: job.ID, Operation: job.Operation, From: job.From, To: job.Request.Version,
		Phase: upgrade.PhaseQueued, Started: now, Updated: now,
	}
	run.Log("accepted by the API")
	if target != nil {
		run.Log("rolling back to %s restores the datastore as it was when %s was installed; changes made since are lost", target.From, target.To)
	}
	previous := state.Current
	state.Current = run
	if err := upgrade.SaveState(l, state); err != nil {
		_ = os.Remove(jobFile)
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if err := s.opts.Lifecycle.Spawn(jobFile); err != nil {
		state.Current = previous
		_ = upgrade.SaveState(l, state)
		_ = os.Remove(jobFile)
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: "could not start the upgrade: " + err.Error()})
		return
	}

	record(r, func(rl *requestLog) {
		rl.changed = []string{string(job.Operation) + " " + job.From + " -> " + job.Request.Version}
	})
	w.Header().Set("Location", "/api/v1/status")
	writeJSON(w, http.StatusAccepted, upgrade.Accepted{
		ID: job.ID, Operation: job.Operation, From: job.From, To: job.Request.Version,
		StatusPath: "/api/v1/status",
	})
}

// handleStatus reports the running version, health, the Portainer agent image
// and the upgrade record. The upgrade part comes from disk, so a KubeSolo that
// has just been rolled back can say what happened to it.
func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	lc := s.opts.Lifecycle
	st := upgrade.Status{
		Version:       lc.Version,
		Commit:        lc.Commit,
		ContainerMode: lc.ContainerMode,
		Upgrade:       upgrade.ReadUpgradeStatus(s.layout(), lc.Version),
	}

	// Both checks are bounded well inside the server's write timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	st.Health = lc.Health(ctx)
	cancel()

	if cfg, err := s.read(); err == nil && cfg.Portainer.EdgeID != "" && cfg.Portainer.EdgeKey != "" {
		st.Agent = s.agentStatus(r.Context(), cfg)
	}

	writeJSON(w, http.StatusOK, st)
}

func (s *Service) agentStatus(ctx context.Context, cfg *types.Config) *upgrade.AgentStatus {
	a := &upgrade.AgentStatus{ConfiguredImage: cfg.Portainer.Image}
	if a.ConfiguredImage == "" {
		a.ConfiguredImage = types.DefaultPortainerEdgeImage
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	running, err := s.opts.Lifecycle.AgentImage(ctx)
	switch {
	case err != nil:
		a.Detail = "could not read the agent Deployment: " + err.Error()
	case running == "":
		a.Detail = "the agent Deployment does not exist yet"
	default:
		a.RunningImage = running
		a.InSync = running == a.ConfiguredImage
		if !a.InSync {
			a.Detail = "the configured image is applied when KubeSolo restarts; an image Portainer set on the Deployment itself is left alone"
		}
	}
	return a
}
