# Task Board, Phase 3 (the desktop Tasks tab) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan one task at a time. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Tasks tab right after Documents in the desktop app: the active profile's board as five live columns, with drag, keyboard moves, a detail drawer, quick add, and a sidebar badge, that feels immediate and stays correct while agents work the same board.

**Architecture:** Go bindings in `wails-app` run every action as `monoagentcli --profile <active> --json task …` and return the CLI's stdout verbatim, so the page sees every refusal code. The board itself is read in process (P1's `Store.Board`, in the document `task board --json` prints), because the CLI refuses `task board` to an agent-driven caller and the tab must still show the board read-only there. An in-process watcher polls the board revision (P1's `Store.Watch`) and emits `tasks:changed`; the page answers with one board read. The page keeps the last read plus a list of optimistic operations laid over it: a move shows at once, stays until the read that includes it, and drops out (the card slides back) when the CLI refuses. All rules about order, placement, search and what changed live in one pure module (`lib/taskModel.js`) that the tests drive; the components stay thin.

**Tech Stack:** Go (Wails v2 bindings, `internal/tasks`), React 19, lucide-react, react-i18next, vitest with jsdom and Testing Library. No new dependency in Go or npm.

**Spec:** `docs/mastermind/specs/2026-10-05-task-board-design.md` (sections 2, 3, 4.5, 5, 6, 10, 13, 14, 15, 17). Section numbers below refer to it. The P1 plan is `docs/mastermind/plans/2026-10-05-tasks-board-phase1-store-cli.md`.

**Branch:** `feat/tasks-board-gui`, stacked on `feat/tasks-board` (P1). Executed only after P1 is complete on `feat/tasks-board`.

## Global Constraints

- Placement: `NAV_ITEMS` gets `{id: 'tasks', labelKey: 'tasks', icon: SquareKanban, section: 'DATA'}` right after Documents; `persistentPages` gets `tasks: <Tasks isActive={activePage === 'tasks'} />` (an id missing there silently shows the dashboard); `sidebar.nav.tasks` in `en.json` and `es.json`. The badge is Inbox plus Review (tooltip: the split), driven by the `tasks:changed` event, so it is right before the tab was ever opened.
- Event: `tasks:changed {profile_id, rev, inbox, review}`. The watcher polls the board revision in process every two seconds and is restarted on startup and profile switch and stopped on shutdown, like the document watcher.
- Data path: actions shell out to `monoagentcli task ... --json`; the whole board is one in-process `Store.Board` read in the `task board --json` document (Ruling: P1 made `task board` operator-only); Done shows the 50 most recent.
- Columns `inbox`, `ready`, `in_progress`, `review`, `done`; `archived` is hidden. A task new to a column goes to the top of Inbox, Review and Done and to the bottom of Ready and In progress.
- Interaction: drag with the app's mouse ghost drag (no native drag), a glowing drop zone, a drop indicator between cards, column auto-scroll; the move is applied at once and the CLI call follows; a refusal reverts it and says why.
- Keyboard: Tab reaches cards; Enter opens; Shift+Left and Shift+Right move to the neighbouring column; Alt+Up and Alt+Down reorder; `A` approves an Inbox card; `N` opens quick add; `/` focuses search; Escape closes the drawer; an `aria-live` region announces each move.
- Motion: cards enter with a short scale and fade; reorders animate by FLIP; a Done card gets a check pulse; a claim pulses softly. All of it is off under `prefers-reduced-motion` (the global rule plus a JS check).
- Empty states, verbatim: Inbox "Highlight text in Chrome and choose MonoAgent, Add selection as task, or select text in any app: Services, Add to MonoAgent Tasks"; Ready "Approve tasks from Inbox so agents can pick them up".
- A stale claim shows in amber. Columns scroll on their own; the board scrolls sideways under 1,100 px.
- No new dependencies, no new HTTP route, no new port. Dark theme. English and Spanish. Selects follow the select policy (`src/selectStyle.test.js`; this plan adds no `<select>`).
- Files stay under 500 lines. Commits are `feat(tasks): ...` or `docs(tasks): ...`, each ending with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Environment rules for whoever runs this plan

- You work in the worktree of branch `feat/tasks-board-gui`. Do not push, merge or switch branches. Other Claude sessions use this repository; touch nothing outside your worktree.
- In this repo's worktree sessions the Bash tool refuses compound git commands, `git -C` and heredocs: run `git add <files>` and `git commit -m "<subject>" -m "<trailer>"` as two separate calls. Write files with the Write and Edit tools.
- A project hook blocks Bash commands whose text holds destructive SQL or `rm -r`, and refuses a Write whose content looks like an API key: the letters `sk-` followed by 20 or more letters, digits, `_` or `-`. That is why every CSS class and test id here starts with `tb-`: never write the word "task" directly followed by a hyphen in code, names or prose.
- The app is a Go module of its own: run Go commands for it as `go -C wails-app <command>`, and frontend commands as `npm --prefix wails-app/frontend <command>` (for one test file: `npm --prefix wails-app/frontend test -- src/path/file.test.js`). Go commands use the real Go caches; never override `HOME` for them.
- `wails-app/main.go` embeds `frontend/dist`: any `go -C wails-app` command fails until `npm --prefix wails-app/frontend run build` has run once (Task 0 does it; any build will do, its content does not matter to the Go tests).
- Never launch the desktop app (`wails dev`, `wails build`, an `.app`) or a daemon: your shell carries agent-context markers the app would inherit. A built `monoagentcli` is built with `-o` into a fresh `mktemp -d` directory, never into the repo, and runs only as `env -i HOME=<that dir>/home PATH=/usr/bin:/bin <that dir>/monoagentcli ...`, so it sees no marker and no real home.
- The machine is shared and often busy: run the named test files only; Task 12 runs the full suites once. Builder subagents run at most two at a time, and two builders in one worktree share one git index: they take turns for `git add` and `git commit`.
- The code here was written without a compiler. A compile slip (an unused import, a shadowed name) is fixed in place and the task goes on. A failing assertion is different: read the spec section the test comes from before changing the test or the code, and say which of the two was wrong.
- The locale files hold Spanish letters and the visible characters `…`, `·`, `⌘`: expected. No invisible character (zero-width, bidi, BOM) belongs in any file; Task 6 checks.

## Rulings (where the spec is silent or this phase decides)

- Ruling: the bindings return the CLI's stdout verbatim, `{"error","code"}` included, through a new `taskCLI` helper (stdin-capable, based on `cliResultJSON`) - `runMonoCLI` and `cliJSON` drop the code the page needs to tell `operator_only` from `claimed` - cost if wrong: one helper to swap.
- Ruling (lead, after P1 made `task board` operator-only): reads in process, writes through the CLI. `TaskBoard` calls `Store.Board(ctx, profileID, doneLimit)` (it takes no actor: the read is the operator's) and returns the `tasks.Board` document `task board --json` prints; every action (add, edit, move, approve, archive, unarchive, comment) still runs the CLI - the CLI now refuses `task board` to an agent-driven caller (the whole board would hand an agent every unreviewed Inbox card, spec §4.1), yet the tab started from an agent's shell must still show its board read-only (§17.3), and the actions keep the CLI's guard, which refuses them there. A deviation from D21 (bindings shell out); Task 12 amends D21 - cost if wrong: one read to put back on the CLI.
- Ruling: the app adds tasks with `--source app` - P1's `sourceKindFor` accepts `app` from the operator, and P1's `--source` help will name it (the lead passed this on to P1), although spec §7 documents only `cli|os` for the flag - cost if wrong: app-made cards show "Terminal".
- Ruling: `TaskPulse()` reads the revision and the counts in process, as the watcher does, for the badge's first value - the watcher's first `tasks:changed` fires during startup before anything listens, and P2's `summary --section tasks` may not be merged when P3 is - a read in process, as amended D21 has every read (the "reads in process" Ruling) - cost: none.
- Ruling: when the profile a watcher was started on is not there, the watcher writes one warning to the app's log and ends, and does not start over. P1's `Watch` returns an error then (ErrInvalid at once for a profile that never existed, an error wrapping ErrNotFound for one deleted while watched); a restart would only get the same error at once, and the next `SwitchProfile` or start begins a new watcher (neither the CLI nor the app can delete a profile yet) - cost if wrong: after a deleted active profile the badge keeps its last value until the next switch.
- Ruling: `TaskPulse` resolves the profile with `Store.Profile` before it reads, because `Rev` and `Counts` read an unknown profile as revision 0 and no cards, a board that never existed; a profile that is not there answers `{}`, as any failed read does, and the sidebar ignores `{}` - cost: one more primary-key read when the sidebar mounts.
- Ruling: when the app was started from an agent's shell (`TaskAgentShell()` names the inherited marker, or `MONOAGENT_ACTOR`), the board is read-only and a banner says to open MonoAgent from the Dock or Finder; a CLI `operator_only` refusal still reverts and toasts the CLI's message plus that hint - the CLI refuses the app's actions in three different ways there (`operator_only`; `invalid_input` for `add --source app`; "name yourself with --as" for `comment`), so acting first would explain only one (spec §10 Errors, §17.3). Accepted by the lead; Task 12 amends §10 and §17.3 - cost if wrong: re-enable the controls.
- Ruling (lead, review F6): one-click approval only for a card whose whole text shows. A card with notes, or whose title the card clamps to two lines, shows a notes marker and never goes from Inbox to Ready in one gesture: its Approve button reads "Read and approve", and it, `A`, Shift+Right and a drop on Ready all open the drawer instead (a dropped card slides back, and the drawer says why), where the person approves with the text in front of them (spec D6: Ready is where agents act on the text). The drawer's own Approve and "Move to" stay. Task 12 amends §10 - cost if wrong: one more click.
- Ruling: dark only (confirmed by the lead); every colour comes from the `:root` variables through board tokens declared once at the top of `tasks.css`, plus the amber `#f59e0b` the app already uses for "stale" in Documents - the app has no light theme (spec D20, §3) - cost if wrong: redefine the tokens.
- Ruling: a column's dot carries its colour, not a stripe on every card (review: the stripe is a stock look and §10 does not ask for it); the amber edge stays on a card whose lease ran out, where it means something, and Review's colour is rose (`--instagram`) so that amber stands out there - cost: taste.
- Ruling: the binding wrappers live in `services/tasks.js` (the `services/library.js` pattern), not `services/api.js`, which is already 515 lines - cost: none.
- Ruling: a drop or Alt+arrow names a shown neighbour (`--before`/`--after`), never `--bottom` - a search filter or the Done column cut at 50 would otherwise bury the card among hidden ones; a card new to Done dropped below the last shown card of a cut Done column takes Done's default (the top), since right after that card is the 51st place, which the next read hides; Shift+arrows, Approve, Done and Back to Ready use the column's default place - cost if wrong: a placement detail.
- Ruling: the plain arrow keys move the focus between cards (Up/Down in a column, Left/Right to the nearest column with cards) - a grid of 50+ focusable cards needs it; the spec's map has no focus keys - cost: four keys.
- Ruling: moving or archiving a card out of In progress while an agent's lease is live asks first (the app's `confirm`; accepted by the lead) - the agent's next call would fail with `not_claimant` mid-work - cost if wrong: one dialog.
- Ruling: quick add is not optimistic: the box shows "Adding…", the card enters when the CLI answers. Text of several lines, or one line longer than 120 characters (the derived title's length), goes whole with `--stdin`, so the CLI makes the title and keeps everything in the notes (D29) instead of cutting a pasted paragraph at 200 characters; a shorter line is the title. The store's rule (spec 4.6, D29) stays the only place that derives a title and notes: the app does not copy it and puts no cap of its own on quick add (the lead's ruling). `TaskAdd` refuses notes together with text, so the CLI never gets both - cost: about 200 ms of wait.
- Ruling: the app does not save notes over 30,000 characters (they travel on the command line, which Windows caps near 32,767): the bindings (`TaskAdd`, `TaskEdit`) refuse them and point to `monoagentcli task` in a terminal - cost: such notes are saved in a terminal.
- Ruling: archiving from the drawer offers Undo (`task unarchive`) in the board's own activity toasts; those toasts (another actor's moves, the archive) are neutral and local to the page, because the app's `Toasts` render every message as a red failure - cost: one small component.
- Ruling: empty-state copy for In progress, Review and Done (the spec gives Inbox and Ready only) - cost: copy.
- Ruling: the drawer's history words a `released` event "Claim ended" (Spanish "Toma terminada"), not "Released to Ready": the store writes `released` when an agent gives a task back, which goes to Ready, and also when the operator moves or archives a card an agent holds, ahead of the move's own event and with the card's destination as its to_status (any column, or `archived`, which the drawer shows as the raw word in both languages), so the line can promise no destination; the note under it carries the detail. The activity note "released" keeps its wording, since the operator's own actions are never noted - cost if wrong: an agent's own release reads less exactly.
- Ruling: a `tasks:changed` that arrives while a mutation is pending, the tab is inactive or the window hidden only marks the board dirty and the read follows; a read asked for by tab activation shows no activity toast; at most three toasts per read; never for the operator's own actions (`last_event.actor == "you"`) - cost if wrong: fewer toasts.
- Ruling: the screenshots the spec wants in the PR are taken with Playwright against `vite preview` with `window.go` mocked, saved outside the repo and listed for the lead to attach (Task 13) - committing PNGs bloats the repo - cost: none.
- Ruling: `ref tasks` and the CLI do not change in this phase; the desktop tab is documented in AGENTS.md (both places the app is described), the changelog and spec §10 - `ref tasks` documents the CLI, which P3 does not touch - cost: none.
- Ruling: the board's clock is minute-floored and ticks every 15 s while the page shows, so a lease turns amber within a minute of ending although no write marks it; the store's `stale` flag is honoured too - cost: up to a minute late.
- Ruling: the `wailsjs` entries are placed by hand in Go byte order, as earlier GUI phases did: the files are generated ("DO NOT EDIT"), committed, and regenerated by every release build (`wails build -skipbindings=false`, release.yml), so a mismatch costs a diff, never a break. Task 3 compares them with what the local `wails generate module` writes (v2.11 here, go.mod pins v2.16, so only the `Task*` lines are compared), and a vitest pins that every binding the service calls exists. `TaskApprove`, `TaskArchive` and `TaskUnarchive` take `[]int64`, the first numeric slice among the app's bindings (review minor 3): Wails passes a JSON array, the generator writes `Array<number>`, and the same comparison checks that spelling - cost: none.

## Review Focus

Failure modes the spec implies and no happy-path test would catch; each has a test in the task that owns the code.

1. A pasted paragraph is one long line: it must not lose its tail. Over 120 characters it goes whole as text (the CLI keeps it in the notes); the drawer's title field takes at most 200. (Task 7b: "sends a line over 120 characters whole, as text"; Task 9: the `maxlength` check)
2. Approving text the person never saw: a card with notes, or a clamped title, must open the drawer, never reach Ready, whatever the gesture: Approve, `A`, Shift+Right or a drop on Ready. (Task 7a: "opens a card that has more to read instead of approving it"; Task 8: "sends a card with more to read to the drawer from A and from Shift+Right", "opens a card with more to read dropped on Ready instead of moving it"; Task 10c: "opens a card with notes instead of moving it to Ready, and says why")
3. Text that starts with a dash (a title `-x fix`, notes `--help me`, a comment `-looks odd`) must reach the CLI as text, never as a flag. (Task 1: `TestTaskBindingsPassTheirArgumentsExactly`)
4. A refetch must not undo an optimistic move, a failed confirming read must not drop it, and the operator's own move must not toast. (Task 5: "keeps a move until the read that includes it", "keeps a confirmed move when the confirming read fails", "toasts what an agent did, never the operator's own move")
5. The badge before the tab was ever opened, and after the window reloads on a profile switch: the first `tasks:changed` was emitted before anything listened, and until `startup` has chosen the profile the app answers for `default`. (Task 2: `TestTaskPulseReadsTheActiveBoard`; Task 11: the two Sidebar badge tests: an older revision of the same profile never overwrites a newer one, the badge asks only once `IsReady` is true, and another profile's value replaces it)

Close runners-up, also pinned: the app started from an agent's shell is read-only up front and still shows its board, although the CLI refuses `task board` there (Task 1: `TestTaskBoardIsReadInProcessEvenUnderAnInheritedMarker`; Task 10c); `/`, `N` and `A` stay quiet while the page is hidden or a field has the focus (Task 10c); untrusted notes render as markdown without images or unsafe links (Task 9); the Done pulse lasts through the confirming read (Task 10a); a drop below the cut Done column's last card stays visible (Task 8); a profile that is not there ends the board watcher with one warning in the app's log, and `TaskPulse` answers `{}` for it (Task 2).

## The board at a glance (what the code below builds)

- **Layout.** A 52 px page header: the kanban icon, "Tasks", a pill "in Work" (the profile), then search (with a `/` key cap), "How to capture" (a popover with this profile's commands to copy) and a primary "New task" button (with an `N` key cap). Below, five columns on a 5-wide grid, at least 1,100 px wide (sideways scroll under that), each a rounded panel with a glowing dot in its colour (Inbox cyan, Ready teal, In progress purple, Review rose, Done green), its name, a count pill ("3 of 12" while searching, Done's full count although it shows 50), Ready's "top is next", and a `+` for quick add in Inbox (box at the top) and Ready (box at the bottom, where the card lands). Each list scrolls on its own.
- **Card (dense).** 8 px by 10 px padding, a hairline border, the title on at most two lines, then a mono meta row: a source chip (globe and domain, the Mac app's name, a terminal, a spark, the board icon), a notes marker when there is more to read than the card shows, the age (`3h`), `#id`, and at the right the claimant's initial in a ring of the agent's own colour with a soft expanding pulse and "12m left". Once the lease ends the ring turns amber and dashed, the pulse stops, the card's edge turns amber, and the text says "ended 4m ago"; the tooltip says another agent may take it over or it can go back to Ready. Review cards carry an amber "Question" or green "Result" tag. Hover lifts a card by 1 px with a shadow; focus shows the cyan ring.
- **Approve and Done.** Inbox cards have a teal "Approve" button when the card shows all of its text, else "Read and approve", which opens the drawer (so do `A`, Shift+Right and a drop on Ready: such a card slides back and the drawer says why); Review cards have a green "Done" and a plain "Back to Ready". A click moves the card at once (it slides there by FLIP) while a thin shimmer under it says the CLI is working; Done adds a check that pops on the card and a green glow. A refusal slides it back, shakes it, and shows a red toast quoting the CLI (plus "open it from the Dock or Finder" for `operator_only`).
- **Drag.** Press and move 5 px: the card stays as a dashed, faded placeholder, a tilted ghost with the title follows the pointer, the column under it glows in its colour, a 3 px line marks the landing place, and lists or the board scroll near their edges. Release drops it next to a shown neighbour; Escape or leaving the window cancels; a press on a card's button never drags; a short press opens the drawer.
- **Keyboard.** Tab reaches each card; Enter opens; Shift+Left/Right moves it a column; Alt+Up/Down reorders; `A` approves an Inbox card; arrows move the focus; `N` opens quick add; `/` focuses search; Escape closes the drawer (or first undoes an edit in progress). A polite live region says each move ("Moved #12 to Ready"), and why one could not happen.
- **Drawer.** Slides in from the right over the board (440 px): `#id` and a status pill, the title as a large inline field (Enter saves, Escape reverts, at most 200 characters), the source link, when it was added, the claim; notes as safe markdown with an Edit button (plain text, Cmd or Ctrl+Enter saves); Approve with "read it before you approve", a row of five "Move to" buttons, Archive; the history as a timeline of initials (an agent's question in an amber frame); a comment box.
- **Live.** Changes by anyone show within about two seconds; another actor's move becomes a neutral note bottom left ("claude-code#a3f9 finished #12"), at most three, six seconds each, a click opens the card; archiving offers Undo there.
- **States.** First load: shimmering card skeletons in the five columns. Failure: a centred message with the CLI's text and "Try again"; a later failure keeps the last board under a quiet banner. Empty columns say how they fill (Inbox and Ready in the spec's words). Opened from an agent's shell: an amber banner, no action buttons, no drag, no quick add.
- **Motion and theme.** Enter scale and fade 200 ms, FLIP 220 ms, drawer slide 200 ms, shake 420 ms, Done glow 900 ms (kept through the read that confirms it), pulse 2.2 s, eased out, never bouncing; all off under reduced motion (FLIP reads the preference when the page mounts), paused while the window is hidden. Dark only, all colours through the app's variables.

## File structure

Create:
- `wails-app/app_tasks.go`: the bindings and `taskCLI`. Test: `wails-app/app_tasks_test.go`.
- `wails-app/app_tasks_watch.go`: the watcher and `TaskPulse`. Test: `wails-app/app_tasks_watch_test.go`.
- `wails-app/frontend/src/services/tasks.js`: binding wrappers and `onTasksChanged`. Test: `services/tasks.test.js`.
- `wails-app/frontend/src/lib/taskModel.js`: pure logic. Test: `lib/taskModel.test.js`.
- `wails-app/frontend/src/pages/Tasks.jsx`: the page. Test: `pages/Tasks.render.test.jsx`.
- `wails-app/frontend/src/pages/tasks/`: `useTasksBoard.js`, `useCardDrag.js`, `useFlip.js`, `useMarks.js`, `Board.jsx`, `Column.jsx`, `Card.jsx`, `QuickAdd.jsx`, `Drawer.jsx`, `ActivityToasts.jsx`, `HowToCapture.jsx`, `tasks.css`; tests `useTasksBoard.test.jsx`, `Card.render.test.jsx`, `Column.render.test.jsx`, `Board.render.test.jsx`, `Drawer.render.test.jsx`, `motion.test.jsx`, `ActivityToasts.render.test.jsx`.
- `wails-app/frontend/src/locales/tasksKeys.test.js`, `wails-app/frontend/src/navPages.test.js`.

Modify:
- `wails-app/app.go` (two fields, the start and the stop), `wails-app/app_profiles.go` (restart on switch).
- `wails-app/frontend/src/wailsjs/go/main/App.js` and `App.d.ts` (eleven entries).
- `wails-app/frontend/src/components/Sidebar.jsx` and `Sidebar.render.test.jsx`, `wails-app/frontend/src/App.jsx`, `wails-app/frontend/src/locales/en.json` and `es.json`.
- `AGENTS.md`, `CHANGELOG.md`, and spec §10 and §17 where the build differs (Task 12).

Tasks: 0 contract and setup; 1 bindings; 2 watcher and pulse; 3 JS bindings and service; 4a board model (moves, places, keys); 4b board model (search, changes, labels); 5 board hook; 6 locales; 7a card and the base CSS; 7b column and quick add; 8 drag and keyboard; 9 drawer; 10a motion hooks; 10b activity notes and how-to; 10c page; 11 sidebar badge and registration; 12 documents and verification; 13 screenshots. Tasks 1 and 2 (Go) need no frontend code and the frontend tasks need no Go code, so the two lines can go to two builders; the frontend tasks (3 to 11) build on each other in order. "Task 4", "Task 7" and "Task 10" in an Interfaces block mean all of their parts.

---

### Task 0: Confirm the contract and prepare the worktree

**Files:** none change. `npm ci` creates `wails-app/frontend/node_modules` and the build creates `wails-app/frontend/dist`, both ignored by git.

**Interfaces:**
- Consumes (P1, `internal/tasks`): `NewStore(*sql.DB) *Store`; `(*Store).Watch(ctx, profileID string, interval time.Duration, fn func(Change)) error` (it blocks, so the app runs it in a goroutine; it returns nil when `ctx` ends, an ErrInvalid error at once for a profile that does not exist, and an error that wraps ErrNotFound when the profile is deleted while it is watched); `Change{Rev int64; Counts Counts}`; `Counts{Inbox, Ready, InProgress, Review, Done, Stale int}`; `(*Store).Profile(ctx, profileID string) (Profile, error)` (ErrInvalid for an unknown profile); `(*Store).Rev(ctx, profileID) (int64, error)` and `(*Store).Counts(ctx, profileID) (Counts, error)` (neither checks the profile: an unknown one reads revision 0 and no cards); `(*Store).Board(ctx context.Context, profileID string, doneLimit int) (Board, error)` with `Board{Profile, Rev, Counts, Tasks map[Status][]Task}` (JSON `profile`, `rev`, `counts`, `tasks`; a column with no card is `[]`); `(*Store).Add(ctx, profileID string, in AddInput, actor Actor) (Task, bool, error)`; `Actor{Kind, Name}` with `Human`; `AddInput{Title string, ...}`. From `internal/orgsign`: `AgentContextMarker() string`, `AgentContextMarkers() []string`.
- Consumes (P1 CLI, as the app calls it): `task board --done-limit N` (operator-only; the app reads the same document in process, Task 1), `task show ID`, `task add [--source app] [--ready] [--notes=TEXT] [--stdin] -- TITLE`, `task edit ID [--title=T] [--notes=N]`, `task move ID STATUS [--before ID|--after ID|--top|--bottom]`, `task approve ID... [--top]`, `task archive ID...`, `task unarchive ID...`, `task comment ID -- TEXT`; JSON documents `{"profile","task"}`, `{"profile","tasks"}`, `{"profile","task","events"}`, `{"profile","created","task"}`, and the board `{"profile","rev","counts","tasks":{"inbox":[],"ready":[],"in_progress":[],"review":[],"done":[]}}`; a task is `{id, profile_id, title, notes, status, position, source:{kind,url,title,app}, claim:{by,until,stale}|null, last_event:{actor,kind,at}|null, created_at, updated_at}`; an event is `{id, at, actor, kind, from_status, to_status, note}`; the operator's actor label is `"you"`; errors are `{"error","code"}` on stdout with exit 2 or 3.
- Produces: nothing. If anything below differs from this plan, spec section 6 wins: stop and tell the lead which line differed and what the code says.

- [ ] **Step 1: The branch holds P1**

Run (separate calls): `git branch --show-current`, then `git merge-base --is-ancestor feat/tasks-board HEAD`, then `ls internal/tasks`, then `ls cmd/monoagentcli | grep '^task'`.
Expected: `feat/tasks-board-gui`; the second exits 0; `internal/tasks` lists `claims.go clean.go doc.go model.go ops.go read.go store.go watch.go` (and tests); the CLI lists `task.go task_agent.go task_ops.go task_read.go` (and tests).

- [ ] **Step 2: The package's names**

Run each: `go doc ./internal/tasks Store.Watch`, `go doc ./internal/tasks Store.Profile`, `go doc ./internal/tasks Change`, `go doc ./internal/tasks Store.Rev`, `go doc ./internal/tasks Store.Counts`, `go doc ./internal/tasks Store.Board`, `go doc ./internal/tasks Board`, `go doc ./internal/tasks Counts`, `go doc ./internal/tasks Store.Add`, `go doc ./internal/tasks NewStore`, `go doc ./internal/tasks Actor`, `go doc ./internal/orgsign AgentContextMarker`, `go doc ./internal/orgsign AgentContextMarkers`, then `grep -n 'reservedNames' internal/tasks/store.go`.
Expected: the signatures in the Interfaces block above, word for word; `reservedNames` lists `you`, `agent`, `capture`, chrome and os (no agent can call itself `you`, so an event by `you` is always the operator's, which Tasks 4b and 9 rely on).

- [ ] **Step 3: The frontend's packages and a first build**

Run: `df -h /Users/morteza/Desktop/monoes` (expected: more than 3 GB available; if not, stop and tell the lead), `node --version` (expected: 22.22 or later in 22, 24.15 or later in 24, or 26 and up: the lock's engines) and `npm --version` (expected: 12 or later; older npm hits npm/cli#7622 in `npm ci`, which is why CI installs npm 12), then `npm --prefix wails-app/frontend ci`, then `npm --prefix wails-app/frontend run build`, then `ls wails-app/frontend/dist`, then `git status --short`.
Expected: `ci` and `build` succeed; `dist` holds `index.html`; `git status` shows no new file (both folders are ignored). If `npm ci` fails, stop and report its output: do not use `npm install`, which rewrites the lock file.

- [ ] **Step 4: Every icon this plan imports exists**

Run: `grep -cE 'declare const (SquareKanban|Search|Plus|X|Check|CheckCheck|Undo2|Globe|AppWindow|Terminal|Sparkles|Bot|MessageCircleQuestionMark|CircleCheck|CircleQuestionMark|Inbox|ListTodo|LoaderCircle|ExternalLink|Archive|Pencil|Send|Copy|TriangleAlert|NotebookText|BookOpen): ' wails-app/frontend/node_modules/lucide-react/dist/lucide-react.d.ts`
Expected: `26`. A smaller number: find the missing name with one grep each and tell the lead (do not substitute an icon silently).

- [ ] **Step 5: The CLI as the app will call it**

Run `mktemp -d` and write down the directory it prints; below it is `T`. Then, as separate calls, with `T` written out in full:
1. `go build -o T/monoagentcli ./cmd/monoagentcli`
2. `mkdir T/home`
3. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task add --source app --notes=--help -- "-x first"` — expected: exit 0, `"created":true`, the task's `"title":"-x first"`, `"notes":"--help"`, `"status":"inbox"`, `"source":{"kind":"app",…}`, `"claim":null`, `"last_event":{"actor":"you","kind":"created",…}`.
4. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task add --source app --ready -- second` — expected: `"status":"ready"`.
5. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task add --source app -- third` (id 3, Inbox).
6. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task board --done-limit 50` — expected: top-level keys `profile` (`id`, `name`), `rev` (a number), `counts` (`inbox`, `ready`, `in_progress`, `review`, `done`, `stale`), `tasks` with exactly the five keys `inbox`, `ready`, `in_progress`, `review`, `done`, each an array (empty ones `[]`).
7. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task move 1 ready --before 2` — expected: `{"profile":…,"task":{"id":1,…,"status":"ready",…}}`.
8. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task edit 1 --title=--renamed` — expected: title `--renamed`.
9. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task comment 1 -- -looks odd` — expected: exit 0, `"last_event":{"actor":"you","kind":"comment",…}`.
10. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task show 1` — expected: `{"profile","task","events"}`; each event has `id`, `at`, `actor`, `kind`, `from_status`, `to_status`, `note`.
11. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli --profile default --json task approve 3` then `… task archive 3` then `… task unarchive 3` — expected: `{"profile","tasks":[…]}` each time; after `unarchive` task 3 is `ready` again.
12. `env -i HOME=T/home PATH=/usr/bin:/bin CLAUDECODE=1 T/monoagentcli --profile default --json task move 1 inbox` — expected: exit 3, stdout `{"code":"operator_only","error":"CLAUDECODE is set, …"}`.
13. `env -i HOME=T/home PATH=/usr/bin:/bin CLAUDECODE=1 T/monoagentcli --profile default --json task add --source app -- x` — expected: exit 3, code `invalid_input` (this is why the app goes read-only under a marker, not only on `operator_only`).
14. `env -i HOME=T/home PATH=/usr/bin:/bin T/monoagentcli task add --help` — expected: the `--source` line names `app` (the lead passed this on to P1). If it does not, say so in your report and go on: step 3 already showed the CLI accepts it.
15. `env -i HOME=T/home PATH=/usr/bin:/bin CLAUDECODE=1 T/monoagentcli --profile default --json task board` — expected: exit 3, code `operator_only` (`board` is operator-only, which is why `TaskBoard` reads in process, Task 1).

Leave `T` in place (it is a temporary directory; no `rm -r`). Nothing to commit in this task.

### Task 1: The bindings

**Files:**
- Create: `wails-app/app_tasks.go`
- Test: `wails-app/app_tasks_test.go`

**Interfaces:**
- Consumes: `tasks.NewStore(*sql.DB)`, `(*tasks.Store).Board(ctx, profileID string, doneLimit int) (tasks.Board, error)` and `(*tasks.Store).Add` (tests), the `tasks.Board` document (Task 0 checks them); `findMonoAgentCLI() (string, error)`, `hideWindow(*exec.Cmd)`, `cliResultJSON(cliBin string, stdout []byte, runErr error) string` (stdout verbatim on success; on failure the CLI's own `{"error",…}` stdout when it printed one), `aiError(err error) string`, `codedError{msg, code string}` (`aiError(&codedError{...})` gives `{"code","error"}`), `(*App).getActiveProfileID()`, `a.ctx`; test helpers `fakeCLI(t, body string) string` (`app_health_stream_test.go`, `!windows`), `loggedArgs(t, path string) []string` (`app_hil_test.go`), `newTestApp(t) *App` (`app_workflows_test.go`); `orgsign.AgentContextMarker()`, `orgsign.AgentContextMarkers()`.
- Produces (bound to the frontend; Task 3 writes their JS entries):
  - `(*App).TaskBoard(doneLimit int) string`
  - `(*App).TaskShow(id int64) string`
  - `(*App).TaskAdd(spec string) string` (spec JSON `{"title","notes","text","ready"}`)
  - `(*App).TaskEdit(id int64, spec string) string` (spec JSON `{"title"?, "notes"?}`)
  - `(*App).TaskMove(id int64, status, where string, ref int64) string` (where `""`, `top`, `bottom`, `before`, `after`)
  - `(*App).TaskApprove(ids []int64, top bool) string`, `(*App).TaskArchive(ids []int64) string`, `(*App).TaskUnarchive(ids []int64) string`
  - `(*App).TaskComment(id int64, text string) string`
  - `(*App).TaskAgentShell() string`
  - unexported: `taskCLI(stdin string, args ...string) string`, `taskRefusal(msg string) string`, `taskIDArgs`, `taskBoardStatuses`, `taskCLITimeout`, `boardDoneLimit` (50), `boardLimit(n int) int`, `maxArgNotes` (30,000), `notesTooLong`; test helper `addTestTask(t, a, profileID, title)` (Task 2 uses it too).
- `TaskBoard` reads the board in process (`Store.Board`, one snapshot) and returns the `tasks.Board` document, the same one `task board --json` prints, or `{"error"}`: P1 made `task board` operator-only, and an app started from an agent's shell must still show its board (Ruling). Every other binding but `TaskAgentShell` runs the CLI and returns its stdout verbatim, or `{"code":"invalid_input","error":…}` when it refuses its input before running the CLI.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/app_tasks_test.go`:

```go
//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/tasks"
)

// addTestTask adds a task to a profile's board in process, as the operator.
func addTestTask(t *testing.T, a *App, profileID, title string) {
	t.Helper()
	_, _, err := tasks.NewStore(a.db).Add(context.Background(), profileID, tasks.AddInput{Title: title}, tasks.Actor{Kind: tasks.Human})
	if err != nil {
		t.Fatal(err)
	}
}

// taskCLIFake installs a monoagentcli stand-in that logs each call as one
// line with every argument followed by "|" (so where an argument ends shows),
// saves its standard input, prints out and exits with code. It returns the
// paths of the log and of the saved standard input.
func taskCLIFake(t *testing.T, out string, code int) (argsLog, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	stdinFile = filepath.Join(dir, "stdin")
	outFile := filepath.Join(dir, "out.json")
	if err := os.WriteFile(outFile, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t,
		"for a in \"$@\"; do printf '%s|' \"$a\"; done >> '"+argsLog+"'\n"+
			"echo >> '"+argsLog+"'\n"+
			"cat > '"+stdinFile+"'\n"+
			"cat '"+outFile+"'\n"+
			"exit "+strconv.Itoa(code)+"\n"))
	return argsLog, stdinFile
}

func newTaskTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	return a
}

// Every binding that runs the CLI runs `task …` for the active profile with
// its arguments exactly so: user text after "--" or in the --flag=value form,
// so text that starts with a dash is never read as a flag. Each returns
// stdout verbatim.
func TestTaskBindingsPassTheirArgumentsExactly(t *testing.T) {
	log, _ := taskCLIFake(t, `{"ok":true}`, 0)
	a := newTaskTestApp(t)
	for i, got := range []string{
		a.TaskShow(12),
		a.TaskAdd(`{"title":"-x fix the bug","notes":"--help me"}`),
		a.TaskAdd(`{"title":"urgent","ready":true}`),
		a.TaskEdit(12, `{"title":"--new"}`),
		a.TaskEdit(12, `{"notes":""}`),
		a.TaskMove(12, "ready", "", 0),
		a.TaskMove(12, "done", "top", 0),
		a.TaskMove(12, "review", "before", 9),
		a.TaskMove(12, "in_progress", "after", 9),
		a.TaskApprove([]int64{3, 4}, false),
		a.TaskApprove([]int64{3}, true),
		a.TaskArchive([]int64{5}),
		a.TaskUnarchive([]int64{5, 6}),
		a.TaskComment(12, "-looks odd"),
	} {
		if got != `{"ok":true}` {
			t.Fatalf("call %d did not return the CLI's stdout verbatim: %q", i, got)
		}
	}
	p := "--profile|work|--json|task|"
	want := []string{
		p + "show|12|",
		p + "add|--source|app|--notes=--help me|--|-x fix the bug|",
		p + "add|--source|app|--ready|--|urgent|",
		p + "edit|12|--title=--new|",
		p + "edit|12|--notes=|",
		p + "move|12|ready|",
		p + "move|12|done|--top|",
		p + "move|12|review|--before|9|",
		p + "move|12|in_progress|--after|9|",
		p + "approve|3|4|",
		p + "approve|3|--top|",
		p + "archive|5|",
		p + "unarchive|5|6|",
		p + "comment|12|--|-looks odd|",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Text of several lines goes on standard input: the CLI makes its first
// line the title and all of it the notes (spec 4.6).
func TestTaskAddSendsTextOnStandardInput(t *testing.T) {
	log, stdin := taskCLIFake(t, `{"created":true}`, 0)
	a := newTaskTestApp(t)
	text := "Reply to Sam\n--about the invoice"
	spec, _ := json.Marshal(map[string]any{"text": text, "ready": true})
	if got := a.TaskAdd(string(spec)); got != `{"created":true}` {
		t.Fatalf("TaskAdd = %q", got)
	}
	if got := loggedArgs(t, log); len(got) != 1 || got[0] != "--profile|work|--json|task|add|--source|app|--ready|--stdin|" {
		t.Fatalf("CLI call: %q", got)
	}
	if raw, err := os.ReadFile(stdin); err != nil || string(raw) != text {
		t.Fatalf("standard input %q (%v), want %q", raw, err, text)
	}
}

// Input no command could accept is refused here, as {"error","code"}, and
// the CLI never runs.
func TestTaskBindingsRefuseBadInputWithoutRunningTheCLI(t *testing.T) {
	log, _ := taskCLIFake(t, `{"ok":true}`, 0)
	a := newTaskTestApp(t)
	long := `{"notes":"` + strings.Repeat("x", maxArgNotes+1) + `"}`
	longAdd := `{"title":"a","notes":"` + strings.Repeat("x", maxArgNotes+1) + `"}`
	for name, got := range map[string]string{
		"a title of spaces":   a.TaskAdd(`{"title":"  "}`),
		"title and text":      a.TaskAdd(`{"title":"a","text":"b"}`),
		"notes with text":     a.TaskAdd(`{"text":"b","notes":"n"}`),
		"long notes on add":   a.TaskAdd(longAdd),
		"not JSON":            a.TaskAdd(`{`),
		"an edit of nothing":  a.TaskEdit(3, `{}`),
		"edit id 0":           a.TaskEdit(0, `{"title":"x"}`),
		"notes over the cap":  a.TaskEdit(3, long),
		"show id -1":          a.TaskShow(-1),
		"move to archived":    a.TaskMove(3, "archived", "", 0),
		"an unknown place":    a.TaskMove(3, "ready", "middle", 0),
		"before no card":      a.TaskMove(3, "ready", "before", 0),
		"after itself":        a.TaskMove(3, "ready", "after", 3),
		"approve nothing":     a.TaskApprove(nil, false),
		"archive id 0":        a.TaskArchive([]int64{0}),
		"unarchive nothing":   a.TaskUnarchive([]int64{}),
		"a comment of spaces": a.TaskComment(3, " \n"),
	} {
		var doc struct{ Error, Code string }
		if err := json.Unmarshal([]byte(got), &doc); err != nil || doc.Error == "" || doc.Code != "invalid_input" {
			t.Errorf("%s: %q, want an invalid_input refusal", name, got)
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the CLI ran for refused input: %v", loggedArgs(t, log))
	}
}

// A refusal comes back as the CLI printed it, code and all, so the page can
// tell operator_only from claimed or not_found.
func TestTaskBindingsReturnTheCLIRefusalVerbatim(t *testing.T) {
	refusal := `{"code":"operator_only","error":"CLAUDECODE is set, so an agent is running this command: only the operator can move a task"}`
	taskCLIFake(t, refusal, 3)
	a := newTaskTestApp(t)
	if got := a.TaskMove(7, "ready", "", 0); got != refusal {
		t.Fatalf("TaskMove = %q", got)
	}
}

// The board is read in process, in the document `task board --json` prints,
// because the CLI refuses `task board` to an agent-driven caller: under an
// inherited CLAUDECODE the app still shows its board, while an action still
// runs the CLI, whose guard refuses it, and the page shows the banner.
func TestTaskBoardIsReadInProcessEvenUnderAnInheritedMarker(t *testing.T) {
	refusal := `{"code":"operator_only","error":"CLAUDECODE is set, so an agent is running this command: only the operator can move a task"}`
	log, _ := taskCLIFake(t, refusal, 3)
	t.Setenv("CLAUDECODE", "1")
	a := newTestApp(t)
	a.ctx = context.Background()
	addTestTask(t, a, "default", "first")
	addTestTask(t, a, "default", "second")
	raw := a.TaskBoard(0)
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil || len(top) != 4 || top["profile"] == nil || top["rev"] == nil || top["counts"] == nil || top["tasks"] == nil {
		t.Fatalf("TaskBoard = %s (%v), want the four keys profile, rev, counts, tasks", raw, err)
	}
	var doc struct {
		Profile struct{ ID string }          `json:"profile"`
		Rev     int64                        `json:"rev"`
		Counts  struct{ Inbox int }          `json:"counts"`
		Tasks   map[string][]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Profile.ID != "default" || doc.Rev != 2 || doc.Counts.Inbox != 2 || len(doc.Tasks) != 5 || len(doc.Tasks["inbox"]) != 2 || doc.Tasks["done"] == nil {
		t.Fatalf("board = %+v (empty columns must be [], never null)", doc)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the board read ran the CLI: %v", loggedArgs(t, log))
	}
	if got := a.TaskMove(1, "ready", "", 0); got != refusal {
		t.Fatalf("a move under the marker = %q, want the CLI's operator_only refusal", got)
	}
	if got := a.TaskAgentShell(); got != "CLAUDECODE" {
		t.Fatalf("TaskAgentShell = %q: the page would not show the read-only banner", got)
	}
}

// The app never asks for every Done card: a limit that is not positive is
// the board's 50 (Store.Board reads 0 as all of them).
func TestTaskBoardLimitIsNeverEveryDoneCard(t *testing.T) {
	for in, want := range map[int]int{0: 50, -3: 50, 10: 10, 50: 50} {
		if got := boardLimit(in); got != want {
			t.Errorf("boardLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

// The app's children inherit its environment: the binding names the agent
// marker (or MONOAGENT_ACTOR) the CLI would see, so the page can go
// read-only before anything is refused.
func TestTaskAgentShellNamesTheMarkerTheAppInherited(t *testing.T) {
	for _, m := range orgsign.AgentContextMarkers() {
		t.Setenv(m, "")
	}
	t.Setenv("MONOAGENT_ACTOR", "")
	a := newTaskTestApp(t)
	if got := a.TaskAgentShell(); got != "" {
		t.Fatalf("no marker: %q", got)
	}
	t.Setenv("MONOAGENT_ACTOR", "bot")
	if got := a.TaskAgentShell(); got != "MONOAGENT_ACTOR" {
		t.Fatalf("MONOAGENT_ACTOR set: %q", got)
	}
	t.Setenv("CLAUDECODE", "1")
	if got := a.TaskAgentShell(); got != "CLAUDECODE" {
		t.Fatalf("CLAUDECODE set: %q", got)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go -C wails-app test -run 'TestTaskBindings|TestTaskAdd|TestTaskAgentShell|TestTaskBoard' -count=1 .`
Expected: FAIL to compile (`a.TaskBoard undefined` and the rest).

- [ ] **Step 3: Write `wails-app/app_tasks.go`**

```go
// wails-app/app_tasks.go
//
// The Tasks tab's bindings (docs/mastermind/specs/2026-10-05-task-board-design.md
// §10). Each action runs `monoagentcli --profile <active> --json task …` and
// returns its stdout verbatim: the document, or the CLI's {"error","code"} on
// a refusal, so the page can tell operator_only from claimed or not_found.
// The board is the one read done in process (TaskBoard), as the watcher
// (app_tasks_watch.go) reads the revision: the CLI refuses `task board` to an
// agent-driven caller, and an app started from an agent's shell must still
// show its board, read-only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/tasks"
)

// taskCLITimeout bounds one task command: each is a few small queries.
const taskCLITimeout = 30 * time.Second

// boardDoneLimit is how many Done cards the board shows (spec §10).
const boardDoneLimit = 50

// maxArgNotes caps the notes the app passes on the command line, which
// Windows limits to 32,767 characters in all; longer notes are saved with
// the CLI in a terminal.
const maxArgNotes = 30000

// notesTooLong is the refusal for notes over maxArgNotes, in TaskAdd and TaskEdit.
const notesTooLong = "these notes are too long to save from the app (over 30,000 characters); save them in a terminal with monoagentcli task"

// taskBoardStatuses are the columns a card can be moved to; archiving has
// its own binding.
var taskBoardStatuses = map[string]bool{"inbox": true, "ready": true, "in_progress": true, "review": true, "done": true}

// taskRefusal is the {"error","code"} document of input refused before the
// CLI runs.
func taskRefusal(msg string) string {
	return aiError(&codedError{msg: msg, code: "invalid_input"})
}

// taskCLI runs `monoagentcli --profile <active> --json task <args…>` with
// stdin when it is not empty, and returns stdout verbatim or the {"error"}
// shape (cliResultJSON keeps the CLI's own error document and its code).
func (a *App) taskCLI(stdin string, args ...string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, taskCLITimeout)
	defer cancel()
	full := append([]string{"--profile", a.getActiveProfileID(), "--json", "task"}, args...)
	cmd := exec.CommandContext(ctx, cliBin, full...)
	hideWindow(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, runErr := cmd.Output()
	return cliResultJSON(cliBin, out, runErr)
}

// TaskBoard is the active profile's whole board, read in one snapshot with
// Store.Board and returned as the document `task board --json` prints. It
// skips the CLI because `task board` refuses an agent-driven caller (the
// whole board would hand an agent every unreviewed Inbox card, spec §4.1),
// while an app started from an agent's shell must still show its board,
// read-only. Every action below runs the CLI, whose guard refuses it there.
func (a *App) TaskBoard(doneLimit int) string {
	if a.db == nil {
		return aiError(errors.New("the database is not open yet"))
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	b, err := tasks.NewStore(a.db).Board(ctx, a.getActiveProfileID(), boardLimit(doneLimit))
	if err != nil {
		return aiError(err)
	}
	out, err := json.Marshal(b)
	if err != nil {
		return aiError(err)
	}
	return string(out)
}

// boardLimit is the Done cards a board read asks for: Store.Board reads 0 as
// every Done card, which the app never wants, so a limit that is not
// positive is boardDoneLimit.
func boardLimit(n int) int {
	if n <= 0 {
		return boardDoneLimit
	}
	return n
}

// TaskShow is `task show ID`: one task with its history.
func (a *App) TaskShow(id int64) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	return a.taskCLI("", "show", strconv.FormatInt(id, 10))
}

// taskAddSpec is TaskAdd's argument: a title (with optional notes), or text
// whose first line the CLI makes the title, and whether the task goes
// straight to Ready (the operator's choice).
type taskAddSpec struct {
	Title string `json:"title"`
	Notes string `json:"notes"`
	Text  string `json:"text"`
	Ready bool   `json:"ready"`
}

// TaskAdd is `task add --source app`: a title as one argument after "--",
// so a title that starts with a dash stays a title, or text on standard
// input with --stdin.
func (a *App) TaskAdd(spec string) string {
	var s taskAddSpec
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return taskRefusal("the task is not valid JSON: " + err.Error())
	}
	hasTitle, hasText := strings.TrimSpace(s.Title) != "", strings.TrimSpace(s.Text) != ""
	switch {
	case !hasTitle && !hasText:
		return taskRefusal("a task needs a title")
	case hasTitle && hasText:
		return taskRefusal("give a title or a text, not both")
	case hasText && s.Notes != "":
		return taskRefusal("give notes or text, not both")
	case len(s.Notes) > maxArgNotes:
		return taskRefusal(notesTooLong)
	}
	args := []string{"add", "--source", "app"}
	if s.Ready {
		args = append(args, "--ready")
	}
	if hasText {
		return a.taskCLI(s.Text, append(args, "--stdin")...)
	}
	if s.Notes != "" {
		args = append(args, "--notes="+s.Notes)
	}
	return a.taskCLI("", append(args, "--", s.Title)...)
}

// taskEditSpec is TaskEdit's argument: a field left out stays as it is, and
// empty notes clear them.
type taskEditSpec struct {
	Title *string `json:"title"`
	Notes *string `json:"notes"`
}

// TaskEdit is `task edit ID --title=T --notes=N`, in the = form so a value
// that starts with a dash stays a value.
func (a *App) TaskEdit(id int64, spec string) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	var s taskEditSpec
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return taskRefusal("the change is not valid JSON: " + err.Error())
	}
	args := []string{"edit", strconv.FormatInt(id, 10)}
	if s.Title != nil {
		args = append(args, "--title="+*s.Title)
	}
	if s.Notes != nil {
		if len(*s.Notes) > maxArgNotes {
			return taskRefusal(notesTooLong)
		}
		args = append(args, "--notes="+*s.Notes)
	}
	if len(args) == 2 {
		return taskRefusal("nothing to change: give a title or notes")
	}
	return a.taskCLI("", args...)
}

// TaskMove is `task move ID STATUS` with a place in the column: where is ""
// (the column's default), "top", "bottom", "before" or "after", the last two
// with ref, another card of that column.
func (a *App) TaskMove(id int64, status, where string, ref int64) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	if !taskBoardStatuses[status] {
		return taskRefusal("unknown column " + strconv.Quote(status))
	}
	args := []string{"move", strconv.FormatInt(id, 10), status}
	switch where {
	case "":
	case "top", "bottom":
		args = append(args, "--"+where)
	case "before", "after":
		if ref <= 0 || ref == id {
			return taskRefusal(where + " needs another card of the column")
		}
		args = append(args, "--"+where, strconv.FormatInt(ref, 10))
	default:
		return taskRefusal("unknown place " + strconv.Quote(where))
	}
	return a.taskCLI("", args...)
}

// taskIDArgs renders ids as arguments; false for an empty list or an id that
// is not positive.
func taskIDArgs(ids []int64) ([]string, bool) {
	if len(ids) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, false
		}
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out, true
}

// TaskApprove is `task approve ID…`: Inbox to the bottom of Ready, or the
// top with top.
func (a *App) TaskApprove(ids []int64, top bool) string {
	args, ok := taskIDArgs(ids)
	if !ok {
		return taskRefusal("give the ids of the tasks to approve")
	}
	args = append([]string{"approve"}, args...)
	if top {
		args = append(args, "--top")
	}
	return a.taskCLI("", args...)
}

// TaskArchive is `task archive ID…`.
func (a *App) TaskArchive(ids []int64) string {
	args, ok := taskIDArgs(ids)
	if !ok {
		return taskRefusal("give the ids of the tasks to archive")
	}
	return a.taskCLI("", append([]string{"archive"}, args...)...)
}

// TaskUnarchive is `task unarchive ID…`: each back to the column it left.
func (a *App) TaskUnarchive(ids []int64) string {
	args, ok := taskIDArgs(ids)
	if !ok {
		return taskRefusal("give the ids of the tasks to restore")
	}
	return a.taskCLI("", append([]string{"unarchive"}, args...)...)
}

// TaskComment is the operator's `task comment ID -- TEXT`.
func (a *App) TaskComment(id int64, text string) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	if strings.TrimSpace(text) == "" {
		return taskRefusal("a comment needs text")
	}
	return a.taskCLI("", "comment", strconv.FormatInt(id, 10), "--", text)
}

// TaskAgentShell names the agent-context marker in this app's environment,
// or MONOAGENT_ACTOR, or "". The CLI the app runs inherits it and then
// refuses every operator action (spec D7, §10 Errors), so the page shows the
// board read-only and says to open MonoAgent from the Dock or Finder.
func (a *App) TaskAgentShell() string {
	if m := orgsign.AgentContextMarker(); m != "" {
		return m
	}
	if strings.TrimSpace(os.Getenv("MONOAGENT_ACTOR")) != "" {
		return "MONOAGENT_ACTOR"
	}
	return ""
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `gofmt -l wails-app/app_tasks.go wails-app/app_tasks_test.go` then `go -C wails-app vet .` then `go -C wails-app test -run 'TestTaskBindings|TestTaskAdd|TestTaskAgentShell|TestTaskBoard' -count=1 .`
Expected: `gofmt` prints nothing (if it lists a file, run `gofmt -w` on that file: hand-aligned literals and struct tags are the usual cause), vet clean, PASS (seven tests).

- [ ] **Step 5: Check that each rule is pinned**

Make each change below, run the same test command, see the named test FAIL, then undo the change:
1. `TaskAdd`: `"--", s.Title` becomes `s.Title` → `TestTaskBindingsPassTheirArgumentsExactly`.
2. `TaskEdit`: `"--title="+*s.Title` becomes two arguments `"--title", *s.Title` → the same test.
3. `TaskMove`: drop `|| ref == id` → `TestTaskBindingsRefuseBadInputWithoutRunningTheCLI`.
4. `TaskAgentShell`: drop the `MONOAGENT_ACTOR` branch → `TestTaskAgentShellNamesTheMarkerTheAppInherited`.
5. `boardLimit`: `n <= 0` becomes `n < 0` → `TestTaskBoardLimitIsNeverEveryDoneCard` (0 would read every Done card).
6. `TaskEdit`: drop the `maxArgNotes` check → `TestTaskBindingsRefuseBadInputWithoutRunningTheCLI`; then the same in `TaskAdd` → the same test.
7. `TaskBoard`: replace its body after the `a.db` check with `return a.taskCLI("", "board", "--done-limit", strconv.Itoa(boardLimit(doneLimit)))` → `TestTaskBoardIsReadInProcessEvenUnderAnInheritedMarker` (the CLI's refusal comes back instead of the board).

- [ ] **Step 6: Commit**

```
git add wails-app/app_tasks.go wails-app/app_tasks_test.go
```
then
```
git commit -m "feat(tasks): desktop bindings for the task board, through the CLI" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 2: The board watcher and the badge's first value

**Files:**
- Create: `wails-app/app_tasks_watch.go`
- Modify: `wails-app/app.go` (two fields in `App`, one line in `startup`, one in `shutdown`), `wails-app/app_profiles.go` (one line in `SwitchProfile`)
- Test: `wails-app/app_tasks_watch_test.go`

**Interfaces:**
- Consumes: `tasks.NewStore`, `(*Store).Watch`, `(*Store).Profile`, `(*Store).Rev`, `(*Store).Counts`, `(*Store).Add`, `tasks.Change`, `tasks.AddInput`, `tasks.Actor`, `tasks.Human` (Task 0 confirmed them); `folderEventFunc` (`func(name string, data map[string]interface{})`) and `(*App).emitFolderEvent` (emits once the Wails runtime is up) from `app_documents_watch.go`; `(*App).emitLog(source, level, message string)` (adds the entry to the app's log, and emits it once the Wails runtime is up) and `(*App).GetLogs() []LogEntry` from `app.go`; test helpers `eventRecorder` (with `emit` and `snapshot()`) and `waitEvents(t, rec, n)` from `app_documents_watch_test.go` (`!windows`), `newTestApp(t)`, and Task 1's `addTestTask(t, a, profileID, title)` (`app_tasks_test.go`).
- Produces:
  - `(*App).restartTaskWatcher()`, `(*App).stopTaskWatcher()`
  - `(*App).startTaskWatcher(profileID string, interval time.Duration, emit folderEventFunc) (stop func())`: `stop` returns only once the watcher's goroutine has ended, so nothing of the old profile is emitted after a restart. When `Watch` returns an error (the profile is not there: it never was, or it was deleted while watched) the goroutine writes one warning to the app's log and ends; it does not start over.
  - `taskPulseData(profileID string, c tasks.Change) map[string]interface{}`: `{"profile_id", "rev" (int64), "inbox" (int), "review" (int)}`, the `tasks:changed` payload.
  - `(*App).TaskPulse() map[string]interface{}`: the same document for the active profile, read now; `{}` without a database, for a profile that is not there, or on a failed read.
  - `App` fields `taskWatchMu sync.Mutex`, `taskWatchStop func()`, `taskWatchProfile string` (the profile being watched, `""` when none).

- [ ] **Step 1: Write the failing tests**

Create `wails-app/app_tasks_watch_test.go`:

```go
//go:build !windows

package main

import (
	"strings"
	"testing"
	"time"
)

// waitTaskWarning waits until the app's log holds a warning that contains want.
func waitTaskWarning(t *testing.T, a *App, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range a.GetLogs() {
			if e.Level == "WARN" && strings.Contains(e.Message, want) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no warning holding %q in the log: %+v", want, a.GetLogs())
}

// The watcher reports the board once at the start and after each write,
// with the Inbox and Review counts the sidebar badge shows.
func TestTaskWatcherEmitsTheStartAndEachChange(t *testing.T) {
	a := newTestApp(t)
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("default", 10*time.Millisecond, rec.emit)
	defer stop()
	names, evts := waitEvents(t, rec, 1)
	if names[0] != "tasks:changed" || evts[0]["profile_id"] != "default" || evts[0]["rev"] != int64(0) || evts[0]["inbox"] != 0 || evts[0]["review"] != 0 {
		t.Fatalf("first event = %s %+v", names[0], evts[0])
	}
	addTestTask(t, a, "default", "first")
	_, evts = waitEvents(t, rec, 2)
	if evts[1]["rev"] != int64(1) || evts[1]["inbox"] != 1 {
		t.Fatalf("after an add: %+v", evts[1])
	}
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 2 {
		t.Fatalf("no write, no event: %v", names)
	}
}

// Once stop has returned, a write emits nothing: after a profile switch the
// page never hears the old profile's board.
func TestTaskWatcherIsSilentOnceStopped(t *testing.T) {
	a := newTestApp(t)
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("default", 10*time.Millisecond, rec.emit)
	waitEvents(t, rec, 1)
	stop()
	addTestTask(t, a, "default", "after the stop")
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 1 {
		t.Fatalf("a stopped watcher emitted: %v", names)
	}
}

// A watcher reports its own profile's board only (spec D9).
func TestTaskWatcherWatchesItsOwnProfile(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('work', 'Work')`); err != nil {
		t.Fatal(err)
	}
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("work", 10*time.Millisecond, rec.emit)
	defer stop()
	waitEvents(t, rec, 1)
	addTestTask(t, a, "default", "on the other board")
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 1 {
		t.Fatalf("a write to another profile's board emitted: %v", names)
	}
	addTestTask(t, a, "work", "on this board")
	_, evts := waitEvents(t, rec, 2)
	if evts[1]["profile_id"] != "work" || evts[1]["inbox"] != 1 {
		t.Fatalf("event = %+v", evts[1])
	}
}

// A watcher on a profile that does not exist ends at once and says so in the
// app's log: Watch returns an error for it (ErrInvalid), where a watcher that
// went on would report revision 0 and an empty board for ever. It emits
// nothing and does not start over, so a profile that appears later is not
// watched until the next restart. A profile deleted while it is watched ends
// Watch the same way, with an error that wraps ErrNotFound (P1's
// TestWatchEndsWhenItsProfileIsDeleted pins that); the branch here is the same.
func TestTaskWatcherEndsWithAWarningForAProfileThatDoesNotExist(t *testing.T) {
	a := newTestApp(t)
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("nobody", 10*time.Millisecond, rec.emit)
	waitTaskWarning(t, a, `unknown profile "nobody"`)
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('nobody', 'Nobody')`); err != nil {
		t.Fatal(err)
	}
	addTestTask(t, a, "nobody", "after the watcher ended")
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 0 {
		t.Fatalf("a watcher that had ended reported: %v", names)
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return for a watcher that had ended")
	}
}

// TaskPulse answers what the watcher would emit, read now: the badge asks
// once when it mounts, because the first event fired before it listened.
func TestTaskPulseReadsTheActiveBoard(t *testing.T) {
	a := newTestApp(t)
	addTestTask(t, a, "default", "one")
	addTestTask(t, a, "default", "two")
	got := a.TaskPulse()
	if got["profile_id"] != "default" || got["rev"] != int64(2) || got["inbox"] != 2 || got["review"] != 0 {
		t.Fatalf("TaskPulse = %+v", got)
	}
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('work', 'Work')`); err != nil {
		t.Fatal(err)
	}
	a.setActiveProfileID("work")
	if got := a.TaskPulse(); got["profile_id"] != "work" || got["inbox"] != 0 {
		t.Fatalf("for the active profile, which has no task: %+v", got)
	}
	// A profile that is not there has no board: {}, as for any failed read, not
	// revision 0 with no cards, which the badge would take for a real, empty
	// board (Rev and Counts do not check the profile; Profile does).
	a.setActiveProfileID("nobody")
	if got := a.TaskPulse(); got == nil || len(got) != 0 {
		t.Fatalf("for a profile that does not exist: %#v, want an empty map", got)
	}
	a.db = nil
	if got := a.TaskPulse(); got == nil || len(got) != 0 {
		t.Fatalf("without a database: %#v, want an empty map", got)
	}
}

// restartTaskWatcher watches the active profile (a switch must move the
// badge and the board to the new board) and stopTaskWatcher ends it.
func TestRestartAndStopTaskWatcher(t *testing.T) {
	a := newTestApp(t)
	a.restartTaskWatcher()
	if a.taskWatchStop == nil || a.taskWatchProfile != "default" {
		t.Fatalf("after a restart: stop set %v, profile %q", a.taskWatchStop != nil, a.taskWatchProfile)
	}
	a.setActiveProfileID("work")
	a.restartTaskWatcher()
	if a.taskWatchStop == nil || a.taskWatchProfile != "work" {
		t.Fatalf("after a switch: stop set %v, profile %q", a.taskWatchStop != nil, a.taskWatchProfile)
	}
	a.stopTaskWatcher()
	if a.taskWatchStop != nil || a.taskWatchProfile != "" {
		t.Fatal("the watcher survived stopTaskWatcher")
	}
	a.db = nil
	a.restartTaskWatcher()
	if a.taskWatchStop != nil {
		t.Fatal("a watcher started without a database")
	}
}

// stop waits for a report that is being emitted: once it returns, nothing
// of the old board can reach the page.
func TestTaskWatcherStopWaitsForAReportInFlight(t *testing.T) {
	a := newTestApp(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	stop := a.startTaskWatcher("default", 10*time.Millisecond, func(string, map[string]interface{}) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	<-entered // the first report is inside emit now
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while a report was still being emitted")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return once the report was done")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go -C wails-app test -run 'TestTaskWatcher|TestTaskPulse|TestRestartAndStopTaskWatcher' -count=1 .`
Expected: FAIL to compile (`a.startTaskWatcher undefined`, `a.taskWatchStop undefined`).

- [ ] **Step 3: Write `wails-app/app_tasks_watch.go`**

```go
// wails-app/app_tasks_watch.go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/tasks"
)

// restartTaskWatcher stops the running task board watcher and starts one on
// the active profile's board, at startup and on SwitchProfile (shutdown
// calls stopTaskWatcher), as restartDocumentWatcher does. The watcher reads
// the board revision, a primary-key read, every two seconds, and the counts
// when it moves (spec D12); tasks:changed {profile_id, rev, inbox, review}
// feeds the sidebar badge and makes the Tasks page read the board again.
func (a *App) restartTaskWatcher() {
	a.taskWatchMu.Lock()
	defer a.taskWatchMu.Unlock()
	if a.taskWatchStop != nil {
		a.taskWatchStop()
		a.taskWatchStop = nil
	}
	a.taskWatchProfile = ""
	if a.db == nil {
		return
	}
	profileID := a.getActiveProfileID()
	a.taskWatchStop = a.startTaskWatcher(profileID, 0, a.emitFolderEvent)
	a.taskWatchProfile = profileID
}

// stopTaskWatcher ends the watcher for good (shutdown).
func (a *App) stopTaskWatcher() {
	a.taskWatchMu.Lock()
	defer a.taskWatchMu.Unlock()
	if a.taskWatchStop != nil {
		a.taskWatchStop()
		a.taskWatchStop = nil
	}
	a.taskWatchProfile = ""
}

// startTaskWatcher watches profileID's board revision (interval <= 0 is the
// store's two seconds) and emits tasks:changed at the start and at each move.
// stop returns only once the watcher's goroutine has ended, and a report
// that races the stop is dropped, so no event of the old profile follows a
// restart. Watch returns an error only for a profile that is not there (it
// never was, or it was deleted while watched): the watcher then writes one
// warning to the app's log and ends. It does not start over, since the
// profile would still be missing; the next SwitchProfile or start begins a
// new one.
func (a *App) startTaskWatcher(profileID string, interval time.Duration, emit folderEventFunc) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	store := tasks.NewStore(a.db)
	go func() {
		defer close(done)
		err := store.Watch(ctx, profileID, interval, func(c tasks.Change) {
			if ctx.Err() != nil {
				return
			}
			emit("tasks:changed", taskPulseData(profileID, c))
		})
		if err != nil {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: task board watcher stopped: %v", profileID, err))
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// taskPulseData is the tasks:changed payload and TaskPulse's answer.
func taskPulseData(profileID string, c tasks.Change) map[string]interface{} {
	return map[string]interface{}{
		"profile_id": profileID,
		"rev":        c.Rev,
		"inbox":      c.Counts.Inbox,
		"review":     c.Counts.Review,
	}
}

// TaskPulse is the active profile's board revision with its Inbox and Review
// counts, read now in process the way the watcher reads them. The sidebar
// asks once when it mounts: the watcher's first tasks:changed fires during
// startup, before any page listens. {} without a database, for a profile
// that is not there, or on a failed read.
func (a *App) TaskPulse() map[string]interface{} {
	if a.db == nil {
		return map[string]interface{}{}
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	profileID := a.getActiveProfileID()
	store := tasks.NewStore(a.db)
	// Rev and Counts do not check the profile: an unknown one reads revision 0
	// and no cards, a board that never existed. Resolve it first, as Watch does.
	if _, err := store.Profile(ctx, profileID); err != nil {
		return map[string]interface{}{}
	}
	rev, err := store.Rev(ctx, profileID)
	if err != nil {
		return map[string]interface{}{}
	}
	counts, err := store.Counts(ctx, profileID)
	if err != nil {
		return map[string]interface{}{}
	}
	return taskPulseData(profileID, tasks.Change{Rev: rev, Counts: counts})
}
```

- [ ] **Step 4: Wire it into the App**

In `wails-app/app.go`, in the `App` struct, after the two image watcher lines:

```go
	imgWatchMu sync.Mutex
	imgWatcher *imagescan.Watcher // polls the active profile's whole folder for images; see restartImageWatcher
```
add:
```go

	taskWatchMu      sync.Mutex
	taskWatchStop    func() // stops the active profile's task board watcher; see restartTaskWatcher
	taskWatchProfile string // the profile that watcher watches
```

In `startup`, change
```go
	a.restartOrgWatcher()
	a.restartDocumentWatcher()
	a.restartImageWatcher()

	a.emitLog("SYSTEM", "INFO", "Mono Agent UI connected to "+a.dbPath)
```
to
```go
	a.restartOrgWatcher()
	a.restartDocumentWatcher()
	a.restartImageWatcher()
	a.restartTaskWatcher()

	a.emitLog("SYSTEM", "INFO", "Mono Agent UI connected to "+a.dbPath)
```

In `shutdown`, change
```go
	a.imgWatchMu.Unlock()

	a.stopRunningCmds()
```
to
```go
	a.imgWatchMu.Unlock()

	a.stopTaskWatcher()

	a.stopRunningCmds()
```

In `wails-app/app_profiles.go`, in `SwitchProfile`, change
```go
	a.restartOrgWatcher()
	a.restartDocumentWatcher()
	a.restartImageWatcher()
	a.emitLog("SYSTEM", "INFO", "Switched to profile: "+switched)
```
to
```go
	a.restartOrgWatcher()
	a.restartDocumentWatcher()
	a.restartImageWatcher()
	a.restartTaskWatcher()
	a.emitLog("SYSTEM", "INFO", "Switched to profile: "+switched)
```
`MoveProfileFolder` restarts the folder watchers only; the board does not depend on the folder, so leave it.

- [ ] **Step 5: Run the tests to see them pass**

Run: `gofmt -l wails-app/*.go` (not the whole folder: `frontend/node_modules` holds Go files of its own) then `go -C wails-app vet .` then `go -C wails-app test -run 'TestTaskWatcher|TestTaskPulse|TestRestartAndStopTaskWatcher|TestDocumentWatcher' -count=1 .`
Expected: nothing from `gofmt` (if it lists `app.go`, run `gofmt -w` on it: the struct block realigns), vet clean, PASS (seven new tests and the three document watcher tests).
Then, because the watcher's goroutine and its stop are this phase's only concurrency (spec §14 asks for `-race` on what a phase touches): `go -C wails-app test -race -run 'TestTask|TestRestartAndStopTaskWatcher' -count=1 .` — expected: PASS with no race report.

- [ ] **Step 6: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. In `startTaskWatcher`'s stop function, delete `<-done` → `TestTaskWatcherStopWaitsForAReportInFlight`.
2. In `restartTaskWatcher`, `profileID := a.getActiveProfileID()` becomes `profileID := "default"` → `TestRestartAndStopTaskWatcher`.
3. In `TaskPulse`, delete the `store.Profile` check → `TestTaskPulseReadsTheActiveBoard` (a profile that is not there answers a zero board).
4. In `startTaskWatcher`'s goroutine, `if err != nil` becomes `if err == nil` → `TestTaskWatcherEndsWithAWarningForAProfileThatDoesNotExist` (no warning reaches the log).

No test reaches the three call sites (`startup`, `SwitchProfile`, `shutdown`): the reviewer checks those lines by reading. Without the `SwitchProfile` one, the badge and the board stop following the board after a profile switch.

- [ ] **Step 7: Commit**

```
git add wails-app/app_tasks_watch.go wails-app/app_tasks_watch_test.go wails-app/app.go wails-app/app_profiles.go
```
then
```
git commit -m "feat(tasks): the app watches the board revision and emits tasks:changed" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3: The JS bindings and the task service

**Files:**
- Modify: `wails-app/frontend/src/wailsjs/go/main/App.js`, `wails-app/frontend/src/wailsjs/go/main/App.d.ts` (eleven entries each)
- Create: `wails-app/frontend/src/services/tasks.js`
- Test: `wails-app/frontend/src/services/tasks.test.js`

**Interfaces:**
- Consumes: Tasks 1 and 2's bound methods; `subscribeEvent(name, callback)` from `services/api.js` (returns the unsubscribe function; a no-op outside the desktop shell).
- Produces (`services/tasks.js`; every later task imports these names):
  - `DONE_LIMIT = 50`
  - `tasksApi.board()`, `.show(id)`, `.add(spec)`, `.edit(id, change)`, `.move(id, status, place)`, `.approve(ids, top)`, `.archive(ids)`, `.unarchive(ids)`, `.comment(id, text)`, `.pulse()`: each resolves to the CLI's parsed JSON or `{error, code?}` and never rejects. `place` is `{where: '' | 'top' | 'bottom' | 'before' | 'after', ref}`.
  - `tasksApi.agentShell()`: resolves to the marker name or `''`.
  - `onTasksChanged(callback)`: subscribes to `tasks:changed`, returns the unsubscribe function.

- [ ] **Step 1: Write the failing test**

Create `wails-app/frontend/src/services/tasks.test.js`:

```js
// The bindings the task service calls exist in the generated module. They
// are placed there by hand (phase 3 plan, Task 3): a misspelt name would
// only fail when a person clicks.
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import * as App from '../wailsjs/go/main/App'

const service = readFileSync(join(__dirname, 'tasks.js'), 'utf8')
const used = [...new Set([...service.matchAll(/GoApp\.(\w+)/g)].map(m => m[1]))].sort()
const dts = readFileSync(join(__dirname, '..', 'wailsjs', 'go', 'main', 'App.d.ts'), 'utf8')

describe('task bindings', () => {
  it('finds the eleven bindings the service uses', () => {
    expect(used).toEqual(['TaskAdd', 'TaskAgentShell', 'TaskApprove', 'TaskArchive', 'TaskBoard', 'TaskComment',
      'TaskEdit', 'TaskMove', 'TaskPulse', 'TaskShow', 'TaskUnarchive'])
  })
  it('has each in App.js and App.d.ts', () => {
    expect(used.filter(n => typeof App[n] !== 'function')).toEqual([])
    expect(used.filter(n => !dts.includes(`export function ${n}(`))).toEqual([])
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix wails-app/frontend test -- src/services/tasks.test.js`
Expected: FAIL (`ENOENT` reading `tasks.js`).

- [ ] **Step 3: Add the entries to the generated bindings**

Do not run `wails generate`. Find the place with `grep -n "TagApplication\|TestAutomation" wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts`. In `App.js`, insert between the end of the `TagApplication` function and `export function TestAutomation(`, one blank line between functions as the file has:

```js
export function TaskAdd(arg1) {
  return window['go']['main']['App']['TaskAdd'](arg1);
}

export function TaskAgentShell() {
  return window['go']['main']['App']['TaskAgentShell']();
}

export function TaskApprove(arg1, arg2) {
  return window['go']['main']['App']['TaskApprove'](arg1, arg2);
}

export function TaskArchive(arg1) {
  return window['go']['main']['App']['TaskArchive'](arg1);
}

export function TaskBoard(arg1) {
  return window['go']['main']['App']['TaskBoard'](arg1);
}

export function TaskComment(arg1, arg2) {
  return window['go']['main']['App']['TaskComment'](arg1, arg2);
}

export function TaskEdit(arg1, arg2) {
  return window['go']['main']['App']['TaskEdit'](arg1, arg2);
}

export function TaskMove(arg1, arg2, arg3, arg4) {
  return window['go']['main']['App']['TaskMove'](arg1, arg2, arg3, arg4);
}

export function TaskPulse() {
  return window['go']['main']['App']['TaskPulse']();
}

export function TaskShow(arg1) {
  return window['go']['main']['App']['TaskShow'](arg1);
}

export function TaskUnarchive(arg1) {
  return window['go']['main']['App']['TaskUnarchive'](arg1);
}
```

In `App.d.ts`, at the same place:

```ts
export function TaskAdd(arg1:string):Promise<string>;

export function TaskAgentShell():Promise<string>;

export function TaskApprove(arg1:Array<number>,arg2:boolean):Promise<string>;

export function TaskArchive(arg1:Array<number>):Promise<string>;

export function TaskBoard(arg1:number):Promise<string>;

export function TaskComment(arg1:number,arg2:string):Promise<string>;

export function TaskEdit(arg1:number,arg2:string):Promise<string>;

export function TaskMove(arg1:number,arg2:string,arg3:string,arg4:number):Promise<string>;

export function TaskPulse():Promise<Record<string, any>>;

export function TaskShow(arg1:number):Promise<string>;

export function TaskUnarchive(arg1:Array<number>):Promise<string>;
```

`models.ts` does not change: no binding returns a struct. These types are spelt as the generator spells them, so the next `wails dev` or `wails build` on the user's machine rewrites nothing: check with `grep -n "GetExecutionDetail\|GetPeopleTagsMap" wails-app/frontend/src/wailsjs/go/main/App.d.ts` that a `map[string]interface{}` result is written `Promise<Record<string, any>>` and a slice argument `Array<…>` (so `[]int64` is `Array<number>`), and copy any difference you see.

- [ ] **Step 4: Write `wails-app/frontend/src/services/tasks.js`**

```js
// The task board bindings (wails-app/app_tasks.go, app_tasks_watch.go).
// Each call resolves to the CLI's JSON (`monoagentcli task … --json`; the
// board, read in process, comes in the same shape) or to {error, code} and
// never rejects: the board shows a refusal on the card it
// concerns instead of a page-wide failure.
import * as GoApp from '../wailsjs/go/main/App'
import { subscribeEvent } from './api.js'

// The Done column shows the most recent DONE_LIMIT cards (spec §10).
export const DONE_LIMIT = 50

// run takes a thunk so a binding missing from an older app build (or from a
// test's mock) becomes {error} too, not a synchronous throw.
const run = (call) => Promise.resolve()
  .then(call)
  .then(r => (typeof r === 'string' ? JSON.parse(r) : r))
  .catch(e => ({ error: e?.message || String(e) }))

export const tasksApi = {
  board:     () => run(() => GoApp.TaskBoard(DONE_LIMIT)),
  show:      (id) => run(() => GoApp.TaskShow(id)),
  // spec: {title, notes} or {text}; ready: straight to Ready.
  add:       (spec) => run(() => GoApp.TaskAdd(JSON.stringify(spec))),
  // change: {title} and/or {notes}; empty notes clear them.
  edit:      (id, change) => run(() => GoApp.TaskEdit(id, JSON.stringify(change))),
  // place: {where: '' | 'top' | 'bottom' | 'before' | 'after', ref}
  move:      (id, status, place = {}) => run(() => GoApp.TaskMove(id, status, place.where || '', place.ref || 0)),
  approve:   (ids, top = false) => run(() => GoApp.TaskApprove(ids, top)),
  archive:   (ids) => run(() => GoApp.TaskArchive(ids)),
  unarchive: (ids) => run(() => GoApp.TaskUnarchive(ids)),
  comment:   (id, text) => run(() => GoApp.TaskComment(id, text)),
  // {profile_id, rev, inbox, review}, or {} before the database is open or for a profile that is not there.
  pulse:     () => run(() => GoApp.TaskPulse()),
  // The agent-context marker the app inherited, or '' (a plain string, not JSON).
  agentShell: () => Promise.resolve().then(() => GoApp.TaskAgentShell()).then(m => m || '').catch(() => ''),
}

// onTasksChanged: the active profile's board revision moved. Payload
// {profile_id, rev, inbox, review}. Returns the unsubscribe function.
export function onTasksChanged(callback) {
  return subscribeEvent('tasks:changed', callback)
}
```

- [ ] **Step 5: Run the test to see it pass**

Run: `npm --prefix wails-app/frontend test -- src/services/tasks.test.js`
Expected: PASS (two tests). Then misspell one entry in `App.js` (`TaskMov`), run again, see "has each in App.js and App.d.ts" FAIL, and undo.

- [ ] **Step 6: Commit**

```
git add wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts wails-app/frontend/src/services/tasks.js wails-app/frontend/src/services/tasks.test.js
```
then
```
git commit -m "feat(tasks): JS bindings and the task service for the desktop board" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Compare with what the generator writes**

Release builds regenerate these files (`wails build -skipbindings=false`), so the hand-placed lines should be exactly what Wails writes. Run `cd wails-app && wails generate module` (the local `wails` is v2.11 while `go.mod` pins v2.16, so it may also rewrite entries this phase did not touch), then `git diff -U0 -G Task -- wails-app/frontend/src/wailsjs`.
Expected: no output (no added or removed line holds `Task`: the generator wrote this phase's eleven entries exactly as placed).
Then put the committed files back: `git checkout -- wails-app/frontend/src/wailsjs`, and `git status --short` must show no change under `wailsjs` (remove, one file at a time, any file the generator added). If the diff did show `Task` lines, now edit those entries by hand to the generator's spelling, run Step 5 again and commit them as `feat(tasks): Wails bindings as the generator writes them`. If `wails generate module` fails, say so in your report and go on: the release build regenerates the bindings anyway.

### Task 4a: The board model: moves, places and keys (pure)

**Files:**
- Create: `wails-app/frontend/src/lib/taskModel.js`
- Test: `wails-app/frontend/src/lib/taskModel.test.js` (Task 4b appends to both)

**Interfaces:**
- Consumes: the board and task JSON of Task 0.
- Produces (later tasks import these exactly): `COLUMNS` (the five statuses in board order), `defaultIndex(status, length)`; `normalizeBoard(doc) → {profile, rev, counts, columns: {inbox: Task[], …}}`, `findTask(board, id) → {task, status, index} | null`; `applyMove(board, id, to, place)`, `applyRemove(board, id)`, `applyOps(board, ops)` with ops `{type: 'move', id, to, place}` or `{type: 'remove', id}`; `placeFor(ids, index) → place`, `isNoopDrop(fromIds, from, to, ids, index, id) → bool`, `dropIndex(rects, y) → index`; `keyMove(shown, id, 'left'|'right'|'up'|'down') → {to, place} | {blocked} | null`, `focusTarget(shown, id, key) → id | null`.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/lib/taskModel.test.js`:

```js
import { describe, it, expect } from 'vitest'
import {
  COLUMNS, normalizeBoard, findTask, applyMove, applyRemove, applyOps, placeFor, isNoopDrop, dropIndex,
  keyMove, focusTarget,
} from './taskModel.js'

const T0 = Date.parse('2026-10-06T09:00:00Z')
const card = (id, status, extra = {}) => ({
  id, title: `t${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T08:00:00Z' },
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z', ...extra,
})
const doc = (tasks, extra = {}) => ({ profile: { id: 'default', name: 'Default' }, rev: 1, counts: {}, tasks, ...extra })
const ids = (board, s) => board.columns[s].map(t => t.id)
const by = (actor, kind, at = '2026-10-06T09:10:00Z') => ({ last_event: { actor, kind, at } })

// Inbox 1 2, Ready 3 4, In progress 5 (held by bot), Review 6, Done 7 (of 120).
const base = () => normalizeBoard(doc({
  inbox: [card(2, 'inbox'), card(1, 'inbox')],
  ready: [card(3, 'ready'), card(4, 'ready')],
  in_progress: [card(5, 'in_progress', { claim: { by: 'bot', until: '2026-10-06T09:30:00Z', stale: false } })],
  review: [card(6, 'review')],
  done: [card(7, 'done')],
}, { counts: { inbox: 2, ready: 2, in_progress: 1, review: 1, done: 120, stale: 0 } }))

describe('normalizeBoard', () => {
  it('keeps the five columns, each in position order, empty ones too', () => {
    const b = normalizeBoard(doc({ inbox: [card(2, 'inbox'), card(1, 'inbox')] }))
    expect(Object.keys(b.columns)).toEqual(COLUMNS)
    expect(ids(b, 'inbox')).toEqual([1, 2])
    expect(b.columns.done).toEqual([])
    expect(b.profile.id).toBe('default')
  })
})

describe('applyMove', () => {
  it('puts a card at the default place of its new column', () => {
    expect(ids(applyMove(base(), 1, 'ready', { where: '' }), 'ready')).toEqual([3, 4, 1])
    expect(ids(applyMove(base(), 3, 'review', { where: '' }), 'review')).toEqual([3, 6])
  })
  it('puts it before or after a card, and falls back when that card is not there', () => {
    expect(ids(applyMove(base(), 1, 'ready', { where: 'before', ref: 4 }), 'ready')).toEqual([3, 1, 4])
    expect(ids(applyMove(base(), 1, 'ready', { where: 'after', ref: 3 }), 'ready')).toEqual([3, 1, 4])
    expect(ids(applyMove(base(), 1, 'ready', { where: 'before', ref: 99 }), 'ready')).toEqual([3, 4, 1])
  })
  it('reorders within a column', () => {
    const b = applyMove(base(), 4, 'ready', { where: 'before', ref: 3 })
    expect(ids(b, 'ready')).toEqual([4, 3])
    expect(b.counts.ready).toBe(2)
  })
  it('ends the claim of a card that leaves In progress and keeps it inside', () => {
    expect(findTask(applyMove(base(), 5, 'ready', { where: '' }), 5).task.claim).toBeNull()
    expect(findTask(applyMove(base(), 5, 'in_progress', { where: 'top' }), 5).task.claim.by).toBe('bot')
  })
  it('keeps the counts in step, Done by what its column gained', () => {
    const b = applyMove(base(), 6, 'done', { where: '' })
    expect(b.counts.review).toBe(0)
    expect(b.counts.done).toBe(121)
    expect(ids(b, 'done')).toEqual([6, 7])
  })
  it('leaves the board alone for an unknown card or column', () => {
    const b = base()
    expect(applyMove(b, 99, 'ready', {})).toBe(b)
    expect(applyMove(b, 1, 'archived', {})).toBe(b)
  })
})

describe('applyOps and applyRemove', () => {
  it('lays the pending operations over the board in order', () => {
    const b = applyOps(base(), [{ type: 'move', id: 1, to: 'ready', place: {} }, { type: 'remove', id: 3 }])
    expect(ids(b, 'inbox')).toEqual([2])
    expect(ids(b, 'ready')).toEqual([4, 1])
    expect(applyRemove(base(), 7).counts.done).toBe(119)
    expect(applyOps(null, [{ type: 'remove', id: 1 }])).toBeNull()
  })
})

describe('placeFor', () => {
  it('names a shown neighbour, never the bottom', () => {
    expect(placeFor([], 0)).toEqual({ where: '' })
    expect(placeFor([3, 4], 0)).toEqual({ where: 'before', ref: 3 })
    expect(placeFor([3, 4], 1)).toEqual({ where: 'before', ref: 4 })
    expect(placeFor([3, 4], 2)).toEqual({ where: 'after', ref: 4 })
    // Done shows the newest of many: below the last shown card means right after it.
    expect(placeFor([7], 1)).toEqual({ where: 'after', ref: 7 })
  })
})

describe('isNoopDrop', () => {
  it('is a drop that changes nothing', () => {
    expect(isNoopDrop([3, 4], 'ready', 'ready', [4], 0, 3)).toBe(true)
    expect(isNoopDrop([3, 4], 'ready', 'ready', [4], 1, 3)).toBe(false)
    expect(isNoopDrop([3, 4], 'ready', 'review', [6], 0, 3)).toBe(false)
  })
})

describe('dropIndex', () => {
  const rects = [{ top: 0, height: 40 }, { top: 50, height: 40 }]
  it('lands before the first card whose middle is below the pointer', () => {
    expect(dropIndex(rects, 5)).toBe(0)
    expect(dropIndex(rects, 19.9)).toBe(0)
    expect(dropIndex(rects, 20)).toBe(1) // exactly at a middle: after that card
    expect(dropIndex(rects, 69)).toBe(1)
    expect(dropIndex(rects, 70)).toBe(2)
    expect(dropIndex([], 10)).toBe(0)
  })
})

describe('keyMove', () => {
  it('moves a column at a time, to the default place', () => {
    expect(keyMove(base(), 1, 'right')).toEqual({ to: 'ready', place: { where: '' } })
    expect(keyMove(base(), 3, 'left')).toEqual({ to: 'inbox', place: { where: '' } })
    expect(keyMove(base(), 1, 'left')).toEqual({ blocked: 'first' })
    expect(keyMove(base(), 7, 'right')).toEqual({ blocked: 'last' })
  })
  it('reorders past the shown neighbour', () => {
    expect(keyMove(base(), 4, 'up')).toEqual({ to: 'ready', place: { where: 'before', ref: 3 } })
    expect(keyMove(base(), 3, 'down')).toEqual({ to: 'ready', place: { where: 'after', ref: 4 } })
    expect(keyMove(base(), 3, 'up')).toEqual({ blocked: 'top' })
    expect(keyMove(base(), 4, 'down')).toEqual({ blocked: 'bottom' })
    expect(keyMove(base(), 99, 'up')).toBeNull()
  })
})

describe('focusTarget', () => {
  it('moves the focus up, down, and across to the nearest column with cards', () => {
    const b = applyRemove(base(), 6) // Review is empty now
    expect(focusTarget(b, 3, 'down')).toBe(4)
    expect(focusTarget(b, 3, 'up')).toBeNull()
    expect(focusTarget(b, 2, 'right')).toBe(4)
    expect(focusTarget(b, 5, 'right')).toBe(7)
    expect(focusTarget(b, 1, 'left')).toBeNull()
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/lib/taskModel.test.js`
Expected: FAIL (cannot resolve `./taskModel.js`).

- [ ] **Step 3: Write `wails-app/frontend/src/lib/taskModel.js`**

```js
// Pure logic of the Tasks tab (spec §10): the board document, the moves the
// page shows before the CLI answers, where a drop or a key puts a card,
// search, what changed between two reads, and what a card shows. No React
// and no DOM: the tests drive all of it.

export const COLUMNS = ['inbox', 'ready', 'in_progress', 'review', 'done']

// A card new to these columns goes on top (newest first); Ready and In
// progress are queues (spec 4.6).
const NEWEST_FIRST = new Set(['inbox', 'review', 'done'])

export function defaultIndex(status, length) {
  return NEWEST_FIRST.has(status) ? 0 : length
}

const byPosition = (a, b) => (a.position - b.position) || (a.id - b.id)

// normalizeBoard turns the board document (as `task board --json` prints it)
// into the page's board: the five columns always present, each in position
// order.
export function normalizeBoard(doc) {
  const columns = {}
  for (const s of COLUMNS) columns[s] = [...(doc?.tasks?.[s] || [])].sort(byPosition)
  return {
    profile: doc?.profile || { id: '', name: '' },
    rev: doc?.rev ?? 0,
    counts: { ...(doc?.counts || {}) },
    columns,
  }
}

// findTask is where card id sits: {task, status, index}, or null.
export function findTask(board, id) {
  if (!board) return null
  for (const s of COLUMNS) {
    const index = board.columns[s].findIndex(t => t.id === id)
    if (index >= 0) return { task: board.columns[s][index], status: s, index }
  }
  return null
}

// insertAt is the index place gives in list (the column without the card);
// a ref that is not there falls back to the column's default.
function insertAt(list, status, place) {
  const at = list.findIndex(t => t.id === place?.ref)
  switch (place?.where) {
    case 'top': return 0
    case 'bottom': return list.length
    case 'before': return at < 0 ? defaultIndex(status, list.length) : at
    case 'after': return at < 0 ? defaultIndex(status, list.length) : at + 1
    default: return defaultIndex(status, list.length)
  }
}

// withCounts keeps the counts in step with the columns. Done is cut to its
// newest cards, so its count moves by what its column gained or lost.
function withCounts(board, columns) {
  const counts = { ...board.counts }
  for (const s of COLUMNS) {
    counts[s] = s === 'done'
      ? Math.max(0, (board.counts.done ?? board.columns.done.length) + columns.done.length - board.columns.done.length)
      : columns[s].length
  }
  return { ...board, columns, counts }
}

// applyMove is the board after moving card id to column `to` at place, as
// the store will do it: a card that leaves In progress loses its claim.
export function applyMove(board, id, to, place) {
  const found = findTask(board, id)
  if (!found || !COLUMNS.includes(to)) return board
  const columns = { ...board.columns, [found.status]: board.columns[found.status].filter(t => t.id !== id) }
  const target = [...columns[to]]
  const moved = { ...found.task, status: to, claim: to === 'in_progress' ? found.task.claim : null }
  target.splice(insertAt(target, to, place), 0, moved)
  columns[to] = target
  return withCounts(board, columns)
}

// applyRemove is the board without card id (archived).
export function applyRemove(board, id) {
  const found = findTask(board, id)
  if (!found) return board
  return withCounts(board, { ...board.columns, [found.status]: board.columns[found.status].filter(t => t.id !== id) })
}

// applyOps lays the pending operations ({type: 'move', id, to, place} or
// {type: 'remove', id}) over the last board read, oldest first.
export function applyOps(board, ops) {
  if (!board) return board
  return ops.reduce((b, op) => (op.type === 'remove' ? applyRemove(b, op.id) : applyMove(b, op.id, op.to, op.place)), board)
}

// placeFor turns a drop at index among ids (the target column's cards as
// shown, without the moved card) into the CLI's place, always next to a
// shown card: a search or the Done column cut to its newest cards must not
// bury the card among hidden ones. An empty column takes its default.
export function placeFor(ids, index) {
  if (ids.length === 0) return { where: '' }
  if (index >= ids.length) return { where: 'after', ref: ids[ids.length - 1] }
  return { where: 'before', ref: ids[Math.max(0, index)] }
}

// isNoopDrop says whether a drop leaves the card where it was: same column,
// same order. fromIds is the source column as shown, the card included.
export function isNoopDrop(fromIds, from, to, ids, index, id) {
  if (from !== to) return false
  const order = [...ids.slice(0, index), id, ...ids.slice(index)]
  return order.length === fromIds.length && order.every((x, i) => x === fromIds[i])
}

// dropIndex is where a card dropped at height y lands among rects (the
// column's shown cards without the dragged one, top to bottom): before the
// first card whose middle is below y.
export function dropIndex(rects, y) {
  const i = rects.findIndex(r => y < r.top + r.height / 2)
  return i < 0 ? rects.length : i
}

// keyMove is the move a key asks of card id on the board as shown:
// 'left'/'right' (Shift+arrows) go to the next column at its default place,
// 'up'/'down' (Alt+arrows) pass the shown neighbour. {to, place}, or
// {blocked: 'first' | 'last' | 'top' | 'bottom'}, or null for no such card.
export function keyMove(shown, id, key) {
  const found = findTask(shown, id)
  if (!found) return null
  const { status, index } = found
  const col = shown.columns[status]
  if (key === 'left' || key === 'right') {
    const to = COLUMNS[COLUMNS.indexOf(status) + (key === 'left' ? -1 : 1)]
    if (!to) return { blocked: key === 'left' ? 'first' : 'last' }
    return { to, place: { where: '' } }
  }
  if (key === 'up') {
    if (index === 0) return { blocked: 'top' }
    return { to: status, place: { where: 'before', ref: col[index - 1].id } }
  }
  if (key === 'down') {
    if (index === col.length - 1) return { blocked: 'bottom' }
    return { to: status, place: { where: 'after', ref: col[index + 1].id } }
  }
  return null
}

// focusTarget is the card a plain arrow moves the focus to: above or below
// in the column, or the card at the same height (else the last) in the
// nearest column that has cards.
export function focusTarget(shown, id, key) {
  const found = findTask(shown, id)
  if (!found) return null
  const col = shown.columns[found.status]
  if (key === 'up') return col[found.index - 1]?.id ?? null
  if (key === 'down') return col[found.index + 1]?.id ?? null
  const step = key === 'left' ? -1 : 1
  for (let c = COLUMNS.indexOf(found.status) + step; c >= 0 && c < COLUMNS.length; c += step) {
    const other = shown.columns[COLUMNS[c]]
    if (other.length) return other[Math.min(found.index, other.length - 1)].id
  }
  return null
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/lib/taskModel.test.js`
Expected: PASS (every test).

- [ ] **Step 5: Check that the rules are pinned**

Each change below must make the named test FAIL (same command), then undo it:
1. `placeFor`: `{ where: 'after', ref: ids[ids.length - 1] }` becomes `{ where: 'bottom' }` → "names a shown neighbour, never the bottom".
2. `dropIndex`: `y < r.top + r.height / 2` becomes `y <= r.top + r.height / 2` → "lands before the first card whose middle is below the pointer".
3. `applyMove`: `claim: to === 'in_progress' ? found.task.claim : null` becomes `claim: found.task.claim` → "ends the claim of a card that leaves In progress".

- [ ] **Step 6: Commit**

```
git add wails-app/frontend/src/lib/taskModel.js wails-app/frontend/src/lib/taskModel.test.js
```
then
```
git commit -m "feat(tasks): the board model: moves, placement and keys" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4b: The board model: search, changes and labels (pure)

**Files:**
- Modify: `wails-app/frontend/src/lib/taskModel.js` (append), `wails-app/frontend/src/lib/taskModel.test.js` (the import list, appended tests)

**Interfaces:**
- Consumes: Task 4a's `COLUMNS`, `normalizeBoard`, `applyMove`, `applyRemove`, `keyMove`, and the test file's helpers (`T0`, `card`, `doc`, `ids`, `by`, `base`).
- Produces: `matches(task, query)`, `filterBoard(board, query)`; `remoteChanges(prev, next, max = 3) → [{id, title, actor, kind, to, at}]`, `transitions(prev, next) → {entered: Set, done: Set}`; `hostOf(url)`, `sourceChip(task) → {kind, text}`, `shortAge(from, now) → {unit, n}`, `claimState(claim, now) → {by, initial, stale, minutes, until} | null`, `splitMinutes(m) → {h, m}`, `actorColor(name) → 'var(--…)'`, `isTypingTarget(el)`.

- [ ] **Step 1: Write the failing tests**

In `wails-app/frontend/src/lib/taskModel.test.js`, replace the `import { … } from './taskModel.js'` statement with:

```js
import {
  COLUMNS, normalizeBoard, findTask, applyMove, applyRemove, applyOps, placeFor, isNoopDrop, dropIndex,
  keyMove, focusTarget, matches, filterBoard, remoteChanges, transitions, hostOf, sourceChip, shortAge,
  claimState, splitMinutes, actorColor, isTypingTarget,
} from './taskModel.js'
```

and append at the end of the file:

```js
describe('keyMove with a search', () => {
  it('passes only the cards a search shows', () => {
    const b = normalizeBoard(doc({ ready: [card(3, 'ready'), card(8, 'ready', { title: 'hidden' }), card(4, 'ready')] }))
    expect(keyMove(filterBoard(b, 't'), 4, 'up')).toEqual({ to: 'ready', place: { where: 'before', ref: 3 } })
  })
})

describe('search', () => {
  const t = card(12, 'inbox', {
    title: 'Fix the Login test', notes: 'flaky on CI',
    source: { kind: 'chrome', url: 'https://github.com/x/y', title: 'PR 41', app: '' },
    claim: { by: 'claude-code#a3f9', until: '', stale: false },
  })
  it('needs every word, in any field, in any case', () => {
    expect(matches(t, 'login FLAKY')).toBe(true)
    expect(matches(t, 'login missing')).toBe(false)
    expect(matches(t, 'github.com')).toBe(true)
    expect(matches(t, 'a3f9')).toBe(true)
    expect(matches(t, '')).toBe(true)
  })
  it('reads #12 as that task only', () => {
    expect(matches(t, '#12')).toBe(true)
    expect(matches(t, '#1')).toBe(false)
  })
  it('leaves the board as it is without a query', () => {
    const b = base()
    expect(filterBoard(b, '  ')).toBe(b)
    expect(ids(filterBoard(b, '#3'), 'ready')).toEqual([3])
    expect(ids(filterBoard(b, '#3'), 'inbox')).toEqual([])
  })
})

describe('remoteChanges', () => {
  it('names what an agent did, never what you did', () => {
    const next = normalizeBoard(doc({
      inbox: [card(1, 'inbox'), card(2, 'inbox')],
      ready: [card(4, 'ready')],
      in_progress: [card(3, 'in_progress', { ...by('bot', 'claimed'), claim: { by: 'bot', until: '', stale: false } })],
      review: [card(5, 'review', by('bot', 'result'))],
      done: [card(6, 'done', by('you', 'moved')), card(7, 'done')],
    }))
    expect(remoteChanges(base(), next).map(c => [c.id, c.actor, c.kind, c.to])).toEqual([
      [3, 'bot', 'claimed', 'in_progress'], [5, 'bot', 'result', 'review']])
  })
  it('names a card someone added and a claim taken over', () => {
    const prev = normalizeBoard(doc({ in_progress: [card(5, 'in_progress', { claim: { by: 'bot', until: '', stale: true } })] }))
    const next = normalizeBoard(doc({
      inbox: [card(9, 'inbox', by('chrome', 'created', '2026-10-06T09:11:00Z'))],
      in_progress: [card(5, 'in_progress', { ...by('other', 'reclaimed', '2026-10-06T09:12:00Z'), claim: { by: 'other', until: '', stale: false } })],
    }))
    expect(remoteChanges(prev, next).map(c => [c.id, c.actor, c.kind])).toEqual([[9, 'chrome', 'created'], [5, 'other', 'reclaimed']])
    expect(remoteChanges(null, next)).toEqual([])
  })
  it('ignores a comment and a card that only came back into view', () => {
    const held = { claim: { by: 'bot', until: '', stale: false } }
    const prev = normalizeBoard(doc({ in_progress: [card(5, 'in_progress', held)] }))
    const next = normalizeBoard(doc({
      in_progress: [card(5, 'in_progress', { ...held, ...by('bot', 'comment') })],
      done: [card(8, 'done', by('bot', 'result'))],
    }))
    expect(remoteChanges(prev, next)).toEqual([])
  })
  it('keeps the newest three, and nothing across profiles', () => {
    const prev = normalizeBoard(doc({}))
    const next = normalizeBoard(doc({ inbox: [1, 2, 3, 4].map(i => card(i, 'inbox', by('chrome', 'created', `2026-10-06T09:1${i}:00Z`))) }))
    expect(remoteChanges(prev, next).map(c => c.id)).toEqual([2, 3, 4])
    expect(remoteChanges({ ...prev, profile: { id: 'work', name: 'Work' } }, next)).toEqual([])
  })
})

describe('transitions', () => {
  it('marks new cards and cards just done, and nothing on the first read', () => {
    const moved = applyMove(base(), 6, 'done', {})
    const next = { ...moved, columns: { ...moved.columns, inbox: [card(9, 'inbox'), ...moved.columns.inbox] } }
    const t = transitions(base(), next)
    expect([...t.entered]).toEqual([9])
    expect([...t.done]).toEqual([6])
    expect(transitions(null, next).entered.size).toBe(0)
  })
})

describe('card labels', () => {
  it('shows the domain, the app, or the kind', () => {
    expect(hostOf('https://www.github.com/x')).toBe('github.com')
    expect(hostOf('not a url')).toBe('')
    expect(sourceChip({ source: { kind: 'chrome', url: 'https://www.github.com/x', title: 'PR' } })).toEqual({ kind: 'chrome', text: 'github.com' })
    expect(sourceChip({ source: { kind: 'chrome', url: '', title: 'Saved page' } })).toEqual({ kind: 'chrome', text: 'Saved page' })
    expect(sourceChip({ source: { kind: 'os', app: 'Safari' } })).toEqual({ kind: 'os', text: 'Safari' })
    expect(sourceChip({ source: { kind: 'agent' } })).toEqual({ kind: 'agent', text: '' })
    expect(sourceChip({ source: { kind: 'app' } })).toEqual({ kind: 'app', text: '' })
    expect(sourceChip({})).toEqual({ kind: 'cli', text: '' })
  })
  it('gives an age in the largest whole unit', () => {
    const M = 60000
    const H = 60 * M
    const D = 24 * H
    const at = (ms) => shortAge('2026-10-06T09:00:00Z', T0 + ms)
    expect(at(59 * 1000)).toEqual({ unit: 'now', n: 0 })
    expect(at(M)).toEqual({ unit: 'm', n: 1 })
    expect(at(59 * M)).toEqual({ unit: 'm', n: 59 })
    expect(at(H)).toEqual({ unit: 'h', n: 1 })
    expect(at(D - 1)).toEqual({ unit: 'h', n: 23 })
    expect(at(D)).toEqual({ unit: 'd', n: 1 })
    expect(at(7 * D)).toEqual({ unit: 'w', n: 1 })
    expect(shortAge('garbage', T0)).toEqual({ unit: 'now', n: 0 })
  })
  it('shows a claim live until its lease ends, then stale', () => {
    const claim = { by: 'claude-code#a3f9', until: '2026-10-06T09:30:00Z', stale: false }
    expect(claimState(claim, T0)).toMatchObject({ by: 'claude-code#a3f9', initial: 'C', stale: false, minutes: 30 })
    expect(claimState(claim, Date.parse('2026-10-06T09:30:00Z'))).toMatchObject({ stale: true, minutes: 0 }) // ending exactly now is ended
    expect(claimState(claim, Date.parse('2026-10-06T09:34:10Z'))).toMatchObject({ stale: true, minutes: 4 })
    expect(claimState({ ...claim, stale: true }, T0).stale).toBe(true)
    expect(claimState({ by: '#42', until: '' }, T0)).toMatchObject({ initial: '4', stale: true })
    expect(claimState(null, T0)).toBeNull()
    expect(splitMinutes(65)).toEqual({ h: 1, m: 5 })
  })
  it('keeps one colour per agent', () => {
    expect(actorColor('claude-code#a3f9')).toBe(actorColor('claude-code#a3f9'))
    expect(actorColor('a')).toMatch(/^var\(--/)
  })
  it('knows a field from the board', () => {
    expect(isTypingTarget({ tagName: 'TEXTAREA' })).toBe(true)
    expect(isTypingTarget({ tagName: 'DIV', isContentEditable: true })).toBe(true)
    expect(isTypingTarget({ tagName: 'LI' })).toBe(false)
    expect(isTypingTarget(null)).toBe(false)
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/lib/taskModel.test.js`
Expected: FAIL in the new tests only (`matches is not a function` and the like); Task 4a's tests still pass.

- [ ] **Step 3: Append to `wails-app/frontend/src/lib/taskModel.js`**

```js
// matches says whether task t answers query: every word must be in its
// title, notes, source (page, page title, app) or claimant; "#12" means
// task 12 exactly.
export function matches(t, query) {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  const hay = [t.title, t.notes, t.source?.url, t.source?.title, t.source?.app, t.claim?.by]
    .filter(Boolean).join('\n').toLowerCase()
  return words.every(w => (/^#\d+$/.test(w) ? t.id === Number(w.slice(1)) : hay.includes(w)))
}

// filterBoard is the board as a search shows it.
export function filterBoard(board, query) {
  if (!board || !String(query || '').trim()) return board
  const columns = {}
  for (const s of COLUMNS) columns[s] = board.columns[s].filter(t => matches(t, query))
  return { ...board, columns }
}

// places maps each card of a board to its column and claimant.
function places(board) {
  const m = new Map()
  for (const s of COLUMNS) for (const t of board.columns[s]) m.set(t.id, { s, by: t.claim?.by || '' })
  return m
}

// remoteChanges lists what someone other than the operator did between two
// reads of one profile's board: a card they moved to another column, a
// claim they took over, a card they added. Oldest first, the last max. The
// operator's own actions ("you") never count.
export function remoteChanges(prev, next, max = 3) {
  if (!prev || !next || prev.profile.id !== next.profile.id) return []
  const before = places(prev)
  const out = []
  for (const s of COLUMNS) {
    for (const t of next.columns[s]) {
      const ev = t.last_event
      if (!ev || ev.actor === 'you') continue
      const was = before.get(t.id)
      if (!was && ev.kind !== 'created') continue
      if (was && was.s === s && (s !== 'in_progress' || was.by === (t.claim?.by || ''))) continue
      out.push({ id: t.id, title: t.title, actor: ev.actor, kind: ev.kind, to: s, at: ev.at })
    }
  }
  out.sort((a, b) => String(a.at).localeCompare(String(b.at)))
  return out.slice(-max)
}

// transitions are the cards to animate after the board changed: entered
// (not there before) and done (just moved into Done). Nothing on the first
// read or after a profile change.
export function transitions(prev, next) {
  const entered = new Set()
  const done = new Set()
  if (!prev || !next || prev.profile.id !== next.profile.id) return { entered, done }
  const before = places(prev)
  for (const s of COLUMNS) {
    for (const t of next.columns[s]) {
      const was = before.get(t.id)
      if (!was) entered.add(t.id)
      else if (s === 'done' && was.s !== 'done') done.add(t.id)
    }
  }
  return { entered, done }
}

// hostOf is a URL's host without "www.", or ''.
export function hostOf(url) {
  try { return new URL(url).hostname.replace(/^www\./, '') } catch { return '' }
}

// sourceChip says how a card shows where it came from (spec §10): kind picks
// the icon (globe, app window, terminal, spark, board); text is the domain
// for Chrome or the app's name for the OS menu, '' for the kind's own word.
export function sourceChip(t) {
  const s = t.source || {}
  switch (s.kind) {
    case 'chrome': return { kind: 'chrome', text: hostOf(s.url) || s.title || '' }
    case 'os': return { kind: 'os', text: s.app || '' }
    case 'agent': return { kind: 'agent', text: '' }
    case 'app': return { kind: 'app', text: '' }
    default: return { kind: 'cli', text: '' }
  }
}

// shortAge is a card's age as {unit, n}: now, minutes, hours, days, weeks.
export function shortAge(from, now) {
  const ms = now - Date.parse(from)
  if (!Number.isFinite(ms) || ms < 60000) return { unit: 'now', n: 0 }
  const m = Math.floor(ms / 60000)
  if (m < 60) return { unit: 'm', n: m }
  const h = Math.floor(m / 60)
  if (h < 24) return { unit: 'h', n: h }
  const d = Math.floor(h / 24)
  if (d < 7) return { unit: 'd', n: d }
  return { unit: 'w', n: Math.floor(d / 7) }
}

// claimState is a claim as a card shows it at now: who, the initial on the
// avatar, stale once the store said so or the lease ended (a lease ends
// with no write, so no read follows: the clock decides; a lease that ends
// exactly now has ended), and the minutes left or since the end.
export function claimState(claim, now) {
  if (!claim?.by) return null
  const until = Date.parse(claim.until)
  const stale = !!claim.stale || !Number.isFinite(until) || until <= now
  const minutes = Number.isFinite(until) ? Math.floor(Math.abs(until - now) / 60000) : 0
  const initial = (claim.by.match(/[A-Za-z0-9]/)?.[0] || '?').toUpperCase()
  return { by: claim.by, initial, stale, minutes, until: claim.until }
}

// splitMinutes is {h, m} for "1h 5m".
export function splitMinutes(minutes) {
  return { h: Math.floor(minutes / 60), m: minutes % 60 }
}

const ACTOR_COLORS = ['var(--purple-light)', 'var(--cyan)', 'var(--teal)', 'var(--orange)', 'var(--instagram)', 'var(--green-neon)']

// actorColor gives each agent name a steady colour, so two agents on one
// board read apart.
export function actorColor(name) {
  let h = 0
  for (const ch of String(name)) h = (h * 31 + ch.codePointAt(0)) >>> 0
  return ACTOR_COLORS[h % ACTOR_COLORS.length]
}

// isTypingTarget: a key pressed here belongs to a field (the search, a
// quick add, the drawer, the assistant's composer), not to the board.
export function isTypingTarget(el) {
  if (!el) return false
  return el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable === true
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/lib/taskModel.test.js`
Expected: PASS (every test).

- [ ] **Step 5: Check that the rules are pinned**

Each change below must make the named test FAIL, then undo it:
1. `claimState`: `until <= now` becomes `until < now` → "shows a claim live until its lease ends, then stale".
2. `remoteChanges`: `if (!ev || ev.actor === 'you') continue` becomes `if (!ev) continue` → "names what an agent did, never what you did".
3. `matches`: drop the `#12` branch (always `hay.includes(w)`) → "reads #12 as that task only".

- [ ] **Step 6: Commit**

```
git add wails-app/frontend/src/lib/taskModel.js wails-app/frontend/src/lib/taskModel.test.js
```
then
```
git commit -m "feat(tasks): the board model: search, what changed, and what a card shows" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 5: The board hook: reads, live events, optimistic moves

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/useTasksBoard.js`
- Test: `wails-app/frontend/src/pages/tasks/useTasksBoard.test.jsx`

**Interfaces:**
- Consumes: Task 3's `tasksApi.board/move/approve/archive/unarchive/add` and `onTasksChanged`; Task 4's `normalizeBoard`, `applyOps`, `remoteChanges`; `useReloadOnActivate(isActive, reload)` (`lib/useReloadOnActivate.js`); `usePageVisible()` and `useVisibleCatchUp(fn)` (`lib/usePageVisible.js`).
- Produces: `useTasksBoard({ isActive, beforeChange, onRemote }) → { board, status, error, pendingIds, load, retry, move, approve, archive, unarchive, add }`:
  - `board`: the last read with the pending operations laid over it, or `null` before the first read; `status`: `'loading' | 'ready' | 'error'` (`'error'` only while there is no board); `error`: the last read's failure text, `''` once a read succeeds; `pendingIds`: the ids with an operation in flight.
  - `load(quiet = false) → Promise` (a read; `quiet` shows no activity toasts and records no FLIP boxes), `retry()`.
  - `move(id, to, place, overrides)`, `approve(id)`, `archive(id)`: optimistic; each resolves to the CLI's document or `{error, code}`.
  - `unarchive(id)`, `add(spec)`: not optimistic; a read follows a success.
  - `beforeChange(overrides)` is called just before what the board shows changes; `onRemote(changes)` gets Task 4's `remoteChanges`.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/useTasksBoard.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act, cleanup } from '@testing-library/react'

let changed = null
vi.mock('../../services/tasks.js', () => ({
  tasksApi: { board: vi.fn(), move: vi.fn(), approve: vi.fn(), archive: vi.fn(), unarchive: vi.fn(), add: vi.fn() },
  onTasksChanged: vi.fn((cb) => { changed = cb; return () => { changed = null } }),
}))
vi.mock('../../lib/usePageVisible.js', () => ({ usePageVisible: () => true, useVisibleCatchUp: () => {} }))
import { tasksApi } from '../../services/tasks.js'
import { useTasksBoard } from './useTasksBoard.js'

const card = (id, status, extra = {}) => ({
  id, title: `t${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T08:00:00Z' },
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z', ...extra,
})
const doc = (rev, tasks) => ({ profile: { id: 'default', name: 'Default' }, rev, counts: {}, tasks })
const ids = (board, s) => board.columns[s].map(t => t.id)
const said = (actor, kind) => ({ last_event: { actor, kind, at: '2026-10-06T09:00:00Z' } })
function deferred() {
  let resolve
  const promise = new Promise(r => { resolve = r })
  return { promise, resolve }
}
const BOARD1 = doc(1, { inbox: [card(1, 'inbox')], ready: [card(2, 'ready')] })
// Card 1 moved below card 2 (positions decide the order, not the list).
const BOARD2 = doc(2, { ready: [card(2, 'ready'), card(1, 'ready', { ...said('you', 'moved'), position: 3072 })] })

beforeEach(() => {
  vi.clearAllMocks()
  changed = null
})
afterEach(cleanup)

async function mounted(props = {}) {
  const hook = renderHook((p) => useTasksBoard(p), { initialProps: { isActive: true, ...props } })
  await waitFor(() => expect(hook.result.current.status).toBe('ready'))
  return hook
}

describe('useTasksBoard', () => {
  it('reads the board once on mount', async () => {
    tasksApi.board.mockResolvedValue(BOARD1)
    const { result } = await mounted()
    expect(tasksApi.board).toHaveBeenCalledTimes(1)
    expect(ids(result.current.board, 'inbox')).toEqual([1])
  })

  it('keeps a move until the read that includes it', async () => {
    const read2 = deferred()
    tasksApi.board.mockResolvedValueOnce(BOARD1).mockReturnValueOnce(read2.promise)
    const answer = deferred()
    tasksApi.move.mockReturnValue(answer.promise)
    const { result } = await mounted()
    let done
    act(() => { done = result.current.move(1, 'ready', { where: 'after', ref: 2 }) })
    expect(ids(result.current.board, 'ready')).toEqual([2, 1]) // at once
    expect(result.current.pendingIds.has(1)).toBe(true)
    expect(tasksApi.move).toHaveBeenCalledWith(1, 'ready', { where: 'after', ref: 2 })
    await act(async () => { answer.resolve({ task: card(1, 'ready') }) })
    // The CLI said yes and the read is still out: the card stays where it was put.
    expect(ids(result.current.board, 'ready')).toEqual([2, 1])
    await act(async () => { read2.resolve(BOARD2); await done })
    expect(ids(result.current.board, 'ready')).toEqual([2, 1])
    expect(ids(result.current.board, 'inbox')).toEqual([])
    expect(result.current.pendingIds.size).toBe(0)
  })

  it('puts a refused card back and returns why', async () => {
    tasksApi.board.mockResolvedValue(BOARD1)
    tasksApi.move.mockResolvedValue({ error: 'only the operator can move a task', code: 'operator_only' })
    const { result } = await mounted()
    let res
    await act(async () => { res = await result.current.move(1, 'ready', { where: '' }) })
    expect(res.code).toBe('operator_only')
    expect(ids(result.current.board, 'inbox')).toEqual([1])
    expect(result.current.pendingIds.size).toBe(0)
    await waitFor(() => expect(tasksApi.board).toHaveBeenCalledTimes(2)) // a read follows a refusal
  })

  it('only marks the board dirty while a move is pending, and reads once after', async () => {
    tasksApi.board.mockResolvedValueOnce(BOARD1).mockResolvedValue(BOARD2)
    const answer = deferred()
    tasksApi.move.mockReturnValue(answer.promise)
    const { result } = await mounted()
    let done
    act(() => { done = result.current.move(1, 'ready', { where: '' }) })
    act(() => { changed({ profile_id: 'default', rev: 2, inbox: 0, review: 0 }) })
    act(() => { changed({ profile_id: 'default', rev: 3, inbox: 0, review: 0 }) })
    expect(tasksApi.board).toHaveBeenCalledTimes(1)
    await act(async () => { answer.resolve({ task: card(1, 'ready') }); await done })
    expect(tasksApi.board).toHaveBeenCalledTimes(2)
  })

  it('reads once more for any number of events during one read, and not for the revision shown', async () => {
    tasksApi.board.mockResolvedValueOnce(BOARD1)
    const { result } = await mounted()
    act(() => { changed({ profile_id: 'default', rev: 1 }) }) // the revision on screen
    expect(tasksApi.board).toHaveBeenCalledTimes(1)
    const second = deferred()
    tasksApi.board.mockReturnValueOnce(second.promise).mockResolvedValue(BOARD2)
    act(() => { changed({ profile_id: 'default', rev: 2 }) })
    act(() => { changed({ profile_id: 'default', rev: 3 }) })
    act(() => { changed({ profile_id: 'default', rev: 4 }) })
    expect(tasksApi.board).toHaveBeenCalledTimes(2)
    await act(async () => { second.resolve(BOARD2) })
    await waitFor(() => expect(tasksApi.board).toHaveBeenCalledTimes(3))
    expect(result.current.board.rev).toBe(2)
  })

  it('toasts what an agent did, never the operator\'s own move, and nothing after activation', async () => {
    const onRemote = vi.fn()
    const held = { claim: { by: 'bot', until: '2026-10-06T10:00:00Z', stale: false } }
    tasksApi.board
      .mockResolvedValueOnce(doc(1, { inbox: [card(5, 'inbox')], in_progress: [card(3, 'in_progress', held)] }))
      .mockResolvedValueOnce(doc(2, { ready: [card(5, 'ready', said('you', 'moved'))], review: [card(3, 'review', said('bot', 'result'))] }))
      .mockResolvedValueOnce(doc(3, { inbox: [card(4, 'inbox', said('chrome', 'created'))], ready: [card(5, 'ready')], review: [card(3, 'review')] }))
    const { rerender } = await mounted({ onRemote })
    await act(async () => { changed({ profile_id: 'default', rev: 2 }) })
    await waitFor(() => expect(onRemote).toHaveBeenCalledTimes(1))
    expect(onRemote.mock.calls[0][0].map(c => [c.id, c.actor, c.kind])).toEqual([[3, 'bot', 'result']])
    rerender({ isActive: false, onRemote })
    act(() => { changed({ profile_id: 'default', rev: 3 }) }) // inactive: only marked dirty
    expect(tasksApi.board).toHaveBeenCalledTimes(2)
    rerender({ isActive: true, onRemote }) // activation reads, quietly
    await waitFor(() => expect(tasksApi.board).toHaveBeenCalledTimes(3))
    expect(onRemote).toHaveBeenCalledTimes(1)
  })

  it('keeps a confirmed move when the confirming read fails, until a read succeeds', async () => {
    tasksApi.board.mockResolvedValueOnce(BOARD1).mockResolvedValueOnce({ error: 'database is locked' }).mockResolvedValueOnce(BOARD2)
    tasksApi.move.mockResolvedValue({ task: card(1, 'ready') })
    const { result } = await mounted()
    await act(async () => { await result.current.move(1, 'ready', { where: 'after', ref: 2 }) })
    expect(ids(result.current.board, 'ready')).toEqual([2, 1]) // the read failed: the move stays
    await act(async () => { changed({ profile_id: 'default', rev: 2 }) })
    await waitFor(() => expect(tasksApi.board).toHaveBeenCalledTimes(3))
    expect(ids(result.current.board, 'ready')).toEqual([2, 1])
    expect(result.current.pendingIds.size).toBe(0)
  })

  it('does not drop a move on a read that started before the CLI answered', async () => {
    const early = deferred()
    tasksApi.board.mockResolvedValueOnce(BOARD1).mockReturnValueOnce(early.promise).mockResolvedValueOnce(BOARD2)
    tasksApi.move.mockResolvedValue({ task: card(1, 'ready') })
    const { result } = await mounted()
    act(() => { changed({ profile_id: 'default', rev: 2 }) }) // a read starts and waits
    let done
    await act(async () => { done = result.current.move(1, 'ready', { where: 'after', ref: 2 }) })
    await act(async () => { early.resolve(BOARD1) }) // the board as it was before the move
    expect(ids(result.current.board, 'ready')).toEqual([2, 1])
    await act(async () => { await done })
    expect(ids(result.current.board, 'ready')).toEqual([2, 1])
    expect(result.current.pendingIds.size).toBe(0)
  })

  it('says when the first read fails, and keeps the board when a later one does', async () => {
    tasksApi.board.mockResolvedValueOnce({ error: 'monoagentcli printed text instead of JSON' })
    const { result } = renderHook(() => useTasksBoard({ isActive: true }))
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toMatch(/instead of JSON/)
    tasksApi.board.mockResolvedValueOnce(BOARD1).mockResolvedValueOnce({ error: 'database is locked' })
    await act(async () => { await result.current.retry() })
    expect(result.current.status).toBe('ready')
    await act(async () => { await result.current.load() })
    expect(result.current.status).toBe('ready')
    expect(result.current.error).toBe('database is locked')
    expect(ids(result.current.board, 'inbox')).toEqual([1])
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/useTasksBoard.test.jsx`
Expected: FAIL (cannot resolve `./useTasksBoard.js`).

- [ ] **Step 3: Write `wails-app/frontend/src/pages/tasks/useTasksBoard.js`**

```js
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { tasksApi, onTasksChanged } from '../../services/tasks.js'
import { applyOps, normalizeBoard, remoteChanges } from '../../lib/taskModel.js'
import { useReloadOnActivate } from '../../lib/useReloadOnActivate.js'
import { usePageVisible, useVisibleCatchUp } from '../../lib/usePageVisible.js'

// useTasksBoard keeps the active profile's board for the Tasks tab (spec §10,
// Live and Interaction). It reads the board (TaskBoard) on mount,
// on activation and on each tasks:changed, and shows the operator's moves at
// once as optimistic operations over the last read: a move that succeeds
// stays until the read that includes it; one the CLI refuses drops out, the
// card goes back, and its {error, code} is returned for the page to say why.
//
// Reads never overlap: a read asked for while one runs is queued once, and
// its promise resolves after that queued read, which started after the ask.
// A tasks:changed naming the revision on screen is ignored; one that comes
// while a mutation is pending, the tab is inactive or the window hidden only
// marks the board dirty, and the read follows.
//
// beforeChange(overrides) runs just before what the board shows changes
// (the page records card boxes for the FLIP animation); onRemote(changes)
// gets what other actors did between two reads, never after a quiet read
// (mount, activation, retry).
export function useTasksBoard({ isActive, beforeChange, onRemote }) {
  const [server, setServer] = useState(null)
  const [ops, setOps] = useState([])
  const [status, setStatus] = useState('loading') // loading | ready | error
  const [error, setError] = useState('')
  const hooks = useRef({})
  hooks.current = { beforeChange, onRemote }
  const serverRef = useRef(null)
  const pending = useRef(0)
  const dirty = useRef(false)
  const visible = usePageVisible()
  const live = useRef({ active: isActive, visible })
  live.current = { active: isActive, visible }
  const opSeq = useRef(0)
  const readSeq = useRef(0) // reads started so far

  const loader = useRef(null)
  if (!loader.current) {
    let inflight = null
    let queued = null
    const readOnce = async (quiet) => {
      const seq = ++readSeq.current
      const res = await tasksApi.board()
      let next = null
      let failure = res?.error || ''
      if (!failure) {
        try {
          next = normalizeBoard(res)
        } catch (e) {
          failure = `the board could not be read: ${e?.message || e}`
        }
      }
      if (failure) {
        setError(failure)
        setStatus(s => (s === 'ready' ? 'ready' : 'error'))
        return
      }
      if (!quiet) {
        const changes = remoteChanges(serverRef.current, next)
        if (changes.length) hooks.current.onRemote?.(changes)
        hooks.current.beforeChange?.()
      }
      serverRef.current = next
      dirty.current = false
      setServer(next)
      // A move the CLI confirmed leaves with the first read that started after
      // the confirmation (that read has it); an earlier read, or a failed one,
      // leaves it on screen.
      setOps(o => (o.some(x => x.confirmedAt !== undefined && x.confirmedAt < seq)
        ? o.filter(x => !(x.confirmedAt !== undefined && x.confirmedAt < seq)) : o))
      setError('')
      setStatus('ready')
    }
    const load = (quiet = false) => {
      if (inflight) {
        if (!queued) {
          let resolve
          const promise = new Promise(r => { resolve = r })
          queued = { promise, resolve, quiet }
        } else if (!quiet) {
          queued.quiet = false
        }
        return queued.promise
      }
      inflight = readOnce(quiet).finally(() => {
        inflight = null
        const q = queued
        queued = null
        if (q) load(q.quiet).then(q.resolve)
      })
      return inflight
    }
    loader.current = load
  }
  const load = loader.current

  useEffect(() => { load(true) }, [load])
  useReloadOnActivate(isActive, () => load(true))
  useVisibleCatchUp(() => { if (dirty.current && live.current.active) load() })

  useEffect(() => onTasksChanged((ev) => {
    const cur = serverRef.current
    if (cur && ev && ev.profile_id === cur.profile.id && ev.rev === cur.rev) return
    if (pending.current > 0 || !live.current.active || !live.current.visible) {
      dirty.current = true
      return
    }
    load()
  }), [load])

  // mutate shows op at once and runs call. A success is marked confirmed and
  // stays until a read that started afterwards succeeds (readOnce drops it);
  // a refusal drops op (the card goes back) and asks for a read. Resolves to
  // call's result.
  const mutate = useCallback(async (op, call, overrides) => {
    const key = ++opSeq.current
    pending.current += 1
    hooks.current.beforeChange?.(overrides)
    setOps(o => [...o, { ...op, key }])
    let res
    try {
      res = await call()
    } finally {
      pending.current -= 1
    }
    if (res?.error) {
      hooks.current.beforeChange?.()
      setOps(o => o.filter(x => x.key !== key))
      load()
      return res
    }
    const at = readSeq.current
    setOps(o => o.map(x => (x.key === key ? { ...x, confirmedAt: at } : x)))
    await load()
    return res
  }, [load])

  const move = useCallback((id, to, place, overrides) =>
    mutate({ type: 'move', id, to, place }, () => tasksApi.move(id, to, place), overrides), [mutate])
  const approve = useCallback((id) =>
    mutate({ type: 'move', id, to: 'ready', place: { where: '' } }, () => tasksApi.approve([id])), [mutate])
  const archive = useCallback((id) =>
    mutate({ type: 'remove', id }, () => tasksApi.archive([id])), [mutate])
  const unarchive = useCallback(async (id) => {
    const res = await tasksApi.unarchive([id])
    if (!res?.error) await load()
    return res
  }, [load])
  const add = useCallback(async (spec) => {
    const res = await tasksApi.add(spec)
    if (!res?.error) await load()
    return res
  }, [load])
  const retry = useCallback(() => {
    setStatus('loading')
    return load(true)
  }, [load])

  const board = useMemo(() => applyOps(server, ops), [server, ops])
  const pendingIds = useMemo(() => new Set(ops.map(o => o.id)), [ops])
  return { board, status, error, pendingIds, load, retry, move, approve, archive, unarchive, add }
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/useTasksBoard.test.jsx`
Expected: PASS (nine tests).

- [ ] **Step 5: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. In the `onTasksChanged` handler, drop `pending.current > 0 ||` → "only marks the board dirty while a move is pending".
2. In `mutate`, drop the op as soon as the CLI answers (`setOps(o => o.filter(x => x.key !== key))` in place of the `confirmedAt` line) → "keeps a move until the read that includes it".
3. In `mutate`, add `setOps(o => o.filter(x => x.key !== key))` after `await load()` → "keeps a confirmed move when the confirming read fails, until a read succeeds".
4. In `readOnce`, both `x.confirmedAt < seq` become `x.confirmedAt <= seq` → "does not drop a move on a read that started before the CLI answered".
3. Drop the `ev.rev === cur.rev` return → "reads once more for any number of events during one read, and not for the revision shown".
4. `useReloadOnActivate(isActive, () => load(true))` becomes `… load())` → "toasts what an agent did, never the operator's own move, and nothing after activation".

- [ ] **Step 6: Commit**

```
git add wails-app/frontend/src/pages/tasks/useTasksBoard.js wails-app/frontend/src/pages/tasks/useTasksBoard.test.jsx
```
then
```
git commit -m "feat(tasks): the board hook: live reads and optimistic moves that roll back" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 6: The words, in English and Spanish

**Files:**
- Modify: `wails-app/frontend/src/locales/en.json`, `wails-app/frontend/src/locales/es.json`
- Test: `wails-app/frontend/src/locales/tasksKeys.test.js`

**Interfaces:**
- Consumes: nothing.
- Produces: the `tasks.*` keys every later task uses (render tests load the real `src/i18n.js`), `sidebar.nav.tasks` and `sidebar.taskBadge`. Keys built at run time: `tasks.columns.<status>`, `tasks.empty.<status>`, `tasks.source.<kind>` (chrome, os, cli, agent, app), `tasks.age.<unit>` (now, m, h, d, w), `tasks.event.<kind>` (the eleven event kinds of spec 4.4, and `other`), `tasks.activity.<kind>` (created, claimed, reclaimed, result, question, released, moved, archived), `tasks.live.blocked.<edge>` (first, last, top, bottom), `tasks.failed.<verb>` (move, approve, archive, unarchive), `tasks.howTo.<way>` (chrome, os, cli, agents), `tasks.actor.<who>` (you, chrome, os).

- [ ] **Step 1: Write the failing test**

Create `wails-app/frontend/src/locales/tasksKeys.test.js`:

```js
// Every tasks.* key the board uses exists in English and Spanish, both
// locales carry the same tasks keys, and no translation hides an invisible
// character. The scan reads the board's sources that exist so far.
import { describe, it, expect } from 'vitest'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))
const value = (o, k) => k.split('.').reduce((v, part) => v?.[part], o)

const src = join(__dirname, '..')
const dir = join(src, 'pages', 'tasks')
const boardSources = [join(src, 'pages', 'Tasks.jsx'), join(src, 'components', 'Sidebar.jsx'),
  ...(existsSync(dir) ? readdirSync(dir).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(dir, f)) : [])]
  .filter(existsSync)
const literal = [...new Set(boardSources.flatMap(f =>
  [...readFileSync(f, 'utf8').matchAll(/['"`]((?:tasks|sidebar)\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]

const COLUMNS = ['inbox', 'ready', 'in_progress', 'review', 'done']
const dynamic = [
  ...COLUMNS.map(s => `tasks.columns.${s}`),
  ...COLUMNS.map(s => `tasks.empty.${s}`),
  ...['chrome', 'os', 'cli', 'agent', 'app'].map(k => `tasks.source.${k}`),
  ...['now', 'm', 'h', 'd', 'w'].map(u => `tasks.age.${u}`),
  ...['created', 'edited', 'moved', 'claimed', 'reclaimed', 'comment', 'question', 'result', 'released', 'archived', 'unarchived', 'other'].map(k => `tasks.event.${k}`),
  ...['created', 'claimed', 'reclaimed', 'result', 'question', 'released', 'moved', 'archived'].map(k => `tasks.activity.${k}`),
  ...['first', 'last', 'top', 'bottom'].map(k => `tasks.live.blocked.${k}`),
  ...['move', 'approve', 'archive', 'unarchive'].map(k => `tasks.failed.${k}`),
  ...['chrome', 'os', 'cli', 'agents'].map(k => `tasks.howTo.${k}`),
  ...['you', 'chrome', 'os'].map(k => `tasks.actor.${k}`),
  'sidebar.nav.tasks', 'sidebar.taskBadge',
]

// Zero-width, bidi and BOM code points, built from numbers so this file
// holds none of them.
const cp = (n) => String.fromCodePoint(n)
const INVISIBLE = new RegExp(`[${cp(0x200b)}-${cp(0x200f)}${cp(0x202a)}-${cp(0x202e)}${cp(0x2060)}-${cp(0x2069)}${cp(0xfeff)}]`)

describe('tasks i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  it('has every used key in English and Spanish', () => {
    const used = [...new Set([...literal, ...dynamic])]
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
  })
  it('carries the same tasks keys in both locales', () => {
    const s = keys => keys.filter(k => k.startsWith('tasks.')).sort()
    expect(s(esKeys)).toEqual(s(enKeys))
  })
  it('keeps the spec\'s empty states word for word', () => {
    expect(en.tasks.empty.inbox).toBe('Highlight text in Chrome and choose MonoAgent, Add selection as task, or select text in any app: Services, Add to MonoAgent Tasks')
    expect(en.tasks.empty.ready).toBe('Approve tasks from Inbox so agents can pick them up')
  })
  it('hides no invisible character in a translation or a board source', () => {
    const strings = [
      ...enKeys.filter(k => k.startsWith('tasks.')).map(k => value(en, k)),
      ...esKeys.filter(k => k.startsWith('tasks.')).map(k => value(es, k)),
    ]
    expect(strings.filter(v => INVISIBLE.test(String(v)))).toEqual([])
    expect(boardSources.filter(f => INVISIBLE.test(readFileSync(f, 'utf8')))).toEqual([])
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix wails-app/frontend test -- src/locales/tasksKeys.test.js`
Expected: FAIL: "has every used key" lists the `tasks.*` and `sidebar.*` keys, and "keeps the spec's empty states" cannot read `en.tasks`.

- [ ] **Step 3: Add the English keys**

In `wails-app/frontend/src/locales/en.json`:

1. Change
```json
      "documents": "Documents",
      "logs": "Live Logs",
```
to
```json
      "documents": "Documents",
      "tasks": "Tasks",
      "logs": "Live Logs",
```
2. Change
```json
    "changeProfileFolder": "Change profile folder location"
  },
```
to
```json
    "changeProfileFolder": "Change profile folder location",
    "taskBadge": "Inbox {{inbox}} · Review {{review}}"
  },
```
3. Change the end of the file
```json
    "openAsBubbleNamed": "Open {{name}} as a chat bubble"
  }
}
```
to
```json
    "openAsBubbleNamed": "Open {{name}} as a chat bubble"
  },
  "tasks": {
    "title": "Tasks",
    "inProfile": "in {{name}}",
    "search": "Search tasks",
    "searchPlaceholder": "Search words, #id, site or agent",
    "newTask": "New task",
    "readyHint": "top is next",
    "countOf": "{{shown}} of {{total}}",
    "loadFailed": "Could not load the task board",
    "retry": "Try again",
    "staleBoard": "Showing the board as last read: {{error}}",
    "columns": { "inbox": "Inbox", "ready": "Ready", "in_progress": "In progress", "review": "Review", "done": "Done" },
    "empty": {
      "inbox": "Highlight text in Chrome and choose MonoAgent, Add selection as task, or select text in any app: Services, Add to MonoAgent Tasks",
      "ready": "Approve tasks from Inbox so agents can pick them up",
      "in_progress": "A task an agent takes shows here, with the time left on its lease",
      "review": "Finished tasks and agents' questions wait here for you",
      "done": "Nothing done yet",
      "noMatch": "No task matches"
    },
    "source": { "chrome": "Chrome", "os": "macOS", "cli": "Terminal", "agent": "Agent", "app": "Board" },
    "age": { "now": "now", "m": "{{n}}m", "h": "{{n}}h", "d": "{{n}}d", "w": "{{n}}w" },
    "claim": {
      "left": "{{time}} left",
      "over": "ended {{time}} ago",
      "lt1": "<1m",
      "m": "{{m}}m",
      "hm": "{{h}}h {{m}}m",
      "heldBy": "{{name}} is working on it until {{until}}",
      "staleHelp": "{{name}}'s lease ended at {{until}}: another agent may take it over, or move it back to Ready"
    },
    "card": {
      "label": "#{{id}}: {{title}}",
      "approve": "Approve",
      "readApprove": "Read and approve",
      "notes": "notes",
      "more": "more",
      "done": "Done",
      "backToReady": "Back to Ready",
      "question": "Question",
      "result": "Result"
    },
    "quickAdd": {
      "open": "Add a task to {{column}}",
      "label": "New task in {{column}}",
      "placeholder": "Add a task…",
      "hint": "Enter adds · Shift+Enter: new line · Esc closes",
      "adding": "Adding…",
      "close": "Close"
    },
    "drawer": {
      "label": "Task #{{id}}",
      "close": "Close",
      "titleLabel": "Title",
      "notes": "Notes",
      "noNotes": "No notes",
      "editNotes": "Edit",
      "save": "Save",
      "cancel": "Cancel",
      "saving": "Saving…",
      "from": "From",
      "added": "Added {{when}}",
      "approve": "Approve",
      "approveHelp": "Agents act on this text as your own words: read it before you approve.",
      "readFirst": "Not moved: read it first, then approve it here. Agents act on all of its text.",
      "moveTo": "Move to",
      "archive": "Archive",
      "history": "History",
      "loadingHistory": "Loading the history…",
      "historyFailed": "Could not load the history: {{error}}",
      "comment": "Comment",
      "commentPlaceholder": "A note for the agent, or for yourself",
      "sendHint": "⌘ or Ctrl + Enter sends",
      "send": "Send"
    },
    "actor": { "you": "You", "chrome": "Chrome", "os": "macOS menu" },
    "event": {
      "created": "Added",
      "edited": "Edited",
      "moved": "Moved from {{from}} to {{to}}",
      "claimed": "Claimed",
      "reclaimed": "Claimed after the last lease ended",
      "comment": "Comment",
      "question": "Question",
      "result": "Result",
      "released": "Claim ended",
      "archived": "Archived",
      "unarchived": "Restored",
      "other": "Changed"
    },
    "activity": {
      "created": "{{actor}} added #{{id}} to Inbox",
      "claimed": "{{actor}} took #{{id}}",
      "reclaimed": "{{actor}} took over #{{id}}",
      "result": "{{actor}} finished #{{id}}",
      "question": "{{actor}} asked about #{{id}}",
      "released": "{{actor}} released #{{id}}",
      "moved": "{{actor}} moved #{{id}} to {{column}}",
      "archived": "Archived #{{id}}",
      "undo": "Undo",
      "dismiss": "Dismiss"
    },
    "live": {
      "moved": "Moved #{{id}} to {{column}}",
      "approved": "Approved #{{id}}: it is in Ready",
      "added": "Added #{{id}} to {{column}}",
      "failed": "Could not change #{{id}}: {{error}}",
      "notInbox": "#{{id}} is not in Inbox: only Inbox tasks are approved",
      "readFirst": "Opened #{{id}}: read it, then approve it in the drawer",
      "readOnly": "The board is read-only here",
      "blocked": {
        "first": "#{{id}} is in the first column",
        "last": "#{{id}} is in the last column",
        "top": "#{{id}} is at the top",
        "bottom": "#{{id}} is at the bottom"
      }
    },
    "failed": { "move": "move #{{id}}", "approve": "approve #{{id}}", "archive": "archive #{{id}}", "unarchive": "restore #{{id}}" },
    "confirmTakeBack": {
      "title": "An agent is working on it",
      "body": "{{name}} holds #{{id}} and its lease is still running. Take it back anyway? The agent will be told it no longer holds the task.",
      "confirm": "Take it back",
      "cancel": "Leave it"
    },
    "agentShell": {
      "banner": "MonoAgent was started from an AI agent's shell ({{marker}} is set), so the board is read-only here: approving, moving and editing are refused. Quit MonoAgent and open it from the Dock or Finder.",
      "hint": "MonoAgent was started from an AI agent's shell: open it from the Dock or Finder."
    },
    "howTo": {
      "button": "How to capture",
      "title": "How tasks reach this board",
      "chrome": "Chrome: highlight text, right-click, MonoAgent, Add selection as task.",
      "os": "Any Mac app: select text, right-click, Services, Add to MonoAgent Tasks.",
      "cli": "A terminal:",
      "agents": "AI agents take what you approve:",
      "copy": "Copy",
      "copied": "Copied"
    }
  }
}
```

- [ ] **Step 4: Add the Spanish keys**

In `wails-app/frontend/src/locales/es.json`:

1. Change
```json
      "documents": "Documentos",
      "logs": "Registros en vivo",
```
to
```json
      "documents": "Documentos",
      "tasks": "Tareas",
      "logs": "Registros en vivo",
```
2. Change
```json
    "changeProfileFolder": "Cambiar la ubicación de la carpeta del perfil"
  },
```
to
```json
    "changeProfileFolder": "Cambiar la ubicación de la carpeta del perfil",
    "taskBadge": "Bandeja de entrada {{inbox}} · Revisión {{review}}"
  },
```
3. Change the end of the file
```json
    "openAsBubbleNamed": "Abrir {{name}} como burbuja de chat"
  }
}
```
to
```json
    "openAsBubbleNamed": "Abrir {{name}} como burbuja de chat"
  },
  "tasks": {
    "title": "Tareas",
    "inProfile": "en {{name}}",
    "search": "Buscar tareas",
    "searchPlaceholder": "Buscar palabras, #id, sitio o agente",
    "newTask": "Nueva tarea",
    "readyHint": "la de arriba va primero",
    "countOf": "{{shown}} de {{total}}",
    "loadFailed": "No se pudo cargar el tablero de tareas",
    "retry": "Reintentar",
    "staleBoard": "Se muestra el tablero de la última lectura: {{error}}",
    "columns": { "inbox": "Bandeja de entrada", "ready": "Aprobadas", "in_progress": "En curso", "review": "Revisión", "done": "Hechas" },
    "empty": {
      "inbox": "Resalta texto en Chrome y elige MonoAgent, Add selection as task, o selecciona texto en cualquier app: Servicios, Add to MonoAgent Tasks",
      "ready": "Aprueba tareas de la Bandeja de entrada para que los agentes puedan tomarlas",
      "in_progress": "Una tarea que toma un agente aparece aquí, con el tiempo que le queda de plazo",
      "review": "Las tareas terminadas y las preguntas de los agentes te esperan aquí",
      "done": "Aún no hay nada hecho",
      "noMatch": "Ninguna tarea coincide"
    },
    "source": { "chrome": "Chrome", "os": "macOS", "cli": "Terminal", "agent": "Agente", "app": "Tablero" },
    "age": { "now": "ahora", "m": "{{n}} min", "h": "{{n}} h", "d": "{{n}} d", "w": "{{n}} sem" },
    "claim": {
      "left": "quedan {{time}}",
      "over": "terminó hace {{time}}",
      "lt1": "<1 min",
      "m": "{{m}} min",
      "hm": "{{h}} h {{m}} min",
      "heldBy": "{{name}} trabaja en ella hasta las {{until}}",
      "staleHelp": "El plazo de {{name}} terminó a las {{until}}: otro agente puede tomarla, o puedes devolverla a Aprobadas"
    },
    "card": {
      "label": "#{{id}}: {{title}}",
      "approve": "Aprobar",
      "readApprove": "Leer y aprobar",
      "notes": "notas",
      "more": "más",
      "done": "Hecha",
      "backToReady": "Devolver a Aprobadas",
      "question": "Pregunta",
      "result": "Resultado"
    },
    "quickAdd": {
      "open": "Añadir una tarea a {{column}}",
      "label": "Nueva tarea en {{column}}",
      "placeholder": "Añadir una tarea…",
      "hint": "Enter añade · Shift+Enter: nueva línea · Esc cierra",
      "adding": "Añadiendo…",
      "close": "Cerrar"
    },
    "drawer": {
      "label": "Tarea #{{id}}",
      "close": "Cerrar",
      "titleLabel": "Título",
      "notes": "Notas",
      "noNotes": "Sin notas",
      "editNotes": "Editar",
      "save": "Guardar",
      "cancel": "Cancelar",
      "saving": "Guardando…",
      "from": "Origen",
      "added": "Añadida el {{when}}",
      "approve": "Aprobar",
      "approveHelp": "Los agentes actúan sobre este texto como si fueran tus palabras: léelo antes de aprobar.",
      "readFirst": "Aún no se ha movido: léela primero y apruébala aquí. Los agentes actúan sobre todo su texto.",
      "moveTo": "Mover a",
      "archive": "Archivar",
      "history": "Historial",
      "loadingHistory": "Cargando el historial…",
      "historyFailed": "No se pudo cargar el historial: {{error}}",
      "comment": "Comentar",
      "commentPlaceholder": "Una nota para el agente, o para ti",
      "sendHint": "⌘ o Ctrl + Enter envía",
      "send": "Enviar"
    },
    "actor": { "you": "Tú", "chrome": "Chrome", "os": "Menú de macOS" },
    "event": {
      "created": "Añadida",
      "edited": "Editada",
      "moved": "Movida de {{from}} a {{to}}",
      "claimed": "Tomada",
      "reclaimed": "Tomada al terminar el plazo anterior",
      "comment": "Comentario",
      "question": "Pregunta",
      "result": "Resultado",
      "released": "Toma terminada",
      "archived": "Archivada",
      "unarchived": "Restaurada",
      "other": "Cambiada"
    },
    "activity": {
      "created": "{{actor}} añadió #{{id}} a la Bandeja de entrada",
      "claimed": "{{actor}} tomó #{{id}}",
      "reclaimed": "{{actor}} tomó #{{id}} al terminar el plazo anterior",
      "result": "{{actor}} terminó #{{id}}",
      "question": "{{actor}} preguntó sobre #{{id}}",
      "released": "{{actor}} devolvió #{{id}}",
      "moved": "{{actor}} movió #{{id}} a {{column}}",
      "archived": "#{{id}} archivada",
      "undo": "Deshacer",
      "dismiss": "Descartar"
    },
    "live": {
      "moved": "#{{id}} movida a {{column}}",
      "approved": "#{{id}} aprobada: está en Aprobadas",
      "added": "#{{id}} añadida a {{column}}",
      "failed": "No se pudo cambiar #{{id}}: {{error}}",
      "notInbox": "#{{id}} no está en la Bandeja de entrada: solo se aprueban tareas de la Bandeja de entrada",
      "readFirst": "#{{id}} abierta: léela y apruébala en el panel",
      "readOnly": "Aquí el tablero es de solo lectura",
      "blocked": {
        "first": "#{{id}} está en la primera columna",
        "last": "#{{id}} está en la última columna",
        "top": "#{{id}} está arriba del todo",
        "bottom": "#{{id}} está abajo del todo"
      }
    },
    "failed": { "move": "mover #{{id}}", "approve": "aprobar #{{id}}", "archive": "archivar #{{id}}", "unarchive": "restaurar #{{id}}" },
    "confirmTakeBack": {
      "title": "Un agente está trabajando en ella",
      "body": "{{name}} tiene #{{id}} y su plazo sigue vigente. ¿Quitársela de todos modos? Al agente se le dirá que ya no la tiene.",
      "confirm": "Quitársela",
      "cancel": "Dejarla"
    },
    "agentShell": {
      "banner": "MonoAgent se abrió desde la terminal de un agente de IA (la variable {{marker}} está definida), así que aquí el tablero es de solo lectura: aprobar, mover y editar se rechazan. Cierra MonoAgent y ábrelo desde el Dock o el Finder.",
      "hint": "MonoAgent se abrió desde la terminal de un agente de IA: ábrelo desde el Dock o el Finder."
    },
    "howTo": {
      "button": "Cómo capturar",
      "title": "Cómo llegan las tareas a este tablero",
      "chrome": "Chrome: resalta texto, clic derecho, MonoAgent, Add selection as task.",
      "os": "Cualquier app del Mac: selecciona texto, clic derecho, Servicios, Add to MonoAgent Tasks.",
      "cli": "Una terminal:",
      "agents": "Los agentes de IA toman lo que apruebas:",
      "copy": "Copiar",
      "copied": "Copiado"
    }
  }
}
```

The Chrome and macOS menu items keep their English names in Spanish: P4 and P5 name them in English.

- [ ] **Step 5: Run the test and the other locale tests**

Run: `npm --prefix wails-app/frontend test -- src/locales/`
Expected: PASS: `tasksKeys.test.js` (four tests) and every existing locale test (the files are still valid JSON). A JSON syntax slip shows as a parse error naming the line: fix the comma.

- [ ] **Step 6: Commit**

```
git add wails-app/frontend/src/locales/en.json wails-app/frontend/src/locales/es.json wails-app/frontend/src/locales/tasksKeys.test.js
```
then
```
git commit -m "feat(tasks): the Tasks tab's words in English and Spanish" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 7a: The card, and the board's look

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/Card.jsx`, `wails-app/frontend/src/pages/tasks/tasks.css`
- Test: `wails-app/frontend/src/pages/tasks/Card.render.test.jsx`

**Interfaces:**
- Consumes: Task 4's `sourceChip`, `shortAge`, `claimState`, `splitMinutes`, `actorColor`; Task 6's keys.
- Produces:
  - `Card` (default export, memoized): props `task, now, readOnly, pending, rejected, entering, justDone, dragging` and the stable callbacks `onOpen(id)`, `onApprove(id)`, `onMove(id, to)`, `onMouseDown(event, task)`. Renders `<li data-tb-id={id} tabIndex=0>`, with `data-stale="true"` once its claim ran out, `aria-busy="true"` while an operation is pending, and `data-read-first="true"` when it holds more than it shows (notes, or a title clamped to two lines): its Inbox button then reads "Read and approve" and calls `onOpen` (lead's ruling on review F6). `leaseTime(t, minutes)` is a named export.
  - `tasks.css`: the board tokens, header, banners and states, cards, keyframes and the reduced-motion block. Tasks 7b to 10c insert their sections above the line `/* Motion */`.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/Card.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import '../../i18n.js'
import i18n from 'i18next'
import Card from './Card.jsx'

const T0 = Date.parse('2026-10-06T09:00:00Z')
const card = (id, status, extra = {}) => ({
  id, title: `Task ${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T06:00:00Z' },
  created_at: '2026-10-06T06:00:00Z', updated_at: '2026-10-06T06:00:00Z', ...extra,
})

function setup(task, props = {}) {
  const calls = { onOpen: vi.fn(), onApprove: vi.fn(), onMove: vi.fn(), onMouseDown: vi.fn() }
  render(
    <ul>
      <Card task={task} now={T0} readOnly={false} pending={false} rejected={false} entering={false}
        justDone={false} dragging={false} {...calls} {...props} />
    </ul>,
  )
  return { ...calls, li: document.querySelector(`[data-tb-id="${task.id}"]`) }
}

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('Card', () => {
  it('shows the title, the page it came from, its age and id, and approves in one click', () => {
    const t = card(12, 'inbox', { title: 'Fix the login test', source: { kind: 'chrome', url: 'https://www.github.com/a/b', title: 'PR', app: '' } })
    const { li, onApprove, onOpen } = setup(t)
    expect(li).toHaveTextContent('Fix the login test')
    expect(li).toHaveTextContent('github.com')
    expect(li).toHaveTextContent('3h')
    expect(li).toHaveTextContent('#12')
    expect(li).toHaveAttribute('tabindex', '0')
    expect(li).not.toHaveAttribute('data-read-first')
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onApprove).toHaveBeenCalledWith(12)
    expect(onOpen).not.toHaveBeenCalled() // the button's click is its own
    fireEvent.click(li)
    expect(onOpen).toHaveBeenCalledWith(12)
  })

  it('opens a card that has more to read instead of approving it', () => {
    const { li, onApprove, onOpen } = setup(card(13, 'inbox', { notes: 'Delete the old backups\nthen run the cleanup script' }))
    expect(li).toHaveAttribute('data-read-first', 'true')
    expect(li).toHaveTextContent('notes')
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Read and approve' }))
    expect(onOpen).toHaveBeenCalledWith(13)
    expect(onApprove).not.toHaveBeenCalled()
  })

  it('treats a title cut to two lines like notes', () => {
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function () {
      return this.classList.contains('tb-card-title') ? 60 : 0
    })
    vi.spyOn(Element.prototype, 'clientHeight', 'get').mockImplementation(function () {
      return this.classList.contains('tb-card-title') ? 36 : 0
    })
    const { li, onOpen } = setup(card(14, 'inbox', { title: 'A title too long for the two lines a narrow card shows' }))
    expect(li).toHaveAttribute('data-read-first', 'true')
    expect(li).toHaveTextContent('more')
    fireEvent.click(screen.getByRole('button', { name: 'Read and approve' }))
    expect(onOpen).toHaveBeenCalledWith(14)
  })

  it('shows who holds a task and the lease left, amber once it has run out', () => {
    const { li: live } = setup(card(5, 'in_progress', { claim: { by: 'claude-code#a3f9', until: '2026-10-06T09:12:00Z', stale: false } }))
    expect(live).not.toHaveAttribute('data-stale')
    expect(live.querySelector('.tb-avatar')).toHaveTextContent('C')
    expect(live).toHaveTextContent('12m left')
    cleanup()
    const { li: over } = setup(card(6, 'in_progress', { claim: { by: 'codex', until: '2026-10-06T08:56:00Z', stale: false } }))
    expect(over).toHaveAttribute('data-stale', 'true')
    expect(over).toHaveTextContent('ended 4m ago')
    expect(over.getAttribute('aria-label')).toMatch(/lease ended/)
  })

  it('tags a Review card that asks a question and offers Done and Back to Ready', () => {
    const { onMove } = setup(card(7, 'review', { last_event: { actor: 'bot', kind: 'question', at: '2026-10-06T08:59:00Z' } }))
    expect(screen.getByText('Question')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Done/ }))
    expect(onMove).toHaveBeenCalledWith(7, 'done')
    fireEvent.click(screen.getByRole('button', { name: /Back to Ready/ }))
    expect(onMove).toHaveBeenCalledWith(7, 'ready')
  })

  it('offers no action when the board is read-only', () => {
    setup(card(1, 'inbox'), { readOnly: true })
    expect(screen.queryByRole('button')).toBeNull()
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Card.render.test.jsx`
Expected: FAIL (cannot resolve `./Card.jsx`).

- [ ] **Step 3: Write `Card.jsx`**

Create `wails-app/frontend/src/pages/tasks/Card.jsx`:

```jsx
import { memo, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Globe, AppWindow, Terminal, Sparkles, SquareKanban, Check, CheckCheck, Undo2, MessageCircleQuestionMark, CircleCheck,
  NotebookText, BookOpen,
} from 'lucide-react'
import { actorColor, claimState, shortAge, sourceChip, splitMinutes } from '../../lib/taskModel.js'

const SOURCE_ICON = { chrome: Globe, os: AppWindow, cli: Terminal, agent: Sparkles, app: SquareKanban }

// leaseTime writes a lease's minutes as a card shows them: <1m, 12m, 1h 5m.
export function leaseTime(t, minutes) {
  if (minutes < 1) return t('tasks.claim.lt1')
  const { h, m } = splitMinutes(minutes)
  return h ? t('tasks.claim.hm', { h, m }) : t('tasks.claim.m', { m })
}

const clock = (iso) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
const firstLine = (text) => text.trim().split('\n')[0].slice(0, 200)

// useClamped says whether the title runs past the two lines a card shows,
// measured after each render and whenever the title's box changes size (the
// column's width follows the window's).
function useClamped(ref, title) {
  const [clamped, setClamped] = useState(false)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return undefined
    const check = () => setClamped(el.scrollHeight - el.clientHeight > 1)
    check()
    if (typeof ResizeObserver !== 'function') return undefined
    const ro = new ResizeObserver(check)
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref, title])
  return clamped
}

// Card is one task on the board (spec §10): its title on two lines, where it
// came from, a notes marker when it holds more than it shows, its age, the
// agent holding it with the lease left (amber, with the time since it ended,
// once it ran out), a tag when a Review card holds a question or a result,
// and the actions of Inbox and Review. An Inbox card whose whole text shows
// is approved in one click; one with more to read opens the drawer instead,
// where the person approves it having read it (spec D6). The board handles
// its keys and its drag.
function Card({ task, now, readOnly, pending, rejected, entering, justDone, dragging, onOpen, onApprove, onMove, onMouseDown }) {
  const { t } = useTranslation()
  const titleRef = useRef(null)
  const clamped = useClamped(titleRef, task.title)
  const hasNotes = !!task.notes?.trim()
  const readFirst = hasNotes || clamped
  const chip = sourceChip(task)
  const Icon = SOURCE_ICON[chip.kind] || Terminal
  const age = shortAge(task.created_at, now)
  const claim = claimState(task.claim, now)
  const kind = task.status === 'review' ? task.last_event?.kind : ''
  const claimText = claim && t(claim.stale ? 'tasks.claim.staleHelp' : 'tasks.claim.heldBy', { name: claim.by, until: clock(claim.until) })
  const label = [t('tasks.card.label', { id: task.id, title: task.title }), claimText].filter(Boolean).join('. ')
  const cls = ['tb-card', entering && 'tb-card--enter', justDone && 'tb-card--done-pulse', rejected && 'tb-card--rejected',
    dragging && 'tb-card--dragging'].filter(Boolean).join(' ')
  const act = (fn) => (e) => { e.stopPropagation(); fn() }
  return (
    <li
      className={cls}
      data-tb-id={task.id}
      data-stale={claim?.stale ? 'true' : undefined}
      data-read-first={readFirst ? 'true' : undefined}
      tabIndex={0}
      aria-label={label}
      aria-busy={pending ? 'true' : undefined}
      onMouseDown={(e) => onMouseDown(e, task)}
      onClick={() => onOpen(task.id)}
    >
      <div className="tb-card-title" ref={titleRef}>{task.title}</div>
      <div className="tb-card-meta">
        <span className="tb-chip" title={task.source?.url || task.source?.title || undefined}>
          <Icon size={11} aria-hidden="true" />
          <span>{chip.text || t(`tasks.source.${chip.kind}`)}</span>
        </span>
        {readFirst && (
          <span className="tb-more" title={hasNotes ? firstLine(task.notes) : task.title}>
            <NotebookText size={11} aria-hidden="true" /> {t(hasNotes ? 'tasks.card.notes' : 'tasks.card.more')}
          </span>
        )}
        <time dateTime={task.created_at} title={new Date(task.created_at).toLocaleString()}>
          {t(`tasks.age.${age.unit}`, { n: age.n })}
        </time>
        <span className="tb-id">#{task.id}</span>
        {claim && (
          <span className="tb-claim" data-stale={claim.stale ? 'true' : undefined} title={claimText}
            style={{ '--tb-actor': claim.stale ? 'var(--tb-amber)' : actorColor(claim.by) }}>
            <span className="tb-avatar" aria-hidden="true">{claim.initial}</span>
            <span className="tb-lease">{t(claim.stale ? 'tasks.claim.over' : 'tasks.claim.left', { time: leaseTime(t, claim.minutes) })}</span>
          </span>
        )}
      </div>
      {kind === 'question' && (
        <div className="tb-tag tb-tag--question"><MessageCircleQuestionMark size={11} aria-hidden="true" /> {t('tasks.card.question')}</div>
      )}
      {kind === 'result' && (
        <div className="tb-tag tb-tag--result"><CircleCheck size={11} aria-hidden="true" /> {t('tasks.card.result')}</div>
      )}
      {!readOnly && task.status === 'inbox' && (
        <div className="tb-card-actions">
          {readFirst ? (
            <button type="button" className="tb-act tb-act--approve" disabled={pending} onClick={act(() => onOpen(task.id))}>
              <BookOpen size={12} aria-hidden="true" /> {t('tasks.card.readApprove')}
            </button>
          ) : (
            <button type="button" className="tb-act tb-act--approve" disabled={pending} onClick={act(() => onApprove(task.id))}>
              <Check size={12} aria-hidden="true" /> {t('tasks.card.approve')}
            </button>
          )}
        </div>
      )}
      {!readOnly && task.status === 'review' && (
        <div className="tb-card-actions">
          <button type="button" className="tb-act tb-act--done" disabled={pending} onClick={act(() => onMove(task.id, 'done'))}>
            <CheckCheck size={12} aria-hidden="true" /> {t('tasks.card.done')}
          </button>
          <button type="button" className="tb-act" disabled={pending} onClick={act(() => onMove(task.id, 'ready'))}>
            <Undo2 size={12} aria-hidden="true" /> {t('tasks.card.backToReady')}
          </button>
        </div>
      )}
      {justDone && <span className="tb-done-pop" aria-hidden="true"><CheckCheck size={14} /></span>}
    </li>
  )
}

export default memo(Card)
```

- [ ] **Step 4: Write `tasks.css`**

Create `wails-app/frontend/src/pages/tasks/tasks.css` (the page imports it in Task 10c; jsdom tests do not need it):

```css
/* The Tasks tab (spec §10). Board tokens come first: every colour is one of
   the app's :root variables, plus the amber Documents already uses for
   "stale", so another theme would only redefine these. A column's dot
   carries its colour; only a stale claim marks a card's edge. Motion is CSS
   keyframes plus FLIP (useFlip.js checks reduced motion itself); it pauses
   while the window is hidden (.paused) and is switched off under
   prefers-reduced-motion at the end of the file. Classes start with tb-. */

.tb-page {
  --tb-inbox: var(--cyan);
  --tb-ready: var(--teal);
  --tb-progress: var(--purple-light);
  --tb-review: var(--instagram);
  --tb-done: var(--green);
  --tb-amber: #f59e0b;
  --tb-col-bg: rgba(13, 21, 32, 0.6); /* --surface, see-through */
  --tb-col-over: rgba(19, 30, 48, 0.85); /* --elevated, a column under a dragged card */
  --tb-card-bg: var(--surface);
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}
.tb-page.paused *, .tb-page.paused *::before, .tb-page.paused *::after { animation-play-state: paused !important; }

/* Header */
.tb-title-icon { color: var(--cyan); }
.tb-profile {
  max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-family: var(--font-mono); font-size: 11px; color: var(--text-muted);
  padding: 2px 8px; border-radius: 999px; border: 1px solid var(--border); background: var(--elevated);
}
.tb-search { position: relative; display: flex; align-items: center; }
.tb-search > svg { position: absolute; left: 9px; color: var(--text-muted); pointer-events: none; }
.tb-search input { padding-left: 28px; padding-right: 28px; min-width: 260px; }
.tb-search kbd { position: absolute; right: 8px; }
.tb-page kbd {
  font-family: var(--font-mono); font-size: 9.5px; line-height: 1.4; padding: 0 4px;
  color: var(--text-muted); border: 1px solid var(--border); border-bottom-width: 2px; border-radius: 3px;
}
.tb-page .btn kbd { color: inherit; border-color: currentColor; opacity: 0.6; }

/* Banners and states */
.tb-banner {
  display: flex; align-items: center; gap: 8px; margin: 10px 16px 0; padding: 8px 12px;
  font-size: 12px; color: var(--text-secondary); background: var(--elevated);
  border: 1px solid var(--border); border-radius: var(--radius);
}
.tb-banner--warn { color: var(--text); border-color: rgba(245, 158, 11, 0.4); background: rgba(245, 158, 11, 0.08); }
.tb-banner--warn svg { color: var(--tb-amber); flex-shrink: 0; }
.tb-error {
  margin: 48px auto; max-width: 520px; display: flex; flex-direction: column; align-items: center; gap: 10px;
  text-align: center; color: var(--text-secondary);
}
.tb-error svg { color: var(--red); }
.tb-error h3 { font-family: var(--font-display); font-size: 15px; color: var(--text); }
.tb-error pre {
  max-width: 100%; white-space: pre-wrap; word-break: break-word; font-family: var(--font-mono); font-size: 11px;
  color: var(--text-muted); background: var(--surface); border: 1px solid var(--border-dim); border-radius: var(--radius); padding: 8px 10px;
}
.tb-sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }

/* Cards */
.tb-card {
  position: relative; flex-shrink: 0; padding: 8px 10px; border-radius: var(--radius);
  background: var(--tb-card-bg); border: 1px solid var(--border);
  cursor: grab; user-select: none; -webkit-user-select: none;
  transition: border-color 120ms ease, transform 120ms ease, box-shadow 120ms ease, opacity 120ms ease;
}
.tb-card:hover { border-color: var(--border-bright); transform: translateY(-1px); box-shadow: 0 6px 18px rgba(0, 0, 0, 0.35); }
.tb-card:focus-visible { outline: 2px solid var(--cyan); outline-offset: 2px; }
.tb-card[data-stale="true"] { border-left: 2px solid var(--tb-amber); padding-left: 9px; }
.tb-card[aria-busy="true"]::after {
  content: ''; position: absolute; left: 6px; right: 6px; bottom: 0; height: 2px; border-radius: 2px;
  background: linear-gradient(90deg, transparent, var(--tb-accent, var(--cyan)), transparent); background-size: 200% 100%;
  animation: tbSync 1s linear infinite;
}
.tb-card--dragging { opacity: 0.35; border-style: dashed; transform: none; box-shadow: none; }
.tb-card--enter { animation: tbEnter 200ms ease-out; }
.tb-card--done-pulse { animation: tbDoneGlow 900ms ease-out; }
.tb-card--rejected { animation: tbShake 420ms ease; border-color: var(--red); }
.tb-card-title {
  font-size: 12.5px; line-height: 1.4; color: var(--text); overflow-wrap: anywhere;
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
.tb-card-meta { display: flex; align-items: center; gap: 6px; margin-top: 6px; min-width: 0; font-family: var(--font-mono); font-size: 10px; color: var(--text-muted); }
.tb-chip {
  display: inline-flex; align-items: center; gap: 4px; min-width: 0; max-width: 130px; padding: 1px 6px;
  border-radius: 999px; background: var(--elevated); border: 1px solid var(--border-dim); color: var(--text-secondary);
}
.tb-chip > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tb-chip > svg { flex-shrink: 0; }
.tb-more { display: inline-flex; align-items: center; gap: 3px; flex-shrink: 0; color: var(--cyan); }
.tb-id { color: var(--text-dim); }
.tb-claim { margin-left: auto; display: inline-flex; align-items: center; gap: 5px; color: var(--tb-actor); white-space: nowrap; }
.tb-avatar {
  position: relative; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0;
  width: 18px; height: 18px; border-radius: 50%; font-family: var(--font-display); font-size: 10px; font-weight: 700;
  color: var(--tb-actor); border: 1.5px solid var(--tb-actor); background: var(--elevated);
}
.tb-claim:not([data-stale]) .tb-avatar::after {
  content: ''; position: absolute; inset: -3px; border-radius: 50%; border: 1.5px solid var(--tb-actor);
  animation: tbPulse 2.2s ease-out infinite;
}
.tb-claim[data-stale] .tb-avatar { border-style: dashed; }
.tb-lease { font-size: 9.5px; }
.tb-tag { display: inline-flex; align-items: center; gap: 4px; margin-top: 6px; padding: 1px 6px; border-radius: 4px; font-family: var(--font-mono); font-size: 9.5px; }
.tb-tag--question { color: var(--tb-amber); background: rgba(245, 158, 11, 0.1); border: 1px solid rgba(245, 158, 11, 0.3); }
.tb-tag--result { color: var(--green-neon); background: rgba(16, 185, 129, 0.1); border: 1px solid rgba(16, 185, 129, 0.25); }
.tb-card-actions { display: flex; gap: 6px; margin-top: 8px; }
.tb-act {
  display: inline-flex; align-items: center; gap: 4px; padding: 3px 8px; border-radius: var(--radius);
  font-family: var(--font-display); font-size: 11px; font-weight: 600; cursor: pointer;
  background: var(--elevated); border: 1px solid var(--border); color: var(--text-secondary); transition: all var(--transition);
}
.tb-act:hover:not(:disabled) { color: var(--text); border-color: var(--border-bright); }
.tb-act:disabled { opacity: 0.45; cursor: default; }
.tb-act--approve { color: var(--teal); border-color: rgba(0, 245, 212, 0.3); background: rgba(0, 245, 212, 0.06); }
.tb-act--approve:hover:not(:disabled) { color: var(--teal); background: rgba(0, 245, 212, 0.14); box-shadow: 0 0 12px rgba(0, 245, 212, 0.18); }
.tb-act--done { color: var(--green-neon); border-color: rgba(16, 185, 129, 0.3); background: rgba(16, 185, 129, 0.08); }
.tb-act--done:hover:not(:disabled) { color: var(--green-neon); background: rgba(16, 185, 129, 0.16); box-shadow: 0 0 12px rgba(16, 185, 129, 0.2); }
.tb-icon-btn {
  display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 22px; height: 22px; padding: 0;
  border: 1px solid transparent; border-radius: var(--radius); background: transparent; color: var(--text-muted); cursor: pointer;
  transition: all var(--transition);
}
.tb-icon-btn:hover { color: var(--text); background: var(--elevated); border-color: var(--border); }
.tb-done-pop { position: absolute; top: 6px; right: 6px; color: var(--tb-done); animation: tbPop 520ms cubic-bezier(0.16, 1, 0.3, 1) both; }

/* Motion */
@keyframes tbFade { from { opacity: 0; } to { opacity: 1; } }
@keyframes tbEnter { from { opacity: 0; transform: scale(0.96); } to { opacity: 1; transform: none; } }
@keyframes tbShake { 0%, 100% { transform: translateX(0); } 20%, 60% { transform: translateX(-4px); } 40%, 80% { transform: translateX(4px); } }
@keyframes tbPulse { from { opacity: 0.7; transform: scale(1); } to { opacity: 0; transform: scale(1.7); } }
@keyframes tbPop { from { opacity: 0; transform: scale(0.4); } to { opacity: 1; transform: scale(1); } }
@keyframes tbDoneGlow { from { box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.55); } to { box-shadow: 0 0 0 10px rgba(16, 185, 129, 0); } }
@keyframes tbSync { from { background-position: 100% 0; } to { background-position: -100% 0; } }
@keyframes tbShimmer { from { background-position: 100% 0; } to { background-position: -100% 0; } }
@keyframes tbSlideIn { from { opacity: 0; transform: translateX(24px); } to { opacity: 1; transform: none; } }
@keyframes tbRise { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }

@media (prefers-reduced-motion: reduce) {
  .tb-page *, .tb-page *::before, .tb-page *::after { animation: none !important; transition: none !important; }
  .tb-ghost, .tb-card:hover { transform: none; }
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Card.render.test.jsx src/locales/tasksKeys.test.js`
Expected: PASS (six card tests; the keys test now scans `Card.jsx`).

- [ ] **Step 6: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. Drop `e.stopPropagation();` from `act` → "shows the title, the page it came from, its age and id, and approves in one click" (the card opens too).
2. `const readFirst = hasNotes || clamped` becomes `const readFirst = clamped` → "opens a card that has more to read instead of approving it".
3. In `useClamped`, `> 1` becomes `> 100` → "treats a title cut to two lines like notes".
4. `data-stale={claim?.stale ? 'true' : undefined}` on the `li` becomes `data-stale={undefined}` → "shows who holds a task and the lease left, amber once it has run out".

- [ ] **Step 7: Commit**

```
git add wails-app/frontend/src/pages/tasks/Card.jsx wails-app/frontend/src/pages/tasks/Card.render.test.jsx wails-app/frontend/src/pages/tasks/tasks.css
```
then
```
git commit -m "feat(tasks): the task card and the board's look" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 7b: The column and quick add

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/Column.jsx`, `wails-app/frontend/src/pages/tasks/QuickAdd.jsx`
- Modify: `wails-app/frontend/src/pages/tasks/tasks.css` (columns, quick add, skeleton)
- Test: `wails-app/frontend/src/pages/tasks/Column.render.test.jsx`

**Interfaces:**
- Consumes: Task 7a's `Card` and its props; Task 6's keys.
- Produces:
  - `Column` (default): props `status, cards, total, searching, drag, readOnly, quickAdd, cardProps(task) → Card props, onOpenQuickAdd(status), onCloseQuickAdd(), onAdd(spec) → Promise<result>`. Renders `<section data-tb-column={status}>` (with `data-drop-target="true"` while a dragged card is over it) holding `<ul data-tb-list={status}>`; `drag` is `{id, overStatus, overIndex}` or null. `ColumnSkeleton({status})` is a named export.
  - `QuickAdd` (default): props `status, onAdd, onClose`; calls `onAdd({title, ready})` for one line of at most `TITLE_RUNES` (120) characters, else `onAdd({text, ready})`, so the CLI keeps the whole text (review F2).

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/Column.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import '../../i18n.js'
import i18n from 'i18next'
import Column from './Column.jsx'

const T0 = Date.parse('2026-10-06T09:00:00Z')
const card = (id, status) => ({
  id, title: `Task ${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T06:00:00Z' },
  created_at: '2026-10-06T06:00:00Z', updated_at: '2026-10-06T06:00:00Z',
})

function setup(over = {}) {
  const calls = { onOpen: vi.fn(), onApprove: vi.fn(), onMove: vi.fn(), onMouseDown: vi.fn() }
  const props = {
    status: 'inbox', cards: [], total: 0, searching: false, drag: null, readOnly: false, quickAdd: null,
    cardProps: () => ({ now: T0, readOnly: over.readOnly || false, pending: false, rejected: false, entering: false, justDone: false, dragging: false, ...calls }),
    onOpenQuickAdd: vi.fn(), onCloseQuickAdd: vi.fn(), onAdd: vi.fn(), ...over,
  }
  render(<Column {...props} />)
  return { ...calls, ...props }
}

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(cleanup)

describe('Column', () => {
  it('says how to capture when Inbox is empty, and how Ready fills', () => {
    setup()
    expect(screen.getByText('Highlight text in Chrome and choose MonoAgent, Add selection as task, or select text in any app: Services, Add to MonoAgent Tasks')).toBeInTheDocument()
    cleanup()
    setup({ status: 'ready' })
    expect(screen.getByText('Approve tasks from Inbox so agents can pick them up')).toBeInTheDocument()
    expect(screen.getByText('top is next')).toBeInTheDocument()
  })

  it('offers no quick add and no action when the board is read-only', () => {
    setup({ cards: [card(1, 'inbox')], total: 1, readOnly: true, quickAdd: 'inbox' })
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('textbox')).toBeNull()
  })

  it('counts the shown cards of the total while a search filters', () => {
    setup({ status: 'ready', cards: [card(3, 'ready')], total: 5, searching: true })
    expect(screen.getByText('1 of 5')).toBeInTheDocument()
    cleanup()
    setup({ status: 'ready', cards: [], total: 5, searching: true })
    expect(screen.getByText('No task matches')).toBeInTheDocument()
  })

  it('glows and draws the drop line while a card is dragged over it', () => {
    setup({ status: 'ready', cards: [card(3, 'ready'), card(4, 'ready')], total: 2, drag: { id: 9, overStatus: 'ready', overIndex: 1 } })
    const col = document.querySelector('[data-tb-column="ready"]')
    expect(col).toHaveAttribute('data-drop-target', 'true')
    const items = [...col.querySelectorAll('li')].map(li => li.className.split(' ')[0])
    expect(items).toEqual(['tb-card', 'tb-drop-line', 'tb-card'])
  })

  it('adds from the quick add: a title, or text of several lines whole; a refusal keeps the text', async () => {
    const onAdd = vi.fn().mockResolvedValueOnce({ task: { id: 1 } }).mockResolvedValueOnce({ task: { id: 2 } })
      .mockResolvedValueOnce({ error: 'limit reached: this profile already has 2000 open tasks', code: 'limit' })
    const { onCloseQuickAdd } = setup({ status: 'ready', quickAdd: 'ready', onAdd })
    const box = screen.getByRole('textbox')
    expect(box).toHaveFocus()
    fireEvent.change(box, { target: { value: 'Call the bank' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(onAdd).toHaveBeenCalledWith({ title: 'Call the bank', ready: true }))
    await waitFor(() => expect(box).toHaveValue(''))
    fireEvent.change(box, { target: { value: 'Reply to Sam\nabout the invoice' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(onAdd).toHaveBeenLastCalledWith({ text: 'Reply to Sam\nabout the invoice', ready: true }))
    fireEvent.change(box, { target: { value: 'One more' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(await screen.findByRole('alert')).toHaveTextContent('limit reached')
    expect(box).toHaveValue('One more')
    fireEvent.keyDown(box, { key: 'Escape' }) // clears first
    expect(box).toHaveValue('')
    fireEvent.keyDown(box, { key: 'Escape' }) // then closes
    expect(onCloseQuickAdd).toHaveBeenCalled()
  })

  it('sends a line over 120 characters whole, as text', async () => {
    const onAdd = vi.fn().mockResolvedValue({ task: { id: 1 } })
    setup({ quickAdd: 'inbox', onAdd })
    const box = screen.getByRole('textbox')
    const fits = 'x'.repeat(120)
    fireEvent.change(box, { target: { value: fits } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(onAdd).toHaveBeenLastCalledWith({ title: fits, ready: false }))
    await waitFor(() => expect(box).toHaveValue(''))
    const pasted = 'y'.repeat(121)
    fireEvent.change(box, { target: { value: pasted } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(onAdd).toHaveBeenLastCalledWith({ text: pasted, ready: false }))
  })

  it('speaks Spanish', async () => {
    await i18n.changeLanguage('es')
    setup()
    expect(screen.getByRole('heading', { name: 'Bandeja de entrada' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Column.render.test.jsx`
Expected: FAIL (cannot resolve `./Column.jsx`).

- [ ] **Step 3: Write `QuickAdd.jsx`**

Create `wails-app/frontend/src/pages/tasks/QuickAdd.jsx`:

```jsx
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { LoaderCircle, X } from 'lucide-react'

// TITLE_RUNES is the length the CLI cuts a title derived from text to;
// one line longer than it goes whole as text, so nothing is cut (the CLI
// keeps it all in the notes).
export const TITLE_RUNES = 120
const LINE = 18
const MAX_LINES = 6

// QuickAdd is the box in Inbox (at the top) and Ready (at the bottom: where
// the new card lands): Enter adds, Shift+Enter starts a new line, Escape
// clears the box, then closes it. Text of several lines, or one long line,
// goes in whole: its first line becomes the title and all of it the notes
// (spec 4.6). The box stays open for the next task; a refused add keeps the
// text and says why under it.
export default function QuickAdd({ status, onAdd, onClose }) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ref = useRef(null)
  useEffect(() => { ref.current?.focus() }, [])
  useEffect(() => { // one to six lines tall
    const el = ref.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, LINE * MAX_LINES + 12)}px`
  }, [text])

  const submit = async () => {
    const value = text.trim()
    if (!value || busy) return
    setBusy(true)
    setError('')
    const whole = value.includes('\n') || [...value].length > TITLE_RUNES
    const res = await onAdd({ ...(whole ? { text: value } : { title: value }), ready: status === 'ready' })
    setBusy(false)
    if (res?.error) {
      setError(res.error)
      return
    }
    setText('')
    ref.current?.focus()
  }
  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      if (text) setText('')
      else onClose()
    }
  }
  const column = t(`tasks.columns.${status}`)
  return (
    <div className="tb-quick" data-tb-quick={status}>
      <textarea ref={ref} rows={1} className="tb-quick-input" value={text} readOnly={busy}
        placeholder={t('tasks.quickAdd.placeholder')} aria-label={t('tasks.quickAdd.label', { column })}
        onChange={e => setText(e.target.value)} onKeyDown={onKeyDown} />
      <div className="tb-quick-foot">
        {busy
          ? <span className="tb-quick-busy"><LoaderCircle size={12} className="spin" aria-hidden="true" /> {t('tasks.quickAdd.adding')}</span>
          : <span className="tb-quick-hint">{t('tasks.quickAdd.hint')}</span>}
        <button type="button" className="tb-icon-btn" onClick={onClose} aria-label={t('tasks.quickAdd.close')}>
          <X size={12} aria-hidden="true" />
        </button>
      </div>
      {error && <div className="tb-quick-error" role="alert">{error}</div>}
    </div>
  )
}
```

- [ ] **Step 4: Write `Column.jsx`**

Create `wails-app/frontend/src/pages/tasks/Column.jsx`:

```jsx
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Inbox, ListTodo } from 'lucide-react'
import Card from './Card.jsx'
import QuickAdd from './QuickAdd.jsx'

const EMPTY_ICON = { inbox: Inbox, ready: ListTodo }

// Column is one of the five (spec §10): its name, its count (shown of total
// while a search filters), Ready's "top is next", a quick add for Inbox and
// Ready, and its cards, which scroll on their own. While a card is dragged
// over it the column glows and a line shows where the card will land.
export default function Column({ status, cards, total, searching, drag, readOnly, quickAdd, cardProps,
  onOpenQuickAdd, onCloseQuickAdd, onAdd }) {
  const { t } = useTranslation()
  const name = t(`tasks.columns.${status}`)
  const over = !!drag && drag.overStatus === status
  const shown = drag ? cards.filter(c => c.id !== drag.id) : cards
  const lineAt = over ? (shown[drag.overIndex]?.id ?? 'end') : null
  const canAdd = !readOnly && (status === 'inbox' || status === 'ready')
  const EmptyIcon = EMPTY_ICON[status]
  const addBox = canAdd && quickAdd === status && <QuickAdd status={status} onAdd={onAdd} onClose={onCloseQuickAdd} />
  return (
    <section className={`tb-col tb-col--${status}`} data-tb-column={status} data-drop-target={over ? 'true' : undefined}
      aria-labelledby={`tb-col-${status}`}>
      <header className="tb-col-head">
        <span className="tb-col-dot" aria-hidden="true" />
        <h3 id={`tb-col-${status}`}>{name}</h3>
        <span className="tb-col-count">{searching ? t('tasks.countOf', { shown: cards.length, total }) : total}</span>
        {status === 'ready' && <span className="tb-col-hint">{t('tasks.readyHint')}</span>}
        {canAdd && (
          <button type="button" className="tb-col-add" onClick={() => onOpenQuickAdd(status)}
            aria-label={t('tasks.quickAdd.open', { column: name })} title={t('tasks.quickAdd.open', { column: name })}>
            <Plus size={13} aria-hidden="true" />
          </button>
        )}
      </header>
      {status === 'inbox' && addBox}
      <ul className="tb-list" data-tb-list={status}>
        {cards.map(task => (
          <Fragment key={task.id}>
            {lineAt === task.id && <li className="tb-drop-line" aria-hidden="true" />}
            <Card task={task} {...cardProps(task)} />
          </Fragment>
        ))}
        {lineAt === 'end' && <li className="tb-drop-line" aria-hidden="true" />}
        {cards.length === 0 && !over && (
          <li className="tb-empty">
            {EmptyIcon && !searching && <EmptyIcon size={18} aria-hidden="true" />}
            <span>{t(searching ? 'tasks.empty.noMatch' : `tasks.empty.${status}`)}</span>
          </li>
        )}
      </ul>
      {status === 'ready' && addBox}
    </section>
  )
}

// ColumnSkeleton holds a column's place while the first read runs.
export function ColumnSkeleton({ status }) {
  return (
    <section className={`tb-col tb-col--${status}`} aria-hidden="true">
      <header className="tb-col-head"><span className="tb-col-dot" /><span className="tb-skel tb-skel--head" /></header>
      <ul className="tb-list">
        {[64, 48, 80].map((h, i) => <li key={i} className="tb-skel" style={{ height: h }} />)}
      </ul>
    </section>
  )
}
```

- [ ] **Step 5: Add the column sections to `tasks.css`**

In `wails-app/frontend/src/pages/tasks/tasks.css`, insert immediately above the line `/* Motion */`:

```css
/* Board and columns: the board scrolls sideways under 1,100 px, each list on its own */
.tb-col--inbox { --tb-accent: var(--tb-inbox); }
.tb-col--ready { --tb-accent: var(--tb-ready); }
.tb-col--in_progress { --tb-accent: var(--tb-progress); }
.tb-col--review { --tb-accent: var(--tb-review); }
.tb-col--done { --tb-accent: var(--tb-done); }
.tb-body { position: relative; flex: 1; min-height: 0; display: flex; }
.tb-board-scroll { flex: 1; min-width: 0; overflow-x: auto; overflow-y: hidden; padding: 14px 16px 16px; }
.tb-board {
  display: grid; grid-template-columns: repeat(5, minmax(200px, 1fr)); gap: 12px;
  min-width: 1100px; height: 100%; animation: tbFade 180ms ease-out;
}
.tb-col {
  display: flex; flex-direction: column; min-height: 0; background-color: var(--tb-col-bg);
  border: 1px solid var(--border-dim); border-radius: var(--radius-lg);
  transition: border-color 120ms ease, box-shadow 120ms ease, background-color 120ms ease;
}
.tb-col[data-drop-target="true"] {
  border-color: var(--tb-accent); background-color: var(--tb-col-over);
  box-shadow: 0 0 0 1px var(--tb-accent), 0 0 28px -6px var(--tb-accent);
}
.tb-col-head { display: flex; align-items: center; gap: 7px; padding: 10px 10px 8px 12px; border-bottom: 1px solid var(--border-dim); }
.tb-col-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--tb-accent); box-shadow: 0 0 8px var(--tb-accent); flex-shrink: 0; }
.tb-col-head h3 { font-family: var(--font-display); font-size: 12.5px; font-weight: 700; letter-spacing: 0.3px; color: var(--text); white-space: nowrap; }
.tb-col-count {
  font-family: var(--font-mono); font-size: 10px; color: var(--text-muted); padding: 0 6px;
  background: var(--elevated); border: 1px solid var(--border-dim); border-radius: 10px;
}
.tb-col-hint { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: var(--font-mono); font-size: 9.5px; color: var(--text-dim); }
.tb-col-add {
  margin-left: auto; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 22px; height: 22px;
  padding: 0; border: 1px solid transparent; border-radius: var(--radius); background: transparent; color: var(--text-muted); cursor: pointer;
  transition: all var(--transition);
}
.tb-col-add:hover { color: var(--text); background: var(--elevated); border-color: var(--border); }
.tb-list { list-style: none; flex: 1; min-height: 60px; overflow-y: auto; padding: 8px; display: flex; flex-direction: column; gap: 8px; }
.tb-empty {
  display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 18px 12px; text-align: center;
  font-size: 11.5px; line-height: 1.5; color: var(--text-muted); border: 1px dashed var(--border); border-radius: var(--radius);
}
.tb-empty svg { color: var(--tb-accent); opacity: 0.7; }
.tb-drop-line { flex-shrink: 0; height: 3px; margin: -5.5px 2px; border-radius: 2px; background: var(--tb-accent); box-shadow: 0 0 10px var(--tb-accent); }

/* Quick add */
.tb-quick {
  margin: 8px 8px 0; padding: 8px; border-radius: var(--radius); background: var(--elevated);
  border: 1px solid var(--border-bright); box-shadow: 0 0 0 3px var(--cyan-glow); animation: tbEnter 160ms ease-out;
}
.tb-col--ready .tb-quick { margin: 0 8px 8px; }
.tb-quick-input {
  display: block; width: 100%; resize: none; overflow-y: auto; border: none; outline: none; background-color: transparent;
  color: var(--text); font-family: var(--font-body); font-size: 12.5px; line-height: 18px;
}
.tb-quick-input::placeholder { color: var(--text-dim); }
.tb-quick-foot { display: flex; align-items: center; gap: 6px; margin-top: 6px; }
.tb-quick-hint, .tb-quick-busy { flex: 1; display: inline-flex; align-items: center; gap: 5px; font-family: var(--font-mono); font-size: 9.5px; color: var(--text-dim); }
.tb-quick-error { margin-top: 6px; font-size: 11px; color: var(--red); }

/* Skeleton */
.tb-skel {
  list-style: none; border-radius: var(--radius);
  background: linear-gradient(90deg, var(--surface) 0%, var(--elevated) 50%, var(--surface) 100%); background-size: 200% 100%;
  animation: tbShimmer 1.3s ease-in-out infinite;
}
.tb-skel--head { display: inline-block; width: 70px; height: 10px; }

```

- [ ] **Step 6: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Column.render.test.jsx src/pages/tasks/Card.render.test.jsx src/locales/tasksKeys.test.js`
Expected: PASS (seven column tests; the card and keys tests still pass).

- [ ] **Step 7: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. In `QuickAdd.jsx`, `> TITLE_RUNES` becomes `> 200` → "sends a line over 120 characters whole, as text".
2. In `QuickAdd.jsx`, drop the `if (res?.error) { … return }` block → "adds from the quick add …" (the text is cleared after a refusal).
3. In `Column.jsx`, `const canAdd = !readOnly && …` loses `!readOnly &&` → "offers no quick add and no action when the board is read-only".

- [ ] **Step 8: Commit**

```
git add wails-app/frontend/src/pages/tasks/Column.jsx wails-app/frontend/src/pages/tasks/QuickAdd.jsx wails-app/frontend/src/pages/tasks/tasks.css wails-app/frontend/src/pages/tasks/Column.render.test.jsx
```
then
```
git commit -m "feat(tasks): columns and quick add for the Tasks tab" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 8: The board: drag and keyboard

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/useCardDrag.js`, `wails-app/frontend/src/pages/tasks/Board.jsx`
- Modify: `wails-app/frontend/src/pages/tasks/tasks.css` (the drag section)
- Test: `wails-app/frontend/src/pages/tasks/Board.render.test.jsx`

**Interfaces:**
- Consumes: Task 4's `COLUMNS`, `findTask`, `focusTarget`, `isNoopDrop`, `keyMove`, `placeFor`, `dropIndex`; Task 7's `Column` and its props, `data-tb-id`, `data-tb-column`, `data-tb-list`, and a card's `data-read-first`.
- Produces:
  - `useCardDrag({ rootRef, onDrop, canDrag, elementFromPoint }) → { drag, onCardMouseDown, consumeClick }`; `drag` is `{id, x, y, offsetX, offsetY, width, overStatus, overIndex}` or null; `DRAG_THRESHOLD = 5`. A column's card boxes are measured once per drag and again after any scroll.
  - `Board` (default): props `board, shown, totals, searching, now, readOnly, pendingIds, rejectedId, transitions, quickAdd, rootRef, hitTest, onOpen(id), onMove(id, to, place, overrides), onApprove(id), onReadFirst(id, overrides?), onAnnounce(text), onOpenQuickAdd(status), onCloseQuickAdd(), onAdd(spec)`. `rootRef` ends on the `.tb-board` element; the scroller around it carries `data-tb-board-scroll`; the ghost is `data-testid="tb-ghost"`. `hitTest` is passed to `useCardDrag` as `elementFromPoint` (tests only). Every way a card with `data-read-first` would leave Inbox for Ready (`A`, the Approve button, Shift+Right, a drop on Ready) calls `onReadFirst` instead of moving it (the lead's ruling on review F6); a drop passes the ghost's box as `overrides`, so the card slides back from where it was let go. After `A`, Done, Back to Ready and a key move, the focus follows the card. A card new to Done dropped below the last card of a Done column cut short (`totals.done` above the cards it holds) takes Done's default place.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/Board.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { useRef } from 'react'
import '../../i18n.js'
import i18n from 'i18next'
import Board from './Board.jsx'
import { normalizeBoard } from '../../lib/taskModel.js'

const T0 = Date.parse('2026-10-06T09:00:00Z')
const card = (id, status, notes = '') => ({
  id, title: `Task ${id}`, notes, status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T08:00:00Z' },
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z',
})
// #2 holds notes, so it must be read before it is approved.
const BOARD = normalizeBoard({
  profile: { id: 'default', name: 'Default' }, rev: 1, counts: { done: 2 },
  tasks: { inbox: [card(1, 'inbox'), card(2, 'inbox', 'read me first')], ready: [card(3, 'ready'), card(4, 'ready')], in_progress: [], review: [card(6, 'review')], done: [card(7, 'done'), card(8, 'done')] },
})
const TOTALS = { inbox: 2, ready: 2, in_progress: 0, review: 1, done: 2 }

function Harness({ calls, hitTest, readOnly = false, totals = TOTALS, board = BOARD }) {
  const rootRef = useRef(null)
  return (
    <Board board={board} shown={board} totals={totals} searching={false} now={T0} readOnly={readOnly}
      pendingIds={new Set()} rejectedId={null} transitions={{ entered: new Set(), done: new Set() }} quickAdd={null}
      rootRef={rootRef} hitTest={hitTest} {...calls}
      onOpenQuickAdd={() => {}} onCloseQuickAdd={() => {}} onAdd={async () => ({})} />
  )
}
const spies = () => ({ onOpen: vi.fn(), onMove: vi.fn(), onApprove: vi.fn(), onReadFirst: vi.fn(), onAnnounce: vi.fn() })
function setup(props = {}) {
  const calls = spies()
  render(<Harness calls={calls} {...props} />)
  return calls
}
const cardEl = (id) => document.querySelector(`[data-tb-id="${id}"]`)
const colEl = (s) => document.querySelector(`[data-tb-column="${s}"]`)
// Ready's cards: #3 from 40 to 80 (middle 60), #4 from 90 to 130 (middle 110);
// Done's #7 and #8 the same.
function layout() {
  for (const [id, top] of [[3, 40], [4, 90], [7, 40], [8, 90]]) {
    cardEl(id).getBoundingClientRect = () => ({ top, height: 40, bottom: top + 40, left: 300, width: 200, right: 500 })
  }
}

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(cleanup)

describe('Board keyboard', () => {
  it('opens a card with Enter and approves an Inbox card with A', () => {
    const { onOpen, onApprove, onAnnounce } = setup()
    fireEvent.keyDown(cardEl(1), { key: 'Enter' })
    expect(onOpen).toHaveBeenCalledWith(1)
    fireEvent.keyDown(cardEl(1), { key: 'a' })
    expect(onApprove).toHaveBeenCalledWith(1)
    fireEvent.keyDown(cardEl(3), { key: 'a' })
    expect(onApprove).toHaveBeenCalledTimes(1)
    expect(onAnnounce).toHaveBeenCalledWith('#3 is not in Inbox: only Inbox tasks are approved')
  })

  it('sends a card with more to read to the drawer from A and from Shift+Right', () => {
    const { onReadFirst, onApprove, onMove } = setup()
    fireEvent.keyDown(cardEl(2), { key: 'a' })
    fireEvent.keyDown(cardEl(2), { key: 'ArrowRight', shiftKey: true })
    expect(onReadFirst).toHaveBeenCalledTimes(2)
    expect(onReadFirst).toHaveBeenLastCalledWith(2)
    expect(onApprove).not.toHaveBeenCalled()
    expect(onMove).not.toHaveBeenCalled()
  })

  it('keeps the focus on a card that A moved', () => {
    const calls = spies()
    const { rerender } = render(<Harness calls={calls} />)
    cardEl(1).focus()
    fireEvent.keyDown(cardEl(1), { key: 'a' })
    const moved = normalizeBoard({
      profile: { id: 'default', name: 'Default' }, rev: 2, counts: { done: 2 },
      tasks: { inbox: [card(2, 'inbox', 'read me first')], ready: [card(3, 'ready'), card(4, 'ready'), card(1, 'ready')], in_progress: [], review: [card(6, 'review')], done: [card(7, 'done'), card(8, 'done')] },
    })
    rerender(<Harness calls={calls} board={moved} />)
    expect(document.querySelector('[data-tb-column="ready"] [data-tb-id="1"]')).toHaveFocus()
  })

  it('moves a column with Shift+arrows and reorders with Alt+arrows', () => {
    const { onMove, onAnnounce } = setup()
    fireEvent.keyDown(cardEl(1), { key: 'ArrowRight', shiftKey: true })
    expect(onMove).toHaveBeenLastCalledWith(1, 'ready', { where: '' })
    fireEvent.keyDown(cardEl(3), { key: 'ArrowDown', altKey: true })
    expect(onMove).toHaveBeenLastCalledWith(3, 'ready', { where: 'after', ref: 4 })
    fireEvent.keyDown(cardEl(1), { key: 'ArrowLeft', shiftKey: true })
    expect(onAnnounce).toHaveBeenLastCalledWith('#1 is in the first column')
    expect(onMove).toHaveBeenCalledTimes(2)
  })

  it('moves the focus with the plain arrows', () => {
    setup()
    cardEl(1).focus()
    fireEvent.keyDown(cardEl(1), { key: 'ArrowDown' })
    expect(cardEl(2)).toHaveFocus()
    fireEvent.keyDown(cardEl(2), { key: 'ArrowRight' })
    expect(cardEl(4)).toHaveFocus()
  })

  it('says the board is read-only and moves nothing', () => {
    const { onMove, onApprove, onAnnounce } = setup({ readOnly: true })
    fireEvent.keyDown(cardEl(1), { key: 'ArrowRight', shiftKey: true })
    fireEvent.keyDown(cardEl(1), { key: 'a' })
    expect(onMove).not.toHaveBeenCalled()
    expect(onApprove).not.toHaveBeenCalled()
    expect(onAnnounce).toHaveBeenCalledWith('The board is read-only here')
  })

  it('leaves the keys of a card\'s buttons to the buttons', () => {
    const { onOpen } = setup()
    fireEvent.keyDown(cardEl(1).querySelector('button'), { key: 'Enter' })
    expect(onOpen).not.toHaveBeenCalled()
  })
})

describe('Board drag', () => {
  it('drags past the threshold, shows the ghost and the glow, and drops between two cards', () => {
    const { onMove, onOpen } = setup({ hitTest: () => colEl('ready') })
    layout()
    fireEvent.mouseDown(cardEl(1), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 12, clientY: 12 }) // under the threshold
    expect(screen.queryByTestId('tb-ghost')).toBeNull()
    fireEvent.mouseMove(document, { clientX: 320, clientY: 95 })
    expect(screen.getByTestId('tb-ghost')).toHaveTextContent('Task 1')
    expect(colEl('ready')).toHaveAttribute('data-drop-target', 'true')
    expect(cardEl(1).className).toContain('tb-card--dragging')
    fireEvent.mouseUp(document, { clientX: 320, clientY: 95 })
    fireEvent.click(cardEl(1)) // the click that ends the drag
    expect(onMove).toHaveBeenCalledWith(1, 'ready', { where: 'before', ref: 4 }, expect.any(Object))
    expect(onOpen).not.toHaveBeenCalled()
    expect(screen.queryByTestId('tb-ghost')).toBeNull()
  })

  it('drops below the last card as right after it', () => {
    const { onMove } = setup({ hitTest: () => colEl('ready') })
    layout()
    fireEvent.mouseDown(cardEl(1), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 200 })
    fireEvent.mouseUp(document, { clientX: 320, clientY: 200 })
    expect(onMove).toHaveBeenCalledWith(1, 'ready', { where: 'after', ref: 4 }, expect.any(Object))
  })

  it('opens on a plain click, cancels with Escape, never drags from a button, ignores a drop in place', () => {
    const { onMove, onOpen } = setup({ hitTest: () => colEl('ready') })
    layout()
    fireEvent.mouseDown(cardEl(2), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseUp(document, { clientX: 10, clientY: 10 })
    fireEvent.click(cardEl(2))
    expect(onOpen).toHaveBeenCalledWith(2)
    fireEvent.mouseDown(cardEl(2), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 95 })
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByTestId('tb-ghost')).toBeNull()
    fireEvent.mouseUp(document, { clientX: 320, clientY: 95 })
    fireEvent.mouseDown(cardEl(1).querySelector('button'), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 95 })
    expect(screen.queryByTestId('tb-ghost')).toBeNull()
    fireEvent.mouseUp(document, { clientX: 320, clientY: 95 })
    fireEvent.mouseDown(cardEl(3), { button: 0, clientX: 310, clientY: 50 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 55 }) // a drag that ends where #3 already is
    fireEvent.mouseUp(document, { clientX: 320, clientY: 55 })
    expect(onMove).not.toHaveBeenCalled()
  })

  // #2 holds notes: dropped on Ready it stays in Inbox, and its drawer opens
  // (the ghost's box goes along, so the card slides back from there).
  it('opens a card with more to read dropped on Ready instead of moving it', () => {
    const { onMove, onReadFirst } = setup({ hitTest: () => colEl('ready') })
    layout()
    fireEvent.mouseDown(cardEl(2), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 95 })
    fireEvent.mouseUp(document, { clientX: 320, clientY: 95 })
    expect(onMove).not.toHaveBeenCalled()
    expect(onReadFirst).toHaveBeenCalledWith(2, { 2: expect.objectContaining({ left: 310, top: 85 }) })
  })

  it('never drags on a read-only board', () => {
    const { onMove } = setup({ readOnly: true, hitTest: () => colEl('ready') })
    layout()
    fireEvent.mouseDown(cardEl(1), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 95 })
    expect(screen.queryByTestId('tb-ghost')).toBeNull()
    fireEvent.mouseUp(document, { clientX: 320, clientY: 95 })
    expect(onMove).not.toHaveBeenCalled()
  })

  // Done holds 120 cards and the board shows two: right after #8 is a place
  // the next read hides, unless the card was one of the shown already.
  it('drops a card into a cut Done column below its last card at Done\'s default place', () => {
    const { onMove } = setup({ hitTest: () => colEl('done'), totals: { ...TOTALS, done: 120 } })
    layout()
    fireEvent.mouseDown(cardEl(6), { button: 0, clientX: 10, clientY: 10 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 200 })
    fireEvent.mouseUp(document, { clientX: 320, clientY: 200 })
    expect(onMove).toHaveBeenLastCalledWith(6, 'done', { where: '' }, expect.any(Object))
    fireEvent.mouseDown(cardEl(7), { button: 0, clientX: 310, clientY: 50 })
    fireEvent.mouseMove(document, { clientX: 320, clientY: 200 })
    fireEvent.mouseUp(document, { clientX: 320, clientY: 200 })
    expect(onMove).toHaveBeenLastCalledWith(7, 'done', { where: 'after', ref: 8 }, expect.any(Object))
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Board.render.test.jsx`
Expected: FAIL (cannot resolve `./Board.jsx`).

- [ ] **Step 3: Write `useCardDrag.js`**

Create `wails-app/frontend/src/pages/tasks/useCardDrag.js`:

```js
import { useCallback, useEffect, useRef, useState } from 'react'
import { dropIndex } from '../../lib/taskModel.js'

// A press becomes a drag once the mouse has moved this far; a shorter press
// is a click and opens the card.
export const DRAG_THRESHOLD = 5
const EDGE = 48 // px from a list's or the board's edge where scrolling starts
const MAX_SPEED = 16 // px per frame at the very edge

const frame = (fn) => (typeof requestAnimationFrame === 'function' ? requestAnimationFrame(fn) : setTimeout(fn, 16))
const cancelFrame = (id) => (typeof cancelAnimationFrame === 'function' ? cancelAnimationFrame(id) : clearTimeout(id))
const speed = (depth) => Math.min(MAX_SPEED, Math.ceil((depth / EDGE) * MAX_SPEED))
const idsIn = (col, without) => [...col.querySelectorAll('[data-tb-id]')].map(el => Number(el.dataset.tbId)).filter(id => id !== without)

// useCardDrag is the app's mouse ghost drag (no native drag and drop, which
// this WebKit view does not run reliably: see OrgDesigner.jsx). A press on a
// card and a move past DRAG_THRESHOLD start it; the card then follows the
// mouse as a ghost, the column under it glows, a line marks where it will
// land, and near a list's or the board's edge that one scrolls. Escape or
// the window losing focus cancels; a press on a button, link or field inside
// a card never starts one. On release over a column onDrop gets {id, from,
// fromIds, to, ids, index, ghostRect}: ids are the column's shown cards
// without the dragged one, top to bottom. A column's cards are measured once
// per drag, and again after a scroll or a new layoutKey (the shown board).
// elementFromPoint is injectable because jsdom has no layout.
export function useCardDrag({ rootRef, onDrop, canDrag, elementFromPoint, layoutKey }) {
  const [drag, setDrag] = useState(null)
  const gesture = useRef(null)
  const listeners = useRef(null)
  const scrollFrame = useRef(0)
  const clickGuard = useRef(false)
  const opts = useRef({})
  opts.current = { onDrop, canDrag, hit: elementFromPoint || ((x, y) => document.elementFromPoint(x, y)) }
  useEffect(() => { gesture.current?.cache.clear() }, [layoutKey])

  const end = useCallback(() => {
    const l = listeners.current
    if (l) {
      document.removeEventListener('mousemove', l.move)
      document.removeEventListener('mouseup', l.up)
      document.removeEventListener('keydown', l.key, true)
      document.removeEventListener('scroll', l.scrolled, true)
      window.removeEventListener('blur', l.cancel)
      listeners.current = null
    }
    if (scrollFrame.current) cancelFrame(scrollFrame.current)
    scrollFrame.current = 0
    document.body.classList.remove('tb-dragging')
    gesture.current = null
    setDrag(null)
  }, [])
  useEffect(() => end, [end]) // unmounting leaves no listener behind

  const autoScroll = useCallback(() => {
    const g = gesture.current
    if (!g?.started) {
      scrollFrame.current = 0
      return
    }
    const list = opts.current.hit(g.x, g.y)?.closest?.('[data-tb-list]')
    if (list) {
      const r = list.getBoundingClientRect()
      if (g.y < r.top + EDGE) list.scrollTop -= speed(r.top + EDGE - g.y)
      else if (g.y > r.bottom - EDGE) list.scrollTop += speed(g.y - (r.bottom - EDGE))
    }
    const board = rootRef.current?.closest?.('[data-tb-board-scroll]')
    if (board) {
      const r = board.getBoundingClientRect()
      if (g.x < r.left + EDGE) board.scrollLeft -= speed(r.left + EDGE - g.x)
      else if (g.x > r.right - EDGE) board.scrollLeft += speed(g.x - (r.right - EDGE))
    }
    scrollFrame.current = frame(autoScroll)
  }, [rootRef])

  const onCardMouseDown = useCallback((e, task) => {
    if (e.button !== 0 || listeners.current) return
    if (e.target.closest?.('button, a, input, textarea, select')) return
    if (!opts.current.canDrag(task.id)) return
    const card = e.currentTarget
    const rect = card.getBoundingClientRect()
    const fromCol = card.closest('[data-tb-column]')
    gesture.current = {
      id: task.id, from: task.status, fromIds: fromCol ? idsIn(fromCol, -1) : [],
      startX: e.clientX, startY: e.clientY, x: e.clientX, y: e.clientY,
      offsetX: e.clientX - rect.left, offsetY: e.clientY - rect.top, width: rect.width, height: rect.height,
      started: false, over: null, index: 0, ids: [], cache: new Map(),
    }
    const move = (ev) => {
      const g = gesture.current
      if (!g) return
      g.x = ev.clientX
      g.y = ev.clientY
      if (!g.started) {
        if (Math.hypot(g.x - g.startX, g.y - g.startY) < DRAG_THRESHOLD) return
        g.started = true
        document.body.classList.add('tb-dragging')
        if (!scrollFrame.current) scrollFrame.current = frame(autoScroll)
      }
      const col = opts.current.hit(g.x, g.y)?.closest?.('[data-tb-column]') || null
      g.over = col?.dataset.tbColumn || null
      if (col) {
        let m = g.cache.get(col)
        if (!m) {
          const els = [...col.querySelectorAll('[data-tb-id]')].filter(el => Number(el.dataset.tbId) !== g.id)
          m = { ids: els.map(el => Number(el.dataset.tbId)), rects: els.map(el => el.getBoundingClientRect()) }
          g.cache.set(col, m)
        }
        g.ids = m.ids
        g.index = dropIndex(m.rects, g.y)
      }
      setDrag({ id: g.id, x: g.x, y: g.y, offsetX: g.offsetX, offsetY: g.offsetY, width: g.width, overStatus: g.over, overIndex: g.index })
    }
    const up = () => {
      const g = gesture.current
      const drop = g?.started && g.over
        ? { id: g.id, from: g.from, fromIds: g.fromIds, to: g.over, ids: g.ids, index: g.index,
            ghostRect: { left: g.x - g.offsetX, top: g.y - g.offsetY, width: g.width, height: g.height } }
        : null
      if (g?.started) {
        clickGuard.current = true // the click that ends a drag must not open the card
        setTimeout(() => { clickGuard.current = false }, 0)
      }
      end()
      if (drop) opts.current.onDrop(drop)
    }
    const key = (ev) => {
      if (ev.key !== 'Escape') return
      ev.preventDefault()
      ev.stopPropagation()
      end()
    }
    const scrolled = () => gesture.current?.cache.clear() // the boxes moved
    listeners.current = { move, up, key, scrolled, cancel: end }
    document.addEventListener('mousemove', move)
    document.addEventListener('mouseup', up)
    document.addEventListener('keydown', key, true)
    document.addEventListener('scroll', scrolled, true)
    window.addEventListener('blur', end)
  }, [autoScroll, end])

  // consumeClick says, once, that the click now arriving ends a drag.
  const consumeClick = useCallback(() => {
    const was = clickGuard.current
    clickGuard.current = false
    return was
  }, [])

  return { drag, onCardMouseDown, consumeClick }
}
```

- [ ] **Step 4: Write `Board.jsx`**

Create `wails-app/frontend/src/pages/tasks/Board.jsx`:

```jsx
import { useCallback, useLayoutEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Column from './Column.jsx'
import { useCardDrag } from './useCardDrag.js'
import { COLUMNS, findTask, focusTarget, isNoopDrop, keyMove, placeFor } from '../../lib/taskModel.js'

const ARROWS = { ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down' }

// Board lays out the five columns and turns the pointer and the keys into
// moves (spec §10, Interaction): the ghost drag with its drop line, and the
// keyboard map on a focused card. Enter opens it; Shift+Left/Right move it a
// column; Alt+Up/Down reorder it; A approves an Inbox card; the plain arrows
// move the focus. A move goes to onMove(id, to, place, overrides); after a
// key move, Approve, Done or Back to Ready the focus follows the card to
// its new place.
export default function Board({ board, shown, totals, searching, now, readOnly, pendingIds, rejectedId, transitions,
  quickAdd, rootRef, hitTest, onOpen, onMove, onApprove, onReadFirst, onAnnounce, onOpenQuickAdd, onCloseQuickAdd, onAdd }) {
  const { t } = useTranslation()
  const focusAfter = useRef(null)

  useLayoutEffect(() => {
    const id = focusAfter.current
    if (id == null) return
    focusAfter.current = null
    rootRef.current?.querySelector(`[data-tb-id="${id}"]`)?.focus()
  })

  // mustRead: the move would take a card that holds more than it shows (its
  // notes, a title clamped on the card) from Inbox to Ready, where agents
  // act on all of its text. Whatever the gesture, it opens in the drawer
  // instead and is approved there, read (spec D6; the lead's ruling on
  // review F6). A card whose whole text shows moves in one gesture.
  const mustRead = useCallback((id, to) => to === 'ready' && findTask(board, id)?.status === 'inbox' &&
    rootRef.current?.querySelector(`[data-tb-id="${id}"]`)?.dataset.readFirst === 'true', [board, rootRef])

  const handleDrop = useCallback(({ id, from, fromIds, to, ids, index, ghostRect }) => {
    if (isNoopDrop(fromIds, from, to, ids, index, id)) return
    if (mustRead(id, to)) {
      onReadFirst(id, { [id]: ghostRect }) // it slides back from where it was let go
      return
    }
    let place = placeFor(ids, index)
    // Done is cut to its newest cards: right after the last one the board
    // holds is a place the next read hides, so a card new to Done goes to
    // Done's default place (the top) instead.
    const done = board.columns.done
    if (to === 'done' && from !== 'done' && place.where === 'after' && place.ref === done[done.length - 1]?.id && totals.done > done.length) {
      place = { where: '' }
    }
    onMove(id, to, place, { [id]: ghostRect })
  }, [onMove, onReadFirst, mustRead, board, totals])
  const canDrag = useCallback((id) => !readOnly && !pendingIds.has(id), [readOnly, pendingIds])
  const { drag, onCardMouseDown, consumeClick } = useCardDrag({
    rootRef, onDrop: handleDrop, canDrag, elementFromPoint: hitTest, layoutKey: shown,
  })

  const openCard = useCallback((id) => { if (!consumeClick()) onOpen(id) }, [onOpen, consumeClick])
  const approveCard = useCallback((id) => {
    if (mustRead(id, 'ready')) {
      onReadFirst(id)
      return
    }
    focusAfter.current = id
    onApprove(id)
  }, [onApprove, onReadFirst, mustRead])
  const moveToColumn = useCallback((id, to) => {
    focusAfter.current = id
    onMove(id, to, { where: '' })
  }, [onMove])

  const onKeyDown = (e) => {
    const el = e.target.closest?.('[data-tb-id]')
    if (!el || el !== e.target) return // keys pressed on a card's buttons are the buttons'
    const id = Number(el.dataset.tbId)
    if (e.key === 'Enter') {
      e.preventDefault()
      onOpen(id)
      return
    }
    if ((e.key === 'a' || e.key === 'A') && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault()
      if (readOnly) onAnnounce(t('tasks.live.readOnly'))
      else if (findTask(board, id)?.status !== 'inbox') onAnnounce(t('tasks.live.notInbox', { id }))
      else approveCard(id)
      return
    }
    const arrow = ARROWS[e.key]
    if (!arrow) return
    e.preventDefault()
    const moving = (e.shiftKey && (arrow === 'left' || arrow === 'right')) || (e.altKey && (arrow === 'up' || arrow === 'down'))
    if (moving) {
      if (readOnly) {
        onAnnounce(t('tasks.live.readOnly'))
        return
      }
      const m = keyMove(shown, id, arrow)
      if (!m) return
      if (m.blocked) {
        onAnnounce(t(`tasks.live.blocked.${m.blocked}`, { id }))
        return
      }
      if (mustRead(id, m.to)) {
        onReadFirst(id)
        return
      }
      focusAfter.current = id
      onMove(id, m.to, m.place)
      return
    }
    if (e.shiftKey || e.altKey || e.metaKey || e.ctrlKey) return
    const next = focusTarget(shown, id, arrow)
    if (next != null) rootRef.current?.querySelector(`[data-tb-id="${next}"]`)?.focus()
  }

  const cardProps = (task) => ({
    now,
    readOnly,
    pending: pendingIds.has(task.id),
    rejected: rejectedId === task.id,
    entering: transitions.entered.has(task.id),
    justDone: transitions.done.has(task.id),
    dragging: drag?.id === task.id,
    onOpen: openCard,
    onApprove: approveCard,
    onMove: moveToColumn,
    onMouseDown: onCardMouseDown,
  })

  const dragged = drag && findTask(board, drag.id)?.task
  return (
    <div className="tb-board-scroll" data-tb-board-scroll>
      <div className={`tb-board${drag ? ' tb-board--dragging' : ''}`} ref={rootRef} onKeyDown={onKeyDown}>
        {COLUMNS.map(status => (
          <Column key={status} status={status} cards={shown.columns[status]} total={totals[status]} searching={searching}
            drag={drag} readOnly={readOnly} quickAdd={quickAdd} cardProps={cardProps}
            onOpenQuickAdd={onOpenQuickAdd} onCloseQuickAdd={onCloseQuickAdd} onAdd={onAdd} />
        ))}
      </div>
      {dragged && (
        <div className="tb-ghost" data-testid="tb-ghost" aria-hidden="true"
          style={{ left: drag.x - drag.offsetX, top: drag.y - drag.offsetY, width: drag.width }}>
          <div className="tb-card-title">{dragged.title}</div>
          <div className="tb-ghost-id">#{dragged.id}</div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Add the drag section to `tasks.css`**

In `wails-app/frontend/src/pages/tasks/tasks.css`, insert immediately above the line `/* Motion */`:

```css
/* The drag: the ghost never takes the pointer (hit tests see the column under it) */
.tb-ghost {
  position: fixed; z-index: 1000; pointer-events: none; padding: 8px 10px; border-radius: var(--radius);
  background: var(--elevated); border: 1px solid var(--border-active);
  box-shadow: 0 18px 40px rgba(0, 0, 0, 0.55), var(--shadow-glow); transform: rotate(1.5deg) scale(1.03);
}
.tb-ghost-id { margin-top: 4px; font-family: var(--font-mono); font-size: 10px; color: var(--text-dim); }
.tb-board--dragging .tb-card:hover { transform: none; box-shadow: none; }
body.tb-dragging, body.tb-dragging * { cursor: grabbing !important; user-select: none !important; -webkit-user-select: none !important; }

```

- [ ] **Step 6: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Board.render.test.jsx src/pages/tasks/Column.render.test.jsx src/pages/tasks/Card.render.test.jsx src/locales/tasksKeys.test.js`
Expected: PASS (thirteen board tests, and the earlier files still pass).

- [ ] **Step 7: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. `useCardDrag.js`: `< DRAG_THRESHOLD` becomes `< 0` → "drags past the threshold …" (the ghost shows under the threshold).
2. `useCardDrag.js`: drop the line `if (e.target.closest?.('button, a, input, textarea, select')) return` → "opens on a plain click, cancels with Escape, never drags from a button …".
3. `Board.jsx`: `openCard` calls `onOpen(id)` without `consumeClick()` → "drags past the threshold …" (the card opens after the drop).
4. `Board.jsx`: drop the `isNoopDrop` check → "opens on a plain click … ignores a drop in place".
5. `Board.jsx`: drop `if (!el || el !== e.target) return`'s second condition (`|| el !== e.target`) → "leaves the keys of a card's buttons to the buttons".
6. `Board.jsx`: `canDrag` loses `!readOnly &&` → "never drags on a read-only board".
7. `Board.jsx`: drop the `if (mustRead(id, 'ready')) { … }` block from `approveCard` → "sends a card with more to read to the drawer from A and from Shift+Right"; then, separately, the `mustRead` block of the Shift+arrows branch → the same test; then the one in `handleDrop` → "opens a card with more to read dropped on Ready instead of moving it".
8. `Board.jsx`: drop `from !== 'done' &&` from the Done rule → "drops a card into a cut Done column …" (the Done card jumps to the top); then drop the whole `if` → the same test (#6 lands as the 51st).
9. `Board.jsx`: drop `focusAfter.current = id` from `approveCard` → "keeps the focus on a card that A moved".

What only a person can check (say so in the PR): the ghost following the pointer smoothly in the real WebKit view, and the auto-scroll speed near an edge (jsdom has no layout).

- [ ] **Step 8: Commit**

```
git add wails-app/frontend/src/pages/tasks/useCardDrag.js wails-app/frontend/src/pages/tasks/Board.jsx wails-app/frontend/src/pages/tasks/tasks.css wails-app/frontend/src/pages/tasks/Board.render.test.jsx
```
then
```
git commit -m "feat(tasks): the board's ghost drag and keyboard moves" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 9: The drawer

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/Drawer.jsx`
- Modify: `wails-app/frontend/src/pages/tasks/tasks.css` (the drawer section)
- Test: `wails-app/frontend/src/pages/tasks/Drawer.render.test.jsx`

**Interfaces:**
- Consumes: Task 3's `tasksApi.show/edit/comment`; `api.openURL(url)` (`services/api.js`); `ChatMarkdown` and `isAllowedURL(href)` (named exports of `components/chat/ChatMarkdown.jsx`: markdown without raw HTML, links only for http, https and mailto, opened through `api.openURL`, images never fetched); Task 4's `COLUMNS`, `actorColor`, `claimState`, `hostOf`, `sourceChip`; Task 7's `leaseTime`.
- Produces: `Drawer` (default): props `task` (the board's copy), `now`, `readOnly`, `hint` (true when it opened in place of a move to Ready: it then says why, as a `role="note"` line), `onClose()`, `onMove(id, to, place)`, `onApprove(id)`, `onArchive(id)`, `onChanged()` (the page reads the board again). It reads the history itself (`tasksApi.show`) and again whenever the board's copy of the task changes (`updated_at` or `last_event.at`). Root: `<aside role="dialog" data-testid="tb-drawer">`. The title field takes at most 200 characters (where the CLI cuts a title); Escape closes the drawer unless an edit is in progress or an `aria-modal="true"` dialog is open.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/Drawer.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
vi.mock('../../services/tasks.js', () => ({ tasksApi: { show: vi.fn(), edit: vi.fn(), comment: vi.fn() } }))
vi.mock('../../services/api.js', () => ({ api: { openURL: vi.fn() } }))
import '../../i18n.js'
import i18n from 'i18next'
import { tasksApi } from '../../services/tasks.js'
import { api } from '../../services/api.js'
import Drawer from './Drawer.jsx'

const T0 = Date.parse('2026-10-06T09:00:00Z')
const task = (extra = {}) => ({
  id: 12, title: 'Fix the login test', status: 'inbox', position: 1024,
  notes: 'See **the log** and [the run](https://ci.example.com/run/1) ![x](https://tracker.example/x.png) [bad](javascript:alert(1))',
  source: { kind: 'chrome', url: 'https://github.com/a/b', title: 'PR 41', app: '' }, claim: null,
  last_event: { actor: 'chrome', kind: 'created', at: '2026-10-06T08:00:00Z' },
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z', ...extra,
})
const EVENTS = [
  { id: 1, at: '2026-10-06T08:00:00Z', actor: 'chrome', kind: 'created', from_status: '', to_status: 'inbox', note: '' },
  { id: 2, at: '2026-10-06T08:30:00Z', actor: 'claude-code#a3f9', kind: 'question', from_status: 'in_progress', to_status: 'review', note: 'Which **database**?' },
]
function setup(props = {}) {
  const calls = { onClose: vi.fn(), onMove: vi.fn(), onApprove: vi.fn(), onArchive: vi.fn(), onChanged: vi.fn() }
  render(<Drawer task={task()} now={T0} readOnly={false} {...calls} {...props} />)
  return calls
}

beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  tasksApi.show.mockResolvedValue({ profile: {}, task: task(), events: EVENTS })
  tasksApi.edit.mockResolvedValue({ task: task() })
  tasksApi.comment.mockResolvedValue({ task: task() })
})
afterEach(cleanup)

describe('Drawer', () => {
  it('shows the notes as markdown, opens safe links in the browser, never loads an image', () => {
    setup()
    expect(screen.getByText('the log').tagName).toBe('STRONG')
    expect(document.querySelector('[data-testid="tb-drawer"] img')).toBeNull()
    fireEvent.click(screen.getByRole('link', { name: 'the run' }))
    expect(api.openURL).toHaveBeenCalledWith('https://ci.example.com/run/1')
    expect(screen.queryByRole('link', { name: 'bad' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /PR 41/ }))
    expect(api.openURL).toHaveBeenLastCalledWith('https://github.com/a/b')
  })

  it('shows the history, with the agent\'s question marked', async () => {
    setup()
    await waitFor(() => expect(document.querySelectorAll('.tb-event')).toHaveLength(2))
    const q = document.querySelector('.tb-event[data-kind="question"]')
    expect(q).toHaveTextContent('claude-code#a3f9')
    expect(q).toHaveTextContent('Question')
    expect(q.querySelector('strong')).toHaveTextContent('database')
    expect(tasksApi.show).toHaveBeenCalledWith(12)
  })

  // The store writes a released event whenever a claim ends: an agent's own
  // release (the card goes to Ready) and also the operator's move or archive of
  // a card an agent holds, with the card's destination as its to_status. So
  // the line names the claim that ended and never a column.
  it('words a released event by the claim that ended, not by a column', async () => {
    tasksApi.show.mockResolvedValue({ profile: {}, task: task(), events: [...EVENTS,
      { id: 3, at: '2026-10-06T08:40:00Z', actor: 'you', kind: 'released', from_status: 'in_progress', to_status: 'done', note: 'the claim of claude-code#a3f9 ended: the operator moved the card' },
      { id: 4, at: '2026-10-06T08:40:00Z', actor: 'you', kind: 'moved', from_status: 'in_progress', to_status: 'done', note: '' },
    ] })
    setup()
    await waitFor(() => expect(document.querySelectorAll('.tb-event')).toHaveLength(4))
    const released = document.querySelector('.tb-event[data-kind="released"]')
    expect(released).toHaveTextContent('Claim ended')
    expect(released).not.toHaveTextContent('Ready')
    expect(released).toHaveTextContent('the operator moved the card')
    expect(document.querySelector('.tb-event[data-kind="moved"]')).toHaveTextContent('Moved from In progress to Done')
  })

  it('saves the title on Enter and the notes on Cmd+Enter, then asks for a new read', async () => {
    const { onChanged } = setup()
    const titleBox = screen.getByLabelText('Title')
    expect(titleBox).toHaveFocus()
    expect(titleBox).toHaveAttribute('maxlength', '200') // the CLI would cut a longer title
    fireEvent.change(titleBox, { target: { value: '-x Fix the login test' } })
    fireEvent.keyDown(titleBox, { key: 'Enter', isComposing: true }) // confirms an input method's word
    expect(titleBox).toHaveFocus()
    fireEvent.keyDown(titleBox, { key: 'Enter' }) // leaves the field, which saves
    await waitFor(() => expect(tasksApi.edit).toHaveBeenCalledWith(12, { title: '-x Fix the login test' }))
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: /Edit/ }))
    const notesBox = screen.getByRole('textbox', { name: 'Notes' })
    fireEvent.change(notesBox, { target: { value: '' } })
    fireEvent.keyDown(notesBox, { key: 'Enter', metaKey: true })
    await waitFor(() => expect(tasksApi.edit).toHaveBeenLastCalledWith(12, { notes: '' }))
  })

  it('sends a comment, approves, moves and archives', async () => {
    const { onApprove, onMove, onArchive, onChanged } = setup()
    const box = screen.getByRole('textbox', { name: 'Comment' })
    fireEvent.change(box, { target: { value: '--looks good' } })
    fireEvent.click(screen.getByRole('button', { name: /Send/ }))
    await waitFor(() => expect(tasksApi.comment).toHaveBeenCalledWith(12, '--looks good'))
    await waitFor(() => expect(box).toHaveValue(''))
    expect(onChanged).toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /^Approve/ }))
    expect(onApprove).toHaveBeenCalledWith(12)
    fireEvent.click(screen.getByRole('button', { name: 'Review' }))
    expect(onMove).toHaveBeenCalledWith(12, 'review', { where: '' })
    expect(screen.getByRole('button', { name: 'Inbox' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /Archive/ }))
    expect(onArchive).toHaveBeenCalledWith(12)
  })

  it('says why an edit was refused and keeps the text', async () => {
    tasksApi.edit.mockResolvedValue({ error: 'only the operator can edit a task', code: 'operator_only' })
    const { onChanged } = setup()
    const titleBox = screen.getByLabelText('Title')
    fireEvent.change(titleBox, { target: { value: 'New title' } })
    fireEvent.blur(titleBox)
    expect(await screen.findByRole('alert')).toHaveTextContent('only the operator')
    expect(titleBox).toHaveValue('New title')
    expect(onChanged).not.toHaveBeenCalled()
  })

  it('closes on Escape, but an edit in progress takes the first Escape', () => {
    const { onClose } = setup()
    const titleBox = screen.getByLabelText('Title')
    fireEvent.change(titleBox, { target: { value: 'changed' } })
    fireEvent.keyDown(titleBox, { key: 'Escape' })
    expect(titleBox).toHaveValue('Fix the login test')
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.keyDown(titleBox, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  // The take-back confirmation opened from "Move to" is a modal dialog with
  // its own Escape (on window, after this document listener).
  it('leaves Escape to a modal dialog above it', () => {
    const { onClose } = setup()
    const modal = document.createElement('div')
    modal.setAttribute('aria-modal', 'true')
    document.body.appendChild(modal)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
    modal.remove()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('says why it opened in place of a move to Ready', () => {
    setup({ hint: true })
    expect(screen.getByRole('note')).toHaveTextContent('Not moved: read it first, then approve it here.')
    cleanup()
    setup()
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('is read-only when the app runs under an agent\'s shell', () => {
    setup({ readOnly: true })
    expect(screen.queryByRole('button', { name: /Approve|Archive|Edit|Send/ })).toBeNull()
    expect(screen.getByLabelText('Title')).toHaveAttribute('readonly')
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Drawer.render.test.jsx`
Expected: FAIL (cannot resolve `./Drawer.jsx`).

- [ ] **Step 3: Write `Drawer.jsx`**

Create `wails-app/frontend/src/pages/tasks/Drawer.jsx`:

```jsx
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { X, ExternalLink, Archive, Pencil, Send, Check, LoaderCircle } from 'lucide-react'
import { ChatMarkdown, isAllowedURL } from '../../components/chat/ChatMarkdown.jsx'
import { tasksApi } from '../../services/tasks.js'
import { api } from '../../services/api.js'
import { COLUMNS, actorColor, claimState, hostOf, sourceChip } from '../../lib/taskModel.js'
import { leaseTime } from './Card.jsx'

const EVENT_KINDS = new Set(['created', 'edited', 'moved', 'claimed', 'reclaimed', 'comment', 'question', 'result', 'released', 'archived', 'unarchived'])

const when = (iso) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
}
const initialOf = (name) => (String(name).match(/[A-Za-z0-9]/)?.[0] || '?').toUpperCase()

// Drawer is a card's detail beside the board (spec §10): its title and
// notes, editable (notes shown as markdown and edited as plain text; links
// open in the browser and images never load, because the text may come from
// a web page), where it came from, its history with an agent's questions
// marked, the operator's comment box, and Approve, Move to and Archive.
// Escape closes it; an edit in progress, or a modal dialog above it, takes
// the Escape first. Enter that confirms an input method's word is the
// input method's.
export default function Drawer({ task, now, readOnly, hint, onClose, onMove, onApprove, onArchive, onChanged }) {
  const { t } = useTranslation()
  const [history, setHistory] = useState(null) // {events} or {error}
  const [title, setTitle] = useState(task.title)
  const [notes, setNotes] = useState(task.notes)
  const [editing, setEditing] = useState(false)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState('') // '' | 'title' | 'notes' | 'comment'
  const [error, setError] = useState('')
  const titleRef = useRef(null)
  const version = `${task.updated_at}|${task.last_event?.at || ''}`

  useEffect(() => { // read again whenever the board shows the task changed
    let alive = true
    tasksApi.show(task.id).then(res => { if (alive) setHistory(res) })
    return () => { alive = false }
  }, [task.id, version])
  useEffect(() => { setTitle(task.title) }, [task.title])
  useEffect(() => { if (!editing) setNotes(task.notes) }, [task.notes, editing])
  useEffect(() => { titleRef.current?.focus() }, [task.id])
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape' && !e.defaultPrevented && !document.querySelector('[aria-modal="true"]')) onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const save = async (field, change) => {
    setBusy(field)
    setError('')
    const res = await tasksApi.edit(task.id, change)
    setBusy('')
    if (res?.error) {
      setError(res.error)
      return false
    }
    onChanged()
    return true
  }
  const saveTitle = () => {
    const v = title.trim()
    if (!v || v === task.title) {
      setTitle(task.title)
      return
    }
    save('title', { title: v })
  }
  const saveNotes = async () => {
    if (await save('notes', { notes })) setEditing(false)
  }
  const cancelNotes = () => {
    setNotes(task.notes)
    setEditing(false)
  }
  const send = async () => {
    const text = comment.trim()
    if (!text || busy) return
    setBusy('comment')
    setError('')
    const res = await tasksApi.comment(task.id, text)
    setBusy('')
    if (res?.error) {
      setError(res.error)
      return
    }
    setComment('')
    onChanged()
  }

  const chip = sourceChip(task)
  const claim = claimState(task.claim, now)
  const link = task.source?.url && isAllowedURL(task.source.url) ? task.source.url : ''
  const actorName = (a) => (['you', 'chrome', 'os'].includes(a) ? t(`tasks.actor.${a}`) : a)
  const column = (s) => (COLUMNS.includes(s) ? t(`tasks.columns.${s}`) : s)
  const events = history?.events || []
  return (
    <aside className="tb-drawer" role="dialog" aria-modal="false" aria-label={t('tasks.drawer.label', { id: task.id })} data-testid="tb-drawer">
      <header className="tb-drawer-head">
        <span className="tb-id">#{task.id}</span>
        <span className={`tb-pill tb-col--${task.status}`}>{column(task.status)}</span>
        <span style={{ flex: 1 }} />
        <button type="button" className="tb-icon-btn" onClick={onClose} aria-label={t('tasks.drawer.close')}>
          <X size={14} aria-hidden="true" />
        </button>
      </header>
      <div className="tb-drawer-body">
        {hint && <p className="tb-drawer-hint" role="note">{t('tasks.drawer.readFirst')}</p>}
        <label className="tb-field-label" htmlFor="tb-drawer-title">{t('tasks.drawer.titleLabel')}</label>
        <input id="tb-drawer-title" ref={titleRef} className="tb-title-input" value={title} readOnly={readOnly || busy === 'title'}
          maxLength={200} onChange={e => setTitle(e.target.value)} onBlur={() => { if (!readOnly) saveTitle() }}
          onKeyDown={e => {
            if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
              e.preventDefault()
              e.currentTarget.blur()
            } else if (e.key === 'Escape' && title !== task.title) {
              e.preventDefault()
              setTitle(task.title)
            }
          }} />
        <div className="tb-row tb-meta-row">
          <span className="tb-chip"><span>{chip.text || t(`tasks.source.${chip.kind}`)}</span></span>
          {link && (
            <button type="button" className="tb-link" onClick={() => api.openURL(link)} title={link}>
              <ExternalLink size={11} aria-hidden="true" /> {task.source.title || hostOf(link)}
            </button>
          )}
          <span>{t('tasks.drawer.added', { when: when(task.created_at) })}</span>
          {claim && (
            <span className="tb-claim" data-stale={claim.stale ? 'true' : undefined}
              style={{ '--tb-actor': claim.stale ? 'var(--tb-amber)' : actorColor(claim.by) }}>
              <span className="tb-avatar" aria-hidden="true">{claim.initial}</span>
              <span>{claim.by} · {t(claim.stale ? 'tasks.claim.over' : 'tasks.claim.left', { time: leaseTime(t, claim.minutes) })}</span>
            </span>
          )}
        </div>

        <div className="tb-section-head">
          <span className="tb-field-label">{t('tasks.drawer.notes')}</span>
          {!readOnly && !editing && (
            <button type="button" className="tb-act" onClick={() => setEditing(true)}>
              <Pencil size={11} aria-hidden="true" /> {t('tasks.drawer.editNotes')}
            </button>
          )}
        </div>
        {editing ? (
          <div>
            <textarea className="tb-notes-input" value={notes} autoFocus readOnly={busy === 'notes'} aria-label={t('tasks.drawer.notes')}
              onChange={e => setNotes(e.target.value)}
              onKeyDown={e => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  saveNotes()
                } else if (e.key === 'Escape') {
                  e.preventDefault()
                  cancelNotes()
                }
              }} />
            <div className="tb-row">
              <button type="button" className="btn btn-primary btn-sm" disabled={busy === 'notes'} onClick={saveNotes}>
                {busy === 'notes' ? t('tasks.drawer.saving') : t('tasks.drawer.save')}
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={cancelNotes}>{t('tasks.drawer.cancel')}</button>
            </div>
          </div>
        ) : (
          <div className="tb-notes">
            {task.notes ? <ChatMarkdown content={task.notes} /> : <span className="tb-muted">{t('tasks.drawer.noNotes')}</span>}
          </div>
        )}

        {!readOnly && (
          <div className="tb-actions">
            {task.status === 'inbox' && (
              <div>
                <button type="button" className="btn btn-primary btn-sm" onClick={() => onApprove(task.id)}>
                  <Check size={13} aria-hidden="true" /> {t('tasks.drawer.approve')}
                </button>
                <p className="tb-help">{t('tasks.drawer.approveHelp')}</p>
              </div>
            )}
            <div className="tb-field-label">{t('tasks.drawer.moveTo')}</div>
            <div className="tb-moves" role="group" aria-label={t('tasks.drawer.moveTo')}>
              {COLUMNS.map(s => (
                <button key={s} type="button" className="tb-act" aria-pressed={s === task.status} disabled={s === task.status}
                  onClick={() => onMove(task.id, s, { where: '' })}>{column(s)}</button>
              ))}
            </div>
            <button type="button" className="btn btn-danger btn-sm tb-archive" onClick={() => onArchive(task.id)}>
              <Archive size={12} aria-hidden="true" /> {t('tasks.drawer.archive')}
            </button>
          </div>
        )}
        {error && <div className="tb-error-line" role="alert">{error}</div>}

        <div className="tb-field-label">{t('tasks.drawer.history')}</div>
        {!history && (
          <div className="tb-muted"><LoaderCircle size={12} className="spin" aria-hidden="true" /> {t('tasks.drawer.loadingHistory')}</div>
        )}
        {history?.error && <div className="tb-error-line">{t('tasks.drawer.historyFailed', { error: history.error })}</div>}
        <ol className="tb-history">
          {events.map(ev => (
            <li key={ev.id} className="tb-event" data-kind={ev.kind}>
              <span className="tb-avatar" aria-hidden="true" style={{ '--tb-actor': ev.actor === 'you' ? 'var(--cyan)' : actorColor(ev.actor) }}>
                {initialOf(ev.actor)}
              </span>
              <div className="tb-event-body">
                <div className="tb-event-line">
                  <b>{actorName(ev.actor)}</b>
                  <span>{t(`tasks.event.${EVENT_KINDS.has(ev.kind) ? ev.kind : 'other'}`, { from: column(ev.from_status), to: column(ev.to_status) })}</span>
                  <time dateTime={ev.at}>{when(ev.at)}</time>
                </div>
                {ev.note && <div className="tb-event-note"><ChatMarkdown content={ev.note} /></div>}
              </div>
            </li>
          ))}
        </ol>

        {!readOnly && (
          <div className="tb-comment">
            <textarea value={comment} placeholder={t('tasks.drawer.commentPlaceholder')} aria-label={t('tasks.drawer.comment')}
              readOnly={busy === 'comment'} onChange={e => setComment(e.target.value)}
              onKeyDown={e => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  send()
                } else if (e.key === 'Escape' && comment) {
                  e.preventDefault()
                  setComment('')
                }
              }} />
            <div className="tb-row">
              <span className="tb-muted">{t('tasks.drawer.sendHint')}</span>
              <button type="button" className="btn btn-secondary btn-sm" disabled={!comment.trim() || busy === 'comment'} onClick={send}>
                <Send size={12} aria-hidden="true" /> {t('tasks.drawer.send')}
              </button>
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}
```

- [ ] **Step 4: Add the drawer section to `tasks.css`**

Insert immediately above the line `/* Motion */`:

```css
/* The drawer: over the right of the board, which stays in view */
.tb-drawer {
  position: absolute; top: 0; right: 0; bottom: 0; z-index: 20; width: min(440px, 92%);
  display: flex; flex-direction: column; background: var(--surface); border-left: 1px solid var(--border-bright);
  box-shadow: -20px 0 50px rgba(0, 0, 0, 0.5); animation: tbSlideIn 200ms ease-out;
}
.tb-drawer-head { display: flex; align-items: center; gap: 8px; padding: 10px 12px; border-bottom: 1px solid var(--border); }
.tb-pill {
  font-family: var(--font-mono); font-size: 10px; padding: 1px 8px; border-radius: 999px;
  color: var(--tb-accent); border: 1px solid var(--tb-accent); background: var(--elevated);
}
.tb-drawer-body { flex: 1; overflow-y: auto; padding: 12px 14px 16px; display: flex; flex-direction: column; gap: 10px; font-size: 12.5px; }
.tb-drawer-hint {
  padding: 7px 10px; border-radius: var(--radius); font-size: 12px; line-height: 1.45; color: var(--text);
  background: var(--elevated); border: 1px solid var(--border-active); animation: tbRise 200ms ease-out;
}
.tb-field-label { font-family: var(--font-mono); font-size: 10px; font-weight: 600; letter-spacing: 1px; text-transform: uppercase; color: var(--text-muted); }
.tb-title-input {
  width: 100%; padding: 6px 8px; border-radius: var(--radius); border: 1px solid transparent; background-color: transparent;
  color: var(--text); font-family: var(--font-display); font-size: 16px; font-weight: 700;
}
.tb-title-input:hover { border-color: var(--border); }
.tb-title-input:focus { outline: none; border-color: var(--border-bright); background-color: var(--elevated); box-shadow: 0 0 0 3px var(--cyan-glow); }
.tb-row { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.tb-meta-row { font-family: var(--font-mono); font-size: 10.5px; color: var(--text-muted); }
.tb-link { display: inline-flex; align-items: center; gap: 4px; padding: 0; border: none; background-color: transparent; color: var(--cyan); font: inherit; cursor: pointer; }
.tb-link:hover { text-decoration: underline; }
.tb-section-head { display: flex; align-items: center; justify-content: space-between; }
.tb-notes { padding: 8px 10px; border-radius: var(--radius); background: var(--elevated); border: 1px solid var(--border-dim); line-height: 1.55; overflow-wrap: anywhere; }
.tb-notes-input, .tb-comment textarea {
  width: 100%; min-height: 110px; resize: vertical; padding: 8px 10px; border-radius: var(--radius);
  border: 1px solid var(--border); background-color: var(--elevated); color: var(--text); font-family: var(--font-mono); font-size: 12px; line-height: 1.5;
}
.tb-comment textarea { min-height: 64px; font-family: var(--font-body); }
.tb-notes-input:focus, .tb-comment textarea:focus { outline: none; border-color: var(--border-bright); box-shadow: 0 0 0 3px var(--cyan-glow); }
.tb-muted { display: inline-flex; align-items: center; gap: 5px; font-size: 11px; color: var(--text-muted); }
.tb-help { margin-top: 4px; font-size: 11px; color: var(--text-muted); }
.tb-actions { display: flex; flex-direction: column; gap: 8px; padding: 10px; border-radius: var(--radius); border: 1px solid var(--border-dim); }
.tb-moves { display: flex; flex-wrap: wrap; gap: 6px; }
.tb-moves .tb-act[aria-pressed="true"] { color: var(--cyan); border-color: var(--border-active); }
.tb-archive { align-self: flex-start; }
.tb-error-line { font-size: 11.5px; color: var(--red); }
.tb-history { list-style: none; position: relative; display: flex; flex-direction: column; gap: 10px; padding-left: 2px; }
.tb-history::before { content: ''; position: absolute; left: 11px; top: 6px; bottom: 6px; width: 1px; background: var(--border); }
.tb-event { position: relative; display: flex; gap: 8px; }
.tb-event .tb-avatar { width: 20px; height: 20px; }
.tb-event-body { flex: 1; min-width: 0; }
.tb-event-line { display: flex; align-items: baseline; flex-wrap: wrap; gap: 6px; font-size: 12px; color: var(--text-secondary); }
.tb-event-line b { color: var(--text); font-weight: 600; }
.tb-event-line time { margin-left: auto; font-family: var(--font-mono); font-size: 10px; color: var(--text-dim); }
.tb-event-note { margin-top: 4px; padding: 6px 8px; border-radius: 4px; background: var(--elevated); overflow-wrap: anywhere; }
.tb-event[data-kind="question"] .tb-event-note { border: 1px solid rgba(245, 158, 11, 0.35); background-color: rgba(245, 158, 11, 0.08); }
.tb-event[data-kind="result"] .tb-event-note { border: 1px solid rgba(16, 185, 129, 0.3); }
.tb-comment { display: flex; flex-direction: column; gap: 6px; }

```

- [ ] **Step 5: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/Drawer.render.test.jsx src/locales/tasksKeys.test.js`
Expected: PASS (ten drawer tests; the keys test now scans `Drawer.jsx` too).

- [ ] **Step 6: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. Render the notes as text (`{task.notes}` in place of `<ChatMarkdown content={task.notes} />`) → "shows the notes as markdown …".
2. In the title's `onKeyDown`, drop `e.preventDefault()` from the Escape branch → "closes on Escape, but an edit in progress takes the first Escape".
3. In `save`, call `onChanged()` before the `if (res?.error)` check → "says why an edit was refused and keeps the text".
4. Drop `maxLength={200}`, then (separately) the title's `&& !e.nativeEvent.isComposing` → "saves the title on Enter and the notes on Cmd+Enter …".
5. Drop `&& !document.querySelector('[aria-modal="true"]')` → "leaves Escape to a modal dialog above it".
6. In `en.json`, put `"released": "Released to Ready"` back → "words a released event by the claim that ended, not by a column".

- [ ] **Step 7: Commit**

```
git add wails-app/frontend/src/pages/tasks/Drawer.jsx wails-app/frontend/src/pages/tasks/Drawer.render.test.jsx wails-app/frontend/src/pages/tasks/tasks.css
```
then
```
git commit -m "feat(tasks): the task drawer: edit, history, comment, approve, move, archive" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 10a: Motion: FLIP slides and lasting marks

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/useFlip.js`, `wails-app/frontend/src/pages/tasks/useMarks.js`
- Test: `wails-app/frontend/src/pages/tasks/motion.test.jsx`

**Interfaces:**
- Consumes: Task 4's `transitions(prev, next) → {entered: Set, done: Set}` and `normalizeBoard`; Task 7a's `tasks.css` (its reduced-motion block and `.tb-page.paused` rule).
- Produces:
  - `useFlip(rootRef, reduced) → capture(overrides)`; `FLIP_MS = 220`. `capture` records every `[data-tb-id]` box under `rootRef`; the render after it animates each card that moved; under `reduced` it records nothing.
  - `useMarks(board) → {entered: Set, done: Set}`; `ENTER_MS = 400`, `DONE_MS = 1000`. A mark starts on the first board that shows the change and lasts its own time, whatever boards follow (review F1: the read that confirms a move no longer cuts the Done pulse short).

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/motion.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, act, cleanup } from '@testing-library/react'
import { useRef } from 'react'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { useFlip, FLIP_MS } from './useFlip.js'
import { useMarks, DONE_MS } from './useMarks.js'
import { normalizeBoard } from '../../lib/taskModel.js'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.useRealTimers()
  delete Element.prototype.animate // jsdom has no Web Animations
})

function FlipHarness({ reduced, order, cap }) {
  const rootRef = useRef(null)
  cap.current = useFlip(rootRef, reduced)
  return <ul ref={rootRef}>{order.map(id => <li key={id} data-tb-id={id}>{id}</li>)}</ul>
}

// Two cards; after the change #1 sits 100 px left and 40 px lower.
function flipOnce(reduced) {
  const animate = vi.fn()
  Element.prototype.animate = animate
  const boxes = { 1: { left: 300, top: 40 }, 2: { left: 300, top: 90 } }
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function () {
    const b = boxes[this.dataset?.tbId] || { left: 0, top: 0 }
    return { ...b, width: 200, height: 40, right: b.left + 200, bottom: b.top + 40 }
  })
  const cap = { current: null }
  const { rerender } = render(<FlipHarness reduced={reduced} order={[1, 2]} cap={cap} />)
  cap.current()
  boxes[1] = { left: 200, top: 80 }
  rerender(<FlipHarness reduced={reduced} order={[2, 1]} cap={cap} />)
  return animate
}

describe('useFlip', () => {
  it('slides a moved card from its old box to its new one', () => {
    const animate = flipOnce(false)
    expect(animate).toHaveBeenCalledTimes(1) // #2 did not move
    expect(animate.mock.calls[0][0]).toEqual([{ transform: 'translate(100px, -40px)' }, { transform: 'none' }])
    expect(animate.mock.calls[0][1]).toMatchObject({ duration: FLIP_MS })
  })

  it('does nothing under reduced motion', () => {
    expect(flipOnce(true)).not.toHaveBeenCalled()
  })
})

const card = (id, status) => ({
  id, title: `Task ${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null, last_event: null,
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z',
})
const boardWith = (rev, review, done) => normalizeBoard({
  profile: { id: 'default', name: 'Default' }, rev, counts: { done: done.length },
  tasks: { inbox: [], ready: [], in_progress: [], review, done },
})

function MarksHarness({ board, out }) {
  out.current = useMarks(board)
  return null
}

describe('useMarks', () => {
  it('keeps the Done pulse through the read that confirms the move', () => {
    vi.useFakeTimers()
    const out = { current: null }
    const { rerender } = render(<MarksHarness board={boardWith(1, [card(6, 'review')], [])} out={out} />)
    expect(out.current.done.size).toBe(0)
    rerender(<MarksHarness board={boardWith(1, [], [card(6, 'done')])} out={out} />) // the optimistic move
    expect(out.current.done.has(6)).toBe(true)
    act(() => { vi.advanceTimersByTime(250) })
    rerender(<MarksHarness board={boardWith(2, [], [card(6, 'done')])} out={out} />) // the confirming read
    expect(out.current.done.has(6)).toBe(true)
    act(() => { vi.advanceTimersByTime(DONE_MS) })
    expect(out.current.done.has(6)).toBe(false)
  })

  // A move made in a terminal, or a confirming read that failed: no board
  // follows, and the mark must still end.
  it('lets a mark go after its time when no board follows', () => {
    vi.useFakeTimers()
    const out = { current: null }
    const { rerender } = render(<MarksHarness board={boardWith(1, [card(6, 'review')], [])} out={out} />)
    rerender(<MarksHarness board={boardWith(2, [], [card(6, 'done')])} out={out} />)
    expect(out.current.done.has(6)).toBe(true)
    act(() => { vi.advanceTimersByTime(DONE_MS) })
    expect(out.current.done.has(6)).toBe(false)
  })
})

describe('tasks.css motion', () => {
  const css = readFileSync(join(__dirname, 'tasks.css'), 'utf8')
  it('switches every animation off under reduced motion and pauses them while the window is hidden', () => {
    const at = css.indexOf('@media (prefers-reduced-motion: reduce)')
    expect(at).toBeGreaterThan(-1)
    expect(css.slice(at)).toMatch(/\.tb-page \*[^{]*\{[^}]*animation: none !important;[^}]*transition: none !important;/)
    expect(css).toMatch(/\.tb-page\.paused \*[^{]*\{[^}]*animation-play-state: paused !important;/)
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/motion.test.jsx`
Expected: FAIL (cannot resolve `./useFlip.js`).

- [ ] **Step 3: Write `useFlip.js`**

Create `wails-app/frontend/src/pages/tasks/useFlip.js`:

```js
import { useCallback, useLayoutEffect, useRef } from 'react'

export const FLIP_MS = 220
const FRESH_MS = 600

// useFlip slides cards from where they were to where a change put them
// (FLIP). capture(overrides) records every card's box just before a change
// (overrides: {id: box}, e.g. where the drag ghost was let go); the render
// that follows animates each card that moved from its old box with the Web
// Animations API, which the CSS reduced-motion rule does not reach: under
// reduced motion nothing is recorded. Empty boxes (a hidden board) are
// skipped, and a capture no render used within FRESH_MS is dropped.
export function useFlip(rootRef, reduced) {
  const first = useRef(null)
  const capture = useCallback((overrides) => {
    const root = rootRef.current
    if (reduced || !root) return
    const boxes = new Map()
    for (const el of root.querySelectorAll('[data-tb-id]')) {
      const r = el.getBoundingClientRect()
      if (r.width || r.height) boxes.set(el.dataset.tbId, r)
    }
    for (const [id, r] of Object.entries(overrides || {})) if (r) boxes.set(String(id), r)
    first.current = { boxes, at: Date.now() }
  }, [rootRef, reduced])

  useLayoutEffect(() => {
    const snap = first.current
    const root = rootRef.current
    if (!snap || !root) return
    first.current = null
    if (Date.now() - snap.at > FRESH_MS) return
    for (const el of root.querySelectorAll('[data-tb-id]')) {
      const was = snap.boxes.get(el.dataset.tbId)
      if (!was || typeof el.animate !== 'function') continue
      const now = el.getBoundingClientRect()
      const dx = was.left - now.left
      const dy = was.top - now.top
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue
      el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }],
        { duration: FLIP_MS, easing: 'cubic-bezier(.2,.75,.25,1)' })
    }
  })
  return capture
}
```

- [ ] **Step 4: Write `useMarks.js`**

Create `wails-app/frontend/src/pages/tasks/useMarks.js`:

```js
import { useEffect, useMemo, useRef, useState } from 'react'
import { transitions } from '../../lib/taskModel.js'

export const ENTER_MS = 400 // covers the 200 ms entrance
export const DONE_MS = 1000 // covers the 900 ms Done glow

const EMPTY = { entered: new Set(), done: new Set() }

// useMarks says which cards just entered the board and which just reached
// Done (spec §10, Motion). A mark starts on the first board that shows the
// change, which is often the optimistic one, and lasts its own time: the
// read that confirms the move arrives 150 to 300 ms later with the card
// already in Done, and must not cut its pulse short (review F1). Timers end
// only with the page.
export function useMarks(board) {
  const prev = useRef(null)
  const timers = useRef(new Map())
  const [kept, setKept] = useState(EMPTY)
  const fresh = useMemo(() => transitions(prev.current, board), [board]) // prev: the board drawn before

  useEffect(() => {
    prev.current = board
    for (const [kind, ms] of [['entered', ENTER_MS], ['done', DONE_MS]]) {
      const ids = fresh[kind]
      if (!ids.size) continue
      for (const id of ids) {
        const key = `${kind}:${id}`
        clearTimeout(timers.current.get(key))
        timers.current.set(key, setTimeout(() => {
          timers.current.delete(key)
          setKept(k => ({ ...k, [kind]: new Set([...k[kind]].filter(x => x !== id)) }))
        }, ms))
      }
      setKept(k => ({ ...k, [kind]: new Set([...k[kind], ...ids]) }))
    }
  }, [board, fresh])
  useEffect(() => {
    const all = timers.current
    return () => all.forEach(clearTimeout)
  }, [])

  // Until the effect has taken this board, its fresh marks show at once
  // (the first paint animates); after that only kept marks count, so each
  // ends with its timer even when no other board follows.
  return useMemo(() => (prev.current === board ? kept : {
    entered: new Set([...kept.entered, ...fresh.entered]),
    done: new Set([...kept.done, ...fresh.done]),
  }), [kept, fresh, board])
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/motion.test.jsx`
Expected: PASS (five tests).

- [ ] **Step 6: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. In `capture`, drop `reduced ||` → "does nothing under reduced motion".
2. In `useMarks`, `return fresh` in place of the `useMemo` → "keeps the Done pulse through the read that confirms the move".
3. In `useMarks`, drop `prev.current === board ? kept :` (always merge the fresh marks) → "lets a mark go after its time when no board follows".
4. In `tasks.css`, delete the `@media (prefers-reduced-motion: reduce)` block → "switches every animation off …".

`Tasks.jsx` (Task 10c) reads the reduced-motion preference once, when the page mounts: the CSS follows a change at once, FLIP on the next launch. Say so in the PR.

- [ ] **Step 7: Commit**

```
git add wails-app/frontend/src/pages/tasks/useFlip.js wails-app/frontend/src/pages/tasks/useMarks.js wails-app/frontend/src/pages/tasks/motion.test.jsx
```
then
```
git commit -m "feat(tasks): FLIP slides and motion marks that outlast the confirming read" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 10b: Activity notes and How to capture

**Files:**
- Create: `wails-app/frontend/src/pages/tasks/ActivityToasts.jsx`, `wails-app/frontend/src/pages/tasks/HowToCapture.jsx`
- Modify: `wails-app/frontend/src/pages/tasks/tasks.css` (activity and how-to sections)
- Test: `wails-app/frontend/src/pages/tasks/ActivityToasts.render.test.jsx` (both components)

**Interfaces:**
- Consumes: Task 4's `COLUMNS`, `actorColor`; Task 6's keys; Task 7a's `.tb-avatar`, `.tb-act`, `.tb-icon-btn`.
- Produces: `ActivityToasts({ items, onDismiss(key), onOpen(id), onUndo(item) })` with items `{key, at, id, title, actor, kind, to?, undo?}`, `ACTIVITY_MS = 6000` and `MAX_NOTES = 3` (it shows the newest three); `HowToCapture({ profileId })`, whose button stays disabled until `profileId` is known (review minor 8). The activity stack is `data-testid="tb-activity"`.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/tasks/ActivityToasts.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import '../../i18n.js'
import i18n from 'i18next'
import ActivityToasts, { ACTIVITY_MS, MAX_NOTES } from './ActivityToasts.jsx'
import HowToCapture from './HowToCapture.jsx'

const note = (key, extra = {}) => ({ key, at: Date.now(), id: key, title: `Task ${key}`, actor: 'bot', kind: 'result', ...extra })
const none = () => {}

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('ActivityToasts', () => {
  it('shows the newest three notes', () => {
    render(<ActivityToasts items={[1, 2, 3, 4].map(k => note(k))} onDismiss={none} onOpen={none} onUndo={none} />)
    expect(MAX_NOTES).toBe(3)
    expect(screen.queryByText('bot finished #1')).toBeNull()
    for (const k of [2, 3, 4]) expect(screen.getByText(`bot finished #${k}`)).toBeInTheDocument()
  })

  it('lets a note go after six seconds', () => {
    vi.useFakeTimers()
    const onDismiss = vi.fn()
    render(<ActivityToasts items={[note(7)]} onDismiss={onDismiss} onOpen={none} onUndo={none} />)
    act(() => { vi.advanceTimersByTime(ACTIVITY_MS - 1) })
    expect(onDismiss).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(1) })
    expect(onDismiss).toHaveBeenCalledWith(7)
  })

  it('opens the card from a note and undoes an archive', () => {
    const onOpen = vi.fn()
    const onUndo = vi.fn()
    const archived = note(5, { actor: 'you', kind: 'archived', undo: true })
    render(<ActivityToasts items={[archived]} onDismiss={none} onOpen={onOpen} onUndo={onUndo} />)
    fireEvent.click(screen.getByText('Archived #5'))
    expect(onOpen).toHaveBeenCalledWith(5)
    fireEvent.click(screen.getByRole('button', { name: /Undo/ }))
    expect(onUndo).toHaveBeenCalledWith(archived)
  })
})

describe('HowToCapture', () => {
  it('waits for the profile, then shows its commands', () => {
    const { rerender } = render(<HowToCapture profileId={undefined} />)
    expect(screen.getByRole('button', { name: /How to capture/ })).toBeDisabled()
    rerender(<HowToCapture profileId="my work" />)
    fireEvent.click(screen.getByRole('button', { name: /How to capture/ }))
    expect(screen.getByText(`monoagentcli --profile 'my work' task add "Call the bank"`)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/ActivityToasts.render.test.jsx`
Expected: FAIL (cannot resolve `./ActivityToasts.jsx`).

- [ ] **Step 3: Write `ActivityToasts.jsx`**

Create `wails-app/frontend/src/pages/tasks/ActivityToasts.jsx`:

```jsx
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { X, Undo2 } from 'lucide-react'
import { COLUMNS, actorColor } from '../../lib/taskModel.js'

export const ACTIVITY_MS = 6000
export const MAX_NOTES = 3
const KINDS = new Set(['created', 'claimed', 'reclaimed', 'result', 'question', 'released', 'archived'])

// ActivityToasts are the board's own notes of what happened (spec §10,
// Live): another actor moved a card ("claude-code#a3f9 finished #12"), or
// you archived one, with Undo. Neutral, the newest MAX_NOTES shown, each
// gone after ACTIVITY_MS; a click on one opens its card. The app's Toasts
// are for failures only.
export default function ActivityToasts({ items, onDismiss, onOpen, onUndo }) {
  const { t } = useTranslation()
  useEffect(() => {
    const timers = items.map(it => setTimeout(() => onDismiss(it.key), Math.max(0, ACTIVITY_MS - (Date.now() - it.at))))
    return () => timers.forEach(clearTimeout)
  }, [items, onDismiss])
  if (!items.length) return null
  const who = (a) => (['you', 'chrome', 'os'].includes(a) ? t(`tasks.actor.${a}`) : a)
  return (
    <div className="tb-activity" role="status" aria-live="polite" data-testid="tb-activity">
      {items.slice(-MAX_NOTES).map(it => (
        <div key={it.key} className="tb-activity-item" style={{ '--tb-actor': it.actor === 'you' ? 'var(--cyan)' : actorColor(it.actor) }}>
          <button type="button" className="tb-activity-main" onClick={() => onOpen(it.id)}>
            <span className="tb-avatar" aria-hidden="true">{(String(it.actor).match(/[A-Za-z0-9]/)?.[0] || '?').toUpperCase()}</span>
            <span className="tb-activity-text">
              <span>{t(`tasks.activity.${KINDS.has(it.kind) ? it.kind : 'moved'}`, {
                actor: who(it.actor), id: it.id, column: COLUMNS.includes(it.to) ? t(`tasks.columns.${it.to}`) : '',
              })}</span>
              <span className="tb-activity-title">{it.title}</span>
            </span>
          </button>
          {it.undo && (
            <button type="button" className="tb-act" onClick={() => onUndo(it)}>
              <Undo2 size={12} aria-hidden="true" /> {t('tasks.activity.undo')}
            </button>
          )}
          <button type="button" className="tb-icon-btn" aria-label={t('tasks.activity.dismiss')} onClick={() => onDismiss(it.key)}>
            <X size={12} aria-hidden="true" />
          </button>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Write `HowToCapture.jsx`**

Create `wails-app/frontend/src/pages/tasks/HowToCapture.jsx`:

```jsx
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CircleQuestionMark, Globe, AppWindow, Terminal, Bot, Copy, Check } from 'lucide-react'

// shellArg quotes a profile id for a shell when it holds anything but
// letters, digits, dot, dash or underscore.
const shellArg = (s) => (/^[A-Za-z0-9._-]+$/.test(s) ? s : `'${String(s).replaceAll("'", "'\\''")}'`)

// HowToCapture is the header's hint (spec §10): the ways a task reaches this
// board, with the commands for this profile ready to copy. It waits for the
// board to name the profile, so it never shows another profile's commands.
export default function HowToCapture({ profileId }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState('')
  const ref = useRef(null)
  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (!ref.current?.contains(e.target)) setOpen(false) }
    const onKey = (e) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])
  const p = shellArg(profileId || 'default')
  const ways = [
    { key: 'chrome', icon: Globe },
    { key: 'os', icon: AppWindow },
    { key: 'cli', icon: Terminal, cmd: `monoagentcli --profile ${p} task add "Call the bank"` },
    { key: 'agents', icon: Bot, cmd: `monoagentcli --profile ${p} task next` },
  ]
  const copy = async (key, cmd) => {
    try {
      await navigator.clipboard?.writeText(cmd)
      setCopied(key)
      setTimeout(() => setCopied(c => (c === key ? '' : c)), 1500)
    } catch { /* the clipboard is a convenience */ }
  }
  return (
    <div className="tb-howto" ref={ref}>
      <button type="button" className="btn btn-ghost btn-sm" aria-expanded={open} disabled={!profileId} onClick={() => setOpen(o => !o)}>
        <CircleQuestionMark size={13} aria-hidden="true" /> {t('tasks.howTo.button')}
      </button>
      {open && (
        <div className="tb-howto-pop" role="dialog" aria-label={t('tasks.howTo.title')}>
          <div className="tb-field-label">{t('tasks.howTo.title')}</div>
          {ways.map(({ key, icon: Icon, cmd }) => (
            <div key={key} className="tb-howto-row">
              <Icon size={14} aria-hidden="true" />
              <div>
                <div>{t(`tasks.howTo.${key}`)}</div>
                {cmd && (
                  <div className="tb-code">
                    <code>{cmd}</code>
                    <button type="button" className="tb-icon-btn" onClick={() => copy(key, cmd)}
                      aria-label={copied === key ? t('tasks.howTo.copied') : t('tasks.howTo.copy')}>
                      {copied === key ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
                    </button>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Add the activity and how-to sections to `tasks.css`**

Insert immediately above the line `/* Motion */`:

```css
/* Activity notes: bottom left of the board (the app's failure toasts are bottom right) */
.tb-activity { position: absolute; left: 16px; bottom: 14px; z-index: 30; display: flex; flex-direction: column; gap: 6px; max-width: 380px; }
.tb-activity-item {
  display: flex; align-items: center; gap: 6px; padding: 6px 8px; border-radius: var(--radius);
  background: var(--elevated); border: 1px solid var(--border-bright);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.45); animation: tbRise 200ms ease-out;
}
.tb-activity-main {
  display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; padding: 0; border: none; background-color: transparent;
  color: var(--text); font: inherit; text-align: left; cursor: pointer;
}
.tb-activity-text { display: flex; flex-direction: column; min-width: 0; font-size: 12px; }
.tb-activity-title { font-size: 11px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* How to capture */
.tb-howto { position: relative; }
.tb-howto-pop {
  position: absolute; right: 0; top: calc(100% + 6px); z-index: 40; width: 380px; padding: 12px;
  display: flex; flex-direction: column; gap: 10px; font-size: 12px; color: var(--text-secondary);
  background: var(--surface); border: 1px solid var(--border-bright); border-radius: var(--radius-lg);
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.5); animation: tbRise 160ms ease-out;
}
.tb-howto-row { display: flex; gap: 10px; align-items: flex-start; }
.tb-howto-row > svg { flex-shrink: 0; margin-top: 2px; color: var(--cyan); }
.tb-code { display: flex; align-items: center; gap: 6px; margin-top: 4px; padding: 4px 6px; border-radius: var(--radius); background: var(--void); border: 1px solid var(--border-dim); }
.tb-code code { flex: 1; min-width: 0; overflow-x: auto; white-space: nowrap; font-family: var(--font-mono); font-size: 10.5px; color: var(--text); }

```

- [ ] **Step 6: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/tasks/ActivityToasts.render.test.jsx src/locales/tasksKeys.test.js`
Expected: PASS (four tests; the keys test now scans both files).

- [ ] **Step 7: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. Render `items.map` in place of `items.slice(-MAX_NOTES).map` → "shows the newest three notes".
2. In the timers, `ACTIVITY_MS - …` becomes `2 * ACTIVITY_MS - …` → "lets a note go after six seconds".
3. Drop `disabled={!profileId}` → "waits for the profile, then shows its commands".

- [ ] **Step 8: Commit**

```
git add wails-app/frontend/src/pages/tasks/ActivityToasts.jsx wails-app/frontend/src/pages/tasks/HowToCapture.jsx wails-app/frontend/src/pages/tasks/ActivityToasts.render.test.jsx wails-app/frontend/src/pages/tasks/tasks.css
```
then
```
git commit -m "feat(tasks): the board's activity notes and the How to capture hint" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 10c: The Tasks page

**Files:**
- Create: `wails-app/frontend/src/pages/Tasks.jsx`
- Modify: `wails-app/frontend/src/pages/tasks/tasks.css` (the header's stacking), `wails-app/frontend/src/locales/tasksKeys.test.js` (one test)
- Test: `wails-app/frontend/src/pages/Tasks.render.test.jsx`

**Interfaces:**
- Consumes: Task 3's `tasksApi.agentShell()`; Task 4's `COLUMNS`, `claimState`, `filterBoard`, `findTask`, `isTypingTarget`; Task 5's `useTasksBoard`; Task 7b's `ColumnSkeleton`; Task 8's `Board` (and its `onReadFirst`); Task 9's `Drawer` (and its `hint`); Task 10a's `useFlip`, `useMarks`; Task 10b's `ActivityToasts`, `MAX_NOTES`, `HowToCapture`; `notify(op, message, code)` (`services/api.js`: a red toast "Failed: op"); `confirm(message, {title, confirmLabel, cancelLabel})` (`components/ConfirmDialog.jsx`, resolves to a boolean; its dialog is `aria-modal="true"`); `usePageVisible()`.
- Produces: `Tasks({ isActive })` (default export of `pages/Tasks.jsx`), which Task 11 registers. Test hooks: the root `data-testid="tasks-page"`, the live region `data-testid="tb-live"`.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/Tasks.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react'

let changed = null
vi.mock('../services/tasks.js', () => ({
  DONE_LIMIT: 50,
  tasksApi: {
    board: vi.fn(), show: vi.fn(), add: vi.fn(), edit: vi.fn(), move: vi.fn(), approve: vi.fn(),
    archive: vi.fn(), unarchive: vi.fn(), comment: vi.fn(), pulse: vi.fn(), agentShell: vi.fn(),
  },
  onTasksChanged: vi.fn((cb) => { changed = cb; return () => { changed = null } }),
}))
vi.mock('../services/api.js', () => ({ notify: vi.fn(), api: { openURL: vi.fn() } }))
vi.mock('../components/ConfirmDialog.jsx', () => ({ confirm: vi.fn(() => Promise.resolve(true)) }))
vi.mock('../lib/usePageVisible.js', () => ({ usePageVisible: () => true, useVisibleCatchUp: () => {} }))
import '../i18n.js'
import i18n from 'i18next'
import { tasksApi } from '../services/tasks.js'
import { notify } from '../services/api.js'
import { confirm } from '../components/ConfirmDialog.jsx'
import Tasks from './Tasks.jsx'

const card = (id, status, extra = {}) => ({
  id, title: `Task ${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T08:00:00Z' },
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z', ...extra,
})
const held = { claim: { by: 'claude-code#a3f9', until: '2999-01-01T00:00:00Z', stale: false } }
const BOARD = {
  profile: { id: 'work', name: 'Work' }, rev: 3, counts: { inbox: 1, ready: 1, in_progress: 1, review: 0, done: 0, stale: 0 },
  tasks: { inbox: [card(1, 'inbox')], ready: [card(2, 'ready')], in_progress: [card(3, 'in_progress', held)], review: [], done: [] },
}
const cardEl = (id) => document.querySelector(`[data-tb-id="${id}"]`)

beforeEach(async () => {
  vi.clearAllMocks()
  changed = null
  await i18n.changeLanguage('en')
  tasksApi.board.mockResolvedValue(BOARD)
  tasksApi.agentShell.mockResolvedValue('')
  tasksApi.show.mockResolvedValue({ events: [] })
  tasksApi.move.mockResolvedValue({ task: {} })
  tasksApi.approve.mockResolvedValue({ tasks: [] })
})
afterEach(cleanup)

describe('Tasks page', () => {
  it('shows the profile\'s board in five columns', async () => {
    render(<Tasks isActive />)
    expect(await screen.findByText('in Work')).toBeInTheDocument()
    for (const name of ['Inbox', 'Ready', 'In progress', 'Review', 'Done']) {
      expect(screen.getByRole('heading', { name })).toBeInTheDocument()
    }
    expect(cardEl(1)).toBeInTheDocument()
  })

  it('approves at once, says so, and puts the card back, shaking, with the CLI\'s reason when refused', async () => {
    tasksApi.approve.mockResolvedValue({ error: 'CLAUDECODE is set, so an agent is running this command: only the operator can approve a task', code: 'operator_only' })
    render(<Tasks isActive />)
    await screen.findByText('in Work')
    fireEvent.click(screen.getByRole('button', { name: /Approve/ }))
    expect(screen.getByTestId('tb-live')).toHaveTextContent('Approved #1: it is in Ready')
    await waitFor(() => expect(notify).toHaveBeenCalled())
    const [op, message, code] = notify.mock.calls[0]
    expect(op).toBe('approve #1')
    expect(message).toMatch(/only the operator can approve/)
    expect(message).toMatch(/open it from the Dock or Finder/)
    expect(code).toBe('operator_only')
    await waitFor(() => expect(document.querySelector('[data-tb-column="inbox"] [data-tb-id="1"]')).toHaveClass('tb-card--rejected'))
  })

  it('asks before taking a task back from an agent whose lease still runs', async () => {
    confirm.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    render(<Tasks isActive />)
    await screen.findByText('in Work')
    fireEvent.keyDown(cardEl(3), { key: 'ArrowRight', shiftKey: true })
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
    expect(confirm.mock.calls[0][0]).toMatch(/claude-code#a3f9 holds #3/)
    expect(confirm.mock.calls[0][1]).toMatchObject({ confirmLabel: 'Take it back', cancelLabel: 'Leave it' })
    expect(tasksApi.move).not.toHaveBeenCalled()
    fireEvent.keyDown(cardEl(3), { key: 'ArrowRight', shiftKey: true })
    await waitFor(() => expect(tasksApi.move).toHaveBeenCalledWith(3, 'review', { where: '' }))
  })

  it('asks before archiving a task an agent still holds', async () => {
    confirm.mockResolvedValueOnce(false)
    render(<Tasks isActive />)
    await screen.findByText('in Work')
    fireEvent.click(cardEl(3))
    fireEvent.click(await screen.findByRole('button', { name: /Archive/ }))
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
    expect(tasksApi.archive).not.toHaveBeenCalled()
    expect(screen.getByTestId('tb-drawer')).toBeInTheDocument()
  })

  it('opens a card with notes instead of moving it to Ready, and says why', async () => {
    tasksApi.board.mockResolvedValue({ ...BOARD, tasks: { ...BOARD.tasks, inbox: [card(1, 'inbox', { notes: 'Delete the old backups' })] } })
    render(<Tasks isActive />)
    await screen.findByText('in Work')
    fireEvent.keyDown(cardEl(1), { key: 'ArrowRight', shiftKey: true })
    expect(await screen.findByRole('note')).toHaveTextContent('Not moved: read it first, then approve it here.')
    expect(screen.getByTestId('tb-live')).toHaveTextContent('Opened #1: read it, then approve it in the drawer')
    expect(tasksApi.move).not.toHaveBeenCalled()
    expect(tasksApi.approve).not.toHaveBeenCalled()
  })

  it('is read-only with a banner when started from an agent shell', async () => {
    tasksApi.agentShell.mockResolvedValue('CLAUDECODE')
    render(<Tasks isActive />)
    expect(await screen.findByText(/started from an AI agent's shell \(CLAUDECODE is set\)/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Approve/ })).toBeNull()
    expect(screen.getByRole('button', { name: /New task/ })).toBeDisabled()
    fireEvent.keyDown(document.body, { key: 'n' })
    expect(screen.getByTestId('tb-live')).toHaveTextContent('The board is read-only here')
  })

  it('ignores N and / while inactive, while typing, or under a modal dialog', async () => {
    const { rerender } = render(<Tasks isActive={false} />)
    await screen.findByText('in Work')
    fireEvent.keyDown(document.body, { key: 'n' })
    expect(screen.queryByRole('textbox', { name: 'New task in Inbox' })).toBeNull()
    rerender(<Tasks isActive />)
    const search = screen.getByRole('searchbox', { name: 'Search tasks' })
    search.focus()
    fireEvent.keyDown(search, { key: 'n' })
    expect(screen.queryByRole('textbox', { name: 'New task in Inbox' })).toBeNull()
    search.blur()
    fireEvent.keyDown(document.body, { key: '/' })
    expect(search).toHaveFocus()
    search.blur()
    fireEvent.keyDown(document.body, { key: 'n' })
    expect(await screen.findByRole('textbox', { name: 'New task in Inbox' })).toHaveFocus()
    const modal = document.createElement('div')
    modal.setAttribute('aria-modal', 'true')
    document.body.appendChild(modal)
    fireEvent.keyDown(document.body, { key: '/' })
    expect(search).not.toHaveFocus()
    modal.remove()
  })

  it('notes what an agent did, and opens the card from the note', async () => {
    const finished = card(3, 'review', { last_event: { actor: 'claude-code#a3f9', kind: 'result', at: '2026-10-06T09:05:00Z' } })
    tasksApi.board.mockResolvedValueOnce(BOARD)
      .mockResolvedValue({ ...BOARD, rev: 4, tasks: { ...BOARD.tasks, in_progress: [], review: [finished] } })
    render(<Tasks isActive />)
    await screen.findByText('in Work')
    await act(async () => { changed({ profile_id: 'work', rev: 4, inbox: 1, review: 1 }) })
    fireEvent.click(await screen.findByText('claude-code#a3f9 finished #3'))
    expect(await screen.findByTestId('tb-drawer')).toBeInTheDocument()
  })

  it('archives from the drawer and offers Undo', async () => {
    tasksApi.archive.mockResolvedValue({ tasks: [] })
    tasksApi.unarchive.mockResolvedValue({ tasks: [] })
    render(<Tasks isActive />)
    await screen.findByText('in Work')
    fireEvent.click(cardEl(2))
    fireEvent.click(await screen.findByRole('button', { name: /Archive/ }))
    expect(await screen.findByText('Archived #2')).toBeInTheDocument()
    expect(confirm).not.toHaveBeenCalled() // no agent holds #2
    fireEvent.click(screen.getByRole('button', { name: /Undo/ }))
    await waitFor(() => expect(tasksApi.unarchive).toHaveBeenCalledWith([2]))
  })

  it('says when the board cannot be read, and tries again', async () => {
    tasksApi.board.mockResolvedValueOnce({ error: 'monoagentcli printed text instead of JSON' })
    render(<Tasks isActive />)
    expect(await screen.findByText('Could not load the task board')).toBeInTheDocument()
    expect(screen.getByText(/instead of JSON/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('in Work')).toBeInTheDocument()
  })

  // A lease ends with no write, so no event and no read follow: the page's
  // own clock turns the claim amber.
  it('turns a claim amber when its lease runs out, with no new read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const soon = new Date(Date.now() + 30 * 1000).toISOString()
      const ending = card(3, 'in_progress', { claim: { by: 'bot', until: soon, stale: false } })
      tasksApi.board.mockResolvedValue({ ...BOARD, tasks: { ...BOARD.tasks, in_progress: [ending] } })
      render(<Tasks isActive />)
      await screen.findByText('in Work')
      expect(cardEl(3)).not.toHaveAttribute('data-stale')
      await act(async () => { vi.advanceTimersByTime(90 * 1000) })
      expect(cardEl(3)).toHaveAttribute('data-stale', 'true')
      expect(tasksApi.board).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/pages/Tasks.render.test.jsx`
Expected: FAIL (cannot resolve `./Tasks.jsx`).

- [ ] **Step 3: Write `Tasks.jsx`**

Create `wails-app/frontend/src/pages/Tasks.jsx`:

```jsx
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { SquareKanban, Search, Plus, TriangleAlert } from 'lucide-react'
import Board from './tasks/Board.jsx'
import { ColumnSkeleton } from './tasks/Column.jsx'
import Drawer from './tasks/Drawer.jsx'
import ActivityToasts, { MAX_NOTES } from './tasks/ActivityToasts.jsx'
import HowToCapture from './tasks/HowToCapture.jsx'
import { useTasksBoard } from './tasks/useTasksBoard.js'
import { useFlip } from './tasks/useFlip.js'
import { useMarks } from './tasks/useMarks.js'
import { tasksApi } from '../services/tasks.js'
import { notify } from '../services/api.js'
import { confirm } from '../components/ConfirmDialog.jsx'
import { usePageVisible } from '../lib/usePageVisible.js'
import { COLUMNS, claimState, filterBoard, findTask, isTypingTarget } from '../lib/taskModel.js'
import './tasks/tasks.css'

const reducedMotion = () => typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
const minute = () => Math.floor(Date.now() / 60000) * 60000

// useNow is the time the cards draw their ages and leases at, to the
// minute. It ticks every 15 s while the page shows, so a lease turns amber
// within a minute of ending although no write (so no read) marks it.
function useNow(running) {
  const [now, setNow] = useState(minute)
  useEffect(() => {
    if (!running) return undefined
    setNow(minute())
    const id = setInterval(() => setNow(minute()), 15000)
    return () => clearInterval(id)
  }, [running])
  return now
}

// Tasks is the Tasks tab (spec §10): the active profile's board as five
// live columns, with search, quick add, drag and keyboard moves, a drawer
// per card, notes of what agents did, and an aria-live line that says each
// move. Opened from an AI agent's shell it is read-only (the CLI would
// refuse every operator action there) and says how to open the app instead.
export default function Tasks({ isActive = true }) {
  const { t } = useTranslation()
  const visible = usePageVisible()
  const reduced = useMemo(reducedMotion, []) // read once: FLIP keeps the setting the page opened with
  const rootRef = useRef(null)
  const searchRef = useRef(null)
  const capture = useFlip(rootRef, reduced)
  const [query, setQuery] = useState('')
  const [openId, setOpenId] = useState(null)
  const [quickAdd, setQuickAdd] = useState(null)
  const [announcement, setAnnouncement] = useState('')
  const [activity, setActivity] = useState([])
  const [rejectedId, setRejectedId] = useState(null)
  const [readFirstId, setReadFirstId] = useState(null)
  const [agentShell, setAgentShell] = useState('')
  const seq = useRef(0)

  // announce puts text in the live region; the same words twice in a row
  // get a trailing space, or a screen reader would not say them again.
  const announce = useCallback((text) => setAnnouncement(cur => (cur === text ? `${text} ` : text)), [])
  const pushActivity = useCallback((items) => {
    setActivity(cur => [...cur, ...items.map(it => ({ ...it, key: ++seq.current, at: Date.now() }))].slice(-MAX_NOTES))
  }, [])
  const dismiss = useCallback((key) => setActivity(cur => cur.filter(x => x.key !== key)), [])
  const { board, status, error, pendingIds, load, retry, move, approve, archive, unarchive, add } =
    useTasksBoard({ isActive, beforeChange: capture, onRemote: pushActivity })
  const boardRef = useRef(board)
  boardRef.current = board
  const now = useNow(isActive && visible)
  const readOnly = agentShell !== ''
  const marks = useMarks(board) // new cards and cards just done, each for its own time

  useEffect(() => { tasksApi.agentShell().then(setAgentShell) }, [])

  const shown = useMemo(() => filterBoard(board, query), [board, query])
  const totals = useMemo(() => (board
    ? Object.fromEntries(COLUMNS.map(s => [s, s === 'done' ? Math.max(board.counts.done ?? 0, board.columns.done.length) : board.columns[s].length]))
    : null), [board])

  // failed reports a refusal: the card shakes where it went back to, a red
  // toast quotes the CLI (and, for operator_only, how to open the app), and
  // the live region says it.
  const failed = useCallback((id, verb, res) => {
    setRejectedId(id)
    setTimeout(() => setRejectedId(cur => (cur === id ? null : cur)), 700)
    const hint = res.code === 'operator_only' ? `\n${t('tasks.agentShell.hint')}` : ''
    notify(t(`tasks.failed.${verb}`, { id }), `${res.error}${hint}`, res.code || '')
    announce(t('tasks.live.failed', { id, error: res.error }))
  }, [announce, t])

  // takeBack asks before a card leaves In progress while an agent's lease
  // on it still runs (its next call would fail mid-work); true goes on.
  const takeBack = useCallback(async (id) => {
    const found = findTask(boardRef.current, id)
    const claim = found && claimState(found.task.claim, Date.now())
    if (found?.status !== 'in_progress' || !claim || claim.stale) return true
    return confirm(t('tasks.confirmTakeBack.body', { name: claim.by, id }), {
      title: t('tasks.confirmTakeBack.title'),
      confirmLabel: t('tasks.confirmTakeBack.confirm'),
      cancelLabel: t('tasks.confirmTakeBack.cancel'),
    })
  }, [t])

  const doMove = useCallback(async (id, to, place, overrides) => {
    if (!findTask(boardRef.current, id)) return
    if (to !== 'in_progress' && !(await takeBack(id))) return
    announce(t('tasks.live.moved', { id, column: t(`tasks.columns.${to}`) }))
    const res = await move(id, to, place, overrides)
    if (res?.error) failed(id, 'move', res)
  }, [move, failed, takeBack, announce, t])

  const doApprove = useCallback(async (id) => {
    announce(t('tasks.live.approved', { id }))
    const res = await approve(id)
    if (res?.error) failed(id, 'approve', res)
  }, [approve, failed, announce, t])

  const doArchive = useCallback(async (id) => {
    const title = findTask(boardRef.current, id)?.task.title || ''
    if (!(await takeBack(id))) return
    setOpenId(null)
    const res = await archive(id)
    if (res?.error) failed(id, 'archive', res)
    else pushActivity([{ id, title, actor: 'you', kind: 'archived', undo: true }])
  }, [archive, failed, pushActivity, takeBack])

  const doUndo = useCallback(async (item) => {
    dismiss(item.key)
    const res = await unarchive(item.id)
    if (res?.error) failed(item.id, 'unarchive', res)
  }, [dismiss, unarchive, failed])

  const doAdd = useCallback(async (spec) => {
    const res = await add(spec)
    if (!res?.error && res?.task) announce(t('tasks.live.added', { id: res.task.id, column: t(`tasks.columns.${res.task.status}`) }))
    return res
  }, [add, announce, t])

  const openIdRef = useRef(null)
  openIdRef.current = openId
  const openCard = useCallback((id) => {
    setReadFirstId(null)
    setOpenId(id)
  }, [])
  // readFirst opens a card that holds more than it shows in place of a move
  // from Inbox to Ready (spec D6; the lead's ruling on review F6): a dropped
  // card slides back from where it was let go, and the drawer says why.
  const readFirst = useCallback((id, overrides) => {
    if (overrides) capture(overrides)
    setReadFirstId(id)
    setOpenId(id)
    announce(t('tasks.live.readFirst', { id }))
  }, [capture, announce, t])
  const closeDrawer = useCallback(() => {
    const id = openIdRef.current
    setOpenId(null)
    setReadFirstId(null)
    setTimeout(() => rootRef.current?.querySelector(`[data-tb-id="${id}"]`)?.focus(), 0)
  }, [])
  const closeQuickAdd = useCallback(() => setQuickAdd(null), [])
  const openTask = openId != null && board ? findTask(board, openId)?.task : null
  useEffect(() => { if (openId != null && board && !openTask) setOpenId(null) }, [openId, board, openTask])

  // The page's keys, only while it is the active tab, no field has the
  // focus and no modal dialog (a confirmation, Settings) sits above it.
  useEffect(() => {
    if (!isActive) return undefined
    const onKey = (e) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || isTypingTarget(e.target)) return
      if (document.querySelector('[aria-modal="true"]')) return
      if (e.key === '/') {
        e.preventDefault()
        searchRef.current?.focus()
      } else if (e.key === 'n' || e.key === 'N') {
        e.preventDefault()
        if (readOnly) announce(t('tasks.live.readOnly'))
        else setQuickAdd('inbox')
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [isActive, readOnly, announce, t])

  const profile = board?.profile
  return (
    <div className={`tb-page${visible ? '' : ' paused'}`} data-testid="tasks-page">
      <div className="page-header">
        <div className="page-header-left">
          <SquareKanban size={16} className="tb-title-icon" aria-hidden="true" />
          <h2 className="page-title">{t('tasks.title')}</h2>
          {profile?.name && <span className="tb-profile" title={profile.id}>{t('tasks.inProfile', { name: profile.name })}</span>}
        </div>
        <div className="page-header-right">
          <label className="tb-search">
            <Search size={13} aria-hidden="true" />
            <input ref={searchRef} type="search" className="search-input" value={query}
              placeholder={t('tasks.searchPlaceholder')} aria-label={t('tasks.search')}
              onChange={e => setQuery(e.target.value)}
              onKeyDown={e => {
                if (e.key !== 'Escape') return
                e.preventDefault()
                setQuery('')
                e.currentTarget.blur()
              }} />
            <kbd>/</kbd>
          </label>
          <HowToCapture profileId={profile?.id} />
          <button type="button" className="btn btn-primary btn-sm" disabled={readOnly} onClick={() => setQuickAdd('inbox')}>
            <Plus size={13} aria-hidden="true" /> {t('tasks.newTask')} <kbd>N</kbd>
          </button>
        </div>
      </div>
      {readOnly && (
        <div className="tb-banner tb-banner--warn" role="note">
          <TriangleAlert size={14} aria-hidden="true" /> {t('tasks.agentShell.banner', { marker: agentShell })}
        </div>
      )}
      {status === 'ready' && error && <div className="tb-banner" role="status">{t('tasks.staleBoard', { error })}</div>}
      <div className="tb-body">
        {!board && status === 'loading' && (
          <div className="tb-board-scroll"><div className="tb-board">{COLUMNS.map(s => <ColumnSkeleton key={s} status={s} />)}</div></div>
        )}
        {!board && status === 'error' && (
          <div className="tb-error" role="alert">
            <TriangleAlert size={22} aria-hidden="true" />
            <h3>{t('tasks.loadFailed')}</h3>
            <pre>{error}</pre>
            <button type="button" className="btn btn-secondary btn-sm" onClick={retry}>{t('tasks.retry')}</button>
          </div>
        )}
        {board && (
          <Board board={board} shown={shown} totals={totals} searching={!!query.trim()} now={now} readOnly={readOnly}
            pendingIds={pendingIds} rejectedId={rejectedId} transitions={marks} quickAdd={quickAdd} rootRef={rootRef}
            onOpen={openCard} onMove={doMove} onApprove={doApprove} onReadFirst={readFirst} onAnnounce={announce}
            onOpenQuickAdd={setQuickAdd} onCloseQuickAdd={closeQuickAdd} onAdd={doAdd} />
        )}
        {openTask && (
          <Drawer key={openTask.id} task={openTask} now={now} readOnly={readOnly} hint={readFirstId === openTask.id}
            onClose={closeDrawer} onMove={doMove} onApprove={doApprove} onArchive={doArchive} onChanged={load} />
        )}
        <ActivityToasts items={isActive ? activity : []} onDismiss={dismiss} onOpen={openCard} onUndo={doUndo} />
      </div>
      <div className="tb-sr-only" role="status" aria-live="polite" data-testid="tb-live">{announcement}</div>
    </div>
  )
}
```

- [ ] **Step 4: Add the header's stacking to `tasks.css`**

Insert immediately above the line `/* Motion */`:

```css
/* The header stacks above the board, so the how-to popover is not painted under it */
.tb-page > .page-header { position: relative; z-index: 5; }

```

- [ ] **Step 5: Make the keys test check that it scans the whole board**

In `wails-app/frontend/src/locales/tasksKeys.test.js`, add inside `describe('tasks i18n', …)`, before the `it('has every used key …')` test:

```js
  it('finds the keys the board uses', () => {
    expect(literal.filter(k => k.startsWith('tasks.')).length).toBeGreaterThan(50)
  })
```

- [ ] **Step 6: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/pages/Tasks.render.test.jsx src/pages/tasks/ src/locales/tasksKeys.test.js`
Expected: PASS (eleven page tests and every earlier board test; the keys test finds more than 50 literal keys).

- [ ] **Step 7: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. In the page's key handler, drop `|| isTypingTarget(e.target)` → "ignores N and / while inactive, while typing, or under a modal dialog".
2. Replace `if (!isActive) return undefined` with nothing (the handler is always attached) → the same test.
3. Drop the line `if (document.querySelector('[aria-modal="true"]')) return` → the same test.
4. In `takeBack`, `return true` as its first line → "asks before taking a task back …" and "asks before archiving a task an agent still holds".
5. In `failed`, drop `${hint}` → "approves at once, says so, and puts the card back …"; then drop `setRejectedId(id)` → the same test (no shake).
6. In the `N` branch, call `setQuickAdd('inbox')` whatever `readOnly` is → "is read-only with a banner when started from an agent shell".
7. In `readFirst`, drop `setReadFirstId(id)` → "opens a card with notes instead of moving it to Ready, and says why" (no hint).
8. In `useNow`, replace `setInterval(() => setNow(minute()), 15000)` with `0` (no tick) → "turns a claim amber when its lease runs out, with no new read".

- [ ] **Step 8: Commit**

```
git add wails-app/frontend/src/pages/Tasks.jsx wails-app/frontend/src/pages/Tasks.render.test.jsx wails-app/frontend/src/pages/tasks/tasks.css wails-app/frontend/src/locales/tasksKeys.test.js
```
then
```
git commit -m "feat(tasks): the Tasks page: header, search, banners, confirmations, live region" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 11: The sidebar entry, its badge, and the page's registration

**Files:**
- Modify: `wails-app/frontend/src/components/Sidebar.jsx`, `wails-app/frontend/src/components/Sidebar.render.test.jsx`, `wails-app/frontend/src/App.jsx`
- Test: `wails-app/frontend/src/navPages.test.js`

**Interfaces:**
- Consumes: Task 3's `tasksApi.pulse()` and `onTasksChanged`; `api.isReady()` (`services/api.js`, `App.IsReady`: true once `startup` has chosen the active profile); Task 10's `Tasks`; Task 6's `sidebar.nav.tasks`, `sidebar.taskBadge`.
- Produces: the Tasks entry right after Documents with the Inbox plus Review badge, and `persistentPages.tasks`. The badge asks `pulse()` only once `isReady()` is true (until then `getActiveProfileID()` answers `default`, review F7); revisions count per profile, so an older revision of the same profile never overwrites a newer one and another profile's value always replaces it.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/navPages.test.js`:

```js
// Every sidebar entry has a page: an id missing from App.jsx's
// persistentPages silently shows the dashboard (spec §10).
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const sidebar = readFileSync(join(__dirname, 'components', 'Sidebar.jsx'), 'utf8')
const app = readFileSync(join(__dirname, 'App.jsx'), 'utf8')
const navIds = [...sidebar.matchAll(/\{\s*id:\s*'([A-Za-z]+)',\s*labelKey:/g)].map(m => m[1])
const pages = app.slice(app.indexOf('const persistentPages = {'), app.indexOf('const detailPages'))

describe('sidebar pages', () => {
  it('lists Tasks right after Documents', () => {
    expect(navIds.indexOf('documents')).toBeGreaterThan(-1)
    expect(navIds.indexOf('tasks')).toBe(navIds.indexOf('documents') + 1)
  })
  it('gives every entry a page', () => {
    expect(navIds.filter(id => !new RegExp(`\\n\\s+${id}:\\s*<`).test(pages))).toEqual([])
  })
})
```

In `wails-app/frontend/src/components/Sidebar.render.test.jsx`:
1. Change the import line `import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'` to `import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'`.
2. In the `vi.mock('../wailsjs/go/main/App', …)` block, change the line `  RevealProfileFolder: () => Promise.resolve(),` to
```jsx
  RevealProfileFolder: () => Promise.resolve(),
  IsReady: vi.fn(() => Promise.resolve(true)),
```
3. Below the existing `vi.mock('./NewProfileModal.jsx', …)` block, add:

```jsx
// The Tasks badge: the first value from TaskPulse, then tasks:changed.
let tasksChanged = null
vi.mock('../services/tasks.js', () => ({
  tasksApi: { pulse: vi.fn(() => Promise.resolve({ profile_id: 'default', rev: 3, inbox: 2, review: 1 })) },
  onTasksChanged: vi.fn((cb) => { tasksChanged = cb; return () => { tasksChanged = null } }),
}))
import { IsReady } from '../wailsjs/go/main/App'
import { tasksApi } from '../services/tasks.js'
```
4. At the end of the file, add:

```jsx
it('lists Tasks after Documents with the Inbox plus Review badge, live', async () => {
  render(<Sidebar activePage="dashboard" onNavigate={() => {}} stats={{}} dbConnected={true} />)
  const names = screen.getAllByRole('button').map(el => el.getAttribute('aria-label'))
  expect(names.indexOf('sidebar.nav.tasks')).toBe(names.indexOf('sidebar.nav.documents') + 1)
  const entry = screen.getByRole('button', { name: 'sidebar.nav.tasks' })
  await waitFor(() => expect(entry).toHaveTextContent('3'))
  expect(entry.querySelector('.nav-badge')).toHaveAttribute('title', 'sidebar.taskBadge')
  act(() => { tasksChanged({ profile_id: 'default', rev: 4, inbox: 5, review: 0 }) })
  expect(entry.querySelector('.nav-badge')).toHaveTextContent('5')
  act(() => { tasksChanged({ profile_id: 'default', rev: 2, inbox: 9, review: 9 }) }) // older than what is shown
  expect(entry.querySelector('.nav-badge')).toHaveTextContent('5')
  act(() => { tasksChanged({ profile_id: 'default', rev: 5, inbox: 0, review: 0 }) })
  expect(entry.querySelector('.nav-badge')).toBeNull()
})

// Until startup has chosen the profile the app answers for "default"
// (review F7): the badge asks once IsReady says so, and a revision of
// another profile, however low, replaces what it shows.
it('asks for the Tasks badge once startup has chosen the profile', async () => {
  IsReady.mockResolvedValueOnce(false)
  tasksApi.pulse.mockResolvedValueOnce({ profile_id: 'default', rev: 10, inbox: 4, review: 0 })
  render(<Sidebar activePage="dashboard" onNavigate={() => {}} stats={{}} dbConnected={true} />)
  const entry = screen.getByRole('button', { name: 'sidebar.nav.tasks' })
  await waitFor(() => expect(entry).toHaveTextContent('4'))
  expect(IsReady).toHaveBeenCalledTimes(2)
  expect(tasksApi.pulse.mock.invocationCallOrder[0]).toBeGreaterThan(IsReady.mock.invocationCallOrder[1])
  act(() => { tasksChanged({ profile_id: 'work', rev: 2, inbox: 1, review: 0 }) })
  expect(entry.querySelector('.nav-badge')).toHaveTextContent('1')
})
```

(The file's `react-i18next` mock returns each key as its text, so the entry's name is `sidebar.nav.tasks` and the badge's title is the key. The test waits one real 200 ms poll.)

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix wails-app/frontend test -- src/navPages.test.js src/components/Sidebar.render.test.jsx`
Expected: FAIL: "lists Tasks right after Documents" (no `tasks` entry), and the new Sidebar test cannot find `sidebar.nav.tasks`.

- [ ] **Step 3: Add the entry and the badge to `Sidebar.jsx`**

In `wails-app/frontend/src/components/Sidebar.jsx`:

1. Change
```jsx
  ChevronDown, Plus, Check, Building2, FolderOpen, FolderCog, Loader2, Briefcase, FileText
} from 'lucide-react'
```
to
```jsx
  ChevronDown, Plus, Check, Building2, FolderOpen, FolderCog, Loader2, Briefcase, FileText, SquareKanban
} from 'lucide-react'
```
2. Change
```jsx
import { notify } from '../services/api.js'
```
to
```jsx
import { api, notify } from '../services/api.js'
import { tasksApi, onTasksChanged } from '../services/tasks.js'
```
3. Change
```jsx
  { id: 'documents',   labelKey: 'documents',   icon: FileText,        section: 'DATA' },
```
to
```jsx
  { id: 'documents',   labelKey: 'documents',   icon: FileText,        section: 'DATA' },
  { id: 'tasks',       labelKey: 'tasks',       icon: SquareKanban,    section: 'DATA' },
```
4. Change
```jsx
  useEffect(() => { requestNotifyPermission() }, [])
```
to
```jsx
  useEffect(() => { requestNotifyPermission() }, [])

  // The Tasks badge: Inbox plus Review of the active profile's board, from
  // the watcher's tasks:changed (wails-app/app_tasks_watch.go), and asked
  // once startup has chosen the profile (IsReady, as OrgsPanel waits),
  // because the watcher's first event fires before anything listens.
  // Revisions count per profile: an older one of the same profile never
  // overwrites a newer one; another profile's replaces it.
  const [taskPulse, setTaskPulse] = useState(null)
  useEffect(() => {
    let alive = true
    const keep = (p) => {
      if (!alive || !p || typeof p.rev !== 'number') return
      setTaskPulse(cur => (cur && cur.profile_id === p.profile_id && cur.rev > p.rev ? cur : p))
    }
    const off = onTasksChanged(keep)
    ;(async () => {
      for (let i = 0; i < 30 && alive; i++) { // about 6 s, as OrgsPanel
        if (await api.isReady()) break
        await new Promise(r => setTimeout(r, 200))
      }
      if (alive) keep(await tasksApi.pulse())
    })()
    return () => {
      alive = false
      off()
    }
  }, [])
```
5. Change
```jsx
  const getBadge = (id) => {
    if (!stats) return null
```
to
```jsx
  const getBadge = (id) => {
    if (id === 'tasks') {
      const n = (taskPulse?.inbox || 0) + (taskPulse?.review || 0)
      return n > 0 ? n : null
    }
    if (!stats) return null
```
and, right after the closing `}` of `getBadge`, add:
```jsx

  // The Tasks badge's tooltip gives the split.
  const badgeTitle = (id) => (id === 'tasks' && taskPulse
    ? t('sidebar.taskBadge', { inbox: taskPulse.inbox || 0, review: taskPulse.review || 0 })
    : undefined)
```
6. Change
```jsx
                    <span className="nav-badge" aria-label={`${badge} items`}>{badge > 999 ? '999+' : badge}</span>
```
to
```jsx
                    <span className="nav-badge" title={badgeTitle(item.id)} aria-label={badgeTitle(item.id) || `${badge} items`}>{badge > 999 ? '999+' : badge}</span>
```

- [ ] **Step 4: Register the page in `App.jsx`**

In `wails-app/frontend/src/App.jsx`, change
```jsx
import Documents from './pages/Documents.jsx'
```
to
```jsx
import Documents from './pages/Documents.jsx'
import Tasks from './pages/Tasks.jsx'
```
and change
```jsx
    documents: <Documents isActive={activePage === 'documents'} />,
```
to
```jsx
    documents: <Documents isActive={activePage === 'documents'} />,
    tasks: <Tasks isActive={activePage === 'tasks'} />,
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `npm --prefix wails-app/frontend test -- src/navPages.test.js src/components/Sidebar.render.test.jsx src/locales/tasksKeys.test.js`
Expected: PASS (two navPages tests, four Sidebar tests, and the keys tests).

- [ ] **Step 6: Check that the rules are pinned**

Each change must make the named test FAIL, then undo it:
1. In `keep`, set `setTaskPulse(p)` unconditionally → "lists Tasks after Documents with the Inbox plus Review badge, live" (the older revision wins).
2. In `keep`, drop `cur.profile_id === p.profile_id &&` → "asks for the Tasks badge once startup has chosen the profile" (the other profile's event is dropped).
3. Drop the `for` loop (ask at once) → the same test (the pulse comes before the second `IsReady`).
4. Remove the `tasks:` line from `persistentPages` → "gives every entry a page".

- [ ] **Step 7: Commit**

```
git add wails-app/frontend/src/components/Sidebar.jsx wails-app/frontend/src/components/Sidebar.render.test.jsx wails-app/frontend/src/App.jsx wails-app/frontend/src/navPages.test.js
```
then
```
git commit -m "feat(tasks): the Tasks tab after Documents, with an Inbox plus Review badge" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 12: Documents, and the whole phase verified

**Files:**
- Modify: `AGENTS.md` (a bullet and one table row), `CHANGELOG.md`, `docs/mastermind/specs/2026-10-05-task-board-design.md` (§10 and §17)

**Interfaces:**
- Consumes: everything above. Produces: the documents of spec §15.2 that belong to P3, and the evidence the PR description quotes.

- [ ] **Step 1: `AGENTS.md`, what the desktop app calls**

Find the anchor with `grep -n "for exit 2, \`invalid_input: …\` for exit 3)." AGENTS.md` (the end of the "OpenAI-compatible API" bullet, just before `## monoes.me library`). Immediately after that bullet's line, add this bullet:

```markdown
- **Task board:** `task show <id>`, `task add --source app [--ready] -- <title>` (or `--stdin` for text of several lines, or one line over 120 characters, so the CLI keeps all of it), `task edit <id> --title=… --notes=…`, `task move <id> <status> [--before <id>|--after <id>|--top|--bottom]`, `task approve <id>…`, `task archive <id>…`, `task unarchive <id>…` and `task comment <id> -- <text>`, for the Tasks tab (`wails-app/app_tasks.go`). User text always follows `--` or sits in `--flag=value`, so text that starts with a dash stays text. The board itself is read in process (`internal/tasks` `Store.Board`, the document `task board --json` prints, 50 Done cards), as are its revision every two seconds (`wails-app/app_tasks_watch.go`) and the sidebar badge (Inbox plus Review): `task board` refuses an agent-driven caller. An app started from an AI agent's shell inherits its markers, and the CLI refuses every operator action from it: the tab then shows the board read-only and says to open MonoAgent from the Dock or Finder.
```

- [ ] **Step 2: `AGENTS.md`, the surfaces table**

The Task board section P1 wrote ends with a table headed `| Surface | Reaches the board through |` (`grep -n '^| Surface |' AGENTS.md`); its rows start `| CLI |` and `| Session-start hook |`, and P2 may have added one. Add exactly this one row right after the table's last row, and no paragraph (P4 and P5 add theirs the same way):

```markdown
| Desktop app | The Tasks tab after Documents: `monoagentcli --json task ...` for each action; the board, its revision (live updates) and the Inbox plus Review badge read in process; read-only when the app was started from an agent's shell |
```

- [ ] **Step 3: `CHANGELOG.md`**

Read the lines after `## [Unreleased]` (`grep -n "Unreleased" CHANGELOG.md`). Add this bullet as the first item of its `### Added` list:

```markdown
- **Task board in the desktop app.** A Tasks tab after Documents shows the active profile's board as five live columns: drag cards or move them with the keyboard, approve Inbox tasks in one click (a task with notes, or a title too long for its card, is approved in its drawer, once read), close or send back Review tasks, search, add tasks (text of several lines, or a long pasted line, becomes a title and notes), and open a task's drawer to edit it, read its history (an agent's questions stand out) and comment. Cards show where a task came from, its age, and the agent holding it with the time left on its lease, amber once it ran out. The sidebar badge counts Inbox plus Review. The board follows changes made anywhere (an in-process watch of its revision) and notes what agents did. Started from an AI agent's shell, the tab is read-only and says to open MonoAgent from the Dock or Finder. English and Spanish.
```

- [ ] **Step 4: The spec, where the build differs**

In `docs/mastermind/specs/2026-10-05-task-board-design.md` (the lead accepted the read-only tab, ruled on approval, review F6, and amended D21; all three change the spec's words):
1. In §10's Card bullet, replace `Inbox cards have a one-click "Approve", Review cards "Done" and "Back to Ready".` with:
```markdown
Inbox cards have a one-click "Approve" when the card shows all of their text. One with notes, or a title cut to two lines, shows a notes marker and "Read and approve", and no gesture takes it from Inbox to Ready: that button, `A`, Shift+Right and a drop on Ready all open the drawer instead (a dropped card slides back, and the drawer says why), where it is approved with the text in view, since Ready is where agents act on it (D6). Review cards have "Done" and "Back to Ready".
```
2. Replace §10's bullet that starts `- Errors: an operator action refused` with:
```markdown
- Errors: when the app was started from an agent's shell (it inherits the markers), the tab is read-only: the board still shows (it is read in process, D21), a banner names the variable and says to open the app from the Dock or Finder, and no action is offered. A refusal that still happens (an older CLI found first) reverts the move and shows the CLI's message in a toast with the same hint.
```
3. After §10's bullet that starts `- Tests: vitest for the model` (followed by a blank line and `## 11. Chrome (P4)`), add:
```markdown
- As built (P3): the actions shell out through `taskCLI` (on `cliResultJSON`, which keeps the CLI's `{"error","code"}`), not `cliJSON`/`runMonoCLI`, and the board is read in process (D21); the app adds with `--source app` (P1 accepts `app` from the operator and names it in the flag's help, where §7 names only `cli|os`); quick add sends one line of up to 120 characters as the title, and anything longer or of several lines as `--stdin` text; the app refuses notes over 30,000 characters (they travel on the command line); the `wailsjs` entries are placed by hand as the generator writes them, and every release build regenerates them; the badge's first value comes from `TaskPulse`, an in-process read like the watcher's, asked once `IsReady` is true; the plain arrows move the focus between cards; moving or archiving a card out of In progress while its lease runs asks first; archiving offers Undo; a drop names a shown neighbour (`--before`/`--after`), and a card new to Done dropped below the cut column's last card goes to Done's top, so neither a search nor the cut buries it; the enter and Done marks outlast the read that confirms a move; the parts also include `services/tasks.js`, `pages/tasks/useFlip.js`, `useMarks.js`, `ActivityToasts.jsx` and `HowToCapture.jsx`.
```
4. In §17, replace item 3 (it starts `3. An operator action in the app fails`) with:
```markdown
3. When the app was started from an agent's shell, the Tasks tab is read-only and its banner says to reopen the app from the Dock or Finder (§10 Errors); the alternative (the app clearing the markers for its own children) was not chosen.
```
5. In §2, replace the row that starts `| D21 | App data path:` with (the lead's decision, after P1 made `task board` operator-only):
```markdown
| D21 | App data path: reads in process, writes through the CLI. Every action shells out to `monoagentcli task ... --json` (the app's doctrine), so the CLI's operator guard refuses it in an app started from an agent's shell. The board is read in process with `Store.Board` (one snapshot, the document `task board --json` prints), and so are the revision and the counts, because `task board` refuses an agent-driven caller (the whole board would hand an agent every unreviewed Inbox card, §4.1) while the tab must still show the board read-only there (§17.3). An in-process watcher on the board revision emits `tasks:changed`, as the document watcher does; the board refetches on that event. Amended in P3. | lead |
```

- [ ] **Step 5: Verify the app module and the frontend, whole**

Run, as separate calls:
1. `gofmt -l wails-app/*.go` — expected: nothing.
2. `npm --prefix wails-app/frontend run build` — expected: success (the Go embed needs `dist`).
3. `go -C wails-app vet ./...` — expected: clean.
4. `go -C wails-app build ./...` — expected: success.
5. `GOOS=windows go -C wails-app vet ./...` — expected: clean. If it fails only in files this phase did not touch, report the output as a failure that predates this phase; do not fix it here.
6. `go -C wails-app test -race -run 'TestTask|TestRestartAndStopTaskWatcher|TestDocumentWatcher' -count=1 .` — expected: PASS with no race report.
7. `go -C wails-app test -count=1 -v ./... > SCRATCH/p3-wails-test.log 2>&1` (`SCRATCH`: your scratchpad directory, written out), then `grep -E '^(--- FAIL|FAIL|ok|panic)' SCRATCH/p3-wails-test.log` — expected: only `ok` lines. A failure outside `app_tasks*_test.go`: run that one test again alone, check whether it also fails without this phase's commits, and report it with the log's path (do not change unrelated tests; this suite has failed once under load before).
8. `npm --prefix wails-app/frontend test` — expected: every test file passes, including `doctrine.test.js` (no binding imports `internal/monomind`) and `selectStyle.test.js`.
9. `git merge-base feat/tasks-board HEAD` (write down the SHA it prints), then `git diff --stat <that SHA> HEAD -- cmd internal data` — expected: nothing. This phase changes no main-module code, so the main module's `go vet`/`GOOS` matrix of spec §14 stays P1's.

- [ ] **Step 6: Re-check the migration number (spec D32)**

Run, each alone with no pipe (read the last names of each output): `ls data/migrations`, `git fetch origin master`, `git ls-tree --name-only origin/master data/migrations/`, and `ls` of `data/migrations` in `/Users/morteza/Desktop/monoes/mono-agent`, `/Users/morteza/Desktop/monoes/mono-agent-freebuff`, `/Users/morteza/Desktop/monoes/mono-agent-kilo`.
Expected: `062_tasks.sql` is the only 062 anywhere. If another 062 exists, stop and tell the lead.

- [ ] **Step 7: Commit**

```
git add AGENTS.md CHANGELOG.md docs/mastermind/specs/2026-10-05-task-board-design.md
```
then
```
git commit -m "docs(tasks): the desktop Tasks tab in AGENTS.md, the changelog and the spec" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 13: Screenshots of the built frontend, bindings mocked

**Files:** none in the repo. The PNGs go to your scratchpad directory (below: `SHOTS`, its absolute path written out); the lead attaches them to the PR (spec §10 Tests, §14). Nothing is committed.

**Interfaces:**
- Consumes: the built frontend; the Playwright browser tools (`browser_navigate`, `browser_run_code_unsafe`, `browser_console_messages`), which drive Playwright's own Chromium, not the user's Chrome.
- Produces: seven PNGs and a short note of what they show. If no Playwright tool is available to you, or `browser_run_code_unsafe` is refused (it runs code in the Playwright server and may be gated), do not work around it: say so in your report, so the lead takes the seven pictures of Step 3 another way (spec §10 wants them in the PR).

- [ ] **Step 1: Serve the build**

Run `npm --prefix wails-app/frontend run build`, then start the static server in the background (Bash with `run_in_background`): `npm --prefix wails-app/frontend run preview -- --port 9347 --strictPort`. If the port is taken, choose another free port and use it below. This serves files only; it is not the desktop app and starts no daemon.

- [ ] **Step 2: Take the screenshots**

Open a page with `browser_navigate` to `about:blank`, then run this with `browser_run_code_unsafe` (put the absolute `SHOTS` path in the first line):

```js
async (page) => {
  const SHOTS = '/absolute/path/of/your/scratchpad'
  const iso = (msAgo) => new Date(Date.now() - msAgo).toISOString()
  const M = 60000
  const H = 60 * M
  const card = (id, status, title, extra = {}) => ({
    id, profile_id: 'work', title, notes: '', status, position: id * 1024,
    source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
    last_event: { actor: 'you', kind: 'created', at: iso(3 * H) }, created_at: iso(3 * H), updated_at: iso(3 * H), ...extra,
  })
  const said = (actor, kind, ago) => ({ last_event: { actor, kind, at: iso(ago) } })
  const board = {
    profile: { id: 'work', name: 'Work' }, rev: 41,
    counts: { inbox: 3, ready: 3, in_progress: 2, review: 2, done: 64, stale: 1 },
    tasks: {
      inbox: [
        card(51, 'inbox', 'Compare the three hosting offers from the vendor call', { notes: 'Prices from the call:\n- Basic: 40 EUR a month\n- Pro: 95 EUR a month', source: { kind: 'chrome', url: 'https://www.notion.so/acme/hosting', title: 'Hosting offers', app: '' }, created_at: iso(12 * M) }),
        card(50, 'inbox', 'Reply to Sam about the invoice dated 12 September', { source: { kind: 'os', url: '', title: '', app: 'Mail' }, created_at: iso(2 * H) }),
        card(49, 'inbox', 'Add retry with backoff to the webhook sender', { source: { kind: 'agent', url: '', title: '', app: '' }, ...said('claude-code#a3f9', 'created', 5 * H), created_at: iso(5 * H) }),
      ],
      ready: [
        card(44, 'ready', 'Fix the flaky login test on CI'),
        card(45, 'ready', 'Write the release notes for v0.110'),
        card(46, 'ready', 'Rename the billing tables (see the ADR)', { source: { kind: 'chrome', url: 'https://github.com/acme/app/pull/412', title: 'PR 412', app: '' } }),
      ],
      in_progress: [
        card(40, 'in_progress', 'Upgrade the payments SDK to v5', { claim: { by: 'claude-code#a3f9', until: new Date(Date.now() + 18 * M).toISOString(), stale: false }, ...said('claude-code#a3f9', 'comment', 4 * M) }),
        card(39, 'in_progress', 'Index the support inbox into the knowledge base', { claim: { by: 'codex-2', until: iso(7 * M), stale: true }, ...said('codex-2', 'claimed', 37 * M) }),
      ],
      review: [
        card(37, 'review', 'Move the cron jobs to the daemon scheduler', said('claude-code#a3f9', 'question', 9 * M)),
        card(36, 'review', 'Draft the onboarding email sequence', said('codex-2', 'result', 26 * M)),
      ],
      done: [card(30, 'done', 'Rotate the staging API keys'), card(29, 'done', 'Export last quarter\'s invoices')],
    },
  }
  const show = {
    profile: board.profile, task: board.tasks.review[0], events: [
      { id: 1, at: iso(3 * H), actor: 'you', kind: 'created', from_status: '', to_status: 'inbox', note: '' },
      { id: 2, at: iso(2 * H), actor: 'you', kind: 'moved', from_status: 'inbox', to_status: 'ready', note: '' },
      { id: 3, at: iso(H), actor: 'claude-code#a3f9', kind: 'claimed', from_status: 'ready', to_status: 'in_progress', note: '' },
      { id: 4, at: iso(30 * M), actor: 'claude-code#a3f9', kind: 'comment', from_status: '', to_status: '', note: 'Moved 9 of 11 jobs; **two** still call the old mailer.' },
      { id: 5, at: iso(9 * M), actor: 'claude-code#a3f9', kind: 'question', from_status: 'in_progress', to_status: 'review', note: 'Should the two mailer jobs move too, or stay on cron until the mailer is replaced?' },
    ],
  }
  const mock = ({ board, show, lang, shell }) => {
    try { localStorage.setItem('monoagent-lang', lang) } catch { /* ignore */ }
    const answers = {
      TaskBoard: () => JSON.stringify(board),
      TaskShow: () => JSON.stringify(show),
      TaskPulse: () => ({ profile_id: 'work', rev: board.rev, inbox: 3, review: 2 }),
      TaskAgentShell: () => shell,
      GetProfiles: () => [{ id: 'work', name: 'Work', is_active: true, root_dir: '', icon: '' }],
      GetVersion: () => ({ version: 'v0.110.0' }),
      IsReady: () => true,
      IsDBConnected: () => true,
    }
    window.go = { main: { App: new Proxy({}, { get: (_, name) => async () => (name in answers ? answers[name]() : null) }) } }
    window.runtime = new Proxy({}, { get: (_, name) => (name === 'EventsOnMultiple' ? () => () => {} : () => {}) })
  }
  const open = async (lang, shell, width = 1440) => {
    await page.setViewportSize({ width, height: 900 })
    await page.addInitScript(mock, { board, show, lang, shell }) // the last one added wins
    await page.goto('http://localhost:9347/')
    await page.locator('[aria-label="Tasks"], [aria-label="Tareas"]').first().click()
    await page.locator('[data-tb-id="51"]').waitFor()
    await page.waitForTimeout(400)
  }

  await open('en', '')
  await page.screenshot({ path: `${SHOTS}/p3-board-en.png` })

  const from = await page.locator('[data-tb-id="51"]').boundingBox()
  const over = await page.locator('[data-tb-id="45"]').boundingBox()
  await page.mouse.move(from.x + 40, from.y + 15)
  await page.mouse.down()
  await page.mouse.move(over.x + 60, over.y + over.height * 0.6, { steps: 12 })
  await page.screenshot({ path: `${SHOTS}/p3-drag.png` })
  await page.keyboard.press('Escape')
  await page.mouse.up()

  await page.locator('[data-tb-id="37"]').click()
  await page.locator('[data-testid="tb-drawer"]').waitFor()
  await page.waitForTimeout(300)
  await page.screenshot({ path: `${SHOTS}/p3-drawer.png` })
  await page.keyboard.press('Escape')
  await page.waitForTimeout(100)
  await page.keyboard.press('n')
  await page.keyboard.type('Book the venue for the team offsite')
  await page.screenshot({ path: `${SHOTS}/p3-quick-add.png` })

  await open('en', '', 1000)
  await page.screenshot({ path: `${SHOTS}/p3-narrow.png` })
  await open('es', '')
  await page.screenshot({ path: `${SHOTS}/p3-board-es.png` })
  await open('en', 'CLAUDECODE')
  await page.screenshot({ path: `${SHOTS}/p3-readonly.png` })
  return 'done'
}
```

If the page shows the error boundary or never reaches a card, read `browser_console_messages`: an app-wide binding this mock does not answer is the usual cause. Add an answer for the binding it names to `answers` and run again. Change no product code for the screenshots. When the run returns `done`, read `browser_console_messages` at level `error`: expected empty; anything there is a finding (quote it in your report).

- [ ] **Step 3: Look at each picture**

Read each PNG and check: five columns with counts (Done says 64), the amber stale card #39 and its "ended 7m ago", the live countdown on #40 with its pulsing avatar ring, #51's notes marker and its "Read and approve" button where the other Inbox cards say "Approve", the Question tag on #37 and the Result tag on #36, the source chips (`notion.so`, `Mail`, Agent, `github.com`), the drag shot's ghost, the glowing Ready column and the drop line, the drawer with the question in its amber frame, the quick add box, the narrow window scrolling sideways, Spanish labels, and the read-only banner with no Approve buttons.

Each of these is a finding: a title or label cut where it has room, text or a popover overlapping another element (the how-to popover under the board, the drawer over the header), a column that does not scroll on its own, the 9.5 to 10.5 px mono text unreadable against its background, a colour that is not one of the board's tokens, anything Spanish that is cut or in English. Fix a finding test-first in its own `feat(tasks): …` commit, or report it; after any fix, run Task 12 Step 5 again and take the pictures again.

- [ ] **Step 4: Stop the server**

Stop the background preview task (TaskStop) and close the Playwright browser (`browser_close`). List the seven PNG paths in your report for the lead.

## What only a person can verify, and what the PR says

- The desktop app itself: the build never launches it. In particular the ghost following the pointer in the real WebKit view, the auto-scroll speed near an edge, the FLIP slides, and the pulse of a live claim (jsdom has no layout and no Web Animations; the screenshots are a still browser render). FLIP reads the reduced-motion setting when the page mounts; the CSS follows a change at once.
- The board following real agents: a `monoagentcli task next --claim` run in a terminal should, within about two seconds, move the card, toast "… took #N", and update the badge.
- An app opened from the Dock or Finder acts as the operator; an app started from a terminal running inside an agent session shows its board under the read-only banner.
- One deviation from D21, made by the lead and recorded in the spec: the board is read in process, while every action still runs the CLI (P1 made `task board` operator-only).
- Two choices the lead made for the user, recorded in the spec (§10, §17.3): the read-only tab under an agent's shell, and approval of a card with notes or a clamped title in its drawer rather than in one click.
- The Inbox empty state and the How to capture popover name the Chrome entry (P4) and the Services entry (P5); until those phases ship, those two hints describe what is coming.
- Spec §10's "the hook with fake timers" is met by the fake-timer tests of `useNow` (Task 10c), `useMarks` (Task 10a) and the activity notes (Task 10b); the board hook itself has no timer.
- `-tags nosocial` is not run: the phase changes no main-module code (Task 12 Step 5 checks).
- Task 3 Step 7's comparison with `wails generate module`: quote its result (no difference, or the commit that fixed the spelling, or that the generator could not run).
