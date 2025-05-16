package logging

import (
	"fmt"
	stdlog "log"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"

	logsapi "k8s.io/component-base/logs/api/v1"
)

// ConfigureLogger configures the default logger for kubesolo
// it is configured to use the zerolog library
// it sets the error stack field name, error stack marshaler, time field format, and the output
func ConfigureLogger() {
	zerolog.ErrorStackFieldName = "stack_trace"
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

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

// SetLoggingMode sets the logging mode for the zerolog library
// it switches on the logging mode
func SetLoggingMode(mode string) {
	switch mode {
	case "PRETTY":
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:           os.Stderr,
			TimeFormat:    "2006/01/02 03:04PM",
			FormatMessage: formatMessage,
		})
	case "NOCOLOR":
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:           os.Stderr,
			TimeFormat:    "2006/01/02 03:04PM",
			FormatMessage: formatMessage,
			NoColor:       true,
		})
	case "JSON":
		log.Logger = log.Output(os.Stderr)
	}
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
// it sets the reapply handling to ignore unchanged
func ConfigureK8sDefaultLogging() {
	logsapi.ReapplyHandling = logsapi.ReapplyHandlingIgnoreUnchanged
}
