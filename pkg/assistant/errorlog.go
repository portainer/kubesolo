package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// maxErrorLines and maxLineLength keep the errors within the snapshot's
	// share of the model's prompt: at most about 9 KB.
	maxErrorLines = 30
	maxLineLength = 300
)

// ErrorLog keeps KubeSolo's latest error log lines, for snapshots. It is an
// io.Writer for zerolog's JSON events. Flush saves the lines to a file, so
// that the start after a crash can still see what the crashed run logged.
type ErrorLog struct {
	path string

	mu    sync.Mutex
	lines []string
	dirty bool
}

// NewErrorLog returns an empty log that flushes to path.
func NewErrorLog(path string) *ErrorLog {
	return &ErrorLog{path: path}
}

// Write records one log event. A fatal one is saved at once, since the
// process exits before the next flush.
func (l *ErrorLog) Write(p []byte) (int, error) {
	line, fatal := formatEvent(p)
	if len(line) > maxLineLength {
		line = line[:maxLineLength]
	}

	l.mu.Lock()
	l.lines = append(l.lines, line)
	if len(l.lines) > maxErrorLines {
		l.lines = l.lines[len(l.lines)-maxErrorLines:]
	}
	l.dirty = true
	l.mu.Unlock()

	if fatal {
		_ = l.Flush()
	}
	return len(p), nil
}

// Lines returns the recorded lines, oldest first.
func (l *ErrorLog) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// Flush saves the lines if any arrived since the last flush.
func (l *ErrorLog) Flush() error {
	l.mu.Lock()
	if !l.dirty {
		l.mu.Unlock()
		return nil
	}
	data := strings.Join(l.lines, "\n") + "\n"
	l.dirty = false
	l.mu.Unlock()

	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// PreviousErrors reads the lines the previous run last flushed to path.
func PreviousErrors(path string) []string {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// formatEvent turns a zerolog JSON event into one readable line, and reports
// whether it ends the process.
func formatEvent(p []byte) (string, bool) {
	var e struct {
		Level     string `json:"level"`
		Time      int64  `json:"time"`
		Component string `json:"component"`
		Message   string `json:"message"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(p, &e); err != nil || e.Message == "" {
		return string(bytes.TrimSpace(p)), false
	}
	line := time.UnixMilli(e.Time).UTC().Format(time.RFC3339) + " " + e.Component + ": " + e.Message
	if e.Error != "" {
		line += ": " + e.Error
	}
	return line, e.Level == "fatal" || e.Level == "panic"
}
