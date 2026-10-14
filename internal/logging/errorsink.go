package logging

import (
	"io"
	"sync/atomic"

	"github.com/rs/zerolog"
)

// errorSink, when set, receives a copy of every error-level event, as JSON,
// from KubeSolo and from the libraries it bridges. The assistant keeps them
// for its snapshots.
var errorSink atomic.Pointer[io.Writer]

// SetErrorSink sends a copy of every error-level event to w. Nil stops it.
func SetErrorSink(w io.Writer) {
	if w == nil {
		errorSink.Store(nil)
		return
	}
	errorSink.Store(&w)
}

// teeErrors passes every event to out, and error-level ones to the sink too.
type teeErrors struct {
	out io.Writer
}

func (t teeErrors) Write(p []byte) (int, error) {
	return t.out.Write(p)
}

func (t teeErrors) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	if sink := errorSink.Load(); sink != nil && level >= zerolog.ErrorLevel && level < zerolog.NoLevel {
		_, _ = (*sink).Write(p)
	}
	return t.out.Write(p)
}
