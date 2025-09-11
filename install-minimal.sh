#!/bin/sh

# Minimal KubeSolo installer for busybox/embedded systems
# This script assumes minimal tooling and focuses on basic functionality

set -e

# Basic error handling
die() {
    echo "ERROR: $1" >&2
    exit 1
}

# Detect architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7l) ARCH="arm" ;;
    riscv64) ARCH="riscv64" ;;
    *) die "Unsupported architecture: $ARCH" ;;
esac

# Configuration
KUBESOLO_VERSION="${KUBESOLO_VERSION:-v0.1.7-beta}"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"
INSTALL_PATH="/usr/local/bin/kubesolo"
BIN_URL="https://github.com/portainer/kubesolo/releases/download/$KUBESOLO_VERSION/kubesolo-$KUBESOLO_VERSION-linux-$ARCH.tar.gz"

# Parse basic arguments
for arg in "$@"; do
    case $arg in
        --version=*) KUBESOLO_VERSION="${arg#*=}" ;;
        --path=*) CONFIG_PATH="${arg#*=}" ;;
        --help)
            echo "Minimal KubeSolo installer for embedded systems"
            echo "Usage: $0 [--version=VERSION] [--path=PATH]"
            echo "Environment variables:"
            echo "  KUBESOLO_VERSION - Version to install (default: $KUBESOLO_VERSION)"
            echo "  KUBESOLO_PATH    - Config path (default: $CONFIG_PATH)"
            exit 0
            ;;
    esac
done

echo "Installing KubeSolo $KUBESOLO_VERSION for $ARCH..."

# Create temporary directory
TEMP_DIR="/tmp/kubesolo-install-$$"
mkdir -p "$TEMP_DIR" || die "Failed to create temp directory"

# Download binary
echo "Downloading..."
if command -v wget >/dev/null 2>&1; then
    wget -q -O "$TEMP_DIR/kubesolo.tar.gz" "$BIN_URL" || die "Download failed"
elif command -v curl >/dev/null 2>&1; then
    curl -sL -o "$TEMP_DIR/kubesolo.tar.gz" "$BIN_URL" || die "Download failed"
else
    die "Neither wget nor curl available"
fi

# Extract
echo "Extracting..."
cd "$TEMP_DIR"
tar -xzf kubesolo.tar.gz || die "Extraction failed"

# Install
echo "Installing to $INSTALL_PATH..."
mv kubesolo "$INSTALL_PATH" || die "Installation failed"
chmod +x "$INSTALL_PATH" || die "Failed to set permissions"

# Cleanup
cd /
rm -rf "$TEMP_DIR"

# Create basic startup script
STARTUP_SCRIPT="/etc/init.d/kubesolo"
cat > "$STARTUP_SCRIPT" << 'EOF'
#!/bin/sh

DAEMON="/usr/local/bin/kubesolo"
PIDFILE="/var/run/kubesolo.pid"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"

start() {
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "kubesolo already running"
        return 1
    fi
    echo "Starting kubesolo..."
    GODEBUG=madvdontneed=1 $DAEMON --path="$CONFIG_PATH" &
    echo $! > "$PIDFILE"
    echo "kubesolo started"
}

stop() {
    if [ ! -f "$PIDFILE" ]; then
        echo "kubesolo not running"
        return 1
    fi
    echo "Stopping kubesolo..."
    kill "$(cat "$PIDFILE")" 2>/dev/null || true
    rm -f "$PIDFILE"
    echo "kubesolo stopped"
}

status() {
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "kubesolo running (PID: $(cat "$PIDFILE"))"
    else
        echo "kubesolo not running"
        return 1
    fi
}

case "$1" in
    start) start ;;
    stop) stop ;;
    restart) stop; sleep 1; start ;;
    status) status ;;
    *) echo "Usage: $0 {start|stop|restart|status}"; exit 1 ;;
esac
EOF

chmod +x "$STARTUP_SCRIPT"

# Create simple management script
cat > "/usr/local/bin/kubesolo-ctl" << 'EOF'
#!/bin/sh
# Simple KubeSolo control script

PIDFILE="/var/run/kubesolo.pid"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"
KUBECONFIG="$CONFIG_PATH/pki/admin/admin.kubeconfig"

case "$1" in
    start)
        if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
            echo "kubesolo already running"
            exit 1
        fi
        echo "Starting kubesolo..."
        GODEBUG=madvdontneed=1 /usr/local/bin/kubesolo --path="$CONFIG_PATH" > /var/log/kubesolo.log 2>&1 &
        echo $! > "$PIDFILE"
        echo "kubesolo started (PID: $!)"
        ;;
    stop)
        if [ -f "$PIDFILE" ]; then
            PID=$(cat "$PIDFILE")
            if kill -0 "$PID" 2>/dev/null; then
                kill "$PID"
                rm -f "$PIDFILE"
                echo "kubesolo stopped"
            else
                echo "kubesolo not running (removing stale PID file)"
                rm -f "$PIDFILE"
            fi
        else
            echo "kubesolo not running"
        fi
        ;;
    status)
        if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
            echo "kubesolo running (PID: $(cat "$PIDFILE"))"
        else
            echo "kubesolo not running"
            exit 1
        fi
        ;;
    logs)
        if [ -f /var/log/kubesolo.log ]; then
            tail -f /var/log/kubesolo.log
        else
            echo "No log file found"
            exit 1
        fi
        ;;
    kubeconfig)
        if [ -f "$KUBECONFIG" ]; then
            echo "export KUBECONFIG=$KUBECONFIG"
            echo "# Or copy to ~/.kube/config:"
            echo "# mkdir -p ~/.kube && cp $KUBECONFIG ~/.kube/config"
        else
            echo "Kubeconfig not found at $KUBECONFIG"
            echo "Make sure kubesolo is running and has generated certificates"
            exit 1
        fi
        ;;
    *)
        echo "Usage: $0 {start|stop|status|logs|kubeconfig}"
        echo ""
        echo "Commands:"
        echo "  start      - Start kubesolo"
        echo "  stop       - Stop kubesolo"
        echo "  status     - Show status"
        echo "  logs       - Show logs (follow mode)"
        echo "  kubeconfig - Show kubeconfig setup instructions"
        exit 1
        ;;
esac
EOF

chmod +x "/usr/local/bin/kubesolo-ctl"

echo ""
echo "✅ KubeSolo installed successfully!"
echo ""
echo "Quick start:"
echo "  kubesolo-ctl start    # Start kubesolo"
echo "  kubesolo-ctl status   # Check status"
echo "  kubesolo-ctl logs     # View logs"
echo "  kubesolo-ctl kubeconfig # Setup kubectl"
echo ""
echo "Or use the init script:"
echo "  /etc/init.d/kubesolo start"
echo ""
echo "Config path: $CONFIG_PATH"
echo "Logs: /var/log/kubesolo.log"
echo ""

# Try to start if we can
if [ -t 0 ]; then
    printf "Start kubesolo now? [y/N] "
    read -r answer
    case $answer in
        [Yy]*) 
            echo "Starting kubesolo..."
            /usr/local/bin/kubesolo-ctl start
            ;;
    esac
fi 
