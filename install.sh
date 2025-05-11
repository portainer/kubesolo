#!/bin/bash

set -e

APP_NAME="kubesolo"
BIN_URL="https://github.com/portainer/kubesolo/releases/download/v0.0.1/kubesolo"
INSTALL_PATH="/usr/local/bin/$APP_NAME"
SERVICE_PATH="/etc/systemd/system/$APP_NAME.service"

# Default configuration
CONFIG_PATH="/var/lib/kubesolo"
PORTAINER_EDGE_ID=""
PORTAINER_EDGE_KEY=""
DEBUG="false"
PPROF_SERVER="false"
INTERACTIVE="false"

# Parse command line arguments
for arg in "$@"; do
  case $arg in
    --path=*)
      CONFIG_PATH="${arg#*=}"
      ;;
    --portainer-edge-id=*)
      PORTAINER_EDGE_ID="${arg#*=}"
      ;;
    --portainer-edge-key=*)
      PORTAINER_EDGE_KEY="${arg#*=}"
      ;;
    --debug=*)
      DEBUG="${arg#*=}"
      ;;
    --pprof-server=*)
      PPROF_SERVER="${arg#*=}"
      ;;
    --non-interactive)
      INTERACTIVE="false"
      ;;
    --help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --path=PATH                  Set configuration path (default: /var/lib/kubesolo)"
      echo "  --portainer-edge-id=ID       Set Portainer Edge ID"
      echo "  --portainer-edge-key=KEY     Set Portainer Edge Key"
      echo "  --debug=true|false           Enable debug logging (default: false)"
      echo "  --pprof-server=true|false    Enable pprof server (default: false)"
      echo "  --non-interactive            Skip interactive prompts"
      echo "  --help                       Show this help message"
      exit 0
      ;;
  esac
done

echo "🔄 Installing $APP_NAME..."

curl -sfL "$BIN_URL" -o "$INSTALL_PATH"
chmod +x "$INSTALL_PATH"

# Configuration prompts (only if interactive mode is enabled)
if [ "$INTERACTIVE" = "true" ]; then
  read -p "Configuration path [default: $CONFIG_PATH]: " input
  CONFIG_PATH=${input:-$CONFIG_PATH}

  read -p "Portainer Edge ID [default: ${PORTAINER_EDGE_ID:-empty}]: " input
  PORTAINER_EDGE_ID=${input:-$PORTAINER_EDGE_ID}

  read -p "Portainer Edge Key [default: ${PORTAINER_EDGE_KEY:-empty}]: " input
  PORTAINER_EDGE_KEY=${input:-$PORTAINER_EDGE_KEY}

  read -p "Enable debug logging? (true/false) [default: $DEBUG]: " input
  DEBUG=${input:-$DEBUG}

  read -p "Enable pprof server? (true/false) [default: $PPROF_SERVER]: " input
  PPROF_SERVER=${input:-$PPROF_SERVER}
fi

echo "📝 Creating systemd service..."

# Construct command arguments
CMD_ARGS="--path=$CONFIG_PATH"

if [ -n "$PORTAINER_EDGE_ID" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-id=$PORTAINER_EDGE_ID"
fi

if [ -n "$PORTAINER_EDGE_KEY" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-key=$PORTAINER_EDGE_KEY"
fi

if [ "$DEBUG" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --debug=$DEBUG"
fi

if [ "$PPROF_SERVER" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --pprof-server=$PPROF_SERVER"
fi

cat <<EOF > "$SERVICE_PATH"
[Unit]
Description=$APP_NAME Service
After=network.target

[Service]
ExecStart=$INSTALL_PATH $CMD_ARGS
Restart=always
RestartSec=3
OOMScoreAdjust=-500
MemoryMax=200M
MemorySwapMax=100M
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

echo "✅ Systemd service file created at $SERVICE_PATH"

echo "🚀 Enabling and starting $APP_NAME..."

systemctl daemon-reexec
systemctl daemon-reload
systemctl enable "$APP_NAME"
systemctl restart "$APP_NAME"

echo "✅ $APP_NAME is now running and self-healing via systemd!"
echo "�� Logs: journalctl -u $APP_NAME -f"
