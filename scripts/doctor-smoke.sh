#!/usr/bin/env bash
# Smoke test for `monoagentcli doctor` (setup & health check) against a
# throwaway HOME: the JSON contract, the required-failure exit code, the
# fix → re-check loop, and `doctor fix`'s NDJSON stream. Runs in CI
# (.github/workflows/ci.yml, job doctor-smoke) and locally:
#
#   go build -o /tmp/monoagentcli ./cmd/monoagentcli && scripts/doctor-smoke.sh /tmp/monoagentcli
set -euo pipefail

cli="${1:?usage: doctor-smoke.sh <path to monoagentcli>}"
home="$(mktemp -d)"
trap 'rm -r "$home" 2>/dev/null || true' EXIT
export HOME="$home" USERPROFILE="$home"
run() { "$cli" --db-path "$home/.monoagent/monoagent.db" "$@"; }
fail() { echo "::error::doctor smoke: $*"; exit 1; }

echo "== fresh HOME: a required check fails, exit 1, valid v1 report"
set +e
run --json doctor --group core > "$home/fresh.json"
code=$?
set -e
[ "$code" -eq 1 ] || fail "fresh HOME: exit $code, want 1"
jq -e '.v == 1' "$home/fresh.json" >/dev/null || fail "report is not schema v1"
jq -e '.results[] | select(.id == "core.db") | .status == "fail" and .required == true and .fix.id == "core.db.migrate" and .fix.safety == "auto"' "$home/fresh.json" >/dev/null \
  || fail "core.db should fail (required) with the auto fix core.db.migrate"
jq -e '.results[] | select(.id == "core.profile") | .status == "skip"' "$home/fresh.json" >/dev/null \
  || fail "core.profile should wait on the database"
jq -e 'all(.results[]; (.id | length > 0) and (.group | length > 0) and (.status | IN("ok","warn","fail","skip","info")))' "$home/fresh.json" >/dev/null \
  || fail "every result needs id, group and a known status"

echo "== doctor --fix: applies the fixes over several passes, exit 0"
run --json doctor --group core --fix > "$home/fixed.json" || fail "doctor --fix exited $?"
for id in core.home core.db core.profile; do
  jq -e --arg id "$id" '.results[] | select(.id == $id) | .status == "ok"' "$home/fixed.json" >/dev/null || fail "$id not ok after --fix"
done
jq -e '[.fixes[] | select(.outcome == "applied") | .id] | (index("core.db.migrate") != null) and (index("core.profile.layout") != null)' "$home/fixed.json" >/dev/null \
  || fail "--fix should report core.db.migrate and core.profile.layout as applied"

echo "== doctor fix <id> --json: NDJSON, ends with done, idempotent"
run --json doctor fix core.db.migrate > "$home/fix.ndjson" || fail "doctor fix exited $?"
jq -se 'all(.[]; .kind | IN("line","done","error")) and (last.kind == "done")' "$home/fix.ndjson" >/dev/null \
  || fail "doctor fix output is not NDJSON ending in done"

echo "== unknown fix: exit 2"
set +e
run doctor fix no.such.fix >/dev/null 2>&1
code=$?
set -e
[ "$code" -eq 2 ] || fail "unknown fix: exit $code, want 2"

echo "== full report: every group renders as valid v1 JSON"
set +e
run --json doctor > "$home/full.json"
set -e
jq -e '.v == 1 and (.results | length) > 5' "$home/full.json" >/dev/null || fail "full report is not a valid v1 report"
jq -r '.results[] | select(.parent == null) | "\(.status)\t\(.id)\t\(.summary)"' "$home/full.json"

echo "doctor smoke: ok"
