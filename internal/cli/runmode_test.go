package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnsureArg(t *testing.T) {
	t.Run("appends when absent", func(t *testing.T) {
		got := ensureArg([]string{"--container-mode"}, "--full")
		assert.Equal(t, []string{"--container-mode", "--full"}, got)
	})

	t.Run("no-op when present as bare flag", func(t *testing.T) {
		in := []string{"--full", "--container-mode"}
		got := ensureArg(in, "--full")
		assert.Equal(t, in, got)
	})

	t.Run("no-op when present as flag=value", func(t *testing.T) {
		in := []string{"--full=true", "--container-mode"}
		got := ensureArg(in, "--full")
		assert.Equal(t, in, got)
	})

	t.Run("does not match on prefix collision", func(t *testing.T) {
		// "--fullscreen" must not satisfy a check for "--full".
		got := ensureArg([]string{"--fullscreen"}, "--full")
		assert.Equal(t, []string{"--fullscreen", "--full"}, got)
	})
}
