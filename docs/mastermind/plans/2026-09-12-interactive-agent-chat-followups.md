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

## Status

- [x] 1. useChatStream historySaved consumption
- [x] 2. Elapsed time on tool cards
- [x] 3. Artifact cache revalidation on click
- [x] 4. Scoped document-by-id lookup
- [x] 5. Remove dead onWorkflowCreated block
- [x] 6. Remove orphaned legacy api.js bindings
- [x] 7. Bucket-switch retry after failure

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
