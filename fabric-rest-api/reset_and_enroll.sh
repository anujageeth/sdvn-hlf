#!/bin/bash
# reset_and_enroll.sh
#
# Run this EVERY TIME you restart the Fabric network with ./network.sh.
# The network generates fresh crypto material on each start, which
# invalidates the old wallet enrollment — causing "access denied" errors.
#
# Usage:
#   chmod +x reset_and_enroll.sh
#   ./reset_and_enroll.sh

set -e

API_DIR="/home/anujageeth/hyperledger_fabric/fabric-samples/fabric-rest-api"

echo ""
echo "========================================"
echo "  SDVN — Wallet Reset + Re-Enroll"
echo "========================================"

# ── Step 1: Stop any running app.js ──────────────────────────────────────────
echo "[1/4] Stopping any running app.js on port 3000..."
fuser -k 3000/tcp 2>/dev/null && echo "  Stopped." || echo "  Nothing was running."
sleep 1

# ── Step 2: Delete the stale wallet ──────────────────────────────────────────
WALLET_DIR="$API_DIR/wallet"
if [ -d "$WALLET_DIR" ]; then
    echo "[2/4] Deleting stale wallet at $WALLET_DIR ..."
    rm -rf "$WALLET_DIR"
    echo "  Deleted."
else
    echo "[2/4] Wallet not found (already clean)."
fi

# ── Step 3: Re-enroll admin against the fresh CA ─────────────────────────────
echo "[3/4] Re-enrolling admin..."
cd "$API_DIR"
source ~/.nvm/nvm.sh
nvm use 18

node enrollAdmin.js
echo "  Enrollment complete."

# ── Step 4: Start app.js in background ───────────────────────────────────────
echo "[4/4] Starting app.js..."
node app.js > /tmp/fabric-api.log 2>&1 &
sleep 3

# Check health
HEALTH=$(curl -s --max-time 3 http://localhost:3000/health)
if echo "$HEALTH" | grep -q '"ok"'; then
    echo ""
    echo "✓ API is running on port 3000."
    echo "  Health: $HEALTH"
else
    echo ""
    echo "✗ API did not respond. Check: tail -50 /tmp/fabric-api.log"
fi

echo "========================================"
echo ""