#!/bin/sh

set -e

# Function to handle errors
handle_error() {
    echo "❌ Error: $1"
    exit 1
}

# Check if running as root
check_root() {
    # Check if EUID is 0 (root) or if id -u returns 0
    if [ "$(id -u)" -ne 0 ]; then
        echo "❌ This script must be run as root (use sudo)"
        echo ""
        echo "Please run: sudo $0 $*"
        exit 1
    fi
}

# Function to detect init system
detect_init_system() {
    if command -v systemctl >/dev/null 2>&1 && [ -d /etc/systemd/system ]; then
        echo "systemd"
    elif [ -f /sbin/init ] && /sbin/init --version 2>/dev/null | grep -q upstart; then
        echo "upstart"
    elif command -v openrc >/dev/null 2>&1 || [ -f /sbin/openrc ]; then
        echo "openrc"
    elif [ -d /etc/s6 ] || command -v s6-svc >/dev/null 2>&1; then
        echo "s6"
    elif command -v runit >/dev/null 2>&1 || [ -d /etc/runit ]; then
        echo "runit"
    elif [ -d /etc/init.d ]; then
        echo "sysvinit"
    else
        echo "unknown"
    fi
}

# Default configuration
APP_NAME="kubesolo"
INSTALL_PATH="/usr/local/bin/$APP_NAME"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"

# Parse command line arguments
REMOVE_DATA=false
REMOVE_KUBECONFIG=false

for arg in "$@"; do
    case $arg in
        --path=*)
            CONFIG_PATH="${arg#*=}"
            ;;
        --remove-data)
            REMOVE_DATA=true
            ;;
        --remove-kubeconfig)
            REMOVE_KUBECONFIG=true
            ;;
        --help)
            echo "Usage: $0 [options]"
            echo "Options:"
            echo "  --path=PATH              Set configuration path (default: $CONFIG_PATH)"
            echo "  --remove-data            Remove configuration and data directory"
            echo "  --remove-kubeconfig      Remove kubeconfig from ~/.kube/config"
            echo "  --help                   Show this help message"
            exit 0
            ;;
    esac
done

# Detect init system
INIT_SYSTEM=$(detect_init_system)
echo "🔍 Detected init system: $INIT_SYSTEM"

# Function to stop and disable systemd service
stop_systemd_service() {
    if systemctl is-active --quiet "$APP_NAME" 2>/dev/null; then
        echo "🛑 Stopping $APP_NAME service..."
        systemctl stop "$APP_NAME" || echo "⚠️  Failed to stop service (may already be stopped)"
    fi
    
    if systemctl is-enabled --quiet "$APP_NAME" 2>/dev/null; then
        echo "🔌 Disabling $APP_NAME service..."
        systemctl disable "$APP_NAME" || echo "⚠️  Failed to disable service"
    fi
    
    if [ -f "/etc/systemd/system/$APP_NAME.service" ]; then
        echo "🗑️  Removing systemd service file..."
        rm -f "/etc/systemd/system/$APP_NAME.service"
        systemctl daemon-reload || echo "⚠️  Failed to reload systemd daemon"
    fi
}

# Function to stop and disable SysV init service
stop_sysvinit_service() {
    if [ -f "/etc/init.d/$APP_NAME" ]; then
        echo "🛑 Stopping $APP_NAME service..."
        if command -v service >/dev/null 2>&1; then
            service "$APP_NAME" stop 2>/dev/null || echo "⚠️  Service may already be stopped"
        else
            "/etc/init.d/$APP_NAME" stop 2>/dev/null || echo "⚠️  Service may already be stopped"
        fi
        
        echo "🔌 Disabling $APP_NAME service..."
        if command -v update-rc.d >/dev/null 2>&1; then
            update-rc.d -f "$APP_NAME" remove 2>/dev/null || echo "⚠️  Failed to remove service"
        elif command -v chkconfig >/dev/null 2>&1; then
            chkconfig --del "$APP_NAME" 2>/dev/null || echo "⚠️  Failed to remove service"
        fi
        
        echo "🗑️  Removing SysV init script..."
        rm -f "/etc/init.d/$APP_NAME"
    fi
}

# Function to stop and disable OpenRC service
stop_openrc_service() {
    if [ -f "/etc/init.d/$APP_NAME" ]; then
        echo "🛑 Stopping $APP_NAME service..."
        rc-service "$APP_NAME" stop 2>/dev/null || echo "⚠️  Service may already be stopped"
        
        echo "🔌 Disabling $APP_NAME service..."
        rc-update del "$APP_NAME" default 2>/dev/null || echo "⚠️  Failed to remove service"
        
        echo "🗑️  Removing OpenRC service script..."
        rm -f "/etc/init.d/$APP_NAME"
    fi
}

# Function to stop and disable s6 service
stop_s6_service() {
    S6_SERVICE_DIR="/etc/s6/sv/$APP_NAME"
    if [ -d "$S6_SERVICE_DIR" ]; then
        echo "🛑 Stopping $APP_NAME service..."
        if command -v s6-svc >/dev/null 2>&1; then
            s6-svc -d "$S6_SERVICE_DIR" 2>/dev/null || echo "⚠️  Service may already be stopped"
        fi
        
        # Remove symlink if it exists
        if [ -L "/etc/s6/adminsv/default/$APP_NAME" ]; then
            rm -f "/etc/s6/adminsv/default/$APP_NAME"
        fi
        
        echo "🗑️  Removing s6 service directory..."
        rm -rf "$S6_SERVICE_DIR"
    fi
}

# Function to stop and disable runit service
stop_runit_service() {
    RUNIT_SERVICE_DIR="/etc/runit/sv/$APP_NAME"
    
    # Stop service if symlink exists
    if [ -L "/var/service/$APP_NAME" ]; then
        echo "🛑 Stopping $APP_NAME service..."
        sv stop "$APP_NAME" 2>/dev/null || echo "⚠️  Service may already be stopped"
        rm -f "/var/service/$APP_NAME"
    elif [ -L "/etc/runit/runsvdir/default/$APP_NAME" ]; then
        echo "🛑 Stopping $APP_NAME service..."
        sv stop "$APP_NAME" 2>/dev/null || echo "⚠️  Service may already be stopped"
        rm -f "/etc/runit/runsvdir/default/$APP_NAME"
    fi
    
    if [ -d "$RUNIT_SERVICE_DIR" ]; then
        echo "🗑️  Removing runit service directory..."
        rm -rf "$RUNIT_SERVICE_DIR"
    fi
}

# Function to stop and disable upstart service
stop_upstart_service() {
    if [ -f "/etc/init/$APP_NAME.conf" ]; then
        echo "🛑 Stopping $APP_NAME service..."
        initctl stop "$APP_NAME" 2>/dev/null || echo "⚠️  Service may already be stopped"
        
        echo "🗑️  Removing upstart service file..."
        rm -f "/etc/init/$APP_NAME.conf"
        initctl reload-configuration 2>/dev/null || echo "⚠️  Failed to reload upstart configuration"
    fi
}

# Function to stop daemon mode
stop_daemon() {
    PIDFILE="/var/run/$APP_NAME.pid"
    if [ -f "$PIDFILE" ]; then
        PID=$(cat "$PIDFILE" 2>/dev/null || echo "")
        if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
            echo "🛑 Stopping $APP_NAME daemon (PID: $PID)..."
            kill "$PID" 2>/dev/null || echo "⚠️  Failed to stop daemon"
            sleep 1
        fi
        rm -f "$PIDFILE"
    fi
    
    # Try to kill any remaining processes
    if pgrep -x "$APP_NAME" >/dev/null 2>&1; then
        echo "🛑 Stopping remaining $APP_NAME processes..."
        pkill -x "$APP_NAME" 2>/dev/null || echo "⚠️  Failed to stop processes"
    fi
}

# Function to kill processes holding KubeSolo ports
kill_port_processes() {
    echo "🔍 Checking for processes holding KubeSolo ports..."
    
    # KubeSolo ports: 2379 (Kine), 6443 (API Server), 10443 (Webhook), 6060 (pprof)
    local ports="2379 6443 10443 6060"
    local found_processes=false
    
    for port in $ports; do
        # Find processes using the port (works with both lsof and ss/netstat)
        local pids=""
        
        # Try lsof first (more reliable)
        if command -v lsof >/dev/null 2>&1; then
            pids=$(lsof -ti ":$port" 2>/dev/null || true)
        # Fallback to ss
        elif command -v ss >/dev/null 2>&1; then
            # ss format: "LISTEN 0 128 *:2379 *:* users:(("kubesolo",pid=12345,fd=3))"
            pids=$(ss -lptn "sport = :$port" 2>/dev/null | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u || true)
        # Fallback to netstat
        elif command -v netstat >/dev/null 2>&1; then
            pids=$(netstat -tlnp 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | grep -E '^[0-9]+$' | sort -u || true)
        fi
        
        if [ -n "$pids" ]; then
            found_processes=true
            for pid in $pids; do
                # Check if it's actually a kubesolo-related process
                local cmdline=""
                local procname=""
                local is_kubesolo=false
                
                if [ -f "/proc/$pid/cmdline" ]; then
                    cmdline=$(cat "/proc/$pid/cmdline" 2>/dev/null | tr '\0' ' ' || echo "")
                fi
                
                if [ -f "/proc/$pid/comm" ]; then
                    procname=$(cat "/proc/$pid/comm" 2>/dev/null || echo "")
                fi
                
                # Check if it's a kubesolo process
                if echo "$cmdline" | grep -q "kubesolo" || echo "$procname" | grep -qi "kubesolo"; then
                    is_kubesolo=true
                fi
                
                # For KubeSolo-specific ports, be more aggressive if we can't determine the process
                # Port 2379 is Kine (KubeSolo-specific), so if something is holding it, it's likely leftover
                if [ "$is_kubesolo" = "true" ] || ([ "$port" = "2379" ] && [ -z "$cmdline" ]); then
                    echo "🛑 Killing process $pid holding port $port"
                    kill -TERM "$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
                else
                    echo "⚠️  Process $pid is holding port $port but doesn't appear to be KubeSolo-related (skipping)"
                fi
            done
        fi
    done
    
    if [ "$found_processes" = "true" ]; then
        echo "⏳ Waiting for ports to be released..."
        sleep 2
    else
        echo "ℹ️  No processes found holding KubeSolo ports"
    fi
}

# Function to kill all remaining kubesolo processes
kill_all_kubesolo_processes() {
    echo "🔍 Checking for any remaining KubeSolo processes..."
    
    # Find all processes with kubesolo in the command line
    local pids
    pids=$(pgrep -f "kubesolo" 2>/dev/null || true)
    
    if [ -z "$pids" ]; then
        echo "ℹ️  No remaining KubeSolo processes found"
        return 0
    fi
    
    echo "🛑 Killing remaining KubeSolo processes..."
    for pid in $pids; do
        if kill -0 "$pid" 2>/dev/null; then
            echo "   Killing PID $pid"
            kill -TERM "$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
        fi
    done
    
    # Wait a bit for processes to terminate
    sleep 2
    
    # Force kill any that are still running
    pids=$(pgrep -f "kubesolo" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "🛑 Force killing remaining processes..."
        for pid in $pids; do
            kill -KILL "$pid" 2>/dev/null || true
        done
        sleep 1
    fi
}

# Function to unmount container runtime mounts
unmount_runtime_mounts() {
    local config_path="$1"
    
    if [ ! -d "$config_path" ]; then
        return 0
    fi
    
    echo "🔍 Checking for container runtime mounts under $config_path..."
    
    # Find all mounts under the config path and unmount them in reverse order
    # This handles nested mounts correctly (e.g., overlay mounts)
    local mounts
    mounts=$(mount | awk -v path="$config_path" '$3 ~ "^" path {print $3}' | sort -r 2>/dev/null || true)
    
    if [ -z "$mounts" ]; then
        echo "ℹ️  No mounts found under $config_path"
        return 0
    fi
    
    echo "🔓 Unmounting container runtime mounts..."
    local unmount_failed=false
    while IFS= read -r mount_point; do
        if [ -n "$mount_point" ]; then
            echo "   Unmounting: $mount_point"
            # Use lazy unmount (-l) to handle busy mounts
            if ! umount -l "$mount_point" 2>/dev/null; then
                echo "⚠️  Failed to unmount $mount_point (may already be unmounted)"
                unmount_failed=true
            fi
        fi
    done <<EOF
$mounts
EOF
    
    if [ "$unmount_failed" = "true" ]; then
        echo "⚠️  Some mounts could not be unmounted. They may be busy or already unmounted."
    else
        echo "✅ All mounts unmounted successfully"
    fi
}

# Function to remove kubeconfig
remove_kubeconfig() {
    # Detect real user when running under sudo
    REAL_USER="$USER"
    REAL_HOME="$HOME"
    
    if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ]; then
        REAL_USER="$SUDO_USER"
        REAL_HOME=$(getent passwd "$SUDO_USER" | cut -d: -f6)
        if [ -z "$REAL_HOME" ]; then
            REAL_HOME="/home/$SUDO_USER"
        fi
    fi
    
    KUBECONFIG_FILE="$REAL_HOME/.kube/config"
    
    if [ -f "$KUBECONFIG_FILE" ]; then
        echo "🔍 Checking for KubeSolo context in kubeconfig..."
        
        # Check if kubectl is available to manipulate kubeconfig
        if command -v kubectl >/dev/null 2>&1; then
            # Try to remove kubesolo context
            if kubectl config get-contexts 2>/dev/null | grep -q "kubesolo"; then
                echo "🗑️  Removing KubeSolo context from kubeconfig..."
                kubectl config delete-context kubesolo 2>/dev/null || true
                kubectl config unset clusters.kubesolo 2>/dev/null || true
                kubectl config unset users.kubesolo-admin 2>/dev/null || true
            fi
            
            # Check if there's a backup and restore it
            LATEST_BACKUP=$(ls -t "$REAL_HOME/.kube/config.backup-"* 2>/dev/null | head -n1)
            if [ -n "$LATEST_BACKUP" ] && [ -f "$LATEST_BACKUP" ]; then
                echo "📋 Found backup kubeconfig: $LATEST_BACKUP"
                printf "Restore backup kubeconfig? [y/N] "
                read -r REPLY
                if echo "$REPLY" | grep -qi "^y"; then
                    cp "$LATEST_BACKUP" "$KUBECONFIG_FILE"
                    echo "✅ Restored backup kubeconfig"
                fi
            fi
        else
            echo "⚠️  kubectl not found, cannot automatically remove KubeSolo context"
            echo "💡 You may need to manually edit $KUBECONFIG_FILE to remove KubeSolo entries"
        fi
    fi
}

# Check for root privileges
check_root "$@"

# Main uninstall process
echo "🗑️  Uninstalling $APP_NAME..."

# Stop and remove service based on init system
case "$INIT_SYSTEM" in
    "systemd")
        stop_systemd_service
        ;;
    "sysvinit")
        stop_sysvinit_service
        ;;
    "openrc")
        stop_openrc_service
        ;;
    "s6")
        stop_s6_service
        ;;
    "runit")
        stop_runit_service
        ;;
    "upstart")
        stop_upstart_service
        ;;
    *)
        echo "⚠️  Unknown init system, trying to stop daemon mode..."
        stop_daemon
        ;;
esac

# Wait a bit for graceful shutdown
echo "⏳ Waiting for graceful shutdown..."
sleep 2

# Kill processes holding KubeSolo ports (ensures ports are released)
kill_port_processes

# Kill any remaining KubeSolo processes
kill_all_kubesolo_processes

# Remove binary
if [ -f "$INSTALL_PATH" ]; then
    echo "🗑️  Removing binary from $INSTALL_PATH..."
    rm -f "$INSTALL_PATH"
else
    echo "ℹ️  Binary not found at $INSTALL_PATH (may have been removed already)"
fi

# Remove control script (from minimal install)
if [ -f "/usr/local/bin/kubesolo-ctl" ]; then
    echo "🗑️  Removing control script..."
    rm -f "/usr/local/bin/kubesolo-ctl"
fi

# Remove PID file
PIDFILE="/var/run/$APP_NAME.pid"
if [ -f "$PIDFILE" ]; then
    echo "🗑️  Removing PID file..."
    rm -f "$PIDFILE"
fi

# Remove log file
LOGFILE="/var/log/$APP_NAME.log"
if [ -f "$LOGFILE" ]; then
    echo "🗑️  Removing log file..."
    rm -f "$LOGFILE"
fi

# Remove configuration and data directory if requested
if [ "$REMOVE_DATA" = "true" ]; then
    if [ -d "$CONFIG_PATH" ]; then
        echo "🗑️  Removing configuration and data directory: $CONFIG_PATH"
        printf "⚠️  This will delete all KubeSolo data. Continue? [y/N] "
        read -r REPLY
        if echo "$REPLY" | grep -qi "^y"; then
            # Unmount any container runtime mounts before removing directory
            unmount_runtime_mounts "$CONFIG_PATH"
            rm -rf "$CONFIG_PATH"
            echo "✅ Configuration directory removed"
        else
            echo "ℹ️  Skipping data directory removal"
        fi
    fi
else
    echo "ℹ️  Configuration directory preserved: $CONFIG_PATH"
    echo "💡 Use --remove-data to delete it"
fi

# Remove kubeconfig if requested
if [ "$REMOVE_KUBECONFIG" = "true" ]; then
    remove_kubeconfig
else
    echo "ℹ️  Kubeconfig preserved in ~/.kube/config"
    echo "💡 Use --remove-kubeconfig to remove KubeSolo context"
fi

echo "✅ $APP_NAME uninstallation completed!"
echo ""
echo "📋 Summary:"
echo "   - Service stopped and removed"
echo "   - Binary removed: $INSTALL_PATH"
if [ "$REMOVE_DATA" = "true" ]; then
    echo "   - Configuration directory removed: $CONFIG_PATH"
else
    echo "   - Configuration directory preserved: $CONFIG_PATH"
fi
if [ "$REMOVE_KUBECONFIG" = "true" ]; then
    echo "   - Kubeconfig entries removed"
else
    echo "   - Kubeconfig entries preserved"
fi

exit 0

