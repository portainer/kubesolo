package assistant

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// event is a log line as zerolog writes it in JSON, before console formatting.
func event(level, component, msg, err string) []byte {
	e := fmt.Sprintf(`{"level":%q,"component":%q,"time":1791947261000,"message":%q`, level, component, msg)
	if err != "" {
		e += fmt.Sprintf(`,"error":%q`, err)
	}
	return []byte(e + "}\n")
}

func TestErrorLogKeepsErrorLinesReadably(t *testing.T) {
	l := NewErrorLog(filepath.Join(t.TempDir(), "errors.log"))
	_, _ = l.Write(event("error", "kine", "failed to open database", "database disk image is malformed"))

	lines := l.Lines()
	if len(lines) != 1 {
		t.Fatalf("lines = %q", lines)
	}
	for _, want := range []string{"2026-10-14T03:07:41Z", "kine", "failed to open database", "database disk image is malformed"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("%q lacks %q", lines[0], want)
		}
	}
}

// The snapshot has a fixed budget in the model's prompt.
func TestErrorLogIsBounded(t *testing.T) {
	l := NewErrorLog(filepath.Join(t.TempDir(), "errors.log"))
	for i := range maxErrorLines + 10 {
		_, _ = l.Write(event("error", "apiserver", fmt.Sprintf("error %d %s", i, strings.Repeat("x", 1000)), ""))
	}

	lines := l.Lines()
	if len(lines) != maxErrorLines {
		t.Errorf("kept %d lines, want %d", len(lines), maxErrorLines)
	}
	if !strings.Contains(lines[len(lines)-1], fmt.Sprintf("error %d ", maxErrorLines+9)) {
		t.Errorf("the newest line should be last: %q", lines[len(lines)-1][:40])
	}
	for _, line := range lines {
		if len(line) > maxLineLength {
			t.Fatalf("line of %d characters, want at most %d", len(line), maxLineLength)
		}
	}
}

// After a crash, the next start reads what the previous run last saved.
func TestErrorLogSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "errors.log")
	l := NewErrorLog(path)
	_, _ = l.Write(event("error", "containerd", "shim died", ""))
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}

	previous := PreviousErrors(path)
	if len(previous) != 1 || !strings.Contains(previous[0], "shim died") {
		t.Errorf("previous run's errors = %q", previous)
	}
	if got := PreviousErrors(filepath.Join(t.TempDir(), "missing")); len(got) != 0 {
		t.Errorf("no previous run should mean no errors, got %q", got)
	}
}

func TestErrorLogTakesUnparseableLinesAsTheyAre(t *testing.T) {
	l := NewErrorLog(filepath.Join(t.TempDir(), "errors.log"))
	_, _ = l.Write([]byte("panic: something went badly wrong\n"))
	if lines := l.Lines(); len(lines) != 1 || lines[0] != "panic: something went badly wrong" {
		t.Errorf("lines = %q", lines)
	}
}

// A fatal error exits the process before the next flush, so it is saved at
// once: it is the line that explains the crash.
func TestFatalErrorsAreSavedAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "errors.log")
	l := NewErrorLog(path)
	_, _ = l.Write(event("fatal", "kubesolo", "failed to generate full certificates", "disk full"))

	if previous := PreviousErrors(path); len(previous) != 1 || !strings.Contains(previous[0], "disk full") {
		t.Errorf("saved = %q", previous)
	}
}
