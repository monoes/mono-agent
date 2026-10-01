# OpenAI-Compatible API — Design Spec

Date: 2026-10-01
Status: Approved by the user on 2026-10-01, including the dedicated off-loopback listener (D13, §6.3). Amended the same day after two independent reviews of the phase 1 code, and the user then decided the two open points: a context key is served only by `chat-only` models unless the operator raises `--context-confinement` (D2, §5, §6.3, §7.2), and each concurrency slot has a fixed working folder per profile instead of one folder per request (§4.2, §5). The other amendments: a sandbox that cannot be applied refuses the turn (§6.1), stopping the server ends the turns in flight (§6.4), and error messages stay generic (§7.4).
Branch: `worktree-feat+openai-compatible-api`, cut from master `c612d46d`.

## 1. Goal

Let other programs use the agent runtimes installed on this machine (claude, codex, antigravity = "agy", …) through standard OpenAI-style web APIs: chat completions with streaming, image generation and a model list.

- Keys are per profile. A key belongs to one profile and reaches nothing of any other profile.
- A per-key switch, `context`, adds that profile's knowledge to requests. Off means a plain model.
- Keys are created and managed from the CLI, MCP and the desktop UI. Everything except the UI runs on a headless Linux server.
- When Jev is available on the profile, the model name `auto` lets Jev pick the model from the prompt.
- Developers can point coding assistants at it from their own machines (streaming chat now, OpenAI tool calling in phase 5). On loopback every runtime stays selectable.

## 2. Decisions register

| # | Decision | From |
|---|---|---|
| D1 | Images go through the agent runtimes: a turn in a scratch folder is told to save the image there and the API returns the files. Verified on codex and agy (App. A). Gemini-in-browser and Hugging Face are not API backends. | user |
| D2 | `context` ON adds excerpts from the profile's own knowledge (`monomind.SearchKnowledge`) to the system prompt. No profile folder, no tools; the turn is as locked down as with OFF. Because the excerpts include captured web pages nobody vetted, a context key is served **only by `chat-only` models** by default: a runtime with native tools could be steered by injected instructions. The operator can raise that on purpose with `--context-confinement` (D15), never above the listener's own limit. | user, tightened after review |
| D3 | Hardened exposure: off-loopback, `/v1` is served only on a dedicated listener that requires TLS, serves nothing but `/v1` and `/health`, and defaults to `--confinement chat-only`. Loopback keeps everything on. | user (listener form: D13) |
| D4 | Coding use: streaming chat from phase 1, OpenAI tool calling as phase 5 (spike first), all runtimes selectable on loopback. | user |
| D5 | The gateway runs inside the existing `httpapi` process (daemon or `httpapi`); the key resolves the profile per request. | design |
| D6 | Keys are SHA-256-hashed rows in SQLite, shown once. No vault, no keyring. | design |
| D7 | Doctrine kept: runtimes only through `monomind.Exec`; no provider keys stored; no per-CLI knowledge in Go (which runtimes make images is a configurable list; image collection is generic). | AGENTS.md |
| D8 | Model ids are `<runtime>/<model>`; `agy` is accepted as an alias of `antigravity`; a bare runtime means its default model. | design |
| D9 | MCP `api_key_create` returns the key once. This is a documented exception to "no tool returns secrets" (AGENTS.md:322). Mutating tools need `--allow-mutations`. | design default |
| D10 | CLI-first: the GUI shells out to `monoagentcli api … --json`; no SQL in `wails-app/`. | AGENTS.md |
| D11 | Requests are stateless. Sampling parameters are accepted and ignored (`agent exec` has none). | design |
| D12 | Jev `api_auto` is a new opt-in surface; Jev only picks among options the code lists; any failure falls back to a rule. | Jev doctrine |
| D13 | Network exposure uses a dedicated `--v1-addr` listener. `/v1` is never served in plaintext off-loopback. | design refinement |
| D14 | Existing legacy-token routes are unchanged. A key never opens them; the legacy token never opens `/v1`. | design |
| D15 | `--context-confinement chat-only|sandboxed|any` (env `MONOAGENT_API_CONTEXT_CONFINEMENT`, default `chat-only`) is the strongest class a key created with `--context` may use. It never goes above the listener's own `--confinement`. | user (2026-10-01, after review) |
| D16 | Working folders are per profile and per concurrency slot: `~/.monoagent/workspaces/api/p-<hash of the profile id>/slot-N`. A profile id never reaches a path as such. | user (2026-10-01, after review) |

## 3. Verified facts (master `c612d46d`, monomind 2.22.0, checked 2026-10-01)

- **Runner.** `monomind.Exec(ctx, ExecOptions, onEvent) (*TurnResult, error)` (`internal/monomind/exec.go:313`; options `:24-90`; result `:93-134`).
  - Exec does not validate runtime, model, effort or resume (`:333-370`), so values from clients must be allowlisted.
  - An empty `Cwd` runs in the server's cwd, so always pass one.
  - `onEvent` runs in order from one goroutine behind a 64-slot channel.
  - After a turn starts Exec returns `res, nil`; check `res.Err`, `StopReason` and `SawDone`.
  - A cancelled context sends a cancel frame, then kills the process group after 5 s.
- **Prompt shape.** One `Prompt` string plus `SystemPrompt` (`--system-file`). `agent exec` has no temperature, max-tokens, stop or response-format option.
- **Existing text posture.** `agent.ask` and chat pass `Sandbox: monomind.TurnSandboxMode` (`workspace-write`), `WorkspacePurpose`, no `Access` (monomind's default `scoped`) and no tools (`--tools none`) (`internal/nodes/agent/ask.go:70-81`, `cmd/monoagentcli/chat.go:287-301`).
- **Scan.** claude, codex and antigravity are installed here. `streams_incrementally` is true for claude, antigravity, opencode and pi, false for codex. Every installed runtime reports `caller_tools: true`. No image field exists anywhere in the scan.
- **Models.** `monomind.ListModels(ctx, runtime, binary)` (`models.go:113`, 0.5–2.7 s, falls back silently to built-in lists) and `monomind.Scan(ctx)` (`exec.go:732`, about 0.8 s).
  - The validated roster (`agentroster.List` and `Build`, `store.go:70`, `roster.go:45`) is machine-wide and filled only by `agent validate`. It is empty on this machine, so `/v1/models` must not depend on it.
  - Ids collide across runtimes (`default`).
- **Confinement observed** (App. A):
  - claude reports `native_sandbox: monomind`, an allow-list gate (`agent-exec-gate.js` `scopedCanUseTool`).
  - codex runs under `workspace-write` yet read files outside its folder.
  - antigravity reports `native_sandbox: none` and ran a shell `cp` with absolute paths.
- **Errors.** Codes in `internal/monomind/types.go:307-320`, `IsAgentNotSetup` in `setup.go:64`, `agentroster.Classify` in `classify.go:50`.
- **httpapi.** `Options.ExtraRoutes` is a single slot (`internal/httpapi/server.go:133`), used by the daemon for the org receiver (`cmd/monoagentcli/daemon.go:212`). `WriteTimeout` is 5 min and `ReadTimeout` 30 s (`server.go:88`). There is no TLS. The legacy error shape is `{"error":"…"}`. The vault token needs a working keyring on every authenticated request (`server.go:149`, `token.go:29-64`).
- **TLS precedent.** The webhook server serves a non-loopback bind only over TLS: operator cert from env, else a cached self-signed cert valid for localhost only (`internal/workflow/webhook_server.go:158-232`).
- **Profiles.** Ids are uuids; the folder is `root_dir` or `~/.monoagent/profiles/<id>` (`profiledir.go:50-61`). Servers are single-profile (`runtime.go:76-80`). The org endpoint receiver resolves its profile from a database row (`orgbridge/receiver.go:113-142`), which is the precedent for key-to-profile lookup.
- **Database.** Latest migration is 060, so the next is 061 (renumber at merge if taken). Style follows `043_jev_usage.sql`; store pattern follows `orggrant.Store`.
- **MCP.** Tool struct in `internal/mcp/tools.go:20-27`. Mutating tools are gated (`server.go:48-54`). Every listed tool needs annotations (`server_concurrency_test.go:121-143`). Cross-profile lookups must say "not found" (`server_profile_isolation_test.go:27-101`). Tool names are also hand-listed in `AGENTS.md:316-346` and `cmd/monoagentcli/mcp.go:22-42`.
- **Jev.**
  - `dynorg.JevChooser.Choose(ctx, state, question, options)` (`monomindlib.go:250-268`) is generic, with a 15 s cap.
  - `jevconf.ResolveKey` (`jevconf.go:103`) decrypts the vault on every call; `KeySource` (`:140`) does not.
  - A new surface needs a constant, `Surfaces`, `Egress`, `Describe`, `DefaultThreshold` (`jevconf.go:28-87`), gating at the call site and docs. No migration; the GUI row is automatic.
  - `jev enable` prints the egress list and asks (`jev.go:195-209`).
- **CLI and GUI.**
  - CLI patterns: `jev_key.go`, `secret_keyring.go`, exit codes in `exitcodes.go:35-45`, `ref api` in `ref.go:2654`.
  - GUI patterns: `wails-app/app_jev.go` and `runMonoCLI` (`app_applications.go:24-51`), settings sections mounted in `Settings.jsx:409-411`, and `doctrine.test.js` banning `internal/monomind` imports in `wails-app/`.
- **Knowledge.** `monomind.SearchKnowledge(ctx, db, profileID, query)` (`docsync.go:257`) searches only the profile's documents and captures, never the personal brain. It starts two monomind processes.
- **Linux service.** `daemon install` writes a systemd user unit with no environment or flags (`internal/autostart/autostart_linux.go:21-32,76-119`). Verifying keys needs no keyring.

## 4. Architecture

### 4.1 Components

| Piece | Responsibility |
|---|---|
| `internal/apikeys` | Key format and hash, `Store` (create, list, get, update, revoke, authenticate). No HTTP. |
| `internal/openaiapi` | `Gateway`: auth, request translation, policy, limits, catalog, handlers (models, chat, stream, images), OpenAI error shapes, adapters over `monomind`, `agentroster`, `dynorg`, `jevconf`. Interfaces (`Runner`, `Catalog`, `Chooser`, `Knowledge`) keep it testable. |
| `internal/workflow` | The webhook TLS rules are extracted into an exported helper reused by the gateway; the webhook tests are the safety net. |
| `internal/mcp` | Five tools (§8.3). |
| `cmd/monoagentcli` | `api` command group; flags on `httpapi` and `daemon`; mounting and the dedicated listener. The daemon's single `ExtraRoutes` slot must compose the org receiver and the gateway mount, or one of them is lost. |
| `internal/jev/jevconf`, `internal/dynorg` | Surface `api_auto`; `JevChooser` reused. |
| `wails-app` | `app_api.go` and `components/settings/ApiSection.jsx`. |
| `data/migrations` | `061_api_keys.sql`. |

### 4.2 Request flow (chat)

1. `Authorization: Bearer sk-ma-…` goes to `apikeys.Authenticate`, which yields a `Principal{KeyID, ProfileID, Context}`; otherwise 401.
2. The listener's policy (confinement maximum, network flag) applies.
3. Parse and validate the body (§7.2).
4. Resolve the model: `auto` (§9), alias and default expansion, membership in the catalog, then its confinement class must be within the policy maximum (for a context key the maximum is capped at `--context-confinement`, `chat-only` unless raised), else 403 `policy_denied`.
5. Take a concurrency slot without blocking, else 429.
6. If `Principal.Context`, run `SearchKnowledge` and build the context block.
7. Work in the folder of the slot taken in step 5 inside the key's profile folder, `~/.monoagent/workspaces/api/p-<hash>/slot-N` (0700, N below the concurrency limit, `<hash>` a hash of the profile id), emptied before and after the turn.
8. Call `monomind.Exec`. Events become SSE chunks or are buffered.
9. Map the result (§7.4) and set headers.
10. Empty the slot's folder and release the slot.

Files stay under 500 lines: `auth.go`, `catalog.go`, `confinement.go`, `translate.go`, `chat.go`, `stream.go`, `images.go`, `models.go`, `errors.go`, `limits.go`, `auto.go`, `register.go`, `serve.go`, plus `types.go` (wire shapes), `config.go`, `gateway.go` (dependencies and the `Gateway`) and `runner.go` (one request as one turn).

## 5. Isolation model

- A key row holds one `profile_id`. Authentication yields that profile and nothing else. An unknown or revoked key gives the same 401. Another profile's key id is "not found" in the CLI, MCP and GUI.
- Every request works in the folder of its concurrency slot inside its profile's folder (`p-<hash>/slot-N`), never the profile's own folder. A slot's folder is fixed rather than one per request, because agent CLIs keep per-folder session state (claude's `~/.claude/projects/<folder>`) that a folder per request would pile up without bound, and it is per profile so that state, and anything else a CLI keeps by folder, is never shared between profiles (D16). It is exclusive to the running turn and emptied before and after it, and at start; a folder that cannot be emptied fails the request. The CLIs' own session stores still keep the prompts of past turns, as they do for any use of them.
- With `context` ON the only profile data in the turn is the excerpts: documents and captures of that profile only. File paths are not included, only base names. Like any prompt, the excerpts go to the runtime's provider. Only models at or below `--context-confinement` (`chat-only` unless the operator raised it) receive them (D2, D15).
- Revocation applies to the next request. A turn already running finishes, within the turn timeout.
- The legacy token (vault `httpapi-token`) and `sk-ma-` keys are separate credentials for separate routes.
- `org teardown-profile` also revokes the profile's keys.

## 6. Confinement and exposure

### 6.1 Classes

| Class | Meaning | Today |
|---|---|---|
| `chat-only` | Monomind's allow-list gate is the only tool gate; with no caller tools nothing native is reachable | claude (live canary in the plan) |
| `sandboxed` | Writes confined to the turn's folder; reads and network open, and the runtime's own configuration (its MCP servers, instructions files) still applies | codex |
| `unconfined` | Full native tools as the OS user | antigravity |

Chat works in every class. Images need `sandboxed` or `unconfined` because the agent must write the file.

### 6.2 Classifier

```
class(runtime):
  e := scan entry for runtime
  e.native_sandbox == "monomind"                                   -> chat-only
  SandboxArgs(caps, e.sandbox_modes, runtime, "workspace-write")
      verdict == sandboxed                                         -> sandboxed
  anything else (including scan failure, unknown runtime)          -> unconfined
```

A class nobody set is invalid and allowed by no policy. Exec is called with `RequireSandbox` for a `sandboxed` model, so a sandbox that cannot be applied refuses the turn (403) instead of running it unconfined. Enforcement is repeated at turn start. When the `start` event arrives, its `native_sandbox` is mapped to a class (`monomind` → chat-only; `workspace-write`, `read-only` → sandboxed; anything else → unconfined). If that class is weaker than the policy allows, the turn is cancelled and 403 `policy_denied` returns. The classifier is table-tested against the captured live scan.

### 6.3 Listeners, TLS, policy

- **Main listener** (existing `--api-addr`). `/v1` is mounted only when the bind is loopback. An off-loopback main bind keeps its legacy behaviour but gets no `/v1`, and a log line points to `--v1-addr`.
- **Dedicated listener** (`--v1-addr`, `MONOAGENT_API_V1_ADDR`, on `daemon` and `httpapi`). It serves `/v1/*` and `GET /health` only.
  - Off-loopback it requires TLS using the webhook rules: `MONOAGENT_API_TLS_CERT` and `_KEY`, else the cached self-signed certificate (localhost only, so remote clients reject it).
  - Real deployments set a certificate or terminate TLS in a proxy. A loopback bind is plain HTTP.
- **Confinement maximum:** `--confinement chat-only|sandboxed|any` (env `MONOAGENT_API_CONFINEMENT`). Unset, the default is per listener: off-loopback `chat-only`, loopback `any`. Behind a reverse proxy the bind is loopback, so set it explicitly.
- **Context maximum:** `--context-confinement chat-only|sandboxed|any` (env `MONOAGENT_API_CONTEXT_CONFINEMENT`). Unset, `chat-only`. It is the strongest class a key created with `--context` may use, and it never goes above the listener's own maximum. A context key is listed, and may use, only the models at or below it (D15).

### 6.4 Limits

| Limit | Default |
|---|---|
| Request body | 2 MiB (images: 64 KiB) |
| Concurrent turns | 4 (`--max-concurrent`, `MONOAGENT_API_MAX_CONCURRENT`); a full gateway returns 429 with `Retry-After: 2` |
| Turn timeout | 10 min (`MONOAGENT_API_TURN_TIMEOUT`) |
| Write deadline | Per request via `http.ResponseController`, turn timeout + 60 s: the request context ends at turn timeout + 30 s, and the response needs time to be written after that (the server-wide 5 min `WriteTimeout` would cut long streams). SSE writes drop a client that stalls for 30 s. |
| Shutdown | Stopping the server cancels the turns in flight and waits up to 30 s for their processes to be killed |
| CORS | None; browser clients are out of scope |

## 7. API surface

Base `…/v1`. Auth `Authorization: Bearer sk-ma-…`. Errors use the OpenAI shape `{"error":{"message","type","param","code"}}`.

### 7.1 Models

- `GET /v1/models` and `GET /v1/models/{id}`. Entries: `{"id":"codex/gpt-6-astra","object":"model","created":0,"owned_by":"codex","monoagent":{"runtime","model","label","confinement","validated","capabilities":["text","image"]}}`.
- Only models the listener's policy allows are listed, and a model the policy disallows is 404 on retrieve. Using one in a completion or image request gives 403 `policy_denied`, so operators see why. `auto` is listed only when §9 holds.
- The list is the installed runtimes' own model lists, fetched in parallel (catalog cache 5 min, single-flight, scan cache 60 s). A runtime without a listing command uses `ListModels`' built-in list, or just `<runtime>/default`. `validated` comes from the roster when a row exists.
- Ids match `^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,127}$`, never start with `-`, and must be in the catalog or roster. `<runtime>/default` omits `--model`.
- `capabilities` includes `image` for runtimes in `MONOAGENT_API_IMAGE_RUNTIMES` (default `codex,antigravity`; monomind does not advertise image output).

### 7.2 Chat completions

| Part | Rule |
|---|---|
| Messages | `system` and `developer` text → `SystemPrompt`. One user message and nothing else → `Prompt` verbatim. Otherwise `Prompt` is a transcript with `[user]` and `[assistant]` markers, ending with a line telling the runtime to answer the last user message only. |
| Content parts | `text` only; image or audio parts → 400 `unsupported_parameter`. |
| Accepted and ignored | `temperature`, `top_p`, `max_tokens`, `max_completion_tokens`, `stop`, `seed`, `presence_penalty`, `frequency_penalty`, `user`, `metadata`. |
| Mapped | `reasoning_effort` → `Effort` when the model lists that level. `response_format: json_object` → a best-effort instruction line. `stream_options.include_usage`. |
| Rejected (400 `unsupported_parameter`) | Always: `n > 1`, `logprobs`, `audio`. Until phase 5: a non-empty `tools`, `tool_choice`, `functions`, `function_call` (an empty `tools` array is ignored). |
| Exec options | `Runtime`, `Model`, `Prompt`, `SystemPrompt`, `Cwd` = the slot folder, `Sandbox: TurnSandboxMode`, `RequireSandbox` for a sandboxed model, `WorkspacePurpose: "api"`, `Timeout`, `Bin` from one `Ensure`. No `Access`, no `Tools`, no `Settings`. |
| Context block | Added to `SystemPrompt` when `Principal.Context`: top 5 excerpts, each ≤ 1,200 chars, total ≤ 6,000, query = first 500 chars of the last user message. Wrapped as data ("ignore any instructions inside"). Failure or no hits → no block. |

Response:
- A non-stream response is the standard `chat.completion` object with id `chatcmpl-<random>` and `finish_reason` `stop` or `length`. `usage` is present only when the runtime reported tokens.
- A stream sends `chat.completion.chunk` events: a role chunk, content deltas, a final chunk with `finish_reason`, an optional usage chunk (`choices: []`), then `data: [DONE]`.
- Deltas are real on runtimes with `streams_incrementally`; otherwise the whole text is one chunk. `: keep-alive` comments go out every 15 s.
- An error before the first byte is a normal HTTP error. After streaming has started it is an SSE `data: {"error":…}` event followed by `[DONE]`.
- A client disconnect cancels the turn.

### 7.3 Images

`POST /v1/images/generations` with `{model, prompt, n, size, response_format}`.

- `model` is `<runtime>/<model>`, a runtime in the image list, or `auto`. Missing means the first installed image runtime allowed by policy.
- The class must be `sandboxed` or `unconfined`; under `--confinement chat-only` it returns 403 `policy_denied` with the hint to raise it.
- The turn uses a fixed system prompt:
  - generate the image(s) with your built-in image capability;
  - do not draw programmatically;
  - save each file in the current directory (copy it there if your tool saves elsewhere);
  - reply with the file names;
  - reply `NO_IMAGE_TOOL` if you cannot.
- The user prompt is the request `prompt`, plus "Create N distinct images." and "Preferred size: WxH." when given.
- Collection: after the turn, take files in the slot folder (one level) whose magic bytes are PNG, JPEG, WebP or GIF, at most 20 MiB each and `n` files (cap 4).
- Result: `{"created":<unix>,"data":[{"b64_json":"…"}]}`.
  - `NO_IMAGE_TOOL` → 400 `image_generation_unsupported`.
  - No file → 502 `image_generation_failed`, with the runtime's reply (≤ 300 chars) in the message.
  - `response_format: "url"` → 400, b64 only. `quality`, `style`, `output_format` and `background` are ignored.
- Expect 40–60 s and about 40k input tokens per image turn (App. A).

### 7.4 Errors

| Condition | HTTP | `type` / `code` |
|---|---|---|
| Missing or invalid key | 401 | `authentication_error` / `invalid_api_key` |
| Bad body or parameter | 400 | `invalid_request_error` / `invalid_json`, `invalid_value`, `missing_required_parameter`, `unsupported_parameter` |
| Body too large | 413 | `invalid_request_error` / `request_too_large` |
| Unknown model, or `auto` unavailable | 404 | `invalid_request_error` / `model_not_found` |
| Class above policy, a context key asking for a model above `--context-confinement`, or a sandbox that cannot be applied | 403 | `permission_error` / `policy_denied` |
| Gateway full | 429 | `rate_limit_error` / `rate_limit_exceeded` + `Retry-After` |
| Runtime rate limited (`rate-limited`) | 429 | `rate_limit_error` / `rate_limit_exceeded` |
| Runtime quota or budget | 429 | `rate_limit_error` / `insufficient_quota` |
| Runtime not set up (`IsAgentNotSetup`) | 503 | `api_error` / `runtime_not_available` |
| Timeout | 504 | `api_error` / `timeout` |
| Runner error, bad frame, no `done` | 502 | `api_error` / `runtime_error` |
| `max_turns`, `tool_round_cap` | 200 | `finish_reason: "length"` |
| Anything else | 500 | `api_error` / `internal_error` |

Messages for 500 and for a runtime error other than setup, rate limit, quota or timeout are generic and tell the caller to quote the `X-Request-Id` header; the detail (a Go error, a runtime's error code) goes to the server log.

### 7.5 Headers

- Always: `X-Request-Id` (on every response, errors and 401 included), `X-Monoagent-Model` (the runtime/model that answered, also for `auto`).
- Non-stream only: `X-Monoagent-Sandbox` (the turn's `SandboxStatus`).
- When relevant: `X-Monoagent-Auto: jev|rule`, `X-Monoagent-Context: <n>|none|unavailable`.
- Logs hold the request id, key id, profile, model, status, duration and context count, plus the operator-only detail of a failure, and never prompts or keys. A failed authentication is logged with the caller's address.

## 8. Keys and management

### 8.1 Data model

```sql
-- 061_api_keys.sql
CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    profile_id   TEXT NOT NULL,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    context      INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_profile ON api_keys(profile_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_profile_name_active
    ON api_keys(profile_id, name) WHERE revoked_at IS NULL;
```

- The key is `sk-ma-` plus base64url of 32 random bytes (49 characters). Only its SHA-256 (hex) is stored, plus a display `prefix` (first 12 characters). The key is shown once.
- Names are 1–64 characters of `[A-Za-z0-9 ._-]`, unique per profile among active keys. Ids are `key_` plus random base32.
- `Authenticate` checks the shape, hashes the token, looks it up, rejects a revoked row, then re-compares the hash in constant time. There is no auth cache, so revocation is immediate.
- `last_used_at` is written at most once a minute per key.

### 8.2 CLI (new `api` group, scoped to the active profile or the global `--profile`; all with `--json`; exit codes 2 not found, 3 invalid input)

```
api key create --name N [--context]        # prints the key once; --json adds "key"
api key list [--all-profiles] [--include-revoked]
api key show <id|name>
api key update <id|name> [--name N] [--context | --no-context]
api key revoke <id|name> [--yes]
api models [--for loopback|network] [--confinement C] [--context-confinement C]   # every model with its confinement class, whether that listener kind serves it, and whether a context key may use it
api status [--json]                         # listeners, exposure, confinement, key counts, auto availability, reachability
```

`httpapi` and `daemon` gain `--v1-addr`, `--confinement`, `--context-confinement` and `--max-concurrent`. Settings otherwise come from env (§6). The daemon records its dedicated `/v1` address in its heartbeat file (`daemonhb`, which already carries the HTTP API address), and `api status` reads it from there, together with the confinement policies and the context maximum the daemon applies; without a daemon it says its policies are assumed from its own environment. `auto` availability joins `api status` and `api models` in phase 3.

### 8.3 MCP

| Tool | Kind | Notes |
|---|---|---|
| `api_key_list` | read-only | metadata only |
| `api_models_list` | read-only | |
| `api_key_create` | mutating | returns the key once (D9) |
| `api_key_update` | mutating | name, context |
| `api_key_revoke` | mutating, destructive | |

All carry annotations. Keys are scoped to the MCP profile; another profile's id is "not found". Names are added to `AGENTS.md` and `cmd/monoagentcli/mcp.go`.

### 8.4 GUI

Settings gets an "OpenAI-compatible API" section (`ApiSection.jsx`, mounted next to Jev).
- A status header with the base URL, copy button, running state, and exposure and confinement badges.
- A keys table with a context switch, last used and revoke.
- A create dialog with a show-once copy panel.
- A model table with validated and auto badges and a link to the Jev section.

The Go side (`app_api.go`) calls `runMonoCLI` with `api … --json`. The bindings are regenerated, or hand-edited when the Wails toolchain is missing. Tests use vitest with `window.go` mocks, and controls follow the UI style guide.

### 8.5 Headless Linux

- It needs no GUI and no keyring: `monoagentcli api key create --name app`, then `monoagentcli daemon` or `httpapi`, with the env from §6.
- The generated systemd user unit has no environment, so the docs show a `systemctl --user edit monoagent-daemon` drop-in (it survives reinstall) and `loginctl enable-linger`.
- The agent CLIs must be installed and logged in on that machine; `monoagentcli doctor` reports it.
- `auto` on a server without a keyring needs the Jev key in `TYPESAFE_API_KEY`; the vault entry would need the file keyring and its passphrase file.

## 9. Jev auto

- **Surface.** New `api_auto`, opt-in per profile through `jev enable api_auto`. The egress list says that the first 4,000 characters of the last user message and the candidate model names, descriptions and any validation cost and latency leave the machine. The surface's threshold is the minimum top-option probability Jev's pick must reach; its default is 0 (always accept the top pick), and below a raised threshold the rule fallback is used.
- **Availability.** `auto` is listed and accepted only when the profile has a Jev key and the surface is enabled for it. Otherwise 404 `model_not_found` with a hint. Listing is gated on the non-decrypting `jevconf.KeySource`; the key itself is resolved (`ResolveKey`, which decrypts the vault on every call) only when a pick actually runs.
- **Choice.** One `choice` question through `dynorg.JevChooser`, using a client built for surface `api_auto` so `jev_usage` records it. Options are the models the policy allows (for images only runtimes in the image list) with label, description and validated stats. The prompt text goes in `untrusted_prompt`.
- **Fallback.** On an error, timeout or empty answer: the cheapest and fastest validated candidate; otherwise the runtime default in the order claude, codex, antigravity, then the rest alphabetically. The header `X-Monoagent-Auto` records `jev` or `rule`.

## 10. Tool calling (phase 5, spike first)

- `tools[].function{name, description, parameters}` map to `monomind.ToolSpec`. Names match `^[A-Za-z0-9_-]{1,64}$`.
  - `tool_choice: none` passes no tools. `required` or a named function adds a best-effort instruction.
  - Native tools stay off and confinement is unchanged.
- When the agent calls a tool, the response ends with one `tool_calls` entry (id `call_<random>`) and `finish_reason: "tool_calls"`. A second simultaneous call gets an error result asking for one at a time.
- The turn is parked (`parkedTurn`: key, profile, call id, resume channel, expiry; TTL 10 min). Parked turns hold no concurrency slot; at most 16 in total and 4 per key.
  - The parked `Exec` runs on a detached context owned by the registry, not the request's, because ending the response would otherwise cancel the turn (Exec sends a cancel frame, then kills the process group).
  - `ExecOptions.ToolTimeout` and `Timeout` are set to cover the TTL and the whole loop; monomind's `--tool-timeout` defaults to 120 s, which would end a parked turn after two minutes.
- The follow-up carrying the `tool` message resumes the parked turn if the key and call id match, so a long coding loop does not re-pay the per-turn overhead. If it is gone, the transcript is replayed statelessly with the tools declared again.
- **Go/no-go spike:** claude, codex and agy each call a declared tool in at least 9 of 10 trials; a parked turn survives the response ending and a tool wait longer than 120 s; resume works; resume cost is measured against replay. The result is recorded in the plan before the build.

## 11. Verification

- **Unit tests.**
  - `apikeys` (hash, shape, create, update, revoke, unique names, cross-profile).
  - The classifier against the captured live scan.
  - Policy defaults by bind address.
  - Message translation and validation, including `--`-style injection attempts in `model` and `reasoning_effort`.
  - Model resolution, SSE framing, error mapping, image collection (magic bytes, caps) and the context block.
  - The `auto` chooser with `jevtest` and its fallback.
  - The limiter.
- **Integration tests** (HTTP → fake `monomind` binary through `ExecOptions.Bin` emitting scripted NDJSON):
  - streaming and client-disconnect cancellation;
  - scratch cleanup, and a 429 when full;
  - a key not opening legacy routes and the legacy token not opening `/v1`;
  - `/v1` not mounted on an off-loopback main bind.
- **Surface tests.** MCP annotations, key-shown-once and isolation tests; CLI JSON and exit codes; vitest for the GUI section; OpenAPI lint for `/v1`; `go vet`, `gofmt`, and a `-tags nosocial` build.
- **Baseline.** `go test ./...` on master `c612d46d` (macOS) passes in 111 packages and has 8 known pre-existing failures: `TestCaptureTaskFilesOnTheBoard`, `TestCoderConversationFolders`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks` (`cmd/monoagentcli`), `TestCreateAttachesEveryArtifact`, `TestCreateRecordsTheRealPathNotASymlink` (`internal/capturetask`), `TestGenerateConfigFailsFastWhenMonomindMissing` (`internal/config`) and `TestFindAll_ListsShadowedCopies` (`internal/monomind`). Compare a red run against this list; they are not caused by this work.
- **Opt-in live canaries** (env-gated, they cost money):
  - a claude chat-only check (a read and a write outside the folder must not happen);
  - one chat per runtime;
  - one image per image runtime;
  - a streamed chat through a real TLS listener.

## 12. Phasing (one spec; each phase is shippable and gets its own plan and PR, P1's plan first)

| Phase | Content |
|---|---|
| P1 | `apikeys` + migration, gateway (models, chat, stream, context), confinement and limits, dedicated listener and TLS, CLI, docs and OpenAPI |
| P2 | MCP tools, GUI section and bindings |
| P3 | Jev `api_auto` (surface, chooser, fallback, CLI and GUI exposure) |
| P4 | Images |
| P5 | Tool calling (spike, then build) |

## 13. Not in scope

`/v1/completions`, `/v1/responses`, embeddings, vision and audio input, per-key quotas or model allowlists, CORS and browser clients, Gemini-in-browser and Hugging Face image backends, sessions or threads, and running the agent inside a caller-chosen project folder (API-driven coder mode). Tool calling (P5) is the supported way to code on the caller's machine.

## 14. Risks and defaults

| Risk | Default or mitigation |
|---|---|
| A key is the runtime's capabilities: codex can read the disk, agy has a shell | Classes and the hardened off-loopback default; docs say to run the server as a dedicated unprivileged OS user; `X-Monoagent-Sandbox` and `api models` show the truth. |
| The claude chat-only claim rests on monomind's source | A live canary gates documenting it as a guarantee; until then the docs say "by design". |
| Cost per call: each turn carried about 40k input tokens in the probes; claude reports cost, codex and agy do not | No quotas in v1; documented. The concurrency cap bounds load. |
| Image collection relies on the agent copying the file into the folder | Failure is a clear 502; a canary measures the success rate. |
| Parked-turn design is unverified | Spike gate (§10) before any build. |
| Wails bindings conflict with uncommitted regenerated diffs in the main checkout | The GUI phase rebases last. |
| Migration number 061 may be taken | The integrator re-checks before merge. |
| Captured web pages in the knowledge base carry prompt injection into a context key's prompt | A context key is served only by `chat-only` models unless the operator raises `--context-confinement` (D2, D15); excerpts are fenced as data. |
| Agent CLIs keep session transcripts of API turns in their own stores | Slot folders bound the directories (one per profile and slot) and keep profiles apart, not the content; documented. |
| Some runtimes (antigravity) put the prompt on their command line, visible to other local users | Documented; run the server on a dedicated host or OS user. |
| monomind changes `native_sandbox` semantics | The classifier fails closed (unknown → unconfined) and the start-event check cancels. |
| antigravity lists a `restricted` sandbox mode that Go does not use today | Untested option, recorded for a later hardening step; v1 treats agy as unconfined. |

## 15. Found along the way (not fixed here)

- In daemon mode with `--allow-mutations`, the legacy token can run, activate or deactivate another profile's workflow by id: the engine allows all profiles (`internal/workflow/engine.go:808-811`) and the handlers add no check (`internal/httpapi/handlers.go:173,296,315`). It is independent of `/v1` keys, so it should be filed as its own issue.
- `AGENTS.md:979-993` says claude and codex are not sandboxed. On monomind 2.22 both are `sandboxed` per `SandboxArgs`. This spec's docs task corrects that paragraph.

## 16. Docs to update

`AGENTS.md` (HTTP API, MCP tool list and the secrets exception, "what the desktop app calls", env vars, Jev surfaces, the sandbox paragraph), `ref api` (`cmd/monoagentcli/ref.go`), `internal/httpapi/openapi.yaml` (the `/v1` paths), `examples/openai-api-quickstart.md` (curl, Python and JS SDK `base_url`), `SECURITY.md` (new "OpenAI-compatible API surface" threat-model section), `README.md` (feature list and Jev surfaces) and `CHANGELOG.md` `[Unreleased]`.

## Appendix A — Probe evidence (2026-10-01)

One real turn per runtime, throwaway folder, `agent exec` with default `scoped` access, `--tools none`, no `--settings`, ambient markers stripped like `FilteredEnviron`; codex with `--sandbox workspace-write`. The prompt asked for a small red circle, to use the built-in image capability and to save the file in the current directory.

| | codex | antigravity |
|---|---|---|
| Start event | `native_sandbox: workspace-write`, `sandbox_applied: workspace-write` | `native_sandbox: none` |
| Image | 1254×1254 PNG in 40 s | 1024×1024 JPEG in 52 s |
| Steps | read its own imagegen skill file (outside the folder), image made by a built-in tool (no tool event), shell `cp` from `~/.codex/generated_images/<thread>/…png` | `generate_image` tool, then shell `cp` from `~/.gemini/antigravity-cli/brain/<conv>/…jpg` |
| Tokens | 36,503 in / 473 out, no cost | 40,785 in / 746 out, no cost |

The first codex attempt failed fast with `quota` (the account's usage limit); the retry after the reset succeeded. The temp folders were deleted; the runtimes' own state folders each kept one image.
