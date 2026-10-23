package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreflightReportRoundTrip(t *testing.T) {
	want := PreflightReport{Checks: []PreflightCheck{
		{Name: "root privileges", Result: PreflightPass},
		{Name: "AppArmor parser", Result: PreflightWarn, Message: "apparmor_parser is missing"},
		{Name: "required ports available", Result: PreflightFail, Message: "port 6443 is in use"},
	}}

	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got PreflightReport
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}

func TestPreflightReportFailed(t *testing.T) {
	assert.False(t, PreflightReport{}.Failed())
	assert.False(t, PreflightReport{Checks: []PreflightCheck{
		{Name: "a", Result: PreflightPass},
		{Name: "b", Result: PreflightWarn},
	}}.Failed(), "a warning is not a failure")
	assert.True(t, PreflightReport{Checks: []PreflightCheck{
		{Name: "a", Result: PreflightPass},
		{Name: "b", Result: PreflightFail},
	}}.Failed())
}
