#!/usr/bin/env bash
# Smoke test for `monoagentcli mcp --tasks-only` over stdio, against a throwaway
# HOME and database: initialize, tools/list (exactly the task_* tools), the
# mutating verbs gated by --allow-mutations, claim -> comment -> finish, a
# non-task tool refused by name, the CLI showing what the agent did, and the
# --api-only combination refused. Never touches the real ~/.monoagent.
#
#   go build -tags devaccount -o /tmp/monoagentcli ./cmd/monoagentcli && scripts/tasks-mcp-smoke.sh /tmp/monoagentcli
set -euo pipefail

cli="${1:?usage: tasks-mcp-smoke.sh <path to monoagentcli>}"
command -v jq >/dev/null || { echo "tasks mcp smoke: jq is required"; exit 1; }
root="$(mktemp -d)"
home="$root/home"
mkdir -p "$home/.claude" "$root/tmp"
trap 'rm -r "$root" 2>/dev/null || true' EXIT
export HOME="$home" USERPROFILE="$home" TMPDIR="$root/tmp" \
  XDG_CONFIG_HOME="$home/.config" XDG_CACHE_HOME="$home/.cache" XDG_DATA_HOME="$home/.local/share" XDG_STATE_HOME="$home/.local/state"
db="$home/.monoagent/monoagent.db"
# Use the devaccount build above: this smoke exercises task tools, independently of the gate.
# A clean environment, as in the operator's own terminal: the "straight to Ready" gate refuses when an
# agent marker (CLAUDECODE, AI_AGENT, ...) is set, and this script may itself run inside an agent's shell.
run() { env -i "PATH=$PATH" "MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z" "HOME=$home" "TMPDIR=$root/tmp" "$cli" --db-path "$db" "$@"; }
fail() { echo "tasks mcp smoke: $*" >&2; exit 1; }

# rpc <extra mcp flags> : reads request lines on stdin, prints responses
rpc() { run mcp --tasks-only "$@"; }
init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"smoke client","version":"1"}}}'
call() { printf '{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"%s","arguments":%s}}\n' "$1" "$2" "$3"; }
resp() { jq -c "select(.id==$2)" <<<"$1"; } # one response by id

echo "== the operator adds a task straight to Ready"
run task add "smoke test the task tools" --notes "from the smoke script" --ready >"$root/add.out" 2>&1 || fail "task add: $(cat "$root/add.out")"

echo "== without --allow-mutations: only the read tools are listed, a verb refuses"
out="$({ echo "$init"; echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; call 3 task_claim '{"next":true}'; } | rpc)"
names="$(resp "$out" 2 | jq -r '[.result.tools[].name]|sort|join(",")')"
[ "$names" = "task_get,task_list,task_next" ] || fail "read-only tools/list: $names"
resp "$out" 3 | jq -e '.result.isError==true and (.result.content[0].text|contains("--allow-mutations"))' >/dev/null || fail "task_claim not refused without --allow-mutations"

echo "== with --allow-mutations: exactly the eight task_* tools; claim, comment, finish; a workflow tool refused"
# The server answers each request from its own goroutine, so a request is sent only after the previous
# one was answered (bounded wait), not after a guessed pause.
fifo="$root/in"; outf="$root/out"; mkfifo "$fifo"; : >"$outf"
rpc --allow-mutations <"$fifo" >"$outf" 2>"$root/rpc.err" &
srv=$!
exec 3>"$fifo"
await() { # id : wait up to 20s for the response with this id
  local i
  for i in $(seq 1 200); do
    jq -e "select(.id==$1)" "$outf" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  fail "no response to request $1 within 20s: $(cat "$root/rpc.err")"
}
echo "$init" >&3; await 1
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' >&3; await 2
call 3 task_claim '{"next":true}' >&3; await 3
call 4 task_comment '{"id":1,"text":"working on it"}' >&3; await 4
call 5 task_finish '{"id":1,"result":"done in the smoke script"}' >&3; await 5
call 6 workflow_list '{}' >&3; await 6
exec 3>&-
wait "$srv" || true
out="$(cat "$outf")"
names="$(resp "$out" 2 | jq -r '[.result.tools[].name]|sort|join(",")')"
[ "$names" = "task_add,task_claim,task_comment,task_finish,task_get,task_list,task_next,task_release" ] || fail "tools/list: $names"
resp "$out" 3 | jq -e '.result.isError!=true and (.result.content[0].text|contains("agent:smoke-client#"))' >/dev/null || fail "task_claim: $(resp "$out" 3)"
resp "$out" 4 | jq -e '.result.isError!=true' >/dev/null || fail "task_comment: $(resp "$out" 4)"
resp "$out" 5 | jq -e '.result.isError!=true' >/dev/null || fail "task_finish: $(resp "$out" 5)"
resp "$out" 6 | jq -e '.result.isError==true and (.result.content[0].text|contains("not served"))' >/dev/null || fail "workflow_list not refused: $(resp "$out" 6)"

echo "== the board as the command shows it: the agent's work waits in review"
run --json task show 1 | jq -e '.task.status=="review" and ([.events[].kind]|index("claimed")!=null) and ([.events[].kind]|index("result")!=null)' >/dev/null || fail "task show 1: $(run --json task show 1)"

echo "== delegation: off by default, an agent cannot turn it on, task_approve exists only while the operator allows it"
run --json task add "captured text" >"$root/inbox.out" 2>"$root/inbox.err" || fail "task add (inbox): $(cat "$root/inbox.out")"
inbox_id="$(jq -r '.task.id' "$root/inbox.out")"
if run task agents allow --approve --as bot >/dev/null 2>"$root/err"; then fail "an agent allowed itself to approve"; fi
grep -q "only the operator" "$root/err" || fail "agents allow by an agent: $(cat "$root/err")"
run --json task agents show | jq -e '.view==false and .approve==false' >/dev/null || fail "delegation is not off by default"
out="$({ echo "$init"; echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; } | rpc --allow-mutations)"
[ "$(resp "$out" 2 | jq -r '[.result.tools[].name]|sort|join(",")')" = "task_add,task_claim,task_comment,task_finish,task_get,task_list,task_next,task_release" ] || fail "tools/list changed while delegation is off"
run task agents allow --approve >/dev/null || fail "operator: task agents allow --approve"
out="$({ echo "$init"; echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; call 3 task_approve "{\"ids\":[$inbox_id]}"; } | rpc --allow-mutations)"
resp "$out" 2 | jq -e '[.result.tools[].name]|index("task_approve")!=null' >/dev/null || fail "task_approve not listed while allowed"
resp "$out" 3 | jq -e '.result.isError!=true' >/dev/null || fail "task_approve: $(resp "$out" 3)"
run --json task show "$inbox_id" | jq -e '.task.status=="ready" and (.events[-1].actor|startswith("agent:smoke-client#")) and (.events[-1].note|contains("delegated"))' >/dev/null || fail "the approval is not recorded as delegated: $(run --json task show "$inbox_id")"
run task agents deny >/dev/null || fail "operator: task agents deny"
out="$({ echo "$init"; echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; call 3 task_approve '{"ids":[1]}'; } | rpc --allow-mutations)"
resp "$out" 2 | jq -e '[.result.tools[].name]|index("task_approve")==null' >/dev/null || fail "task_approve still listed after deny"
resp "$out" 3 | jq -e '.result.isError==true' >/dev/null || fail "task_approve worked after deny"

echo "== --tasks-only with --api-only is refused"
if run mcp --tasks-only --api-only </dev/null >/dev/null 2>"$root/err"; then fail "--tasks-only --api-only started"; fi
grep -q "cannot be combined" "$root/err" || fail "refusal text: $(cat "$root/err")"

echo "tasks mcp smoke: ok"
