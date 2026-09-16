#!/usr/bin/env bash
# Org Arena — dev-time auto-approver.
#
# The org runtime asks a human to approve certain tools (Bash, confirmed live
# on 2026-09-16: Write/Edit do not need approval, Bash does) before a role
# may use them. That is the right default for unattended runs, but it stalls
# a live show or a rehearsal if nobody is watching the terminal. This script
# polls each org's pending approvals and grants them automatically, and
# prints every cross-org message, file asset, gate, and question as it
# happens so you can follow along without tailing raw bus.jsonl.
#
# Usage: ./auto-approve.sh forge anvil herald
# Run from the repo/worktree root (the directory containing .monomind/).
# Stop with Ctrl-C; it also exits on its own after ~5 minutes idle.

set -u
ORGS=("$@")
if [ "${#ORGS[@]}" -eq 0 ]; then
  echo "usage: $0 <org> [org...]" >&2
  exit 1
fi

declare -A SEEN
for ((i = 0; i < 60; i++)); do
  for org in "${ORGS[@]}"; do
    # Follow whichever run is latest for this org right now — a role
    # respawn or a fresh `org run` starts a new run directory.
    run_dir=$(ls -dt ".monomind/orgs/$org"/run-* 2>/dev/null | head -1)
    bus="$run_dir/bus.jsonl"
    [ -f "$bus" ] || continue
    n=$(wc -l < "$bus" 2>/dev/null || echo 0)
    last="${SEEN[$org]:-0}"
    if [ "$n" -gt "$last" ]; then
      tail -n +"$((last + 1))" "$bus" | while IFS= read -r line; do
        case "$line" in
          *'"type":"xorg"'*)
            echo "[$org] XORG: $(echo "$line" | grep -oE '"from":"[^"]*"|"to":"[^"]*"|"subject":"[^"]*"' | tr '\n' ' ')" ;;
          *'"type":"asset"'*)
            echo "[$org] ASSET: $(echo "$line" | grep -oE '"from":"[^"]*"|"path":"[^"]*"' | tr '\n' ' ')" ;;
          *'"type":"gate"'*)
            echo "[$org] GATE: $line" ;;
          *'"type":"question"'*)
            echo "[$org] QUESTION: $line" ;;
        esac
      done
      SEEN[$org]=$n
    fi
  done
  for org in "${ORGS[@]}"; do
    out=$(CI=true npx -y monomind@latest org approvals "$org" 2>&1)
    echo "$out" | grep '❓' | while IFS= read -r reqline; do
      role=$(echo "$reqline" | sed -E 's/^❓ [0-9-]+ [0-9:]+ +([a-zA-Z_-]+): .*/\1/')
      tool=$(echo "$reqline" | sed -E 's/^.*: (.*)$/\1/')
      if [ -n "$role" ] && [ -n "$tool" ]; then
        echo "[$org] AUTO-APPROVE $role -> $tool"
        CI=true npx -y monomind@latest org approve "$org" "$role" "$tool" >/dev/null 2>&1
      fi
    done
  done
  sleep 5
done
echo "auto-approve.sh: idle timeout reached, exiting"
