package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	containerdlog "github.com/containerd/log"
	"github.com/portainer/kubesolo/internal/logging"
	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	logsapi "k8s.io/component-base/logs/api/v1"
	"k8s.io/klog/v2"
)

func TestMain(m *testing.M) {
	// Apply the format the way a Kubernetes component does, so the tests run
	// against the logger the format factory builds.
	cfg := logsapi.NewLoggingConfiguration()
	cfg.Format = logging.K8sLogFormat
	if err := logsapi.ValidateAndApply(cfg, nil); err != nil {
		panic(err)
	}
	logging.ConfigureLogrusLogging()

	os.Exit(m.Run())
}

// capture points the bridge at a buffer for the duration of a test and returns a
// function that decodes the lines written to it. The bridge is global, so tests
// using it cannot run in parallel.
func capture(t *testing.T, level zerolog.Level) func() []map[string]any {
	t.Helper()

	buf := &bytes.Buffer{}
	previousLevel := zerolog.GlobalLevel()
	logging.SetBridgeOutput(buf)
	zerolog.SetGlobalLevel(level)
	t.Cleanup(func() {
		logging.SetBridgeOutput(os.Stderr)
		zerolog.SetGlobalLevel(previousLevel)
	})

	return func() []map[string]any {
		klog.Flush()
		var lines []map[string]any
		for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
			if len(raw) == 0 {
				continue
			}
			line := map[string]any{}
			require.NoError(t, json.Unmarshal(raw, &line), string(raw))
			lines = append(lines, line)
		}
		return lines
	}
}

func assertCallerIsThisFile(t *testing.T, line map[string]any) {
	t.Helper()
	caller, _ := line[zerolog.CallerFieldName].(string)
	assert.Equal(t, "bridge_test.go", filepath.Base(strings.Split(caller, ":")[0]), "caller %q", caller)
}

func TestKlogStructured(t *testing.T) {
	read := capture(t, zerolog.InfoLevel)

	klog.InfoS("pod started", "pod", klog.KRef("default", "nginx"), "attempt", 2)
	klog.ErrorS(errors.New("boom"), "pod failed", "pod", klog.KRef("default", "nginx"))
	klog.V(2).InfoS("too detailed to show")

	lines := read()
	require.Len(t, lines, 2)

	assert.Equal(t, "info", lines[0]["level"])
	assert.Equal(t, "pod started", lines[0]["message"])
	assert.Equal(t, "kubernetes", lines[0]["component"])
	assert.Equal(t, "default/nginx", lines[0]["pod"])
	assert.EqualValues(t, 2, lines[0]["attempt"])
	assertCallerIsThisFile(t, lines[0])

	assert.Equal(t, "error", lines[1]["level"])
	assert.Equal(t, "boom", lines[1]["error"])
	assertCallerIsThisFile(t, lines[1])
}

func TestKlogFormattedKeepsSeverity(t *testing.T) {
	read := capture(t, zerolog.InfoLevel)

	klog.Infof("listening on %s", ":6443")
	klog.Warningf("disk %d%% full", 91)
	klog.Errorf("failed: %v", "timeout")

	lines := read()
	require.Len(t, lines, 3)

	assert.Equal(t, "info", lines[0]["level"])
	assert.Equal(t, "listening on :6443", lines[0]["message"])
	assert.Equal(t, "warn", lines[1]["level"])
	assert.Equal(t, "disk 91% full", lines[1]["message"])
	assert.Equal(t, "error", lines[2]["level"])
	assert.Equal(t, "failed: timeout", lines[2]["message"])
	for _, line := range lines {
		assertCallerIsThisFile(t, line)
	}
}

func TestKlogContextualLogger(t *testing.T) {
	read := capture(t, zerolog.InfoLevel)

	logger := klog.Background().WithName("garbagecollector").WithValues("resource", "pods")
	logger.Info("synced")

	lines := read()
	require.Len(t, lines, 1)
	assert.Equal(t, "garbagecollector", lines[0]["logger"])
	assert.Equal(t, "pods", lines[0]["resource"])
	assertCallerIsThisFile(t, lines[0])
}

func TestKlogFollowsGlobalLevel(t *testing.T) {
	read := capture(t, zerolog.WarnLevel)

	klog.InfoS("hidden")
	klog.Warning("shown")

	lines := read()
	require.Len(t, lines, 1)
	assert.Equal(t, "shown", lines[0]["message"])
}

func TestLogrus(t *testing.T) {
	read := capture(t, zerolog.DebugLevel)

	logrus.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() { logrus.SetLevel(logrus.InfoLevel) })

	containerdlog.G(context.Background()).
		WithField("namespace", "k8s.io").
		WithError(errors.New("no such image")).
		Warn("pull failed")
	logrus.Debug("plugin loaded")

	lines := read()
	require.Len(t, lines, 2)

	assert.Equal(t, "warn", lines[0]["level"])
	assert.Equal(t, "pull failed", lines[0]["message"])
	assert.Equal(t, "containerd", lines[0]["component"])
	assert.Equal(t, "k8s.io", lines[0]["namespace"])
	assert.Equal(t, "no such image", lines[0]["error"])
	assertCallerIsThisFile(t, lines[0])

	assert.Equal(t, "debug", lines[1]["level"])
	assertCallerIsThisFile(t, lines[1])
}

func TestKlogComponent(t *testing.T) {
	tests := map[string]string{
		"k8s.io/kubernetes/pkg/kubelet/kuberuntime.(*kubeGenericRuntimeManager).SyncPod": "kubelet",
		"k8s.io/kubernetes/pkg/proxy/iptables.(*Proxier).syncProxyRules":                 "kubeproxy",
		"k8s.io/kubernetes/pkg/controller/garbagecollector.(*GarbageCollector).Sync":     "controller",
		"k8s.io/apiserver/pkg/server.(*GenericAPIServer).Run":                            "apiserver",
		"k8s.io/client-go/tools/cache.(*Reflector).ListAndWatch":                         "",
		// A package that only shares a prefix with a mapped one is not mapped.
		"k8s.io/kubernetes/pkg/kubeletx.Run": "",
	}
	for function, want := range tests {
		assert.Equal(t, want, logging.KlogComponent(function), function)
	}
}

func TestLogrusComponent(t *testing.T) {
	assert.Equal(t, "kine", logging.LogrusComponent("github.com/k3s-io/kine/pkg/logstructured/sqllog.(*SQLLog).compactor"))
	assert.Equal(t, "", logging.LogrusComponent("github.com/containerd/containerd/v2/cmd/containerd/server.New"))
}

// zerolog's Fatal() and Panic() exit and panic, but WithLevel does not. The
// bridges rely on that so klog and logrus keep their own fatal and panic
// handling; these tests fail if a zerolog upgrade changes it.

func TestLogrusPanicKeepsLogrusControlFlow(t *testing.T) {
	read := capture(t, zerolog.InfoLevel)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		logrus.Panic("bad state")
	}()

	_, isEntry := recovered.(*logrus.Entry)
	assert.True(t, isEntry, "logrus, not zerolog, should raise the panic, got %T", recovered)

	lines := read()
	require.Len(t, lines, 1)
	assert.Equal(t, "panic", lines[0]["level"])
	assert.Equal(t, "bad state", lines[0]["message"])
}

func TestLogrusFatalLeavesExitToLogrus(t *testing.T) {
	read := capture(t, zerolog.InfoLevel)

	exited := 0
	logger := logrus.StandardLogger()
	previousExit := logger.ExitFunc
	logger.ExitFunc = func(int) { exited++ }
	t.Cleanup(func() { logger.ExitFunc = previousExit })

	// Called through a value: linters treat logrus.Fatal as never returning,
	// which it does here with ExitFunc stubbed out.
	fatal := logger.Fatal
	fatal("cannot continue")

	assert.Equal(t, 1, exited, "logrus's ExitFunc should run")
	lines := read()
	require.Len(t, lines, 1)
	assert.Equal(t, "fatal", lines[0]["level"])
}

func TestKlogFatalBufferDoesNotExit(t *testing.T) {
	read := capture(t, zerolog.InfoLevel)

	// The buffer klog hands over for klog.Fatal, before klog exits on its own.
	sink := klog.Background().GetSink().(interface{ WriteKlogBuffer([]byte) })
	sink.WriteKlogBuffer([]byte("F1008 21:29:01.123456    1234 server.go:42] cannot bind\n"))

	lines := read()
	require.Len(t, lines, 1)
	assert.Equal(t, "fatal", lines[0]["level"])
	assert.Equal(t, "cannot bind", lines[0]["message"])
}
