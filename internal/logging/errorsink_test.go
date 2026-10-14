package logging_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/portainer/kubesolo/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

// Error lines from KubeSolo and from the libraries it bridges reach the sink;
// everything still reaches the normal output, and nothing below error reaches
// the sink.
func TestErrorsAreCopiedToTheSink(t *testing.T) {
	logger := log.Logger
	t.Cleanup(func() { log.Logger = logger; logging.SetErrorSink(nil) })

	var sink, out bytes.Buffer
	logging.SetErrorSink(&sink)
	logging.SetOutput(&out)

	log.Info().Str("component", "kubesolo").Msg("all is well")
	log.Error().Str("component", "kine").Msg("database is locked")
	logging.SetBridgeOutput(io.Discard)

	assert.Contains(t, out.String(), "all is well")
	assert.Contains(t, out.String(), "database is locked")
	assert.Contains(t, sink.String(), `"message":"database is locked"`)
	assert.NotContains(t, sink.String(), "all is well")
}
