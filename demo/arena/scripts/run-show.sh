#!/usr/bin/env bash
# Org Arena — launch the whole show: bridge, all three orgs (staggered),
# and the auto-approver, from one command.
#
# Usage: ./demo/arena/scripts/run-show.sh [budget-usd-per-org]
# Run from the repo/worktree root (the directory containing .monomind/).
#
# What it does, in order (matches the run-of-show in the design doc):
#   1. copies demo/arena/orgs/*.json -> .monomind/orgs/ (picks up any edits)
#   2. validates all three configs
#   3. starts the bridge in live mode on :4300 (map at /, votes at /vote)
#   4. starts the auto-approver (Bash approvals only — see README for why)
#   5. starts herald, then forge (+10s), then anvil (+10s), each capped at
#      --budget-usd, matching "01:10-ish" pacing before agents start talking
#
# Everything is backgrounded; PIDs go to demo/arena/.run/pids so
# stop-show.sh can find and stop them. Logs go to demo/arena/.run/*.log.

set -euo pipefail
ROOT="$(pwd)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARENA="$(cd "$HERE/.." && pwd)"
BUDGET="${1:-5}"
RUN_DIR="$ARENA/.run"
mkdir -p "$RUN_DIR"
: > "$RUN_DIR/pids"

if [ ! -d "$ROOT/.monomind" ]; then
  echo "error: run this from a directory with .monomind/ in it (a monomind-initialized worktree or repo)" >&2
  exit 1
fi

echo "== copying org configs =="
cp "$ARENA/orgs/"*.json "$ROOT/.monomind/orgs/"
CI=true npx -y monomind@latest org validate

echo "== starting bridge on :4300 (map: http://localhost:4300/  vote: http://localhost:4300/vote) =="
node "$ARENA/bridge/bridge.mjs" live --root "$ROOT" --orgs forge,anvil,herald --port 4300 > "$RUN_DIR/bridge.log" 2>&1 &
echo $! >> "$RUN_DIR/pids"

echo "== starting auto-approver =="
bash "$ARENA/scripts/auto-approve.sh" forge anvil herald > "$RUN_DIR/auto-approve.log" 2>&1 &
echo $! >> "$RUN_DIR/pids"

launch_org() {
  local org="$1"
  echo "== starting $org (--budget-usd $BUDGET) =="
  ( cd "$ROOT" && CI=true npx -y monomind@latest org run "$org" --budget-usd "$BUDGET" -y > "$RUN_DIR/$org.log" 2>&1 ) &
  echo $! >> "$RUN_DIR/pids"
}

launch_org herald
sleep 10
launch_org forge
sleep 10
launch_org anvil

echo ""
echo "Show is live. Map: http://localhost:4300/   Vote (put this on a QR code): http://localhost:4300/vote"
echo "Stop everything with: ${HERE}/stop-show.sh"
