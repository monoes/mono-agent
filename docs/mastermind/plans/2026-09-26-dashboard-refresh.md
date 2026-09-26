# Dashboard Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the desktop Dashboard from a "workflows + recent runs + sessions" page (unchanged in substance since April 2026) into the at-a-glance home for everything the system now does: orgs, human-in-the-loop, automation packages and selector health, recordings, captures/documents, people review, applications, Jev, daemon/bridge/org-serve health, schedules — with every fact coming from `monoagentcli … --json`.

**Architecture:** Add one cheap, read-only, network-free CLI roll-up (`monoagentcli summary`) backed by a new `internal/summary` package, plus a separate `monoagentcli org summary [--fast]` for the org roll-up (which needs monomind subprocesses and is slow). The Wails app gains two thin bindings that return that JSON verbatim; `GetDashboardStats` is re-implemented on top of `summary` so the Sidebar and StatusBar keep working unchanged. The frontend Dashboard is split into a `pages/dashboard/` folder of focused cards fed by one `useDashboardData` hook.

**Tech Stack:** Go 1.x + cobra (CLI), SQLite via `modernc.org/sqlite`, `github.com/robfig/cron/v3` (next-run times), Wails v2 bindings, React + Vite, Vitest + Testing Library (jsdom), react-i18next (en, es).

## Global Constraints

- **CLI first:** every fact the dashboard shows comes from `monoagentcli … --json`. The Wails layer only shells out and returns stdout (see memory *ui-calls-cli-for-functionality*; pattern: `wails-app/app_org_unification.go:504` `cliResultJSON`, `wails-app/app_applications.go:23` `runMonoCLI`). No new SQL in `wails-app/`.
- **`summary` must never spend money or leave the machine:** no Jev calls, no `--suggest`, no monomind subprocess, no GitHub/API calls. The only I/O allowed beyond SQLite and local files is a loopback HTTP probe of the extension bridge with a ≤ 500 ms timeout.
- **`summary` must be fast:** p95 ≤ 250 ms wall time on the author's real profile (`time monoagentcli --json summary`), because the dashboard polls it every 15 s.
- **One section failing never fails the command:** each section carries its own `"error"` string; exit code stays 0 unless the DB cannot be opened at all.
- **JSON envelope:** `{"v":1, "generated_at": RFC3339, "profile_id": "...", <sections>}`; snake_case keys everywhere in new output.
- **Files under 500 lines** (CONTRIBUTING.md:41). `Dashboard.jsx` is already 493 — it must be split, not grown.
- **i18n:** every new user-visible string gets a key in both `wails-app/frontend/src/locales/en.json` and `es.json`.
- **Commits:** conventional style `type(scope): description`; never add `Co-Authored-By`, `Claude-Session`, or "Generated with" lines; author stays `nokhodian <nokhodian@gmail.com>`.
- **Git hygiene:** do the work in a worktree under `~/scratch/` on branch `feat/dashboard-refresh`; one integration PR = one release (memory *release-per-push-batch-via-integration*). Commit with `git commit -F - -- <exact paths>`; never amend/rebase/reset a shared branch.
- **Verification:** GUI changes are verified in a browser against the Vite/`wails dev` server, and behavior is verified via the CLI (memory *test-gui-in-browser-and-via-cli*). `wails dev` on this machine needs `-tags webkit2_41`. Restore regenerated `wailsjs/` and `package.json.md5` before committing.
- **Scratch space:** test HOMEs and builds live under `~/scratch/`, not `/tmp` (memory *scratch-on-main-disk*).
- **Do not touch the extension bridge on 9222** (the user's Edge uses it). Browser tests use a private bridge on 9232 with a scratch HOME (memory *extension-test-bridge*).

---

## 0. Where things stand (audit, verified at `d38a778`)

### 0.1 The current dashboard

| Piece | File | What it does | Problem |
|---|---|---|---|
| Stat cards | `wails-app/frontend/src/pages/Dashboard.jsx:369-374` | Workflows, Active, Recent Runs (= length of a 30-row fetch), People Found | "Recent Runs" is a page size, not a metric |
| Workflows list | `Dashboard.jsx:68-204` | toggle / run / stop / open | No schedule, no next run, no org ownership |
| Recent runs | `Dashboard.jsx:207-252` | last 15 executions | `ExecStatusDot` knows `COMPLETED/RUNNING/FAILED/PENDING`; the engine writes `SUCCESS`, `SUCCESS_WITH_ERRORS`, `QUEUED`, `WAITING`, `CANCELLED` → successes render grey. `animation: 'pulse …'` references a keyframe that does not exist (`index.css` only has `pulse-dot`, `spin`) |
| Connected accounts | `Dashboard.jsx:425-472` | `stats.sessions` | Pre-dates automation packages' login blocks; no "expiring soon" |
| Database path | `Dashboard.jsx:476-483` | raw path | Low value |
| Data | `wails-app/app.go:438-512` `GetDashboardStats`; `app_workflows.go:191,710` | **direct SQL / store reads in the GUI** | Violates the CLI-first rule; `RecentExecutions` in the DTO is unused |
| Refresh | `App.jsx:195-240` | stats on mount + on `workflow:complete`; page polls its own lists 2 s/5 s | `workflow:complete` only fires for GUI-launched runs, not daemon cron runs |
| i18n | `locales/en.json:39-56` | 11 keys | ~12 hard-coded English strings remain ("Recent Runs", "Connected Accounts", "Manage", "No runs yet", "Run", "Stop", "never run", relative times…) |
| Layout | `index.css:537` `.stat-grid` fixed 4 cols; `:1500` `.dashboard-grid` `1fr 300px` | | No responsive breakpoints |

### 0.2 What the system gained since the dashboard was designed (none of it on the dashboard)

| Subsystem (feat commits) | Where it lives today in the GUI | Cheap CLI summary today? |
|---|---|---|
| **Orgs** — run/serve, autonomy levels, Jev decider, needs-you, queued messages, grants, automation roles, holding orgs (`org …`) | `orgs` page (`OrgsPanel`, `AutonomyBar`, `NeedsYouPanel`, `QueuedMessagesPanel`, `DecisionsFeed`) | No — per-org only; `needs-you` spawns monomind 4× per org (`internal/orgdecide/service.go:187-230`) |
| **HIL** — workflow HIL + people approvals + org questions/approvals/gates, Jev suggestions/auto-decide | global drawer (`App.jsx:311-317`), not a page id | No — `hil list` returns full payloads; the Sidebar polls `hil list --suggest` every 5 s, **which can trigger paid Jev calls** (`hil.go:163-215`) |
| **Automation packages** — install/trust tiers, selector health (ok/decaying/broken/stale), re-record, doctor | `connections` page → `BrowserAutomations`, `AutomationDrawer` tabs | No totals — `automation list` / `automation doctor` rows only |
| **Recordings** — record → analyze → verify → save | `AutomationDrawer` › Recordings | `record list --json` rows (`complete`, `automation`) |
| **Captures & documents** — extension captures, AI summaries, documents watch | `documents` page | No — `capture list` has no `--since`; `profile documents list` **writes** (runs `capturedocs.Sync`) |
| **People review / links / messages** — pending leads, Jev link suggestions, drafts, classification | `people`, `communications`, HIL drawer | No counts |
| **Applications** — discover/evaluate/apply, fit scores | `applications` page | No grouping |
| **Jev (TypeSafe)** — key, per-surface opt-in, usage | Settings › Jev | `jev usage --since 24h --json` (cheap) |
| **Health / doctor / setup** — check framework, fixes, runtimes, managed Node | Settings › System health, StatusBar dot (`lib/health.js`, already runs `doctor --skip-group runtimes` in background every 30 min) | Yes — reuse `lib/health.js` state, don't re-run doctor |
| **Daemon + extension bridge + org serve** | only inside doctor results / `GetDaemonStatus` | `status --json` has daemon + org_serve; heartbeat also holds `bridge_addr`, `version`, and serve heartbeat holds `running[]` orgs — not surfaced |
| **Schedules** — `trigger.schedule` cron | nowhere | No — next-run exists only in the daemon's in-memory cron |
| **Vault** — secrets, image vault | `secretsVault`, `vault` | No counts via CLI |

### 0.3 Gaps this plan closes (and the ones it deliberately does not)

Closes: G1 no roll-up command · G2 no org roll-up · G3 no next-run times · G4 no profile-wide executions listing · G5 dashboard SQL in the GUI · G6 wrong status colours / dead keyframe · G7 no deep links into Settings/Connections tabs · G8 untranslated strings · G9 non-responsive layout.

Does **not** close (out of scope, listed in §6): unread/read message state (no data model), the GUI updater calling GitHub directly (`updater.go:54`), `login status --json` untagged keys, image-vault/chat CLIs, the Sidebar's `hil list --suggest` polling cost (tracked as follow-up F1).

---

## 1. Decisions

- **D1 — One cheap roll-up, one slow roll-up.** `monoagentcli summary` covers everything readable from SQLite, local files and loopback. Orgs get `monoagentcli org summary [--fast]` because pending org items require monomind (`svc.Pending` → Approvals/Questions/Gates/Status subprocesses). The dashboard polls `summary` every 15 s, `org summary --fast` every 15 s and full `org summary` every 60 s, all only while visible.
- **D2 — Sections, selectable.** `summary --section workflows,hil` returns only those sections (unknown names → exit 2 with `errInvalidInput`). Default: all.
- **D3 — Counts, not payloads.** Sections return counts plus at most a handful of rows needed to render (recent executions ≤ 15, next schedules ≤ 5, sessions list). Anything more is a click-through to the owning page.
- **D4 — Next-run is computed, and labelled as such.** `internal/summary` parses each active workflow's enabled `trigger.schedule` node with the same spec the daemon builds (`CRON_TZ=<tz> <cron>`, 6-field with seconds, `internal/workflow/trigger_manager.go:185-196`) and reports `next_run`. The section also carries `daemon_running`; the UI shows "paused — daemon offline" when false instead of a misleading time.
- **D5 — Time filters are format-agnostic.** Timestamps in this DB exist as `YYYY-MM-DD HH:MM:SS`, RFC3339 (`…T…Z`) and Go's `time.Time.String()` form. All "since" filters compare `julianday(replace(substr(col,1,19),'T',' '))` against a Go-computed `YYYY-MM-DD HH:MM:SS` UTC bound. One helper, `sinceExpr(col)`.
- **D6 — HIL counts never touch Jev.** The `hil` section counts `hil_pending` rows (`status='pending'`) joined to the profile's workflows, plus people-review pending (`people.category='pending_approval'`), draft messages (`person_messages.status='draft'`) and suggested links (`person_links.status='suggested'`). Org items come from `org summary`.
- **D7 — Health is not re-run by the dashboard.** The System card reads `lib/health.js` (`getHealth`, `subscribeHealth`, `summarize`) — the same store the StatusBar dot uses — and offers "Run check" which calls the existing `runHealth()`.
- **D8 — Backward-compatible `GetDashboardStats`.** Same DTO (`DashboardStats`) and JSON keys so `Sidebar.jsx:193,370,394` and `StatusBar.jsx:38-41` keep working; implementation becomes `summary --section workflows,people,accounts` + mapping. `RecentExecutions` field is dropped from the DTO (unused).
- **D9 — Profile-wide executions via CLI.** `workflow executions` gains `--all` (no workflow id) returning `WorkflowExecutionSummary`-shaped rows incl. `workflow_name`; `GetRecentExecutions` shells out to it. Perf gate: if p95 of `--json workflow executions --all --limit 30` exceeds 150 ms, the dashboard's fast poll drops from 2 s to 5 s (documented in the hook).
- **D10 — Deep links.** `navigate(page, data)` already stores `navData`; extend it to `settings` (`{section:'health'|'jev'}`) and `connections` (`{automationId, tab}`), and give Dashboard an `onOpenHil` prop wired to the existing drawer.
- **D11 — Attention first.** The top of the page is an "Needs you" strip that only renders non-zero items; the stat cards follow. Empty strip → a single "All clear" line.
- **D13 — Vault is counts only.** The `vault` section returns `secrets`, `images`, `image_bytes` and nothing else — no secret names, usernames, URLs, kinds or values. A test seeds a secret with a distinctive name and asserts the full `summary --json` output does not contain it.
- **D12 — Events + polling.** Refresh summary immediately on `workflow:complete`, `workflow:exec-started`, `documents:changed`, `images:changed`, `org:runStatus`, `health` store changes; otherwise poll per D1. Daemon-cron runs are caught by the poll (they emit no GUI event).

### 1.1 Target layout

```
┌ Dashboard · v0.x ─────────────────────────────── [Refresh] [Workflow Editor] ┐
│ NEEDS YOU  ● 3 approvals  ● 2 org questions  ● 4 leads to review             │
│            ● 1 broken selector  ● 1 login expired  ● health: 2 warnings      │
├──────────────┬──────────────┬──────────────┬──────────────┬─────────────────┤
│ Workflows 12 │ Running 1    │ Failed 24h 2 │ Orgs 2/5 run │ People 1,204    │
│ 7 active     │ 3 queued     │ of 41 runs   │ 1 paused     │ +18 this week   │
├──────────────┴──────────────┴──────────┬───┴──────────────┴─────────────────┤
│ WORKFLOWS                              │ SYSTEM                             │
│ ⏻ Lead scraper  ⏱ next 14:00  SUCCESS ▶│ ● daemon  ● bridge (connected)     │
│ ⏻ Digest        ⏱ paused      FAILED  ▶│ ● org serve (2 running)            │
│ …                                      │ health 2 warn · Jev $0.03/24h      │
├────────────────────────────────────────┤ ORGS                               │
│ RECENT RUNS (all workflows)            │ acme  ● running  2 need you  1 q   │
│ ● Lead scraper   12s  3m ago           │ ops   ○ stopped  queued 3          │
│ …                                      │ AUTOMATIONS                        │
├────────────────────────────────────────┤ 9 installed · 1 broken · 2 decay   │
│ ACTIVITY (7 days)                      │ recordings: 2 unsaved              │
│ captures 14 · docs 3 summarising       │ ACCOUNTS                           │
│ messages in 22 · drafts 2              │ linkedin  @me  ● active (5d)       │
│ applications 4 pending · 2 applied     │ x         @me  ⚠ expires 20h       │
└────────────────────────────────────────┴────────────────────────────────────┘
```

Below 1100 px the right column stacks under the left; below 760 px the stat grid becomes 2 columns.

---

## 2. File structure

### New (Go, CLI side)

| File | Responsibility |
|---|---|
| `internal/summary/summary.go` | `Options`, `Summary` envelope, `Build(ctx, Options)`, section registry, `sinceExpr` |
| `internal/summary/workflows.go` | `workflows`, `executions`, `schedules` sections |
| `internal/summary/inbox.go` | `hil`, `people`, `activity`, `applications` sections |
| `internal/summary/system.go` | `services`, `automations`, `recordings`, `jev`, `accounts`, `vault` sections |
| `internal/summary/*_test.go` | one test file per source file, seeded migrated DB |
| `cmd/monoagentcli/summary.go` | cobra command, wires real stores/registries into `summary.Options` |
| `cmd/monoagentcli/summary_test.go` | CLI-level tests incl. zero-Jev-calls guarantee |
| `cmd/monoagentcli/org_summary.go` (+ `_test.go`) | `org summary [--fast]` |

### Modified (Go)

| File | Change |
|---|---|
| `cmd/monoagentcli/root.go:82` | register `newSummaryCmd(cfg)` |
| `cmd/monoagentcli/org.go:34` | register `newOrgSummaryCmd(env)` |
| `cmd/monoagentcli/workflow.go:1073-1139` | `workflow executions --all` |
| `wails-app/app_summary.go` (new) + `app_summary_test.go` | `GetSummary`, `GetOrgSummary`, new `GetDashboardStats`, CLI-backed `GetRecentExecutions` |
| `wails-app/app.go:434-512` | delete old `GetDashboardStats`/`DashboardStats` (moved to `app_summary.go`) |
| `wails-app/app_workflows.go:710` | delete SQL `GetRecentExecutions` (moved) |
| `AGENTS.md` | document `summary`, `org summary`, `workflow executions --all` |

### New (frontend)

| File | Responsibility |
|---|---|
| `src/lib/execStatus.js` (+ test) | normalise execution statuses → `{tone, label, live}` |
| `src/pages/dashboard/useDashboardData.js` (+ test) | polling/events for summary, org summary, workflows, executions |
| `src/pages/dashboard/format.js` (+ test) | `relTime`, `duration`, `untilTime` (moved from Dashboard.jsx, i18n-aware) |
| `src/pages/dashboard/WorkflowsCard.jsx` | workflow rows (moved `WorkflowRow`) + next-run chip |
| `src/pages/dashboard/RecentRunsCard.jsx` | moved `ExecRow` |
| `src/pages/dashboard/AttentionStrip.jsx` | "Needs you" chips |
| `src/pages/dashboard/StatRow.jsx` | five stat cards |
| `src/pages/dashboard/SystemCard.jsx` | daemon / bridge / org serve / health / Jev |
| `src/pages/dashboard/OrgsCard.jsx` | per-org row |
| `src/pages/dashboard/AutomationsCard.jsx` | packages, selectors, recordings |
| `src/pages/dashboard/AccountsCard.jsx` | sessions with expiring-soon |
| `src/pages/dashboard/ActivityCard.jsx` | captures, docs, messages, applications |
| `src/pages/dashboard/*.render.test.jsx` | render tests per card |
| `src/locales/dashboardKeys.test.js` | en/es parity for `dashboard.*` |

### Modified (frontend)

| File | Change |
|---|---|
| `src/pages/Dashboard.jsx` | becomes a ~120-line layout that composes the cards |
| `src/services/api.js:46,64` | add `getSummary`, `getOrgSummary`; keep `getDashboardStats`, `getRecentExecutions` names |
| `src/App.jsx:251,255,269` | pass `navData` to Settings and Connections; pass `onOpenHil` to Dashboard |
| `src/pages/Settings.jsx`, `src/pages/Connections.jsx` | honour `navData` (scroll/open section; open `AutomationDrawer` with `initialTab`) |
| `src/index.css:537,1500` | responsive grid, `.attn-chip`, `.dash-row`, pulse keyframe fix |
| `src/locales/en.json`, `es.json` | `dashboard.*` keys |

---

## Phase A — CLI contract

### Task 1: `internal/summary` core + workflows/executions/schedules sections

**Files:**
- Create: `internal/summary/summary.go`, `internal/summary/workflows.go`
- Test: `internal/summary/workflows_test.go`

**Interfaces:**
- Produces:
  - `type Options struct { DB *sql.DB; ProfileID string; Now time.Time; Sections map[string]bool; Workflows WorkflowSource; DaemonRunning func() bool; Automations AutomationSource; Recordings func() ([]recording.Summary, error); Captures func() ([]capture.Entry, error); Bridge func() (*BridgeStatus, error); OrgServe func() (running bool, orgs []string) }`
  - `type WorkflowSource interface { ListWorkflows(ctx context.Context, profileID string) ([]workflow.Workflow, error); GetWorkflow(ctx context.Context, id string) (*workflow.Workflow, error) }` (satisfied by `*workflow.HybridWorkflowStore`)
  - `func Build(ctx context.Context, o Options) Summary`
  - `var SectionNames = []string{"workflows","executions","schedules","hil","people","activity","applications","services","automations","recordings","jev","accounts","vault"}`
  - `func sinceExpr(col string) string`
  - JSON: `workflows{total,active,error?}`, `executions{running,queued,waiting,last_24h{total,success,failed,cancelled},recent[{id,workflow_id,workflow_name,status,trigger_type,started_at,finished_at,created_at,error}],error?}`, `schedules{daemon_running,upcoming[{workflow_id,workflow_name,node_id,cron,timezone,next_run}],invalid[{workflow_id,node_id,error}],error?}`

- [ ] **Step 1: Confirm stored timestamp formats** (drives D5)

Run: `sqlite3 ~/.monoagent/monoagent.db "SELECT created_at FROM workflow_executions ORDER BY rowid DESC LIMIT 3; SELECT created_at FROM person_messages LIMIT 2; SELECT created_at FROM applications LIMIT 2;"`
Expected: a mix of `2026-09-25 10:00:00`, `2026-09-25T10:00:00Z` and/or `2026-09-25 10:00:00.123 +0000 UTC`. Whatever appears, the first 19 chars are `YYYY-MM-DD?HH:MM:SS` — confirm that, and if any format differs, adjust `sinceExpr` before continuing.

- [ ] **Step 2: Write the failing test**

```go
// internal/summary/workflows_test.go
package summary

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

type fakeWorkflows struct{ wfs []workflow.Workflow }

func (f fakeWorkflows) ListWorkflows(context.Context, string) ([]workflow.Workflow, error) {
	return f.wfs, nil
}
func (f fakeWorkflows) GetWorkflow(_ context.Context, id string) (*workflow.Workflow, error) {
	for i := range f.wfs {
		if f.wfs[i].ID == id {
			return &f.wfs[i], nil
		}
	}
	return nil, nil
}

func testDB(t *testing.T) *storage.Database {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func TestWorkflowSections(t *testing.T) {
	db := testDB(t)
	for _, q := range []string{
		`INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w1','Scraper',1,'default'), ('w2','Digest',0,'default')`,
		// three formats on purpose (D5)
		`INSERT INTO workflow_executions (id, workflow_id, status, trigger_type, profile_id, created_at) VALUES
		 ('e1','w1','SUCCESS','manual','default','2026-09-26 11:00:00'),
		 ('e2','w1','FAILED','schedule','default','2026-09-26T10:00:00Z'),
		 ('e3','w1','RUNNING','manual','default','2026-09-26 11:59:00.5 +0000 UTC'),
		 ('e4','w2','SUCCESS','manual','default','2026-09-20 09:00:00'),
		 ('e5','w1','QUEUED','manual','other','2026-09-26 11:00:00')`,
	} {
		if _, err := db.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	src := fakeWorkflows{wfs: []workflow.Workflow{
		{ID: "w1", Name: "Scraper", IsActive: true, Nodes: []workflow.WorkflowNode{
			{ID: "n1", Type: "trigger.schedule", Config: map[string]interface{}{"cron": "0 0 14 * * *", "timezone": "UTC"}},
		}},
		{ID: "w2", Name: "Digest", IsActive: false, Nodes: []workflow.WorkflowNode{
			{ID: "n2", Type: "trigger.schedule", Config: map[string]interface{}{"cron": "0 0 * * * *"}},
		}},
	}}
	s := Build(context.Background(), Options{
		DB: db.DB, ProfileID: "default", Now: now, Workflows: src,
		DaemonRunning: func() bool { return true },
		Sections:      map[string]bool{"workflows": true, "executions": true, "schedules": true},
	})
	if s.Workflows == nil || s.Workflows.Total != 2 || s.Workflows.Active != 1 {
		t.Fatalf("workflows = %+v", s.Workflows)
	}
	e := s.Executions
	if e == nil || e.Running != 1 || e.Queued != 0 || e.Last24h.Total != 3 || e.Last24h.Success != 1 || e.Last24h.Failed != 1 {
		t.Fatalf("executions = %+v", e)
	}
	if len(e.Recent) != 4 || e.Recent[0].ID != "e3" || e.Recent[0].WorkflowName != "Scraper" {
		t.Fatalf("recent = %+v", e.Recent)
	}
	sc := s.Schedules
	if sc == nil || !sc.DaemonRunning || len(sc.Upcoming) != 1 {
		t.Fatalf("schedules = %+v", sc)
	}
	if got := sc.Upcoming[0].NextRun; got != "2026-09-26T14:00:00Z" {
		t.Fatalf("next_run = %s", got)
	}
	if s.HIL != nil {
		t.Fatal("unselected section must be omitted")
	}
}

func TestInvalidCronIsReportedNotFatal(t *testing.T) {
	db := testDB(t)
	src := fakeWorkflows{wfs: []workflow.Workflow{{ID: "w1", Name: "Bad", IsActive: true, Nodes: []workflow.WorkflowNode{
		{ID: "n1", Type: "trigger.schedule", Config: map[string]interface{}{"cron": "every tuesday"}},
	}}}}
	s := Build(context.Background(), Options{DB: db.DB, ProfileID: "default", Now: now, Workflows: src,
		DaemonRunning: func() bool { return false }, Sections: map[string]bool{"schedules": true}})
	if len(s.Schedules.Invalid) != 1 || s.Schedules.Invalid[0].NodeID != "n1" || s.Schedules.Error != "" {
		t.Fatalf("schedules = %+v", s.Schedules)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/summary/ -run 'TestWorkflowSections|TestInvalidCron' -v`
Expected: FAIL — `package github.com/monoes/mono-agent/internal/summary` has no Go files / `undefined: Build`.

- [ ] **Step 4: Write `summary.go`**

```go
// Package summary builds the dashboard's at-a-glance roll-up
// (`monoagentcli summary`). Every section is read-only and local: SQLite,
// files under the profile, and at most a loopback probe of the extension
// bridge. Nothing here may call Jev, monomind, or the network — the GUI
// polls this every 15 seconds.
package summary

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/recording"
)

// SectionNames lists every section in output order; `--section` accepts these.
var SectionNames = []string{"workflows", "executions", "schedules", "hil", "people", "activity",
	"applications", "services", "automations", "recordings", "jev", "accounts", "vault"}

// Options carries the stores the sections read. Nil sources make their
// section report an error instead of failing the whole summary.
type Options struct {
	DB            *sql.DB
	ProfileID     string
	Now           time.Time
	Sections      map[string]bool // nil = all
	Workflows     WorkflowSource
	DaemonRunning func() bool
	Automations   AutomationSource
	Recordings    func() ([]recording.Summary, error)
	Captures      func() ([]capture.Entry, error)
	Bridge        func() (*BridgeStatus, error)
	OrgServe      func() (running bool, orgs []string)
	Daemon        func() *DaemonStatus
}

// Summary is the `summary --json` envelope. Unselected sections are nil and omitted.
type Summary struct {
	V            int                  `json:"v"`
	GeneratedAt  string               `json:"generated_at"`
	ProfileID    string               `json:"profile_id"`
	Workflows    *WorkflowsSection    `json:"workflows,omitempty"`
	Executions   *ExecutionsSection   `json:"executions,omitempty"`
	Schedules    *SchedulesSection    `json:"schedules,omitempty"`
	HIL          *HILSection          `json:"hil,omitempty"`
	People       *PeopleSection       `json:"people,omitempty"`
	Activity     *ActivitySection     `json:"activity,omitempty"`
	Applications *ApplicationsSection `json:"applications,omitempty"`
	Services     *ServicesSection     `json:"services,omitempty"`
	Automations  *AutomationsSection  `json:"automations,omitempty"`
	Recordings   *RecordingsSection   `json:"recordings,omitempty"`
	Jev          *JevSection          `json:"jev,omitempty"`
	Accounts     *AccountsSection     `json:"accounts,omitempty"`
	Vault        *VaultSection        `json:"vault,omitempty"`
}

func (o Options) want(name string) bool { return o.Sections == nil || o.Sections[name] }

// Build runs every selected section. A section's failure lands in its own
// Error field; Build itself never fails.
func Build(ctx context.Context, o Options) Summary {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	s := Summary{V: 1, GeneratedAt: o.Now.UTC().Format(time.RFC3339), ProfileID: o.ProfileID}
	if o.want("workflows") {
		s.Workflows = workflowsSection(ctx, o)
	}
	if o.want("executions") {
		s.Executions = executionsSection(ctx, o)
	}
	if o.want("schedules") {
		s.Schedules = schedulesSection(ctx, o)
	}
	if o.want("hil") {
		s.HIL = hilSection(ctx, o)
	}
	if o.want("people") {
		s.People = peopleSection(ctx, o)
	}
	if o.want("activity") {
		s.Activity = activitySection(ctx, o)
	}
	if o.want("applications") {
		s.Applications = applicationsSection(ctx, o)
	}
	if o.want("services") {
		s.Services = servicesSection(o)
	}
	if o.want("automations") {
		s.Automations = automationsSection(ctx, o)
	}
	if o.want("recordings") {
		s.Recordings = recordingsSection(o)
	}
	if o.want("jev") {
		s.Jev = jevSection(ctx, o)
	}
	if o.want("accounts") {
		s.Accounts = accountsSection(ctx, o)
	}
	if o.want("vault") {
		s.Vault = vaultSection(ctx, o)
	}
	return s
}

// sinceExpr normalises the three timestamp shapes this DB holds
// ("2006-01-02 15:04:05", RFC3339, and time.Time.String()) to a julianday,
// so "since" filters work on all of them (plan D5).
func sinceExpr(col string) string {
	return fmt.Sprintf("julianday(replace(substr(%s,1,19),'T',' '))", col)
}

// sqlTime is the bound sinceExpr compares against.
func sqlTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
```

- [ ] **Step 5: Write `workflows.go`**

```go
package summary

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/monoes/mono-agent/internal/workflow"
)

// WorkflowSource is the slice of *workflow.HybridWorkflowStore this package reads.
type WorkflowSource interface {
	ListWorkflows(ctx context.Context, profileID string) ([]workflow.Workflow, error)
	GetWorkflow(ctx context.Context, id string) (*workflow.Workflow, error)
}

type WorkflowsSection struct {
	Total  int    `json:"total"`
	Active int    `json:"active"`
	Error  string `json:"error,omitempty"`
}

type ExecCounts struct {
	Total     int `json:"total"`
	Success   int `json:"success"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

type ExecRow struct {
	ID           string `json:"id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	Status       string `json:"status"`
	TriggerType  string `json:"trigger_type"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	CreatedAt    string `json:"created_at"`
	Error        string `json:"error"`
}

type ExecutionsSection struct {
	Running int        `json:"running"`
	Queued  int        `json:"queued"`
	Waiting int        `json:"waiting"`
	Last24h ExecCounts `json:"last_24h"`
	Recent  []ExecRow  `json:"recent"`
	Error   string     `json:"error,omitempty"`
}

type ScheduleRow struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	NodeID       string `json:"node_id"`
	Cron         string `json:"cron"`
	Timezone     string `json:"timezone"`
	NextRun      string `json:"next_run"`
}

type ScheduleIssue struct {
	WorkflowID string `json:"workflow_id"`
	NodeID     string `json:"node_id"`
	Error      string `json:"error"`
}

type SchedulesSection struct {
	DaemonRunning bool            `json:"daemon_running"`
	Upcoming      []ScheduleRow   `json:"upcoming"`
	Invalid       []ScheduleIssue `json:"invalid"`
	Error         string          `json:"error,omitempty"`
}

const recentLimit = 15
const upcomingLimit = 5

func workflowsSection(ctx context.Context, o Options) *WorkflowsSection {
	s := &WorkflowsSection{}
	if o.Workflows == nil {
		s.Error = "workflow store unavailable"
		return s
	}
	wfs, err := o.Workflows.ListWorkflows(ctx, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	s.Total = len(wfs)
	for _, w := range wfs {
		if w.IsActive {
			s.Active++
		}
	}
	return s
}

func executionsSection(ctx context.Context, o Options) *ExecutionsSection {
	s := &ExecutionsSection{Recent: []ExecRow{}}
	err := o.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(status = 'RUNNING'),0), COALESCE(SUM(status = 'QUEUED'),0), COALESCE(SUM(status = 'WAITING'),0)
		FROM workflow_executions WHERE profile_id = ?`, o.ProfileID).Scan(&s.Running, &s.Queued, &s.Waiting)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	err = o.DB.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(status IN ('SUCCESS','SUCCESS_WITH_ERRORS')),0),
		COALESCE(SUM(status = 'FAILED'),0), COALESCE(SUM(status = 'CANCELLED'),0)
		FROM workflow_executions WHERE profile_id = ? AND `+sinceExpr("created_at")+` >= julianday(?)`,
		o.ProfileID, sqlTime(o.Now.Add(-24*time.Hour))).
		Scan(&s.Last24h.Total, &s.Last24h.Success, &s.Last24h.Failed, &s.Last24h.Cancelled)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	rows, err := o.DB.QueryContext(ctx, `SELECT e.id, e.workflow_id, COALESCE(w.name,''), e.status,
		COALESCE(e.trigger_type,''), COALESCE(e.started_at,''), COALESCE(e.finished_at,''),
		COALESCE(e.created_at,''), COALESCE(e.error,'')
		FROM workflow_executions e LEFT JOIN workflows w ON w.id = e.workflow_id
		WHERE e.profile_id = ? ORDER BY `+sinceExpr("e.created_at")+` DESC LIMIT ?`, o.ProfileID, recentLimit)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var r ExecRow
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.WorkflowName, &r.Status, &r.TriggerType,
			&r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.Error); err != nil {
			s.Error = err.Error()
			return s
		}
		s.Recent = append(s.Recent, r)
	}
	s.Error = errString(rows.Err())
	return s
}

// cronParser matches internal/scheduler (cron.WithSeconds) plus descriptors
// such as "@every 5m", which robfig accepts there too.
var cronParser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func schedulesSection(ctx context.Context, o Options) *SchedulesSection {
	s := &SchedulesSection{Upcoming: []ScheduleRow{}, Invalid: []ScheduleIssue{}}
	if o.DaemonRunning != nil {
		s.DaemonRunning = o.DaemonRunning()
	}
	if o.Workflows == nil {
		s.Error = "workflow store unavailable"
		return s
	}
	wfs, err := o.Workflows.ListWorkflows(ctx, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for _, meta := range wfs {
		if !meta.IsActive {
			continue
		}
		wf := &meta
		if len(meta.Nodes) == 0 { // list rows may omit nodes; load the full workflow
			if full, err := o.Workflows.GetWorkflow(ctx, meta.ID); err == nil && full != nil {
				wf = full
			}
		}
		for _, n := range wf.Nodes {
			if n.Type != "trigger.schedule" || n.Disabled {
				continue
			}
			if n.Config == nil {
				_ = n.ParseConfig()
			}
			row, err := nextRun(wf, n, o.Now)
			if err != nil {
				s.Invalid = append(s.Invalid, ScheduleIssue{WorkflowID: wf.ID, NodeID: n.ID, Error: err.Error()})
				continue
			}
			s.Upcoming = append(s.Upcoming, row)
		}
	}
	sort.Slice(s.Upcoming, func(i, j int) bool { return s.Upcoming[i].NextRun < s.Upcoming[j].NextRun })
	if len(s.Upcoming) > upcomingLimit {
		s.Upcoming = s.Upcoming[:upcomingLimit]
	}
	return s
}

// nextRun builds the spec exactly as trigger_manager.activateSchedule does.
func nextRun(wf *workflow.Workflow, n workflow.WorkflowNode, now time.Time) (ScheduleRow, error) {
	spec, _ := n.Config["cron"].(string)
	if spec == "" {
		return ScheduleRow{}, errors.New(`missing "cron"`)
	}
	tz, _ := n.Config["timezone"].(string)
	if tz == "" {
		tz = "UTC"
	}
	sched, err := cronParser.Parse(fmt.Sprintf("CRON_TZ=%s %s", tz, spec))
	if err != nil {
		return ScheduleRow{}, err
	}
	return ScheduleRow{WorkflowID: wf.ID, WorkflowName: wf.Name, NodeID: n.ID, Cron: spec, Timezone: tz,
		NextRun: sched.Next(now).UTC().Format(time.RFC3339)}, nil
}
```

Note: the daemon's parser is `cron.New(cron.WithSeconds())`, i.e. seconds **required**. `SecondOptional` here is deliberately more lenient so a 5-field spec shows up in `upcoming` — but that spec would be rejected by the daemon. To stay truthful, after parsing also reject when `len(strings.Fields(spec)) == 5`:

```go
	if !strings.HasPrefix(spec, "@") && len(strings.Fields(spec)) == 5 {
		return ScheduleRow{}, errors.New("5-field cron: the scheduler requires seconds (6 fields)")
	}
```
Add that check (and the `strings` import) right after the `spec == ""` check.

- [ ] **Step 6: Stub the other sections so the package compiles**

Create `internal/summary/inbox.go` and `internal/summary/system.go` containing only the types and functions Tasks 2–3 fill in; each function returns an empty section with `Error: "not implemented"`. (Tasks 2 and 3 replace these files wholesale — the stub exists only so Task 1 builds.)

```go
// internal/summary/inbox.go (stub — replaced in Task 2)
package summary

import "context"

type HILSection struct{ Error string `json:"error,omitempty"` }
type PeopleSection struct{ Error string `json:"error,omitempty"` }
type ActivitySection struct{ Error string `json:"error,omitempty"` }
type ApplicationsSection struct{ Error string `json:"error,omitempty"` }

func hilSection(context.Context, Options) *HILSection { return &HILSection{Error: "not implemented"} }
func peopleSection(context.Context, Options) *PeopleSection { return &PeopleSection{Error: "not implemented"} }
func activitySection(context.Context, Options) *ActivitySection {
	return &ActivitySection{Error: "not implemented"}
}
func applicationsSection(context.Context, Options) *ApplicationsSection {
	return &ApplicationsSection{Error: "not implemented"}
}
```

```go
// internal/summary/system.go (stub — replaced in Task 3)
package summary

import "context"

type AutomationSource interface{}
type BridgeStatus struct{}
type DaemonStatus struct{}
type ServicesSection struct{ Error string `json:"error,omitempty"` }
type AutomationsSection struct{ Error string `json:"error,omitempty"` }
type RecordingsSection struct{ Error string `json:"error,omitempty"` }
type JevSection struct{ Error string `json:"error,omitempty"` }
type AccountsSection struct{ Error string `json:"error,omitempty"` }
type VaultSection struct{ Error string `json:"error,omitempty"` }

func servicesSection(Options) *ServicesSection { return &ServicesSection{Error: "not implemented"} }
func automationsSection(context.Context, Options) *AutomationsSection {
	return &AutomationsSection{Error: "not implemented"}
}
func recordingsSection(Options) *RecordingsSection { return &RecordingsSection{Error: "not implemented"} }
func jevSection(context.Context, Options) *JevSection { return &JevSection{Error: "not implemented"} }
func accountsSection(context.Context, Options) *AccountsSection {
	return &AccountsSection{Error: "not implemented"}
}
func vaultSection(context.Context, Options) *VaultSection { return &VaultSection{Error: "not implemented"} }
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/summary/ -v`
Expected: PASS for `TestWorkflowSections` and `TestInvalidCronIsReportedNotFatal`. If `workflow_executions` insert fails on a NOT NULL column, add that column to the seed (check `data/migrations/*workflow*` for the full schema) — do not weaken the assertions.

- [ ] **Step 8: Commit**

```bash
git add internal/summary/
git commit -F - -- internal/summary/ <<'MSG'
feat(summary): roll-up package with workflow, execution and schedule sections
MSG
```

---

### Task 2: inbox sections — HIL, people, activity, applications

**Files:**
- Replace: `internal/summary/inbox.go`
- Test: `internal/summary/inbox_test.go`

**Interfaces:**
- Consumes: `Options`, `sinceExpr`, `sqlTime`, `errString` (Task 1); `peoplereview.Pending` (`internal/peoplereview/peoplereview.go:19`).
- Produces JSON:
  - `hil{workflow_pending, people_review, drafts, link_suggestions, total, oldest_waiting_since, error?}`
  - `people{total, added_7d, lists, error?}`
  - `activity{captures_7d, captures_total, documents{total, indexed, index_errors, summarising, summary_errors}, messages_in_7d, messages_out_7d, error?}`
  - `applications{by_status{pending,applied,rejected,cancelled}, evaluated, unevaluated_pending, added_7d, error?}`

- [ ] **Step 1: Write the failing test**

```go
// internal/summary/inbox_test.go
package summary

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/capture"
)

func TestInboxSections(t *testing.T) {
	db := testDB(t)
	for _, q := range []string{
		`INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w1','A',1,'default'), ('wx','X',1,'other')`,
		`INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, status, created_at) VALUES
		 ('h1','e1','w1','n','N','pending','2026-09-26 08:00:00'),
		 ('h2','e1','w1','n','N','approved','2026-09-26 08:00:00'),
		 ('h3','e9','wx','n','N','pending','2026-09-26 07:00:00')`,
		`INSERT INTO people (id, profile_id, platform, platform_username, full_name, category, created_at) VALUES
		 ('p1','default','LINKEDIN','a','A','pending_approval','2026-09-25 10:00:00'),
		 ('p2','default','LINKEDIN','b','B','',                '2026-09-01 10:00:00'),
		 ('p3','default','LINKEDIN','c','C','',                '2026-09-24 10:00:00')`,
		`INSERT INTO person_messages (id, person_id, source, direction, status, profile_id, created_at) VALUES
		 ('m1','p2','linkedin','outbound','draft','default','2026-09-26 09:00:00'),
		 ('m2','p2','linkedin','inbound','sent','default','2026-09-25 09:00:00'),
		 ('m3','p2','linkedin','inbound','sent','default','2026-08-01 09:00:00')`,
		`INSERT INTO person_links (id, profile_id, person_a, person_b, relation, status) VALUES
		 ('l1','default','p2','p3','same','suggested')`,
		`INSERT INTO applications (id, profile_id, kind, status, created_at, updated_at) VALUES
		 ('a1','default','job','pending','2026-09-25T10:00:00Z','2026-09-25T10:00:00Z'),
		 ('a2','default','job','applied','2026-09-01T10:00:00Z','2026-09-02T10:00:00Z')`,
	} {
		if _, err := db.DB.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	caps := []capture.Entry{{CapturedAt: "2026-09-25T10:00:00Z"}, {CapturedAt: "2026-08-01T10:00:00Z"}}
	s := Build(context.Background(), Options{DB: db.DB, ProfileID: "default", Now: now,
		Captures: func() ([]capture.Entry, error) { return caps, nil },
		Sections: map[string]bool{"hil": true, "people": true, "activity": true, "applications": true}})

	h := s.HIL
	if h.WorkflowPending != 1 || h.PeopleReview != 1 || h.Drafts != 1 || h.LinkSuggestions != 1 || h.Total != 4 {
		t.Fatalf("hil = %+v", h)
	}
	if h.OldestWaitingSince == "" {
		t.Fatal("oldest_waiting_since must be set when anything is pending")
	}
	if p := s.People; p.Total != 3 || p.Added7d != 2 {
		t.Fatalf("people = %+v", p)
	}
	a := s.Activity
	if a.Captures7d != 1 || a.CapturesTotal != 2 || a.MessagesIn7d != 1 || a.MessagesOut7d != 1 {
		t.Fatalf("activity = %+v", a)
	}
	ap := s.Applications
	if ap.ByStatus["pending"] != 1 || ap.ByStatus["applied"] != 1 || ap.UnevaluatedPending != 1 || ap.Added7d != 1 {
		t.Fatalf("applications = %+v", ap)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/summary/ -run TestInboxSections -v`
Expected: FAIL — `h.WorkflowPending undefined (type *HILSection has no field …)`.

- [ ] **Step 3: Implement `inbox.go`**

```go
package summary

import (
	"context"
	"database/sql"
	"time"

	"github.com/monoes/mono-agent/internal/peoplereview"
)

type HILSection struct {
	WorkflowPending    int    `json:"workflow_pending"`
	PeopleReview       int    `json:"people_review"`
	Drafts             int    `json:"drafts"`
	LinkSuggestions    int    `json:"link_suggestions"`
	Total              int    `json:"total"`
	OldestWaitingSince string `json:"oldest_waiting_since"`
	Error              string `json:"error,omitempty"`
}

type PeopleSection struct {
	Total   int    `json:"total"`
	Added7d int    `json:"added_7d"`
	Lists   int    `json:"lists"`
	Error   string `json:"error,omitempty"`
}

type DocumentCounts struct {
	Total         int `json:"total"`
	Indexed       int `json:"indexed"`
	IndexErrors   int `json:"index_errors"`
	Summarising   int `json:"summarising"`
	SummaryErrors int `json:"summary_errors"`
}

type ActivitySection struct {
	Captures7d    int            `json:"captures_7d"`
	CapturesTotal int            `json:"captures_total"`
	Documents     DocumentCounts `json:"documents"`
	MessagesIn7d  int            `json:"messages_in_7d"`
	MessagesOut7d int            `json:"messages_out_7d"`
	Error         string         `json:"error,omitempty"`
}

type ApplicationsSection struct {
	ByStatus           map[string]int `json:"by_status"`
	Evaluated          int            `json:"evaluated"`
	UnevaluatedPending int            `json:"unevaluated_pending"`
	Added7d            int            `json:"added_7d"`
	Error              string         `json:"error,omitempty"`
}

func week(o Options) string { return sqlTime(o.Now.Add(-7 * 24 * time.Hour)) }

// hilSection counts what a person must decide, without asking Jev (plan D6).
// Org questions/approvals/gates come from `org summary`, not here.
func hilSection(ctx context.Context, o Options) *HILSection {
	s := &HILSection{}
	var oldest sql.NullString
	err := o.DB.QueryRowContext(ctx, `SELECT COUNT(*), MIN(h.created_at) FROM hil_pending h
		JOIN workflows w ON w.id = h.workflow_id
		WHERE h.status = 'pending' AND w.profile_id = ?`, o.ProfileID).Scan(&s.WorkflowPending, &oldest)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	s.OldestWaitingSince = oldest.String
	queries := []struct {
		dst *int
		q   string
		arg []any
	}{
		{&s.PeopleReview, `SELECT COUNT(*) FROM people WHERE profile_id = ? AND category = ?`, []any{o.ProfileID, peoplereview.Pending}},
		{&s.Drafts, `SELECT COUNT(*) FROM person_messages WHERE profile_id = ? AND status = 'draft'`, []any{o.ProfileID}},
		{&s.LinkSuggestions, `SELECT COUNT(*) FROM person_links WHERE profile_id = ? AND status = 'suggested'`, []any{o.ProfileID}},
	}
	for _, q := range queries {
		if err := o.DB.QueryRowContext(ctx, q.q, q.arg...).Scan(q.dst); err != nil {
			s.Error = err.Error()
			return s
		}
	}
	s.Total = s.WorkflowPending + s.PeopleReview + s.Drafts + s.LinkSuggestions
	return s
}

func peopleSection(ctx context.Context, o Options) *PeopleSection {
	s := &PeopleSection{}
	err := o.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(`+sinceExpr("created_at")+` >= julianday(?)),0)
		FROM people WHERE profile_id = ?`, week(o), o.ProfileID).Scan(&s.Total, &s.Added7d)
	if err == nil {
		err = o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM social_lists WHERE profile_id = ?`, o.ProfileID).Scan(&s.Lists)
	}
	s.Error = errString(err)
	return s
}

// activitySection reads vault_documents directly rather than calling
// `profile documents list`, which syncs (writes) first — summary is read-only.
func activitySection(ctx context.Context, o Options) *ActivitySection {
	s := &ActivitySection{}
	if o.Captures != nil {
		caps, err := o.Captures()
		if err != nil {
			s.Error = err.Error()
		}
		bound := o.Now.Add(-7 * 24 * time.Hour)
		for _, c := range caps {
			s.CapturesTotal++
			if t, err := time.Parse(time.RFC3339, c.CapturedAt); err == nil && !t.Before(bound) {
				s.Captures7d++
			}
		}
	}
	err := o.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(indexed = 1),0),
		COALESCE(SUM(COALESCE(index_error,'') <> ''),0)
		FROM vault_documents WHERE profile_id = ?`, o.ProfileID).
		Scan(&s.Documents.Total, &s.Documents.Indexed, &s.Documents.IndexErrors)
	if err == nil {
		err = o.DB.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(direction = 'inbound'),0), COALESCE(SUM(direction = 'outbound' AND status <> 'draft'),0)
			FROM person_messages WHERE profile_id = ? AND `+sinceExpr("created_at")+` >= julianday(?)`,
			o.ProfileID, week(o)).Scan(&s.MessagesIn7d, &s.MessagesOut7d)
	}
	if err != nil && s.Error == "" {
		s.Error = err.Error()
	}
	return s
}

func applicationsSection(ctx context.Context, o Options) *ApplicationsSection {
	s := &ApplicationsSection{ByStatus: map[string]int{"pending": 0, "applied": 0, "rejected": 0, "cancelled": 0}}
	rows, err := o.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM applications WHERE profile_id = ? GROUP BY status`, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			s.ByStatus[st] = n
		}
	}
	rows.Close()
	err = o.DB.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(DISTINCT e.application_id) FROM application_evaluations e JOIN applications a ON a.id = e.application_id WHERE a.profile_id = ?),
		(SELECT COUNT(*) FROM applications a WHERE a.profile_id = ? AND a.status = 'pending'
		   AND NOT EXISTS (SELECT 1 FROM application_evaluations e WHERE e.application_id = a.id)),
		(SELECT COUNT(*) FROM applications WHERE profile_id = ? AND `+sinceExpr("created_at")+` >= julianday(?))`,
		o.ProfileID, o.ProfileID, o.ProfileID, week(o)).Scan(&s.Evaluated, &s.UnevaluatedPending, &s.Added7d)
	s.Error = errString(err)
	return s
}
```

Before running, confirm the column names this code assumes: `vault_documents.indexed`, `vault_documents.index_error` (`data/migrations/037_vault_documents_indexed.sql`), `application_evaluations.application_id` (`036_application_evaluations.sql`), `people.created_at`. Run `grep -n 'indexed\|index_error' data/migrations/037_*.sql; grep -n application_id data/migrations/036_*.sql`. If a name differs, fix the query, not the test.

`summarising`/`summary_errors` stay 0 in this task: summary status lives in capture-directory files (`internal/capturesummary/status.go:42-45`). Fill them in Step 4.

- [ ] **Step 4: Summary-status counts for captured documents**

Read `internal/capturesummary/status.go` for the exported function that returns a capture dir's status (`pending|running|done|error|stalled`). Add an `Options.SummaryStatus func(captureDir string) string` field, and in `activitySection` iterate `SELECT capture_dir FROM vault_documents WHERE profile_id = ? AND COALESCE(capture_dir,'') <> ''`, counting `pending|running` into `Summarising` and `error|stalled` into `SummaryErrors`. Extend the test with two documents with `capture_dir` values and a fake `SummaryStatus` returning `"running"` and `"error"`, asserting `Summarising == 1 && SummaryErrors == 1`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/summary/ -v`
Expected: PASS (all tests so far).

- [ ] **Step 6: Commit**

```bash
git commit -F - -- internal/summary/inbox.go internal/summary/inbox_test.go internal/summary/summary.go <<'MSG'
feat(summary): hil, people, activity and application counts
MSG
```

---

### Task 3: system sections — services, automations, recordings, Jev, accounts, vault

**Files:**
- Replace: `internal/summary/system.go`
- Test: `internal/summary/system_test.go`

**Interfaces:**
- Consumes: `automation.InstalledInfo` (`internal/automation/types.go:100-125`), `automation.SelectorHealth` + `automation.SelectorStatus(h) string` (`internal/automation/health_status.go:63`), `jevconf.UsageSince`, `jevconf.KeySource`, `jevconf.Enabled`, `jevconf.Surfaces` (`internal/jev/jevconf/jevconf.go`), `recording.Summary`.
- Produces:
  - `type AutomationSource interface { List() ([]automation.InstalledInfo, error); SelectorHealth() ([]automation.SelectorHealth, error) }`
  - `type BridgeStatus struct { Status string; Connected bool; Addr string; InFlight int; Version string }` (json snake_case)
  - `type DaemonStatus struct { Running bool; PID int; APIAddr, BridgeAddr, Version string; HeartbeatAgeMS int64 }`
  - JSON: `services{daemon{running,pid,api_addr,bridge_addr,version,heartbeat_age_ms}, bridge{status,connected,addr,in_flight,version}|null, org_serve{running,orgs[]}, error?}`, `automations{installed, enabled, unavailable, pending_update, scripts_blocked, selectors{ok,decaying,broken,stale}, broken[{automation_id,selector_key}], error?}`, `recordings{total, unsaved, incomplete, latest_started_at, error?}`, `jev{key_configured, key_source, surfaces_enabled, calls_24h, failures_24h, estimated_usd_24h, error?}`, `accounts{sessions[{platform,username,expiry,status}], active, expired, expiring_soon, error?}`, `vault{secrets, images, image_bytes, error?}`

- [ ] **Step 1: Write the failing test**

```go
// internal/summary/system_test.go
package summary

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/recording"
)

type fakeAutomations struct {
	infos  []automation.InstalledInfo
	health []automation.SelectorHealth
}

func (f fakeAutomations) List() ([]automation.InstalledInfo, error)                  { return f.infos, nil }
func (f fakeAutomations) SelectorHealth() ([]automation.SelectorHealth, error) { return f.health, nil }

func TestSystemSections(t *testing.T) {
	db := testDB(t)
	for _, q := range []string{
		`INSERT INTO crawler_sessions (platform, username, expiry, profile_id) VALUES
		 ('linkedin','me','2026-10-10 00:00:00','default'),
		 ('x','me','2026-09-27 08:00:00','default'),
		 ('tiktok','me','2026-09-01 00:00:00','default')`,
		`INSERT INTO vault_images (id, seq, path, filename, size_bytes, profile_id) VALUES ('img-001',1,'/p','a.png',100,'default')`,
	} {
		if _, err := db.DB.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	autos := fakeAutomations{
		infos: []automation.InstalledInfo{
			{ID: "linkedin", Enabled: true, Available: true},
			{ID: "hn", Enabled: false, Available: false, PendingUpdate: "1.2.0", ContainsScripts: true, ScriptsAllowed: false},
		},
		health: []automation.SelectorHealth{
			{AutomationID: "linkedin", SelectorKey: "post.like", OKCount: 10},
			{AutomationID: "linkedin", SelectorKey: "post.send", FailCount: 6, Recent: "FFFFFF"},
		},
	}
	recs := []recording.Summary{{ID: "r1", Complete: true, StartedAt: "2026-09-25T10:00:00Z"},
		{ID: "r2", Complete: true, Automation: "linkedin", StartedAt: "2026-09-20T10:00:00Z"},
		{ID: "r3", Complete: false, StartedAt: "2026-09-26T10:00:00Z"}}
	s := Build(context.Background(), Options{DB: db.DB, ProfileID: "default", Now: now,
		Automations: autos,
		Recordings:  func() ([]recording.Summary, error) { return recs, nil },
		Daemon:      func() *DaemonStatus { return &DaemonStatus{Running: true, PID: 42, BridgeAddr: "127.0.0.1:9222"} },
		Bridge:      func() (*BridgeStatus, error) { return &BridgeStatus{Status: "connected", Connected: true}, nil },
		OrgServe:    func() (bool, []string) { return true, []string{"acme"} },
		Sections:    map[string]bool{"services": true, "automations": true, "recordings": true, "jev": true, "accounts": true, "vault": true}})

	if sv := s.Services; !sv.Daemon.Running || sv.Bridge == nil || !sv.Bridge.Connected || len(sv.OrgServe.Orgs) != 1 {
		t.Fatalf("services = %+v", sv)
	}
	a := s.Automations
	if a.Installed != 2 || a.Enabled != 1 || a.Unavailable != 1 || a.PendingUpdate != 1 || a.ScriptsBlocked != 1 {
		t.Fatalf("automations = %+v", a)
	}
	if a.Selectors.Broken != 1 || a.Selectors.OK != 1 || len(a.BrokenList) != 1 || a.BrokenList[0].SelectorKey != "post.send" {
		t.Fatalf("selectors = %+v / %+v", a.Selectors, a.BrokenList)
	}
	if r := s.Recordings; r.Total != 3 || r.Unsaved != 1 || r.Incomplete != 1 || r.LatestStartedAt != "2026-09-26T10:00:00Z" {
		t.Fatalf("recordings = %+v", r)
	}
	if j := s.Jev; j.KeyConfigured || j.Calls24h != 0 || j.Error != "" {
		t.Fatalf("jev with no key = %+v", j)
	}
	if ac := s.Accounts; ac.Active != 2 || ac.Expired != 1 || ac.ExpiringSoon != 1 || len(ac.Sessions) != 3 {
		t.Fatalf("accounts = %+v", ac)
	}
	if v := s.Vault; v.Images != 1 || v.ImageBytes != 100 {
		t.Fatalf("vault = %+v", v)
	}
}

// D13: the roll-up carries vault counts only, never anything identifying a secret.
func TestVaultSectionLeaksNoSecretFields(t *testing.T) {
	db := testDB(t)
	if _, err := db.DB.Exec(`INSERT INTO vault_secrets (id, profile_id, kind, name, username, url) VALUES
		('s1','default','login','ZZ-SECRET-NAME','zz-user','https://zz.example')`); err != nil {
		t.Skipf("adjust seed to vault_secrets' real NOT NULL columns (017/021/022 migrations): %v", err)
	}
	s := Build(context.Background(), Options{DB: db.DB, ProfileID: "default", Now: now, Sections: map[string]bool{"vault": true}})
	raw, _ := json.Marshal(s)
	for _, leak := range []string{"ZZ-SECRET-NAME", "zz-user", "zz.example"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("summary leaked %q: %s", leak, raw)
		}
	}
	if s.Vault.Secrets != 1 {
		t.Fatalf("vault = %+v", s.Vault)
	}
}
```

The `SelectorHealth` field names (`AutomationID`, `SelectorKey`, `OKCount`, `FailCount`, `Recent`) must match `internal/automation/health.go`; open it first and adjust the fixture to the real names so that `SelectorStatus` classifies `post.send` as `broken` (≥ 5 trailing failures, `health_status.go:10-15`).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/summary/ -run TestSystemSections -v`
Expected: FAIL — `unknown field Installed in struct literal` / `AutomationSource` has no method `List`.

- [ ] **Step 3: Implement `system.go`**

```go
package summary

import (
	"context"
	"database/sql"
	"time"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/jev/jevconf"
)

// AutomationSource is what the automations section reads; cmd wires the
// real registry and selector-health table (automation_doctor.go).
type AutomationSource interface {
	List() ([]automation.InstalledInfo, error)
	SelectorHealth() ([]automation.SelectorHealth, error)
}

type DaemonStatus struct {
	Running        bool   `json:"running"`
	PID            int    `json:"pid"`
	APIAddr        string `json:"api_addr"`
	BridgeAddr     string `json:"bridge_addr"`
	Version        string `json:"version"`
	HeartbeatAgeMS int64  `json:"heartbeat_age_ms"`
}

type BridgeStatus struct {
	Status    string `json:"status"` // connected | waiting | unpaired
	Connected bool   `json:"connected"`
	Addr      string `json:"addr"`
	InFlight  int    `json:"in_flight"`
	Version   string `json:"version"`
}

type OrgServeStatus struct {
	Running bool     `json:"running"`
	Orgs    []string `json:"orgs"`
}

type ServicesSection struct {
	Daemon   DaemonStatus   `json:"daemon"`
	Bridge   *BridgeStatus  `json:"bridge"` // null when no bridge answers
	OrgServe OrgServeStatus `json:"org_serve"`
	Error    string         `json:"error,omitempty"`
}

type SelectorCounts struct {
	OK       int `json:"ok"`
	Decaying int `json:"decaying"`
	Broken   int `json:"broken"`
	Stale    int `json:"stale"`
}

type SelectorRef struct {
	AutomationID string `json:"automation_id"`
	SelectorKey  string `json:"selector_key"`
}

type AutomationsSection struct {
	Installed      int            `json:"installed"`
	Enabled        int            `json:"enabled"`
	Unavailable    int            `json:"unavailable"`
	PendingUpdate  int            `json:"pending_update"`
	ScriptsBlocked int            `json:"scripts_blocked"`
	Selectors      SelectorCounts `json:"selectors"`
	BrokenList     []SelectorRef  `json:"broken"`
	Error          string         `json:"error,omitempty"`
}

type RecordingsSection struct {
	Total           int    `json:"total"`
	Unsaved         int    `json:"unsaved"`    // complete, not yet saved into an automation
	Incomplete      int    `json:"incomplete"` // no stop frame
	LatestStartedAt string `json:"latest_started_at"`
	Error           string `json:"error,omitempty"`
}

type JevSection struct {
	KeyConfigured   bool    `json:"key_configured"`
	KeySource       string  `json:"key_source"`
	SurfacesEnabled int     `json:"surfaces_enabled"`
	Calls24h        int     `json:"calls_24h"`
	Failures24h     int     `json:"failures_24h"`
	EstimatedUSD24h float64 `json:"estimated_usd_24h"`
	Error           string  `json:"error,omitempty"`
}

type SessionRow struct {
	Platform string `json:"platform"`
	Username string `json:"username"`
	Expiry   string `json:"expiry"`
	Status   string `json:"status"` // active | expiring | expired
}

type AccountsSection struct {
	Sessions     []SessionRow `json:"sessions"`
	Active       int          `json:"active"` // includes expiring
	Expired      int          `json:"expired"`
	ExpiringSoon int          `json:"expiring_soon"`
	Error        string       `json:"error,omitempty"`
}

type VaultSection struct {
	Secrets    int    `json:"secrets"`
	Images     int    `json:"images"`
	ImageBytes int64  `json:"image_bytes"`
	Error      string `json:"error,omitempty"`
}

// ExpiringWindow is how close to expiry a login counts as "expiring".
const ExpiringWindow = 72 * time.Hour

func servicesSection(o Options) *ServicesSection {
	s := &ServicesSection{OrgServe: OrgServeStatus{Orgs: []string{}}}
	if o.Daemon != nil {
		if d := o.Daemon(); d != nil {
			s.Daemon = *d
		}
	}
	if o.Bridge != nil {
		if b, err := o.Bridge(); err == nil {
			s.Bridge = b
		}
	}
	if o.OrgServe != nil {
		running, orgs := o.OrgServe()
		s.OrgServe.Running = running
		if orgs != nil {
			s.OrgServe.Orgs = orgs
		}
	}
	return s
}

func automationsSection(_ context.Context, o Options) *AutomationsSection {
	s := &AutomationsSection{BrokenList: []SelectorRef{}}
	if o.Automations == nil {
		s.Error = "automation registry unavailable"
		return s
	}
	infos, err := o.Automations.List()
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for _, i := range infos {
		s.Installed++
		if i.Enabled {
			s.Enabled++
		}
		if !i.Available {
			s.Unavailable++
		}
		if i.PendingUpdate != "" {
			s.PendingUpdate++
		}
		if i.ContainsScripts && !i.ScriptsAllowed {
			s.ScriptsBlocked++
		}
	}
	health, err := o.Automations.SelectorHealth()
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for _, h := range health {
		switch automation.SelectorStatus(h) {
		case "ok":
			s.Selectors.OK++
		case "decaying":
			s.Selectors.Decaying++
		case "broken":
			s.Selectors.Broken++
			s.BrokenList = append(s.BrokenList, SelectorRef{AutomationID: h.AutomationID, SelectorKey: h.SelectorKey})
		case "stale":
			s.Selectors.Stale++
		}
	}
	return s
}

func recordingsSection(o Options) *RecordingsSection {
	s := &RecordingsSection{}
	if o.Recordings == nil {
		s.Error = "recording store unavailable"
		return s
	}
	list, err := o.Recordings()
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for _, r := range list {
		s.Total++
		if !r.Complete {
			s.Incomplete++
		} else if r.Automation == "" {
			s.Unsaved++
		}
		if r.StartedAt > s.LatestStartedAt { // RFC3339 sorts lexically
			s.LatestStartedAt = r.StartedAt
		}
	}
	return s
}

// jevSection reads configuration and the local usage table only — no call
// to TypeSafe, not even a key test.
func jevSection(ctx context.Context, o Options) *JevSection {
	s := &JevSection{}
	if src, err := jevconf.KeySource(ctx, o.DB, o.ProfileID); err == nil {
		s.KeyConfigured, s.KeySource = true, src
	}
	for _, surf := range jevconf.Surfaces {
		if jevconf.Enabled(o.DB, o.ProfileID, surf) {
			s.SurfacesEnabled++
		}
	}
	usage, err := jevconf.UsageSince(o.DB, o.ProfileID, o.Now.Add(-24*time.Hour))
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for _, u := range usage {
		s.Calls24h += u.Calls
		s.Failures24h += u.Failures
		s.EstimatedUSD24h += u.EstimatedUSD
	}
	return s
}

func accountsSection(ctx context.Context, o Options) *AccountsSection {
	s := &AccountsSection{Sessions: []SessionRow{}}
	rows, err := o.DB.QueryContext(ctx, `SELECT platform, username, expiry FROM crawler_sessions
		WHERE profile_id = ? ORDER BY platform`, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var r SessionRow
		var expiry time.Time
		if err := rows.Scan(&r.Platform, &r.Username, &expiry); err != nil {
			s.Error = err.Error()
			return s
		}
		r.Expiry = expiry.UTC().Format(time.RFC3339)
		switch {
		case expiry.Before(o.Now):
			r.Status = "expired"
			s.Expired++
		case expiry.Before(o.Now.Add(ExpiringWindow)):
			r.Status = "expiring"
			s.Active++
			s.ExpiringSoon++
		default:
			r.Status = "active"
			s.Active++
		}
		s.Sessions = append(s.Sessions, r)
	}
	s.Error = errString(rows.Err())
	return s
}

func vaultSection(ctx context.Context, o Options) *VaultSection {
	s := &VaultSection{}
	var bytes sql.NullInt64
	err := o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_secrets WHERE profile_id = ?`, o.ProfileID).Scan(&s.Secrets)
	if err == nil {
		err = o.DB.QueryRowContext(ctx, `SELECT COUNT(*), SUM(size_bytes) FROM vault_images WHERE profile_id = ?`,
			o.ProfileID).Scan(&s.Images, &bytes)
	}
	s.ImageBytes = bytes.Int64
	s.Error = errString(err)
	return s
}
```

`crawler_sessions.expiry` scanning into `time.Time` mirrors `querySessionIndex` (`cmd/monoagentcli/automation_list.go:153`), which works with this driver. If the test's string-typed seed doesn't scan into `time.Time`, seed via a Go `time.Time` parameter the way production writes it (look at the session save path in `internal/storage`), instead of changing the scan.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/summary/ -v && go vet ./internal/summary/`
Expected: PASS, no vet output.

- [ ] **Step 5: Commit**

```bash
git commit -F - -- internal/summary/system.go internal/summary/system_test.go <<'MSG'
feat(summary): services, automations, recordings, jev, accounts and vault sections
MSG
```

---

### Task 4: `monoagentcli summary` command

**Files:**
- Create: `cmd/monoagentcli/summary.go`, `cmd/monoagentcli/summary_test.go`
- Modify: `cmd/monoagentcli/root.go:82` (register), `AGENTS.md` (§ machine-readable output, near `:115-145`)

**Interfaces:**
- Consumes: `summary.Build`, `summary.Options`, `summary.SectionNames`; `newHybridStore(db)` (`workflow.go:34`); `openAutomationRegistry()` (`automation.go:112`); `automation.LoadSelectorHealth(db, "")`; `recording.List()`; `capture.List(capture.DefaultInbox())`; `daemonhb.Read()` (`internal/daemonhb`); `extension.FetchStatus(baseURL)` (`internal/extension/status.go:143`); `monomind.ReadServeHeartbeat(root)`; `profiledir.Root(db.DB, profileID)`; `errInvalidInput`; `writeJSONTo`.
- Produces: `monoagentcli [--profile P] --json summary [--section a,b]` → `summary.Summary` JSON; text mode prints one line per section.

- [ ] **Step 1: Write the failing tests**

```go
// cmd/monoagentcli/summary_test.go
package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/storage"
)

func newSummaryCLITestDB(t *testing.T) *globalConfig {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	dbPath := filepath.Join(t.TempDir(), "s.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, status) VALUES ('h1','e','w1','n','N','pending');
		INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w1','A',1,'default')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}
}

func runSummary(t *testing.T, cfg *globalConfig, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		cmd := newSummaryCmd(cfg)
		cmd.SetArgs(args)
		err = cmd.Execute()
	})
	return out, err
}

func TestSummaryJSONHasEverySection(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	out, err := runSummary(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range append([]string{"v", "generated_at", "profile_id"},
		"workflows", "executions", "schedules", "hil", "people", "activity", "applications",
		"services", "automations", "recordings", "jev", "accounts", "vault") {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q in %s", k, out)
		}
	}
	if !strings.Contains(string(got["hil"]), `"workflow_pending":1`) {
		t.Errorf("hil = %s", got["hil"])
	}
}

func TestSummarySectionFilter(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	out, err := runSummary(t, cfg, "--section", "hil,workflows")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out), &got)
	if _, ok := got["executions"]; ok {
		t.Fatalf("unrequested section present: %s", out)
	}
	if _, err := runSummary(t, cfg, "--section", "nope"); exitCode(err) != 2 {
		t.Fatalf("unknown section exit = %d, want 2", exitCode(err))
	}
}

// The dashboard polls this every 15 s: it must never reach TypeSafe, even
// with a key configured and every surface enabled.
func TestSummaryNeverCallsJev(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	srv := jevtest.NewServer(t, jevtest.Fixed(nil))
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("TYPESAFE_BASE_URL", srv.URL)
	if _, err := runSummary(t, cfg); err != nil {
		t.Fatal(err)
	}
	if n := srv.Calls(); n != 0 {
		t.Fatalf("summary made %d Jev calls", n)
	}
}
```

Check the exact env var Jev reads for its base URL (`grep -rn 'BASE_URL\|base_url' internal/jev/`) and the jevtest server's URL field name (`internal/jev/jevtest/jevtest.go:50`) and use those in the test.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./cmd/monoagentcli/ -run TestSummary -v`
Expected: FAIL — `undefined: newSummaryCmd`.

- [ ] **Step 3: Implement the command**

```go
// cmd/monoagentcli/summary.go
package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/recording"
	"github.com/monoes/mono-agent/internal/summary"
)

func newSummaryCmd(cfg *globalConfig) *cobra.Command {
	var sections string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "At-a-glance roll-up of everything that needs attention (read-only, local, fast)",
		Long: "Counts across workflows, runs, schedules, approvals, people, captures, applications, " +
			"automation packages, recordings, Jev usage, logins and the background services. " +
			"Read-only and local: it never calls Jev, monomind or the network. The desktop dashboard polls it. " +
			"For orgs, use `org summary`.",
		Example: `  monoagentcli --json summary
  monoagentcli --json summary --section hil,executions`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			want, err := parseSections(sections)
			if err != nil {
				return err
			}
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			root := profiledir.Root(db.DB, cfg.ProfileID)
			opts := summary.Options{
				DB: db.DB, ProfileID: cfg.ProfileID, Now: time.Now(), Sections: want,
				Workflows:     newHybridStore(db),
				DaemonRunning: func() bool { _, live := daemonhb.Read(); return live },
				Daemon:        summaryDaemon,
				Bridge:        summaryBridge,
				OrgServe: func() (bool, []string) {
					hb, live := monomind.ReadServeHeartbeat(root)
					if hb == nil {
						return false, nil
					}
					return live, hb.Running
				},
				Recordings: func() ([]recording.Summary, error) {
					if err := applyRecordScope(cfg); err != nil {
						return nil, err
					}
					return recording.List()
				},
				Captures: func() ([]capture.Entry, error) { return capture.List(capture.DefaultInbox()) },
			}
			if reg, err := openAutomationRegistry(); err == nil {
				opts.Automations = automationSource{reg: reg, db: db.DB}
			}
			s := summary.Build(cmd.Context(), opts)
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), s)
			}
			printSummaryText(cmd, s)
			return nil
		},
	}
	cmd.Flags().StringVar(&sections, "section", "", "Comma-separated sections to include (default: all): "+strings.Join(summary.SectionNames, ","))
	return cmd
}

func parseSections(csv string) (map[string]bool, error) {
	if strings.TrimSpace(csv) == "" {
		return nil, nil
	}
	known := map[string]bool{}
	for _, n := range summary.SectionNames {
		known[n] = true
	}
	want := map[string]bool{}
	for _, n := range strings.Split(csv, ",") {
		n = strings.TrimSpace(n)
		if !known[n] {
			return nil, errInvalidInput("unknown section %q (known: %s)", n, strings.Join(summary.SectionNames, ", "))
		}
		want[n] = true
	}
	return want, nil
}

type automationSource struct {
	reg *automation.Registry
	db  *sql.DB
}

func (a automationSource) List() ([]automation.InstalledInfo, error) { return a.reg.List(false) }
func (a automationSource) SelectorHealth() ([]automation.SelectorHealth, error) {
	return automation.LoadSelectorHealth(a.db, "")
}

func summaryDaemon() *summary.DaemonStatus {
	hb, live := daemonhb.Read()
	if hb.PID <= 0 {
		return &summary.DaemonStatus{}
	}
	return &summary.DaemonStatus{Running: live, PID: hb.PID, APIAddr: hb.APIAddr, BridgeAddr: hb.BridgeAddr,
		Version: hb.Version, HeartbeatAgeMS: time.Since(hb.TS).Milliseconds()}
}

// summaryBridge probes the first loopback address that answers — the same
// list `extension status` uses — with FetchStatus's short timeout.
func summaryBridge() (*summary.BridgeStatus, error) {
	st, addr, ok := findRunningBridge()
	if !ok {
		return nil, fmt.Errorf("no bridge")
	}
	return &summary.BridgeStatus{Status: st.Status, Connected: st.Connected, Addr: addr,
		InFlight: st.InFlight, Version: st.Version}, nil
}

func printSummaryText(cmd *cobra.Command, s summary.Summary) {
	out := cmd.OutOrStdout()
	if w := s.Workflows; w != nil {
		fmt.Fprintf(out, "workflows     %d (%d active)\n", w.Total, w.Active)
	}
	if e := s.Executions; e != nil {
		fmt.Fprintf(out, "runs          %d running, %d queued · 24h: %d ok, %d failed\n", e.Running, e.Queued, e.Last24h.Success, e.Last24h.Failed)
	}
	if h := s.HIL; h != nil {
		fmt.Fprintf(out, "needs you     %d (approvals %d, leads %d, drafts %d, links %d)\n", h.Total, h.WorkflowPending, h.PeopleReview, h.Drafts, h.LinkSuggestions)
	}
	if a := s.Automations; a != nil {
		fmt.Fprintf(out, "automations   %d installed · selectors broken %d, decaying %d\n", a.Installed, a.Selectors.Broken, a.Selectors.Decaying)
	}
	if ac := s.Accounts; ac != nil {
		fmt.Fprintf(out, "logins        %d active (%d expiring), %d expired\n", ac.Active, ac.ExpiringSoon, ac.Expired)
	}
	if sv := s.Services; sv != nil {
		fmt.Fprintf(out, "daemon        %v · bridge %v · org serve %v\n", sv.Daemon.Running, sv.Bridge != nil && sv.Bridge.Connected, sv.OrgServe.Running)
	}
}
```

`findRunningBridge` and `applyRecordScope` already exist in `package main` (`extension_serve.go:82`, `record.go`). Confirm that `FetchStatus`'s `statusProbeTimeout` is ≤ 500 ms (`internal/extension/status.go`); if it's longer, add a `FetchStatusTimeout(baseURL, d)` variant there and use 400 ms here (Global Constraints).

Register in `root.go` next to `newStatusCmd(cfg)`:

```go
		newStatusCmd(cfg),
		newSummaryCmd(cfg),
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/monoagentcli/ -run 'TestSummary' -v && go vet ./cmd/monoagentcli/`
Expected: PASS.

- [ ] **Step 5: Perf gate on a real profile**

Run: `go build -o ~/scratch/dash/monoagentcli ./cmd/monoagentcli && for i in 1 2 3 4 5 6 7 8 9 10; do /usr/bin/time -f %e ~/scratch/dash/monoagentcli --json summary >/dev/null; done`
Expected: every run ≤ 0.25 s. If slower, time sections individually with `--section X` and fix the slow one (likely candidates: automation registry seeding in `openAutomationRegistry`, capture inbox walk). Record the numbers in the PR description.

- [ ] **Step 6: Document in AGENTS.md**

Add under the machine-readable output section:

```markdown
### `summary` — at-a-glance roll-up

`monoagentcli --json summary [--section workflows,executions,…]` returns counts for
workflows, runs (running/queued, last 24 h), next scheduled runs, things waiting
for a person (workflow HIL, leads to review, drafts, link suggestions), people,
captures/documents/messages (7 days), applications by status, automation packages
and selector health, recordings, Jev usage (24 h, from the local table), logins
(active/expiring within 72 h/expired), vault counts, and daemon/bridge/org-serve
state. It is read-only and local — it never calls Jev, monomind or the network —
so it is safe to poll. A failing section reports `"error"` inside itself; the
command still exits 0. Orgs: `monoagentcli org summary [--fast]`.
```

- [ ] **Step 7: Commit**

```bash
git commit -F - -- cmd/monoagentcli/summary.go cmd/monoagentcli/summary_test.go cmd/monoagentcli/root.go AGENTS.md <<'MSG'
feat(cli): summary command, a read-only local roll-up for the dashboard
MSG
```

---

### Task 5: `monoagentcli org summary [--fast]`

**Files:**
- Create: `cmd/monoagentcli/org_summary.go`, `cmd/monoagentcli/org_summary_test.go`
- Modify: `cmd/monoagentcli/org.go:34` (register)

**Interfaces:**
- Consumes: `orgdesign.ListOrgNames(root)` (`internal/orgdesign/store.go:114`), `orgbridge.ReadQueued(root, org)`, `orgdecide.NewService(db.DB, nil).Store.Get(ctx, profileID, org)` + `a.EffectiveLevel(now)`, `needsYou(ctx, db, profileID, root, org)` (`org_autonomy.go:485`), `monomind.ReadServeHeartbeat(root)`, `env.Profile()` (`org_env.go:76`), `printJSONValue`.
- Produces: `{v:1, fast:bool, serve_running:bool, orgs:[{name, running, level, paused, queued, needs_you (int|null), needs_you_error?}], totals{orgs, running, queued, needs_you}}`. `--fast` skips `needs_you` (null) and therefore spawns no monomind process.

- [ ] **Step 1: Write the failing test**

```go
// cmd/monoagentcli/org_summary_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --fast must be answerable from local files only: two org configs, a
// queue file for one, a serve heartbeat listing it as running.
func TestOrgSummaryFast(t *testing.T) {
	env, root := newOrgTestEnv(t) // existing helper in org_*_test.go; see note below
	orgs := filepath.Join(root, ".monomind", "orgs")
	must(t, os.MkdirAll(filepath.Join(orgs, "acme"), 0o755))
	must(t, os.WriteFile(filepath.Join(orgs, "acme.json"), []byte(`{"name":"acme","roles":[]}`), 0o644))
	must(t, os.WriteFile(filepath.Join(orgs, "ops.json"), []byte(`{"name":"ops","roles":[]}`), 0o644))
	must(t, os.WriteFile(filepath.Join(orgs, "acme", "inbox.jsonl"), []byte("{\"text\":\"hi\"}\n{\"text\":\"yo\"}\n"), 0o644))

	out := captureStdout(t, func() {
		c := newOrgSummaryCmd(env)
		c.SetArgs([]string{"--fast"})
		must(t, c.Execute())
	})
	var got struct {
		Fast bool `json:"fast"`
		Orgs []struct {
			Name     string `json:"name"`
			Queued   int    `json:"queued"`
			NeedsYou *int   `json:"needs_you"`
		} `json:"orgs"`
		Totals struct{ Orgs, Queued int } `json:"totals"`
	}
	must(t, json.Unmarshal([]byte(out), &got))
	if !got.Fast || len(got.Orgs) != 2 || got.Totals.Orgs != 2 || got.Totals.Queued != 2 {
		t.Fatalf("got %+v\n%s", got, out)
	}
	for _, o := range got.Orgs {
		if o.NeedsYou != nil {
			t.Fatalf("--fast must not compute needs_you: %+v", o)
		}
	}
}
```

Before writing it, find the org command test helper that builds an `*orgEnv` over a temp profile root (`grep -n 'func newOrg.*Env\|&orgEnv{' cmd/monoagentcli/*_test.go`) and use it; if none exists, create `newOrgTestEnv(t) (*orgEnv, string)` in this test file following how `org_queued_test.go` builds its env. Check the inbox line format `orgbridge.ReadQueued` expects (`internal/orgbridge`) and match the fixture to it. Add `func must(t *testing.T, err error) { t.Helper(); if err != nil { t.Fatal(err) } }` if the package doesn't have one.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/monoagentcli/ -run TestOrgSummaryFast -v`
Expected: FAIL — `undefined: newOrgSummaryCmd`.

- [ ] **Step 3: Implement**

```go
// cmd/monoagentcli/org_summary.go
package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

type orgSummaryRow struct {
	Name          string `json:"name"`
	Running       bool   `json:"running"`
	Level         string `json:"level"`
	Paused        bool   `json:"paused"`
	Queued        int    `json:"queued"`
	NeedsYou      *int   `json:"needs_you"`
	NeedsYouError string `json:"needs_you_error,omitempty"`
}

// perOrgNeedsYouTimeout bounds the monomind round-trips for one org.
const perOrgNeedsYouTimeout = 10 * time.Second

func newOrgSummaryCmd(env *orgEnv) *cobra.Command {
	var fast bool
	c := &cobra.Command{
		Use:   "summary",
		Short: "One row per org: running, autonomy level, queued messages and items waiting for you",
		Long: "Cross-org roll-up for the dashboard. --fast reads local files only (no monomind process) " +
			"and leaves needs_you null; without it, needs_you is computed per org in parallel.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			names, err := orgdesign.ListOrgNames(root)
			if err != nil {
				return err
			}
			hb, serveLive := monomind.ReadServeHeartbeat(root)
			running := map[string]bool{}
			if hb != nil && serveLive {
				for _, n := range hb.Running {
					running[n] = true
				}
			}
			svc := orgdecide.NewService(db.DB, nil)
			now := time.Now()
			rows := make([]orgSummaryRow, len(names))
			var wg sync.WaitGroup
			for i, name := range names {
				row := orgSummaryRow{Name: name, Running: running[name]}
				if a, err := svc.Store.Get(cmd.Context(), profileID, name); err == nil && a != nil {
					row.Level = string(a.EffectiveLevel(now))
					row.Paused = a.PausedUntil != nil && a.PausedUntil.After(now)
				}
				if msgs, _, err := orgbridge.ReadQueued(root, name); err == nil {
					row.Queued = len(msgs)
				}
				rows[i] = row
				if fast {
					continue
				}
				wg.Add(1)
				go func(i int, name string) {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(cmd.Context(), perOrgNeedsYouTimeout)
					defer cancel()
					items, err := needsYou(ctx, db, profileID, root, name)
					if err != nil {
						rows[i].NeedsYouError = err.Error()
						return
					}
					n := len(items)
					rows[i].NeedsYou = &n
				}(i, name)
			}
			wg.Wait()
			sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			totals := map[string]int{"orgs": len(rows), "running": 0, "queued": 0, "needs_you": 0}
			for _, r := range rows {
				if r.Running {
					totals["running"]++
				}
				totals["queued"] += r.Queued
				if r.NeedsYou != nil {
					totals["needs_you"] += *r.NeedsYou
				}
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "fast": fast, "serve_running": serveLive, "orgs": rows, "totals": totals,
			})
		},
	}
	c.Flags().BoolVar(&fast, "fast", false, "Local files only: skip needs_you (no monomind process)")
	return c
}
```

Confirm the `Autonomy` field for the pause deadline (`grep -n 'PausedUntil\|paused_until' internal/orgdecide/*.go`) and `EffectiveLevel`'s return type; adjust `row.Paused` / `row.Level` to the real names. `needsYou` shares `db` across goroutines — `*sql.DB` is safe for concurrent use; if `needsYou` touches non-thread-safe state (check `idleDeadline`), fall back to a sequential loop and note it.

Register in `org.go`'s `cmd.AddCommand(` list: `newOrgSummaryCmd(env),`.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/monoagentcli/ -run 'TestOrgSummary' -v -race`
Expected: PASS, no race reports.

- [ ] **Step 5: Real-profile smoke**

Run: `time ~/scratch/dash/monoagentcli org summary --fast && time ~/scratch/dash/monoagentcli org summary`
Expected: `--fast` well under 200 ms; full mode bounded by ~10 s even with a stopped monomind. Note both timings in the PR.

- [ ] **Step 6: Commit** (AGENTS.md: add one line under the org section: "`org summary [--fast]` — one row per org with running, level, queued and needs-you counts.")

```bash
git commit -F - -- cmd/monoagentcli/org_summary.go cmd/monoagentcli/org_summary_test.go cmd/monoagentcli/org.go AGENTS.md <<'MSG'
feat(org): org summary, a cross-org roll-up with a local-only --fast mode
MSG
```

---

### Task 6: `workflow executions --all`

**Files:**
- Modify: `cmd/monoagentcli/workflow.go:1073-1139`
- Test: `cmd/monoagentcli/workflow_executions_all_test.go`

**Interfaces:**
- Produces: `monoagentcli --json workflow executions --all [--limit 30]` → `[{id, workflow_id, workflow_name, status, trigger_type, started_at, finished_at, error, created_at}]` — exactly the field set of `WorkflowExecutionSummary` (`wails-app/app_workflows.go:74`) so the GUI binding can unmarshal it unchanged. `--all` with a workflow id → exit 2.

- [ ] **Step 1: Write the failing test**

```go
// cmd/monoagentcli/workflow_executions_all_test.go
package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func TestWorkflowExecutionsAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "e.db")
	db, err := storage.NewDatabase(dbPath)
	must(t, err)
	must(t, db.ApplyMigrations())
	_, err = db.DB.Exec(`INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w1','A',1,'default'),('w2','B',1,'default');
		INSERT INTO workflow_executions (id, workflow_id, status, profile_id, created_at) VALUES
		('e1','w1','SUCCESS','default','2026-09-26 10:00:00'),('e2','w2','FAILED','default','2026-09-26 11:00:00'),
		('e3','w1','SUCCESS','other','2026-09-26 12:00:00')`)
	must(t, err)
	db.Close()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}
	out := captureStdout(t, func() {
		c := newWorkflowCmd(cfg)
		c.SetArgs([]string{"executions", "--all", "--limit", "10"})
		must(t, c.Execute())
	})
	var rows []struct {
		ID           string `json:"id"`
		WorkflowName string `json:"workflow_name"`
	}
	must(t, json.Unmarshal([]byte(out), &rows))
	if len(rows) != 2 || rows[0].ID != "e2" || rows[0].WorkflowName != "B" {
		t.Fatalf("rows = %+v\n%s", rows, out)
	}
}
```

(Confirm the constructor name for the `workflow` command group — `grep -n 'func newWorkflowCmd' cmd/monoagentcli/workflow.go`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/monoagentcli/ -run TestWorkflowExecutionsAll -v`
Expected: FAIL — `unknown flag: --all`.

- [ ] **Step 3: Implement**

In `newWorkflowExecutionsCmd` change `Args: cobra.ExactArgs(1)` to `cobra.MaximumNArgs(1)` and add `var all bool` + `cmd.Flags().BoolVar(&all, "all", false, "List recent runs across every workflow in the profile")`.

`summary.Build` caps its recent list at 15, so add an exported helper to `internal/summary/workflows.go` that takes a limit:

```go
// RecentExecutions is the profile-wide run list behind `workflow executions --all`.
func RecentExecutions(ctx context.Context, db *sql.DB, profileID string, limit int) ([]ExecRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.id, e.workflow_id, COALESCE(w.name,''), e.status,
		COALESCE(e.trigger_type,''), COALESCE(e.started_at,''), COALESCE(e.finished_at,''),
		COALESCE(e.created_at,''), COALESCE(e.error,'')
		FROM workflow_executions e LEFT JOIN workflows w ON w.id = e.workflow_id
		WHERE e.profile_id = ? ORDER BY `+sinceExpr("e.created_at")+` DESC LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExecRow{}
	for rows.Next() {
		var r ExecRow
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.WorkflowName, &r.Status, &r.TriggerType,
			&r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

and make `executionsSection` call `RecentExecutions(ctx, o.DB, o.ProfileID, recentLimit)` for its `Recent` list (delete the duplicated query there). Then put this at the top of the command's `RunE`:

```go
			if all == (len(args) == 1) {
				return errInvalidInput("pass either a workflow id or --all")
			}
			if all {
				db, err := initDB(cfg)
				if err != nil {
					return fmt.Errorf("open database: %w", err)
				}
				defer db.Close()
				rows, err := summary.RecentExecutions(cmd.Context(), db.DB, cfg.ProfileID, limit)
				if err != nil {
					return err
				}
				if cfg.JSONOutput || jsonOut {
					return json.NewEncoder(os.Stdout).Encode(rows)
				}
				for _, r := range rows {
					fmt.Printf("%s\t%s\t%s\t%s\n", r.CreatedAt, r.Status, r.WorkflowName, r.ID)
				}
				return nil
			}
```

(`limit` and `jsonOut` are the existing flag variables of that command — use whatever names it already has.)

- [ ] **Step 4: Run tests + perf gate (D9)**

Run: `go test ./cmd/monoagentcli/ -run 'TestWorkflowExecutionsAll|TestSummary' -v && go test ./internal/summary/ && for i in 1 2 3 4 5; do /usr/bin/time -f %e ~/scratch/dash/monoagentcli --json workflow executions --all --limit 30 >/dev/null; done`
Expected: PASS; timings recorded. If p95 > 0.15 s, set `FAST_POLL_MS = 5000` in Task 9's hook and note why.

- [ ] **Step 5: Commit**

```bash
git commit -F - -- cmd/monoagentcli/workflow.go cmd/monoagentcli/workflow_executions_all_test.go internal/summary/workflows.go <<'MSG'
feat(workflow): executions --all lists recent runs across the profile
MSG
```

---

## Phase B — Wails bindings

### Task 7: summary bindings; `GetDashboardStats` and `GetRecentExecutions` shell out

**Files:**
- Create: `wails-app/app_summary.go`, `wails-app/app_summary_test.go`
- Modify: `wails-app/app.go:434-512` (delete old `DashboardStats`, `SessionSummary`, `GetDashboardStats`), `wails-app/app_workflows.go:710-…` (delete SQL `GetRecentExecutions`)
- Regenerate: `wails-app/frontend/src/wailsjs/go/main/App.{js,d.ts}`, `models.ts` (via `wails generate module` or a `wails dev` start)

**Interfaces:**
- Consumes: `findMonoAgentCLI`, `hideWindow`, `cliResultJSON` (`app_org_unification.go:504`), `runMonoCLI` (`app_applications.go:23`), `a.getActiveProfileID()`.
- Produces (bound to JS):
  - `GetSummary() string` — raw `summary --json` or `{"error":…}`
  - `GetOrgSummary(fast bool) string` — raw `org summary [--fast]` JSON
  - `GetDashboardStats() DashboardStats` — same JSON keys as before minus `recent_executions`
  - `GetRecentExecutions(limit int) ([]WorkflowExecutionSummary, error)` — same signature as before

- [ ] **Step 1: Write the failing test**

```go
// wails-app/app_summary_test.go
package main

import (
	"context"
	"strings"
	"testing"
)

func TestSummaryBindingsShellOut(t *testing.T) {
	log := fakeCLI(t, `case "$*" in
  *"summary --section"*) echo '{"v":1,"workflows":{"total":3,"active":2},"people":{"total":9,"lists":1},"accounts":{"sessions":[{"platform":"linkedin","username":"me","expiry":"2026-10-01T00:00:00Z","status":"active"}],"active":1},"executions":{"running":1}}' ;;
  *"org summary --fast"*) echo '{"v":1,"fast":true,"orgs":[]}' ;;
  *" summary"*) echo '{"v":1}' ;;
  *"executions --all"*) echo '[{"id":"e1","workflow_id":"w1","workflow_name":"A","status":"SUCCESS"}]' ;;
esac`)
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")

	if got := a.GetSummary(); got != `{"v":1}` {
		t.Fatalf("GetSummary = %q", got)
	}
	if got := a.GetOrgSummary(true); !strings.Contains(got, `"fast":true`) {
		t.Fatalf("GetOrgSummary = %q", got)
	}
	st := a.GetDashboardStats()
	if st.TotalWorkflows != 3 || st.TotalPeople != 9 || st.ActiveSessions != 1 || len(st.Sessions) != 1 ||
		!st.Sessions[0].Active || st.ExecutionsByStatus["RUNNING"] != 1 || st.TotalLists != 1 {
		t.Fatalf("GetDashboardStats = %+v", st)
	}
	rows, err := a.GetRecentExecutions(30)
	if err != nil || len(rows) != 1 || rows[0].WorkflowName != "A" {
		t.Fatalf("GetRecentExecutions = %+v, %v", rows, err)
	}
	want := []string{
		"--profile work --json summary",
		"--profile work --json org summary --fast",
		"--profile work --json summary --section workflows,executions,people,accounts",
		"--profile work --json workflow executions --all --limit 30",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
```

`fakeCLI(t, body)` is in `app_health_stream_test.go:16`; check whether it logs argv and returns the log path like `fakeMonoCLI`. If not, add a small `fakeCLIWithLog(t, caseBody) (argsLog string)` next to `fakeMonoCLI` in `app_hil_test.go` that writes `echo "$*" >> log` then the case body.

- [ ] **Step 2: Run to verify it fails**

Run: `cd wails-app && go test -tags webkit2_41 -run TestSummaryBindingsShellOut -v .`
Expected: FAIL — `a.GetSummary undefined`.

- [ ] **Step 3: Implement `app_summary.go`**

```go
// wails-app/app_summary.go
//
// Dashboard data. Everything here shells out to `monoagentcli … --json`
// (the CLI owns the facts; see app_orgs.go's header). No SQL.
package main

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

const summaryCLITimeout = 20 * time.Second

func (a *App) rawCLI(timeout time.Duration, args ...string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	defer cancel()
	full := append([]string{"--profile", a.getActiveProfileID(), "--json"}, args...)
	cmd := exec.CommandContext(ctx, cliBin, full...)
	hideWindow(cmd)
	out, err := cmd.Output()
	return cliResultJSON(cliBin, out, err)
}

// GetSummary returns `monoagentcli summary --json` verbatim.
func (a *App) GetSummary() string { return a.rawCLI(summaryCLITimeout, "summary") }

// GetOrgSummary returns `monoagentcli org summary [--fast]` verbatim.
func (a *App) GetOrgSummary(fast bool) string {
	args := []string{"org", "summary"}
	if fast {
		args = append(args, "--fast")
	}
	return a.rawCLI(60*time.Second, args...)
}

// DashboardStats is kept for the Sidebar and StatusBar (same JSON keys as
// before the dashboard refresh), now filled from `summary`.
type DashboardStats struct {
	ActiveSessions     int              `json:"active_sessions"`
	TotalWorkflows     int              `json:"total_workflows"`
	ExecutionsByStatus map[string]int   `json:"executions_by_status"`
	TotalPeople        int              `json:"total_people"`
	TotalLists         int              `json:"total_lists"`
	Sessions           []SessionSummary `json:"sessions"`
	DBPath             string           `json:"db_path"`
}

type SessionSummary struct {
	Platform string `json:"platform"`
	Username string `json:"username"`
	Expiry   string `json:"expiry"`
	Active   bool   `json:"active"`
}

func (a *App) GetDashboardStats() DashboardStats {
	stats := DashboardStats{ExecutionsByStatus: map[string]int{}, DBPath: a.dbPath}
	var s struct {
		Workflows struct {
			Total int `json:"total"`
		} `json:"workflows"`
		Executions struct {
			Running int `json:"running"`
			Queued  int `json:"queued"`
		} `json:"executions"`
		People struct {
			Total int `json:"total"`
			Lists int `json:"lists"`
		} `json:"people"`
		Accounts struct {
			Active   int `json:"active"`
			Sessions []struct {
				Platform, Username, Expiry, Status string
			} `json:"sessions"`
		} `json:"accounts"`
	}
	if err := a.runMonoCLI("", &s, "summary", "--section", "workflows,executions,people,accounts"); err != nil {
		a.logWarn("dashboard stats: " + err.Error())
		return stats
	}
	stats.TotalWorkflows = s.Workflows.Total
	stats.TotalPeople = s.People.Total
	stats.TotalLists = s.People.Lists
	stats.ActiveSessions = s.Accounts.Active
	stats.ExecutionsByStatus["RUNNING"] = s.Executions.Running
	stats.ExecutionsByStatus["QUEUED"] = s.Executions.Queued
	for _, r := range s.Accounts.Sessions {
		stats.Sessions = append(stats.Sessions, SessionSummary{Platform: r.Platform, Username: r.Username,
			Expiry: r.Expiry, Active: r.Status != "expired"})
	}
	return stats
}

// GetRecentExecutions lists recent runs across the profile via the CLI.
func (a *App) GetRecentExecutions(limit int) ([]WorkflowExecutionSummary, error) {
	rows := []WorkflowExecutionSummary{}
	err := a.runMonoCLI("", &rows, "workflow", "executions", "--all", "--limit", strconv.Itoa(limit))
	return rows, err
}
```

Anonymous-struct fields without tags (`Platform, Username, Expiry, Status string`) unmarshal case-insensitively from `platform`/`username`/… — that's standard `encoding/json` behavior. Use whatever logging helper `app.go` already has instead of `a.logWarn` (`grep -n 'func (a \*App) log' wails-app/app.go`).

Delete the old definitions from `app.go` and `app_workflows.go`. `executions_by_status` previously held every status; only `RUNNING` is read anywhere (`StatusBar.jsx:38`) — confirm with `grep -rn executions_by_status wails-app/frontend/src` before deleting.

- [ ] **Step 4: Run tests + build + regenerate bindings**

Run: `cd wails-app && go vet -tags webkit2_41 . && go test -tags webkit2_41 . && wails generate module`
Expected: PASS; `frontend/src/wailsjs/go/main/App.js` now exports `GetSummary` and `GetOrgSummary`. Keep the regenerated `wailsjs` files for these bindings, but revert unrelated churn (`git diff --stat wails-app/frontend`).

- [ ] **Step 5: Commit**

```bash
git add wails-app/app_summary.go wails-app/app_summary_test.go
git commit -F - -- wails-app/app_summary.go wails-app/app_summary_test.go wails-app/app.go wails-app/app_workflows.go wails-app/app_hil_test.go wails-app/frontend/src/wailsjs <<'MSG'
refactor(gui): dashboard stats and recent runs come from the CLI
MSG
```

---

## Phase C — Frontend

All render tests follow the house pattern (`src/pages/HumanInLoop.render.test.jsx`): `// @vitest-environment jsdom`, `import '@testing-library/jest-dom/vitest'`, `vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => o?.count != null ? `${k}:${o.count}` : k }) }))`, `cleanup()` in `afterEach`. Run with `cd wails-app/frontend && npx vitest run <path>`.

### Task 8: execution-status normalisation (fixes the grey-success bug)

**Files:**
- Create: `wails-app/frontend/src/lib/execStatus.js`, `wails-app/frontend/src/lib/execStatus.test.js`
- Modify: `wails-app/frontend/src/index.css` (add `@keyframes pulse` alias)

**Interfaces:**
- Produces: `export function execStatus(raw) → { key: 'success'|'partial'|'running'|'queued'|'waiting'|'failed'|'cancelled'|'unknown', tone: CSS color var, live: boolean }`; `export const LIVE_STATUSES` (Set of raw upper-case statuses that mean "in progress").

- [ ] **Step 1: Failing test**

```js
// wails-app/frontend/src/lib/execStatus.test.js
import { describe, it, expect } from 'vitest'
import { execStatus, LIVE_STATUSES } from './execStatus.js'

describe('execStatus', () => {
  it.each([
    ['SUCCESS', 'success', false], ['COMPLETED', 'success', false], ['success', 'success', false],
    ['SUCCESS_WITH_ERRORS', 'partial', false], ['RUNNING', 'running', true], ['QUEUED', 'queued', true],
    ['PENDING', 'queued', true], ['WAITING', 'waiting', true], ['FAILED', 'failed', false],
    ['CANCELLED', 'cancelled', false], ['', 'unknown', false], [undefined, 'unknown', false],
  ])('%s → %s', (raw, key, live) => {
    const s = execStatus(raw)
    expect(s.key).toBe(key)
    expect(s.live).toBe(live)
    expect(s.tone).toMatch(/^(var\(--|#)/)
  })
  it('LIVE_STATUSES matches live', () => {
    for (const s of LIVE_STATUSES) expect(execStatus(s).live).toBe(true)
  })
})
```

- [ ] **Step 2: Run** `npx vitest run src/lib/execStatus.test.js` → FAIL (module not found).

- [ ] **Step 3: Implement**

```js
// wails-app/frontend/src/lib/execStatus.js
// One mapping from the engine's execution statuses (internal/workflow
// models.go + SUCCESS_WITH_ERRORS/WAITING) to what the UI shows.
const MAP = {
  SUCCESS: 'success', COMPLETED: 'success', SUCCESS_WITH_ERRORS: 'partial',
  RUNNING: 'running', QUEUED: 'queued', PENDING: 'queued', WAITING: 'waiting',
  FAILED: 'failed', CANCELLED: 'cancelled',
}
const TONE = {
  success: 'var(--green-neon)', partial: '#fbbf24', running: 'var(--cyan)', queued: '#eab308',
  waiting: 'var(--purple-light)', failed: '#ef4444', cancelled: '#6b7280', unknown: 'var(--text-muted)',
}
const LIVE = new Set(['running', 'queued', 'waiting'])

export const LIVE_STATUSES = new Set(Object.keys(MAP).filter(k => LIVE.has(MAP[k])))

export function execStatus(raw) {
  const key = MAP[(raw || '').toUpperCase()] || 'unknown'
  return { key, tone: TONE[key], live: LIVE.has(key) }
}
```

In `index.css`, next to `@keyframes pulse-dot` (`:320`), add:

```css
@keyframes pulse { 0%, 100% { opacity: 1 } 50% { opacity: .45 } }
```

- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `fix(gui): map every execution status, including SUCCESS, to a colour` (paths: the two new files + `src/index.css`).

---

### Task 9: `useDashboardData` hook + formatters

**Files:**
- Create: `src/pages/dashboard/useDashboardData.js`, `src/pages/dashboard/useDashboardData.test.jsx`, `src/pages/dashboard/format.js`, `src/pages/dashboard/format.test.js`
- Modify: `src/services/api.js` (add `getSummary`, `getOrgSummary`)

**Interfaces:**
- Consumes: `GetSummary`, `GetOrgSummary` bindings; `api.listWorkflows`, `api.getRecentExecutions`; `subscribeEvent` (`services/api.js`); `usePageVisibleRef`, `useVisibleCatchUp` (`lib/usePageVisible.js`); `LIVE_STATUSES` (Task 8).
- Produces:
  - `api.getSummary() → Promise<Summary|null>`, `api.getOrgSummary(fast) → Promise<OrgSummary|null>` (parse JSON; `{error}` → `notify` once + `null`)
  - `useDashboardData() → { summary, orgs, workflows, executions, loading, refresh, setExecutions }`
  - `format.js`: `relTime(ts, t, now=Date.now())`, `duration(start, end, now=Date.now())`, `untilTime(ts, t, now=Date.now())`
  - Constants: `SUMMARY_POLL_MS = 15000`, `ORG_FAST_POLL_MS = 15000`, `ORG_FULL_POLL_MS = 60000`, `FAST_POLL_MS = 2000` (or 5000 per Task 6 gate), `BASE_POLL_MS = 5000`

- [ ] **Step 1: Failing tests**

```js
// src/pages/dashboard/format.test.js
import { describe, it, expect } from 'vitest'
import { relTime, duration, untilTime } from './format.js'

const t = (k, o) => (o ? `${k}:${JSON.stringify(o)}` : k)
const now = Date.parse('2026-09-26T12:00:00Z')

describe('format', () => {
  it('relTime', () => {
    expect(relTime(null, t, now)).toBe('—')
    expect(relTime('2026-09-26T11:59:30Z', t, now)).toBe('dashboard.time.justNow')
    expect(relTime('2026-09-26T11:50:00Z', t, now)).toBe('dashboard.time.minutesAgo:{"count":10}')
    expect(relTime('2026-09-26T09:00:00Z', t, now)).toBe('dashboard.time.hoursAgo:{"count":3}')
  })
  it('duration', () => {
    expect(duration('2026-09-26T11:59:48Z', '2026-09-26T12:00:00Z', now)).toBe('12s')
    expect(duration('2026-09-26T11:57:55Z', null, now)).toBe('2m 5s')
    expect(duration(null, null, now)).toBeNull()
  })
  it('untilTime', () => {
    expect(untilTime('2026-09-26T12:20:00Z', t, now)).toBe('dashboard.time.inMinutes:{"count":20}')
    expect(untilTime('2026-09-26T15:00:00Z', t, now)).toBe('dashboard.time.inHours:{"count":3}')
  })
})
```

```jsx
// src/pages/dashboard/useDashboardData.test.jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor, cleanup } from '@testing-library/react'

const handlers = {}
vi.mock('../../services/api.js', () => ({
  api: {
    getSummary: vi.fn(() => Promise.resolve({ v: 1, hil: { total: 2 } })),
    getOrgSummary: vi.fn(() => Promise.resolve({ v: 1, orgs: [] })),
    listWorkflows: vi.fn(() => Promise.resolve([{ id: 'w1' }])),
    getRecentExecutions: vi.fn(() => Promise.resolve([{ id: 'e1', status: 'SUCCESS' }])),
  },
  subscribeEvent: vi.fn((name, fn) => { handlers[name] = fn; return () => {} }),
}))
vi.mock('../../lib/usePageVisible.js', () => ({
  usePageVisibleRef: () => ({ current: true }), useVisibleCatchUp: () => {},
}))
import { api } from '../../services/api.js'
import { useDashboardData } from './useDashboardData.js'

afterEach(cleanup)

describe('useDashboardData', () => {
  it('loads everything once and refreshes summary on workflow:complete', async () => {
    const { result } = renderHook(() => useDashboardData())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.summary.hil.total).toBe(2)
    expect(result.current.workflows).toHaveLength(1)
    expect(api.getOrgSummary).toHaveBeenCalledWith(true)
    const before = api.getSummary.mock.calls.length
    handlers['workflow:complete']()
    await waitFor(() => expect(api.getSummary.mock.calls.length).toBe(before + 1))
  })
})
```

- [ ] **Step 2: Run** `npx vitest run src/pages/dashboard/` → FAIL (modules missing).

- [ ] **Step 3: Implement**

Add to `services/api.js` (next to `getDashboardStats`, line 46):

```js
  getSummary:           () => GoApp.GetSummary().then(parseCLIJSON('summary')).catch(guard('summary', null)),
  getOrgSummary:        (fast = true) => GoApp.GetOrgSummary(fast).then(parseCLIJSON('org summary')).catch(guard('org summary', null)),
```

and above the `api` object (reuse an existing JSON-parse helper if `api.js` has one — `grep -n 'JSON.parse' src/services/api.js`):

```js
// Bindings that return the CLI's stdout verbatim yield either the payload or
// {"error": "..."}; turn the latter into a rejected promise so guard() reports it.
const parseCLIJSON = (label) => (raw) => {
  const v = typeof raw === 'string' ? JSON.parse(raw) : raw
  if (v && typeof v === 'object' && !Array.isArray(v) && typeof v.error === 'string' && Object.keys(v).length === 1) {
    throw new Error(`${label}: ${v.error}`)
  }
  return v
}
```

`format.js`:

```js
// Time labels for the dashboard. `t` is react-i18next's t; `now` is injectable for tests.
export function relTime(ts, t, now = Date.now()) {
  if (!ts) return '—'
  const diff = now - new Date(ts)
  if (isNaN(diff)) return '—'
  if (diff < 60000) return t('dashboard.time.justNow')
  if (diff < 3600000) return t('dashboard.time.minutesAgo', { count: Math.floor(diff / 60000) })
  if (diff < 86400000) return t('dashboard.time.hoursAgo', { count: Math.floor(diff / 3600000) })
  return new Date(ts).toLocaleDateString()
}

export function duration(startedAt, finishedAt, now = Date.now()) {
  if (!startedAt) return null
  const ms = (finishedAt ? new Date(finishedAt) : now) - new Date(startedAt)
  if (isNaN(ms) || ms < 0) return null
  const sec = Math.round(ms / 1000)
  return sec < 60 ? `${sec}s` : `${Math.floor(sec / 60)}m ${sec % 60}s`
}

export function untilTime(ts, t, now = Date.now()) {
  const diff = new Date(ts) - now
  if (isNaN(diff)) return '—'
  if (diff < 60000) return t('dashboard.time.now')
  if (diff < 3600000) return t('dashboard.time.inMinutes', { count: Math.round(diff / 60000) })
  if (diff < 86400000) return t('dashboard.time.inHours', { count: Math.round(diff / 3600000) })
  return new Date(ts).toLocaleString()
}
```

`useDashboardData.js`:

```js
// Everything the dashboard shows, from the CLI via bindings. Polls only while
// the page is visible (plan D1/D12); events trigger an immediate refresh.
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, subscribeEvent } from '../../services/api.js'
import { usePageVisibleRef, useVisibleCatchUp } from '../../lib/usePageVisible.js'
import { LIVE_STATUSES } from '../../lib/execStatus.js'

export const SUMMARY_POLL_MS = 15000
export const ORG_FAST_POLL_MS = 15000
export const ORG_FULL_POLL_MS = 60000
export const FAST_POLL_MS = 2000 // set to 5000 if Task 6's perf gate failed
export const BASE_POLL_MS = 5000

const REFRESH_EVENTS = ['workflow:complete', 'workflow:exec-started', 'documents:changed', 'images:changed', 'org:runStatus']

export function useDashboardData() {
  const [summary, setSummary] = useState(null)
  const [orgs, setOrgs] = useState(null)
  const [workflows, setWorkflows] = useState([])
  const [executions, setExecutions] = useState([])
  const [loading, setLoading] = useState(true)
  const visible = usePageVisibleRef()
  const orgFull = useRef(false)

  const loadSummary = useCallback(async () => { const s = await api.getSummary(); if (s) setSummary(s) }, [])
  const loadOrgs = useCallback(async (fast = true) => {
    const o = await api.getOrgSummary(fast)
    // a fast reply must not wipe needs_you counts a full reply already filled in
    if (o) setOrgs(prev => (fast && prev && orgFull.current) ? mergeFast(prev, o) : o)
    if (o && !fast) orgFull.current = true
  }, [])
  const loadLists = useCallback(async () => {
    const [w, e] = await Promise.all([api.listWorkflows(), api.getRecentExecutions(30)])
    setWorkflows(w || [])
    setExecutions(e || [])
  }, [])
  const refresh = useCallback(async () => {
    await Promise.all([loadSummary(), loadLists(), loadOrgs(true)])
    setLoading(false)
  }, [loadSummary, loadLists, loadOrgs])

  useEffect(() => { refresh(); loadOrgs(false) }, [refresh, loadOrgs])
  useVisibleCatchUp(refresh)

  useEffect(() => {
    const offs = REFRESH_EVENTS.map(n => subscribeEvent(n, () => { loadSummary(); loadLists() }))
    return () => offs.forEach(off => off && off())
  }, [loadSummary, loadLists])

  useInterval(() => visible.current && loadSummary(), SUMMARY_POLL_MS)
  useInterval(() => visible.current && loadOrgs(true), ORG_FAST_POLL_MS)
  useInterval(() => visible.current && loadOrgs(false), ORG_FULL_POLL_MS)
  const live = executions.some(e => LIVE_STATUSES.has((e.status || '').toUpperCase()))
  useInterval(() => visible.current && loadLists(), live ? FAST_POLL_MS : BASE_POLL_MS)

  return { summary, orgs, workflows, executions, loading, refresh, setExecutions, reloadLists: loadLists }
}

function mergeFast(prev, fast) {
  const byName = Object.fromEntries((prev.orgs || []).map(o => [o.name, o]))
  return { ...fast, totals: { ...fast.totals, needs_you: prev.totals?.needs_you ?? 0 },
    orgs: (fast.orgs || []).map(o => ({ ...o, needs_you: byName[o.name]?.needs_you ?? null })) }
}

function useInterval(fn, ms) {
  const ref = useRef(fn)
  ref.current = fn
  useEffect(() => { const id = setInterval(() => ref.current(), ms); return () => clearInterval(id) }, [ms])
}
```

`subscribeEvent` must return an unsubscribe function — confirm in `services/api.js` and adapt the cleanup if it's named differently.

- [ ] **Step 4: Run** `npx vitest run src/pages/dashboard/ src/services/` → PASS.
- [ ] **Step 5: Commit** `feat(gui): dashboard data hook fed by summary and org summary` (paths: the four new files + `src/services/api.js`).

---

### Task 10: split the page — WorkflowsCard (with next run) and RecentRunsCard

**Files:**
- Create: `src/pages/dashboard/WorkflowsCard.jsx`, `src/pages/dashboard/RecentRunsCard.jsx`, `src/pages/dashboard/WorkflowsCard.render.test.jsx`
- Modify: `src/pages/Dashboard.jsx` (remove `ExecStatusDot`, `duration`, `relTime`, `WorkflowRow`, `ExecRow`; import the cards)

**Interfaces:**
- Consumes: `execStatus` (Task 8), `relTime`/`duration`/`untilTime` (Task 9), `summary.schedules` shape (Task 1).
- Produces: `<WorkflowsCard workflows executions schedules onRun onStop onToggle onNavigate />`, `<RecentRunsCard executions onNavigate />`, `<ExecStatusDot status />` (exported from `RecentRunsCard.jsx`).

- [ ] **Step 1: Failing render test**

```jsx
// src/pages/dashboard/WorkflowsCard.render.test.jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.count != null ? `${k}:${o.count}` : k) }) }))
import WorkflowsCard from './WorkflowsCard.jsx'

afterEach(cleanup)
const wfs = [{ id: 'w1', name: 'Scraper', is_active: true }, { id: 'w2', name: 'Digest', is_active: true }]
const execs = [{ id: 'e1', workflow_id: 'w1', status: 'SUCCESS', created_at: new Date().toISOString() }]

describe('WorkflowsCard', () => {
  it('shows next run for scheduled workflows and paused when the daemon is off', () => {
    const next = new Date(Date.now() + 20 * 60000).toISOString()
    const { rerender } = render(<WorkflowsCard workflows={wfs} executions={execs}
      schedules={{ daemon_running: true, upcoming: [{ workflow_id: 'w1', next_run: next }], invalid: [] }}
      onRun={vi.fn()} onStop={vi.fn()} onToggle={vi.fn()} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.time.inMinutes:20')).toBeInTheDocument()
    expect(screen.getByText('SUCCESS')).toHaveStyle({ color: 'var(--green-neon)' })
    rerender(<WorkflowsCard workflows={wfs} executions={execs}
      schedules={{ daemon_running: false, upcoming: [{ workflow_id: 'w1', next_run: next }], invalid: [] }}
      onRun={vi.fn()} onStop={vi.fn()} onToggle={vi.fn()} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.workflows.schedulePaused')).toBeInTheDocument()
  })
  it('flags invalid schedules', () => {
    render(<WorkflowsCard workflows={wfs} executions={[]}
      schedules={{ daemon_running: true, upcoming: [], invalid: [{ workflow_id: 'w2', node_id: 'n', error: 'bad' }] }}
      onRun={vi.fn()} onStop={vi.fn()} onToggle={vi.fn()} onNavigate={vi.fn()} />)
    expect(screen.getByTitle('bad')).toBeInTheDocument()
  })
  it('run button calls onRun', () => {
    const onRun = vi.fn(() => Promise.resolve())
    render(<WorkflowsCard workflows={[wfs[1]]} executions={[]} schedules={null}
      onRun={onRun} onStop={vi.fn()} onToggle={vi.fn()} onNavigate={vi.fn()} />)
    fireEvent.click(screen.getByText('dashboard.workflows.run'))
    expect(onRun).toHaveBeenCalledWith('w2')
  })
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** Move `WorkflowRow` from `Dashboard.jsx:68-204` into `WorkflowsCard.jsx` verbatim, then change:
  - status colour/`isRunning`: `const st = execStatus(last?.status)`; `isRunning = st.live`; colour `st.tone`.
  - strings: `'Deactivate'`/`'Activate'` → `t('dashboard.workflows.deactivate')`/`t('dashboard.workflows.activate')`; `'never run'` → `t('dashboard.workflows.neverRun')`; `'Stop'`, `'Run'`, `'Starting'`, `'Stop this execution'`, `'Open in editor'` → `dashboard.workflows.{stop,run,starting,stopTitle,openEditor}`.
  - add a schedule chip before the "Last run" block:

```jsx
function ScheduleChip({ sched, invalid, daemonRunning }) {
  const { t } = useTranslation()
  if (invalid) return <span className="dash-chip dash-chip-warn" title={invalid.error}><Clock size={10} /> {t('dashboard.workflows.scheduleInvalid')}</span>
  if (!sched) return null
  if (!daemonRunning) return <span className="dash-chip" title={t('dashboard.workflows.schedulePausedTitle')}><Clock size={10} /> {t('dashboard.workflows.schedulePaused')}</span>
  return <span className="dash-chip" title={new Date(sched.next_run).toLocaleString()}><Clock size={10} /> {untilTime(sched.next_run, t)}</span>
}
```

  The card wrapper (`section-header`, empty state, list) moves from `Dashboard.jsx:379-416`. Build `execMap` inside the card with `useMemo` (from `executions`), and `schedByWf`/`invalidByWf` maps from `schedules`.
  - `RecentRunsCard.jsx`: move `ExecRow` (`Dashboard.jsx:207-252`) and the card at `:403-417`; `ExecStatusDot` uses `execStatus(status)` → `{tone, live}`, `animation: live ? 'pulse 1.4s ease-in-out infinite' : 'none'`; header `t('dashboard.recentRuns.title')`, empty `t('dashboard.recentRuns.empty')`; `title` → `t('dashboard.recentRuns.openTitle')`.

- [ ] **Step 4: Run** `npx vitest run src/pages/dashboard/` → PASS.
- [ ] **Step 5: Commit** `refactor(gui): split dashboard workflow and run cards; show next scheduled run`.

---

### Task 11: AttentionStrip ("Needs you")

**Files:**
- Create: `src/pages/dashboard/AttentionStrip.jsx`, `src/pages/dashboard/attention.js`, `src/pages/dashboard/attention.test.js`, `src/pages/dashboard/AttentionStrip.render.test.jsx`

**Interfaces:**
- Consumes: `summary` (Tasks 1–3 shape), `orgs` (Task 5 shape), `health` = `summarize(getHealth().report)` from `lib/health.js` (`{level, issues}`).
- Produces: `attentionItems(summary, orgs, health) → [{id, count, severity:'danger'|'warn'|'info', labelKey, target:{page, data?}|{hil:true}}]` (pure, tested); `<AttentionStrip items onNavigate onOpenHil />`.

- [ ] **Step 1: Failing unit test**

```js
// src/pages/dashboard/attention.test.js
import { describe, it, expect } from 'vitest'
import { attentionItems } from './attention.js'

describe('attentionItems', () => {
  it('returns only non-zero items, most severe first', () => {
    const summary = {
      hil: { workflow_pending: 2, people_review: 3, drafts: 0, link_suggestions: 1 },
      executions: { last_24h: { failed: 1 } },
      automations: { selectors: { broken: 1 }, unavailable: 0, pending_update: 2 },
      accounts: { expired: 1, expiring_soon: 0 },
      schedules: { daemon_running: false, upcoming: [{}] },
      recordings: { unsaved: 0 },
      applications: { unevaluated_pending: 0 },
    }
    const orgs = { totals: { needs_you: 4 } }
    const items = attentionItems(summary, orgs, { level: 'warn', issues: 2 })
    const ids = items.map(i => i.id)
    expect(ids).toEqual(['daemonOffline', 'failedRuns', 'brokenSelectors', 'expiredLogins',
      'hilApprovals', 'orgNeedsYou', 'leadsToReview', 'health', 'linkSuggestions', 'automationUpdates'])
    expect(items.find(i => i.id === 'hilApprovals').target).toEqual({ hil: true })
    expect(items.find(i => i.id === 'brokenSelectors').target).toEqual({ page: 'connections', data: { tab: 'health' } })
    expect(items.find(i => i.id === 'health').target).toEqual({ page: 'settings', data: { section: 'health' } })
  })
  it('is empty when everything is fine or data is missing', () => {
    expect(attentionItems(null, null, null)).toEqual([])
    expect(attentionItems({ schedules: { daemon_running: false, upcoming: [] } }, null, { level: 'ok', issues: 0 })).toEqual([])
  })
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement `attention.js`**

```js
// What the "Needs you" strip shows: pure, derived only from CLI output
// (summary, org summary, the shared health report) — presentation, not facts.
const RANK = { danger: 0, warn: 1, info: 2 }

export function attentionItems(summary, orgs, health) {
  const s = summary || {}
  const items = []
  const add = (id, count, severity, target) => { if (count > 0) items.push({ id, count, severity, labelKey: `dashboard.attention.${id}`, target }) }

  // daemon off while something is scheduled: nothing will fire
  add('daemonOffline', s.schedules && !s.schedules.daemon_running ? (s.schedules.upcoming || []).length : 0, 'danger', { page: 'settings', data: { section: 'health' } })
  add('failedRuns', s.executions?.last_24h?.failed || 0, 'danger', { page: 'noderunner' })
  add('brokenSelectors', s.automations?.selectors?.broken || 0, 'danger', { page: 'connections', data: { tab: 'health' } })
  add('expiredLogins', s.accounts?.expired || 0, 'danger', { page: 'connections' })
  add('hilApprovals', s.hil?.workflow_pending || 0, 'warn', { hil: true })
  add('orgNeedsYou', orgs?.totals?.needs_you || 0, 'warn', { page: 'orgs' })
  add('leadsToReview', s.hil?.people_review || 0, 'warn', { hil: true })
  add('drafts', s.hil?.drafts || 0, 'warn', { hil: true })
  add('expiringLogins', s.accounts?.expiring_soon || 0, 'warn', { page: 'connections' })
  add('health', health && health.level !== 'ok' ? health.issues || 0 : 0, health?.level === 'fail' ? 'danger' : 'warn', { page: 'settings', data: { section: 'health' } })
  add('linkSuggestions', s.hil?.link_suggestions || 0, 'info', { page: 'people' })
  add('unsavedRecordings', s.recordings?.unsaved || 0, 'info', { page: 'connections', data: { tab: 'recordings' } })
  add('automationUpdates', s.automations?.pending_update || 0, 'info', { page: 'connections' })
  add('unevaluatedApplications', s.applications?.unevaluated_pending || 0, 'info', { page: 'applications' })

  return items.sort((a, b) => RANK[a.severity] - RANK[b.severity]) // stable: keeps declaration order within a rank
}
```

Note the test's expected order places `health` (warn) after `leadsToReview` and before the info items — matches declaration order within the `warn` rank. `Array.prototype.sort` is stable in all engines Wails ships.

`AttentionStrip.jsx`:

```jsx
import { useTranslation } from 'react-i18next'
import { AlertTriangle, CheckCircle } from 'lucide-react'

export default function AttentionStrip({ items, onNavigate, onOpenHil }) {
  const { t } = useTranslation()
  if (!items.length) {
    return <div className="attn-strip attn-clear"><CheckCircle size={13} /> {t('dashboard.attention.allClear')}</div>
  }
  const go = (target) => (target.hil ? onOpenHil?.() : onNavigate(target.page, target.data))
  return (
    <div className="attn-strip" role="region" aria-label={t('dashboard.attention.title')}>
      <span className="attn-title"><AlertTriangle size={12} /> {t('dashboard.attention.title')}</span>
      {items.map(it => (
        <button key={it.id} className={`attn-chip attn-${it.severity}`} onClick={() => go(it.target)}>
          <span className="attn-count">{it.count}</span> {t(it.labelKey, { count: it.count })}
        </button>
      ))}
    </div>
  )
}
```

Render test (`AttentionStrip.render.test.jsx`): render with two items; click the `{hil:true}` chip → `onOpenHil` called; click the `{page:'orgs'}` chip → `onNavigate('orgs', undefined)`; render with `[]` → `dashboard.attention.allClear` visible.

- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(gui): needs-you strip on the dashboard`.

---

### Task 12: StatRow + SystemCard

**Files:**
- Create: `src/pages/dashboard/StatRow.jsx`, `src/pages/dashboard/SystemCard.jsx`, `src/pages/dashboard/SystemCard.render.test.jsx`

**Interfaces:**
- Consumes: `summary.{workflows,executions,people,services,jev}`, `orgs.totals`, `lib/health.js` (`getHealth`, `subscribeHealth`, `summarize`, `runHealth`).
- Produces: `<StatRow summary orgs loading />` (5 cards: Workflows `total`/`active`; Running `running`/`queued`; Failed 24h `failed`/`last_24h.total`; Orgs `running`/`orgs`; People `total`/`added_7d`), each card clickable (`onNavigate`) — reuse `StatCard` from the old page, add an optional `sub` line and `onClick`. `<SystemCard summary orgs onNavigate />`.

- [ ] **Step 1: Failing render test**

```jsx
// src/pages/dashboard/SystemCard.render.test.jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o ? `${k}:${JSON.stringify(o)}` : k) }) }))
const runHealth = vi.fn()
vi.mock('../../lib/health.js', () => ({
  getHealth: () => ({ report: { summary: { warn: 2, fail: 0 } }, running: false }),
  subscribeHealth: () => () => {},
  summarize: () => ({ level: 'warn', issues: 2 }),
  runHealth: (...a) => runHealth(...a),
}))
import SystemCard from './SystemCard.jsx'

afterEach(cleanup)
const summary = {
  services: { daemon: { running: true, pid: 1 }, bridge: { status: 'waiting', connected: false }, org_serve: { running: true, orgs: ['acme', 'ops'] } },
  jev: { key_configured: true, calls_24h: 12, estimated_usd_24h: 0.0312, surfaces_enabled: 3 },
}

describe('SystemCard', () => {
  it('renders service states, health and Jev usage', () => {
    render(<SystemCard summary={summary} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.system.daemon')).toBeInTheDocument()
    expect(screen.getByText('dashboard.system.bridgeState.waiting')).toBeInTheDocument()
    expect(screen.getByText('dashboard.system.orgServeRunning:{"count":2}')).toBeInTheDocument()
    expect(screen.getByText('dashboard.system.healthIssues:{"count":2}')).toBeInTheDocument()
    expect(screen.getByText(/\$0\.03/)).toBeInTheDocument()
  })
  it('run check calls runHealth in background mode', () => {
    render(<SystemCard summary={summary} onNavigate={vi.fn()} />)
    fireEvent.click(screen.getByText('dashboard.system.runCheck'))
    expect(runHealth).toHaveBeenCalledWith({ background: true })
  })
  it('bridge null shows not running', () => {
    render(<SystemCard summary={{ ...summary, services: { ...summary.services, bridge: null } }} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.system.bridgeState.off')).toBeInTheDocument()
  })
})
```

Check `runHealth`'s option names in `lib/health.js:218` and `healthMode` (`:51`) and use the real ones.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement `SystemCard.jsx`**

```jsx
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, ChevronRight, Activity } from 'lucide-react'
import { getHealth, subscribeHealth, summarize, runHealth } from '../../lib/health.js'

function Row({ ok, warn, label, value, onClick }) {
  const cls = ok ? 'connected' : warn ? 'pulse' : 'disconnected'
  return (
    <div className="dash-row" onClick={onClick} role={onClick ? 'button' : undefined} tabIndex={onClick ? 0 : undefined}
      onKeyDown={e => { if (onClick && (e.key === 'Enter' || e.key === ' ')) onClick() }}>
      <span className={`status-dot ${cls}`} />
      <span className="dash-row-label">{label}</span>
      <span className="dash-row-value">{value}</span>
    </div>
  )
}

export default function SystemCard({ summary, onNavigate }) {
  const { t } = useTranslation()
  const [health, setHealth] = useState(getHealth())
  useEffect(() => subscribeHealth(setHealth), [])
  const sv = summary?.services
  const jev = summary?.jev
  const h = summarize(health?.report)
  const bridge = sv?.bridge ? sv.bridge.status : 'off'
  const toHealth = () => onNavigate('settings', { section: 'health' })

  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Server size={12} /> {t('dashboard.system.title')}</div>
        <button className="btn btn-ghost btn-sm" onClick={toHealth} style={{ fontSize: 11, gap: 3 }}>
          {t('dashboard.system.details')} <ChevronRight size={11} />
        </button>
      </div>
      <Row ok={sv?.daemon?.running} label={t('dashboard.system.daemon')}
        value={sv?.daemon?.running ? t('dashboard.system.running') : t('dashboard.system.stopped')} onClick={toHealth} />
      <Row ok={bridge === 'connected'} warn={bridge === 'waiting' || bridge === 'unpaired'} label={t('dashboard.system.bridge')}
        value={t(`dashboard.system.bridgeState.${bridge}`)} onClick={toHealth} />
      <Row ok={sv?.org_serve?.running} label={t('dashboard.system.orgServe')}
        value={sv?.org_serve?.running ? t('dashboard.system.orgServeRunning', { count: sv.org_serve.orgs.length }) : t('dashboard.system.stopped')}
        onClick={() => onNavigate('orgs')} />
      <Row ok={h.level === 'ok'} warn={h.level === 'warn'} label={t('dashboard.system.health')}
        value={h.level === 'ok' ? t('dashboard.system.healthOk') : t('dashboard.system.healthIssues', { count: h.issues })} onClick={toHealth} />
      <Row ok={jev?.key_configured} warn={!jev?.key_configured} label={t('dashboard.system.jev')}
        value={jev?.key_configured
          ? t('dashboard.system.jevUsage', { calls: jev.calls_24h, usd: `$${(jev.estimated_usd_24h || 0).toFixed(2)}` })
          : t('dashboard.system.jevNoKey')}
        onClick={() => onNavigate('settings', { section: 'jev' })} />
      <button className="btn btn-ghost btn-sm" style={{ marginTop: 8, gap: 5 }} disabled={health?.running}
        onClick={() => runHealth({ background: true })}>
        <Activity size={11} /> {t('dashboard.system.runCheck')}
      </button>
    </div>
  )
}
```

The `$0.03` assertion depends on the `jevUsage` interpolation reaching the rendered text; with the test's `t` mock it renders `…{"calls":12,"usd":"$0.03"}` so `/\$0\.03/` matches.

`StatRow.jsx`: move `StatCard` from `Dashboard.jsx:55-66`, add `sub` + `onClick` props (render `<div className="stat-sub">{sub}</div>`; when `onClick` set, render as `<button className="stat-card stat-card-btn">`). Compose the five cards per Interfaces above, with labels `dashboard.stat.{workflows,running,failed24h,orgs,people}` and subs `dashboard.stat.{activeCount,queuedCount,ofRuns,pausedOrStopped,addedWeek}`.

- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(gui): dashboard stat row and system card`.

---

### Task 13: OrgsCard

**Files:** Create `src/pages/dashboard/OrgsCard.jsx`, `src/pages/dashboard/OrgsCard.render.test.jsx`

**Interfaces:**
- Consumes: `orgs` (Task 5). Uses `components/orgs/ui.jsx` `Chip`/`TierChip` if they fit; otherwise `.dash-chip`.
- Produces: `<OrgsCard orgs onNavigate />` — one row per org: status dot (running), name, level chip (`org.level`, plus "paused" chip), `needs_you` count (or `…` while null, with `needs_you_error` as title), queued count; row click → `onNavigate('orgs', { org: name })`. Empty (`orgs.orgs.length === 0`) → empty state with "Create an org" button → `onNavigate('orgs')`. Max 6 rows, "+N more" link.

- [ ] **Step 1: Failing test** — render with `{orgs:[{name:'acme',running:true,level:'assisted',paused:false,queued:1,needs_you:2},{name:'ops',running:false,level:'manual',paused:true,queued:0,needs_you:null,needs_you_error:'monomind down'}],totals:{orgs:2}}`; assert `acme` row shows `2` and `1`; `ops` shows `dashboard.orgs.paused` and an element with `title="monomind down"`; clicking `acme` calls `onNavigate('orgs', { org: 'acme' })`. Render with `{orgs:[]}` → `dashboard.orgs.empty` present.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** (≤ 120 lines; same `card`/`section-header` scaffold as SystemCard; level label via `t('dashboard.orgs.level.' + level)` falling back to the raw value — check the level enum in `internal/orgdesign` for the exact values and add a key per value).
- [ ] **Step 4: Run** → PASS. Also make `pages/Orgs.jsx` honour `navData?.org` by selecting that org on mount if it doesn't already (check how OrgsPanel selects an org; if it has a `selected` state, seed it). Add a render test for that seeding in the existing `OrgsPanel.render.test.jsx` style.
- [ ] **Step 5: Commit** `feat(gui): orgs card on the dashboard`.

---

### Task 14: AutomationsCard + AccountsCard

**Files:** Create `src/pages/dashboard/AutomationsCard.jsx`, `src/pages/dashboard/AccountsCard.jsx`, `src/pages/dashboard/AutomationsCard.render.test.jsx`, `src/pages/dashboard/AccountsCard.render.test.jsx`

**Interfaces:**
- Consumes: `summary.automations`, `summary.recordings`, `summary.accounts`; `PLATFORM_COLORS` (`services/api.js:376`).
- Produces:
  - `<AutomationsCard summary onNavigate />`: line 1 `installed · enabled`; selector bar (ok/decaying/broken/stale as a 4-segment stacked bar with counts, colours `--green-neon`/`#fbbf24`/`#ef4444`/`--text-dim`); up to 3 broken selectors listed as `automation_id › selector_key`, each → `onNavigate('connections', { automationId, tab: 'health' })`; badges for `unavailable`, `pending_update`, `scripts_blocked` (each hidden at 0); recordings line `total · unsaved · incomplete` → `onNavigate('connections', { tab: 'recordings' })`.
  - `<AccountsCard summary onNavigate />`: replaces "Connected Accounts" (`Dashboard.jsx:425-472`) — rows from `accounts.sessions` with status dot (`active` green, `expiring` amber with `dashboard.accounts.expiresIn` + `untilTime`, `expired` red), platform badge (existing style), "Manage" → `onNavigate('connections')`.

- [ ] **Step 1: Failing tests** — AutomationsCard: `selectors {ok:10,decaying:2,broken:1,stale:0}`, `broken:[{automation_id:'linkedin',selector_key:'post.send'}]`, `pending_update:1` → assert `linkedin › post.send` visible and clicking it calls `onNavigate('connections', { automationId: 'linkedin', tab: 'health' })`; `dashboard.automations.pendingUpdate:1` visible; `scripts_blocked` badge absent. AccountsCard: sessions `[{platform:'x',username:'me',status:'expiring',expiry:<+20h>},{platform:'tiktok',username:'me',status:'expired'}]` → `dashboard.accounts.expiresIn` text and `dashboard.accounts.expired` present.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** (each file ≤ 150 lines).
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(gui): automations and accounts cards on the dashboard`.

---

### Task 15: ActivityCard

**Files:** Create `src/pages/dashboard/ActivityCard.jsx`, `src/pages/dashboard/ActivityCard.render.test.jsx`

**Interfaces:**
- Consumes: `summary.activity`, `summary.applications`, `summary.people`, `summary.vault`.
- Produces: `<ActivityCard summary onNavigate />` — a 2×3 grid of mini-metrics, each a button:
  - Captures 7d (`captures_7d` / `captures_total`) → `documents`
  - Documents (`documents.total`, sub: `summarising` in progress / `summary_errors` + `index_errors` errors) → `documents`
  - Messages in 7d / out 7d → `communications`
  - Applications pending / applied → `applications`
  - Unevaluated pending applications → `applications`
  - People added 7d / lists → `people`
  - Vault: `vault.secrets` secrets · `vault.images` images → `secretsVault` (counts only — the dashboard never shows a secret's name, username, URL or value; D13)

- [ ] **Step 1: Failing test** — render with a full activity/applications/people/vault fixture; assert seven buttons (`getAllByRole('button')` length 7), clicking the vault tile calls `onNavigate('secretsVault', undefined)`, the documents tile shows the error sub-label when `summary_errors + index_errors > 0`, and clicking the messages tile calls `onNavigate('communications', undefined)`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** (≤ 130 lines, CSS class `.dash-tiles` with `grid-template-columns: repeat(3, minmax(0,1fr))`, 2 columns under 760 px).
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(gui): activity card on the dashboard`.

---

### Task 16: compose the page; deep-link plumbing

**Files:**
- Modify: `src/pages/Dashboard.jsx` (rewrite as layout), `src/App.jsx:251,255,269,311-317`, `src/pages/Settings.jsx`, `src/pages/Connections.jsx`, `src/pages/connections/AutomationDrawer.jsx` (already has `initialTab`)
- Test: `src/pages/Dashboard.render.test.jsx`, `src/pages/Settings.navdata.render.test.jsx`, `src/pages/Connections.navdata.render.test.jsx`

**Interfaces:**
- Consumes: every card (Tasks 10–15), `useDashboardData` (Task 9), `attentionItems` (Task 11), `summarize/getHealth/subscribeHealth`.
- Produces: `<Dashboard onRefresh onNavigate onOpenHil />` (the `stats` prop is dropped — Dashboard no longer reads it); `Settings` accepts `navData={{section}}` and scrolls to / expands that section; `Connections` accepts `navData={{automationId?, tab?}}` and opens `AutomationDrawer` for that automation with `initialTab={tab}` (tab without id → switch to the Browser automations list and, for `recordings`, open the first automation's drawer only if exactly one has recordings; otherwise just the list).

- [ ] **Step 1: Failing tests**

`Dashboard.render.test.jsx`: mock `./dashboard/useDashboardData.js` to return a fixture (summary with `hil.workflow_pending: 2`, orgs, workflows, executions, `loading:false`), mock `../lib/health.js` as in Task 12, mock `react-i18next`. Assert: `dashboard.attention.hilApprovals` chip present; clicking it calls `onOpenHil`; the five stat labels present; `dashboard.workflowsSection.title`, `dashboard.recentRuns.title`, `dashboard.system.title`, `dashboard.orgs.title`, `dashboard.automations.title`, `dashboard.accounts.title`, `dashboard.activity.title` present. Assert the page does **not** import `GetDashboardStats` (`expect(Object.keys(await import('../wailsjs/go/main/App'))).…` is overkill — instead grep in Step 4).

`Settings.navdata.render.test.jsx`: render `<Settings navData={{ section: 'health' }} … />` with the heavy children mocked; assert the `HealthSection` gets `defaultOpen` (or equivalent prop — read `Settings.jsx:354` for how it's collapsed) and `scrollIntoView` was called on its container (`Element.prototype.scrollIntoView = vi.fn()`).

`Connections.navdata.render.test.jsx`: mock `listAutomations` to return `[{id:'linkedin', …}]`; render `<Connections navData={{ automationId: 'linkedin', tab: 'health' }} />`; assert `AutomationDrawer` rendered with `initialTab="health"` (mock `./connections/AutomationDrawer.jsx` to render its props as text).

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement**

`Dashboard.jsx`:

```jsx
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw, GitBranch } from 'lucide-react'
import { api } from '../services/api.js'
import { GetVersion } from '../wailsjs/go/main/App'
import { getHealth, subscribeHealth, summarize } from '../lib/health.js'
import { useDashboardData } from './dashboard/useDashboardData.js'
import { attentionItems } from './dashboard/attention.js'
import AttentionStrip from './dashboard/AttentionStrip.jsx'
import StatRow from './dashboard/StatRow.jsx'
import WorkflowsCard from './dashboard/WorkflowsCard.jsx'
import RecentRunsCard from './dashboard/RecentRunsCard.jsx'
import ActivityCard from './dashboard/ActivityCard.jsx'
import SystemCard from './dashboard/SystemCard.jsx'
import OrgsCard from './dashboard/OrgsCard.jsx'
import AutomationsCard from './dashboard/AutomationsCard.jsx'
import AccountsCard from './dashboard/AccountsCard.jsx'

export default function Dashboard({ onRefresh, onNavigate, onOpenHil }) {
  const { t } = useTranslation()
  const { summary, orgs, workflows, executions, loading, refresh, setExecutions, reloadLists } = useDashboardData()
  const [ver, setVer] = useState(null)
  const [refreshing, setRefreshing] = useState(false)
  const [health, setHealth] = useState(getHealth())
  useEffect(() => { GetVersion().then(setVer).catch(() => {}) }, [])
  useEffect(() => subscribeHealth(setHealth), [])
  const items = useMemo(() => attentionItems(summary, orgs, summarize(health?.report)), [summary, orgs, health])

  const handleRefresh = async () => {
    setRefreshing(true)
    await Promise.all([onRefresh?.(), refresh()])
    setTimeout(() => setRefreshing(false), 400)
  }
  const handleRun = async (id) => {
    setExecutions(prev => prev.map(e => (e.workflow_id === id && e.status !== 'RUNNING' ? { ...e, status: 'RUNNING' } : e)))
    await api.runWorkflow(id)
    setTimeout(reloadLists, 1500)
  }
  const handleStop = async (executionId) => {
    setExecutions(prev => prev.map(e => (e.id === executionId ? { ...e, status: 'CANCELLED' } : e)))
    await api.cancelWorkflow(executionId)
    setTimeout(reloadLists, 500)
  }
  const handleToggle = async (id, active) => { await api.setWorkflowActive(id, active); await reloadLists() }

  return (
    <>
      <div className="page-header">
        <div className="page-header-left">
          <div className="page-title">{t('dashboard.title')}</div>
          <div className="page-subtitle">{t('dashboard.subtitle')}{ver ? ` · v${ver.version.replace(/^v/, '')}` : ''}</div>
        </div>
        <div className="page-header-right">
          <button className="btn btn-ghost btn-sm" onClick={handleRefresh} style={{ gap: 5 }}>
            <RefreshCw size={13} style={{ animation: refreshing ? 'spin 0.7s linear infinite' : 'none' }} /> {t('dashboard.refresh')}
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => onNavigate('noderunner')} style={{ gap: 5 }}>
            <GitBranch size={13} /> {t('dashboard.workflowEditor')}
          </button>
        </div>
      </div>
      <div className="page-body">
        <AttentionStrip items={items} onNavigate={onNavigate} onOpenHil={onOpenHil} />
        <StatRow summary={summary} orgs={orgs} loading={loading} onNavigate={onNavigate} />
        <div className="dashboard-grid">
          <div className="dash-col">
            <WorkflowsCard workflows={workflows} executions={executions} schedules={summary?.schedules}
              onRun={handleRun} onStop={handleStop} onToggle={handleToggle} onNavigate={onNavigate} />
            <RecentRunsCard executions={executions} onNavigate={onNavigate} />
            <ActivityCard summary={summary} onNavigate={onNavigate} />
          </div>
          <div className="dash-col">
            <SystemCard summary={summary} onNavigate={onNavigate} />
            <OrgsCard orgs={orgs} onNavigate={onNavigate} />
            <AutomationsCard summary={summary} onNavigate={onNavigate} />
            <AccountsCard summary={summary} onNavigate={onNavigate} />
          </div>
        </div>
      </div>
    </>
  )
}
```

`App.jsx`: Dashboard mount (`:251`) → `<Dashboard onRefresh={refreshStats} onNavigate={navigate} onOpenHil={() => setHilOpen(true)} />` (use the actual HIL-open state setter name from `:311-317`). Pass `navData={activePage === 'settings' ? navData : null}` to Settings and the same for Connections; clear `navData` after consumption the same way NodeRunner does (read how `:269` handles it). Also: `navigate('orgs', {org})` → pass navData to Orgs (Task 13).

`Settings.jsx`: accept `navData`; `useEffect(() => { if (navData?.section) { setOpen(navData.section); refs[navData.section]?.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }) } }, [navData])` — wire `refs.health` to the HealthSection wrapper (`:354`) and `refs.jev` to JevSection. Use the file's actual open/collapse state.

`Connections.jsx`: accept `navData`; on change, switch to the browser-automations tab, then if `automationId` is set, open the drawer for it with `initialTab={navData.tab || 'overview'}`. The drawer already takes `initialTab` (`connections/AutomationDrawer.jsx:15`).

- [ ] **Step 4: Run + static checks**

Run: `cd wails-app/frontend && npx vitest run && ! grep -n "GetDashboardStats\|stats\." src/pages/Dashboard.jsx && wc -l src/pages/Dashboard.jsx src/pages/dashboard/*.jsx`
Expected: all tests PASS; grep finds nothing; every file < 500 lines (Dashboard.jsx ≈ 90).

- [ ] **Step 5: Commit** `feat(gui): dashboard composed from focused cards; deep links into settings, connections and orgs`.

---

### Task 17: i18n (en + es), parity test, responsive CSS

**Files:**
- Modify: `src/locales/en.json:39-56`, `src/locales/es.json:39-…`, `src/index.css:537,1500`
- Create: `src/locales/dashboardKeys.test.js`

**Interfaces:**
- Consumes: every `t('dashboard.…')` key used in Tasks 10–16.
- Produces: complete `dashboard.*` trees in both locales.

- [ ] **Step 1: Failing parity test**

```js
// src/locales/dashboardKeys.test.js
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
// i18next plural keys: "x_one"/"x_other" satisfy a t('x', {count}) call
const has = (keys, k) => keys.includes(k) || keys.includes(`${k}_one`) || keys.includes(`${k}_other`)

const dir = join(__dirname, '..', 'pages')
const sources = [join(dir, 'Dashboard.jsx'), ...readdirSync(join(dir, 'dashboard')).filter(f => /\.(jsx|js)$/.test(f) && !f.includes('.test.')).map(f => join(dir, 'dashboard', f))]
const used = new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](dashboard\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))

describe('dashboard i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  it('every dashboard key used in code exists in en and es', () => {
    const missing = [...used].filter(k => !k.endsWith('.') && (!has(enKeys, k) || !has(esKeys, k)))
    expect(missing).toEqual([])
  })
  it('en and es have the same dashboard keys', () => {
    const e = enKeys.filter(k => k.startsWith('dashboard.')).sort()
    const s = esKeys.filter(k => k.startsWith('dashboard.')).sort()
    expect(s).toEqual(e)
  })
})
```

Dynamic keys (`dashboard.system.bridgeState.${bridge}`, `dashboard.orgs.level.${level}`, `dashboard.attention.${id}`) are not matched by the regex; add them explicitly as a list at the top of the test (`const dynamic = ['dashboard.system.bridgeState.connected', …]`) and fold it into `used`.

- [ ] **Step 2: Run** → FAIL listing the missing keys.

- [ ] **Step 3: Add the keys.** English tree (Spanish mirrors it; get the Spanish wording reviewed by the user or reuse existing `es.json` terminology, e.g. `sidebar.nav.*`):

```json
"dashboard": {
  "title": "Dashboard",
  "subtitle": "Everything at a glance",
  "refresh": "Refresh",
  "workflowEditor": "Workflow Editor",
  "time": { "justNow": "just now", "minutesAgo": "{{count}}m ago", "hoursAgo": "{{count}}h ago",
            "now": "now", "inMinutes": "in {{count}}m", "inHours": "in {{count}}h" },
  "attention": {
    "title": "Needs you", "allClear": "All clear — nothing is waiting for you",
    "daemonOffline_one": "scheduled workflow won't run (daemon off)", "daemonOffline_other": "scheduled workflows won't run (daemon off)",
    "failedRuns_one": "failed run (24h)", "failedRuns_other": "failed runs (24h)",
    "brokenSelectors_one": "broken selector", "brokenSelectors_other": "broken selectors",
    "expiredLogins_one": "login expired", "expiredLogins_other": "logins expired",
    "hilApprovals_one": "approval", "hilApprovals_other": "approvals",
    "orgNeedsYou_one": "org item needs you", "orgNeedsYou_other": "org items need you",
    "leadsToReview_one": "lead to review", "leadsToReview_other": "leads to review",
    "drafts_one": "draft to approve", "drafts_other": "drafts to approve",
    "expiringLogins_one": "login expiring soon", "expiringLogins_other": "logins expiring soon",
    "health_one": "health issue", "health_other": "health issues",
    "linkSuggestions_one": "possible duplicate person", "linkSuggestions_other": "possible duplicate people",
    "unsavedRecordings_one": "unsaved recording", "unsavedRecordings_other": "unsaved recordings",
    "automationUpdates_one": "automation update", "automationUpdates_other": "automation updates",
    "unevaluatedApplications_one": "application to evaluate", "unevaluatedApplications_other": "applications to evaluate"
  },
  "stat": { "workflows": "Workflows", "running": "Running", "failed24h": "Failed (24h)", "orgs": "Orgs running", "people": "People",
            "activeCount": "{{count}} active", "queuedCount": "{{count}} queued", "ofRuns": "of {{count}} runs",
            "pausedOrStopped": "of {{count}} orgs", "addedWeek": "+{{count}} this week" },
  "workflowsSection": { "title": "Workflows", "openEditor": "Open Editor", "emptyTitle": "No workflows yet",
                        "emptyDesc": "Build your first workflow in the editor." },
  "workflows": { "activate": "Activate", "deactivate": "Deactivate", "neverRun": "never run", "run": "Run", "starting": "Starting",
                 "stop": "Stop", "stopTitle": "Stop this execution", "openEditor": "Open in editor",
                 "schedulePaused": "paused", "schedulePausedTitle": "The daemon is not running, so schedules do not fire",
                 "scheduleInvalid": "bad schedule" },
  "recentRuns": { "title": "Recent Runs", "empty": "No runs yet", "openTitle": "View execution in workflow editor" },
  "system": { "title": "System", "details": "Details", "daemon": "Daemon", "bridge": "Browser bridge", "orgServe": "Org serve",
              "health": "Health", "jev": "Jev", "running": "running", "stopped": "stopped",
              "bridgeState": { "connected": "connected", "waiting": "waiting for browser", "unpaired": "not paired", "off": "not running" },
              "orgServeRunning_one": "{{count}} org running", "orgServeRunning_other": "{{count}} orgs running",
              "healthOk": "all checks pass", "healthIssues_one": "{{count}} issue", "healthIssues_other": "{{count}} issues",
              "jevUsage": "{{calls}} calls · {{usd}} (24h)", "jevNoKey": "no key", "runCheck": "Run check" },
  "orgs": { "title": "Orgs", "empty": "No orgs yet", "create": "Create an org", "paused": "paused", "queued": "queued",
            "needsYou": "need you", "more": "+{{count}} more", "level": { } },
  "automations": { "title": "Automations", "installed": "{{count}} installed", "enabled": "{{count}} enabled",
                   "unavailable": "{{count}} unavailable", "pendingUpdate": "{{count}} update", "scriptsBlocked": "{{count}} scripts blocked",
                   "selectors": "Selectors", "ok": "ok", "decaying": "decaying", "broken": "broken", "stale": "stale",
                   "recordings": "Recordings: {{total}} · {{unsaved}} unsaved · {{incomplete}} incomplete" },
  "accounts": { "title": "Connected Accounts", "manage": "Manage", "empty": "No logins yet", "active": "active",
                "expiresIn": "expires {{when}}", "expired": "expired" },
  "activity": { "title": "Activity (7 days)", "captures": "Captures", "documents": "Documents", "summarising": "{{count}} summarising",
                "docErrors": "{{count}} errors", "messages": "Messages", "inOut": "{{in}} in · {{out}} out",
                "applications": "Applications", "pendingApplied": "{{pending}} pending · {{applied}} applied",
                "toEvaluate": "To evaluate", "people": "People", "addedLists": "+{{added}} · {{lists}} lists",
                "vault": "Vault", "vaultCounts": "{{secrets}} secrets · {{images}} images" }
}
```

Fill `orgs.level` with one key per level value found in Task 13. Check `src/i18n.js` for the i18next version's plural suffix convention (`_one/_other` for i18next ≥ 21); if older (`key`/`key_plural`), use that instead and adjust the test's `has()`. The existing `dashboard.stat.{active,recentRuns,peopleFound}` keys become unused — delete them from both files.

- [ ] **Step 4: Responsive CSS** — in `index.css`:

```css
.stat-grid { grid-template-columns: repeat(5, minmax(0, 1fr)); }
.stat-sub { font-family: var(--font-mono); font-size: 10px; color: var(--text-dim); margin-top: 3px; }
.stat-card-btn { text-align: left; cursor: pointer; font: inherit; color: inherit; }
.dashboard-grid { grid-template-columns: minmax(0, 1fr) 340px; }
.dash-col { display: flex; flex-direction: column; gap: 16px; min-width: 0; }
.dash-row { display: flex; align-items: center; gap: 8px; padding: 6px 4px; border-radius: var(--radius); cursor: default; }
.dash-row[role="button"] { cursor: pointer; }
.dash-row[role="button"]:hover { background: var(--elevated); }
.dash-row-label { flex: 1; font-size: 12px; color: var(--text); }
.dash-row-value { font-family: var(--font-mono); font-size: 10.5px; color: var(--text-muted); }
.dash-chip { display: inline-flex; align-items: center; gap: 3px; font-family: var(--font-mono); font-size: 10px;
  color: var(--text-muted); border: 1px solid var(--border-dim); border-radius: 999px; padding: 1px 7px; white-space: nowrap; }
.dash-chip-warn { color: #fbbf24; border-color: rgba(251,191,36,.35); }
.attn-strip { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 14px; padding: 8px 10px;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg); }
.attn-clear { color: var(--text-muted); font-size: 12px; gap: 6px; }
.attn-title { font-family: var(--font-mono); font-size: 10.5px; letter-spacing: 2px; text-transform: uppercase; color: var(--text-muted); margin-right: 4px; display: inline-flex; gap: 5px; align-items: center; }
.attn-chip { display: inline-flex; align-items: center; gap: 5px; font-size: 11.5px; padding: 3px 9px; border-radius: 999px;
  border: 1px solid var(--border); background: var(--elevated); color: var(--text); cursor: pointer; }
.attn-chip:focus-visible { outline: 2px solid var(--cyan); outline-offset: 1px; }
.attn-count { font-family: var(--font-mono); font-weight: 700; }
.attn-danger { border-color: rgba(239,68,68,.45); } .attn-danger .attn-count { color: #ef4444; }
.attn-warn { border-color: rgba(251,191,36,.45); } .attn-warn .attn-count { color: #fbbf24; }
.attn-info .attn-count { color: var(--cyan); }
.dash-tiles { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
@media (max-width: 1100px) { .dashboard-grid { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 760px) { .stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } .dash-tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
```

Edit the existing `.stat-grid` (`:537`) and `.dashboard-grid` (`:1500`) rules in place rather than duplicating selectors.

- [ ] **Step 5: Run** `npx vitest run` → PASS. **Commit** `feat(i18n): dashboard strings in English and Spanish; responsive dashboard layout`.

---

## Phase D — Verification and release

### Task 18: browser verification, real-CLI smoke, docs

**Files:**
- Create: `~/scratch/dashboard-screens/stub.js` (not committed) — CDP stub for `window.go.main.App` / `window.runtime`
- Modify: `CHANGELOG.md` (Unreleased section), `docs/` screenshot if the repo keeps GUI screenshots (check `docs/*.png`)

- [ ] **Step 1: Whole-repo checks**

Run: `gofmt -l . && go vet ./... && go test ./... && go build -tags nosocial ./cmd/monoagentcli && (cd wails-app && go vet -tags webkit2_41 . && go test -tags webkit2_41 ./...) && (cd wails-app/frontend && npm test -- --run && npm run build)`
Expected: `gofmt -l` prints nothing; every suite PASS; frontend build succeeds. Record the Go and Vitest test counts in the PR (memory *parallel-agent-waves*: state scope with the number).

- [ ] **Step 2: Real-profile CLI smoke**

Run: `~/scratch/dash/monoagentcli summary; ~/scratch/dash/monoagentcli --json summary | jq 'to_entries | map(select(.value | type == "object" and has("error"))) | map({(.key): .value.error}) | add'; ~/scratch/dash/monoagentcli org summary --fast | jq .totals`
Expected: text summary readable; the `jq` error probe prints `null` (no section errors) — any section error is a bug to fix before merging; org totals match the Orgs page.

- [ ] **Step 3: Browser check with stubbed bindings (fast loop)**

Start the Vite dev server (`cd wails-app/frontend && npm run dev`), open it via `Skill("agent-browser-testing")` or monobrowse, and inject before load (`Page.addScriptToEvaluateOnNewDocument`) a stub where `GetSummary` returns the real JSON captured in Step 2, `GetOrgSummary` the real org summary, `ListWorkflows`/`GetRecentExecutions` real data, `IsReady: true`, runtime `EventsOn` returning a no-op unsubscribe (memory *test-gui-in-browser-and-via-cli*). Verify and screenshot at 1440 px, 1024 px and 700 px widths:
  1. Needs-you strip shows exactly the non-zero items; each chip navigates to the right page/tab (HIL chip opens the drawer; broken-selector chip opens Connections › the automation's Health tab; health chip opens Settings with System health expanded).
  2. SUCCESS runs are green; RUNNING dot pulses.
  3. A workflow with a `trigger.schedule` shows "in Nm"; with daemon stopped (stub `services.daemon.running=false`, `schedules.daemon_running=false`) it shows "paused" and the `daemonOffline` chip appears.
  4. No horizontal scroll at 700 px; right column stacks under 1100 px.
  5. Switch language to Spanish in Settings: no English strings left on the dashboard.

- [ ] **Step 4: Real app check**

Run `wails dev -tags webkit2_41` with an isolated `HOME` under `~/scratch/dash-home` (after `monomind init` on that profile), open the served URL in the browser, and repeat checks 1–3 against real CLI data. Start a workflow from the dashboard and confirm the run appears in Recent Runs within 2 s and the summary counts update on `workflow:complete`. Start `monoagentcli daemon` in that HOME and confirm the System card flips to running within 15 s. Restore regenerated `wailsjs/` and `package.json.md5` files that aren't part of Task 7.

- [ ] **Step 5: Polling cost check**

With the dashboard visible for 2 minutes, count CLI processes spawned: `pgrep -fc 'monoagentcli.*summary'` sampled, or wrap `MONOAGENTCLI_BIN` in a logging shim. Expected per minute while idle: ~4 `summary`, ~4 `org summary --fast`, ~1 `org summary`, ~12 `workflow executions --all`/`workflow list` pairs. Hide the window (switch page) and confirm spawning stops except the Sidebar/StatusBar's existing polls.

- [ ] **Step 6: CHANGELOG + commit**

```markdown
### Added
- Dashboard: a "Needs you" strip (approvals, org items, leads, broken selectors, expired logins, health),
  next scheduled run per workflow, orgs, automations, accounts, activity and system cards.
- `monoagentcli summary` — read-only, local at-a-glance roll-up (`--section` to narrow).
- `monoagentcli org summary [--fast]` — one row per org.
- `monoagentcli workflow executions --all`.
### Fixed
- Dashboard showed successful runs in grey and the running indicator never pulsed.
### Changed
- The dashboard's data now comes from the CLI instead of direct database reads in the desktop app.
```

```bash
git commit -F - -- CHANGELOG.md <<'MSG'
docs(changelog): dashboard refresh, summary and org summary commands
MSG
```

- [ ] **Step 7: PR** — push `feat/dashboard-refresh`, open one PR to `master` titled `feat: dashboard refresh — at-a-glance home backed by monoagentcli summary`; body lists perf numbers (Tasks 4, 5, 6), test counts (Step 1), screenshots (Step 3), and the follow-ups in §6. Wait for the required Doctor smoke check; merging = one release.

---

## 5. Risks and caveats

| # | Risk | Mitigation in this plan |
|---|---|---|
| C1 | Mixed timestamp formats make "last 24h / 7d" counts silently wrong | D5 `sinceExpr`; Task 1 Step 1 checks real data; tests seed all three formats |
| C2 | `summary` gets slow as data grows (it's polled) | Perf gate Task 4 Step 5; counts use indexed columns (`profile_id`, `status`); recent list capped at 15 |
| C3 | `openAutomationRegistry()` seeds built-ins (writes files) on every call | Measure in Task 4 Step 5; if seeding dominates, add a read-only open path (`automation.Default()` without `SeedWithReport`) for summary |
| C4 | Loopback bridge probe hangs when a stale process holds the port | ≤ 500 ms timeout (Task 4 Step 3); bridge `null` renders "not running" |
| C5 | `org summary` (full) spawns ~4 monomind processes per org every 60 s | Parallel with 10 s cap per org; only while visible; `--fast` for the 15 s poll; fast replies keep the last full `needs_you` (`mergeFast`) |
| C6 | Computed next-run disagrees with the daemon (5-field specs, timezone) | Same `CRON_TZ=` construction as `trigger_manager.go`; 5-field specs reported as invalid; `daemon_running=false` → "paused" |
| C7 | `GetDashboardStats` semantics change for Sidebar/StatusBar (`executions_by_status` now only RUNNING/QUEUED; `active_sessions` now excludes expired only) | Task 7 greps consumers before deleting; StatusBar render test still passes |
| C8 | People-review / drafts / HIL counts duplicate the HIL drawer's own fetch | Counts only; drawer keeps owning the items; chip opens the drawer |
| C9 | Summary's `hil.total` ≠ Sidebar HIL badge (badge includes org items) | Attention strip shows org items as a separate chip from `org summary`; document in the PR |
| C10 | Stubbed browser tests pass while real bindings differ | Task 18 Step 4 re-runs the checks against `wails dev` with real CLI data |
| C11 | Shared checkout: another session moves the branch mid-work | Worktree under `~/scratch/` from the start; `git branch --show-current` before every commit; pathspec commits |
| C12 | Spanish copy quality | Reuse existing `es.json` terminology; ask the user to skim `dashboard.*` in `es.json` in the PR |
| C13 | `profile documents list` side effect | `summary` reads `vault_documents` directly and never calls the sync |

## 6. Out of scope / follow-ups

- **F1** Sidebar polls `hil list --suggest` every 5 s (can spend Jev money): switch the badge to `summary --section hil` + `org summary --fast` totals. Separate PR — it changes HIL drawer behavior.
- **F2** GUI updater calls GitHub from the desktop app (`updater.go:54`); add `monoagentcli update --check --json` and move `CheckForUpdate` behind it; then show "update available" in the System card.
- **F3** `login status --json` untagged keys + `null` on empty.
- **F4** Unread/read state for messages (needs a data-model change) → "unread" chip.
- **F5** Daemon-published schedule entries (heartbeat `schedules[]`) to replace computed next-run.
- **F6** Profile-switch event so the dashboard refreshes instantly on `SwitchProfile` (today: next poll, ≤ 15 s).
- **F7** Remaining GUI-side SQL (tags, people, image vault, chat, sessions, connections) → CLI commands, per the CLI-first rule.
- **F8** An `automations` group in `doctor`, so selector health also shows up under System health.

## 7. Self-review

- **Spec coverage** ("update the dashboard with all new stuff added to the system"): orgs → Tasks 5, 13; HIL/people approvals → 2, 11; automation packages + selector health → 3, 14; recordings → 3, 14; captures/documents → 2, 15; people review/links/messages → 2, 11, 15; applications → 2, 15; Jev → 3, 12; health/doctor → 12 (reuses `lib/health.js`); daemon/bridge/org serve → 3, 12; schedules → 1, 10; vault → 3, 15 (counts-only tile, D13 — user confirmed 2026-09-26); logins → 3, 14; i18n → 17; old bugs → 8. Update-available (F2) and unread messages (F4) are deliberately deferred because the CLI can't provide them yet.
- **Placeholders:** where a real column or helper name could not be confirmed from the survey (e.g. `SelectorHealth` field names, `jevtest` URL field, org test env helper, the `Autonomy` pause field), the step says which file to open and what to check, instead of guessing silently.
- **Type consistency:** `ExecRow` (Go) ↔ `WorkflowExecutionSummary` (Wails) ↔ `executions[]` (JS) share `id, workflow_id, workflow_name, status, trigger_type, started_at, finished_at, created_at, error`. `summary.schedules.upcoming[].workflow_id/next_run` are used by `WorkflowsCard`. `orgs.orgs[].needs_you` (int|null) and `orgs.totals.needs_you` are used by `attentionItems`, `mergeFast` and `OrgsCard`. `attentionItems` targets `{page,data}|{hil:true}` match `AttentionStrip.go()` and the navData handling in Task 16.
