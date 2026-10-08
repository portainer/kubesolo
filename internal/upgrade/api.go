package upgrade

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Request asks for an upgrade. It is the body of POST /api/v1/upgrade, and what
// kubesoloctl hands the executor when the API is not available.
type Request struct {
	// Version is the release to install, e.g. "v1.2.2".
	Version string `json:"version"`

	// Source is an absolute path to a release archive (.tar.gz) or a kubesolo
	// binary already on this host, for air-gapped upgrades. Empty means
	// download the release for this host's architecture.
	Source string `json:"source,omitempty"`

	// SHA256 is the expected checksum of the archive or binary. Optional: when
	// empty it is taken from SHA256SUMS published with the release, or placed
	// next to Source.
	SHA256 string `json:"sha256,omitempty"`

	// Force allows installing a version that is not newer than the running one.
	Force bool `json:"force,omitempty"`

	// HealthTimeoutSeconds bounds how long the new version has to become
	// healthy before it is rolled back. Zero means DefaultHealthTimeout.
	HealthTimeoutSeconds int `json:"healthTimeoutSeconds,omitempty"`
}

// DefaultHealthTimeout is generous because the slowest supported hosts take
// minutes to start the control plane and import images.
const DefaultHealthTimeout = 10 * time.Minute

// MinHealthTimeout is the shortest health-gate timeout a request may ask for.
// The gate passes only once KubeSolo has run healthy for 30s as one process,
// after it has started, so a shorter timeout can never be met.
const MinHealthTimeout = time.Minute

// maxHealthTimeoutSeconds is the largest healthTimeoutSeconds that converts to a
// time.Duration without overflowing.
const maxHealthTimeoutSeconds = math.MaxInt64 / int64(time.Second)

// ValidateHealthTimeout checks a healthTimeoutSeconds value: 0 for the default,
// or from MinHealthTimeout up to the longest duration that can be represented.
// Upgrades and rollbacks both carry one. The seconds are compared as they are:
// converting first could overflow, and a wrapped value can land on a plausible
// timeout.
func ValidateHealthTimeout(seconds int) error {
	switch {
	case seconds < 0:
		return errors.New("healthTimeoutSeconds cannot be negative")
	case seconds == 0:
		return nil
	case seconds < int(MinHealthTimeout/time.Second):
		return fmt.Errorf("healthTimeoutSeconds must be at least %d: KubeSolo has to stay up and healthy for 30s after it starts", int(MinHealthTimeout/time.Second))
	case int64(seconds) > maxHealthTimeoutSeconds:
		return fmt.Errorf("healthTimeoutSeconds %d is too large", seconds)
	}
	return nil
}

// HealthTimeout returns the effective health-gate timeout.
func (r Request) HealthTimeout() time.Duration {
	if r.HealthTimeoutSeconds > 0 {
		return time.Duration(r.HealthTimeoutSeconds) * time.Second
	}
	return DefaultHealthTimeout
}

// RestoreHealthTimeout returns how long the version restored after a failed
// upgrade has to become healthy: the request's timeout, but never less than
// DefaultHealthTimeout. That version was running before the upgrade, and a
// short timeout chosen for the new one should not decide whether the rollback
// counts as one.
func (r Request) RestoreHealthTimeout() time.Duration {
	return max(r.HealthTimeout(), DefaultHealthTimeout)
}

// Validate checks the request on its own, without looking at the host.
func (r Request) Validate() error {
	if r.Version == "" {
		return errors.New("version is required")
	}
	if _, err := ParseVersion(r.Version); err != nil {
		return err
	}
	if r.Source != "" && !filepath.IsAbs(r.Source) {
		return fmt.Errorf("source %q must be an absolute path on this host", r.Source)
	}
	if r.SHA256 != "" {
		if b, err := hex.DecodeString(r.SHA256); err != nil || len(b) != 32 {
			return fmt.Errorf("sha256 %q is not a hex-encoded SHA-256 checksum", r.SHA256)
		}
	}
	return ValidateHealthTimeout(r.HealthTimeoutSeconds)
}

// Accepted is the body of a 202 from POST /api/v1/upgrade or /rollback.
type Accepted struct {
	ID        string    `json:"id"`
	Operation Operation `json:"operation"`
	From      string    `json:"from"`
	To        string    `json:"to"`

	// StatusPath is where to follow the run.
	StatusPath string `json:"statusPath"`
}

// Manifest describes the retained backup.
type Manifest struct {
	// From is the version the backup holds: the rollback target.
	From string `json:"from"`

	// To is the version that was installed over it. A rollback is only valid
	// while To is the version running: the datastore snapshot belongs to the
	// moment From was replaced by To, and to nothing later.
	To string `json:"to"`

	Created time.Time `json:"created"`

	// RunID is the upgrade that took the backup.
	RunID string `json:"runId"`

	DatastoreBytes int64  `json:"datastoreBytes"`
	BinarySHA256   string `json:"binarySha256"`
	HasConfigFile  bool   `json:"hasConfigFile"`

	// ServiceDefinition is where the backed-up service definition goes back
	// to; empty when there was none to back up (daemon mode).
	ServiceDefinition string `json:"serviceDefinition,omitempty"`
}

// LoadManifest reads the backup manifest. It returns nil, nil when there is no
// backup.
func LoadManifest(l Layout) (*Manifest, error) {
	raw, err := os.ReadFile(l.BackupManifest())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("backup manifest %s is unreadable: %w", l.BackupManifest(), err)
	}
	return &m, nil
}

// RollbackTarget returns the backup a rollback would restore, or why there is
// none. running is the version currently running.
func RollbackTarget(l Layout, running string) (*Manifest, error) {
	m, err := LoadManifest(l)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("there is no backup to roll back to")
	}
	if m.To != running {
		return nil, fmt.Errorf("the backup of %s was taken when upgrading to %s, but %s is running; restoring it now would lose everything since", m.From, m.To, running)
	}
	for _, p := range []string{l.BackupBinary(), l.BackupDatastore()} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("the backup is incomplete: %w", err)
		}
	}
	return m, nil
}

// UpgradeStatus is the upgrade part of GET /api/v1/status. Everything in it is
// read from disk.
type UpgradeStatus struct {
	InFlight bool `json:"inFlight"`
	Current  *Run `json:"current,omitempty"`
	Last     *Run `json:"last,omitempty"`

	// Interrupted is a run whose executor stopped before it finished — the
	// host lost power or rebooted, or the executor was killed. KubeSolo closes
	// it once it is healthy again; until then it is reported here.
	Interrupted *Run `json:"interrupted,omitempty"`

	// PendingVerify is the version that was started but has not yet been proven
	// healthy, and BootAttempts how many times it has been started.
	PendingVerify string `json:"pendingVerify,omitempty"`
	BootAttempts  int    `json:"bootAttempts,omitempty"`

	// RollbackTarget is the version a rollback would restore, if one is valid.
	RollbackTarget *Manifest `json:"rollbackTarget,omitempty"`

	// RollbackUnavailable says why there is no rollback target.
	RollbackUnavailable string `json:"rollbackUnavailable,omitempty"`

	// GuardEvents are the boot guard's records of restoring a previous binary.
	GuardEvents []string `json:"guardEvents,omitempty"`
}

// ReadUpgradeStatus assembles the upgrade status from disk. running is the
// version currently running.
func ReadUpgradeStatus(l Layout, running string) UpgradeStatus {
	var st UpgradeStatus

	if s, err := LoadState(l); err == nil {
		st.InFlight = s.InFlight()
		switch {
		case st.InFlight:
			st.Current = s.Current
		case s.Current != nil:
			st.Interrupted = s.Current
		}
		st.Last = s.Last
	}

	if raw, err := os.ReadFile(l.PendingFile()); err == nil {
		st.PendingVerify = strings.TrimSpace(string(raw))
		if a, err := os.ReadFile(l.AttemptsFile()); err == nil {
			st.BootAttempts, _ = strconv.Atoi(strings.TrimSpace(string(a)))
		}
	}

	if m, err := RollbackTarget(l, running); err == nil {
		st.RollbackTarget = m
	} else {
		st.RollbackUnavailable = err.Error()
	}

	if raw, err := os.ReadFile(l.GuardLog()); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				st.GuardEvents = append(st.GuardEvents, line)
			}
		}
	}
	return st
}

// Status is the body of GET /api/v1/status.
type Status struct {
	// Version is the running KubeSolo.
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`

	ContainerMode bool `json:"containerMode"`

	Health Health `json:"health"`

	// Agent compares the Portainer agent image the configuration asks for with
	// the one the cluster runs. Nil when no agent is configured.
	Agent *AgentStatus `json:"agent,omitempty"`

	Upgrade UpgradeStatus `json:"upgrade"`
}

// Health is KubeSolo's own view of whether it is working.
type Health struct {
	Healthy bool `json:"healthy"`

	// Checks maps each check to "ok" or the reason it failed.
	Checks map[string]string `json:"checks"`
}

// AgentStatus is the Portainer agent image, desired and actual.
type AgentStatus struct {
	ConfiguredImage string `json:"configuredImage"`
	RunningImage    string `json:"runningImage,omitempty"`
	InSync          bool   `json:"inSync"`
	Detail          string `json:"detail,omitempty"`
}

// CommitBackup makes the backup in staged the current one, keeping the one it
// replaces as the previous backup until the upgrade is settled.
func CommitBackup(l Layout, staged string) error {
	if err := os.RemoveAll(l.PreviousBackupDir()); err != nil {
		return err
	}
	moved := false
	if _, err := os.Stat(l.BackupDir()); err == nil {
		if err := os.Rename(l.BackupDir(), l.PreviousBackupDir()); err != nil {
			return err
		}
		moved = true
	}
	if err := os.Rename(staged, l.BackupDir()); err != nil {
		// The upgrade aborts on this error, and an abort settles nothing, so
		// the previous backup has to be put back here or it stops being the
		// rollback target.
		if moved {
			if rerr := os.Rename(l.PreviousBackupDir(), l.BackupDir()); rerr != nil {
				return fmt.Errorf("%w; and restoring the previous backup from %s failed: %v", err, l.PreviousBackupDir(), rerr)
			}
		}
		return err
	}
	return nil
}

// SettleBackups decides which backup to keep once an upgrade has ended.
//
// The backup an upgrade takes is of the version it replaces. Once the upgrade
// has succeeded it is the rollback target, and the one before it can go. If
// the upgrade was rolled back, the version it was taken of is the one running
// again, so it is useless as a target, and the previous backup — of the version
// before that — is the one worth keeping. A failed upgrade keeps both: someone
// has to look, and the backups are what they will need.
func SettleBackups(l Layout, op Operation, result Result) {
	if op != OpUpgrade {
		return
	}
	switch result {
	case ResultSucceeded:
		_ = os.RemoveAll(l.PreviousBackupDir())
	case ResultRolledBack:
		if _, err := os.Stat(l.PreviousBackupDir()); err != nil {
			// The first upgrade has no earlier backup to go back to, and the
			// one it took is of the version now running: no target is better
			// than a wrong one.
			_ = os.RemoveAll(l.BackupDir())
			return
		}
		_ = os.RemoveAll(l.BackupDir())
		_ = os.Rename(l.PreviousBackupDir(), l.BackupDir())
	}
}
