package configapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/portainer/kubesolo/pkg/assistant"
)

// askTimeout bounds one question: loading the model, reading the prompt and
// writing the answer. On two CPUs a long prompt alone takes over a minute.
const askTimeout = 10 * time.Minute

// Asker answers questions with KubeSolo's own model.
type Asker interface {
	Ask(ctx context.Context, question string) (string, error)
}

// AskRequest is the body of POST /api/v1/assistant:ask.
type AskRequest struct {
	Question string `json:"question"`
}

// AskResponse is the assistant's answer.
type AskResponse struct {
	Answer string `json:"answer"`
}

func (s *Service) handleAsk(w http.ResponseWriter, r *http.Request) {
	if s.opts.Assistant == nil {
		writeErrorFor(w, r, http.StatusNotFound, assistantDisabled)
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req AskRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: "body is not a valid question"})
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: "question is empty"})
		return
	}

	// Answering takes far longer than the server's write timeout allows any
	// other request.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(askTimeout + 10*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), askTimeout)
	defer cancel()

	answer, err := s.opts.Assistant.Ask(ctx, req.Question)
	switch {
	case errors.Is(err, assistant.ErrBusy):
		writeErrorFor(w, r, http.StatusConflict, ErrorResponse{Error: err.Error()})
	case err != nil:
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
	default:
		writeJSON(w, http.StatusOK, AskResponse{Answer: answer})
	}
}

// Incidents reads the incidents the assistant has recorded, and records
// people's reviews of them.
type Incidents interface {
	List(since string) ([]assistant.Summary, error)
	Get(id string) (*assistant.Incident, error)
	Review(id string, r assistant.Review, now time.Time) (*assistant.Incident, error)
}

// ReviewRequest is the body of POST /api/v1/assistant/incidents/{id}:approve
// and :reject; both fields are optional.
type ReviewRequest struct {
	// Action is the right action, when rejecting.
	Action string `json:"action,omitempty"`
	Note   string `json:"note,omitempty"`
}

// reviewVerbs maps each verb on an incident to its review verdict.
var reviewVerbs = map[string]string{"approve": assistant.VerdictApproved, "reject": assistant.VerdictRejected}

// assistantDisabled is the answer to every assistant endpoint when it is off.
var assistantDisabled = ErrorResponse{Error: "the assistant is not enabled: set assistant.enabled to true and restart KubeSolo"}

// handleIncidents lists incidents after ?since=<id>, oldest first. A poller
// passes the newest ID it has seen.
func (s *Service) handleIncidents(w http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeErrorFor(w, r, http.StatusNotFound, assistantDisabled)
		return
	}
	list, err := s.opts.Incidents.List(r.URL.Query().Get("since"))
	if err != nil {
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Service) handleIncident(w http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeErrorFor(w, r, http.StatusNotFound, assistantDisabled)
		return
	}
	inc, err := s.opts.Incidents.Get(r.PathValue("id"))
	switch {
	case errors.Is(err, assistant.ErrNoIncident):
		writeErrorFor(w, r, http.StatusNotFound, ErrorResponse{Error: err.Error()})
	case err != nil:
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
	default:
		writeJSON(w, http.StatusOK, inc)
	}
}

// handleReview records a review: POST /api/v1/assistant/incidents/{id}:approve
// or {id}:reject. Approving agrees with the suggested action; nothing is
// carried out yet.
func (s *Service) handleReview(w http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeErrorFor(w, r, http.StatusNotFound, assistantDisabled)
		return
	}
	id, verb, _ := strings.Cut(r.PathValue("idverb"), ":")
	verdict, ok := reviewVerbs[verb]
	if !ok {
		writeErrorFor(w, r, http.StatusNotFound, ErrorResponse{Error: "an incident takes :approve or :reject"})
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req ReviewRequest
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: "body is not a valid review"})
			return
		}
	}

	inc, err := s.opts.Incidents.Review(id, assistant.Review{Verdict: verdict, Action: req.Action, Note: req.Note}, time.Now())
	switch {
	case errors.Is(err, assistant.ErrNoIncident):
		writeErrorFor(w, r, http.StatusNotFound, ErrorResponse{Error: err.Error()})
	case errors.Is(err, assistant.ErrNotOffered):
		writeErrorFor(w, r, http.StatusConflict, ErrorResponse{Error: err.Error()})
	case errors.Is(err, assistant.ErrInvalidReview):
		writeErrorFor(w, r, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
	case err != nil:
		writeErrorFor(w, r, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
	default:
		record(r, func(rl *requestLog) { rl.changed = []string{"assistant.incidents." + id + ".review"} })
		writeJSON(w, http.StatusOK, inc)
	}
}
