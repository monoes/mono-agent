#!/usr/bin/env bash
# Org Arena — snapshot a good rehearsal into a named fallback run, so a
# live-show outage can switch to replay without losing the room.
#
# Usage: ./demo/arena/scripts/branch-fallback.sh [branch-name]
# Run AFTER a rehearsal (run-show.sh + stop-show.sh) from the same root.
# Defaults branch-name to "fallback-1".
#
# Afterwards, to replay it:
#   node demo/arena/bridge/bridge.mjs replay --root "$(pwd)" \
#     --run-map forge=<branch-name>,anvil=<branch-name>,herald=<branch-name> \
#     --speed 12

set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$HERE/../.run"
NAME="${1:-fallback-1}"
RUN_MAP=""

for org in forge anvil herald; do
  latest_run=$(ls -dt ".monomind/orgs/$org"/run-* 2>/dev/null | head -1 | xargs -n1 basename 2>/dev/null || true)
  if [ -z "$latest_run" ]; then
    echo "skip $org: no run found under .monomind/orgs/$org/" >&2
    continue
  fi
  echo "== branching $org/$latest_run (label: $NAME) =="
  # `org branch` takes NAME as a label, not the resulting directory name —
  # confirmed live 2026-09-16: it creates its own "branch-<ts>-<hash>" run
  # dir and prints it as "... as <branch-id>". Capture that real id; don't
  # assume it equals $NAME (an earlier draft of this script got that wrong).
  out=$(CI=true npx -y monomind@latest org branch "$org" "$latest_run" "$NAME")
  echo "$out"
  branch_id=$(echo "$out" | grep -oE 'as [A-Za-z0-9_-]+' | awk '{print $2}')
  if [ -z "$branch_id" ]; then
    echo "warning: could not parse the branch id out of 'org branch' output for $org" >&2
    continue
  fi
  RUN_MAP="${RUN_MAP:+$RUN_MAP,}$org=$branch_id"
done

echo ""
if [ -n "$RUN_MAP" ]; then
  echo "Replay this fallback with:"
  echo "  node demo/arena/bridge/bridge.mjs replay --root \"\$(pwd)\" --run-map $RUN_MAP --speed 12"
  echo "$RUN_MAP" > "$HERE/../.run/last-fallback-run-map.txt"
else
  echo "No branches created — nothing to replay." >&2
  exit 1
fi
