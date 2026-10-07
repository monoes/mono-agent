#!/usr/bin/env bash
# Mutation proof for the task MCP tools (issue #371, plan Task 6 step 3): removes, one at a time, the
# mutation gate, the --tasks-only filter and the per-profile scoping, and requires the internal/mcp
# tests to fail for each. A mutation that survives means a rule is no longer tested. Edits are made
# to a scratch copy of the repository, never the working tree.
#
#   scripts/tasks-mcp-mutation-check.sh
set -euo pipefail

src="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -r "$work" 2>/dev/null || true' EXIT
cp -r "$src/." "$work/"
cd "$work"

survived=0
mutate() { # name file old new
  local name="$1" file="$2" old="$3" new="$4"
  cp "$file" "$file.orig"
  OLD="$old" NEW="$new" python3 - "$file" <<'PY'
import os, sys
f = sys.argv[1]
s = open(f).read()
old = os.environ["OLD"]
if old not in s:
    sys.exit("anchor missing in %s: %s" % (f, old))
open(f, "w").write(s.replace(old, os.environ["NEW"], 1))
PY
  if go test ./internal/mcp -run 'Task|Tasks|Grant' -count=1 >"$work/out.txt" 2>&1; then
    echo "SURVIVED: $name"
    survived=1
  else
    echo "killed:   $name"
  fi
  mv "$file.orig" "$file"
}

mutate "mutating tools callable without --allow-mutations" internal/mcp/tools.go 't.mutating && !s.opts.AllowMutations {' 'false {'
mutate "mutating tools listed without --allow-mutations" internal/mcp/tools.go 't.mutating && !allowMutations {' 'false {'
mutate "--tasks-only no longer narrows the server" internal/mcp/apionly.go 'case s.opts.TasksOnly:' 'case false:'
mutate "--tasks-only serves every tool" internal/mcp/apionly.go 'keep = taskToolNames()' 'return all'
mutate "server follows another profile" internal/mcp/task_tools.go 'store.Profile(ctx, rt.profileID)' 'store.Profile(ctx, "work")'
mutate "task_list ignores the profile" internal/mcp/task_read.go 'store.List(ctx, p.ID,' 'store.List(ctx, "",'
mutate "task_claim ignores the profile" internal/mcp/task_write.go 'store.Claim(ctx, p.ID,' 'store.Claim(ctx, "",'

exit "$survived"
