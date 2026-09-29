# Coder mode on every runtime — plan (2026-09-29)

Goal: coder mode (full-access chat in a folder) runs on every coding runtime
monomind supports, not only `claude`, with the same services Claude gets —
as far as each runtime's CLI can give them — and the UI says honestly where
a runtime falls short.

Two repos, one contract. monomind ships first (runner work); mono-agent
feature-detects it, so both are built in parallel against the contract below.

## Scope

| Runtime | In scope | Installed here | Notes |
|---|---|---|---|
| claude | already | yes | reference implementation |
| codex | yes (tier 1) | yes | `--dangerously-bypass-approvals-and-sandbox`, rich JSON events |
| opencode | yes (tier 1) | yes | permission override + SSE tool parts |
| antigravity | yes (tier 1) | yes | already `--dangerously-skip-permissions` |
| kimicode | yes (tier 1) | no | full mode drops the forced agent file / empty skills dir |
| pi | yes (tier 1, wave 2) | yes | pi 0.87: session ids, matched tool events, thinking, cost (monomind#381) |
| grok, qwen, copilot, crush | yes (tier 2) | grok, copilot, crush | already auto-approve; best-effort tool events |
| cline | yes (new runner, wave 2) | no | `cline --json` / ACP; hub daemon cleanup (monomind#382) |
| aider | yes (new runner, wave 2) | no | Python shim for real shell autonomy (monomind#383) |
| dsh (DeepSeek Harness) | yes (new runner, wave 2) | no | `DSH_PERMISSION_MODE=danger-full-access`, `--json` (monomind#384) |
| vercel | no | — | no native tools: not a coding agent |
| hermes, qwen-rpc, pi-rpc | no | — | no resume / no tool events; rpc variants duplicate their CLI sibling |
| gemini, cursor | later | — | no runner exists in monomind; separate issue |

Issues: epic monoes/monomind#385 (runner issues #377–#384); mono-agent #222 (backend), #223 (GUI).

## Services (what "same as Claude" means)

| # | Service | claude today | target for other runtimes |
|---|---|---|---|
| S1 | Full access: no approvals, no sandbox, real cwd | yes | native yolo/bypass flag or permission override |
| S2 | User's normal setup (their config, MCP servers, AGENTS.md/skills) | `--settings user,project,local` | runner does not isolate the CLI's own config in full mode; `status` event lists what loaded |
| S3 | Appended system prompt | preset + append | first-prompt / `system` injection (already exists) |
| S4 | Streamed native tool calls, start + matched end, normalized kind | full | `full` where the CLI reports ends (codex, opencode, antigravity, kimi); `start-only` otherwise |
| S5 | Resume by session id | yes | where the CLI supports it (reported per runtime) |
| S6 | Model + effort | yes | `agent exec --effort`, mapped per runtime |
| S7 | Limits: timeout, max turns, USD budget | yes | timeout everywhere; max turns / budget only where reported |
| S8 | Cancel kills the whole process tree, reports background pids | yes | process-group spawn + group kill for every vendor runner |
| S9 | Folder init | `monomind init --target claude` | `--target <runtime's init target>` |
| S10 | Full-access audit log | yes | every runtime |
| S11 | Org roles with full access | claude only | any full-access runtime; agent-context markers for each CLI |

## Contract (monomind → mono-agent)

1. `agent scan --json` entries gain (static `RunnerSpec` metadata, additive):
   - `full_access: bool` — flipped to `true` for every in-scope runtime.
   - `tool_activity_fidelity` — updated as runners gain end events.
   - `resume: bool`, `effort: bool`, `max_turns: bool`, `reports_cost: bool`
   - `init_target: string | null` — the `monomind init --target` value for the runtime's setup files.
2. `agent exec --effort <level>` (new capability `agent-exec-effort`). Mapped per
   runtime: claude → SDK `effort`; codex → `-c model_reasoning_effort=<level>`;
   others → ignored with a `status` notice. mono-agent passes `--effort` when the
   capability is present, else falls back to today's `CLAUDE_EFFORT` env for claude.
3. `--access full` accepted for any runtime whose `full_access` is true
   (new capability `agent-exec-full-access-any`). `--settings` on a non-claude
   runtime means "don't isolate the CLI's own config" and emits a `status`
   event naming what the CLI will load.
4. `tool_activity` start events gain `kind`:
   `shell | edit | write | read | search | web | mcp | task | todo | patch | other`.
   Canonical `input` keys per kind (runners translate native shapes):
   - shell: `{command, description?, cwd?}`; end `output` + `exit_code?`
   - edit: `{file_path, old_string, new_string}`
   - write: `{file_path, content}`
   - read: `{file_path}`
   - search: `{pattern, path?}`
   - patch: `{files: [{file_path, action: add|update|delete, diff?}]}`
   - mcp: `{server, tool, arguments}`
   - web: `{url?, query?}`
   - other: raw input
   Claude's events keep their native `input` and just gain `kind` (mapped from the tool name).
   The original tool name stays in `name`.
5. Cancel/timeout: every runner is spawned as a process-group leader; `result`
   carries `background_pids` for all runtimes.

## monomind work (repo monoes/monomind, branch feat/coder-all-runtimes)

- **MM1 core** — `runner-registry.ts` spec fields + scan output; `agent-exec-access.ts`
  generic gate; `--effort` flag + `AgentRunArgs.effort` plumbing; capabilities
  `agent-exec-full-access-any`, `agent-exec-effort`; `tool-activity.ts` `kind`
  mapping (tool-name table incl. Claude names); audit log for every runtime;
  org role full access (`access-grant.ts`, `access-validate.ts`,
  `org-subcommands-role.ts`) generic; process-group spawn/kill helper in
  `agent-runner-types.ts`; `doc/agent-exec-protocol.md` rev 13;
  `doc/concepts/coder-mode-security.md` updated (regression test now pins the
  allowed set to `full_access` runtimes, not `claude`).
- **MM2 codex** — full mode: `--dangerously-bypass-approvals-and-sandbox`, no
  config isolation; parse `command_execution`, `file_change`/`patch_apply`,
  `mcp_tool_call`, `web_search` item start/complete into matched
  `tool_activity` (fidelity → full); effort via `-c model_reasoning_effort`.
- **MM3 opencode** — full mode: permission override to `allow` for the served
  instance (config env / server config), answer any permission event with
  "always"; SSE `message.part` tool parts → matched `tool_activity`
  (fidelity → full); session resume.
- **MM4 CLI runners** — antigravity, kimicode, grok, qwen, copilot, crush, pi:
  full-mode flags and setup (kimicode: skip the forced agent file / empty
  skills dir in full mode); real end events where the stream carries them;
  canonical inputs; resume/effort flags reported honestly.

Release: monomind minor (2.19.0) after all tests pass.

## mono-agent work (repo monoes/mono-agent, branch feat/coder-all-runtimes)

- **MA1 backend** — `internal/monomind`: `ScanEntry` new fields; per-runtime
  coder readiness (`CoderRuntimes(scan, caps)`); `InitWorkspace(target)`;
  `--effort` when capable (drop the duplicated `CLAUDE_EFFORT` env path);
  agent-context markers for every CLI (org access guard).
  `cmd/monoagentcli`: remove `coderRuntime`; `coder status` returns a
  `runtimes` list (`{id, installed, fullAccess, ready, toolActivity, resume,
  effort, maxTurns, reportsCost, initTarget}`, keep `runtime` = "claude" for
  old GUIs); `--mode coder --runtime X` validated against that list;
  `coderTurn.runtime` from the conversation; runtime-neutral system prompt and
  status text ("Starting codex…"); coder conversations store effort;
  journal: tool-kind aware (file existed / exit code from canonical keys),
  fidelity-aware close (start-only calls close as `ok` unknown, not cancelled).
- **MA2 GUI** — `app_coder.go` `CreateCoderConversation(runtime, model, effort, cwd, newWorkspace)`;
  frontend: coder mode's runtime picker lists coder-ready runtimes (replaces the
  claude lock); per-runtime readiness text; effort picker for coder;
  `NativeToolCard` renders by `kind` (shell/edit/write/patch/mcp/…) with Claude
  names as fallback; `CoderHeader` init files per runtime; runtime-neutral copy
  in picker, settings section and org full-access dialog; org role full-access
  section gated on the role runtime's `full_access`.
- **MA3 docs** — AGENTS.md coder section, CHANGELOG entry.

## Verification

1. monomind: package test suite (both test roots) + typecheck; fixture tests
   per runner for full-mode args and event parsing.
2. mono-agent: `go build ./... && go test ./... && go vet ./...`; frontend
   `vitest run` + `npm run build`.
3. Live: local monomind build in `~/scratch`, a real `agent exec --access full`
   turn per installed runtime (claude, codex, opencode, antigravity, grok,
   crush, copilot, pi) in a scratch folder — each must list files, write one,
   run a command, and resume. Then the same through `monoagentcli chat`
   with `HOME` isolated for the monoagent DB.

## Risks

- Full access on CLIs whose permission models we don't control (opencode's
  `ask` rules, kimi's agent file) — covered by per-runtime fixture tests and
  the live run.
- Start-only runtimes show less detail; the UI labels fidelity instead of
  pretending.
- Budget is unenforceable where the runtime reports no cost; the UI hides the
  budget field for those runtimes instead of showing a limit that never trips.
