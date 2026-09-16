#!/usr/bin/env bash
# Org Arena — stop the show: `org stop` all three orgs, then kill the
# bridge and auto-approver background processes started by run-show.sh.
#
# Usage: ./demo/arena/scripts/stop-show.sh
# Run from the same repo/worktree root run-show.sh was launched from.

set -uo pipefail
ROOT="$(pwd)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARENA="$(cd "$HERE/.." && pwd)"
RUN_DIR="$ARENA/.run"

for org in forge anvil herald; do
  echo "== stopping $org =="
  CI=true npx -y monomind@latest org stop "$org" 2>&1 || true
done

if [ -f "$RUN_DIR/pids" ]; then
  echo "== stopping bridge + auto-approver =="
  while read -r pid; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done < "$RUN_DIR/pids"
  rm -f "$RUN_DIR/pids"
fi

echo "Done. Logs are in $RUN_DIR/*.log if you want to see what happened."
