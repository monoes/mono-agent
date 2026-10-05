#!/usr/bin/env bash
# Pre-release check: does an installed monomind still produce what mono-agent's
# parsers read? Records a fresh sections-org run (validate, status, events and
# the document store) from the given monomind into a throwaway folder and runs
# internal/monomind's golden tests on it (MONOMIND_GOLDEN_DIR; the committed
# fixtures are internal/monomind/testdata/monomind-2.24.1).
#
# No model is called: the org's roles run the scripted stand-in
# testdata/monomind-2.24.1/fake-codex.py as their "codex" runtime, and
# monomind's real runtime does the routing, the documents and the event log.
#
#   # newest published monomind, before cutting a release:
#   d=$(mktemp -d) && npm install --prefix "$d" @monoes/monomindcli@latest \
#     && scripts/monomind-golden-check.sh "$d/node_modules/.bin/monomind"
#   # the monomind on PATH:
#   scripts/monomind-golden-check.sh
#
# Everything monomind writes (HOME, project, run state) lives under one temp
# folder; nothing touches ~/.monomind or ~/.monoagent. Needs jq, python3, go.
# It is not a workflow job: CI never runs an unpinned npm package (see
# mcp-pin-guard in ci.yml), and the monomind pinned in .mcp.json is older than
# the one the fixtures were recorded from.
set -euo pipefail

mm="${1:-monomind}"
command -v "$mm" >/dev/null || { echo "monomind not found: $mm" >&2; exit 1; }
for tool in jq python3 go; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done
repo="$(cd "$(dirname "$0")/.." && pwd)"
src="$repo/internal/monomind/testdata/monomind-2.24.1"
root="$(mktemp -d)"
trap 'rm -r "$root" 2>/dev/null || true' EXIT
home="$root/home" proj="$root/proj" gold="$root/golden"
mkdir -p "$home" "$proj/.monomind/orgs" "$gold"
cp "$src"/* "$gold/" # what is not re-recorded below (synthetic and schedule files) stays
cp "$src/fake-codex.py" "$root/codex"
chmod +x "$root/codex"

# Every monomind call runs in the project with its own HOME and the scripted runtime.
mmrun() { (cd "$proj" && env HOME="$home" CODEX_CLI_BIN="$root/codex" FAKE_STATE="$root/state" "$mm" "$@"); }

echo "== monomind $("$mm" --version --json | jq -r .version)"
"$mm" --version --json > "$gold/version.json"
cp "$src/org-sec.json" "$proj/.monomind/orgs/sec.json"
jq '.name = "bad" | .loops = [{"id": "revise", "max_rounds": 3}] | .sections.review.mode = "execution"' \
  "$src/org-sec.json" > "$proj/.monomind/orgs/bad.json"

mmrun org validate sec > "$gold/validate-valid.txt" 2>&1 \
  || { cat "$gold/validate-valid.txt"; echo "::error::monomind org validate rejected the sections org"; exit 1; }
if mmrun org validate bad > "$gold/validate-invalid.txt" 2>&1; then
  echo "::error::monomind org validate accepted a sections org that still has loops"
  exit 1
fi

mmrun org sign sec --yes > /dev/null
timeout 120 env HOME="$home" CODEX_CLI_BIN="$root/codex" FAKE_STATE="$root/state" \
  bash -c "cd '$proj' && '$mm' org run sec -y --auto-approve org_complete" > "$root/run.log" 2>&1 \
  || { tail -20 "$root/run.log"; echo "::error::the scripted sections run did not complete"; exit 1; }

mmrun org events sec > "$gold/events.ndjson"
mmrun org status sec --format json > "$gold/status-stopped.json"
run="$(jq -r .run "$gold/status-stopped.json")"
docs="$proj/.monomind/orgs/sec/docs/$run"
cp "$docs/events.jsonl" "$gold/doc-events.jsonl"
cp "$docs/snapshot.json" "$gold/doc-snapshot.json"
cp "$docs/notices.jsonl" "$gold/doc-notices.jsonl"

echo "== golden tests on the fresh recording"
cd "$repo"
MONOMIND_GOLDEN_DIR="$gold" go test ./internal/monomind/ -run 'Golden|KnownGood' -count=1
echo "== ok: this monomind still matches the recorded shapes"
