#!/usr/bin/env bash
# Build the official monoes.me library artifacts: the seven automation
# packages in automations/ as .mpkg files, the bundled workflow templates and
# the starter orgs in orgtemplates/, plus manifest.json listing them all.
# monoes.me's admin seed script uploads the directory as official items.
#
#   scripts/library-official.sh [OUT]      (make library-official OUT=...)
#
# OUT defaults to ~/scratch/monoes-library/official. Packing runs the CLI
# under a throwaway HOME, so it never touches ~/.monoagent.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="${1:-${OUT:-$HOME/scratch/monoes-library/official}}"
work="$(mktemp -d "${TMPDIR:-/tmp}/library-official.XXXXXX")"
trap 'find "$work" -delete 2>/dev/null || true' EXIT

mkdir -p "$out"
out="$(cd "$out" && pwd)"
cd "$root"

# Keep the Go caches where they were before HOME changes below.
export GOCACHE="${GOCACHE:-$(go env GOCACHE)}" GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
go build -tags devaccount -o "$work/monoagentcli" ./cmd/monoagentcli
# Keep this devaccount build independent of the production enforcement date.
export MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z

mkdir -p "$work/home"
for dir in automations/*/; do
	id="$(basename "$dir")"
	[ -f "$dir/automation.json" ] || continue
	HOME="$work/home" USERPROFILE="$work/home" \
		"$work/monoagentcli" automation pack "$dir" -o "$out/$id.mpkg" >/dev/null
done

go run ./scripts/libraryofficial -root "$root" -out "$out"
echo "wrote $out/manifest.json"
