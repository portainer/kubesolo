package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func configureLogging(debug bool) {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "2006/01/02 03:04PM",
	})
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
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

// printServiceHints logs management commands appropriate for the detected init system.
func printServiceHints(init detect.InitSystem) {
	app := config.AppName
	switch init {
	case detect.InitSystemd:
		log.Info().Msgf("status: systemctl status %s", app)
		log.Info().Msgf("logs:   journalctl -u %s -f", app)
	case detect.InitOpenRC:
		log.Info().Msgf("status: rc-service %s status", app)
		log.Info().Msgf("logs:   tail -f /var/log/messages")
	case detect.InitSysV:
		log.Info().Msgf("status: service %s status", app)
		log.Info().Msgf("logs:   tail -f /var/log/syslog")
	default:
		log.Info().Msgf("logs:   tail -f %s", config.LogFile)
	}
}
