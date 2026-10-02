# OpenAI-compatible API, phase 3: the `auto` model (Jev picks) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `model: "auto"` on `/v1/chat/completions` lets Jev pick the runtime and model from the user's prompt, for a profile that has a Jev key and has switched the new `api_auto` surface on; any failure falls back to a rule.

**Architecture:** A new Jev surface `api_auto` (opt-in per profile, `jev enable api_auto`) in `jevconf`. The gateway gets an `AutoFuncs` seam in `Deps` (available? / choose / threshold), wired in `DefaultDeps` to `jevconf` and `jev.Client`. The chat handler resolves `auto` before the catalog lookup: it lists the models the key's effective policy allows, takes a concurrency slot, asks Jev one `choice` question (the last user message in `untrusted_prompt`), and runs the normal turn on the pick. The spec (§9, D12) is the source of truth; this plan fixes what it left open.

**Tech Stack:** Go, the existing `internal/jev`, `internal/jev/jevconf`, `internal/openaiapi`, `internal/agentroster`; `internal/jev/jevtest` for the fake TypeSafe server.

## Global Constraints

- TDD: a test that fails for the right reason first; mutation-check each fix.
- Files under 500 lines; conventional commits `type(scope): description` with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- Never run a bare `go build ./cmd/monoagentcli` in the repo root (use `go build ./...`, `go vet ./...` or `-o <scratchpad>`).
- The git wrapper of this session refuses compound git commands, heredocs and `git -C <other dir>`: plain separate commands.
- Known failing tests on macOS (8) are not regressions; run with and without `-tags nosocial`; CI runs `-race`, so tests must be race-clean and cheap: use `internal/testdb`, not a fresh migrated database per test.
- No real model call in default tests. An opt-in live check needs `MONOAGENT_LIVE_API_TESTS=1` and `TYPESAFE_API_KEY`.
- Never print or log keys or prompts. The prompt leaves the machine only to TypeSafe, only after `jev enable api_auto`, and the egress list says exactly what.
- Jev is decision-only (AGENTS.md doctrine): it picks among options the code lists, and never widens what the policy allows.
- Out of scope: images (`auto` for them), tool calling, MCP tools and the desktop UI (the Settings row for the surface appears by itself: it is built from `jev status`).

## Decisions this plan adds to spec §9

1. **No `dynorg` import.** `dynorg.JevChooser` is a ten-line adapter for dynorg's own `Chooser` interface with a fixed 15 s cap; the other surfaces (`retry_jev.go`, capture, HIL) call `jev.Client.Ask` directly through `jevconf.NewClient`, and so does this one. The timeout is the gateway's own (`Config.AutoTimeout`, default 8 s).
2. **The slot comes before the Jev call.** A request that would be refused with 429 never costs a Jev call, and at most `MaxConcurrent` picks run at once.
3. **One candidate means no question.** If the policy leaves a single model, it is used and `X-Monoagent-Auto: rule`.
4. **The choice is validated twice.** `jev.Client.Ask` validates the answer against the options; the gateway also checks the id is in the candidate list and that the probability reaches the profile's threshold (default 0: always accept the top pick), else the rule decides.
5. **The rule** (spec): among candidates with a recent passing validation, the cheapest, then the fastest; with no validated candidate, the runtime default (`<runtime>/default`) in the order claude, codex, antigravity, then the rest alphabetically; with no default either, the first candidate in that runtime order.
6. **Availability** (spec): listed and accepted only when the profile has a Jev key (`jevconf.KeySource`, which never decrypts) and `api_auto` is on. Otherwise 404 `model_not_found` with a message that says what is missing. The key itself is resolved (decrypted) only when a pick runs.
7. **`GET /v1/models`** lists `auto` first when it is available and at least one model is visible; `monoagent.runtime` is `auto` and `confinement` is the strongest class the key's policy allows (the pick is never above it).
8. **What the answer says:** `X-Monoagent-Model` and the completion's `model` are the model that answered; `X-Monoagent-Auto: jev|rule` says how it was chosen. The request log line carries `auto=jev|rule` and never the prompt.

## File Structure

- Modify `internal/jev/jevconf/jevconf.go`: `APIAuto`, and its entries in `Surfaces`, `Egress`, `Describe`, `DefaultThreshold`.
- Modify `internal/openaiapi/catalog.go`: `ModelInfo` gets `CostUSD`, `HasCost`, `LatencyMs` of the latest passing validation.
- Create `internal/openaiapi/auto.go` (< 250 lines): `AutoFuncs`, `AutoStatus`, `DefaultAuto`, `(*Gateway).resolveAuto`, `ruleChoice`, `autoCandidates` description text, `truncateRunes`.
- Modify `internal/openaiapi/gateway.go` (`Deps.Auto`, `DefaultDeps`), `config.go` (`AutoTimeout`), `chat.go` (resolution order), `models.go` (list and retrieve), `errors.go` (`errAutoUnavailable`, drop the "not implemented yet" special case).
- Modify `cmd/monoagentcli/api_models.go`, `api_status.go`, `ref.go` (`ref api`).
- Tests: `internal/jev/jevconf/jevconf_test.go`, `cmd/monoagentcli/jev_test.go`, `internal/openaiapi/auto_test.go` (new), `auto_wiring_test.go` (new, `jevtest`), `catalog_test.go`, `errors_test.go`, `cmd/monoagentcli/api_models_status_test.go`.
- Docs: `AGENTS.md`, `SECURITY.md`, `CHANGELOG.md`, `examples/openai-api-quickstart.md`, `internal/httpapi/openapi.yaml`, and §9 of the spec (status).

## Tasks

### Task 1: the `api_auto` surface

- [ ] Test: every entry of `jevconf.Surfaces` has `Egress`, `Describe` and `DefaultThreshold` (an existing test may cover it: extend it); `jev status --json` lists `api_auto` with default threshold 0 and `jev enable api_auto --yes` prints the egress list and switches it on.
- [ ] Add `APIAuto Surface = "api_auto"`, append it to `Surfaces`; `Egress`: "the first 4,000 characters of the last user message", "the names, descriptions and validated cost and latency of the models this API serves"; `Describe`: title "OpenAI-compatible API: auto model", description "When a request asks for the model `auto`, Jev picks the runtime and model from the prompt, among the models the server's confinement policy allows."; `DefaultThreshold[APIAuto] = 0`.
- [ ] Commit `feat(jev): the api_auto surface`.

### Task 2: validation stats on the catalog's models

- [ ] Test (`catalog_test.go`): a model with a passing roster row carries its `CostUSD`, `HasCost` and `LatencyMs`; one without has zeros and `Validated` false.
- [ ] Build a `stats` map next to `validatedIDs` from the roster rows of the ready models; fill the three fields in `refresh`.
- [ ] Commit `feat(openaiapi): the catalog carries each validated model's cost and latency`.

### Task 3: the rule and the choice

- [ ] Tests (`auto_test.go`, table tests on pure functions first): `ruleChoice` (cheapest then fastest validated; unknown cost sorts last; no validated: runtime default by the order claude, codex, antigravity, then alphabetical; no default: first candidate by that order; empty list: none); `truncateRunes` (rune boundary, 4,000); the option description text names label, class, and cost/latency when known.
- [ ] Implement `AutoFuncs{Available, Choose, Threshold}`, `AutoStatus{Available bool; Missing string}`, `ruleChoice`, `truncateRunes`, `autoDescription(m)`; `Config.AutoTimeout` (default 8 s).
- [ ] Tests for `(*Gateway).pickAuto` with fake `AutoFuncs`: Jev's pick wins (`jev`); an id outside the candidates, an error, a timeout, a probability under the threshold and an empty id each give the rule's pick (`rule`); one candidate asks nothing; the options sent are exactly the candidates; the prompt sent is the last user message cut to 4,000 characters.
- [ ] Commit `feat(openaiapi): choose a model for auto, with Jev and a rule`.

### Task 4: the routes

- [ ] Tests through the harness (`auto_test.go`): not available (no key, or surface off): `auto` is not listed, `GET /v1/models/auto` and a completion are 404 `model_not_found` with the message naming what is missing; available: listed first with `confinement` the policy cap, retrievable, and a completion with `"model":"auto"` runs the picked runtime and model (the fake `Exec` sees them), answers with `model` and `X-Monoagent-Model` of the pick and `X-Monoagent-Auto`; a `chat-only` policy offers Jev only chat-only models, and so does a context key; a busy server answers 429 and Jev is not asked; a streamed request carries the same headers; the log line has `auto=` and no prompt.
- [ ] `chat.go`: compute the effective policy before resolving; `auto` goes through `resolveAuto` (availability, candidates via `catalog.Visible`, slot, pick), anything else through `Resolve` as before. `models.go`: list and retrieve. `errors.go`: `errAutoUnavailable(missing)`; remove the "not implemented yet" branch of `errModelNotFound` and its test.
- [ ] Commit `feat(openaiapi): serve the auto model`.

### Task 5: the production wiring

- [ ] Tests (`auto_wiring_test.go`, `jevtest` fake server through `TYPESAFE_BASE_URL`, `TYPESAFE_API_KEY` from the test): `DefaultAuto` is unavailable without the key and without the surface, available with both and without decrypting anything; `Choose` sends one `choice` question whose options are the candidates, whose state has `untrusted_prompt`, records one `jev_usage` row under `api_auto`, and does not retry; the profile's threshold is read from the settings.
- [ ] Implement `DefaultAuto(db *sql.DB)` and set `DefaultDeps().Auto` to it.
- [ ] An end-to-end test through the real HTTP handlers with `DefaultDeps`' Auto, the fake monomind script and the fake Jev server.
- [ ] Commit `feat(openaiapi): wire the auto model to Jev`.

### Task 6: the CLI

- [ ] Tests (`api_models_status_test.go`): `api models --json` has an `auto` object (`available`, `missing`, `candidates`) for the active profile and the table says it in one line; `api status` says whether auto works for the active profile.
- [ ] Implement in `api_models.go` and `api_status.go`; update `ref api`.
- [ ] Commit `feat(cli): api models and api status say whether auto is available`.

### Task 7: docs and the spec

- [ ] `AGENTS.md` (the `/v1` section: `auto`, headers; the Jev surfaces list: `api_auto` and its egress), `SECURITY.md` (what leaves the machine, that Jev only picks among allowed options and a failure falls back to a rule, never to something wider), `CHANGELOG.md`, the quickstart (enable, curl, what the headers say; replace "changes nothing for now"), `internal/httpapi/openapi.yaml` (the `auto` id, `X-Monoagent-Auto`, the 404 text), spec §9 marked implemented.
- [ ] Lint the OpenAPI file (`npx --yes @redocly/cli@2.49.0 lint internal/httpapi/openapi.yaml`).
- [ ] Commit `docs(api): the auto model`.

### Task 8: an opt-in live check

- [ ] `TestLiveAutoPicksAModel` (skipped without `MONOAGENT_LIVE_API_TESTS=1` and `TYPESAFE_API_KEY`): a prompt, `auto`, a `chat-only` policy: 200, `X-Monoagent-Auto` is `jev` or `rule`, the model is one the list offered.
- [ ] Commit `test(api): a live check of the auto model`.

## Verification

`gofmt -l .`; `go build ./...` and `go build -tags nosocial ./...`; `go vet ./...`; `go test ./...` with and without `-tags nosocial` (only the 8 known failures); `go test -race ./internal/openaiapi ./internal/jev/... ./cmd/monoagentcli -run 'Auto|Jev|API'`; the OpenAPI lint; a smoke run of the real binary under a throwaway `HOME` with a fake TypeSafe server (`TYPESAFE_BASE_URL`), a key, `jev enable api_auto --yes`, and a curl with `"model":"auto"`; then a read-only review of the diff (correctness and security) before the PR.
