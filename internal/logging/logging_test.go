package logging_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Under systemd, in a container or behind a pipe, stderr is not a terminal and
// colour codes would end up in the journal as binary data.
func TestPrettyHasNoColourWhenStderrIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stderr, logger := os.Stderr, log.Logger
	os.Stderr = w
	t.Cleanup(func() { os.Stderr, log.Logger = stderr, logger })

	logging.SetLoggingMode("PRETTY")
	log.Warn().Str("component", "kubesolo").Msg("not a terminal")
	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Contains(t, string(out), "not a terminal")
	assert.False(t, strings.Contains(string(out), "\x1b["), "colour codes in %q", out)
}
