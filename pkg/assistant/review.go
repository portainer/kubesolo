package assistant

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Review verdicts.
const (
	// VerdictApproved agrees with the suggested action.
	VerdictApproved = "approved"
	// VerdictRejected disagrees with it.
	VerdictRejected = "rejected"
)

// maxNoteLength bounds a reviewer's note.
const maxNoteLength = 1000

var (
	// ErrNotOffered means the incident has no action to approve: its action
	// is rated below high, is report_only, or it was not diagnosed.
	ErrNotOffered = errors.New("no action is offered for this incident")

	// ErrInvalidReview means the review itself is malformed.
	ErrInvalidReview = errors.New("invalid review")
)

// Review is what a person made of a diagnosis. Reviewed incidents are the
// labelled data the model and its knowledge are measured, and later trained,
// against: Action is always the action the reviewer holds right.
type Review struct {
	Verdict string `json:"verdict"`

	// Action is the right action: the suggested one when approved, and the
	// reviewer's when rejected, if they said.
	Action string `json:"action,omitempty"`

	Note string    `json:"note,omitempty"`
	Time time.Time `json:"time"`
}

// Review records a review of the triaged incident id, replacing any earlier
// one, and returns the incident.
func (s *Store) Review(id string, r Review, now time.Time) (*Incident, error) {
	switch {
	case r.Verdict != VerdictApproved && r.Verdict != VerdictRejected:
		return nil, fmt.Errorf("%w: verdict must be %q or %q, got %q", ErrInvalidReview, VerdictApproved, VerdictRejected, r.Verdict)
	case r.Action != "" && !slices.Contains(Actions, r.Action):
		return nil, fmt.Errorf("%w: unknown action %q", ErrInvalidReview, r.Action)
	case len(r.Note) > maxNoteLength:
		return nil, fmt.Errorf("%w: the note is %d characters, and can be at most %d", ErrInvalidReview, len(r.Note), maxNoteLength)
	}

	var out *Incident
	err := s.update(id, func(inc *Incident) error {
		t := inc.Triage
		if r.Verdict == VerdictApproved {
			if t.Status != TriageDone {
				return fmt.Errorf("%w: the incident was %s, not diagnosed", ErrNotOffered, t.Status)
			}
			if t.Action == "report_only" || t.Rating != RatingHigh {
				return fmt.Errorf("%w: %s is rated %s, and only an action rated %s can be approved", ErrNotOffered, t.Action, t.Rating, RatingHigh)
			}
			if r.Action != "" && r.Action != t.Action {
				return fmt.Errorf("%w: approving agrees with the suggested action, %s; reject it to name another", ErrInvalidReview, t.Action)
			}
			r.Action = t.Action
		}
		r.Time = now.UTC()
		inc.Review = &r
		out = inc
		return nil
	})
	return out, err
}
