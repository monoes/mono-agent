# Setup & Health Check — `monoagentcli doctor` + Settings › System health

Status: implemented · 2026-09-24 · decisions recorded 2026-09-24 (see §8)

Delivered as stacked PRs #131 → #133 → #134 → #135 → #136 → #138 → #139 →
#141 → #143 → #144 → (CI) in mono-agent, and monoes/monomind#332 (`doctor
--json`, `doctor-json` capability, scan `install`/`login_hint`, update notice
on stderr). Deviations from the plan below: fix progress uses `--json` (no
`--json-stream`); the CLI command is `nodejs` (`node` runs workflow nodes);
scan exposes `login_hint` instead of an `authenticated` probe (monomind's
protocol deliberately doesn't probe auth); runtime installs, start-at-login,
MCP registration and monomind `--install` fixes are *optional* fixes that
`doctor --fix` never applies.

## Goal

One place that answers "is everything mono-agent and monomind need installed,
configured and working on this machine?" — and fixes what it can. The CLI
owns all logic (`monoagentcli doctor` / `monoagentcli setup`); the Wails
Settings page only renders the CLI's JSON and runs its fixes, following the
existing "GUI shells to monoagentcli" rule.

## What exists today (scattered, no single view)

| Area | Existing code | Gap |
|---|---|---|
| monomind discovery + handshake | `internal/monomind/find.go` (`Find`, `Handshake`, `Ensure`), `capabilities.go` | Only surfaces as an error when a feature fails; no proactive check, no install fix |
| Agent runtimes (claude, codex, opencode, antigravity, kimi…) | `monomind agent scan --json` → `ScanResult`, GUI `ScanAgentRuntimes` | No auth/login check, not shown in Settings |
| Profile folder monomind init | GUI `IsMonomindInitialized` / `InitializeMonomindProfile` (`wails-app/app_monomind_init.go`) | GUI-only, no CLI equivalent |
| Claude Code skills | `init --claude`, `runClaudeFirstRunCheck` (`cmd/monoagentcli/init.go`) | Only checks existence, not staleness |
| Daemons | `status` → `statusDaemons` (`org_process.go`), `daemon install/uninstall`, `internal/autostart` | Not framed as health, no fix action |
| Browser / extension / bridge | `chrome_helper.go` (`findLocalChromePath`, `isExtensionInstalled`, `ensureExtensionConnected`), `extension status/pair/serve` | Separate command, no unified report |
| AI providers / connections / logins | `ai provider test`, `connect test/refresh`, `login status` | One-at-a-time, no summary |
| Updates | `update`, GUI `CheckForUpdate` / `AppSelfUpdate` | GUI and CLI version skew never checked |
| CLI location from GUI | `findCLIBinary` (`wails-app/updater.go`) | Failure is silent until a page breaks |
| monomind's own diagnostics | `monomind doctor [-c X] [--fix] [--install]` (~30 checks) | **No `--json` output** (verified: `--json` is ignored, prints text) — mono-agent can't consume it |

## Design

### 1. `internal/health` package (new)

```go
type Status string // "ok" | "warn" | "fail" | "skip" | "info"

type Result struct {
    ID       string   `json:"id"`        // "monomind.handshake"
    Group    string   `json:"group"`     // "monomind"
    Title    string   `json:"title"`
    Status   Status   `json:"status"`
    Summary  string   `json:"summary"`   // one line, shown in the row
    Detail   string   `json:"detail,omitempty"`
    Required bool     `json:"required"`  // fail => app is broken, not just a feature
    Features []string `json:"features,omitempty"` // what breaks: "orgs", "chat", "crawl"
    Fix      *FixInfo `json:"fix,omitempty"`
    Source   string   `json:"source"`    // "monoagent" | "monomind"
    Millis   int64    `json:"ms"`
}

type FixInfo struct {
    ID      string `json:"id"`       // passed back to `doctor fix <id>`
    Label   string `json:"label"`    // "Install monomind"
    Safety  string `json:"safety"`   // "auto" | "confirm" | "manual"
    Command string `json:"command"`  // exact command shown before running / for manual
}

type Check interface {
    ID() string
    Group() string
    DependsOn() []string                    // skip when a dependency failed
    Run(ctx context.Context, env *Env) Result
}

type Fixer interface {
    Fix(ctx context.Context, env *Env, progress func(line string)) error
}
```

- `Env` injects everything checks touch (HOME, `LookPath`, exec runner, HTTP
  client, DB handle, profile ID, clock) so every check is unit-testable with
  fakes — no real Chrome/node/monomind in `go test ./...`.
- Runner: dependency-ordered, parallel within a level, per-check timeout
  (default 10s, network checks 20s), whole run cancellable.
- Registry split by group: `checks_core.go`, `checks_monomind.go`,
  `checks_runtimes.go`, `checks_browser.go`, `checks_services.go`,
  `checks_integrations.go`, `checks_accounts.go` (keeps files < 500 lines).
- Checks **call existing functions** (`monomind.Find`, `statusDaemons`,
  `isExtensionInstalled`, provider/connection testers). Code that lives in
  `cmd/monoagentcli` today and is needed by checks moves into `internal/`
  (e.g. chrome helpers → `internal/browser/detect`), not duplicated.

### 2. Fix safety levels

| Level | Meaning | Examples | CLI | GUI |
|---|---|---|---|---|
| `auto` | Local, idempotent, reversible | create `~/.monoagent`, reinstall Claude skills, start daemon, run DB migrations | `doctor --fix` applies | "Fix" button, no dialog |
| `confirm` | Installs software, changes system/user config, uses network | `npm install -g @monoes/monomindcli@latest`, `daemon install` (autostart), `monomind init` in profile folder, register MCP server with Claude Code, runtime CLI install, managed Node download | `--fix` asks per item (`--yes` skips) | Dialog showing the exact command |
| `manual` | Needs the user (browser login, OS package manager, sudo) | install Chrome/Edge, runtime `login`, OAuth re-consent | prints command | "Copy command" / "Open" button |

Hard rules: never `sudo`, never modify the shell rc files, never touch a
running extension bridge the user owns (port 9222 service) — fixes that would
restart it are `confirm`.

### 3. Component catalog

**Required** = the app can't work without it. Everything else lists the
`features` it gates, so a warning reads as "Orgs disabled because …".

**A. Core (monoagent)**
| ID | Checks | Fix |
|---|---|---|
| `core.cli` | CLI binary resolves; GUI↔CLI version match (GUI only) | confirm: install bundled CLI next to app / `update` |
| `core.home` | `~/.monoagent` exists, writable | auto |
| `core.db` | DB opens, migrations current, `PRAGMA quick_check` | auto: migrate; manual: restore from backup |
| `core.profile` | active profile exists, profile folder exists | auto: create folder |
| `core.vault` | vault key present, a test secret round-trips | manual (never regenerate a key silently) |
| `core.path` | login-shell PATH resolvable (`internal/shellpath`), node/npm visible to GUI-launched process | manual hint |
| `core.disk` | free space in `~/.monoagent` > 1 GB | manual |
| `core.update` | newer release available | confirm: `update` |

**B. monomind engine**
| ID | Checks | Fix |
|---|---|---|
| `monomind.node` | node ≥ 22.12 (monomind's floor), npm present — system Node **or** the managed Node in `~/.monoagent/node` | confirm: download managed Node (§7) |
| `monomind.binary` | `monomind.Find()` | confirm: `npm install -g @monoes/monomindcli@latest` using managed Node's npm when no system Node |
| `monomind.handshake` | protocol v, `MinMonomindVersion`, `RequiredCapabilities` | confirm: same npm command `@latest` |
| `monomind.capabilities` | optional caps (`org-tool-providers`, `org-endpoint-roles`, `org-federation`, `org-decision-attribution`) → `info`/`warn` listing disabled features | confirm: upgrade |
| `monomind.profile_init` | `.monomind/config.yaml` in profile folder (moves `IsMonomindInitialized` logic to CLI) | confirm: `monomind init` in profile folder (moves `InitializeMonomindProfile` to CLI; GUI streams it) |
| `monomind.doctor.<component>` | **every** `monomind doctor` component (version, node, npm, config, memory, api, git, mcp, claude, disk, native, monograph, graph-freshness, helpers, gates, gitignore, registry, platforms, crash-reporting, jev, catalog, security-audit, documents, …) run in the **active profile's folder**, imported from `monomind doctor --json` | pass-through: `monomind doctor --fix -c <component>` (auto/confirm per monomind's own safety tag); `--install` variants are `confirm` |
| `monomind.project.<name>.<component>` | same full doctor run per monomind project **belonging to the active profile** (`ListMonomindProjects`), on demand — not in the 30-min background pass | per-project fix: `monomind doctor --fix -c <component>` with cwd = that project; "Fix all safe in this project" |

**C. AI runtimes & providers**
| ID | Checks | Fix |
|---|---|---|
| `runtimes.any` | ≥ 1 runtime installed (`agent scan`) — **required for chat/orgs** | — |
| `runtimes.<id>` | installed, version, binary path; auth probe where cheap (via monomind so the runtime list stays in one place) | confirm: **one-click install** (§5a) — npm-based hints run with system or managed Node; curl/installer-based hints run the vendor script after showing it; then a login step (manual: opens the runtime's own login) |
| `providers.<id>` | each configured API connection: `ai provider test` (network, `--deep` only) — grouped as **AI connections (legacy)** | manual: edit key in AI connections (legacy) |

**D. Browser & extension**
| ID | Checks | Fix |
|---|---|---|
| `browser.installed` | Chrome/Edge/Chromium found (`findLocalChromePath`) | manual |
| `browser.extension` | extension installed in any profile (`isExtensionInstalled`) | manual: open store/unpacked page |
| `browser.bridge` | bridge reachable (`findRunningBridge`), who owns it (service vs ad-hoc) | confirm: `extension serve` / enable service |
| `browser.paired` | extension connected & paired | manual: `extension pair` opens pairing page |

**E. Background services**
| ID | Checks | Fix |
|---|---|---|
| `services.daemon` | workflow daemon heartbeat fresh (`daemonhb`) | auto: start daemon |
| `services.autostart` | autostart installed (`internal/autostart`) | confirm: `daemon install` |
| `services.org_serve` | org serve heartbeat for profile root (only if orgs exist) | auto: start |
| `services.api` | HTTP API answers `/health` | auto: restart daemon |

**F. Agent-tool integrations**
| ID | Checks | Fix |
|---|---|---|
| `integrations.claude_skills` | skills in `~/.claude/skills` present **and match embedded content hash** | auto: `init --claude` |
| `integrations.mcp_monoagent` | `monoagentcli mcp` registered in Claude Code (and other detected runtimes) | confirm: `claude mcp add …` |
| `integrations.mcp_monomind` | monomind MCP registered | confirm |

**G. Accounts (network, only with `--deep` / "Deep check" button)**
| ID | Checks | Fix |
|---|---|---|
| `accounts.connection.<id>` | `connect test`, token expiry | auto: `connect refresh`; manual: reconnect |
| `accounts.login.<platform>` | `login status` session validity (social build only) | manual: `login <platform>` |

### 4. CLI surface

```
monoagentcli doctor                     # all local checks, human table grouped, exit 0/1/2
monoagentcli doctor --json              # stable schema v1 (below)
monoagentcli doctor --group monomind    # or --check monomind.handshake (repeatable)
monoagentcli doctor --deep              # include network/account checks
monoagentcli doctor --fix [--yes]       # apply auto fixes; prompt for confirm fixes
monoagentcli doctor fix <fix-id> [--json]         # one fix; with --json, NDJSON progress for the GUI
monoagentcli setup                      # guided first run: required fixes in order, then optional
monoagentcli doctor --projects          # also run full monomind doctor on every project of the active profile
monoagentcli doctor --project <name>    # one project; combine with --fix
monoagentcli agent install <runtime-id> [--json]          # install an AI agent runtime (§5a)
monoagentcli nodejs status|install|update|remove          # managed Node (§7; `node` is the workflow node runner)
```

Exit codes via `exitcodes.go`: 0 all ok/warn, 1 a required check failed
(2 stays "not found", e.g. an unknown fix id).

`--json` schema (versioned, contract-tested):

```json
{"v":1,"generated_at":"…","monoagent_version":"…","deep":false,
 "summary":{"ok":21,"warn":3,"fail":1,"skip":2},
 "results":[{ "id":"monomind.binary","group":"monomind","status":"fail",
   "required":false,"features":["orgs","agent chat"],
   "summary":"monomind not found",
   "fix":{"id":"monomind.install","label":"Install monomind","safety":"confirm",
          "command":"npm install -g @monoes/monomindcli@latest"},
   "source":"monoagent","ms":12 }]}
```

`doctor fix <id> --json` emits `{"kind":"line"|"done"|"error",…}` —
the same shape the GUI already consumes for `monomind:initProgress`, so the
init streaming UI can be reused.

`monoagentcli setup` = `doctor` → walk failed required checks in dependency
order (node → monomind → profile init → runtime → daemon → extension),
offering each fix, re-running the check after each fix, and ending with the
full report. Non-interactive with `--yes` for scripted installs.

### 5. GUI — Settings › System health

- New `HealthSection` at the top of `Settings.jsx` (own file
  `components/settings/HealthSection.jsx` to keep Settings small).
- Wails bindings in a new `wails-app/app_health.go` — thin wrappers only:
  `RunHealthCheck(deep bool) string` → `monoagentcli doctor --json [--deep]`;
  `RunHealthFix(fixID string)` → spawns `doctor fix <id> --json`,
  relays lines as `health:fixProgress` events. One native check stays in the
  GUI: if `findCLIBinary` fails, the section shows "CLI missing" with the
  install action, since it can't ask the CLI.
- Layout: overall banner (Healthy / N issues / Broken), groups as collapsible
  cards, each row = status dot · title · summary · Fix / Copy command /
  Details. "Fix all safe issues" runs every `auto` fix; `confirm` fixes open a
  dialog showing `fix.command` first. "Run deep check" button for group G.
- Status-bar dot (`StatusBar.jsx`) driven by a cached last result; background
  re-check on app start and every 30 min (local checks only), and after any
  fix.
- First-run: if a required check fails on launch, open Settings › System
  health with a "Finish setup" stepper that mirrors `monoagentcli setup`.
- Replace ad-hoc "monomind not found" / "initialize profile" prompts in
  Orgs/Agents pages with a link to the relevant health row.
- A **monomind** card shows the full `monomind doctor` result for the active
  profile's folder, plus a **Projects** sub-list (one row per project of the
  active profile) with "Check", "Fix safe issues" and per-component Fix.
- A **Node** row shows system vs managed Node with Install / Update / Remove.
- All strings through i18n (`settings.health.*`).

### 5a. AI agent install (Agents page) and "AI connections (legacy)"

- **Agents page** (`pages/Agents.jsx`, nav key `ai`) becomes the home for
  agent runtimes: each not-installed card gets an **Install** button (replaces
  the plain `install_hint` text), installed ones get **Update** and a
  login/auth state badge. Button → `monoagentcli agent install <id>
  --json` → confirm dialog showing the command → streamed log →
  re-scan.
- The install recipe per runtime comes from monomind (`agent scan`
  `install_hint`, extended with a structured `install` object:
  `{kind:"npm"|"script"|"manual", package|url, login_command}`), so the
  runtime list keeps a single owner. mono-agent only executes it.
- Health rows `runtimes.<id>` link to the Agents page for install; the fix
  button there calls the same command.
- **AI Providers → "AI connections (legacy)"**: rename the nav entry, page
  title and Settings quick-access card (`settings.aiProvidersTitle` and the
  page header, all locales), add a short banner "Agent runtimes on the Agents
  page are the recommended way to use AI; API connections remain for existing
  workflow nodes." No behavior or data change; `ai provider …` CLI unchanged.

### 6. monomind side (cross-repo, `@monoes/monomindcli`)

1. `monomind doctor --json` (+ `-c`, `--fix`) emitting the same `Result`
   shape (group = component, `fix.command`), advertised as capability
   `doctor-json` in `--version --json`.
   Each component carries its fix safety (`auto`/`confirm`/`manual`) and
   `doctor --fix -c <component> --json` reports what it changed. Must work
   with cwd = any project folder (per-project checks).
2. `monomind agent scan --json` gains an optional `authenticated` field and a
   structured `install` recipe per runtime (§5a) so runtime install and login
   state have one owner.
3. Until (1) ships, mono-agent skips `monomind.doctor.*` with an `info` row
   "update monomind for detailed diagnostics".

### 7. Managed Node

Node is downloaded and managed by mono-agent, not left to the user:

- Location `~/.monoagent/node/<version>/`, symlink/pointer `current`.
  Official `nodejs.org/dist` tarball/zip for os/arch, SHA-256 verified
  against `SHASUMS256.txt` (same pattern as `update`'s
  `verifyReleaseDigest`). Pinned to an LTS ≥ 22.12 by default.
- Used only by mono-agent-launched processes: `shellpath.Prepend` the managed
  `bin` when no suitable system Node is on the login PATH. Never edits shell rc
  files; a suitable system Node always wins.
- npm global prefix for managed installs: `~/.monoagent/npm-global`, added to
  `monomind.CandidatePaths` so monomind and npm-based runtimes installed
  through it are found.
- `monoagentcli nodejs status|install|update|remove`; health row
  `monomind.node` offers it as a `confirm` fix; Settings shows it.
- The `monomind-bundle` sidecar (`docs/plans/local-agent-monomind-delegation.md`)
  stays a possible later optimisation; managed Node removes the blocker now.

## Delivery — PRs in order

| # | PR | Contents | Done when |
|---|---|---|---|
| 1 | `feat(health): check framework + core checks + doctor CLI` | `internal/health` (types, runner, Env, registry), group A, `doctor` text + `--json`, exit codes | unit tests with fake Env; `doctor --json` golden-file schema test |
| 2 | `feat(node): managed Node runtime` | `internal/nodemgr` download/verify/activate, `nodejs` command, PATH + npm prefix wiring, `monomind.node` check | tests with httptest dist server; real install into scratch HOME |
| 3 | `feat(health): monomind + runtime checks and fixes` | group B native checks + C; move profile-init from `wails-app/app_monomind_init.go` to `internal/monomind` + `doctor fix monomind.profile_init` | fake exec runner tests; manual run |
| 4 | `feat(agent): install agent runtimes` | `agent install <id> --json` from scan recipe (npm via system/managed Node, scripts) | tests with fake runner; install a runtime in scratch HOME |
| 5 | `feat(health): browser, services, integration checks` | groups D, E, F; chrome helpers → `internal/browser/detect`; skill content-hash | tests; manual run on private bridge 9232 + scratch HOME (never the 9222 bridge) |
| 6 | `feat(health): deep account checks` | group G behind `--deep` | httptest servers |
| 7 | `feat(cli): setup guided command` | `setup`, `doctor --fix`, `doctor fix <id> --json` | scripted run in `~/scratch` HOME from empty to healthy (incl. managed Node) |
| 8 | monomind repo: `doctor --json` for all components, per-component `--fix --json`, `doctor-json` cap, scan `authenticated` + `install` recipe | cross-repo | contract fixtures committed to mono-agent |
| 9 | `feat(health): full monomind doctor import + per-project` | `monomind.doctor.*` for active profile folder, `--projects` / `--project`, pass-through fixes | fixture tests; gated on `doctor-json` |
| 10 | `feat(gui): Settings › System health` | `app_health.go`, `HealthSection.jsx` (monomind card + projects list, Node row), status-bar dot, 30-min background local re-check, fix dialog, first-run stepper, i18n | render tests + driven via `wails dev` in browser |
| 11 | `feat(gui): Agents install + AI connections (legacy)` | Install/Update/login on Agents cards, rename AI Providers page/nav/card to "AI connections (legacy)" + banner, all locales | render tests + browser run |
| 12 | `ci: doctor smoke` | clean-HOME `doctor --json` schema + expected fails | green CI |

PRs 1–7 are CLI-only and shippable in order; 9 needs 8; 10 needs 1, 7 (and 9 for the monomind card); 11 needs 4 (and 8 for structured recipes — falls back to npm-only hints before that).

## 8. Decisions (2026-09-24)

1. Agent runtimes are **installable** — from the Agents page (§5a); the old
   AI Providers page becomes **"AI connections (legacy)"**.
2. Node is **downloaded and managed** by mono-agent (§7).
3. GUI background re-check every **30 minutes** (local checks only).
4. Scope is the **active profile** — its folder and its monomind projects.
5. **All of monomind's health** is included, based on `monomind doctor`, with
   fixes both for the active profile folder and **per project** (§3 B, §5).
