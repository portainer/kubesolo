package assistant

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/rs/zerolog/log"
)

// KindWorkload is an incident in a workload running on KubeSolo.
const KindWorkload = "workload"

// DefaultMaxWorkloadIncidentsPerHour bounds workload incidents, so that a
// failure across the cluster does not flood the store and the model.
const DefaultMaxWorkloadIncidentsPerHour = 6

// maxLogLines is how much of a failing container's log a snapshot carries.
const maxLogLines = 50

// FailingPod is a pod in a failing state, and the workload it belongs to.
type FailingPod struct {
	// Workload names it as namespace/kind/name, e.g. "shop/deployment/api";
	// a pod no controller owns is namespace/pod/name.
	Workload string
	Pod      string
	Reason   string
}

// WorkloadState is what a snapshot carries of a failing workload.
type WorkloadState struct {
	Name       string           `json:"name"`
	Pod        string           `json:"pod"`
	Containers []ContainerState `json:"containers"`

	// Logs are the failing container's last lines; for a crash loop, those
	// of the run that crashed.
	Logs []string `json:"logs,omitempty"`
}

// ContainerState is one container of a failing pod.
type ContainerState struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	State    string `json:"state"`
	Restarts int32  `json:"restarts"`

	// LastTermination is how its previous run ended, e.g. "exit 1: Error".
	LastTermination string `json:"lastTermination,omitempty"`
}

// WorkloadWatcher records an incident when a workload keeps failing. It is
// diagnose-and-report only: nothing acts on a workload's incident.
type WorkloadWatcher struct {
	// Failing lists the pods in a failing state now.
	Failing func(context.Context) ([]FailingPod, error)

	// Describe reads a failing pod: its state and logs, and its warning
	// events.
	Describe func(context.Context, FailingPod) (*WorkloadState, []string, error)

	Store      *Store
	DataDir    string
	Interval   time.Duration
	Threshold  time.Duration
	MaxPerHour int

	now func() time.Time

	// failingSince is when each failing workload was first seen failing in
	// its current outage, and recorded whether that outage has an incident.
	failingSince map[string]time.Time
	recorded     map[string]bool
	// recent are the times of the latest incidents, for MaxPerHour.
	recent []time.Time
}

// Run checks the workloads every Interval until ctx is done.
func (w *WorkloadWatcher) Run(ctx context.Context) {
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

func (w *WorkloadWatcher) check(ctx context.Context) {
	if w.now == nil {
		w.now = time.Now
	}
	if w.failingSince == nil {
		w.failingSince, w.recorded = map[string]time.Time{}, map[string]bool{}
	}
	pods, err := w.Failing(ctx)
	if err != nil {
		return // the API server is down: the component watcher sees to that
	}
	now := w.now()

	// One pod per workload stands for it: the first, in a stable order.
	byWorkload := map[string]FailingPod{}
	for _, p := range pods {
		if _, ok := byWorkload[p.Workload]; !ok {
			byWorkload[p.Workload] = p
		}
	}
	for name := range w.failingSince {
		if _, ok := byWorkload[name]; !ok {
			delete(w.failingSince, name) // recovered: the outage is over
			delete(w.recorded, name)
		}
	}

	w.recent = slices.DeleteFunc(w.recent, func(t time.Time) bool { return now.Sub(t) >= time.Hour })
	for _, name := range slices.Sorted(maps.Keys(byWorkload)) {
		since, seen := w.failingSince[name]
		switch {
		case !seen:
			w.failingSince[name] = now
			continue
		case w.recorded[name] || now.Sub(since) < w.Threshold:
			continue
		case len(w.recent) >= w.MaxPerHour:
			continue // waits for room; recorded then if it still fails
		}
		// Incidents of one pass take their IDs from distinct times, a
		// nanosecond apart, so that they neither collide nor reorder.
		at := now.Add(time.Duration(len(w.recent)))
		if err := w.record(ctx, at, byWorkload[name], now.Sub(since)); err != nil {
			log.Error().Str("component", "assistant").Str("workload", name).Err(err).Msg("cannot record the workload incident")
			continue
		}
		w.recorded[name] = true
		w.recent = append(w.recent, now)
	}
}

func (w *WorkloadWatcher) record(ctx context.Context, now time.Time, p FailingPod, failingFor time.Duration) error {
	state, events, err := w.Describe(ctx, p)
	if err != nil {
		return err
	}
	snap := Snapshot{Workload: state, Events: since(events, now.Add(-MaxEvidenceAge)), Host: hostInfo(w.DataDir)}
	fit(&snap)
	inc := Incident{ID: newIncidentID(now), Time: now.UTC(), Kind: KindWorkload,
		Trigger: fmt.Sprintf("%s failing for %s: %s", p.Workload, failingFor.Round(time.Second), p.Reason), Snapshot: snap}
	if err := w.Store.Save(inc); err != nil {
		return err
	}
	log.Warn().Str("component", "assistant").Str("incident", inc.ID).Msg(inc.Trigger)
	return nil
}
