#!/bin/bash
# Stop what setup.sh started, by the PIDs it recorded (process groups, since
# each was started with setsid). Leaves $E2E_WORK in place for inspection.
source "$(dirname "$0")/env.sh"
for f in chrome bridge fixture; do
  pidfile="$E2E_WORK/pids/$f"
  [ -f "$pidfile" ] || continue
  pid=$(cat "$pidfile")
  kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
  mv "$pidfile" "$pidfile.stopped"
done
sleep 1
echo "e2e stack stopped"
