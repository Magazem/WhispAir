#!/bin/bash
# deploy.sh - Deploy memoire to server
# Usage: ./deploy.sh [server] [server_dir]

set -euo pipefail

SERVER="${1:-memoire@192.168.1.100}"
SERVER_DIR="${2:-/opt/memoire}"
BINARY_NAME="memoire"
SSH_OPTS="-o StrictHostKeyChecking=no"

echo "=== Memoire Deploy ==="
echo "Server: $SERVER"
echo "Dir: $SERVER_DIR"
echo ""

# Step 1: Build
echo "[1/5] Building binary..."
GOOS=linux GOARCH=amd64 go build -o "$BINARY_NAME" ./cmd/server
echo "  Built: $BINARY_NAME ($(stat -c%s "$BINARY_NAME") bytes)"

# Step 2: Backup current binary
echo "[2/5] Backing up current binary..."
ssh "$SSH_OPTS" "$SERVER" " \
    cd $SERVER_DIR && \
    if [ -f $BINARY_NAME ]; then \
        cp $BINARY_NAME ${BINARY_NAME}.prev && \
        echo '  Backed up to ${BINARY_NAME}.prev'; \
    else \
        echo '  No existing binary to backup'; \
    fi"

# Step 3: Upload
echo "[3/5] Uploading..."
rsync -avz --progress \
    "$BINARY_NAME" \
    server/pipeline/prompts.yaml \
    "$SERVER:$SERVER_DIR/" \
    "$SSH_OPTS"
echo "  Uploaded."

# Step 4: Restart
echo "[4/5] Restarting service..."
ssh "$SSH_OPTS" "$SERVER" "sudo systemctl restart memoire"
echo "  Restarted."

# Step 5: Verify
echo "[5/5] Verifying..."
sleep 2
if ssh "$SSH_OPTS" "$SERVER" "systemctl is-active memoire" | grep -q "active"; then
    echo "  ✓ Service is active"
else
    echo "  ✗ Service failed to start!"
    echo "  Run: ssh $SERVER 'journalctl -u memoire -n 50'"
    exit 1
fi

echo ""
echo "=== Deploy Complete ==="
echo "View logs: ssh $SERVER 'journalctl -u memoire -f'"
