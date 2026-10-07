# Mandatory monoes.me Account — B5c: the real-binary smoke Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The whole gate runs as shipped, in CI, before release R: the CLI built with `-tags devaccount` is driven as subprocesses against a fake monoes.me, and the spec's acceptance items 1 to 4 are shown with real processes: no session means refused and nothing written but the clock-guard record of a machine that never signed in (A25); a sign-in opens every entry point and every door; a block locks within one refresh and cancels work in flight; an outage is grace for 24 hours, and the node in flight then finishes (a run ends at its next node, ruling R4); trouble that is not a refusal never locks; an unknown signing key is the "run update" case; the warn period locks nothing; a refresh whose answer is lost never ends the account (an interrupted one is completed, a killed one is retried at once inside monoes.me's reuse window and dropped after it, on that one machine); and a machine that was refused once stays enforced when its clock is set back.

**Architecture:** One development-only variable, `MONOAGENT_DEV_ENFORCE_FROM`, moves the enforcement date of a `devaccount` binary, so the smoke can force enforcement or a warn period while production builds are still dormant (the date is the zero time until R). The package `internal/accountsmoke` holds tests only and builds only with the tag: each test is a rig, a throwaway HOME, a fake monoes.me (`libraryfake`) behind a switch, a fake monomind and free ports, around the CLI built once. A real binary has no clock hook, so the smoke ages the stored token instead of the clock. The CI job `account-smoke` runs it. B5c adds no gate code and proves the gates of B1 to B4 from outside.

**Tech Stack:** Go 1.26, `internal/library/libraryfake`, gorilla/websocket and modernc sqlite (both already in `go.mod`), GitHub Actions.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§1 acceptance 1 to 4, §9, §11, D22, D24, D27; §13 A1, A4, A8, A9, A12) and the index `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§1 merge order, §2, §3.4, §3.6, §4). Depends on B1a to B4b. It is test-only, merges before release R and **must pass before R** (R is B5a and `b5b-docs` merged together as one push). Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

## Global Constraints

- Go is `go 1.26.0`; `internal/account` adds no third-party dependency (D13): `crypto/ed25519` and a small strict JWS parser only. One accepted algorithm (EdDSA); the verifier ignores `jku`, `jwk` and `x5u` headers and never negotiates from the header.
- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace. A grant whose outcome is unknown (the request may have been processed, so monoes.me may have rotated the refresh token) or whose answer could not be saved (A24(d)) is retried within 240 seconds and after that is never presented again: this machine drops its refresh token and the reason is `unconfirmed` (A24, §3.6), a grace reason that ends as `locked(unconfirmed)`; the other installs of the account are untouched.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- Storage (§4.6): `~/.monoagent/account/` (directory 0700) with `session.json`, `refresh.enc` and `session.lock` (files 0600). One session per OS user, shared by all profiles, whatever `--db-path` says.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, and neither does a guard pass (`EnsureFresh`, `Refresh`, the background refresher) while the gate is dormant or its date is still ahead, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear on a login or a refresh and, from the enforcement date on (A25), on the first guard pass of a machine that has no session: that pass creates the directory, `session.lock` and a session with no token (`{v, host, hw}`, never `refresh.enc`), the clock-guard record of a machine that never signed in. So a gated command that is refused on an empty HOME leaves exactly `account/session.lock` and `account/session.json` once the date has been reached, and nothing before it. Otherwise the high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- Open commands (D6): `version`, `help`, `completion`, cobra's hidden `__complete` and `__completeNoDesc`, `ref`, `update`, `doctor` (with `doctor fix`), `setup`, `account` (all of it), `library login`, `library logout`, `library status`. Everything else is gated, except the serving commands:
- Serving commands (spec §6.4) start even when locked, because launchd's `KeepAlive` and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop and MCP hosts must see a clear error. The CLI gate's third class `serve` is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`); layers 2 and 3 do the refusing. `org serve` (a launcher) and `daemon install`, `restart` and `uninstall` stay gated.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

## Decisions this plan makes (read before Task 1)

1. **The variable is read only in a file compiled under `-tags devaccount`** (spec A12, index §3.6, with the lead's constraints): a `devaccount` file reads it; the default build has a stub with no `os.Getenv`; a test pins that a default build ignores it; B5a's release guard keeps the tag out of releases. It only sets a date: a value that is not an RFC 3339 time, and the zero time (which would switch the gate off), stop the process at start. A past date forces enforcement, a future one a warn period. Set it for the process under test only: exported in the shell that runs `go test -tags devaccount ./internal/account/`, it moves the date those tests start from, and B1a's `TestEnforcedBuildPinsAKey` fails while no key is pinned.
2. **The smoke ages the token, not the clock.** The 24-hour expiry is reached by rewriting `session.json` with a token issued 24 hours and a minute ago, signed with the development key under the `kid` read from the real token the sign-in produced. The same rewrite expires a token at once and ends a CLI process's one-minute negative cache (it writes `last_attempt` that old too, so the next command tries to refresh; spec A8), so a scenario never waits for a short-lived token. Only the block scenario, which depends on a daemon's refresher, uses short-lived tokens. The guard's own tests (B1a) inject the clock.
3. **The fake monoes.me has a switch in front of its own handler** (`edge`): it can drop connections, answer a refresh grant with `invalid_grant`, a 500, `invalid_client`, `invalid_target`, a page that is not JSON, or a token signed by a key nothing pins, or hold an answer back after the fake has already acted on it (Task 3b), and it counts the refresh grants that reach it (and the requests it dropped while it was down), so that a test shows the attempt was made. It also keeps the 300-second reuse window of the real monoes.me, which the fake does not (`reuseWindow`): a refresh token presented again inside the window is answered with the answer it got the first time, a repeat after it reaches the fake, which punishes it, and a scenario moves the edge's clock (`later`) instead of waiting. The rig depends on nothing of the fake but the OAuth wire shapes. The package is not run with `-race`: `httptest` documents changes to `Config` as valid only before `Start`.
4. **Doors are probed without a credential.** Signed in, the door's own check answers a request that carries no bearer, key or endpoint; locked, the gate answers first. The same request tells the two states apart and needs no token or vault (the wire shapes are B3b's).
5. **A serving command starts locked** (spec A1): `daemon`, `httpapi`, `mcp` and `extension serve` start with no session, log the command to run and refuse at the doors; no scenario expects one of them to exit 4. Gated one-shot commands, `org serve` among them, do.
6. **The account object has four keys**, `{state, reason, valid_until, enforced}` (A4), on the heartbeat, `GET /health` and the bridge's `ping`, and the smoke asserts all four: `enforced` tells a warn-period `locked` (nothing refused) from a real lock.
7. **Not here.** The desktop's Go side starts no gate-site code in-process (B4's `TestDesktopGoSideCallsNoGatedFunction`) and runs every execution through the CLI, which the entry points exercise; the release guard and the date are B5a's; the first-run adoption of an older library login merges with R, so its real-binary check is B5a's dry run against production.
8. **The A20 to A25 scenarios use what the rig already has** (Task 3b). The interrupt is a held answer (`modeHold`: the fake rotates the token at once, the edge answers `holdAnswer` later) and a signal, or a kill, sent to the real process meanwhile. The edge keeps monoes.me's reuse window and the fake punishes a repeat after it, so no scenario waits for the 300 seconds: it moves the edge's clock (`later`), and, because a real binary has no clock to move, ages the marker of a grant in flight (`agePending`) or the token (`backdate`) in the files that the binary compares with the clock. Two installs of one account are two rigs that share one monoes.me (`rigOptions.sharing`). A blocked key store is the rig's file keyring with its passphrase file replaced, for one process, by a named pipe that nobody writes to: it needs no hook in a release or a development build. A real binary has no clock, so the clock-set-back scenario moves the date and the stored high-water mark instead.

## Review Focus

The six failure modes the spec implies that no phase's tests exercise from outside and that are most likely to bite a person using this software, most likely first. Each is pinned in the task that owns the code.

1. **A process type that never installed a guard fails closed for signed-in users.** In a test binary `Require` fails open without a guard (D24), so every unit test passes. Pinned by `TestEveryEntryPointWorksSignedIn` (Task 5): one operation through `workflow run`, `daemon`, `httpapi`, `mcp`, an `mcp --grant` child, `extension serve`, `org serve` and `chat`.
2. **A door stays open while locked, or stays shut after a sign-in.** Pinned by `TestEveryDoorRefusesWhenLockedAndOpensWhenSignedIn` (Task 4): the HTTP API, `/v1`, the org receiver, the webhook server, the bridge and MCP, in both states, in a daemon that started locked.
3. **A failure that is not a refusal locks someone, or a block does not stop the work.** Pinned by `TestAnswersThatAreNotARefusalKeepTheGrace`, `TestUnreachableIsGraceUntilTwentyFourHours`, `TestBlockedAccountLocksAndCancelsWorkInFlight` and `TestAnUnknownSigningKeyIsTheRunUpdateCase` (Task 3).
4. **The warn period locks something or corrupts a `--json` consumer's stdout.** Pinned by `TestNothingLocksBeforeTheDateAndEverySurfaceWarns` (Task 2), which also checks that nothing is written to the account folder before the date, and by `TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten` for the refusal that follows it, which leaves exactly the clock-guard record of a machine that never signed in (A25) and nothing else.
5. **The development variable relaxes a build that ships.** Pinned by `TestDefaultBuildIgnoresTheEnforceOverride` (Task 1), with B5a's release guard that keeps the tag out of releases.
6. **An abandoned or killed refresh, a logout or a refusal with the clock set back, and a key store that never answers cost an account or a machine.** A command interrupted while monoes.me answers must still store the new refresh token (spec A20); one that is killed leaves a dead token, which the guard must retry at once inside monoes.me's reuse window and never present after it, so that one install signs in again and the account is not ended (A24); a logout must keep the clock-guard record (A23) and a refusal must leave one on a machine that never signed in (A25), so that setting the clock back before the date un-enforces nobody who leaves the account folder alone (spec §4.8); and a key store that waits for ever must not hold the session lock against the other processes (A22). Pinned by `TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount`, `TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow`, `TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow`, `TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack`, `TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack` and `TestABlockedKeyStoreDoesNotHoldUpAnotherProcess` (Task 3b).

---

### Task 1: The development-only enforcement override

**Files:**
- Create: `internal/account/rollout_override.go`, `internal/account/rollout_override_default.go`, `internal/account/rollout_override_devaccount.go`
- Create: `internal/account/rollout_override_test.go`, `internal/account/rollout_override_default_test.go`, `internal/account/rollout_override_devaccount_test.go`

**Interfaces:**
- Consumes B1a's `var enforceFrom = time.Time{}` (`internal/account/rollout.go`; the zero time is dormant, and B5a sets the date later). It is assigned in an `init`, before any goroutine, so the accessors' lock is not needed.
- Produces the unexported `devEnforceFrom() (time.Time, bool)`, in two builds, and the variable `MONOAGENT_DEV_ENFORCE_FROM` (an RFC 3339 time) for binaries built with `-tags devaccount`. `rollout_override.go` has no build tag and no `os.Getenv`.

- [ ] **Step 1: Write the failing tests.** The helper both tests share, which runs this test binary again with the variable set and reports the date the child starts with (`internal/account/rollout_override_test.go`):

```go
package account

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestEnforceDateHelper is the child of the tests below: it reports the date this build starts with.
func TestEnforceDateHelper(t *testing.T) {
	if os.Getenv("ACCOUNT_OVERRIDE_HELPER") == "1" {
		fmt.Println("date:", EnforceDate().UTC().Format(time.RFC3339))
	}
}

// startsWith runs this test binary again with the variable set to value ("" leaves it unset) and
// returns what the child reports and whether it started at all.
func startsWith(t *testing.T, value string) (out string, ok bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEnforceDateHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "ACCOUNT_OVERRIDE_HELPER=1", "MONOAGENT_DEV_ENFORCE_FROM="+value)
	b, err := cmd.CombinedOutput()
	return string(b), err == nil
}

// date is the date out reports, "" when it reports none.
func date(out string) string {
	if _, after, found := strings.Cut(out, "date: "); found {
		return strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
	}
	return ""
}
```

the default build's test (`internal/account/rollout_override_default_test.go`):

```go
//go:build !devaccount

package account

import "testing"

// A default build, the build every release is, ignores the variable whatever it holds: it cannot
// move the date, and it cannot stop the process either.
func TestDefaultBuildIgnoresTheEnforceOverride(t *testing.T) {
	compiled, ok := startsWith(t, "")
	if !ok || date(compiled) == "" {
		t.Fatalf("the helper did not report a date: %s", compiled)
	}
	for _, value := range []string{"2020-01-01T00:00:00Z", "2999-01-01T00:00:00Z", "0001-01-01T00:00:00Z", "tomorrow"} {
		out, ok := startsWith(t, value)
		if !ok || date(out) != date(compiled) {
			t.Errorf("with the variable set to %q a default build reports %q (started: %v), want the compiled date %q", value, date(out), ok, date(compiled))
		}
	}
}
```

and the development build's (`internal/account/rollout_override_devaccount_test.go`):

```go
//go:build devaccount

package account

import (
	"strings"
	"testing"
)

// A development build reads the variable, and only to set a date: a value that is not an RFC 3339
// time, or the zero time, which would switch the gate off, stops it at start.
func TestDevBuildReadsTheEnforceOverride(t *testing.T) {
	if out, ok := startsWith(t, "2020-01-01T00:00:00Z"); !ok || date(out) != "2020-01-01T00:00:00Z" {
		t.Errorf("a past date forces enforcement: %q (started: %v)", date(out), ok)
	}
	if out, ok := startsWith(t, "2999-01-01T00:00:00Z"); !ok || date(out) != "2999-01-01T00:00:00Z" {
		t.Errorf("a future date forces the warn period: %q (started: %v)", date(out), ok)
	}
	for value, why := range map[string]string{"tomorrow": "must be an RFC 3339 time", "0001-01-01T00:00:00Z": "would switch the gate off"} {
		if out, ok := startsWith(t, value); ok || !strings.Contains(out, why) {
			t.Errorf("%q should stop the process with %q: started %v: %s", value, why, ok, out)
		}
	}
}
```

- [ ] **Step 2: Run them and watch the development one fail.**

```
go test ./internal/account/ -run '^TestDefaultBuildIgnoresTheEnforceOverride$' -count=1
go test -tags devaccount ./internal/account/ -run '^TestDevBuildReadsTheEnforceOverride$' -count=1
```
Expected: `ok` for the first (a default build has nothing to ignore yet), FAIL for the second: `a past date forces enforcement: "0001-01-01T00:00:00Z" (started: true)`, because the variable is not read.

- [ ] **Step 3: Implement.** The `init`, in no build tag (`internal/account/rollout_override.go`):

```go
package account

// init lets a development build move the enforcement date (spec A12, index §3.6). devEnforceFrom
// reads the environment only in a build with the devaccount tag; a default build's version never
// looks, so nothing in a user's environment can move the date of a release.
func init() {
	if at, ok := devEnforceFrom(); ok {
		enforceFrom = at // init runs before any goroutine, so the accessors' lock is not needed
	}
}
```

the default build's side, which never looks at the environment (`internal/account/rollout_override_default.go`):

```go
//go:build !devaccount

package account

import "time"

// devEnforceFrom is the default build's side of the override: there is none, and nothing here
// looks at the environment.
func devEnforceFrom() (time.Time, bool) { return time.Time{}, false }
```

and the development build's (`internal/account/rollout_override_devaccount.go`):

```go
//go:build devaccount

package account

import (
	"fmt"
	"os"
	"time"
)

// devEnforceFromEnv moves the enforcement date of a development build: a past date forces
// enforcement and a future one a warn period, which is what the real-binary smoke needs. It only sets
// a date. Anything that is not an RFC 3339 time, and the zero time, which would switch the gate off,
// stop the process at start instead of being read as one.
const devEnforceFromEnv = "MONOAGENT_DEV_ENFORCE_FROM"

func devEnforceFrom() (time.Time, bool) {
	v := os.Getenv(devEnforceFromEnv)
	if v == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(fmt.Sprintf("%s must be an RFC 3339 time: %v", devEnforceFromEnv, err))
	}
	if at.IsZero() {
		panic(devEnforceFromEnv + " must be a date: the zero time would switch the gate off")
	}
	return at, true
}
```

- [ ] **Step 4: Run the tests in both builds, expect PASS.**

```
go test ./internal/account/ -run '^TestDefaultBuildIgnoresTheEnforceOverride$' -count=1 -v
go test -tags devaccount ./internal/account/ -run '^TestDevBuildReadsTheEnforceOverride$' -count=1 -v
go vet ./internal/account/ && go vet -tags devaccount ./internal/account/
gofmt -l internal/account
```
Expected: `--- PASS` and `ok` twice; no output from vet or gofmt.

- [ ] **Step 5: Commit.**

```
git add internal/account/rollout_override.go internal/account/rollout_override_default.go internal/account/rollout_override_devaccount.go internal/account/rollout_override_test.go internal/account/rollout_override_default_test.go internal/account/rollout_override_devaccount_test.go
git commit -m "test(account): a devaccount-only variable moves the enforcement date" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Prove the default-build test can fail.** In `internal/account/rollout_override_default.go` replace the body with one that reads the variable (add `"os"` to its imports): `func devEnforceFrom() (time.Time, bool) { at, err := time.Parse(time.RFC3339, os.Getenv("MONOAGENT_DEV_ENFORCE_FROM")); return at, err == nil }`. Run the first command of Step 4 and expect FAIL: `with the variable set to "2020-01-01T00:00:00Z" a default build reports "2020-01-01T00:00:00Z"`. Then `git checkout -- internal/account/rollout_override_default.go` and run it again: `ok`.

### Task 2: The rig, the locked machine and the warn period

The smoke is the one place the whole feature runs as shipped. It exists because in a test binary `account.Require` fails open without a guard (D24), so a process that forgot to install one passes every unit test and would fail closed for signed-in users in production. A failure in Tasks 2 to 5, Task 3b included, is a defect of the phase that owns the behaviour (named in the message): fix it there, with a unit test in that package, never by loosening the smoke.

**Files:**
- Create: `internal/accountsmoke/doc.go`, `internal/accountsmoke/rig_test.go`, `internal/accountsmoke/account_test.go`, `internal/accountsmoke/clients_test.go`, `internal/accountsmoke/locked_test.go`, `internal/accountsmoke/warn_test.go`
- Modify: `.github/workflows/ci.yml` (a new job after `doctor-smoke`, before line 95)

**Interfaces:**
- Consumes, frozen contract (index §3): `account.OpenStore(dir string, s Sealer) Store` with `Store.Lock(ctx) (func(), error)`, `Load() (*Session, error)`, `Save(*Session) error`; `account.Session{AccessToken, LastAttempt, LastResult}`; `account.NewMemorySealer()`; `account.Key{KID, Public}`; `accounttest.New(t) *Fixture` with `Fixture{Private, Key}` and `(*Fixture).Token(TokenOptions)`; `TokenOptions{Sub, IssuedAt, Lifetime}`; `accounttest.DevKeyPair()`.
- Consumes, observable behaviour of earlier phases: B1b: `account login --email E --send`, `account login --email E --code C` (against a fake that answers the email-code route with a refresh token, as plan A's Task 7 makes the real server do; B1b's fake does by default, its `SetEmailOpaque` turns it off), `account status --json` (the `Status` document; exit 0 for `ok` and `grace`, 4 for `locked`), `account logout`, `MONOES_BASE_URL` honored by a devaccount build; B2: the refused-command contract (index §3.4 items 2 and 3), the warn line, `doctor --check core.monoes_account`; B3a: the heartbeat's `account` object; Task 1: `MONOAGENT_DEV_ENFORCE_FROM`.
- Consumes, existing at master: `libraryfake.New()`, `Server.AccessTTL`, `Server.EmailCode`, `Server.URL`, `Server.Config.Handler` (the embedded `httptest.Server`'s handler, which the rig wraps), `Server.Close`.
- Produces: package `accountsmoke`: tests only, built only with `-tags devaccount`, not on Windows. Everything the CLI writes lands under the rig's HOME; the passphrase file, the fake monomind and the process logs live beside it, so "nothing was written" can be asserted of HOME.

- [ ] **Step 1: Write the package and the rig.** `internal/accountsmoke/doc.go`:

```go
// Package accountsmoke is the real-binary smoke test of the monoes.me account gate: the CLI built
// with -tags devaccount, run as subprocesses against a fake monoes.me. It holds tests only, and
// they build only with the tag (go test -tags devaccount ./internal/accountsmoke/), so a plain
// go test ./... never builds the binary.
package accountsmoke
```

`internal/accountsmoke/rig_test.go` (the CLI build, the fake monomind, the switch in front of the fake monoes.me, the rig and its helpers):

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// cli builds the CLI with the dev tag, once per test run.
func cli(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		if binDir, buildErr = os.MkdirTemp("", "accountsmoke-"); buildErr != nil {
			return
		}
		root, _ := filepath.Abs("../..")
		cmd := exec.Command("go", "build", "-tags", "devaccount", "-ldflags", "-X main.version=v0.0.0-smoke",
			"-o", filepath.Join(binDir, "monoagentcli"), "./cmd/monoagentcli")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build -tags devaccount: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(binDir, "monoagentcli")
}

// fakeMonomind stands in for the monomind CLI: the handshake, one canned agent turn, an org
// validation and an org daemon that runs until stopped. Every call is logged.
const fakeMonomind = `#!/bin/sh
echo "monomind $*" >> "$FAKE_MONOMIND_LOG"
case "$1" in
  --version) echo '{"v":1,"version":"2.16.5","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","org-tool-providers"]}' ;;
  agent) [ "$2" = "exec" ] || exit 2
    echo '{"v":1,"type":"start","runtime":"claude","pid":1}'
    echo '{"v":1,"type":"assistant","text":"smoke-ok"}'
    echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"smoke-ok"}'
    echo '{"v":1,"type":"done","exit_code":0}' ;;
  org) case "$2" in
    serve) trap 'exit 0' TERM INT; while :; do sleep 1; done ;;
    validate) echo '{"valid":true}' ;;
    *) echo '{}' ;;
  esac ;;
  *) exit 2 ;;
esac
`

// The edge is a switch in front of the fake monoes.me's own handler, so that a test can make
// monoes.me unreachable or have it answer a refresh grant in a way the fake never would. The binary
// talks to one server, the fake's, and the rig depends on nothing of it but the OAuth wire shapes.
const (
	modePass          = iota // everything reaches the fake
	modeDown                 // the connection is dropped: monoes.me is unreachable
	modeInvalidGrant         // a refresh-token grant is answered invalid_grant: monoes.me says no
	modeServerError          // ... answered 500
	modeInvalidClient        // ... answered invalid_client
	modeInvalidTarget        // ... answered invalid_target
	modeGarbage              // ... answered 200 with a page that is not JSON
	modeUnknownKey           // ... answered 200 with a token signed by a key no build pins
	modeHold                 // ... reaches the fake at once, which rotates the token, and is answered holdAnswer later
)

// holdAnswer is how long modeHold keeps back the answer to a refresh grant that the fake has already
// acted on: time for a test to interrupt the caller while the refresh token is rotated and the answer
// is still on its way.
const holdAnswer = 6 * time.Second

// What a refresh-token grant is answered with, per mode. Only invalid_grant is monoes.me refusing
// the account (D27); every other answer is trouble on the way, and keeps the grace.
var refreshAnswer = map[int32]struct {
	status int
	body   string
}{
	modeInvalidGrant:  {http.StatusBadRequest, `{"error":"invalid_grant","error_description":"the account was blocked"}`},
	modeServerError:   {http.StatusInternalServerError, `{"error":"server_error"}`},
	modeInvalidClient: {http.StatusBadRequest, `{"error":"invalid_client"}`},
	modeInvalidTarget: {http.StatusBadRequest, `{"error":"invalid_target"}`},
	modeGarbage:       {http.StatusOK, `<html>down for maintenance</html>`},
}

// reuseWindow is how long monoes.me answers a refresh token that is presented again with the answer it
// gave the first time (plan A, Task 3: refreshTokenReuseInterval). A repeat after the window is a replay:
// monoes.me deletes every refresh token of the account. The fake monoes.me keeps no window and punishes
// every repeat, so the edge keeps it, on a clock of its own that a scenario can move (later).
const reuseWindow = 300 * time.Second

type edge struct {
	mode     atomic.Int32
	grants   atomic.Int32  // refresh-token grants that reached the edge, whatever it did with them
	drops    atomic.Int32  // requests of any kind that reached the edge while it was down, and were dropped
	skew     atomic.Int64  // how far monoes.me's clock is ahead of the test's, in nanoseconds
	unpinned func() string // a token signed by a key no build pins, for modeUnknownKey

	mu   sync.Mutex
	seen map[string]firstAnswer // the refresh tokens that monoes.me has rotated, by token
}

// firstAnswer is what monoes.me remembers of a refresh token it has rotated: when it first saw it and
// the answer it gave.
type firstAnswer struct {
	at  time.Time
	rec *httptest.ResponseRecorder
}

// later moves monoes.me's clock d ahead of the test's, so that a scenario can let the reuse window pass
// without waiting for it.
func (e *edge) later(d time.Duration) { e.skew.Add(int64(d)) }

func (e *edge) now() time.Time { return time.Now().Add(time.Duration(e.skew.Load())) }

// repeat is the first answer to a refresh token that is presented again inside the reuse window.
func (e *edge) repeat(token string) (*httptest.ResponseRecorder, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.seen[token]
	return a.rec, ok && e.now().Sub(a.at) < reuseWindow
}

// remember keeps the answer given to a refresh token the first time monoes.me rotates it.
func (e *edge) remember(token string, rec *httptest.ResponseRecorder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.seen[token]; !ok && rec.Code == http.StatusOK {
		e.seen[token] = firstAnswer{e.now(), rec}
	}
}

// reply writes a recorded answer.
func reply(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// newEdge wraps the fake's handler. It is installed before the first request, so nothing races it in
// practice; httptest documents changes to Config as valid only before Start and the swap is not
// synchronized with the server's goroutines, so this package is not run with -race (CI does not).
func newEdge(fake *libraryfake.Server) *edge {
	e := &edge{seen: map[string]firstAnswer{}}
	inner := fake.Config.Handler
	fake.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refresh, token := false, ""
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/oauth2/token" {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body)) // the fake still reads what it was sent
			form, _ := url.ParseQuery(string(body))
			if refresh = form.Get("grant_type") == "refresh_token"; refresh {
				token = form.Get("refresh_token")
			}
		}
		mode := e.mode.Load()
		held := refresh && mode == modeHold
		if refresh && !held {
			e.grants.Add(1) // a held grant is counted once monoes.me has acted on it, below
		}
		if mode == modeDown {
			e.drops.Add(1)
			if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
				c.Close()
			}
			return
		}
		if refresh && mode == modePass {
			if rec, ok := e.repeat(token); ok { // inside the window monoes.me repeats its first answer
				reply(w, rec)
				return
			}
		}
		if refresh && (mode == modePass || held) {
			rec := httptest.NewRecorder()
			inner.ServeHTTP(rec, r) // monoes.me rotates the refresh token now...
			e.remember(token, rec)
			if held {
				e.grants.Add(1)
				select {
				case <-time.After(holdAnswer): // ...and the caller hears of it later
				case <-r.Context().Done(): // unless the caller is gone: nothing is left to answer
					return
				}
			}
			reply(w, rec)
			return
		}
		if mode == modeUnknownKey && refresh {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": e.unpinned(), "refresh_token": "rt-stranger",
				"token_type": "Bearer", "expires_in": 3600, "scope": "openid"})
			return
		}
		if a, ok := refreshAnswer[mode]; ok && refresh {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(a.status)
			_, _ = w.Write([]byte(a.body))
			return
		}
		inner.ServeHTTP(w, r)
	})
	return e
}

func (e *edge) set(mode int32) { e.mode.Store(mode) }

// refreshes is how many refresh-token grants have been presented to monoes.me so far.
func (e *edge) refreshes() int { return int(e.grants.Load()) }

// dropped is how many requests reached monoes.me while it was down. A refresh that cannot find monoes.me
// stops at the endpoint discovery and never sends its grant, so it shows here and not in refreshes.
func (e *edge) dropped() int { return int(e.drops.Load()) }

type rigOptions struct {
	ttl     time.Duration // how long the fake's access tokens live (default: its own, an hour)
	enforce time.Time     // the date the binary enforces from, in its environment; zero leaves the variable unset
	sharing *rig          // another install of the same account: this rig uses that rig's monoes.me, in a HOME of its own
}

// A rig is one throwaway HOME, one fake monoes.me with its edge, one fake monomind and the CLI
// built with the dev tag. Everything the CLI writes lands under home; everything else the test
// needs lives in dir, so that "nothing was written" can be asserted of home.
type rig struct {
	t       *testing.T
	bin     string
	dir     string
	home    string
	fake    *libraryfake.Server
	edge    *edge
	enforce time.Time
	ports   map[string]int
	files   *int
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	r := &rig{t: t, bin: cli(t), dir: t.TempDir(), enforce: o.enforce, ports: map[string]int{}, files: new(int)}
	r.home = filepath.Join(r.dir, "home")
	must(t, os.Mkdir(r.home, 0o700))
	if o.sharing != nil {
		r.fake, r.edge = o.sharing.fake, o.sharing.edge
	} else {
		r.fake = libraryfake.New()
		t.Cleanup(r.fake.Close)
		if o.ttl > 0 {
			r.fake.AccessTTL = o.ttl
		}
		r.edge = newEdge(r.fake)
		stranger := accounttest.New(t) // a throwaway signing key that the binary under test does not pin
		r.edge.unpinned = func() string { return stranger.Token(accounttest.TokenOptions{Sub: "u-ada"}) }
	}
	must(t, os.WriteFile(filepath.Join(r.dir, "passphrase"), []byte("smoke-passphrase\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(r.dir, "monomind"), []byte(fakeMonomind), 0o755))
	return r
}

// sub is the rig as a subtest sees it: a failure inside must fail the subtest, not its parent.
func (r *rig) sub(t *testing.T) *rig {
	c := *r
	c.t = t
	return &c
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// port is a free loopback port, the same one for every call with the same name.
func (r *rig) port(name string) int {
	if _, ok := r.ports[name]; !ok {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		must(r.t, err)
		defer l.Close()
		r.ports[name] = l.Addr().(*net.TCPAddr).Port
	}
	return r.ports[name]
}

func (r *rig) addr(name string) string { return "127.0.0.1:" + strconv.Itoa(r.port(name)) }

// env is the whole environment of a process: nothing of the developer's own leaks in.
func (r *rig) env(extra ...string) []string {
	if !r.enforce.IsZero() {
		extra = append([]string{"MONOAGENT_DEV_ENFORCE_FROM=" + r.enforce.UTC().Format(time.RFC3339)}, extra...)
	}
	return append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + r.home,
		"MONOES_BASE_URL=" + r.fake.URL,
		"MONOAGENT_ALLOW_FILE_KEYRING=1", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE=" + filepath.Join(r.dir, "passphrase"),
		"MONOMIND_BIN=" + filepath.Join(r.dir, "monomind"), "FAKE_MONOMIND_LOG=" + filepath.Join(r.dir, "monomind.log"),
		"MONOAGENT_SUMMARY_RUNTIME=off",
		"MONOAGENT_WEBHOOK_ADDR=" + r.addr("webhook"), "MONOAGENT_EXTENSION_PORT=" + strconv.Itoa(r.port("bridge")),
		"MONOAGENT_API_ADDR=" + r.addr("api"),
	}, extra...)
}

type result struct {
	code           int
	stdout, stderr string
}

func (r *rig) run(args ...string) result {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Env, cmd.Dir = r.env(), r.dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var ee *exec.ExitError
	switch err := cmd.Run(); {
	case errors.As(err, &ee):
		return result{ee.ExitCode(), out.String(), errb.String()}
	case err != nil:
		r.t.Fatalf("run %v: %v", args, err)
	}
	return result{0, out.String(), errb.String()}
}

func mustExit(t testing.TB, res result, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", res.code, want, res.stdout, res.stderr)
	}
}

func mustJSON(t testing.TB, s string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), into); err != nil {
		t.Fatalf("not JSON (%v): %s", err, s)
	}
}

// proc is a long-running process of the rig (a daemon, a server, a bridge); its output goes to
// <first argument>.log in the rig's folder.
type proc struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func (r *rig) start(args ...string) *proc { return r.startWith(nil, args...) }

// startWith is start with more environment for this one process: a later value replaces an earlier one.
func (r *rig) startWith(extra []string, args ...string) *proc {
	r.t.Helper()
	log, err := os.Create(filepath.Join(r.dir, args[0]+".log"))
	must(r.t, err)
	cmd := exec.Command(r.bin, args...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = r.env(extra...), r.dir, log, log
	must(r.t, cmd.Start())
	p := &proc{cmd: cmd, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	r.t.Cleanup(p.stop)
	return p
}

func (p *proc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// stop asks the process to end (SIGTERM, as a service manager does) and kills it after 20 seconds.
func (p *proc) stop() {
	if !p.alive() {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func (r *rig) waitFor(what string, d time.Duration, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(d); !ok(); time.Sleep(250 * time.Millisecond) {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out after %v waiting for %s", d, what)
		}
	}
}

// read returns a file of the rig's folder, "" when it is not there.
func (r *rig) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, rel))
	return string(b)
}

// execution is one row of the CLI's database, as the daemon left it (Status "" when there is none).
func (r *rig) execution(id string) (e struct {
	Status string
	PID    int
}) {
	r.t.Helper()
	path := filepath.Join(r.home, ".monoagent", "monoagent.db")
	if _, err := os.Stat(path); err != nil {
		return e
	}
	db, err := sql.Open("sqlite", path)
	must(r.t, err)
	defer db.Close()
	_ = db.QueryRow(`SELECT status, COALESCE(pid, 0) FROM workflow_executions WHERE id = ?`, id).Scan(&e.Status, &e.PID)
	return e
}

// accountReport is the account object that the daemon's heartbeat, GET /health and the bridge's ping
// carry: {state, reason, valid_until, enforced} (index section 3.4 items 4, 5 and 7; spec A4).
type accountReport struct {
	State      string `json:"state"`
	Reason     string `json:"reason"`
	ValidUntil string `json:"valid_until"`
	Enforced   *bool  `json:"enforced"`
}

// is checks the report against what the machine is: its state, whether the verdict is enforced (a
// warn period reports locked and not enforced), and whether a token's expiry is named, which a
// session has and a machine with no session has not.
func (a *accountReport) is(t testing.TB, state string, enforced, hasSession bool) {
	t.Helper()
	if a == nil || a.State != state || a.Enforced == nil || *a.Enforced != enforced || (a.ValidUntil != "") != hasSession {
		t.Fatalf("the account report is %+v, want state %s, enforced %v, session %v", a, state, enforced, hasSession)
	}
}

// heartbeatAccount is what the daemon last wrote: its pid and the account it reports (nil before
// a heartbeat, and while the gate is dormant).
func (r *rig) heartbeatAccount() (pid int, a *accountReport) {
	var hb struct {
		PID     int
		Account *accountReport
	}
	if b, err := os.ReadFile(filepath.Join(r.home, ".monoagent", "daemon-heartbeat.json")); err == nil && json.Unmarshal(b, &hb) == nil {
		return hb.PID, hb.Account
	}
	return 0, nil
}

// heartbeat is the pid and the account state and reason ("" before a heartbeat).
func (r *rig) heartbeat() (pid int, state, reason string) {
	pid, a := r.heartbeatAccount()
	if a == nil {
		return pid, "", ""
	}
	return pid, a.State, a.Reason
}

func (r *rig) accountState() string { _, state, _ := r.heartbeat(); return state }

func (r *rig) startDaemon() *proc {
	r.t.Helper()
	p := r.start("daemon", "--api-addr", r.addr("api"), "--bridge=false")
	r.waitFor("the daemon's heartbeat", 60*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(r.home, ".monoagent", "daemon-heartbeat.json"))
		return err == nil && p.alive()
	})
	return p
}

// waitWorkflow is a manual trigger followed by a wait node: a run that lasts about seconds.
func waitWorkflow(name string, seconds int) string {
	return fmt.Sprintf(`{"name":%q,"nodes":[
 {"id":"t","type":"trigger.manual","name":"Start","config":{}},
 {"id":"w","type":"core.wait","name":"Wait","config":{"duration":%d}}],
 "connections":[{"id":"t-w","source":"t","target":"w"}]}`, name, seconds)
}

// workflow imports def, activates it when asked, and returns its id.
func (r *rig) workflow(def string, activate bool) string {
	r.t.Helper()
	*r.files++
	path := filepath.Join(r.dir, "wf-"+strconv.Itoa(*r.files)+".json")
	must(r.t, os.WriteFile(path, []byte(def), 0o600))
	res := r.run("--json", "workflow", "import", "--file", path)
	mustExit(r.t, res, 0)
	var doc struct{ ID string }
	mustJSON(r.t, res.stdout, &doc)
	if activate {
		mustExit(r.t, r.run("workflow", "activate", doc.ID), 0)
	}
	return doc.ID
}

// enqueue starts a run for the daemon to adopt and returns its execution id.
func (r *rig) enqueue(workflowID string) string {
	r.t.Helper()
	res := r.run("--json", "workflow", "run", workflowID, "--no-wait")
	mustExit(r.t, res, 0)
	var doc struct {
		ExecutionID string `json:"execution_id"`
	}
	mustJSON(r.t, res.stdout, &doc)
	return doc.ExecutionID
}
```

`internal/accountsmoke/account_test.go` (the words the gate says, signing in, reading the status, ageing the token, the refusal assertions):

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// What the gate says (index §3.4); the smoke asserts these words, never the tokens behind them.
const (
	loginLine       = "Log in to monoes.me first: monoagentcli account login"
	graceLine       = "monoes.me is unreachable; this login works offline until"
	unconfirmedLine = "This login can no longer be renewed on this machine and works until"
	warnLine        = "A monoes.me login will be required from"
	testEmail       = "ada@example.com" // the user the fake's simulated browser is logged in as
)

// past is an enforcement date long gone: the binary enforces from the first second. future is one
// that never comes: the binary is in its warn period and nothing is refused.
var (
	past   = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	future = time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
)

// accountStatus is `account status --json` (spec §7): the fields the smoke reads.
type accountStatus struct {
	State       string    `json:"state"`
	Reason      string    `json:"reason"`
	EnforceFrom time.Time `json:"enforce_from"`
	Enforced    bool      `json:"enforced"`
	User        *struct {
		Email string `json:"email"`
	} `json:"user"`
}

// status runs `account status --json`: the document and the exit code (0 for ok and grace, 4 for locked).
// It reads the first document on stdout: that the command prints only that one is B1b's and B2's to
// pin, and a reader of the state should not fail on what follows it.
func (r *rig) status() (accountStatus, int) {
	r.t.Helper()
	res := r.run("account", "status", "--json")
	var st accountStatus
	if err := json.NewDecoder(strings.NewReader(res.stdout)).Decode(&st); err != nil {
		r.t.Fatalf("account status --json (exit %d) printed no document (%v)\nstdout: %s\nstderr: %s", res.code, err, res.stdout, res.stderr)
	}
	return st, res.code
}

// signIn signs in the way a headless machine does, with the code the fake sends.
func (r *rig) signIn() {
	r.t.Helper()
	mustExit(r.t, r.run("account", "login", "--email", testEmail, "--send"), 0)
	mustExit(r.t, r.run("account", "login", "--email", testEmail, "--code", r.fake.EmailCode), 0)
	if st, code := r.status(); st.State != "ok" || code != 0 || st.User == nil || st.User.Email != testEmail {
		r.t.Fatalf("after signing in: state %q reason %q, exit %d", st.State, st.Reason, code)
	}
}

// accountDir is where the rig's HOME keeps the session.
func (r *rig) accountDir() string { return filepath.Join(r.home, ".monoagent", "account") }

func (r *rig) refreshFile() string { return filepath.Join(r.accountDir(), "refresh.enc") }

// session is the stored session, nil when there is none.
func (r *rig) session() *account.Session {
	r.t.Helper()
	sess, err := account.OpenStore(r.accountDir(), account.NewMemorySealer()).Load()
	must(r.t, err)
	return sess
}

// assertOnlyTheClockGuardRecord checks what a refused command leaves on a machine that never signed in,
// from the enforcement date on (A25): HOME holds the two files of the record and nothing else (no
// first-run marker, no database, no refresh token), and the session in them has no token and a
// high-water mark that lies between the two times the test took the clock.
func (r *rig) assertOnlyTheClockGuardRecord(from, to time.Time) {
	r.t.Helper()
	var files []string
	must(r.t, filepath.WalkDir(r.home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(r.home, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	}))
	if want := []string{".monoagent/account/session.json", ".monoagent/account/session.lock"}; !slices.Equal(files, want) {
		r.t.Fatalf("a refused command left %v in HOME, want %v: the clock-guard record and nothing else", files, want)
	}
	if sess := r.session(); sess == nil || sess.AccessToken != "" || sess.State != "" || sess.HW.Before(from) || sess.HW.After(to) {
		r.t.Fatalf("the record is %+v, want a session with no token and a high-water mark between %v and %v", sess, from, to)
	}
}

// backdate ages the session: its access token becomes one issued age ago (signed with the
// development key, under the kid read from the real token the sign-in produced) and its last
// refresh attempt becomes that old too, which ends the one-minute negative cache of a CLI process,
// so that its next command tries to refresh at once. A real binary has no clock to set, by design
// (no environment variable relaxes the gate), so the smoke ages what the clock is compared with;
// the guard's own tests inject the clock. An age over an hour is an expired token, over 24 hours
// the end of the grace.
func (r *rig) backdate(age time.Duration) {
	r.t.Helper()
	st := account.OpenStore(filepath.Join(r.home, ".monoagent", "account"), account.NewMemorySealer())
	unlock, err := st.Lock(context.Background())
	must(r.t, err)
	defer unlock()
	sess, err := st.Load()
	must(r.t, err)
	if sess == nil {
		r.t.Fatal("no session to age")
	}
	parts := strings.Split(sess.AccessToken, ".")
	if len(parts) != 3 {
		r.t.Fatal("the access token is not a JWS")
	}
	var header struct{ KID string }
	var claims struct{ Sub string }
	for i, into := range []any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		must(r.t, err)
		must(r.t, json.Unmarshal(raw, into))
	}
	pub, priv := accounttest.DevKeyPair()
	f := accounttest.New(r.t)
	f.Private, f.Key = priv, account.Key{KID: header.KID, Public: pub}
	sess.AccessToken = f.Token(accounttest.TokenOptions{Sub: claims.Sub, IssuedAt: time.Now().Add(-age), Lifetime: time.Hour})
	sess.LastAttempt = time.Now().Add(-age)
	if sess.LastResult != string(account.ReasonUnconfirmed) { // a machine that dropped its token keeps saying why (A24)
		sess.LastResult = "ok"
	}
	must(r.t, st.Save(sess))
}

// assertLocked checks a refused gated command: exit 4, the fixed first line on stderr and, when
// the command asked for JSON, the one document of index §3.4.
func (r *rig) assertLocked(res result, reason string, wantJSON bool) {
	r.t.Helper()
	mustExit(r.t, res, 4)
	if first, _, _ := strings.Cut(res.stderr, "\n"); first != loginLine {
		r.t.Fatalf("the first line on stderr is %q, want %q", first, loginLine)
	}
	if !wantJSON {
		return
	}
	var doc struct {
		LoginRequired bool   `json:"login_required"`
		Code          string `json:"code"`
		Account       struct{ State, Reason string }
	}
	mustJSON(r.t, res.stdout, &doc)
	if !doc.LoginRequired || doc.Code != "auth_or_connection" || doc.Account.State != "locked" || doc.Account.Reason != reason {
		r.t.Fatalf("the JSON error is %+v, want login_required, auth_or_connection, locked(%s)", doc, reason)
	}
}
```

`internal/accountsmoke/clients_test.go` (the clients the later tasks talk to the processes with: a stdio MCP server, the extension bridge's request channel, and requests that carry no credential):

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mcpConn is a stdio MCP server run by the rig: one JSON-RPC request per line, one answer per line.
type mcpConn struct {
	t   *testing.T
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	id  int
}

func (r *rig) mcp(extraEnv []string, args ...string) *mcpConn {
	r.t.Helper()
	cmd := exec.Command(r.bin, args...)
	cmd.Env, cmd.Dir = r.env(extraEnv...), r.dir
	in, err := cmd.StdinPipe()
	must(r.t, err)
	out, err := cmd.StdoutPipe()
	must(r.t, err)
	must(r.t, cmd.Start())
	r.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return &mcpConn{t: r.t, cmd: cmd, in: in, out: bufio.NewReader(out)}
}

// call sends one request and returns its result object; a JSON-RPC error fails the test.
func (c *mcpConn) call(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	_, err := c.in.Write(append(req, '\n'))
	must(c.t, err)
	watchdog := time.AfterFunc(60*time.Second, func() { _ = c.cmd.Process.Kill() })
	defer watchdog.Stop()
	line, err := c.out.ReadString('\n')
	if err != nil {
		c.t.Fatalf("%s: no answer: %v", method, err)
	}
	var resp struct {
		Result map[string]any
		Error  *struct{ Message string }
	}
	mustJSON(c.t, line, &resp)
	if resp.Error != nil {
		c.t.Fatalf("%s: %s", method, resp.Error.Message)
	}
	return resp.Result
}

// try calls a tool and returns its text and whether it answered an error.
func (c *mcpConn) try(name string, args map[string]any) (text string, isErr bool) {
	return c.tryWith(name, args, nil)
}

func (c *mcpConn) tryWith(name string, args, meta map[string]any) (text string, isErr bool) {
	c.t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	if meta != nil {
		params["_meta"] = meta
	}
	res := c.call("tools/call", params)
	if content, _ := res["content"].([]any); len(content) > 0 {
		text, _ = content[0].(map[string]any)["text"].(string)
	}
	isErr, _ = res["isError"].(bool)
	return text, isErr
}

// tool calls a tool and returns its text; an error result fails the test.
func (c *mcpConn) tool(name string, args, meta map[string]any) string {
	c.t.Helper()
	text, isErr := c.tryWith(name, args, meta)
	if isErr {
		c.t.Fatalf("%s answered an error: %s", name, text)
	}
	return text
}

// bridgeReply is the settling frame of the extension bridge's request channel; ping's data carries
// the account (index section 3.4 item 7, and B3b's plan for its shape).
type bridgeReply struct {
	OK   bool
	Code string
	Data struct {
		Pong    bool
		Account *accountReport
	}
}

// bridge connects to the extension bridge the way the browser's extension does (the pairing
// token, then request frames) and returns a function that sends one request and returns the frame.
func (r *rig) bridge() func(method string) (string, bridgeReply) {
	r.t.Helper()
	addr := r.addr("bridge")
	r.waitFor("the bridge", 30*time.Second, func() bool { code, _ := get(r.t, "http://"+addr+"/monoagent/health", ""); return code == 200 })
	token, err := os.ReadFile(filepath.Join(r.home, ".monoagent", "extension.token"))
	must(r.t, err)
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/monoagent", http.Header{"Origin": {"chrome-extension://smoke"}})
	must(r.t, err)
	r.t.Cleanup(func() { ws.Close() })
	must(r.t, ws.WriteJSON(map[string]string{"type": "auth", "token": strings.TrimSpace(string(token))}))
	n := 0
	return func(method string) (string, bridgeReply) {
		n++
		id := fmt.Sprintf("r%d", n)
		must(r.t, ws.WriteJSON(map[string]any{"kind": "request", "id": id, "method": method, "params": map[string]any{"url": "https://example.com/"}}))
		_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		for {
			_, msg, err := ws.ReadMessage()
			must(r.t, err)
			var head struct{ Kind, ID string }
			if json.Unmarshal(msg, &head) == nil && head.Kind == "reply" && head.ID == id {
				var reply bridgeReply
				mustJSON(r.t, string(msg), &reply)
				return string(msg), reply
			}
		}
	}
}

func get(t testing.TB, url, bearer string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	must(t, err)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// answer is what one request got back.
type answer struct {
	code   int
	header http.Header
	body   string
}

// probe sends one request with no credential. Signed in, the door's own check answers it (no bearer,
// no key, no endpoint); locked, the account gate answers first. So the same request tells the two
// states apart, and nothing here needs a token or the vault.
func probe(t testing.TB, method, url, body string) answer {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	must(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return answer{body: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return answer{resp.StatusCode, resp.Header, string(b)}
}

// health is the account object of GET /health, which is open in every state and never names the user.
func (r *rig) health(api string) *accountReport {
	r.t.Helper()
	a := probe(r.t, "GET", api+"/health", "")
	if a.code != 200 || strings.Contains(a.body, testEmail) {
		r.t.Fatalf("GET /health: %d %s", a.code, a.body)
	}
	var doc struct{ Account *accountReport }
	mustJSON(r.t, a.body, &doc)
	return doc.Account
}
```

- [ ] **Step 2: Write the test of the locked machine** (acceptance 1, the command line). A command that would write is refused and writes nothing but the clock-guard record of a machine that never signed in (A25), an open command works, one sign-in opens the machine and a logout closes it. `internal/accountsmoke/locked_test.go`:

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Acceptance 1, the command line as a person meets it: after the date, with no valid session, a
// gated command exits 4 with login_required and does nothing else but leave the clock-guard record
// of a machine that never signed in (A25); an open command still works. Signing in opens the
// machine, and logging out closes it again. (The doors are Task 4's.)
func TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})

	// A command that would write (it creates the database and a workflow) is refused first: exit 4,
	// the fixed first line, the JSON error, and nothing in HOME but the two files of the record: no
	// first-run writes, no database, no refresh token.
	def := filepath.Join(r.dir, "refused.json")
	must(t, os.WriteFile(def, []byte(waitWorkflow("smoke-refused", 1)), 0o600))
	from := time.Now()
	r.assertLocked(r.run("--json", "workflow", "import", "--file", def), "not_logged_in", true)
	r.assertOnlyTheClockGuardRecord(from, time.Now())
	r.assertLocked(r.run("workflow", "list"), "not_logged_in", false)
	r.assertLocked(r.run("org", "serve", "--foreground"), "not_logged_in", false) // a launcher, not a serving command (spec A1)
	mustExit(t, r.run("version"), 0)                                              // an open command needs no account

	r.signIn()
	mustExit(t, r.run("workflow", "list"), 0)
	mustExit(t, r.run("account", "logout"), 0)
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("logging out deletes the refresh token: %v", err)
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "not_logged_in", true)
}
```

- [ ] **Step 3: Write the warn-period test** (acceptance 4). Every surface that warns is read from the real binary: the stderr line, `account status` (the desktop banner reads exactly that document), the doctor row and the daemon's heartbeat, and the daemon still runs work. `internal/accountsmoke/warn_test.go`:

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance 4: before the date nothing locks, and every surface warns: the stderr line (once,
// never on stdout), `account status` (what the desktop banner reads), the doctor row and the
// daemon's heartbeat. And nothing is written to the account folder: the clock-guard record of
// A25 is for the days after the date.
func TestNothingLocksBeforeTheDateAndEverySurfaceWarns(t *testing.T) {
	date := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	r := newRig(t, rigOptions{enforce: date})

	res := r.run("--json", "workflow", "list")
	mustExit(t, res, 0)
	if !strings.HasPrefix(strings.TrimSpace(res.stdout), "[") || strings.Contains(res.stdout, warnLine) {
		t.Fatalf("stdout must stay the command's own JSON: %s", res.stdout)
	}
	if n := strings.Count(res.stderr, warnLine); n != 1 || !strings.Contains(res.stderr, "monoagentcli account login") {
		t.Fatalf("the warning must appear once on stderr, with the way out, got %d times:\n%s", n, res.stderr)
	}
	if st, _ := r.status(); st.State != "locked" || st.Reason != "not_logged_in" || st.Enforced || !st.EnforceFrom.Equal(date) {
		t.Fatalf("account status in the warn period: %+v", st)
	}

	var report struct {
		Results []struct{ ID, Status string }
	}
	mustJSON(t, r.run("--json", "doctor", "--check", "core.monoes_account").stdout, &report)
	row := ""
	for _, x := range report.Results {
		if x.ID == "core.monoes_account" {
			row = x.Status
		}
	}
	if row != "warn" {
		t.Fatalf("the doctor row should warn in the warn period, it is %q", row)
	}

	r.startDaemon()
	r.waitFor("the heartbeat's account state", 30*time.Second, func() bool { return r.accountState() != "" })
	// With no session the heartbeat reports the state the warning is about, and that nothing is
	// enforced yet.
	_, hb := r.heartbeatAccount()
	hb.is(t, "locked", false, false)
	id := r.enqueue(r.workflow(waitWorkflow("smoke-fast", 1), true))
	r.waitFor("the daemon to run work although the state is locked", 60*time.Second, func() bool { return r.execution(id).Status == "SUCCESS" })
	if _, err := os.Stat(r.accountDir()); !os.IsNotExist(err) {
		t.Fatalf("before the date nothing is written to the account folder, the daemon's refresher included (A25): %v", err)
	}
}
```

- [ ] **Step 4: Check that it compiles.** `go vet -tags devaccount ./internal/accountsmoke/`. Expected: no output. `go test ./internal/accountsmoke/ -count=1` without the tag prints `?   	github.com/monoes/mono-agent/internal/accountsmoke	[no test files]`: a plain `go test ./...` never builds the binary.

- [ ] **Step 5: Run them.**

```
go test -tags devaccount ./internal/accountsmoke/ -run '^(TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten|TestNothingLocksBeforeTheDateAndEverySurfaceWarns)$' -count=1 -timeout 15m -v
```
Expected: both `--- PASS`, about a minute after the first build of the CLI (one to two minutes the first time). The heartbeat says `locked` in the warn test although nothing is refused: its `enforced` key is `false`, which is what tells the two apart. If the first test fails inside `signIn` at `account login --email ... --code ...`, the fake answered the email-code route without a refresh token: fix `libraryfake` in B1b's package, with a test there, and not in the rig.

- [ ] **Step 6: Prove they can fail.** These tests have no red phase of their own: the behaviour they pin was built by B1 to B4. In `locked_test.go` change `rigOptions{enforce: past}` to `rigOptions{enforce: future}` and expect `TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten` to FAIL at the first command (`exit 0, want 4`: before the date nothing is refused). Restore the line. In `warn_test.go` change `rigOptions{enforce: date}` to `rigOptions{enforce: past}` and expect `TestNothingLocksBeforeTheDateAndEverySurfaceWarns` to FAIL at the first command (`exit 4, want 0`, with `Log in to monoes.me first` on stderr: a binary under enforcement refuses a machine with no session). Restore the line and run both again: PASS.

- [ ] **Step 7: Add the CI job.** In `.github/workflows/ci.yml` insert before the comment at line 95 (`# Closes issue #20's OpenAPI acceptance criterion`):

```yaml
  # The mandatory monoes.me account (docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md,
  # section 11): the real binary, built with the devaccount tag, against a fake monoes.me. Locked,
  # sign in, ok, blocked, unreachable, grace, expired, the warn period, every door and one operation
  # through every process type and entry point, so a path that forgot to install the guard fails here
  # and not for signed-in users. It must pass before release R.
  account-smoke:
    name: Account gate smoke (devaccount)
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - name: Vet with the dev tag
        run: go vet -tags devaccount ./internal/account/... ./internal/accountsmoke/... ./cmd/monoagentcli/
      - name: Account tests with the dev tag
        run: go test -race -tags devaccount ./internal/account/... -count=1
      - name: Real-binary smoke
        run: go test -tags devaccount ./internal/accountsmoke/ -count=1 -timeout 25m -v

```
Validate: `python3 -c "import yaml; print(list(yaml.safe_load(open('.github/workflows/ci.yml'))['jobs']))"`. Expected: the list contains `account-smoke` right after `doctor-smoke`.

- [ ] **Step 8: Commit.**

```
git add internal/accountsmoke/doc.go internal/accountsmoke/rig_test.go internal/accountsmoke/account_test.go internal/accountsmoke/clients_test.go internal/accountsmoke/locked_test.go internal/accountsmoke/warn_test.go .github/workflows/ci.yml
git commit -m "test(account): real-binary smoke rig, the locked machine, the warn period and its CI job" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3: Grace, expiry, trouble that is not a refusal, an unknown key, and a block

**Files:**
- Create: `internal/accountsmoke/gate_test.go`

**Interfaces:**
- Consumes: Task 2's rig (`newRig`, `rig.edge.set(mode)` with `modePass`, `modeDown`, `modeInvalidGrant`, `modeServerError`, `modeInvalidClient`, `modeInvalidTarget`, `modeGarbage` and `modeUnknownKey`, `rig.edge.refreshes()`, `rig.edge.dropped()`, `rig.backdate`, `rig.signIn`, `rig.status`, `rig.startDaemon`, `rig.workflow`, `rig.enqueue`, `rig.execution`, `rig.heartbeat`, `rig.assertLocked`, `rig.waitFor`, `past`, `graceLine`), `shortToken`; the `doctor --json` report (`results[].id`, `status`, `fix.id`) and B2's fix id `core.update.install` for an unknown key.
- Produces: `TestUnreachableIsGraceUntilTwentyFourHours` (acceptance 3), `TestAnswersThatAreNotARefusalKeepTheGrace` (D27), `TestAnUnknownSigningKeyIsTheRunUpdateCase` (spec §4.7, A9) and `TestBlockedAccountLocksAndCancelsWorkInFlight` (acceptance 2). D27 is the line between the middle two and the last: only `invalid_grant` answered to a refresh-token grant locks, deletes `refresh.enc` and cancels; a 500, `invalid_client`, `invalid_target` or a page that is not JSON leaves the machine in grace with reason `server_error`, and the kept refresh token ends the grace when monoes.me answers again. A refreshed token signed by a key nothing pins is not stored; the grace shows `server_error` and, once it is over, the verdict is `locked(key_unknown)`: the case that wants an update, not another sign-in. In the block scenario the fake's tokens live 15 seconds, so a daemon (which refreshes at half the lifetime) notices a change within seconds; the others age the stored token.

- [ ] **Step 1: Write the scenarios.** `internal/accountsmoke/gate_test.go`:

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"os"
	"strings"
	"testing"
	"time"
)

// shortToken is how long the fake's access tokens live in the scenario that waits for a daemon's
// refresher to notice a change: a daemon refreshes at half of it, so it learns of a block within
// seconds instead of half an hour.
const shortToken = 15 * time.Second

// Acceptance 3: signed in, monoes.me unreachable: the work goes on until 24 hours after the newest
// token was issued, then the machine is locked; work in flight at that moment finishes the node it is
// in (this job has one node, so it ends SUCCESS; a run with a next node ends there, ruling R4).
func TestUnreachableIsGraceUntilTwentyFourHours(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	r.edge.set(modeDown)
	r.backdate(2 * time.Hour) // the access token expired an hour ago, and monoes.me cannot be asked for another

	if st, code := r.status(); st.State != "grace" || st.Reason != "unreachable" || code != 0 || r.edge.dropped() == 0 {
		t.Fatalf("expired token, monoes.me down: exit %d, %+v, %d requests reached monoes.me", code, st, r.edge.dropped())
	}
	res := r.run("--json", "workflow", "list")
	mustExit(t, res, 0)
	if !strings.Contains(res.stderr, graceLine) || strings.Contains(res.stdout, graceLine) || !strings.HasPrefix(strings.TrimSpace(res.stdout), "[") {
		t.Fatalf("grace is one line on stderr and never on stdout:\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
	}

	// A daemon in grace runs a 40-second job; the 24 hours run out while it runs.
	r.startDaemon()
	id := r.enqueue(r.workflow(waitWorkflow("smoke-forty", 40), true))
	r.waitFor("the job to be running in the daemon", 60*time.Second, func() bool { return r.execution(id).Status == "RUNNING" })
	r.backdate(24*time.Hour + time.Minute)
	r.waitFor("the daemon to lock", 60*time.Second, func() bool { return r.accountState() == "locked" })
	if got := r.execution(id).Status; got != "RUNNING" {
		t.Fatalf("the job was %s when the 24 hours ran out: unreachable is not a refusal, so the node in flight finishes", got)
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "expired", true)
	r.waitFor("the job to finish", 90*time.Second, func() bool { return r.execution(id).Status != "RUNNING" })
	if got := r.execution(id).Status; got != "SUCCESS" {
		t.Fatalf("the job ended %s, want SUCCESS", got)
	}

	r.edge.set(modePass) // monoes.me is back: one sign-in and the machine works again
	r.signIn()
	mustExit(t, r.run("workflow", "list"), 0)
}

// D27: only invalid_grant answered to a refresh-token grant is a refusal. A server error, a client
// or a resource monoes.me does not know, a page that is not JSON: each is trouble on the way, so
// the machine stays in grace with reason server_error, the refresh token is kept (it may well be
// good) and nothing is refused. (A24: the server error and the page that is not JSON are the two
// of these whose outcome is unknown, a 5xx that may follow a rotation and a 200 that holds no token
// set, so the guard keeps pending_since beside the token and retries at once for 240 seconds; this
// test stays well inside that. The unknown client and resource are complete 4xx answers: settled.)
func TestAnswersThatAreNotARefusalKeepTheGrace(t *testing.T) {
	for _, c := range []struct {
		name string
		mode int32
	}{
		{"a server error", modeServerError},
		{"an unknown client", modeInvalidClient},
		{"an unknown resource", modeInvalidTarget},
		{"a page that is not JSON", modeGarbage},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, rigOptions{enforce: past})
			r.signIn()
			r.edge.set(c.mode)
			r.backdate(2 * time.Hour) // the access token expired an hour ago: the next command asks for another

			if st, code := r.status(); st.State != "grace" || st.Reason != "server_error" || code != 0 || r.edge.refreshes() == 0 {
				t.Fatalf("expired token, refresh answered by %s: exit %d, %+v, %d refresh attempts; only invalid_grant locks", c.name, code, st, r.edge.refreshes())
			}
			res := r.run("--json", "workflow", "list")
			mustExit(t, res, 0)
			if !strings.Contains(res.stderr, graceLine) || strings.Contains(res.stdout, graceLine) {
				t.Fatalf("grace is one line on stderr and never on stdout:\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
			}
			if _, err := os.Stat(r.refreshFile()); err != nil {
				t.Fatalf("the refresh token must be kept: %v", err)
			}
			// monoes.me recovers: the refresh token that was kept is still good, so the next refresh
			// ends the grace (aged again, so that the failed attempt's minute is over).
			r.edge.set(modePass)
			r.backdate(2 * time.Hour)
			if st, code := r.status(); st.State != "ok" || code != 0 {
				t.Fatalf("after monoes.me recovered: exit %d, %+v", code, st)
			}
		})
	}
}

// Spec 4.7 and A9: a refreshed token signed by a key this build does not pin is not stored, and the
// grace shows server_error; when the grace ends the verdict is locked(key_unknown), the case that
// wants an update and not another sign-in, and doctor's fix is the update.
func TestAnUnknownSigningKeyIsTheRunUpdateCase(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	r.edge.set(modeUnknownKey)
	r.backdate(2 * time.Hour)
	if st, code := r.status(); st.State != "grace" || st.Reason != "server_error" || code != 0 || r.edge.refreshes() == 0 {
		t.Fatalf("a refresh answered with a token of an unknown key: exit %d, %+v", code, st)
	}
	r.backdate(25 * time.Hour) // the grace is over
	if st, code := r.status(); st.State != "locked" || st.Reason != "key_unknown" || code != 4 {
		t.Fatalf("at the end of the grace: exit %d, %+v, want locked(key_unknown)", code, st)
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "key_unknown", true)
	var report struct {
		Results []struct {
			ID, Status string
			Fix        *struct{ ID string }
		}
	}
	mustJSON(t, r.run("--json", "doctor", "--check", "core.monoes_account").stdout, &report)
	found := false
	for _, x := range report.Results {
		if x.ID == "core.monoes_account" {
			found = true
			if x.Status != "fail" || x.Fix == nil || x.Fix.ID != "core.update.install" {
				t.Fatalf("doctor row for an unknown key: %+v, want fail with the update as its fix", x)
			}
		}
	}
	if !found {
		t.Fatal("doctor has no core.monoes_account row")
	}
}

// Acceptance 2: signed in, then blocked on monoes.me: the machine is locked within one refresh
// interval, work in flight in the daemon is cancelled, the daemon stays up and resumes by itself
// when a valid session appears.
func TestBlockedAccountLocksAndCancelsWorkInFlight(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past, ttl: shortToken})
	r.signIn()
	d := r.startDaemon()
	r.waitFor("the daemon to report a signed-in account", 30*time.Second, func() bool { return r.accountState() == "ok" })
	pid, _, _ := r.heartbeat()
	fast, slow := r.workflow(waitWorkflow("smoke-fast", 1), true), r.workflow(waitWorkflow("smoke-slow", 300), true)
	id := r.enqueue(slow)
	r.waitFor("the slow run to be running in the daemon", 60*time.Second, func() bool {
		e := r.execution(id)
		return e.Status == "RUNNING" && e.PID == pid
	})

	r.edge.set(modeInvalidGrant) // monoes.me answers no
	r.waitFor("the run in flight to be cancelled", 120*time.Second, func() bool { return r.execution(id).Status == "CANCELLED" })
	r.waitFor("the heartbeat to say locked", 30*time.Second, func() bool { return r.accountState() == "locked" })
	if _, _, reason := r.heartbeat(); reason != "refused" {
		t.Fatalf("the heartbeat's reason is %q, want refused", reason)
	}
	if !d.alive() {
		t.Fatal("a locked daemon stays up: a service manager would only restart it in a loop")
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "refused", true)
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("a refusal deletes the refresh token: %v", err)
	}

	r.edge.set(modePass)
	r.signIn()
	r.waitFor("the daemon to resume by itself", 60*time.Second, func() bool { return r.accountState() == "ok" })
	again := r.enqueue(fast)
	r.waitFor("the resumed daemon to run work", 60*time.Second, func() bool { return r.execution(again).Status == "SUCCESS" })
}
```

- [ ] **Step 2: Run them.**

```
go vet -tags devaccount ./internal/accountsmoke/
go test -tags devaccount ./internal/accountsmoke/ -run '^(TestUnreachableIsGraceUntilTwentyFourHours|TestAnswersThatAreNotARefusalKeepTheGrace|TestAnUnknownSigningKeyIsTheRunUpdateCase|TestBlockedAccountLocksAndCancelsWorkInFlight)$' -count=1 -timeout 20m -v
```
Expected: no vet output; the four tests and the four subtests of the second `--- PASS`, three to five minutes together. The unreachable test ends with a 40-second job that finishes `SUCCESS` after the 24 hours ran out (unreachable is not a refusal); the blocked test ends with the slow run `CANCELLED`, the heartbeat `locked`/`refused`, `refresh.enc` gone, and the daemon running work again after one sign-in.

- [ ] **Step 3: Prove they can fail.** In `TestBlockedAccountLocksAndCancelsWorkInFlight` change `r.edge.set(modeInvalidGrant)` to `r.edge.set(modeDown)` and run that test alone: expect FAIL after two minutes with `timed out after 2m0s waiting for the run in flight to be cancelled` (monoes.me answering nothing at all is the grace, never a cancel: D27). Restore the line. In `TestAnswersThatAreNotARefusalKeepTheGrace` change the row `{"a server error", modeServerError}` to `{"a server error", modeInvalidGrant}` and run that test alone: expect the subtest `a_server_error` to FAIL with `exit 4` and a `locked` state (an `invalid_grant` is a refusal). Restore the line and run both tests again: PASS.

- [ ] **Step 4: Commit.**

```
git add internal/accountsmoke/gate_test.go
git commit -m "test(account): real-binary smoke of grace, expiry, trouble that is not a refusal, an unknown key and a block" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3b: What the second security review found: an interrupted or killed refresh, a logout, a refusal with the clock set back, a blocked key store

The review of B1a and of these plans found losses that no scenario above would notice. A refresh the caller abandons while monoes.me is answering ends every install of the account (spec A20), and so does one whose process is killed, unless the guard knows that a grant may have been lost and stops presenting the token (A24). A logout that deletes `session.json` lets an account that monoes.me has blocked out from under the clock guard (A23), and so does a refusal that leaves nothing behind on a machine that never signed in (A25). A key store that waits for ever holds the machine's session lock against every other process (A22). Each has its fix and its unit tests in an earlier phase; this task proves them in the real processes. A failure names the phase that owns the behaviour (B1a's guard and store, B1b's logout, B2's `run()`): fix it there with a unit test, never by loosening the smoke.

**Files:**
- Create: `internal/accountsmoke/security_test.go`

**Interfaces:**
- Consumes: Task 2's rig and its helpers (`newRig` with `rigOptions.sharing`, `rig.edge.set(mode)` with `modeHold` and `modePass`, `rig.edge.refreshes()`, `rig.edge.later`, `reuseWindow`, `rig.fake.Replays`, `rig.start`, `rig.startWith`, `proc`, `rig.backdate`, `rig.signIn`, `rig.status`, `rig.run`, `rig.assertLocked`, `rig.waitFor`, `rig.refreshFile`, `rig.accountDir`, `rig.session`, `rig.read`, `rig.enforce`, `past`, `unconfirmedLine`, `accountStatus`, `mustExit`, `mustJSON`) and B1a's `account.OpenStore`, `account.Session` (with `PendingSince`), `Store.Lock`, `Store.Load` and `Store.Save`.
- Produces: `TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount` (A20: Ctrl-C and SIGTERM during a refresh), `TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow` and `TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow` (A24: SIGKILL during a refresh), `TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack` (A25), `TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack` (A23) and `TestABlockedKeyStoreDoesNotHoldUpAnotherProcess` (A22), and the rig helpers `agePending`, `killedMidRefresh`, `setMark`, `assertStillEnforcedWithTheClockSetBack` and `lockHeld`. The edge keeps monoes.me's reuse window (Task 2): a refresh token presented again within 300 seconds of its first presentation is answered with the first answer, and a repeat after the window is a replay that the fake punishes (`invalid_grant`, every token of the account revoked, `Replays` counted). A scenario that needs the window to pass moves the edge's clock (`later`) and, because a real binary has no clock of its own to move, ages what the binary compares with the clock (`backdate` for a token, `agePending` for the marker of a grant in flight); none of them waits.

- [ ] **Step 1: Write the scenarios.** `internal/accountsmoke/security_test.go`:

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// setMark rewrites the high-water mark of the stored session: the highest time the machine has seen.
func (r *rig) setMark(at time.Time) {
	r.t.Helper()
	st := account.OpenStore(r.accountDir(), account.NewMemorySealer())
	unlock, err := st.Lock(context.Background())
	must(r.t, err)
	defer unlock()
	sess, err := st.Load()
	must(r.t, err)
	if sess == nil {
		r.t.Fatal("no session to move the mark of")
	}
	sess.HW = at
	must(r.t, st.Save(sess))
}

// agePending makes the stored marker of a refresh in flight (pending_since, A24) d older. A real binary
// has no clock to move, so the smoke ages what the clock is compared with, as backdate does for a token.
func (r *rig) agePending(d time.Duration) {
	r.t.Helper()
	st := account.OpenStore(r.accountDir(), account.NewMemorySealer())
	unlock, err := st.Lock(context.Background())
	must(r.t, err)
	defer unlock()
	sess, err := st.Load()
	must(r.t, err)
	if sess == nil || sess.PendingSince.IsZero() {
		r.t.Fatal("no refresh in flight to age")
	}
	sess.PendingSince = sess.PendingSince.Add(-d)
	must(r.t, st.Save(sess))
}

// killedMidRefresh signs in, expires the access token and kills the command that is in the middle of the
// refresh. monoes.me has rotated the refresh token (modeHold: the edge passes the grant to the fake at once
// and answers holdAnswer later), the answer is lost with the process, and the dead token is still on disk.
// What is left for the next command to find is the marker that the guard wrote before it sent the grant.
func (r *rig) killedMidRefresh() {
	r.t.Helper()
	r.signIn()
	r.backdate(2 * time.Hour) // the access token expired an hour ago: the next command asks for another
	r.edge.set(modeHold)
	p := r.start("account", "status", "--json")
	r.waitFor("the refresh grant to reach monoes.me", 30*time.Second, func() bool { return r.edge.refreshes() == 1 })
	if sess := r.session(); sess == nil || sess.PendingSince.IsZero() {
		r.t.Fatal("the grant was sent before pending_since was written (A24)")
	}
	must(r.t, p.cmd.Process.Kill()) // nothing can finish the grant, and the answer is lost with the process
	<-p.done
	r.edge.set(modePass)
}

// assertStillEnforcedWithTheClockSetBack is what a clock set back looks like to the guard: the date is
// ahead of the real clock and the stored high-water mark says that the machine has seen a time after it.
// A real binary has no clock to set, so the smoke moves the date and the mark. The machine must still be
// refused, and the same date on a machine with no record must be a warn period, in which nothing is.
func (r *rig) assertStillEnforcedWithTheClockSetBack() {
	r.t.Helper()
	date := time.Now().Add(24 * time.Hour).Truncate(time.Second) // the clock is now before this date
	r.setMark(date.Add(time.Hour))                               // and the machine has seen a time after it
	r.enforce = date
	r.assertLocked(r.run("--json", "workflow", "list"), "not_logged_in", true)
	if st, code := r.status(); st.State != "locked" || st.Reason != "not_logged_in" || !st.Enforced || code != 4 {
		r.t.Fatalf("account status with the clock set back: exit %d, state %q, reason %q, enforced %v, want locked(not_logged_in), enforced", code, st.State, st.Reason, st.Enforced)
	}
	// The same date and clock on a machine with no record are a warn period, and nothing is refused: the
	// case spec 4.8 accepts (a clock set back before the first check).
	mustExit(r.t, newRig(r.t, rigOptions{enforce: date}).run("workflow", "list"), 0)
}

// lockHeld says whether a process holds the machine's session lock right now.
func (r *rig) lockHeld() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	unlock, err := account.OpenStore(r.accountDir(), account.NewMemorySealer()).Lock(ctx)
	if err == nil {
		unlock()
	}
	return err != nil
}

// A20: a refresh grant, once sent, is always completed and stored. monoes.me rotates the refresh token
// when it answers, and the answer is the only copy of the new one: a command that gives up while the
// answer is on its way leaves the dead token on disk, and the next refresh would end every install of
// the account (spec A7). Here the answer is held back (modeHold) and the command is interrupted while it
// is on its way. The command finishes the grant before it ends, so the next refresh presents the new
// token and nothing is revoked. The edge keeps monoes.me's reuse window, so the test lets it pass
// (later) before the next command: a dead token presented after the window is a replay, which the fake
// punishes, and the scenario needs no wait.
func TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount(t *testing.T) {
	for _, c := range []struct {
		name string
		sig  os.Signal
	}{{"Ctrl-C", syscall.SIGINT}, {"SIGTERM", syscall.SIGTERM}} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, rigOptions{enforce: past})
			r.signIn()
			r.backdate(2 * time.Hour) // the access token expired an hour ago: the next command asks for another
			r.edge.set(modeHold)      // monoes.me rotates the refresh token at once and answers a few seconds later
			p := r.start("account", "status", "--json")
			r.waitFor("the refresh grant to reach monoes.me", 30*time.Second, func() bool { return r.edge.refreshes() == 1 })
			must(t, p.cmd.Process.Signal(c.sig)) // while the answer is on its way
			r.waitFor("the interrupted command to end", 40*time.Second, func() bool { return !p.alive() })

			r.edge.set(modePass)
			r.edge.later(reuseWindow + time.Second) // five minutes pass at monoes.me: it no longer repeats its answer
			r.backdate(2 * time.Hour)               // what the interrupted command stored is aged too: the next command asks again
			st, code := r.status()
			if st.State != "ok" || code != 0 || r.fake.Replays != 0 || r.edge.refreshes() != 2 {
				t.Fatalf("the refresh after the interruption: exit %d, state %q, reason %q, %d refreshes, %d replays: the interrupted grant was not completed and stored",
					code, st.State, st.Reason, r.edge.refreshes(), r.fake.Replays)
			}
		})
	}
}

// A24, the case A20 cannot close: a command that is killed (SIGKILL, a crash, a power cut) while monoes.me
// is answering cannot finish the grant, because the refresh token is rotated and the new one is lost with
// the process. The guard wrote pending_since before it sent the grant, so the next attempt knows that a
// grant may have been lost. Inside monoes.me's reuse window the retry is immediate and monoes.me repeats
// its answer: a kill costs nothing, and the account is whole.
func TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.killedMidRefresh()
	st, code := r.status() // seconds later, inside the window
	if st.State != "ok" || code != 0 || r.edge.refreshes() != 2 || r.fake.Replays != 0 {
		t.Fatalf("the retry inside the window: exit %d, state %q, reason %q, %d refreshes, %d replays: the guard must present the token again at once, and monoes.me repeats its answer",
			code, st.State, st.Reason, r.edge.refreshes(), r.fake.Replays)
	}
	if sess := r.session(); sess == nil || !sess.PendingSince.IsZero() {
		t.Fatal("the answer to the retry must clear pending_since")
	}
	if _, err := os.Stat(r.refreshFile()); err != nil {
		t.Fatalf("the retry must store the refresh token it was answered with: %v", err)
	}
	r.backdate(2 * time.Hour) // and the account is whole: the next refresh presents the new token
	if st, code := r.status(); st.State != "ok" || code != 0 || r.edge.refreshes() != 3 || r.fake.Replays != 0 {
		t.Fatalf("the refresh after the retry: exit %d, state %q, reason %q, %d refreshes, %d replays", code, st.State, st.Reason, r.edge.refreshes(), r.fake.Replays)
	}
}

// A24, after the window: monoes.me no longer repeats its answer, so presenting the dead token would end
// every install of the account (A7). The guard does not present it: it deletes this machine's refresh
// token, makes no call, and says why (unconfirmed, a grace reason). One install signs in again; the
// account and the other install are untouched. Two installs of one account share monoes.me and the edge.
// The window passes at monoes.me (later) and, on this machine, in the marker (agePending), which is what
// the clock does to a real process.
func TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	other := newRig(t, rigOptions{enforce: past, sharing: r}) // a second install of the same account
	other.signIn()
	r.killedMidRefresh()
	r.edge.later(reuseWindow + 10*time.Second) // monoes.me no longer answers a repeat of the dead token
	r.agePending(reuseWindow + 10*time.Second) // and on this machine the wait is over too
	grants := r.edge.refreshes()

	st, code := r.status()
	if st.State != "grace" || st.Reason != "unconfirmed" || code != 0 || r.edge.refreshes() != grants || r.fake.Replays != 0 {
		t.Fatalf("the attempt after the window: exit %d, state %q, reason %q, %d new grants, %d replays: want grace(unconfirmed) and no call to monoes.me",
			code, st.State, st.Reason, r.edge.refreshes()-grants, r.fake.Replays)
	}
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("this machine must drop the refresh token it cannot vouch for: %v", err)
	}
	res := r.run("--json", "workflow", "list") // what the person sees: the work goes on, and the line says what to do
	mustExit(t, res, 0)
	if !strings.Contains(res.stderr, unconfirmedLine) || strings.Contains(res.stdout, unconfirmedLine) || !strings.HasPrefix(strings.TrimSpace(res.stdout), "[") {
		t.Fatalf("the unconfirmed grace is one line on stderr and never on stdout:\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
	}

	other.backdate(2 * time.Hour) // the account was not revoked: the other install still refreshes
	if st, code := other.status(); st.State != "ok" || code != 0 || r.fake.Replays != 0 {
		t.Fatalf("the other install after this one dropped its token: exit %d, state %q, reason %q, %d replays: the account was ended", code, st.State, st.Reason, r.fake.Replays)
	}

	r.backdate(25 * time.Hour) // the grace is over: locked, with the reason that says why
	if st, code := r.status(); st.State != "locked" || st.Reason != "unconfirmed" || code != 4 {
		t.Fatalf("at the end of the grace: exit %d, state %q, reason %q, want locked(unconfirmed)", code, st.State, st.Reason)
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "unconfirmed", true)
	r.signIn() // this machine signs in again
	mustExit(t, r.run("workflow", "list"), 0)
}

// A25, and spec 4.5: a machine that is refused after the date keeps the clock-guard record from that
// moment, so setting its clock back before the date does not un-enforce it while the record is left in
// place (spec 4.8). It never signed in, and it still needs to.
func TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.assertLocked(r.run("workflow", "list"), "not_logged_in", false) // the machine runs after the date: the refusal leaves the record
	if sess := r.session(); sess == nil || sess.AccessToken != "" || sess.HW.IsZero() {
		t.Fatal("a refusal after the date must leave the clock-guard record: a session with no token and a high-water mark")
	}
	r.assertStillEnforcedWithTheClockSetBack()
}

// A23, and spec 4.5: the high-water mark is what makes setting the clock back worthless to a user who
// leaves the account folder alone (spec 4.8), and it lives in session.json, so logging out, an open
// command, must not erase it. The mark says the machine ran after the date, and the date is then put
// ahead of the real clock, which is what a clock set back looks like to the guard.
func TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	mustExit(t, r.run("workflow", "list"), 0) // the machine runs, after the date
	mustExit(t, r.run("account", "logout"), 0)
	if sess := r.session(); sess == nil || sess.AccessToken != "" || sess.HW.IsZero() {
		t.Fatal("logging out must leave the clock-guard record: a session with no token and a high-water mark")
	}
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("logging out deletes the refresh token: %v", err)
	}
	r.assertStillEnforcedWithTheClockSetBack()
}

// A22: a key store that waits for ever (a locked keychain, an unlock dialog nobody answers) must not hold
// the machine's session lock, and with it every other process's refresh, for ever. A refresh reads the
// refresh token, and so calls the key store, while it holds the lock; every such call is bounded to 10
// seconds and a timeout is keyring_unavailable (grace). The key store here is the rig's file keyring
// (MONOAGENT_ALLOW_FILE_KEYRING, which the quiet read of the key honors on every OS), and its passphrase
// file is a named pipe that nobody writes to for the one process that is given that path: opening it
// blocks, as an unanswered unlock dialog would.
func TestABlockedKeyStoreDoesNotHoldUpAnotherProcess(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	r.backdate(2 * time.Hour) // the access token expired an hour ago: both commands below are due for a refresh
	pipe := filepath.Join(r.dir, "passphrase.pipe")
	must(t, syscall.Mkfifo(pipe, 0o600))

	stuck := r.startWith([]string{"MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE=" + pipe}, "account", "status", "--json")
	r.waitFor("the blocked command to hold the session lock or to end", 20*time.Second, func() bool { return !stuck.alive() || r.lockHeld() })
	if !stuck.alive() {
		t.Skip("the key store answered without reading the passphrase file, so the pipe blocks nothing here")
	}
	began := time.Now()
	res := r.run("account", "status", "--json") // another process, whose key store answers
	waited := time.Since(began)
	var doc accountStatus
	mustJSON(t, res.stdout, &doc)
	if waited > 20*time.Second || res.code != 0 || (doc.State != "ok" && doc.State != "grace") {
		t.Fatalf("another process behind a blocked key store: waited %v, exit %d, state %q: its wait must stay bounded (about 10 seconds) and account status must still answer",
			waited, res.code, doc.State)
	}
	r.waitFor("the blocked command to give up", 20*time.Second, func() bool { return !stuck.alive() })
	if stuck.err != nil || !strings.Contains(r.read("account.log"), "keyring_unavailable") {
		t.Fatalf("the blocked command must end by itself, in grace with reason keyring_unavailable: %v\n%s", stuck.err, r.read("account.log"))
	}

	// Nothing was lost: with the key store answering, the next refresh works.
	r.backdate(2 * time.Hour)
	if st, code := r.status(); st.State != "ok" || code != 0 {
		t.Fatalf("after the key store answered again: exit %d, state %q, reason %q", code, st.State, st.Reason)
	}
}
```

- [ ] **Step 2: Run them.**

```
go vet -tags devaccount ./internal/accountsmoke/
go test -tags devaccount ./internal/accountsmoke/ -run '^(TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount|TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow|TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow|TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack|TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack|TestABlockedKeyStoreDoesNotHoldUpAnotherProcess)$' -count=1 -timeout 15m -v
```
Expected: no vet output; the interrupted test with its two subtests and the other five tests `--- PASS`, about two minutes together after the first build of the CLI (one to two minutes the first time). The interrupted test ends with the account `ok`, two refresh grants at monoes.me and no replay; the retried-at-once test ends with the account `ok`, two grants for the kill and its retry, a third for the next refresh, and no replay; the dropped-token test ends in `grace(unconfirmed)` with no new grant, the other install refreshing, `locked(unconfirmed)` once the grace is over and one sign-in after that; the key-store test shows the second process answering `grace` or `ok` within about ten seconds. If the key-store test reports `--- SKIP`, the key store answered without reading the passphrase file (an OS keychain entry for the account's key exists on this machine) and nothing was proven: run it on a machine without one, as CI's runner is.

- [ ] **Step 3: Prove they can fail.** Each test failed against the behaviour it guards when this plan was written. In `internal/account/guard_refresh.go` (B1a), replace `context.WithoutCancel(ctx)` in the call that bounds the grant with `ctx`, and run the interrupted test: expect FAIL in both subtests with `state "locked", reason "refused", 2 refreshes, 1 replays` (the interrupted command lost the answer, and the next refresh presented the dead token after the window). In the same function delete the save of `pending_since` before the grant is sent: both A24 tests FAIL at `the grant was sent before pending_since was written`. Make the age rule never drop the token (replace `age > pendingRetryWindow` by `false` in the check that decides): `TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow` FAILS with `state "locked", reason "refused"` and `1 replays` (the dead token was presented after the window and the account ended). Set `pendingRetryWindow` to zero in `guard.go`: `TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow` FAILS with `state "grace", reason "unconfirmed"` (the token was dropped although monoes.me would have repeated its answer). In `touchHW` (B1a) make the branch for a machine with no session write nothing: `TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack` FAILS at its first check and `TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten` at the files left in HOME. In `internal/account/logout.go` (B1b) make `clockRecord` return nil, and run the logout test: expect FAIL at the first check, `logging out must leave the clock-guard record`. In `internal/account/store.go` (B1a) raise `keyStoreTimeout` from ten seconds to ten minutes, and run the key-store test: expect FAIL with `waited 25s` (the second process gave up on the lock). Restore each file with `git checkout -- <file>` and run the six tests again: PASS.

- [ ] **Step 4: Commit.**

```
git add internal/accountsmoke/security_test.go
git commit -m "test(account): real-binary smoke of an interrupted or killed refresh, a logout or a refusal with the clock set back and a blocked key store" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4: Every door, locked and signed in

Acceptance 1 says every door refuses a locked machine. B3b proves each door in-process; this proves them in the real processes, in a daemon that started locked (a serving command, spec A1) and then signed in, without a restart.

**Files:**
- Create: `internal/accountsmoke/doors_test.go`

**Interfaces:**
- Consumes: Task 2's rig and clients (`rig.bridge()`, `rig.mcp()`, `mcpConn.try`, `probe`, `rig.health`), `rig.heartbeatAccount`, `accountReport.is`, `past`, `loginLine`, `testEmail`.
- Consumes, the wire shapes of B3b, answered to a request that carries no credential:

| Door | Locked | Signed in |
|---|---|---|
| `GET /health` (open in every state) | 200, `account` `{locked, not_logged_in, enforced: true}`, no `valid_until`, the user never named | 200, `account` `{ok, enforced: true, valid_until}` |
| `GET /workflows` | 401 `{"error":"login_required","login_required":true,"account":{…}}`, no `WWW-Authenticate` | 401 `missing or invalid bearer credential`, with `WWW-Authenticate` |
| `GET /v1/models` | 401, error code `login_required` | 401, error code `invalid_api_key` |
| `POST /org-endpoint/ep_x` | 401 `login_required` | 404 `unknown endpoint` |
| `POST /webhook/nope` on the webhook port | 503 `{"error":"login_required"}`, `Retry-After: 60` | 404 `webhook not found` |
| bridge request `doc.lookup` | reply with `code: "account_locked"` and no `ok` key | any other answer |
| bridge `ping` | answers, `data.account` `{locked, …}` | `data.account` `{ok, …}` |
| MCP `tools/call` | `isError` with the text `Log in to monoes.me first: monoagentcli account login`; `initialize` and `tools/list` still answer | the tool's own result |

- Produces: `TestEveryDoorRefusesWhenLockedAndOpensWhenSignedIn`. The daemon's own evidence while locked is that it stays up, logs `monoagentcli account login`, and reports `account: locked` in its heartbeat and `/health`.

- [ ] **Step 1: Write the test.** `internal/accountsmoke/doors_test.go`:

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"strings"
	"testing"
	"time"
)

// Acceptance 1, door by door, in the real processes (the wire shapes are B3b's). The daemon is a
// serving command: started with no session after the date it stays up, logs the command to run,
// reports account: locked in /health and its heartbeat, runs nothing, and every door refuses.
// Signing in opens every door without a restart.
func TestEveryDoorRefusesWhenLockedAndOpensWhenSignedIn(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	d := r.startDaemon()
	r.start("extension", "serve")
	ask := r.bridge()
	mcp := r.mcp(nil, "mcp")
	mcp.call("initialize", map[string]any{})
	api, hook := "http://"+r.addr("api"), "http://"+r.addr("webhook")

	// Locked.
	if !d.alive() || !strings.Contains(r.read("daemon.log"), "monoagentcli account login") {
		t.Fatalf("a locked daemon stays up and logs the command to run:\n%s", r.read("daemon.log"))
	}
	_, hb := r.heartbeatAccount()
	hb.is(t, "locked", true, false)
	r.health(api).is(t, "locked", true, false)
	if a := probe(t, "GET", api+"/workflows", ""); a.code != 401 || !strings.Contains(a.body, `"login_required"`) || a.header.Get("WWW-Authenticate") != "" {
		t.Errorf("the HTTP API door, locked: %d %s", a.code, a.body)
	}
	if a := probe(t, "GET", api+"/v1/models", ""); a.code != 401 || !strings.Contains(a.body, `"code":"login_required"`) {
		t.Errorf("the /v1 door, locked: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", api+"/org-endpoint/ep_x", "{}"); a.code != 401 || !strings.Contains(a.body, "login_required") {
		t.Errorf("the org receiver door, locked: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", hook+"/webhook/nope", "{}"); a.code != 503 || a.header.Get("Retry-After") != "60" || !strings.Contains(a.body, "login_required") {
		t.Errorf("the webhook door, locked: %d %v %s", a.code, a.header, a.body)
	}
	if raw, reply := ask("doc.lookup"); reply.Code != "account_locked" || strings.Contains(raw, testEmail) {
		t.Errorf("the bridge door, locked: %s", raw)
	}
	if raw, reply := ask("ping"); !reply.OK || strings.Contains(raw, testEmail) {
		t.Errorf("ping answers a locked bridge, so that the side panel can say so: %s", raw)
	} else {
		reply.Data.Account.is(t, "locked", true, false)
	}
	if text, isErr := mcp.try("workflow_list", map[string]any{}); !isErr || text != loginLine {
		t.Errorf("the MCP door, locked: %v %q", isErr, text)
	}
	if res := mcp.call("tools/list", map[string]any{}); res["tools"] == nil {
		t.Error("tools/list still answers when locked")
	}

	// Signed in: no restart, each door sees the session within its poll interval.
	r.signIn()
	open := func(what string, ok func() bool) { r.waitFor(what+" to open", 60*time.Second, ok) }
	open("the HTTP API", func() bool { return probe(t, "GET", api+"/workflows", "").header.Get("WWW-Authenticate") != "" })
	if a := probe(t, "GET", api+"/workflows", ""); a.code != 401 || !strings.Contains(a.body, "missing or invalid bearer credential") {
		t.Errorf("the HTTP API door, signed in: %d %s", a.code, a.body)
	}
	if a := probe(t, "GET", api+"/v1/models", ""); a.code != 401 || !strings.Contains(a.body, "invalid_api_key") {
		t.Errorf("the /v1 door, signed in: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", api+"/org-endpoint/ep_x", "{}"); a.code != 404 || !strings.Contains(a.body, "unknown endpoint") {
		t.Errorf("the org receiver door, signed in: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", hook+"/webhook/nope", "{}"); a.code != 404 || !strings.Contains(a.body, "webhook not found") {
		t.Errorf("the webhook door, signed in: %d %s", a.code, a.body)
	}
	open("the bridge", func() bool { _, reply := ask("doc.lookup"); return reply.Code != "account_locked" })
	if raw, reply := ask("ping"); !reply.OK {
		t.Errorf("ping, signed in: %s", raw)
	} else {
		reply.Data.Account.is(t, "ok", true, true)
	}
	open("MCP", func() bool { _, isErr := mcp.try("workflow_list", map[string]any{}); return !isErr })
	open("the heartbeat", func() bool { return r.accountState() == "ok" })
	_, hb = r.heartbeatAccount()
	hb.is(t, "ok", true, true)
	r.health(api).is(t, "ok", true, true)
}
```

- [ ] **Step 2: Check that it compiles.** `go vet -tags devaccount ./internal/accountsmoke/`. Expected: no output.

- [ ] **Step 3: Run it.**

```
go test -tags devaccount ./internal/accountsmoke/ -run '^TestEveryDoorRefusesWhenLockedAndOpensWhenSignedIn$' -count=1 -timeout 15m -v
```
Expected: `--- PASS`, under a minute (a long-running process looks for a new session when it is asked, at most once every 5 seconds: spec A8). A failing line names the door and the answer it got: the door is the defect, in B3b's plan (or B3a's, for the heartbeat).

- [ ] **Step 4: Prove it can fail.** Change `rigOptions{enforce: past}` to `rigOptions{enforce: future}` and expect FAIL in the locked part, at the first line that depends on enforcement: the daemon's log has no `monoagentcli account login`, or its heartbeat reports `enforced` false (`the account report is ... want state locked, enforced true`), because before the date nothing is locked and no door refuses. Restore the line and run it again: PASS.

- [ ] **Step 5: Commit.**

```
git add internal/accountsmoke/doors_test.go
git commit -m "test(account): real-binary smoke of every door, locked and signed in" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 5: One operation through every entry point

Spec §11 asks for the signed-in path through every process type and entry point. It is the check no unit test can make: in a test binary `account.Require` fails open when no guard is installed (D24), so a process type that forgot to install one passes everything in B1 to B4 and then fails closed, with `login_required`, for every signed-in user of the release. Here the real binary runs each entry point once, signed in, and the operation must succeed.

**Files:**
- Create: `internal/accountsmoke/entrypoints_test.go`

**Interfaces:**
- Consumes: Task 2's rig and clients (`rig.sub`, `rig.run`, `rig.start`, `rig.startDaemon`, `rig.addr`, `rig.read`, `rig.workflow`, `waitWorkflow`, `rig.enqueue`, `rig.execution`, `rig.accountState`, `rig.heartbeatAccount`, `rig.mcp`, `rig.bridge`, `get`, `mustExit`, `mustJSON`, `accountReport.is`, `rig.assertLocked`, `testEmail`, `past`) and the fake monomind (`monomind.log` records every call: `agent exec` for a chat turn, `org serve` for the organization receiver).
- Consumes, existing at master and checked there with a binary that has no gate: `workflow run <id> --timeout`, `httpapi --addr` (the bearer token is created in the vault by the first request and read with `secret reveal httpapi-token --reveal`), `mcp` and `mcp --grant <id> --profile <name>` (stdio, the way monomind spawns an org role's tool server), `extension serve` (the bridge on the rig's port), `org serve --foreground`, `chat history create --runtime claude` and `chat --conversation <id> --turn <t> -- <text>`.
- Produces: `TestEveryEntryPointWorksSignedIn`, one subtest per entry point:

| Subtest | Operation | It worked when |
|---|---|---|
| `workflow run` | a one-second workflow, `--json workflow run`, in a CLI process | exit 0, status `SUCCESS` |
| `daemon` | an execution row queued in the database, adopted by the daemon | the daemon's own pid ran it to `SUCCESS`, and its heartbeat said `ok`, `enforced`, with a `valid_until` |
| `httpapi` | `GET /workflows` with the vault's bearer token, in a stand-alone `httpapi` | 200 and the workflow listed; `/health` open and `ok` |
| `mcp` | the `workflow_list` tool over stdio | the tool's own result |
| `mcp --grant child` | the grant's tool `automation_publish_post`, with the org trace in `_meta`, which runs the workflow in the daemon | the text `published hello` |
| `extension serve` | the bridge's `ping` and `doc.lookup` | `pong` and an `ok` account that never names the user; no `account_locked` |
| `org serve` | `org serve --foreground` with the fake monomind | monomind was started with `org serve`, and a SIGTERM ends the process cleanly |
| `chat` | a turn on a new conversation, in a CLI process | the last event is `turn.finished` with status `completed`, and monomind was asked to `agent exec` |

At the end the daemon is still alive, `account logout` exits 0 and the next command is refused (`not_logged_in`, exit 4): the same machine, closed again.

- [ ] **Step 1: Write the test.** `internal/accountsmoke/entrypoints_test.go`:

```go
//go:build devaccount && !windows

package accountsmoke

import (
	"strings"
	"testing"
	"time"
)

// The signed-in path through every process type and entry point (spec §11). Each subtest runs one
// operation of one entry point and expects it to succeed: in a test binary Require fails open
// without a guard, so a path that forgot to install one passes the unit tests and would fail closed
// for signed-in users in production. Logging out closes the machine again.
func TestEveryEntryPointWorksSignedIn(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	fast := r.workflow(waitWorkflow("smoke-fast", 1), true)

	t.Run("workflow run", func(t *testing.T) {
		res := r.sub(t).run("--json", "workflow", "run", fast, "--timeout", "60s")
		mustExit(t, res, 0)
		var rec struct{ Status string }
		mustJSON(t, res.stdout, &rec)
		if rec.Status != "SUCCESS" {
			t.Fatalf("the run ended %s", rec.Status)
		}
	})

	d := r.startDaemon()
	t.Run("daemon", func(t *testing.T) {
		r := r.sub(t)
		r.waitFor("the heartbeat to report an ok account", 30*time.Second, func() bool { return r.accountState() == "ok" })
		pid, report := r.heartbeatAccount()
		report.is(t, "ok", true, true)
		id := r.enqueue(fast) // adopted by the daemon, which runs it through handleExecution
		r.waitFor("the daemon to run the queued execution", 60*time.Second, func() bool {
			e := r.execution(id)
			return e.Status == "SUCCESS" && e.PID == pid
		})
	})

	t.Run("httpapi", func(t *testing.T) {
		r := r.sub(t)
		p := r.start("httpapi", "--addr", r.addr("httpapi"))
		base := "http://" + r.addr("httpapi")
		r.waitFor("the HTTP API", 30*time.Second, func() bool { code, _ := get(t, base+"/health", ""); return code == 200 })
		var health struct{ Account *accountReport }
		_, body := get(t, base+"/health", "") // open, with no credential, and it says what the account is
		mustJSON(t, body, &health)
		health.Account.is(t, "ok", true, true)
		get(t, base+"/workflows", "") // the first request creates the bearer token in the vault
		token := strings.TrimSpace(r.run("secret", "reveal", "httpapi-token", "--reveal").stdout)
		if code, body := get(t, base+"/workflows", token); code != 200 || !strings.Contains(body, fast) {
			t.Fatalf("GET /workflows with the bearer: %d", code)
		}
		p.stop()
	})

	t.Run("mcp", func(t *testing.T) {
		c := r.sub(t).mcp(nil, "mcp")
		c.call("initialize", map[string]any{})
		if text := c.tool("workflow_list", map[string]any{}, nil); !strings.Contains(text, fast) {
			t.Fatalf("workflow_list: %s", text)
		}
	})

	t.Run("mcp --grant child", func(t *testing.T) {
		r := r.sub(t)
		wf := r.workflow(`{"name":"Publish post","version":1,"is_active":false,
 "nodes":[{"id":"t","type":"trigger.manual","name":"Start","config":{}},
  {"id":"s","type":"core.set","name":"Publish","config":{"assignments":"[{\"field\":\"result\",\"value\":\"published {{ $json.input.text }}\"}]","include_input":false}}],
 "connections":[{"id":"c","source":"t","source_handle":"main","target":"s","target_handle":"main"}]}`, false)
		mustExit(t, r.run("org", "create-json", "growth", "--json", `{"name":"growth","goal":"Publish posts.","status":"stopped","schedule":null,"roles":[{"id":"lead","title":"Lead","type":"boss","reports_to":null,"responsibilities":["Decide."]},{"id":"writer","title":"Writer","type":"specialist","reports_to":"lead","responsibilities":["Write posts."]}]}`), 0)
		mustExit(t, r.run("org", "automation", "add", "growth", "--workflow", wf, "--alias", "publish_post"), 0)
		res := r.run("org", "grant", "add", "growth", "--role", "writer", "--automation", "publish_post", "--approval", "none")
		mustExit(t, res, 0)
		var grant struct{ Grant struct{ ID string } }
		mustJSON(t, res.stdout, &grant)

		// monomind spawns this for an org role: its own process, running the work in the daemon.
		c := r.mcp([]string{"MONOMIND_ORG_NAME=growth", "MONOMIND_ORG_ROLE=writer", "MONOMIND_ORG_RUN=run-smoke"}, "mcp", "--grant", grant.Grant.ID, "--profile", "default")
		c.call("initialize", map[string]any{})
		meta := map[string]any{"trace": map[string]any{"org": "growth", "run": "run-smoke", "role": "writer", "chain_id": "chn_smoketest", "hop": 1}}
		if text := c.tool("automation_publish_post", map[string]any{"text": "hello"}, meta); !strings.Contains(text, "published hello") {
			t.Fatalf("the grant call did not run the workflow in the daemon: %s", text)
		}
	})

	t.Run("extension serve", func(t *testing.T) {
		r := r.sub(t)
		p := r.start("extension", "serve")
		ask := r.bridge()
		raw, reply := ask("ping")
		if !reply.OK || !reply.Data.Pong || strings.Contains(raw, testEmail) {
			t.Fatalf("ping should answer, and never name the user: %s", raw)
		}
		reply.Data.Account.is(t, "ok", true, true)
		if raw, reply := ask("doc.lookup"); reply.Code == "account_locked" {
			t.Fatalf("a signed-in bridge refused a request: %s", raw)
		}
		p.stop()
	})

	t.Run("org serve", func(t *testing.T) {
		r := r.sub(t)
		p := r.start("org", "serve", "--foreground")
		r.waitFor("monomind org serve to be started", 30*time.Second, func() bool { return strings.Contains(r.read("monomind.log"), "org serve") })
		p.stop()
		if p.err != nil {
			t.Fatalf("org serve did not end cleanly: %v\n%s", p.err, r.read("org.log"))
		}
	})

	t.Run("chat", func(t *testing.T) {
		r := r.sub(t)
		res := r.run("--json", "chat", "history", "create", "--runtime", "claude")
		mustExit(t, res, 0)
		var conv struct{ ID string }
		mustJSON(t, res.stdout, &conv)
		res = r.run("--json", "chat", "--conversation", conv.ID, "--turn", "t1", "--", "hello")
		mustExit(t, res, 0)
		lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
		var last struct {
			Type    string
			Payload struct{ Status string }
		}
		mustJSON(t, lines[len(lines)-1], &last)
		if last.Type != "turn.finished" || last.Payload.Status != "completed" || !strings.Contains(r.read("monomind.log"), "agent exec") {
			t.Fatalf("the turn did not reach monomind.Exec and complete: %s", lines[len(lines)-1])
		}
	})

	if !d.alive() {
		t.Fatalf("the daemon died during the smoke:\n%s", r.read("daemon.log"))
	}
	mustExit(t, r.run("account", "logout"), 0)
	r.assertLocked(r.run("--json", "workflow", "list"), "not_logged_in", true)
}
```

- [ ] **Step 2: Check that it compiles and run it.**

```
go vet -tags devaccount ./internal/accountsmoke/
go test -tags devaccount ./internal/accountsmoke/ -run '^TestEveryEntryPointWorksSignedIn$' -count=1 -timeout 15m -v
```
Expected: no vet output; `--- PASS` with the eight subtests above each `--- PASS`, under a minute once the CLI is built (against a build with no gate, which is how these operations were checked, the same test took 22 seconds). A failing subtest names the entry point. `exit 4, want 0` with `Log in to monoes.me first` in the output means a signed-in user was refused: the process never installed a guard, or a gate site reads one that is not the process's. That is a defect of the phase that owns the entry point (B2 for the guard, B3a for runners, B3b for doors), to be fixed there with a unit test, not here.

- [ ] **Step 3: Prove it can fail.** In `cmd/monoagentcli/main.go`, in `processGuard` (B2, Task 4), replace the line `account.Install(g)` with `_ = account.Install`. The command-level gate still has its guard, but nothing deeper does, and a real binary with no installed guard judges the process as not logged in once the date has passed (B1a, `Require`). Run Step 2 and expect FAIL with `exit 4, want 0` and the login line on stderr, at the first command that reaches a gate site below the command-level one: the `workflow run` subtest if only the engine has one, or the `workflow import` that precedes the subtests, if B3a gates that too. Then `git checkout -- cmd/monoagentcli/main.go` and run it again: PASS. (A unit test cannot show this: the same missing guard is a pass there by design.)

- [ ] **Step 4: Commit.**

```
git add internal/accountsmoke/entrypoints_test.go
git commit -m "test(account): real-binary smoke of one operation through every entry point" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 6: Run it all, and what must be true before release R

No code. B5c is test-only and merging it releases (every merge to master does), but the release carries nothing a user can reach. It **must pass before release R**, which is B5a and `b5b-docs` merged as one push: R is what gives the gate a date, so every behaviour R switches on has to have run in the real binary first, here, with the date forced.

- [ ] **Step 1: Run the whole smoke on a clean tree.** This is the command B5a's readiness list (its Task 8, Step 5) asks for, run on the tree that will become R as well as on master:

```
go vet -tags devaccount ./internal/account/... ./internal/accountsmoke/... ./cmd/monoagentcli/
go test -race -tags devaccount ./internal/account/... -count=1
go test -tags devaccount ./internal/accountsmoke/ -count=1 -timeout 25m -v
go build ./... && go vet ./... && gofmt -l .
go test ./internal/account/... ./internal/accountsmoke/ -count=1
```
Expected: no output from the vets and `gofmt`; `ok` for `internal/account` with the tag (under `-race`, as every Linux job of CI is; the smoke package is the one that is not, decision 3), then the fourteen tests of `internal/accountsmoke` (named in the table below and in Task 3b) `--- PASS`, none skipped (the key-store test skips itself on a machine whose OS key store answers without reading the passphrase file: Task 3b, Step 2), about ten minutes after the first build of the CLI (one to two minutes), well inside CI's 25-minute limit; the default-build run ends `ok` for `internal/account` (with `TestDefaultBuildIgnoresTheEnforceOverride`) and `[no test files]` for `internal/accountsmoke`. Run it on the owner's Mac as well as in CI: every CI job runs on Linux, and the package builds everywhere but Windows.

- [ ] **Step 2: Check what this merge ships.** `git diff --stat origin/master...HEAD` lists the CI file, six files `internal/account/rollout_override*.go` (three of them tests) and ten files in `internal/accountsmoke/`, and nothing else. `grep -n 'os.Getenv' internal/account/rollout_override.go internal/account/rollout_override_default.go` prints nothing: only the `devaccount` file reads the environment. `grep -n devaccount .github/workflows/release.yml` prints nothing: this plan adds no step to the release workflow, and B5a's guard keeps the tag out of every artifact.

- [ ] **Step 3: Know what a red result means.** A failure names a door, an entry point or a state, and the owner of that behaviour is in the message: B1b (sign-in, `account status`), B2 (the gate at the command line, the doctor row), B3a (runners, the daemon and its heartbeat), B3b (doors: the bridge and its `ping` among them). The fix is made in that phase's package with a unit test of its own and the smoke is run again; the smoke is never loosened to pass. If the CLI cannot be built with the tag, or `TestMain` cannot start the fake, nothing ran: fix the build first.

- [ ] **Step 4: Hold R until it is green.** Do not merge R until `account-smoke` is green on master after this plan merged, and green again on R's branch. B5a's Task 8 lists it as a box to tick.

## Acceptance coverage

| Spec §1 acceptance | Proven here by |
|---|---|
| 1. After the date, with no valid session, every gated command exits 4 with `login_required` and does nothing else, and every door refuses | `TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten` (Task 2): a command that would write is refused with the fixed line and the JSON error and leaves nothing in HOME but the clock-guard record of a machine that never signed in (A25: `account/session.lock` and a `session.json` with no token), `org serve --foreground` is refused as a gated launcher, an open command works, one sign-in opens the machine, a logout closes it. `TestEveryDoorRefusesWhenLockedAndOpensWhenSignedIn` (Task 4): in a daemon that started locked, the HTTP API, `/v1`, the org receiver, the webhook server, the bridge and MCP refuse, and each opens after a sign-in without a restart. |
| 2. Signed in, then blocked: locked within one refresh interval, work in flight cancelled | `TestBlockedAccountLocksAndCancelsWorkInFlight` (Task 3): the fake answers `invalid_grant`, the run in the daemon ends `CANCELLED`, the heartbeat says `locked`/`refused`, `refresh.enc` is deleted, the daemon stays up and resumes after one sign-in. |
| 3. Signed in, monoes.me unreachable: works until 24 hours after the newest token was issued, then locked; work in flight at that moment finishes the node it is in, and a run ends at its next node (ruling R4) | `TestUnreachableIsGraceUntilTwentyFourHours` (Task 3): grace with its stderr line on stderr only, then `locked(expired)` while a 40-second job of one node finishes `SUCCESS` (a run with a next node ends there: B3a's Task 3b pins that in the engine); `TestAnswersThatAreNotARefusalKeepTheGrace` (D27: a 500, `invalid_client`, `invalid_target` and a page that is not JSON never lock and keep the refresh token); `TestAnUnknownSigningKeyIsTheRunUpdateCase` (spec §4.7, A9: `server_error` in the grace, then `locked(key_unknown)` and a doctor fix that is the update). |
| 4. Before the date nothing locks, and once a date is set every surface warns | `TestNothingLocksBeforeTheDateAndEverySurfaceWarns` (Task 2): with a date two days ahead and no session, a command succeeds, the warning appears once on stderr and never on stdout, `account status` says `locked`/`not_logged_in` with `enforced` false, the doctor row warns, the heartbeat says so, and the daemon runs work. The dormant half (a date of zero until R) is B5a's `TestEnforcedFlipsAtTheDate` and its suite run. |
| 5. A release binary built with the `devaccount` tag cannot ship | B5a's guard. Here `TestDefaultBuildIgnoresTheEnforceOverride` (Task 1) pins that a default build, the one every release is, ignores `MONOAGENT_DEV_ENFORCE_FROM` whatever it holds. |

Other spec items: §11's signed-in path through every process type and entry point is `TestEveryEntryPointWorksSignedIn` (Task 5); A12 is Task 1; D24 is the tag the whole package is built with; D27 is `TestAnswersThatAreNotARefusalKeepTheGrace`; A1 (a serving command starts locked and a launcher does not) is Task 4's daemon and Task 2's `org serve`; A4 (the four-key account object) is asserted on the heartbeat, `/health` and the bridge's `ping` by `accountReport.is`; A8 (the one-minute negative cache) is why `rig.backdate` writes `last_attempt`; A9 is `TestAnUnknownSigningKeyIsTheRunUpdateCase`; A20, A22 and A23 are `TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount`, `TestABlockedKeyStoreDoesNotHoldUpAnotherProcess` and `TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack` (Task 3b); A24 is `TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow` and `TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow` (Task 3b); A25 is `TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack` (Task 3b), with the files that `TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten` and `TestNothingLocksBeforeTheDateAndEverySurfaceWarns` check in HOME (Task 2).

## Notes for other plans

- B4 (Task 8, Step 4) and B4b (Task 3, Step 4) name B5a, "decision 2", as the owner of `MONOAGENT_DEV_ENFORCE_FROM`, and so does B5b's "Written against the other plans as they stand" note. Since spec A12 it is this plan's Task 1, and it lands with B5c, which merges after B4 and B4b: their "look at the real thing" steps need B5c, not B5a.
- B5b's `TestContributingExplainsTheDevelopmentBuild` expects `internal/accountsmoke`, the CI job `account-smoke` and `-timeout 25m` to be spelled as here.

## Proving notes

The rig, the scenarios of Tasks 2 and 3b and the amended check of Task 3 were run as written against the real binary built with `-tags devaccount`, over B1b and B2 as amended and a stand-in for B1a's amended core (B1a's merged branches and its security fix branch, plus the A24 marker, `TransientError.Settled`, the `unconfirmed` reason and the A25 record, as the implementation brief describes them). B3a and B3b were not in that tree. `go vet -tags devaccount` and `gofmt` are clean, and these pass together in about a minute and a half once the CLI is built: `TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten`, `TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount` with both signals, `TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow`, `TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow`, `TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack`, `TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack` and `TestABlockedKeyStoreDoesNotHoldUpAnotherProcess`; the CLI half of the warn-period test passes too (the heartbeat and the daemon's work need B3a). Seven deliberate breakages of the stand-in each made the scenarios Step 3 names fail, with the messages it quotes: the grant on the caller's context, no marker before the grant, an age rule that never drops, a window of zero, no record for a machine with no session, a logout that keeps no record, and a key store that may take ten minutes. Not run here: Tasks 4 and 5, which need B3a and B3b, the daemon halves of Task 2's warn test and of Task 3's unreachable and block tests, and the CI job.

Running the unchanged Task 3 against that tree found a defect that was already in this plan. With monoes.me down, the refresher stops at the endpoint discovery and never sends its grant, so `rig.edge.refreshes()` stayed 0 and `TestUnreachableIsGraceUntilTwentyFourHours` failed at its first check. The edge now counts the requests it dropped (`dropped`) and the check reads that. The same run showed that the daemon halves still wait on B3a's heartbeat, which this tree did not have.

## Contract change requests

None. The three override files are in the index's §3.1 table with B5c as owner, and the development variable is spec A12; this plan adds no exported name and changes none of the frozen contract in index §3.
