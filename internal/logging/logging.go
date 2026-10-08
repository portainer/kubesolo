package logging

import (
	"fmt"
	"io"
	stdlog "log"
	"os"
	"sync/atomic"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"

	logsapi "k8s.io/component-base/logs/api/v1"
)

// bridge is the logger that log lines from Kubernetes and containerd are written
// through. It shares the output of the default logger but carries no caller hook:
// the caller of a bridged line is somewhere inside the library that logged it, so
// the bridge looks it up and sets the field itself.
var bridge atomic.Pointer[zerolog.Logger]

func init() {
	setBridgeOutput(os.Stderr)
}

// ConfigureLogger configures the default logger for kubesolo
// it is configured to use the zerolog library
// it sets the error stack field name, error stack marshaler, time field format, and the output
func ConfigureLogger() {
	zerolog.ErrorStackFieldName = "stack_trace"
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
	// Milliseconds, so the console time below has them to show.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.CallerMarshalFunc = callerMarshal

	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

	log.Logger = log.Logger.With().Caller().Logger()
}

// SetLoggingLevel sets the logging level for the zerolog library
// it switches on the logging level
func SetLoggingLevel(level string) {
	switch level {
	case "ERROR":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "WARN":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "INFO":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "DEBUG":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
}

// consoleTimeFormat is the time in console output: 24-hour, to the millisecond,
// the precision the Kubernetes and containerd formats had before their lines
// were written through this logger.
const consoleTimeFormat = "2006/01/02 15:04:05.000"

// SetLoggingMode sets the logging mode for the zerolog library
// it switches on the logging mode
func SetLoggingMode(mode string) {
	var out io.Writer
	switch mode {
	case "PRETTY":
		out = zerolog.ConsoleWriter{
			Out:           os.Stderr,
			TimeFormat:    consoleTimeFormat,
			FormatMessage: formatMessage,
			NoColor:       !isTerminal(os.Stderr),
		}
	case "NOCOLOR":
		out = zerolog.ConsoleWriter{
			Out:           os.Stderr,
			TimeFormat:    consoleTimeFormat,
			FormatMessage: formatMessage,
			NoColor:       true,
		}
	case "JSON":
		out = os.Stderr
	default:
		return
	}

	log.Logger = log.Output(out)
	setBridgeOutput(out)
}

// isTerminal reports whether f is a terminal. Under systemd, in a container or
// behind a pipe it is not, and colour codes would only reach the journal or the
// log file as escape sequences.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func setBridgeOutput(out io.Writer) {
	l := zerolog.New(out).With().Timestamp().Logger()
	bridge.Store(&l)
}

// FormatMessage formats the message for the zerolog library
// it returns the message as a string
func formatMessage(i any) string {
	if i == nil {
		return ""
	}

	return fmt.Sprintf("%s |", i)
}

// ConfigureK8sDefaultLogging configures the default logging for kubernetes
// it sets the reapply handling to ignore unchanged, and routes klog through the
// kubesolo logger so that output written before the first Kubernetes component
// applies its logging configuration is formatted the same way
func ConfigureK8sDefaultLogging() {
	logsapi.ReapplyHandling = logsapi.ReapplyHandlingIgnoreUnchanged
	configureKlog()
}
