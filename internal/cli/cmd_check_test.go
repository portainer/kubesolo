package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/types"
)

func TestWriteCheckReport(t *testing.T) {
	cases := []struct {
		name    string
		result  types.PreflightResult
		wantErr error
	}{
		{name: "pass exits zero", result: types.PreflightPass},
		{name: "warn exits zero", result: types.PreflightWarn},
		{name: "fail exits non-zero", result: types.PreflightFail, wantErr: ui.ErrAlreadyReported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := types.PreflightReport{Checks: []types.PreflightCheck{
				{Name: "a", Result: types.PreflightPass},
				{Name: "b", Result: tc.result, Message: "m"},
			}}
			var out bytes.Buffer
			if err := writeCheckReport(&out, report); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}

			var got types.PreflightReport
			dec := json.NewDecoder(&out)
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("output is not a report: %v", err)
			}
			if dec.More() {
				t.Error("output holds more than one JSON document")
			}
			if len(got.Checks) != 2 || got.Checks[1] != report.Checks[1] {
				t.Errorf("got %+v, want %+v", got, report)
			}
		})
	}
}

// Check messages are multi-line help texts, quoted hostnames and package
// commands; Portainer must read back exactly what the check said.
func TestWriteCheckReport_MessagesSurviveEncoding(t *testing.T) {
	messages := []string{
		"pods cannot reach the API server at 10.0.0.5:6443: i/o timeout\n\n  firewalld:  firewall-cmd --reload\n\tiptables -I INPUT -s 10.42.0.0/16 -j ACCEPT",
		`hostname "Edge_Gateway" is not RFC 1123 compliant`,
		"no supported package manager was detected — install them with your distribution's package manager",
		`add "ip saddr 10.42.0.0/16 accept" & reload <nftables>`,
	}
	report := types.PreflightReport{}
	for _, m := range messages {
		report.Checks = append(report.Checks, types.PreflightCheck{Name: "c", Result: types.PreflightFail, Message: m})
	}

	var out bytes.Buffer
	_ = writeCheckReport(&out, report)
	var got types.PreflightReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not a report: %v\n%s", err, out.String())
	}
	for i, m := range messages {
		if got.Checks[i].Message != m {
			t.Errorf("message %d: got %q, want %q", i, got.Checks[i].Message, m)
		}
	}
}
