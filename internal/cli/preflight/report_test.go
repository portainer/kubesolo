package preflight

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// stubChecks pass, warn, fail, and pass again after the failure, recording
// which ran in ran.
func stubChecks(ran *[]string) []Check {
	stub := func(name string, err error) Check {
		return Check{Name: name, Run: func() error { *ran = append(*ran, name); return err }}
	}
	return []Check{
		stub("passes", nil),
		stub("warns", Warning("could be better")),
		stub("fails", errors.New("broken")),
		stub("passes after a failure", nil),
	}
}

func TestReport_RunsEveryCheck(t *testing.T) {
	var ran []string
	report := Report(stubChecks(&ran))

	want := []types.PreflightCheck{
		{Name: "passes", Result: types.PreflightPass},
		{Name: "warns", Result: types.PreflightWarn, Message: "could be better"},
		{Name: "fails", Result: types.PreflightFail, Message: "broken"},
		{Name: "passes after a failure", Result: types.PreflightPass},
	}
	if len(ran) != len(want) {
		t.Fatalf("ran %v, want every check", ran)
	}
	if len(report.Checks) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(report.Checks), len(want), report.Checks)
	}
	for i, w := range want {
		if report.Checks[i] != w {
			t.Errorf("check %d: got %+v, want %+v", i, report.Checks[i], w)
		}
	}
	if !report.Failed() {
		t.Error("a report with a failed check must be Failed")
	}
}

func TestReport_NoChecksEncodesEmptyList(t *testing.T) {
	raw, err := json.Marshal(Report(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"checks":[]}` {
		t.Errorf("got %s, want an empty list rather than null", raw)
	}
}

// A port held by another process, such as a KubeSolo that is already running,
// fails the port check with the port named, and the checks after it still run.
func TestReport_PortInUse(t *testing.T) {
	// If the port is already taken the check fails just the same.
	if ln, err := net.Listen("tcp", ":10443"); err == nil {
		t.Cleanup(func() { _ = ln.Close() })
	}

	var ran []string
	checks := append([]Check{{Name: "required ports available", Run: func() error { return CheckPorts(false) }}},
		stubChecks(&ran)[0])
	report := Report(checks)

	if got := report.Checks[0]; got.Result != types.PreflightFail || !strings.Contains(got.Message, "port 10443 (Webhook server, kubernetes.webhook.port)") {
		t.Errorf("got %+v, want the port check to fail naming port 10443", got)
	}
	if len(ran) != 1 || report.Checks[1].Result != types.PreflightPass {
		t.Errorf("the check after the port check did not run: %+v", report.Checks)
	}
}

// With AppArmor enabled and apparmor_parser missing, the AppArmor check warns
// rather than passing or failing. Most hosts without AppArmor skip this; CI's
// Ubuntu runners have it enabled.
func TestCheckAppArmor_MissingParserWarns(t *testing.T) {
	if !appArmorEnabled() {
		t.Skip("AppArmor is not enabled on this host")
	}
	t.Setenv("PATH", t.TempDir())

	err := CheckAppArmor(false)
	var w Warning
	if !errors.As(err, &w) {
		t.Fatalf("got %v, want a Warning", err)
	}
	if !strings.Contains(w.Error(), "apparmor_parser is missing") {
		t.Errorf("warning %q does not say what is missing", w)
	}
}

// TestReport_Golden pins the JSON Portainer decodes from `kubesoloctl check
// --json`, so a change to its shape shows up here first.
func TestReport_Golden(t *testing.T) {
	var ran []string
	raw, err := json.MarshalIndent(Report(stubChecks(&ran)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')

	path := filepath.Join("testdata", "report.json")
	if *update {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if string(raw) != string(want) {
		t.Errorf("report JSON changed\n--- got ---\n%s\n--- want ---\n%s", raw, want)
	}
}

// RunSuite logs a warning and carries on, as CheckAppArmor's own log line did
// before it returned a Warning, so install and plain check print the same.
func TestRunSuite_WarningContinues(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	var ran []string
	checks := stubChecks(&ran)
	err := RunSuite(checks)
	if err == nil || err.Error() != "fails: broken" {
		t.Fatalf("got %v, want the failing check's error", err)
	}
	if len(ran) != 3 {
		t.Errorf("ran %v, want RunSuite to stop at the failure", ran)
	}

	type line struct{ Level, Message string }
	var got []line
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var l line
		if err := dec.Decode(&l); err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	want := []line{{"info", "passes"}, {"warn", "could be better"}, {"info", "warns"}}
	if len(got) != len(want) {
		t.Fatalf("logged %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
