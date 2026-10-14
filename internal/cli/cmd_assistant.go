package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/ui"
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
whole as JSON. The last 100 are kept.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runIncident(args[0])
			}
			return runIncidents()
		},
	})
	return cmd
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
		fmt.Printf("%s  %s  %s\n", inc.ID, inc.Kind, inc.Trigger)
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
