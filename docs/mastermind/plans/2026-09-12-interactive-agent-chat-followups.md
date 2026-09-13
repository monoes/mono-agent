# Interactive agent chat — deferred follow-ups (2026-09-12)

Seven items accepted-with-disclosure during the Task 6 review pass on
[2026-09-11-interactive-agent-chat.md](2026-09-11-interactive-agent-chat.md),
now being worked. Each entry has the exact file/line grounding checked at
the time this doc was written (not the reviewer's paraphrase) plus a
proposed fix and test. TDD discipline applies to all of them: failing test
first, watch it fail for the right reason, minimal fix, watch it pass.

## 1. `useChatStream.js` — `historySaved:false` is captured but never consumed

**Where:** `chatReducer.js`'s `turn.finished` case sets
`terminal.historySaved` from the event payload (line ~100). Confirmed via
grep: no file anywhere (`useChatStream.js`, `AIChatPanel.jsx`,
`TurnStatus.jsx`, `ChatTimeline.jsx`) reads `.historySaved` back out of
state. It is dead-on-arrival data today, not a display gap.

**Investigate first (systematic-debugging, root cause before fix):** what
does the backend actually mean by `historySaved:false`? Check
`wails-app/app_chat.go`'s `turn.finished` emission — is this a durability
failure (the turn's final journal write failed, e.g. two-app-owner race,
disk error) that the frontend currently has zero recovery or warning
path for? If so, the fix is almost certainly in `useChatStream.js`: when a
terminal event arrives with `historySaved: false`, re-run something
equivalent to `hydrate()`'s backlog fetch to reconcile against what's
durably stored, or at minimum surface a notice so the user knows the
visible transcript might not match what a reopened session will show.
Do **not** invent a "race" mechanism if the investigation turns up
something simpler (e.g. it really is just an unused warning flag with an
easy single-line consumer) — write down what you actually find before
fixing it.

**Files:** `wails-app/frontend/src/components/chat/useChatStream.js`,
`useChatStream.test.jsx`; read `wails-app/app_chat.go` for where
`historySaved` is set before touching it. Avoid touching `chatReducer.js`'s
`tool.started`/`tool.completed` cases — item 2 below is editing those
concurrently.

**Test:** a `useChatStream` hook test where a mocked `getChatEvents`/live
event delivers `turn.finished` with `historySaved:false`, asserting
whatever the actual fix does (reconcile fetch fires, or a notice appears).

## 2. Tool cards show no elapsed time (Task 4 plan gate, never implemented)

**Where:** `chatReducer.js`'s `calls[callId]` object
(`tool.started`/`tool.completed` cases, lines ~40-70) has no timestamp
field at all — only a turn-level `startedAt` and `lastEventAt` exist.
`ToolActivityCard.jsx` receives just `{ call }` and has no elapsed-time
rendering. The plan's Task 4 gate explicitly wanted "meaningful title,
**elapsed time** and bounded input/output copy" per call.

**Fix:** in `chatReducer.js`, capture `startedAt: ev.at` on the call object
in the `tool.started` case, and `finishedAt: ev.at` in `tool.completed`
(leave both `undefined` for the existing "unmatched result, never saw the
start" fallback branch — no fabricated timing for data that was never
seen). In `ToolActivityCard.jsx`, render a duration: static
(`finishedAt - startedAt`) once completed; for a still-`started` call, a
live-ticking elapsed time is needed — **check `TurnStatus.jsx` first**,
it already re-renders against a ticking `now` for its own "idle for Ns"
display; reuse that pattern instead of inventing a second `setInterval`
mechanism in this component.

**Files:** `chatReducer.js` (only the `tool.*` cases — item 1 above is
editing the same file's `turn.finished` handling concurrently, so keep
the diff scoped), `chatReducer.test.js`, `ToolActivityCard.jsx`, and
`ChatTimeline.render.test.jsx` (existing render tests for this card).

**Test:** reducer test asserting `startedAt`/`finishedAt` land on the call
object; render test asserting a completed card shows a duration and a
still-running card's duration increases across a fake-timer tick.

## 3. Resolved-artifact cache never invalidates after deletion

**Where:** `AIChatPanel.jsx`'s `useResolvedArtifacts` hook caches
`resolved[turnId:callId]` permanently once set — there is no re-check, no
TTL, no invalidation. If a workflow/org/document is deleted *after* its
artifact card first resolved successfully, the card keeps showing an
"Open"/"Copy ID" action against something that no longer exists.

**Fix:** the click handler (`onOpenArtifact` in `App.jsx`, or a wrapper
in `AIChatPanel.jsx` around it) should re-validate at click time — call
the same `resolveArtifact` lookup again right before acting, and if it
now resolves to `null`, show a toast ("this workflow/org/document no
longer exists") instead of navigating/opening. Do not attempt a
background polling/subscription invalidation scheme — that's
disproportionate for how rarely this actually matters; revalidate lazily,
on click.

**Files:** `AIChatPanel.jsx` (`useResolvedArtifacts`, the artifact-card
render call sites), `ChatInteraction.render.test.jsx`.

**Owner:** doing this one directly (same file as items 5 and 7 below;
keeping all three in one hand avoids three-way edit collisions on
`AIChatPanel.jsx`).

**Done, partially — disclosed gap:** implemented as an `openArtifact`
wrapper in `AIChatPanel.jsx` that re-runs `detectArtifactCandidate` +
`resolveArtifact` at click time before calling the real `onOpenArtifact`;
on a `null` result it shows "Couldn't confirm this &lt;type&gt; still
exists" rather than asserting deletion (a failed lookup and a genuine
not-found are indistinguishable through `api.js`'s error-swallowing
`guard()`, so the copy deliberately doesn't claim more than it knows).
**This only covers org and document cards** — workflow cards never call
`onOpenArtifact` at all (Copy ID is a local clipboard action, no
navigation), so a workflow deleted after its card resolved still shows a
copyable stale ID. Not fixed; the workflow card has no click-through path
to hook a revalidation into short of adding one, which felt
disproportionate for "you can copy an ID that no longer resolves to
anything."

## 4. `ListProfileDocuments`-based artifact resolution fetches everything

**Where:** `chatArtifacts.js`'s `resolveArtifact` document branch calls
`api.listProfileDocuments()` (fetches the *entire* vault) then does a
client-side `.find()` by id. `wails-app/app_documents.go` has no by-id
lookup — `ListProfileDocuments` (line 34) is the only entry point, and it
shells out via `a.runMonoCLI("", &raw, "profile", "documents", "list")`.
Confirmed via `cmd/monoagentcli/profile_documents.go`: the CLI only has
`list`/`index`/`search` subcommands, no `get`/`show` by id.

**Fix, sized to the actual severity (scalability, not correctness — this
profile's document counts are realistically small):** add a
`profile documents get <id>` CLI subcommand backed by whatever
`internal/vault` already exposes for a single-row lookup (check before
adding a new vault-layer method — it may already exist for
`DeleteProfileDocument`/`IndexProfileDocument`'s own by-id use). Wire a
new `GetProfileDocument(id)` in `app_documents.go` mirroring
`GetWorkflow`'s trust shape (`app_workflows.go:223`: fetch, `nil` +
not-found error if missing, no separate profile check needed here since
the CLI subprocess is already scoped to the active profile the same way
`ListProfileDocuments` is). Add `api.getProfileDocument(id)` to `api.js`.
Update `chatArtifacts.js`'s document branch to call it instead of
`listProfileDocuments()` + `.find()`.

**Correction after implementation:** the line above originally described
`getProfileDocument` as needing a guard-free shape "so callers can
distinguish errors," by analogy with `getWorkflow`. That was wrong about
`getWorkflow` itself — it already uses `.catch(guard(...))` same as every
other binding, degrading to `null` on any failure. `getProfileDocument`
correctly matches that real pattern instead of the guard-free one
described here.

**Also fold in item 6** (below) while this agent is already in `api.js`.

**Files:** `cmd/monoagentcli/profile_documents.go` (+ test),
`internal/vault/*` only if no by-id getter exists there yet (+ test),
`wails-app/app_documents.go` (+ test if one exists for this file, check
first), `wails-app/frontend/src/services/api.js`,
`wails-app/frontend/src/components/chat/chatArtifacts.js`,
`chatArtifacts.test.js`. Regenerate Wails bindings
(`wails-app/frontend/src/wailsjs/go/main/App.{js,d.ts}`) via the project's
normal build step — do not hand-edit the `.d.ts` only.

**Test:** Go test for the new CLI subcommand/vault getter (found, missing,
cross-profile if the vault layer is profile-scoped); `chatArtifacts.test.js`
update so the document-resolve tests call the new binding instead of
`listProfileDocuments`.

## 5. Dead `onWorkflowCreated` auto-navigation block

**Where:** `AIChatPanel.jsx` lines 569-578 — after a turn with a
`create_workflow` call finishes, it parses the result and calls
`onWorkflowCreated(id)` after a 300ms delay. **Confirmed dead**: grep of
`App.jsx` shows `onWorkflowCreated` is never passed as a prop to
`<AIChatPanel>` at all, so this block's `onWorkflowCreated` is always
`undefined` in production and the `if (createWf && onWorkflowCreated)`
guard never passes.

**Fix:** remove the block. It predates Task 6's `ChatArtifactCard`, which
already gives `create_workflow` results a "Copy ID" action deliberately
(not an "Open" navigation, per Task 6's own scope — there is no
workflow-editor deep-link route to navigate to). Reviving rather than
removing would mean building a route that Task 6 explicitly decided not
to build; don't do that as a side effect of a dead-code cleanup.

**Files:** `AIChatPanel.jsx` (remove the block and the now-unused
`onWorkflowCreated` prop from the component signature); check
`AIChatPanel.render.test.jsx`/`ChatInteraction.render.test.jsx` for any
now-invalid references (grep first — likely none, since the prop was
never wired).

**Owner:** doing this one directly, alongside items 3 and 7.

## 6. Orphaned legacy `api.js` chat bindings

**Where:** `api.js` lines 111-113, 123-130 — `streamAIChat`, `stopAIChat`,
`getAIChatHistory`, `streamAgentChat`, `stopAgentChat`, `listChatSessions`,
`getChatSessionMessages`. Confirmed via grep across the entire frontend
`src/` tree (not just `AIChatPanel.jsx`): zero call sites anywhere outside
`api.js`/`api.test.js` itself. These are the pre-event-sourced chat API
this whole plan replaced.

**Fix:** the original plan text explicitly sanctioned keeping these
during migration, but the migration is now complete (Tasks 1-6 shipped,
this session's commit lands the whole feature) — remove the seven
bindings and their `api.test.js` coverage. If any Go-side handlers
(`StreamAIChat`, `StopAIChat`, etc. in `wails-app/app_ai.go`) are now
*also* unreferenced from any remaining Go caller, note that in this file
rather than removing Go code as a drive-by — that's a separate,
larger-blast-radius decision (removing a Wails-exported method changes
the generated bindings) than trimming a dead JS wrapper.

**Owner:** bundled into item 4's agent (already touching `api.js`).

## 7. Bucket-switch fetch failure has no retry path

**Where:** `AIChatPanel.jsx`'s bucket-switch effect sets
`conversationsFetchedRef.current = bucket` *before* the
`listChatConversations` call resolves. Tonight's fix added a `.catch()`
so a failure now surfaces a toast — but the bucket is already marked
fetched, so switching back to it won't retry; the only way to retry today
is switching to a different workflow/mode and back (which touches a
*different* bucket key, incidentally leaving this one still marked
fetched-but-never-loaded).

**Fix:** on the `.catch()` path, clear `conversationsFetchedRef.current`
(reset to `null`, or specifically un-mark this bucket) so the effect's
guard (`conversationsFetchedRef.current === bucket`) doesn't block a
retry the next time this bucket's effect deps re-run. Confirm this
doesn't reintroduce a refetch-on-every-render loop — the effect's own
dependency array (`[workflowID, useAgents, isOpen]`) only re-runs on an
actual bucket-relevant change, not on every render, so clearing the ref
on failure should be safe, but verify with a test that toggles back to
the same bucket after a failure and confirms exactly one retry fetch,
not a loop.

**Files:** `AIChatPanel.jsx`, `ChatInteraction.render.test.jsx`.

**Owner:** doing this one directly, alongside items 3 and 5.

---

## Round 1 multi-agent review (2026-09-12): fixed vs. documented

`/mastermind:review --tillend` dispatched four independent specialist
agents (Code Reviewer, Security Engineer, Software Architect,
Accessibility Auditor) over the full `b8f53bb..HEAD` diff (58 files,
~9,500 insertions — commits `b955d8e`, `a024b77`, `4dc342b`, i.e. the
seven items above plus their own review passes). All four were briefed on
this document and told not to re-report anything already listed. Per
advisor guidance, round 1 fixed only the four highest-severity,
clearest-scoped findings (all independently reproduced with a failing
test first); everything else is recorded below for a later round rather
than crammed into one pass.

### Fixed this round (TDD: failing test confirmed, then fix, then green)

1. **`turn.finished` fallback event used `seq: 0`, permanently soft-locking
   the chat panel.** When `store.FinalizeTurn` itself fails, `app_chat.go`'s
   `finalize()` built a live-only fallback event with `Seq: 0` — always
   `<= state.lastSeq` in `chatReducer.js`, so the reducer unconditionally
   dropped it, `state.terminal` never got set, and `send()`'s own guard
   then blocked every further message for the rest of the session (only a
   restart recovers, via `reconcileOrphanedTurns`). Fixed by giving this
   sentinel event `chatevents.MaxSafeSeq` (2^53-1, the largest value that
   round-trips exactly through JSON into a JS Number) instead of `0`.
   Found independently by the Code Reviewer. Files: `chatevents/event.go`,
   `app_chat.go`, `app_chat_test.go`.
2. **`historySaved:false` notice could render twice for one occurrence.**
   `useChatStream.js`'s `dispatchEvent` fired its `localNotice` side effect
   unconditionally on every call for a matching `turn.finished` payload,
   not gated by whether the event was actually newly applied. Two
   independent mechanisms reach this: `fillGap` has no upper bound and can
   re-dispatch an event `dispatchLive` then dispatches again right after
   (Code Reviewer), or a hydration-tail/live-buffer overlap at the
   hydrating→live handoff (traced independently before the Code Reviewer's
   report landed). Fixed by gating the side effect on the same
   "already-seen-this-seq" check the reducer itself uses. Files:
   `useChatStream.js`, `useChatStream.test.jsx`.
3. **`save_document` could be reached by a prompt injection post
   synced-comms-read.** Unlike `run_workflow`, `save_document` never called
   `checkRunGate`, so once untrusted synced-comms content entered the
   session, nothing stopped an injected instruction from writing a
   malicious `.html` artifact (the new `docscan` allowlist entry is safe
   for the in-app sandboxed preview, but `FileViewerModal`'s "Open
   Externally" button hands the file to a real, unsandboxed browser).
   Fixed by extracting the synced-comms check into `checkInjectionGate`
   (shared by both tools) and calling it from `saveDocument` — without
   also requiring the unrelated `runsAllowed()` opt-in `checkRunGate`
   carries, since saving a document isn't a workflow/action run. Found by
   the Security Engineer. Files: `monoagent_tools.go`,
   `monoagent_tools_test.go`.
4. **Bucket-switch effect's success path had no staleness guard.** Its
   `.catch()` already checked `conversationsFetchedRef.current === bucket`
   before acting (item 7 above); the `.then()` success path did not, so a
   late-resolving fetch for a bucket the UI no longer shows (e.g. a quick
   agents↔providers toggle) could fully overwrite an already-loaded,
   correct transcript with the stale bucket's conversation — reproduced
   exactly by a regression test before fixing. Found by the Software
   Architect. Files: `AIChatPanel.jsx`, `ChatInteraction.render.test.jsx`.

### Documented, not fixed this round

**Architecture**

- **`useResolvedArtifacts` permanently caches a `null` from a transient
  failure, not just genuine non-existence** (`AIChatPanel.jsx:173-202`).
  `resolveArtifact`'s own lookups can't distinguish "confirmed gone" from
  "the lookup itself failed" (same ambiguity item 3 above already accepts
  for click-time revalidation) — but unlike that case, this is the
  *first* resolution: if it fails transiently right as a tool call
  completes (plausible, since this often coincides with the same SQLite
  file the chat supervisor is concurrently writing to), the Open/Copy-ID
  card never appears at all, with no click affordance to ever retry.
  Deferred because a correct fix needs to bound retries (an unconditional
  retry-on-every-render risks spamming a genuinely-nonexistent lookup on
  every unrelated re-render) — a small design decision, not a
  one-line fix. Confirmed untested either way.
- **`OwnerInstanceID` is written and read back but never compared to
  anything** (`chat_events.go:53`, `app_chat.go:218/704/738`). The plan
  requires another live instance's turn to be read-only/stoppable-only-in-
  its-owner; the schema/scaffolding exists but `admit()`, `StopChatTurn`,
  and `GetChatTurns`/`GetChatEvents` never consult it — two concurrently
  live app instances can each admit a turn against the same conversation,
  and whichever finishes last silently overwrites the other's
  `--resume` session binding. Narrow trigger (two live instances against
  one `~/.monoagent` DB) but a real, disclosed gap, not a documented
  trade-off. Deferred as a design question (what should the UI show for a
  foreign-owned active turn? should `StopChatTurn` reject it explicitly?),
  not a mechanical bug fix.
- **`AIChatPanel.jsx` coherence has measurably decayed**: both findings
  above live in code that stayed in this 1,095-line file instead of being
  extracted, while everything that *was* extracted (`chatReducer.js`,
  `useChatStream.js`, `chatArtifacts.js`) has direct unit tests and no
  comparable defect found in this review. Extraction candidates:
  `useResolvedArtifacts` (beside `chatArtifacts.js`, which it exclusively
  wraps), `openArtifact` (a generic revalidate-before-acting helper with
  no panel-specific dependency), `reduceTurnEvents`/`loadTurnState` (a
  second, independent "paginate getChatEvents to exhaustion" loop,
  conceptually `useChatStream.js`'s sibling). Structural, not urgent — a
  refactor beyond this review's fix-a-bug scope, not attempted here.

**Accessibility** (all confirmed by direct source reading, not just the
auditor's report)

- **Past-sessions dropdown is a keyboard trap.** Rows are bare
  `<div onClick>` (no `tabIndex`/`role`/`onKeyDown`); the toggle button has
  no `aria-expanded`/`aria-haspopup`; Escape while the list is open closes
  the whole panel instead of just the list (the handler never checks
  `showSessions`); the active conversation is distinguished only by a 10%-
  opacity tint, no `aria-current`. `AIChatPanel.jsx:796-848,527-536`.
- **Continuous panel-resize drag has no keyboard path** — the *discrete*
  Expand/Collapse toggle is keyboard-operable (380px⇄760px), so this is a
  narrower gap than a hard block, not fixed given a working alternative
  already exists. `AIChatPanel.jsx:731-740,539-564`.
- **Turn-status live region is mount-coupled — likely silent exactly when
  it matters most.** `TurnStatus`'s `role="status" aria-live="polite"` div
  only exists inside `{streaming && ...}`; at turn-finish, that block
  unmounts and a *new* `TurnStatus` instance mounts inside `messages.map`
  with the terminal label already baked in — a fresh node's initial
  content is unreliably announced by AT (a well-known ARIA gotcha), so
  "Completed"/"Failed" likely goes unannounced despite mid-stream status
  changes working correctly. Compounding gap: tool failures, notices
  (including this session's own `history_not_saved` warning), and
  `ChatComposer`'s disabled-reason banner are all plain DOM with no
  `aria-live` anywhere. Fix shape (per the auditor, matches this
  reviewer's own read): one persistent, always-mounted
  `aria-live="polite"` region at the panel level, fed short strings for
  exactly these status-message events — not sprinkled across each
  component. Deferred because the fix threads through `AIChatPanel.jsx`
  (same file as fix 4 above), `TurnStatus.jsx`, `ChatTimeline.jsx`, and
  `ChatComposer.jsx`; doing it concurrently with a correctness fix in the
  same file risked a regression in currently-100%-passing code.
  `AIChatPanel.jsx:236,567-579,1020-1057`, `TurnStatus.jsx`,
  `ToolActivityCard.jsx`, `ChatTimeline.jsx:16-48`, `ChatComposer.jsx:32-36`.
- **`ToolActivityCard`'s `aria-controls` is dangling while collapsed, and
  its id isn't turn-scoped.** The controlled `<div id={panelId}>` only
  exists `{open && ...}` — while collapsed (the default), `aria-controls`
  points at nothing. Separately, `panelId` is built from `call.callId`
  alone, which this codebase's own code elsewhere documents as *not*
  unique across turns (`AIChatPanel.jsx:158-165`'s cache key is
  `${turnId}:${callId}` for exactly this reason) — every past turn stays
  mounted simultaneously, so two tool-using turns produce duplicate DOM
  ids. Fix needs threading `turnId` through `ChatTimeline` into
  `ToolActivityCard` (both already receive/have access to it) and
  rendering the controlled div unconditionally (toggle visibility, not
  mount) — deferred as a dedicated pass alongside the live-region fix
  above, same file-overlap reasoning. `ToolActivityCard.jsx:60,96-124`.

**Code Reviewer** (findings 1-6 traced/verified directly; 7-10 sourced
from the reviewer's own sub-agents — the reviewer's report was internally
inconsistent about whether finding 7 was independently re-verified, so
all of 7-10 are treated as lower-confidence and none were acted on here)

- **Provider-backend tool cards always show ~0.0s elapsed time.**
  `app_chat.go`'s `onToolCall` closure emits `tool.started` immediately
  followed by `tool.completed` because the provider tool loop
  (`service.go`) invokes the callback only once, after the tool has
  already fully executed — there's no true start timestamp to record.
  Root-caused to the callback signature having no separate start/end
  hook; fixing it means changing that signature across `service.go` and
  `app_chat.go`, not a local patch. Agent-backend elapsed time is
  unaffected.
- **Provider-backend tool failures always render as success.** Same
  `onToolCall` closure hardcodes `ok := true` unconditionally;
  `executeTool` collapses a real failure into a `{"error":...}` string
  with no separate channel for `app_chat.go` to read. Same root cause and
  same cross-file signature-change scope as the item above — the plan's
  own review-resolution table calls this fixed, but only for the
  agent/CLI backend, not the provider backend.
- **Agent-backend: a tool call orphaned mid-turn (Stop, crash, kill)
  ticks forever on replay.** `ToolActivityCard`'s live-ticking clock is
  driven purely by `call.status === 'started'`, which can't distinguish
  "belongs to the currently-streaming live turn" from "belongs to a
  finalized historical turn being replayed." A call stuck at `started`
  (no `tool.completed` ever arrives) shows a live-ticking "Running" clock
  counting up from its original `startedAt` — hours or days later,
  indefinitely, every time that conversation is reopened. Concrete,
  testable fix (thread whether the enclosing turn is live vs. replayed
  into `ToolActivityCard`) — deferred alongside the two `aria-controls`/
  live-region a11y fixes above due to the same `ChatTimeline.jsx`/
  `AIChatPanel.jsx` overlap.
- **`DeleteConversation` isn't atomic against a concurrent
  `StartChatTurn`.** The active-turn check
  (`chat_events.go:249-282`) runs outside any transaction, before the
  delete's own transaction opens; nothing locks the conversation
  in between, and neither `ai_chat_turns` nor `ai_chat_events` has a
  foreign key back to the conversation. A `StartChatTurn` admitted
  in-memory but not yet DB-committed can race a `DeleteConversation` that
  sees zero active turns, proceeds, and leaves an orphaned, invisible,
  un-stoppable turn still writing events for a conversation_id nothing
  can query. Real, but needs a concurrency-control design decision
  (transactional lock scope, or a FK plus migration), not attempted here.
- **Tool-output truncation silently discards the metadata the plan
  requires, and one field has no bound at all.** `BoundText`'s
  `(truncated, originalBytes)` return values are discarded at both call
  sites, and neither `ToolStartedPayload` nor `ToolCompletedPayload` has a
  field to carry them — contrary to the plan's explicit spec ("Oversized
  tool content is represented by ... truncated:true"); the UI gives no
  indication content was cut, and Copy silently copies the truncated text
  as complete. Separately and more sharply, `NoticePayload.Message` has
  **no bound at all** (unlike every other field), so a verbose external
  adapter's error text can write an unbounded row/event payload. The
  `NoticePayload.Message` half is a small, mechanical, low-risk fix
  (apply the same bounding used elsewhere); the `truncated`/
  `originalBytes` half is a real but larger plan-spec gap spanning Go
  payload types, both call sites, the JS reducer, and `ToolActivityCard`
  rendering. Neither attempted this round — bundling a small fix with a
  larger one in the same area seemed likely to get half-done; both are
  cleanly scoped for a dedicated pass.
- **Lower-confidence (sub-agent-sourced, not independently re-verified at
  the source — do not act on these without first reading the actual code
  in `service.go`/`exec.go` directly):** provider-backend round-cap
  (`maxToolRounds`) silently reports turn success with tool calls still
  pending, instead of a distinct status; no `ctx.Err()` check in the
  provider tool-calling loop between round iterations (Stop mid-round
  lets the rest of that round run); the 40-message history window is a
  blind positional slice with no role-awareness, which can orphan a
  tool-result message at the window's start; `exec.go`'s
  `ApplyEventToResult` treats `res.Err` as sticky (first `is_error:true`
  wins for the whole turn) while other fields are last-write-wins, against
  `TurnResult.StopReason`'s own doc comment implying multiple `result`
  events per turn; `exec.go`'s tool-dispatch has no `ctx.Err()` check
  before invoking `OnToolCall` (unlike `app_chat.go`'s own comments,
  which suggest this was intended — plan Task 2 commits to context checks
  "before tools"). One item from this group *was* narrow and mechanical
  enough to note as safe: `save_document`'s duplicate-filename check
  (`os.Stat` then `os.WriteFile`) is a TOCTOU race — `O_CREATE|O_EXCL`
  would close it in one line — but wasn't bundled into this round's
  `save_document` fix to keep that fix's diff minimal and singly-focused.

**Security**

- **Non-artifact tool call output is relayed to the UI without
  independent re-verification** (`app_chat.go:496-502`, `ev.OK`/
  `ev.Result.Text` from the external `monomind` binary's NDJSON, for any
  tool call outside the three allowlisted artifact types). Explicitly
  assessed by the Security Engineer as acceptable as implemented (that
  content only ever renders inert in a `<pre>`, never triggers an action,
  and `monomind` is a local sibling binary, not network-facing) — recorded
  here as a named trust boundary, not a finding requiring a fix.

## Round 2 verification (2026-09-12): all four round-1 fixes confirmed correct

A fresh, independent Code Reviewer agent adversarially re-verified all 4
round-1 fixes against the actual source (not just the diff) — tracing the
exact double-dispatch mechanism for fix 2, confirming fix 4's test
actually exercises the new guard and not the pre-existing
`loadGenerationRef` mechanism, confirming `MaxSafeSeq`'s value and
checking for other hardcoded-seq call sites for fix 1, and confirming
`checkInjectionGate`'s placement and `checkRunGate`'s preserved behavior
for fix 3. All four: **confirmed correct, no defects found.** Full
`go build`/`go vet`/`go test` (fresh, `-count=1`) from both modules and
`npx vitest run` (244/244) all pass.

Two adjacent, lower-severity observations surfaced from directly
answering this review's own adversarial sub-questions (not independent
findings from an open-ended pass) — neither is a defect in the round-1
fixes, both are narrower/pre-existing:

- **Other `MonoagentTools` that write durable state aren't gated by
  `checkInjectionGate`**: `add_secret`/`update_secret` (vault secrets),
  `create_org`/`add_org_role`/`update_org_role`/`set_role_reports_to`/
  `remove_org_role` (org config JSON), `create_workflow`/
  `add_workflow_node`. Judged lower-severity than `save_document`: none
  produce a directly-openable/executable artifact on their own — a
  poisoned secret or workflow/org still needs a separate step (wiring the
  secret into a node, or a human running the workflow/starting the org)
  before it does anything, and `run_workflow`'s own gate still blocks
  execution within the same poisoned session. Same risk profile as the
  already-accepted, unflagged `create_workflow` risk — not a gap this
  round's security fix was scoped to close, but worth a dedicated pass if
  the injection-guard surface is revisited.
- **`conversationsFetchedRef` is a single bucket-string flag, not a
  per-request token** — a rapid **A→B→A** toggle (not just the simpler
  A→B case fix 4 targets) can leave two overlapping in-flight requests
  both "for bucket A"; if the older one resolves after the newer one, its
  staleness check also reads `current === bucket` as true and can
  redundantly re-run `loadConversation` with stale data, silently
  overwriting a freshly-loaded correct transcript if a new conversation
  was created in bucket A during that window. Identical exposure already
  exists in the `.catch()` branch. Narrower than the bug fix 4 closed
  (needs a 3-state toggle, not just 2) — fold into the same future pass as
  the `AIChatPanel.jsx` extraction items above (a per-request token/ref
  would close this and is a natural fit alongside that refactor).

## Pre-existing test flake found while bounding NoticePayload.Message (2026-09-12)

`TestChatSupervisor_UnknownFlagLaunchFailure_ReportsDistinctNoticeThenFailed`
fails intermittently (~3/5 runs observed) when run alongside the rest of
`TestChatSupervisor_*`, but passes reliably in isolation. Confirmed via
`git stash` that this reproduces identically on the round-1 commit
(`ce2495f`), before today's `NoticePayload.Message` bounding change — not
introduced or worsened by that fix.

**Likely root cause:** `runAgentTurn` (`app_chat.go` ~line 357-399) reads
stderr in one goroutine into a mutex-guarded `stderrBuf`, while a second
goroutine calls `proc.Wait()` and then immediately snapshots
`stderrBuf.String()` with no synchronization ensuring the first goroutine
has actually finished draining the pipe by that point — a real missing
happens-before edge, not just CI noise. For a real OS process this window
is usually negligible (`Wait()` returning implies the process exited,
and its pipes closing follows closely), but under enough concurrent
goroutine scheduling pressure (many tests' fixtures running in the same
process) the reader goroutine can lose the race, and
`finalizeAgentTurn` sees an empty `stderrText`, missing the
`strings.Contains(stderrText, "unknown flag")` check this specific test
depends on.

**Fix shape:** join the stderr-reader goroutine (e.g. a `sync.WaitGroup`
or a done channel) before reading `stderrBuf` in the wait goroutine,
rather than relying on `Wait()`'s return as an implicit signal.

**Files:** `wails-app/app_chat.go`, `wails-app/app_chat_test.go`.

Not fixed here — found incidentally while verifying an unrelated fix, and
concurrent-goroutine synchronization deserves its own focused pass rather
than a bolt-on.

## Round 3 (2026-09-12): quick wins + accessibility/UX cluster

Two small, low-risk mechanical fixes, plus a coherent cluster of
accessibility/UX fixes deliberately kept together because they touch the
same files (`AIChatPanel.jsx`, `ChatTimeline.jsx`, `ToolActivityCard.jsx`,
`TurnStatus.jsx`) — round 1's own advisor guidance was to defer these
rather than risk a regression by mixing them with unrelated correctness
fixes in the same round. Each fixed with a failing test confirmed first.

- **`NoticePayload.Message` bounded.** `app_chat.go`'s non-fatal-error
  notice path now runs `ev.ErrMessage` through `chatevents.BoundText` like
  every other field, instead of passing an external adapter's error text
  through with no cap at all. Files: `app_chat.go`, `app_chat_test.go`.
  (Found and documented, but did not fix, a genuine pre-existing test
  flake while verifying this — see the dedicated section above.)
- **`save_document`'s TOCTOU race closed.** The `os.Stat`-then-`os.WriteFile`
  duplicate-filename check let two concurrent calls for the same filename
  both pass the check before either wrote, silently overwriting instead of
  refusing. Now a single atomic `os.OpenFile(..., O_CREATE|O_EXCL, ...)`.
  Files: `monoagent_tools.go`, `monoagent_tools_test.go`.
- **Past-sessions dropdown keyboard trap fixed.** Rows are now real
  `role="option"` elements (`tabIndex={0}`, `aria-selected`,
  Enter/Space-activated); the toggle button reports `aria-expanded`/
  `aria-haspopup="listbox"`; the list itself is `role="listbox"`; Escape
  now closes only the dropdown when it's open, not the whole panel. Files:
  `AIChatPanel.jsx`, `ChatInteraction.render.test.jsx`.
- **`ToolActivityCard`'s controlled panel fixed.** The panel div is now
  always mounted (visibility toggled via `hidden`, not conditional
  rendering), so `aria-controls` never points at a nonexistent element
  while collapsed — the default state for every non-error card. Its id is
  now scoped by `turnId` (threaded through `ChatTimeline` from both
  `AIChatPanel.jsx` call sites), closing the duplicate-id risk across
  turns reusing the same `callId`. Files: `ToolActivityCard.jsx`,
  `ChatTimeline.jsx`, `AIChatPanel.jsx`, `ChatTimeline.render.test.jsx`.
- **Orphaned tool calls no longer tick forever on replay.** `ToolActivityCard`
  now takes an `isLive` prop (threaded the same way as `turnId`, default
  `true` for backward compatibility) — a call stuck at `status:'started'`
  in a finalized/replayed turn now shows "Interrupted" once, instead of a
  live-ticking "Running" clock counting up from its original `startedAt`
  indefinitely. Files: `ToolActivityCard.jsx`, `ChatTimeline.jsx`,
  `AIChatPanel.jsx`, `ChatTimeline.render.test.jsx`,
  `ChatInteraction.render.test.jsx`.
- **Turn-completion is now announced to screen readers.** A single,
  always-mounted, visually-hidden `role="status" aria-live="polite"`
  region lives at the panel level (outside `{streaming && ...}`), fed by a
  new pure, directly-tested `composeLiveAnnouncement(turnState)` function
  called when a turn finalizes. This is the fix shape the accessibility
  audit itself recommended, and it also folds in any tool failures and
  notices already present at finalize time — which incidentally gives the
  `historySaved:false` warning (previously a plain, non-live
  `NoticeBanner`) its first real live-region coverage too. **Not fully
  closed**: notices that arrive *mid-turn* (before finalize) and
  `ChatComposer`'s disabled-reason banner still have no live-region
  coverage — left open rather than expanding this round's scope further.
  Files: `AIChatPanel.jsx`, `AIChatPanel.render.test.jsx`,
  `ChatInteraction.render.test.jsx`.

Full suite reconfirmed green after each fix and at the end: `go build`/
`go vet`/`go test` from both Go modules, `npx vitest run` (262/262,
frontend).

## Round 4 (2026-09-12): the remaining harder-tier items, via 8 parallel agents

Two file-disjoint tracks (Go backend, 4 sequential fixes; frontend, 4
sequential fixes) run concurrently via a Workflow script, each step
building on the previous step's already-applied changes. All 8 completed
with TDD discipline; independently re-verified afterward (full fresh
`go build`/`go vet`/`go test` across both Go modules, `npx vitest run`,
plus direct code review of the two highest-stakes diffs — the schema
migration and the provider-callback restructuring).

**Backend — fixed:**

- **The pre-existing flaky test is fixed.** `runAgentTurn`'s wait goroutine
  now joins a `stderrDone` channel (closed by the stderr-reader goroutine)
  before reading `stderrBuf`, closing the missing happens-before edge.
  Verified with a new deterministic repro test (injects a delayed stderr
  read) plus `-race -count=5` and 15+ repeat runs of the full
  `TestChatSupervisor` family — zero failures where roughly half used to
  fail. One disclosed, out-of-scope residual: a real process whose stderr
  goroutine never gets scheduled before `Wait()` force-closes the pipe can
  still lose kernel-buffered bytes — fixing that needs the larger
  `Wait()`/drain-ordering restructuring already deferred, not a bolt-on.
- **Cross-instance turn ownership is now enforced**, with a UI label
  (frontend half below). `CreateTurn` refuses a new turn when the
  conversation already has an active turn owned by a different,
  identified instance (`ErrTurnOwnedByOtherInstance`); `StopChatTurn` now
  returns an explicit failure for a foreign-active turn instead of a
  silent `{"ok":true}`; `GetChatTurns` exposes `ownedByThisInstance` per
  turn. **Two disclosed, accepted gaps**: (1) the admission check is a
  plain read-then-decide, not transactional — matches this file's
  existing precedent (`AppendEvent`, `DeleteConversation` pre-fix) and was
  deliberately not hardened further, since a DB-level unique constraint
  would fail migration outright on any existing database that already has
  two active rows for one conversation; (2) **`reconcileOrphanedTurns`
  still bypasses this fix for a newly-*launched* second instance** —
  arguably the more common trigger than "an already-running instance
  starts a new turn." A window B launched while window A is mid-turn will
  have its own startup unconditionally finalize A's "active" row as
  `interrupted` before B ever calls `StartChatTurn`, silently reverting to
  today's broken behavior for that specific sequence. This needs a real
  liveness/heartbeat mechanism — a separate, larger design problem than
  this round attempted.
- **Provider-backend tool status bugs are both fixed at their shared root
  cause.** `internal/ai/chat/service.go`'s tool loop now calls
  `onToolStart` before executing a tool and `onToolCall` after, with a
  real `error` value instead of a string to sniff — `app_chat.go` derives
  a fresh `ok` per call from that error (fixing the false-success bug) and
  gets a true elapsed time between the two calls (fixing the ~0.0s bug).
  `wails-app/app_ai.go`'s legacy `StreamAIChat` binding needed a mechanical
  compile-fix for the shared signature change (verified as the only other
  caller); zero behavior change there. Disclosed gap: no dedicated
  `wails-app`-level test for the two new closures specifically — the
  injection seams needed are unexported/package-private, and adding a
  production-visible seam wasn't justified for this bug; coverage instead
  comes from the service-level contract tests plus the full supervisor
  suite (incl. `-race`) staying green.
- **`DeleteConversation`'s race is closed at both levels, and both were
  empirically proven necessary, not redundant.** `DeleteConversation`'s
  check-then-delete now runs inside one `BEGIN IMMEDIATE` transaction, and
  migration `040_ai_chat_conversation_foreign_keys.sql` adds real foreign
  keys (`ai_chat_turns.conversation_id`, `ai_chat_events.conversation_id`
  and `.turn_id`, all `ON DELETE CASCADE`) via the standard SQLite
  rebuild-and-rename recipe, safe on both a fresh database and one with
  existing rows (both scenarios have a dedicated migration test). The
  concurrency test settles the "is the FK actually load-bearing" question
  with real numbers: unfixed, 34/100 races orphaned rows; **the
  transaction lock *alone* still left 99/100 orphaned** (`DeleteConversation`
  grabs its write lock first, so by the time a racing `CreateTurn`'s INSERT
  reaches the database, the delete has usually already committed and
  released it); lock + FK together, 0/100 across repeated runs. Disclosed:
  `CreateTurn` never validated the target conversation's existence at all,
  race or not — now fails cleanly via the FK (a generic wrapped error) but
  a typed sentinel for that specific case wasn't added (out of scope).

**Frontend — fixed:**

- **`useResolvedArtifacts` now retries once** on a null/failed resolution
  (300ms delay) before permanently caching a negative result; the retry
  timer is cancelled on unmount so closing the panel mid-retry can't fire
  into a dead component.
- **The two remaining live-region gaps are closed.** Mid-turn notices are
  now announced as they arrive (not just at finalize), and
  `composeLiveAnnouncement` gained an `alreadyAnnouncedCount` parameter so
  finalize's summary never repeats one already announced live. The
  composer's `disabledReason` banner is now announced on change too. Both
  feed the same single live region — deliberately merged into one
  `useEffect` rather than two, since a `historySaved:false` notice and its
  triggering `turn.finished` land in the same React batch, and two
  independent effects would race on which one's `setLiveAnnouncement` call
  actually reaches the DOM. **Disclosed process deviation**: this agent
  wrote the fix before the tests (against explicit instructions), caught
  it before declaring done, and retroactively verified red-then-green by
  reverting and re-applying — a real if out-of-order verification, not a
  skipped one, but worth knowing. **Unconfirmed, low-frequency flake
  observed**: a one-off failure in the new `AIChatPanel.liveRegion.test.jsx`
  (written by this task), seen once by a *later* agent in ~6 full-suite
  runs, never in isolation. Independently re-run 20 times since (12
  full-suite, 8 isolated) with zero reproductions — noted here rather than
  chased further.
- **The cross-instance ownership label is rendered.** A foreign-active
  turn now shows a static "Running in another window" label with no
  spinner/ticking clock (`TurnStatus.jsx`), and `loadTurnState`'s
  zero-parts skip no longer hides a foreign turn that hasn't produced any
  event yet (a gap the assigned task didn't name explicitly but the
  advisor caught — a foreign turn admitted moments ago would otherwise
  vanish from the transcript entirely right as this window's own next
  `send()` gets refused, with nothing on screen explaining why). Disclosed
  gap: `ChatTimeline.jsx`/`ToolActivityCard.jsx` weren't touched, so an
  individual tool call within a foreign-but-active turn still renders as
  "Interrupted" (round 3's orphaned-call handling) rather than something
  more precise — a real, minor, adjacent inaccuracy left for a later round.
- **`AIChatPanel.jsx` is extracted, zero behavior change.**
  `useResolvedArtifacts` → its own file beside `chatArtifacts.js`;
  `openArtifact` → an exported helper in `chatArtifacts.js` itself;
  `reduceTurnEvents`/`loadTurnState` → into `useChatStream.js`, beside its
  own `hydrate()`. Each extracted piece now has its own direct unit tests.

Verification, independently re-run after all 8 agents reported (not just
each agent's own siloed check): `go build`/`go vet`/`go test` clean across
both Go modules (root + `wails-app`), `wails-app` suite re-run 5x with
zero failures, `npx vitest run` **302/302** (26 files) re-run 12x with
zero failures. Direct code review (not just trusting the summaries) of
the migration SQL against the live Go schema, the migration's ordering
relative to `ApplyMigrations()`/`initTables()` in both the GUI and CLI
boot paths, the provider callback's start/before-execution and
call/after-execution placement, and the `CreateTurn`/`StopChatTurn`/
`GetChatTurns` ownership logic — all confirmed correct as reported.

## Post-round-4 real-usage findings (2026-09-13)

Manual use of the actual built app (not just automated tests) surfaced two
issues automated coverage missed entirely:

- **`ToolActivityCard`'s collapse/expand regressed by round 3's own
  `aria-controls` fix, and no test caught it.** The controlled panel div
  was given both `hidden={!open}` *and* an unconditional inline
  `style={{ display: 'flex', ... }}`. An inline style always wins over the
  UA stylesheet's `[hidden]{display:none}` rule, so the body rendered
  permanently visible regardless of the toggle — clicking the chevron to
  collapse a tool card did nothing visible. Every round-3 test checked DOM
  *presence* (`getElementById`, `getByText().toBeInTheDocument()`) or used
  jest-dom's `toBeVisible()`, which checks the `hidden` *property* directly
  and so didn't expose the conflict either — none checked the element's
  actual `style.display`, which is the one place the bug was visible.
  Fixed by making the inline style agree with `hidden` instead of fighting
  it (`display: open ? 'flex' : 'none'`), and added a test that checks
  `style.display` directly, specifically because the softer checks already
  in place had proven insufficient. A reminder that DOM-presence assertions
  and even standard visibility matchers are not a substitute for checking
  the exact style property a bug actually touches.
- **A generated document mentioned in an assistant's own reply text isn't
  clickable — by design, but it's a rough edge worth knowing about.**
  When `save_document` fails because the file already exists (e.g. the
  model retries a name from an earlier turn/conversation), the model's
  reply may still describe the file by path in prose. `ChatMarkdown`
  correctly refuses to make a relative/local path clickable (the same
  scheme-gating that blocks `javascript:`/`data:`/relative URLs generally
  — tested, deliberate). This is not a bug, but it means the *only*
  currently-working path to open a document from chat is a fresh,
  successful `save_document` call's own `ChatArtifactCard` — there is no
  fallback affordance when the call fails specifically because the
  document already exists. A real, scoped enhancement would be: on that
  specific failure, have `save_document` return enough information (e.g.
  the existing file's path/vault id) for the frontend to still offer an
  "Open existing document" affordance instead of a bare error string. Not
  implemented — this is new scope, not a bug fix, and needs a decision on
  the tool's response contract before building it.

---

## Status

- [x] 1. useChatStream historySaved consumption
- [x] 2. Elapsed time on tool cards
- [x] 3. Artifact cache revalidation on click
- [x] 4. Scoped document-by-id lookup
- [x] 5. Remove dead onWorkflowCreated block
- [x] 6. Remove orphaned legacy api.js bindings
- [x] 7. Bucket-switch retry after failure

### Round 1 review — fixed

- [x] R1.1 `turn.finished` fallback seq:0 soft-lock
- [x] R1.2 Duplicate `historySaved:false` notice
- [x] R1.3 `save_document` missing injection gate
- [x] R1.4 Bucket-switch `.then()` staleness guard

### Round 3 (quick wins + accessibility/UX cluster) — fixed

- [x] `NoticePayload.Message` now bounded like every other field
- [x] `save_document` TOCTOU race on duplicate-filename check (`O_CREATE|O_EXCL`)
- [x] Past-sessions dropdown keyboard trap + Escape/aria-expanded/aria-selected
- [x] `ToolActivityCard` aria-controls dangling + unscoped id cross-turn collision
- [x] Agent-backend: orphaned tool call ticks forever on replay
- [x] Turn-status live region mount-coupled — turn-completion + tool-failure +
      already-present-notice announcements now fixed; mid-turn notices and
      the composer's disabled-reason banner remain open (see below)

### Round 4 (cross-instance ownership, provider tool status, delete race, artifact retry, live-region gaps, AIChatPanel extraction) — fixed

- [x] R4.1 Pre-existing flaky test (`TestChatSupervisor_UnknownFlagLaunchFailure_...`)
- [x] R4.2 `OwnerInstanceID` cross-instance admission enforced + UI label
- [x] R4.3 Provider-backend tool elapsed time + false-success bugs
- [x] R4.4 `DeleteConversation` race (transaction + FK migration)
- [x] R4.5 `useResolvedArtifacts` permanent null-cache (bounded 1-retry)
- [x] R4.6 Live-region coverage for mid-turn notices + composer disabled-reason
- [x] R4.7 `AIChatPanel.jsx` extraction (useResolvedArtifacts, openArtifact, reduceTurnEvents/loadTurnState)

### Documented, open for a later round

- [ ] Continuous panel-resize has no keyboard path (discrete alternative exists)
- [ ] `reconcileOrphanedTurns` bypasses R4.2's fix for a newly-launched second instance (see Round 4 detail above)
- [ ] `ChatTimeline.jsx`/`ToolActivityCard.jsx` don't distinguish a foreign-active turn's tool calls from an ordinary orphaned one (see Round 4 detail above)
- [ ] Tool-output truncation metadata discarded (`truncated`/`originalBytes`)
- [ ] Lower-confidence service.go/exec.go items (round-cap status, ctx.Err()
      checks, history-window slicing, sticky res.Err — see round-1 detail above)
- [ ] Unconfirmed low-frequency flake in `AIChatPanel.liveRegion.test.jsx` (see Round 4 detail above)

## Addendum: two things found outside the seven items' scope

- **Wails binding regeneration picked up the pre-existing v2.11.0/v2.15.0
  mismatch.** Item 4's `wails generate module` run (needed to add
  `GetProfileDocument` to the generated bindings) regenerated
  `wails-app/frontend/src/wailsjs/runtime/runtime.d.ts` and `runtime.js`
  under whatever Wails version `wails-app/go.mod` currently resolves to —
  which is the same uncommitted, unrelated v2.15.0→v2.11.0 downgrade
  flagged as out-of-scope before any of this work started (see the
  commit-scope discussion for `b955d8e`). The regeneration stripped
  v2.15-only runtime APIs from both files (83/56 changed lines, almost
  entirely deletions). `App.d.ts`/`App.js` themselves only gained the
  expected `GetProfileDocument` declaration (+2/+4 lines) — those are
  clean. **`runtime.d.ts`/`runtime.js` must NOT be included in any commit
  of this follow-up work** — they're a byproduct of the unrelated
  downgrade, not of anything in this document, and reflect whatever Wails
  version happens to be checked out rather than a deliberate decision.
- **An 8th orphaned `api.js` binding, `clearAIChatHistory`**, was found
  alongside the seven removed in item 6 — same dead-since-migration shape,
  confirmed via the same grep. Removed in a follow-up pass (no test
  coverage existed for it either); full suite reconfirmed green (242/242)
  after removal.
