# Mandatory monoes.me Account — B3a: runners and serving processes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every execution, agent turn and browser action refuses to start while the monoes.me account is locked, a run in flight ends at its next node once the account is locked, and a locked daemon stays up, starts nothing, stops its org services, reports the state in its heartbeat and resumes by itself.

**Architecture:** Layer 2 is one account check at each choke point: `account.Require` in `handleExecution`, in the engine entry points that create an execution, before every node of a run in the engine's node loop (`RunExecution`, ruling R4) and in `ActionExecutor.executeDef`, and a gate that `cmd/monoagentcli` installs as `account.Require` for `monomind.Exec` (`internal/monomind` cannot import the account package). Holds in the engine's trigger and resume paths make a locked process start nothing and fail nothing for good. The engine registers its own cancel-on-refusal handler when it starts; serving commands start the guard's refresher at once from a pre-run hook; the daemon gets an account supervisor that stops and restarts the org services as the verdict changes.

**Tech Stack:** Go 1.26, cobra, zerolog, SQLite (the existing stores); no new dependency.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§6.2, §6.4, D7, D8, D17, D22, and the amendments A1, A4, A15, A16 and A17 of its section 13) and the index `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§3.2, §3.3, §3.4 items 4 and 9). Depends on B1a being merged: it needs `internal/account` and `internal/account/accounttest`. Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

## Global Constraints

Copied from the index §2; these bind every task.

- Go is `go 1.26.0`; `internal/account` adds no third-party dependency (D13): `crypto/ed25519` and a small strict JWS parser only. One accepted algorithm (EdDSA); the verifier ignores `jku`, `jwk` and `x5u` headers and never negotiates from the header.
- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace. A grant whose outcome is unknown (the request may have been processed, so monoes.me may have rotated the refresh token) or whose answer could not be saved (A24(d)) is retried within 240 seconds and after that is never presented again: this machine drops its refresh token and the reason is `unconfirmed` (A24, §3.6), a grace reason that ends as `locked(unconfirmed)`; the other installs of the account are untouched.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, and neither does a guard pass (`EnsureFresh`, `Refresh`, the background refresher) while the gate is dormant or its date is still ahead, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear on a login or a refresh and, from the enforcement date on (A25), on the first guard pass of a machine that has no session: that pass creates the directory, `session.lock` and a session with no token (`{v, host, hw}`, never `refresh.enc`), the clock-guard record of a machine that never signed in. So a gated command that is refused on an empty HOME leaves exactly `account/session.lock` and `account/session.json` once the date has been reached, and nothing before it. Otherwise the high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

## Review Focus

The six failure modes the spec implies that a plain refuse-or-allow test would miss, most likely first. Each is pinned by a named test in the task that owns the code.

1. **A change a user can see before the enforcement date.** Every phase before B5a ships dormant: while `account.EnforceDate()` is the zero time nothing here may refuse, cancel, stop an org service or add an `account` key to the heartbeat. Pinned by the `dormant` rows of the gate tests (Tasks 1, 2, 4), the `warn period` rows of the engine's (Tasks 1, 2: a date set and not reached refuses nothing), `TestADormantGateNeverEndsARun` (Task 3b), the `refused before the enforcement date` row of `TestOnlyARefusalCancels` (Task 3), the `dormant` third of `TestDaemonHeartbeatRefreshCarriesTheAccount` (Task 6) and `TestSupervisorLeavesTheOrgServicesAloneBeforeTheEnforcementDate` (Task 7).
2. **A locked daemon destroys paused and queued runs.** The spec routes every Human-in-Loop resume and every `--no-wait` adoption through `handleExecution`, which would record them FAILED, for good, while the account is locked. Task 2 leaves them as they are until the account is valid: `TestResumeTickGate`, `TestResumeExecutionGate`.
3. **A refusal leaves runs that wait for a concurrency slot QUEUED forever.** They have no goroutine to stop, so cancelling a context is not enough: `TestRefusalEndsRunsQueuedBehindTheLimit` (Task 3).
4. **A refused node sits in retry backoff.** A node with a retry policy would retry a gate refusal for minutes: `TestExecuteWithRetry_LoginRequiredNotRetried` (Task 1).
5. **`expired` treated like a refusal, or a person's cancel treated like one.** Only `invalid_grant` cancels; at the 24 hours nothing is cancelled, the node in flight finishes and the run ends at its next node (Task 3b), and a person's own cancel never says `login_required`: `TestOnlyARefusalCancels`, `TestRefusalCancelsWhatIsRunning` (Task 3), `TestSupervisorFollowsTheAccount` (Task 7).
6. **A run in flight outlives the gate, or a valid account stops it.** A run that has started meets the gates of Tasks 1 and 4 only at its agent turns and browser actions, so one made of plain nodes (a polling loop, a long wait) would run on for good after the 24 hours; the node loop's check ends it at its next node, and must never end one while the account is `ok` or `grace`, or interrupt the node in flight: `TestARunEndsAtItsNextNodeOnceTheAccountLocks`, `TestARunGoesOnWhileTheAccountIsOkOrInGrace`, `TestARefusedNodeEndsTheRunWithTheFirstLineOfTheRefusal` (Task 3b).

## Decisions this plan makes (read before Task 1)

- **B1a seams this plan relies on**, all stated in B1a's plan: (a) `Require` with no guard installed, under `StrictForTest`, refuses only once an enforcement date has passed and returns nil while dormant, so a strict test also calls `SetEnforceFromForTest`; (b) `Guard.OnRefused` callbacks append, each fires once per refusal on its own goroutine, `Guard.Refresh` fires them (also while dormant: the handlers here check `Allowed()`), so does a refusal another process wrote into `session.json`, and a callback registered while already refused is called at once (the engine's handler then finds nothing running and says nothing). Task 1 step 1 and Task 3 step 1 re-read the merged code and stop if either is false.
- **`monomind.Exec` asks a hook, not `account.Require`.** `internal/monomind` cannot import `internal/account`: `internal/account` imports `internal/secrets`, the tests of `internal/secrets` import `internal/storage` (`internal/secrets/blob_test.go:8`), and `internal/storage` imports `internal/monomind` (`internal/storage/repository.go:11`), so `go vet ./internal/secrets/` and `go test ./internal/secrets/` stop with `import cycle not allowed in test` (reproduced against B1a's code; the import rule of index §3.1 only limits what `internal/account` imports). The same holds for every package in that test binary's closure: `internal/daemonhb`, `internal/monomind`, `internal/orgdesign`, `internal/orgsign`, `internal/profiledir`, `internal/shellpath` and `internal/storage` must not import `internal/account`, directly or through another package. Task 4 therefore adds `monomind.SetAccountGate` and `cmd/monoagentcli` installs `account.Require` in an `init`; with no gate installed `Exec` refuses outside a test binary (`ErrNoAccountGate`) and runs in a test binary. This refuses nothing before the enforcement date: `monoagentcli` is the only binary that links `Exec` (Task 8 step 1 reads it from the linker's symbol table), and with the gate installed `account.Require` is nil while dormant.
- **Three engine sites beyond the named ones**, because "starts nothing new" and "fails nothing for good" need them: `RetryExecution` (no junk row, typed error), `ResumeExecution` and the resume loop's tick (`resumeTick`: adoption and resume). Without them every approved Human-in-Loop run would be failed by the backstop in `handleExecution`.
- **Cancel on refusal belongs to the engine.** `WorkflowEngine.Start` registers one `OnRefused` handler, so the daemon, `httpapi`, `mcp` and a one-shot `workflow run` cancel their running executions with no per-command code. The daemon's supervisor also cancels, by polling `CurrentStatus()`: the daemon is the unattended process that matters most, the poll needs no callback semantics, and both paths are idempotent.
- **Not done here.** A one-shot command cancelling its command context is B2's `run()` (`cancelWhenRefused` in its plan), not this plan's: `main.go` is B2's. In-flight `/v1` turns and MCP tool calls are not cancelled: `Gateway.Shutdown` is terminal (`internal/openaiapi/gateway.go:301`: "The gateway serves nothing useful afterwards"), so there is no non-terminal cancel to call; they end by their own timeout and every new turn is refused at the door (B3b).
- **`expired` (24 hours unreachable):** nothing is cancelled, and a run in flight finishes the node it is in; its next node is refused (Task 3b) and the run ends FAILED with `login_required:`, not retried.
- **A run in flight ends at its next node while the account is locked (ruling R4 of 2026-10-07; an open question for the owner until then).** The gate sites of Tasks 1 and 4 meet a run only when it starts (`handleExecution`) and at its gated calls (`monomind.Exec` for an agent turn, `ActionExecutor.executeDef` for a browser node and the rest of Task 4's list), so an execution that started before the lock and has no such node ahead (a loop of HTTP requests, a polling workflow, a long wait) would never meet a gate again and would keep running while the account is `locked(expired)`. Task 3b adds `account.Require(ctx)` at the start of every node in the engine's node loop (`RunExecution`, `internal/workflow/execution.go:35`): it needs no I/O (it reads the cached verdict), `ok` and `grace` never stop a run, the node in flight finishes (an agent turn or a browser action included: only the next node is refused), and a locked verdict, `expired` or any other lock, ends the run FAILED at its next node with `login_required: ` and the first line of the refusal. The spec says so too (D8 and §6.4: in-flight work finishes while the account is ok or grace). The cost, accepted with the ruling: a run in flight when the clock goes back (`clock_rollback`) also ends at its next node. A refusal (`invalid_grant`) still cancels everything in flight at once (Task 3).
- **Heartbeat:** `account` is `{state, reason, valid_until, enforced}` (index §3.4 item 4 with the `enforced` key of spec A4), set from `account.CurrentStatus()` before every write (`daemonhb.RunWith`: at start and every 10 s). It is absent while the gate is dormant (`EnforceFrom` is the zero time), so a dormant daemon writes the file it always wrote; readers treat absent as "nothing to report". In the warn period (a date set and not reached, nobody signed in) it reads `{"state":"locked","reason":"not_logged_in","enforced":false}` while nothing is refused and the daemon starts executions: `enforced` is how a reader tells that from a real lock.
- **Which commands can start locked.** The CLI gate's `serve` class (index §3.5, spec A1) is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (alias `bridge serve`): they pass the gate when locked, start, and are refused at layers 2 and 3, so Tasks 5 and 7 assume they can start locked. `org serve` is gated, refused at start when locked, `--foreground` included; without `--foreground` it is a launcher that starts a detached monomind process and returns (`cmd/monoagentcli/org_process.go:28-52`). Task 5's wiring there is for `org serve --foreground` while signed in, which locks later.
- **Known edges, not covered by layer 2** (spec A17), listed so the owner can see them: (a) validators run through `monomind.AgentTest`, not `Exec` (`internal/agentroster/validate.go:180` calls `internal/monomind/agenttest.go:62`, a separate `monomind agent test` process): `agent validate` is gated at layer 1, and the daemon's automatic re-validation waits while locked (Task 6), because a refusal would be stored as a failed validation; (b) `doctor fix`, an open command, runs one fixed `claude -p` profile-init prompt outside `Exec` (`internal/monomind/profile_init.go:129`, reached from `cmd/monoagentcli/doctor_env.go:115`); (c) a capture summary refused while locked is recorded with status `error` and is not retried (`internal/capturesummary/summarizer.go:218`); (d) in-flight `/v1` and MCP turns finish instead of being cancelled (see "Not done here").
- **Mutation recipe**, the last step of every task. Run after the task's commit so `git restore` returns to the committed code: `perl -pi -e 's/(; VAR != nil) \{/$1 && false {/' FILE` makes the named gate's `if` let everything through and still compiles; run the named test, expect FAIL; `git restore FILE`; run it again, expect PASS.
- **Why the existing tests stay green.** They install no guard and never call `StrictForTest`: `internal/workflow/engine_handletrigger_test.go:22` (`newTriggerEngine`) and `engine_test.go:79` (`newTestEngine`) build engines with `NewWorkflowEngineWithStore`; `internal/monomind/exec_test.go` calls `Exec(context.Background(), ExecOptions{Bin: bin, …})` 16 times; the `internal/action` tests build `NewActionExecutor(context.Background(), …)` 21 times. In a test binary `testing.Testing()` is true, so `account.Require` returns nil (index §3.2), and so does `Exec`'s own `requireAccount` when no gate is installed; the tests of `cmd/monoagentcli` run with the gate that `init` installs, which is `account.Require`, so they get the same nil. The tests of `RunExecution` (`execution_test.go`, `execution_realnode_test.go`, `execution_confine_test.go`, `trigger_context_test.go`) call it with no guard either, so Task 3b's node check lets them through. Tasks 1, 3b, 4 and 8 run those suites unchanged.

---

### Task 1: The engine's entry points refuse while the account is locked

The backstop in `handleExecution` (the queue's handler: schedules, webhooks, manual runs, org bridge, resumes and retries all end there), the three calls that create an execution row, and the retry filter.

**Files:**
- Modify: `internal/workflow/engine.go` (imports, lines 14-16; `handleExecution` after the cancelled-before-dispatch check, lines 606-611; `TriggerWorkflow`, lines 946-949; `TriggerWorkflowPersistOnly`, lines 979-980; `RetryExecution`, lines 1052-1053)
- Modify: `internal/workflow/errors.go` (imports, lines 3-8; `isNonRetryable`, lines 70-82)
- Test: Create `internal/workflow/engine_account_test.go`

**Interfaces:**
- Consumes (B1a, index §3.2 and §3.3): `func account.Require(ctx context.Context) error` (nil when allowed, else `*account.LoginRequiredError`), `func account.IsLoginRequired(err error) bool`, `type account.LoginRequiredError struct{ Status account.Status }`, `func account.StrictForTest(t testing.TB)`, `func account.SetEnforceFromForTest(t testing.TB, at time.Time)`, `func account.InstallForTest(t testing.TB, g *account.Guard)` (a nil guard installs none, and the previous one is put back on cleanup), `func accounttest.Install(t testing.TB, m accounttest.Mode) *account.Guard` with the modes `SignedIn`, `InGrace`, `LockedNoLogin`, `LockedRefused`, `Dormant`.
- Produces: no new exported API. `handleExecution` records `FAILED` with an error text starting `login_required: ` and does not load the workflow; `TriggerWorkflow`, `TriggerWorkflowPersistOnly` and `RetryExecution` return `*account.LoginRequiredError` and create no row (index §3.4 item 9); `isNonRetryable` is true for it.

- [ ] **Step 1: Confirm B1a is in your branch and reads as assumed (no edits).**

  ```
  go build ./internal/account/... ./internal/account/accounttest/
  ```
  Expected: no output. Note the commit you start from (`git rev-parse --short HEAD`; Task 8 step 7 compares against it). Then read `Require` in `internal/account/guard.go`: with no guard installed and `StrictForTest` on, it must return `*LoginRequiredError` once an enforcement date has passed and nil while `EnforceDate()` is the zero time. If it refuses while dormant, D22 is broken in B1a: stop and tell the lead; do not weaken the `dormant, strict, no guard` row below.

- [ ] **Step 2: Write the failing test.** Create `internal/workflow/engine_account_test.go`:

  ```go
  package workflow

  import (
  	"context"
  	"fmt"
  	"strings"
  	"sync"
  	"testing"
  	"time"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  )

  // engineGateStore is the stub store plus a record of what the engine did with
  // it ("load", "finished <id> <status> <message>", "resume <id>", "list"), so a
  // gate test can tell "refused at the gate" from "went on and failed later".
  type engineGateStore struct {
  	*stubStore
  	mu        sync.Mutex
  	calls     []string
  	waitingOn string // when set, GetExecution answers a WAITING run with this id
  }

  func newEngineGateStore(wf *Workflow) *engineGateStore {
  	return &engineGateStore{stubStore: &stubStore{workflowToReturn: wf}}
  }

  func (s *engineGateStore) note(format string, args ...any) {
  	s.mu.Lock()
  	defer s.mu.Unlock()
  	s.calls = append(s.calls, fmt.Sprintf(format, args...))
  }

  // count is how many recorded calls start with prefix; first is the earliest one, or "".
  func (s *engineGateStore) count(prefix string) (n int) {
  	s.mu.Lock()
  	defer s.mu.Unlock()
  	for _, c := range s.calls {
  		if strings.HasPrefix(c, prefix) {
  			n++
  		}
  	}
  	return n
  }

  func (s *engineGateStore) first(prefix string) string {
  	s.mu.Lock()
  	defer s.mu.Unlock()
  	for _, c := range s.calls {
  		if strings.HasPrefix(c, prefix) {
  			return c
  		}
  	}
  	return ""
  }

  func (s *engineGateStore) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
  	s.note("load")
  	return s.stubStore.GetWorkflow(ctx, id)
  }

  func (s *engineGateStore) SetExecutionFinished(_ context.Context, id, status, msg string) error {
  	s.note("finished %s %s %s", id, status, msg)
  	return nil
  }

  func (s *engineGateStore) GetExecution(ctx context.Context, id string) (*WorkflowExecution, error) {
  	if s.waitingOn == id {
  		return &WorkflowExecution{ID: id, WorkflowID: "wf-gate", Status: "WAITING"}, nil
  	}
  	return s.stubStore.GetExecution(ctx, id)
  }

  func (s *engineGateStore) ResumeWaitingExecution(_ context.Context, id string) (bool, error) {
  	s.note("resume %s", id)
  	return true, nil
  }

  func (s *engineGateStore) ListResumableExecutions(context.Context) ([]string, error) {
  	s.note("list")
  	return []string{"exec-waiting"}, nil
  }

  func (s *engineGateStore) ListAdoptableExecutions(context.Context) ([]string, error) {
  	s.note("list")
  	return nil, nil
  }

  // engineGateWorkflow is a loadable, active workflow of the engine's own profile.
  func engineGateWorkflow() *Workflow {
  	return &Workflow{ID: "wf-gate", IsActive: true, ProfileID: "engine-profile",
  		Nodes: []WorkflowNode{{ID: "t1", Type: "trigger.schedule"}}}
  }

  // engineGateMode is one state of the account that a gate site is judged in.
  type engineGateMode struct {
  	name    string
  	set     func(t *testing.T)
  	refused bool
  }

  // engineGatePastDate is an enforcement date that has already passed on the real clock,
  // which is the one Require reads when no guard is installed.
  func engineGatePastDate(t *testing.T) {
  	account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour))
  }

  // engineGateNoGuard is a process with no guard installed that is judged as a
  // release binary is: whatever guard an earlier test left behind is removed (and put
  // back when this test ends) and the test binary's fail-open rule is switched off.
  func engineGateNoGuard(t *testing.T) {
  	account.InstallForTest(t, nil)
  	account.StrictForTest(t)
  }

  // engineGateModes: refusal with no guard under the strict hook (the enforcement date
  // is set, or the dormant rule would let it pass), both locked verdicts, then the
  // states that must let work through: dormant and the warn period with no guard, and
  // a guard that says dormant, ok or grace. The test binary's fail-open rule is
  // switched off by the strict hook and by every accounttest.Install.
  var engineGateModes = []engineGateMode{
  	{"strict, no guard installed", func(t *testing.T) { engineGateNoGuard(t); engineGatePastDate(t) }, true},
  	{"locked, not logged in", func(t *testing.T) { accounttest.Install(t, accounttest.LockedNoLogin) }, true},
  	{"locked, refused", func(t *testing.T) { accounttest.Install(t, accounttest.LockedRefused) }, true},
  	{"dormant, strict, no guard", func(t *testing.T) { engineGateNoGuard(t); account.SetEnforceFromForTest(t, time.Time{}) }, false},
  	{"warn period, strict, no guard", func(t *testing.T) {
  		engineGateNoGuard(t)
  		account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
  	}, false},
  	{"dormant, guard installed", func(t *testing.T) { accounttest.Install(t, accounttest.Dormant) }, false},
  	{"signed in", func(t *testing.T) { accounttest.Install(t, accounttest.SignedIn) }, false},
  	{"grace", func(t *testing.T) { accounttest.Install(t, accounttest.InGrace) }, false},
  }

  func eachEngineGateMode(t *testing.T, fn func(t *testing.T, refused bool)) {
  	t.Helper()
  	for _, m := range engineGateModes {
  		t.Run(m.name, func(t *testing.T) {
  			m.set(t)
  			fn(t, m.refused)
  		})
  	}
  }

  // TestHandleExecutionGate: a refused run is recorded FAILED with a
  // "login_required:" message before its workflow is loaded; an admitted one
  // goes on to load it (the store has none, so it ends "workflow not found").
  func TestHandleExecutionGate(t *testing.T) {
  	eachEngineGateMode(t, func(t *testing.T, refused bool) {
  		store := newEngineGateStore(nil)
  		eng := newTriggerEngine(store)

  		eng.handleExecution(context.Background(), ExecutionRequest{WorkflowID: "wf-gate", ExecutionID: "exec-1"})

  		row := store.first("finished exec-1 ")
  		if refused {
  			if !strings.HasPrefix(row, "finished exec-1 FAILED login_required: ") || store.count("load") != 0 {
  				t.Fatalf("refused run: %q after %d loads, want FAILED with a login_required: message and no load", row, store.count("load"))
  			}
  			return
  		}
  		if row != "finished exec-1 FAILED workflow not found" || store.count("load") != 1 {
  			t.Fatalf("admitted run: %q after %d loads, want it to go on and end \"workflow not found\"", row, store.count("load"))
  		}
  	})
  }

  // TestTriggerEntryPointsGate: the three calls that create an execution row
  // return the typed error, and create nothing, when the account is locked.
  func TestTriggerEntryPointsGate(t *testing.T) {
  	ctx := context.Background()
  	sites := map[string]func(e *WorkflowEngine) error{
  		"TriggerWorkflow": func(e *WorkflowEngine) error {
  			_, err := e.TriggerWorkflow(ctx, "wf-gate", nil)
  			return err
  		},
  		"TriggerWorkflowPersistOnly": func(e *WorkflowEngine) error {
  			_, err := e.TriggerWorkflowPersistOnly(ctx, "wf-gate", nil)
  			return err
  		},
  		"RetryExecution": func(e *WorkflowEngine) error {
  			_, err := e.RetryExecution(ctx, "exec-old")
  			return err
  		},
  	}
  	for name, call := range sites {
  		t.Run(name, func(t *testing.T) {
  			eachEngineGateMode(t, func(t *testing.T, refused bool) {
  				store := newEngineGateStore(engineGateWorkflow())

  				err := call(newTriggerEngine(store))

  				if got := account.IsLoginRequired(err); got != refused {
  					t.Fatalf("err = %v, login required = %v, want %v", err, got, refused)
  				}
  				if n := len(store.createdExecs); refused && n != 0 {
  					t.Fatalf("a refused call created %d rows, want none", n)
  				}
  			})
  		})
  	}
  }

  // TestExecuteWithRetry_LoginRequiredNotRetried: a node refused by a gate fails
  // once; a retry policy must not hold the run for minutes of backoff.
  func TestExecuteWithRetry_LoginRequiredNotRetried(t *testing.T) {
  	refused := fmt.Errorf("agent.ask: %w", &account.LoginRequiredError{})
  	ex := &countingExecutor{typ: "t", errs: []error{refused}}

  	_, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, noWait)

  	if ex.calls != 1 {
  		t.Fatalf("executed %d times, want 1", ex.calls)
  	}
  	if !account.IsLoginRequired(err) {
  		t.Fatalf("err = %v, want the login_required error back", err)
  	}
  }
  ```

- [ ] **Step 3: Run it and see it fail.**

  ```
  go test ./internal/workflow/ -run '^(TestHandleExecutionGate|TestTriggerEntryPointsGate|TestExecuteWithRetry_LoginRequiredNotRetried)$' -count=1
  ```
  Expected: `FAIL`. The `strict` and both `locked` rows of each site fail: `refused run: "finished exec-1 FAILED workflow not found" after 1 loads, want FAILED with a login_required: message and no load` (`handleExecution`), `err = <nil>, login required = false, want true` (`TriggerWorkflow`, `TriggerWorkflowPersistOnly`) and `err = engine: retry execution: workflow: execution not found, login required = false, want true` (`RetryExecution`); the retry test says `executed 4 times, want 1`. The `dormant`, `signed in` and `grace` rows pass.

- [ ] **Step 4: Implement the engine gates.** In `internal/workflow/engine.go`:

  (a) Add the import, keeping the group sorted:
  ```go
  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/connections"
  ```
  (b) In `handleExecution`, directly after the `if st, err := e.store.GetExecutionStatus(…); err == nil && st == "CANCELLED" { … return }` block (step 0) and before the `persistCtx` closure, insert:
  ```go
  	// 0b. A locked account starts nothing (spec section 6.2). Record the refusal
  	// and stop before the workflow is loaded; the row is final, nothing retries it.
  	if lrErr := account.Require(ctx); lrErr != nil {
  		log.Warn().Err(lrErr).Msg("engine: handleExecution: monoes.me login required; execution refused")
  		e.persistExecutionFinished(log, req.ExecutionID, "FAILED", "login_required: "+lrErr.Error())
  		return
  	}
  ```
  (c) `TriggerWorkflow`: extend its doc comment and gate it:
  ```go
  // TriggerWorkflow manually triggers a workflow (for manual trigger nodes).
  // Returns the new execution ID. A locked account creates no execution row: the
  // caller gets the typed *account.LoginRequiredError.
  func (e *WorkflowEngine) TriggerWorkflow(ctx context.Context, workflowID string, data map[string]interface{}) (string, error) {
  	if trigErr := account.Require(ctx); trigErr != nil {
  		return "", trigErr
  	}
  	exec, err := e.newManualExecution(ctx, workflowID, data)
  ```
  (d) `TriggerWorkflowPersistOnly` (the line after its signature):
  ```go
  	if persistErr := account.Require(ctx); persistErr != nil {
  		return "", persistErr
  	}
  ```
  (e) `RetryExecution` (the line after its signature, before `orig, err := e.store.GetExecution(ctx, executionID)`):
  ```go
  	if retryErr := account.Require(ctx); retryErr != nil {
  		return "", retryErr
  	}
  ```

- [ ] **Step 5: Implement the retry filter.** In `internal/workflow/errors.go` add `"github.com/monoes/mono-agent/internal/account"` as its own import group after the standard library, extend the doc comment of `isNonRetryable` with "a locked monoes.me account (a gate refused the node; backing off for minutes would only hold the run)", and add the line before `errors.As(err, &pe)`:
  ```go
  		account.IsLoginRequired(err) ||
  ```

- [ ] **Step 6: Run it and see it pass.** Same command as step 3. Expected: `ok  	github.com/monoes/mono-agent/internal/workflow`.

- [ ] **Step 7: The existing engine tests still pass.** They never install a guard, and in a test binary `Require` then returns nil.
  ```
  go test ./internal/workflow/ -count=1
  ```
  Expected: `ok  	github.com/monoes/mono-agent/internal/workflow` (about 15 s).

- [ ] **Step 8: Commit.**
  ```
  git add internal/workflow/engine.go internal/workflow/errors.go internal/workflow/engine_account_test.go
  ```
  ```
  git commit -m "feat(account): the engine refuses to create or start an execution while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 9: Mutation checks** (recipe in Decisions; FILE is `internal/workflow/engine.go`). For each row: apply, run the test, expect the FAIL, `git restore FILE`.

  | VAR | test (`-run`) | expected FAIL |
  |---|---|---|
  | `lrErr` | `'^TestHandleExecutionGate$'` | the `strict, no guard installed` and both `locked` rows |
  | `trigErr` | `'^TestTriggerEntryPointsGate$/TriggerWorkflow$'` | the same three rows of that site |
  | `persistErr` | `'^TestTriggerEntryPointsGate$/TriggerWorkflowPersistOnly$'` | the same |
  | `retryErr` | `'^TestTriggerEntryPointsGate$/RetryExecution$'` | the same |

  The retry filter: `perl -pi -e 's/account\.IsLoginRequired\(err\) \|\|/account.IsLoginRequired(nil) ||/' internal/workflow/errors.go`, run `'^TestExecuteWithRetry_LoginRequiredNotRetried$'`, expect `executed 4 times, want 1`, `git restore internal/workflow/errors.go`. Afterwards step 6 passes again.

### Task 2: A locked engine drops triggers and leaves paused and queued runs alone

A schedule or webhook trigger that fires while locked is dropped with one log line a minute (the daemon still restores every active workflow's triggers at start, `RestoreActiveWorkflows`, so they fire again by themselves once the account is valid). The resume loop's adoption and resume pass, and `ResumeExecution`, do nothing while locked, so a Human-in-Loop run that was approved or a `--no-wait` row stays exactly as it is until the account is valid (Review Focus 2).

**Files:**
- Create: `internal/workflow/account_gate.go`
- Modify: `internal/workflow/engine.go` (struct `WorkflowEngine`, lines 21-38; `resumeLoop`, lines 307-328; `ResumeExecution`, lines 386-387; `handleTrigger`, lines 523-524)
- Test: Create `internal/workflow/engine_locked_test.go` (uses the helpers of Task 1's `engine_account_test.go`)

**Interfaces:**
- Consumes: the Task 1 list, and the test helpers `newEngineGateStore`, `engineGateWorkflow`, `eachEngineGateMode`, `newTriggerEngine` (`engine_handletrigger_test.go:22`).
- Produces (all unexported): `const lockedLogEvery = time.Minute`; `type dropNotes struct{ … }` with `func (d *dropNotes) note() (n int, due bool)` (counts one drop; `due` when a log line may be written, `n` the drops it covers); `func (e *WorkflowEngine) resumeTick(ctx context.Context)` (one pass of `resumeLoop`); the field `drops dropNotes` on `WorkflowEngine`. `ResumeExecution(executionID string) error` returns `*account.LoginRequiredError` while locked and flips nothing.

- [ ] **Step 1: Write the failing tests.** Create `internal/workflow/engine_locked_test.go`:

  ```go
  package workflow

  import (
  	"bytes"
  	"context"
  	"strings"
  	"testing"
  	"time"

  	"github.com/rs/zerolog"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  )

  // TestHandleTriggerGate: a schedule or webhook trigger that fires while the
  // account is locked is dropped before anything is loaded or created.
  func TestHandleTriggerGate(t *testing.T) {
  	eachEngineGateMode(t, func(t *testing.T, refused bool) {
  		store := newEngineGateStore(engineGateWorkflow())
  		eng := newTriggerEngine(store)

  		eng.handleTrigger("wf-gate", "t1", nil)

  		if refused {
  			if n := len(store.createdExecs); n != 0 || store.count("load") != 0 {
  				t.Fatalf("a dropped trigger created %d rows and loaded %d workflows, want none", n, store.count("load"))
  			}
  			return
  		}
  		if n := len(store.createdExecs); n != 1 {
  			t.Fatalf("an admitted trigger created %d rows, want 1", n)
  		}
  	})
  }

  // TestHandleTriggerLogsOnceAMinute: a schedule that fires every second while
  // the account is locked writes one line a minute, with the count it covers.
  func TestHandleTriggerLogsOnceAMinute(t *testing.T) {
  	accounttest.Install(t, accounttest.LockedNoLogin)
  	var logs bytes.Buffer
  	eng := NewWorkflowEngineWithStore(newEngineGateStore(engineGateWorkflow()), nil, nil, NewNodeTypeRegistry(),
  		EngineConfig{ProfileID: "engine-profile"}, zerolog.New(&logs))
  	clock := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
  	eng.drops.now = func() time.Time { return clock }

  	for i := 0; i < 3; i++ {
  		eng.handleTrigger("wf-gate", "t1", nil)
  		clock = clock.Add(time.Second)
  	}
  	if n := strings.Count(logs.String(), "trigger dropped"); n != 1 {
  		t.Fatalf("%d log lines for 3 drops inside a minute, want 1:\n%s", n, logs.String())
  	}

  	clock = clock.Add(time.Minute)
  	eng.handleTrigger("wf-gate", "t1", nil)
  	if n := strings.Count(logs.String(), "trigger dropped"); n != 2 {
  		t.Fatalf("%d log lines after a minute passed, want 2:\n%s", n, logs.String())
  	}
  	if !strings.Contains(logs.String(), `"dropped":3`) {
  		t.Errorf("the second line does not say that it covers 3 drops (the two held back and the one that broke the silence):\n%s", logs.String())
  	}
  }

  // TestResumeTickGate: a locked account neither adopts queued runs nor resumes
  // paused ones, so they are still there when the account is valid again.
  func TestResumeTickGate(t *testing.T) {
  	eachEngineGateMode(t, func(t *testing.T, refused bool) {
  		store := newEngineGateStore(engineGateWorkflow())
  		store.waitingOn = "exec-waiting"
  		eng := newTriggerEngine(store)

  		eng.resumeTick(context.Background())

  		if refused {
  			if store.count("list") != 0 || store.count("resume") != 0 {
  				t.Fatalf("a locked tick listed %d times and resumed %d, want it to do nothing", store.count("list"), store.count("resume"))
  			}
  			return
  		}
  		if store.count("list") != 2 || store.count("resume exec-waiting") != 1 {
  			t.Fatalf("an admitted tick listed %d times and resumed %d, want 2 and 1", store.count("list"), store.count("resume exec-waiting"))
  		}
  	})
  }

  // TestResumeExecutionGate: the public call that other loops (the org waker and
  // receiver) use refuses before it flips a paused run to QUEUED.
  func TestResumeExecutionGate(t *testing.T) {
  	eachEngineGateMode(t, func(t *testing.T, refused bool) {
  		store := newEngineGateStore(engineGateWorkflow())
  		store.waitingOn = "exec-waiting"
  		eng := newTriggerEngine(store)

  		err := eng.ResumeExecution("exec-waiting")

  		resumes := 1
  		if refused {
  			resumes = 0
  		}
  		if account.IsLoginRequired(err) != refused || store.count("resume") != resumes {
  			t.Fatalf("err = %v, %d resumes: want the typed error and none when refused, nil and one otherwise", err, store.count("resume"))
  		}
  	})
  }
  ```

- [ ] **Step 2: Run it and see it fail.**
  ```
  go test ./internal/workflow/ -run '^(TestHandleTriggerGate|TestHandleTriggerLogsOnceAMinute|TestResumeTickGate|TestResumeExecutionGate)$' -count=1
  ```
  Expected: `FAIL … [build failed]` with `eng.drops undefined (type *WorkflowEngine has no field or method drops)` and `eng.resumeTick undefined (type *WorkflowEngine has no field or method resumeTick)`.

- [ ] **Step 3: Create the drop log.** `internal/workflow/account_gate.go`:

  ```go
  package workflow

  import (
  	"sync"
  	"time"
  )

  // lockedLogEvery spaces the log lines of an engine that is dropping triggers
  // because the account is locked: a schedule that fires every second must not
  // write a line per tick.
  const lockedLogEvery = time.Minute

  // dropNotes counts the triggers a locked engine dropped and says when a log
  // line is due: at most one per lockedLogEvery, carrying the count since the
  // last one. The zero value is ready to use.
  type dropNotes struct {
  	mu      sync.Mutex
  	last    time.Time
  	dropped int
  	now     func() time.Time // nil: time.Now; a test sets it
  }

  // note counts one drop. When a line is due it returns how many drops that line
  // covers and true.
  func (d *dropNotes) note() (int, bool) {
  	d.mu.Lock()
  	defer d.mu.Unlock()
  	now := time.Now
  	if d.now != nil {
  		now = d.now
  	}
  	d.dropped++
  	at := now()
  	if !d.last.IsZero() && at.Sub(d.last) < lockedLogEvery {
  		return 0, false
  	}
  	n := d.dropped
  	d.last, d.dropped = at, 0
  	return n, true
  }
  ```

- [ ] **Step 4: Gate the three paths.** In `internal/workflow/engine.go`:

  (a) Add one field to the end of the `WorkflowEngine` struct (after `allowAllProfiles bool`):
  ```go
  	drops            dropNotes // triggers dropped while the account is locked
  ```
  (b) In `resumeLoop`, replace the body of `case <-ticker.C:` (from `e.adoptQueuedExecutions(ctx)` to the closing brace of the `for _, id := range ids` loop) with the call, and add the method right after the function:
  ```go
  		case <-ticker.C:
  			e.resumeTick(ctx)
  		}
  	}
  }

  // resumeTick is one pass of resumeLoop. A locked account does neither half
  // (spec D8: a locked daemon starts nothing new): adopting would claim queued
  // rows and resuming would flip paused runs to QUEUED, where the gate in
  // handleExecution would fail them for good. Left as they are, they run again
  // once the account is valid.
  func (e *WorkflowEngine) resumeTick(ctx context.Context) {
  	if tickErr := account.Require(ctx); tickErr != nil {
  		return
  	}
  	e.adoptQueuedExecutions(ctx)
  	ids, err := e.store.ListResumableExecutions(ctx)
  	if err != nil {
  		e.logger.Warn().Err(err).Msg("engine: listing resumable executions")
  		return
  	}
  	for _, id := range ids {
  		if err := e.ResumeExecution(id); err != nil {
  			e.logger.Warn().Err(err).Str("execution_id", id).Msg("engine: failed to resume execution")
  		}
  	}
  }
  ```
  (the `continue` of the old loop body became `return`; nothing else in it changed).

  (c) `ResumeExecution`, first lines of the function:
  ```go
  	// No ctx here (the callers are loops and receivers); Require reads memory only.
  	if resumeErr := account.Require(context.Background()); resumeErr != nil {
  		return resumeErr
  	}
  ```
  (d) `handleTrigger`, directly after `ctx := e.ctx` and before the `triggerType := "unknown"` block:
  ```go
  	// A locked account starts nothing: the trigger is dropped, and a schedule's
  	// tick is not made up later. One log line a minute says so (spec section
  	// 6.2). The judgement uses its own context: e.ctx is nil before Start and
  	// cancelled during Stop, and a trigger that fires then is still a trigger.
  	if dropErr := account.Require(context.Background()); dropErr != nil {
  		if n, due := e.drops.note(); due {
  			e.logger.Warn().Err(dropErr).Int("dropped", n).Str("workflow_id", workflowID).
  				Msg("engine: monoes.me login required; trigger dropped (logged once a minute)")
  		}
  		return
  	}
  ```

- [ ] **Step 5: Run it and see it pass.** Same command as step 2. Expected: `ok  	github.com/monoes/mono-agent/internal/workflow`.

- [ ] **Step 6: The whole package still passes.** `go test ./internal/workflow/ -count=1`. Expected: `ok`. (`TestEngine_AdoptsUnownedQueuedExecution` calls `adoptQueuedExecutions` directly and is untouched; `engine_handletrigger_test.go` calls `handleTrigger` on an engine that was never started, which is why the gate must not use `e.ctx`.)

- [ ] **Step 7: Commit.**
  ```
  git add internal/workflow/account_gate.go internal/workflow/engine.go internal/workflow/engine_locked_test.go
  ```
  ```
  git commit -m "feat(account): a locked engine drops triggers and leaves paused and queued runs alone" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 8: Mutation checks** (FILE is `internal/workflow/engine.go`; same rows as Task 1 step 9).

  | VAR | test (`-run`) | expected FAIL |
  |---|---|---|
  | `dropErr` | `'^TestHandleTriggerGate$'` | the `strict, no guard installed` and both `locked` rows |
  | `tickErr` | `'^TestResumeTickGate$'` | the same |
  | `resumeErr` | `'^TestResumeExecutionGate$'` | the same |

  The log limit: `perl -pi -e 's/at\.Sub\(d\.last\) < lockedLogEvery/false/' internal/workflow/account_gate.go`, run `'^TestHandleTriggerLogsOnceAMinute$'`, expect `3 log lines for 3 drops inside a minute, want 1`, `git restore internal/workflow/account_gate.go`.

### Task 3: The engine cancels what is running when monoes.me refuses the account

`WorkflowEngine.Start` registers one `OnRefused` handler, so every process that runs an engine (the daemon, `httpapi`, `mcp`, a one-shot `workflow run`) cancels its running executions when the guard learns that monoes.me answered no (D8, §6.4). `expired` cancels nothing.

**Files:**
- Create: `internal/workflow/account_refused.go`
- Modify: `internal/workflow/queue.go` (add `RunningIDs` before `Cancel`, line 254)
- Modify: `internal/workflow/engine.go` (`Start`, after the loops of step 5, lines 245-251; the cancelled case of `handleExecution`, lines 679-681; add `CancelRunning` before `RetryExecution`, line 1051)
- Test: Create `internal/workflow/engine_refused_test.go`

**Interfaces:**
- Consumes (B1a): `func account.Current() *account.Guard`, `func (g *account.Guard) OnRefused(fn func(account.Status))` (appends; each callback fires once when the verdict becomes `locked(refused)`), `func (g *account.Guard) Refresh(ctx context.Context) (account.Status, error)`, `func account.CurrentStatus() account.Status`, `func (s account.Status) Allowed() bool`, `account.ReasonRefused`, `account.ReasonExpired`, `account.StateLocked`, `account.StateOK`. For the tests: `accounttest.New(t)` with `.Token(accounttest.TokenOptions{Sub})` and `.Clock.Advance(d)`, `account.OpenStore(dir, account.NewMemorySealer())`, `account.NewGuard(account.GuardOptions{Store, Refresher, Now})`, `account.Refresher`, `account.RefusedError{Description}`, `account.TokenSet`, `account.Session`, `account.User`, `account.HostURL`, `account.InstallForTest(t, g)`.
- Produces: `func (q *ExecutionQueue) RunningIDs() []string` (dispatched and not finished: running, or waiting for a concurrency slot); `func (e *WorkflowEngine) CancelRunning() int` (`CancelExecution` on each, returns how many; Task 7's supervisor calls it); unexported `watchAccount()` (`Start` calls it), `onAccountRefused(st account.Status)`, `refusalCancels(st account.Status) bool` (a refusal, and only a refusal) and `cancelledMessage(runErr error) string`.

- [ ] **Step 1: Read B1a's `OnRefused` (no edits).** In `internal/account/guard.go` confirm that `OnRefused` appends to a list rather than replacing one callback, that each callback is called once when the verdict becomes `locked(refused)`, and that `Refresh` (not only the background refresher) fires it. If it replaces, the engine's handler and B2's `run()` would collide: stop and tell the lead. If `Refresh` does not fire it, `refusalGuard` below needs `StartRefresher` instead: stop and tell the lead.

- [ ] **Step 2: Write the failing tests.** Create `internal/workflow/engine_refused_test.go`:

  ```go
  package workflow

  import (
  	"context"
  	"strings"
  	"testing"
  	"time"

  	"github.com/rs/zerolog"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  )

  // refusalRefresher answers every refresh with invalid_grant: monoes.me said no.
  type refusalRefresher struct{}

  func (refusalRefresher) Refresh(context.Context, string) (*account.TokenSet, error) {
  	return nil, &account.RefusedError{Description: "the account was blocked"}
  }

  // refusalGuard installs a signed-in guard whose next refresh is refused and
  // returns the func that makes it try: the clock moves inside the refresh margin
  // and the guard refreshes, learns the refusal and tells whoever registered.
  func refusalGuard(t *testing.T) (refuse func()) {
  	t.Helper()
  	fx := accounttest.New(t)
  	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
  	signed := fx.Token(accounttest.TokenOptions{Sub: "u1"})
  	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: signed, User: &account.User{ID: "u1"}}); err != nil {
  		t.Fatal(err)
  	}
  	if err := store.SaveRefresh("refresh-1"); err != nil {
  		t.Fatal(err)
  	}
  	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: refusalRefresher{}, Now: fx.Clock.Now})
  	account.InstallForTest(t, g)
  	return func() {
  		fx.Clock.Advance(56 * time.Minute)
  		_, _ = g.Refresh(context.Background())
  	}
  }

  // refusalRun starts an engine over a real store whose one workflow takes a
  // minute to run (at most maxConcurrent runs at once), triggers n runs and waits
  // until one of them is RUNNING. It returns the engine, the store and the run ids.
  func refusalRun(t *testing.T, maxConcurrent, n int) (*WorkflowEngine, *SQLiteWorkflowStore, []string) {
  	t.Helper()
  	store := newFullEngineStore(t)
  	reg := NewNodeTypeRegistry()
  	reg.Register("test.slow", func() NodeExecutor { return slowNode{d: time.Minute} })
  	eng := NewWorkflowEngineWithStore(store, store.RawDB(), &fakeScheduler{}, reg,
  		EngineConfig{ProfileID: "default", WebhookAddr: "127.0.0.1:0", MaxConcurrent: maxConcurrent}, zerolog.Nop())
  	t.Cleanup(func() { _ = eng.Stop() })
  	ctx := context.Background()
  	if err := eng.Start(ctx); err != nil {
  		t.Fatalf("Start: %v", err)
  	}
  	wf := createActiveManualWorkflow(t, ctx, eng, "test.slow")
  	var ids []string
  	for i := 0; i < n; i++ {
  		id, err := eng.TriggerWorkflow(ctx, wf, nil)
  		if err != nil {
  			t.Fatalf("TriggerWorkflow: %v", err)
  		}
  		ids = append(ids, id)
  	}
  	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
  		for _, id := range ids {
  			if st, _ := store.GetExecutionStatus(ctx, id); st == "RUNNING" {
  				return eng, store, ids
  			}
  		}
  		if time.Now().After(deadline) {
  			t.Fatal("no run started")
  		}
  	}
  }

  // TestRefusalCancelsWhatIsRunning is the registration test: an engine that was
  // started while the guard was installed cancels its running execution when the
  // guard learns that monoes.me refused the account, and the run records why. A
  // person's own cancel records nothing about the account.
  func TestRefusalCancelsWhatIsRunning(t *testing.T) {
  	t.Run("refused by monoes.me", func(t *testing.T) {
  		refuse := refusalGuard(t)
  		_, store, ids := refusalRun(t, 0, 1)

  		refuse()

  		if st := waitForTerminalStatus(t, store, ids[0], 10*time.Second); st != "CANCELLED" {
  			t.Fatalf("status = %s, want CANCELLED", st)
  		}
  		exec, _ := store.GetExecution(context.Background(), ids[0])
  		if !strings.HasPrefix(exec.ErrorMessage, "login_required: ") {
  			t.Errorf("the cancelled run recorded %q, want it to start with login_required: ", exec.ErrorMessage)
  		}
  	})
  	t.Run("cancelled by a person while signed in", func(t *testing.T) {
  		accounttest.Install(t, accounttest.SignedIn)
  		eng, store, ids := refusalRun(t, 0, 1)

  		eng.CancelExecution(ids[0])

  		if st := waitForTerminalStatus(t, store, ids[0], 10*time.Second); st != "CANCELLED" {
  			t.Fatalf("status = %s, want CANCELLED", st)
  		}
  		exec, _ := store.GetExecution(context.Background(), ids[0])
  		if strings.Contains(exec.ErrorMessage, "login_required") {
  			t.Errorf("a person's cancel recorded %q, want no mention of the account", exec.ErrorMessage)
  		}
  	})
  }

  // TestRefusalEndsRunsQueuedBehindTheLimit: a run still waiting for a concurrency
  // slot has no goroutine of its own to stop, and cancelling only its context
  // would leave its row QUEUED for good. Both runs end: CANCELLED, or, when the
  // freed slot reached the queued run before its cancel did, FAILED by the gate.
  func TestRefusalEndsRunsQueuedBehindTheLimit(t *testing.T) {
  	refuse := refusalGuard(t)
  	_, store, ids := refusalRun(t, 1, 2)
  	time.Sleep(200 * time.Millisecond)
  	queued := 0
  	for _, id := range ids {
  		if st, _ := store.GetExecutionStatus(context.Background(), id); st == "QUEUED" {
  			queued++
  		}
  	}
  	if queued != 1 {
  		t.Fatalf("%d of the two runs are QUEUED, want exactly the one behind the limit", queued)
  	}

  	refuse()

  	for _, id := range ids {
  		st := waitForTerminalStatus(t, store, id, 10*time.Second)
  		exec, _ := store.GetExecution(context.Background(), id)
  		if st != "CANCELLED" && (st != "FAILED" || !strings.HasPrefix(exec.ErrorMessage, "login_required: ")) {
  			t.Errorf("execution %s ended %s (%q), want CANCELLED, or FAILED with login_required", id, st, exec.ErrorMessage)
  		}
  	}
  }

  // TestOnlyARefusalCancels: a refusal cancels; at the 24 hours (expired) nothing is
  // cancelled (a run in flight ends at its next node, Task 3b), and before the
  // enforcement date nothing is cancelled.
  func TestOnlyARefusalCancels(t *testing.T) {
  	for name, c := range map[string]struct {
  		st   account.Status
  		want bool
  	}{
  		"refused":                             {account.Status{State: account.StateLocked, Reason: account.ReasonRefused, Enforced: true}, true},
  		"expired":                             {account.Status{State: account.StateLocked, Reason: account.ReasonExpired, Enforced: true}, false},
  		"refused before the enforcement date": {account.Status{State: account.StateLocked, Reason: account.ReasonRefused}, false},
  		"signed in":                           {account.Status{State: account.StateOK, Enforced: true}, false},
  	} {
  		if got := refusalCancels(c.st); got != c.want {
  			t.Errorf("%s: refusalCancels = %v, want %v", name, got, c.want)
  		}
  	}
  }
  ```

- [ ] **Step 3: Run it and see it fail.**
  ```
  go test ./internal/workflow/ -run '^(TestRefusalCancelsWhatIsRunning|TestOnlyARefusalCancels|TestRefusalEndsRunsQueuedBehindTheLimit)$' -count=1
  ```
  Expected: `FAIL … [build failed]` with `undefined: refusalCancels`.

- [ ] **Step 4: Implement.**

  (a) `internal/workflow/queue.go`, before `// Cancel signals cancellation for a specific execution.`:
  ```go
  // RunningIDs lists the executions this queue has dispatched and not seen
  // finish: the ones running and the ones waiting for a concurrency slot. Each
  // holds a cancel func in cancelFuncs for exactly that long.
  func (q *ExecutionQueue) RunningIDs() []string {
  	var ids []string
  	q.cancelFuncs.Range(func(k, _ any) bool {
  		if id, ok := k.(string); ok {
  			ids = append(ids, id)
  		}
  		return true
  	})
  	return ids
  }
  ```
  (b) `internal/workflow/engine.go`, `Start`: after `go e.staleReapLoop(e.ctx)` and before `e.logger.Info().Msg("workflow engine started")`:
  ```go

  	// 6. Cancel what is in flight when monoes.me refuses the account (spec
  	// section 6.4). Registered here, so every process that runs an engine has it.
  	e.watchAccount()
  ```
  (c) the cancelled case of `handleExecution` step 5: replace `runErr.Error()` with `cancelledMessage(runErr)` in
  ```go
  		e.persistExecutionFinished(log, req.ExecutionID, "CANCELLED", cancelledMessage(runErr))
  ```
  (d) before `// RetryExecution re-queues a failed execution as a new execution.`:
  ```go
  // CancelRunning asks every execution this engine has dispatched and not
  // finished to stop, through CancelExecution, and returns how many it asked.
  // The account guard's refusal handler calls it (spec section 6.4). A WAITING
  // run has no goroutine and is left as it is.
  func (e *WorkflowEngine) CancelRunning() int {
  	ids := e.queue.RunningIDs()
  	for _, id := range ids {
  		e.CancelExecution(id)
  	}
  	return len(ids)
  }
  ```
  (e) Create `internal/workflow/account_refused.go`:
  ```go
  package workflow

  import "github.com/monoes/mono-agent/internal/account"

  // watchAccount registers the cancel-on-refused handler with the process guard.
  // With none installed (a test, or a process nothing guards) there is nothing to hear.
  func (e *WorkflowEngine) watchAccount() {
  	if g := account.Current(); g != nil {
  		g.OnRefused(e.onAccountRefused)
  	}
  }

  // refusalCancels says whether a verdict makes the engine cancel what it is
  // running: a refusal, and only a refusal. At the 24 hours (expired) nothing is
  // cancelled: the node in flight finishes and the run ends at its next node (spec
  // D8, Task 3b's node check), and before the enforcement date nothing is cancelled.
  func refusalCancels(st account.Status) bool {
  	return !st.Allowed() && st.Reason == account.ReasonRefused
  }

  // onAccountRefused cancels the executions in flight when monoes.me answered no.
  // It does the work on its own goroutine: CancelExecution writes to the
  // database, and the guard must not wait for that. A callback registered while
  // the account is already refused is called at once and finds nothing running:
  // that says nothing.
  func (e *WorkflowEngine) onAccountRefused(st account.Status) {
  	if !refusalCancels(st) {
  		return
  	}
  	go func() {
  		if n := e.CancelRunning(); n > 0 {
  			e.logger.Warn().Int("cancelled", n).Msg("engine: monoes.me refused this account; running executions cancelled")
  		}
  	}()
  }

  // cancelledMessage is what a CANCELLED run records. When monoes.me has refused
  // the account the run says so (spec section 12: the run records login_required):
  // the cancel came from the guard, not from a person.
  func cancelledMessage(runErr error) string {
  	if refusalCancels(account.CurrentStatus()) {
  		return "login_required: monoes.me refused this account: " + runErr.Error()
  	}
  	return runErr.Error()
  }
  ```

- [ ] **Step 5: Run it and see it pass, with the race detector.**
  ```
  go test ./internal/workflow/ -run '^(TestRefusalCancelsWhatIsRunning|TestOnlyARefusalCancels|TestRefusalEndsRunsQueuedBehindTheLimit)$' -count=1 -race
  ```
  Expected: `ok  	github.com/monoes/mono-agent/internal/workflow` in about 5 s.

- [ ] **Step 6: The whole package still passes.** `go test ./internal/workflow/ -count=1`. Expected: `ok`.

- [ ] **Step 7: Commit.**
  ```
  git add internal/workflow/account_refused.go internal/workflow/queue.go internal/workflow/engine.go internal/workflow/engine_refused_test.go
  ```
  ```
  git commit -m "feat(account): the engine cancels what is running when monoes.me refuses the account" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 8: Mutation checks.** For each: apply, run the test, expect the FAIL, `git restore` the file.
  - `perl -pi -e 's/^\te\.watchAccount\(\)$/\t_ = e.watchAccount/' internal/workflow/engine.go`; `'^TestRefusalCancelsWhatIsRunning$'`: after 10 s `execution still RUNNING after 10s` (the refusal reached nobody).
  - `perl -pi -e 's/cancelledMessage\(runErr\)/runErr.Error()/' internal/workflow/engine.go`; the same test: `recorded "node n (N): workflow: execution was cancelled", want it to start with login_required: `.
  - `perl -pi -e 's/ && st\.Reason == account\.ReasonRefused//' internal/workflow/account_refused.go`; `'^TestOnlyARefusalCancels$'`: `expired: refusalCancels = true, want false`.
  - `perl -0pi -e 's/\t\te\.CancelExecution\(id\)\n/\t\te.queue.Cancel(id)\n/' internal/workflow/engine.go`; `'^TestRefusalEndsRunsQueuedBehindTheLimit$'`: after 10 s `execution still QUEUED after 10s`.

### Task 3b: A run in flight ends at its next node while the account is locked

Ruling R4 of 2026-10-07 (spec D8 and §6.4 as amended): `account.Require(ctx)` at the start of every node in the engine's node loop (`RunExecution`, `internal/workflow/execution.go:35` at master `f4441a2a`). A locked verdict (the 24 hours, `expired`, or any other lock) ends the run `FAILED` at its next node, with an error text that is `login_required: ` and the first line of the refusal; `ok` and `grace` never stop a run; the node in flight finishes, an agent turn or a browser action included, and only the next node is refused. Without the check a run with no agent or browser node (a polling loop, a long wait) never meets a gate again once it has started, and outlives the 24 hours. The check reads the guard's cached verdict, so a node pays no I/O for it. A refusal (`invalid_grant`) still cancels at once (Task 3); this task ends what a cancel does not reach and every other lock.

**Files:**
- Create: `internal/workflow/account_node.go`
- Modify: `internal/workflow/execution.go` (the node loop of `RunExecution`: after the context-cancellation check, lines 129-134 at master; Tasks 1 to 3 do not touch this file)
- Test: Create `internal/workflow/execution_account_test.go`

**Interfaces:**
- Consumes (B1a): `func account.Require(ctx context.Context) error`, `type account.LoginRequiredError struct{ Status account.Status }` (its `Error()` is `account.LoginRequiredMessage`, then for `expired` a second line with the reason), `account.LoginRequiredMessage`, `func account.CurrentStatus() account.Status`, `account.StateLocked`, `account.StateOK`, `account.StateGrace`, `account.ReasonExpired`, `account.InstallForTest`, `account.StrictForTest`, `account.SetEnforceFromForTest`; `func accounttest.InstallWithFixture(t testing.TB, m accounttest.Mode) (*account.Guard, *accounttest.Fixture)` (the guard runs on the fixture's `Clock`, which `.Advance(d)` moves) with the modes `SignedIn` and `InGrace`. From package `workflow`: `RunExecution`, `BuildDAG`, `NewNodeTypeRegistry`, `NewWorkflowEngineWithStore`, `handleExecution`, `ExecutionRequest`, `EngineConfig`, and the test store `stubStore` with `nodeRecord` (`internal/workflow/execution_test.go`).
- Produces: unexported `requireAccountForNode(ctx context.Context) error` and `nodeRefusedError` (its `Error()` is `login_required: ` and the first line of the refusal; `Unwrap()` returns the `*account.LoginRequiredError`, so `account.IsLoginRequired` sees it). `RunExecution` returns it at the node it refuses, and `handleExecution` records the run `FAILED` with that text through its existing default case (no change there).

- [ ] **Step 1: Read the node loop (no edits).** In `internal/workflow/execution.go` confirm that the loop of `RunExecution` starts with the resume skip (`if completedNodes[node.ID] { continue }`) and the context-cancellation check (`// Check context cancellation.`, a `select` on `ctx.Done()` that returns `ErrExecutionCancelled`), and that `handleExecution`'s step 5 records any other error of `runExecution` with `persistExecutionFinished(log, req.ExecutionID, "FAILED", runErr.Error())`. The check goes right after the cancellation check, so a cancelled run still ends `CANCELLED` (Task 3's tests) and a node that a resume skips is not judged again.

- [ ] **Step 2: Write the failing test.** Create `internal/workflow/execution_account_test.go`:

  ```go
  package workflow

  import (
  	"context"
  	"errors"
  	"slices"
  	"strings"
  	"sync"
  	"testing"
  	"time"

  	"github.com/rs/zerolog"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  )

  // nodeGateStep is a plain node (no agent turn, no browser action). The test's hook
  // for it runs while it is in flight, and then it tells the test it ran to its end.
  type nodeGateStep struct{ ran func(nodeID string) }

  func (nodeGateStep) Type() string { return "test.step" }

  func (s nodeGateStep) Execute(_ context.Context, in NodeInput, _ map[string]interface{}) ([]NodeOutput, error) {
  	s.ran(in.NodeID)
  	return []NodeOutput{{Handle: "main", Items: in.Items}}, nil
  }

  // nodeGateStore is the stub store that hands the engine the run's execution and
  // keeps the terminal status and message the engine records.
  type nodeGateStore struct {
  	*stubStore
  	exec     *WorkflowExecution
  	finished []string // "<status> <message>"
  }

  func (s *nodeGateStore) GetExecution(context.Context, string) (*WorkflowExecution, error) {
  	cp := *s.exec
  	return &cp, nil
  }

  func (s *nodeGateStore) SetExecutionFinished(_ context.Context, _ string, status, msg string) error {
  	s.mu.Lock()
  	defer s.mu.Unlock()
  	s.finished = append(s.finished, status+" "+msg)
  	return nil
  }

  // nodeGateWorkflow is a trigger and three plain nodes in a row: t1, n1, n2, n3.
  func nodeGateWorkflow() *Workflow {
  	node := func(id, typ string) WorkflowNode {
  		return WorkflowNode{ID: id, WorkflowID: "wf-node-gate", Type: typ, Name: id}
  	}
  	edge := func(from, to string) WorkflowConnection {
  		return WorkflowConnection{SourceNodeID: from, SourceHandle: "main", TargetNodeID: to, TargetHandle: "main"}
  	}
  	return &Workflow{ID: "wf-node-gate", Name: "node gate", IsActive: true, ProfileID: "engine-profile",
  		Nodes:       []WorkflowNode{node("t1", "trigger.manual"), node("n1", "test.step"), node("n2", "test.step"), node("n3", "test.step")},
  		Connections: []WorkflowConnection{edge("t1", "n1"), edge("n1", "n2"), edge("n2", "n3")}}
  }

  // nodeGateRegistry registers test.step, whose nodes call during(nodeID) while they
  // are in flight and then append their id to *ran.
  func nodeGateRegistry(during func(nodeID string), ran *[]string) *NodeTypeRegistry {
  	var mu sync.Mutex
  	reg := NewNodeTypeRegistry()
  	reg.Register("test.step", func() NodeExecutor {
  		return nodeGateStep{ran: func(id string) {
  			if during != nil {
  				during(id)
  			}
  			mu.Lock()
  			*ran = append(*ran, id)
  			mu.Unlock()
  		}}
  	})
  	return reg
  }

  // runNodeGateWorkflow runs nodeGateWorkflow through the engine's queue handler, as
  // every run starts, and returns the plain nodes that ran to their end, the terminal
  // status and message the engine recorded, and the store with the node records.
  func runNodeGateWorkflow(t *testing.T, during func(nodeID string)) (ran []string, final string, store *nodeGateStore) {
  	t.Helper()
  	wf := nodeGateWorkflow()
  	store = &nodeGateStore{stubStore: &stubStore{workflowToReturn: wf},
  		exec: &WorkflowExecution{ID: "exec-node-gate", WorkflowID: wf.ID, ProfileID: wf.ProfileID}}
  	eng := NewWorkflowEngineWithStore(store, nil, nil, nodeGateRegistry(during, &ran),
  		EngineConfig{ProfileID: "engine-profile"}, zerolog.Nop())

  	eng.handleExecution(context.Background(), ExecutionRequest{WorkflowID: wf.ID, ExecutionID: "exec-node-gate"})

  	store.mu.Lock()
  	defer store.mu.Unlock()
  	if len(store.finished) != 1 {
  		t.Fatalf("the engine recorded %d terminal statuses %q, want one", len(store.finished), store.finished)
  	}
  	return ran, store.finished[0], store
  }

  // Ruling R4: a run in flight ends at its next node once the account is locked. The
  // 24 hours run out while n1 runs (the guard's clock jumps past the grace): n1
  // finishes, n2 never starts, and the run is FAILED with "login_required: " and the
  // first line of the refusal. A run of plain nodes, with no agent turn or browser
  // action of its own, cannot outlive the gate.
  func TestARunEndsAtItsNextNodeOnceTheAccountLocks(t *testing.T) {
  	_, fx := accounttest.InstallWithFixture(t, accounttest.SignedIn)

  	ran, final, store := runNodeGateWorkflow(t, func(id string) {
  		if id == "n1" {
  			fx.Clock.Advance(25 * time.Hour) // no refresh for 25 hours: the grace is over
  		}
  	})

  	if st := account.CurrentStatus(); st.State != account.StateLocked || st.Reason != account.ReasonExpired {
  		t.Fatalf("the verdict after the jump is %s(%s), want locked(expired)", st.State, st.Reason)
  	}
  	if !slices.Equal(ran, []string{"n1"}) {
  		t.Fatalf("the nodes that ran to their end are %v, want [n1]: the node in flight finishes, the next one is refused", ran)
  	}
  	n1, n2 := store.nodeRecord("n1"), store.nodeRecord("n2")
  	if n1 == nil || n1.Status != "SUCCESS" || n2 != nil {
  		t.Fatalf("n1 recorded SUCCESS %v, n2 recorded at all %v: want n1, the node in flight, finished and no record of n2",
  			n1 != nil && n1.Status == "SUCCESS", n2 != nil)
  	}
  	if want := "FAILED login_required: " + account.LoginRequiredMessage; final != want {
  		t.Fatalf("the run recorded %q, want %q", final, want)
  	}
  }

  // Ruling R4: ok and grace never stop a run, also when the verdict moves from ok to
  // grace while it runs.
  func TestARunGoesOnWhileTheAccountIsOkOrInGrace(t *testing.T) {
  	for _, c := range []struct {
  		name string
  		mode accounttest.Mode
  		jump time.Duration // how far the guard's clock moves while n1 runs
  		want account.State
  	}{
  		{"ok", accounttest.SignedIn, 0, account.StateOK},
  		{"grace", accounttest.InGrace, 0, account.StateGrace},
  		{"ok, then grace while n1 runs", accounttest.SignedIn, 2 * time.Hour, account.StateGrace},
  	} {
  		t.Run(c.name, func(t *testing.T) {
  			_, fx := accounttest.InstallWithFixture(t, c.mode)

  			ran, final, _ := runNodeGateWorkflow(t, func(id string) {
  				if id == "n1" {
  					fx.Clock.Advance(c.jump)
  				}
  			})

  			if st := account.CurrentStatus(); st.State != c.want {
  				t.Fatalf("the verdict is %s(%s), want %s", st.State, st.Reason, c.want)
  			}
  			if !slices.Equal(ran, []string{"n1", "n2", "n3"}) || final != "SUCCESS " {
  				t.Fatalf("nodes %v, recorded %q: want all three and SUCCESS", ran, final)
  			}
  		})
  	}
  }

  // A dormant gate (no enforcement date) or a date still ahead changes nothing: the
  // run goes on when the guard's clock passes the 24 hours while it runs, and with no
  // guard installed at all, judged strictly as a release binary is.
  func TestADormantGateNeverEndsARun(t *testing.T) {
  	for _, c := range []struct {
  		name string
  		set  func(t *testing.T) (jump func())
  	}{
  		{"dormant, a guard whose grace runs out while n1 runs", func(t *testing.T) func() {
  			_, fx := accounttest.InstallWithFixture(t, accounttest.SignedIn)
  			account.SetEnforceFromForTest(t, time.Time{})
  			return func() { fx.Clock.Advance(25 * time.Hour) }
  		}},
  		{"dormant, strict, no guard", func(t *testing.T) func() {
  			account.InstallForTest(t, nil)
  			account.StrictForTest(t)
  			account.SetEnforceFromForTest(t, time.Time{})
  			return func() {}
  		}},
  		{"a date still ahead, strict, no guard", func(t *testing.T) func() {
  			account.InstallForTest(t, nil)
  			account.StrictForTest(t)
  			account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
  			return func() {}
  		}},
  	} {
  		t.Run(c.name, func(t *testing.T) {
  			jump := c.set(t)

  			ran, final, _ := runNodeGateWorkflow(t, func(id string) {
  				if id == "n1" {
  					jump()
  				}
  			})

  			if !slices.Equal(ran, []string{"n1", "n2", "n3"}) || final != "SUCCESS " {
  				t.Fatalf("nodes %v, recorded %q: want all three and SUCCESS", ran, final)
  			}
  		})
  	}
  }

  // The refusal of a node is a typed error. The execution's text is "login_required: "
  // and the first line of the refusal; the error unwraps to the
  // *account.LoginRequiredError, whose verdict says why, and whose own text keeps the
  // reason line that the execution's leaves out.
  func TestARefusedNodeEndsTheRunWithTheFirstLineOfTheRefusal(t *testing.T) {
  	_, fx := accounttest.InstallWithFixture(t, accounttest.SignedIn)
  	var ran []string
  	reg := nodeGateRegistry(func(id string) {
  		if id == "n1" {
  			fx.Clock.Advance(25 * time.Hour)
  		}
  	}, &ran)
  	wf := nodeGateWorkflow()
  	dag, err := BuildDAG(wf.Nodes, wf.Connections)
  	if err != nil {
  		t.Fatalf("BuildDAG: %v", err)
  	}

  	err = RunExecution(context.Background(), &WorkflowExecution{ID: "exec-node-gate", WorkflowID: wf.ID}, wf, dag, reg,
  		&stubStore{}, nil, NewExpressionEngine(), zerolog.Nop())

  	var lr *account.LoginRequiredError
  	if !errors.As(err, &lr) || lr.Status.Reason != account.ReasonExpired {
  		t.Fatalf("RunExecution = %v, want a *account.LoginRequiredError for expired", err)
  	}
  	if got, want := err.Error(), "login_required: "+account.LoginRequiredMessage; got != want {
  		t.Fatalf("the error text is %q, want %q", got, want)
  	}
  	if !strings.HasPrefix(lr.Error(), account.LoginRequiredMessage+"\n") {
  		t.Fatalf("the refusal is %q, want its first line and then the reason", lr.Error())
  	}
  	if !slices.Equal(ran, []string{"n1"}) {
  		t.Fatalf("the nodes that ran to their end are %v, want [n1]", ran)
  	}
  }
  ```

- [ ] **Step 3: Run it and see it fail.**
  ```
  go test ./internal/workflow/ -run '^(TestARunEndsAtItsNextNodeOnceTheAccountLocks|TestARunGoesOnWhileTheAccountIsOkOrInGrace|TestADormantGateNeverEndsARun|TestARefusedNodeEndsTheRunWithTheFirstLineOfTheRefusal)$' -count=1
  ```
  Expected: `FAIL`. Two tests fail and say why; the other two pass, because before this task nothing ends a run in flight:
  ```
  --- FAIL: TestARunEndsAtItsNextNodeOnceTheAccountLocks (0.01s)
      execution_account_test.go:119: the nodes that ran to their end are [n1 n2 n3], want [n1]: the node in flight finishes, the next one is refused
  --- FAIL: TestARefusedNodeEndsTheRunWithTheFirstLineOfTheRefusal (0.01s)
      execution_account_test.go:228: RunExecution = <nil>, want a *account.LoginRequiredError for expired
  ```

- [ ] **Step 4: Implement.**

  (a) Create `internal/workflow/account_node.go`:
  ```go
  package workflow

  import (
  	"context"
  	"strings"

  	"github.com/monoes/mono-agent/internal/account"
  )

  // requireAccountForNode is the account check before every node of a run (spec D8
  // and section 6.4, ruling R4 of 2026-10-07). A run in flight goes on while the
  // account is ok or grace; a locked verdict (the 24 hours without monoes.me, a
  // refusal, any other lock) ends it at its next node, so a run with no agent or
  // browser node (a polling loop, a long wait) cannot outlive the gate. The node in
  // flight is never interrupted: an agent turn or a browser action that started
  // before the lock finishes, and only the next node is refused. Require reads the
  // guard's cached verdict, so a node pays no I/O for it.
  func requireAccountForNode(ctx context.Context) error {
  	if err := account.Require(ctx); err != nil {
  		return &nodeRefusedError{err: err}
  	}
  	return nil
  }

  // nodeRefusedError ends a run at a node that a locked account refuses. Its text is
  // what the execution records: "login_required: " and the first line of the
  // refusal (the reason line stays in the wrapped error). It unwraps to the
  // *account.LoginRequiredError, so account.IsLoginRequired sees it.
  type nodeRefusedError struct{ err error }

  func (e *nodeRefusedError) Error() string {
  	first, _, _ := strings.Cut(e.err.Error(), "\n")
  	return "login_required: " + first
  }

  func (e *nodeRefusedError) Unwrap() error { return e.err }
  ```
  (b) In `internal/workflow/execution.go`, in the loop of `RunExecution`, directly after the context-cancellation check (the `select { case <-ctx.Done(): return ErrExecutionCancelled; default: }` block) and before `// Skip disabled nodes; still mark their successors so mergeWaiting`, insert (no import changes: the check lives in `account_node.go`):
  ```go
  		// A locked account ends the run here, before this node starts; the node
  		// before it has finished (account_node.go).
  		if nodeErr := requireAccountForNode(ctx); nodeErr != nil {
  			return nodeErr
  		}
  ```

- [ ] **Step 5: Run it and see it pass, with the race detector.** The command of step 3 with `-race`. Expected: `ok  	github.com/monoes/mono-agent/internal/workflow`.

- [ ] **Step 6: The whole package still passes.** The older tests of `RunExecution` install no guard, and in a test binary `Require` then returns nil.
  ```
  go test ./internal/workflow/ -count=1
  ```
  Expected: `ok  	github.com/monoes/mono-agent/internal/workflow` (about 15 s).

- [ ] **Step 7: Commit.**
  ```
  git add internal/workflow/account_node.go internal/workflow/execution.go internal/workflow/execution_account_test.go
  ```
  ```
  git commit -m "feat(account): a run in flight ends at its next node while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 8: Mutation checks.** For each: apply, run the command of step 3, expect the FAIL, `git restore` the file.
  - The recipe of the Decisions list with VAR `nodeErr` on `internal/workflow/execution.go` (the check lets everything through): `TestARunEndsAtItsNextNodeOnceTheAccountLocks` says `the nodes that ran to their end are [n1 n2 n3], want [n1]` and `TestARefusedNodeEndsTheRunWithTheFirstLineOfTheRefusal` says `RunExecution = <nil>, want a *account.LoginRequiredError for expired`.
  - `perl -pi -e 's/"\\n"\)/"\\x00")/' internal/workflow/account_node.go` (the execution's text carries the whole refusal, its reason line included): both of those tests fail on the text, `want "FAILED login_required: Log in to monoes.me first: monoagentcli account login"` and `want "login_required: Log in to monoes.me first: monoagentcli account login"`.
  - The check after the node instead of before it, so that the node in flight is refused too (two commands, then restore the file): `perl -0pi -e 's/\t\tif nodeErr := requireAccountForNode\(ctx\); nodeErr != nil \{\n\t\t\treturn nodeErr\n\t\t\}\n\n//' internal/workflow/execution.go` and `perl -0pi -e 's/(\t\toutputs, execErr := executeWithRetry\(ctx, executor, nodeInput, resolvedConfig, retryPolicy\)\n)/$1\t\tif nodeErr := requireAccountForNode(ctx); nodeErr != nil {\n\t\t\treturn nodeErr\n\t\t}\n/' internal/workflow/execution.go`: `TestARunEndsAtItsNextNodeOnceTheAccountLocks` says `n1 recorded SUCCESS false, n2 recorded at all false`.

### Task 4: Every agent turn and every browser action refuses while locked

`monomind.Exec` (chat, coder, `/v1`, `agent.ask`, summaries, matching, org turns) and `ActionExecutor.executeDef` (`node run`, `login`, `crawl`, `capture page`, `connect oauth`, `application apply` and `send`, `automation test`, and every browser node) each ask the account before they do anything, and both return the typed error (index §3.4 item 9). `executeDef` calls `account.Require` as its first statement. `Exec` asks a gate as its first statement, and `cmd/monoagentcli`, the one binary that links `Exec`, installs `account.Require` as that gate, because `internal/monomind` cannot import `internal/account` (see "`monomind.Exec` asks a hook" in the Decisions list). With no gate installed `Exec` refuses outside a test binary (`ErrNoAccountGate`, even before the enforcement date, so a binary that starts running turns without the gate fails at once instead of running unguarded) and lets a test binary through, as every older suite expects.

**Files:**
- Create: `internal/monomind/account_gate.go`, `cmd/monoagentcli/monomind_gate.go`
- Modify: `internal/monomind/exec.go` (the `Exec` doc comment and its first statement, lines 317-321; no import change)
- Modify: `internal/action/executor.go` (imports, lines 3-15; `executeDef`, line 535)
- Test: Create `internal/monomind/exec_account_test.go`, `internal/action/executor_account_test.go`, `cmd/monoagentcli/monomind_gate_test.go`

**Interfaces:**
- Consumes (B1a): as Task 1 (`account.Require`, `account.IsLoginRequired`, `account.StrictForTest`, `account.SetEnforceFromForTest`, `accounttest.Install` with `LockedNoLogin` and `Dormant`).
- Produces, in `internal/monomind`: `var ErrNoAccountGate error` and `func SetAccountGate(gate func(ctx context.Context) error)` (nil removes the gate), both for `cmd/monoagentcli`; the unexported `requireAccount(ctx context.Context) error`, which `Exec` calls first, and `inTestBinary` (`= testing.Testing`), the one seam `TestRequireAccount` replaces. `Exec(ctx, opts, onEvent)` returns `(nil, <the gate's error>)` before it looks for the monomind binary: `*account.LoginRequiredError` once the `init` of `cmd/monoagentcli` has installed `account.Require`. `(*ActionExecutor).executeDef(action, actionDef)` returns `(nil, *account.LoginRequiredError)` before it touches `ae.startTime`, validates or runs a step; `ExecuteDef` passes it through unchanged (`redactErr` returns an error with nothing to redact as it is).

- [ ] **Step 1: Write the failing tests.** Create `internal/monomind/exec_account_test.go`:

  ```go
  package monomind

  import (
  	"context"
  	"errors"
  	"os"
  	"path/filepath"
  	"runtime"
  	"testing"
  	"time"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  )

  // useAccountGate installs gate as Exec's account gate for one test.
  func useAccountGate(t *testing.T, gate func(context.Context) error) {
  	t.Helper()
  	SetAccountGate(gate)
  	t.Cleanup(func() { SetAccountGate(nil) })
  }

  // TestExecGate: with account.Require as the gate (what cmd/monoagentcli installs),
  // a locked account is refused with the typed error before the monomind binary is
  // even started; every other state runs the turn.
  func TestExecGate(t *testing.T) {
  	if runtime.GOOS == "windows" {
  		t.Skip("fake monomind is a shell script")
  	}
  	for name, c := range map[string]struct {
  		set     func(t *testing.T)
  		refused bool
  	}{
  		"strict, no guard installed": {func(t *testing.T) {
  			account.InstallForTest(t, nil)
  			account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour))
  			account.StrictForTest(t)
  		}, true},
  		"locked, not logged in": {func(t *testing.T) { accounttest.Install(t, accounttest.LockedNoLogin) }, true},
  		"dormant":               {func(t *testing.T) { accounttest.Install(t, accounttest.Dormant) }, false},
  	} {
  		t.Run(name, func(t *testing.T) {
  			c.set(t)
  			useAccountGate(t, account.Require)
  			started := filepath.Join(t.TempDir(), "started")
  			bin := writeInlineFakeBin(t, `echo started > `+started+`
  echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"the answer"}'
  echo '{"v":1,"type":"done","exit_code":0}'
  exit 0
  `)

  			res, err := Exec(context.Background(), ExecOptions{Bin: bin, Runtime: "codex", Prompt: "hi"}, nil)

  			_, statErr := os.Stat(started)
  			ran := statErr == nil
  			if c.refused && (!account.IsLoginRequired(err) || res != nil || ran) {
  				t.Fatalf("res %v, err %v, binary started %v: want nil, *account.LoginRequiredError and no process", res, err, ran)
  			}
  			if !c.refused && (err != nil || res == nil || res.ResultText != "the answer" || !ran) {
  				t.Fatalf("an admitted turn: res %v, err %v, binary started %v", res, err, ran)
  			}
  		})
  	}
  }

  // requireAccount: an installed gate's error comes back as it is; with none
  // installed a test binary is let through (every older suite) and any other
  // process is refused.
  func TestRequireAccount(t *testing.T) {
  	ctx := context.Background()
  	if err := requireAccount(ctx); err != nil {
  		t.Fatalf("no gate in a test binary: %v, want nil", err)
  	}

  	was := inTestBinary
  	inTestBinary = func() bool { return false }
  	t.Cleanup(func() { inTestBinary = was })
  	if err := requireAccount(ctx); !errors.Is(err, ErrNoAccountGate) {
  		t.Fatalf("no gate outside a test binary: %v, want ErrNoAccountGate", err)
  	}

  	boom := errors.New("boom")
  	useAccountGate(t, func(context.Context) error { return boom })
  	if err := requireAccount(ctx); err != boom {
  		t.Fatalf("an installed gate: %v, want its own error", err)
  	}
  }
  ```
  Create `internal/action/executor_account_test.go`:
  ```go
  package action

  import (
  	"testing"
  	"time"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  )

  // TestExecuteDefGate: a locked account is refused with the typed error before
  // the first step runs; every other state runs the action.
  func TestExecuteDefGate(t *testing.T) {
  	for name, c := range map[string]struct {
  		set     func(t *testing.T)
  		refused bool
  	}{
  		"strict, no guard installed": {func(t *testing.T) {
  			account.InstallForTest(t, nil)
  			account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour))
  			account.StrictForTest(t)
  		}, true},
  		"locked, not logged in": {func(t *testing.T) { accounttest.Install(t, accounttest.LockedNoLogin) }, true},
  		"dormant":               {func(t *testing.T) { accounttest.Install(t, accounttest.Dormant) }, false},
  	} {
  		t.Run(name, func(t *testing.T) {
  			c.set(t)
  			ae := newPkgExecutor(nil, nil)
  			def := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{
  				{ID: "save", Type: "set_variable", Variable: "saved", Value: "yes", SideEffect: true},
  			}}

  			res, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)

  			_, ran := ae.execCtx.GetVariable("saved")
  			if c.refused && (!account.IsLoginRequired(err) || res != nil || ran) {
  				t.Fatalf("res %v, err %v, first step ran %v: want nil, *account.LoginRequiredError and no step", res, err, ran)
  			}
  			if !c.refused && (err != nil || !ran) {
  				t.Fatalf("an admitted action: err %v, first step ran %v", err, ran)
  			}
  		})
  	}
  }
  ```
  Create `cmd/monoagentcli/monomind_gate_test.go`:
  ```go
  package main

  import (
  	"context"
  	"path/filepath"
  	"testing"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  	"github.com/monoes/mono-agent/internal/monomind"
  )

  // The binary installs account.Require as monomind's gate (monomind_gate.go), so
  // a locked account stops an agent turn before monomind is even looked for. In
  // this test binary Exec would otherwise run: there is no gate and no guard.
  func TestExecIsGatedByTheAccountInTheCLI(t *testing.T) {
  	accounttest.Install(t, accounttest.LockedNoLogin)

  	_, err := monomind.Exec(context.Background(),
  		monomind.ExecOptions{Bin: filepath.Join(t.TempDir(), "never-run"), Runtime: "codex", Prompt: "hi"}, nil)

  	if !account.IsLoginRequired(err) {
  		t.Fatalf("err = %v, want *account.LoginRequiredError", err)
  	}
  }
  ```
  (`writeInlineFakeBin` is `internal/monomind/exec_test.go:655`; `newPkgExecutor` is `internal/action/core_pkg_fakes_test.go:122`.)

- [ ] **Step 2: Run them and see them fail.**
  ```
  go test ./internal/monomind/ -run '^(TestExecGate|TestRequireAccount)$' -count=1
  go test ./internal/action/ -run '^TestExecuteDefGate$' -count=1
  go test ./cmd/monoagentcli/ -run '^TestExecIsGatedByTheAccountInTheCLI$' -count=1
  ```
  Expected: the first does not build (`undefined: SetAccountGate`, `undefined: requireAccount`, `undefined: inTestBinary`); the second `FAIL`s in the rows `strict, no guard installed` and `locked, not logged in` with `err <nil>, first step ran true: want nil, *account.LoginRequiredError and no step` (the action ran); the third `FAIL`s with `err = start monomind: fork/exec …/never-run: no such file or directory, want *account.LoginRequiredError` (no gate is installed, so the turn went on to look for the binary).

- [ ] **Step 3: Implement the gates.** Create `internal/monomind/account_gate.go`:

  ```go
  package monomind

  import (
  	"context"
  	"errors"
  	"sync/atomic"
  	"testing"
  )

  // ErrNoAccountGate is what Exec returns in a process that never installed an
  // account gate. It refuses even before the enforcement date, so a binary that
  // starts running turns without the gate fails at once instead of running
  // unguarded.
  var ErrNoAccountGate = errors.New("monomind: no monoes.me account gate is installed in this process")

  // accountGate is what Exec asks before it starts a turn (spec section 6.2). It is
  // a hook and not an import of internal/account on purpose: internal/storage
  // imports this package, internal/secrets' tests import internal/storage, and
  // internal/account imports internal/secrets, so an import here closes a cycle
  // that `go vet ./internal/secrets/` rejects. cmd/monoagentcli installs
  // account.Require when it starts (cmd/monoagentcli/monomind_gate.go).
  var accountGate atomic.Pointer[func(context.Context) error]

  // SetAccountGate installs the gate Exec asks; nil removes it.
  func SetAccountGate(gate func(ctx context.Context) error) {
  	if gate == nil {
  		accountGate.Store(nil)
  		return
  	}
  	accountGate.Store(&gate)
  }

  // inTestBinary is testing.Testing; a test replaces it to see the default-deny.
  var inTestBinary = testing.Testing

  // requireAccount is Exec's first statement. With no gate installed it refuses,
  // except in a test binary, where the suites that never heard of the account run
  // as they always did.
  func requireAccount(ctx context.Context) error {
  	if gate := accountGate.Load(); gate != nil {
  		return (*gate)(ctx)
  	}
  	if inTestBinary() {
  		return nil
  	}
  	return ErrNoAccountGate
  }
  ```
  In `internal/monomind/exec.go` extend the doc comment of `Exec` with its last paragraph and make the check the first statement (no import change; the file does not import `internal/account`):
  ```go
  // escalates to a process-group kill after a grace window so neither
  // monomind nor an agent-CLI grandchild survives the caller.
  //
  // A locked monoes.me account runs no turn: Exec returns the account gate's
  // error (the typed *account.LoginRequiredError) before it looks for monomind
  // (spec section 6.2).
  func Exec(ctx context.Context, opts ExecOptions, onEvent func(Event)) (*TurnResult, error) {
  	if lrErr := requireAccount(ctx); lrErr != nil {
  		return nil, lrErr
  	}
  	bin := opts.Bin
  ```
  In `internal/action/executor.go` add `"github.com/monoes/mono-agent/internal/account"` before the `internal/browser` import and make the check the first statement:
  ```go
  func (ae *ActionExecutor) executeDef(action *StorageAction, actionDef *ActionDef) (*ExecutionResult, error) {
  	// A locked monoes.me account drives no browser (spec section 6.2): node run,
  	// login, crawl, capture, apply and every other direct action path end here.
  	if lrErr := account.Require(ae.ctx); lrErr != nil {
  		return nil, lrErr
  	}
  	ae.startTime = time.Now()
  ```
  Create `cmd/monoagentcli/monomind_gate.go`:
  ```go
  package main

  import (
  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/monomind"
  )

  // monomind.Exec asks a gate before it starts an agent turn, and the gate is
  // account.Require: monomind cannot import internal/account without a test-only
  // import cycle (see internal/monomind/account_gate.go), so the binary that has
  // both installs it, once, before anything runs. A guard installed later (run())
  // is what account.Require reads at each call.
  func init() { monomind.SetAccountGate(account.Require) }
  ```

- [ ] **Step 4: Run them and see them pass.** The three commands of step 2, the first with `-race` added (`TestRequireAccount` replaces a package variable). Expected: `ok  	github.com/monoes/mono-agent/internal/monomind`, `ok  	github.com/monoes/mono-agent/internal/action` and `ok  	github.com/monoes/mono-agent/cmd/monoagentcli`.

- [ ] **Step 5: The cycle is gone and the existing suites still pass.** None of them installs a guard (every `Exec` test passes `context.Background()` and a fake `Bin`; every `internal/action` test builds `NewActionExecutor(context.Background(), …)`).
  ```
  go vet ./internal/secrets/ ./internal/monomind/ ./cmd/monoagentcli/
  go test ./internal/monomind/ ./internal/action/ -count=1
  ```
  Expected: no output from `go vet` (had `exec.go` imported `internal/account`, it would stop on `internal/secrets` with `import cycle not allowed in test`); `ok  	github.com/monoes/mono-agent/internal/action`, and for `internal/monomind` the single failure `TestFindAll_ListsShadowedCopies`, which is on the index §4 known-failure list for pristine master. The whole `cmd/monoagentcli` suite runs with the gate that `init` installs in Task 8 step 6.

- [ ] **Step 6: Commit.**
  ```
  git add internal/monomind/account_gate.go internal/monomind/exec.go internal/monomind/exec_account_test.go internal/action/executor.go internal/action/executor_account_test.go cmd/monoagentcli/monomind_gate.go cmd/monoagentcli/monomind_gate_test.go
  ```
  ```
  git commit -m "feat(account): agent turns and browser actions refuse while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 7: Mutation checks.** For each: apply, run the test, expect the FAIL, `git restore` the file.
  - `perl -pi -e 's/(; lrErr != nil) \{/$1 && false {/' internal/monomind/exec.go`; `go test ./internal/monomind/ -run '^TestExecGate$' -count=1`: the rows `strict, no guard installed` and `locked, not logged in` fail with `err <nil>, binary started true: want nil, *account.LoginRequiredError and no process` (the turn ran and answered).
  - The same perl line on `internal/action/executor.go`; `go test ./internal/action/ -run '^TestExecuteDefGate$' -count=1`: the same two rows fail with `err <nil>, first step ran true`.
  - `perl -pi -e 's/^func init\(\) \{ monomind\.SetAccountGate\(account\.Require\) \}$/func init() { _ = account.Require; _ = monomind.SetAccountGate }/' cmd/monoagentcli/monomind_gate.go`; `go test ./cmd/monoagentcli/ -run '^TestExecIsGatedByTheAccountInTheCLI$' -count=1`: `err = start monomind: fork/exec …/never-run: no such file or directory, want *account.LoginRequiredError` (the binary installs no gate).
  - `perl -pi -e 's/return ErrNoAccountGate/return nil/' internal/monomind/account_gate.go`; `go test ./internal/monomind/ -run '^TestRequireAccount$' -count=1`: `no gate outside a test binary: <nil>, want ErrNoAccountGate`.

  Rerun step 4: PASS.

### Task 5: The serving commands start the guard's refresher at once

`daemon`, `httpapi`, `mcp` and `extension serve` (alias `bridge serve`) are the CLI gate's `serve` class: they pass it when locked and serve for as long as they run, so their guard must not wait the five minutes `run()` (B2) gives every other process. `org serve --foreground` serves in its own process too, but `org serve` is a gated command (refused at start when locked, `--foreground` included) and without `--foreground` a launcher that starts nothing here (`cmd/monoagentcli/org_process.go:28-52`), so its refresher is for a session that locks while it runs. Done in each command's pre-run (the index says "in their own RunE"; a pre-run runs just before it, on the same context, and can be tested without starting a server). Nothing here sets an `OnRefused` handler: cancel-on-refusal is the engine's (Task 3), and the serving processes keep serving (§6.4).

**Files:**
- Create: `cmd/monoagentcli/account_serving.go`
- Modify: `cmd/monoagentcli/daemon.go` (`return c`, line 194), `cmd/monoagentcli/httpapi.go` (`return cmd`, line 130), `cmd/monoagentcli/mcp.go` (`return cmd`, line 119), `cmd/monoagentcli/extension_serve.go` (`newExtensionServeCmd`'s `return cmd`, line 130), `cmd/monoagentcli/org_process.go` (`newOrgServeCmd`'s `return c`, line 65)
- Test: Create `cmd/monoagentcli/account_serving_test.go`

**Interfaces:**
- Consumes (B1a): `func account.Current() *account.Guard` (nil when none is installed) and `func (g *account.Guard) StartRefresher(ctx context.Context)` (idempotent; a no-op while dormant).
- Produces (unexported): `var startServingGuard = func(ctx context.Context)` (starts `account.Current()`'s refresher; a test replaces it), `func servingCommand(c *cobra.Command, when ...func(*cobra.Command) bool) *cobra.Command` (sets `c.PreRun`, returns `c`), `func servesInForeground(cmd *cobra.Command) bool` (`--foreground` and not `--stop`).

- [ ] **Step 1: Write the failing tests.** Create `cmd/monoagentcli/account_serving_test.go`:

  ```go
  package main

  import (
  	"context"
  	"strings"
  	"testing"

  	"github.com/monoes/mono-agent/internal/account"
  )

  // recordServingGuard replaces startServingGuard with a counter for one test.
  func recordServingGuard(t *testing.T) *int {
  	t.Helper()
  	var started int
  	old := startServingGuard
  	startServingGuard = func(context.Context) { started++ }
  	t.Cleanup(func() { startServingGuard = old })
  	return &started
  }

  // TestServingCommandsStartTheGuardRefresher: the four commands that serve for
  // as long as they run start the refresher before their RunE does anything.
  func TestServingCommandsStartTheGuardRefresher(t *testing.T) {
  	started := recordServingGuard(t)
  	root := newRootCmd()
  	for _, path := range [][]string{{"daemon"}, {"httpapi"}, {"mcp"}, {"extension", "serve"}} {
  		name := strings.Join(path, " ")
  		cmd, _, err := root.Find(path)
  		if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
  			t.Fatalf("%s: Find = %v, %v", name, cmd, err)
  		}
  		if cmd.PreRunE != nil {
  			t.Errorf("%s has a PreRunE, which would shadow the PreRun that starts the guard", name)
  		}
  		if cmd.PreRun == nil {
  			t.Errorf("%s does not start the guard's refresher: no PreRun", name)
  			continue
  		}
  		before := *started
  		cmd.PreRun(cmd, nil)
  		if *started != before+1 {
  			t.Errorf("%s: its pre-run started the refresher %d times, want 1", name, *started-before)
  		}
  	}
  }

  // TestOrgServeStartsTheGuardOnlyWhenItServes: `org serve` without --foreground
  // spawns a background process and exits, and --stop stops one; neither serves.
  func TestOrgServeStartsTheGuardOnlyWhenItServes(t *testing.T) {
  	started := recordServingGuard(t)
  	cmd, _, err := newRootCmd().Find([]string{"org", "serve"})
  	if err != nil || cmd == nil || cmd.PreRun == nil {
  		t.Fatalf("org serve: Find = %v, %v (PreRun set: %v)", cmd, err, cmd != nil && cmd.PreRun != nil)
  	}
  	for _, c := range []struct {
  		foreground, stop string
  		want             int
  	}{
  		{"true", "false", 1},  // org serve --foreground
  		{"false", "false", 0}, // org serve
  		{"true", "true", 0},   // org serve --foreground --stop
  		{"false", "true", 0},  // org serve --stop
  	} {
  		_ = cmd.Flags().Set("foreground", c.foreground)
  		_ = cmd.Flags().Set("stop", c.stop)
  		before := *started
  		cmd.PreRun(cmd, nil)
  		if got := *started - before; got != c.want {
  			t.Errorf("foreground=%s stop=%s: started the refresher %d times, want %d", c.foreground, c.stop, got, c.want)
  		}
  	}
  }

  // With no guard installed (a test, or a process nothing guards) there is
  // nothing to start, and a nil context is not a crash.
  func TestStartServingGuardWithNoGuardDoesNothing(t *testing.T) {
  	account.InstallForTest(t, nil)
  	startServingGuard(context.Background())
  	startServingGuard(nil)
  }
  ```

- [ ] **Step 2: Run them and see them fail.**
  ```
  go test ./cmd/monoagentcli/ -run '^(TestServingCommandsStartTheGuardRefresher|TestOrgServeStartsTheGuardOnlyWhenItServes|TestStartServingGuardWithNoGuardDoesNothing)$' -count=1
  ```
  Expected: `FAIL … [build failed]` with `undefined: startServingGuard`.

- [ ] **Step 3: Create the helper.** `cmd/monoagentcli/account_serving.go`:

  ```go
  package main

  import (
  	"context"

  	"github.com/spf13/cobra"

  	"github.com/monoes/mono-agent/internal/account"
  )

  // startServingGuard starts the refresher of the process guard at once. A process
  // that serves (daemon, httpapi, mcp, extension serve, org serve --foreground)
  // must not wait the account.LateRefresher that every other process waits (spec
  // section 6.4): a daemon that sat out its first minutes could not notice a
  // refusal in time. With no guard installed there is nothing to start. A test
  // replaces it.
  var startServingGuard = func(ctx context.Context) {
  	g := account.Current()
  	if g == nil {
  		return
  	}
  	if ctx == nil {
  		ctx = context.Background()
  	}
  	g.StartRefresher(ctx)
  }

  // servingCommand marks c as a command that serves for as long as it runs: its pre-run
  // starts the account guard's refresher. when, if given, narrows that to the
  // invocations that really serve (`org serve` without --foreground starts a
  // background process and exits). The command's own PreRun, if any, still runs.
  func servingCommand(c *cobra.Command, when ...func(*cobra.Command) bool) *cobra.Command {
  	prev := c.PreRun
  	c.PreRun = func(cmd *cobra.Command, args []string) {
  		if len(when) == 0 || when[0](cmd) {
  			startServingGuard(cmd.Context())
  		}
  		if prev != nil {
  			prev(cmd, args)
  		}
  	}
  	return c
  }

  // servesInForeground is servingCommand's predicate for `org serve`: only
  // `--foreground` (and not `--stop`) keeps this process alive to serve.
  func servesInForeground(cmd *cobra.Command) bool {
  	foreground, _ := cmd.Flags().GetBool("foreground")
  	stop, _ := cmd.Flags().GetBool("stop")
  	return foreground && !stop
  }
  ```

- [ ] **Step 4: Mark the five commands.** Change each constructor's final `return`:
  - `daemon.go`: `return c` (after `c.AddCommand(newDaemonInstallCmd(), newDaemonUninstallCmd(), newDaemonRestartCmd(cfg))`) becomes `return servingCommand(c)`.
  - `httpapi.go`: the last `return cmd` of `newHTTPAPICmd` becomes `return servingCommand(cmd)`.
  - `mcp.go`: the `return cmd` before `var runMCP = mcp.Run` becomes `return servingCommand(cmd)`.
  - `extension_serve.go`: the `return cmd` of `newExtensionServeCmd` becomes `return servingCommand(cmd)`.
  - `org_process.go`: the `return c` of `newOrgServeCmd` (line 65, after the `--stop` flag) becomes `return servingCommand(c, servesInForeground)`.

- [ ] **Step 5: Run them and see them pass.** The command of step 2. Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli`.

- [ ] **Step 6: The command tree still builds and the other CLI tests that read it still pass.**
  ```
  go vet ./cmd/monoagentcli/
  go test ./cmd/monoagentcli/ -run '^(TestMCPCommandHandsTheOperatorsSwitchesToTheServer|TestMCPCommandHandsAPIOnlyToTheServer|TestDaemonRestartJSON|TestExtensionServe_BindsAndReportsWhereItIs)$' -count=1
  ```
  Expected: no vet output; `ok  	github.com/monoes/mono-agent/cmd/monoagentcli`. The full CLI suite runs in Task 8.

- [ ] **Step 7: Commit.**
  ```
  git add cmd/monoagentcli/account_serving.go cmd/monoagentcli/account_serving_test.go cmd/monoagentcli/daemon.go cmd/monoagentcli/httpapi.go cmd/monoagentcli/mcp.go cmd/monoagentcli/extension_serve.go cmd/monoagentcli/org_process.go
  ```
  ```
  git commit -m "feat(account): serving commands start the guard's refresher at once" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 8: Mutation checks, one per command.** Each turns a command back into an unmarked one: `perl -pi -e 's/return servingCommand\(c\)/return c/' cmd/monoagentcli/daemon.go`, then `go test ./cmd/monoagentcli/ -run '^TestServingCommandsStartTheGuardRefresher$' -count=1`, expect FAIL `daemon does not start the guard's refresher: no PreRun`, then `git restore` the file. The same for `httpapi.go`, `mcp.go` and `extension_serve.go` (`s/return servingCommand\(cmd\)/return cmd/`; the message names `httpapi`, `mcp`, `extension serve`) and for `org_process.go` (`s/return servingCommand\(c, servesInForeground\)/return c/`, test `'^TestOrgServeStartsTheGuardOnlyWhenItServes$'`).

### Task 6: The heartbeat reports the account, and automatic re-validation waits while locked

`daemonhb.Heartbeat` gains `Account *AccountState` (index §3.4 item 4, with the `enforced` key of spec A4); the daemon fills it before every write, so the desktop and `doctor` see a lock within the 10-second heartbeat interval. The daemon's automatic re-validation (off by default, a paid model call per check) reports itself busy while locked: the validators call `monomind.AgentTest`, not `Exec`, so Task 4's gate would not stop them, and a refused check would be stored as a failed validation (see Decisions).

**Files:**
- Modify: `internal/daemonhb/heartbeat.go` (struct `Heartbeat`, ends at line 48; add the type after it)
- Create: `cmd/monoagentcli/daemon_account_status.go`
- Modify: `cmd/monoagentcli/daemon.go` (the `daemonhb.RunWith(…)` call, lines 162-163), `cmd/monoagentcli/agent_auto_revalidate.go` (`newAutoRevalidator`, line 110)
- Test: Create `internal/daemonhb/account_test.go`, `cmd/monoagentcli/daemon_account_status_test.go`

**Interfaces:**
- Consumes (B1a): `func account.CurrentStatus() account.Status` (nil-safe, no I/O), `(account.Status).Allowed() bool`, the `Status` fields `State`, `Reason`, `ValidUntil`, `EnforceFrom`, `Enforced`; `accounttest.Install` with `LockedNoLogin`, `SignedIn`, `Dormant`; `account.InstallForTest(t, nil)` and `account.SetEnforceFromForTest` for the warn period. From this repo: `heartbeatSchedules` (`daemon.go:266`), `appBusy(ctx, db) (bool, string)` (`agent_auto_revalidate.go:54`).
- Produces: in `internal/daemonhb`, `type AccountState struct{ State string "json:\"state\""; Reason string "json:\"reason,omitempty\""; ValidUntil time.Time "json:\"valid_until,omitzero\""; Enforced bool "json:\"enforced\"" }` and the field `Account *AccountState "json:\"account,omitempty\""` on `Heartbeat` (index §3.4 item 4); in `cmd/monoagentcli` (unexported) `heartbeatAccount(st account.Status) *daemonhb.AccountState` (nil while `st.EnforceFrom` is the zero time), `daemonHeartbeatRefresh(engine *workflow.WorkflowEngine) func(*daemonhb.Heartbeat)` and `busyOrLocked(ctx context.Context, db *sql.DB) (bool, string)` (locked: `true, "the monoes.me account is locked"`; otherwise `appBusy`).

- [ ] **Step 1: Write the failing tests.** Create `internal/daemonhb/account_test.go`:

  ```go
  package daemonhb

  import (
  	"os"
  	"path/filepath"
  	"strings"
  	"testing"
  	"time"
  )

  // A heartbeat carries the account verdict as the desktop and doctor read it: the
  // empty keys are left out, `enforced` is always there (a warn-period "locked",
  // which refuses nothing, differs from a real lock only by it), and with nothing
  // to report there is no account key at all.
  func TestHeartbeatCarriesTheAccount(t *testing.T) {
  	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
  	until := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
  	for _, c := range []struct {
  		account *AccountState
  		want    string // "" means: no account key at all
  	}{
  		{&AccountState{State: "locked", Reason: "expired", ValidUntil: until, Enforced: true}, `"account":{"state":"locked","reason":"expired","valid_until":"2027-01-02T03:04:05Z","enforced":true}`},
  		{&AccountState{State: "locked", Reason: "not_logged_in"}, `"account":{"state":"locked","reason":"not_logged_in","enforced":false}`},
  		{&AccountState{State: "ok", Enforced: true}, `"account":{"state":"ok","enforced":true}`},
  		{nil, ""},
  	} {
  		if err := Write(Heartbeat{PID: os.Getpid(), Account: c.account}); err != nil {
  			t.Fatal(err)
  		}
  		raw, _ := os.ReadFile(Path())
  		if has := strings.Contains(string(raw), `"account"`); has != (c.want != "") || (c.want != "" && !strings.Contains(string(raw), c.want)) {
  			t.Errorf("heartbeat = %s, want it to contain %q", raw, c.want)
  		}
  		hb, _ := Read()
  		got := hb.Account
  		if (got == nil) != (c.account == nil) ||
  			(got != nil && (got.State != c.account.State || got.Reason != c.account.Reason || got.Enforced != c.account.Enforced || !got.ValidUntil.Equal(c.account.ValidUntil))) {
  			t.Errorf("read back %+v, want %+v", got, c.account)
  		}
  	}
  }
  ```
  Create `cmd/monoagentcli/daemon_account_status_test.go`:
  ```go
  package main

  import (
  	"context"
  	"strings"
  	"testing"
  	"time"

  	"github.com/rs/zerolog"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/account/accounttest"
  	"github.com/monoes/mono-agent/internal/daemonhb"
  	"github.com/monoes/mono-agent/internal/storage"
  	"github.com/monoes/mono-agent/internal/workflow"
  )

  // The refresh the daemon runs before every heartbeat write carries the account
  // verdict: locked and enforced is a real lock, locked and not enforced is the
  // warn period (nothing is refused), and a dormant gate says nothing at all.
  func TestDaemonHeartbeatRefreshCarriesTheAccount(t *testing.T) {
  	engine := workflow.NewWorkflowEngineWithStore(nil, nil, nil, workflow.NewNodeTypeRegistry(),
  		workflow.EngineConfig{WebhookAddr: "127.0.0.1:0"}, zerolog.Nop())
  	refresh := daemonHeartbeatRefresh(engine)
  	var hb daemonhb.Heartbeat

  	accounttest.Install(t, accounttest.LockedNoLogin)
  	refresh(&hb)
  	if hb.Account == nil || hb.Account.State != "locked" || hb.Account.Reason != "not_logged_in" || !hb.Account.Enforced {
  		t.Fatalf("locked: Account = %+v, want locked / not_logged_in / enforced", hb.Account)
  	}

  	account.InstallForTest(t, nil)
  	account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
  	refresh(&hb)
  	if hb.Account == nil || hb.Account.State != "locked" || hb.Account.Reason != "not_logged_in" || hb.Account.Enforced {
  		t.Fatalf("warn period: Account = %+v, want locked / not_logged_in / not enforced", hb.Account)
  	}

  	accounttest.Install(t, accounttest.Dormant)
  	refresh(&hb)
  	if hb.Account != nil {
  		t.Fatalf("dormant: Account = %+v, want nil", hb.Account)
  	}
  }

  // busyOrLocked: a locked account is busy, with a reason that names it; every
  // other state (dormant included) leaves the usual check, which an empty
  // database passes.
  func TestAutoRevalidationWaitsWhileTheAccountIsLocked(t *testing.T) {
  	db, err := storage.NewDatabase(t.TempDir() + "/auto.db")
  	if err != nil {
  		t.Fatal(err)
  	}
  	defer db.Close()
  	if err := db.ApplyMigrations(); err != nil {
  		t.Fatal(err)
  	}
  	for name, c := range map[string]struct {
  		mode accounttest.Mode
  		busy bool
  	}{"locked": {accounttest.LockedNoLogin, true}, "signed in": {accounttest.SignedIn, false}, "dormant": {accounttest.Dormant, false}} {
  		accounttest.Install(t, c.mode)
  		busy, why := busyOrLocked(context.Background(), db.DB)
  		if busy != c.busy || (busy && !strings.Contains(why, "monoes.me account is locked")) {
  			t.Errorf("%s: busy = %v (%q), want %v", name, busy, why, c.busy)
  		}
  	}
  }
  ```

- [ ] **Step 2: Run them and see them fail.**
  ```
  go test ./internal/daemonhb/ -run '^TestHeartbeatCarriesTheAccount$' -count=1
  go test ./cmd/monoagentcli/ -run '^(TestDaemonHeartbeatRefreshCarriesTheAccount|TestAutoRevalidationWaitsWhileTheAccountIsLocked)$' -count=1
  ```
  Expected: both `FAIL … [build failed]`: `unknown field Account in struct literal of type Heartbeat` and `undefined: AccountState` (daemonhb); `undefined: daemonHeartbeatRefresh` and `undefined: busyOrLocked` (cmd).

- [ ] **Step 3: Add the field.** In `internal/daemonhb/heartbeat.go`, after the `Schedules` field (line 47) and before the closing brace of `Heartbeat`:
  ```go
  	// Account is the monoes.me account verdict, refreshed on every write. It is
  	// absent while the account gate is dormant, and in a daemon that predates it.
  	Account *AccountState `json:"account,omitempty"`
  ```
  and after the struct:
  ```go
  // AccountState is the account verdict a heartbeat carries: ok, grace or locked,
  // and why. It repeats the fields of internal/account's Status that a reader of
  // this file needs, so that this package depends on nothing else of the repository.
  type AccountState struct {
  	State      string    `json:"state"`
  	Reason     string    `json:"reason,omitempty"`
  	ValidUntil time.Time `json:"valid_until,omitzero"`
  	// Enforced is false in the warn period: the state can say locked while
  	// nothing is refused yet.
  	Enforced bool `json:"enforced"`
  }
  ```

- [ ] **Step 4: Create the status helpers.** `cmd/monoagentcli/daemon_account_status.go`:

  ```go
  package main

  import (
  	"context"
  	"database/sql"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/daemonhb"
  	"github.com/monoes/mono-agent/internal/workflow"
  )

  // heartbeatAccount is the account verdict as the heartbeat file carries it. It
  // is nil while the gate is dormant (no enforcement date: nothing to report, D22).
  func heartbeatAccount(st account.Status) *daemonhb.AccountState {
  	if st.EnforceFrom.IsZero() {
  		return nil
  	}
  	return &daemonhb.AccountState{State: string(st.State), Reason: string(st.Reason), ValidUntil: st.ValidUntil, Enforced: st.Enforced}
  }

  // daemonHeartbeatRefresh is what the daemon updates in its heartbeat before every
  // write: the schedules, and the account verdict, so that the desktop and doctor
  // see "locked" within one heartbeat interval.
  func daemonHeartbeatRefresh(engine *workflow.WorkflowEngine) func(*daemonhb.Heartbeat) {
  	return func(hb *daemonhb.Heartbeat) {
  		hb.Schedules = heartbeatSchedules(engine.ScheduledRuns())
  		hb.Account = heartbeatAccount(account.CurrentStatus())
  	}
  }

  // busyOrLocked is the Busy check of the daemon's automatic re-validation with
  // the account folded in. A locked account starts nothing, and a model check that
  // was refused would be stored as a failed validation, so the scheduler waits.
  func busyOrLocked(ctx context.Context, db *sql.DB) (bool, string) {
  	if !account.CurrentStatus().Allowed() {
  		return true, "the monoes.me account is locked"
  	}
  	return appBusy(ctx, db)
  }
  ```

- [ ] **Step 5: Wire them.** In `cmd/monoagentcli/daemon.go` replace
  ```go
  			go daemonhb.RunWith(ctx, apiRT.heartbeat(servingAddr, bridgeServingAddr, v1ServingAddr),
  				func(hb *daemonhb.Heartbeat) { hb.Schedules = heartbeatSchedules(engine.ScheduledRuns()) })
  ```
  with
  ```go
  			go daemonhb.RunWith(ctx, apiRT.heartbeat(servingAddr, bridgeServingAddr, v1ServingAddr),
  				daemonHeartbeatRefresh(engine))
  ```
  and in `cmd/monoagentcli/agent_auto_revalidate.go` change the one line of `newAutoRevalidator`:
  ```go
  		Busy: func(ctx context.Context) (bool, string) { return busyOrLocked(ctx, db) },
  ```

- [ ] **Step 6: Run them and see them pass.**
  ```
  go test ./internal/daemonhb/ -count=1
  go test ./cmd/monoagentcli/ -run '^(TestDaemonHeartbeatRefreshCarriesTheAccount|TestAutoRevalidationWaitsWhileTheAccountIsLocked)$' -count=1
  ```
  Expected: both `ok` (the second takes about a second once built: it applies the database migrations).

- [ ] **Step 7: Commit.**
  ```
  git add internal/daemonhb/heartbeat.go internal/daemonhb/account_test.go cmd/monoagentcli/daemon_account_status.go cmd/monoagentcli/daemon_account_status_test.go cmd/monoagentcli/daemon.go cmd/monoagentcli/agent_auto_revalidate.go
  ```
  ```
  git commit -m "feat(account): the daemon heartbeat reports the account, and re-validation waits while locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 8: Mutation checks** (FILE is `cmd/monoagentcli/daemon_account_status.go`; `git restore FILE` after each).
  - `perl -ni -e 'print unless /hb\.Account = heartbeatAccount/' FILE`; `'^TestDaemonHeartbeatRefreshCarriesTheAccount$'`: `locked: Account = <nil>, want locked / not_logged_in / enforced`.
  - `perl -pi -e 's/if st\.EnforceFrom\.IsZero\(\) \{/if false \&\& st.EnforceFrom.IsZero() {/' FILE`; the same test: `dormant: Account = &{State:locked …}, want nil`.
  - `perl -pi -e 's/, Enforced: st\.Enforced//' FILE`; the same test: `locked: Account = &{State:locked Reason:not_logged_in … Enforced:false}, want locked / not_logged_in / enforced`.
  - `perl -pi -e 's/if !account\.CurrentStatus\(\)\.Allowed\(\) \{/if false \&\& !account.CurrentStatus().Allowed() {/' FILE`; `'^TestAutoRevalidationWaitsWhileTheAccountIsLocked$'`: `locked: busy = false (""), want true`.

### Task 7: A locked daemon stops its org services and resumes by itself

D8 and §6.4, exactly. The daemon never exits because of a lock (launchd's KeepAlive would respawn it in a loop). Locked, it starts no org services (a daemon that starts locked never starts them) and stops the ones it started, logs once the exact command that ends the lock, and polls `account.CurrentStatus()` every `account.PollInterval`; when the verdict is allowed again it starts them again. A refusal also cancels what the engine is running, once per lock (the engine's own handler, Task 3, usually got there first; both are idempotent, and this one needs no callback). Everything else the daemon starts stays up and refuses by itself: the engine (Tasks 1 to 3), the HTTP API, `/v1`, the webhook server and the bridge (B3b), the heartbeat (Task 6).

**Files:**
- Modify: `cmd/monoagentcli/daemon_org.go` (imports, lines 3-23; struct `orgServices`, lines 54-67; `start`, lines 101, 102, 115 and 125)
- Modify: `cmd/monoagentcli/daemon_org_watch.go` (`watchOrgFiles`, the goroutine at lines 32-44)
- Create: `cmd/monoagentcli/daemon_account.go`
- Modify: `cmd/monoagentcli/daemon.go` (imports, line 16; `orgs.start(ctx, engine)`, line 164; `msg := …`, line 169)
- Test: Create `cmd/monoagentcli/daemon_account_test.go`

**Interfaces:**
- Consumes: `func (e *WorkflowEngine) CancelRunning() int` (Task 3); from B1a `account.CurrentStatus()`, `account.PollInterval` (5 s), `(account.Status).Allowed()`, `account.ReasonRefused`, `account.ReasonKeyUnknown`, `account.ReasonExpired`, `account.ReasonNotLoggedIn`, `account.StateOK`, `account.StateLocked`.
- Produces (unexported): `(*orgServices).goTracked(fn func())` (like `go fn()`, counted) and `(*orgServices).wait(timeout time.Duration) bool` (every goroutine `start` launched has ended); `const orgStopWait = 30 * time.Second`; `type accountSupervisor struct{ status func() account.Status; poll time.Duration; startOrg func(ctx context.Context); waitOrg func(timeout time.Duration) bool; cancelRunning func() int; logf func(format string, args ...any) }`; `newAccountSupervisor(orgs *orgServices, engine *workflow.WorkflowEngine) *accountSupervisor`; `(*accountSupervisor).run(ctx context.Context)` (returns only when `ctx` ends); `daemonLockedLine(st account.Status) string`; `daemonRunningLine(st account.Status) string`.

- [ ] **Step 1: Write the failing tests.** Create `cmd/monoagentcli/daemon_account_test.go`:

  ```go
  package main

  import (
  	"context"
  	"fmt"
  	"strings"
  	"sync"
  	"sync/atomic"
  	"testing"
  	"time"

  	"github.com/rs/zerolog"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/storage"
  	"github.com/monoes/mono-agent/internal/workflow"
  )

  var supOK = account.Status{State: account.StateOK, Enforced: true}

  func supLocked(r account.Reason) account.Status {
  	return account.Status{State: account.StateLocked, Reason: r, Enforced: true}
  }

  // supRig is an accountSupervisor over fakes, running until its test ends.
  type supRig struct {
  	verdict atomic.Pointer[account.Status]
  	done    chan struct{} // closed when run returns

  	mu      sync.Mutex
  	gens    []context.Context // one per start of the org services
  	cancels int               // calls to cancelRunning
  	logs    []string
  	drained bool // what waitOrg answers
  }

  func (r *supRig) with(f func()) { r.mu.Lock(); defer r.mu.Unlock(); f() }

  func newSupRig(t *testing.T, first account.Status) *supRig {
  	t.Helper()
  	r := &supRig{drained: true, done: make(chan struct{})}
  	r.set(first)
  	sup := &accountSupervisor{
  		status:        func() account.Status { return *r.verdict.Load() },
  		poll:          5 * time.Millisecond,
  		startOrg:      func(ctx context.Context) { r.with(func() { r.gens = append(r.gens, ctx) }) },
  		waitOrg:       func(time.Duration) (ended bool) { r.with(func() { ended = r.drained }); return ended },
  		cancelRunning: func() int { r.with(func() { r.cancels++ }); return 2 },
  		logf:          func(f string, a ...any) { r.with(func() { r.logs = append(r.logs, fmt.Sprintf(f, a...)) }) },
  	}
  	ctx, cancel := context.WithCancel(context.Background())
  	go func() { sup.run(ctx); close(r.done) }()
  	t.Cleanup(func() { cancel(); <-r.done })
  	return r
  }

  func (r *supRig) set(st account.Status) { r.verdict.Store(&st) }

  // state: how many times the org services were started, whether the newest start
  // is still live, and how many times the running executions were cancelled.
  func (r *supRig) state() (starts int, live bool, cancels int) {
  	r.with(func() {
  		starts, cancels = len(r.gens), r.cancels
  		live = starts > 0 && r.gens[starts-1].Err() == nil
  	})
  	return
  }

  func (r *supRig) logged(substr string) (n int) {
  	r.with(func() {
  		for _, l := range r.logs {
  			if strings.Contains(l, substr) {
  				n++
  			}
  		}
  	})
  	return n
  }

  func supUntil(t *testing.T, what string, cond func() bool) {
  	t.Helper()
  	for deadline := time.Now().Add(2 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
  		if time.Now().After(deadline) {
  			t.Fatalf("timed out waiting for: %s", what)
  		}
  	}
  }

  // supSettle gives the supervisor several polls to do something it should not.
  func supSettle() { time.Sleep(60 * time.Millisecond) }

  // The supervisor follows the account: it starts the org services while the
  // account is valid; at the 24 hours (expired) it stops them, cancels nothing
  // (the engine ends each run at its next node), logs once and does not end the
  // daemon; at the next sign-in it starts them again; on a refusal it stops them
  // and cancels what the engine is running, once per lock.
  func TestSupervisorFollowsTheAccount(t *testing.T) {
  	rig := newSupRig(t, supOK)
  	supUntil(t, "the org services start", func() bool { n, live, _ := rig.state(); return n == 1 && live })

  	rig.set(supLocked(account.ReasonExpired))
  	supUntil(t, "the lock is logged", func() bool { return rig.logged("account is locked (expired)") > 0 })
  	supSettle()
  	if n, live, c := rig.state(); live || c != 0 || n != 1 || rig.logged("account is locked (expired)") != 1 {
  		t.Fatalf("org services live %v, %d cancels and %d lock lines at expiry, want stopped, 0 and 1", live, c, rig.logged("account is locked (expired)"))
  	}
  	select {
  	case <-rig.done:
  		t.Fatal("the supervisor returned because of the lock")
  	default:
  	}

  	rig.set(supOK) // a sign-in from the CLI or the app
  	supUntil(t, "the org services start again", func() bool { n, live, _ := rig.state(); return n == 2 && live })

  	rig.set(supLocked(account.ReasonRefused))
  	supUntil(t, "the refusal is handled", func() bool { return rig.logged("cancelled 2 running execution") > 0 })
  	supSettle()
  	if _, live, c := rig.state(); live || c != 1 || rig.logged("cancelled 2 running execution") != 1 {
  		t.Fatalf("org services live %v, %d cancels and %d log lines across many polls of one refusal, want stopped, 1 and 1", live, c, rig.logged("cancelled 2 running execution"))
  	}

  	rig.set(supOK)
  	supUntil(t, "the org services start again", func() bool { n, live, _ := rig.state(); return n == 3 && live })
  	rig.set(supLocked(account.ReasonRefused))
  	supUntil(t, "the second refusal cancels again", func() bool { _, _, c := rig.state(); return c == 2 })
  }

  // A daemon that is locked when it starts never starts the org services and says
  // how to fix it; it starts them by itself when a valid session appears.
  func TestSupervisorStartedLockedWaitsForTheLogin(t *testing.T) {
  	rig := newSupRig(t, supLocked(account.ReasonNotLoggedIn))
  	supUntil(t, "the lock is logged with its command", func() bool { return rig.logged("run: monoagentcli account login") == 1 })
  	supSettle()
  	if n, _, c := rig.state(); n != 0 || c != 0 {
  		t.Fatalf("%d starts and %d cancels while locked at start, want none", n, c)
  	}

  	rig.set(supOK)
  	supUntil(t, "the org services start", func() bool { n, live, _ := rig.state(); return n == 1 && live })
  }

  // Before the enforcement date a status that says locked is still allowed: the
  // org services start, once, and keep running.
  func TestSupervisorLeavesTheOrgServicesAloneBeforeTheEnforcementDate(t *testing.T) {
  	rig := newSupRig(t, account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn})
  	supUntil(t, "the org services start", func() bool { n, _, _ := rig.state(); return n == 1 })
  	supSettle()
  	if n, live, c := rig.state(); n != 1 || !live || c != 0 || rig.logged("locked") != 0 {
  		t.Fatalf("%d starts, live %v, %d cancels, %d lock lines: want one live start and nothing said", n, live, c, rig.logged("locked"))
  	}
  }

  // A start after a stop waits until the previous org services have ended, so two
  // generations never overlap.
  func TestSupervisorWaitsForTheOldOrgServicesBeforeStartingNew(t *testing.T) {
  	rig := newSupRig(t, supOK)
  	supUntil(t, "the org services start", func() bool { _, live, _ := rig.state(); return live })
  	setDrained := func(v bool) { rig.with(func() { rig.drained = v }) }

  	setDrained(false) // the old services are still winding down
  	rig.set(supLocked(account.ReasonExpired))
  	supUntil(t, "the org services stop", func() bool { _, live, _ := rig.state(); return !live })
  	rig.set(supOK)
  	supSettle()
  	if n, _, _ := rig.state(); n != 1 {
  		t.Fatalf("%d starts while the old services are still ending, want 1", n)
  	}

  	setDrained(true)
  	supUntil(t, "the new generation starts", func() bool { n, live, _ := rig.state(); return n == 2 && live })
  }

  // What the daemon says: the lock line names the command that ends it (update,
  // for an unknown key), and a locked daemon does not claim live triggers.
  func TestDaemonAccountTexts(t *testing.T) {
  	if got := daemonLockedLine(supLocked(account.ReasonNotLoggedIn)); !strings.Contains(got, "run: monoagentcli account login") || !strings.Contains(got, "--email") {
  		t.Errorf("line = %q, want the login command and its headless form", got)
  	}
  	if got := daemonLockedLine(supLocked(account.ReasonKeyUnknown)); !strings.Contains(got, "run: monoagentcli update") {
  		t.Errorf("line = %q, want the update command", got)
  	}
  	if got := daemonRunningLine(supOK); !strings.Contains(got, "triggers are live") {
  		t.Errorf("line = %q", got)
  	}
  	if got := daemonRunningLine(supLocked(account.ReasonRefused)); strings.Contains(got, "triggers are live") || !strings.Contains(got, "locked") {
  		t.Errorf("line = %q, want it to say the account is locked", got)
  	}
  }

  // The real org services: every goroutine start launches ends with its context,
  // and wait says so, in both generations; that is what lets the supervisor stop
  // them and start them again.
  func TestOrgServicesStopWhenTheirContextEnds(t *testing.T) {
  	db, err := storage.NewDatabase(t.TempDir() + "/org.db")
  	if err != nil {
  		t.Fatal(err)
  	}
  	defer db.Close()
  	if err := db.ApplyMigrations(); err != nil {
  		t.Fatal(err)
  	}
  	engine := workflow.NewWorkflowEngineWithStore(workflow.NewSQLiteWorkflowStore(db.DB), db.DB, nil,
  		workflow.NewNodeTypeRegistry(), workflow.EngineConfig{WebhookAddr: "127.0.0.1:0"}, zerolog.Nop())
  	orgs := newOrgServices(db, engine)
  	orgs.logf = t.Logf
  	orgs.watchInterval = 20 * time.Millisecond

  	for generation := 1; generation <= 2; generation++ {
  		ctx, cancel := context.WithCancel(context.Background())
  		orgs.start(ctx, engine)
  		if orgs.wait(50 * time.Millisecond) {
  			t.Fatalf("generation %d: wait said the services had ended while their context was live", generation)
  		}
  		cancel()
  		if !orgs.wait(10 * time.Second) {
  			t.Fatalf("generation %d: the org services did not end within 10s of their context ending", generation)
  		}
  	}
  }
  ```

- [ ] **Step 2: Run it and see it fail.**
  ```
  go test ./cmd/monoagentcli/ -run '^(TestSupervisorFollowsTheAccount|TestSupervisorStartedLockedWaitsForTheLogin|TestSupervisorLeavesTheOrgServicesAloneBeforeTheEnforcementDate|TestSupervisorWaitsForTheOldOrgServicesBeforeStartingNew|TestDaemonAccountTexts|TestOrgServicesStopWhenTheirContextEnds)$' -count=1
  ```
  Expected: `FAIL … [build failed]` with `undefined: accountSupervisor`, `undefined: daemonLockedLine`, `undefined: daemonRunningLine` and `orgs.wait undefined (type *orgServices has no field or method wait)`.

- [ ] **Step 3: Make the org services stoppable.** In `cmd/monoagentcli/daemon_org.go`: add `"sync/atomic"` to the imports (after `"sync"`); add one field to the end of `orgServices` (after `watchers`):
  ```go
  	active atomic.Int32 // goroutines start launched and not yet ended; see wait
  ```
  and add these two methods after the struct:
  ```go
  // goTracked runs fn on a goroutine that wait can see. (A sync.WaitGroup would
  // do, but the supervisor waits, may give up, and starts again; reusing a
  // WaitGroup while an earlier Wait is still pending is a panic.)
  func (s *orgServices) goTracked(fn func()) {
  	s.active.Add(1)
  	go func() {
  		defer s.active.Add(-1)
  		fn()
  	}()
  }

  // wait reports whether every goroutine start launched has ended, waiting up to
  // timeout for them to. They end when the context start was given ends.
  func (s *orgServices) wait(timeout time.Duration) bool {
  	deadline := time.Now().Add(timeout)
  	for s.active.Load() > 0 {
  		if time.Now().After(deadline) {
  			return false
  		}
  		time.Sleep(10 * time.Millisecond)
  	}
  	return true
  }
  ```
  In `start`, replace the four `go` statements one for one:
  ```go
  	s.goTracked(func() { waker.Run(ctx) })
  	s.goTracked(func() { s.receiver.Run(ctx) })
  ```
  (lines 101 and 102), `s.goTracked(func() { reportUp.Run(ctx) })` for `go reportUp.Run(ctx)` (line 115) and `s.goTracked(func() { svc.Run(ctx) })` for `go svc.Run(ctx)` (line 125). In `cmd/monoagentcli/daemon_org_watch.go` change the goroutine of `watchOrgFiles` from `go func() { … }()` to `s.goTracked(func() { … })`: the opening line (32) becomes `s.goTracked(func() {` and the closing `}()` (44) becomes `})`; the body is untouched.

- [ ] **Step 4: Create the supervisor.** `cmd/monoagentcli/daemon_account.go`:

  ```go
  package main

  import (
  	"context"
  	"fmt"
  	"time"

  	"github.com/monoes/mono-agent/internal/account"
  	"github.com/monoes/mono-agent/internal/workflow"
  )

  // The daemon's locked mode (spec D8, section 6.4). While the monoes.me account is
  // locked the daemon stays up, because launchd's KeepAlive would respawn an exiting
  // one in a loop, and starts nothing: the engine refuses executions and drops
  // triggers by itself (internal/workflow), the doors refuse, and this supervisor
  // stops the org services. It resumes by itself when a valid session appears: a
  // sign-in from the CLI or the app writes session.json and the guard reads it.

  // orgStopWait bounds how long the supervisor waits for the org services to end,
  // when the account locks and before it starts them again. Their loops end with
  // their context; the longest thing in one is an agent turn, killed within seconds.
  const orgStopWait = 30 * time.Second

  // accountSupervisor starts and stops the org services as the account verdict
  // changes, and cancels the running executions when monoes.me refused the account.
  // Its dependencies are fields so that a test can drive it.
  type accountSupervisor struct {
  	status        func() account.Status            // account.CurrentStatus in the daemon
  	poll          time.Duration                    // account.PollInterval in the daemon
  	startOrg      func(ctx context.Context)        // launches the org services; they end when ctx ends
  	waitOrg       func(timeout time.Duration) bool // whether everything startOrg launched has ended
  	cancelRunning func() int                       // cancels the engine's running executions
  	logf          func(format string, args ...any)
  }

  func newAccountSupervisor(orgs *orgServices, engine *workflow.WorkflowEngine) *accountSupervisor {
  	return &accountSupervisor{
  		status:        account.CurrentStatus,
  		poll:          account.PollInterval,
  		startOrg:      func(ctx context.Context) { orgs.start(ctx, engine) },
  		waitOrg:       orgs.wait,
  		cancelRunning: engine.CancelRunning,
  		logf:          orgs.logf,
  	}
  }

  // run applies the verdict at once and then every poll, until ctx ends. It never
  // returns because of a lock. A daemon that starts locked never starts the org
  // services; one that is locked later stops them and starts them again when the
  // account is valid. A refusal (and only a refusal: at the 24 hours, expired, the
  // engine ends each run at its next node) also cancels what the engine is running,
  // once per lock.
  func (s *accountSupervisor) run(ctx context.Context) {
  	var (
  		stop      context.CancelFunc // non-nil while the org services run
  		ran       bool               // the org services ran at some point, so a stop may still be under way
  		reported  bool               // this lock has been logged
  		cancelled bool               // this lock has cancelled the running executions
  	)
  	apply := func(st account.Status) {
  		if st.Allowed() {
  			if reported {
  				s.logf("daemon: the monoes.me account is valid again; the org services start again")
  			}
  			reported, cancelled = false, false
  			if stop != nil {
  				return
  			}
  			if ran && !s.waitOrg(orgStopWait) {
  				s.logf("daemon: the org services are still stopping; they start again at the next check")
  				return
  			}
  			orgCtx, cancel := context.WithCancel(ctx)
  			stop, ran = cancel, true
  			s.startOrg(orgCtx)
  			return
  		}
  		if stop != nil {
  			stop()
  			stop = nil
  			s.waitOrg(orgStopWait)
  		}
  		if !reported {
  			reported = true
  			s.logf("%s", daemonLockedLine(st))
  		}
  		if st.Reason == account.ReasonRefused && !cancelled {
  			cancelled = true
  			if n := s.cancelRunning(); n > 0 {
  				s.logf("daemon: monoes.me refused this account: cancelled %d running execution(s)", n)
  			}
  		}
  	}

  	apply(s.status())
  	t := time.NewTicker(s.poll)
  	defer t.Stop()
  	for {
  		select {
  		case <-ctx.Done():
  			return
  		case <-t.C:
  			apply(s.status())
  		}
  	}
  }

  // daemonLockedLine is what a locked daemon logs, once per lock: why, and the
  // exact command that ends it (spec section 9).
  func daemonLockedLine(st account.Status) string {
  	fix := "run: monoagentcli account login   (without a browser: monoagentcli account login --email <address>)"
  	if st.Reason == account.ReasonKeyUnknown {
  		fix = "run: monoagentcli update"
  	}
  	return fmt.Sprintf("daemon: the monoes.me account is locked (%s): nothing runs and the org services are stopped until a login is valid again; %s", st.Reason, fix)
  }

  // daemonRunningLine is the line the daemon prints once it is up. A locked one
  // says so, since "triggers are live" would be untrue.
  func daemonRunningLine(st account.Status) string {
  	if st.Allowed() {
  		return "Daemon running. Active workflows' triggers are live."
  	}
  	return "Daemon running, but the monoes.me account is locked (" + string(st.Reason) + "): nothing runs until a login is valid again."
  }
  ```

- [ ] **Step 5: Wire the daemon.** In `cmd/monoagentcli/daemon.go`: add `"github.com/monoes/mono-agent/internal/account"` before the `daemonhb` import; replace `orgs.start(ctx, engine)` (the line right after the `go daemonhb.RunWith(…)` call) with
  ```go
  			// The org services run only while the account is valid (daemon_account.go);
  			// the daemon itself never exits because of a lock.
  			go newAccountSupervisor(orgs, engine).run(ctx)
  ```
  and replace `msg := "Daemon running. Active workflows' triggers are live."` with
  ```go
  			msg := daemonRunningLine(account.CurrentStatus())
  ```

- [ ] **Step 6: Run it and see it pass, with the race detector.**
  ```
  go test ./cmd/monoagentcli/ -run '^(TestSupervisorFollowsTheAccount|TestSupervisorStartedLockedWaitsForTheLogin|TestSupervisorLeavesTheOrgServicesAloneBeforeTheEnforcementDate|TestSupervisorWaitsForTheOldOrgServicesBeforeStartingNew|TestDaemonAccountTexts|TestOrgServicesStopWhenTheirContextEnds)$' -count=1 -race
  ```
  Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli` in about 10 s. Then the org-services tests that already exist: `go test ./cmd/monoagentcli/ -run '^TestDaemonWatchersFollowProfiles$' -count=1 -race`: `ok`.

- [ ] **Step 7: Commit.**
  ```
  git add cmd/monoagentcli/daemon_org.go cmd/monoagentcli/daemon_org_watch.go cmd/monoagentcli/daemon_account.go cmd/monoagentcli/daemon_account_test.go cmd/monoagentcli/daemon.go
  ```
  ```
  git commit -m "feat(account): a locked daemon stops its org services and resumes by itself" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 8: Mutation checks** (FILE is `cmd/monoagentcli/daemon_account.go` unless named; `git restore` it after each).
  - `perl -0pi -e 's/\t\t\tstop\(\)\n\t\t\tstop = nil\n/\t\t\tstop = nil\n/' FILE`; `'^TestSupervisorFollowsTheAccount$'`: `org services live true, 0 cancels and 1 lock lines at expiry, want stopped, 0 and 1`.
  - `perl -pi -e 's/if st\.Reason == account\.ReasonRefused && !cancelled \{/if false && st.Reason == account.ReasonRefused && !cancelled {/' FILE`; the same test: `timed out waiting for: the refusal is handled`.
  - `perl -pi -e 's/if st\.Reason == account\.ReasonRefused && !cancelled \{/if !cancelled {/' FILE`; the same test: `org services live false, 1 cancels and 1 lock lines at expiry, want stopped, 0 and 1`.
  - `perl -pi -e 's/if ran && !s\.waitOrg\(orgStopWait\) \{/if false \&\& ran \&\& !s.waitOrg(orgStopWait) {/' FILE`; `'^TestSupervisorWaitsForTheOldOrgServicesBeforeStartingNew$'`: `2 starts while the old services are still ending, want 1`.
  - `perl -pi -e 's/^\t\tif st\.Allowed\(\) \{$/\t\tif true {/' FILE`; `'^TestSupervisorStartedLockedWaitsForTheLogin$'`: `timed out waiting for: the lock is logged with its command`.
  - `perl -pi -e 's/for s\.active\.Load\(\) > 0 \{/for false \&\& s.active.Load() > 0 {/' cmd/monoagentcli/daemon_org.go`; `'^TestOrgServicesStopWhenTheirContextEnds$'`: `generation 1: wait said the services had ended while their context was live`.
  - `perl -pi -e 's/fix := "run: monoagentcli account login   /fix := "run: login   /' FILE`; `'^TestDaemonAccountTexts$'`: `want the login command and its headless form`.

### Task 8: The layer-2 caller survey for the findings file, and the full verification

Spike S4's layer-2 half (which processes call the three gate sites with no guard installed) goes into the findings file, then the whole branch is verified in both builds.

**Files:**
- Modify (append): `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md`. Plan A's first task creates it; if it does not exist in your branch yet, create it with the single line `# Spike findings` first.

**Interfaces:** none.

- [ ] **Step 1: Re-derive the survey.** From the repository root:
  ```
  grep -rn 'monomind\.Exec(' --include='*.go' wails-app | grep -v _test.go
  grep -rn 'TriggerWorkflow\|NewActionExecutor\|ExecuteDef\|NewWorkflowEngine' --include='*.go' wails-app | grep -v _test.go
  grep -ln 'internal/workflow"\|internal/monomind"\|internal/action"' cmd/debug_registry/*.go cmd/inspect/*.go cmd/schemagen/*.go
  grep -rln 'monomind\.Exec(\|\.TriggerWorkflow\|NewActionExecutor(\|\.ExecuteDef(' --include='*.go' . | grep -v _test.go | sort
  ```
  Expected: the first three print nothing. The fourth prints only paths under `cmd/monoagentcli/` and `internal/` (at master `f4441a2a`: 16 files, `cmd/monoagentcli/{agent,automation_fixture_site,chat_coder,chat,workflow}.go` and `internal/{action/executor,action/validate,capturesummary/runner,config/agentgen,httpapi/handlers,mcp/tools,nodes/agent/ask,nodes/browser_adapter,nodes/browserjev/node,recordanalyze/llm,recordanalyze/pageexec}.go`), none under `wails-app/` or another `cmd/` binary. A path from `wails-app/` or another `cmd/` binary is a new unguarded caller: add it to the list below and tell the lead.

  Then the symbol table, which shows which binaries link `Exec` at all (it builds three binaries into a temporary directory and removes them again):
  ```
  B="$(mktemp -d)"
  for p in monoagentcli inspect schemagen; do
    go build -o "$B/$p" ./cmd/$p && printf '%s: ' "$p" && go tool nm "$B/$p" | grep -cE ' github\.com/monoes/mono-agent/internal/monomind\.Exec(\.|$)'
    rm "$B/$p"
  done
  rmdir "$B"
  ```
  Expected: `monoagentcli: ` and a number above zero (23 when this plan was written), `inspect: 0`, `schemagen: 0`. A binary other than `monoagentcli` that prints a non-zero number links `Exec`: it must call `monomind.SetAccountGate(account.Require)` before it runs a turn (without it `Exec` refuses, even before the enforcement date), so add it to the list below and tell the lead. The desktop's Go side is the same check on its own binary (it needs the Wails toolchain, so it is optional here); when this plan was written it linked none of `Exec`, `ActionExecutor.executeDef` and `WorkflowEngine.handleExecution`.

- [ ] **Step 2: Append the findings.** Add this section to the end of the findings file:

  ```markdown
  ## S4, layer 2 (from plan B3a)

  Which processes reach the three layer-2 gate sites (`WorkflowEngine.handleExecution` and the calls that create an execution, `monomind.Exec`, `ActionExecutor.executeDef`), and whether a guard is installed there. The node check of `RunExecution` (plan B3a task 3b, ruling R4) is reached only through `handleExecution` (`WorkflowEngine.runExecution` is its one caller outside tests), so it is covered by the same answer. Surveyed at master `f4441a2a` with the greps of plan B3a task 8.

  - **`monoagentcli`** (every command, so also the daemon, `httpapi`, `mcp`, `mcp --grant`, `extension serve` and, while signed in, `org serve --foreground`, and the engines that `internal/httpapi` and `internal/mcp` start inside those processes) is the only production process that reaches the three sites. `run()` (B2) installs its guard, and the `init` of `cmd/monoagentcli` installs `account.Require` as the gate `monomind.Exec` asks (plan B3a task 4): a binary that links `monomind` and never installs that gate is refused by `Exec`, and none exists.
  - **The desktop's Go side (`wails-app`) reaches none of them.** It calls `monomind.Scan`, `ListModels` and `IngestDocument`, `nodes.BootAutomations` and `workflow.NewWorkflowFileStore`, and runs every execution through a `monoagentcli` subprocess with its own guard. B4's read-only guard is not load-bearing for layer 2. The linker agrees: the desktop's binary (the Go side built with a stub `frontend/dist`), `cmd/inspect` and `cmd/schemagen` link none of `monomind.Exec`, `ActionExecutor.executeDef` and `WorkflowEngine.handleExecution` (`go tool nm`), and `monoagentcli` links all three.
  - **`cmd/debug_registry`, `cmd/inspect`, `cmd/schemagen`** import neither `internal/workflow`, `internal/monomind` nor `internal/action` directly (`schemagen` reaches `internal/workflow` through `internal/tools/schemagen`, for schemas only).
  - **Test binaries** have no guard and `account.Require` fails open there (`testing.Testing()`); `StrictForTest` and `accounttest.Install` switch that off for the gate-site tests.
  - **External `monomind` processes** (an org's agents) call mono-agent only through `monoagentcli mcp --grant`.
  - **Two model calls that do not go through `monomind.Exec`.** `monomind.AgentTest` (`internal/monomind/agenttest.go:62`, called from `internal/agentroster/validate.go:180`) runs `monomind agent test`; it serves `agent validate` (gated at layer 1) and the daemon's automatic re-validation (waits while the account is locked, plan B3a task 6). Spec §3 says validators go through `Exec`; with `agent test --json` available they do not. `registerClaudeCodeProject` (`internal/monomind/profile_init.go:129`) runs one `claude -p` turn; it is reached from `doctor fix` (`cmd/monoagentcli/doctor_env.go:115`), which D6 leaves open. **Capture summaries** (`internal/capturesummary/runner.go:50`) call `Exec` from the bridge's background worker: one processed while the account is locked is recorded as `summary.json` status `error` with the login text (`internal/capturesummary/summarizer.go:218`) and is not retried; the capture itself is untouched.
  ```

- [ ] **Step 3: Commit the findings.**
  ```
  git add docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md
  ```
  ```
  git commit -m "docs(account): S4 layer-2 callers without a guard" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
  ```

- [ ] **Step 4: Format, build and vet, both build variants.**
  ```
  gofmt -l .
  go build ./...
  go vet ./...
  go build -tags nosocial -o /dev/null ./cmd/monoagentcli
  go build -tags devaccount -o /dev/null ./cmd/monoagentcli
  ```
  Expected: no output from any of the five. `go vet ./...` includes `internal/secrets`, where an import of `internal/account` from any package of its test closure (Decisions list) would stop with `import cycle not allowed in test`.

- [ ] **Step 5: The packages this plan touched, with the race detector where the plan has concurrency.**
  ```
  go test ./internal/workflow/ ./internal/daemonhb/ ./internal/action/ -count=1 -race
  go test ./internal/monomind/ -count=1 -race
  ```
  Expected: `ok` for the first three (`internal/workflow` takes about 20 s, several times that under `-race` on a loaded machine); for `internal/monomind` the single failure `TestFindAll_ListsShadowedCopies`, a known failure of pristine master (index §4).

- [ ] **Step 6: This plan's CLI tests, with the race detector, then the whole CLI suite.**
  ```
  go test ./cmd/monoagentcli/ -run '^(TestServingCommandsStartTheGuardRefresher|TestOrgServeStartsTheGuardOnlyWhenItServes|TestStartServingGuardWithNoGuardDoesNothing|TestSupervisorFollowsTheAccount|TestSupervisorStartedLockedWaitsForTheLogin|TestSupervisorLeavesTheOrgServicesAloneBeforeTheEnforcementDate|TestSupervisorWaitsForTheOldOrgServicesBeforeStartingNew|TestDaemonAccountTexts|TestOrgServicesStopWhenTheirContextEnds|TestDaemonHeartbeatRefreshCarriesTheAccount|TestAutoRevalidationWaitsWhileTheAccountIsLocked|TestExecIsGatedByTheAccountInTheCLI)$' -count=1 -race
  go test ./cmd/monoagentcli/ -count=1
  ```
  Expected: `ok` for the first. The second takes several minutes and fails only on tests of the index §4 known-failure list: `TestCaptureTaskFilesOnTheBoard`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCoderConversationFolders` (and the load flake `TestAgentTestGoDeadline`, which passes alone). Any other failure is this plan's until shown otherwise: export `HEAD` with `git archive HEAD | tar -x -C <fresh dir>` and run it there.

- [ ] **Step 7: Confirm the unchanged tests were really unchanged.**
  ```
  git diff --stat <the commit noted in Task 1 step 1>..HEAD -- '*_test.go'
  ```
  Expected: only the new files of Tasks 1 to 7, Task 3b included (`internal/workflow/engine_account_test.go`, `engine_locked_test.go`, `engine_refused_test.go`, `execution_account_test.go`, `internal/monomind/exec_account_test.go`, `internal/action/executor_account_test.go`, `internal/daemonhb/account_test.go`, `cmd/monoagentcli/{monomind_gate,account_serving,daemon_account,daemon_account_status}_test.go`), and a summary line with no `deletions(-)`: no existing test was edited.

## Contract change requests

None open. Both requests this plan made earlier were ruled by the lead, are in the index and the spec now, and the plan implements them as written:

1. `daemonhb.AccountState` has `Enforced bool` with the JSON key `enforced`, always present, set from `account.Status.Enforced` (index §3.4 item 4, spec A4): Task 6.
2. `internal/monomind` and the packages in its import cycle (`internal/daemonhb`, `internal/orgdesign`, `internal/orgsign`, `internal/profiledir`, `internal/shellpath`, `internal/storage`) do not import `internal/account`, and `monomind.Exec` asks the hook `monomind.SetAccountGate`, which `cmd/monoagentcli` installs (index §3.1 exception, spec A15): Task 4. A consequence for other plans' tests: the real `Exec` returns the typed error only in a process that installed the gate, so a test outside `cmd/monoagentcli` that wants it to refuse calls `monomind.SetAccountGate(account.Require)` itself and `monomind.SetAccountGate(nil)` in its cleanup. None does today: B3b's gateway tests inject their own exec function (`TestAnExecRefusedForTheAccountIsA401NotA500` returns the typed error from a fake).

What this plan leaves to others, so it does not implement it: cancelling a one-shot command's context on a refusal (`cancelWhenRefused`, gated commands only) and mapping any `*account.LoginRequiredError` to exit 4 in `exitCodeFor` are B2's (`main.go`, `exitcodes.go`); the JSON fields of a refusal are B1a's `(*LoginRequiredError).JSONErrorFields()`.

Checked against the other plans, so nothing else is asked of them: B1a's plan makes `OnRefused` append, fire on its own goroutine, fire from `Refresh` and from a refusal another process wrote, and call once at once a callback registered while already refused (the engine's handler says nothing when it finds nothing running); B2's `run()` installs the guard (`account.Install`) before cobra runs any `PreRun`, which Task 5 relies on; no other plan edits a file this plan edits, and the new file names and identifiers of `cmd/monoagentcli` here (`monomind_gate.go`, `account_serving.go`, `daemon_account*.go`) clash with none of theirs (B2 owns `account_gate.go` and `account_gate_refusal.go`).
