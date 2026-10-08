#!/usr/bin/env bash
# Copies every file under SRC into DEST by base name, the way the release workflow flattens the
# downloaded build artifacts. It fails when two files share a base name, because a plain `cp`
# would let the later one silently replace the earlier.
#
#   scripts/release-flatten.sh SRC DEST [EXCLUDED-SUBDIR-OF-SRC...]
set -euo pipefail

[ "$#" -ge 2 ] || { echo "usage: release-flatten.sh SRC DEST [EXCLUDED-SUBDIR...]" >&2; exit 2; }
src="${1%/}"
dest="$2"
shift 2
mkdir -p "$dest"

while IFS= read -r -d '' f; do
  skip=0
  for ex in "$@"; do
    case "$f" in "$src/$ex"/*) skip=1 ;; esac
  done
  [ "$skip" -eq 0 ] || continue
  base="$(basename "$f")"
  if [ -e "$dest/$base" ]; then
    echo "::error::duplicate artifact file name '$base' ($f): refusing to overwrite $dest/$base" >&2
    exit 1
  fi
  cp "$f" "$dest/$base"
done < <(find "$src" -type f -print0)
