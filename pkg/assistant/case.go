package assistant

import "time"

// Case is a test case of the assistant's triage: an incident, and what its
// diagnosis should be. test/assistant holds them; reviewed incidents and
// test/assistant/inject both make them.
type Case struct {
	Incident CaseIncident `json:"incident"`
	Expect   CaseExpect   `json:"expect"`
	Source   CaseSource   `json:"source"`
}

// CaseIncident is what the model reads of an incident.
type CaseIncident struct {
	Trigger  string    `json:"trigger"`
	Time     time.Time `json:"time"`
	Snapshot Snapshot  `json:"snapshot"`
}

// CaseExpect is what the diagnosis should be.
type CaseExpect struct {
	// Actions are the acceptable actions.
	Actions []string `json:"actions"`
	// Mentions are words of which the summary or cause should name one.
	Mentions []string `json:"mentions,omitempty"`
}

// CaseSource is where a case came from, for reference: it is not scored.
type CaseSource struct {
	Fault    string  `json:"fault,omitempty"`
	Run      int     `json:"run,omitempty"`
	Node     string  `json:"node,omitempty"`
	Incident string  `json:"incident,omitempty"`
	Triage   *Triage `json:"triage,omitempty"`
	Review   *Review `json:"review,omitempty"`
}

// CaseFromReview makes a test case of a reviewed incident, labelled with the
// reviewer's action. A review that names no action is no label.
func CaseFromReview(inc Incident) (Case, bool) {
	if inc.Review == nil || inc.Review.Action == "" {
		return Case{}, false
	}
	return Case{
		Incident: CaseIncident{Trigger: inc.Trigger, Time: inc.Time, Snapshot: inc.Snapshot},
		Expect:   CaseExpect{Actions: []string{inc.Review.Action}},
		Source:   CaseSource{Incident: inc.ID, Triage: inc.Triage, Review: inc.Review},
	}, true
}
