package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

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
	return cmd
}

func runAsk(question string) error {
	p := ui.New()
	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	if h.api == nil {
		return p.Fail("assistant", errors.New("the configuration API is not reachable: the assistant is asked through it, so set api.enabled to true and restart KubeSolo"))
	}

	p.Step("asking the assistant (this can take a minute or more)")
	resp, err := h.api.Ask(question)
	if err != nil {
		return p.Fail("assistant", err)
	}
	fmt.Println(resp.Answer)
	return nil
}
