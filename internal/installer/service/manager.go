// Package service provides an init-system-agnostic interface for installing,
// enabling, and controlling the KubeSolo system service.
//
// Each init system (systemd, OpenRC, SysV, s6, runit, upstart) is implemented
// as a separate type that satisfies the Manager interface. The Daemon and
// Foreground runners implement the non-service run modes.
package service

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/portainer/kubesolo/internal/installer/detect"
)

// Manager is the interface every service backend must implement.
type Manager interface {
	// Install writes service files and configures the service to start on boot.
	Install(cfg *config.Config, cmdArgs []string) error
	// Uninstall removes service files and disables the service.
	Uninstall() error
}

// New returns the appropriate Manager for the detected init system and run mode.
// For run mode "service" the init system is used; "daemon" and "foreground"
// always return their respective runners regardless of init system.
func New(info *detect.SystemInfo, runMode string) (Manager, error) {
	switch runMode {
	case config.RunModeDaemon:
		return &daemonManager{}, nil
	case config.RunModeForeground:
		return &foregroundManager{}, nil
	case config.RunModeService, "":
		return newServiceManager(info.InitSystem)
	default:
		return nil, fmt.Errorf("unknown run mode %q (valid: service, daemon, foreground)", runMode)
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
	CmdArgs     string // space-joined argument list for service files
	Proxy       string // optional HTTP/HTTPS proxy URL
}

func buildTemplateData(cfg *config.Config, cmdArgs []string) templateData {
	return templateData{
		AppName:     config.AppName,
		InstallPath: config.DefaultInstallPath,
		CmdArgs:     strings.Join(cmdArgs, " "),
		Proxy:       cfg.Proxy,
	}
}

// renderTemplate parses and executes a text/template, returning the result.
func renderTemplate(name, tmpl string, data templateData) (string, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s template: %w", name, err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render %s template: %w", name, err)
	}
	return buf.String(), nil
}
