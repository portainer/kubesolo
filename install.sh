#!/bin/sh

set -e

# Function to handle errors
handle_error() {
    echo "❌ Error: $1"
    exit 1
}

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

# Function to detect if we're in a container or embedded environment
detect_environment() {
    if [ -f /.dockerenv ] || [ -f /run/.containerenv ]; then
        echo "container"
    elif [ -f /proc/device-tree/model ] && grep -qi "raspberry\|beagle\|odroid" /proc/device-tree/model 2>/dev/null; then
        echo "embedded"
    elif [ "$(uname -m)" = "armv7l" ] || [ "$(uname -m)" = "aarch64" ]; then
        echo "arm"
    else
        echo "standard"
    fi
}

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64)
        ARCH="arm64"
        ;;
    armv7l)
        ARCH="arm"
        ;;
    *)
        handle_error "Unsupported architecture: $ARCH"
        ;;
esac

# Detect init system and environment
INIT_SYSTEM=$(detect_init_system)
ENVIRONMENT=$(detect_environment)

echo "🔍 Detected init system: $INIT_SYSTEM"
echo "🔍 Detected environment: $ENVIRONMENT"

# Default configuration from environment variables
KUBESOLO_VERSION="${KUBESOLO_VERSION:-v0.1.2-beta}"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"
PORTAINER_EDGE_ID="${KUBESOLO_PORTAINER_EDGE_ID:-}"
PORTAINER_EDGE_KEY="${KUBESOLO_PORTAINER_EDGE_KEY:-}"
PORTAINER_EDGE_ASYNC="${KUBESOLO_PORTAINER_EDGE_ASYNC:-false}"
LOCAL_STORAGE="${KUBESOLO_LOCAL_STORAGE:-false}"
DEBUG="${KUBESOLO_DEBUG:-false}"
PPROF_SERVER="${KUBESOLO_PPROF_SERVER:-false}"
RUN_MODE="${KUBESOLO_RUN_MODE:-service}"  # service, foreground, or daemon

# Parse command line arguments
for arg in "$@"; do
  case $arg in
    --version=*)
      KUBESOLO_VERSION="${arg#*=}"
      ;;
    --path=*)
      CONFIG_PATH="${arg#*=}"
      ;;
    --portainer-edge-id=*)
      PORTAINER_EDGE_ID="${arg#*=}"
      ;;
    --portainer-edge-key=*)
      PORTAINER_EDGE_KEY="${arg#*=}"
      ;;
    --portainer-edge-async=*)
      PORTAINER_EDGE_ASYNC="${arg#*=}"
      ;;
    --local-storage=*)
      LOCAL_STORAGE="${arg#*=}"
      ;;
    --debug=*)
      DEBUG="${arg#*=}"
      ;;
    --pprof-server=*)
      PPROF_SERVER="${arg#*=}"
      ;;
    --run-mode=*)
      RUN_MODE="${arg#*=}"
      ;;
    --help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --version=VERSION            Set KubeSolo version (default: $KUBESOLO_VERSION)"
      echo "  --path=PATH                  Set configuration path (default: $CONFIG_PATH)"
      echo "  --portainer-edge-id=ID       Set Portainer Edge ID"
      echo "  --portainer-edge-key=KEY     Set Portainer Edge Key"
      echo "  --portainer-edge-async=true|false   Enable Portainer Edge Async (default: $PORTAINER_EDGE_ASYNC)"
      echo "  --local-storage=true|false   Enable local storage (default: $LOCAL_STORAGE)"
      echo "  --debug=true|false           Enable debug logging (default: $DEBUG)"
      echo "  --pprof-server=true|false    Enable pprof server (default: $PPROF_SERVER)"
      echo "  --run-mode=MODE              Run mode: service, foreground, or daemon (default: $RUN_MODE)"
      echo "  --help                       Show this help message"
      echo ""
      echo "Supported Init Systems: systemd, sysvinit, s6, runit, openrc, upstart"
      echo "Fallback modes: foreground (manual start), daemon (background process)"
      exit 0
      ;;
  esac
done

# Service configuration
APP_NAME="kubesolo"
BIN_URL="https://github.com/portainer/kubesolo/releases/download/$KUBESOLO_VERSION/kubesolo-$KUBESOLO_VERSION-$OS-$ARCH.tar.gz"
INSTALL_PATH="/usr/local/bin/$APP_NAME"

echo "🔄 Installing $APP_NAME for $INIT_SYSTEM init system..."

# Download and extract the archive
TEMP_DIR=$(mktemp -d -p $HOME) || handle_error "Failed to create temporary directory"
echo "📥 Downloading $APP_NAME..."
curl -sfL "$BIN_URL" -o "$TEMP_DIR/kubesolo.tar.gz" || handle_error "Failed to download $APP_NAME from $BIN_URL"

echo "📦 Extracting $APP_NAME..."
tar --no-xattr -xzf "$TEMP_DIR/kubesolo.tar.gz" -C "$TEMP_DIR" || handle_error "Failed to extract $APP_NAME archive"

echo "📝 Installing binary..."
mv "$TEMP_DIR/kubesolo" "$INSTALL_PATH" || handle_error "Failed to move binary to $INSTALL_PATH"
rm -rf "$TEMP_DIR"
chmod +x "$INSTALL_PATH" || handle_error "Failed to set executable permissions on $INSTALL_PATH"

# Construct command arguments
CMD_ARGS="--path=$CONFIG_PATH"

if [ -n "$PORTAINER_EDGE_ID" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-id=\"$PORTAINER_EDGE_ID\""
fi

if [ -n "$PORTAINER_EDGE_KEY" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-key=\"$PORTAINER_EDGE_KEY\""
fi

if [ "$LOCAL_STORAGE" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --local-storage=true"
fi

if [ "$DEBUG" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --debug=$DEBUG"
fi

if [ "$PPROF_SERVER" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --pprof-server=$PPROF_SERVER"
fi

# Function to create systemd service
create_systemd_service() {
    SERVICE_PATH="/etc/systemd/system/$APP_NAME.service"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create systemd service file"
[Unit]
Description=$APP_NAME Service
After=network.target

[Service]
ExecStart=$INSTALL_PATH $CMD_ARGS
Restart=always
RestartSec=3
OOMScoreAdjust=-500
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
    
    systemctl daemon-reexec || handle_error "Failed to reexecute systemd daemon"
    systemctl daemon-reload || handle_error "Failed to reload systemd daemon"
    systemctl enable "$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
    systemctl restart "$APP_NAME" || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with systemd"
}

# Function to create SysV init script
create_sysvinit_service() {
    SERVICE_PATH="/etc/init.d/$APP_NAME"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create SysV init script"
#!/bin/sh
### BEGIN INIT INFO
# Provides:          $APP_NAME
# Required-Start:    \$network \$local_fs
# Required-Stop:     \$network \$local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: $APP_NAME service
# Description:       KubeSolo single-node Kubernetes distribution
### END INIT INFO

DAEMON="$INSTALL_PATH"
DAEMON_ARGS="$CMD_ARGS"
PIDFILE="/var/run/$APP_NAME.pid"
USER="root"

. /lib/lsb/init-functions

case "\$1" in
    start)
        log_daemon_msg "Starting $APP_NAME"
        GODEBUG=madvdontneed=1 start-stop-daemon --start --quiet --pidfile \$PIDFILE --make-pidfile --background --chuid \$USER --exec \$DAEMON -- \$DAEMON_ARGS
        log_end_msg \$?
        ;;
    stop)
        log_daemon_msg "Stopping $APP_NAME"
        start-stop-daemon --stop --quiet --pidfile \$PIDFILE --remove-pidfile
        log_end_msg \$?
        ;;
    restart)
        \$0 stop
        \$0 start
        ;;
    status)
        status_of_proc -p \$PIDFILE \$DAEMON "$APP_NAME" && exit 0 || exit \$?
        ;;
    *)
        echo "Usage: \$0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
EOF
    
    chmod +x "$SERVICE_PATH" || handle_error "Failed to make SysV init script executable"
    
    # Enable service for different runlevels
    if command -v update-rc.d >/dev/null 2>&1; then
        update-rc.d "$APP_NAME" defaults || handle_error "Failed to enable $APP_NAME service"
    elif command -v chkconfig >/dev/null 2>&1; then
        chkconfig --add "$APP_NAME" || handle_error "Failed to add $APP_NAME service"
        chkconfig "$APP_NAME" on || handle_error "Failed to enable $APP_NAME service"
    fi
    
    service "$APP_NAME" start || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with SysV init"
}

# Function to create OpenRC service
create_openrc_service() {
    SERVICE_PATH="/etc/init.d/$APP_NAME"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create OpenRC service script"
#!/sbin/openrc-run

name="$APP_NAME"
description="KubeSolo single-node Kubernetes distribution"
command="$INSTALL_PATH"
command_args="$CMD_ARGS"
command_background=true
pidfile="/var/run/\${RC_SVCNAME}.pid"
command_user="root"

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath --directory --owner \$command_user --mode 0755 /var/run
    export GODEBUG=madvdontneed=1
}
EOF
    
    chmod +x "$SERVICE_PATH" || handle_error "Failed to make OpenRC service script executable"
    rc-update add "$APP_NAME" default || handle_error "Failed to enable $APP_NAME service"
    rc-service "$APP_NAME" start || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with OpenRC"
}

# Function to create s6 service
create_s6_service() {
    S6_SERVICE_DIR="/etc/s6/sv/$APP_NAME"
    mkdir -p "$S6_SERVICE_DIR" || handle_error "Failed to create s6 service directory"
    
    cat <<EOF > "$S6_SERVICE_DIR/run" || handle_error "Failed to create s6 run script"
#!/bin/sh
export GODEBUG=madvdontneed=1
exec $INSTALL_PATH $CMD_ARGS
EOF
    
    chmod +x "$S6_SERVICE_DIR/run" || handle_error "Failed to make s6 run script executable"
    
    # Create finish script for proper cleanup
    cat <<EOF > "$S6_SERVICE_DIR/finish"
#!/bin/sh
echo "$APP_NAME service finished"
EOF
    chmod +x "$S6_SERVICE_DIR/finish"
    
    # Enable and start service
    if [ -d /etc/s6/adminsv/default ]; then
        ln -sf "$S6_SERVICE_DIR" "/etc/s6/adminsv/default/$APP_NAME" 2>/dev/null || true
    fi
    
    if command -v s6-svc >/dev/null 2>&1; then
        s6-svc -u "$S6_SERVICE_DIR" || handle_error "Failed to start $APP_NAME with s6"
    fi
    echo "✅ $APP_NAME service created and started with s6"
}

# Function to create runit service
create_runit_service() {
    RUNIT_SERVICE_DIR="/etc/runit/sv/$APP_NAME"
    mkdir -p "$RUNIT_SERVICE_DIR" || handle_error "Failed to create runit service directory"
    
    cat <<EOF > "$RUNIT_SERVICE_DIR/run" || handle_error "Failed to create runit run script"
#!/bin/sh
export GODEBUG=madvdontneed=1
exec $INSTALL_PATH $CMD_ARGS
EOF
    
    chmod +x "$RUNIT_SERVICE_DIR/run" || handle_error "Failed to make runit run script executable"
    
    # Enable service
    if [ -d /var/service ]; then
        ln -sf "$RUNIT_SERVICE_DIR" "/var/service/$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
    elif [ -d /etc/runit/runsvdir/default ]; then
        ln -sf "$RUNIT_SERVICE_DIR" "/etc/runit/runsvdir/default/$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
    fi
    
    echo "✅ $APP_NAME service created and started with runit"
}

# Function to create upstart service
create_upstart_service() {
    SERVICE_PATH="/etc/init/$APP_NAME.conf"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create upstart service file"
description "$APP_NAME service"
author "KubeSolo"

start on runlevel [2345]
stop on runlevel [!2345]

respawn
respawn limit 10 5

env GODEBUG=madvdontneed=1
exec $INSTALL_PATH $CMD_ARGS
EOF
    
    initctl reload-configuration || handle_error "Failed to reload upstart configuration"
    initctl start "$APP_NAME" || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with upstart"
}

# Function to run in foreground mode
run_foreground() {
    echo "🚀 Starting $APP_NAME in foreground mode..."
    echo "📝 Command: $INSTALL_PATH $CMD_ARGS"
    echo "⚠️  Press Ctrl+C to stop the service"
    echo "💡 To run in background, use: GODEBUG=madvdontneed=1 nohup $INSTALL_PATH $CMD_ARGS > /var/log/$APP_NAME.log 2>&1 &"
    export GODEBUG=madvdontneed=1
    exec $INSTALL_PATH $CMD_ARGS
}

# Function to run as daemon
run_daemon() {
    PIDFILE="/var/run/$APP_NAME.pid"
    LOGFILE="/var/log/$APP_NAME.log"
    
    echo "🚀 Starting $APP_NAME as daemon..."
    
    # Create log directory if it doesn't exist
    mkdir -p "$(dirname "$LOGFILE")" || handle_error "Failed to create log directory"
    
    # Start daemon
    GODEBUG=madvdontneed=1 nohup $INSTALL_PATH $CMD_ARGS > "$LOGFILE" 2>&1 &
    echo $! > "$PIDFILE" || handle_error "Failed to write PID file"
    
    echo "✅ $APP_NAME started as daemon (PID: $(cat "$PIDFILE"))"
    echo "📋 Logs: tail -f $LOGFILE"
    echo "🛑 Stop: kill \$(cat $PIDFILE)"
}

# Main service creation logic
echo "📝 Setting up $APP_NAME service..."

case "$RUN_MODE" in
    "foreground")
        run_foreground
        ;;
    "daemon")
        run_daemon
        ;;
    "service"|*)
        case "$INIT_SYSTEM" in
            "systemd")
                create_systemd_service
                ;;
            "sysvinit")
                create_sysvinit_service
                ;;
            "openrc")
                create_openrc_service
                ;;
            "s6")
                create_s6_service
                ;;
            "runit")
                create_runit_service
                ;;
            "upstart")
                create_upstart_service
                ;;
            "unknown"|*)
                echo "⚠️  Unknown or unsupported init system: $INIT_SYSTEM"
                echo "🔄 Falling back to daemon mode..."
                run_daemon
                ;;
        esac
        ;;
esac

# Wait for kubesolo to start and generate kubeconfig
if [ "$RUN_MODE" != "foreground" ]; then
    echo "📋 Service status and logs:"
    case "$INIT_SYSTEM" in
        "systemd")
            echo "   Status: systemctl status $APP_NAME"
            echo "   Logs: journalctl -u $APP_NAME -f"
            ;;
        "sysvinit")
            echo "   Status: service $APP_NAME status"
            echo "   Logs: tail -f /var/log/syslog | grep $APP_NAME"
            ;;
        "openrc")
            echo "   Status: rc-service $APP_NAME status"
            echo "   Logs: tail -f /var/log/messages | grep $APP_NAME"
            ;;
        *)
            echo "   Logs: tail -f /var/log/$APP_NAME.log"
            ;;
    esac
fi

# Check for kubectl and merge kubeconfig (same as original)
KUBECTL_PATH=$(command -v kubectl 2>/dev/null)
if [ -n "$KUBECTL_PATH" ] && [ -x "$KUBECTL_PATH" ] && [ "$RUN_MODE" != "foreground" ]; then
    echo "🔍 Detected kubectl installation at $KUBECTL_PATH"
    
    # Wait for kubesolo to generate the kubeconfig
    echo "⏳ Waiting for kubesolo to generate kubeconfig..."
    i=1
    while [ $i -le 30 ]; do
        if [ -f "$CONFIG_PATH/pki/admin/admin.kubeconfig" ]; then
            break
        fi
        if [ $i -eq 30 ]; then
            echo "⚠️  Kubeconfig not found in $CONFIG_PATH/pki/admin/admin.kubeconfig after waiting"
            exit 0
        fi
        sleep 1
        i=$((i + 1))
    done

    if [ -f "$CONFIG_PATH/pki/admin/admin.kubeconfig" ]; then
        echo "🔄 Merging kubeconfig..."
        # Create backup of existing kubeconfig
        if [ -f "$HOME/.kube/config" ]; then
            cp "$HOME/.kube/config" "$HOME/.kube/config.backup-$(date +%Y%m%d%H%M%S)" || handle_error "Failed to backup existing kubeconfig"
        fi
        
        # Create .kube directory if it doesn't exist
        mkdir -p "$HOME/.kube" || handle_error "Failed to create .kube directory"
        
        # Merge the configs
        KUBECONFIG="$HOME/.kube/config:$CONFIG_PATH/pki/admin/admin.kubeconfig" "$KUBECTL_PATH" config view --flatten > "$HOME/.kube/config.tmp" || handle_error "Failed to merge kubeconfigs"
        mv "$HOME/.kube/config.tmp" "$HOME/.kube/config" || handle_error "Failed to update kubeconfig"
        
        echo "✅ Kubeconfig merged successfully"
        echo "📝 Your existing kubeconfig has been backed up with timestamp"
    fi
else
    if [ "$RUN_MODE" != "foreground" ]; then
        echo "ℹ️  kubectl not found, skipping kubeconfig merge"
        echo "💡 To use kubectl, please install it first: https://kubernetes.io/docs/tasks/tools/install-kubectl/"
        echo "📁 Kubeconfig location: $CONFIG_PATH/pki/admin/admin.kubeconfig"
    fi
fi

echo "✅ $APP_NAME installation completed!"
