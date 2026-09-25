# Browser Automation Packages, Connections Split & Record-to-Action — Design Spec

Date: 2026-09-25
Status: Decisions resolved (§13) — ready for the Phase 0–1 implementation plan
Related: `docs/BROWSER_TRACK_PLAN.md` (GLU-02 "demonstrate → workflow",
RIG-12 "replay", GLU-04), `docs/plans/2026-03-11-connections-design.md`,
`data/actions/README.md`, `docs/USAGE_POLICY.md`

## 1. Goal

Three linked outcomes:

1. **Connections page split.** Two top-level sections:
   **Browser Automations** (called *actions* / *crawl*: things driven through
   the user's real browser) and **API Connections** (OAuth, API key,
   connection string, app password, SSH key).
2. **Browser automations as portable packages.** Every automation (e.g.
   `hackernews`, `linkedin`, or a user's own `acme-crm`) follows one template
   layout: a manifest, action files, selectors, forms, optional scripts and
   test fixtures. Adding an action means adding files, not recompiling.
   A package, or any single action in it, can be exported in full and
   imported on another machine.
3. **Record → analyze → replicate.** The Chrome extension records a user
   doing something. The recording is saved, an AI turns it into an action
   definition (parameterized inputs, extracted outputs, robust selectors),
   the action is replayed to verify it, and the result is saved as either a
   whole action (so a workflow node) or a fragment reused inside another
   action.

## 2. What exists today (verified in code)

It is the right foundation: most of the pieces exist, but they are not
packaged or connected.

| Area | Today | Gap |
|---|---|---|
| Action format | `data/actions/<platform>/<action>.json` (`internal/action/loader.go:26` `ActionDef`, `executor.go:24` `StepDef`). 19 step types, loops with daily caps, templating, 3-tier selectors (`xpath` → `selector` → `configKey` → `alternatives`), Jev `intent` fallback (`jevfallback.go:72`) | Unknown step types are **skipped silently** (`executor.go:~771`). No JSON Schema file. `version` is informational only |
| Where logic lives | Shipped JSON uses `call_bot_method` 102× but `click` 8× and `type` 4×. Real behaviour is hand-written Go in `internal/bot/<platform>/bot.go` (`GetMethodByName` switch) | **An imported package cannot carry logic.** New bot methods require Go and a rebuild |
| Node exposure | `internal/nodes/browser_register.go:14` registers every action as node `<platform>.<action>` (embedded + `~/.monoagent/actions/`) | User actions get no config form (`schema_loader.go:58` hardcodes 4 platforms) and open `about:blank` (start URL hardcoded in `internal/browser/provider.go:43`). `GetPage` ignores `username` |
| Install | `action template install <file>` copies one JSON into `~/.monoagent/actions/` (`cmd/monoagentcli/action_template.go:149`). Embedded wins on name clash | No manifest, bundle, export, versioning, dependencies or uninstall |
| Authoring | `crawl <url>` → HTML → `/action-template-generator` skill → JSON | Works from static HTML, not from what the user actually did |
| Browser driving | `extension.ExtensionPage` drives the user's real Chrome/Edge over WS `:9222` using `chrome.debugger` CDP. Full command set exists (navigate, click, input, keyboard, eval, wait, race, set_files…) | — (reusable as the replay engine) |
| Extension | MV3 "MonoAgent Bridge": page **snapshots** (MHTML, readable, screenshot), highlights, side panel with AI | **Records no user activity**: no click, input or navigation listeners |
| Capture envelope | `~/.monomind/inbox/<ts>-<slug>/meta.json` + artifacts; classify, summary, docs sync and export pipelines | Reusable as-is for recordings (new artifact names + `Meta.Extra`) |
| Connections storage | API credentials: `connections` table (`internal/connections/storage.go`, vault-backed). Browser logins: `crawler_sessions` table via `login` / `login confirm` (`cmd/monoagentcli/login.go`) | Platforms declared only in Go (`internal/connections/registry.go`) |
| Connections UI | `Connections.jsx` groups by `category`. Browser vs API is decided by a hardcoded `SOCIAL_IDS` set (`:14`). `connectVia` is sent but never read | Most bindings in `app_connections.go` bypass the CLI, which breaks the "UI calls the CLI" rule |

Known bug found while mapping: `connections.Manager.Connect` rejects the
`browser` method and then tells the user to run `connect <p>`, the command
that just failed (`manager.go:80`). It should say `login <p>`.

## 3. Vocabulary

- **Automation (package):** everything needed to drive one website through
  the browser: login detection, actions, selectors, forms, scripts, tests.
  ID is a slug (`hackernews`, `acme-crm`). This replaces "platform" for the
  browser side. `bot.PlatformRegistry` IDs map onto automation IDs.
- **Action:** one runnable unit inside an automation (`submit_post`).
  It is always exposed as workflow node `<automation>.<action>`, the same
  naming as today, so **existing workflows keep working unchanged**.
- **Fragment:** a reusable step sequence (e.g. `dismiss_cookie_banner`,
  `open_compose_dialog`) that actions include through a `call_fragment` step.
  It is not a node on its own.
- **Recording:** a raw event log captured by the extension (an envelope in
  the inbox with `source: "recording"`).
- **Browser session:** the logged-in cookie state for an automation
  (today's `crawler_sessions` row). It is shown on the Connections page and
  owned by the automation's `login` spec.
- **API connection:** everything in the `connections` table. Unchanged.

## 4. Package format

### 4.1 Layout (the "template")

```
acme-crm/                         # directory form (source/editing)
├── automation.json               # manifest (required)
├── actions/
│   ├── create_contact.json       # ActionDef (existing format + new fields)
│   └── list_deals.json
├── fragments/
│   └── dismiss_banner.json       # reusable step sequences
├── selectors.json                # named selectors, referenced by configKey
├── scripts/                      # optional, sandboxed (see §6)
│   └── parse_deal_row.js
├── forms/                        # optional; overrides auto-generated forms
│   └── create_contact.json
├── tests/
│   ├── fixtures/list_deals.html  # recorded DOM snapshots
│   └── list_deals.expect.json    # expected extraction output
├── recordings/                   # optional provenance (see §8), stripped by default on export
├── icon.svg
└── README.md
```

Distributed form: the same tree zipped as **`acme-crm-1.2.0.mpkg`** (a zip
with `automation.json` at the root plus a generated `CHECKSUMS` file listing
sha256 per file). The same code reads either form: the loader takes an
`fs.FS`, which covers `embed.FS`, `os.DirFS` and `zip.Reader`.

### 4.2 Manifest `automation.json`

```json
{
  "schema": "monoagent.automation/v1",
  "id": "acme-crm",
  "name": "Acme CRM",
  "version": "1.2.0",
  "description": "Create contacts and list deals in Acme CRM via the web UI",
  "publisher": { "name": "Jane Doe", "url": "https://example.com" },
  "license": "MIT",
  "engine": ">=0.68.0",
  "category": "crm",
  "icon": "icon.svg",
  "site": {
    "startUrl": "https://app.acme-crm.com/",
    "domains": ["app.acme-crm.com", "*.acme-crm.com"]
  },
  "login": {
    "url": "https://app.acme-crm.com/login",
    "loggedIn": { "selector": "[data-testid=avatar]", "cookie": "acme_session" },
    "usernameFrom": { "selector": "[data-testid=avatar]", "attribute": "title" },
    "sessionTtlDays": 30
  },
  "permissions": {
    "steps": ["navigate", "click", "type", "extract_*", "upload", "transform"],
    "scripts": [],
    "downloads": false
  },
  "requires": { "native": null, "fragments": [] },
  "actions": ["create_contact", "list_deals"],
  "defaults": { "humanLike": true, "timeoutSec": 60 },
  "policy": { "tier": "standard" }
}
```

Notes:

- **`site.domains` is enforced.** A step that navigates outside it fails
  (see §6). This is the main safety property of imported packages.
- **`login`** replaces the hardcoded per-platform login URL and start URL.
  `login <automation>` and the Connections page read it.
- **`requires.native`** names a compiled Go bot (`"instagram"`). Built-in
  social automations set it. A package that needs a native bot missing from
  the running binary installs as *unavailable*, with a clear reason, instead
  of failing at run time.
- **`policy.tier`** is `"standard"` or `"social"`. `social` packages obey the
  same `-tags social` gate as today (see §6.4).

### 4.3 Action file additions (backward compatible)

The existing `ActionDef` stays valid. New, optional fields:

```jsonc
{
  "actionType": "create_contact",
  "automation": "acme-crm",          // replaces "platform"; "platform" still accepted
  "version": "1.0.0",
  "sideEffects": "write",            // none | read | write | message | destructive
  "inputs": { "required": [ { "name": "email", "type": "string", "format": "email",
                              "ui": { "label": "Email", "placeholder": "jane@x.com" } } ] },
  "outputs": { "success": ["contactId", "url"], "schema": { /* JSON Schema of one item */ } },
  "steps": [
    { "id": "open", "type": "navigate", "url": "{{site.startUrl}}contacts/new" },
    { "id": "banner", "type": "call_fragment", "fragment": "dismiss_banner" },
    { "id": "email", "type": "type", "configKey": "contact.email_input",
      "intent": "the Email field of the new-contact form",
      "value": "{{email}}" },
    { "id": "save", "type": "click", "configKey": "contact.save_button",
      "sideEffect": true,
      "waitFor": { "any": [ { "urlMatches": "/contacts/\\d+" }, { "selector": ".toast-success" } ] } }
  ],
  "provenance": { "recording": "rec-2026-09-25-1412", "generator": "record-analyze/1" }
}
```

- `sideEffects` / `sideEffect: true` drive verification replay (§8.5), the
  UI badges, and a recommended `core.human_in_loop` hint for `message`.
- `outputs.schema` is used for type-checking in the workflow editor.
- `ui` hints on inputs let forms be **auto-generated from inputs**. The
  `forms/` override is for special cases only. This removes the hardcoded
  4-platform fallback in `schema_loader.go`.

### 4.4 `selectors.json`

```json
{
  "contact.email_input": {
    "candidates": [
      { "css": "[data-testid=contact-email]", "score": 0.95 },
      { "aria": { "role": "textbox", "name": "Email" }, "score": 0.9 },
      { "css": "form#new-contact input[name=email]", "score": 0.8 },
      { "xpath": "//label[normalize-space()='Email']/following::input[1]", "score": 0.6 }
    ],
    "intent": "Email field on the new-contact form",
    "verifiedAt": "2026-09-25T14:12:00Z"
  }
}
```

This becomes the first tier of the existing `ConfigInterface.GetConfig`
lookup (package selectors → stored validated config → AI generation). The
existing per-step `alternatives[]` still works. A new `aria` selector kind
(role + accessible name, resolved with CDP `Accessibility.queryAXTree`) is
the most stable against layout changes, and is what the recorder prefers.

### 4.5 JSON Schemas as the actual template files

Ship `data/schemas/automation.v1.schema.json`, `action.v1.schema.json`,
`fragment.v1.schema.json` and `selectors.v1.schema.json`. They:

- are embedded and used by `automation validate`
- are referenced by `"$schema"` in scaffolded files, so any editor validates
  and autocompletes them
- are generated from the Go structs (a `go generate` step plus a test that
  fails on drift), so code and schema cannot diverge

`automation new <id> --template <basic|login-form|list-scrape|post-content|search-and-extract>`
scaffolds a full package tree from `data/automation-templates/<name>/`.

## 5. Registry, install, export

### 5.1 Storage

```
~/.monoagent/automations/
├── index.json                       # installed set: id → {version, source, enabled, sha256, installedAt, trust}
├── acme-crm/1.2.0/…                 # extracted package
└── acme-crm/1.1.0/…                 # previous version kept for rollback (keep last 2)
```

**Built-ins are ordinary packages. They ship with the software but are not
special at run time.**

- Built-ins move from `data/actions/<platform>/` to
  `data/automations/<id>/{automation.json,actions/…}`, embedded in the
  binary as a **seed set**.
- **Seeding:** on first run, and on every upgrade, each embedded package is
  installed into `~/.monoagent/automations/` with `source: builtin`, unless
  `index.json` already has it:
  - **at an equal or newer version:** left alone. A user-installed update
    is never downgraded by an app release.
  - **marked `removed`:** skipped. An uninstall sticks; a new release does
    not bring it back.
  - **user-modified** (checksum differs from the seeded copy): the new
    version is installed alongside, and the user is asked whether to
    switch. Local edits are never overwritten silently.
- **Update:** from a newer `.mpkg` (`automation install`), or automatically
  when a new app release seeds a newer built-in (rule above).
- **Remove:** `automation uninstall hackernews` works exactly as for any
  other package. `automation restore <id>` reinstalls the shipped copy from
  the embedded seed.
- **Run time only reads `~/.monoagent/automations/`.** There is one
  resolution path, highest enabled installed version, with no
  embedded-vs-user precedence rules.
- The legacy `~/.monoagent/actions/<platform>/*.json` directory is wrapped
  once into a generated `local-<platform>` package, with a one-time notice.
  Nothing is deleted.

### 5.2 CLI (all functionality lives here; the GUI shells out to it)

```
monoagentcli automation list [--json] [--all]            # packages + login state + action count
monoagentcli automation show <id> [--json]               # manifest, actions, inputs/outputs, permissions
monoagentcli automation new <id> --template <t> [--dir]  # scaffold
monoagentcli automation validate <dir|file.mpkg>        # schema + lint + fixture tests
monoagentcli automation test <id> [action] [--live]      # fixture tests; --live runs against the browser
monoagentcli automation pack <dir> [-o file.mpkg]       # directory → package
monoagentcli automation install <file.mpkg|dir|url> [--yes]           # also updates an installed package
monoagentcli automation export <id> [-o file] [--actions a,b] [--with-recordings] [--with-selectors-cache]
monoagentcli automation uninstall <id> | enable <id> | disable <id> | rollback <id>
monoagentcli automation restore <id>                     # reinstall a removed built-in from the shipped seed
monoagentcli automation doctor [<id>]                    # selector health, missing native bots, login state

monoagentcli action export <id>.<action> [-o file.mpkg]  # single action, packaged with its deps
monoagentcli action import <file>                         # merges into the target automation (prompts on conflict)
monoagentcli login <automation>                           # reads manifest.login (existing command, generalized)
```

`action template install` stays as a deprecated alias of
`automation install` for one release.

### 5.3 "Fully exported"

- **Package export** writes every file needed to run on another machine:
  manifest, actions, fragments, selectors, scripts, forms, tests and icon.
  **Never** included: cookies, sessions, vault entries or credentials.
  Recordings are opt-in (`--with-recordings`) because they contain
  real page content.
- **Single-action export** builds a minimal package containing the
  manifest (same `id`, bumped build metadata), that action, and the
  transitive closure of the fragments, selectors and scripts it references.
  Importing it into a machine that already has the automation **merges**
  the action. Otherwise it installs as a new package.
- **Workflow export** (`workflow export --bundle-automations`) embeds the
  packages a workflow's nodes need, so a shared workflow works on import.
  `workflow import` offers to install the missing packages.
- Round-trip guarantee, enforced by a test: `export → install → export` is
  byte-identical except for `installedAt`.

### 5.4 Versioning & compatibility

- Semver on packages and actions. `engine` is checked at install time.
- A workflow node stores the package version it was built against. A
  **major** version bump of an installed package shows a "may be
  incompatible" warning on the nodes that use it (input and output diff shown).
- `automation rollback <id>` switches back to the kept previous version.

## 6. Portable logic and safety

### 6.1 The `call_bot_method` problem

Imported packages cannot contain Go. Options considered:

| Option | Portability | Safety | Effort |
|---|---|---|---|
| A. Keep Go bots; packages are declarative only | Low: complex sites cannot be done | High | None |
| B. Richer declarative steps only | Medium | High | Medium |
| **C. B first; `page_script` (JS inside the page) only as an escape hatch** | **High** | **Controllable** | **Medium** |
| D. WASM plugins | High | High | Large, and poor authoring ergonomics |

**Decision: C, JSON-first.** The ideal package is **pure JSON**, run by one
generic engine. A script is allowed only where the declarative vocabulary
truly cannot express the logic, and it is visible and reviewable when it
is used.

- **The declarative vocabulary is the main investment.** Every time a
  script is needed for a recurring pattern, that pattern is a candidate
  for a new generic step. The steps:
  - `call_fragment` and `call_action`, to compose actions
  - `for_each`, for inline loops (easier than id-based `loops[]`)
  - `wait_for`, on URL, selector, network-idle or text conditions
  - `select_option`, `press_key` and `assert`
  - `extract_table` and `extract_json` (from a script tag or a network
    response)
  - `download` and `switch_tab`
  - `http_fetch_in_page`, a fetch using the page's own session, so it
    respects the domain allowlist
  - declarative data transforms: `transform`, with ops `map`, `filter`,
    `dedupe`, `regex_extract`, `parse_date`, `parse_number`, `join` and
    `split`
- **`page_script` (escape hatch):** runs a JS function **inside the page**
  through the existing extension `eval` command. It gets `args` and returns
  JSON, and it can only touch the page it is already allowed to be on.
  Scripts live as files in `scripts/`, never inline, so they are easy to
  review and diff.
- **No host-side script VM.** Data reshaping is done with `transform`
  steps instead. That removes a whole sandbox from the attack surface.
- **Scripts are always visible:**
  - `automation validate` reports every script, with a lint hint when a
    known declarative step could replace it.
  - The package card shows a "contains scripts" badge.
  - The record analyzer is instructed to emit a script only after the
    declarative form fails lint.
- **Native bots stay** for built-in social platforms (`requires.native`).
  They are ported to declarative steps one method at a time, when
  convenient. It is not a prerequisite.

### 6.2 Enforcement points (in `internal/action`, not in prompts)

1. **Validation fails on unknown step types.** This fixes today's silent
   skip. The executor refuses to run an action that did not pass validation.
2. **Domain allowlist:** `navigate`, `http_fetch_in_page` and new-tab steps
   check the target host against `site.domains`. Redirects out of the
   allowlist abort the step.
3. **Permissions allowlist:** a step type not listed in
   `manifest.permissions.steps` is rejected at validate time.
4. **No cookie or storage reads** from packages. Scripts can't reach
   `chrome.*`, because `page_script` runs in the page world, not the
   extension world.
5. **Secrets:** inputs with `"type": "secret"` resolve only from the vault
   and are masked in logs, run history and recordings.
6. **Rate limiting:** existing `maxItems` / `maxItemsPerDay` apply to every
   package. `sideEffects: message` actions get a default per-day cap unless
   the manifest overrides it, which the install review shows.

### 6.3 Trust on install

- The install review screen (CLI prompt or GUI dialog) shows: publisher,
  domains, permissions, whether scripts are present, side-effect levels of
  each action, and the file list with sizes.
- `index.json` records trust as `builtin | local | imported`. Before the
  first live run of an `imported` action with `sideEffects ≥ write`, the
  user must confirm once.
- Updates show a diff of permissions and scripts before applying.
- Optional signing (minisign/ed25519 over `CHECKSUMS`) is planned for when
  a shared catalog exists. It is not needed for file-based sharing.

### 6.4 Usage policy

`docs/USAGE_POLICY.md` gates social automation behind `-tags social`.
Importable packages must not become a way around that:

- A package with `policy.tier: "social"`, or whose `site.domains` match the
  social list (instagram, linkedin, x/twitter, tiktok, …), installs as
  disabled in a default build, with the same explanation the build tag
  gives today.
- Ship this check in the same phase as `install` (Phase 1). It must not
  come later.

## 7. Connections page split

### 7.1 Layout

```
Connections                                          [Import automation] [Record new]
─────────────────────────────────────────────────────────────────────────────
BROWSER AUTOMATIONS  (actions & crawl — run in your browser)       4 / 9 logged in
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ Hacker News  │ │ Gemini       │ │ Acme CRM     │ │ + Create     │
│ ● logged in  │ │ ○ logged out │ │ ● logged in  │ │   automation │
│ 4 actions    │ │ 4 actions    │ │ 2 actions    │ │              │
│ built-in 1.1 │ │ built-in 1.0 │ │ imported 1.2 │ │              │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘

API CONNECTIONS                                                    6 / 40 connected
  Services · Communication · Databases · Infrastructure · Custom   (existing grouping)
```

- **Section membership is data-driven.** Browser automations come from
  `automation list --json`. API connections come from the connection
  registry, filtered to platforms that have a non-`browser` method. The
  hardcoded `SOCIAL_IDS` set is deleted.
- A site that has both (Product Hunt: an API key and a browser package)
  appears **in both sections**, each card showing its own auth state. They
  are different things with different nodes.

### 7.2 Automation detail drawer (click a card)

Tabs:

- **Overview:** manifest summary, domains, permissions, trust badge,
  version, source (built-in / imported / local), "modified" and
  "contains scripts" badges.
- **Session:** login state, username, expiry, Log in, Log out, Test.
  This reuses `login` / `login confirm` / `logout`, now reading
  `manifest.login`.
- **Actions:** a list with side-effect badges, inputs and outputs, and
  per-action buttons: **Run test** (fixture or live), **Use in workflow**,
  **Export action**, **Re-record**, **Edit JSON**.
- **Health:** selector health from `automation doctor` (last success per
  selector, failing selectors, suggested fix: *re-record this step*).
- **Recordings:** provenance recordings linked to this package.
- Footer: Export package, Update, Rollback, Disable, Uninstall.

### 7.3 Bindings

In line with the project rule (UI renders, CLI does the work), the new
functionality is added as `wails-app/app_automations.go` bindings that run
`monoagentcli automation … --json` and `monoagentcli record … --json`. The
existing in-process bindings in `app_connections.go` (`ListConnections`,
`TestConnection`, …) are migrated to CLI calls in a follow-up. That is out
of scope for this feature, apart from `ListPlatformsJSON`, whose browser
entries are replaced by `automation list`.

## 8. Record → Analyze → Replicate

### 8.1 End-to-end flow

```
 side panel [● Record]  ─ goal: "create a contact from an email" ─┐
        │                                                        │
 recorder.js (content script, all frames)                        │
   click / input / change / submit / key / nav / "mark data"     │
        │ event + element fingerprint + DOM snippet (+ thumbnail) │
 background.js ── WS frame {kind:"recording", op:"event"} ──►  Go: internal/recording
                                                                 │ envelope in inbox
                                                                 │ events.jsonl, dom/, shots/, meta.json
                                                                 ▼
                                  recordanalyze: normalize → segment → LLM draft → lint
                                                                 ▼
                                  replay verify (extension, safe mode) → self-heal selectors
                                                                 ▼
                                  Review UI (side panel + desktop): steps, inputs, outputs
                                                                 ▼
                     save as: new action │ add to existing automation │ fragment │ workflow draft
```

### 8.2 Recorder (extension)

**New files:** `chrome-extension/recorder.js` (content script, injected
into all frames on demand via `chrome.scripting` only while recording),
`recorder_selectors.js` (fingerprinting) and `sidepanel_record.js` (UI).
Each has `.test.mjs` coverage, like the existing modules.

**Captured events:**

| Event | Recorded as | Notes |
|---|---|---|
| click / dblclick / contextmenu | `click` | Target is the nearest actionable ancestor (button, a, [role=button], input) |
| input / change | `type` | Debounced. Only the **final** value is kept. Typed characters are not |
| select change | `select_option` | Value and label |
| checkbox / radio | `click` with `checked` state | |
| submit | `submit` | Usually folded into the preceding click |
| Enter / Escape / Tab / shortcuts | `press_key` | Printable keys are dropped (covered by `type`) |
| navigation | `navigate` / `navigated` | `chrome.webNavigation`: user-typed URL vs caused by a click |
| file chooser | `upload` | File **name only**, parameterized as an input |
| scroll | `scroll` | Coarse. Kept only when followed by lazily loaded content |
| **mark as data** (Alt+click, or side panel "Pick data") | `extract` | The user shows *what to extract*. Clicking a second, similar element proposes a **list** (common-ancestor pattern). Field naming happens in the side panel |
| **mark as input** (side panel toggle on a typed field) | `param` | "This value changes each run" |

**Per-event element fingerprint** (drives selector robustness):
`tag`, `id`, `name`, `data-testid` / `data-*`, ARIA role and accessible name,
label text, placeholder, visible text (trimmed), `href`, a CSS path, an
XPath, the bounding box, a 1–2 KB DOM snippet around the element, and
frame path. The recorder ranks selector candidates at capture time and
**checks uniqueness in the live DOM**: a candidate matching more than one
element is scored down.

**Privacy (on by default):**

- Password fields, `autocomplete=cc-*`, and inputs of type `hidden` are never
  captured. Their value becomes `{{secret:<field>}}`.
- Values matching email, phone or card patterns are flagged in review as
  suggested inputs, so they are not baked into an action.
- Screenshots are off by default. When turned on, they are taken only at
  step boundaries, and password fields are blurred through a CSS mask
  injected before capture.
- A visible red recording badge on the tab and in the side panel. Recording
  stops on tab close or when the side panel is closed.
- Network capture is optional and off by default. When on, it records
  method, URL, status and content-type from CDP `Network.*` (already
  reachable through `cdp_proxy.js`), without bodies. It is used only to
  hint "this page loads data from a JSON API" to the analyzer.

**Transport:** a new `kind:"recording"` frame on the existing WS
(`internal/extension/protocol.go`), with ops `start | event | snapshot | stop`.
Snapshots reuse the chunked capture framing (`internal/capture/chunks.go`).
If the bridge is offline, events are buffered through the existing
`capture_queue.js` pattern.

### 8.3 Storage

A recording is a normal capture envelope:
`meta.json` (`source: "recording"`, `Extra.goal`, `Extra.automationHint`),
`events.jsonl`, `dom/<eventId>.html` snippets, `shots/<eventId>.png`
(optional) and `network.jsonl` (optional). Add these names to
`ValidArtifactName`. This gives inbox listing, export (`captureexport`),
Documents sync and summary with no new infrastructure. The GLU-04 "capture
triggers workflow" work can later hook onto recordings for free.

CLI:

```
monoagentcli record list [--json]
monoagentcli record show <rec> [--json]
monoagentcli record analyze <rec> [--automation <id>] [--model …] [--json]   # → draft
monoagentcli record verify <draft> [--safe|--full]                          # replay
monoagentcli record save <draft> --as action|fragment|workflow [--automation <id>|--new <id>]
monoagentcli record delete <rec>
```

### 8.4 AI analysis (`internal/recordanalyze`)

The pipeline is deliberately **deterministic first, then AI**. The LLM
works on a clean, small input rather than raw events.

1. **Normalize (Go, no AI):** merge keystrokes into final values, fold
   click+submit, drop focus noise and duplicate navigations, attach each
   event's best-ranked selector candidates, and split into **segments** by
   navigation or URL-pattern change.
2. **Detect structure (Go):**
   - **Repetition:** the same action sequence on sibling elements becomes a
     `for_each` over an extracted list.
   - **Literals to parameters:** typed values, picked options and URL path
     segments that look like IDs become candidate inputs. Values the user
     marked as `param` are always inputs.
   - **Data:** `extract` marks, with inferred list containers and field
     selectors.
   - **Outcome waits:** for each side-effecting click, the next observed
     change (URL change, new element or toast) becomes its `waitFor`.
3. **LLM draft:** runs through the configured runner (`monomind agent exec`,
   the same mechanism as `capturesummary`). **The monomind runner is the
   only AI backend for this feature.** Nothing here uses the in-app AI
   provider settings, which are slated for removal.
   - **Input:** the normalized segments, fingerprints, DOM snippets, the
     user's goal text, the target automation's existing
     `selectors.json`/fragments (so it reuses rather than duplicates), and
     the JSON Schema of an action.
   - **Output, strictly schema-validated:** action name and description;
     inputs with types and `ui` hints; outputs and output schema; steps
     with `configKey` references into a proposed `selectors.json` delta;
     an `intent` on every element step (enables the Jev fallback);
     `sideEffects` classification; and suggested fragments (e.g. "steps
     1–3 match `dismiss_banner` already in this package").
   - Invalid JSON gets one automatic repair round, fed with the validator
     errors, then fails with the validator output shown.
4. **Lint (Go):** validate against the schema and the manifest permissions.
   Every `configKey` must resolve against the **recorded DOM snippets**
   to exactly one element. There must be no literal secrets, and no
   navigation outside the domains.

The prompt lives in `data/skills/record-to-action.md`, next to the
existing `action-template-generator.md` skill, so it can also be run
by hand in Claude Code.

### 8.5 Verify by replay

`record verify` runs the draft through the normal `ActionExecutor` over the
extension bridge, with the recorded values as inputs.

- **`--safe` (default):** the executor stops **before** the first step with
  `sideEffect: true`. It highlights the target element in the page and
  reports "would click *Save*". Everything up to that point (navigation,
  form filling, extraction) is exercised for real.
- **`--full`:** runs everything. The user must confirm in the review UI
  whenever `sideEffects ≥ write`.
- **Self-heal:** when the primary selector fails but a later candidate or
  the Jev `intent` fallback finds the element, the working selector is
  promoted in the draft's `selectors.json` delta and the step is marked
  *healed*.
- The result is a per-step pass / healed / fail report, with a screenshot
  on failure.

### 8.6 Review UI

This lives in both the extension side panel (right after stopping) and
the desktop Connections drawer → Recordings.

- A step list: human-readable description, thumbnail, selector (with
  candidates) and verify status. Steps can be reordered, deleted, merged,
  or re-captured (just this step: "show me the element again").
- An inputs panel: rename, change type and default, toggle
  *fixed value ↔ input*.
- An outputs panel: field names, a preview of extracted data from the
  recording.
- **Naming is done by the AI.** The analyzer proposes the action ID,
  display name and description, input and output field names, fragment
  names, and, for a new automation, the package ID and name (from the
  site and the goal). They are prefilled and editable, with collisions
  auto-suffixed.
- **Save as:**
  - **New action** in an existing automation, or in a **new automation**.
    The manifest is generated from the recorded domains and the login is
    detected from the recording if the user logged in during it.
  - **Fragment**, for "part of a node": a reusable sequence that other
    actions include with `call_fragment`. It can also be inserted at a
    chosen position in an existing action ("prepend to `create_contact`").
  - **Workflow draft**, for recordings that span several sites or have
    separate phases: one action per segment plus a workflow wiring them,
    opened in the editor. This is GLU-02.
- Saving writes into `~/.monoagent/automations/<id>/<version>` through
  `automation` CLI calls, bumps the patch version, registers the node
  immediately (loader cache invalidation, as `action template install`
  does today), and keeps the recording as provenance.

### 8.7 Keeping recorded actions alive

- Every run records per-selector success, failure and healing into
  `automation_selector_health` (migration). `automation doctor` and the
  Health tab surface decaying selectors.
- When a run heals a selector through a fallback, the healed candidate is
  promoted automatically **only for local packages**. For imported or
  built-in packages it is written to a local overlay
  (`~/.monoagent/automations/<id>/overlay/selectors.json`), so updates don't
  clobber it and the overlay can be exported as a patch.
- **Re-record step** from the Health tab or a failed run: the extension
  opens the page at that step, and the user clicks the element once. Only
  that selector entry is replaced.

## 9. Code map (new and changed)

| Path | Change |
|---|---|
| `internal/automation/` (new) | `manifest.go`, `package.go` (fs.FS loader: dir, zip, embed), `registry.go` (index, seeding from embedded built-ins, removed/modified tracking, rollback, restore), `install.go`, `export.go` (closure computation), `validate.go`, `permissions.go`, `policy.go` (social gate) |
| `internal/action/` | Validation-before-run. New steps (`call_fragment`, `call_action`, `for_each`, `wait_for`, `select_option`, `press_key`, `assert`, `extract_table`, `transform`, `page_script`, …). Domain enforcement. Selector tier from package. `aria` selector kind. Selector health hooks. Unknown step type becomes an error |
| `internal/browser/provider.go` | Start URL from the manifest. Honour `username` (the right session per account) |
| `internal/nodes/browser_register.go` | Register from the automation registry. Carry package version into node metadata |
| `internal/workflow/schema_loader.go` | Auto-generate forms from action inputs and `ui` hints. Remove the hardcoded platform list |
| `internal/recording/` (new) | WS handler for `kind:"recording"`, envelope writer |
| `internal/recordanalyze/` (new) | normalize, segment, detect, llm, lint, draft |
| `internal/extension/protocol.go` | recording frames |
| `internal/connections/manager.go` | Fix the `connect` → `login` hint |
| `cmd/monoagentcli/automation*.go`, `record*.go`, `action_export.go` (new) | CLI |
| `cmd/monoagentcli/login.go` | Read `manifest.login`. Accept any installed automation |
| `data/automations/` | Built-ins restructured from `data/actions/` |
| `data/schemas/*.schema.json`, `data/automation-templates/` | Template files |
| `data/skills/record-to-action.md` | Analyzer prompt |
| `data/migrations/0xx_automation_selector_health.sql` | Health table |
| `chrome-extension/recorder*.js`, `sidepanel_record.js` | Recorder and review UI |
| `wails-app/app_automations.go`, `frontend/src/pages/Connections.jsx` (split into `connections/BrowserAutomations.jsx`, `connections/ApiConnections.jsx`, `connections/AutomationDrawer.jsx`) | GUI |

All files stay under 500 lines. The test files sit next to the code.

## 10. Phased delivery

Each phase ships as an integration branch followed by one release, in line
with the release-per-push rule. Each phase is useful on its own.

### Phase 0 — Hardening the base (small, ~2–3 days)
- Unknown step type becomes a validation error. The executor refuses
  unvalidated actions.
- JSON Schemas generated from the structs, plus a drift test.
- Forms auto-generated from action inputs. Remove the 4-platform hardcode.
- Fix the `connect` → `login` hint bug.
- **Done when** all 65 shipped actions validate, and a user action installed
  with `action template install` gets a working form.

### Phase 1 — Package format & registry (~1–1.5 weeks)
- `internal/automation`: manifest, fs.FS loader (dir, zip, embed), index,
  built-in seeding (skip removed, never downgrade, keep user edits), legacy
  wrap migration, social policy gate.
- Move `data/actions/*` into `data/automations/*` with manifests. Node type
  names unchanged. Golden test: the node list before equals the node list
  after.
- CLI: `automation list/show/new/validate/pack/install/export/uninstall/restore/enable/disable/rollback`,
  `action export/import`. `login` reads the manifest.
- **Done when:** export `hackernews`, uninstall it, install the `.mpkg`,
  and the same workflow still runs. Round-trip byte-identity test passes.
  A social package is refused in a default build. An uninstalled built-in
  stays uninstalled after a simulated upgrade, and `restore` brings it back.

### Phase 2 — Connections page split (~4–5 days)
- `app_automations.go` (CLI-backed), the two-section page, the automation
  drawer (Overview, Session, Actions, export/import/uninstall), and an
  import dialog with the permission review.
- **Done when** it is verified in the browser through `wails dev` (per the
  project's GUI-testing practice). Import → card appears → log in → run
  action test → export works end to end.

### Phase 3 — Portable logic & safety (~1.5 weeks)
- New declarative steps (incl. `transform`), `page_script` escape hatch, domain and
  permission enforcement, `secret` inputs, trust and first-run confirmation.
- **Proof point:** port `hackernews` fully off `call_bot_method`. It becomes
  the reference package, with no `requires.native`.
- **Done when** the hackernews package runs from an imported `.mpkg` in a
  build where its Go bot is removed. Negative tests: an off-domain
  navigate, an undeclared step type and a host-script network attempt are
  all blocked.

### Phase 4 — Recorder (~1.5 weeks)
- `recorder.js`, fingerprinting, privacy masking, side panel record UI,
  `kind:"recording"` frames, the `internal/recording` envelope, and the
  `record list/show/delete` CLI.
- **Done when** a recording on a fixture site (served locally, tested
  through the private test bridge on 9232, never the user's Edge on 9222)
  produces the expected `events.jsonl`, with password values absent.

### Phase 5 — Analyze, verify, save (~2 weeks)
- `internal/recordanalyze` (deterministic stages unit-tested against
  recorded fixtures, with the LLM stage behind an interface and a stubbed
  test double), `record analyze/verify/save`, the review UI in the side
  panel and the desktop, save as action, fragment or workflow draft.
- **Done when** three fixture scenarios pass end to end: *form submit*
  (parameterized inputs), *list scrape* (the `for_each` + extract pattern)
  and *multi-page flow with login*. Each produces an action that passes
  `record verify --safe`, and it shows up as a node without a restart.

### Phase 6 — Durability & sharing (~1 week, and ongoing)
- Selector health table, doctor, Health tab, self-heal overlay,
  re-record step, `workflow export --bundle-automations`, and
  signing groundwork.
- Later: a shared catalog (reuse `workflow-marketplace-curation-policy.md`
  for curation rules), and gradually porting the social bots to
  declarative packages.

**Rough total:** 8–10 weeks of focused work. Phases 0–2 alone already
deliver the connections split and import/export.

## 11. Testing strategy

- **Unit:** manifest/schema validation, resolution order, export closure,
  permissions, normalization and segmentation of recorded event fixtures,
  selector ranking.
- **Golden:** the registered node list and the generated forms before and
  after the Phase 1 migration.
- **Extension:** `node --test` for `recorder*.js` (same pattern as the
  existing `*.test.mjs`), plus browser tests with `browser_harness.mjs`
  against local fixture pages.
- **E2E:** the private bridge on port 9232 with a scratch HOME under
  `~/scratch`, a fixture site served locally, and the whole pipeline from
  record through analyze (LLM stubbed), verify, save and run.
- **Security:** a table of negative tests for §6.2.

## 12. Risks

| Risk | Mitigation |
|---|---|
| Recorded selectors break when sites change | ARIA and test-id first, multiple candidates, the `intent` Jev fallback, health telemetry, one-click re-record of a single step |
| Imported package abuses the logged-in browser | Domain allowlist, a permissions allowlist enforced in code, no cookie access, trust tiers, first-run confirmation, script diff on update |
| LLM produces a plausible but wrong action | Schema validation, DOM-resolving lint, safe replay before save, the human review step, `sideEffects` gating |
| Recording captures private data | Masking on by default, screenshots and network off by default, recordings excluded from export by default |
| Policy bypass of the social gate through packages | The tier plus domain check at install (Phase 1, not later) |
| Migration breaks existing workflows | Node names unchanged, a golden test, and the legacy dir auto-wrapped, never deleted |
| Scope creep in porting social bots | Explicitly optional. `requires.native` keeps them working as-is |

## 13. Decisions (resolved 2026-09-25)

1. **Package file extension:** `.mpkg`.
2. **Scripts:** JSON-first. The goal is pure-JSON packages run by the
   generic engine. `page_script` is an escape hatch, allowed only when the
   declarative steps cannot express the logic. It is always a visible file
   in `scripts/`, flagged by validate and in the UI. There is no host-side
   script VM; data transforms are declarative (§6.1).
3. **Built-ins are packages:** shipped in the binary as a seed, installed
   into the same registry as everything else, and updatable and removable
   like any other package. Uninstall sticks across upgrades.
   `automation restore` brings a built-in back (§5.1).
4. **AI:** always the monomind runner. The in-app AI provider is not used
   by this feature and will eventually be removed from the app.
5. **Naming:** the AI names recorded actions, inputs, outputs, fragments
   and new automations, and the user can edit them before saving (§8.6).
   The Connections sections default to "Browser Automations" and
   "API Connections".
6. **Recording scope:** one tab only in v1. A navigation that opens a new
   tab ends the recorded segment with a notice. There is no `switch_tab`
   recording; the step exists for hand-written actions only.
