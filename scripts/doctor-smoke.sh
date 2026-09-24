#!/usr/bin/env bash
# Smoke test for `monoagentcli doctor` (setup & health check) against a
# throwaway HOME: the JSON contract, the required-failure exit code, the
# fix → re-check loop, and `doctor fix`'s NDJSON stream. Runs in CI
# (.github/workflows/ci.yml, job doctor-smoke) and locally:
#
#   go build -o /tmp/monoagentcli ./cmd/monoagentcli && scripts/doctor-smoke.sh /tmp/monoagentcli
set -euo pipefail

cli="${1:?usage: doctor-smoke.sh <path to monoagentcli>}"
command -v jq >/dev/null || { echo "::error::doctor smoke: jq is required"; exit 1; }
root="$(mktemp -d)"
home="$root/home"
out="$root/out" # reports live outside HOME, so "doctor wrote nothing" can be checked
mkdir -p "$home" "$out"
trap 'rm -r "$root" 2>/dev/null || true' EXIT
# Everything a run could write to goes under the scratch folder, locally too.
export HOME="$home" USERPROFILE="$home" TMPDIR="$root/tmp" \
  XDG_CONFIG_HOME="$home/.config" XDG_CACHE_HOME="$home/.cache" XDG_DATA_HOME="$home/.local/share" XDG_STATE_HOME="$home/.local/state"
mkdir -p "$TMPDIR"
run() { "$cli" --db-path "$home/.monoagent/monoagent.db" "$@"; }
fail() { echo "::error::doctor smoke: $*"; exit 1; }

echo "== fresh HOME: plain doctor writes nothing; a required check fails, exit 1, valid v1 report"
set +e
run --json doctor --group core > "$out/fresh.json"
code=$?
set -e
[ "$code" -eq 1 ] || fail "fresh HOME: exit $code, want 1"
[ -z "$(ls -A "$home")" ] || fail "plain doctor wrote to HOME: $(ls -A "$home" | tr '\n' ' ')"
jq -e '.v == 1 and (.generated_at | type == "string") and (.summary | type == "object")' "$out/fresh.json" >/dev/null || fail "report is not schema v1"
jq -e '.results[] | select(.id == "core.home") | .status == "fail" and .required == true and .fix.id == "core.home.create" and .fix.safety == "auto"' "$out/fresh.json" >/dev/null \
  || fail "core.home should fail (required) with the auto fix core.home.create"
jq -e '.results[] | select(.id == "core.db") | .status == "skip"' "$out/fresh.json" >/dev/null \
  || fail "core.db should wait on the data folder"
jq -e 'all(.results[]; (.id | length > 0) and (.group | length > 0) and (.title | type == "string") and (.summary | type == "string")
        and (.status | IN("ok","warn","fail","skip","info")) and ((.required // false) | type == "boolean")
        and (.fix == null or (.fix.safety | IN("auto","confirm","manual"))))' "$out/fresh.json" >/dev/null \
  || fail "every result needs id, group, title, summary, a known status, and a fix with a known safety"

echo "== doctor --fix: applies the fixes over several passes, exit 0"
run --json doctor --group core --fix > "$out/fixed.json" || fail "doctor --fix exited $?"
for id in core.home core.db core.profile; do
  jq -e --arg id "$id" '.results[] | select(.id == $id) | .status == "ok"' "$out/fixed.json" >/dev/null || fail "$id not ok after --fix"
done
jq -e '[.fixes[] | select(.outcome == "applied") | .id] | (index("core.home.create") != null) and (index("core.db.migrate") != null) and (index("core.profile.layout") != null)' "$out/fixed.json" >/dev/null \
  || fail "--fix should report core.home.create, core.db.migrate and core.profile.layout as applied"

echo "== doctor fix <id> --json: NDJSON, ends with done, idempotent"
run --json doctor fix core.db.migrate > "$out/fix.ndjson" || fail "doctor fix exited $?"
jq -se 'all(.[]; .kind | IN("line","done","error")) and (last.kind == "done")' "$out/fix.ndjson" >/dev/null \
  || fail "doctor fix output is not NDJSON ending in done"

echo "== unknown fix: exit 2"
set +e
run doctor fix no.such.fix >/dev/null 2>&1
code=$?
set -e
[ "$code" -eq 2 ] || fail "unknown fix: exit $code, want 2"

echo "== setup with no terminal: exit 0, NDJSON progress, nothing that needs a yes is applied"
setup_home="$root/setup-home"
mkdir -p "$setup_home"
set +e
HOME="$setup_home" USERPROFILE="$setup_home" timeout 120 "$cli" --db-path "$setup_home/.monoagent/monoagent.db" --json setup \
  < /dev/null > "$out/setup.json" 2> "$out/setup.ndjson"
code=$?
set -e
[ "$code" -eq 0 ] || fail "setup with no terminal: exit $code, want 0 (124 = it hung)"
jq -e '.v == 1' "$out/setup.json" >/dev/null || fail "setup report is not schema v1"
# stderr also carries plain log lines (a CI runner has no keychain, so the
# database setup warns there); the progress events are the JSON lines.
grep '^{' "$out/setup.ndjson" > "$out/setup.events" || fail "setup streamed no progress events"
jq -se 'length > 0 and all(.[]; .kind | type == "string")' "$out/setup.events" >/dev/null || fail "setup progress events are not NDJSON"
auto_ok='["core.home.create","core.db.migrate","core.profile.layout"]'
jq -e --argjson ok "$auto_ok" '([.fixes[] | select(.outcome == "applied") | .id] - $ok) | length == 0' "$out/setup.json" >/dev/null \
  || fail "setup with no terminal applied a fix that needs a yes: $(jq -c '[.fixes[] | select(.outcome == "applied") | .id]' "$out/setup.json")"

echo "== full report: every group renders as valid v1 JSON, and nothing required fails after --fix"
set +e
run --json doctor > "$out/full.json"
code=$?
set -e
[ "$code" -eq 0 ] || fail "full report after --fix: exit $code; required failures: $(jq -c '[.results[] | select(.required and .status == "fail") | .id]' "$out/full.json")"
jq -e '.v == 1 and (.results | length) > 5' "$out/full.json" >/dev/null || fail "full report is not a valid v1 report"
jq -r '.results[] | select(.parent == null) | "\(.status)\t\(.id)\t\(.summary)"' "$out/full.json"

echo "doctor smoke: ok"
