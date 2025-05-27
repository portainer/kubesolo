#!/bin/sh

set -e

APP_NAME="kubesolo"
PIDFILE="/var/run/$APP_NAME.pid"
LOGFILE="/var/log/$APP_NAME.log"

# Function to detect init system
detect_init_system() {
    if command -v systemctl >/dev/null 2>&1 && [ -d /etc/systemd/system ]; then
        echo "systemd"
    elif [ -f /sbin/init ] && /sbin/init --version 2>/dev/null | grep -q upstart; then
        echo "upstart"
    elif [ -d /etc/init.d ]; then
        echo "sysvinit"
    elif [ -d /etc/s6 ] || command -v s6-svc >/dev/null 2>&1; then
        echo "s6"
    elif command -v runit >/dev/null 2>&1 || [ -d /etc/runit ]; then
        echo "runit"
    elif command -v openrc >/dev/null 2>&1 || [ -f /sbin/openrc ]; then
        echo "openrc"
    else
        echo "unknown"
    fi
}

INIT_SYSTEM=$(detect_init_system)

# Function to start service
start_service() {
    case "$INIT_SYSTEM" in
        "systemd")
            systemctl start "$APP_NAME"
            ;;
        "sysvinit")
            service "$APP_NAME" start
            ;;
        "openrc")
            rc-service "$APP_NAME" start
            ;;
        "s6")
            if [ -d "/etc/s6/sv/$APP_NAME" ]; then
                s6-svc -u "/etc/s6/sv/$APP_NAME"
            else
                echo "❌ s6 service not found"
                exit 1
            fi
            ;;
        "runit")
            if [ -L "/var/service/$APP_NAME" ] || [ -L "/etc/runit/runsvdir/default/$APP_NAME" ]; then
                echo "✅ runit service should start automatically"
            else
                echo "❌ runit service not found"
                exit 1
            fi
            ;;
        "upstart")
            initctl start "$APP_NAME"
            ;;
        *)
            echo "❌ Unknown init system or daemon mode"
            echo "💡 Try: nohup kubesolo [args] > $LOGFILE 2>&1 &"
            exit 1
            ;;
    esac
    echo "✅ $APP_NAME service started"
}

# Function to stop service
stop_service() {
    case "$INIT_SYSTEM" in
        "systemd")
            systemctl stop "$APP_NAME"
            ;;
        "sysvinit")
            service "$APP_NAME" stop
            ;;
        "openrc")
            rc-service "$APP_NAME" stop
            ;;
        "s6")
            if [ -d "/etc/s6/sv/$APP_NAME" ]; then
                s6-svc -d "/etc/s6/sv/$APP_NAME"
            else
                echo "❌ s6 service not found"
                exit 1
            fi
            ;;
        "runit")
            if [ -L "/var/service/$APP_NAME" ]; then
                sv stop "$APP_NAME"
            elif [ -L "/etc/runit/runsvdir/default/$APP_NAME" ]; then
                sv stop "$APP_NAME"
            else
                echo "❌ runit service not found"
                exit 1
            fi
            ;;
        "upstart")
            initctl stop "$APP_NAME"
            ;;
        *)
            # Try to stop daemon mode
            if [ -f "$PIDFILE" ]; then
                PID=$(cat "$PIDFILE")
                if kill -0 "$PID" 2>/dev/null; then
                    kill "$PID"
                    rm -f "$PIDFILE"
                    echo "✅ $APP_NAME daemon stopped"
                else
                    echo "⚠️  PID file exists but process not running"
                    rm -f "$PIDFILE"
                fi
            else
                echo "❌ No PID file found. Try: pkill kubesolo"
                exit 1
            fi
            ;;
    esac
    echo "✅ $APP_NAME service stopped"
}

# Function to restart service
restart_service() {
    echo "🔄 Restarting $APP_NAME service..."
    stop_service
    sleep 2
    start_service
}

# Function to check service status
status_service() {
    case "$INIT_SYSTEM" in
        "systemd")
            systemctl status "$APP_NAME"
            ;;
        "sysvinit")
            service "$APP_NAME" status
            ;;
        "openrc")
            rc-service "$APP_NAME" status
            ;;
        "s6")
            if [ -d "/etc/s6/sv/$APP_NAME" ]; then
                s6-svstat "/etc/s6/sv/$APP_NAME"
            else
                echo "❌ s6 service not found"
                exit 1
            fi
            ;;
        "runit")
            if [ -L "/var/service/$APP_NAME" ]; then
                sv status "$APP_NAME"
            elif [ -L "/etc/runit/runsvdir/default/$APP_NAME" ]; then
                sv status "$APP_NAME"
            else
                echo "❌ runit service not found"
                exit 1
            fi
            ;;
        "upstart")
            initctl status "$APP_NAME"
            ;;
        *)
            # Check daemon mode
            if [ -f "$PIDFILE" ]; then
                PID=$(cat "$PIDFILE")
                if kill -0 "$PID" 2>/dev/null; then
                    echo "✅ $APP_NAME daemon is running (PID: $PID)"
                else
                    echo "❌ $APP_NAME daemon is not running (stale PID file)"
                    exit 1
                fi
            else
                if pgrep kubesolo >/dev/null; then
                    echo "⚠️  $APP_NAME process found but no PID file"
                    pgrep -l kubesolo
                else
                    echo "❌ $APP_NAME is not running"
                    exit 1
                fi
            fi
            ;;
    esac
}

# Function to show logs
show_logs() {
    case "$INIT_SYSTEM" in
        "systemd")
            journalctl -u "$APP_NAME" -f
            ;;
        "sysvinit")
            tail -f /var/log/syslog | grep "$APP_NAME"
            ;;
        "openrc")
            tail -f /var/log/messages | grep "$APP_NAME"
            ;;
        *)
            if [ -f "$LOGFILE" ]; then
                tail -f "$LOGFILE"
            else
                echo "❌ Log file not found: $LOGFILE"
                exit 1
            fi
            ;;
    esac
}

# Function to enable service
enable_service() {
    case "$INIT_SYSTEM" in
        "systemd")
            systemctl enable "$APP_NAME"
            ;;
        "sysvinit")
            if command -v update-rc.d >/dev/null 2>&1; then
                update-rc.d "$APP_NAME" defaults
            elif command -v chkconfig >/dev/null 2>&1; then
                chkconfig "$APP_NAME" on
            fi
            ;;
        "openrc")
            rc-update add "$APP_NAME" default
            ;;
        *)
            echo "⚠️  Auto-enable not supported for $INIT_SYSTEM"
            ;;
    esac
    echo "✅ $APP_NAME service enabled"
}

# Function to disable service
disable_service() {
    case "$INIT_SYSTEM" in
        "systemd")
            systemctl disable "$APP_NAME"
            ;;
        "sysvinit")
            if command -v update-rc.d >/dev/null 2>&1; then
                update-rc.d "$APP_NAME" remove
            elif command -v chkconfig >/dev/null 2>&1; then
                chkconfig "$APP_NAME" off
            fi
            ;;
        "openrc")
            rc-update del "$APP_NAME" default
            ;;
        *)
            echo "⚠️  Auto-disable not supported for $INIT_SYSTEM"
            ;;
    esac
    echo "✅ $APP_NAME service disabled"
}

# Main script logic
case "$1" in
    start)
        start_service
        ;;
    stop)
        stop_service
        ;;
    restart)
        restart_service
        ;;
    status)
        status_service
        ;;
    logs)
        show_logs
        ;;
    enable)
        enable_service
        ;;
    disable)
        disable_service
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status|logs|enable|disable}"
        echo ""
        echo "Commands:"
        echo "  start    - Start the $APP_NAME service"
        echo "  stop     - Stop the $APP_NAME service"
        echo "  restart  - Restart the $APP_NAME service"
        echo "  status   - Show service status"
        echo "  logs     - Show service logs (follow mode)"
        echo "  enable   - Enable service to start at boot"
        echo "  disable  - Disable service from starting at boot"
        echo ""
        echo "Detected init system: $INIT_SYSTEM"
        exit 1
        ;;
esac

exit 0 