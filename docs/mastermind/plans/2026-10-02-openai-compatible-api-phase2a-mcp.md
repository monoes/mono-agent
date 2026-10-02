# OpenAI-compatible API, phase 2a: MCP tools for API key management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `monoagentcli mcp` serves five tools over the profile's API keys and the models `/v1` would serve (spec §8.3): `api_key_list` and `api_models_list` (read-only), `api_key_create`, `api_key_update` and `api_key_revoke` (mutating, only with `--allow-mutations`). `api_key_create` returns the key once (D9).

**Architecture:** One new file in `internal/mcp` registers the family in `allTools()`. Every tool uses the MCP server's profile (`rt.profileID`) and `internal/apikeys.Store`, whose errors (`ErrInvalidName`, `ErrNameTaken`, `ErrNotFound`) are returned as they are: no validation is copied. `api_models_list` needs what `api models` computes, which lives in `package main`, so that part moves to `internal/openaiapi` and the CLI calls it too.

**Tech Stack:** Go; `internal/mcp`, `internal/apikeys`, `internal/openaiapi`; `internal/testdb`; a fake monomind script (`MONOMIND_BIN`) as the CLI tests use.

**Base:** branch `feat/openai-api-jev-auto` at `eab85b0d` (it added `--auto-confinement` at `d0f9fb16`, and the breaker of the Jev questions after it). `api models --json` there carries `policy.auto_confinement`, `models[].auto_allowed` and `auto.{confinement,candidates,held_back}`; the shared report and the tool carry them too.

## Global Constraints

- TDD: the failing test first, seen failing for the right reason; then mutation-check every behaviour test (break the production code outside the commits, see the test fail at run time, restore).
- Files under 500 lines; conventional commits with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`; plain separate git commands; no push, no `gh`.
- Never run a bare `go build ./cmd/monoagentcli` in the repo root. No real model call, no real monomind, no secret printed or logged. `testdb.Path(t)` for databases.
- The CLI's output stays byte-identical and its tests are not edited.
- Known failing tests on macOS (8) are not regressions; run with and without `-tags nosocial`.

## Decisions this plan adds to spec §8.3

1. **Inputs and results mirror the CLI's `--json`.** `api_key_list {include_revoked}` returns `[]apikeys.Key`; no `all_profiles` and no `show` (keys are scoped to the MCP profile). `api_key_create {name, context}` returns the key's metadata plus `key`. `api_key_update {id, name?, context?}` and `api_key_revoke {id}` take an id or the name of an active key, and return the key's metadata. `api_models_list {for?, confinement?, context_confinement?, auto_confinement?}` (the flags of `api models`) returns the document of `api models --json`.
2. **The key appears once.** Only in `api_key_create`'s `key` field. Every other result and every error of the key tools is metadata or static text, and none echoes the `id` argument (a caller may paste a key there). Nothing in the new code logs.
3. **Another profile's key is `api key not found`**, the same text as an unknown id.
4. **No `confirm` on revoke** (like `secret_delete`): the host gates on `destructiveHint`. Annotations: the two reads `readOnlyHint` + `idempotentHint`; create and update `readOnlyHint:false`; revoke `readOnlyHint:false, destructiveHint:true`.
5. **`policy.source` is `mcp`** for the tool (`shell` for the CLI): the policy comes from the MCP server's own flags and environment, which a running `/v1` server may not share.
6. **Not served in grant mode.** An org role's tool provider cannot mint or revoke keys.
7. **The shared part** is the data and the rules: the report types (field order, tags and `omitempty` verbatim), the pure `NewModelsReport` (the row marks, the candidate count, the "at least one model" override, what an unavailable auto clears), the four parsers (`ListenerAddr`, `EffectivePolicy`, `EffectiveContextMax`, `EffectiveAutoMax`) and `LoadModels` (the production catalog). Parsers return plain errors; the CLI and the tool prefix them with their own argument names, so the CLI's messages do not change. `autoNote` is presentation: it stays in `package main` as a plain function, since a method cannot stay on an alias of another package's type.
8. **The secrets doctrine has no exceptions list in AGENTS.md**, only "values are never returned by any tool" on the `secret_list` line. The exception goes there (vault values; the one secret a tool returns is `api_key_create`'s key, once) and into the mutating list; no new section, and the `secret_list` tool description stays (it is about vault values, and chat shares it).

## File Structure

- Create `internal/openaiapi/modelsreport.go` (+ `modelsreport_test.go`): the report types (`ModelsReport`, `ModelReport`, `AutoReport`), `NewModelsReport`, `LoadModels`, `ListenerAddr`, `EffectivePolicy`, `EffectiveContextMax`, `EffectiveAutoMax`.
- Modify `cmd/monoagentcli/api_models.go` (aliases for the three types, thin wrappers `representativeAddr`, `effectivePolicy`, `effectiveContextMax`, `effectiveAutoMax`, `autoNote(a)` as a function, `RunE` calls the shared code) and `api_status.go` (`st.Auto.autoNote()` becomes `autoNote(st.Auto)`).
- Create `internal/mcp/apikeys_tools.go`; modify `internal/mcp/tools.go` (`allTools()` appends the family) and the `AllowMutations` comment in `server.go`.
- Tests: `internal/mcp/apikeys_tools_test.go` (a server on `testdb.Path`; ordered calls through `callTool`, which is what `tools/call` runs; one `io.Pipe` session, each request sent after the previous response, for the wire transcript), `apikeys_tools_models_test.go` (the fake monomind script is copied: the `package main` helper is not importable); a grant-mode check in `grant_test.go`'s style.
- Docs: `AGENTS.md`, `SECURITY.md` (one sentence), `cmd/monoagentcli/mcp.go` (help), `cmd/monoagentcli/ref.go` (`ref api`), `CHANGELOG.md`, spec §8.3 (as built, status).

## Tasks

### Task 1: the report moves to `internal/openaiapi`

- [ ] Before touching it: build the base CLI to a scratch folder and record `api models` (text and `--json`, `--for network`, `--context-confinement sandboxed`, `--auto-confinement sandboxed|any`, bad values, with and without auto, auto held back to nothing) and `api status` (text and `--json`; heartbeat file missing, `MONOAGENT_HTTPAPI_ADDR=127.0.0.1:1`, `MONOAGENT_API_V1_ADDR` empty) against the fake monomind under a throwaway HOME.
- [ ] Tests (`modelsreport_test.go`): `NewModelsReport` table (aliases skipped; `allowed`, `context_allowed` and `auto_allowed` per class; a network policy; the context and auto caps never above the listener; auto available with N candidates and M held back, unavailable clears candidates, held back, key source and confinement, zero allowed models or zero within the auto cap turn it unavailable with the CLI's two texts; the JSON field order and `omitempty`); the parsers (flag beats environment beats default, bad values are errors).
- [ ] Implement; make the CLI use it. The CLI tests pass untouched, and the recorded outputs are byte-identical.
- [ ] Commit `refactor(openaiapi): the report of api models moves where the MCP tool can reach it`.

### Task 2: the read-only tools

- [ ] Tests: both listed without `--allow-mutations`; annotations; `api_key_list` empty is `[]`, shows only this profile's active keys, `include_revoked` adds revoked ones, no secret or hash anywhere; `api_models_list` with the fake monomind returns the three classes, `for: network`, the context and auto caps and the auto fields, a bad `for` or class is a tool error, `source` is `mcp`; the answer equals the CLI's for the same inputs apart from `source`.
- [ ] Implement `apiTools()` for the two; register.
- [ ] Commit `feat(mcp): api_key_list and api_models_list`.

### Task 3: the mutating tools

- [ ] Tests: absent from `tools/list` and refused by name without the flag, with no key created; with it, listed with annotations; create (key shape, authenticates as the MCP profile, only the hash stored, context flag); invalid and duplicate names; update (rename, context on and off by an explicit `false`, nothing to change, a taken name, a revoked key); revoke (stops authenticating, idempotent by id, free name afterwards); another profile's id and name are not found for update and revoke and read exactly like an unknown id; one piped session that creates, lists, updates, revokes and hits error paths holds the key exactly once in the whole stdout, and a session without the flag, where create is refused, holds no key; grant mode serves none of them.
- [ ] Implement; register.
- [ ] Commit `feat(mcp): api_key_create, api_key_update and api_key_revoke`.

### Task 4: docs

- [ ] AGENTS.md (tool lists; the `secret_list` line names `api_key_create` as the one exception, and that the key then passes through the MCP host; the `/v1` section's management line), SECURITY.md (one sentence in "Credentials"), the `mcp` help, `ref api`, CHANGELOG (also drop "MCP tools" from phase 1's "Not yet"), spec §8.3 as built.
- [ ] Check every claim against the code (tool names, flags, annotations); run the whole module's tests.
- [ ] Commit `docs(mcp): the API key tools, and api_key_create as the one tool that returns a secret`.

## Verification

`gofmt -l .` (empty); `go build ./...` and `-tags nosocial`; `go vet ./...` and `-tags nosocial`; `go test` of the touched packages, then `go test ./...` (only the 8 known failures); `go test -race ./internal/mcp ./internal/openaiapi ./cmd/monoagentcli -run 'API|Api|MCP|Models'`; the byte comparison of the CLI outputs; the mutation checks of every behaviour test; a smoke run of the real `monoagentcli mcp --allow-mutations` over stdio under a throwaway HOME with the fake monomind, checking that every stdout line is JSON-RPC (`api_models_list` spawns processes, and nothing may leak onto the protocol channel).

## How it was executed

- The base moved from `0aff731f` to `d0f9fb16` (`--auto-confinement`) before any code was written, so the branch started there and the shared report carries `auto_allowed`, `policy.auto_confinement` and `auto.{confinement,held_back}`. It moved on to `eab85b0d` (the breaker) before the end, and the branch was rebased onto it: only the CHANGELOG had a conflict, in prose.
- Added to the plan: `apikeys.Update.IsEmpty`, so that `api key update` and `api_key_update` refuse "nothing to change" on one rule (task 3, its own commit).
- Tests drive dependent calls through `callTool` on a server that stays open, because `Serve` answers on goroutines and closes the database when its input ends; only the wire-level checks (the key exactly once, a refused create, grant mode) go through `Serve`, over a pipe.
- Failure messages of the key tests go through `scrubbed`, and fields are read without a panicking assertion: a mutation run printed a throwaway key and panicked once, which is how both were found.
- Verified against the real binary: the recorded output of `api models` and `api status` (235 invocations) is byte-identical before and after the move; `api models --json` and `api_models_list` give the same document, field order included, apart from `source`; over stdio every line is JSON-RPC and the key is on stdout once.
