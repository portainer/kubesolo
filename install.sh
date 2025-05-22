#!/bin/sh

set -e

# Function to handle errors
handle_error() {
    echo "❌ Error: $1"
    exit 1
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
    *)
        handle_error "Unsupported architecture: $ARCH"
        ;;
esac

# Default configuration from environment variables
KUBESOLO_VERSION="${KUBESOLO_VERSION:-0.1.0}"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"
PORTAINER_EDGE_ID="${KUBESOLO_PORTAINER_EDGE_ID:-}"
PORTAINER_EDGE_KEY="${KUBESOLO_PORTAINER_EDGE_KEY:-}"
PORTAINER_EDGE_ASYNC="${KUBESOLO_PORTAINER_EDGE_ASYNC:-false}"
LOCAL_STORAGE="${KUBESOLO_LOCAL_STORAGE:-false}"
DEBUG="${KUBESOLO_DEBUG:-false}"
PPROF_SERVER="${KUBESOLO_PPROF_SERVER:-false}"

# Parse command line arguments (these will override environment variables)
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
      echo "  --help                       Show this help message"
      echo ""
      echo "Environment Variables:"
      echo "  KUBESOLO_VERSION            Set KubeSolo version"
      echo "  KUBESOLO_PATH               Set configuration path"
      echo "  KUBESOLO_PORTAINER_EDGE_ID  Set Portainer Edge ID"
      echo "  KUBESOLO_PORTAINER_EDGE_KEY Set Portainer Edge Key"
      echo "  KUBESOLO_PORTAINER_EDGE_ASYNC Set Portainer Edge Async"
      echo "  KUBESOLO_LOCAL_STORAGE      Enable local storage (true/false)"
      echo "  KUBESOLO_DEBUG              Enable debug logging"
      echo "  KUBESOLO_PPROF_SERVER       Enable pprof server"
      exit 0
      ;;
  esac
done

# Service configuration
APP_NAME="kubesolo"
BIN_URL="https://github.com/portainer/kubesolo/releases/download/$KUBESOLO_VERSION-beta/kubesolo-$KUBESOLO_VERSION-$OS-$ARCH.tar.gz"
INSTALL_PATH="/usr/local/bin/$APP_NAME"
SERVICE_PATH="/etc/systemd/system/$APP_NAME.service"

echo "🔄 Installing $APP_NAME..."

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

echo "📝 Creating systemd service..."

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

cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create systemd service file at $SERVICE_PATH"
[Unit]
Description=$APP_NAME Service
After=network.target

[Service]
ExecStart=$INSTALL_PATH $CMD_ARGS
Restart=always
Environment="GODEBUG=madvdontneed=1"
RestartSec=3
OOMScoreAdjust=-500
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

echo "✅ Systemd service file created at $SERVICE_PATH"

echo "🚀 Enabling and starting $APP_NAME..."

systemctl daemon-reexec || handle_error "Failed to reexecute systemd daemon"
systemctl daemon-reload || handle_error "Failed to reload systemd daemon"
systemctl enable "$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
systemctl restart "$APP_NAME" || handle_error "Failed to start $APP_NAME service"

echo "✅ $APP_NAME is now running and self-healing via systemd!"
echo "📋 Logs: journalctl -u $APP_NAME -f"

# Check for kubectl and merge kubeconfig
KUBECTL_PATH=$(command -v kubectl 2>/dev/null)
if [ -n "$KUBECTL_PATH" ] && [ -x "$KUBECTL_PATH" ]; then
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
    echo "ℹ️  kubectl not found, skipping kubeconfig merge"
    echo "💡 To use kubectl, please install it first: https://kubernetes.io/docs/tasks/tools/install-kubectl/"
fi
