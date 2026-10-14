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
	"time"
)

// MaxIncidents is how many incidents are kept; older ones are deleted.
const MaxIncidents = 100

// KindKubeSolo is an incident in KubeSolo itself, as opposed to a workload.
const KindKubeSolo = "kubesolo"

// ErrNoIncident means there is no incident with that ID.
var ErrNoIncident = errors.New("no such incident")

// idLayout makes IDs that sort in time order, so a poller can ask for
// everything after the newest ID it has seen.
const idLayout = "20060102T150405.000000000Z"

var idPattern = regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z$`)

// Incident is a failure worth explaining: what triggered it, and the state of
// the node at the time.
type Incident struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Trigger  string    `json:"trigger"`
	Snapshot Snapshot  `json:"snapshot"`
}

// Summary is an incident without its snapshot, for listing.
type Summary struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Trigger string    `json:"trigger"`
}

func newIncidentID(t time.Time) string {
	return t.UTC().Format(idLayout)
}

// Store keeps incidents as one JSON file each, so they survive a crash and a
// restart.
type Store struct {
	dir string
}

// NewStore returns a store in dir, created on the first save.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Save writes the incident and deletes the oldest beyond MaxIncidents.
func (s *Store) Save(inc Incident) error {
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

	ids, err := s.ids()
	if err != nil {
		return err
	}
	for _, id := range ids[:max(0, len(ids)-MaxIncidents)] {
		_ = os.Remove(s.path(id))
	}
	return nil
}

// List summarises the incidents after since, oldest first. An empty since
// lists them all.
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
			continue // deleted by a concurrent save
		}
		out = append(out, Summary{ID: inc.ID, Time: inc.Time, Kind: inc.Kind, Trigger: inc.Trigger})
	}
	return out, nil
}

// Get reads one incident. Anything that is not an ID the store issued is
// ErrNoIncident, so a caller's ID never names an arbitrary file.
func (s *Store) Get(id string) (*Incident, error) {
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
