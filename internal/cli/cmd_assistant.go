package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/pkg/assistant"
	"github.com/portainer/kubesolo/pkg/components/configapi"
	"github.com/spf13/cobra"
)

var errAPIUnreachable = errors.New("the configuration API is not reachable: the assistant is reached through it, so set api.enabled to true and restart KubeSolo")

func assistantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assistant",
		Short: "Talk to KubeSolo's built-in assistant",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "ask QUESTION",
		Short: "Ask the assistant a question",
		Long: `Ask the assistant a question.

KubeSolo starts its model, answers, and stops it again, so the first word of
the answer can take a minute or more on a small device. The assistant is
reached through the configuration API, so both assistant.enabled and
api.enabled must be true.`,
		Example: `  sudo kubesoloctl assistant ask "why would containerd keep restarting?"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAsk(strings.Join(args, " "))
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "incidents [ID]",
		Short: "List the incidents the assistant has recorded, or show one",
		Long: `List the incidents the assistant has recorded, oldest first, or show one
whole as JSON. Each is listed once triaged, with the action the model
suggests. The last 100 are kept.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runIncident(args[0])
			}
			return runIncidents()
		},
	})
	var note, action string
	approve := &cobra.Command{
		Use:   "approve ID",
		Short: "Agree with the action the assistant suggests for an incident, and carry it out",
		Long: `Agree with the action the assistant suggests for an incident, and carry it
out. Only an action rated high can be approved. Its preconditions are checked
again first; if they no longer hold, it is refused and nothing is done.
"kubesoloctl assistant report" shows how it went. Reviews are recorded with the
incident, to measure and improve the assistant.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(args[0], func(api *configapi.Client) (*assistant.Incident, error) {
				return api.Approve(args[0], note)
			})
		},
	}
	approve.Flags().StringVar(&note, "note", "", "why, for whoever reads the incident later")
	reject := &cobra.Command{
		Use:   "reject ID",
		Short: "Disagree with the assistant's diagnosis of an incident",
		Long: `Disagree with the assistant's diagnosis of an incident, and say what the
right action was if you know. Reviews are recorded with the incident, to
measure and improve the assistant.`,
		Example: `  sudo kubesoloctl assistant reject 20261015T090000.000000000Z --action repair_database --note "lease UID mismatch"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(args[0], func(api *configapi.Client) (*assistant.Incident, error) {
				return api.Reject(args[0], action, note)
			})
		},
	}
	reject.Flags().StringVar(&action, "action", "", "the right action: "+strings.Join(assistant.Actions, ", "))
	reject.Flags().StringVar(&note, "note", "", "why, for whoever reads the incident later")
	var asJSON bool
	report := &cobra.Command{
		Use:   "report",
		Short: "Show the node's health and the assistant's latest incidents",
		Long: `Show the node's health now and the assistant's latest incidents: each one's
diagnosis, how far to trust its action, and whether it awaits approval or was
reviewed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(asJSON)
		},
	}
	report.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	cmd.AddCommand(approve, reject, report, &cobra.Command{
		Use:   "export DIR",
		Short: "Write the reviewed incidents as test cases for the assistant",
		Long: `Write each reviewed incident that names the right action as a test case in
DIR, in the format of KubeSolo's triage test set (test/assistant): the
incident, labelled with the reviewer's action. They measure, and can train,
the assistant against real failures.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(args[0])
		},
	})
	return cmd
}

func runExport(dir string) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errAPIUnreachable)
	}
	node, _ := os.Hostname()
	n, err := exportCases(h.api, dir, node)
	if err != nil {
		return p.Fail("assistant", err)
	}
	p.OK(fmt.Sprintf("%d test case(s) written", n), dir)
	return nil
}

// incidentReader is the part of the API exportCases reads.
type incidentReader interface {
	Incidents(since string) ([]assistant.Summary, error)
	Incident(id string) (*assistant.Incident, error)
}

// exportCases writes each reviewed incident that names the right action to
// dir as a test case, and returns how many it wrote.
func exportCases(api incidentReader, dir, node string) (int, error) {
	list, err := api.Incidents("")
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	n := 0
	for _, s := range list {
		if s.Review == nil || s.Review.Action == "" {
			continue
		}
		inc, err := api.Incident(s.ID)
		if err != nil {
			return n, err
		}
		c, ok := assistant.CaseFromReview(*inc)
		if !ok {
			continue
		}
		c.Source.Node = node
		raw, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(filepath.Join(dir, s.ID+".json"), append(raw, '\n'), 0o644); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func runReport(asJSON bool) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errAPIUnreachable)
	}
	r, err := h.api.Report()
	if err != nil {
		return p.Fail("assistant", err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	printReport(p, *r)
	return nil
}

// printReport prints the report for a person at a terminal.
func printReport(p *ui.Printer, r assistant.Report) {
	p.Header("assistant report")

	p.Section("Health, " + r.Time.Format("2006-01-02 15:04 MST"))
	for _, name := range slices.Sorted(maps.Keys(r.Components)) {
		if status := r.Components[name]; status == "ok" {
			p.OK(name, "")
		} else {
			_ = p.Fail(name, errors.New(status))
		}
	}
	p.Info(fmt.Sprintf("disk %d%% used, %d MiB memory available, up %s",
		r.Host.DiskUsedPercent, r.Host.MemoryAvailableMiB, time.Duration(r.Host.UptimeSeconds)*time.Second))

	p.Section(fmt.Sprintf("Incidents, newest first (%d of %d)", len(r.Incidents), r.TotalIncidents))
	if len(r.Incidents) == 0 {
		p.Info("none")
	}
	var next []string
	for _, inc := range r.Incidents {
		t := inc.Triage
		p.Step(inc.Trigger)
		p.Label("ID", inc.ID)
		p.Label("Time", inc.Time.UTC().Format("2006-01-02 15:04 MST"))
		if t.Status == assistant.TriageDone {
			rated := "unrated"
			if t.Rating != "" {
				rated = fmt.Sprintf("rated %s (%.2f)", t.Rating, t.ActionProbability)
			}
			p.Label("Action", fmt.Sprintf("%s, %s", t.Action, rated))
			p.Label("State", inc.State)
			p.Label("Summary", t.Summary)
		} else {
			p.Label("State", fmt.Sprintf("%s: %s", inc.State, t.Reason))
		}
		if run := inc.ActionRun; run != nil {
			p.Label("Run", strings.TrimSuffix(fmt.Sprintf("%s %s, %s", run.Action, run.Status, run.Reason), ", "))
		}
		if rv := inc.Review; rv != nil {
			review := fmt.Sprintf("%s, right action %s", rv.Verdict, cmp.Or(rv.Action, "not given"))
			if rv.Note != "" {
				review += fmt.Sprintf(": %q", rv.Note)
			}
			p.Label("Review", review)
		}
		if inc.State == assistant.StateAwaitingApproval {
			next = append(next,
				"sudo kubesoloctl assistant approve "+inc.ID,
				"sudo kubesoloctl assistant reject "+inc.ID+" --action <the right action>")
		}
	}
	if r.AwaitingApproval > 0 {
		p.Hint(fmt.Sprintf("%d action(s) awaiting approval", r.AwaitingApproval), next...)
	}
	p.Blank()
}

func runReview(id string, review func(*configapi.Client) (*assistant.Incident, error)) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errAPIUnreachable)
	}
	inc, err := review(h.api)
	if err != nil {
		return p.Fail("incident "+id, err)
	}
	if inc.Review.Verdict == assistant.VerdictApproved {
		p.OK("incident approved", inc.ID+" · "+inc.Review.Action)
		p.Info("follow the action with: sudo kubesoloctl assistant report")
	} else {
		p.OK("incident rejected", inc.ID+" · right action "+cmp.Or(inc.Review.Action, "not given"))
	}
	return nil
}

func runIncidents() error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errAPIUnreachable)
	}
	list, err := h.api.Incidents("")
	if err != nil {
		return p.Fail("assistant", err)
	}
	if len(list) == 0 {
		p.Info("no incidents recorded")
		return nil
	}
	printIncidents(os.Stdout, list)
	return nil
}

// printIncidents prints the incidents as a table, like kubectl get.
func printIncidents(w io.Writer, list []assistant.Summary) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tKIND\tTRIAGE\tACTION\tRATING\tREVIEW\tTRIGGER")
	for _, inc := range list {
		status, action, rating, review := "-", "-", "-", "-"
		if inc.Triage != nil {
			status, action, rating = inc.Triage.Status, cmp.Or(inc.Triage.Action, "-"), cmp.Or(inc.Triage.Rating, "-")
		}
		if inc.Review != nil {
			review = inc.Review.Verdict
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", inc.ID, inc.Kind, status, action, rating, review, inc.Trigger)
	}
	_ = tw.Flush()
}

func runIncident(id string) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errAPIUnreachable)
	}
	inc, err := h.api.Incident(id)
	if err != nil {
		return p.Fail("incident "+id, err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(inc)
}

func runAsk(question string) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errAPIUnreachable)
	}

	p.Header("assistant ask")
	p.Step("Asking the assistant (this can take a minute or more)")
	resp, err := h.api.Ask(question)
	if err != nil {
		return p.Fail("assistant", err)
	}
	p.Blank()
	fmt.Println(resp.Answer)
	p.Blank()
	return nil
}
