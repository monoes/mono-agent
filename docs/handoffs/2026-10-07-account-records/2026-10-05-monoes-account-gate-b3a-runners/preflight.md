> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Pre-flight of plan B3a (runners and serving processes), 9 tasks, scanned 2026-10-07

Plan: `feat+monoes-account-gate/docs/mastermind/plans/2026-10-05-monoes-account-gate-b3a-runners.md` (2554 lines, mtime 06:39, clean in git; "plan N" below is a line of it).
Code: `feat/account-core` tip 02592963 (B1a tip dbe36f97 plus the two CI-only commits 665eca43 and 02592963, which touch `.github/workflows/ci.yml` and two `internal/account/*_test.go` files; no API change). Master part is f4441a2a.
Docs: index sections 2 to 4 and the spec sections the plan cites, read at about 07:00 and re-diffed against the docs head 8354c255 (07:56), which carries the rulings R8, R9 and R10 that came after the ledger's R1-R7 (only R9 touches this plan, see finding 9).

## 0. Result

No task is blocked: every task builds and passes as written. I replayed the plan in a scratch export (section 9): all new code and tests of Tasks 1 to 7 and 3b compile, `gofmt`, `go vet ./...` and both tag builds are silent, every new test passes with `-race`, all 36 mutation checks of the plan (16 in Tasks 1 to 3b, 20 in Tasks 4 to 7) fail with the texts the plan states, the whole `internal/workflow` package passes, `internal/monomind` fails only on `TestFindAll_ListsShadowedCopies`, and the whole `cmd/monoagentcli` suite fails only on the four known tests of index section 4. The findings are about tests that do not pin what they claim (finding 1), an ordering the plan does not say (finding 2), the findings file of Task 8 (finding 3) and stale text (findings 4 to 10).

## 1. Task table (9 rows)

| Task | Do its own parts agree? | Result |
|---|---|---|
| 1 engine entry points | Tests (plan 80-300) against code (312-353): the 8 gate modes pass; the retry test needs `account.IsLoginRequired` in `isNonRetryable` (351-353) and it is there. Files: `git add` (365) lists the 3 files that change. Commands: step 3's failures (307) are what the mutations of step 9 (373-380) produce, texts included; step 7's `ok` ran in 25 s (plan says about 15 s). | agrees. Cosmetic: plan 64 says `isNonRetryable` is lines 70-82, it ends at 83. Plan 76 sends the reader to `guard.go` for the no-guard branch of `Require`, which is `process.go:57-76` (finding 4). |
| 2 triggers, resume loop | Tests (397-503) against code (513-614): `TestResumeTickGate` counts 2 list calls and 1 resume as admitted; that is what `adoptQueuedExecutions` (engine.go:340) plus `ListResumableExecutions` do. Log test: first line `"dropped":1`, second `"dropped":3` as the comment says. Mutations (628-636) give the stated texts. | agrees |
| 3 cancel on refusal | Tests (656-818) against code (829-916): `RunningIDs` is `cancelFuncs.Range`, and `cancelFuncs` is stored before `slot.Acquire` (queue.go:162), so a run queued behind the limit is listed; `CancelExecution` flips its QUEUED row (engine.go:1036). 60 runs alone and 60 under 12 busy loops, both `-race`: no failure. Mutations (935-938) give the stated texts (the two 10 s waits included). | agrees. Interplay with Task 3b: section 2, finding 2. |
| 3b per-node check | Tests (957-1197) against code (1214-1259): `RunExecution` returns `nodeRefusedError`, `handleExecution` records it through its default case (engine.go:682-684); the stub store leaves `execNode.ID` empty so `SetExecutionNodeFinished` marks n1 SUCCESS, which is what the test reads. Failure lines `execution_account_test.go:119` and `:228` (1206, 1208) are the real lines. Mutations (1278-1280) give the stated texts, the blank-line dependence of the third included. | agrees |
| 4 Exec and executeDef | Tests (1298-1461) against code (1474-1562): `TestExecGate` uses `writeInlineFakeBin` (exec_test.go:655) and `TestExecuteDefGate` uses `newPkgExecutor` (core_pkg_fakes_test.go:122), both as cited. Step 5: `go vet ./internal/secrets/ ./internal/monomind/ ./cmd/monoagentcli/` is silent and `internal/monomind` fails only on `TestFindAll_ListsShadowedCopies`. Mutations (1581-1585) give the stated texts. The premise of the hook (plan 44) is reproduced: a blank import of `internal/account` in `internal/monomind` stops `go vet ./internal/secrets/` with `import cycle not allowed in test` through `blob_test.go:8` and `repository.go:11`. | agrees |
| 5 serving commands | Tests (1604-1685) against code (1695-1747): 5 PreRun wirings pass, `org serve` fires only for `--foreground` without `--stop`. Mutations (1773) turn each wiring off and are caught. The test list does not pin the one real action: deleting `g.StartRefresher(ctx)` (plan 1720) passes all three tests (finding 1). | agrees, with a weak test |
| 6 heartbeat, auto re-validation | Tests (1791-1905) against code (1915-1992): JSON keys and order of `AccountState` are the ones the test spells out (`omitzero`, `omitempty`); `daemonhb.Read` returns a value and a bool as the test uses it. Mutations (2009-2013) give the stated texts. | agrees |
| 7 daemon supervisor | Tests (2032-2253) against code (2262-2437): the supervisor tests pass 40 times under 12 busy loops with `-race`; `Watcher.Stop` waits for its goroutine (`internal/orgdesign/watch.go:118`), so `wait` covers the watchers; `Receiver.Run` unsubscribes on exit, so a second generation can start. Mutations (2453-2460) give the stated texts. Existing `TestDaemonWatchersFollowProfiles` passes. | agrees |
| 8 survey and verification | Step 1 greps print what the plan says (nothing, nothing, nothing, 16 files). The symbol table: `monoagentcli` links `monomind.Exec` 23 times (the plan's "23"), `executeDef` 2, `handleExecution` 1; `inspect`, `schemagen`, `debug_registry`, `internal/schemagen/cmd/gen`, `scripts/libraryofficial` and the desktop's Go side (built with a stub `frontend/dist`) link none of the three. Step 4: `gofmt -l .`, `go build ./...`, `go vet ./...`, `-tags nosocial` and `-tags devaccount` builds are silent. Step 5 and 6 as in section 0. Step 7: the 11 test files the plan lists are exactly the 11 files created; no existing test was edited. The findings text (2493-2503) is accurate. | agrees. Caveats: the findings file (finding 3), step 1's loop counts only `Exec` while step 2 asserts three symbols, `TestAgentTestGoDeadline` is not in `cmd/monoagentcli` (finding 5). |

## 2. How Task 3b fits (the part the dispatch asked for)

- **Task 1.** The check sits after the resume skip (`execution.go:125`) and the cancellation select (129-134), before the disabled, merge-wait and no-input branches (136 onward). `handleExecution`'s switch (engine.go:665-685) does not match `ErrExecutionPaused`, `PartialFailureError` or `ErrExecutionCancelled` for it, so it lands in `default` and is stored FAILED, as plan 951 says. The text differs from Task 1's own gate on purpose: Task 1 stores `"login_required: "+lrErr.Error()` (all lines, engine.go gate at plan 322), Task 3b stores the first line only (index 3.4 item 9). A refused run therefore reads differently by where it was refused; the literal `login_required: ` is written in three places (plan 322, 912, 1247).
- **Task 3.** The cancellation check runs first, so a cancel that has already reached the run's context still ends it CANCELLED. A refusal that reaches the guard between two nodes before `CancelRunning` has cancelled the context ends the run FAILED at the next node instead (probe in section 9: `FAILED login_required: Log in to monoes.me first: monoagentcli account login`, no reason stored). Task 3's tests only cover a node in flight (one slow node), where the node's own ctx error wins: 120 runs, none differs. Neither plan nor spec says "CANCELLED or FAILED" for a refusal that lands between nodes (finding 2).
- **Task 4.** A refused `Exec` or `executeDef` returns inside a node: `isNonRetryable` (Task 1) stops the retry, then `on_error=continue/skip/error_branch` could let the run go on past it; Task 3b's check at the next node is what ends it, so R4 closes that hole too. The node in flight is never interrupted, which is also all Task 4's gates can do (they refuse at start).
- **Task 7.** At `expired` the supervisor stops the org services and cancels nothing (daemon_account.go, plan 2125 and 2350-2352); the engine ends each run at its next node. At `refused` both the engine handler and the supervisor cancel, idempotently. Consistent.
- **Task 8.** The findings text says the node check is reached only through `handleExecution`. True: `RunExecution` has one non-test caller, `runExecution` (engine.go:1191), whose one non-test caller is `handleExecution` (engine.go:659); the desktop binary links none of them.
- **Spec D7, D8, 6.2, 6.4 and index 3.4 item 9 as amended.** Matched: ok and grace never stop a run (`Allowed()`, state.go:55), a locked verdict ends it FAILED at its next node with `login_required: ` and the first line, the node in flight finishes, a refusal still cancels at once.
- **Real code.** One pass of the loop is cheap but not free of I/O: `Require` goes through `Guard.Status` (guard.go:138), which stats `session.json` at most once per poll (5 s) and re-reads it when it changed (`pollIfDue`/`reload`, guard.go:210-250). The plan says "no I/O" (plan 942, 1231); index section 2 says the same lazy stat is by design. The check also runs before nodes that would only be skipped, so a run whose remaining nodes are all disabled or have no input still ends FAILED at the first of them. A node in retry backoff (up to 10 retries of up to 3600 s) is "in flight" for the whole backoff.

## 3. Pair table (17 rows)

| Pair | One produces / the other consumes | Result |
|---|---|---|
| 1 and 2 | `engine.go` edited in disjoint places (imports, handleExecution, 3 entry points / struct field, resumeLoop, ResumeExecution, handleTrigger). Task 2's tests consume `newEngineGateStore`, `engineGateWorkflow`, `eachEngineGateMode` (engine_account_test.go) and `newTriggerEngine` (engine_handletrigger_test.go:22). | agrees (compiled and ran together) |
| 1 and 3 | Task 3's queued-behind-the-limit test accepts "FAILED by the gate" (Task 1, engine.go step 0b); `refusalRun` calls `TriggerWorkflow`, which Task 1 gates, so the guard must be signed in first (it is). `CancelRunning` goes before the comment of `RetryExecution`, whose first lines Task 1 edits. | agrees |
| 2 and 3 | `Start` registers `watchAccount` after the three loops (including Task 2's `resumeLoop`); no shared lines. | agrees |
| 3 and 3b | Both end a run on a lock: ctx check first, account check second; outcomes CANCELLED or FAILED depending on the race. | agrees; ordering not stated (finding 2) |
| 1 and 3b | 3b's tests drive `handleExecution` (Task 1's gate must admit: signed in, grace, dormant) and rely on its default case; message format differs (section 2). | agrees |
| 3 and 7 | `func (e *WorkflowEngine) CancelRunning() int` (Task 3) is used as the method value `engine.CancelRunning` in `newAccountSupervisor` (daemon_account.go). | agrees |
| 3b and 7 | Supervisor at `expired` cancels nothing and relies on 3b; its comment says so. | agrees |
| 4 and 1 | The typed error of `Exec` and `executeDef` is not retried (`isNonRetryable` in errors.go, extended by Task 1). | agrees |
| 4 and 8 | `init()` gate (Task 4) and the survey (Task 8): `monoagentcli` is the only binary that links `Exec` (nm: 23/2/1 against 0/0/0 in five other mains and the desktop). Task 4 step 5 defers "the whole CLI suite runs with the gate" to Task 8 step 6: it ran, only the four known failures. | agrees |
| 5 and 6 and 7 | `cmd/monoagentcli/daemon.go` is edited by three tasks: `return servingCommand(c)` (line 194), `daemonhb.RunWith(...)` (162-163), imports, `orgs.start` (164), `msg :=` (169). Tasks 6 and 7 touch adjacent lines 162-164; they are sequential. | agrees |
| 5 and (B2) | Task 5's PreRun runs after B2's `run()` has installed the guard (plan 2553); B2's own probe test replaces `PreRun` on its copy of the tree (B2 plan 1583-1584) and does not touch Task 5's command objects. | agrees |
| 6 and 7 | Both read `account.CurrentStatus()`; `heartbeatAccount` and `daemonRunningLine` do not share code. | agrees |
| 6 and B5c, index 3.4 item 4 | `AccountState` keys `state, reason, valid_until, enforced` (heartbeat.go after plan 1921-1932) against `accountReport` in B5c plan 732-737 (`valid_until` read as a string, `enforced` as `*bool`). | agrees |
| 7 and existing code | `goTracked` replaces four `go` statements in `daemon_org.go:101,102,115,125` and the goroutine of `watchOrgFiles` (daemon_org_watch.go:32-44); `TestDaemonWatchersFollowProfiles` still passes. | agrees |
| 2 and B3b | Both declared `lockedLogEvery` in package `workflow`; B3b's ruling R9 (commit 4e81b3d5) renamed its constant to `webhookLockedLogEvery`. Scan of every other plan for the identifiers and test names of B3a: no other clash, no file name clash. | agrees now; keep the name |
| 8 and the docs branch | Task 8 appends to `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md`, which exists on the docs branch since c9d4f646 under another title and is absent from this branch. | disagrees (finding 3) |
| all and Task 8 step 7 | 11 new test files listed (plan 2542), 11 created. | agrees |

## 4. Contract drift (every consumed identifier; 32 rows)

Real code is `feat/account-core` tip. "behaviour" means a check of what the plan relies on, not only the name.

| Identifier | Plan | Real | Result |
|---|---|---|---|
| `account.Require(ctx) error` | 68, 320, 332, 576, 597, 607, 1233, 1542, 1561 | process.go:57; no guard: 58-62 and `requireNoGuard` 68-76 | match; nil while dormant, strict needs a date (plan 43a) |
| `account.IsLoginRequired(err)` | 68, 274, 352, 498 | errors.go:38 (`errors.As`) | match |
| `account.LoginRequiredError{Status}` and `Error()` | 68, 950, 1190 | errors.go:14, 16 (first line, then the reason line for expired, refused, clock, key, invalid, unconfirmed) | match |
| `account.LoginRequiredMessage` | 950, 1083, 1187 | errors.go:10 | match |
| `account.StrictForTest(t)` | 68 | testhooks.go:69 | match |
| `account.SetEnforceFromForTest(t, at)` | 68 | testhooks.go:50 | match |
| `account.InstallForTest(t, g)`, nil installs none and is restored | 68 | process.go:31 (sets `testStateEnv`, so a test that calls `t.Parallel()` fails; none does) | match |
| `account.Current()` | 649 | process.go:22 | match |
| `(*Guard).OnRefused(fn)`: appends, once per refusal on its own goroutine, from `Refresh` and from a refusal another process wrote, at once when already refused | 43b, 649, 652 | guard.go:174-190 and `note` 194-205; `Refresh` reaches it through `g.Status()` in `applyRefusal` (guard_refresh.go) | match |
| `(*Guard).Refresh(ctx)` | 649, 696 | guard_refresh.go:67; due when under `RefreshMargin` (5 min), so the plan's 56-minute step on a 1-hour token is due | match |
| `(*Guard).StartRefresher(ctx)`, idempotent, no-op while dormant | 1599 | guard_loop.go:22 | match |
| `account.CurrentStatus()` | 649, 1786, 2027 | process.go:82 (nil-safe) | match |
| `(Status).Allowed()`, fields `State, Reason, ValidUntil, EnforceFrom, Enforced` | 649, 1786 | state.go:55, 40-51 | match |
| `StateOK/Grace/Locked`, `ReasonExpired/Refused/KeyUnknown/NotLoggedIn` | 649, 2027 | state.go:9-11, 19-24 | match |
| `account.PollInterval` | 2027 | claims.go:21 (5 s) | match |
| `account.OpenStore(dir, sealer)`, `NewMemorySealer()` | 649, 684 | store.go:183, sealer.go:108 | match |
| `account.NewGuard(GuardOptions{Store, Refresher, Now})` | 649, 692 | guard.go:113, 55 | match |
| `account.Refresher`, `RefusedError{Description}`, `TokenSet` | 649, 672-676 | guard.go:48, errors.go:67, guard.go:15 | match; the fake returns a nil `*TokenSet` with its error as the interface demands |
| `account.Session{V, Host, AccessToken, User}`, `User{ID}`, `HostURL` | 686 | session.go:13, state.go:33, claims.go:11 | match |
| `accounttest.Install(t, m) *Guard` and the five modes | 68 | install.go:35, 14-18 (strict, installs, restores) | match |
| `accounttest.InstallWithFixture(t, m) (*Guard, *Fixture)` | 950 | install.go:44 | match |
| `accounttest.New(t)`, `(*Fixture).Token(TokenOptions{Sub})` | 649, 683 | fixture.go:39, 53 | match |
| `Fixture.Clock.Advance(d)`; the guard runs on `Clock.Now` | 950, 1068 | clock.go:45; install.go (`GuardOptions.Now: f.Clock.Now`) | match |
| one fixture per test, no `t.Parallel()` | not stated | install.go doc | Task 6's `TestDaemonHeartbeatRefreshCarriesTheAccount` builds two fixtures (LockedNoLogin, then Dormant) in one test; harmless, no token is verified (cosmetic) |
| `Require` "reads the cached verdict, no I/O" | 942, 1231 | guard.go:138-150, 210-250: a stat of `session.json` at most every 5 s, a read when it changed | differs in wording (cosmetic) |
| `internal/secrets` | 44 | `account` imports it (sealer.go); secrets' tests close the cycle through storage | match, reproduced |
| `stubStore`, `nodeRecord`, `createdExecs`, `mu` | 950, 138-168 | execution_test.go:21-65 | match |
| `newFullEngineStore`, `createActiveManualWorkflow`, `waitForTerminalStatus`, `slowNode` | 700-734 | engine_test.go:54, 88, 113, 134 | match |
| `fakeScheduler`, `countingExecutor`, `noWait` | 709, 289, 291 | trigger_manager_test.go:15, retry_deterministic_test.go:12, 33 | match |
| `executeWithRetry(ctx, ex, in, cfg, policy)` | 291 | execution.go:625 (5 parameters, 2 results) | match |
| `storage.NewDatabase`, `ApplyMigrations`, `workflow.NewSQLiteWorkflowStore`, `daemonhb.Write/Read/Path/RunWith` | 1886-1891, 2227-2235, 1807-1825 | database.go:29, 66; storage.go:132; heartbeat.go:80, 101, 68, 131 | match |
| cobra `PreRun` / `PreRunE` precedence, `Find` | 1636-1644 | no serving command defines either (grep); root has `PersistentPreRun` only (root.go:45) | match |

## 5. Real-code references (every one the plan cites for the tasks; 28 rows)

| File | Cited lines | Result |
|---|---|---|
| `internal/workflow/engine.go` | 14-16 (imports), 21-38 (struct), 245-251 (Start), 307-328 (resumeLoop), 386-387 (ResumeExecution), 523-524 (handleTrigger), 606-611 (cancelled check), 679-681 (cancelled case), 946-949, 979-980, 1051-1053 | all exist as described |
| `internal/workflow/errors.go` | 3-8 (imports), 70-82 (`isNonRetryable`) | imports right; the function is 77-83 |
| `internal/workflow/queue.go` | 254 (`Cancel` comment) | right (RunningIDs goes above it) |
| `internal/workflow/execution.go` | 35 (`RunExecution`), 129-134 (cancellation check) | right (`for` at 121, resume skip at 125, next comment at 136) |
| `internal/workflow/engine_handletrigger_test.go` | 22 | right |
| `internal/workflow/engine_test.go` | 79 | right |
| `internal/monomind/exec.go` | 317-321 | doc comment 312-319, `Exec` at 320, first statement 321: right |
| `internal/monomind/exec_test.go` | 655 (`writeInlineFakeBin`); "16 calls" | right; 16 calls of `Exec(context.Background()` (10 with `ExecOptions{Bin: bin`) |
| `internal/action/executor.go` | 3-15 (imports), 535 (`executeDef`) | imports are 3-17 (`internal/browser` at 14); 535 right |
| `internal/action/core_pkg_fakes_test.go` | 122; "21 builds" | right; 21 |
| `internal/secrets/blob_test.go`, `internal/storage/repository.go` | 8, 11 | right |
| `internal/openaiapi/gateway.go` | 301 | the `Shutdown` declaration; the quoted sentence is lines 299-300 |
| `cmd/monoagentcli/daemon.go` | 16, 162-163, 164, 169, 194, 266 | all right |
| `cmd/monoagentcli/httpapi.go`, `mcp.go`, `extension_serve.go` | 130, 119, 130 | right |
| `cmd/monoagentcli/org_process.go` | 28-52, 65 | 65 right; the launcher spans 28-60 |
| `internal/daemonhb/heartbeat.go` | 47, 48 | right |
| `cmd/monoagentcli/agent_auto_revalidate.go` | 54, 110 | right |
| `cmd/monoagentcli/daemon_org.go` | 3-23, 54-67, 101, 102, 115, 125 | right |
| `cmd/monoagentcli/daemon_org_watch.go` | 32-44 | right |
| `internal/agentroster/validate.go`, `internal/monomind/agenttest.go` | 180, 62 | right (validate.go:177-179 falls back to `Exec` with `ErrUseExec` for sandboxed runtimes) |
| `internal/monomind/profile_init.go`, `cmd/monoagentcli/doctor_env.go` | 129, 115 | right (`registerClaudeCodeProject` at 109, called from `InitProfile`) |
| `internal/capturesummary/runner.go`, `summarizer.go` | 50, 218 | 50 right; 218 is the `writeStatus` inside `fail`, the status is set at 217 |
| Task 8 step 1 greps | 16 file list, three empty greps | identical output at the tip |
| Existing test names | `TestMCPCommandHandsTheOperatorsSwitchesToTheServer`, `TestMCPCommandHandsAPIOnlyToTheServer`, `TestDaemonRestartJSON`, `TestExtensionServe_BindsAndReportsWhereItIs`, `TestDaemonWatchersFollowProfiles`, `TestEngine_AdoptsUnownedQueuedExecution` | all exist |
| Known failing tests (plan 2536, index section 4) | four in `cmd/monoagentcli`, `TestFindAll_ListsShadowedCopies` in `internal/monomind` | all exist and are the only failures; `TestAgentTestGoDeadline` is in `internal/monomind/agenttest_test.go:83`, not in `cmd/monoagentcli` (finding 5) |
| Spec and index sections | spec 6.2, 6.4, D7, D8, D17, D22, A1, A4, A15, A16, A17; index 3.2, 3.3, 3.4 items 4 and 9 | all exist and say what the plan quotes |
| Binaries | "`monoagentcli` is the only binary that links `Exec`" (plan 44) | verified by `go tool nm` on all six main packages of the root module and on the desktop's Go side |
| `go list` imports | "`cmd/schemagen` reaches `internal/workflow` through `internal/tools/schemagen`" (plan 2500) | right |

## 6. Plan-mandated defects (8 rows)

| # | Where | What a review would say |
|---|---|---|
| a | Task 5, plan 1678-1684 and 1712-1721 | `TestStartServingGuardWithNoGuardDoesNothing` asserts nothing (it can only panic), and no test shows that the real `startServingGuard` starts the installed guard's refresher: deleting `g.StartRefresher(ctx)` (plan 1720) passes all three Task 5 tests (replayed). Step 8's mutation list (1773) has no row for it. |
| b | Task 1, plan 332-342 | The same three-line gate is written twice, in `TriggerWorkflow` and `TriggerWorkflowPersistOnly`, which share `newManualExecution` (engine.go:994, whose comment says so); distinct variable names exist only for the mutation recipe. The literal `login_required: ` is written three times (plan 322, 912, 1247) with two formats. |
| c | Tasks 2, 7, 3 tests | Errors swallowed: `resumeTick` returns on a refusal with no log line (plan 576-578); the stop path of `apply` drops `waitOrg`'s answer (plan 2381), so a stop that outlasts 30 s is only reported at the next start attempt; `refusalGuard` drops both results of `g.Refresh` (plan 696), so a failed refresh would show up as a 10-second timeout. |
| d | Task 3, plan 877-881 | `watchAccount` registers an `OnRefused` callback that cannot be removed (B1a has no unregister), so a stopped engine stays reachable from the guard. Harmless in a process with one engine. |
| e | Tasks 3, 3b, 6 tests | Literal durations (56 minutes at plan 695, 25 hours at 1068, 1131, 1171, 2 hours in the table) depend on `RefreshMargin`, `GraceWindow` and the fixture's 1-hour token; `claims.go` says S6 may change its constants and that tests stepping a clock use literals. Same practice as B1a's tests. |
| f | Task 6 test | Two `accounttest` fixtures in one test function (install.go says one per test); harmless here. |
| g | Task 3b, plan 1278-1280 | The third mutation only matches when the inserted check is followed by a blank line (`}\n\n`); my replay kept the blank line, an implementer who does not gets "mutation did not change the file". |
| h | Files over 500 lines | New files are all under 500 (largest: `execution_account_test.go` 239). Four existing files are already over and grow: `engine.go` 1199 to 1263, `execution.go` 937 to 943, `monomind/exec.go` 756 to 763, `action/executor.go` 1476 to 1482 (new logic went into new files where it could). |

## 7. Constraint check

Index section 2 (15 bullets; the plan copies 12 of them, see the last row):

| Bullet | Result |
|---|---|
| Go 1.26, no new dependency (D13) | ok: `go.mod` untouched, build and vet clean |
| Offline grace, token lifetime, clock guard | not touched; tests use literals (6e) |
| States, `invalid_grant` only is a refusal (D27) | ok: `refusalCancels` is `locked(refused)` only; `unconfirmed` is a grace reason that ends as a lock and then ends runs through 3b |
| Refresh (4.4): serving processes refresh at once | ok: Task 5 (weak test, 6a); non-serving 5 minutes is B2's |
| Dormant (D22) | ok, pinned by dormant and warn-period rows in Tasks 1, 2, 3 (`refused before the enforcement date`), 3b, 4, 6, 7. One deliberate exception: `monomind.ErrNoAccountGate` refuses in a non-test process that never installed the gate, even while dormant (plan 44, 1284). It holds because only `monoagentcli` links `Exec` (nm: 23 symbols there, 0 in five other mains and in the desktop's Go side, which is also built with these changes). A future binary that links `Exec` fails closed. |
| Nothing on disk until a write | ok: B3a writes nothing new; the heartbeat file already existed and gets its key only when not dormant; `Status` only stats |
| Process globals, `*ForTest` without `t.Parallel()` | ok: no `t.Parallel()` in any new test; `inTestBinary` is a package variable replaced in one test, which the plan runs with `-race` (passes) |
| Gated command exits 4, first line | B2's; B3a returns `*LoginRequiredError` unchanged |
| `devaccount`, seams panic outside tests | ok; the plan's copy of this bullet (plan 25) is older than index section 2 (commit 5f1d682c, 07:51: `MONOES_BASE_URL` never reaches the machine session, `library logout` leaves it alone) |
| No token, refresh token or key in output | ok: throwaway keys; the only secret-looking literal is the in-memory `"refresh-1"`; no log line carries one |
| Files under 500 lines, conventional commits | ok for new files (6h); the nine commit subjects are `type(scope): subject` |
| Only B5b edits the docs list | ok: B3a edits none of them; Task 8 appends to the spike-findings document, which is not on the list |
| Bullets the plan's copy omits | Storage (4.6), Open commands (D6) and Serving commands (spec 6.4) are not copied into plan 15-28 (the serving classes are explained in the Decisions list, plan 51); the `devaccount` bullet is the old text |

Rulings: R1, R2, R3, R5, R6, R7 do not touch B3a. R4 is Task 3b (section 2). R8 (the bridge keeps captures and runs nothing on them) leaves plan 52(c) and plan 2503 true, since they speak of a summary that was queued before the lock; spec A17 now says more. R9 renamed B3b's constant so `lockedLogEvery` stays B3a's. R10 does not touch B3a.

## 8. Findings, ranked

1. DRIFT, plan fix. Task 5's real behaviour is unpinned: `TestStartServingGuardWithNoGuardDoesNothing` (plan 1678-1684) asserts nothing and deleting `g.StartRefresher(ctx)` (plan 1720) passes every Task 5 test and is not in step 8's mutation list (1773). Add one test and a mutation row for it. The test has to be built so that it cannot pass for nothing, because `StartRefresher` is a no-op while the gate is dormant (guard_loop.go:22-25): the enforcement date must have passed (`accounttest.New` sets one a day before its clock), the guard needs a counting `Refresher` (as `refusalGuard` in Task 3 builds one) and a session that is due in background mode (past half the token's life, or not ok), its cleanup must cancel the context it passed and call `g.Close()`, and it asserts that the fake `Refresher` was called. A test that only installs a guard over a store passes with or without the call.
2. DRIFT, document or rule. Task 3 and Task 3b can both end a run on a refusal; whichever reaches the run first decides: CANCELLED with `login_required: monoes.me refused this account: ...` (Task 3) or FAILED with `login_required: Log in to monoes.me first: monoagentcli account login`, with the reason `refused` not stored (Task 3b; replayed with a refusal installed while n1 runs). Task 3's tests and B5c's and B5a's "the run ends CANCELLED" only hold for a run whose node is in flight. Say so in the plan (one sentence in Task 3b and in Review Focus 5), or make `requireAccountForNode` return the cancellation error for `locked(refused)`.
3. DRIFT, plan fix. Task 8 step 2 appends to a findings file that now exists on the docs branch (c9d4f646, "# Mandatory monoes.me Account — Spike Findings (S1, S2, S3, S6, S7)", written by plan A Task 2, which also says later plans append their own sections) while the plan says "Plan A's first task creates it" and offers `# Spike findings` as the fallback title (plan 2467); B2 and B5a offer two other titles. In this branch the file will not exist, so the fallback creates a third version and the merge conflicts. Have the lead apply the S4 section on the docs branch (or fix one title). Step 1's loop counts only `Exec`; the other two symbols the findings text asserts are 2 and 1 in `monoagentcli` and 0 elsewhere (checked, all six mains and the desktop).
4. DRIFT, small. Plan 76: the no-guard branch of `Require` is `internal/account/process.go:57-76`, not `guard.go` (`guard.go:153` is the method).
5. DRIFT, small. `TestAgentTestGoDeadline` is `internal/monomind/agenttest_test.go:83`, not a `cmd/monoagentcli` test (index section 4 line 412 and plan 2536 say so). It can show up in the `internal/monomind` runs of Task 4 step 5 and Task 8 step 5, and not in the CLI suite.
6. COSMETIC, name it in the S4 text. A fourth path creates execution rows without Task 1's gates: `workflow.CreateUnownedExecution` (`internal/workflow/org_unification.go:111`), called from `internal/mcp/grant.go:304` (the B3b MCP door refuses `tools/call` while locked) and `internal/orgbridge/receiver.go:382` (the receiver loop is an org service that Task 7 stops within 5 s; its HTTP route is behind B3b's door). A row created in that window waits QUEUED and runs after the next sign-in (Task 2 holds adoption).
7. COSMETIC. A resumed run that was flipped to QUEUED just before the lock and is still waiting in the in-memory queue fails for good at Task 1's gate when it is dequeued; Review Focus 2 protects the flip only.
8. COSMETIC, stale text. Plan 942 and 1231 ("no I/O": one stat every 5 s, a read on change); plan 1591 ("the index says in their own RunE": index 3.5 and spec A17 say PreRun); plan 2017 ("Tasks 1 to 3" should say 3b); plan 15-28 (copy of index section 2, see section 7); line ranges in section 5 (errors.go 70-82, executor.go 3-15, org_process.go 28-52, gateway.go:301, summarizer.go:218).
9. COSMETIC. B3b's R9 already avoids the clash on `lockedLogEvery`; keep that name in Task 2 or tell B3b.
10. COSMETIC, behaviour notes for the implementer: the node check also fires before nodes that would be skipped, and a node in retry backoff is "in flight" for its whole backoff (section 2); the engine's stored text for a refused start is multi-line and for a refused node one line (section 2).

## 9. How this was checked

Nothing in a repository was changed. Scratch work lives in the session scratchpad under `pf-b3a/` (`tree/` is `git archive HEAD` of `feat/account-core`; the scripts `ext.sh`, `patch.py`, `mut_wf.sh`, `mut_cmd.sh`, `clash.sh`, `nm.sh` and the outputs are beside it). Not durable: copy what you need.

- Extraction: the new files were cut from the plan between code fences (fence lines 80-300, 397-503, 513-554, 656-818, 870-916, 957-1197, 1214-1251, 1298-1386, 1388-1433, 1435-1461, 1474-1522, 1548-1562, 1604-1685, 1695-1747, 1791-1833, 1835-1905, 1937-1977, 2032-2253, 2300-2426), the edits of existing files were applied by exact-match replacement (each matched once).
- Build: `gofmt -l .`, `go build ./...`, `go vet ./...`, `go build -tags nosocial` and `-tags devaccount -o /dev/null ./cmd/monoagentcli`: no output.
- Tests: every new test of Tasks 1 to 7 and 3b passes with `-race`; the new CLI and workflow tests also pass with `-tags nosocial`; `go test ./internal/workflow/ ./internal/daemonhb/ ./internal/action/ -race` ok (workflow 99 s); `go test ./internal/monomind/ -race`: only `TestFindAll_ListsShadowedCopies`; `go test ./cmd/monoagentcli/ -count=1`: only `TestCaptureTaskFilesOnTheBoard`, `TestCoderConversationFolders`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks` (657 s).
- Mutations: the 36 recipes of the plan, run as written (perl lines copied from the plan), each failed with the texts the plan names; one added mutation (delete `g.StartRefresher(ctx)`) passed all Task 5 tests.
- Stress: Task 3's two refusal tests 60 times, and again 60 times under 12 busy loops; Task 7's five supervisor tests 40 times under the same load; all `-race`, no failure.
- Probe (a throwaway test, removed): with a signed-in guard, installing a `LockedRefused` guard while n1 runs gives nodes `[n1]` and `"FAILED login_required: Log in to monoes.me first: monoagentcli account login"`.
- Linker: `go tool nm` on `monoagentcli`, `inspect`, `schemagen`, `debug_registry`, `internal/schemagen/cmd/gen`, `scripts/libraryofficial` and on the desktop's Go side (`go build -tags desktop,production` with a stub `frontend/dist` and `CGO_LDFLAGS="-framework UniformTypeIdentifiers"`): `Exec`/`executeDef`/`handleExecution` are 23/2/1 in `monoagentcli` and 0/0/0 in all the others; `RunExecution` is 0 in the desktop.
- Cycle: a blank import of `internal/account` in `internal/monomind/account_gate.go` makes `go vet ./internal/secrets/` and `go test ./internal/secrets/` fail with `import cycle not allowed in test` (chain secrets, storage via `blob_test.go`, monomind via `repository.go`, account, secrets via `sealer.go`).
- Names: every package-level identifier, test name and file name B3a creates, searched in the other nine plans: no clash left (R9 removed the only one).
- Not done: the red step of each task was not replayed at its own commit. I replayed the end state with all tasks applied, then the mutations. The failing behaviour the plan describes is shown by the mutations where its text is specific: Task 1 step 3 by its five mutations, Task 3b step 3 ("two fail, two pass") by the `nodeErr` mutation (only the two named tests fail; nothing after Task 3b touches `internal/workflow`), Tasks 3 and 7 by theirs. The compile-failure reds of Tasks 2, 4, 5, 6 and 7 (`undefined: ...`) were not checked.
- Cross-compile: `GOOS=windows go vet` and `GOOS=linux go vet` of `internal/workflow`, `internal/monomind`, `internal/action`, `internal/daemonhb` and `cmd/monoagentcli` (tests included) are silent.
