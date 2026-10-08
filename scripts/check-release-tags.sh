#!/usr/bin/env bash
# Fails when a Go binary that is about to ship was built with the devaccount tag (spec D11, D24):
# the tag trusts a development signing key anyone can sign with. It reads the build settings every
# Go binary carries (`go version -m`), whatever OS it was built for.
#
#   scripts/check-release-tags.sh [--min N] <file-or-folder>...
#
# A folder is searched; a .tar.gz, .tgz or .zip is unpacked and searched too, so the desktop app's
# executable and the CLI bundled with it are covered. Files that are not Go binaries are ignored.
# --min N fails when fewer than N Go binaries were found, so a release whose layout changed cannot
# pass by checking nothing.
set -euo pipefail

forbidden="devaccount"
min=1
if [ "${1:-}" = "--min" ]; then
  min="${2:?--min needs a number}"
  shift 2
fi
[ "$#" -gt 0 ] || { echo "usage: check-release-tags.sh [--min N] <file-or-folder>..." >&2; exit 2; }
command -v go >/dev/null || { echo "check-release-tags: go is required" >&2; exit 2; }

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
checked=0
bad=0

# check inspects one file: a Go binary is counted and judged, an archive is unpacked and each
# file inside is checked, anything else is skipped. label is the name to report.
check() {
  local f="$1" label="$2" info tags dir
  case "$f" in
    *.tar.gz | *.tgz | *.zip)
      dir="$(mktemp -d "$scratch/unpack.XXXXXX")"
      if [ "${f##*.}" = "zip" ]; then unzip -q "$f" -d "$dir"; else tar -xzf "$f" -C "$dir"; fi
      while IFS= read -r inner; do check "$inner" "$label:${inner#"$dir"/}"; done < <(find "$dir" -type f | sort)
      return
      ;;
  esac
  info="$(go version -m "$f" 2>/dev/null)" || return 0
  [ -n "$info" ] || return 0
  checked=$((checked + 1))
  tags="$(printf '%s\n' "$info" | awk -F'\t' '$2 == "build" && $3 ~ /^-tags=/ { sub(/^-tags=/, "", $3); print $3 }')"
  case ",$tags," in
    *",$forbidden,"*)
      echo "::error::$label was built with the $forbidden tag (-tags=$tags): a release must never carry it" >&2
      bad=$((bad + 1))
      ;;
    *) echo "ok: $label (tags: ${tags:-none})" ;;
  esac
}

for arg in "$@"; do
  if [ -d "$arg" ]; then
    while IFS= read -r f; do check "$f" "${f#"$arg"/}"; done < <(find "$arg" -type f | sort)
  elif [ -f "$arg" ]; then
    check "$arg" "$(basename "$arg")"
  else
    echo "check-release-tags: $arg does not exist" >&2
    exit 2
  fi
done

if [ "$bad" -gt 0 ]; then
  echo "check-release-tags: $bad binary(ies) carry the $forbidden tag" >&2
  exit 1
fi
if [ "$checked" -lt "$min" ]; then
  echo "::error::check-release-tags: found $checked Go binaries, expected at least $min: the release layout changed, or an archive did not unpack" >&2
  exit 1
fi
echo "check-release-tags: $checked Go binaries, none carries the $forbidden tag"
