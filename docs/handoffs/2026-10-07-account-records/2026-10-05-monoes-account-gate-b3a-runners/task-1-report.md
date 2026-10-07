> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# B3a Task 1 report: the engine's entry points refuse while the account is locked

Worktree `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b3a`, branch `feat/account-b3a`.
Started from `02592963`, finished at `2a477432`. Model: Claude Sonnet 5.5.

## Status: DONE

## What I implemented
Exactly the brief's code, nothing more.

- `internal/workflow/engine.go` (+21 / -2 lines):
  - import `internal/account`, sorted before `connections`;
  - `handleExecution`: step "0b", right after the cancelled-before-dispatch check and before the `persistCtx` closure. `account.Require(ctx)` refused: `log.Warn`, `persistExecutionFinished(log, id, "FAILED", "login_required: "+lrErr.Error())`, return. The workflow is not loaded;
  - `TriggerWorkflow` (doc comment extended), `TriggerWorkflowPersistOnly`, `RetryExecution`: `account.Require(ctx)` as the first statement (`trigErr`, `persistErr`, `retryErr`), the typed error is returned, no row is created.
- `internal/workflow/errors.go` (+5 / -1): `internal/account` import as its own group after the standard library; `isNonRetryable` gets `account.IsLoginRequired(err) ||` before `errors.As(err, &pe)`; its doc comment names the locked account.
- `internal/workflow/engine_account_test.go` (new, 219 lines): `TestHandleExecutionGate`, `TestTriggerEntryPointsGate`, `TestExecuteWithRetry_LoginRequiredNotRetried`, plus the `engineGateStore` recorder and the eight-row mode table. Copied from the brief with the markdown list indentation removed.

No new exported API. No change to `internal/account`.

## Step 1 (B1a reads as assumed)
- `go build ./internal/account/... ./internal/account/accounttest/`: no output.
- `internal/account/process.go:57-76` `Require` / `requireNoGuard`: with no guard, `isTest && !strictNow` returns nil; otherwise `noGuardStatus(now)` = `judge(nil,nil,nil,now)`, which sets `Enforced` from `Enforced(now, hw)`. `rollout.go:21-33`: the zero date gives `Enforced == false`, a future date gives false until reached. `state.go:55`: `Allowed() = !Enforced || ok || grace`. So dormant and warn-period rows pass and a passed date refuses: D22 holds in B1a.
- `testhooks.go`: `StrictForTest`, `SetEnforceFromForTest`, `InstallForTest` (nil installs none, previous restored on cleanup) as the brief says. `accounttest/install.go`: the five modes as the brief says.
- Import-cycle check for the new `workflow -> account` edge: `go list -test -deps ./internal/secrets/` and `./internal/account/ ./internal/account/accounttest/` contain no `internal/workflow` and print no cycle error.

## TDD evidence
RED, before the engine code (test file only):

```
go test ./internal/workflow/ -run '^(TestHandleExecutionGate|TestTriggerEntryPointsGate|TestExecuteWithRetry_LoginRequiredNotRetried)$' -count=1
```
```
--- FAIL: TestHandleExecutionGate/strict,_no_guard_installed   (and locked,_not_logged_in, locked,_refused)
    engine_account_test.go:159: refused run: "finished exec-1 FAILED workflow not found" after 1 loads, want FAILED with a login_required: message and no load
--- FAIL: TestTriggerEntryPointsGate/TriggerWorkflow/...       (the same three rows)
    engine_account_test.go:195: err = <nil>, login required = false, want true
--- FAIL: TestTriggerEntryPointsGate/TriggerWorkflowPersistOnly/...  (the same three rows)
    engine_account_test.go:195: err = <nil>, login required = false, want true
--- FAIL: TestTriggerEntryPointsGate/RetryExecution/...        (the same three rows)
    engine_account_test.go:195: err = engine: retry execution: workflow: execution not found, login required = false, want true
--- FAIL: TestExecuteWithRetry_LoginRequiredNotRetried
    engine_account_test.go:214: executed 4 times, want 1
FAIL	github.com/monoes/mono-agent/internal/workflow	4.111s
```
This is the brief's expected output, line for line: 3 refused rows per site fail, the `dormant`, `warn period`, `dormant, guard installed`, `signed in` and `grace` rows pass.

GREEN, after the engine code, same command with `-v`: `ok  github.com/monoes/mono-agent/internal/workflow  1.524s`, 38 `--- PASS`, 0 `--- FAIL` (9 + 28 + 1).

Step 7, the whole package, unchanged suites: `go test ./internal/workflow/ -count=1` gives `ok  github.com/monoes/mono-agent/internal/workflow  22.734s` (load average 40 to 60 at the time).

Also clean: `gofmt -l internal/workflow/` (no output), `go build ./internal/workflow/`, `go vet ./internal/workflow/` (no output).

## Mutation checks (after the commit; script in the scratchpad, EXIT trap restores both files)
Recipe taken from the plan's Decisions (see Concerns 1): `perl -pi -e 's/(; VAR != nil) \{/$1 && false {/' internal/workflow/engine.go`. Each mutation changed exactly one line (checked with `git diff -U0`), compiled, and was restored with `git restore` (`git status --short` empty after each).

| VAR | test | result |
|---|---|---|
| `lrErr` | `^TestHandleExecutionGate$` | FAIL: `strict, no guard installed`, `locked, not logged in`, `locked, refused` |
| `trigErr` | `^TestTriggerEntryPointsGate$/TriggerWorkflow$` | FAIL: the same three rows of that site |
| `persistErr` | `^TestTriggerEntryPointsGate$/TriggerWorkflowPersistOnly$` | FAIL: the same three rows |
| `retryErr` | `^TestTriggerEntryPointsGate$/RetryExecution$` | FAIL: the same three rows |
| retry filter (`errors.go`: `IsLoginRequired(err)` -> `IsLoginRequired(nil)`) | `^TestExecuteWithRetry_LoginRequiredNotRetried$` | FAIL: `executed 4 times, want 1` |

Afterwards step 6 passes again (`ok ... 0.801s`), `git status --short` empty, `git diff` empty, HEAD is `2a477432`.

## Files changed
`internal/workflow/engine.go`, `internal/workflow/errors.go`, `internal/workflow/engine_account_test.go` (new). `git status --short` before the commit listed only these three.
Size rule: `engine.go` was already 1199 lines and is now 1218 (the plan's lines only); `errors.go` 87; the new test 219.

## Self-review findings
- Variable names (`lrErr`, `trigErr`, `persistErr`, `retryErr`) are the ones the mutation table keys on; none shadows the later `err` in `handleExecution`.
- The gate in `TriggerWorkflow` and `TriggerWorkflowPersistOnly` runs before the workflow is loaded, so a locked account answers `login_required` even for an unknown workflow id. That is the plan's intent (no row, typed error first).
- `handleExecution` records the whole `Error()` text (a reason line after the first line for expired, refused, clock and similar locks); Task 3b records only the first line. The plan states this on purpose ("where Task 1's refused start records every line", index §3.4 item 9), so it is not a defect.
- The `dormant, strict, no guard` and `warn period, strict, no guard` rows prove D22 against the real `Require` (no stub), so a gate that skipped `Require` when `Current()` is nil would be caught by the `strict, no guard installed` row.
- No background goroutine can run `handleExecution` during a subtest (read, not assumed): `NewExecutionQueue` (`queue.go:122-129`) only builds a struct and a buffered channel; the dispatcher goroutine is created in `ExecutionQueue.Start` (`queue.go:133-152`), which only `WorkflowEngine.Start` (`engine.go:203`) and four tests in `engine_test.go` call, and `engine_account_test.go` never calls it. `Enqueue` (`queue.go:230-252`) only buffers. So the admitted `TriggerWorkflow` row leaves its request unread in the channel, every `handleExecution` call in the new tests is the test's own synchronous call, and the strict hook and installed guard of a subtest are not observed by another goroutine. This is why I did not run `-race` (not named by the brief; CI's Linux jobs run it).
- Not covered here by design (the plan lists them): trigger-created rows in `handleTrigger` (Task 2), `workflow.CreateUnownedExecution`, a run already waiting in the in-memory queue when the lock lands (it meets this gate and ends FAILED).

## Notes for the controller (none blocks DONE)
1. The brief's step 9 says "recipe in Decisions", but the brief does not contain that section. I read it in the plan (`.../feat+monoes-account-gate/docs/mastermind/plans/2026-10-05-monoes-account-gate-b3a-runners.md`, line 56) and used it as written. Worth adding the one-line recipe to the other task briefs that say the same.
2. The brief's commit command is subject plus trailer only; the lane rules ask for a why paragraph as the middle `-m`. I used the brief's exact subject, added one short why paragraph and the `Claude Sonnet 5.5` trailer.

Contract change requests: none.
