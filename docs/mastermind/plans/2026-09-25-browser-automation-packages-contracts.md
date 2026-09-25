# Browser Automation Packages — Build Contracts

Spec: `docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md`
(read it first; section numbers below refer to it).
Worktree: `~/scratch/wt-automation`, branch `feat/browser-automation-packages`.

This file is the single source of truth for **who owns which file** and the
**interfaces between builders**. If you need something that another builder
owns, do not edit their file: code against the contract, and put the request
in your final report under "Requests for other owners".

## 0. Working rules (every builder)

- Work only in `~/scratch/wt-automation`. Before every commit run
  `git -C ~/scratch/wt-automation branch --show-current` and confirm it prints
  `feat/browser-automation-packages`. Never touch `~/projects/monoes/mono-agent`.
- Every Bash call that builds or tests starts with
  `export GOTMPDIR=/home/monoes/scratch/agent-tmp TMPDIR=/home/monoes/scratch/agent-tmp`.
  Scratch data goes under `~/scratch/automation-<yourname>/`, never /tmp.
- Commit only your own files with `git commit -F - -- <paths>` (pathspec form: commits exactly those paths even if others have staged files in the shared index; never a bare `git commit` after `git add`, never `git add -A` / `.`),
  small conventional commits (`feat(automation): …`). Use
  `git commit -F - <<'MSG' … MSG` (quoted heredoc). No Co-Authored-By or
  Claude attribution lines. Other builders commit concurrently in the same
  worktree: if a commit fails on the index lock, retry.
- TDD: tests next to code. Files under 500 lines. `gofmt`, `go vet`.
- Before reporting done: `go build ./... && go build -tags social ./...`,
  `go test` of **every package you touched** (state the exact command and
  the pass count), plus `go vet` on them. Report honestly: failures, skipped
  work, anything stubbed. A test you did not run is not passing.
- Never start a bridge on 9222 or talk to the user's browser. Live browser
  tests use the private bridge recipe: scratch HOME +
  `MONOAGENT_EXTENSION_PORT=9232` (see the test wave).
- AI: always the monomind runner (`monomind agent exec`, as
  `internal/capturesummary/runner.go` does). Never the in-app AI provider.
- GUI logic: none. Functionality is a `monoagentcli … --json` command; the
  Wails binding shells out to it and returns stdout.

## 1. Already in place (contract commit)

- `data/actions/<p>/*.json` moved to `data/automations/<p>/actions/*.json`;
  `data.ActionsFS` renamed `data.AutomationsFS` (`//go:embed automations`).
  Minimal seed manifests at `data/automations/<p>/automation.json`.
- `internal/action/package_ctx.go`: `PackageContext`, `FragmentDef`,
  `SelectorEntry`, `SelectorCandidate`, `AriaSelector`, `WaitSpec`,
  `TransformOp`, `SelectorObserver`, `DefSource`.
- New `StepDef` fields (`executor.go`): `sideEffect, until, fragment, action,
  inputs, items, as, steps, key, script, input, ops, fields, path`.
  New `ActionDef` fields (`loader.go`): `$schema, automation, sideEffects,
  outputSchema, provenance`.
- `internal/action/executor_pkg.go`: `SetPackage`, `SetSelectorObserver`,
  `SetSafeMode`, `SafeStopped`, `SafeStop`.
- `internal/action/steps_ext.go`: `initExtendedHandlers()` stub, called from
  `initHandlers`.
- `internal/action/validate.go`: `Issue`, `Validate` stub, `HasErrors`.
- `internal/action/loader.go`: `SetDefSource`, `CurrentDefSource`.
- `internal/automation/types.go` + `api.go`: all public types and function
  signatures (skeleton returning `ErrNotImplemented`).
- `internal/recording/types.go`: WS frame, event, fingerprint, summary types.

Signatures in these files are the contract. Adding methods/fields is fine
(tell the lead); changing or removing existing ones needs a request.

## 2. Ownership

| Builder | Owns (create/edit) | Must not edit |
|---|---|---|
| **core** (action engine) | `internal/action/{executor.go,executor_pkg.go,loader.go,steps.go,page_lookup.go,validate.go,package_ctx.go,variables.go,errors.go,jevfallback.go}` + new `internal/action/{domains.go,selectors_pkg.go,safe_mode.go}` + their tests | `steps_ext*.go` |
| **steps** (new step types) | `internal/action/steps_ext*.go` (split: `steps_ext_flow.go`, `steps_ext_input.go`, `steps_ext_extract.go`, `steps_ext_transform.go`, `steps_ext_script.go`) + tests | everything else in `internal/action` |
| **registry** (packages) | `internal/automation/**` except `health*.go` | `cmd/**`, `internal/action/**` |
| **seed** (built-in manifests, templates, schemas) | `data/automations/**`, `data/automation-templates/**`, `data/schemas/**`, `internal/schemagen/**` (new: struct → JSON Schema generator + drift test), `data/automations/README.md` | Go outside `internal/schemagen` |
| **cli** (automation/action CLI) | `cmd/monoagentcli/automation*.go` (new, except `automation_doctor.go`), `cmd/monoagentcli/action.go`, `cmd/monoagentcli/action_template.go`, `cmd/monoagentcli/action_export.go` (new), `cmd/monoagentcli/root.go` | `internal/**` |
| **runtime** (node/runtime wiring) | `cmd/monoagentcli/login.go`, `internal/nodes/browser_register.go`, `internal/nodes/browser_adapter.go`, `internal/browser/provider.go`, `internal/workflow/schema_loader.go`, `internal/connections/manager.go`, startup wiring (wherever `SetGlobalSessionProvider` / node registration happens, incl. `wails-app` startup if it registers nodes), a shared `internal/automation/boot` is **not** yours — call `automation.Default()`, `Seed(data.AutomationsFS)`, `action.SetDefSource(reg.DefSource())` from startup | `internal/automation/**`, `cmd/monoagentcli/automation*.go` |
| **recorder** (extension) | `chrome-extension/recorder*.js`, `chrome-extension/sidepanel_record*.js`, `chrome-extension/sidepanel.html`, `chrome-extension/sidepanel.css`, `chrome-extension/background.js`, `chrome-extension/manifest.json` + `*.test.mjs` | Go |
| **ingest** (recording storage) | `internal/recording/**`, `internal/extension/{protocol.go,server.go,request.go,recording.go}` (routing of `kind:"recording"` frames; extension→Go request methods `record.list`, `record.analyze`, `record.verify`, `record.save` which exec this binary's `record … --json` and return its JSON), `internal/capture/meta.go` (add source "recording"), `cmd/monoagentcli/record.go` (list/show/delete + the `record` parent command) | `cmd/monoagentcli/record_analyze*.go` |
| **analyze** (AI + verify + save) | `internal/recordanalyze/**`, `data/skills/record-to-action.md`, `cmd/monoagentcli/record_analyze*.go` (analyze/verify/save subcommands, defined as `func addRecordAnalyzeCommands(parent *cobra.Command, cfg *globalConfig)` in `record_analyze.go`; ingest's `record.go` calls it when building the `record` command) | `internal/recording/**` |
| **gui** | `wails-app/app_automations.go`, `wails-app/frontend/src/pages/Connections.jsx`, `wails-app/frontend/src/pages/connections/**` (new), `wails-app/frontend/src/services/api.js` (additions only) | Go outside `wails-app/app_automations.go` |
| **health** (selector health, doctor, workflow bundle) | `data/migrations/0NN_automation_selector_health.sql`, `internal/automation/health*.go`, `cmd/monoagentcli/automation_doctor.go`, workflow export/import `--bundle-automations` (in the workflow export/import CLI files) | `internal/automation` non-health files |

`go.mod`/`go.sum`: only the lead edits; ask.

## 3. Action engine contract (core + steps)

- `Validate(def, pkg)` returns errors for: unknown step type
  (`unknown_step_type`), step type outside `pkg.PermittedSteps()`
  (`step_not_permitted`), call_fragment to a missing fragment, page_script
  to a script not in the package, `configKey` not in package selectors when a
  package is set and no fallback, missing `id`, duplicate ids, loop steps
  referencing unknown ids, `navigate` URL literal outside `pkg.Domains()`.
  Warnings: `page_script` used (`script_used`), no `sideEffects` declared.
  `KnownStepTypes() []string` exported (union of core + extended handlers).
  **All shipped built-in actions must validate with zero errors** (test).
- `Execute` runs `Validate` first and refuses on errors.
- Unknown step type at run time is an error, not a skip.
- Domain enforcement: `navigate`, `http_fetch_in_page`, and any URL change
  observed after a step are checked against `pkg.Domains()` (exact host or
  `*.suffix`; `*.x.com` also matches `x.com`). Off-domain → step error
  `off_domain`. No package / empty domains → unrestricted.
- Selector resolution order for a step: `xpath` → `selector` →
  `configKey` via `pkg.Selector()` candidates in order (css, xpath, aria via
  CDP `Accessibility.queryAXTree` or an in-page role/name matcher, text) →
  existing `ConfigInterface` 3-tier → `alternatives[]` → Jev `intent`.
  Each package-selector lookup calls `selObs.ObserveSelector` if set.
- Safe mode: before executing a step with `SideEffect`, stop, set
  `safeStop`, return success. Applies inside fragments/for_each/call_action.
- `SetPackage(nil)` = legacy behaviour; the 65 legacy actions behave exactly
  as before (existing tests are the guard).
- steps builder implements exactly: `call_fragment`, `call_action`,
  `for_each`, `wait_for`, `assert`, `select_option`, `press_key`,
  `extract_table`, `extract_json`, `transform`, `page_script`,
  `http_fetch_in_page`, `download` — semantics in spec §6.1 and the field
  comments in `package_ctx.go`/`executor.go`. `page_script` evaluates the
  script (from `pkg.Script`) in the page via `page.Eval`, wrapped as
  `(function(args){ <script> })(<json args>)`; the script must `return`
  JSON-serialisable data; 30s default timeout. `http_fetch_in_page` runs
  `fetch()` in the page (credentials: include), domain-checked, returns
  `{status, headers, body}` (body parsed as JSON when content-type is JSON).
  `download` requires the manifest permission (checked via a new
  `PackageContext`-independent executor flag the core builder exposes as
  `SetDownloadsAllowed(bool)` — coordinate: core adds it).

## 4. Registry contract (registry)

Implements every function in `internal/automation/api.go`:

- Layout: `<home>/automations/index.json` +
  `<home>/automations/<id>/<version>/…` (package files) +
  `<home>/automations/<id>/overlay/selectors.json`. Keep the current and one
  previous version.
- `index.json`: `{"version":1,"packages":{"<id>":{"version","previous",
  "source","trust","enabled","removed","seedSha256","installedSha256",
  "installedAt"}}}`. Atomic writes (temp + rename) and a file lock
  (reuse the repo's existing lock helper if one exists; else flock).
- `Seed(builtins)` per spec §5.1: skip removed; never downgrade; if the
  installed built-in's files differ from `seedSha256` (user-modified),
  install the new version alongside as previous→current? **No**: keep
  the user's copy current, record `pendingSeedVersion`, and surface it as a
  warning in `InstalledInfo` (`UnavailableReason` stays empty; add field
  `PendingUpdate string`). Called on every CLI start that touches automations
  (cheap: compares versions + hashes, only writes on change).
- `Install`: accepts `.mpkg`, directory, http(s) URL (download to scratch,
  size cap 20MB). Runs `Validate` (errors abort), `PolicyAllows` (blocked →
  installed but disabled with reason), engine check, zip-slip protection,
  file count/size caps, CHECKSUMS verification when present. `DryRun`
  returns the full `Review` including `Changes` vs the installed version.
- `Export`: deterministic zip (sorted entries, fixed mtime 2000-01-01,
  generated `CHECKSUMS`), excludes `recordings/` unless `WithRecordings`,
  never includes overlay/sessions. Actions subset → closure (fragments,
  selector keys, scripts referenced, transitively) and manifest `actions`
  rewritten. Round-trip test: export → install → export byte-identical.
- `PolicyAllows`: `tier=="social"` or any domain matching the social list
  (instagram.com, linkedin.com, x.com, twitter.com, tiktok.com,
  facebook.com, threads.net) → allowed only when
  `bot.PlatformCompiledIn(<that platform>)`-style social build is on
  (use the existing social gate in `internal/bot`).
- `Package.Context()` implements `action.PackageContext` (overlay selector
  wins over package selector). `ResolveAction("x.y")` looks up other
  installed packages through the registry the package came from.
- `DefSource()`: `List` = enabled+available packages' actions as
  `<id>/<action>`; `Load` reads the file; `Package` returns the context.
- Legacy migration: on `Seed`, if `~/.monoagent/actions/<p>/*.json` exist,
  wrap them into package `local-<p>` (source local, generated manifest,
  `requires.native` = p if that bot exists), once (marker in index).
- `Registry.Default()` = `Open(~/.monoagent)` honouring `$HOME`.

## 5. CLI JSON contract (wiring → gui, analyze)

All commands accept `--json`; errors with `--json` print
`{"error":"…"}` and exit 1.

```
automation list [--all] --json   → {"automations":[InstalledInfo + {"session":{"loggedIn":bool,"username":"","expiresAt":"RFC3339"},"pendingUpdate":""}]}
automation show <id> --json      → {"info":InstalledInfo,"manifest":Manifest,"actions":[{"name","description","sideEffects","containsScript":bool,"nodeType":"<id>.<action>","inputs":[{"name","type","required":bool,"description","default","options","ui":{}}],"outputs":["…"]}],"fragments":["…"],"issues":[IssueJSON]}
automation new <id> --template <t> [--dir D] --json → {"dir":"…"}
automation validate <dir|file> --json → {"ok":bool,"issues":[IssueJSON]}
automation test <id> [action] [--live] --json → {"results":[{"action","fixture","ok","message"}]}
automation pack <dir> -o F --json → {"file":"…","sha256":"…"}
automation install <src> [--dry-run] [--yes] --json → InstallResult   (without --yes and not --dry-run, non-interactive → error "confirmation required")
automation export <id> -o F [--actions a,b] [--with-recordings] --json → {"file":"…","sha256":"…"}
automation uninstall|restore|enable|disable|rollback <id> --json → {"ok":true,"info":InstalledInfo|null}
automation doctor [id] --json    → {"automations":[{"id","issues":[…],"selectors":[{"key","ok","fail","healed","lastOk","lastFail","status":"ok|decaying|broken"}],"session":{…}}]}
action export <id>.<action> -o F --json → {"file":"…"}
action import <file> [--into <id>] [--yes] --json → InstallResult
record list --json               → {"recordings":[recording.Summary]}
record show <rec> --json         → {"summary":Summary,"events":[Event],"artifacts":["…"]}
record delete <rec> --json       → {"ok":true}
record analyze <rec> [--automation <id>] --json → {"draftDir":"…","draft":Draft}
record verify <draftDir> [--full] --json → {"steps":[{"id","type","status":"pass|healed|fail|stopped_before_side_effect|skipped","message","selector"}],"stoppedAt":SafeStop|null,"ok":bool}
record save <draftDir> [--as action|fragment|workflow] [--automation <id>|--new <id>] [--name N] --json → {"automation","action","version","nodeType","workflowId":""}
login <automation>              (existing command; reads manifest.login when present)
```

`automation list` also calls `Seed` first. The session block comes from
`crawler_sessions` (same source `login status` uses).

Draft (analyze → verify → save): a directory
`~/.monoagent/recording-drafts/<recordingId>/` laid out **as a package**
(automation.json, actions/<name>.json, selectors.json, fragments/, scripts/)
plus `draft.json`:
`{"recordingId","targetAutomation","isNew":bool,"action":"<name>","saveAs":"action|fragment|workflow","names":{"automation","action","fragment"},"lint":[IssueJSON],"segments":[{"from","to","action"}],"createdAt"}`.
verify opens it with `automation.OpenDir`; save calls `Registry.AddAction`
(or, for `--as fragment`, a fragment merge) and deletes nothing.

## 6. Recording protocol (recorder ↔ ingest ↔ analyze)

Types: `internal/recording/types.go` (authoritative; JS mirrors it).
Transport: the extension's existing WS to the bridge. Frames are JSON with
`"kind":"recording"`. ingest routes them in the WS read loop before the
Response/request handling, writes to a spool, and finalises the envelope on
`stop` (or after 30 min idle → `Complete=false`; a recording with no events
is discarded, not written). Envelope via `internal/capture` writer:
`meta.json` (`source:"recording"`, `Extra`: goal, tabId, stopReason,
eventCount, complete, warnings), `events.jsonl`, `dom-<eventId>.html`,
`network.jsonl`.
**Storage (revised after the security review):** recordings are *not* kept
in the capture inbox — `~/.monomind/inbox` feeds monomind's knowledge
ingest. They live in their own store: `~/.monoagent/recordings/` when the
frames name no profile, `~/.monoagent/profiles/<id>/recordings/` for
profile `<id>`, 0700 dirs and 0600 files; spools and restart recovery live
there too. On first use, recordings older builds left in the capture
inboxes are moved into their store (each move logged; runs once, marker
`.migrated-from-inbox`). `recording.List/Find/Load/Delete/DraftsDir` keep
their signatures; `recording.StoreDir(profile)` names a store.
Recorder privacy rules (spec §8.2) are enforced in the extension **and**
re-checked in ingest (sensitive fields masked — `inputType` password/hidden,
`autocomplete` cc-*/password, the recorder's `sensitive` flag, secret-like
names — snippets scrubbed, URLs sanitised).
v1 is one tab: a new tab ends the recording with reason `new_tab`.

## 7. Waves

1. **Build** (parallel): core, steps, registry, seed, wiring, recorder,
   ingest, analyze, gui, health.
2. **Integrate** (lead): merge-free (same branch) — lead builds, runs the
   full suite, resolves cross-owner requests.
3. **Port**: hackernews to declarative (no `requires.native`), E2E harness.
4. **Review**: security, correctness/contracts, extension, GUI (browser).
5. **Fix**, re-verify, docs/CHANGELOG, PR.

## 8. Security hardening contract (after the security review)

Trust tiers (registry index `trust`): `builtin` | `local` (hand-authored or
`automation new`) | `recorded` (saved from `record save`) | `imported`.
Only `builtin` and `local` get the bare-name vault fallback; `recorded`
and `imported` read only `automation:<id>/<name>` secrets. Unknown/failed
lookup ⇒ treat as `imported` (fail closed).

New optional interfaces a `PackageContext` may implement (registry
implements all):
- `Trust() string`
- `CallActions() []string` — manifest `permissions.callActions`: exact
  `"<automation>.<action>"` refs this package may call (own-package calls
  need no declaration).
- `ScriptsAllowed() bool` — false for `imported`/`recorded` packages unless
  the user opted in (`automation trust <id> --scripts`), true for
  builtin/local.
- `LiveRunConfirmed() bool` — imported packages with an action of
  sideEffects ≥ write refuse to run for real until the user confirms once
  (`automation trust <id> --live`); verify --safe is unaffected.

Engine (core/steps):
- `SetSecretLookup(func(automationID, name string) (string, bool))` — the
  executor passes the CURRENT package id (re-scoped inside call_action).
- `SetGlobalHostDeny(func(host string) (bool, string))` — package-wide deny
  applied in every URL check (runtime installs: social hosts when
  `!bot.PlatformCompiledIn`). URL checks fail CLOSED when the page URL
  can't be read. Ports are part of the match when the pattern has one.
- `upload`: with a package, a path is allowed only if it (a) equals the
  value of an input whose declared type is `file`/`path`, or (b) resolves
  inside `~/.monoagent/uploads/<id>/` or the run's fsconfine root. Upload is
  always treated as a side effect by safe mode.
- `call_action`: refs must be literal (no templates — validate error);
  cross-package refs must be listed in `CallActions()`; an `imported`
  or `recorded` caller may never call a `builtin` or social-tier target.
- `page_script`/`http_fetch_in_page`: refused at run time when
  `!ScriptsAllowed()` (http_fetch_in_page counts as a script capability).
- Safe mode also stops before: page_script, upload, download,
  non-GET http_fetch_in_page, call_action whose target sideEffects ≥ write.
- Validate: error when action sideEffects ≥ write and no step is flagged.
- Secret masking: every resolved secret value is redacted from logs,
  StepResult data/errors, events, FailedItems and saved data; resolved
  text is never re-resolved; a plain variable never shadows `{{secret:}}`.
- http_fetch_in_page / download: `redirect:'manual'`, hops followed in Go
  with a host check each hop.

Registry: manifest `permissions.callActions`; domain patterns need ≥ 2
labels and must not be a public suffix (`golang.org/x/net/publicsuffix`),
same for `*.` globs; `login.url` and `site.startUrl` must be inside
`site.domains`; installing an imported package over an existing
`builtin`/`local` id requires `InstallOptions.ReplaceBuiltin`; Review adds
`source`, `replaces` (id+source), `computedTier`, `native`, `callActions`,
`loginURL`, `scripts` WITH source text (`scriptSources map[name]string`),
`capabilities` (plain-language list: "can run scripts in the page that can
read site data and send it anywhere", "can upload local files", ...).
Index adds `scriptsAllowed`, `liveRunConfirmed`; API `SetTrustFlags(id,
scripts, live *bool)`. `AddAction` takes `InstallOptions.Trust`.

CLI: `automation trust <id> [--scripts|--no-scripts] [--live|--no-live]`,
`install --replace-builtin`, `record save` saves with trust `recorded`.

## 9. Re-record a single selector (round 2)

Flow: Health tab (or CLI) → `monoagentcli automation rerecord <id> <selectorKey>
[--url U] [--timeout 180s] --json` → opens a tab at U (default
`site.startUrl`) in the user's browser via the same connect path `node run`
uses → sends extension command `pick_element` → the page shows a picker
overlay ("Click: <intent or key>", Esc cancels, hover outline) → the user
clicks once → the extension answers with a `recording.Fingerprint` (ranked,
uniqueness-checked candidates, sensitive-field rules apply: never return a
value) → CLI builds an `action.SelectorEntry` (candidates from the
fingerprint, unique ones first, previous entry's intent kept, verifiedAt now)
→ `Registry.ReplaceSelector(id, key, entry)` → JSON result.

- Extension command (Go → extension): `Command{Type:"pick_element",
  TabID, Params:{"prompt":string,"timeoutMs":int}}`. Response data:
  `{"fingerprint": Fingerprint, "url": string}` or error `"cancelled"` /
  `"timeout"`. Go constant `CmdPickElement = "pick_element"` in
  internal/extension/protocol.go; `(*ExtensionPage).PickElement(ctx,
  prompt string, timeout time.Duration) (*recording.Fingerprint, string,
  error)` — owner ingest. If importing internal/recording from
  internal/extension would cycle, return json.RawMessage and let the CLI
  decode.
- Registry: `ReplaceSelector(id, key string, e action.SelectorEntry)
  (where string, err error)` — local packages (source+trust local): rewrite
  selectors.json in place under the lock (refresh sha, bump generation);
  everything else: overlay full entry (existing overlay v2 guard). `where` is
  "package" or "overlay". Key must already exist in the effective selectors
  (error otherwise).
- CLI JSON: `{"automation","key","where","candidates":[…],"url"}`; errors
  `{"error"}` incl. "cancelled", "timeout", "browser bridge not connected".
- GUI: Health tab row action "Re-record" for decaying/broken (and on any
  row via a menu); binding `RerecordSelector(id, key)` shells the CLI with a
  long timeout, shows result and refreshes Health.
