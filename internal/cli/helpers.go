package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// configureLogging sets up zerolog for the given verbosity level.
//
// In normal (non-debug) mode the ConsoleWriter is configured to produce
// indented detail lines that nest cleanly under the ui.Printer's step output:
//
//	INFO  →  "     message"          (5-space indent, dim-gray in colour mode)
//	WARN  →  "  ⚠  message"         (yellow symbol at column 3)
//	ERROR →  "  ✗  message"         (red symbol at column 3)
//
// In debug mode the classic timestamped ConsoleWriter format is used.
func configureLogging(debug bool) {
	color := ui.ColorEnabled()

	if debug {
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stderr,
			NoColor:    !color,
			TimeFormat: "2006/01/02 03:04PM",
		})
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		return
	}

	// currentLevel is set by FormatPrepare before FormatMessage is called so
	// that INFO/DEBUG messages can be dimmed without touching WARN/ERROR text.
	var currentLevel string

	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:          os.Stderr,
		NoColor:      true, // we apply colour ourselves in the format functions
		PartsExclude: []string{zerolog.TimestampFieldName},

		FormatPrepare: func(m map[string]interface{}) error {
			currentLevel = ""
			if l, ok := m[zerolog.LevelFieldName].(string); ok {
				currentLevel = l
			}
			return nil
		},

		FormatLevel: func(i interface{}) string {
			level := ""
			if l, ok := i.(string); ok {
				level = l
			}
			switch level {
			case "warn":
				if color {
					return "  \033[33m⚠\033[0m  "
				}
				return "  !  "
			case "error":
				if color {
					return "  \033[31m✗\033[0m  "
				}
				return "  x  "
			default: // info, debug, trace — plain indent, no symbol
				return "     "
			}
		},

		FormatMessage: func(i interface{}) string {
			if i == nil {
				return ""
			}
			isDetail := currentLevel != "warn" && currentLevel != "error"
			if color && isDetail {
				return fmt.Sprintf("\033[90m%v\033[0m", i)
			}
			return fmt.Sprintf("%v", i)
		},

		FormatFieldName: func(i interface{}) string {
			if color {
				return fmt.Sprintf(" \033[90m%v=\033[0m", i)
			}
			return fmt.Sprintf(" %v=", i)
		},

		FormatFieldValue: func(i interface{}) string {
			if color {
				return fmt.Sprintf("\033[90m%v\033[0m", i)
			}
			return fmt.Sprintf("%v", i)
		},

		FormatErrFieldName: func(i interface{}) string {
			if color {
				return " \033[90merror=\033[0m"
			}
			return " error="
		},

		FormatErrFieldValue: func(i interface{}) string {
			if color {
				return fmt.Sprintf("\033[31m%v\033[0m", i)
			}
			return fmt.Sprintf("%v", i)
		},
	})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	switch os.Getenv(key) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}

// initControlBinary returns the path to the init system's service control
// binary so process.StopViaInitSystem can invoke a graceful shutdown.
func initControlBinary(init detect.InitSystem) string {
	var candidates []string
	switch init {
	case detect.InitSystemd:
		candidates = []string{"systemctl", "/usr/bin/systemctl", "/bin/systemctl"}
	case detect.InitOpenRC:
		candidates = []string{"rc-service", "/sbin/rc-service", "/usr/sbin/rc-service"}
	case detect.InitSysV:
		candidates = []string{"service", "/usr/sbin/service", "/sbin/service"}
	case detect.InitUpstart:
		candidates = []string{"initctl", "/sbin/initctl", "/usr/sbin/initctl"}
	default:
		return ""
	}
	if p, err := exec.LookPath(candidates[0]); err == nil {
		return p
	}
	for _, p := range candidates[1:] {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// restoreSELinux restores SELinux file contexts for path if restorecon exists.
func restoreSELinux(path string) {
	for _, dir := range []string{"/usr/sbin", "/sbin"} {
		rc := dir + "/restorecon"
		if _, err := os.Stat(rc); err == nil {
			log.Info().Msg("restoring SELinux file context for installed binary...")
			if err := runCmd(rc, "-v", path); err != nil {
				log.Warn().Err(err).Msg("restorecon returned an error (may be normal in permissive mode)")
			}
			return
		}
	}
}

// runCmd executes a command, forwarding stdout/stderr.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runServiceAction runs a service management action (start, stop, restart,
// etc.) via the detected init system.
func runServiceAction(init detect.InitSystem, action string) error {
	if action == "logs" {
		return runServiceLogs(init)
	}

	switch init {
	case detect.InitSystemd:
		return runCmd("systemctl", action, config.AppName)
	case detect.InitOpenRC:
		return runOpenRCAction(action)
	case detect.InitSysV:
		return runSysVAction(action)
	case detect.InitUpstart:
		return runUpstartAction(action)
	default:
		return fmt.Errorf(
			"service management not supported for init system %q — use direct process signals instead",
			init,
		)
	}
}

func runServiceLogs(init detect.InitSystem) error {
	switch init {
	case detect.InitSystemd:
		return runCmd("journalctl", "-u", config.AppName, "-f")
	case detect.InitOpenRC:
		return runCmd("tail", "-f", "/var/log/messages")
	case detect.InitSysV:
		return runCmd("tail", "-f", "/var/log/syslog")
	case detect.InitUpstart:
		return runCmd("tail", "-f", "/var/log/upstart/"+config.AppName+".log")
	default:
		return runCmd("tail", "-f", config.LogFile)
	}
}

func runOpenRCAction(action string) error {
	switch action {
	case "enable":
		return runCmd("rc-update", "add", config.AppName, "default")
	case "disable":
		return runCmd("rc-update", "del", config.AppName, "default")
	default:
		return runCmd("rc-service", config.AppName, action)
	}
}

func runSysVAction(action string) error {
	switch action {
	case "enable":
		if err := runCmd("update-rc.d", config.AppName, "defaults"); err != nil {
			if err2 := runCmd("chkconfig", "--add", config.AppName); err2 != nil {
				return fmt.Errorf("enable: neither update-rc.d nor chkconfig found")
			}
			return runCmd("chkconfig", config.AppName, "on")
		}
		return nil
	case "disable":
		if err := runCmd("update-rc.d", "-f", config.AppName, "remove"); err != nil {
			if err2 := runCmd("chkconfig", "--del", config.AppName); err2 != nil {
				return fmt.Errorf("disable: neither update-rc.d nor chkconfig found")
			}
		}
		return nil
	case "start", "stop", "restart", "status":
		return runCmd("service", config.AppName, action)
	default:
		return fmt.Errorf("action %q is not supported for SysV init", action)
	}
}

func runUpstartAction(action string) error {
	switch action {
	case "enable", "disable":
		return fmt.Errorf(
			"action %q is not supported for Upstart — add or remove /etc/init/%s.conf to enable/disable the service",
			action, config.AppName,
		)
	case "start", "stop", "restart", "status":
		return runCmd("initctl", action, config.AppName)
	default:
		return fmt.Errorf("action %q is not supported for Upstart", action)
	}
}
