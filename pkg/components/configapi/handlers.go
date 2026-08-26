package configapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
)

// redacted replaces a secret's value in responses.
//
// It is not merely a display concern: a client that reads the configuration and
// writes it back must not be able to overwrite the real credential with this
// placeholder, so writes reject it explicitly.
const redacted = "***"

// maxBodySize bounds a request body. The configuration document is a few
// kilobytes; anything approaching this is a mistake or an attack.
const maxBodySize = 1 << 20 // 1 MiB

// Response is what every endpoint returns. It is exported so that a client —
// kubesoloctl — shares one definition of the wire format with the server.
//
// RestartRequired is stated rather than implied. Almost every setting is read
// once during bootstrap and baked into types.Embedded, so a write here changes
// desired state and nothing else until KubeSolo restarts.
type Response struct {
	Config *types.Config `json:"config"`

	// Changed lists the settings this request altered, by config path.
	Changed []string `json:"changed,omitempty"`

	// RequiresRestart is the subset of Changed that cannot take effect until
	// KubeSolo restarts. Today that is all of them.
	RequiresRestart []string `json:"requiresRestart,omitempty"`
	RestartRequired bool     `json:"restartRequired"`

	Warnings []string `json:"warnings,omitempty"`
}

// ErrorResponse is the body of every failed request.
type ErrorResponse struct {
	Error string `json:"error"`

	// Field names the setting at fault, where one is identifiable.
	Field string `json:"field,omitempty"`
}

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("GET /api/v1/config", s.handleGet)
	mux.HandleFunc("GET /api/v1/config/schema", s.handleSchema)
	mux.HandleFunc("PUT /api/v1/config", s.handlePut)
	mux.HandleFunc("PATCH /api/v1/config", s.handlePatch)
	mux.HandleFunc("DELETE /api/v1/config", s.handleDelete)
	mux.HandleFunc("POST /api/v1/config:validate", s.handleValidate)

	return mux
}

// handleGet returns the stored configuration.
//
// This is the document on disk, not the settings the running process resolved.
// They differ when a flag or environment variable overrides the file, and
// returning the resolved values would mean a read-modify-write cycle silently
// writing those overrides into the file permanently.
func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.read()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	etag, err := etagOf(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	w.Header().Set("ETag", etag)

	if r.URL.Query().Get("showSecrets") != "true" {
		redactSecrets(cfg)
	}
	writeJSON(w, http.StatusOK, Response{Config: cfg})
}

func (s *Service) handleSchema(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": types.ConfigAPIVersion,
		"settings":   config.Describe(),
	})
}

// handlePut replaces the configuration wholesale.
func (s *Service) handlePut(w http.ResponseWriter, r *http.Request) {
	s.mutate(w, r, func(current *types.Config, body []byte) (*types.Config, error) {
		// Replacement starts from the defaults, not from the current document:
		// a setting the caller omitted is being removed, not left alone. That is
		// what distinguishes PUT from PATCH.
		next := config.Defaults()
		if err := json.Unmarshal(body, next); err != nil {
			return nil, badRequest{fmt.Errorf("body is not a valid configuration document: %w", err)}
		}
		return next, nil
	})
}

// handlePatch applies an RFC 7386 JSON merge patch.
//
// A null value removes the setting, which restores its default — the same thing
// deleting the line from the file would do.
func (s *Service) handlePatch(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/merge-patch+json") && !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, ErrorResponse{
			Error: fmt.Sprintf("content type %q is not supported; use application/merge-patch+json", ct),
		})
		return
	}

	s.mutate(w, r, func(current *types.Config, body []byte) (*types.Config, error) {
		if !json.Valid(body) {
			return nil, badRequest{errors.New("patch is not valid JSON")}
		}

		currentJSON, err := json.Marshal(current)
		if err != nil {
			return nil, err
		}

		merged, err := jsonpatch.MergePatch(currentJSON, body)
		if err != nil {
			return nil, badRequest{fmt.Errorf("could not apply the patch: %w", err)}
		}

		// Decoding onto the defaults rather than onto an empty document is what
		// makes a null in the patch mean "restore the default" instead of "set
		// the zero value".
		next := config.Defaults()
		if err := json.Unmarshal(merged, next); err != nil {
			return nil, badRequest{fmt.Errorf("the patched document is not valid: %w", err)}
		}
		return next, nil
	})
}

// handleDelete resets every setting to its default.
func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	s.mutate(w, r, func(current *types.Config, _ []byte) (*types.Config, error) {
		next := config.Defaults()

		// Path is immutable, so a reset cannot move it. Carrying it over keeps
		// the reset from being rejected by its own immutability check.
		next.Path = current.Path
		return next, nil
	})
}

// handleValidate checks a candidate document and reports what it would change,
// without saving anything.
func (s *Service) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	current, err := s.read()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	candidate := config.Defaults()
	if err := json.Unmarshal(body, candidate); err != nil {
		writeError(w, http.StatusBadRequest, ErrorResponse{
			Error: fmt.Sprintf("body is not a valid configuration document: %v", err),
		})
		return
	}

	warnings, err := config.Validate(candidate, s.opts.Host)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, ErrorResponse{Error: err.Error()})
		return
	}

	changed := changedSettings(current, candidate)
	redactSecrets(candidate)
	writeJSON(w, http.StatusOK, Response{
		Config:          candidate,
		Changed:         changed,
		RequiresRestart: changed,
		RestartRequired: len(changed) > 0,
		Warnings:        warningStrings(warnings),
	})
}

// badRequest marks an error the caller caused, so mutate can answer 400 rather
// than 500.
type badRequest struct{ error }

// mutate is the shared body of every write: read, build the next document,
// check the precondition, validate, save.
//
// Nothing touches the file until validation has passed, so a rejected request
// leaves the existing configuration exactly as it was.
func (s *Service) mutate(w http.ResponseWriter, r *http.Request, build func(*types.Config, []byte) (*types.Config, error)) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	// Serialised so two concurrent writers cannot both read, then both write,
	// losing one of the changes. A writer outside this process is caught by the
	// If-Match precondition instead, which a mutex cannot see.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	current, err := s.read()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if !s.checkPrecondition(w, r, current) {
		return
	}

	next, err := build(current, body)
	if err != nil {
		var bad badRequest
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, ErrorResponse{Error: bad.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if field, ok := rejectsRedactedSecret(next); ok {
		writeError(w, http.StatusBadRequest, ErrorResponse{
			Field: field,
			Error: fmt.Sprintf("%s was sent back as %q, the placeholder a redacted read returns; send the real value or omit the setting to keep the stored one", field, redacted),
		})
		return
	}

	if field, ok := immutableChange(current, next); ok {
		writeError(w, http.StatusConflict, ErrorResponse{
			Field: field,
			Error: fmt.Sprintf("%s cannot be changed on an existing installation: every certificate, the database and all container state live below it, and none of them move", field),
		})
		return
	}

	warnings, err := config.Validate(next, s.opts.Host)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, ErrorResponse{Error: err.Error()})
		return
	}

	changed := changedSettings(current, next)

	if err := config.Write(s.opts.ConfigPath, next); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	etag, err := etagOf(next)
	if err == nil {
		w.Header().Set("ETag", etag)
	}

	saved := *next
	redactSecrets(&saved)
	writeJSON(w, http.StatusOK, Response{
		Config:          &saved,
		Changed:         changed,
		RequiresRestart: changed,
		RestartRequired: len(changed) > 0,
		Warnings:        warningStrings(warnings),
	})
}

// checkPrecondition enforces If-Match, which is how a caller detects that the
// file changed under it — by another API client, or by kubesoloctl.
func (s *Service) checkPrecondition(w http.ResponseWriter, r *http.Request, current *types.Config) bool {
	want := r.Header.Get("If-Match")
	if want == "" || want == "*" {
		return true
	}

	got, err := etagOf(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return false
	}
	if got != want {
		writeError(w, http.StatusPreconditionFailed, ErrorResponse{
			Error: "the configuration changed since you read it; read it again and reapply your change",
		})
		return false
	}
	return true
}

// read loads the configuration file over the defaults.
func (s *Service) read() (*types.Config, error) {
	cfg := config.Defaults()
	if _, _, err := config.Read(s.opts.ConfigPath, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// etagOf identifies a document by its content.
//
// It is computed over the unredacted document deliberately. If it were computed
// after redaction, a client could read a redacted document, send it back with
// a matching If-Match, and blank the credential without the precondition
// noticing.
func etagOf(cfg *types.Config) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:]) + `"`, nil
}

func redactSecrets(cfg *types.Config) {
	for _, f := range config.Registry() {
		if !f.Secret {
			continue
		}
		if v, ok := f.Get(cfg).(string); ok && v != "" {
			_ = f.Set(cfg, redacted)
		}
	}
}

// rejectsRedactedSecret reports a secret sent back as the redaction placeholder.
func rejectsRedactedSecret(cfg *types.Config) (string, bool) {
	for _, f := range config.Registry() {
		if !f.Secret {
			continue
		}
		if v, ok := f.Get(cfg).(string); ok && v == redacted {
			return f.ConfigPath, true
		}
	}
	return "", false
}

// immutableChange reports the first immutable setting the request would alter.
func immutableChange(current, next *types.Config) (string, bool) {
	for _, f := range config.Registry() {
		if f.Mutability != config.MutabilityImmutable {
			continue
		}
		if !reflect.DeepEqual(f.Get(current), f.Get(next)) {
			return f.ConfigPath, true
		}
	}
	return "", false
}

// changedSettings lists the settings that differ between two documents, by
// config path, so a caller is told exactly what a write altered.
func changedSettings(current, next *types.Config) []string {
	var changed []string
	for _, f := range config.Registry() {
		if !reflect.DeepEqual(f.Get(current), f.Get(next)) {
			changed = append(changed, f.ConfigPath)
		}
	}
	return changed
}

func warningStrings(warnings []config.Warning) []string {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.String())
	}
	return out
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrorResponse{Error: "could not read the request body: " + err.Error()})
		return nil, false
	}
	return body, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, body ErrorResponse) {
	writeJSON(w, status, body)
}
