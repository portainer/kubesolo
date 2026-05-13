package service

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

const sysvinitServicePath = "/etc/init.d/kubesolo"

const sysvinitTemplate = `#!/bin/sh
### BEGIN INIT INFO
# Provides:          {{.AppName}}
# Required-Start:    $network $local_fs
# Required-Stop:     $network $local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.AppName}} service
# Description:       KubeSolo single-node Kubernetes distribution
### END INIT INFO
{{- if .Proxy}}
export HTTP_PROXY={{.Proxy | shellQuote}}
export HTTPS_PROXY={{.Proxy | shellQuote}}
export NO_PROXY='localhost,127.0.0.1'
{{- end}}

DAEMON="{{.InstallPath}}"
DAEMON_ARGS="{{.CmdArgs | shellDoubleQuoteVal}}"
PIDFILE="/var/run/{{.AppName}}.pid"
USER="root"

. /lib/lsb/init-functions

case "$1" in
    start)
        log_daemon_msg "Starting {{.AppName}}"
        start-stop-daemon --start --quiet --pidfile $PIDFILE --make-pidfile --background --chuid $USER --exec $DAEMON -- $DAEMON_ARGS
        log_end_msg $?
        ;;
    stop)
        log_daemon_msg "Stopping {{.AppName}}"
        start-stop-daemon --stop --quiet --pidfile $PIDFILE
        RETVAL=$?
        [ $RETVAL -eq 0 ] && rm -f $PIDFILE
        log_end_msg $RETVAL
        ;;
    restart)
        $0 stop
        $0 start
        ;;
    status)
        status_of_proc -p $PIDFILE $DAEMON "{{.AppName}}" && exit 0 || exit $?
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
`

type sysvinitManager struct{}

func (m *sysvinitManager) Install(cfg *config.Config, cmdArgs []string) error {
	data := buildTemplateData(cfg, cmdArgs)
	script, err := renderTemplate("sysvinit", sysvinitTemplate, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(sysvinitServicePath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("failed to write SysV init script: %w", err)
	}
	log.Info().Msgf("wrote SysV init script: %s", sysvinitServicePath)

	// Enable: try update-rc.d (Debian/Ubuntu) then chkconfig (RHEL/CentOS)
	if _, err := exec.LookPath("update-rc.d"); err == nil {
		if err := runSysVCmd("update-rc.d", config.AppName, "defaults"); err != nil {
			return fmt.Errorf("update-rc.d defaults: %w", err)
		}
	} else if _, err := exec.LookPath("chkconfig"); err == nil {
		if err := runSysVCmd("chkconfig", "--add", config.AppName); err != nil {
			return fmt.Errorf("chkconfig --add: %w", err)
		}
		if err := runSysVCmd("chkconfig", config.AppName, "on"); err != nil {
			return fmt.Errorf("chkconfig on: %w", err)
		}
	}

	// Start: prefer the service command, fall back to direct invocation
	if _, err := exec.LookPath("service"); err == nil {
		if err := runSysVCmd("service", config.AppName, "start"); err != nil {
			return fmt.Errorf("service start: %w", err)
		}
	} else {
		if err := runSysVCmd(sysvinitServicePath, "start"); err != nil {
			return fmt.Errorf("init script start: %w", err)
		}
	}
	log.Info().Msg("KubeSolo service installed and started via SysV init")
	return nil
}

func (m *sysvinitManager) Uninstall() error {
	if _, err := exec.LookPath("service"); err == nil {
		_ = runSysVCmd("service", config.AppName, "stop")
	} else {
		_ = runSysVCmd(sysvinitServicePath, "stop")
	}
	if _, err := exec.LookPath("update-rc.d"); err == nil {
		_ = runSysVCmd("update-rc.d", "-f", config.AppName, "remove")
	} else if _, err := exec.LookPath("chkconfig"); err == nil {
		_ = runSysVCmd("chkconfig", "--del", config.AppName)
	}
	_ = os.Remove(sysvinitServicePath)
	log.Info().Msg("KubeSolo SysV init service removed")
	return nil
}

func runSysVCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
}
