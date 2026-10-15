package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

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
suggests; none is taken. The last 100 are kept.`,
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
		Short: "Agree with the action the assistant suggests for an incident",
		Long: `Agree with the action the assistant suggests for an incident. Only an
action rated high can be approved. Reviews are recorded with the incident, to
measure and improve the assistant; no action is carried out yet.`,
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
	cmd.AddCommand(approve, reject, report)
	return cmd
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
	fmt.Print(r.Text())
	return nil
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
		return p.Fail("assistant", err)
	}
	p.Info(fmt.Sprintf("%s %s: %s", inc.ID, inc.Review.Verdict, cmp.Or(inc.Review.Action, "no action named")))
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
	for _, inc := range list {
		status, action, rating, review := "-", "-", "-", "-"
		if inc.Triage != nil {
			status, action, rating = inc.Triage.Status, cmp.Or(inc.Triage.Action, "-"), cmp.Or(inc.Triage.Rating, "-")
		}
		if inc.Review != nil {
			review = inc.Review.Verdict
		}
		fmt.Printf("%s  %s  %s  %s  %s  %s  %s\n", inc.ID, inc.Kind, status, action, rating, review, inc.Trigger)
	}
	return nil
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
		return p.Fail("assistant", err)
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

	p.Step("asking the assistant (this can take a minute or more)")
	resp, err := h.api.Ask(question)
	if err != nil {
		return p.Fail("assistant", err)
	}
	fmt.Println(resp.Answer)
	return nil
}
