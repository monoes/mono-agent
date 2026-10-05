# Dynamic org for coder chat — plan

Date: 2026-09-29 · Status: decisions settled (§10); issues filed; phase 0 in progress

Issues:
- mono-agent: epic #224. Phase 0 #225, phase 1 #226, phase 2 #227, phase 3 #228, phase 3b #229, phase 4 #230, release #231.
- monomind: #387 (subagent events), #388 (`--access read`), #389 (stdio tools with full access), #390 (`agent test --json`). Another session owns these, targeting 2.19.0 or later.
Base: `feat/coder-all-runtimes` (the `~/scratch/wt-coder-all` worktree, not yet pushed) and monomind 2.19.0 (`~/scratch/wt-mm-coder-all`, not yet released).
The dynamic org needs coder mode on every runtime, so that work has to land first.

## 1. The idea in one paragraph

A coder chat stops being one agent. It becomes a small, short-lived org. A **lead** agent (the chat's
runtime and model) talks to the user and splits the work. A **conductor** inside the `monoagentcli chat` turn process then
staffs each piece of work:
- monomind `pick` chooses the role (agent definition) and its skills.
- Jev chooses the runtime, model and effort, drawing only on models that have **passed validation**.
- The conductor runs every worker as its own `monomind agent exec`.

Every spawn, brief, result and tool call is written to the chat journal with an `agentId`. The GUI shows each open chat as a
floating bubble. Clicking a bubble expands a full-page view with the live org at the top (who is working, on which model,
and animated messages between agents) and the normal chat at the bottom.

## 2. What exists today (facts from exploration)

| Area | Today | Gap |
|---|---|---|
| Runtime health | `agent scan` only checks install and version. `monoagentcli agent test <rt>` sends one real "ok" turn but has no `--model` and no GUI. Auth failures only show up mid-turn (`agent_not_setup`). | No per-model validation, nothing persisted, no roster |
| Models | `monomind agent models` lists models live for claude, codex, agy and opencode. Other runtimes return `supported:false`. Nothing is cached. | Unlistable runtimes can only use their default model |
| Coder turn | `runCoderTurn` → `Exec` with `AccessFull`, settings `user,project,local`, and **no caller tools** (`chat.go:148` rejects `--tools`) | Needs stdio caller tools next to full access |
| Subagents | Claude's native `Task` shows up as `tool_activity` plus `parent_tool_use_id`. The SDK's `task_started/progress/notification` messages are **dropped** by the runner. Subagent assistant text probably **leaks** into the main text. | Upstream fix needed (§7) |
| Routing | `monomind pick -t … --json` returns ranked agents (Task `subagent_type` names) and skills (`invoke`), plus `confident`. It uses Jev when a TypeSafe key is set. | Nothing to fix |
| Jev | Typed `choice/noul/score` questions over a JSON state, ~0.3 s per call. It **cannot write text**. | Used for staffing only, never for decomposition |
| Org Runtime v2 | File-based orgs, `org_send` messaging, bus events, OrgCanvas. Full-access roles need an HMAC ack that only a human can create in their own terminal. | Too heavy for a per-chat org, and the ack blocks dynamic roles (§3) |
| UI | React 19 with no router and no state library. Chat is `useChatStream` + `chatReducer` + `ChatTimeline`. Org visuals are hand-rolled SVG (`orgGraph.tidyTree`, `RoleNode`, `odNodeEnter`/`odLiveDash` keyframes). | Needs bubble dock, overlay and org stage |

## 3. Architecture decision: a conductor in the chat process, not an Org Runtime v2 org

Two options were considered.

**A. Build a real monomind org per chat** (write `.monomind/orgs/chat-<id>.json`, then `org run`). This gets messaging, budgets
and bus events for free. It is rejected for four reasons:
- **Full access blocks it.** Each role needs a human-signed ack, and dynamic roles change the config, which suspends the ack.
- `org run` runs in the foreground until the goal is done. That does not fit multi-turn chat.
- Org files would pile up in the profile.
- Staffing changes mid-run would mean editing the config and reloading.

**B. A conductor inside the `monoagentcli chat` turn process (recommended).** The lead exec gets a few stdio caller tools, and
the turn process handles them in-process. Worker execs are goroutines inside that same process. Their events go into the
**same turn journal** with an `agentId`. This fits the product rules:
- all AI still goes through `monomind agent exec`;
- the logic lives in the CLI and can be tested without a GUI;
- stop, reconcile and resume already work per turn;
- mixed runtimes and models inside one chat work, which Claude's native subagents cannot do.

The native Claude `Task` tool stays usable. Those subagents are drawn as nodes too (see §5.4), but the lead's system prompt
tells it to prefer `org_spawn`, which is the path that can pick another model.

## 4. Phase 0 — validated agent roster (ships on its own)

### 4.1 CLI: `monoagentcli agent validate`
```
monoagentcli agent validate [--runtime R]... [--model M]... [--all] [--concurrency 3] [--timeout 60s] --json
monoagentcli agent roster [--ready-only] [--json]      # read the stored results, no model calls
```
For each installed runtime:
1. List its models with `agent models`. A runtime that cannot list models gets one entry: `default`.
2. For each model, run `Exec` with the prompt "Reply with the single word: ok", `--max-turns 1`, `--tools none`,
   `--access scoped`, a throwaway cwd, and the timeout.
3. Classify the outcome as one of:
   - `ok`, `auth` (with a login hint), `quota`, `model_unavailable`, `timeout`, `missing_binary`, `error`;
   - or `ok_unexpected` when the reply was not "ok". The model works but did not follow the instruction, which is itself a weak
     quality signal.
4. Record `latency_ms` (first token and total), tokens, `cost_usd` (or an estimate from pricing), runtime version, and
   `validated_at`.

Runs are streamed as NDJSON progress lines (`validate.started`, `validate.result`, `validate.done`) so the GUI can tick rows
live. Concurrency is one call at a time per runtime, and up to 3 runtimes in parallel, so no single CLI gets rate-limited.

### 4.2 Storage
- A migration adds the table `agent_model_validations(runtime, model, status, detail, latency_first_ms, latency_ms, tokens_in,
  tokens_out, cost_usd, cost_estimated, runtime_version, validated_at)`.
- The primary key is `(runtime, model)`, which keeps the latest result.
- A small `agent_model_validation_runs` history supports "last validated 2 h ago" and trends.
- The data is global, not per profile, because runtimes and logins are machine-wide.

### 4.3 How long results stay valid
A model is **ready** when its latest status is `ok` or `ok_unexpected`, it is less than 7 days old (setting), and the runtime version is
unchanged. It becomes:
- **stale** when older than the limit or the runtime version changed. It stays usable but shows a warning, and a background re-check is queued.
- **failed** when a real turn later hits `auth`, `quota` or `model_unavailable`. The conductor writes this back, so the roster
  heals itself.

### 4.4 GUI: the AI agents page
- A roster card per runtime, with a logo, version and login state. It expands into model rows, each showing a status chip
  (`✓ 1.4 s`, `✗ auth — Log in`, `⚠ stale`), latency, cost per call, effort levels, a full-access badge and tool fidelity.
- "Validate all" shows the call count and cost before it runs ("≈ 23 calls, ≈ $0.04"). There is also a re-validate button per
  runtime and per row, and rows update live as results stream in.
- The first time a coder chat is opened with no roster, a banner offers "Validate your agents (≈ 1 min)". Until that has run,
  only the lead's own model is used, so nothing is blocked.
- The binding shells out to `agent validate --json` and relays its lines as the Wails event `agents:validate`.

## 5. Phase 1 — the conductor (backend)

### 5.1 Caller tools for the lead
Given to the lead only when the chat's org mode is `dynamic`:

| Tool | Purpose |
|---|---|
| `org_roster()` | The ready models (with cost and latency tier, effort levels, full-access flag), the access profiles, the agent roles and skills available from monomind, and the current staff. This lets the lead choose for itself. |
| `org_spawn({brief, role?, skills?, runtime?, model?, effort?, access?, files?, needs_write?, wait?})` | Starts a worker with the settings the lead chose. Anything it leaves out is filled in by the staffing pipeline (§5.2). Returns `{agentId, role, skills, runtime, model, effort, access, why}`. By default it returns straight away (async). |
| `org_wait({agentIds, timeout_s ≤ 100})` | Blocks until the agents finish or the timeout passes. Returns results and summaries. Short timeouts keep each call under the exec `--tool-timeout`, and the lead loops. |
| `org_message({agentId, text})` | A follow-up to a finished or idle worker. Resumes its session (`--resume`) when the runtime supports it. |
| `org_stop({agentId})` | Cancels one worker. |

The **lead is in charge of its team**. It decides when to spawn, writes each brief, and can choose each worker's role,
skills, model, effort and access itself. The **decomposition** (what the pieces of work are and what each brief says) is
always the lead's job, because Jev cannot write text.

### 5.2 Staffing pipeline (per `org_spawn`)
The lead's own choices always win. The conductor only **checks** them and **fills in** what's missing:
- a lead-chosen model must be `ready` in the roster; otherwise the tool returns an error listing the ready alternatives;
- a lead-chosen role or skill must exist in monomind's registry;
- anything not given is chosen by the steps below.

A Coder setting `model_picker: lead | jev | lead-then-jev` (default `lead-then-jev`) decides who chooses a model when the lead
doesn't. With `lead`, the conductor asks the lead to always name one.

1. **Gate (Jev `noul`, cached per turn):** "Is this brief worth a separate agent, or should the lead do it itself?" This stops
   the lead from spawning five agents to answer "hi". Below the confidence floor, allow the spawn.
2. **Role:** `monomind pick -t <brief> --agents --top 5 --json` returns candidates. When pick is `confident` and `role_hint` agrees, take its
   first choice. Otherwise ask Jev a `choice` question over the top 5 with `role_hint` in the state.
3. **Skills:** `pick --skills --top 3`, keeping only the ones pick marks `confident`.
4. **Model:** Jev `choice` over the **ready** roster entries that can serve the role. A writer needs `full_access`. State:
   brief length, whether it writes, role category, cost and latency tier, and the lead's own model. It tie-breaks toward
   the cheaper model. Effort is a Jev `choice` over that model's `effort_levels`.
5. **Fallback without a Jev key:** use pick's keyword ranking plus a rule table (review/research → cheaper and faster tier;
   write/refactor → the lead's tier). The `why` field says which path chose.
6. **Build the prompt:** the agent definition's markdown body goes in `--system-file`, followed by the skill texts, a worker
   contract ("you are <role> in a team led by …; report in ≤ 200 words; list files you changed"), the brief, and the file
   scope.
7. **Launch:** `Exec` with the chosen runtime, model and effort, `cwd` = the chat's folder, access (§5.3), `--settings
   user,project,local`, `--max-turns` from the chat's settings, and a timeout.
8. **If the launch fails** with auth, quota or model_unavailable, mark that roster entry failed, take the next-ranked model,
   and emit `agent.reassigned`.

### 5.3 Safety, limits and concurrency
- **The lead gives each worker its access**, as a named profile in `org_spawn.access`. When the lead doesn't say, the
  profile follows from the role's category.

  | Profile | Gets | Typical roles |
  |---|---|---|
  | `coding` (default) | Full access: every coding tool, the shell, edits, the internet (the runtime's own web tools) | coder, refactorer, debugger, tester |
  | `qa` | `coding` plus the browser: the mono-agent extension and browser tools, as stdio caller tools | QA, e2e tester, accessibility auditor |
  | `automation` | `coding` plus mono-agent automation tools (workflow runs, extension, nodes) as caller tools | automation builder, integration engineer |
  | `research` | The internet and reading files; no edits. It runs as full access until monomind has `--access read` (§7) | researcher, reviewer, planner |

  The human grants full access once, when they create the coder chat, and that grant is the ceiling: no profile goes past it.
  Workers get no `org_spawn`, so they cannot hand out access themselves.
  Coder turns keep the existing rule that they **never** get messaging or people tools, so `qa` and `automation` leave those
  out.
- **One writer at a time (phase 1).** Any number of readers can run in parallel. A writer (`needs_write`, or a role whose
  category writes) holds a write lease on the chat folder, and others queue behind it. Isolating each writer in its own git
  worktree is phase 4. It works but merging adds risk.
  Readers still run with full access today, because scoped mode denies native Read and Grep. A read-only access mode is
  requested upstream (§7).
- **Limits** (per chat, editable in Coder settings): max agents per turn 6, max running at once 3, spawn depth 1 (workers do
  not get `org_spawn`), and a USD budget per turn. Runtimes that don't report cost use a pricing estimate, marked "≈".
  At a limit, `org_spawn` returns a tool error the lead can reason about. It does not fail silently.
- **Stop** cancels the lead and every worker (process-group kill, which is already in the worktree). Each worker also has its
  own stop.
- **Crash or app quit:** `chat history reconcile` marks orphaned workers `cancelled` and kills them by recorded PID.
  Bubbles rebuild from the journal.

### 5.4 Journal events (additive, chatevents v2)
New types:
- `agent.spawned {agentId, parentId, role, agentType, skills[], runtime, model, effort, why, pickConfidence, jevConfidence}`
- `agent.status {agentId, from, to}`, with statuses `queued|starting|working|waiting_lease|done|failed|cancelled`
- `agent.message {agentId, direction: brief|result|followup|question, from, to, text, summary}`
- `agent.reassigned {agentId, fromModel, toModel, reason}`
- `agent.finished {agentId, outcome, usage, filesChanged[]}`

Existing `assistant.delta`, `tool.started`, `tool.completed` and `usage.updated` gain an optional `agentId`. It is empty for the
lead, so old readers keep working. Claude-native `Task` subagents map to **nodes of type `native`**, keyed by
`parent_tool_use_id`, so they appear on the stage too.

There is no schema change, because payloads are JSON in `ai_chat_events`. The CLI makes it all visible:
`chat history events --agent <id>` and `chat history transcript --by-agent`.

## 6. Phases 2–3 — UI: bubbles and the org stage

### 6.1 Bubble dock (phase 2)
- **Opening chats.** Opening or creating a coder chat adds a bubble to a dock at the bottom-right, stacked upward like chat
  heads. At most 6 bubbles show; the rest go into a "+N" overflow bubble that fans out.
- **What a bubble shows:**
  - a small cluster of avatars for the active agents;
  - a status ring: spins while working, green on done, red on error, and pulses amber when a worker or the lead is waiting on
    the user;
  - an unread badge;
  - a hover card with the title, folder, elapsed time and cost;
  - a close control: ×, which asks first if a turn is running.
- **Dragging.** Bubbles can be dragged to reorder them, and the dock can be dragged to another edge.
- **Clicking a bubble** expands it into the overlay. The overlay animates from the bubble's position (FLIP). Clicking another
  bubble while one is open switches between them without closing.
- **Closing the overlay.** A click on the dimmed backdrop, Esc, or the collapse button closes it back to its bubble. Several
  things do **not** count as clicks outside:
  - a drag or text selection that ends outside;
  - clicks inside portals the overlay owns (menus, `ConfirmDialog`, tooltips);
  - clicks on the dock.
- **Collapsing never stops work.** Collapsing is not stopping; the turn keeps running and the bubble shows its progress. The draft
  and scroll position are kept per chat.
- **Narrow windows** (< 640 px) make the overlay full-screen with an explicit collapse button, because there is no outside to click.
- **Across restarts.** The list of open bubbles is a UI concern, so it is kept in localStorage and restored on launch. Chats whose
  turns were running come back from the journal.
- **One subscription.** A single app-wide `chat:event` subscription routes events by `conversationId`. A collapsed chat keeps
  only summary state (status, agent count, unread, cost). The full reducer state is built when the chat is expanded.

### 6.2 The expanded page
- **Top: the org stage.** Resizable, collapsible, 35% by default.
  - **Layout:** `orgGraph.tidyTree`, with the lead at the top and workers below. Native subagents hang off the agent that called them.
  - **Nodes:** the role icon and color (from `roleIcons.js`), the role title, a chip with the runtime logo and model name, a
    status ring, a live "now doing" line (from `tool.started`, e.g. "editing chat.go"), and a small token/cost meter.
  - **Spawns:** a new node pops in (`odNodeEnter`) and an edge draws toward it.
  - **Messages animate along edges.** A brief is an envelope flying down. A result is a glowing packet flying up, and when it
    lands a speech bubble with the summary appears above the parent for about 4 s. A question to the user makes the node pulse
    amber and a "needs you" pin appears.
  - **Outcomes:** done dims the node and shows a check. Failed shakes and turns red. Reassigned shows the old model chip crossed
    out and the new one sliding in.
  - **The "gamified" part stays informative, not decorative:**
    - a quest log strip lists the pieces of work as cards (queued, active, done), with a progress bar across the turn;
    - a lease indicator shows who holds the pen;
    - at the end of the turn a scoreboard card shows agents used, time, cost, files changed, and tests run and passed;
    - a per-agent XP-style tally counts tools used and files touched.
  - **Clicking a node** filters the chat below to that agent's thread and opens a side drawer with its brief, transcript,
    tool cards (reusing `NativeToolCard`), model, why it was picked, and a stop button.
  - `prefers-reduced-motion` turns the particles into instant state changes. The stage pauses its animations when collapsed or hidden.
- **Bottom: the normal chat.** `ChatTimeline` shows the lead's text. Worker activity appears as compact, collapsible "team" rows
  (spawned / result), so the conversation stays readable. `ChatComposer` is at the bottom as today, with model and effort
  pickers and an "Org: dynamic / solo" toggle.

### 6.3 Relationship to today's chat panel
Only coder chats and running orgs (§6.4) get bubbles and the org stage. The general assistant side panel (`AIChatPanel` in
`App.jsx`) stays as it is.

### 6.4 Running orgs as bubbles (the same surface for both kinds of org)
A running Org Runtime v2 org can be opened as a bubble too, which gives one surface for both kinds of org:

| | Dynamic org (coder chat) | Running org |
|---|---|---|
| Who you talk to | The lead | The boss role |
| Where the stage comes from | Journal `agent.*` events (`chat:event`) | Bus events via `org events --follow` (`org:event`, which the `orgActivity` reducer already folds) |
| Staff | Spawned during the turn (pop-in) | Fixed roles from the config; lazily started roles pop in when their first message arrives |
| Sending a message | A new chat turn | The org inbox, addressed to the boss (existing `org_control` inbox command) |
| What shows up in the chat | Lead text, worker rows | Boss replies, `ask_human` questions and gates (answerable inline), role-to-role messages as rows |
| Stop | Stop the turn | Pause, resume or stop the org, and each still asks first |

- **One stage.** Both sources are turned into one stage model (`nodes`, `edges`, `flights`, `feed`) by two small adapters. The
  stage component doesn't know which kind it is drawing.
- **Opening one.** An "Open as bubble" action sits on the Orgs page and on org cards. A running org that asks the human
  something gets a bubble automatically (amber pulse), so a pending question can't be missed.
- **CLI first.** If the GUI needs something that has no `monoagentcli` command yet (for example a boss-thread transcript),
  that command comes before the binding.

## 7. Upstream monomind work (monoes/monomind #387–#390)
1. **Subagent lifecycle.** Forward the SDK's `task_started/task_progress/task_notification` as `status` or new `subagent`
   events. Tag or suppress subagent `assistant` text (`parent_tool_use_id`), which fixes the likely text leak into `result.text`.
2. **Read-only access.** Add `--access read` (Read, Grep, Glob, WebSearch/WebFetch, and read-only shell like `git diff`) so
   reviewer and researcher workers can run with less than full access.
3. **Caller tools with full access.** Confirm, with a test, that `--tools stdio` works together with `--access full` on every
   full-access runtime, and declare this as a capability (`agent-exec-full-access-tools`).
4. Optionally, `agent test --model` with a `--json` result, so validation could shell out to monomind instead of building
   the "ok" turn itself. Mono-agent can build it on `Exec` today, so this is nice to have only.

**Notes on #387 from its implementer** (protocol rev 17, not merged yet):
- Subagent `usage` is `{total_tokens, tool_uses, duration_ms}` (SDK 0.3.226), not an input/output/cost split.
- Subagent text arrives as `assistant {text, parent_tool_use_id}` and is kept out of `result.text`. The leak was real.
- `task_started` also fires for non-agent background tasks such as background Bash. Filter on `subagent_type` to get agents only.

The mono-agent side feature-detects each of these and degrades cleanly:
- without (1), native subagents show as tool cards only;
- without (2), readers run with full access, under the one-writer lease;
- without (3), a lead on that runtime runs solo.

## 8. Phasing and verification

| Phase | Scope | Done when |
|---|---|---|
| 0 | `agent validate` / `agent roster` CLI, migration, AI agents page roster | A scratch-HOME run lists every installed runtime's models with real statuses. A logged-out runtime shows `auth` with a hint. The GUI updates rows live (checked in a browser via `wails dev`). |
| 1 | Conductor, caller tools, Jev and pick staffing, limits, lease, journal events, reconcile | A CLI e2e run where a scripted lead spawns 2 readers and 1 writer produces `agent.*` events. The limits return tool errors. Stop kills every PID. Go tests use a fake `Exec` (mock-first). |
| 2 | Bubble dock and overlay, click-outside rules, restore, single event bus | Vitest covers the click-outside matrix. A browser check covers 3 chats at once, collapsing while running, and restoring after a reload. |
| 3 | Org stage, animations, node drawer, scoreboard, reduced motion | A replay of a recorded journal renders the same stage as the live run did (a deterministic reducer test). Screenshot review. |
| 3b | Running orgs as bubbles: bus adapter, chat with the boss through the inbox, questions and gates inline | A scratch-HOME org run opened as a bubble shows its roles live. A message to the boss is delivered and its reply shows up. An `ask_human` question pops the bubble. |
| 4 | Worktree-isolated writers, spawn depth 2, worker follow-ups across turns, cost estimates | The reviewer finds no merge regressions on a two-writer task. |

Rules:
- Every branch build runs with `HOME=~/scratch/...`, because branch builds migrate the real DB. The live Claude credentials are
  never copied into a test home.
- One integration PR per release.

## 9. Caveats and how each is handled

| Caveat | Handling |
|---|---|
| Validation costs real calls (N runtimes × M models) | Cost shown before running. Only runs on demand or for stale entries. One call per runtime at a time. No automatic "validate all" on startup. |
| "ok" only proves the model answers, not that it is good | Roster quality comes from Jev staffing plus outcome history (`agent.finished.outcome` feeds a success rate per model). `ok_unexpected` is flagged. |
| Runtimes that can't list models | A single `default` entry. The user can add a model id by hand, and it is then validated. |
| Jev can't write text or plans | Jev only gates, picks and ranks. The lead LLM decomposes. |
| No Jev key | Keyword pick plus the rule table. The UI shows "staffing: heuristic". |
| Parallel writers corrupt the folder | Write lease in phase 1, worktrees in phase 4 |
| Runaway spawning and cost | Gate, limits, budget, depth 1, and visible "≈" costs |
| Cost unknown on non-claude runtimes | Estimate from pricing and tokens, marked "≈". The hard budget counts the estimates too. |
| Different tool-activity fidelity | A "limited activity" badge on nodes whose runtime is start-only |
| Long workers vs. tool timeouts | Async `org_spawn` plus a looping `org_wait` with a short timeout |
| Lead runtime without stdio tools | Org mode off for that lead, with the reason shown in the composer toggle |
| Full-access consent | The human's grant on the coder chat is the ceiling. The lead hands out profiles below it, and workers can't spawn or grant. |
| Browser contention (`qa` or `automation` workers share one extension bridge) | One browser-using worker at a time, with a browser lease like the write lease. Use the user's bridge; never start a competing one. |
| Many bubbles cost CPU | Summary-only state while collapsed, animations paused, one event subscription |
| App closed mid-turn | Journal replay plus reconcile, and orphaned PIDs killed |
| Stale roster during a turn | Real failures mark entries failed and trigger an automatic reassign, shown as `agent.reassigned` |

## 10. Decisions (settled by the user, 2026-09-29)
1. **Worker access is the lead's call.** The lead spawns each worker and chooses its model, skills, work and access profile.
   Coding workers normally get full access to coding tools. QA and automation workers also get the internet and the
   extension (§5.3).
2. **Bubbles and animations are only for coder chats and running orgs**, and running orgs let you chat with the boss (§6.4).
   The general assistant panel stays as it is.
3. **One writer at a time** in phase 1. Worktree-per-writer comes in phase 4.
4. **Model choice:** the lead can choose the model itself, or leave it to Jev (default `lead-then-jev`, §5.2). Validation
   covers every listed model on demand, with validate buttons per runtime and per model, and shows the cost before
   "validate all".
