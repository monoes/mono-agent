# OpenAI-compatible API, phase 4: image generation (`POST /v1/images/generations`) Implementation Plan

**Goal:** `POST /v1/images/generations` runs one agent turn on a runtime that can make images (codex, antigravity), collects the image files the turn saved in its slot folder and returns them as `b64_json`. Spec §7.3 is the source of truth; this plan fixes what it left open.

**Architecture:** One new file of handlers (`images.go`), one of validation (`images_request.go`), one of collection (`images_collect.go`). The turn runs through the existing `runTurn`, which gets one hook (`turn.Collect`) that runs after a clean turn and before the slot folder is emptied. Which runtimes make images is configuration (`MONOAGENT_API_IMAGE_RUNTIMES`, default `codex,antigravity`), never per-CLI code (D7). `auto` generalises by a capability filter.

## Global constraints

- TDD: the failing test first, seen to fail for the right reason; afterwards each behaviour test is mutation-checked (a deliberate break of the production code, seen to FAIL at run time, restored).
- Files under 500 lines. Conventional commits with the `Co-Authored-By: Claude Sonnet 5.5` trailer. No bare `go build ./cmd/monoagentcli` in the repo root. Plain separate git commands.
- No real model call in default tests (fake `ExecFunc`); `internal/testdb` for databases. Never log or print a prompt, an answer, a key or image bytes.
- Shared files (`chat.go`, `runner.go`, `models.go`, `auto.go`, `errors.go`, `empty.go`) change only by small extractions that keep chat's behaviour byte-identical and its tests untouched. Another agent builds tool calling in the same package.

## Decisions this plan adds to spec §7.3

1. **What an image model is.** A runtime in the image list whose class is `sandboxed` or `unconfined` (`Config.CanMakeImages`). A chat-only runtime in the list is not one (it has no tool to save a file), so `monoagent.capabilities` says `["text","image"]` only for real ones.
2. **Errors.** Unknown model 404 `model_not_found`; a model that cannot make images 400 `invalid_value` (param `model`: it exists, so not a 404, and no policy would make it work, so not a 403); class above the policy 403 `policy_denied` with chat's messages; no model and nothing allowed 403 naming `--confinement` and, for a `--context` key, `--context-confinement`; no image runtime installed 404 `model_not_found`; `auto` without image candidates 404 `model_not_found` naming `--auto-confinement`.
3. **Body cap** is the smaller of the configured one and 64 KiB (spec §6.4); the slot, 429, headers and the log line are chat's.
4. **Fields.** `n` 1 to 4 (default 1; a runtime may save fewer, at least one is needed); above 4 or below 1 is 400 `invalid_value`. `size` is empty, `auto` or `WxH` (digits only, each side 64 to 8192) and reaches the prompt only in that canonical form. `response_format` absent or `b64_json`; `url` 400 `unsupported_parameter`, anything else 400 `invalid_value`; `stream: true` 400 `unsupported_parameter`. `quality`, `style`, `output_format`, `background`, `user` and the rest are ignored.
5. **Collection** runs inside `runTurn` (the slot folder is emptied when it returns): the folder is opened by the same function the emptying uses (`openFolder`, extracted from `emptyDirBy`: parent `Lstat`, same-file check, a link or non-directory refused); one level; only `Lstat`-regular files; opened `O_NONBLOCK` and compared with the `Lstat` (a FIFO or link swapped in between cannot hang or redirect it); at most 20 MiB read; magic bytes PNG, JPEG, WebP or GIF decide, not the name; sorted by name, the first `n` kept.
6. **Reply handling order:** `NO_IMAGE_TOOL` in the reply (even with files: they would be drawn programmatically, which the system prompt forbids) is 400 `image_generation_unsupported`; no image is 502 `image_generation_failed` with the reply through `runtimeWords` (300 characters, one line); the log detail says how many files were skipped and why, never a name or a byte.
7. **`auto`.** `autoCandidatesFor(capability)` filters the models `auto` may pick; Jev's question is unchanged (spec §9). The `auto` model object lists `image` only when it has image candidates. The `api_auto` egress list gains the image prompt.
8. **Shape** is exactly `{"created":…,"data":[{"b64_json":…}]}`: no mime type or usage (a client sniffs the first bytes; the quickstart shows how).

## File structure

- Create `internal/openaiapi/images.go` (handler, model resolution, reply mapping), `images_request.go` (`ImageRequest`, validation, prompt), `images_collect.go` (`collectImages`, magic bytes), `request.go` (what chat and images share: `logRequest`, `policyRefusal`, `resultError`, `pickForRequest`).
- Modify `config.go` (`ImageRuntimes`, parsing, `CanMakeImages`, `Capabilities`), `register.go` (route), `models.go` (`objectFor` takes the capabilities), `auto.go` (`autoCandidatesFor`, `withCapability`, `autoObject`), `empty.go` (`openFolder`), `runner.go` (`turn.Collect`, one call), `chat.go` (its four inline pieces now call `request.go`), `internal/jev/jevconf/jevconf.go` (egress line).
- Modify `cmd/monoagentcli/api_models.go` (capabilities per model, an IMAGES column), `ref.go`.
- Tests (new files, each under 500 lines): `images_config_test.go`, `openfolder_test.go`, `images_collect_test.go`, `images_collect_unix_test.go`, `images_request_test.go`, `images_auto_test.go`, `images_test.go`, `images_policy_test.go`, `images_turn_test.go`, `images_slots_test.go`, `images_unix_test.go`, `images_auto_routes_test.go`, `cmd/monoagentcli/api_models_images_test.go`; `live_test.go` gets one canary, `jevconf_test.go` one test.
- Docs: `AGENTS.md`, `SECURITY.md`, `CHANGELOG.md`, `internal/httpapi/openapi.yaml`, `examples/openai-api-quickstart.md`, `ref api`, spec §7.3.

## Tasks

### Task 1: the plan
- [x] Commit this file: `docs(api): the plan of phase 4, images`.

### Task 2: configuration and the image capability
- [x] Tests: `ParseImageRuntimes` (default when empty; `codex, AGY ,codex` gives `codex, antigravity`; an empty entry, `--x`, `a b` and a 33-character id are errors naming the variable); `ConfigFromEnv` reads it; `CanMakeImages` (listed and sandboxed or unconfined yes; listed chat-only no; unlisted no); `GET /v1/models` and `/v1/models/{id}` give `["text","image"]` for codex and antigravity models only, and the list follows the configured one.
- [x] Implement; commit `feat(openaiapi): MONOAGENT_API_IMAGE_RUNTIMES and the image capability of a model`.

### Task 3: one way to open a turn's folder
- [x] Behaviour-preserving: `openFolder` extracted from `emptyDirBy`; the existing `empty_test.go` and `empty_unix_test.go` are the safety net. Commit `refactor(openaiapi): the emptying and the collection open a turn's folder the same way`.

### Task 4: collecting images
- [x] Tests: PNG, JPEG, WebP, GIF collected; the name does not decide (a `.txt` with PNG magic is taken, a `.png` of text is not); at most `n`, by name; one level only; 20 MiB taken and 20 MiB + 1 not; a symlink to an image outside, one inside, a dangling one; a FIFO (unix); a directory named `x.png`; a swap between `Lstat` and open (hook) to a FIFO or link; a slot folder replaced by a link yields nothing; the report counts what was skipped by reason.
- [x] Implement `collectImages`; commit `feat(openaiapi): collect the images a turn left in its folder`.

### Task 5: the request
- [x] Tests (table): missing and blank `prompt`; `n` 0, -1, 5, 1.5; `size` table (valid, `auto`, malformed, out of range, injection-shaped, full-width digits); `response_format`; `stream`; ignored fields; the prompt text for n and size.
- [x] Implement `images_request.go`; commit `feat(openaiapi): validate an image request and build its prompt`.

### Task 6: the endpoint
- [x] Extract from `chat.go` `policyRefusal`, `logRequest`, `resultError`, `autoPick` (chat tests untouched and green), add `turn.Collect` to `runTurn`.
- [x] Tests (fake exec writing files into `opts.Cwd`): happy path with the locked-down `ExecOptions` and the fixed system prompt; headers; slot folder emptied afterwards; 3 files with n=2; `NO_IMAGE_TOOL`; no file with a noisy reply; collection security end to end (a symlink to a file outside is not returned); the policy table (chat-only, a `--context` key, raised context cap, antigravity under sandboxed); non-image model 400; unknown 404; default model by list order and policy; no runtime installed; 401, 413 (64 KiB), 429 busy and no process; runtime errors (quota 429, timeout 504, not signed in 503); policy at start 403; the log line holds no prompt or reply.
- [x] Commit `feat(openaiapi): POST /v1/images/generations`.

### Task 7: `auto` for images
- [x] Tests: default policy gives 404 naming `--auto-confinement`; raised, Jev is offered only image models, the header, the log and `X-Monoagent-Model` say the pick; the rule fallback; the `auto` object lists `image` only with image candidates; chat's `auto` is unchanged.
- [x] Implement; add the egress line; commit `feat(openaiapi): auto picks among image models for an image request`.

### Task 8: the CLI
- [x] `api models --json` gets `capabilities` per model (existing fields untouched), the table an `IMAGES` column; a bad `MONOAGENT_API_IMAGE_RUNTIMES` is exit 3. Commit `feat(cli): api models shows which models make images`.

### Task 9: docs and the canary
- [x] The docs listed above (every claim checked against the code; `redocly lint`), spec §7.3 marked as built with the deviations. Commits per document group.
- [x] `TestLiveImagePerImageRuntime` (opt-in), run once per image runtime at the very end.

### Task 10: verification
- [x] Mutation-check every behaviour test; `gofmt -l`, `go vet ./...`, `go test ./internal/openaiapi` with and without `-race`, the touched packages, `go test ./...`, `go build -tags nosocial ./...`, `GOOS=windows go build ./...`.

## Outcome

- Live canary, 2026-10-02 (monomind 2.22.0): antigravity passed (a 496,150 byte JPEG in 71.9 s, the runtime saved it at the top of the slot folder, `X-Monoagent-Sandbox: unsupported`); codex was skipped, its usage limit answered 429 `insufficient_quota`, so the codex path is untested live.
- `go test ./...` shows only the 8 failures known on this machine; `GOOS=windows go build ./...`, `go build -tags nosocial ./...` and `redocly lint` pass.

## Review round (2026-10-02)

Two read-only reviews (security, correctness) of the branch led to these changes, each test-first and mutation-checked, in small commits:

- [x] The turn gets an output folder with an unpredictable name (`turn.Subdir`) and only that is read, so a process an earlier turn left running cannot put a file into a later response; the collection is bounded (first 12 bytes, 64 entries, a budget, the caller leaving) and counts files outside the folder.
- [x] The response is streamed, the slot is released before it is written, and a client has two minutes to read it.
- [x] `MONOAGENT_API_IMAGE_RUNTIMES=none` is the off switch; the messages tell a runtime that is not installed from one that is chat-only, the hint of the 400 follows the key's policy, and the operator is told once which listed runtimes make no images.
- [x] Earlier in the round: images win over `NO_IMAGE_TOOL` (a line of its own only), no collection after a turn that never said done, a field of the wrong type is `invalid_value`, refusals log the model, a success logs what was left out.
- [x] The slot and emptying tests test what they say (one slot, a start-of-turn case, the endings of a request, a refused request while the slot is held).
- [x] SECURITY.md, AGENTS.md, CHANGELOG, `openapi.yaml`, the quickstart (the proxy timeout), `ref api` and spec §7.3 say the same.
- Live: antigravity returned a JPEG from the folder it was given (74.9 s).
