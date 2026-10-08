package logging

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/go-logr/logr"
	"github.com/rs/zerolog"
	logsapi "k8s.io/component-base/logs/api/v1"
	"k8s.io/klog/v2"
)

// K8sLogFormat is the Kubernetes log format that writes through the kubesolo
// logger. Every in-process Kubernetes component must be started with it: the
// first component to apply its logging configuration installs it, and the others
// are only allowed to apply the same configuration again.
const K8sLogFormat = "kubesolo"

// klogLibraries are the packages between a Kubernetes call site and the sink.
var klogLibraries = []string{
	"k8s.io/klog",
	"github.com/go-logr/logr",
	"k8s.io/component-base/logs",
}

// klogComponents attributes a Kubernetes log line to the kubesolo component that
// owns the code it came from, or that called into the shared code it came from.
// Lines with no component on the stack fall back to "kubernetes".
var klogComponents = []struct {
	component string
	packages  []string
}{
	{"kubelet", []string{
		"k8s.io/kubernetes/cmd/kubelet",
		"k8s.io/kubernetes/pkg/kubelet",
		"k8s.io/kubernetes/pkg/volume",
		"k8s.io/kubelet",
		"k8s.io/cri-client",
	}},
	{"kubeproxy", []string{
		"k8s.io/kubernetes/cmd/kube-proxy",
		"k8s.io/kubernetes/pkg/proxy",
		"k8s.io/kube-proxy",
	}},
	{"controller", []string{
		"k8s.io/kubernetes/cmd/kube-controller-manager",
		"k8s.io/kubernetes/pkg/controller",
		"k8s.io/controller-manager",
		"k8s.io/cloud-provider",
	}},
	{"apiserver", []string{
		"k8s.io/kubernetes/cmd/kube-apiserver",
		"k8s.io/kubernetes/pkg/controlplane",
		"k8s.io/kubernetes/pkg/registry",
		"k8s.io/kubernetes/plugin/pkg/admission",
		"k8s.io/apiserver",
		"k8s.io/apiextensions-apiserver",
		"k8s.io/kube-aggregator",
	}},
}

func init() {
	// Registration has to happen before any Kubernetes command builds its flags,
	// which freezes the registry, so it cannot wait for bootstrap.
	if err := logsapi.RegisterLogFormat(K8sLogFormat, klogFactory{}, logsapi.LoggingStableOptions); err != nil {
		panic(fmt.Sprintf("failed to register the %s log format: %v", K8sLogFormat, err))
	}
}

// configureKlog routes klog through the kubesolo logger until a Kubernetes
// component applies its logging configuration, which installs the same sink.
func configureKlog() {
	sink := &klogSink{verbosity: &atomic.Int32{}}
	klog.SetLoggerWithOptions(logr.New(sink), klog.WriteKlogBuffer(sink.WriteKlogBuffer))
}

// klogFactory builds the logger for the kubesolo Kubernetes log format.
type klogFactory struct{}

func (klogFactory) Create(c logsapi.LoggingConfiguration, _ logsapi.LoggingOptions) (logr.Logger, logsapi.RuntimeControl) {
	verbosity := &atomic.Int32{}
	verbosity.Store(int32(c.Verbosity))

	return logr.New(&klogSink{verbosity: verbosity}), logsapi.RuntimeControl{
		SetVerbosityLevel: func(v uint32) error {
			verbosity.Store(int32(v))
			return nil
		},
	}
}

// klogSink writes Kubernetes log lines through the kubesolo logger.
//
// Structured calls (InfoS, ErrorS and contextual logging) arrive through Info and
// Error. Formatted calls (Infof, Warningf, ...) arrive through WriteKlogBuffer
// with klog's header still attached, because that is the only way they keep their
// warning or fatal severity: klog otherwise passes a warning to Info.
type klogSink struct {
	verbosity *atomic.Int32
	name      string
	values    []any
}

var (
	_ logr.LogSink                         = (*klogSink)(nil)
	_ logr.CallDepthLogSink                = (*klogSink)(nil)
	_ interface{ WriteKlogBuffer([]byte) } = (*klogSink)(nil)
)

func (s *klogSink) Init(logr.RuntimeInfo) {}

// WithCallDepth returns the sink unchanged: the caller is found by walking the
// stack past the logging libraries rather than by counting frames.
func (s *klogSink) WithCallDepth(int) logr.LogSink { return s }

func (s *klogSink) Enabled(level int) bool {
	return level <= int(s.verbosity.Load())
}

func (s *klogSink) Info(level int, msg string, keysAndValues ...any) {
	// klog verbosity 0 is the normal operating output; anything above it is the
	// detail Kubernetes only shows when asked for, which is debug output here.
	zlevel := zerolog.InfoLevel
	if level > 0 {
		zlevel = zerolog.DebugLevel
	}
	s.write(zlevel, nil, msg, keysAndValues)
}

func (s *klogSink) Error(err error, msg string, keysAndValues ...any) {
	s.write(zerolog.ErrorLevel, err, msg, keysAndValues)
}

func (s *klogSink) WithValues(keysAndValues ...any) logr.LogSink {
	c := *s
	c.values = append(append([]any(nil), s.values...), keysAndValues...)
	return &c
}

func (s *klogSink) WithName(name string) logr.LogSink {
	c := *s
	if c.name == "" {
		c.name = name
	} else {
		c.name += "/" + name
	}
	return &c
}

func (s *klogSink) write(level zerolog.Level, err error, msg string, keysAndValues []any) {
	l := bridge.Load()
	e := l.WithLevel(level)
	if e == nil {
		return
	}

	caller, component := externalCaller(klogComponent, klogLibraries...)
	if caller != "" {
		e = e.Str(zerolog.CallerFieldName, caller)
	}
	if component == "" {
		component = "kubernetes"
	}

	e = e.Str("component", component)
	if s.name != "" {
		e = e.Str("logger", s.name)
	}
	e = appendKeysAndValues(e, s.values)
	e = appendKeysAndValues(e, keysAndValues)
	if err != nil {
		e = e.Err(err)
	}
	e.Msg(msg)
}

// klogHeader matches the header klog puts in front of a formatted log line:
// severity, date, time, thread id, then file:line.
var klogHeader = regexp.MustCompile(`^([IWEF])\d{4} \d{2}:\d{2}:\d{2}\.\d{6}\s+\d+ [^\]]*\] `)

// WriteKlogBuffer writes a formatted klog line, whose header carries its severity.
func (s *klogSink) WriteKlogBuffer(data []byte) {
	level := zerolog.InfoLevel
	if m := klogHeader.FindSubmatch(data); m != nil {
		switch m[1][0] {
		case 'W':
			level = zerolog.WarnLevel
		case 'E':
			level = zerolog.ErrorLevel
		case 'F':
			level = zerolog.FatalLevel
		}
		data = data[len(m[0]):]
	}

	s.write(level, nil, string(bytes.TrimRight(data, "\n")), nil)
}

func klogComponent(function string) string {
	for _, c := range klogComponents {
		if isFrameOf(function, c.packages...) {
			return c.component
		}
	}
	return ""
}

// appendKeysAndValues adds logr key/value pairs to the event. Values that know
// how to describe themselves are written as text, so a Kubernetes object
// reference reads as namespace/name rather than as a JSON object.
func appendKeysAndValues(e *zerolog.Event, keysAndValues []any) *zerolog.Event {
	for i := 0; i < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			key = fmt.Sprint(keysAndValues[i])
		}
		if i+1 == len(keysAndValues) {
			e = e.Str(key, "(MISSING)")
			break
		}

		switch v := keysAndValues[i+1].(type) {
		case string:
			e = e.Str(key, v)
		case []byte:
			e = e.Str(key, strings.TrimSpace(string(v)))
		case error:
			e = e.Str(key, describe(v.Error))
		case fmt.Stringer:
			e = e.Str(key, describe(v.String))
		case logr.Marshaler:
			e = e.Interface(key, v.MarshalLog())
		default:
			e = e.Interface(key, v)
		}
	}
	return e
}

// describe calls a String or Error method, which Kubernetes types do not all
// guard against a nil receiver, without letting a panic take down the process.
func describe(fn func() string) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = fmt.Sprintf("<panic: %v>", r)
		}
	}()
	return fn()
}
