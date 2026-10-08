// Package executor carries out an upgrade or rollback. It runs as a detached
// kubesolo process (kubesolo --upgrade-executor=<job>), outside the KubeSolo
// service it stops and restarts, and holds the upgrade lock from start to
// finish.
//
// It needs SQLite and a Kubernetes client, so only the kubesolo binary links
// it. The parts kubesoloctl shares live in internal/upgrade.
package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/upgrade"
)

// lockWait is how long the executor waits for the lock. The API or kubesoloctl
// that started it may still hold it for a moment while recording the run.
const lockWait = 30 * time.Second

// Main runs the job in jobFile and returns the process exit code. It is the
// whole of `kubesolo --upgrade-executor`.
func Main(jobFile string) int {
	if !upgrade.UnderSystemdUnit() {
		upgrade.EscapeServiceCgroup()
	}

	job, err := upgrade.ReadJob(jobFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade executor:", err)
		return 2
	}
	defer func() { _ = os.Remove(jobFile) }()

	// A signal must not leave a half-replaced binary behind without a record:
	// cancel, let the current step fail, and finish as normal.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	e := &executor{job: job, l: job.Layout(), ctx: ctx, out: os.Stdout}
	if err := e.execute(); err != nil {
		fmt.Fprintln(os.Stderr, "upgrade executor:", err)
		return 1
	}
	return 0
}

type executor struct {
	job upgrade.Job
	l   upgrade.Layout
	ctx context.Context
	out io.Writer

	state *upgrade.State
	run   *upgrade.Run
	svc   *upgrade.Service
	info  *detect.SystemInfo

	// from is the version installed when the run began.
	from string
}

func (e *executor) execute() error {
	lock, err := upgrade.WaitLock(e.l, lockWait)
	if err != nil {
		return fmt.Errorf("take the upgrade lock: %w", err)
	}
	defer lock.Unlock()

	if e.state, err = upgrade.LoadState(e.l); err != nil {
		return err
	}
	e.claimRun()

	var result upgrade.Result
	switch e.job.Operation {
	case upgrade.OpUpgrade:
		result, err = e.upgrade()
	case upgrade.OpRollback:
		result, err = e.rollback()
	default:
		result, err = upgrade.ResultAborted, fmt.Errorf("unknown operation %q", e.job.Operation)
	}
	e.finish(result, err)
	return err
}

// claimRun takes over the run the caller recorded, or starts one. A different
// run still recorded as current had an executor that died; it is closed off.
func (e *executor) claimRun() {
	if cur := e.state.Current; cur != nil && cur.ID != e.job.ID {
		cur.Result = upgrade.ResultFailed
		cur.Error = "the executor stopped before the run finished"
		cur.Finished = time.Now().UTC()
		e.state.Last = cur
		e.state.Current = nil
	}
	if e.state.Current == nil {
		now := time.Now().UTC()
		e.state.Current = &upgrade.Run{
			ID: e.job.ID, Operation: e.job.Operation,
			From: e.job.From, To: e.job.Request.Version,
			Phase: upgrade.PhaseQueued, Started: now, Updated: now,
		}
	}
	e.run = e.state.Current
	e.run.PID = os.Getpid()
	e.save()
}

func (e *executor) phase(p upgrade.Phase) {
	e.run.Phase = p
	e.logf("%s", p)
}

func (e *executor) logf(format string, args ...any) {
	e.run.Log(format, args...)
	_, _ = fmt.Fprintf(e.out, format+"\n", args...)
	if f, err := os.OpenFile(e.l.RunLog(e.run.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		_, _ = fmt.Fprintf(f, format+"\n", args...)
		_ = f.Close()
	}
	e.save()
}

func (e *executor) save() {
	if err := upgrade.SaveState(e.l, e.state); err != nil {
		_, _ = fmt.Fprintf(e.out, "could not save the upgrade state: %v\n", err)
	}
}

func (e *executor) finish(result upgrade.Result, err error) {
	upgrade.SettleBackups(e.l, e.job.Operation, result)
	e.run.Result = result
	if err != nil {
		e.run.Error = err.Error()
	}
	e.run.Phase = upgrade.PhaseDone
	e.run.Finished = time.Now().UTC()
	e.logf("finished: %s", result)
	e.state.Last = e.run
	e.state.Current = nil
	e.save()
	_ = os.RemoveAll(e.l.StagingDir())

	// No run can be accepted while this one holds the lock, so any other job
	// file was left by an executor that died before it could remove its own.
	stale, _ := filepath.Glob(filepath.Join(e.l.Dir(), "job-*.json"))
	for _, f := range stale {
		_ = os.Remove(f)
	}
	// Run logs of earlier runs go too; this one stays for whoever is following
	// it, until the next run.
	logs, _ := filepath.Glob(filepath.Join(e.l.Dir(), "run-*.log"))
	for _, f := range logs {
		if f != e.l.RunLog(e.run.ID) {
			_ = os.Remove(f)
		}
	}
}

// detectHost works out the platform and how KubeSolo is run.
func (e *executor) detectHost() error {
	info, err := detect.Detect()
	if err != nil {
		return err
	}
	svc, err := upgrade.DetectService(e.l, info)
	if err != nil {
		return err
	}
	e.info, e.svc = info, svc
	e.logf("KubeSolo runs under %s on %s/%s", svc, info.Arch, libc(info))
	return nil
}

func libc(info *detect.SystemInfo) string {
	if info.LibC == "" {
		return "glibc"
	}
	return string(info.LibC)
}

// ── upgrade ──────────────────────────────────────────────────────────────────

func (e *executor) upgrade() (upgrade.Result, error) {
	req := e.job.Request
	e.phase(upgrade.PhasePreflight)

	if err := e.preflight(req); err != nil {
		return upgrade.ResultAborted, err
	}

	staged, err := upgrade.Stage(e.ctx, e.l, e.l.RunStagingDir(e.run.ID), req, e.info, e.logf)
	if err != nil {
		return upgrade.ResultAborted, err
	}

	if err := e.requireBackupSpace(); err != nil {
		return upgrade.ResultAborted, err
	}
	if err := upgrade.InstallGuard(e.l, e.svc); err != nil {
		return upgrade.ResultAborted, err
	}

	e.phase(upgrade.PhaseBackup)
	pending := e.l.BackupDir() + ".new"
	if err := e.backup(pending, req.Version); err != nil {
		_ = os.RemoveAll(pending)
		return upgrade.ResultAborted, err
	}

	e.phase(upgrade.PhaseDryRun)
	if err := e.dryRun(staged.Binary, filepath.Join(pending, filepath.Base(e.l.BackupDatastore()))); err != nil {
		_ = os.RemoveAll(pending)
		return upgrade.ResultAborted, err
	}

	// The new backup becomes the current one only now, when nothing is left
	// that could abort the upgrade. The one it replaces is kept until the
	// upgrade is settled: if it fails, that one is still the rollback target.
	if err := upgrade.CommitBackup(e.l, pending); err != nil {
		return upgrade.ResultAborted, err
	}

	// ── nothing above this line changed the host ──
	e.run.HostChanged = true
	e.phase(upgrade.PhaseStopping)
	if err := e.svc.Stop(e.ctx); err != nil {
		return e.restore(fmt.Errorf("stop KubeSolo: %w", err))
	}

	e.phase(upgrade.PhaseReplacing)
	if err := e.setPending(req.Version); err != nil {
		return e.restore(err)
	}
	if err := installBinary(staged.Binary, e.l.Binary); err != nil {
		return e.restore(fmt.Errorf("install the new binary: %w", err))
	}

	e.phase(upgrade.PhaseStarting)
	if err := e.svc.Start(); err != nil {
		return e.restore(fmt.Errorf("start KubeSolo %s: %w", req.Version, err))
	}

	e.phase(upgrade.PhaseVerifying)
	switch outcome := e.gate(req.Version, req.HealthTimeout(), true); outcome.kind {
	case gateHealthy:
		e.clearPending()
		e.logf("KubeSolo %s is healthy", req.Version)
		return upgrade.ResultSucceeded, nil
	case gateGuardRestored:
		// The boot guard has already put the previous version back. Confirm it
		// came up.
		e.run.Phase = upgrade.PhaseRollingBack
		e.logf("%s", outcome.reason)
		if back := e.gate(e.from, req.RestoreHealthTimeout(), false); back.kind != gateHealthy {
			return upgrade.ResultFailed, fmt.Errorf("%s, and %s did not become healthy: %s", outcome.reason, e.from, back.reason)
		}
		return upgrade.ResultRolledBack, errors.New(outcome.reason)
	default:
		return e.restore(fmt.Errorf("%s did not become healthy: %s", req.Version, outcome.reason))
	}
}

func (e *executor) preflight(req upgrade.Request) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := e.detectHost(); err != nil {
		return err
	}

	installed, err := upgrade.BinaryVersion(e.ctx, e.l.Binary)
	if err != nil {
		return fmt.Errorf("read the installed version: %w", err)
	}
	e.from = installed
	e.run.From = installed

	target, _ := upgrade.ParseVersion(req.Version)
	current, err := upgrade.ParseVersion(installed)
	switch {
	case err != nil && !req.Force:
		return fmt.Errorf("the installed version %q is not a release; pass force to replace it", installed)
	case err == nil && target.Compare(current) == 0 && !req.Force:
		return fmt.Errorf("%s is already installed", installed)
	case err == nil && target.Compare(current) < 0 && !req.Force:
		return fmt.Errorf("%s is older than the installed %s; a downgrade needs force, and rolling back to the version before an upgrade is what rollback is for", req.Version, installed)
	}
	return nil
}

// requireBackupSpace checks there is room for the backup — the datastore
// snapshot and the old binary — and for the dry-run's copy of the snapshot.
func (e *executor) requireBackupSpace() error {
	var db int64
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(e.l.Datastore() + suffix); err == nil {
			db += fi.Size()
		}
	}
	var bin int64
	if fi, err := os.Stat(e.l.Binary); err == nil {
		bin = fi.Size()
	}
	need := uint64(2*db+bin) + 64<<20
	return upgrade.RequireFree(e.l.Dir(), need)
}

// backup writes the datastore snapshot, the current binary and the
// configuration file to dir, with a manifest.
func (e *executor) backup(dir, to string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	db := filepath.Join(dir, filepath.Base(e.l.BackupDatastore()))
	if err := SnapshotDatastore(e.l.Datastore(), db); err != nil {
		return err
	}
	if err := QuickCheck(db); err != nil {
		return err
	}
	fi, err := os.Stat(db)
	if err != nil {
		return err
	}

	bin := filepath.Join(dir, filepath.Base(e.l.BackupBinary()))
	if err := CloneFile(e.l.Binary, bin, 0o755); err != nil {
		return fmt.Errorf("back up the binary: %w", err)
	}
	binSum, err := sha256File(bin)
	if err != nil {
		return err
	}

	// The service definition, so a rollback can put back the one the older
	// release runs under: kubesoloctl moves a flag-based install onto a
	// configuration file after an upgrade, which rewrites the definition to a
	// --config an older release does not understand.
	definition := ""
	if def := e.svc.DefinitionFile(); def != "" {
		if fi, err := os.Stat(def); err == nil {
			if err := CloneFile(def, filepath.Join(dir, filepath.Base(e.l.BackupServiceDefinition())), fi.Mode().Perm()); err != nil {
				return fmt.Errorf("back up the service definition: %w", err)
			}
			definition = def
		}
	}

	hasConfig := false
	if _, err := os.Stat(e.l.ConfigFile); err == nil {
		if err := CloneFile(e.l.ConfigFile, filepath.Join(dir, filepath.Base(e.l.BackupConfigFile())), 0o600); err != nil {
			return fmt.Errorf("back up the configuration file: %w", err)
		}
		hasConfig = true
	}

	m := upgrade.Manifest{
		From: e.from, To: to, Created: time.Now().UTC(), RunID: e.run.ID,
		DatastoreBytes: fi.Size(), BinarySHA256: binSum, HasConfigFile: hasConfig,
		ServiceDefinition: definition,
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := upgrade.WriteFileAtomic(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		return err
	}
	e.logf("backed up %s: datastore %d KiB, binary, configuration file: %t", e.from, fi.Size()>>10, hasConfig)
	return nil
}

const dryRunTimeout = 10 * time.Minute

// dryRun has the new binary open a copy of the snapshot through kine — which
// applies kine's migrations — and check the result.
func (e *executor) dryRun(newBinary, snapshot string) error {
	work := filepath.Join(e.l.RunStagingDir(e.run.ID), "dry-run.db")
	if err := CloneFile(snapshot, work, 0o600); err != nil {
		return err
	}
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(work + suffix)
		}
	}()

	ctx, cancel := context.WithTimeout(e.ctx, dryRunTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, newBinary, upgrade.CheckDatastoreFlag+"="+work).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		// A release older than the dry-run does not know the flag. That is a
		// downgrade, which already needed force; its datastore is protected by
		// the backup alone.
		if strings.Contains(text, "unknown long flag") {
			e.logf("%s has no datastore dry-run; relying on the backup", e.job.Request.Version)
			return nil
		}
		return fmt.Errorf("the datastore dry-run failed: %w: %s", err, lastLine(text))
	}
	e.logf("datastore dry-run passed: %s", lastLine(text))
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func (e *executor) setPending(version string) error {
	if err := upgrade.WriteFileAtomic(e.l.AttemptsFile(), []byte("0\n"), 0o600); err != nil {
		return err
	}
	return upgrade.WriteFileAtomic(e.l.PendingFile(), []byte(version+"\n"), 0o600)
}

func (e *executor) clearPending() {
	_ = os.Remove(e.l.PendingFile())
	_ = os.Remove(e.l.AttemptsFile())
}

// installBinary puts src in place at dst atomically: a copy beside dst, then a
// rename over it. The running process, if any, keeps the old inode.
func installBinary(src, dst string) error {
	tmp := dst + ".upgrade"
	if err := CloneFile(src, tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	restoreSELinuxContext(dst)
	return nil
}

func restoreSELinuxContext(path string) {
	for _, rc := range []string{"/usr/sbin/restorecon", "/sbin/restorecon"} {
		if _, err := os.Stat(rc); err == nil {
			_ = exec.Command(rc, path).Run()
			return
		}
	}
}

// ── rollback ─────────────────────────────────────────────────────────────────

func (e *executor) rollback() (upgrade.Result, error) {
	e.phase(upgrade.PhasePreflight)
	if err := upgrade.ValidateHealthTimeout(e.job.Request.HealthTimeoutSeconds); err != nil {
		return upgrade.ResultAborted, err
	}
	if err := e.detectHost(); err != nil {
		return upgrade.ResultAborted, err
	}
	installed, err := upgrade.BinaryVersion(e.ctx, e.l.Binary)
	if err != nil {
		return upgrade.ResultAborted, fmt.Errorf("read the installed version: %w", err)
	}
	e.from = installed
	e.run.From = installed

	m, err := upgrade.RollbackTarget(e.l, installed)
	if err != nil {
		return upgrade.ResultAborted, err
	}
	e.run.To = m.From
	if err := e.verifyBackupBinary(m); err != nil {
		return upgrade.ResultAborted, err
	}

	e.run.HostChanged = true
	e.phase(upgrade.PhaseStopping)
	if err := e.svc.Stop(e.ctx); err != nil {
		return upgrade.ResultFailed, fmt.Errorf("stop KubeSolo: %w", err)
	}
	e.phase(upgrade.PhaseRollingBack)
	if err := e.restoreFiles(m); err != nil {
		return upgrade.ResultFailed, err
	}
	e.phase(upgrade.PhaseStarting)
	if err := e.svc.Start(); err != nil {
		return upgrade.ResultFailed, fmt.Errorf("start KubeSolo %s: %w", m.From, err)
	}
	e.phase(upgrade.PhaseVerifying)
	if outcome := e.gate(m.From, e.job.Request.HealthTimeout(), false); outcome.kind != gateHealthy {
		return upgrade.ResultFailed, fmt.Errorf("%s did not become healthy after the rollback: %s", m.From, outcome.reason)
	}
	// The backup has been used: what it holds is what is now running.
	_ = os.RemoveAll(e.l.BackupDir())
	e.logf("rolled back to %s; changes to the cluster made since the upgrade to %s are gone", m.From, m.To)
	return upgrade.ResultSucceeded, nil
}

func (e *executor) verifyBackupBinary(m *upgrade.Manifest) error {
	sum, err := sha256File(e.l.BackupBinary())
	if err != nil {
		return err
	}
	if sum != m.BinarySHA256 {
		return fmt.Errorf("the backed-up binary does not match its manifest (sha256 %s, expected %s)", sum, m.BinarySHA256)
	}
	return QuickCheck(e.l.BackupDatastore())
}

// restore puts the backup back after a failed upgrade and starts the previous
// version.
func (e *executor) restore(cause error) (upgrade.Result, error) {
	e.run.Phase = upgrade.PhaseRollingBack
	e.logf("rolling back: %v", cause)

	m, err := upgrade.LoadManifest(e.l)
	if err != nil || m == nil {
		return upgrade.ResultFailed, fmt.Errorf("%w; and the backup cannot be read: %v", cause, err)
	}
	if err := e.svc.Stop(e.ctx); err != nil {
		return upgrade.ResultFailed, fmt.Errorf("%w; and stopping it to roll back failed: %v", cause, err)
	}
	if err := e.restoreFiles(m); err != nil {
		return upgrade.ResultFailed, fmt.Errorf("%w; and restoring the backup failed: %v", cause, err)
	}
	if err := e.svc.Start(); err != nil {
		return upgrade.ResultFailed, fmt.Errorf("%w; and starting %s again failed: %v", cause, m.From, err)
	}
	if back := e.gate(m.From, e.job.Request.RestoreHealthTimeout(), false); back.kind != gateHealthy {
		return upgrade.ResultFailed, fmt.Errorf("%w; %s was restored but did not become healthy: %s", cause, m.From, back.reason)
	}
	e.logf("%s restored and healthy", m.From)
	return upgrade.ResultRolledBack, cause
}

// restoreFiles stops whatever the failed version left running and puts the
// backed-up binary, datastore and configuration file in place. KubeSolo must
// be stopped.
func (e *executor) restoreFiles(m *upgrade.Manifest) error {
	// The datastore is going back in time, and the pods running now belong to
	// the version being abandoned. Starting the restored version beside them
	// would duplicate them, so they go — and with them any chance of an old
	// release that clears containerd state at start stranding them.
	process.StopWorkloads()

	if err := installBinary(e.l.BackupBinary(), e.l.Binary); err != nil {
		return fmt.Errorf("restore the binary: %w", err)
	}

	db := e.l.Datastore()
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(db + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", db+suffix, err)
		}
	}
	tmp := db + ".restore"
	if err := CloneFile(e.l.BackupDatastore(), tmp, 0o644); err != nil {
		return fmt.Errorf("restore the datastore: %w", err)
	}
	if err := os.Rename(tmp, db); err != nil {
		return fmt.Errorf("restore the datastore: %w", err)
	}

	if m.HasConfigFile {
		tmp := e.l.ConfigFile + ".restore"
		if err := CloneFile(e.l.BackupConfigFile(), tmp, 0o600); err != nil {
			return fmt.Errorf("restore the configuration file: %w", err)
		}
		if err := os.Rename(tmp, e.l.ConfigFile); err != nil {
			return fmt.Errorf("restore the configuration file: %w", err)
		}
	} else if _, err := os.Stat(e.l.ConfigFile); err == nil {
		// The restored release ran without one; it was created since, by the
		// move off flags. Set aside rather than deleted.
		if err := os.Rename(e.l.ConfigFile, e.l.ConfigFile+".rolled-back"); err != nil {
			return fmt.Errorf("set aside the configuration file: %w", err)
		}
		e.logf("%s was created after the backup; moved to %s", e.l.ConfigFile, e.l.ConfigFile+".rolled-back")
	}

	if m.ServiceDefinition != "" {
		fi, err := os.Stat(e.l.BackupServiceDefinition())
		if err != nil {
			return fmt.Errorf("restore the service definition: %w", err)
		}
		tmp := m.ServiceDefinition + ".restore"
		if err := CloneFile(e.l.BackupServiceDefinition(), tmp, fi.Mode().Perm()); err != nil {
			return fmt.Errorf("restore the service definition: %w", err)
		}
		if err := os.Rename(tmp, m.ServiceDefinition); err != nil {
			return fmt.Errorf("restore the service definition: %w", err)
		}
		if err := e.svc.ReloadDefinitions(); err != nil {
			return err
		}
	}
	e.clearPending()
	e.logf("restored %s, its datastore and configuration", m.From)
	return nil
}

// ── health gate ──────────────────────────────────────────────────────────────

type gateKind int

const (
	gateHealthy gateKind = iota
	gateUnhealthy
	gateGuardRestored
)

type gateOutcome struct {
	kind   gateKind
	reason string
}

const (
	gateInterval = 5 * time.Second

	// gateStable is how long KubeSolo must run as one process, healthy, before
	// it passes. A crash loop can look healthy between crashes: the node object
	// and pod statuses in the datastore outlive the process that wrote them.
	gateStable = 30 * time.Second

	// daemonExitGrace is how long a daemon-mode KubeSolo may be missing before
	// the gate fails. Nothing restarts it.
	daemonExitGrace = 15 * time.Second
)

// gate waits up to timeout for KubeSolo running version want to be healthy.
// watchGuard is set while the new binary is pending, when the boot guard may
// restore the previous one on its own.
func (e *executor) gate(want string, timeout time.Duration, watchGuard bool) gateOutcome {
	deadline := time.Now().Add(timeout)
	guardEvents := guardLogLines(e.l)
	var reason string

	var pid int
	var since, missingSince time.Time
	for {
		if watchGuard {
			if _, err := os.Stat(e.l.PendingFile()); errors.Is(err, os.ErrNotExist) {
				if lines := guardLogLines(e.l); len(lines) > len(guardEvents) {
					return gateOutcome{gateGuardRestored, "the boot guard restored the previous version: " + lines[len(lines)-1]}
				}
			}
		}

		pids := upgrade.MainPIDs(e.l.Binary)
		switch {
		case len(pids) == 0:
			pid = 0
			if missingSince.IsZero() {
				missingSince = time.Now()
			}
			reason = "KubeSolo is not running"
			if e.svc.Daemon && time.Since(missingSince) > daemonExitGrace {
				return gateOutcome{gateUnhealthy, "KubeSolo exited"}
			}
		case pids[0] != pid:
			pid, since, missingSince = pids[0], time.Now(), time.Time{}
			reason = "KubeSolo started"
		default:
			h := CheckHealth(e.ctx, e.l.AdminKubeconfig(), want)
			switch {
			case !h.Healthy:
				since = time.Now()
				reason = Summary(h)
			case time.Since(since) >= gateStable:
				return gateOutcome{gateHealthy, "healthy"}
			default:
				reason = "healthy, confirming it stays up"
			}
		}

		if time.Now().After(deadline) {
			return gateOutcome{gateUnhealthy, fmt.Sprintf("not healthy after %s: %s", timeout, reason)}
		}
		select {
		case <-e.ctx.Done():
			return gateOutcome{gateUnhealthy, "interrupted: " + e.ctx.Err().Error()}
		case <-time.After(gateInterval):
		}
	}
}

func guardLogLines(l upgrade.Layout) []string {
	raw, err := os.ReadFile(l.GuardLog())
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
