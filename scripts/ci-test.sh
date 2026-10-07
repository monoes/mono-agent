#!/usr/bin/env bash
# Runs `go test ./...` with cmd/monoagentcli split across parallel shards.
#
# That package holds ~1000 serial tests (~13 min under -race) and is the long
# pole of the Build & test jobs: go test parallelises across packages, not
# across tests inside one. Every test process gets its own throwaway home from
# internal/testhome (see cmd/monoagentcli/testmain_test.go), so the shards are
# isolated from each other exactly as separate `go test` runs are.
#
# usage: scripts/ci-test.sh [go test flags, e.g. -race -tags nosocial]
# env:   CLI_SHARDS (default 3)
set -euo pipefail

shards="${CLI_SHARDS:-3}"
cli=github.com/monoes/mono-agent/cmd/monoagentcli
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Compile the CLI test binary once with the same flags.
go test "$@" -c -o "$work/cli.test" ./cmd/monoagentcli

# Deterministic round-robin split of the top-level tests (stable test order
# keeps the heavy, alphabetically-adjacent groups spread across shards).
"$work/cli.test" -test.list='^Test' | grep '^Test' | sort > "$work/tests.txt"
for i in $(seq 0 $((shards - 1))); do
  awk -v n="$shards" -v i="$i" '(NR - 1) % n == i' "$work/tests.txt" | paste -sd'|' > "$work/shard$i.re"
done

pids=()
for i in $(seq 0 $((shards - 1))); do
  (
    cd cmd/monoagentcli
    "$work/cli.test" -test.timeout=35m -test.run="^($(cat "$work/shard$i.re"))\$" > "$work/shard$i.log" 2>&1
  ) &
  pids+=($!)
done

# Everything else, in the normal way, while the shards run.
pkgs="$(go list "$@" ./... | grep -vx "$cli")"
status=0
# shellcheck disable=SC2086
go test -timeout 35m "$@" $pkgs || status=$?

for i in "${!pids[@]}"; do
  echo "=== cmd/monoagentcli shard $i ==="
  if wait "${pids[$i]}"; then
    tail -n 3 "$work/shard$i.log"
  else
    echo "::error::cmd/monoagentcli shard $i failed"
    cat "$work/shard$i.log"
    status=1
  fi
done
exit "$status"
