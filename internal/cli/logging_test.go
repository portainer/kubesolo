package cli

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The debug writer prints the time to the millisecond, so the event time it is
// given has to carry milliseconds too, not only whole seconds.
func TestDebugLoggingTimeHasMilliseconds(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stderr, logger, level := os.Stderr, log.Logger, zerolog.GlobalLevel()
	timeFormat, timestamp := zerolog.TimeFieldFormat, zerolog.TimestampFunc
	os.Stderr = w
	zerolog.TimestampFunc = func() time.Time { return time.Date(2026, 10, 8, 23, 42, 13, 789_000_000, time.Local) }
	t.Cleanup(func() {
		os.Stderr, log.Logger = stderr, logger
		zerolog.SetGlobalLevel(level)
		zerolog.TimeFieldFormat, zerolog.TimestampFunc = timeFormat, timestamp
	})

	configureLogging(true)
	log.Debug().Msg("timed")
	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Regexp(t, `2026/10/08 23:42:13\.789 DBG timed`, string(out))
}
