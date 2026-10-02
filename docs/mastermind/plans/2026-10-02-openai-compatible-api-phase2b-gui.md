# OpenAI-compatible API, phase 2b: the desktop GUI section, Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Settings gets an "OpenAI-compatible API" section (spec §8.4, mounted after the Jev section): where `/v1` listens and whether it runs, the active profile's API keys (create with a show-once panel, context switch, revoke) and the models with what the listener's policy serves, including `auto` and how to switch it on in the Jev section.

**Architecture:** The CLI is the source of truth (D10). `wails-app/app_api.go` runs `monoagentcli api … --json` through `runMonoCLI` and decodes typed DTOs; no SQL, no `internal/*` import. The section (`ApiSection.jsx`, parts in `components/settings/api/`) reads only those DTOs. Base commit `d0f9fb16`: the JSON includes `policy.auto_confinement`, `models[].auto_allowed`, `auto.{confinement,candidates,held_back}` and `listeners[].auto_confinement`. Unknown fields are ignored and missing ones are absent, so an older CLI still renders.

## Global Constraints

- TDD: stubs first (Go methods, components) so the first red run fails on an assertion, then the code; then mutation-check each behaviour test at run time (break the production code, see the test fail, restore).
- Files under 500 lines. Reuse the `.btn`, `.data-table`, `.modal*` and `.form-input` classes, Jev's `Switch` (now exported), `copyText` and the global `confirm()`; tokens only. Never `appearance` or `background` on a form control (`selectStyle.test.js`).
- A new key lives in the create dialog's state only: not in `localStorage`, the console, a URL, an error string or the parent's state. It is cleared on Done, Escape and close.
- `wails-app/*.go` imports nothing from `internal/monomind` (`doctrine.test.js`) and, here, nothing from `internal/*`.
- Do not touch `internal/mcp` (another agent) or the CLI. Conventional commits with the `Co-Authored-By: Claude Sonnet 5.5` trailer. Plain separate git commands; never a bare `go build ./cmd/monoagentcli`.

## Decisions this plan adds to spec §8.4

1. **The listener shown** is the main one when it is loopback and serves `/v1`, else the dedicated `--v1-addr` one (else the main one, only to say why `/v1` is not served). The state text ports `listenerNote` one-to-one: not reachable, the daemon does not mount `/v1` on a loopback main listener (`loopback && !v1`), a main listener off loopback (`!loopback && !v1`), answers `/health` only (restart), serving.
2. **The base URL** has no scheme in `api status`: derive `http` for the main listener, `https` for a dedicated off-loopback one and `http` for a dedicated loopback one (wrong when the server has `MONOAGENT_API_TLS_CERT` set: an owner question). A wildcard host (`0.0.0.0`, `::`, none) is shown as `localhost` with a hint to use the machine's address from elsewhere.
3. **The model table is evaluated for that listener**: `api models --for loopback|network --confinement C --context-confinement C --auto-confinement C`, from the listener's reported policy, because this app's environment rarely matches the daemon's. The caption says whether the policy is the daemon's (`confinement_source: daemon`) or assumed.
4. **Load cost:** `api status` on mount (it never decrypts); `api key list` and `api models` (it scans monomind and lists every runtime) on the first expand and on Refresh. The fold header shows the state and `keys.length` (else `keys.active`).
5. **Auto row:** the model table has an AUTO column (`auto_allowed`). When `auto.available`, the row says what it picks among (`candidates`, `confinement`) and, when `held_back > 0`, how many served models auto may not pick and that `--auto-confinement` (`MONOAGENT_API_AUTO_CONFINEMENT`) on the server raises it. When not, it shows the CLI's `missing` text and, if that is about Jev (`api_auto` or a Jev key), a button that calls `onNavigate('settings', { section: 'jev' })`, the dashboard's deep link. A second jump after the user folds Jev scrolls but does not reopen it (JevSection expands only when `defaultExpanded` changes): same as the dashboard link.
6. **Revoke** asks with the global `confirm()` and passes `--yes`; the context switch applies at once (it narrows the models a key may use; a line under the table says what context adds).
7. i18n: every string under `settings.api.*` in `en.json` and `es.json`, with a parity test that scans the section's files; dynamic keys are avoided so the scan sees them all.

## File structure

- Create `wails-app/app_api.go` (+ `app_api_test.go`): `APIStatus`, `APIModels(for, confinement, contextConfinement, autoConfinement)`, `APIKeyList`, `APIKeyCreate(name, withContext)`, `APIKeySetContext(id, on)`, `APIKeyRevoke(id)`.
- Modify `frontend/src/wailsjs/go/main/App.js`, `App.d.ts` and `go/models.ts`: only the new entries.
- Create `frontend/src/components/settings/ApiSection.jsx` (fold header, loading, refresh) and `api/`: `apiModel.js` (pure: listener choice, state, base URL, models args, relative time), `ui.jsx` (shared styles, badge), `ApiStatusBlock.jsx`, `ApiKeysBlock.jsx`, `ApiKeyDialog.jsx`, `ApiModelsBlock.jsx`; one `*.render.test.jsx` or `*.test.js` each.
- Modify `JevSection.jsx` (`export` on `Switch`), `pages/Settings.jsx` (mount, `onNavigate`), `pages/deeplinks.render.test.jsx` (mock `ApiSection`: it mocks the bindings with three exports), `locales/en.json`, `es.json`; create `locales/settingsApiKeys.test.js`.
- Docs: `AGENTS.md` (what the desktop app calls, the section), `CHANGELOG.md` `[Unreleased]`.

## Tasks

### Task 1: the Go bindings
- [ ] Stub `app_api.go`; test with a fake CLI (the `fakeJevCLI` pattern): the exact argv per method (`--name=N`, `--context`, `update <id> --context|--no-context`, `revoke <id> --yes`, each `api models` flag only when non-empty); `[]` for a nil key list; `APIKeyCreate` returns the key and no other method decodes one; an id that is empty or starts with `-` and an empty name are refused before the CLI runs; the CLI's stderr surfaces as the error; unknown JSON fields are ignored. Opt-in `API_REAL_CLI=1` test against the real CLI in a scratch HOME.
- [ ] Mutation checks: drop `--yes`, the id guard, the nil to `[]`. Commit `feat(gui): bindings for the OpenAI-compatible API settings`.

### Task 2: the Wails bindings
- [ ] Try `wails generate module` (v2.11.0 here, `go.mod` pins 2.16.0); keep only the `API*` hunks, or place them by hand in Go byte order beside the neighbours. Commit `feat(gui): Wails bindings for the API settings`.

### Task 3: the pure helpers (`apiModel.js`)
- [ ] Tests with fixtures from `api_status_test.go` and `api_models_status_test.go`: listener choice, each state of decision 1, the base URL (scheme, wildcard, IPv6), `modelsArgs`, held-back and Jev-missing detection, relative time. Mutation-check each branch. Commit `feat(gui): the API section's pure helpers`.

### Task 4: the create dialog
- [ ] Tests (real `i18n`): the name is required; the CLI is called with name and context; the panel shows the key once; copy; Done and Escape close and the key is gone from the DOM even when the parent keeps `open` true; `onCreated` gets metadata without the key; nothing reaches `localStorage` or `console`; a CLI error is shown and does not contain the key; focus stays inside. Mutation: remove the clear, pass the key up. Commit `feat(gui): the API key dialog with a show-once panel`.

### Task 5: the keys block
- [ ] Tests: columns (name, prefix, created, last used or never), context switch calls `APIKeySetContext`, revoke only after the confirm resolves true, empty state, CLI errors inline. Commit `feat(gui): the API keys table`.

### Task 6: the status block and the models block
- [ ] Tests per state and per fixture, with and without the `auto_*` fields. Mutation: change a state's condition, drop the held-back note. Commit `feat(gui): the API status header and the models table`.

### Task 7: the section, the mount and the locales
- [ ] Container test (`window.go` mocks): folded by default, status on mount only, keys and models on first expand, Refresh, create then reload, the Jev jump, a Spanish render. Locale keys and the parity test. Mount in `Settings.jsx`; update `deeplinks.render.test.jsx`. Commit `feat(gui): the OpenAI-compatible API settings section`.

### Task 8: docs and verification
- [ ] `AGENTS.md`, `CHANGELOG.md`. Every step of the CI job "Wails GUI module" locally (`npm ci`, `npm run build`, `go vet`, `go build`, `npm test -- --run`, `go test`), `gofmt -l .`, and a look at the section in a browser (vite dev server, `window.go` mocked from the scratchpad). Commit `docs(gui): the API settings section`.
