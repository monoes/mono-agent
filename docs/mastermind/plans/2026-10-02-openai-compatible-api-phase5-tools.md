# OpenAI-compatible API, phase 5: tool calling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `tools`, `tool_choice`, `tool_calls` and `tool` messages work on `/v1/chat/completions`, streaming and not, on claude and codex, so a client that runs its own tools (a coding assistant, an SDK tool loop) can use the local runtimes.

**Architecture (D18, as amended by the spike).** No process is parked. A request with tools runs one *leg*: an ordinary turn (slot, sandbox, timeout, cleanup as chat) with the tools declared. The leg ends at the FIRST tool call: the event hook records it and cancels the leg's context, so the process group is killed; the answer is one `tool_calls` entry and `finish_reason: "tool_calls"`. The runtime's session id goes into a short-lived, single-use record in memory. The follow-up carrying the `tool` message *resumes* that session when the record matches, and otherwise *replays* the transcript statelessly. Replay is built first and always works; resume is an optimisation that falls back to it.

## Global Constraints

- TDD: a failing test first; then mutation-check each behaviour test (break the code outside the commit, see the test fail at run time, restore).
- Files under 500 lines; conventional commits with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`; never a bare `go build ./cmd/monoagentcli` in the repo root; plain separate git commands.
- No real model call in default tests. Tests that run the real monomind against the fake `codex` (`CODEX_CLI_BIN`) are gated by `MONOMIND_SMOKE=1`; the fake lives in `internal/openaiapi/testdata`.
- No tool name, argument or result in a log line or in an error message. Chat without tools stays as it is.
- Known failing tests on macOS (8) are not regressions (see the spec, section 11).

## Evidence this plan rests on

Spike (143 turns, `scratchpad/p5_spike/REPORT.md`): declared tool used first time on claude 9/10 (then 3/3 more), codex 9/10, agy 8/10 (native tools beat the declared one: not served). claude denied all 15 native attempts; codex made 31/31 native attempts and with `Access: read` used the declared tool 2/2 (0/4 without). Calls arrive 0.6 s apart on claude and together on codex. Cancel at the call: claude returns in 0.01-0.03 s, nothing survives. monomind keeps only the top-level schema properties (descriptions and nested keys are dropped) and a numeric `enum` rejects every call; a rule that lived only in a description was kept when the whole schema was folded into the tool description. A 54-character tool name worked, 60 failed (64 limit including `mcp__org__`). The `tool_choice` line worked 9/9 (named) and 5/6 (required). `tool_round_cap` and claude's max-turns need a delivered tool result, which a leg never has.

Follow-up from the lead: claude resume in a different folder works (9 legs, 0 failures); **two calls in one leg resumed with both results pasted worked 1/3** (the CLI writes a rejected `tool_result` and "Continue from where you left off" into the session when it is cancelled at the call), so a leg returns exactly one call and the model asks for the rest in the next round; `parallel_tool_calls` is accepted and treated as false.

Probed here against monomind 2.22.0 (fake codex under the real monomind, real claude/codex for the unknown-session errors):
- **D20.** `Access: read` with `Sandbox: workspace-write` and `RequireSandbox` is accepted: start event `access: read`, `native_sandbox: read-only`, `sandbox_status: sandboxed`, codex argv `--sandbox read-only`. `ClassFromNativeSandbox` maps `read-only` to `sandboxed`, so the start-event check keeps passing; the sandbox is stricter, not weaker. No stop was needed.
- A resume of an unknown session ends `runner-error`, exit 1, `done` seen, no assistant output, on claude ("No conversation found") and codex ("no rollout found"). The `session` event echoes the requested id even then.
- On resume monomind does not repeat the system file or the fence protocol for codex: the resumed thread keeps the first leg's. So a record is only resumed when the declared tools are identical to the ones it was made with.
- `--max-turns` is accepted for every runtime (codex's scan says `max_turns: false`, and the turn ran).
- After a cancel there is no `done` event on codex, so the handler waits for `done` or a short grace before it returns (it must return on `ctx.Done()`: Exec waits for handlers).

## Decisions this plan adds to spec section 10

1. **Serial legs** (above); no collect window, no batching, no de-duplication.
2. **Resume conditions** (all of them, else replay): the follow-up's last assistant message has exactly one tool call; its id has a live record; the record is of the same key and profile, the same model, the same tool-name and the same declared tools (hash); a tool message for it follows; the record is single-use (a retry replays). Records hold ids, the name, the session id, the tools hash and the expiry, never arguments or results: TTL 10 minutes, 1,024 in all, 64 per key.
3. **Resume falls back to replay** in the same request when the resume leg ends `runner-error` or `bad-frame` (or vanishes without `done`) before any text or call. No CLI message is matched, and quota, auth, rate-limit and timeout errors are never retried.
4. **Servable predicate**, one function for the refusal and for the `tools` capability: the runtime is in `MONOAGENT_API_TOOL_RUNTIMES` (default `claude,codex`) and (its class is chat-only, or monomind has `agent-exec-access-read` and the runtime lists `read` in `access_modes`). A non-chat-only runtime runs the leg with `Access: read`. Otherwise 400 `unsupported_parameter` on `tools` (the message says which runtimes serve tools).
5. **Tool choice:** `none` passes no tools (a plain turn; tool history is rendered); a named function or `required` adds a best-effort system line. `functions`, `function_call` and the `function` role stay rejected, with a pointer to `tools`.
6. **Prompts.** First leg: the chat prompt. Replay: a transcript where an assistant call is `(called the function NAME with arguments ARGS)` and a result is `[tool NAME (ID)]` (the format measured 7/7), ending "Use the function results above; call a function again only if you still need one". Resume: a short statement that the caller ran the tool (the session holds a rejected-result residue), then `Result of NAME (call ID): TEXT`, then any later user message.
7. **Schema:** the ToolSpec keeps the flat top-level `properties` and `required`; the full `parameters` schema, compact, is appended to its description. Arguments are checked in Go against the declared schema (small subset validator, no dependency); a bad call is still returned and only a count reaches the log.

## File structure (new files; chat.go gets one branch point)

- `tools_types.go` wire types and limits; `tools_validate.go` request validation; `tools_schema.go` ToolSpec building and the argument check; `tools_translate.go` system line, replay transcript, resume prompt; `tools_leg.go` event hook and handler; `tools_store.go` the continuation store; `tools_chat.go` plan (first, resume, replay), non-stream answer; `tools_stream.go` SSE tool-call chunks; `tools_config.go` the runtime list; testdata `fake-codex.py`.
- Modify `translate.go` (validateChat), `types.go` (`Message`, `ChatRequest`, `delta`), `runner.go` (turn fields: tools, hook, resume, access, max turns), `catalog.go` (`ModelInfo.ReadAccess`), `config.go` (`ToolRuntimes`), `gateway.go` (the store), `chat.go`.

## Tasks

### Task 1: this plan. Commit `docs(api): the plan of phase 5, tool calling`.

### Task 2: types, parsing and validation
- [ ] Table tests: 64 tools max; names `^[A-Za-z0-9_-]{1,54}$`, unique; non-function type; `parameters` not an object or too large; non-string `enum` in a top-level property (nested ones are fine); `tool_choice` shapes, undeclared name, `required` without tools; assistant `tool_calls` shape; `tool` message without or with an unknown `tool_call_id`, over the size cap; `functions`, `function_call` and the `function` role pointing at `tools`; `"tools": []` and `tool_choice` `none`/`auto` still accepted; every error has its `param` and echoes no name.
- [ ] Retarget only the rows of the existing tests that cannot hold (tools and the tool role in `TestValidateChatRejections`, tools in `TestChatRejectsBeforeSpawningAnything`).
- [ ] Commit `feat(openaiapi): validate tools, tool_choice and tool messages`.

### Task 3: translation
- [ ] Tests: ToolSpec (folded schema, flat properties, no description, no properties); the system line per choice; replay transcript with one and with many calls, content and null content, results by id, and the end-of-prompt wording; resume prompt; per-runtime options (`Access: read` only for a non-chat-only class, `MaxTurns` headroom).
- [ ] Commit `feat(openaiapi): translate tools and tool history into a turn`.

### Task 4: the leg driver
- [ ] Tests with a fake Exec that emits events then calls the handler like Exec: ends at the first call and answers promptly; two calls at once give the first only; text before the call is kept, text after is dropped; the session id is read from the result of a cancelled leg; the handler returns on cancel and on `done`; a leg that never calls is an ordinary answer; denied turns, a client that leaves and shutdown classified in the order 499, 403, call, existing mapping; no leftover goroutine.
- [ ] Commit `feat(openaiapi): run a turn as a leg that ends at its first tool call`.

### Task 5: the continuation store and the decision
- [ ] Tests: put/take, TTL (a moved clock), single use, the 64 and 1,024 caps evict the oldest, another key or profile or model or tools hash or name finds nothing, concurrent takes give one winner; the decision table (resume, replay for each failed condition); a resume that fails falls back to replay once and a quota error does not.
- [ ] Commit `feat(openaiapi): continuation records and the choice between resume and replay`.

### Task 6: handlers
- [ ] Tests over HTTP: non-stream tool call; stream chunks (index, id, name, then arguments, then finish `tool_calls`, `[DONE]`, no usage); the whole loop (call, result, answer) by resume and by replay; text before the call as `content`; `parallel_tool_calls`; a runtime that answers without calling; refused runtimes (400 for antigravity, spawns nothing); headers; a revoked key's records unreachable; client disconnect.
- [ ] Wire `chat.go` with one branch point, keeping chat without tools identical; a real-monomind test with the fake codex (`MONOMIND_SMOKE=1`): call, result, answer, and the start event `read-only` accepted by a sandboxed policy.
- [ ] Commit `feat(openaiapi): tool calls on chat completions, streaming and not`.

### Task 7: after the rebase onto `feat/openai-api-images`
- [ ] `auto` with tools picks among servable candidates (none: the same 404); the `tools` capability on `GET /v1/models`; the `tools=` and `leg=` log fields; `api models --json` gets a `tools` flag per model.

### Task 8: docs
- [ ] `AGENTS.md`, `SECURITY.md` (what tool calling changes: the caller's tools run on the caller's side, results are untrusted prompt text, the CLIs' sessions hold arguments and results, codex runs read-only; what it does not), `CHANGELOG.md`, `openapi.yaml` (lint with `npx --yes @redocly/cli@2.49.0 lint`), `examples/openai-api-quickstart.md`, `ref api`, spec section 10 as built.

### Task 9: the live canary (`MONOAGENT_LIVE_API_TESTS=1`), once per installed runtime at the very end: a call, a result and an answer that uses it, through HTTP; skipped on 503, 429 or 502.

## Risks

Parallel calls cost a round (the model asks again). The CLI residue on resume may confuse a model: replay is the fallback and the canary measures it. Tools declared with a numeric `enum` are refused until monomind takes them. A context key with tools lets a captured page steer which calls are proposed; the client decides whether to run them (SECURITY.md says so).
