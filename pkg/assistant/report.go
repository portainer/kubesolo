package assistant

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// reportIncidents is how many of the latest incidents a report shows.
const reportIncidents = 10

// Where an incident's suggested action stands.
const (
	StateAwaitingApproval = "awaiting approval"
	StateApproved         = "approved"
	StateRejected         = "rejected"
	StateNotOffered       = "not offered"
	StateReportOnly       = "report only"
	StateTriageFailed     = "triage failed"
	StateTriageSkipped    = "triage skipped"
)

// Report is the node's health now and its latest incidents.
type Report struct {
	Time       time.Time         `json:"time"`
	Components map[string]string `json:"components"`
	Host       HostInfo          `json:"host"`

	// Incidents are the latest triaged incidents, newest first.
	Incidents []ReportIncident `json:"incidents"`

	// TotalIncidents and AwaitingApproval count every incident kept, not
	// only those shown.
	TotalIncidents   int `json:"totalIncidents"`
	AwaitingApproval int `json:"awaitingApproval"`
}

// ReportIncident is an incident with where its action stands.
type ReportIncident struct {
	Summary
	State string `json:"state"`
}

// Reporter writes reports.
type Reporter struct {
	// Collector probes the components and reads the host.
	Collector *Collector
	Store     *Store

	now func() time.Time
}

// Report checks the node's health and gathers the latest incidents.
func (r *Reporter) Report(ctx context.Context) (Report, error) {
	all, err := r.Store.List("")
	if err != nil {
		return Report{}, err
	}
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	rep := Report{
		Time:           now().UTC(),
		Components:     r.Collector.probe(ctx),
		Host:           hostInfo(r.Collector.DataDir),
		TotalIncidents: len(all),
		Incidents:      []ReportIncident{},
	}
	for i := len(all) - 1; i >= 0; i-- {
		inc := ReportIncident{Summary: all[i], State: state(all[i])}
		if inc.State == StateAwaitingApproval {
			rep.AwaitingApproval++
		}
		if len(rep.Incidents) < reportIncidents {
			rep.Incidents = append(rep.Incidents, inc)
		}
	}
	return rep, nil
}

func state(s Summary) string {
	t := s.Triage
	switch {
	case t.Status == TriageFailed:
		return StateTriageFailed
	case t.Status == TriageSkipped:
		return StateTriageSkipped
	case s.Review != nil && s.Review.Verdict == VerdictApproved:
		return StateApproved
	case s.Review != nil:
		return StateRejected
	case t.Action == "report_only":
		return StateReportOnly
	case t.Rating != RatingHigh:
		return StateNotOffered
	}
	return StateAwaitingApproval
}

// Text renders the report for a person at a terminal.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "KubeSolo assistant report, %s\n\nHealth\n", r.Time.Format("2006-01-02 15:04 MST"))
	for _, name := range slices.Sorted(maps.Keys(r.Components)) {
		fmt.Fprintf(&b, "  %-11s %s\n", name, r.Components[name])
	}
	fmt.Fprintf(&b, "  disk %d%% used, %d MiB memory available, up %s\n",
		r.Host.DiskUsedPercent, r.Host.MemoryAvailableMiB, time.Duration(r.Host.UptimeSeconds)*time.Second)

	fmt.Fprintf(&b, "\nIncidents, newest first (%d of %d)\n", len(r.Incidents), r.TotalIncidents)
	if len(r.Incidents) == 0 {
		b.WriteString("  none\n")
	}
	for _, inc := range r.Incidents {
		t := inc.Triage
		fmt.Fprintf(&b, "\n  %s  %s\n  %s\n", inc.Time.UTC().Format("2006-01-02 15:04"), inc.ID, inc.Trigger)
		if t.Status == TriageDone {
			rated := "unrated"
			if t.Rating != "" {
				rated = fmt.Sprintf("rated %s (%.2f)", t.Rating, t.ActionProbability)
			}
			fmt.Fprintf(&b, "    %s, %s: %s\n", t.Action, rated, inc.State)
			fmt.Fprintf(&b, "    %s\n", t.Summary)
		} else {
			fmt.Fprintf(&b, "    %s: %s\n", inc.State, t.Reason)
		}
		if rv := inc.Review; rv != nil {
			fmt.Fprintf(&b, "    review: %s, right action %s", rv.Verdict, cmp.Or(rv.Action, "not given"))
			if rv.Note != "" {
				fmt.Fprintf(&b, ": %q", rv.Note)
			}
			b.WriteString("\n")
		}
	}

	if r.AwaitingApproval > 0 {
		fmt.Fprintf(&b, "\n%d action(s) awaiting approval: kubesoloctl assistant approve <id>, or reject <id>\n", r.AwaitingApproval)
	}
	return b.String()
}
