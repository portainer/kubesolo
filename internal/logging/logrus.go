package logging

import (
	"io"
	"slices"
	"strings"

	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
)

// logrusLibraries are the packages between a logrus call site and the hook.
var logrusLibraries = []string{
	"github.com/sirupsen/logrus",
	"github.com/containerd/log",
}

// logrusComponents attributes a logrus log line to the kubesolo component that
// owns the code it came from. containerd and kine share the global logrus logger;
// everything that is not kine is containerd or one of its plugins.
var logrusComponents = []struct {
	component string
	packages  []string
}{
	{"kine", []string{
		"github.com/k3s-io/kine",
		"github.com/nats-io",
	}},
}

// ConfigureLogrusLogging routes the global logrus logger, which containerd and
// kine log through, to the kubesolo logger.
//
// containerd replaces the logrus formatter when it starts, so the formatter is
// left alone: the hook writes each entry and the formatted output is discarded.
func ConfigureLogrusLogging() {
	logger := logrus.StandardLogger()
	logger.ReplaceHooks(logrus.LevelHooks{})
	logger.AddHook(logrusHook{})
	logger.SetOutput(io.Discard)
}

// logrusHook writes logrus entries through the kubesolo logger.
type logrusHook struct{}

func (logrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (logrusHook) Fire(entry *logrus.Entry) error {
	l := bridge.Load()
	e := l.WithLevel(logrusLevel(entry.Level))
	if e == nil {
		return nil
	}

	caller, component := externalCaller(logrusComponent, logrusLibraries...)
	if caller != "" {
		e = e.Str(zerolog.CallerFieldName, caller)
	}
	if component == "" {
		component = "containerd"
	}
	e = e.Str("component", component)

	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		if k != "component" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)

	for _, k := range keys {
		switch v := entry.Data[k].(type) {
		case error:
			e = e.Str(k, describe(v.Error))
		case string:
			e = e.Str(k, v)
		default:
			e = e.Interface(k, v)
		}
	}

	e.Msg(strings.TrimRight(entry.Message, "\n"))
	return nil
}

func logrusLevel(level logrus.Level) zerolog.Level {
	switch level {
	case logrus.PanicLevel:
		return zerolog.PanicLevel
	case logrus.FatalLevel:
		return zerolog.FatalLevel
	case logrus.ErrorLevel:
		return zerolog.ErrorLevel
	case logrus.WarnLevel:
		return zerolog.WarnLevel
	case logrus.InfoLevel:
		return zerolog.InfoLevel
	case logrus.DebugLevel:
		return zerolog.DebugLevel
	default:
		return zerolog.TraceLevel
	}
}

func logrusComponent(function string) string {
	for _, c := range logrusComponents {
		if isFrameOf(function, c.packages...) {
			return c.component
		}
	}
	return ""
}
