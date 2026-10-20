package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// MaxIncidents is how many unreviewed incidents are kept, and
// MaxReviewedIncidents how many reviewed ones: those are the labelled data the
// assistant is measured and trained against, so they are kept longer. Older
// ones are deleted.
const (
	MaxIncidents         = 100
	MaxReviewedIncidents = 1000
)

// KindKubeSolo is an incident in KubeSolo itself, as opposed to a workload.
const KindKubeSolo = "kubesolo"

// ErrNoIncident means there is no incident with that ID.
var ErrNoIncident = errors.New("no such incident")

// idLayout makes IDs that sort in time order, so a poller can ask for
// everything after the newest ID it has seen.
const idLayout = "20060102T150405.000000000Z"

var idPattern = regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z$`)

// Incident is a failure worth explaining: what triggered it, the state of the
// node at the time, and the model's diagnosis.
type Incident struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Trigger  string    `json:"trigger"`
	Snapshot Snapshot  `json:"snapshot"`

	// Triage is nil until the incident has been triaged; until then it is not
	// served.
	Triage *Triage `json:"triage,omitempty"`

	// Review is what a person made of the diagnosis; nil until reviewed.
	Review *Review `json:"review,omitempty"`
}

// Summary is an incident without its snapshot, for listing.
type Summary struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Trigger string    `json:"trigger"`
	Triage  *Triage   `json:"triage"`
	Review  *Review   `json:"review,omitempty"`
}

// Triage outcomes.
const (
	// TriageDone means the model diagnosed the incident.
	TriageDone = "triaged"
	// TriageFailed means the model ran but gave no usable diagnosis.
	TriageFailed = "failed"
	// TriageSkipped means the model was not run; Reason says why.
	TriageSkipped = "skipped"
)

// Triage is the outcome of triaging an incident.
type Triage struct {
	Status string    `json:"status"`
	Reason string    `json:"reason,omitempty"`
	Time   time.Time `json:"time"`
	Diagnosis
}

// Diagnosis is the model's reading of an incident.
type Diagnosis struct {
	Summary string `json:"summary,omitempty"`
	Cause   string `json:"cause,omitempty"`

	// Action is one of Actions. It is only suggested; nothing acts on it.
	Action string `json:"action,omitempty"`

	// ActionProbability is the probability the model gave Action among the
	// six, from its token probabilities rather than its own say; 0 when
	// unknown.
	ActionProbability float64 `json:"actionProbability,omitempty"`

	// Rating is ActionProbability in words: RatingHigh, RatingMedium or
	// RatingLow.
	Rating string `json:"rating,omitempty"`
}

func newIncidentID(t time.Time) string {
	return t.UTC().Format(idLayout)
}

// Store keeps incidents as one JSON file each, so they survive a crash and a
// restart.
type Store struct {
	dir   string
	added chan struct{}

	// mu serialises writes, so that a review and a triage cannot overwrite
	// each other.
	mu sync.Mutex

	// reviewed caches which stored incidents are reviewed, for pruning; nil
	// until first needed.
	reviewed map[string]bool
}

// NewStore returns a store in dir, created on the first save.
func NewStore(dir string) *Store {
	return &Store{dir: dir, added: make(chan struct{}, 1)}
}

// Added receives when an incident waiting for triage is saved.
func (s *Store) Added() <-chan struct{} {
	return s.added
}

// Save writes the incident and deletes the oldest beyond MaxIncidents, or
// MaxReviewedIncidents for reviewed ones.
func (s *Store) Save(inc Incident) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(inc)
}

// update applies change to the triaged incident id and saves it.
func (s *Store) update(id string, change func(*Incident) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inc, err := s.Get(id)
	if err != nil {
		return err
	}
	if err := change(inc); err != nil {
		return err
	}
	return s.save(*inc)
}

func (s *Store) save(inc Incident) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(inc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".incident-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path(inc.ID)); err != nil {
		return err
	}

	if err := s.prune(inc); err != nil {
		return err
	}

	if inc.Triage == nil {
		select {
		case s.added <- struct{}{}:
		default:
		}
	}
	return nil
}

// prune deletes the oldest incidents beyond the bounds, now that inc has been
// saved.
func (s *Store) prune(inc Incident) error {
	ids, err := s.ids()
	if err != nil {
		return err
	}
	if s.reviewed == nil {
		s.reviewed = map[string]bool{}
		for _, id := range ids {
			if stored, err := s.read(id); err == nil {
				s.reviewed[id] = stored.Review != nil
			}
		}
	}
	s.reviewed[inc.ID] = inc.Review != nil

	var reviewed, unreviewed []string
	for _, id := range ids {
		if s.reviewed[id] {
			reviewed = append(reviewed, id)
		} else {
			unreviewed = append(unreviewed, id)
		}
	}
	for _, id := range slices.Concat(unreviewed[:max(0, len(unreviewed)-MaxIncidents)], reviewed[:max(0, len(reviewed)-MaxReviewedIncidents)]) {
		_ = os.Remove(s.path(id))
		delete(s.reviewed, id)
	}
	return nil
}

// List summarises the triaged incidents after since, oldest first. An empty
// since lists them all.
func (s *Store) List(since string) ([]Summary, error) {
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, id := range ids {
		if id <= since {
			continue
		}
		inc, err := s.Get(id)
		if err != nil {
			continue // deleted by a concurrent save, or not triaged yet
		}
		out = append(out, Summary{ID: inc.ID, Time: inc.Time, Kind: inc.Kind, Trigger: inc.Trigger, Triage: inc.Triage, Review: inc.Review})
	}
	return out, nil
}

// Get reads one triaged incident. Anything that is not an ID the store issued,
// or not triaged yet, is ErrNoIncident, so a caller's ID never names an
// arbitrary file.
func (s *Store) Get(id string) (*Incident, error) {
	inc, err := s.read(id)
	if err != nil {
		return nil, err
	}
	if inc.Triage == nil {
		return nil, ErrNoIncident
	}
	return inc, nil
}

// Pending returns the incidents waiting for triage, oldest first.
func (s *Store) Pending() ([]Incident, error) {
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	var out []Incident
	for _, id := range ids {
		if inc, err := s.read(id); err == nil && inc.Triage == nil {
			out = append(out, *inc)
		}
	}
	return out, nil
}

func (s *Store) read(id string) (*Incident, error) {
	if !idPattern.MatchString(id) {
		return nil, ErrNoIncident
	}
	raw, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoIncident
	}
	if err != nil {
		return nil, err
	}
	var inc Incident
	if err := json.Unmarshal(raw, &inc); err != nil {
		return nil, fmt.Errorf("read incident %s: %w", id, err)
	}
	return &inc, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// ids lists the stored incident IDs in time order.
func (s *Store) ids() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && idPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}
