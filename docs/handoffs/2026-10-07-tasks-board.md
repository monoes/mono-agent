# Tasks board handoff — 2026-10-07

This is an unfinished checkpoint for continuation on another machine. Keep the user-selected model throughout resumed work; historical instructions to use Fable/Opus for final reviews are superseded.

## Preserved branches

| Branch | Code checkpoint | Status |
| --- | --- | --- |
| feat/tasks-board | 3c7bc612 plus this handoff | Phase 1 store/CLI complete; PR #359 open, all currently reported CI checks pass |
| feat/tasks-board-mcp | 8c266272 | Phase 2 Tasks 1–2 complete/reviewed; Task 3 fix committed, scoped re-review unfinished |
| feat/tasks-board-gui | 847971bb | Phase 3 Tasks 1–2 complete; initial Task 3 service/tests |
| feat/tasks-board-gui-service-tests | 91b0f854 | Task 3 additional tests and re-review complete; supersedes initial service-test checkpoint |
| feat/tasks-board-gui-model | 35c5b6c4 | Task 4a model implementation plus fix round 1; scoped re-review unfinished |

Phase 2 Task 4 was interrupted before edits/commits; there is no partial implementation to rescue. Phase 4/5 refs point only at phase 1 and have no new implementation.

## Remaining phase 2 (MCP / skill)

First re-review Task 3's fix f8ce7599..8c266272. Then implement Task 4 agent verbs, 5a tasks-only MCP server, 5b command switch, 6 profile/untrusted-content/concurrency proofs, 7 summary tasks section, 8 monoagent-tasks skill, 9 reference/security/spec/changelog docs and 10 final verification.

Carry-forwards: Task 6 decoder mutation row names an obsolete loop; use the current collect-unknown implementation. Task 9 must change deleted-profile claim refusal in spec 4.2 from not_found to invalid_input. Task 4 mutating schemas need the property-type walk too. Deferred minor findings/unbounded output are preserved in the archived ledger and tracked for #364.

## Remaining phase 3 (desktop)

First scoped re-review Task 4a fix 3173256f..35c5b6c4: findings I-1 through I-4 (cross-column no-op, focus clamp, operation order, default move placement/status) plus the accepted same-column/no-place no-op guard.

Before Task 4b, combine both saved lines in the GUI worktree:

```sh
git fetch origin
git switch feat/tasks-board-gui
git merge --ff-only origin/feat/tasks-board-gui-model
git merge --no-edit origin/feat/tasks-board-gui-service-tests
```

The two lines start at 847971bb and touch different files; no merge was performed in this handoff. Inspect and test the combined result before continuing.

Then Tasks 4b search/changes/labels, 5 live/optimistic board hook, 6 English/Spanish strings, 7a card/style, 7b column/quick add, 8 drag/keyboard board, 9 drawer, 10a motion, 10b activity/capture help, 10c page, 11 sidebar/badge/registration, 12 docs/verification and 13 mocked-binding frontend screenshots.

Task 5's mutate path must preserve the service-never-rejects contract. Read-only agent-shell mode disables quick add and the comment box as well. Run focused Go/Wails/frontend checks and the final quiet-machine phase verification; do not treat mocked screenshots as real desktop validation.

## Existing backlog

- #360 phase 4 Chrome capture/outbox/profile controls (not built).
- #361 phase 5 macOS Services capture (not built).
- #362 existing skills installed as unloadable flat files.
- #363 baseline macOS test failures/flakes.
- #364 deferred review findings (phase 2/3 carry-forwards now saved in this archive).
- #365 real-machine/manual checks. PR #359 CI is currently green; actual local-data/desktop/Chrome/Services checks still remain.

## Verification at this handoff

Task 4a model: Vitest 19/19 passed. Task 3 service behavior: Vitest 18/18 passed. MCP: `GOMAXPROCS=2 go test ./internal/mcp -run Task -count=1` passed. `git diff --check` is clean. Existing session test results remain historical; no full-suite or release-ready claim is made. Initial attempts with node --test on a Vitest file and a Go filter in the wrong package were corrected; only the proper runner/package results count.

## Portable records

Plans/spec are already tracked in docs/mastermind. Archived phase 2/3 ledgers, pending-review reports and global constraints are under `docs/handoffs/2026-10-07-tasks-records/`; load only the record needed for the next task. Local `.superpowers` directories and Claude memory/scratch paths are unnecessary on the other machine. No raw chat logs, private profile data or credentials are included.
