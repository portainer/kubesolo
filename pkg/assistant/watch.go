package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// MaxEvidenceAge is how far back an outage's snapshot looks, for errors and
// Kubernetes events: older ones belong to an earlier failure.
const MaxEvidenceAge = 30 * time.Minute

const (
	// maxSnapshotBytes is the snapshot's share of the model's prompt: about
	// 3,000 of the default 8,192 tokens.
	maxSnapshotBytes = 12 << 10

	// DefaultWatchInterval and DefaultUnhealthyThreshold: a component must fail
	// every check for the threshold before it is an incident, so restarts and
	// blips are not.
	DefaultWatchInterval      = 30 * time.Second
	DefaultUnhealthyThreshold = 5 * time.Minute
)

// Collector takes snapshots.
type Collector struct {
	// Probes checks each control plane component; nil means healthy.
	Probes map[string]func(context.Context) error

	// Errors returns KubeSolo's recent error lines.
	Errors func() []string

	// Events returns recent Kubernetes warning events. It may be nil, and may
	// fail when the API server is down.
	Events func(context.Context) ([]string, error)

	// Node returns the node's Ready condition. It may be nil, and may fail
	// when the API server is down.
	Node func(context.Context) (string, error)

	// ProcNet is where the listening ports are read; empty means /proc/net.
	ProcNet string

	// DataDir is where KubeSolo keeps its state, for the disk usage.
	DataDir string

	// PodLogsDir is where the kubelet keeps pod logs, and ModelFile returns
	// the assistant's model, if any: both are told apart in a full disk's
	// usage. Either may be empty.
	PodLogsDir string
	ModelFile  func() string
}

// diskUsageTimeout bounds walking a full disk for its usage.
const diskUsageTimeout = 15 * time.Second

// Collect takes a snapshot, trimmed to fit its share of the prompt.
func (c *Collector) Collect(ctx context.Context) Snapshot {
	s := Snapshot{Components: c.probe(ctx), Host: hostInfo(c.DataDir)}
	if s.Host.DiskUsedPercent >= DiskFullPercent {
		l := diskLayout{dataDir: c.DataDir, podLogsDir: c.PodLogsDir}
		if c.ModelFile != nil {
			l.modelFile = c.ModelFile()
		}
		dctx, cancel := context.WithTimeout(ctx, diskUsageTimeout)
		s.Host.DiskUsageMiB = diskUsage(dctx, l)
		cancel()
	}
	if c.Errors != nil {
		s.RecentErrors = c.Errors()
	}
	if c.Events != nil {
		if events, err := c.Events(ctx); err == nil {
			s.Events = events
		}
	}
	s.Listening = listening(s.Components, listeningPorts(cmp.Or(c.ProcNet, "/proc/net")))
	if c.Node != nil {
		nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if n, err := c.Node(nctx); err == nil {
			s.Node = n
		}
		cancel()
	}
	fit(&s)
	return s
}

// fit trims a snapshot to its share of the prompt: each line to
// maxLineLength, then the oldest lines — errors, then logs, then events —
// until it fits.
func fit(s *Snapshot) {
	s.RecentErrors = truncateAll(s.RecentErrors)
	s.Events = truncateAll(s.Events)
	if s.Workload != nil {
		s.Workload.Logs = truncateAll(s.Workload.Logs[max(0, len(s.Workload.Logs)-maxLogLines):])
	}
	for size(*s) > maxSnapshotBytes {
		switch {
		case len(s.RecentErrors) > 0:
			s.RecentErrors = s.RecentErrors[1:]
		case s.Workload != nil && len(s.Workload.Logs) > 0:
			s.Workload.Logs = s.Workload.Logs[1:]
		case len(s.Events) > 0:
			s.Events = s.Events[1:]
		default:
			return
		}
	}
}

func (c *Collector) probe(ctx context.Context) map[string]string {
	out := map[string]string{}
	for name, probe := range c.Probes {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := probe(pctx); err != nil {
			out[name] = truncate(err.Error())
		} else {
			out[name] = "ok"
		}
		cancel()
	}
	return out
}

func size(s Snapshot) int {
	raw, _ := json.Marshal(s)
	return len(raw)
}

func truncate(line string) string {
	if len(line) > maxLineLength {
		return line[:maxLineLength]
	}
	return line
}

func truncateAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = truncate(l)
	}
	return out
}

// Watcher records an incident when control plane components stay unhealthy.
type Watcher struct {
	Collector *Collector
	Store     *Store
	Interval  time.Duration
	Threshold time.Duration

	now func() time.Time

	// failingSince is when the current outage began; zero when all is well.
	failingSince time.Time
	recorded     bool
}

// Run checks the components every Interval until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	if w.now == nil {
		w.now = time.Now
	}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check(ctx)
		}
	}
}

// check runs one round. An outage lasts from the first failed check until a
// round in which every component is healthy, and is recorded once, however
// many components it takes down.
func (w *Watcher) check(ctx context.Context) {
	components := w.Collector.probe(ctx)
	var failing []string
	for name, state := range components {
		if state != "ok" {
			failing = append(failing, name)
		}
	}
	slices.Sort(failing)

	now := w.now()
	switch {
	case len(failing) == 0:
		w.failingSince, w.recorded = time.Time{}, false
		return
	case w.failingSince.IsZero():
		w.failingSince = now
		return
	case w.recorded || now.Sub(w.failingSince) < w.Threshold:
		return
	}

	trigger := fmt.Sprintf("unhealthy for %s: %s", now.Sub(w.failingSince).Round(time.Second), strings.Join(failing, ", "))
	if err := w.record(ctx, now, trigger); err != nil {
		log.Error().Str("component", "assistant").Err(err).Msg("cannot record the incident")
		return
	}
	w.recorded = true
}

func (w *Watcher) record(ctx context.Context, now time.Time, trigger string) error {
	inc := Incident{ID: newIncidentID(now), Time: now.UTC(), Kind: KindKubeSolo, Trigger: trigger, Snapshot: w.Collector.Collect(ctx)}
	inc.Snapshot.RecentErrors = since(inc.Snapshot.RecentErrors, now.Add(-MaxEvidenceAge))
	if err := w.Store.Save(inc); err != nil {
		return err
	}
	log.Warn().Str("component", "assistant").Str("incident", inc.ID).Msg(trigger)
	return nil
}

// CrashLoopWindow is how long after a crash another one counts as the same
// crash loop, and is not recorded again.
const CrashLoopWindow = 15 * time.Minute

// RecordUncleanShutdown records that the previous run ended without shutting
// down: a crash, a fatal error, a kill or a power loss. The snapshot carries
// the errors that run logged last; its components are still starting, so
// they are left out.
//
// A crash within CrashLoopWindow of the previous one, recorded or not, is part
// of the same crash loop and is only logged: a loop restarting every 30
// seconds is one incident, and the first crash carries the telling errors.
// lastCrashFile keeps the time of the previous crash.
func RecordUncleanShutdown(store *Store, previousErrors []string, dataDir, lastCrashFile string, now time.Time) error {
	var last time.Time
	if raw, err := os.ReadFile(lastCrashFile); err == nil {
		last, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	}
	if err := os.WriteFile(lastCrashFile, []byte(now.UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		return err
	}
	if !last.IsZero() && now.Sub(last) < CrashLoopWindow {
		log.Warn().Str("component", "assistant").Msg("KubeSolo did not shut down cleanly again, within its crash loop; not recording another incident")
		return nil
	}

	c := &Collector{Errors: func() []string { return previousErrors }, DataDir: dataDir}
	inc := Incident{ID: newIncidentID(now), Time: now.UTC(), Kind: KindKubeSolo,
		Trigger: "KubeSolo did not shut down cleanly (crash, fatal error, kill or power loss)", Snapshot: c.Collect(context.Background())}
	if err := store.Save(inc); err != nil {
		return err
	}
	log.Warn().Str("component", "assistant").Str("incident", inc.ID).Msg(inc.Trigger)
	return nil
}

// MarkRunning records that KubeSolo is running, and reports whether the
// previous run never called MarkStopped.
func MarkRunning(path string) (bool, error) {
	_, err := os.Stat(path)
	unclean := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return unclean, os.WriteFile(path, nil, 0o600)
}

// MarkStopped records a clean shutdown.
func MarkStopped(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// since keeps the lines logged at or after t, and those whose time cannot be
// read. Each line starts with its time, as formatEvent writes it.
func since(lines []string, t time.Time) []string {
	var out []string
	for _, l := range lines {
		stamp, _, _ := strings.Cut(l, " ")
		if at, err := time.Parse(time.RFC3339, stamp); err == nil && at.Before(t) {
			continue
		}
		out = append(out, l)
	}
	return out
}
