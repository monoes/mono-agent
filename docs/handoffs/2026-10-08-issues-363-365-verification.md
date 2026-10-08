# Issues 363 and 365: macOS verification

Checked on 2026-10-08 from master `ab14b469`. This report distinguishes completed checks from checks that still require the operator. It does not approve the design decisions on the operator's behalf.

## Issue 363

The complete default `go test ./... -count=1 -timeout 15m` passed on macOS. The remaining reported tests also passed ten normal repeats and three race repeats, concurrently with the full suite:

- CLI: capture artifacts, shared coder root, coder conversation folders, and every `TestWorkflowCancel*` case.
- capturetask: every artifact and the real-path/symlink case.
- config: missing monomind fails fast without discovering a real system installation.
- monomind: shadowed copies and cancellation while org startup is in flight.

The fixes are already in master. No tests were skipped or changed for this verification. A later broad verification attempt exhausted temporary disk space during concurrent linking; it was stopped and does not count as a test pass. The first complete macOS run and scoped repeat/race runs are the evidence above.

## Issue 365

Completed:

- PR #359 is merged. Both reported Linux `Build & test (default)` and `Build & test (nosocial)` runs are green, including the final handoff revision. Other listed checks, including Wails and Chrome extension, are green.
- `062_tasks.sql` is the only migration numbered 062 on fetched master. Running the built branch's CLI against the actual local database applied migrations 062–064 successfully, and the active profile's task list could be read.
- A harmless, idempotent verification task was added to the actual active profile's Inbox. `task show` reads its event correctly, and `task next` returns no task while it remains unapproved. Approval and `task os install` correctly refused the agent session with exit 3 / `operator_only`. No environment marker was removed to bypass the operator gate. The operator was asked to approve that verification task in their own terminal; claiming and finishing it remain pending that approval.
- The Wails app builds with the completed frontend on this Mac, and full Wails module tests pass on dormant and disposable past-date trees. The actual app and bundled CLI were signed with the hardened runtime and verified. The built frontend's full suite passes with two workers: 167 files, 1,795 tests. An earlier simultaneous-build run hit three five-second timeouts; the limited-worker rerun passed all three and the rest of the suite.
- The five task-focused extension test files pass (including title fallback, outbox, context menus, wiring and side panel).

Still requires the operator / real UI:

- Approve the smoke task, then verify `next`, `claim`, and `finish` on that approved task; archive it afterwards if desired.
- Launch the built desktop app and assess the board, drag/drop, keyboard map, drawer, badge, and Spanish rendering. A build and render tests are not visual acceptance.
- Load the current extension in the user's Chrome and check actual capture behavior, including a page without a title. Mocked Chrome tests cannot establish real `tab.title` behavior.
- Run `task os install` from the installed CLI in the operator's own terminal and confirm the Services menu item appears, enabling it in System Settings if needed. The actual Services folder currently has no task menus; the agent did not install one.
- Have a Spanish speaker accept the wording. Current master already differs from the older examples in the issue; automated locale parity is not human language review.
- Accept or amend spec decisions D5–D32 and the additional phase-1 rulings. A question about the principal defaults was presented to the operator; no answer is assumed.

Native UI attempts were blocked twice by pending macOS Accessibility and Screen Recording permissions. No desktop, Chrome, or Services-menu interaction is claimed. The project instructions explicitly forbid an agent bypassing the operator-only guard, so these remaining items are not closed by this report.
