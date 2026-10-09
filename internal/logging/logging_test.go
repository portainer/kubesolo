package logging_test

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/logging"
	"github.com/rs/zerolog"
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

// The console time keeps the sub-second precision the Kubernetes and containerd
// formats had before their lines were bridged: the milliseconds of the event's
// own time, not zeros appended to a whole second.
func TestConsoleTimeHasMilliseconds(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stderr, logger := os.Stderr, log.Logger
	timeFormat, timestamp := zerolog.TimeFieldFormat, zerolog.TimestampFunc
	os.Stderr = w
	zerolog.TimestampFunc = func() time.Time { return time.Date(2026, 10, 8, 23, 42, 13, 789_000_000, time.Local) }
	t.Cleanup(func() {
		os.Stderr, log.Logger = stderr, logger
		zerolog.TimeFieldFormat, zerolog.TimestampFunc = timeFormat, timestamp
	})

	logging.ConfigureLogger()
	logging.SetLoggingMode("NOCOLOR")
	log.Info().Msg("timed")
	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Regexp(t, `^2026/10/08 23:42:13\.789 INF .*timed`, string(out))
}

// SetOutput sends the console format to another writer, such as syslog for the
// upgrade executor where there is no journal. Nothing there reads colour codes.
func TestSetOutputWritesTheConsoleFormatWithoutColour(t *testing.T) {
	logger := log.Logger
	t.Cleanup(func() { log.Logger = logger })

	var buf bytes.Buffer
	logging.SetOutput(&buf)
	log.Info().Str("component", "upgrade").Msg("preflight")

	assert.Regexp(t, `^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{3} INF .*preflight \| component=upgrade`, buf.String())
	assert.NotContains(t, buf.String(), "\x1b[")
}
