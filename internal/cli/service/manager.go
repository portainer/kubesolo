// Package service provides an init-system-agnostic interface for installing,
// enabling, and controlling the KubeSolo system service.
//
// Each init system (systemd, OpenRC, SysV, s6, runit, upstart) is implemented
// as a separate type that satisfies the Manager interface. The Daemon and
// Foreground runners implement the non-service run modes.
package service

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
)

// Manager is the interface every service backend must implement.
type Manager interface {
	// Install writes service files and configures the service to start on boot.
	Install(cfg *config.Config, cmdArgs []string) error
	// Uninstall removes service files and disables the service.
	Uninstall() error
}

// New returns the appropriate Manager for the detected init system and run mode.
// name is the instance identifier used by the container manager as the Docker
// container name; it is ignored for non-container run modes.
func New(info *detect.SystemInfo, runMode, name string) (Manager, error) {
	if name == "" {
		name = config.AppName
	}
	switch runMode {
	case config.RunModeDaemon:
		return &daemonManager{}, nil
	case config.RunModeForeground:
		return &foregroundManager{}, nil
	case config.RunModeContainer:
		return &containerManager{name: name}, nil
	case config.RunModeService, "":
		return newServiceManager(info.InitSystem)
	default:
		return nil, fmt.Errorf("unknown run mode %q (valid: service, daemon, foreground, container)", runMode)
	}
}

// newServiceManager returns the Manager implementation for the given init system.
func newServiceManager(init detect.InitSystem) (Manager, error) {
	switch init {
	case detect.InitSystemd:
		return &systemdManager{}, nil
	case detect.InitOpenRC:
		return &openrcManager{}, nil
	case detect.InitSysV:
		return &sysvinitManager{}, nil
	case detect.InitS6:
		return &s6Manager{}, nil
	case detect.InitRunit:
		return &runitManager{}, nil
	case detect.InitUpstart:
		return &upstartManager{}, nil
	default:
		// Unknown init system — fall back to daemon mode so the binary at least starts
		return &daemonManager{}, nil
	}
}

// ── template helpers ──────────────────────────────────────────────────────────

// templateData is passed to every service file template.
type templateData struct {
	AppName     string
	InstallPath string
	CmdArgs     string   // space-joined sanitised argument list (for double-quoted shell variables)
	CmdArgsList []string // sanitised arguments as individual strings (for per-arg shell quoting)
	Proxy       string   // optional HTTP/HTTPS proxy URL (sanitised, no newlines)
}

// buildTemplateData constructs templateData from cfg and cmdArgs.
// It strips CR/LF from all string fields so that no user-supplied value
// can break the line-oriented format of the generated service files.
func buildTemplateData(cfg *config.Config, cmdArgs []string) templateData {
	// Strip newlines from proxy — they would inject extra lines into the file.
	proxy := strings.ReplaceAll(cfg.Proxy, "\r", "")
	proxy = strings.ReplaceAll(proxy, "\n", "")

	// Strip newlines from each argument for the same reason.
	sanitised := make([]string, len(cmdArgs))
	for i, a := range cmdArgs {
		a = strings.ReplaceAll(a, "\r", "")
		sanitised[i] = strings.ReplaceAll(a, "\n", "")
	}

	return templateData{
		AppName:     config.AppName,
		InstallPath: config.DefaultInstallPath,
		CmdArgs:     strings.Join(sanitised, " "),
		CmdArgsList: sanitised,
		Proxy:       proxy,
	}
}

// renderTemplate parses and executes a text/template, returning the result.
// The FuncMap exposes shellQuote, shellDoubleQuoteVal, and systemdEnvVal so
// that templates can safely embed user-controlled strings.
func renderTemplate(name, tmpl string, data templateData) (string, error) {
	funcs := template.FuncMap{
		"shellQuote":          shellQuote,
		"shellDoubleQuoteVal": shellDoubleQuoteVal,
		"systemdEnvVal":       systemdEnvVal,
	}
	t, err := template.New(name).Funcs(funcs).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s template: %w", name, err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render %s template: %w", name, err)
	}
	return buf.String(), nil
}

// ── escaping helpers ──────────────────────────────────────────────────────────

// shellQuote wraps s in POSIX single quotes, using the '"'"' idiom to embed
// any literal single-quote characters. Newlines are stripped because they
// would break the line-based format of shell scripts and service unit files.
// Use this for proxy values in shell export statements and for individual
// args in exec positions.
func shellQuote(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// shellDoubleQuoteVal escapes a value so it is safe to embed inside a
// double-quoted shell string ("..."). It escapes \, `, $, and " and strips
// newlines. Use this for shell variable assignments like DAEMON_ARGS="...".
func shellDoubleQuoteVal(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "$", `\$`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// systemdEnvVal escapes a value for safe embedding inside a systemd
// Environment= directive that is already wrapped in double quotes.
// It escapes \, ", and % (systemd unit-file specifier prefix) and strips
// newlines.
func systemdEnvVal(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `%`, `%%`)
	return s
}

// FilePath returns the service definition an init system was installed with, or
// empty if the init system is not one KubeSolo writes a file for.
//
// It exists so that an upgrade can read back the command line a previous install
// wrote, and convert it into a configuration file.
func FilePath(init detect.InitSystem) string {
	switch init {
	case detect.InitSystemd:
		return systemdServicePath
	case detect.InitOpenRC:
		return openrcServicePath
	case detect.InitSysV:
		return sysvinitServicePath
	case detect.InitUpstart:
		return upstartConfPath
	case detect.InitRunit:
		return filepath.Join(runitServiceDir, "run")
	case detect.InitS6:
		return filepath.Join(s6ServiceDir, "run")
	default:
		return ""
	}
}
