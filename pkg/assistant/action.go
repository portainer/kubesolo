package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Where a carried-out action stands.
const (
	ActionRunning   = "running"
	ActionSucceeded = "succeeded"
	ActionFailed    = "failed"
	// ActionRefused means its preconditions no longer held when it was to be
	// carried out; nothing was done.
	ActionRefused = "refused"
)

var (
	// ErrNotApproved means the incident's action has not been approved.
	ErrNotApproved = errors.New("the incident's action has not been approved")
	// ErrAlreadyCarriedOut means the action has been carried out, or refused.
	ErrAlreadyCarriedOut = errors.New("the incident's action has already been carried out")
	// ErrActionRunning means another action is being carried out.
	ErrActionRunning = errors.New("another action is being carried out, try again shortly")
)

// ActionRun is an action carried out for an incident.
type ActionRun struct {
	Action string    `json:"action"`
	Status string    `json:"status"`
	Reason string    `json:"reason,omitempty"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end,omitzero"`

	// Auto means it was carried out without approval.
	Auto bool `json:"auto,omitempty"`

	// Job is the job the action was left to, when it outlives the process
	// that started it, as a restart does; the action is settled from it.
	Job string `json:"job,omitempty"`
}

// An Actor carries out actions on the node.
type Actor interface {
	// Check says why the action must not be carried out now, or returns nil:
	// the preconditions are checked against the node as it is, not as it was
	// at the incident.
	Check(ctx context.Context, action string, inc Incident) error

	// Run carries the action out. An action that outlives KubeSolo, such as
	// restarting it, is left to a job whose ID Run returns; Settle reports its
	// outcome later.
	Run(ctx context.Context, action string, inc Incident) (job string, err error)

	// Settle reports whether job has ended and, if so, how: nil when it
	// succeeded.
	Settle(ctx context.Context, job string) (done bool, err error)
}

// Runner carries out approved actions, one at a time, in the background.
type Runner struct {
	Actor Actor
	Store *Store

	now func() time.Time
	mu  sync.Mutex
	wg  sync.WaitGroup
}

// autoInterval is how seldom a restart is carried out without approval.
const autoInterval = time.Hour

// retryAfter is how long an action that failed is not carried out again for
// the same components: it would not help until something else changes.
const retryAfter = 6 * time.Hour

// failingOf is what an incident is about: the components its trigger names,
// as in "unhealthy for 5m0s: kubelet", or else the trigger itself.
func failingOf(trigger string) string {
	if rest, ok := strings.CutPrefix(trigger, "unhealthy for "); ok {
		if _, names, ok := strings.Cut(rest, ": "); ok {
			return names
		}
	}
	return trigger
}

// failedBefore says why inc's action is not worth carrying out, or returns
// "": the same action, for the same components, failed within retryAfter.
func failedBefore(all []Summary, inc Summary, now time.Time) string {
	if inc.Triage == nil {
		return ""
	}
	for _, s := range all {
		run := s.ActionRun
		if s.ID == inc.ID || run == nil || run.Status != ActionFailed || run.Action != inc.Triage.Action ||
			failingOf(s.Trigger) != failingOf(inc.Trigger) || now.Sub(run.Start) > retryAfter {
			continue
		}
		return fmt.Sprintf("%s failed for %s at %s and would not help again: %s",
			run.Action, failingOf(s.Trigger), run.Start.UTC().Format("2006-01-02 15:04 MST"), run.Reason)
	}
	return ""
}

// notAgain says why inc's action is not worth carrying out, or returns nil.
func (r *Runner) notAgain(inc Incident) error {
	all, err := r.Store.List("")
	if err != nil {
		return err
	}
	if why := failedBefore(all, inc.Summary(), r.clock()); why != "" {
		return errors.New(why)
	}
	return nil
}

// Auto carries out, without approval, an incident's restart_component rated
// high — the only action KubeSolo takes on its own, and only when
// assistant.autoRestart is set — at most once an hour. Its preconditions are
// checked as for an approved one.
func (r *Runner) Auto(ctx context.Context, inc Incident) {
	t := inc.Triage
	if t == nil || t.Status != TriageDone || t.Action != "restart_component" || t.Rating != RatingHigh {
		return
	}
	if last := r.lastAuto(); !last.IsZero() && r.clock().Sub(last) < autoInterval {
		log.Info().Str("component", "assistant").Str("incident", inc.ID).Msg("not restarting automatically again within the hour; approve it to restart")
		return
	}
	if err := r.start(ctx, inc.ID, true); err != nil {
		log.Warn().Str("component", "assistant").Str("incident", inc.ID).Err(err).Msg("cannot restart automatically")
	}
}

// lastAuto is when an action was last carried out automatically, from the
// incidents kept: a restart restarts the runner too.
func (r *Runner) lastAuto() time.Time {
	all, err := r.Store.List("")
	if err != nil {
		return r.clock() // unknown: as if just now, so none is carried out
	}
	var last time.Time
	for _, s := range all {
		if run := s.ActionRun; run != nil && run.Auto && run.Start.After(last) {
			last = run.Start
		}
	}
	return last
}

// Start carries out the approved action of incident id in the background,
// and records how it went on the incident.
func (r *Runner) Start(ctx context.Context, id string) error {
	return r.start(ctx, id, false)
}

func (r *Runner) start(ctx context.Context, id string, auto bool) error {
	inc, err := r.Store.Get(id)
	if err != nil {
		return err
	}
	switch {
	case !auto && (inc.Review == nil || inc.Review.Verdict != VerdictApproved):
		return ErrNotApproved
	case inc.ActionRun != nil:
		return ErrAlreadyCarriedOut
	}
	if !r.mu.TryLock() {
		return ErrActionRunning
	}

	action := inc.Triage.Action
	run := &ActionRun{Action: action, Status: ActionRunning, Start: r.clock().UTC(), Auto: auto}
	if err := r.record(id, run); err != nil {
		r.mu.Unlock()
		return err
	}
	r.wg.Go(func() {
		defer r.mu.Unlock()
		log.Info().Str("component", "assistant").Str("incident", id).Str("action", action).Bool("auto", auto).Msg("carrying out the action...")
		err := r.notAgain(*inc)
		if err == nil {
			err = r.Actor.Check(ctx, action, *inc)
		}
		switch {
		case err != nil:
			run.Status, run.Reason = ActionRefused, err.Error()
		default:
			job, err := r.Actor.Run(ctx, action, *inc)
			switch {
			case err != nil:
				run.Status, run.Reason = ActionFailed, err.Error()
			case job != "":
				run.Job = job // settled once the job ends
			default:
				run.Status = ActionSucceeded
			}
		}
		if run.Status != ActionRunning {
			run.End = r.clock().UTC()
		}
		if err := r.record(id, run); err != nil {
			log.Error().Str("component", "assistant").Str("incident", id).Err(err).Msg("cannot record the action")
		}
		log.Info().Str("component", "assistant").Str("incident", id).Str("action", action).Str("status", run.Status).Str("reason", run.Reason).Msg("action done")
	})
	return nil
}

// Settle records the outcome of actions left running: those left to a job
// that has since ended, and those cut short by KubeSolo stopping. It does
// nothing while an action is being carried out here.
func (r *Runner) Settle(ctx context.Context) {
	if !r.mu.TryLock() {
		return
	}
	defer r.mu.Unlock()
	all, err := r.Store.List("")
	if err != nil {
		return
	}
	for _, s := range all {
		run := s.ActionRun
		if run == nil || run.Status != ActionRunning {
			continue
		}
		settled := *run
		if run.Job == "" {
			settled.Status, settled.Reason = ActionFailed, "KubeSolo stopped while the action was being carried out"
		} else {
			done, err := r.Actor.Settle(ctx, run.Job)
			if !done {
				continue
			}
			settled.Status = ActionSucceeded
			if err != nil {
				settled.Status, settled.Reason = ActionFailed, err.Error()
			}
		}
		settled.End = r.clock().UTC()
		if err := r.record(s.ID, &settled); err != nil {
			log.Error().Str("component", "assistant").Str("incident", s.ID).Err(err).Msg("cannot record the action")
			continue
		}
		log.Info().Str("component", "assistant").Str("incident", s.ID).Str("action", settled.Action).Str("status", settled.Status).Str("reason", settled.Reason).Msg("action settled")
	}
}

// Wait waits for the action in progress, if any.
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) record(id string, run *ActionRun) error {
	return r.Store.update(id, func(inc *Incident) error {
		c := *run
		inc.ActionRun = &c
		return nil
	})
}

func (r *Runner) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}
