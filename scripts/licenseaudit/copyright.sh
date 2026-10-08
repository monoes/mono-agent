#!/usr/bin/env bash
# Who still has code in the tree: the copyright audit of the license workstream
# (spec §10 step 1). Prints a Markdown table: for each person in the history,
# whether the owner can relicense their work. The owner's own commits can be
# relicensed by the owner; a bot's version bumps hold no expression; anyone else
# with a line still in the tree must consent, or the line must be replaced.
#
#   bash scripts/licenseaudit/copyright.sh <owner e-mails, comma-separated> [repo]
set -euo pipefail
owners=",$(printf '%s' "${1:?usage: copyright.sh <owner e-mails, comma-separated> [repo]}" | tr 'A-Z' 'a-z'),"
cd "${2:-.}"
echo "## Contributors at commit $(git rev-parse --short HEAD)"
echo
echo "| Contributor | Commits | Files that still hold their lines | Lines | Can the owner relicense |"
echo "|---|---|---|---|---|"
git shortlog -sne HEAD | while IFS=$'\t' read -r commits ident; do
  commits=$(echo $commits)
  email=$(printf '%s' "${ident##*<}" | tr -d '>' | tr 'A-Z' 'a-z')
  case "$owners" in *",$email,"*) echo "| $ident | $commits | all | | yes (the owner) |"; continue ;; esac
  case "$ident" in *"[bot]"*) echo "| $ident | $commits | manifests and lockfiles | | yes (a bot: version bumps hold no expression; confirm) |"; continue ;; esac
  files="" lines=0
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    n=$(git blame -w --line-porcelain HEAD -- "$f" | tr 'A-Z' 'a-z' | grep -c "^author-mail <$email>\$" || true)
    if [ "$n" -gt 0 ]; then files="${files:+$files, }$f"; lines=$((lines + n)); fi
  done < <(git log -i -F --author="<$email>" --name-only --format= | sort -u)
  if [ "$lines" -eq 0 ]; then echo "| $ident | $commits | none | 0 | yes (none of their lines is left) |"
  else echo "| $ident | $commits | $files | $lines | **needs consent** (or replace those lines) |"; fi
done
