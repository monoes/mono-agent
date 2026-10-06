# Mandatory monoes.me Account — B1a: the account core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/account`, the offline core of the monoes.me account gate: strict JWS verification, the state machine and clock guard, the on-disk session with its keyring-sealed refresh token, the cached `Guard` with its refresh algorithm and background refresher, the process-wide guard, and the `accounttest` fixtures every other plan's tests use.

**Architecture:** One package that imports only the standard library, `golang.org/x/sys` (the Windows lock) and `internal/secrets`. A pure verdict (`Evaluate`, `judge`) is wrapped by a `Guard` that caches the session (re-read when `session.json`'s modification time changes), refreshes under a cross-process lock through a `Refresher` seam that B1b implements, and tells `OnRefused` callbacks. One new exported accessor in `internal/secrets` (`AccountKEK`) lets the sealer use the vault's own key stores. The package is dormant until B5a sets the enforcement date.

**Tech Stack:** Go 1.26 standard library (`crypto/ed25519`, `encoding/json`, `syscall.Flock`, `golang.org/x/sys/windows` `LockFileEx`), `internal/secrets` (AES-256-GCM, OS keyring and file keyring), `testing` with `-race`, `internal/testhome`, and go-keyring's in-memory mock in tests only.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§4.1 to §4.8, D13 to D17, D22, D24, D27). Index: `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§2 global constraints, §3.1 file ownership, §3.2 Go API, §3.3 `accounttest`). Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

> **Superseded in implementation (2026-10-06).** Spec amendments A20 to A25 (spec §13, index §3.6) govern parts of the task text below, and the code on the integrated branch is the authority where the two differ; the tasks are not rewritten. In particular: the refresh call runs on a context detached from the caller's cancellation (`context.WithoutCancel`) with a deadline of its own (`refreshCallTimeout`), not on the caller's context as the listing of `refreshUnderLock` in Task 7 shows (A20); `Close` waits for a grant that is in flight, up to that deadline, and does not abort it, so the Task 8 test `TestCloseStopsARefresherThatIsMidCallAndRecordsNothing`, which expects `Close` to cut a call short and record nothing, describes the older behaviour (A20); `refresh.enc` is deleted after a refusal only once the `refused` marker is saved (A21); every key-store call made under the session lock is bounded to ten seconds and a timeout is `keyring_unavailable` (A22); a logout leaves a session with no token and the high-water mark instead of deleting `session.json`, and `touchHW` writes the same record on a machine with no session once the enforcement date has been reached, so `Evaluate` judges it `locked(not_logged_in)` with the mark (A23, A25); and a refresh whose outcome is unknown keeps a `pending_since` marker, retries at once inside 240 seconds, then drops this machine's refresh token and reports `unconfirmed` (`TransientError.Settled`, `ReasonUnconfirmed`, `Session.PendingSince`), which closes the one window that the Task 7 notes accept (A24).

## Global Constraints

Copied verbatim from the index §2 (the lines that bind this plan):

- Go is `go 1.26.0`; `internal/account` adds no third-party dependency (D13): `crypto/ed25519` and a small strict JWS parser only. One accepted algorithm (EdDSA); the verifier ignores `jku`, `jwk` and `x5u` headers and never negotiates from the header.
- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- Storage (§4.6): `~/.monoagent/account/` (directory 0700) with `session.json`, `refresh.enc` and `session.lock` (files 0600). One session per OS user, shared by all profiles, whatever `--db-path` says.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `EnsureFresh`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear only on a login or a refresh. The high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

## Review Focus

The failure modes that matter most to a person using this software and that nothing in the spec's tables would exercise on its own. Each has a test inside the task that owns the code, named here so a reviewer can find it.

1. **Several processes refresh at once** (a daemon, a cron job and a CLI call all see an expiring token). The server rotates the refresh token on use and monoes.me treats presenting a rotated-away token as theft, revoking every refresh token of the account (measured by plan A), so a second network refresh is not a harmless duplicate: it locks every install of the account. Pinned by `TestSeveralProcessesRefreshingAtOnceMakeOneNetworkCall` (Task 7): five guards with separate store handles, three goroutines each, a fake server that refuses a rotated-away token; exactly one call, and nobody locked.
2. **A clock that is wrong** (a VM that was paused, a dead battery). The verdict must lock with `clock_rollback`, and the next online refresh must repair it even though the stored `hw` and `last_attempt` lie in the future. Pinned by `TestARolledBackClockIsRepairedByTheRefreshThatResetsHW` (Task 7), `TestElapsed` (Task 3) and the `hw` rows of `TestEvaluateMatrix` (Task 3).
3. **monoes.me answers a refresh with something this build cannot verify** (an opaque token after a bad deploy, a wrong audience, a signing key this build does not pin). The install must stay in grace, never lock, keep the rotated refresh token, and say `key_unknown` (run `update`) when the grace ends. Pinned by `TestATokenThatDoesNotVerifyIsNeverStoredButTheRotatedRefreshTokenIs` (Task 7).
4. **A passphrase prompt on piped stdin.** With the file keyring and no passphrase file, the vault would read the passphrase from stdin, so an implicit refresh in the gate (which runs before a command claims stdin) would swallow `cat input | monoagentcli workflow run … --input -`. The implicit sealer never prompts. Pinned by `TestAccountKEKQuietNeverPrompts` (Task 4).
5. **A corrupt or foreign `session.json`** (hand-edited, truncated by a full disk, version other than 1). It must give `locked(invalid)` with no panic, must not downgrade a session that already works in memory, and a sign-in must overwrite it. Pinned by `TestLoadRefusesAFileItCannotTrustAndASaveRepairsIt` (Task 5) and `TestACorruptSessionFileIsLockedInvalidAndASignInRepairsIt` (Task 6).

## What this plan decides where the contract is silent

Each item is also listed under `Contract change requests` at the end when another plan can see it.

- **Where things live.** `Evaluate` and its helper `judge` are in `session.go` beside `Session`. `InstallForTest` is in `process.go` beside `Install`; the other three test hooks are in `testhooks.go`. Extra files: `globals.go` (the lock and the strict flag), `guard_refresh.go`, `guard_loop.go`. Every file is created whole in exactly one task, so no task edits a file an earlier task wrote.
- **Verify** checks, in this order: structure, algorithm, key, signature, claims, lifetime, and (in `Verify` only) the clock. A claim is never read from a token whose signature failed. An expired token is not rejected: the grace needs its receipt. Segments are strict unpadded base64url (the standard decoder skips `\r` and `\n`; a test caught that), a `crit` header is refused, and `jku`, `jwk`, `x5u` and `x5c` are never read.
- **Evaluate** checks, in this order: no session, refused, no token, a token that does not verify, `clock_rollback`, `clock_skew`, then `ok`, `grace`, `expired`. When the last refresh returned a key this build does not pin, the end of the grace says `key_unknown`, not `expired`.
- **`Require` with no guard installed** judges the process as not logged in with enforcement judged normally, so while dormant, or before the date, it returns nil (D22 wins over the literal text of §3.2); inside a test binary it returns nil unless `StrictForTest` is active (D24). `StrictForTest` alone does not refuse while dormant: a strict gate-site test also calls `SetEnforceFromForTest`.
- **Due for a refresh.** Not `ok` (grace, expired, rolled back, unverifiable) is always due; `ok` is due under `RefreshMargin` (CLI) or at half the lifetime (background). The negative cache (`NegativeCache`) holds only the CLI path, and a stored timestamp in the future counts as elapsed, so a rolled-back clock can be repaired.
- **A refreshed token that does not verify is never stored** (it counts as `server_error`), but the rotated refresh token that came with it is, because the old one is dead. A bad monoes.me deploy must not log every online install out. The refresh token is always written before the session, and if it cannot be written the dead one is removed from disk, because a later process that presented it would trigger the account-wide revocation above.
- **`hw`** is written only by `EnsureFresh`, `Refresh` and the refresher, only when a session exists, the package is not dormant and the stored value is a minute stale, under the lock; never lowered except by a newly verified token (`NewSession`). `Status` and `Require` do no I/O beyond one `Mtime` stat per `Poll`.
- **Polling** is lazy: `Status` stats `session.json` at most once per `Poll`, so a guard that never starts a refresher owns no goroutine. The refresher wakes every `Poll`, judges the half-life and the backoff by the guard's clock, and keeps running with no session.
- **Lock waits.** One network call is capped at 20 s, a waiter outlasts it (25 s), a `hw` write never waits more than 2 s. The `Status` returned by `EnsureFresh` and `Refresh` is always usable; their error is advisory and a caller must not fail a command because of it.
- **`OnRefused` callbacks** run on their own goroutine, once per transition into `locked(refused)`; registering during a refusal calls at once.
- **The sealer** has two forms: `NewKeyringSealer` (never prompts) for implicit use and `NewInteractiveKeyringSealer` (as the vault does) for an explicit sign-in. The key is `secrets.AccountKEK`, stored under the id `monoes..account`, which no profile can have (`profiledir.ValidProfileID` rejects `..`). With the file keyring this adds `monoes..account` to `secret keyring status` and to the keys `secret keyring set-passphrase` checks: a deliberate, visible consequence of sharing the vault's key stores.
- **The suite does not depend on the ambient date or pinned keys.** B5a sets the real enforcement date and pins the first key. Every leak check compares with the value captured before a hook, and `TestRequireWithNoGuardInstalled` sets dormancy explicitly. Proven by running the whole suite with `enforceFrom` set to 2020 and to 2099 and one key pinned (the two guards `TestEnforcedBuildPinsAKey` and `TestPinnedKeysAreWellFormed` read the real values on purpose).
- **No test touches the OS keychain or the real home.** `main_test.go` installs go-keyring's mock and a throwaway `HOME`; account tests use `NewMemorySealer`.
- **Length.** This plan is far over the usual 1500 lines because it carries a whole package with its tests, every line of which was compiled and run in a scratch export before it was written here.

---

### Task 1: Constants, states, errors, the enforcement date and the test hooks

The vocabulary every later task uses, and the process globals with their lock. Nothing here touches the disk or the network.

**Files:**
- Create: `internal/account/doc.go`, `claims.go`, `state.go`, `errors.go`, `globals.go`, `rollout.go`, `keys.go`, `keys_default.go`, `keys_devaccount.go`, `testhooks.go`
- Test: `internal/account/main_test.go`, `internal/account/foundation_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package `account`, exactly as index §3.2):
  - `claims.go`: `HostURL`, `Issuer`, `Audience`, `ClientID`, `GraceWindow`, `MaxTokenLife`, `ClockSkew`, `RefreshMargin`, `NegativeCache`, `ConnectTimeout`, `PollInterval`, `LateRefresher`.
  - `state.go`: `type State string` (`StateOK`, `StateGrace`, `StateLocked`); `type Reason string` (`ReasonNone`, `ReasonNotLoggedIn`, `ReasonExpired`, `ReasonRefused`, `ReasonClockRollback`, `ReasonClockSkew`, `ReasonKeyUnknown`, `ReasonInvalid`, `ReasonUnreachable`, `ReasonServerError`, `ReasonKeyringUnavailable`); `type User struct{ ID, Email, Username string }`; `type Status struct{…}`; `func (s Status) Allowed() bool`.
  - `errors.go`: `const LoginRequiredMessage`; `type LoginRequiredError struct{ Status Status }`, `func (e *LoginRequiredError) Error() string`, `func (e *LoginRequiredError) JSONErrorFields() map[string]any` (`{"login_required": true, "code": "auth_or_connection", "account": {"state", "reason"}}`, so the CLI's `jsonErrorFields` interface in `cmd/monoagentcli/automation.go:94` picks it up through `errors.As`), `func IsLoginRequired(err error) bool`; `type RefusedError struct{ Description string }`; `type TransientError struct{ Reason Reason; Err error }` with `Unwrap`.
  - `rollout.go`: `func EnforceDate() time.Time`, `func Enforced(now, hw time.Time) bool`; unexported `dormant() bool`, `var enforceFrom` (B5a edits this one initializer).
  - `keys.go`: `type Key struct{ KID string; Public ed25519.PublicKey }`, `func TrustedKeys() []Key`; unexported `pinnedKeys`, `lookupKey(kid string) (Key, bool)`, `pinKey(kid, publicHex string) Key`, `cloneKeys`, `extraKeys() []Key` (per build tag).
  - `globals.go`: unexported `globalsMu sync.RWMutex`, `strict bool`.
  - `testhooks.go`: `SetTrustedKeysForTest(t testing.TB, keys []Key)`, `SetEnforceFromForTest(t testing.TB, at time.Time)`, `StrictForTest(t testing.TB)`; unexported `requireTestBinary(name string)`, `mustBeTestBinary(isTest bool, name string)`.

- [ ] **Step 1: Create `internal/account/main_test.go`.** The package's one `TestMain` (the internal and the external tests share a binary): a throwaway `HOME` and go-keyring's in-memory mock, so no test can touch `~/.monoagent` or the OS keychain.

```go
package account

import (
	"testing"

	"github.com/monoes/mono-agent/internal/testhome"
	"github.com/zalando/go-keyring"
)

// TestMain gives the whole test binary (the internal and the external tests
// share it) a throwaway HOME and an in-memory keyring, so no test can touch the
// real ~/.monoagent or the real OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	testhome.Main(m)
}
```

- [ ] **Step 2: Create `internal/account/foundation_test.go`.**

```go
package account

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEnforced(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		date, now, hw time.Time
		want          bool
	}{
		{"dormant", time.Time{}, date.Add(time.Hour), time.Time{}, false},
		{"before the date", date, date.Add(-time.Second), time.Time{}, false},
		{"at the date", date, date, time.Time{}, true},
		{"after the date", date, date.Add(time.Hour), time.Time{}, true},
		{"clock set back but hw is past the date", date, date.Add(-48 * time.Hour), date.Add(time.Minute), true},
		{"hw behind now", date, date.Add(-time.Hour), date.Add(-2 * time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			SetEnforceFromForTest(t, c.date)
			if got := Enforced(c.now, c.hw); got != c.want {
				t.Fatalf("Enforced(%v, %v) with date %v = %v, want %v", c.now, c.hw, c.date, got, c.want)
			}
			if got := dormant(); got != c.date.IsZero() {
				t.Fatalf("dormant() = %v with date %v", got, c.date)
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		enforced bool
		state    State
		want     bool
	}{
		{false, StateLocked, true}, // nothing locks before the date
		{false, StateOK, true},
		{true, StateOK, true},
		{true, StateGrace, true},
		{true, StateLocked, false},
	}
	for _, c := range cases {
		if got := (Status{State: c.state, Enforced: c.enforced}).Allowed(); got != c.want {
			t.Errorf("Allowed(enforced=%v, %s) = %v, want %v", c.enforced, c.state, got, c.want)
		}
	}
}

func TestSeamsRestoreWhenTheTestEnds(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	dateBefore, keysBefore := EnforceDate(), len(TrustedKeys()) // whatever the release pins and dates, not zero
	t.Run("hooks", func(t *testing.T) {
		SetEnforceFromForTest(t, at)
		SetTrustedKeysForTest(t, []Key{{KID: "k1", Public: make(ed25519.PublicKey, ed25519.PublicKeySize)}})
		StrictForTest(t)
		if !EnforceDate().Equal(at) || len(TrustedKeys()) != 1 {
			t.Fatalf("the hooks did not take effect: date %v, keys %d", EnforceDate(), len(TrustedKeys()))
		}
		globalsMu.RLock()
		isStrict := strict
		globalsMu.RUnlock()
		if !isStrict {
			t.Fatal("StrictForTest did not set the strict flag")
		}
	})
	globalsMu.RLock()
	isStrict := strict
	globalsMu.RUnlock()
	if !EnforceDate().Equal(dateBefore) || len(TrustedKeys()) != keysBefore || isStrict {
		t.Fatalf("a hook leaked out of its test: date %v (was %v), keys %d (was %d), strict %v", EnforceDate(), dateBefore, len(TrustedKeys()), keysBefore, isStrict)
	}
}

func TestSeamsPanicOutsideATestBinary(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "only be used from a test binary") {
			t.Fatalf("mustBeTestBinary(false) recovered %v, want the test-seam panic", r)
		}
	}()
	mustBeTestBinary(false, "SetEnforceFromForTest")
}

func TestTrustedKeysIsACopy(t *testing.T) {
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	SetTrustedKeysForTest(t, []Key{{KID: "k1", Public: pub}})
	got := TrustedKeys()
	got[0].KID = "changed"
	got[0].Public[0] = 9
	if again := TrustedKeys(); again[0].KID != "k1" || again[0].Public[0] != 0 {
		t.Fatalf("a caller changed the trusted set: %+v", again[0])
	}
	if pub[0] != 0 {
		t.Fatal("SetTrustedKeysForTest kept the caller's slice instead of copying it")
	}
}

func TestPinKey(t *testing.T) {
	k := pinKey("kid-1", strings.Repeat("ab", ed25519.PublicKeySize))
	if k.KID != "kid-1" || len(k.Public) != ed25519.PublicKeySize || k.Public[0] != 0xab {
		t.Fatalf("pinKey = %+v", k)
	}
	for _, bad := range []string{"", "zz", strings.Repeat("ab", 31)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("pinKey(%q) did not panic", bad)
				}
			}()
			pinKey("bad", bad)
		}()
	}
}

func TestLoginRequiredError(t *testing.T) {
	first := "Log in to monoes.me first: monoagentcli account login"
	if LoginRequiredMessage != first {
		t.Fatalf("LoginRequiredMessage = %q", LoginRequiredMessage)
	}
	for _, c := range []struct {
		reason   Reason
		hasExtra bool
	}{
		{ReasonNotLoggedIn, false},
		{ReasonExpired, true},
		{ReasonRefused, true},
		{ReasonClockRollback, true},
		{ReasonClockSkew, true},
		{ReasonKeyUnknown, true},
		{ReasonInvalid, true},
	} {
		msg := (&LoginRequiredError{Status: Status{State: StateLocked, Reason: c.reason}}).Error()
		line, rest, hasRest := strings.Cut(msg, "\n")
		if line != first {
			t.Errorf("%s: first line = %q, want %q", c.reason, line, first)
		}
		if hasRest != c.hasExtra || (hasRest && rest == "") {
			t.Errorf("%s: extra line present = %v, want %v (message %q)", c.reason, hasRest, c.hasExtra, msg)
		}
	}
	wrapped := fmt.Errorf("gate: %w", &LoginRequiredError{})
	if !IsLoginRequired(wrapped) || IsLoginRequired(errors.New("other")) || IsLoginRequired(nil) {
		t.Fatal("IsLoginRequired must find a wrapped *LoginRequiredError and nothing else")
	}
}

// The CLI's JSON error wrappers find these fields with errors.As on an
// interface (cmd/monoagentcli/automation.go: jsonErrorFields), so a layer-2
// refusal that comes out of any command carries the document of index §3.4 item 2.
func TestLoginRequiredErrorJSONFields(t *testing.T) {
	cases := []struct {
		name   string
		status Status
		want   string
	}{
		{"locked, not logged in", Status{State: StateLocked, Reason: ReasonNotLoggedIn},
			`{"account":{"reason":"not_logged_in","state":"locked"},"code":"auth_or_connection","login_required":true}`},
		{"locked, refused", Status{State: StateLocked, Reason: ReasonRefused},
			`{"account":{"reason":"refused","state":"locked"},"code":"auth_or_connection","login_required":true}`},
		{"grace", Status{State: StateGrace, Reason: ReasonUnreachable},
			`{"account":{"reason":"unreachable","state":"grace"},"code":"auth_or_connection","login_required":true}`},
	}
	type fieldser = interface{ JSONErrorFields() map[string]any }
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrapped := fmt.Errorf("gate: %w", &LoginRequiredError{Status: c.status})
			var fields fieldser
			if !errors.As(wrapped, &fields) {
				t.Fatal("errors.As does not find JSONErrorFields on a wrapped *LoginRequiredError")
			}
			raw, err := json.Marshal(fields.JSONErrorFields())
			if err != nil || string(raw) != c.want {
				t.Fatalf("JSONErrorFields marshalled to %s (err %v), want %s", raw, err, c.want)
			}
		})
	}
}

func TestRefreshErrors(t *testing.T) {
	var refused *RefusedError
	if !errors.As(fmt.Errorf("w: %w", &RefusedError{Description: "revoked"}), &refused) || refused.Description != "revoked" {
		t.Fatal("RefusedError must survive wrapping")
	}
	if got := (&RefusedError{}).Error(); !strings.Contains(got, "invalid_grant") {
		t.Fatalf("RefusedError message %q does not name invalid_grant", got)
	}
	cause := errors.New("dial tcp: i/o timeout")
	te := &TransientError{Reason: ReasonUnreachable, Err: cause}
	if !errors.Is(te, cause) || !strings.Contains(te.Error(), "unreachable") {
		t.Fatalf("TransientError = %q, want it to wrap its cause and name the reason", te)
	}
	if got := (&TransientError{Reason: ReasonServerError}).Error(); !strings.Contains(got, "server_error") {
		t.Fatalf("TransientError without a cause = %q", got)
	}
}
```

- [ ] **Step 3: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, a build failure (the first lines, the rest are the same kind):

```text
# github.com/monoes/mono-agent/internal/account [github.com/monoes/mono-agent/internal/account.test]
internal/account/foundation_test.go:29:4: undefined: SetEnforceFromForTest
internal/account/foundation_test.go:30:14: undefined: Enforced
internal/account/foundation_test.go:33:14: undefined: dormant
internal/account/foundation_test.go:43:12: undefined: State
internal/account/foundation_test.go:46:11: undefined: StateLocked
internal/account/foundation_test.go:47:11: undefined: StateOK
...
```

- [ ] **Step 4: Create `internal/account/doc.go`.**

```go
// Package account is the machine-wide monoes.me session. One access token,
// an EdDSA-signed JWT that monoes.me issues and this binary verifies offline
// against keys pinned in it, plus a refresh token sealed under the OS keyring
// key, prove that a signed-in account exists on this machine. A Guard turns
// that session into a cached verdict (ok, grace or locked) that every gate of
// the program asks for: the CLI gate, the engine and runner checks, and the
// doors (HTTP API, webhook server, extension bridge, MCP).
//
// Until the enforcement date in rollout.go is set the package is dormant:
// nothing locks, nothing warns and nothing contacts monoes.me implicitly.
//
// Import rule: this package imports the standard library, golang.org/x/sys
// (the Windows lock) and internal/secrets (the keyring key), and nothing else
// of this repository, so every other package may import it.
//
// Files: claims.go (constants), state.go and session.go (the verdict),
// verify.go and keys.go (the proof), sealer.go and store.go (the session on
// disk), guard*.go and process.go (the cached verdict and the process-wide
// guard), testhooks.go (test seams that panic outside a test binary).
package account
```

- [ ] **Step 5: Create `internal/account/claims.go`.** The constants of index §3.2. Spike S6 may correct one; this is the only file that changes if it does.

```go
package account

import "time"

// The constants below are proposals until spike S6 has read them from a real
// token (spec §4.1). If S6 corrects one, this is the only file that changes.
const (
	HostURL        = "https://monoes.me"
	Issuer         = "https://monoes.me/api/auth"
	Audience       = "https://monoes.me/api/monoagent"
	ClientID       = "monoagent"
	GraceWindow    = 24 * time.Hour
	MaxTokenLife   = 24 * time.Hour
	ClockSkew      = 5 * time.Minute
	RefreshMargin  = 5 * time.Minute
	NegativeCache  = time.Minute
	ConnectTimeout = 2 * time.Second
	PollInterval   = 5 * time.Second
	LateRefresher  = 5 * time.Minute // a non-serving process starts its refresher after this
)
```

- [ ] **Step 6: Create `internal/account/state.go`.**

```go
package account

import "time"

// State is the verdict of a session (spec §4.3).
type State string

const (
	StateOK     State = "ok"
	StateGrace  State = "grace"
	StateLocked State = "locked"
)

// Reason says why a session is locked, or, in grace, why it was not refreshed.
type Reason string

const (
	ReasonNone               Reason = ""
	ReasonNotLoggedIn        Reason = "not_logged_in"
	ReasonExpired            Reason = "expired"
	ReasonRefused            Reason = "refused"
	ReasonClockRollback      Reason = "clock_rollback"
	ReasonClockSkew          Reason = "clock_skew"
	ReasonKeyUnknown         Reason = "key_unknown"
	ReasonInvalid            Reason = "invalid"
	ReasonUnreachable        Reason = "unreachable"         // grace: why it was not refreshed
	ReasonServerError        Reason = "server_error"        // grace
	ReasonKeyringUnavailable Reason = "keyring_unavailable" // grace
)

// User is the account the session belongs to.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

// Status is the verdict and the JSON of `account status --json` (spec §7).
type Status struct {
	V           int       `json:"v"` // always 1
	State       State     `json:"state"`
	Reason      Reason    `json:"reason"`
	User        *User     `json:"user,omitempty"`
	Plan        string    `json:"plan"`
	IssuedAt    time.Time `json:"issued_at,omitzero"`
	ValidUntil  time.Time `json:"valid_until,omitzero"` // the access token's exp
	GraceUntil  time.Time `json:"grace_until,omitzero"` // iat + GraceWindow
	EnforceFrom time.Time `json:"enforce_from,omitzero"`
	Enforced    bool      `json:"enforced"`
}

// Allowed reports whether work may run: true when the gate is not enforced
// yet, or the state is ok or grace.
func (s Status) Allowed() bool {
	return !s.Enforced || s.State == StateOK || s.State == StateGrace
}
```

- [ ] **Step 7: Create `internal/account/errors.go`.**

```go
package account

import (
	"errors"
	"fmt"
)

// LoginRequiredMessage is the first line of every refusal. The doors that
// must not carry more (/v1, MCP) use it as the whole message.
const LoginRequiredMessage = "Log in to monoes.me first: monoagentcli account login"

// LoginRequiredError is what every gate returns. Its message starts with
// LoginRequiredMessage and, for the reasons that have one, adds a second line.
type LoginRequiredError struct{ Status Status }

func (e *LoginRequiredError) Error() string {
	if line := reasonLine(e.Status.Reason); line != "" {
		return LoginRequiredMessage + "\n" + line
	}
	return LoginRequiredMessage
}

// JSONErrorFields is the machine-readable form of the refusal for `--json`
// callers: the CLI's JSON error wrappers add these keys to {"error": …}, so a
// refusal that comes out of any command prints the document of index §3.4
// item 2, not only one that comes out of the CLI gate. A raw error of this type
// is not classified by the CLI (exit 1), so the code is carried here.
func (e *LoginRequiredError) JSONErrorFields() map[string]any {
	return map[string]any{
		"login_required": true,
		"code":           "auth_or_connection",
		"account":        map[string]any{"state": string(e.Status.State), "reason": string(e.Status.Reason)},
	}
}

// IsLoginRequired reports whether err is, or wraps, a *LoginRequiredError.
func IsLoginRequired(err error) bool {
	var e *LoginRequiredError
	return errors.As(err, &e)
}

func reasonLine(r Reason) string {
	switch r {
	case ReasonExpired:
		return "This login expired: monoes.me has not been reachable for 24 hours."
	case ReasonRefused:
		return "monoes.me ended this login (the account was blocked or the login was revoked)."
	case ReasonClockRollback:
		return "The system clock went back. Fix the clock, then sign in again."
	case ReasonClockSkew:
		return "The system clock is more than 5 minutes behind monoes.me. Fix the clock."
	case ReasonKeyUnknown:
		return "This build cannot verify the login. Update it: monoagentcli update"
	case ReasonInvalid:
		return "The stored login is not valid. Sign in again."
	}
	return ""
}

// RefusedError is what a Refresher returns when monoes.me answered
// invalid_grant to a refresh-token grant, and only then (spec D27): every
// other failure is a TransientError.
type RefusedError struct{ Description string }

func (e *RefusedError) Error() string {
	if e.Description == "" {
		return "account: monoes.me refused the refresh token (invalid_grant)"
	}
	return "account: monoes.me refused the refresh token (invalid_grant): " + e.Description
}

// TransientError is any other refresh failure: no network, a timeout, another
// OAuth error, a 4xx or 5xx answer. It is not a decision about the account, so
// the grace applies.
type TransientError struct {
	Reason Reason // ReasonUnreachable or ReasonServerError
	Err    error
}

func (e *TransientError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("account: refresh failed (%s)", e.Reason)
	}
	return fmt.Sprintf("account: refresh failed (%s): %v", e.Reason, e.Err)
}

func (e *TransientError) Unwrap() error { return e.Err }
```

- [ ] **Step 8: Create `internal/account/globals.go`.**

```go
package account

import "sync"

// globalsMu guards every process-wide variable of the package: the
// enforcement date (rollout.go), the key override (keys.go), the strict flag
// (below) and the installed guard (process.go). They are read only through
// accessors that take the lock. Tests change them only through the *ForTest
// hooks (testhooks.go), which take it too; a test that uses them must not call
// t.Parallel().
var globalsMu sync.RWMutex

// strict turns off the test-binary exception of Require (process.go).
var strict bool
```

- [ ] **Step 9: Create `internal/account/rollout.go`.** The date is the zero time: dormant (spec D22). B5a sets it here.

```go
package account

import "time"

// The enforcement date (spec D22). The zero time means the package is
// dormant: nothing locks, nothing warns and nothing contacts monoes.me
// implicitly. B5a is the one change that sets it, in the initializer below;
// everything else reads it through EnforceDate.
var enforceFrom = time.Time{}

// EnforceDate returns the enforcement date, or the zero time while dormant.
func EnforceDate() time.Time {
	globalsMu.RLock()
	defer globalsMu.RUnlock()
	return enforceFrom
}

// Enforced reports whether the gate is enforced: a date is set and
// max(now, hw) has reached it. Judging against the high-water mark hw as well
// means that setting the clock back does not postpone the date.
func Enforced(now, hw time.Time) bool {
	date := EnforceDate()
	if date.IsZero() {
		return false
	}
	if hw.After(now) {
		now = hw
	}
	return !now.Before(date)
}

// dormant reports whether no enforcement date is set.
func dormant() bool { return EnforceDate().IsZero() }
```

- [ ] **Step 10: Create `internal/account/keys.go`.** The pinned set is empty until spike S1 settles how monoes.me signs; `TestEnforcedBuildPinsAKey` (Task 10) keeps B5a from setting the date before a key is pinned.

```go
package account

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
)

// Key is a pinned verification key: the Ed25519 public key monoes.me signs
// access tokens with, and the kid its tokens name it by.
type Key struct {
	KID    string
	Public ed25519.PublicKey
}

// pinnedKeys is the set a release trusts: the current signing key and the next
// one, so a rotation never strands a client (spec §4.7). It is empty until
// spike S1 settles how monoes.me signs with a key that can be pinned; the first
// entry goes here as pinKey("<kid>", "<64 hex digits>"). B5a must not set the
// enforcement date while it is empty (TestEnforcedBuildPinsAKey).
var pinnedKeys = []Key{}

// keysOverride replaces the whole trusted set while a test holds it
// (SetTrustedKeysForTest); keysOverridden tells an empty override from none.
var (
	keysOverride   []Key
	keysOverridden bool
)

// TrustedKeys returns the pinned set; under the devaccount build tag it also
// holds the development key. A copy: callers cannot change what is trusted.
func TrustedKeys() []Key {
	globalsMu.RLock()
	defer globalsMu.RUnlock()
	if keysOverridden {
		return cloneKeys(keysOverride)
	}
	return append(cloneKeys(pinnedKeys), extraKeys()...)
}

// lookupKey finds a trusted key by kid.
func lookupKey(kid string) (Key, bool) {
	for _, k := range TrustedKeys() {
		if k.KID == kid {
			return k, true
		}
	}
	return Key{}, false
}

func cloneKeys(keys []Key) []Key {
	out := make([]Key, len(keys))
	for i, k := range keys {
		out[i] = Key{KID: k.KID, Public: append(ed25519.PublicKey(nil), k.Public...)}
	}
	return out
}

// pinKey builds a Key from a hex-encoded Ed25519 public key. It panics on a
// malformed key: a bad constant is a developer error that the tests catch.
func pinKey(kid, publicHex string) Key {
	pub, err := hex.DecodeString(publicHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("account: pinned key %q is not a hex-encoded Ed25519 public key", kid))
	}
	return Key{KID: kid, Public: pub}
}
```

- [ ] **Step 11: Create `internal/account/keys_default.go`.**

```go
//go:build !devaccount

package account

// extraKeys is the build-tag slot for keys beyond the pinned set. A default
// build trusts the pinned set and nothing else.
func extraKeys() []Key { return nil }
```

- [ ] **Step 12: Create `internal/account/keys_devaccount.go`.** A stub: B1b adds the development key here (see `Notes for B1b`).

```go
//go:build devaccount

package account

// extraKeys is where a devaccount build adds its development signing key.
// B1b puts it here (the public half of accounttest.DevKeyPair); until then a
// devaccount build trusts the pinned set only.
func extraKeys() []Key { return nil }
```

- [ ] **Step 13: Create `internal/account/testhooks.go`.**

```go
package account

import (
	"testing"
	"time"
)

// The hooks below are test seams. Each panics unless the binary is a test
// binary (testing.Testing), takes globalsMu, and restores what it changed when
// the test ends. A test that uses one must not call t.Parallel().

// requireTestBinary panics unless the running binary is a test binary.
func requireTestBinary(name string) { mustBeTestBinary(testing.Testing(), name) }

func mustBeTestBinary(isTest bool, name string) {
	if !isTest {
		panic("account." + name + " is a test seam and may only be used from a test binary")
	}
}

// SetTrustedKeysForTest replaces the whole trusted key set (the pinned keys
// and, under devaccount, the development key) for the rest of the test.
func SetTrustedKeysForTest(t testing.TB, keys []Key) {
	t.Helper()
	requireTestBinary("SetTrustedKeysForTest")
	globalsMu.Lock()
	prevKeys, prevSet := keysOverride, keysOverridden
	keysOverride, keysOverridden = cloneKeys(keys), true
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		keysOverride, keysOverridden = prevKeys, prevSet
		globalsMu.Unlock()
	})
}

// SetEnforceFromForTest sets the enforcement date for the rest of the test;
// the zero time makes the package dormant.
func SetEnforceFromForTest(t testing.TB, at time.Time) {
	t.Helper()
	requireTestBinary("SetEnforceFromForTest")
	globalsMu.Lock()
	prev := enforceFrom
	enforceFrom = at
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		enforceFrom = prev
		globalsMu.Unlock()
	})
}

// StrictForTest turns off the test-binary exception of Require for the rest of
// the test: with no guard installed Require then judges the process as not
// logged in, as a release binary does. While the package is dormant that still
// allows everything, so a strict test also calls SetEnforceFromForTest.
func StrictForTest(t testing.TB) {
	t.Helper()
	requireTestBinary("StrictForTest")
	globalsMu.Lock()
	prev := strict
	strict = true
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		strict = prev
		globalsMu.Unlock()
	})
}
```

- [ ] **Step 14: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
```

- [ ] **Step 15: Check formatting and vet.**

Run: `gofmt -l internal/account && go vet ./internal/account/...`

Expected: no output.

- [ ] **Step 16: Commit.**

```bash
git add internal/account/doc.go internal/account/claims.go internal/account/state.go internal/account/errors.go internal/account/globals.go internal/account/rollout.go internal/account/keys.go internal/account/keys_default.go internal/account/keys_devaccount.go internal/account/testhooks.go internal/account/main_test.go internal/account/foundation_test.go
```

```bash
git commit -m "feat(account): states, errors, the enforcement date and the test hooks" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Strict JWS verification and the `accounttest` fixture

`Verify` is the root of trust: one algorithm, only pinned keys, a small parser with no dependency (spec D13, D14). The fixture mints the tokens that every test of every plan uses, and carries the fixed development key pair.

**Files:**
- Create: `internal/account/verify.go`, `internal/account/accounttest/clock.go`, `internal/account/accounttest/devkey.go`, `internal/account/accounttest/fixture.go`
- Test: `internal/account/verify_test.go`, `internal/account/accounttest/accounttest_test.go`

**Interfaces:**
- Consumes (Task 1): `Key`, `TrustedKeys`, `lookupKey`, the `Reason…` constants, `Issuer`, `Audience`, `ClientID`, `MaxTokenLife`, `ClockSkew`, `SetTrustedKeysForTest`, `SetEnforceFromForTest`.
- Produces:
  - `type Receipt struct{ Sub, Plan string; IssuedAt, ExpiresAt time.Time; KID string }`
  - `type VerifyError struct{ Reason Reason }` (one unexported detail field), `func (e *VerifyError) Error() string`; `Reason` is `ReasonInvalid`, `ReasonKeyUnknown` or `ReasonClockSkew`.
  - `func Verify(token string, now time.Time) (*Receipt, error)`; unexported `func verifyToken(token string) (*Receipt, *VerifyError)` (everything but the clock).
  - `accounttest` (package `github.com/monoes/mono-agent/internal/account/accounttest`): `var DefaultNow time.Time`; `type Clock` with `func NewClock(t time.Time) *Clock`, `Now() time.Time`, `Set(time.Time)`, `Advance(time.Duration)`; `type Fixture struct{ Private ed25519.PrivateKey; Key account.Key; Clock *Clock }`; `type TokenOptions struct{ Sub, Plan, Issuer, ClientClaim string; Audience []string; IssuedAt time.Time; Lifetime time.Duration; KID, Alg string }`; `func New(t testing.TB) *Fixture`; `func (f *Fixture) Token(o TokenOptions) string`; `func (f *Fixture) Sign(header, claims map[string]any) string`; `const DevKID = "monoagent-dev-1"`; `func DevKeyPair() (ed25519.PublicKey, ed25519.PrivateKey)`.

- [ ] **Step 1: Create `internal/account/verify_test.go`.** One wrong claim at a time, algorithm confusion (HS256, none, a real signature under the wrong `alg`), a wrong key, jwk/jku/x5u headers, truncated and garbage input.

```go
package account_test

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// reasonOf returns the reason of a *VerifyError, or "" for a nil error and
// "other" for anything else.
func reasonOf(err error) account.Reason {
	if err == nil {
		return ""
	}
	var ve *account.VerifyError
	if !errors.As(err, &ve) {
		return "other"
	}
	return ve.Reason
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": account.Issuer, "aud": account.Audience, "azp": account.ClientID,
		"sub": "user-1", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func TestVerifyAcceptsAValidToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	r, err := account.Verify(f.Token(accounttest.TokenOptions{Sub: "user-7", Plan: "pro"}), now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Sub != "user-7" || r.Plan != "pro" || r.KID != f.Key.KID || !r.IssuedAt.Equal(now) || !r.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("receipt = %+v", r)
	}
	if r, err := account.Verify(f.Token(accounttest.TokenOptions{}), now); err != nil || r.Plan != "free" {
		t.Fatalf("a token without plan: receipt %+v, err %v, want plan free", r, err)
	}
}

func TestVerifyKeepsAnExpiredTokenForTheGrace(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	tok := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-10 * time.Hour)})
	r, err := account.Verify(tok, now)
	if err != nil || !r.ExpiresAt.Before(now) {
		t.Fatalf("Verify of an expired token: %+v, %v; it must return the receipt, the grace needs it", r, err)
	}
}

func TestVerifyRejectsOneWrongClaimAtATime(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	header := map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
	set := func(k string, v any) func(map[string]any) { return func(m map[string]any) { m[k] = v } }
	del := func(k string) func(map[string]any) { return func(m map[string]any) { delete(m, k) } }
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong issuer", set("iss", "https://evil.example/api/auth")},
		{"issuer missing", del("iss")},
		{"issuer of the wrong type", set("iss", 7)},
		{"wrong audience", set("aud", "https://monoes.me/api/other")},
		{"audience array without ours", set("aud", []string{"a", "b"})},
		{"audience missing", del("aud")},
		{"audience of the wrong type", set("aud", 5)},
		{"audience array with a non-string", set("aud", []any{account.Audience, 5})},
		{"wrong client", set("azp", "someone-else")},
		{"client missing", del("azp")},
		{"azp ours but client_id not", set("client_id", "someone-else")},
		{"sub missing", del("sub")},
		{"sub empty", set("sub", "")},
		{"sub of the wrong type", set("sub", 12)},
		{"iat missing", del("iat")},
		{"iat a string", set("iat", "1")},
		{"iat null", set("iat", nil)},
		{"iat negative", set("iat", -1)},
		{"exp missing", del("exp")},
		{"exp before iat", set("exp", now.Add(-time.Hour).Unix())},
		{"exp equal to iat", set("exp", now.Unix())},
		{"lifetime a second over 24h", set("exp", now.Add(account.MaxTokenLife+time.Second).Unix())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := goodClaims(now)
			c.mutate(claims)
			_, err := account.Verify(f.Sign(header, claims), now)
			if got := reasonOf(err); got != account.ReasonInvalid {
				t.Fatalf("reason = %q, want invalid", got)
			}
		})
	}
}

func TestVerifyAcceptedClaimShapes(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	header := map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"audience array that contains ours", func(m map[string]any) { m["aud"] = []string{"other", account.Audience} }},
		{"client_id instead of azp", func(m map[string]any) { delete(m, "azp"); m["client_id"] = account.ClientID }},
		{"azp and client_id both ours", func(m map[string]any) { m["client_id"] = account.ClientID }},
		{"lifetime of exactly 24h", func(m map[string]any) { m["exp"] = now.Add(account.MaxTokenLife).Unix() }},
		{"fractional NumericDate", func(m map[string]any) { m["iat"] = float64(now.Unix()) + 0.5 }},
		{"plan of the wrong type is free", func(m map[string]any) { m["plan"] = 3 }},
		{"unknown extra claims", func(m map[string]any) { m["scope"] = "openid library:read"; m["jti"] = "x" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := goodClaims(now)
			c.mutate(claims)
			if _, err := account.Verify(f.Sign(header, claims), now); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestVerifyClockSkew(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	if _, err := account.Verify(f.Token(accounttest.TokenOptions{IssuedAt: now.Add(account.ClockSkew)}), now); err != nil {
		t.Fatalf("iat exactly %v ahead must pass: %v", account.ClockSkew, err)
	}
	_, err := account.Verify(f.Token(accounttest.TokenOptions{IssuedAt: now.Add(account.ClockSkew + time.Second)}), now)
	if got := reasonOf(err); got != account.ReasonClockSkew {
		t.Fatalf("reason = %q, want clock_skew", got)
	}
}

func TestVerifyAlgorithmConfusion(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	enc := base64.RawURLEncoding
	signed := func(header map[string]any) string {
		h, _ := json.Marshal(header)
		c, _ := json.Marshal(goodClaims(now))
		return enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	}
	junk := enc.EncodeToString(make([]byte, ed25519.SignatureSize))
	cases := map[string]string{
		"HS256 signed with the public key as secret": hs256(f.Key.Public, signed(map[string]any{"alg": "HS256", "kid": f.Key.KID})),
		"alg none with no signature":                 signed(map[string]any{"alg": "none", "kid": f.Key.KID}) + ".",
		"alg none with a junk signature":             signed(map[string]any{"alg": "none", "kid": f.Key.KID}) + "." + junk,
		"a real EdDSA signature under alg HS256":     f.Token(accounttest.TokenOptions{Alg: "HS256"}),
		"a real EdDSA signature under alg ES256":     f.Token(accounttest.TokenOptions{Alg: "ES256"}),
		"alg in the wrong case":                      f.Token(accounttest.TokenOptions{Alg: "eddsa"}),
		"alg missing":                                f.Sign(map[string]any{"kid": f.Key.KID}, goodClaims(now)),
		"alg of the wrong type":                      f.Sign(map[string]any{"alg": []string{"EdDSA"}, "kid": f.Key.KID}, goodClaims(now)),
		"critical header":                            f.Sign(map[string]any{"alg": "EdDSA", "kid": f.Key.KID, "crit": []string{"exp"}}, goodClaims(now)),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonInvalid {
				t.Fatalf("reason = %q (%v), want invalid", reasonOf(err), err)
			}
		})
	}
}

// hs256 signs signedPart the way an alg-confusion attacker would: an HMAC
// keyed with the public key, which the attacker can read.
func hs256(secret []byte, signedPart string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signedPart))
	return signedPart + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyKeys(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	attackerPub, attackerPriv, _ := ed25519.GenerateKey(rand.Reader)
	enc := base64.RawURLEncoding
	forge := func(header map[string]any) string {
		h, _ := json.Marshal(header)
		c, _ := json.Marshal(goodClaims(now))
		signed := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
		return signed + "." + enc.EncodeToString(ed25519.Sign(attackerPriv, []byte(signed)))
	}
	jwk := map[string]any{"kty": "OKP", "crv": "Ed25519", "x": enc.EncodeToString(attackerPub)}

	t.Run("a kid that is not pinned", func(t *testing.T) {
		_, err := account.Verify(f.Token(accounttest.TokenOptions{KID: "rotated-away"}), now)
		if reasonOf(err) != account.ReasonKeyUnknown {
			t.Fatalf("reason = %q, want key_unknown", reasonOf(err))
		}
	})
	t.Run("no kid", func(t *testing.T) {
		_, err := account.Verify(f.Sign(map[string]any{"alg": "EdDSA"}, goodClaims(now)), now)
		if reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("reason = %q, want invalid", reasonOf(err))
		}
	})
	t.Run("signed by another key under the pinned kid", func(t *testing.T) {
		_, err := account.Verify(forge(map[string]any{"alg": "EdDSA", "kid": f.Key.KID}), now)
		if reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("reason = %q, want invalid", reasonOf(err))
		}
	})
	t.Run("jwk, jku and x5u headers are ignored, not trusted", func(t *testing.T) {
		tok := forge(map[string]any{"alg": "EdDSA", "kid": f.Key.KID, "jwk": jwk, "jku": "https://evil.example/jwks", "x5u": "https://evil.example/cert"})
		if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("a token signed by the key its own jwk header names was not refused: %v", err)
		}
		own := f.Sign(map[string]any{"alg": "EdDSA", "kid": f.Key.KID, "jwk": jwk, "jku": "https://evil.example/jwks"}, goodClaims(now))
		if _, err := account.Verify(own, now); err != nil {
			t.Fatalf("the extra headers must be ignored, not refused: %v", err)
		}
	})
	t.Run("an empty trusted set", func(t *testing.T) {
		tok := f.Token(accounttest.TokenOptions{})
		account.SetTrustedKeysForTest(t, nil)
		if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonKeyUnknown {
			t.Fatalf("reason = %q, want key_unknown", reasonOf(err))
		}
	})
	t.Run("a malformed pinned key does not panic", func(t *testing.T) {
		tok := f.Token(accounttest.TokenOptions{})
		account.SetTrustedKeysForTest(t, []account.Key{{KID: f.Key.KID, Public: f.Key.Public[:10]}})
		if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("reason = %q, want invalid", reasonOf(err))
		}
	})
}

// The pinned set holds the current signing key and the next one (spec §4.7), so
// a rotation never strands a client: either key verifies, and a later release
// that drops the old key stops trusting its tokens.
func TestVerifyDuringAKeyRotation(t *testing.T) {
	current := accounttest.New(t)
	next := accounttest.New(t)
	account.SetTrustedKeysForTest(t, []account.Key{current.Key, next.Key})
	now := current.Clock.Now()
	for name, f := range map[string]*accounttest.Fixture{"the current key": current, "the next key": next} {
		r, err := account.Verify(f.Token(accounttest.TokenOptions{}), now)
		if err != nil || r.KID != f.Key.KID {
			t.Errorf("a token signed by %s: receipt %+v, err %v", name, r, err)
		}
	}
	account.SetTrustedKeysForTest(t, []account.Key{next.Key}) // a later release drops the old key
	if _, err := account.Verify(current.Token(accounttest.TokenOptions{}), now); reasonOf(err) != account.ReasonKeyUnknown {
		t.Fatalf("a token signed by the dropped key: reason %q, want key_unknown", reasonOf(err))
	}
	if _, err := account.Verify(next.Token(accounttest.TokenOptions{}), now); err != nil {
		t.Fatalf("a token signed by the remaining key: %v", err)
	}
}

func TestVerifyTruncatedAndGarbageInput(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	good := f.Token(accounttest.TokenOptions{})
	enc := base64.RawURLEncoding
	object := enc.EncodeToString([]byte(`{"alg":"EdDSA","kid":"` + f.Key.KID + `"}`))
	sig := enc.EncodeToString(make([]byte, ed25519.SignatureSize))
	inputs := map[string]string{
		"empty":                    "",
		"one dot":                  "a.b",
		"four segments":            good + ".x",
		"empty segments":           "..",
		"empty signature":          strings.Join(strings.Split(good, ".")[:2], ".") + ".",
		"empty payload":            strings.Split(good, ".")[0] + ".." + strings.Split(good, ".")[2],
		"padding in a segment":     strings.Replace(good, ".", "=.", 1),
		"a space":                  good[:20] + " " + good[20:],
		"a newline at the end":     good + "\n",
		"a bad character":          good[:20] + "!" + good[21:],
		"standard base64 alphabet": good + "+/",
		"header not JSON":          enc.EncodeToString([]byte("not json")) + "." + object + "." + sig,
		"payload not JSON":         object + "." + enc.EncodeToString([]byte("not json")) + "." + sig,
		"payload a JSON array":     object + "." + enc.EncodeToString([]byte(`[1,2]`)) + "." + sig,
		"payload JSON null":        object + "." + enc.EncodeToString([]byte(`null`)) + "." + sig,
		"short signature":          object + "." + object + "." + enc.EncodeToString([]byte("short")),
		"far too large":            strings.Repeat("a", 9<<10),
	}
	for i := 10; i < len(good); i += 37 {
		inputs[fmt.Sprintf("truncated at %d", i)] = good[:i]
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			_, err := account.Verify(in, now)
			if reasonOf(err) != account.ReasonInvalid {
				t.Fatalf("reason = %q (%v), want invalid", reasonOf(err), err)
			}
			if len(in) > 12 && strings.Contains(err.Error(), in) {
				t.Fatal("the error message carries the token")
			}
		})
	}
}

func TestVerifyErrorMessagesNeverCarryTheToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	for name, tok := range map[string]string{
		"key_unknown": f.Token(accounttest.TokenOptions{KID: "rotated-away"}),
		"clock_skew":  f.Token(accounttest.TokenOptions{IssuedAt: now.Add(time.Hour)}),
		"invalid":     f.Token(accounttest.TokenOptions{Audience: []string{"x"}}),
	} {
		_, err := account.Verify(tok, now)
		if err == nil || strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), strings.Split(tok, ".")[2]) {
			t.Errorf("%s: the error is missing or carries token material", name)
		}
	}
}
```

- [ ] **Step 2: Create `internal/account/accounttest/accounttest_test.go`.**

```go
package accounttest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

func TestClock(t *testing.T) {
	c := NewClock(DefaultNow)
	if !c.Now().Equal(DefaultNow) {
		t.Fatalf("Now = %v, want %v", c.Now(), DefaultNow)
	}
	c.Advance(90 * time.Minute)
	if !c.Now().Equal(DefaultNow.Add(90 * time.Minute)) {
		t.Fatalf("after Advance, Now = %v", c.Now())
	}
	c.Set(DefaultNow.Add(-time.Hour))
	if !c.Now().Equal(DefaultNow.Add(-time.Hour)) {
		t.Fatalf("after Set back, Now = %v", c.Now())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Advance(time.Second)
				_ = c.Now()
			}
		}()
	}
	wg.Wait()
}

func TestNewTrustsOnlyItsKeyAndEnforces(t *testing.T) {
	keysBefore, dateBefore := len(account.TrustedKeys()), account.EnforceDate()
	var f *Fixture
	t.Run("inside", func(t *testing.T) {
		f = New(t)
		keys := account.TrustedKeys()
		if len(keys) != 1 || keys[0].KID != f.Key.KID {
			t.Fatalf("trusted keys = %+v, want only the fixture key", keys)
		}
		if !account.Enforced(f.Clock.Now(), time.Time{}) {
			t.Fatal("New must leave the gate enforced")
		}
		if _, err := account.Verify(f.Token(TokenOptions{}), f.Clock.Now()); err != nil {
			t.Fatalf("a default token must verify: %v", err)
		}
	})
	if len(account.TrustedKeys()) != keysBefore || !account.EnforceDate().Equal(dateBefore) {
		t.Fatalf("New leaked out of its test: %d keys (was %d), date %v (was %v)", len(account.TrustedKeys()), keysBefore, account.EnforceDate(), dateBefore)
	}
}

func claimsOf(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestTokenDefaultsAndOverrides(t *testing.T) {
	f := New(t)
	def := claimsOf(t, f.Token(TokenOptions{}))
	if def["iss"] != account.Issuer || def["aud"] != account.Audience || def["azp"] != account.ClientID || def["sub"] != "user-1" {
		t.Fatalf("default claims = %v", def)
	}
	if _, has := def["plan"]; has {
		t.Fatal("a token without Plan must carry no plan claim")
	}
	if def["iat"] != float64(f.Clock.Now().Unix()) || def["exp"] != float64(f.Clock.Now().Add(time.Hour).Unix()) {
		t.Fatalf("default times = %v / %v", def["iat"], def["exp"])
	}
	two := claimsOf(t, f.Token(TokenOptions{Audience: []string{"a", account.Audience}, Plan: "pro", Lifetime: 2 * time.Hour}))
	if aud, ok := two["aud"].([]any); !ok || len(aud) != 2 || two["plan"] != "pro" || two["exp"].(float64)-two["iat"].(float64) != 7200 {
		t.Fatalf("overridden claims = %v", two)
	}
}

func TestSignKeepsARealSignatureWhateverTheHeaderSays(t *testing.T) {
	f := New(t)
	tok := f.Sign(map[string]any{"alg": "HS256", "kid": f.Key.KID}, map[string]any{"sub": "x"})
	parts := strings.Split(tok, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(f.Key.Public, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatalf("Sign must always sign with the fixture key (err %v)", err)
	}
}

func TestDevKeyPairIsFixed(t *testing.T) {
	const wantPublic = "b2f5cb7a39788e27efd2b9c4f96873b495b61933f28c9c42904be76cd4328cff"
	pub, priv := DevKeyPair()
	if got := hex.EncodeToString(pub); got != wantPublic {
		t.Fatalf("the development public key changed to %s; keys_devaccount.go (B1b) pins %s", got, wantPublic)
	}
	msg := []byte("dev key check")
	if !ed25519.Verify(pub, msg, ed25519.Sign(priv, msg)) {
		t.Fatal("the development pair does not sign and verify")
	}
	pub2, _ := DevKeyPair()
	if !pub.Equal(pub2) || DevKID != "monoagent-dev-1" {
		t.Fatal("DevKeyPair must return the same pair every time")
	}
}
```

- [ ] **Step 3: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, the `accounttest` package has no non-test files yet:

```text
github.com/monoes/mono-agent/internal/account/accounttest: no non-test Go files in internal/account/accounttest
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
# github.com/monoes/mono-agent/internal/account/accounttest [github.com/monoes/mono-agent/internal/account/accounttest.test]
internal/account/accounttest/accounttest_test.go:17:7: undefined: NewClock
internal/account/accounttest/accounttest_test.go:17:16: undefined: DefaultNow
internal/account/accounttest/accounttest_test.go:18:20: undefined: DefaultNow
...
```

- [ ] **Step 4: Create `internal/account/verify.go`.**

```go
package account

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Receipt is what a verified access token proves.
type Receipt struct {
	Sub       string
	Plan      string // "free" when the token has no plan claim
	IssuedAt  time.Time
	ExpiresAt time.Time
	KID       string
}

// VerifyError is the typed reason a token was refused: ReasonInvalid
// (structure, signature or claims), ReasonKeyUnknown (the kid is not pinned) or
// ReasonClockSkew (iat is more than ClockSkew ahead of now). Its message names
// the failing check and never carries any part of the token.
type VerifyError struct {
	Reason Reason
	why    string
}

func (e *VerifyError) Error() string {
	switch e.Reason {
	case ReasonKeyUnknown:
		return fmt.Sprintf("account: token key %q is not pinned in this build", e.why)
	case ReasonClockSkew:
		return "account: token was issued in the future (" + e.why + "); check the system clock"
	}
	if e.why == "" {
		return "account: token is invalid"
	}
	return "account: token is invalid: " + e.why
}

func invalid(why string) *VerifyError { return &VerifyError{Reason: ReasonInvalid, why: why} }

// maxTokenBytes bounds the work a hostile session.json can cause.
const maxTokenBytes = 8 << 10

// Verify checks a compact JWS access token and returns its receipt. It accepts
// one algorithm (EdDSA), only keys pinned in this build, and never reads the
// jku, jwk, x5u or x5c headers. It does not reject an expired token: the grace
// period needs the receipt of one. now is only used for the clock-skew check.
func Verify(token string, now time.Time) (*Receipt, error) {
	r, verr := verifyToken(token)
	if verr != nil {
		return nil, verr
	}
	if r.IssuedAt.After(now.Add(ClockSkew)) {
		return nil, &VerifyError{Reason: ReasonClockSkew, why: "issued " + r.IssuedAt.Sub(now).Round(time.Second).String() + " ahead"}
	}
	return r, nil
}

// verifyToken is Verify without the clock: structure, algorithm, key,
// signature, claims and lifetime, checked in that order, so a claim is never
// read from a token whose signature did not verify.
func verifyToken(token string) (*Receipt, *VerifyError) {
	if len(token) > maxTokenBytes {
		return nil, invalid("too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || slices.Contains(parts, "") {
		return nil, invalid("not a compact JWS")
	}
	headerJSON, ok := decodeSegment(parts[0])
	if !ok {
		return nil, invalid("header is not base64url")
	}
	payloadJSON, ok := decodeSegment(parts[1])
	if !ok {
		return nil, invalid("payload is not base64url")
	}
	sig, ok := decodeSegment(parts[2])
	if !ok || len(sig) != ed25519.SignatureSize {
		return nil, invalid("signature is not a base64url Ed25519 signature")
	}

	var header map[string]json.RawMessage
	if json.Unmarshal(headerJSON, &header) != nil {
		return nil, invalid("header is not a JSON object")
	}
	var alg, kid string
	if _, ok := field(header, "alg", &alg); !ok || alg != "EdDSA" {
		return nil, invalid("alg is not EdDSA")
	}
	if _, has := header["crit"]; has {
		return nil, invalid("critical headers are not supported")
	}
	if _, ok := field(header, "kid", &kid); !ok || kid == "" {
		return nil, invalid("no kid")
	}
	key, ok := lookupKey(kid)
	if !ok {
		return nil, &VerifyError{Reason: ReasonKeyUnknown, why: kid}
	}
	if len(key.Public) != ed25519.PublicKeySize {
		return nil, invalid("pinned key is malformed")
	}
	if !ed25519.Verify(key.Public, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, invalid("signature does not verify")
	}

	var claims map[string]json.RawMessage
	if json.Unmarshal(payloadJSON, &claims) != nil {
		return nil, invalid("payload is not a JSON object")
	}
	r, verr := receiptFromClaims(claims)
	if verr != nil {
		return nil, verr
	}
	r.KID = kid
	return r, nil
}

// decodeSegment decodes one unpadded base64url segment. It is stricter than
// the standard decoder, which skips \r and \n: any byte outside the alphabet,
// and any non-canonical trailing bits, is an error.
func decodeSegment(s string) ([]byte, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return nil, false
		}
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return b, err == nil
}

// receiptFromClaims checks the claims of a token whose signature verified.
func receiptFromClaims(claims map[string]json.RawMessage) (*Receipt, *VerifyError) {
	var iss, sub string
	if _, ok := field(claims, "iss", &iss); !ok || iss != Issuer {
		return nil, invalid("issuer")
	}
	if !audienceMatches(claims["aud"]) {
		return nil, invalid("audience")
	}
	if !clientMatches(claims) {
		return nil, invalid("client")
	}
	if _, ok := field(claims, "sub", &sub); !ok || sub == "" {
		return nil, invalid("sub")
	}
	iat, okIat := numericDate(claims["iat"])
	exp, okExp := numericDate(claims["exp"])
	if !okIat || !okExp {
		return nil, invalid("iat or exp")
	}
	if !exp.After(iat) || exp.Sub(iat) > MaxTokenLife {
		return nil, invalid("lifetime")
	}
	// plan is read and not enforced (spec D14): a missing or odd one is "free".
	plan := "free"
	var p string
	if raw, has := claims["plan"]; has && json.Unmarshal(raw, &p) == nil && p != "" {
		plan = p
	}
	return &Receipt{Sub: sub, Plan: plan, IssuedAt: iat, ExpiresAt: exp}, nil
}

// field decodes the claim or header named key into dst. present reports
// whether it exists; ok is false when it exists with the wrong type. Keys match
// exactly: encoding/json's case-insensitive struct matching is not used.
func field(m map[string]json.RawMessage, key string, dst any) (present, ok bool) {
	raw, present := m[key]
	if !present {
		return false, true
	}
	return true, json.Unmarshal(raw, dst) == nil
}

// audienceMatches accepts aud as a string or an array of strings that
// contains Audience.
func audienceMatches(raw json.RawMessage) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == Audience
	}
	var many []string
	return json.Unmarshal(raw, &many) == nil && slices.Contains(many, Audience)
}

// clientMatches requires the client claim (azp, or client_id as RFC 9068 names
// it) and every one that is present to be ClientID.
func clientMatches(claims map[string]json.RawMessage) bool {
	seen := 0
	for _, name := range []string{"azp", "client_id"} {
		var v string
		present, ok := field(claims, name, &v)
		if !present {
			continue
		}
		if !ok || v != ClientID {
			return false
		}
		seen++
	}
	return seen > 0
}

// numericDate reads a JWT NumericDate (seconds, possibly fractional).
func numericDate(raw json.RawMessage) (time.Time, bool) {
	var v *float64
	if json.Unmarshal(raw, &v) != nil || v == nil || *v < 0 || *v > 1e11 {
		return time.Time{}, false
	}
	sec := int64(*v)
	return time.Unix(sec, int64((*v-float64(sec))*1e9)).UTC(), true
}
```

- [ ] **Step 5: Create `internal/account/accounttest/clock.go`.**

```go
// Package accounttest holds the fixtures every test of the monoes.me account
// gate uses: a throwaway signing key and token minting (Fixture), a settable
// clock (Clock), a guard in a chosen state (Install) and the fixed development
// key pair (DevKeyPair). Only test binaries may use it: it calls the
// account.*ForTest hooks, which panic anywhere else.
package accounttest

import (
	"sync"
	"time"
)

// DefaultNow is where a fresh Clock starts: a fixed instant, so tests do not
// depend on the wall clock.
var DefaultNow = time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)

// Clock is a settable clock safe for concurrent use. Its Now method is what a
// guard takes as GuardOptions.Now.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock that reads t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t, forward or back.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// Advance moves the clock by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
```

- [ ] **Step 6: Create `internal/account/accounttest/devkey.go`.** The development key pair is fixed on purpose. It is trusted only by builds with the `devaccount` tag (B1b adds its public half to `keys_devaccount.go`), so committing it grants nothing; `libraryfake` (B1b) signs with it so a `devaccount` binary can sign in against the fake.

```go
package accounttest

import (
	"crypto/ed25519"
	"encoding/hex"
)

// DevKID is the kid of the development signing key.
const DevKID = "monoagent-dev-1"

// devSeedHex is the seed of the fixed development key pair. It is a
// development-only key: builds with the devaccount tag trust its public half,
// and nothing else does, so publishing it gives no access to anything. A fake
// monoes.me (libraryfake) signs with it so a devaccount binary can sign in
// against the fake.
const devSeedHex = "0203903c037c7e989f9a31ac125086a822b629a82f58303218d5788978baf650"

// DevKeyPair returns the fixed development key pair: the public half is
// trusted by `-tags devaccount` builds, the private half signs for libraryfake.
func DevKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed, err := hex.DecodeString(devSeedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		panic("accounttest: the development seed is malformed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}
```

- [ ] **Step 7: Create `internal/account/accounttest/fixture.go`.**

```go
package accounttest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// Fixture is a throwaway signing key that the account package trusts for the
// rest of the test, and a clock to judge tokens by.
type Fixture struct {
	Private ed25519.PrivateKey
	Key     account.Key
	Clock   *Clock
}

// TokenOptions shapes a token. Every zero field takes the value of a valid
// token, so a test sets only the one thing it wants wrong.
type TokenOptions struct {
	Sub, Plan, Issuer, ClientClaim string
	Audience                       []string      // zero: [account.Audience]; one element is written as a string
	IssuedAt                       time.Time     // zero: Clock.Now()
	Lifetime                       time.Duration // zero: 1 hour
	KID                            string        // zero: the fixture key's kid
	Alg                            string        // zero: EdDSA
}

// New generates a throwaway key, makes the account package trust exactly that
// key and sets the enforcement date a day before the clock's start, so the gate
// is enforced. Both are restored when the test ends. A test that uses it must
// not call t.Parallel().
func New(t testing.TB) *Fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("accounttest: generating a key: %v", err)
	}
	// The kid is derived from the key, so two fixtures in one test never share a kid.
	f := &Fixture{Private: priv, Key: account.Key{KID: "accounttest-" + hex.EncodeToString(pub[:4]), Public: pub}, Clock: NewClock(DefaultNow)}
	account.SetTrustedKeysForTest(t, []account.Key{f.Key})
	account.SetEnforceFromForTest(t, f.Clock.Now().Add(-24*time.Hour))
	return f
}

// Token signs a JWT valid at Clock.Now() unless o says otherwise.
func (f *Fixture) Token(o TokenOptions) string {
	iat := o.IssuedAt
	if iat.IsZero() {
		iat = f.Clock.Now()
	}
	life := o.Lifetime
	if life == 0 {
		life = time.Hour
	}
	var aud any = o.Audience
	switch len(o.Audience) {
	case 0:
		aud = account.Audience
	case 1:
		aud = o.Audience[0]
	}
	claims := map[string]any{
		"iss": orDefault(o.Issuer, account.Issuer),
		"aud": aud,
		"azp": orDefault(o.ClientClaim, account.ClientID),
		"sub": orDefault(o.Sub, "user-1"),
		"iat": iat.Unix(),
		"exp": iat.Add(life).Unix(),
	}
	if o.Plan != "" {
		claims["plan"] = o.Plan
	}
	header := map[string]any{"alg": orDefault(o.Alg, "EdDSA"), "kid": orDefault(o.KID, f.Key.KID), "typ": "JWT"}
	return f.Sign(header, claims)
}

// Sign signs any header and claims with the fixture key, whatever alg the
// header names. It is how a test builds a token that is wrong in a way
// TokenOptions cannot say (a missing claim, a claim of the wrong type, extra
// headers).
func (f *Fixture) Sign(header, claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	return signed + "." + enc.EncodeToString(ed25519.Sign(f.Private, []byte(signed)))
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```

- [ ] **Step 8: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 9: Commit.**

```bash
git add internal/account/verify.go internal/account/verify_test.go internal/account/accounttest/clock.go internal/account/accounttest/devkey.go internal/account/accounttest/fixture.go internal/account/accounttest/accounttest_test.go
```

```bash
git commit -m "feat(account): strict EdDSA JWS verification and the accounttest fixture" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The session and its verdict

The pure state machine of spec §4.3 and the clock guard of §4.5: `Evaluate` turns a stored session and a time into a `Status` with no I/O. `judge` is `Evaluate` after the token has been verified, so the guard (Task 6) can cache the verification and re-judge on every `Status` call without checking a signature each time.

**Files:**
- Create: `internal/account/session.go`
- Test: `internal/account/evaluate_test.go` (external package, uses `accounttest`), `internal/account/session_internal_test.go` (package `account`)

**Interfaces:**
- Consumes: `verifyToken`, `Receipt`, `VerifyError`, `Verify` (Task 2); `Status`, `State…`, `Reason…`, `User`, `EnforceDate`, `Enforced`, `ClockSkew`, `GraceWindow` (Task 1).
- Produces:
  - `type Session struct{ V int; Host, AccessToken string; User *User; Plan string; HW, LastAttempt time.Time; LastResult, State, Reason string }` with the JSON tags of index §3.2. `LastResult` is `ok`, `unreachable`, `server_error`, `keyring_unavailable`, `key_unknown` or `refused`.
  - `func NewSession(host, accessToken string, user *User, now time.Time) (*Session, error)`: verifies the token at `now` and builds the session with `HW` reset to the token's `iat`, `LastAttempt` now, `LastResult` `ok`.
  - `func Evaluate(sess *Session, now time.Time) Status`.
  - Unexported: `judge(sess *Session, rcpt *Receipt, verr *VerifyError, now time.Time) Status`, `graceReason(lastResult string) Reason`, `elapsed(now, t time.Time, d time.Duration) bool` (a zero or future `t` counts as elapsed), constants `sessionVersion = 1`, `resultOK = "ok"`, `stateRefused = "refused"`.

- [ ] **Step 1: Create `internal/account/evaluate_test.go`.** `TestEvaluateMatrix` is the table across `iat`, `exp`, `now`, `hw`: every boundary (a second before and exactly at `exp`, `iat + 24h`, the 5 minute skew and rollback allowances) and the order of the checks.

```go
package account_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

func TestEvaluateMatrix(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	const hour = time.Hour
	cases := []struct {
		name        string
		iat         time.Duration // relative to now
		life        time.Duration
		hw          time.Duration // relative to now
		lastResult  string
		wantState   account.State
		wantReason  account.Reason
		wantAllowed bool
	}{
		{"fresh token", -10 * time.Minute, hour, 0, "ok", account.StateOK, "", true},
		{"a second before exp", -hour + time.Second, hour, 0, "", account.StateOK, "", true},
		{"at exp, nothing recorded: grace, unreachable", -hour, hour, 0, "", account.StateGrace, account.ReasonUnreachable, true},
		{"grace after an unreachable attempt", -2 * hour, hour, 0, "unreachable", account.StateGrace, account.ReasonUnreachable, true},
		{"grace after a server error", -2 * hour, hour, 0, "server_error", account.StateGrace, account.ReasonServerError, true},
		{"grace with the keyring unavailable", -2 * hour, hour, 0, "keyring_unavailable", account.StateGrace, account.ReasonKeyringUnavailable, true},
		{"grace after a last ok", -2 * hour, hour, 0, "ok", account.StateGrace, account.ReasonUnreachable, true},
		{"grace after an unknown key is server_error", -2 * hour, hour, 0, "key_unknown", account.StateGrace, account.ReasonServerError, true},
		{"a second before iat+24h", -24*hour + time.Second, hour, 0, "unreachable", account.StateGrace, account.ReasonUnreachable, true},
		{"exactly iat+24h", -24 * hour, hour, 0, "unreachable", account.StateLocked, account.ReasonExpired, false},
		{"long expired", -30 * hour, hour, 0, "unreachable", account.StateLocked, account.ReasonExpired, false},
		{"expired after an unknown key", -30 * hour, hour, 0, "key_unknown", account.StateLocked, account.ReasonKeyUnknown, false},
		{"a 24h token is ok to its exp", -24*hour + time.Second, 24 * hour, 0, "ok", account.StateOK, "", true},
		{"iat exactly 5m ahead", 5 * time.Minute, hour, 0, "ok", account.StateOK, "", true},
		{"iat a second over 5m ahead", 5*time.Minute + time.Second, hour, 0, "ok", account.StateLocked, account.ReasonClockSkew, false},
		{"hw exactly 5m ahead", -10 * time.Minute, hour, 5 * time.Minute, "ok", account.StateOK, "", true},
		{"hw a second over 5m ahead", -10 * time.Minute, hour, 5*time.Minute + time.Second, "ok", account.StateLocked, account.ReasonClockRollback, false},
		{"rollback wins over expiry", -30 * hour, hour, 2 * hour, "unreachable", account.StateLocked, account.ReasonClockRollback, false},
		{"rollback wins over skew", 20 * time.Minute, hour, 2 * hour, "ok", account.StateLocked, account.ReasonClockRollback, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := &account.Session{
				V: 1, Host: account.HostURL, LastResult: c.lastResult,
				AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(c.iat), Lifetime: c.life}),
				HW:          now.Add(c.hw),
			}
			st := account.Evaluate(sess, now)
			if st.State != c.wantState || st.Reason != c.wantReason {
				t.Fatalf("Evaluate = %s/%q, want %s/%q", st.State, st.Reason, c.wantState, c.wantReason)
			}
			if !st.Enforced || st.Allowed() != c.wantAllowed {
				t.Fatalf("Enforced = %v, Allowed = %v, want enforced and allowed %v", st.Enforced, st.Allowed(), c.wantAllowed)
			}
			if !st.IssuedAt.Equal(now.Add(c.iat)) || !st.ValidUntil.Equal(now.Add(c.iat+c.life)) || !st.GraceUntil.Equal(now.Add(c.iat+24*hour)) {
				t.Fatalf("times = %v / %v / %v, want iat, iat+life, iat+24h", st.IssuedAt, st.ValidUntil, st.GraceUntil)
			}
			if st.V != 1 || st.User == nil || st.User.ID != "user-1" || st.Plan != "free" {
				t.Fatalf("status fields = %+v, want v 1, the sub of the token as user and plan free", st)
			}
		})
	}
}

func TestEvaluateWithoutAUsableToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	valid := f.Token(accounttest.TokenOptions{})
	notAJWT := "not-a-jwt"
	user := &account.User{ID: "u-9", Email: "a@b.c"}
	cases := []struct {
		name       string
		sess       *account.Session
		wantReason account.Reason
	}{
		{"no session", nil, account.ReasonNotLoggedIn},
		{"an empty token", &account.Session{V: 1, User: user}, account.ReasonNotLoggedIn},
		{"garbage", &account.Session{V: 1, AccessToken: notAJWT, User: user}, account.ReasonInvalid},
		{"a key that is not pinned", &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{KID: "gone"}), User: user}, account.ReasonKeyUnknown},
		{"refused, whatever the token", &account.Session{V: 1, AccessToken: valid, User: user, State: "refused", Reason: "revoked"}, account.ReasonRefused},
		{"refused with the token cleared", &account.Session{V: 1, User: user, State: "refused"}, account.ReasonRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := account.Evaluate(c.sess, now)
			if st.State != account.StateLocked || st.Reason != c.wantReason || st.Allowed() {
				t.Fatalf("Evaluate = %s/%q allowed=%v, want locked/%q", st.State, st.Reason, st.Allowed(), c.wantReason)
			}
			if c.sess != nil && (st.User == nil || st.User.ID != "u-9") {
				t.Fatalf("the user must be kept for the message: %+v", st.User)
			}
			if !st.IssuedAt.IsZero() || st.Plan != "" {
				t.Fatalf("a locked session without a verified token reports no times or plan: %+v", st)
			}
		})
	}
}

func TestEvaluateDormantAndEnforcementDate(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	expired := &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-30 * time.Hour)})}

	account.SetEnforceFromForTest(t, time.Time{})
	st := account.Evaluate(expired, now)
	if st.State != account.StateLocked || st.Reason != account.ReasonExpired || st.Enforced || !st.Allowed() || !st.EnforceFrom.IsZero() {
		t.Fatalf("dormant: %+v; the state is still computed, but nothing is enforced", st)
	}
	if st := account.Evaluate(nil, now); st.Reason != account.ReasonNotLoggedIn || !st.Allowed() {
		t.Fatalf("dormant with no session: %+v", st)
	}

	date := now.Add(48 * time.Hour)
	account.SetEnforceFromForTest(t, date)
	if st := account.Evaluate(expired, now); st.Enforced || !st.Allowed() || !st.EnforceFrom.Equal(date) {
		t.Fatalf("before the date: %+v", st)
	}
	if st := account.Evaluate(expired, date); !st.Enforced || st.Allowed() {
		t.Fatalf("at the date: %+v", st)
	}
	// Setting the clock back does not postpone the date: hw has seen it.
	rolledBack := *expired
	rolledBack.HW = date.Add(time.Minute)
	if st := account.Evaluate(&rolledBack, now); !st.Enforced {
		t.Fatalf("with hw past the date, enforcement must hold even for an earlier now: %+v", st)
	}
}

func TestEvaluateDoesNotShareTheSessionUser(t *testing.T) {
	f := accounttest.New(t)
	sess := &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{}), User: &account.User{ID: "u-1", Email: "x@y.z"}}
	st := account.Evaluate(sess, f.Clock.Now())
	st.User.Email = "changed"
	if sess.User.Email != "x@y.z" {
		t.Fatal("Status.User must be a copy: a caller changed the stored session")
	}
}

func TestStatusJSONShape(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	st := account.Evaluate(&account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{Plan: "pro"}), User: &account.User{ID: "u-1", Email: "a@b.c"}, HW: now}, now)
	keys := func(st account.Status) []string {
		raw, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	want := []string{"enforce_from", "enforced", "grace_until", "issued_at", "plan", "reason", "state", "user", "v", "valid_until"}
	if got := keys(st); !reflect.DeepEqual(got, want) {
		t.Fatalf("signed-in JSON keys = %v, want %v", got, want)
	}
	// Locked with no session: the optional fields drop out, the always-present ones stay.
	if got, want := keys(account.Evaluate(nil, now)), []string{"enforce_from", "enforced", "plan", "reason", "state", "v"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("no-session JSON keys = %v, want %v", got, want)
	}
}

func TestNewSession(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	iat := now.Add(-10 * time.Minute)
	issued := f.Token(accounttest.TokenOptions{IssuedAt: iat, Sub: "u-3", Plan: "pro"})
	sess, err := account.NewSession("https://monoes.me", issued, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if sess.V != 1 || sess.Host != "https://monoes.me" || sess.AccessToken != issued || sess.User == nil || sess.User.ID != "u-3" ||
		sess.Plan != "pro" || !sess.HW.Equal(iat) || !sess.LastAttempt.Equal(now) || sess.LastResult != "ok" || sess.State != "" {
		t.Fatalf("session fields: v=%d host=%q user=%v plan=%q hw=%v attempt=%v last=%q state=%q; hw must be the iat of the token (server time is authoritative)",
			sess.V, sess.Host, sess.User, sess.Plan, sess.HW, sess.LastAttempt, sess.LastResult, sess.State)
	}
	user := &account.User{ID: "u-3", Email: "a@b.c"}
	if sess, _ := account.NewSession("h", issued, user, now); sess.User != user {
		t.Fatal("a given user must be kept")
	}
	notAJWT := "not-a-jwt"
	if _, err := account.NewSession("h", notAJWT, nil, now); reasonOf(err) != account.ReasonInvalid {
		t.Fatalf("a token that does not verify must not become a session: %v", err)
	}
	ahead := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(time.Hour)})
	if _, err := account.NewSession("h", ahead, nil, now); reasonOf(err) != account.ReasonClockSkew {
		t.Fatalf("a token from the future must not become a session: %v", err)
	}
}
```

- [ ] **Step 2: Create `internal/account/session_internal_test.go`.**

```go
package account

import (
	"testing"
	"time"
)

func TestElapsed(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"never recorded", time.Time{}, true},
		{"a minute ago", now.Add(-time.Minute), true},
		{"a second short of a minute", now.Add(-time.Minute + time.Second), false},
		{"just now", now, false},
		{"in the future: the clock went back", now.Add(time.Hour), true},
	}
	for _, c := range cases {
		if got := elapsed(now, c.t, time.Minute); got != c.want {
			t.Errorf("%s: elapsed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGraceReason(t *testing.T) {
	cases := map[string]Reason{
		"unreachable":         ReasonUnreachable,
		"server_error":        ReasonServerError,
		"keyring_unavailable": ReasonKeyringUnavailable,
		"key_unknown":         ReasonServerError,
		"ok":                  ReasonUnreachable,
		"refused":             ReasonUnreachable,
		"":                    ReasonUnreachable,
		"something new":       ReasonUnreachable,
	}
	for in, want := range cases {
		if got := graceReason(in); got != want {
			t.Errorf("graceReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJudgeWithoutAReceiptIsInvalidNotAPanic(t *testing.T) {
	st := judge(&Session{V: 1, AccessToken: "x"}, nil, nil, time.Now())
	if st.State != StateLocked || st.Reason != ReasonInvalid {
		t.Fatalf("judge = %s/%s, want locked/invalid", st.State, st.Reason)
	}
}
```

- [ ] **Step 3: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors:

```text
# github.com/monoes/mono-agent/internal/account [github.com/monoes/mono-agent/internal/account.test]
internal/account/session_internal_test.go:22:13: undefined: elapsed
internal/account/session_internal_test.go:40:13: undefined: graceReason
internal/account/session_internal_test.go:47:8: undefined: judge
internal/account/session_internal_test.go:47:15: undefined: Session
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
FAIL
```

- [ ] **Step 4: Create `internal/account/session.go`.**

```go
package account

import "time"

// Session is the stored session, the JSON of session.json (spec §4.6). It
// holds a token that expires within the hour and no secret that needs the
// keychain: the refresh token lives in refresh.enc.
type Session struct {
	V           int       `json:"v"`
	Host        string    `json:"host"`
	AccessToken string    `json:"access_token"`
	User        *User     `json:"user,omitempty"`
	Plan        string    `json:"plan,omitempty"`
	HW          time.Time `json:"hw,omitzero"`
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastResult  string    `json:"last_result,omitempty"` // "ok", "unreachable", "server_error", "keyring_unavailable", "key_unknown" or "refused"
	State       string    `json:"state,omitempty"`       // "" or "refused"
	Reason      string    `json:"reason,omitempty"`
}

const (
	sessionVersion = 1
	resultOK       = "ok"
	stateRefused   = "refused"
)

// NewSession builds the session to store for a freshly issued access token. It
// verifies the token at now and starts the clock guard at the token's iat:
// server time is authoritative, which is also the way out of a rolled-back
// clock (spec §4.5). A nil user is taken from the token's sub.
func NewSession(host, accessToken string, user *User, now time.Time) (*Session, error) {
	r, err := Verify(accessToken, now)
	if err != nil {
		return nil, err
	}
	if user == nil {
		user = &User{ID: r.Sub}
	}
	return &Session{
		V: sessionVersion, Host: host, AccessToken: accessToken, User: user, Plan: r.Plan,
		HW: r.IssuedAt, LastAttempt: now, LastResult: resultOK,
	}, nil
}

// Evaluate is the pure verdict of a stored session at a time: no I/O. It
// verifies the token, applies the clock guard and the grace rule, and fills
// Enforced and EnforceFrom from rollout.go. A nil session is
// locked(not_logged_in); a session marked refused is locked(refused).
func Evaluate(sess *Session, now time.Time) Status {
	var rcpt *Receipt
	var verr *VerifyError
	if sess != nil && sess.State != stateRefused && sess.AccessToken != "" {
		rcpt, verr = verifyToken(sess.AccessToken)
	}
	return judge(sess, rcpt, verr, now)
}

// judge is Evaluate after the token has been verified. verifyToken does not
// read the clock, so a guard can cache its result and call judge on every
// Status without checking a signature each time. The checks run in this order:
// no session, refused, no token, a token that does not verify, a clock that
// went back, a token from the future, then ok, grace and expired.
func judge(sess *Session, rcpt *Receipt, verr *VerifyError, now time.Time) Status {
	st := Status{V: 1, EnforceFrom: EnforceDate()}
	var hw time.Time
	if sess != nil {
		hw = sess.HW
	}
	st.Enforced = Enforced(now, hw)
	locked := func(r Reason) Status {
		st.State, st.Reason = StateLocked, r
		return st
	}
	if sess == nil {
		return locked(ReasonNotLoggedIn)
	}
	if sess.User != nil {
		u := *sess.User
		st.User = &u
	}
	st.Plan = sess.Plan
	switch {
	case sess.State == stateRefused:
		return locked(ReasonRefused)
	case sess.AccessToken == "":
		return locked(ReasonNotLoggedIn)
	case verr != nil:
		return locked(verr.Reason)
	case rcpt == nil:
		return locked(ReasonInvalid) // a caller that forgot to verify must not crash the gate
	}
	st.IssuedAt, st.ValidUntil, st.GraceUntil = rcpt.IssuedAt, rcpt.ExpiresAt, rcpt.IssuedAt.Add(GraceWindow)
	st.Plan = rcpt.Plan
	if st.User == nil {
		st.User = &User{ID: rcpt.Sub}
	}
	switch {
	case now.Before(sess.HW.Add(-ClockSkew)):
		return locked(ReasonClockRollback)
	case rcpt.IssuedAt.After(now.Add(ClockSkew)):
		return locked(ReasonClockSkew)
	case now.Before(rcpt.ExpiresAt):
		st.State = StateOK
		return st
	case now.Before(st.GraceUntil):
		st.State, st.Reason = StateGrace, graceReason(sess.LastResult)
		return st
	case sess.LastResult == string(ReasonKeyUnknown):
		// The last refresh returned a token this build cannot verify: the
		// server rotated its key (spec §4.7). Say so, not just "expired".
		return locked(ReasonKeyUnknown)
	}
	return locked(ReasonExpired)
}

// graceReason says why a session in grace was not refreshed: the stored result
// of the last attempt, unreachable when none is recorded. A key this build
// does not know is reported as server_error until the grace ends.
func graceReason(lastResult string) Reason {
	switch r := Reason(lastResult); r {
	case ReasonUnreachable, ReasonServerError, ReasonKeyringUnavailable:
		return r
	case ReasonKeyUnknown:
		return ReasonServerError
	}
	return ReasonUnreachable
}

// elapsed reports whether d has passed between t and now. A zero t, or a t
// after now (the clock went back since it was stored), counts as elapsed: a
// stored timestamp must never be able to hold a refresh off, or a rolled-back
// clock could not be repaired by the refresh that resets it.
func elapsed(now, t time.Time, d time.Duration) bool {
	return t.IsZero() || t.After(now) || now.Sub(t) >= d
}
```

- [ ] **Step 5: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 6: Commit.**

```bash
git add internal/account/session.go internal/account/evaluate_test.go internal/account/session_internal_test.go
```

```bash
git commit -m "feat(account): the session and its verdict, with the clock guard" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The refresh-token sealer and the key accessor in `internal/secrets`

`refresh.enc` is the refresh token sealed under a key from the OS keyring or the file-keyring fallback, as the vault does it (spec §4.6). `internal/secrets` keeps its keyring primitives unexported, so this task adds the smallest exported accessor there, in a new file, and edits no existing line.

**Files:**
- Create: `internal/secrets/account_kek.go`, `internal/account/sealer.go`
- Test: `internal/secrets/account_kek_test.go` (package `secrets`, reuses `resetKEKState` from `keyring_test.go:16` and `forceKeyringUnavailable`, `captureFileKeyringWarns`, `stubFilePassphrase`, `forceKeyringFirstUseWriteFails` from `filekeyring_test.go:21,36,48,69`), `internal/account/sealer_test.go` (package `account`)

**Interfaces:**
- Consumes: `secrets.Encrypt`, `secrets.Decrypt` (`internal/secrets/crypto.go`), the unexported `getOrCreateKEK` (`keyring.go:93`), `peekKEK` (`keyring.go:124`), `peekFileKEK`, `fileKeyringPath` (`filekeyring.go:127`), `unwrapFileKEK`, `rememberFilePassphrase` (`filekeyring_passphrase.go:182`), `readPassphraseFile`, `readPassphraseFileAs`, `ConfiguredPassphrasePath`, `filePassphraseHint`.
- Produces:
  - `func AccountKEK(create, interactive bool) (kek []byte, found bool, err error)` in package `secrets`. `create=false` only reads (`found=false` is "no key yet", nothing is written). `interactive=false` never prompts for the file keyring's passphrase (it reads the key only when the passphrase is remembered in the process, in `MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE` or in the configured passphrase file, and creates a key only through the OS keychain). An unusable key store is an error, never `found=false`.
  - `var ErrKeyringUnavailable = errors.New("account: key store unavailable")`; `type Sealer interface{ Seal(plain []byte) ([]byte, error); Open(sealed []byte) ([]byte, error) }`.
  - `func NewKeyringSealer() Sealer` (never prompts: for every implicit use), `func NewInteractiveKeyringSealer() Sealer` (may prompt, as the vault does: for an explicit sign-in), `func NewMemorySealer() Sealer` (tests). Every failure to open the key store, a missing key, or a key that does not open the blob is an error wrapping `ErrKeyringUnavailable`.
  - Sealed format: one version byte `1`, the 12-byte nonce, the AES-256-GCM ciphertext.
  - Unexported: `keyringSealer{kek func(create bool) ([]byte, bool, error)}` (the field lets a test stand in for the key store), `seal`, `open`.

Two facts in the existing code shape the accessor, and a test pins each:
- `fetchOrCreateKEK` routes a first-use creation to the file keyring whenever `MONOAGENT_ALLOW_FILE_KEYRING=1`, even when the OS keychain works (`keyring.go:162-181`), while `peekKEK` then answers "not found" for an OS keychain without an entry (`keyring.go:124-132`). Reading with `peekKEK` alone would never find the key the sign-in just created. The accessor looks in the file keyring after an empty OS lookup.
- `promptFilePassphrase` reads stdin when the process has not called `MarkStdinConsumed` (`filekeyring_passphrase.go:80-90`). The gate runs before any command claims stdin, so an implicit refresh must not reach it.

- [ ] **Step 1: Create `internal/secrets/account_kek_test.go`.**

```go
package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/zalando/go-keyring"
)

func TestAccountKEKIDIsNotAProfileID(t *testing.T) {
	if profiledir.ValidProfileID(accountKEKID) {
		t.Fatalf("%q must not be a valid profile id, or a profile could own the account key", accountKEKID)
	}
}

func TestAccountKEKReadCreatesNothing(t *testing.T) {
	resetKEKState(t)
	keyring.MockInit()
	for _, interactive := range []bool{true, false} {
		kek, found, err := AccountKEK(false, interactive)
		if err != nil || found || kek != nil {
			t.Fatalf("interactive=%v: found=%v err=%v, want no key and no error", interactive, found, err)
		}
	}
	if _, err := keyring.Get(keyringService, kekAccount(accountKEKID)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("a read created a keychain entry: %v", err)
	}
}

func TestAccountKEKOSKeyringRoundTrip(t *testing.T) {
	resetKEKState(t)
	keyring.MockInit()
	created, found, err := AccountKEK(true, true)
	if err != nil || !found || len(created) != 32 {
		t.Fatalf("create: len=%d found=%v err=%v", len(created), found, err)
	}
	for _, c := range []struct{ create, interactive bool }{{false, true}, {false, false}, {true, false}} {
		got, found, err := AccountKEK(c.create, c.interactive)
		if err != nil || !found || !bytes.Equal(got, created) {
			t.Fatalf("create=%v interactive=%v: found=%v err=%v, want the created key", c.create, c.interactive, found, err)
		}
	}
	if _, err := keyring.Get(keyringService, "kek-"+accountKEKID); err != nil {
		t.Fatalf("the key is not stored in the vault's entry shape: %v", err)
	}
}

func TestAccountKEKKeyringUnavailableIsAnError(t *testing.T) {
	resetKEKState(t)
	t.Setenv(fileKeyringEnv, "")
	forceKeyringUnavailable(t)
	for _, c := range []struct{ create, interactive bool }{{false, true}, {false, false}, {true, true}, {true, false}} {
		if _, found, err := AccountKEK(c.create, c.interactive); err == nil || found {
			t.Fatalf("create=%v interactive=%v: found=%v err=%v, want an error", c.create, c.interactive, found, err)
		}
	}
}

// createAccountFileKEK makes the account's file keyring on a host whose OS
// keychain answers "no entry" (first-use creation goes to the file) and
// returns the key and the throwaway HOME.
func createAccountFileKEK(t *testing.T, passphrase string) (kek []byte, home string) {
	t.Helper()
	resetKEKState(t)
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(fileKeyringEnv, "1")
	captureFileKeyringWarns(t)
	forceKeyringFirstUseWriteFails(t)
	stubFilePassphrase(t, passphrase)
	kek, found, err := AccountKEK(true, true)
	if err != nil || !found || len(kek) != 32 {
		t.Fatalf("interactive create under the file keyring: len=%d found=%v err=%v", len(kek), found, err)
	}
	return kek, home
}

func TestAccountKEKQuietNeverPrompts(t *testing.T) {
	created, home := createAccountFileKEK(t, "pw one")
	if _, err := os.Stat(filepath.Join(home, ".monoagent", "vault", ".file-keyring-"+accountKEKID)); err != nil {
		t.Fatalf("the file keyring was not created: %v", err)
	}
	got, found, err := AccountKEK(false, true) // the OS keychain has no entry: look in the file
	if err != nil || !found || !bytes.Equal(got, created) {
		t.Fatalf("interactive read: found=%v err=%v, want the created key", found, err)
	}

	// From here on, asking for a passphrase fails the test.
	forgetFilePassphrases()
	filePassphraseFunc = func() (string, error) {
		t.Fatal("the quiet path asked for a passphrase")
		return "", nil
	}
	if _, found, err := AccountKEK(false, false); found || err == nil || !strings.Contains(err.Error(), "must not prompt") {
		t.Fatalf("quiet read with no passphrase source: found=%v err=%v, want the no-prompt error", found, err)
	}
	if _, found, err := AccountKEK(true, false); found || err == nil {
		t.Fatalf("quiet create with no passphrase source: found=%v err=%v, want an error", found, err)
	}

	if _, err := SetConfiguredPassphrase("pw one"); err != nil {
		t.Fatalf("SetConfiguredPassphrase: %v", err)
	}
	got, found, err = AccountKEK(false, false)
	if err != nil || !found || !bytes.Equal(got, created) {
		t.Fatalf("quiet read with the configured passphrase file: found=%v err=%v, want the created key", found, err)
	}
}

// The account's file keyring is a first-class file keyring: `secret keyring
// status` lists it and a new passphrase must unlock it (a deliberate, visible
// consequence of sharing the vault's key stores).
func TestAccountKEKFileKeyringIsListedAndGuardsPassphraseChanges(t *testing.T) {
	createAccountFileKEK(t, "pw one")
	if got := existingFileKeyrings(); len(got) != 1 || got[0] != accountKEKID {
		t.Fatalf("existingFileKeyrings = %v, want [%s]", got, accountKEKID)
	}
	if _, err := SetConfiguredPassphrase("pw two"); err == nil {
		t.Fatal("a passphrase that cannot unlock the account's key must be refused")
	}
}
```

- [ ] **Step 2: Create `internal/account/sealer_test.go`.**

```go
package account

import (
	"bytes"
	"errors"
	"testing"
)

func TestMemorySealerRoundTrip(t *testing.T) {
	s := NewMemorySealer()
	plain := []byte("refresh-value-1")
	a, err := s.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Seal(plain)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of one plaintext must differ (fresh nonce)")
	}
	if bytes.Contains(a, plain) {
		t.Fatal("the sealed blob contains the plaintext")
	}
	got, err := s.Open(a)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open did not return the sealed plaintext (match=%v, err=%v)", bytes.Equal(got, plain), err)
	}
	empty, _ := s.Seal(nil)
	if got, err := s.Open(empty); err != nil || len(got) != 0 {
		t.Fatalf("an empty plaintext must round-trip (len %d, err %v)", len(got), err)
	}
}

func TestSealedBlobsThatCannotBeOpened(t *testing.T) {
	s := NewMemorySealer()
	sealed, _ := s.Seal([]byte("refresh-value-1"))
	flipped := append([]byte(nil), sealed...)
	flipped[len(flipped)-1] ^= 1
	newer := append([]byte(nil), sealed...)
	newer[0] = 2
	cases := map[string][]byte{
		"another key":         func() []byte { b, _ := NewMemorySealer().Seal([]byte("x")); return b }(),
		"a flipped byte":      flipped,
		"truncated":           sealed[:len(sealed)-3],
		"shorter than a head": sealed[:5],
		"empty":               nil,
		"a newer version":     newer,
	}
	for name, blob := range cases {
		if _, err := s.Open(blob); !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("%s: err = %v, want ErrKeyringUnavailable", name, err)
		}
	}
}

func TestKeyringSealerMapsEveryKeyStoreFailureToErrKeyringUnavailable(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	good := keyringSealer{kek: func(bool) ([]byte, bool, error) { return key, true, nil }}
	sealed, err := good.Seal([]byte("refresh-value-1"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := good.Open(sealed); err != nil || string(got) != "refresh-value-1" {
		t.Fatalf("the round trip failed (match=%v, err=%v)", string(got) == "refresh-value-1", err)
	}

	broken := errors.New("keychain locked")
	failing := keyringSealer{kek: func(bool) ([]byte, bool, error) { return nil, false, broken }}
	missing := keyringSealer{kek: func(bool) ([]byte, bool, error) { return nil, false, nil }}
	wrongKey := keyringSealer{kek: func(bool) ([]byte, bool, error) { return bytes.Repeat([]byte{9}, 32), true, nil }}
	shortKey := keyringSealer{kek: func(bool) ([]byte, bool, error) { return []byte("short"), true, nil }}
	for name, c := range map[string]struct {
		s    Sealer
		open bool
	}{
		"seal, key store failing": {failing, false},
		"open, key store failing": {failing, true},
		"open, key missing":       {missing, true},
		"open, wrong key":         {wrongKey, true},
		"seal, key of bad length": {shortKey, false},
	} {
		var err error
		if c.open {
			_, err = c.s.Open(sealed)
		} else {
			_, err = c.s.Seal([]byte("x"))
		}
		if !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("%s: err = %v, want ErrKeyringUnavailable", name, err)
		}
	}
	if _, err := failing.Open(sealed); !errors.Is(err, broken) {
		t.Errorf("the cause must stay in the chain: %v", err)
	}
}

// The production sealers, over the in-memory keyring TestMain installs.
func TestKeyringSealersOverTheMockKeyring(t *testing.T) {
	sealed, err := NewKeyringSealer().Seal([]byte("refresh-value-1"))
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Sealer{"quiet": NewKeyringSealer(), "interactive": NewInteractiveKeyringSealer()} {
		if got, err := s.Open(sealed); err != nil || string(got) != "refresh-value-1" {
			t.Errorf("%s: Open failed (match=%v, err=%v)", name, string(got) == "refresh-value-1", err)
		}
	}
}
```

- [ ] **Step 3: Run both; they fail to compile.**

Run: `go test ./internal/secrets/ -run '^TestAccountKEK' -count=1 -race`

Expected: FAIL, build errors:

```text
# github.com/monoes/mono-agent/internal/secrets [github.com/monoes/mono-agent/internal/secrets.test]
internal/secrets/account_kek_test.go:16:31: undefined: accountKEKID
internal/secrets/account_kek_test.go:17:89: undefined: accountKEKID
internal/secrets/account_kek_test.go:25:22: undefined: AccountKEK
internal/secrets/account_kek_test.go:30:54: undefined: accountKEKID
...
```

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors:

```text
# github.com/monoes/mono-agent/internal/account [github.com/monoes/mono-agent/internal/account.test]
internal/account/sealer_test.go:10:7: undefined: NewMemorySealer
internal/account/sealer_test.go:34:7: undefined: NewMemorySealer
internal/account/sealer_test.go:41:50: undefined: NewMemorySealer
internal/account/sealer_test.go:49:46: undefined: ErrKeyringUnavailable
...
```

- [ ] **Step 4: Create `internal/secrets/account_kek.go`.** A new file; no existing file in `internal/secrets` changes.

```go
package secrets

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// accountKEKID names the key that seals the monoes.me account's refresh token
// (internal/account) in the vault's own key stores: the OS keychain entry
// "kek-<id>" under the vault's service, or the file keyring's
// ".file-keyring-<id>". It contains ".." so no real profile can own it
// (profiledir.ValidProfileID rejects that), and so it reads as "not a
// profile" in `secret keyring status`.
const accountKEKID = "monoes..account"

// AccountKEK returns the 32-byte key that seals the monoes.me account's
// refresh token, from the same key stores and with the same fallbacks the
// vault uses for a profile's KEK.
//
//   - create=false only reads: found=false means there is no key yet, and
//     nothing is written to the keychain or to disk.
//   - create=true generates the key when it is missing (the first sign-in).
//   - interactive=true may ask for the file keyring's passphrase, exactly as
//     the vault does. Only an explicit command that owns the terminal (the
//     sign-in) should pass it.
//   - interactive=false never prompts, so a gate that runs before a command
//     has claimed stdin cannot swallow the command's piped input as a
//     passphrase. With the file keyring it reads the key only when the
//     passphrase is already known (remembered in this process, or in
//     MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE or the configured passphrase
//     file) and it creates a key only through the OS keychain.
//
// A key store that cannot be opened is an error, not found=false.
func AccountKEK(create, interactive bool) (kek []byte, found bool, err error) {
	if interactive || !fileKeyringEnabled() {
		// Without the file keyring nothing can prompt, so one path serves both.
		return accountKEKVault(create)
	}
	return accountKEKQuiet(create)
}

// accountKEKVault reads or creates the key through the vault's own functions.
func accountKEKVault(create bool) ([]byte, bool, error) {
	if create {
		kek, err := getOrCreateKEK(accountKEKID)
		return kek, err == nil, err
	}
	kek, found, err := peekKEK(accountKEKID)
	if err != nil || found || !fileKeyringEnabled() {
		return kek, found, err
	}
	// The OS keychain answers but has no entry. With the file keyring opted in
	// the vault's first-use creation (fetchOrCreateKEK) puts the key in the
	// file, so look there before reporting "no key".
	return peekFileKEK(accountKEKID)
}

// accountKEKQuiet is AccountKEK(create, false) with the file keyring opted in.
func accountKEKQuiet(create bool) ([]byte, bool, error) {
	keyringIOMu.Lock()
	stored, err := keyringGet(keyringService, kekAccount(accountKEKID))
	keyringIOMu.Unlock()
	if err == nil {
		key, derr := hex.DecodeString(stored)
		if derr != nil {
			return nil, false, fmt.Errorf("secrets: decoding stored KEK: %w", derr)
		}
		return key, true, nil
	}
	// No entry, or an OS keychain that is unavailable: both lead to the file
	// keyring, as fetchOrCreateKEK routes them.
	kek, found, ferr := readAccountFileKEK()
	if ferr != nil || found {
		return kek, found, ferr
	}
	if create {
		return nil, false, fmt.Errorf("secrets: creating the file-keyring key needs its passphrase prompt, which this call must not show; %s", filePassphraseHint)
	}
	return nil, false, nil
}

// readAccountFileKEK reads the account's file-keyring KEK without creating it
// and without prompting. A missing file is found=false.
func readAccountFileKEK() (kek []byte, found bool, err error) {
	path := fileKeyringPath(accountKEKID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secrets: reading file-based KEK: %w", err)
	}
	var env fileKEKEnvelope
	if json.Unmarshal(data, &env) != nil || env.Format != fileKEKFormat || env.Version != fileKEKVersion || env.KDF != "argon2id" {
		return nil, false, fmt.Errorf("secrets: file-based KEK %s is not a recognized format", path)
	}
	pass, err := quietFilePassphrase(accountKEKID)
	if err != nil {
		return nil, false, err
	}
	kek, err = unwrapFileKEK(env, pass)
	if err != nil {
		return nil, false, err
	}
	rememberFilePassphrase(accountKEKID, pass)
	warnFileKeyring()
	return kek, true, nil
}

// quietFilePassphrase is filePassphraseFor without its last two sources (stdin
// and the terminal): a passphrase remembered in this process, then
// MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE, then the configured passphrase file.
func quietFilePassphrase(profileID string) (string, error) {
	knownFilePassphrasesMu.Lock()
	pass, ok := knownFilePassphrases[profileID]
	knownFilePassphrasesMu.Unlock()
	if ok {
		return pass, nil
	}
	if path := os.Getenv(filePassphraseFileEnv); path != "" {
		return readPassphraseFile(path)
	}
	if path := ConfiguredPassphrasePath(); fileExists(path) {
		return readPassphraseFileAs(path, "configured passphrase file")
	}
	return "", fmt.Errorf("secrets: the file keyring (MONOAGENT_ALLOW_FILE_KEYRING=1) needs its passphrase and this call must not prompt for it; %s", filePassphraseHint)
}
```

- [ ] **Step 5: Run the accessor tests; they pass.**

Run: `go test ./internal/secrets/ -run '^TestAccountKEK' -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/secrets	<time>
```

- [ ] **Step 6: Create `internal/account/sealer.go`.**

```go
package account

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/monoes/mono-agent/internal/secrets"
)

// ErrKeyringUnavailable means the key that seals the refresh token cannot be
// used: the key store cannot be opened, the key is missing, or it does not open
// the stored token. The session stays usable until its grace ends, and `account
// status` reports keyring_unavailable so it is not mistaken for a network
// problem.
var ErrKeyringUnavailable = errors.New("account: key store unavailable")

// Sealer seals the refresh token under a key from the OS keyring or the
// file-keyring fallback, as the vault does.
type Sealer interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// sealedVersion starts every sealed blob: version byte, 12-byte nonce, then
// the AES-256-GCM ciphertext (the vault's primitive, secrets.Encrypt).
const (
	sealedVersion = 1
	nonceSize     = 12
)

// keyringSealer seals under the account key of internal/secrets. kek is
// secrets.AccountKEK with the interactive choice bound in; it is a field so a
// test can stand in for the key store.
type keyringSealer struct {
	kek func(create bool) (key []byte, found bool, err error)
}

// NewKeyringSealer is the production sealer for implicit use: it never prompts
// for the file keyring's passphrase, so a gate that runs before a command has
// claimed stdin cannot swallow the command's piped input. With the file keyring
// the passphrase must come from MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE or the
// configured passphrase file, or the key store counts as unavailable.
func NewKeyringSealer() Sealer {
	return keyringSealer{kek: func(create bool) ([]byte, bool, error) { return secrets.AccountKEK(create, false) }}
}

// NewInteractiveKeyringSealer is NewKeyringSealer for an explicit command that
// owns the terminal (the sign-in): it may ask for the file keyring's
// passphrase, exactly as the vault does.
func NewInteractiveKeyringSealer() Sealer {
	return keyringSealer{kek: func(create bool) ([]byte, bool, error) { return secrets.AccountKEK(create, true) }}
}

func (s keyringSealer) Seal(plain []byte) ([]byte, error) {
	key, _, err := s.kek(true)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}
	return seal(key, plain)
}

func (s keyringSealer) Open(sealed []byte) ([]byte, error) {
	key, found, err := s.kek(false)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	case !found:
		return nil, fmt.Errorf("%w: the key that sealed the refresh token is missing; sign in again", ErrKeyringUnavailable)
	}
	return open(key, sealed)
}

// memorySealer is the test sealer: a random key that lives in memory.
type memorySealer struct{ key []byte }

// NewMemorySealer returns a sealer with a fresh in-memory key, for tests. Two
// of them cannot open each other's blobs.
func NewMemorySealer() Sealer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("account: no randomness: " + err.Error())
	}
	return memorySealer{key: key}
}

func (m memorySealer) Seal(plain []byte) ([]byte, error)  { return seal(m.key, plain) }
func (m memorySealer) Open(sealed []byte) ([]byte, error) { return open(m.key, sealed) }

func seal(key, plain []byte) ([]byte, error) {
	ciphertext, nonce, err := secrets.Encrypt(key, plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(ciphertext))
	out = append(out, sealedVersion)
	out = append(out, nonce...)
	return append(out, ciphertext...), nil
}

// open fails with ErrKeyringUnavailable for a damaged blob and for a blob the
// key does not open: either way the refresh token cannot be read.
func open(key, sealed []byte) ([]byte, error) {
	if len(sealed) < 1+nonceSize || sealed[0] != sealedVersion {
		return nil, fmt.Errorf("%w: the sealed refresh token is damaged or from a newer version", ErrKeyringUnavailable)
	}
	plain, err := secrets.Decrypt(key, sealed[1+nonceSize:], sealed[1:1+nonceSize])
	if err != nil {
		return nil, fmt.Errorf("%w: the stored key does not open the sealed refresh token: %w", ErrKeyringUnavailable, err)
	}
	return plain, nil
}
```

- [ ] **Step 7: Run the account tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 8: Prove no existing vault behavior changed.** The whole `internal/secrets` package, as it was before plus the new tests (about 80 seconds).

Run: `go test ./internal/secrets/ -count=1`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/secrets	<time>
```

- [ ] **Step 9: Commit.**

```bash
git add internal/secrets/account_kek.go internal/secrets/account_kek_test.go internal/account/sealer.go internal/account/sealer_test.go
```

```bash
git commit -m "feat(account): seal the refresh token under the vault's key stores" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The on-disk store and its cross-process lock

`~/.monoagent/account/` with `session.json`, `refresh.enc` and `session.lock` (spec §4.6). Atomic 0600 writes, the directory (0700) created by the first write and by nothing else, and an exclusive lock that follows the repo's own helpers (`internal/daemonhb/lock.go:32` `LockFile`, `lock_unix.go:11`, `lock_windows.go:12`) but waits with a context instead of failing at once. `internal/account` may not import `daemonhb` (index §3.1 import rule), so the two small per-OS primitives are repeated here.

**Files:**
- Create: `internal/account/store.go`, `internal/account/lock_unix.go`, `internal/account/lock_windows.go`
- Test: `internal/account/store_test.go` (external package)

**Interfaces:**
- Consumes: `Session`, `sessionVersion` (Task 3); `Sealer`, `NewKeyringSealer`, `ErrKeyringUnavailable` (Task 4).
- Produces:
  - `type Store interface` exactly as index §3.2: `Load() (*Session, error)`, `Save(*Session) error`, `LoadRefresh() (string, error)`, `SaveRefresh(token string) error`, `DeleteRefresh() error`, `Lock(ctx context.Context) (unlock func(), err error)`, `Mtime() (time.Time, error)`, `Dir() string`.
  - `func DefaultDir() (string, error)` (`~/.monoagent/account` through `os.UserHomeDir`), `func OpenStore(dir string, s Sealer) Store` (`dir == ""` is `DefaultDir()`, `s == nil` is `NewKeyringSealer()`; it touches nothing on disk).
  - Behavior the later tasks rely on: every read of a missing file answers "none" and creates nothing; `LoadRefresh` with no `refresh.enc` never reaches the sealer (so never the keychain); `Load` returns an error for a file that is not JSON or whose `v` is not 1 (a `Save` overwrites it); `Save` writes `v: 1` on a copy; `SaveRefresh("")` is refused; `Lock` is the only call besides the writers that creates the directory and `session.lock`, and its `unlock` is safe to call twice.
  - Unexported: `errLockHeld`, `tryLock(f *os.File) error`, `unlock(f *os.File)`, `writeFileAtomic(path string, data []byte) error`, `renameReplacing(from, to string) error` (retried on Windows, where a rename can fail while another process has the target open).

CI runs the tests on Linux only (every job of `.github/workflows/ci.yml` is `ubuntu-latest`; macOS and Windows only build, in `release.yml`). On Windows production relies on `renameReplacing`'s retry, which no test exercises: Go opens files without `FILE_SHARE_DELETE`, so a rename over a file another process is reading can fail there, and `TestReadersNeverSeeAPartialSession` may fail if someone runs it on Windows. Real-binary smoke on Windows is a B5a concern.

- [ ] **Step 1: Create `internal/account/store_test.go`.** It also defines the helpers `brokenSealer`, `newStore`, `names` and `describe(s *account.Session) string` (a session without its access token, so a failing test never prints a token) that the guard tests reuse.

```go
package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

type brokenSealer struct{}

// describe prints a session without its access token, so a failing test never
// puts a token in its output.
func describe(s *account.Session) string {
	if s == nil {
		return "<no session>"
	}
	return fmt.Sprintf("v=%d host=%q user=%v plan=%q hw=%v attempt=%v last=%q state=%q reason=%q token-set=%t",
		s.V, s.Host, s.User, s.Plan, s.HW, s.LastAttempt, s.LastResult, s.State, s.Reason, s.AccessToken != "")
}

func (brokenSealer) Seal([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }
func (brokenSealer) Open([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }

func newStore(t *testing.T) (account.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "account")
	return account.OpenStore(dir, account.NewMemorySealer()), dir
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestReadsCreateNothing(t *testing.T) {
	st, dir := newStore(t)
	if sess, err := st.Load(); sess != nil || err != nil {
		t.Fatalf("Load = a session %v, %v, want none and no error", sess != nil, err)
	}
	if rt, err := st.LoadRefresh(); rt != "" || err != nil {
		t.Fatalf("LoadRefresh = a token %v, %v, want none and no error", rt != "", err)
	}
	if mt, err := st.Mtime(); !mt.IsZero() || err != nil {
		t.Fatalf("Mtime = %v, %v, want zero, nil", mt, err)
	}
	if err := st.DeleteRefresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read created %s (stat err %v)", dir, err)
	}
}

func TestDefaultDirFollowsHomeAndTouchesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := account.DefaultDir()
	if want := filepath.Join(home, ".monoagent", "account"); err != nil || got != want {
		t.Fatalf("DefaultDir = %q, %v, want %q", got, err, want)
	}
	st := account.OpenStore("", nil)
	if st.Dir() != got {
		t.Fatalf("Dir = %q, want %q", st.Dir(), got)
	}
	_, _ = st.Load()
	_, _ = st.LoadRefresh()
	_, _ = st.Mtime()
	_ = st.DeleteRefresh()
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("opening and reading the default store wrote %d entries into HOME", len(entries))
	}
}

func TestStoreWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	st := account.OpenStore("", nil)
	if sess, err := st.Load(); sess != nil || err != nil {
		t.Fatalf("Load = a session %v, %v: with no home there is no session, not an error", sess != nil, err)
	}
	if rt, err := st.LoadRefresh(); rt != "" || err != nil {
		t.Fatalf("LoadRefresh = a token %v, %v", rt != "", err)
	}
	if err := st.Save(&account.Session{}); err == nil {
		t.Fatal("Save must fail without a home directory")
	}
	if err := st.SaveRefresh("x"); err == nil {
		t.Fatal("SaveRefresh must fail without a home directory")
	}
	if _, err := st.Lock(context.Background()); err == nil {
		t.Fatal("Lock must fail without a home directory")
	}
}

func TestSaveWritesAnAtomic0600File(t *testing.T) {
	st, dir := newStore(t)
	hw := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	in := &account.Session{
		Host: "https://monoes.me", AccessToken: "x.y.z", Plan: "free", HW: hw, LastAttempt: hw, LastResult: "ok",
		User: &account.User{ID: "u1", Email: "a@b.c", Username: "ab"},
	}
	if err := st.Save(in); err != nil {
		t.Fatal(err)
	}
	if in.V != 0 {
		t.Fatal("Save must not change the caller's session")
	}
	out, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := *in
	want.V = 1
	if !reflect.DeepEqual(*out, want) {
		t.Fatalf("Load = %s, want %s", describe(out), describe(&want))
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"session.json"}) {
		t.Fatalf("directory holds %v, want only session.json (no temporary file left)", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "session.json"))
	if !json.Valid(raw) || !bytes.Contains(raw, []byte("\n  \"host\"")) || raw[len(raw)-1] != '\n' {
		t.Fatalf("session.json is not indented JSON with a final newline:\n%s", raw)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "session.json"))
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes: file %v, directory %v, want 0600 and 0700", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
}

func TestReadersNeverSeeAPartialSession(t *testing.T) {
	st, _ := newStore(t)
	if err := st.Save(&account.Session{Host: "h", AccessToken: "t0"}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int32
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if sess, err := st.Load(); err != nil || sess == nil {
					bad.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if err := st.Save(&account.Session{Host: "h", AccessToken: "t", LastResult: string(rune('a' + i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d reads saw a missing or partial session while it was being replaced", bad.Load())
	}
}

func TestMtimeFollowsSaves(t *testing.T) {
	st, dir := newStore(t)
	if err := st.Save(&account.Session{Host: "h"}); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "session.json"), old, old); err != nil {
		t.Fatal(err)
	}
	if mt, _ := st.Mtime(); !mt.Equal(old) {
		t.Fatalf("Mtime = %v, want %v", mt, old)
	}
	if err := st.Save(&account.Session{Host: "h2"}); err != nil {
		t.Fatal(err)
	}
	if mt, _ := st.Mtime(); mt.Equal(old) || mt.IsZero() {
		t.Fatalf("Mtime did not change after a Save: %v", mt)
	}
}

func TestLoadRefusesAFileItCannotTrustAndASaveRepairsIt(t *testing.T) {
	st, dir := newStore(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.json")
	for name, content := range map[string]string{
		"not JSON":        "{not json",
		"empty":           "",
		"a JSON array":    "[]",
		"no version":      `{"host":"h"}`,
		"a newer version": `{"v":2,"host":"h"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if sess, err := st.Load(); err == nil || sess != nil {
			t.Errorf("%s: Load = a session %v, %v, want an error", name, sess != nil, err)
		}
	}
	if err := st.Save(&account.Session{Host: "h"}); err != nil {
		t.Fatal(err)
	}
	if sess, err := st.Load(); err != nil || sess == nil || sess.Host != "h" {
		t.Fatalf("a Save must repair the file: a session %v, %v", sess != nil, err)
	}
}

func TestRefreshTokenIsSealedOnDisk(t *testing.T) {
	sealer := account.NewMemorySealer()
	dir := filepath.Join(t.TempDir(), "account")
	st := account.OpenStore(dir, sealer)
	refresh := "rt-0123456789abcdef"
	if err := st.SaveRefresh(refresh); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "refresh.enc"))
	if err != nil || bytes.Contains(raw, []byte(refresh)) {
		t.Fatalf("refresh.enc missing or readable as plaintext (err %v)", err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "refresh.enc")); fi.Mode().Perm() != 0o600 {
			t.Fatalf("refresh.enc mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	if got, err := st.LoadRefresh(); err != nil || got != refresh {
		t.Fatalf("LoadRefresh did not return the saved refresh token (match=%v, err=%v)", got == refresh, err)
	}
	other := account.OpenStore(dir, account.NewMemorySealer())
	if _, err := other.LoadRefresh(); !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("a store with another key: err = %v, want ErrKeyringUnavailable", err)
	}
	if err := st.DeleteRefresh(); err != nil {
		t.Fatal(err)
	}
	if got, err := st.LoadRefresh(); got != "" || err != nil {
		t.Fatalf("after DeleteRefresh: a token %v, %v", got != "", err)
	}
	if err := st.DeleteRefresh(); err != nil {
		t.Fatalf("a second DeleteRefresh: %v", err)
	}
	if err := st.SaveRefresh(""); err == nil {
		t.Fatal("an empty refresh token must be refused")
	}
}

func TestAKeyStoreThatCannotBeOpened(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	broken := account.OpenStore(dir, brokenSealer{})
	if err := broken.SaveRefresh("rt-1"); !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("SaveRefresh err = %v, want ErrKeyringUnavailable", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed seal must not leave a directory or a file behind")
	}
	good := account.OpenStore(dir, account.NewMemorySealer())
	if err := good.SaveRefresh("rt-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := broken.LoadRefresh(); !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("LoadRefresh err = %v, want ErrKeyringUnavailable", err)
	}
}

func TestLockExcludesAndCreatesItsFileOnlyWhenTaken(t *testing.T) {
	a, dir := newStore(t)
	b := account.OpenStore(dir, account.NewMemorySealer())
	ctx := context.Background()
	unlockA, err := a.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dir, "session.lock")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("session.lock: %v, %v", fi, err)
		}
	}
	short, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	if _, err := b.Lock(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second Lock while held: err = %v, want a deadline", err)
	}
	unlockA()
	unlockA() // safe twice
	unlockB, err := b.Lock(ctx)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlockB()
}

func TestLockWaitsForTheHolder(t *testing.T) {
	a, dir := newStore(t)
	b := account.OpenStore(dir, account.NewMemorySealer())
	unlockA, err := a.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		unlock, err := b.Lock(ctx)
		if err != nil {
			got <- -1
			return
		}
		unlock()
		got <- time.Since(start)
	}()
	time.Sleep(150 * time.Millisecond)
	unlockA()
	if waited := <-got; waited < 100*time.Millisecond {
		t.Fatalf("the second Lock returned after %v: it did not wait for the holder (-1 means it failed)", waited)
	}
}

func TestLockSerializesGoroutines(t *testing.T) {
	_, dir := newStore(t)
	var inside, overlaps atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := account.OpenStore(dir, account.NewMemorySealer())
			for i := 0; i < 5; i++ {
				unlock, err := st.Lock(context.Background())
				if err != nil {
					overlaps.Add(100)
					return
				}
				if inside.Add(1) != 1 {
					overlaps.Add(1)
				}
				time.Sleep(time.Millisecond)
				inside.Add(-1)
				unlock()
			}
		}()
	}
	wg.Wait()
	if overlaps.Load() != 0 {
		t.Fatalf("the lock let %d holders overlap (100 or more means a Lock failed)", overlaps.Load())
	}
}
```

- [ ] **Step 2: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors:

```text
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/store_test.go:36:38: undefined: account.Store
internal/account/store_test.go:39:17: undefined: account.OpenStore
internal/account/store_test.go:78:22: undefined: account.DefaultDir
internal/account/store_test.go:82:16: undefined: account.OpenStore
...
```

- [ ] **Step 3: Create `internal/account/store.go`.**

```go
package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	sessionFile = "session.json"
	refreshFile = "refresh.enc"
	lockFile    = "session.lock"
)

// Store is the on-disk session. Nothing creates a file or a directory until a
// write: every read of a missing session answers "none". The mutating methods
// (Save, SaveRefresh, DeleteRefresh) are safe across processes only under Lock.
type Store interface {
	Load() (*Session, error)                             // nil, nil when there is no session.json
	Save(*Session) error                                 // atomic, 0600
	LoadRefresh() (string, error)                        // "", nil when none; ErrKeyringUnavailable when the key store cannot be opened
	SaveRefresh(token string) error                      // seals it; the directory is created here if needed
	DeleteRefresh() error                                // nil when there is none
	Lock(ctx context.Context) (unlock func(), err error) // exclusive, cross-process
	Mtime() (time.Time, error)                           // of session.json; zero time when absent
	Dir() string
}

// DefaultDir is ~/.monoagent/account, found through os.UserHomeDir. The path
// is the same whatever --db-path says: there is one session per OS user.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("account: finding the home directory: %w", err)
	}
	return filepath.Join(home, ".monoagent", "account"), nil
}

type fileStore struct {
	dir    string
	sealer Sealer
	err    error // the default directory could not be resolved: no session can exist, and none can be written
}

// OpenStore opens the session in dir (the default directory when dir is "")
// with s as the refresh-token sealer (the production keyring sealer when s is
// nil). It touches nothing on disk.
func OpenStore(dir string, s Sealer) Store {
	st := &fileStore{dir: dir, sealer: s}
	if dir == "" {
		st.dir, st.err = DefaultDir()
	}
	if s == nil {
		st.sealer = NewKeyringSealer()
	}
	return st
}

func (s *fileStore) Dir() string { return s.dir }

func (s *fileStore) path(name string) string { return filepath.Join(s.dir, name) }

func (s *fileStore) Load() (*Session, error) {
	if s.err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(s.path(sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("account: reading %s: %w", sessionFile, err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("account: %s is not valid: %w", sessionFile, err)
	}
	if sess.V != sessionVersion {
		return nil, fmt.Errorf("account: %s has version %d, this build reads version %d", sessionFile, sess.V, sessionVersion)
	}
	return &sess, nil
}

func (s *fileStore) Save(sess *Session) error {
	if s.err != nil {
		return s.err
	}
	cp := *sess
	cp.V = sessionVersion
	data, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(sessionFile), append(data, '\n'))
}

func (s *fileStore) Mtime() (time.Time, error) {
	if s.err != nil {
		return time.Time{}, nil
	}
	fi, err := os.Stat(s.path(sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

// LoadRefresh unseals the refresh token. A missing refresh.enc is "", nil and
// never reaches the key store, so reading a machine with no session cannot
// raise a keychain prompt.
func (s *fileStore) LoadRefresh() (string, error) {
	if s.err != nil {
		return "", nil
	}
	sealed, err := os.ReadFile(s.path(refreshFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("account: reading %s: %w", refreshFile, err)
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *fileStore) SaveRefresh(token string) error {
	if s.err != nil {
		return s.err
	}
	if token == "" {
		return errors.New("account: empty refresh token")
	}
	sealed, err := s.sealer.Seal([]byte(token))
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(refreshFile), sealed)
}

func (s *fileStore) DeleteRefresh() error {
	if s.err != nil {
		return nil
	}
	if err := os.Remove(s.path(refreshFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// errLockHeld is what tryLock returns while another holder has the lock.
var errLockHeld = errors.New("account: lock is held")

// Lock takes the exclusive cross-process lock on session.lock, waiting until
// ctx ends. The lock file and the directory are created here, so Lock is only
// for a caller that is about to write. unlock is safe to call twice. The OS
// drops the lock if the process dies, so a crash leaves nothing stale.
func (s *fileStore) Lock(ctx context.Context) (func(), error) {
	if s.err != nil {
		return nil, s.err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.path(lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for delay := 5 * time.Millisecond; ; {
		err := tryLock(f)
		if err == nil {
			break
		}
		if !errors.Is(err, errLockHeld) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 50*time.Millisecond)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlock(f)
			f.Close()
		})
	}, nil
}

// writeFileAtomic writes data to path (mode 0600, directory 0700 created on
// the first write) through a temporary file in the same directory and a rename,
// so a reader sees the old file or the new one, never half of one.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has moved it
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameReplacing(tmp.Name(), path)
}

// renameReplacing renames over an existing file. On Windows the rename can
// fail while another process has the target open, so it is retried briefly: a
// lost write here could lose a rotated refresh token.
func renameReplacing(from, to string) error {
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 5
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}
```

- [ ] **Step 4: Create `internal/account/lock_unix.go`.**

```go
//go:build !windows

package account

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock without waiting, the way
// internal/daemonhb does; Store.Lock polls it so a context can end the wait.
func tryLock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return errLockHeld
		}
		return err
	}
	return nil
}

func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
```

- [ ] **Step 5: Create `internal/account/lock_windows.go`.**

```go
//go:build windows

package account

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes an exclusive LockFileEx lock without waiting, the way
// internal/daemonhb does; Store.Lock polls it so a context can end the wait.
func tryLock(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return errLockHeld
		}
		return err
	}
	return nil
}

func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
```

- [ ] **Step 6: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 7: Vet for the other operating systems.** The Windows file is never compiled on a Mac or Linux machine otherwise.

Run: `GOOS=windows go vet ./internal/account/... && GOOS=linux go vet ./internal/account/...`

Expected: no output.

- [ ] **Step 8: Commit.**

```bash
git add internal/account/store.go internal/account/lock_unix.go internal/account/lock_windows.go internal/account/store_test.go
```

```bash
git commit -m "feat(account): the on-disk session, atomic writes and the cross-process lock" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The guard's cached verdict

`Guard` turns the stored session into a verdict that every gate can ask for cheaply (spec D17). `NewGuard` does no I/O; the session is read on the first `Status` and again when `session.json`'s modification time changes, checked at most once per `Poll`. `Status` and `Require` make no network call and no write. The refresh algorithm (Task 7) and the background refresher (Task 8) are separate files on the same type.

**Files:**
- Create: `internal/account/guard.go`
- Test: `internal/account/guard_helpers_test.go` (the fake monoes.me and the test machine every guard test uses), `internal/account/guard_test.go`

**Interfaces:**
- Consumes: `Store`, `OpenStore`, `NewMemorySealer` (Tasks 4 and 5); `Session`, `NewSession`, `judge`, `elapsed`, `stateRefused` (Task 3); `Receipt`, `VerifyError`, `verifyToken` (Task 2); `Status`, `Reason…`, `LoginRequiredError`, `PollInterval` (Task 1); `accounttest.New`, `Fixture`, `Clock` (Task 2).
- Produces:
  - `type TokenSet struct{ AccessToken, RefreshToken string }`; `type Refresher interface{ Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) }` (B1b implements it: `*RefusedError` only for `invalid_grant`, `*TransientError` for everything else).
  - `type GuardOptions struct{ Store Store; Refresher Refresher; Now func() time.Time; Poll time.Duration }` (defaults: the default store, no refresher, `time.Now`, `PollInterval`).
  - `type Guard struct{…}`, `func NewGuard(o GuardOptions) *Guard`, `func (g *Guard) Status() Status`, `func (g *Guard) Require(ctx context.Context) error`, `func (g *Guard) OnRefused(fn func(Status))`, `func (g *Guard) Close()`.
  - Unexported, used by Tasks 7 and 8: the timing constants `hwInterval`, `hwLockWait`, `refreshCallTimeout`, `lockWaitTimeout`, `backoffMin`, `backoffMax`; the fields `sem chan struct{}` (one refresh at a time in the process), `mu`, `sess`, `rcpt`, `verr`, `lastHWAttempt`, `loopCancel`, `loopDone`, `closed`; the methods `adopt(sess *Session)` (make a session the caller just read or wrote under the file lock the cached one) and `cached() (*Session, *Receipt)`.
  - Test helpers (package `account_test`, used by Tasks 7, 8 and 9): `fakeRefresher` (rotates the refresh token on every use and refuses one it already rotated, so a double refresh shows up as a lockout), `env` with `newEnv`, `newGuard(poll)`, `signIn(age, life)`, `save`, `touch`, `session`, and the helpers `eventually`, `settle`, `transient`.
- Guard behavior pinned here: a failed read keeps a session that already works in memory (with nothing cached it is `locked(invalid)`); a `session.json` that was removed (B1b's logout deletes it under the lock) reads as "no session" at the next poll, and a later sign-in is picked up; `OnRefused` appends (several callbacks all fire), runs each callback on its own goroutine, once per transition into `locked(refused)`, and a callback registered during a refusal is called once at once.

- [ ] **Step 1: Create `internal/account/guard_helpers_test.go`.**

```go
package account_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// fakeRefresher stands in for monoes.me. It accepts one refresh token at a time
// and rotates it on every use, as the real server does, so a caller that
// refreshes twice with the same token is refused (invalid_grant) and the test
// sees a lockout, not just a call count of two.
type fakeRefresher struct {
	f     *accounttest.Fixture
	calls atomic.Int32

	mu        sync.Mutex
	valid     string        // the one refresh token the server accepts
	seq       int           // how many it has issued
	err       error         // answered instead of a token set
	empty     bool          // answer a success with no tokens in it
	badAccess string        // the access token a success carries instead of a good one
	kid       string        // the kid of the access token it mints
	delay     time.Duration // spent inside the call, so racing callers overlap
	block     bool          // wait for the context instead of answering
}

func (r *fakeRefresher) set(fn func(*fakeRefresher)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r)
}

func (r *fakeRefresher) Refresh(ctx context.Context, refreshToken string) (*account.TokenSet, error) {
	r.calls.Add(1)
	r.mu.Lock()
	block, delay := r.block, r.delay
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.err != nil:
		return nil, r.err
	case r.empty:
		return &account.TokenSet{}, nil
	case refreshToken != r.valid:
		return nil, &account.RefusedError{Description: "refresh token already used"}
	}
	r.seq++
	r.valid = fmt.Sprintf("rt-%d", r.seq+1)
	access := r.f.Token(accounttest.TokenOptions{KID: r.kid})
	if r.badAccess != "" {
		access = r.badAccess
	}
	return &account.TokenSet{AccessToken: access, RefreshToken: r.valid}, nil
}

// env is one machine: a fixture (key, clock), a session directory and a fake
// monoes.me, with a guard over it. newGuard makes another guard over the same
// session, as another process would.
type env struct {
	t     *testing.T
	f     *accounttest.Fixture
	dir   string
	seal  account.Sealer
	store account.Store
	ref   *fakeRefresher
	g     *account.Guard
	bumps int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := accounttest.New(t)
	dir := filepath.Join(t.TempDir(), "account")
	seal := account.NewMemorySealer()
	e := &env{t: t, f: f, dir: dir, seal: seal, store: account.OpenStore(dir, seal), ref: &fakeRefresher{f: f, valid: "rt-1"}}
	e.g = e.newGuard(0)
	return e
}

// newGuard returns a guard over the session, refreshing through the fake
// monoes.me, on the fixture clock. poll 0 is the default PollInterval.
func (e *env) newGuard(poll time.Duration) *account.Guard {
	e.t.Helper()
	g := account.NewGuard(account.GuardOptions{
		Store: account.OpenStore(e.dir, e.seal), Refresher: e.ref, Now: e.f.Clock.Now, Poll: poll,
	})
	e.t.Cleanup(g.Close)
	return g
}

// signIn stores a session as a login would have: a token issued age ago for
// life, hw at its iat, and the refresh token the fake server accepts.
func (e *env) signIn(age, life time.Duration) *account.Session {
	e.t.Helper()
	now := e.f.Clock.Now()
	issued := e.f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-age), Lifetime: life})
	sess, err := account.NewSession(account.HostURL, issued, &account.User{ID: "user-1", Email: "u@example.test"}, now)
	if err != nil {
		e.t.Fatalf("signIn: %v", err)
	}
	sess.LastAttempt = now.Add(-age)
	e.save(sess)
	if err := e.store.SaveRefresh("rt-1"); err != nil {
		e.t.Fatalf("signIn: %v", err)
	}
	e.ref.set(func(r *fakeRefresher) { r.valid = "rt-1" })
	return sess
}

// save writes a session as another process would and makes sure its
// modification time differs from every earlier write.
func (e *env) save(sess *account.Session) {
	e.t.Helper()
	if err := e.store.Save(sess); err != nil {
		e.t.Fatalf("save: %v", err)
	}
	e.touch()
}

// touch moves the session file's modification time to a time no earlier write had.
func (e *env) touch() {
	e.t.Helper()
	e.bumps++
	at := time.Now().Add(time.Duration(e.bumps) * time.Second)
	if err := os.Chtimes(filepath.Join(e.dir, "session.json"), at, at); err != nil {
		e.t.Fatalf("touch: %v", err)
	}
}

func (e *env) session() *account.Session {
	e.t.Helper()
	sess, err := e.store.Load()
	if err != nil || sess == nil {
		e.t.Fatalf("session: found=%v, err=%v", sess != nil, err)
	}
	return sess
}

// eventually waits for cond, polling in real time.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// settle gives a goroutine that should NOT act a real moment to do so.
func settle() { time.Sleep(80 * time.Millisecond) }

func transient(reason account.Reason) error {
	return &account.TransientError{Reason: reason, Err: fmt.Errorf("fake network failure")}
}
```

- [ ] **Step 2: Create `internal/account/guard_test.go`.**

```go
package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

func TestStatusFollowsTheClock(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // iat -10m, exp +50m, grace until +23h50m
	start := e.f.Clock.Now()
	steps := []struct {
		at     time.Duration
		state  account.State
		reason account.Reason
	}{
		{0, account.StateOK, ""},
		{49*time.Minute + 59*time.Second, account.StateOK, ""},
		{50 * time.Minute, account.StateGrace, account.ReasonUnreachable},
		{23*time.Hour + 49*time.Minute + 59*time.Second, account.StateGrace, account.ReasonUnreachable},
		{23*time.Hour + 50*time.Minute, account.StateLocked, account.ReasonExpired},
	}
	for _, s := range steps {
		e.f.Clock.Set(start.Add(s.at))
		if st := e.g.Status(); st.State != s.state || st.Reason != s.reason {
			t.Fatalf("at +%v: Status = %s/%q, want %s/%q", s.at, st.State, st.Reason, s.state, s.reason)
		}
	}
}

func TestStatusReReadsTheFileOnlyWhenThePollIsDue(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s", st.State)
	}
	// Another process ends the login.
	e.save(&account.Session{V: 1, Host: account.HostURL, User: &account.User{ID: "user-1"}, State: "refused"})
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s before the poll is due: the cached session must answer", st.State)
	}
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.Reason != account.ReasonRefused {
		t.Fatalf("Status = %s/%q after the poll, want locked/refused", st.State, st.Reason)
	}
}

func TestRequire(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.signIn(10*time.Minute, time.Hour)
	if err := e.g.Require(ctx); err != nil {
		t.Fatalf("Require while ok: %v", err)
	}
	e.f.Clock.Advance(30 * time.Hour)
	err := e.g.Require(ctx)
	var lr *account.LoginRequiredError
	if !errors.As(err, &lr) || lr.Status.State != account.StateLocked || lr.Status.Reason != account.ReasonExpired || !account.IsLoginRequired(err) {
		t.Fatalf("Require after 30 hours = %v, want a LoginRequiredError with locked/expired", err)
	}
	account.SetEnforceFromForTest(t, time.Time{})
	if err := e.g.Require(ctx); err != nil {
		t.Fatalf("Require while dormant must allow whatever the state: %v", err)
	}
	if st := e.g.Status(); st.State != account.StateLocked || st.Enforced {
		t.Fatalf("while dormant the state is still computed: %+v", st)
	}
}

func TestOnRefusedFiresOncePerRefusal(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour)
	got := make(chan account.Status, 8)
	e.g.OnRefused(func(st account.Status) { got <- st })
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatal(st)
	}
	refuse := func() {
		e.save(&account.Session{V: 1, Host: account.HostURL, User: &account.User{ID: "user-1"}, State: "refused"})
		e.f.Clock.Advance(account.PollInterval)
		e.g.Status()
	}
	refuse()
	select {
	case st := <-got:
		if st.State != account.StateLocked || st.Reason != account.ReasonRefused {
			t.Fatalf("callback got %s/%q", st.State, st.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnRefused did not fire")
	}
	e.g.Status()
	e.g.Status()
	settle()
	if len(got) != 0 {
		t.Fatal("OnRefused fired again for the same refusal")
	}
	late := make(chan account.Status, 1)
	e.g.OnRefused(func(st account.Status) { late <- st })
	select {
	case <-late:
	case <-time.After(2 * time.Second):
		t.Fatal("a callback registered during a refusal must fire at once")
	}
	// A new sign-in ends the refusal; a second refusal is a new transition.
	e.signIn(time.Minute, time.Hour)
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("after signing in again: %s/%q", st.State, st.Reason)
	}
	refuse()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("OnRefused did not fire for the second refusal")
	}
}

func TestACorruptSessionFileIsLockedInvalidAndASignInRepairsIt(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dir, "session.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonInvalid {
		t.Fatalf("a corrupt session.json: Status = %s/%q, want locked/invalid", st.State, st.Reason)
	}
	e.signIn(10*time.Minute, time.Hour)
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("after a sign-in over the corrupt file: %s/%q", st.State, st.Reason)
	}
	// A file that later cannot be read must not downgrade a session that works.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.touch()
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("a corrupt file replaced a working session: %s/%q", st.State, st.Reason)
	}
}

// A logout removes session.json and refresh.enc under the lock. A guard that
// had the session cached sees "no session" at its next poll, and keeps working.
func TestAGuardNoticesASessionThatWasRemoved(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s", st.State)
	}
	if err := os.Remove(filepath.Join(e.dir, "session.json")); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteRefresh(); err != nil {
		t.Fatal(err)
	}
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
		t.Fatalf("after the logout: Status = %s/%q, want locked/not_logged_in", st.State, st.Reason)
	}
	e.signIn(time.Minute, time.Hour) // a later sign-in is picked up
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("after signing in again: %s/%q", st.State, st.Reason)
	}
}

func TestAGuardWithNothingStoredIsNotLoggedIn(t *testing.T) {
	f := accounttest.New(t)
	g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(filepath.Join(t.TempDir(), "account"), account.NewMemorySealer()), Now: f.Clock.Now})
	t.Cleanup(g.Close)
	st := g.Status()
	if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced || st.Allowed() {
		t.Fatalf("Status = %+v", st)
	}
}
```

- [ ] **Step 3: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors:

```text
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/guard_helpers_test.go:42:85: undefined: account.TokenSet
internal/account/guard_helpers_test.go:60:19: undefined: account.TokenSet
internal/account/guard_helpers_test.go:70:18: undefined: account.TokenSet
internal/account/guard_helpers_test.go:83:17: undefined: account.Guard
...
```

- [ ] **Step 4: Create `internal/account/guard.go`.**

```go
package account

import (
	"context"
	"sync"
	"time"
)

// TokenSet is what a refresh-token grant returns.
type TokenSet struct{ AccessToken, RefreshToken string }

// Refresher performs the OAuth refresh-token grant with resource=Audience. It
// returns *RefusedError only when monoes.me answered invalid_grant (spec D27)
// and *TransientError for every other failure. B1b implements it; a Guard
// without one never refreshes.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*TokenSet, error)
}

// GuardOptions configures NewGuard.
type GuardOptions struct {
	Store     Store            // default: the session in DefaultDir
	Refresher Refresher        // nil: never refreshes
	Now       func() time.Time // default time.Now
	Poll      time.Duration    // default PollInterval
}

// The timings the guard owns besides the contract constants in claims.go.
const (
	hwInterval         = time.Minute                        // hw is written at most this often
	hwLockWait         = 2 * time.Second                    // a high-water write never waits longer for the lock
	refreshCallTimeout = 20 * time.Second                   // backstop on one Refresher call
	lockWaitTimeout    = refreshCallTimeout + 5*time.Second // a waiter outlasts the holder's refresh
	backoffMin         = 30 * time.Second                   // the refresher's first retry
	backoffMax         = 5 * time.Minute                    // and its ceiling
)

// Guard turns the stored session into a cached verdict. NewGuard does no I/O;
// the session is read on the first Status and again whenever session.json's
// modification time changes (checked at most once per Poll), so Status and
// Require stay cheap and make no network call.
type Guard struct {
	store     Store
	refresher Refresher
	now       func() time.Time
	poll      time.Duration

	sem      chan struct{} // one refresh at a time in this process
	reloadMu sync.Mutex    // one reload at a time

	mu            sync.Mutex // guards everything below
	loaded        bool       // the first read has happened
	sess          *Session   // the cached session; never modified, only replaced
	rcpt          *Receipt   // verifyToken of sess's token, so Status checks no signature
	verr          *VerifyError
	loadErr       error // the last failed read, reported as locked(invalid) while nothing is cached
	mtime         time.Time
	lastPoll      time.Time
	lastHWAttempt time.Time
	refusedNoted  bool // OnRefused has fired for the current refusal
	onRefused     []func(Status)
	closed        bool
	loopCancel    context.CancelFunc
	loopDone      chan struct{}
}

// NewGuard returns a guard over o.Store. It reads nothing and writes nothing.
func NewGuard(o GuardOptions) *Guard {
	g := &Guard{store: o.Store, refresher: o.Refresher, now: o.Now, poll: o.Poll, sem: make(chan struct{}, 1)}
	if g.store == nil {
		g.store = OpenStore("", nil)
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.poll <= 0 {
		g.poll = PollInterval
	}
	return g
}

// Status is the current verdict, recomputed from the cached session and the
// clock on every call.
func (g *Guard) Status() Status {
	now := g.now()
	g.pollIfDue(now)
	g.mu.Lock()
	sess, rcpt, verr, loadErr := g.sess, g.rcpt, g.verr, g.loadErr
	g.mu.Unlock()
	st := judge(sess, rcpt, verr, now)
	if sess == nil && loadErr != nil {
		st.Reason = ReasonInvalid // a session.json that cannot be read is not "not logged in"
	}
	g.note(st)
	return st
}

// Require returns nil when Status().Allowed(), else a *LoginRequiredError.
func (g *Guard) Require(ctx context.Context) error {
	if st := g.Status(); !st.Allowed() {
		return &LoginRequiredError{Status: st}
	}
	return nil
}

// OnRefused registers fn to be called once each time the verdict becomes
// locked(refused), whichever way the guard learns it: its own refresh
// (EnsureFresh, Refresh or the refresher) or a re-read of a session.json that
// another process marked refused (noticed by Status or by the refresher).
// Any number of callbacks may be registered and every one fires. A refusal
// already in effect calls fn at once. Each call runs on its own goroutine,
// after the guard's locks are released, so a slow callback blocks nothing and
// may take locks the caller of Status holds.
func (g *Guard) OnRefused(fn func(Status)) {
	g.mu.Lock()
	g.onRefused = append(g.onRefused, fn)
	already := g.refusedNoted
	g.mu.Unlock()
	if already {
		go fn(g.Status())
		return
	}
	g.Status() // a refusal in the stored session fires every callback, this one included
}

// note fires the OnRefused callbacks on the transition into locked(refused).
func (g *Guard) note(st Status) {
	refused := st.State == StateLocked && st.Reason == ReasonRefused
	g.mu.Lock()
	var fns []func(Status)
	if refused && !g.refusedNoted {
		fns = append(fns, g.onRefused...)
	}
	g.refusedNoted = refused
	g.mu.Unlock()
	for _, fn := range fns {
		go fn(st)
	}
}

// pollIfDue re-reads session.json when its modification time changed. The
// first call always reads; later calls look at the file at most once per poll.
func (g *Guard) pollIfDue(now time.Time) {
	g.mu.Lock()
	first := !g.loaded
	due := first || elapsed(now, g.lastPoll, g.poll)
	if due {
		g.lastPoll = now
	}
	g.mu.Unlock()
	if due {
		g.reload(first)
	}
}

func (g *Guard) reload(force bool) {
	g.reloadMu.Lock()
	defer g.reloadMu.Unlock()
	mt, err := g.store.Mtime()
	g.mu.Lock()
	unchanged := g.loaded && err == nil && mt.Equal(g.mtime)
	g.mu.Unlock()
	if unchanged && !force {
		return
	}
	sess, lerr := g.store.Load()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loaded = true
	if lerr != nil {
		// A read that fails now must not downgrade a session that worked: keep
		// what is cached and retry at the next poll. With nothing cached, loadErr
		// makes the verdict locked(invalid).
		g.loadErr = lerr
		return
	}
	g.loadErr = nil
	g.mtime = mt
	g.setSessionLocked(sess)
}

// adopt makes sess, which the caller has just read or written under the file
// lock, the cached session.
func (g *Guard) adopt(sess *Session) {
	mt, _ := g.store.Mtime()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loaded, g.loadErr, g.mtime = true, nil, mt
	g.setSessionLocked(sess)
}

func (g *Guard) setSessionLocked(sess *Session) {
	g.sess, g.rcpt, g.verr = sess, nil, nil
	if sess != nil && sess.State != stateRefused && sess.AccessToken != "" {
		g.rcpt, g.verr = verifyToken(sess.AccessToken)
	}
}

func (g *Guard) cached() (*Session, *Receipt) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sess, g.rcpt
}

// Close stops the refresher, if one runs, and waits for it. It is safe to call
// twice, and a closed guard still answers Status and Require.
func (g *Guard) Close() {
	g.mu.Lock()
	g.closed = true
	cancel, done := g.loopCancel, g.loopDone
	g.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
```

- [ ] **Step 5: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 6: Commit.**

```bash
git add internal/account/guard.go internal/account/guard_helpers_test.go internal/account/guard_test.go
```

```bash
git commit -m "feat(account): the guard with its cached verdict and OnRefused" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The refresh algorithm

Spec §4.4 as code: decide from the cache without touching the disk; only when a refresh is due take the cross-process lock, read the session again, decide again, and call the `Refresher`. Outcomes: success stores the pair (refresh token first, then the session); `invalid_grant` marks the session refused and deletes `refresh.enc`; everything else records the attempt and keeps the grace (D27).

**Files:**
- Create: `internal/account/guard_refresh.go`
- Test: `internal/account/guard_refresh_test.go`

**Interfaces:**
- Consumes: everything from Task 6 (`Guard`, `adopt`, `cached`, `sem`, the timing constants, the test helpers), `describe` and `brokenSealer` from `store_test.go` (Task 5), `Store` (`Lock`, `Load`, `LoadRefresh`, `SaveRefresh`, `Save`, `DeleteRefresh`), `NewSession`, `elapsed`, `judge` (Task 3), `RefusedError`, `TransientError` (Task 1), `VerifyError` (Task 2).
- Produces:
  - `func (g *Guard) EnsureFresh(ctx context.Context) (Status, error)`: a no-op while dormant (no implicit call to monoes.me, spec D22); otherwise refreshes when due and keeps `hw` current.
  - `func (g *Guard) Refresh(ctx context.Context) (Status, error)`: the same, also while dormant (`account status`).
  - Both return a `Status` that is always usable; the error is advisory (the lock could not be taken in time, the context ended, a write failed) and a caller must not fail a command because of it.
  - Unexported: `refreshMode` (`modeCLI`, `modeBackground`), `outcome` (`outcomeSkipped`, `outcomeRefreshed`, `outcomeFailed`, `outcomeRefused`), `dueForRefresh(sess *Session, st Status, rcpt *Receipt, now time.Time, mode refreshMode) bool`, `(*Guard).refreshIfDue(ctx, mode) (Status, outcome, error)`, `refreshUnderLock`, `applyTokens`, `applyRefusal`, `recordAttempt`, `bumpHW(s *Session, now time.Time)`, `touchHW(now time.Time)`.
  - An ended context starts no refresh and records nothing (a loop that was cancelled once ran a last pass with a dead context; `TestACallerThatHasGivenUpStartsNoRefresh` and `TestCloseStopsARefresherThatIsMidCallAndRecordsNothing` pin it).
  - The refresh token is written before the session (`TestTheRefreshTokenIsWrittenBeforeTheSession`: the order of the two writes, and a session write that fails still leaves the new refresh token on disk and usable by another process). If the rotated refresh token cannot be written, the dead one is removed with `DeleteRefresh` (best effort; `os.Remove` needs no key store), the attempt is recorded as `keyring_unavailable`, the error is returned, and no later attempt presents the dead token (`TestAFailedWriteOfTheRotatedRefreshTokenRemovesTheDeadOne`): monoes.me treats a rotated-away token that is presented again as theft and revokes every refresh token of the account.
  - After a refusal `LastResult` is `refused`, `State` is `refused`, `AccessToken` is cleared and the user is kept; `refresh.enc` is gone. After a refreshed token with a `kid` this build does not pin, `LastResult` is `key_unknown` and the old access token stays.
- Not stored, on purpose: a refreshed access token that does not verify. The rotated refresh token that came with it is stored, because the old one is dead. A crash between the server's rotation and the write is the one window left: the dead token stays on disk, and the next process that presents it triggers the account-wide revocation. The spec accepts that window (§4.4); closing it would need a write-ahead step that this plan does not take.

- [ ] **Step 1: Create `internal/account/guard_refresh_test.go`.** It includes the Review Focus tests 1 (concurrency), 2 (the rolled-back clock) and 3 (a token that does not verify).

```go
package account_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

func TestEnsureFreshRefreshesUnderTheMargin(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // four minutes left
	now := e.f.Clock.Now()
	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK || !st.ValidUntil.Equal(now.Add(time.Hour)) {
		t.Fatalf("EnsureFresh = %s valid until %v, %v; want ok with a fresh hour", st.State, st.ValidUntil, err)
	}
	if e.ref.calls.Load() != 1 {
		t.Fatalf("%d network refreshes, want 1", e.ref.calls.Load())
	}
	sess := e.session()
	if sess.LastResult != "ok" || !sess.LastAttempt.Equal(now) || !sess.HW.Equal(now) || sess.State != "" || sess.User == nil || sess.User.ID != "user-1" {
		t.Fatalf("stored session = %s; want ok, attempt now, hw reset to the new iat, user kept", describe(sess))
	}
	if rt, err := e.store.LoadRefresh(); err != nil || rt != "rt-2" {
		t.Fatalf("the rotated refresh token was not stored (err %v)", err)
	}
}

func TestEnsureFreshDoesNothingWhileTheTokenIsHealthy(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour)
	if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateOK || e.ref.calls.Load() != 0 {
		t.Fatalf("EnsureFresh = %s, %v with %d refreshes, want ok and none", st.State, err, e.ref.calls.Load())
	}
}

func TestTheNegativeCacheLimitsAnOfflineCallToOneAttemptAMinute(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // expired an hour ago
	e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) })
	ctx := context.Background()
	attempt := func() account.Status {
		st, err := e.g.EnsureFresh(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := attempt(); st.State != account.StateGrace || st.Reason != account.ReasonUnreachable {
		t.Fatalf("Status = %s/%q", st.State, st.Reason)
	}
	if sess := e.session(); sess.LastResult != "unreachable" || !sess.LastAttempt.Equal(e.f.Clock.Now()) {
		t.Fatalf("the attempt was not recorded: %s", describe(sess))
	}
	attempt()
	e.f.Clock.Advance(59 * time.Second)
	attempt()
	if e.ref.calls.Load() != 1 {
		t.Fatalf("%d attempts inside the first minute, want 1", e.ref.calls.Load())
	}
	e.f.Clock.Advance(2 * time.Second)
	attempt()
	if e.ref.calls.Load() != 2 {
		t.Fatalf("%d attempts after a minute, want 2", e.ref.calls.Load())
	}
	// Back online: the next attempt recovers.
	e.ref.set(func(r *fakeRefresher) { r.err = nil })
	e.f.Clock.Advance(time.Minute)
	if st := attempt(); st.State != account.StateOK {
		t.Fatalf("after the network came back: %s/%q", st.State, st.Reason)
	}
}

func TestEveryFailureThatIsNotInvalidGrantKeepsTheGrace(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeRefresher)
		want  account.Reason
	}{
		{"no network", func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) }, account.ReasonUnreachable},
		{"a 5xx answer", func(r *fakeRefresher) { r.err = transient(account.ReasonServerError) }, account.ReasonServerError},
		{"an error of no known type", func(r *fakeRefresher) { r.err = errors.New("boom") }, account.ReasonUnreachable},
		{"a transient error with an odd reason", func(r *fakeRefresher) { r.err = transient(account.ReasonInvalid) }, account.ReasonUnreachable},
		{"a success with no tokens in it", func(r *fakeRefresher) { r.empty = true }, account.ReasonServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour)
			e.ref.set(c.setup)
			st, err := e.g.EnsureFresh(context.Background())
			if err != nil || st.State != account.StateGrace || st.Reason != c.want {
				t.Fatalf("Status = %s/%q, %v, want grace/%q", st.State, st.Reason, err, c.want)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatal("a failed attempt changed the refresh token")
			}
			if e.session().State != "" {
				t.Fatal("a failed attempt marked the session")
			}
		})
	}
}

func TestInvalidGrantLocksDeletesTheRefreshTokenAndCancelsWork(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	e.ref.set(func(r *fakeRefresher) { r.err = &account.RefusedError{Description: "revoked"} })
	got := make(chan account.Status, 4)
	e.g.OnRefused(func(st account.Status) { got <- st })

	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused || st.Allowed() {
		t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused", st.State, st.Reason, err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "refresh.enc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refresh.enc survived a refusal (stat err %v)", err)
	}
	sess := e.session()
	if sess.State != "refused" || sess.Reason != "revoked" || sess.AccessToken != "" || sess.User == nil || sess.User.ID != "user-1" || sess.LastResult != "refused" {
		t.Fatalf("stored session = %s; want refused, the reason, no access token, the user kept", describe(sess))
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("OnRefused did not fire")
	}
	settle()
	if len(got) != 0 {
		t.Fatal("OnRefused fired more than once")
	}
	e.f.Clock.Advance(time.Hour)
	if st, _ := e.g.EnsureFresh(context.Background()); st.Reason != account.ReasonRefused || e.ref.calls.Load() != 1 {
		t.Fatalf("a refused session must not try again: %s/%q after %d calls", st.State, st.Reason, e.ref.calls.Load())
	}
	// Only a new sign-in unlocks it.
	e.signIn(time.Minute, time.Hour)
	e.f.Clock.Advance(account.PollInterval)
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("after signing in again: %s/%q", st.State, st.Reason)
	}
}

func TestAKeyStoreThatCannotBeOpenedKeepsTheGraceAndNeverCallsTheServer(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	broken := account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, brokenSealer{}), Refresher: e.ref, Now: e.f.Clock.Now})
	t.Cleanup(broken.Close)
	st, err := broken.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonKeyringUnavailable || e.ref.calls.Load() != 0 {
		t.Fatalf("Status = %s/%q, %v with %d calls, want grace/keyring_unavailable and none", st.State, st.Reason, err, e.ref.calls.Load())
	}
	if e.session().LastResult != "keyring_unavailable" {
		t.Fatalf("LastResult = %q", e.session().LastResult)
	}

	gone := newEnv(t)
	gone.signIn(2*time.Hour, time.Hour)
	if err := os.Remove(filepath.Join(gone.dir, "refresh.enc")); err != nil {
		t.Fatal(err)
	}
	if st, _ := gone.g.EnsureFresh(context.Background()); st.State != account.StateGrace || st.Reason != account.ReasonKeyringUnavailable || gone.ref.calls.Load() != 0 {
		t.Fatalf("a missing refresh.enc: %s/%q with %d calls", st.State, st.Reason, gone.ref.calls.Load())
	}
}

func TestATokenThatDoesNotVerifyIsNeverStoredButTheRotatedRefreshTokenIs(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(*fakeRefresher)
		wantLast string
	}{
		{"an opaque token", func(r *fakeRefresher) { r.badAccess = "opaque-0123456789" }, "server_error"},
		{"a wrong audience", func(r *fakeRefresher) {
			r.badAccess = r.f.Token(accounttest.TokenOptions{Audience: []string{"https://elsewhere.example"}})
		}, "server_error"},
		{"a key this build does not pin", func(r *fakeRefresher) { r.kid = "rotated-key" }, "key_unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			before := e.signIn(2*time.Hour, time.Hour)
			e.ref.set(c.setup)
			st, err := e.g.EnsureFresh(context.Background())
			if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
				t.Fatalf("Status = %s/%q, %v, want grace/server_error: a bad deploy must not lock anyone out", st.State, st.Reason, err)
			}
			sess := e.session()
			if sess.AccessToken != before.AccessToken || sess.LastResult != c.wantLast {
				t.Fatalf("stored session changed: token kept=%v last=%q, want the old token and %q", sess.AccessToken == before.AccessToken, sess.LastResult, c.wantLast)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
				t.Fatal("the server rotated the refresh token, so the new one must be kept")
			}
			if c.wantLast == "key_unknown" {
				// The session's iat is two hours back, so its grace ends 22 hours from now.
				// Until then the reason is server_error; after it, key_unknown (run update), not expired.
				start := e.f.Clock.Now()
				e.f.Clock.Set(start.Add(22*time.Hour - time.Second))
				if st := e.g.Status(); st.State != account.StateGrace || st.Reason != account.ReasonServerError {
					t.Fatalf("a second before the grace ends: %s/%q", st.State, st.Reason)
				}
				e.f.Clock.Set(start.Add(22 * time.Hour))
				if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonKeyUnknown {
					t.Fatalf("after the grace the verdict must say key_unknown (run update), got %s/%q", st.State, st.Reason)
				}
			}
		})
	}
}

func TestARolledBackClockIsRepairedByTheRefreshThatResetsHW(t *testing.T) {
	e := newEnv(t)
	sess := e.signIn(10*time.Minute, time.Hour)
	now := e.f.Clock.Now()
	// The clock was set back: hw and the last attempt lie in the future.
	sess.HW, sess.LastAttempt = now.Add(2*time.Hour), now.Add(2*time.Hour)
	e.save(sess)
	if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonClockRollback {
		t.Fatalf("Status = %s/%q, want locked/clock_rollback", st.State, st.Reason)
	}
	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d calls: a stored time in the future must not hold the repair off", st.State, st.Reason, err, e.ref.calls.Load())
	}
	if hw := e.session().HW; !hw.Equal(now) {
		t.Fatalf("hw = %v, want the new token's iat %v", hw, now)
	}
}

func TestAnExpiredLoginIsRefreshedWhenMonoesMeIsBack(t *testing.T) {
	e := newEnv(t)
	e.signIn(30*time.Hour, time.Hour)
	if st := e.g.Status(); st.Reason != account.ReasonExpired {
		t.Fatalf("Status = %s/%q", st.State, st.Reason)
	}
	if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateOK {
		t.Fatalf("EnsureFresh = %s/%q, %v, want ok: the grace ended but the refresh token still works", st.State, st.Reason, err)
	}
}

func TestDormantEnsureFreshIsANoOpAndRefreshStillWorks(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	account.SetEnforceFromForTest(t, time.Time{})
	if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateGrace || e.ref.calls.Load() != 0 {
		t.Fatalf("EnsureFresh while dormant = %s, %v with %d calls, want no call", st.State, err, e.ref.calls.Load())
	}
	if st, err := e.g.Refresh(context.Background()); err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
		t.Fatalf("Refresh while dormant = %s, %v with %d calls, want one call and ok", st.State, err, e.ref.calls.Load())
	}
}

func TestAGuardWithoutARefresherNeverRefreshes(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, e.seal), Now: e.f.Clock.Now})
	t.Cleanup(g.Close)
	if st, err := g.EnsureFresh(context.Background()); err != nil || st.State != account.StateGrace {
		t.Fatalf("EnsureFresh = %s, %v", st.State, err)
	}
}

func TestSeveralProcessesRefreshingAtOnceMakeOneNetworkCall(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // due for everyone
	e.ref.set(func(r *fakeRefresher) { r.delay = 40 * time.Millisecond })
	var guards []*account.Guard
	for i := 0; i < 5; i++ {
		guards = append(guards, e.newGuard(0)) // five "processes", each with its own store handle
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 15)
	for _, g := range guards {
		for j := 0; j < 3; j++ { // and three goroutines in each
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := g.EnsureFresh(context.Background()); err != nil {
					errs <- err
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("EnsureFresh: %v", err)
	}
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d network refreshes, want exactly 1 (a second one is refused: the token was rotated away)", n)
	}
	for i, g := range guards {
		if st := g.Status(); st.State != account.StateOK {
			t.Errorf("guard %d: %s/%q, want ok", i, st.State, st.Reason)
		}
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
		t.Fatal("the stored refresh token is not the rotated one")
	}
}

func TestALockThatCannotBeTakenInTimeIsAnAdvisoryError(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour)
	unlock, err := account.OpenStore(e.dir, e.seal).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	st, err := e.g.EnsureFresh(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if st.State != account.StateOK || e.ref.calls.Load() != 0 {
		t.Fatalf("the returned Status must stay usable: %s with %d calls", st.State, e.ref.calls.Load())
	}
}

func TestTheHighWaterMarkIsWrittenAtMostOnceAMinuteAndOnlyWithASession(t *testing.T) {
	ctx := context.Background()
	mtime := func(e *env) time.Time {
		fi, err := os.Stat(filepath.Join(e.dir, "session.json"))
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime()
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	pin := func(e *env) {
		if err := os.Chtimes(filepath.Join(e.dir, "session.json"), old, old); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("no session, nothing written", func(t *testing.T) {
		e := newEnv(t)
		if _, err := e.g.EnsureFresh(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(e.dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("EnsureFresh created %s with no session (stat err %v)", e.dir, err)
		}
	})
	t.Run("a stale mark is written, then not again within the minute", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(10*time.Minute, time.Hour) // hw = iat, ten minutes old
		pin(e)
		now := e.f.Clock.Now()
		if _, err := e.g.EnsureFresh(ctx); err != nil {
			t.Fatal(err)
		}
		if hw := e.session().HW; !hw.Equal(now) {
			t.Fatalf("hw = %v, want now %v", hw, now)
		}
		pin(e)
		e.f.Clock.Advance(59 * time.Second)
		e.g.EnsureFresh(ctx)
		if !mtime(e).Equal(old) {
			t.Fatal("hw was written again inside the minute")
		}
		e.f.Clock.Advance(2 * time.Second)
		e.g.EnsureFresh(ctx)
		if mtime(e).Equal(old) || !e.session().HW.Equal(e.f.Clock.Now()) {
			t.Fatalf("hw was not written after the minute: %v", e.session().HW)
		}
	})
	t.Run("a mark ahead of the clock is never lowered", func(t *testing.T) {
		e := newEnv(t)
		sess := e.signIn(10*time.Minute, time.Hour)
		ahead := e.f.Clock.Now().Add(time.Minute) // inside the 5 minute allowance, so still ok
		sess.HW = ahead
		e.save(sess)
		pin(e)
		e.g.EnsureFresh(ctx)
		if !mtime(e).Equal(old) || !e.session().HW.Equal(ahead) {
			t.Fatal("hw ahead of now was rewritten")
		}
	})
	t.Run("dormant writes nothing", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(10*time.Minute, time.Hour)
		pin(e)
		account.SetEnforceFromForTest(t, time.Time{})
		e.g.EnsureFresh(ctx)
		if !mtime(e).Equal(old) {
			t.Fatal("EnsureFresh wrote while dormant")
		}
	})
}

func TestACallerThatHasGivenUpStartsNoRefresh(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 40; i++ { // a select between "free" and "ended" picks at random: ask often
		st, err := e.g.EnsureFresh(ctx)
		if !errors.Is(err, context.Canceled) || e.ref.calls.Load() != 0 {
			t.Fatalf("EnsureFresh with an ended context: %v with %d calls, want context.Canceled and none", err, e.ref.calls.Load())
		}
		if st.State != account.StateGrace || e.session().LastResult != "ok" {
			t.Fatalf("an ended context must leave the session as it was: %s, last result %q", st.State, e.session().LastResult)
		}
	}
}

func TestRefreshFiresEveryOnRefusedCallbackEvenWhileDormant(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	e.ref.set(func(r *fakeRefresher) { r.err = &account.RefusedError{Description: "revoked"} })
	first, second := make(chan account.Status, 2), make(chan account.Status, 2)
	e.g.OnRefused(func(st account.Status) { first <- st })
	e.g.OnRefused(func(st account.Status) { second <- st }) // callbacks append: none replaces another
	account.SetEnforceFromForTest(t, time.Time{})
	st, err := e.g.Refresh(context.Background()) // the explicit refresh of `account status`, also while dormant
	if err != nil || st.Reason != account.ReasonRefused {
		t.Fatalf("Refresh = %s/%q, %v, want locked/refused", st.State, st.Reason, err)
	}
	for i, ch := range []chan account.Status{first, second} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("callback %d did not fire", i+1)
		}
	}
	settle()
	if len(first)+len(second) != 0 {
		t.Fatal("a callback fired more than once for one refusal")
	}
}

func TestEnsureFreshWithNoSessionLeftHasNothingToDo(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	if st := e.g.Status(); st.State != account.StateGrace { // the session is cached
		t.Fatalf("Status = %s", st.State)
	}
	if err := os.Remove(filepath.Join(e.dir, "session.json")); err != nil { // a logout
		t.Fatal(err)
	}
	if err := e.store.DeleteRefresh(); err != nil {
		t.Fatal(err)
	}
	e.f.Clock.Advance(account.PollInterval)
	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.Reason != account.ReasonNotLoggedIn || e.ref.calls.Load() != 0 {
		t.Fatalf("EnsureFresh with no session = %s/%q, %v with %d calls, want locked/not_logged_in and no call", st.State, st.Reason, err, e.ref.calls.Load())
	}
}

// failingStore wraps a real store, records the writes a refresh makes, and
// fails the ones a test names.
type failingStore struct {
	account.Store
	mu              sync.Mutex
	writes          []string // "SaveRefresh", "Save", "DeleteRefresh", in call order
	failSaveRefresh bool
	failSave        bool
}

func (s *failingStore) note(name string) {
	s.mu.Lock()
	s.writes = append(s.writes, name)
	s.mu.Unlock()
}

func (s *failingStore) order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.writes...)
}

func (s *failingStore) SaveRefresh(token string) error {
	s.note("SaveRefresh")
	if s.failSaveRefresh {
		return fmt.Errorf("%w: simulated", account.ErrKeyringUnavailable)
	}
	return s.Store.SaveRefresh(token)
}

func (s *failingStore) Save(sess *account.Session) error {
	s.note("Save")
	if s.failSave {
		return errors.New("simulated: disk full")
	}
	return s.Store.Save(sess)
}

func (s *failingStore) DeleteRefresh() error {
	s.note("DeleteRefresh")
	return s.Store.DeleteRefresh()
}

// monoes.me treats a rotated-away refresh token that is presented again as
// theft and revokes every refresh token of the account. So when the new one
// cannot be written, the dead one on disk must go, and no later attempt may
// present it.
func TestAFailedWriteOfTheRotatedRefreshTokenRemovesTheDeadOne(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due; refresh.enc holds the token the server will rotate
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSaveRefresh: true}
	g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
	t.Cleanup(g.Close)

	st, err := g.EnsureFresh(ctx)
	if !errors.Is(err, account.ErrKeyringUnavailable) || e.ref.calls.Load() != 1 {
		t.Fatalf("EnsureFresh = %v with %d calls, want the write error and one call", err, e.ref.calls.Load())
	}
	if _, err := os.Stat(filepath.Join(e.dir, "refresh.enc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the dead refresh token is still on disk (stat err %v)", err)
	}
	if st.State != account.StateGrace || st.Reason != account.ReasonKeyringUnavailable {
		t.Fatalf("Status = %s/%q, want grace/keyring_unavailable", st.State, st.Reason)
	}
	other := e.newGuard(0) // and so does every other process
	for i := 0; i < 3; i++ {
		e.f.Clock.Advance(2 * time.Minute)
		for _, guard := range []*account.Guard{g, other} {
			if st, _ := guard.EnsureFresh(ctx); st.State != account.StateGrace || st.Reason == account.ReasonRefused {
				t.Fatalf("Status = %s/%q, want grace and never refused", st.State, st.Reason)
			}
		}
	}
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d calls: the dead refresh token was presented to the server again", n)
	}
}

func TestTheRefreshTokenIsWrittenBeforeTheSession(t *testing.T) {
	ctx := context.Background()
	t.Run("the order of the two writes", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(56*time.Minute, time.Hour)
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal)}
		g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
		t.Cleanup(g.Close)
		if _, err := g.EnsureFresh(ctx); err != nil {
			t.Fatal(err)
		}
		if got := fs.order(); !reflect.DeepEqual(got, []string{"SaveRefresh", "Save"}) {
			t.Fatalf("writes = %v, want the refresh token before the session", got)
		}
	})
	t.Run("a session that cannot be written still leaves the new refresh token usable", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true}
		g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
		t.Cleanup(g.Close)
		if _, err := g.EnsureFresh(ctx); err == nil {
			t.Fatal("the failed session write must be reported")
		}
		other := e.newGuard(0) // another process: it still sees the old session, and the new refresh token
		st, err := other.EnsureFresh(ctx)
		if err != nil || st.State != account.StateOK || e.ref.calls.Load() != 2 {
			t.Fatalf("the other process: %s/%q, %v with %d calls, want ok after a second refresh with the new token", st.State, st.Reason, err, e.ref.calls.Load())
		}
	})
}
```

- [ ] **Step 2: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors (`EnsureFresh`, `Refresh` are not defined):

```text
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/guard_refresh_test.go:22:17: e.g.EnsureFresh undefined (type *account.Guard has no field or method EnsureFresh)
internal/account/guard_refresh_test.go:41:20: e.g.EnsureFresh undefined (type *account.Guard has no field or method EnsureFresh)
internal/account/guard_refresh_test.go:52:18: e.g.EnsureFresh undefined (type *account.Guard has no field or method EnsureFresh)
internal/account/guard_refresh_test.go:100:19: e.g.EnsureFresh undefined (type *account.Guard has no field or method EnsureFresh)
...
```

- [ ] **Step 3: Create `internal/account/guard_refresh.go`.**

```go
package account

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// refreshMode says when a session is due for a refresh.
type refreshMode int

const (
	// modeCLI: under RefreshMargin left, or not ok at all, and not tried in the
	// last NegativeCache. What EnsureFresh and Refresh use, so an offline CLI
	// call pays the connect timeout at most once a minute.
	modeCLI refreshMode = iota
	// modeBackground: at half the token's lifetime, or not ok at all. The
	// refresher loop keeps its own backoff, so the stored attempt time does not
	// hold it off.
	modeBackground
)

// outcome is what one pass of refreshIfDue did.
type outcome int

const (
	outcomeSkipped   outcome = iota // nothing was due, or nothing could be tried
	outcomeRefreshed                // a new token was verified and stored
	outcomeFailed                   // an attempt failed in a way that keeps the grace
	outcomeRefused                  // monoes.me refused the refresh token
)

// EnsureFresh refreshes the session when it is due, and does nothing while the
// package is dormant (spec D22: no implicit call to monoes.me). It also keeps
// the high-water mark current. The returned Status is always usable; the error
// is advisory (the lock could not be taken in time, the context ended, a write
// failed) and a caller must not fail a command because of it.
func (g *Guard) EnsureFresh(ctx context.Context) (Status, error) {
	if dormant() {
		return g.Status(), nil
	}
	st, _, err := g.refreshIfDue(ctx, modeCLI)
	return st, err
}

// Refresh is EnsureFresh that also runs while dormant: an explicit `account
// status` refreshes when due even before the enforcement date.
func (g *Guard) Refresh(ctx context.Context) (Status, error) {
	st, _, err := g.refreshIfDue(ctx, modeCLI)
	return st, err
}

// dueForRefresh decides from the cached session whether a refresh should be
// tried now. A session that is not ok (grace, expired, a clock that went back,
// a token this build cannot verify) is always due: only a new token repairs it.
func dueForRefresh(sess *Session, st Status, rcpt *Receipt, now time.Time, mode refreshMode) bool {
	if sess == nil || sess.State == stateRefused || sess.AccessToken == "" {
		return false
	}
	switch {
	case st.State != StateOK:
	case mode == modeCLI && rcpt.ExpiresAt.Sub(now) < RefreshMargin:
	case mode == modeBackground && !now.Before(rcpt.IssuedAt.Add(rcpt.ExpiresAt.Sub(rcpt.IssuedAt)/2)):
	default:
		return false
	}
	return mode == modeBackground || elapsed(now, sess.LastAttempt, NegativeCache)
}

// refreshIfDue is one pass of the refresh algorithm (spec §4.4): decide from the
// cache without touching the disk, and only when a refresh is due take the
// cross-process lock and read the session again.
func (g *Guard) refreshIfDue(ctx context.Context, mode refreshMode) (Status, outcome, error) {
	if err := ctx.Err(); err != nil {
		return g.Status(), outcomeSkipped, err // a caller that has given up starts nothing
	}
	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-ctx.Done():
		return g.Status(), outcomeSkipped, ctx.Err()
	}
	st := g.Status()
	now := g.now()
	sess, rcpt := g.cached()
	if g.refresher == nil || !dueForRefresh(sess, st, rcpt, now, mode) {
		g.touchHW(now)
		return g.Status(), outcomeSkipped, nil
	}
	return g.refreshUnderLock(ctx, mode)
}

func (g *Guard) refreshUnderLock(ctx context.Context, mode refreshMode) (Status, outcome, error) {
	lctx, cancel := context.WithTimeout(ctx, lockWaitTimeout)
	unlock, err := g.store.Lock(lctx)
	cancel()
	if err != nil {
		return g.Status(), outcomeSkipped, fmt.Errorf("account: taking the session lock: %w", err)
	}
	defer unlock()

	// Another process may have refreshed, signed in or been refused while this
	// one waited: read the session again and decide again.
	sess, err := g.store.Load()
	if err != nil {
		return g.Status(), outcomeSkipped, err
	}
	g.adopt(sess)
	st := g.Status()
	now := g.now()
	sess, rcpt := g.cached()
	if !dueForRefresh(sess, st, rcpt, now, mode) {
		return st, outcomeSkipped, nil
	}

	if err := ctx.Err(); err != nil {
		return st, outcomeSkipped, err // the context ended while this waited for the lock
	}
	refreshToken, err := g.store.LoadRefresh()
	if err != nil || refreshToken == "" {
		// Unreadable: the key store is unavailable, or the file is gone or
		// does not open. Not a decision about the account, so the grace applies.
		return g.recordAttempt(sess, now, string(ReasonKeyringUnavailable))
	}
	cctx, cancel := context.WithTimeout(ctx, refreshCallTimeout)
	ts, err := g.refresher.Refresh(cctx, refreshToken)
	cancel()

	var refused *RefusedError
	var transient *TransientError
	result := ReasonUnreachable // D27: every failure that is not invalid_grant is "unreachable" unless it says server_error
	switch {
	case errors.As(err, &refused):
		return g.applyRefusal(sess, now, refused)
	case err != nil && ctx.Err() != nil:
		return g.Status(), outcomeSkipped, ctx.Err() // the caller gave up; that says nothing about the account
	case err == nil && ts != nil && ts.AccessToken != "":
		return g.applyTokens(sess, now, refreshToken, ts)
	case err == nil:
		result = ReasonServerError // an answer with nothing in it is the server's fault
	case errors.As(err, &transient) && transient.Reason == ReasonServerError:
		result = ReasonServerError
	}
	return g.recordAttempt(sess, now, string(result))
}

// applyTokens stores a successful refresh. The server rotates the refresh
// token on use, so the old one is dead, and presenting a rotated-away token
// again is taken for theft: monoes.me then revokes every refresh token of the
// account, which locks every install of it. Hence the order and the cleanup.
// The new refresh token is saved before the session, even when the access token
// that came with it is not usable, so a crash in between leaves a working pair.
// If it cannot be saved, the dead one is removed from disk (os.Remove needs no
// key store) so that no later process presents it.
func (g *Guard) applyTokens(cur *Session, now time.Time, oldRefresh string, ts *TokenSet) (Status, outcome, error) {
	next, verr := NewSession(cur.Host, ts.AccessToken, cur.User, now)
	if ts.RefreshToken != "" && ts.RefreshToken != oldRefresh {
		if err := g.store.SaveRefresh(ts.RefreshToken); err != nil {
			_ = g.store.DeleteRefresh() // best effort: the token on disk is dead
			st, oc, _ := g.recordAttempt(cur, now, string(ReasonKeyringUnavailable))
			return st, oc, err
		}
	}
	if verr != nil {
		// A token this build cannot verify is never stored: a bad monoes.me
		// deploy (an opaque token, a wrong audience) must not log every online
		// install out. A kid it does not know is remembered, so that when the
		// grace ends the verdict says key_unknown (run update), not expired.
		result := string(ReasonServerError)
		var ve *VerifyError
		if errors.As(verr, &ve) && ve.Reason == ReasonKeyUnknown {
			result = string(ReasonKeyUnknown)
		}
		return g.recordAttempt(cur, now, result)
	}
	err := g.store.Save(next)
	g.adopt(next)
	return g.Status(), outcomeRefreshed, err
}

// applyRefusal records that monoes.me answered invalid_grant: the session is
// marked refused (keeping the user for the message) until a new sign-in.
func (g *Guard) applyRefusal(cur *Session, now time.Time, r *RefusedError) (Status, outcome, error) {
	next := *cur
	next.State = stateRefused
	next.Reason = r.Description
	if len(next.Reason) > 200 {
		next.Reason = next.Reason[:200]
	}
	next.AccessToken = ""
	next.LastAttempt, next.LastResult = now, string(ReasonRefused)
	// The marker first, then the refresh token: a crash in between leaves a
	// marker beside a dead token (still locked), never a live-looking session.
	err := g.store.Save(&next)
	if derr := g.store.DeleteRefresh(); err == nil {
		err = derr
	}
	g.adopt(&next)
	return g.Status(), outcomeRefused, err
}

// recordAttempt stores the result of a failed attempt: the negative cache, and
// the reason a session in grace shows.
func (g *Guard) recordAttempt(cur *Session, now time.Time, result string) (Status, outcome, error) {
	next := *cur
	next.LastAttempt, next.LastResult = now, result
	bumpHW(&next, now)
	err := g.store.Save(&next)
	g.adopt(&next)
	return g.Status(), outcomeFailed, err
}

// bumpHW raises the high-water mark to now once it is a minute stale. It never
// lowers it: only a verified token resets it (NewSession).
func bumpHW(s *Session, now time.Time) {
	if now.After(s.HW) && now.Sub(s.HW) >= hwInterval {
		s.HW = now
	}
}

// touchHW persists the high-water mark (spec §4.5): only when a session exists,
// the package is not dormant, the stored mark is a minute stale, and this guard
// has not tried in the last minute. It never waits long for the lock and never
// reports a failure: a missed write is made up at the next call.
func (g *Guard) touchHW(now time.Time) {
	if dormant() {
		return
	}
	sess, _ := g.cached()
	if sess == nil || sess.State == stateRefused || !now.After(sess.HW) || now.Sub(sess.HW) < hwInterval {
		return
	}
	g.mu.Lock()
	try := elapsed(now, g.lastHWAttempt, hwInterval)
	if try {
		g.lastHWAttempt = now
	}
	g.mu.Unlock()
	if !try {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hwLockWait)
	defer cancel()
	unlock, err := g.store.Lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	fresh, err := g.store.Load()
	if err != nil || fresh == nil || fresh.State == stateRefused {
		return
	}
	next := *fresh
	bumpHW(&next, now)
	if next.HW.Equal(fresh.HW) {
		g.adopt(fresh)
		return
	}
	if g.store.Save(&next) == nil {
		g.adopt(&next)
	}
}
```

- [ ] **Step 4: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 5: Commit.**

```bash
git add internal/account/guard_refresh.go internal/account/guard_refresh_test.go
```

```bash
git commit -m "feat(account): the refresh algorithm under the cross-process lock" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: The background refresher

Long-running processes (the daemon, the serving commands, and any process still running after `LateRefresher`) refresh at half the token's lifetime and retry a failure with a backoff of 30 seconds doubling to 5 minutes (spec §4.4). The loop wakes every `Poll`, judges the half-life and the backoff by the guard's clock, follows `session.json` (so a sign-in from the CLI is picked up by itself and a refusal written by another process fires `OnRefused`), and runs no timer of its own, so a test that moves the clock controls it exactly. Counting the backoff from the start of the failed attempt, not its end, keeps the schedule independent of how long the call or the write took.

**Files:**
- Create: `internal/account/guard_loop.go`
- Test: `internal/account/guard_loop_test.go`

**Interfaces:**
- Consumes: `Guard` fields `loopCancel`, `loopDone`, `closed`, `mu`, `poll` and the constants `backoffMin`, `backoffMax` (Task 6); `refreshIfDue`, `modeBackground`, the `outcome…` values (Task 7); `dormant()` (Task 1).
- Produces: `func (g *Guard) StartRefresher(ctx context.Context)`: idempotent; a no-op while dormant; a no-op after `Close`; ends when `ctx` ends or `Close` is called (`Close`, Task 6, cancels the loop and waits for it, even in the middle of a network call, and an attempt cut short that way records nothing).
- A refresher started when the guard has no `Refresher` only follows the file.

- [ ] **Step 1: Create `internal/account/guard_loop_test.go`.**

```go
package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// loopPoll is the refresher's tick in these tests. Everything else, the
// half-life and the backoff, is judged by the fixture clock, which the test moves.
const loopPoll = 5 * time.Millisecond

func TestTheRefresherRefreshesAtHalfTheTokenLifetime(t *testing.T) {
	e := newEnv(t)
	e.signIn(20*time.Minute, time.Hour) // half-life is ten minutes from now
	g := e.newGuard(loopPoll)
	g.StartRefresher(context.Background())
	settle()
	if n := e.ref.calls.Load(); n != 0 {
		t.Fatalf("%d refreshes before the half-life", n)
	}
	e.f.Clock.Advance(10 * time.Minute)
	eventually(t, "the refresh at half-life", func() bool { return e.ref.calls.Load() == 1 })
	eventually(t, "the new token", func() bool { return g.Status().ValidUntil.Equal(e.f.Clock.Now().Add(time.Hour)) })
	settle()
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d refreshes, want 1: a fresh token is not due again", n)
	}
}

func TestTheRefresherBacksOffFrom30SecondsToFiveMinutes(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // expired: every pass is due
	e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) })
	g := e.newGuard(loopPoll)
	g.StartRefresher(context.Background())
	eventually(t, "the first attempt", func() bool { return e.ref.calls.Load() == 1 })
	settle() // let the loop record the failure and schedule the retry before the clock moves

	calls := int32(1)
	for _, gap := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		e.f.Clock.Advance(gap - time.Second)
		settle()
		if n := e.ref.calls.Load(); n != calls {
			t.Fatalf("a retry came before the %v backoff ended: %d calls, want %d", gap, n, calls)
		}
		e.f.Clock.Advance(2 * time.Second)
		calls++
		want := calls
		eventually(t, "the retry after "+gap.String(), func() bool { return e.ref.calls.Load() == want })
		settle()
	}
	if st := g.Status(); st.State != account.StateGrace || st.Reason != account.ReasonUnreachable {
		t.Fatalf("Status = %s/%q, want grace/unreachable", st.State, st.Reason)
	}
}

func TestTheRefresherPicksUpASignInFromAnotherProcess(t *testing.T) {
	e := newEnv(t)
	g := e.newGuard(loopPoll)
	g.StartRefresher(context.Background())
	settle()
	if st := g.Status(); st.Reason != account.ReasonNotLoggedIn {
		t.Fatalf("Status = %s/%q", st.State, st.Reason)
	}
	e.signIn(10*time.Minute, time.Hour) // the CLI signs in
	e.f.Clock.Advance(loopPoll)
	eventually(t, "the sign-in", func() bool { return g.Status().State == account.StateOK })
}

func TestStartRefresherIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) })
	g := e.newGuard(loopPoll)
	for i := 0; i < 3; i++ {
		g.StartRefresher(context.Background())
	}
	eventually(t, "the first attempt", func() bool { return e.ref.calls.Load() >= 1 })
	settle()
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d attempts, want 1: three StartRefresher calls must make one loop", n)
	}
}

func TestTheRefresherDoesNothingWhileDormant(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	account.SetEnforceFromForTest(t, time.Time{})
	g := e.newGuard(loopPoll)
	g.StartRefresher(context.Background())
	settle()
	if n := e.ref.calls.Load(); n != 0 {
		t.Fatalf("%d refreshes while dormant: no background refresher may run", n)
	}
}

func TestTheRefresherEndsWithItsContext(t *testing.T) {
	e := newEnv(t)
	e.signIn(20*time.Minute, time.Hour)
	g := e.newGuard(loopPoll)
	ctx, cancel := context.WithCancel(context.Background())
	g.StartRefresher(ctx)
	cancel()
	settle()
	e.f.Clock.Advance(time.Hour)
	settle()
	if n := e.ref.calls.Load(); n != 0 {
		t.Fatalf("%d refreshes after the context ended", n)
	}
}

func TestCloseStopsARefresherThatIsMidCallAndRecordsNothing(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	e.ref.set(func(r *fakeRefresher) { r.block = true })
	g := e.newGuard(loopPoll)
	g.StartRefresher(context.Background())
	eventually(t, "the attempt to start", func() bool { return e.ref.calls.Load() == 1 })

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.Close()
		g.Close() // safe twice
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not stop a refresher that was inside a network call")
	}
	if got := e.session().LastResult; got != "ok" {
		t.Fatalf("LastResult = %q: an attempt cut short by Close says nothing about the account", got)
	}
	g.StartRefresher(context.Background()) // a closed guard starts nothing
	settle()
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d calls: a closed guard started a loop", n)
	}
	if st := g.Status(); st.State != account.StateGrace {
		t.Fatalf("a closed guard still answers Status: %s", st.State)
	}
}

func TestTheRefresherTellsOnRefusedWhenItsOwnRefreshIsRefused(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	e.ref.set(func(r *fakeRefresher) { r.err = &account.RefusedError{Description: "blocked"} })
	g := e.newGuard(loopPoll)
	got := make(chan account.Status, 4)
	g.OnRefused(func(st account.Status) { got <- st })
	g.StartRefresher(context.Background())
	select {
	case st := <-got:
		if st.Reason != account.ReasonRefused {
			t.Fatalf("callback got %s/%q", st.State, st.Reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnRefused did not fire")
	}
	e.f.Clock.Advance(time.Hour)
	settle()
	if n := e.ref.calls.Load(); n != 1 || len(got) != 0 {
		t.Fatalf("%d calls and %d extra callbacks after a refusal, want 1 and 0", n, len(got))
	}
}

func TestTheRefresherNoticesARefusalFromAnotherProcess(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour)
	g := e.newGuard(loopPoll)
	got := make(chan account.Status, 4)
	g.OnRefused(func(st account.Status) { got <- st })
	g.StartRefresher(context.Background())
	settle()
	e.save(&account.Session{V: 1, Host: account.HostURL, User: &account.User{ID: "user-1"}, State: "refused"})
	e.f.Clock.Advance(loopPoll)
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("the refresher did not notice another process's refusal")
	}
}
```

- [ ] **Step 2: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors (`StartRefresher` is not defined):

```text
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/guard_loop_test.go:19:4: g.StartRefresher undefined (type *account.Guard has no field or method StartRefresher)
internal/account/guard_loop_test.go:38:4: g.StartRefresher undefined (type *account.Guard has no field or method StartRefresher)
internal/account/guard_loop_test.go:63:4: g.StartRefresher undefined (type *account.Guard has no field or method StartRefresher)
internal/account/guard_loop_test.go:79:5: g.StartRefresher undefined (type *account.Guard has no field or method StartRefresher)
...
```

- [ ] **Step 3: Create `internal/account/guard_loop.go`.**

```go
package account

import (
	"context"
	"time"
)

// StartRefresher starts the background refresher: it follows session.json,
// tells OnRefused callbacks about a refusal, and refreshes at half the token's
// lifetime, retrying a failure with a backoff of 30 seconds doubling to 5
// minutes. It is idempotent, does nothing while the package is dormant (spec
// D22) and ends when ctx ends or the guard is closed. A daemon starts it at
// once; any other process starts it after LateRefresher.
func (g *Guard) StartRefresher(ctx context.Context) {
	if dormant() {
		return
	}
	g.mu.Lock()
	if g.closed || g.loopCancel != nil {
		g.mu.Unlock()
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	g.loopCancel, g.loopDone = cancel, done
	g.mu.Unlock()
	go func() {
		defer close(done)
		g.runLoop(lctx)
	}()
}

// runLoop wakes every poll and judges everything by the guard's clock, so a
// test that moves the clock controls it exactly. It keeps running with no
// session, so a sign-in from another process is picked up by itself.
func (g *Guard) runLoop(ctx context.Context) {
	ticker := time.NewTicker(g.poll)
	defer ticker.Stop()
	var backoff time.Duration
	var notBefore time.Time // no attempt before this, by the guard's clock; counted from the start of the failed attempt
	for ctx.Err() == nil {
		now := g.now()
		// A notBefore further away than the longest backoff means the clock went back.
		if !notBefore.After(now) || notBefore.Sub(now) > backoffMax {
			_, oc, _ := g.refreshIfDue(ctx, modeBackground)
			switch oc {
			case outcomeFailed:
				backoff = min(max(backoff*2, backoffMin), backoffMax)
				notBefore = now.Add(backoff)
			case outcomeRefreshed, outcomeRefused:
				backoff, notBefore = 0, time.Time{}
			}
		} else {
			g.Status() // still follows the file and fires OnRefused
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 4: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 5: Run the loop tests three times; they do not flake.**

Run: `go test ./internal/account/ -count=3 -race -run '^(TestTheRefresherRefreshesAtHalfTheTokenLifetime|TestTheRefresherBacksOffFrom30SecondsToFiveMinutes|TestTheRefresherPicksUpASignInFromAnotherProcess|TestStartRefresherIsIdempotent|TestTheRefresherDoesNothingWhileDormant|TestTheRefresherEndsWithItsContext|TestCloseStopsARefresherThatIsMidCallAndRecordsNothing|TestTheRefresherTellsOnRefusedWhenItsOwnRefreshIsRefused|TestTheRefresherNoticesARefusalFromAnotherProcess)$'`

Expected: `ok  	github.com/monoes/mono-agent/internal/account	<time>`

- [ ] **Step 6: Commit.**

```bash
git add internal/account/guard_loop.go internal/account/guard_loop_test.go
```

```bash
git commit -m "feat(account): the background refresher with backoff" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: The process-wide guard and `accounttest.Install`

One `Guard` per process (spec D17): `Install`, `Current`, `Require` and `CurrentStatus`, and the fixture every gate-site test of every other plan calls to put the process in a chosen state. This task also proves the rule that nothing is created on disk until a write.

**Files:**
- Create: `internal/account/process.go`, `internal/account/accounttest/install.go`
- Test: `internal/account/process_test.go`, `internal/account/accounttest/install_test.go`

**Interfaces:**
- Consumes: `Guard`, `NewGuard`, `OpenStore`, `NewMemorySealer`, `NewSession`, `judge`, `LoginRequiredError`, `globalsMu`, `strict`, `requireTestBinary` (Tasks 1 to 8); `accounttest.New`, `Fixture`, `Clock`, `TokenOptions` (Task 2).
- Produces:
  - `func Install(g *Guard)` (`Install(nil)` removes the guard; `run()` uses it to uninstall the guard it built, so tests do not leak it), `func Current() *Guard`, `func InstallForTest(t testing.TB, g *Guard)` (installs, restores the previous guard on cleanup, panics outside a test binary; `InstallForTest(t, nil)` installs no guard for the rest of the test; both take the package lock, contract amendment approved by the lead), `func Require(ctx context.Context) error`, `func CurrentStatus() Status`, unexported `noGuardStatus(now time.Time) Status`.
  - `Require` with no guard installed: in a test binary and not strict, nil (spec D24); otherwise the process is judged as not logged in with enforcement judged normally, so it is `*LoginRequiredError` once enforced and nil while dormant or before the date (spec D22). `StrictForTest` alone does not refuse while dormant, so a strict gate-site test (B3a, B3b) also calls `SetEnforceFromForTest`.
  - `CurrentStatus` is nil-safe: the guard's `Status`, or `locked(not_logged_in)` with `Enforced` and `EnforceFrom` filled in.
  - `accounttest`: `type Mode int` with `SignedIn`, `InGrace`, `LockedNoLogin`, `LockedRefused`, `Dormant` (as index §3.3); `func Install(t testing.TB, m Mode) *account.Guard`; `func InstallWithFixture(t testing.TB, m Mode) (*account.Guard, *Fixture)` (the same, plus the fixture, for a test that moves the clock or mints another token).

- [ ] **Step 1: Create `internal/account/process_test.go`.** It includes `TestNothingIsCreatedOnAnEmptyHome`: the default store, `NewGuard`, `Status`, `Require`, `EnsureFresh`, `Refresh`, `StartRefresher`, `CurrentStatus` and `Evaluate` on an empty `HOME` create no file and no directory and make no network call (`scripts/doctor-smoke.sh:25` asserts that a plain `doctor` on a fresh `HOME` writes nothing, and `run()` installs a guard for every command, open ones included).

```go
package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

func TestInstallCurrentAndInstallForTest(t *testing.T) {
	account.InstallForTest(t, nil) // start from no guard; restored at the end
	if account.Current() != nil {
		t.Fatal("Current must be nil when no guard is installed")
	}
	g1 := account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())})
	g2 := account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())})
	account.Install(g1)
	if account.Current() != g1 {
		t.Fatal("Install did not install")
	}
	t.Run("a test installs its own", func(t *testing.T) {
		account.InstallForTest(t, g2)
		if account.Current() != g2 {
			t.Fatal("InstallForTest did not install")
		}
	})
	if account.Current() != g1 {
		t.Fatal("InstallForTest must put the previous guard back")
	}
	t.Run("a test installs none", func(t *testing.T) {
		account.InstallForTest(t, nil)
		if account.Current() != nil {
			t.Fatal("InstallForTest(t, nil) must leave no guard installed")
		}
	})
	if account.Current() != g1 {
		t.Fatal("InstallForTest(t, nil) must put the previous guard back")
	}
	account.Install(nil)
	if account.Current() != nil {
		t.Fatal("Install(nil) must remove the guard")
	}
}

func TestRequireWithNoGuardInstalled(t *testing.T) {
	account.InstallForTest(t, nil)
	ctx := context.Background()
	// A test binary that never installed a guard keeps working (D24).
	if err := account.Require(ctx); err != nil {
		t.Fatalf("Require in a test binary with no guard = %v, want nil", err)
	}
	// Strict, but dormant: nothing is enforced (D22), so nothing locks.
	account.SetEnforceFromForTest(t, time.Time{})
	account.StrictForTest(t)
	if err := account.Require(ctx); err != nil {
		t.Fatalf("a dormant strict Require = %v, want nil", err)
	}
	// Strict and enforced: fails closed, as a release binary does.
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour))
	err := account.Require(ctx)
	var lr *account.LoginRequiredError
	if !errors.As(err, &lr) || lr.Status.State != account.StateLocked || lr.Status.Reason != account.ReasonNotLoggedIn || !lr.Status.Enforced {
		t.Fatalf("a strict enforced Require = %v, want locked/not_logged_in", err)
	}
	// Strict, with the date still ahead: the warn period, nothing locks yet.
	account.SetEnforceFromForTest(t, time.Now().Add(24*time.Hour))
	if err := account.Require(ctx); err != nil {
		t.Fatalf("a strict Require before the date = %v, want nil", err)
	}
}

func TestCurrentStatusWithNoGuardInstalled(t *testing.T) {
	account.InstallForTest(t, nil)
	date := time.Now().Add(-time.Hour)
	account.SetEnforceFromForTest(t, date)
	st := account.CurrentStatus()
	if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced || !st.EnforceFrom.Equal(date) || st.V != 1 {
		t.Fatalf("CurrentStatus = %+v, want locked/not_logged_in with Enforced and EnforceFrom filled in", st)
	}
	account.SetEnforceFromForTest(t, time.Time{})
	if st := account.CurrentStatus(); st.Enforced || !st.Allowed() {
		t.Fatalf("while dormant: %+v", st)
	}
}

func TestRequireUsesTheInstalledGuard(t *testing.T) {
	g, f := accounttest.InstallWithFixture(t, accounttest.SignedIn)
	ctx := context.Background()
	if err := account.Require(ctx); err != nil {
		t.Fatalf("Require while signed in = %v", err)
	}
	f.Clock.Advance(30 * time.Hour)
	err := account.Require(ctx)
	if !account.IsLoginRequired(err) || g.Status().Reason != account.ReasonExpired {
		t.Fatalf("Require after 30 hours = %v, want the guard's locked/expired", err)
	}
	if st := account.CurrentStatus(); st.Reason != account.ReasonExpired {
		t.Fatalf("CurrentStatus = %s/%q, want the guard's", st.State, st.Reason)
	}
}

// The doors and the CLI gate call these on every request: none of them may
// create a file or a directory when nothing has been written (scripts/doctor-smoke.sh
// asserts that a plain `doctor` on a fresh HOME leaves it empty, and run()
// installs a guard for every command, open ones included).
func TestNothingIsCreatedOnAnEmptyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := accounttest.New(t)
	ref := &fakeRefresher{f: f, valid: "rt-1"}
	ctx := context.Background()

	st := account.OpenStore("", nil)
	_, _ = st.Load()
	_, _ = st.LoadRefresh()
	_, _ = st.Mtime()
	_ = st.DeleteRefresh()

	g := account.NewGuard(account.GuardOptions{Refresher: ref, Now: f.Clock.Now, Poll: loopPoll}) // the default store, under HOME
	_ = g.Status()
	_ = g.Require(ctx)
	_, _ = g.EnsureFresh(ctx)
	_, _ = g.Refresh(ctx)
	g.StartRefresher(ctx)
	settle()
	g.Close()

	account.InstallForTest(t, g)
	_ = account.CurrentStatus()
	_ = account.Require(ctx)
	_ = account.Evaluate(nil, f.Clock.Now())

	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("an empty HOME gained %v", names)
	}
	if n := ref.calls.Load(); n != 0 {
		t.Fatalf("%d network calls with no session", n)
	}
	if _, err := os.Stat(filepath.Join(home, ".monoagent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("~/.monoagent appeared")
	}
}
```

- [ ] **Step 2: Create `internal/account/accounttest/install_test.go`.**

```go
package accounttest

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

func TestInstallModes(t *testing.T) {
	guardBefore, dateBefore := account.Current(), account.EnforceDate()
	cases := []struct {
		name     string
		mode     Mode
		state    account.State
		reason   account.Reason
		enforced bool
		allowed  bool
	}{
		{"SignedIn", SignedIn, account.StateOK, account.ReasonNone, true, true},
		{"InGrace", InGrace, account.StateGrace, account.ReasonUnreachable, true, true},
		{"LockedNoLogin", LockedNoLogin, account.StateLocked, account.ReasonNotLoggedIn, true, false},
		{"LockedRefused", LockedRefused, account.StateLocked, account.ReasonRefused, true, false},
		{"Dormant", Dormant, account.StateLocked, account.ReasonNotLoggedIn, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := Install(t, c.mode)
			st := g.Status()
			if st.State != c.state || st.Reason != c.reason || st.Enforced != c.enforced || st.Allowed() != c.allowed {
				t.Fatalf("Status = %s/%q enforced=%v allowed=%v, want %s/%q enforced=%v allowed=%v",
					st.State, st.Reason, st.Enforced, st.Allowed(), c.state, c.reason, c.enforced, c.allowed)
			}
			if account.Current() != g {
				t.Fatal("Install must make the guard the process guard")
			}
			if got := account.CurrentStatus(); got.State != c.state || got.Reason != c.reason {
				t.Fatalf("CurrentStatus = %s/%q, want the guard's", got.State, got.Reason)
			}
			err := account.Require(context.Background())
			if c.allowed != (err == nil) || (err != nil && !account.IsLoginRequired(err)) {
				t.Fatalf("Require = %v, want allowed=%v", err, c.allowed)
			}
		})
	}
	if account.Current() != guardBefore || !account.EnforceDate().Equal(dateBefore) {
		t.Fatalf("Install leaked out of its test: guard %v, date %v (was %v)", account.Current(), account.EnforceDate(), dateBefore)
	}
}

func TestInstallStrictRefusesWhenTheGuardIsGone(t *testing.T) {
	Install(t, LockedNoLogin)
	account.Install(nil)                                         // the guard is gone; strictness remains
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour)) // with no guard Require judges by the real clock
	if err := account.Require(context.Background()); !account.IsLoginRequired(err) {
		t.Fatalf("Require with no guard under Install's strictness = %v, want a LoginRequiredError", err)
	}
}

func TestInstallWithFixtureLetsATestMoveTime(t *testing.T) {
	g, f := InstallWithFixture(t, SignedIn)
	f.Clock.Advance(90 * time.Minute) // the one-hour token has expired
	if st := g.Status(); st.State != account.StateGrace {
		t.Fatalf("after the clock moved, Status = %s, want grace", st.State)
	}
}
```

- [ ] **Step 3: Run the tests; they fail to compile.**

Run: `go test ./internal/account/... -count=1 -race`

Expected: FAIL, build errors:

```text
# github.com/monoes/mono-agent/internal/account/accounttest [github.com/monoes/mono-agent/internal/account/accounttest.test]
internal/account/accounttest/install_test.go:12:37: undefined: account.Current
internal/account/accounttest/install_test.go:15:12: undefined: Mode
internal/account/accounttest/install_test.go:21:16: undefined: SignedIn
internal/account/accounttest/install_test.go:22:15: undefined: InGrace
internal/account/accounttest/install_test.go:23:21: undefined: LockedNoLogin
...
```

- [ ] **Step 4: Create `internal/account/process.go`.**

```go
package account

import (
	"context"
	"testing"
	"time"
)

// installed is the process-wide guard; globalsMu guards it.
var installed *Guard

// Install makes g the process-wide guard that Require and CurrentStatus use.
// Install(nil) removes it. main() installs one for every command.
func Install(g *Guard) {
	globalsMu.Lock()
	installed = g
	globalsMu.Unlock()
}

// Current returns the installed guard, or nil when none is installed.
func Current() *Guard {
	globalsMu.RLock()
	defer globalsMu.RUnlock()
	return installed
}

// InstallForTest installs g for the rest of the test and puts the previous
// guard back when it ends; a nil g installs no guard for the rest of the test.
// A test that uses it must not call t.Parallel().
func InstallForTest(t testing.TB, g *Guard) {
	t.Helper()
	requireTestBinary("InstallForTest")
	globalsMu.Lock()
	prev := installed
	installed = g
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		installed = prev
		globalsMu.Unlock()
	})
}

// Require is the gate every layer calls. It uses the installed guard. With none
// installed it fails closed: the process is judged as not logged in, so once the
// gate is enforced it returns *LoginRequiredError. Two exceptions: while the
// package is dormant, or before the enforcement date, nothing locks (spec D22),
// so it returns nil; and inside a test binary (testing.Testing) it returns nil
// unless StrictForTest is active, so unit tests that never install a guard keep
// working (spec D24).
func Require(ctx context.Context) error {
	if g := Current(); g != nil {
		return g.Require(ctx)
	}
	globalsMu.RLock()
	isStrict := strict
	globalsMu.RUnlock()
	if testing.Testing() && !isStrict {
		return nil
	}
	if st := noGuardStatus(time.Now()); !st.Allowed() {
		return &LoginRequiredError{Status: st}
	}
	return nil
}

// CurrentStatus is nil-safe: the installed guard's Status, or, with none
// installed, locked(not_logged_in) with Enforced and EnforceFrom filled in.
// The doors and /health use it; a process that must react to a change (the
// daemon) polls it every PollInterval.
func CurrentStatus() Status {
	if g := Current(); g != nil {
		return g.Status()
	}
	return noGuardStatus(time.Now())
}

func noGuardStatus(now time.Time) Status { return judge(nil, nil, nil, now) }
```

- [ ] **Step 5: Create `internal/account/accounttest/install.go`.**

```go
package accounttest

import (
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// Mode is the state Install puts the process guard in.
type Mode int

const (
	SignedIn      Mode = iota // enforced, a valid token, state ok
	InGrace                   // enforced, an expired token inside the 24 hours, state grace(unreachable)
	LockedNoLogin             // enforced, no session, locked(not_logged_in)
	LockedRefused             // enforced, session marked refused, locked(refused)
	Dormant                   // EnforceFrom is the zero time: nothing is enforced
)

// Install builds a guard in the given mode over a temporary store with a
// throwaway key, trusts that key, sets the enforcement date for the mode,
// installs the guard as the process guard with the test-binary exception
// switched off (strict), and restores all of it when the test ends. It returns
// the guard. A test that uses it must not call t.Parallel().
func Install(t testing.TB, m Mode) *account.Guard {
	t.Helper()
	g, _ := InstallWithFixture(t, m)
	return g
}

// InstallWithFixture is Install that also returns the Fixture, for a test that
// needs the key to mint another token or the Clock to move time.
func InstallWithFixture(t testing.TB, m Mode) (*account.Guard, *Fixture) {
	t.Helper()
	f := New(t)
	if m == Dormant {
		account.SetEnforceFromForTest(t, time.Time{})
	}
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	now := f.Clock.Now()
	user := &account.User{ID: "user-1", Email: "user@example.test", Username: "user"}
	var sess *account.Session
	switch m {
	case SignedIn, InGrace:
		issued := now
		if m == InGrace {
			issued = now.Add(-2 * time.Hour) // a one-hour token, expired an hour ago
		}
		var err error
		if sess, err = account.NewSession(account.HostURL, f.Token(TokenOptions{IssuedAt: issued}), user, now); err != nil {
			t.Fatalf("accounttest: building the session: %v", err)
		}
		if m == InGrace {
			sess.LastResult = string(account.ReasonUnreachable)
		}
	case LockedRefused:
		sess = &account.Session{V: 1, Host: account.HostURL, User: user, HW: now, State: "refused", LastResult: "refused", LastAttempt: now}
	}
	if sess != nil {
		if err := store.Save(sess); err != nil {
			t.Fatalf("accounttest: saving the session: %v", err)
		}
	}
	g := account.NewGuard(account.GuardOptions{Store: store, Now: f.Clock.Now})
	t.Cleanup(g.Close)
	account.StrictForTest(t)
	account.InstallForTest(t, g)
	return g, f
}
```

- [ ] **Step 6: Run the tests; they pass.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 7: Commit.**

```bash
git add internal/account/process.go internal/account/process_test.go internal/account/accounttest/install.go internal/account/accounttest/install_test.go
```

```bash
git commit -m "feat(account): the process-wide guard and accounttest.Install" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Hygiene tests and the full gate

Three guard tests that keep later plans honest (the import rule, the pinned-key rule B5a must meet, well-formed pinned keys), then every check the index asks for before a plan is offered to the owner: both build variants, `-race`, the other operating systems, formatting, vet, and no new dependency.

**Files:**
- Test: `internal/account/hygiene_test.go` (package `account`), `internal/account/devkey_default_test.go` (package `account_test`, build tag `!devaccount`)
- Create: nothing; the code of Tasks 1 to 9 already satisfies these tests.

**Interfaces:**
- Consumes: `pinnedKeys`, `EnforceDate`, `TrustedKeys` (Task 1).
- Produces: `TestImportsOnlyWhatTheImportRuleAllows` (parses the package's non-test sources and allows only the standard library, `internal/secrets`, and `golang.org/x/sys/windows` in `lock_windows.go`), `TestEnforcedBuildPinsAKey` (fails when the enforcement date is set while `pinnedKeys` is empty: B5a must pin the first key in the same change that sets the date), `TestPinnedKeysAreWellFormed`; `TestDefaultBuildDoesNotTrustTheDevelopmentKey` (a build without the `devaccount` tag must not trust the development key whose seed is in the repository: acceptance 5 needs the key to be absent from release builds; B1b adds the matching `devaccount` test, see `Notes for B1b`).

- [ ] **Step 1: Create `internal/account/hygiene_test.go`.**

```go
package account

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The import rule (index §3.1): this package imports the standard library,
// golang.org/x/sys (the Windows lock) and internal/secrets, and nothing else of
// this repository, so that every other package may import it without a cycle.
func TestImportsOnlyWhatTheImportRuleAllows(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files found (%v)", err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(path, "/")
			switch {
			case !strings.Contains(first, "."): // the standard library
			case path == "github.com/monoes/mono-agent/internal/secrets":
			case path == "golang.org/x/sys/windows" && name == "lock_windows.go":
			default:
				t.Errorf("%s imports %s, which the import rule does not allow", name, path)
			}
		}
	}
}

// B5a sets the enforcement date. A build that enforces but pins no key would
// refuse every real token, so the date and the first pinned key must arrive
// together.
func TestEnforcedBuildPinsAKey(t *testing.T) {
	if !EnforceDate().IsZero() && len(pinnedKeys) == 0 {
		t.Fatal("the enforcement date is set but no signing key is pinned: every token would be refused as key_unknown")
	}
}

// Every pinned key must be a well-formed Ed25519 key with a unique kid.
func TestPinnedKeysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range TrustedKeys() {
		if k.KID == "" || seen[k.KID] || len(k.Public) != 32 {
			t.Errorf("malformed or duplicate pinned key %q (%d bytes)", k.KID, len(k.Public))
		}
		seen[k.KID] = true
	}
}
```

- [ ] **Step 1b: Create `internal/account/devkey_default_test.go`.**

```go
//go:build !devaccount

package account_test

import (
	"testing"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// A build without the devaccount tag must never trust the development key: its
// seed is in this repository, so trusting it would let anyone mint a session.
func TestDefaultBuildDoesNotTrustTheDevelopmentKey(t *testing.T) {
	pub, _ := accounttest.DevKeyPair()
	for _, k := range account.TrustedKeys() {
		if k.KID == accounttest.DevKID || k.Public.Equal(pub) {
			t.Fatalf("a build without the devaccount tag trusts the development key %q", k.KID)
		}
	}
}
```

- [ ] **Step 2: Run the whole package with the race detector.**

Run: `go test ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 3: Run it again with the `devaccount` tag.** The package must compile and pass with the tag (`keys_devaccount.go` is a stub until B1b).

Run: `go vet -tags devaccount ./internal/account/... && go test -tags devaccount ./internal/account/... -count=1 -race`

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

- [ ] **Step 4: Vet for the other operating systems.**

Run: `GOOS=windows go vet ./internal/account/... && GOOS=linux go vet ./internal/account/...`

Expected: no output.

- [ ] **Step 5: Build everything, and both variants of the CLI.** Never a bare `go build ./cmd/monoagentcli`: it overwrites the owner's local binary.

Run: `go build ./... && go build -tags nosocial -o /dev/null ./cmd/monoagentcli && go build -tags devaccount -o /dev/null ./cmd/monoagentcli`

Expected: no output.

- [ ] **Step 6: Format, vet, and confirm no dependency changed.**

Run: `gofmt -l . && go vet ./... && git diff --exit-code go.mod go.sum`

Expected: no output.

- [ ] **Step 7: Confirm the existing vault tests are unchanged and green.** About 80 seconds.

Run: `go test ./internal/secrets/ -count=1`

Expected: `ok  	github.com/monoes/mono-agent/internal/secrets	<time>`

- [ ] **Step 8: Mutation proof for the reviewer.** `B1a` is security-critical and gets a second review that tries to bypass the gate. Each change below was applied by hand to `internal/account` in the scratch export and made the named test fail; none survived. Re-apply any of them and run `go test ./internal/account/ -count=1 -race -run '<test>'` to see it fail, then undo it.

| Break this | Test that fails |
|---|---|
| In `refreshUnderLock`, skip the re-check after reading the session again | `TestSeveralProcessesRefreshingAtOnceMakeOneNetworkCall` |
| Make `elapsed` ignore a time in the future | `TestARolledBackClockIsRepairedByTheRefreshThatResetsHW` |
| In `applyTokens`, save the refresh token only when the access token verifies | `TestATokenThatDoesNotVerifyIsNeverStoredButTheRotatedRefreshTokenIs` |
| In `applyRefusal`, do not delete `refresh.enc` | `TestInvalidGrantLocksDeletesTheRefreshTokenAndCancelsWork` |
| Set `hwInterval` to 0 | `TestTheHighWaterMarkIsWrittenAtMostOnceAMinuteAndOnlyWithASession` |
| In `verifyToken`, drop the `alg` check | `TestVerifyAlgorithmConfusion` |
| In `verifyToken`, skip the signature check | `TestVerifyKeys` |
| In `Verify`, skip the clock-skew check | `TestVerifyClockSkew` |
| In `receiptFromClaims`, skip the audience check, or the 24 hour lifetime cap | `TestVerifyRejectsOneWrongClaimAtATime` |
| In `judge`, never report `clock_rollback` | `TestEvaluateMatrix` |
| In `note`, never clear `refusedNoted` | `TestOnRefusedFiresOncePerRefusal` |
| In `fileStore.Load`, create the directory | `TestReadsCreateNothing`, `TestNothingIsCreatedOnAnEmptyHome` |
| In `writeFileAtomic`, write in place instead of renaming | `TestReadersNeverSeeAPartialSession` |
| In `tryLock`, never fail | `TestLockExcludesAndCreatesItsFileOnlyWhenTaken`, `TestSeveralProcessesRefreshingAtOnceMakeOneNetworkCall` |
| In `Require`, refuse with no guard even while dormant | `TestRequireWithNoGuardInstalled` |
| Remove the ended-context checks in `refreshIfDue` and `refreshUnderLock` | `TestACallerThatHasGivenUpStartsNoRefresh` |
| In `JSONErrorFields`, drop the `code` | `TestLoginRequiredErrorJSONFields` |
| In `applyTokens`, do not remove the dead refresh token when the new one cannot be written | `TestAFailedWriteOfTheRotatedRefreshTokenRemovesTheDeadOne` |
| In `applyTokens`, write the session before the refresh token | `TestTheRefreshTokenIsWrittenBeforeTheSession` |

- [ ] **Step 9: Prove the suite does not lean on the ambient date or pinned keys.** B5a will set a real enforcement date and pin the first key, so no test may assume a dormant package or an empty pinned set. Set both temporarily, run the suite with a date in the past and again with one in the future, then undo (both files were committed in Task 1).

Run (a date in the past):

```bash
perl -pi -e 's/^var enforceFrom = time\.Time\{\}$/var enforceFrom = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)/' internal/account/rollout.go
```

```bash
perl -pi -e 's/^var pinnedKeys = \[\]Key\{\}$/var pinnedKeys = []Key{pinKey("release-key-1", "abababababababababababababababababababababababababababababababab")}/' internal/account/keys.go
```

```bash
go test ./internal/account/... -count=1 -race
```

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

Run (a date in the future):

```bash
perl -pi -e 's/^var enforceFrom = time\.Date\(2020,/var enforceFrom = time.Date(2099,/' internal/account/rollout.go
```

```bash
go test ./internal/account/... -count=1 -race
```

Expected:

```text
ok  	github.com/monoes/mono-agent/internal/account	<time>
ok  	github.com/monoes/mono-agent/internal/account/accounttest	<time>
```

Undo:

```bash
git checkout -- internal/account/rollout.go internal/account/keys.go
```

Expected: no output, and `git status --short` shows nothing.

- [ ] **Step 10: Commit.**

```bash
git add internal/account/hygiene_test.go internal/account/devkey_default_test.go
```

```bash
git commit -m "test(account): the import rule, the pinned-key rule and the default-build key check" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

## Notes for B1b and the plans after it

What B1a hands over. None of it is work for B1a; it is here so the next plans do not rediscover it.

**B1b (the network side).**
- Implement `account.Refresher`: `*account.RefusedError` only for `invalid_grant` answered to a refresh-token grant, `*account.TransientError{Reason: ReasonUnreachable | ReasonServerError}` for everything else (a body that cannot be read, a 4xx or 5xx, any other OAuth error, no network, a timeout). Put `ConnectTimeout` on the dialer; the guard adds a 20 second ceiling to every call and cancels it when the guard is closed. Never put a token in an error text.
- `Login`, `VerifyEmailCode` and `Logout` change the session files, so they hold `Store.Lock` around the writes. Build the session with `account.NewSession(host, accessToken, user, now)` (it verifies the token and resets `hw` to its `iat`) and open the store for an explicit sign-in with `account.OpenStore("", account.NewInteractiveKeyringSealer())`. Everything implicit (`NewDefaultGuard`, the refresher) uses the default store, whose sealer never prompts.
- Write `refresh.enc` (`SaveRefresh`) before `session.json` (`Save`), as the guard does (`TestTheRefreshTokenIsWrittenBeforeTheSession` pins the order). If an exchange has succeeded at the server but the new refresh token cannot be written, remove the old one with `DeleteRefresh`: monoes.me treats presenting a rotated-away refresh token as theft and revokes every MonoAgent refresh token of the account, which locks every install. The same rule binds adoption in `internal/library/adopt.go`, which exchanges the library's refresh token: persist the replacement first, and never leave the old token behind once the exchange has succeeded.
- `*account.LoginRequiredError` carries its own `--json` fields (`JSONErrorFields()`: `login_required`, `code`, `account`), the same keys as B1b's `loginRequiredError`; keep the two in step.
- `libraryfake` signs with `accounttest.DevKeyPair()`'s private half and `accounttest.DevKID`. The claims are the ones `accounttest.Fixture.Token` writes: `iss` `account.Issuer`, `aud` `account.Audience`, `azp` `account.ClientID`, `sub`, `iat`, `exp`, optional `plan`.
- Trust the development key in `devaccount` builds: replace the stub body of `keys_devaccount.go` with the file below (proven in the scratch export together with the test after it, three runs under `-race`).

```go
//go:build devaccount

package account

// The development signing key: the public half of accounttest.DevKeyPair.
// Builds with the devaccount tag trust it and nothing else does (spec D24), so
// a developer or a CI job can sign in against libraryfake, which signs with the
// private half.
const (
	devKID          = "monoagent-dev-1"
	devPublicKeyHex = "b2f5cb7a39788e27efd2b9c4f96873b495b61933f28c9c42904be76cd4328cff"
)

func extraKeys() []Key { return []Key{pinKey(devKID, devPublicKeyHex)} }
```

- Add the matching test. It fails until the file above is in place, which is why B1a does not carry it; `TestDefaultBuildDoesNotTrustTheDevelopmentKey` (Task 10) is its counterpart for release builds.

```go
//go:build devaccount

package account_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// A devaccount build trusts the development key, and a token signed with its
// private half verifies end to end.
func TestDevAccountBuildTrustsTheDevelopmentKey(t *testing.T) {
	pub, priv := accounttest.DevKeyPair()
	trusted := false
	for _, k := range account.TrustedKeys() {
		if k.KID == accounttest.DevKID && k.Public.Equal(pub) {
			trusted = true
		}
	}
	if !trusted {
		t.Fatal("a devaccount build does not trust the development key")
	}
	enc := base64.RawURLEncoding
	now := time.Now()
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": accounttest.DevKID})
	claims, _ := json.Marshal(map[string]any{
		"iss": account.Issuer, "aud": account.Audience, "azp": account.ClientID,
		"sub": "dev-user", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	signed := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	issued := signed + "." + enc.EncodeToString(ed25519.Sign(priv, []byte(signed)))
	if _, err := account.Verify(issued, now); err != nil {
		t.Fatalf("a token signed with the development key does not verify: %v", err)
	}
}
```

**B2 (the CLI gate).** `run()` installs its guard with `account.Install(g)` and removes it with `account.Install(nil)` when it returns, so tests do not leak it. Per command: `g.EnsureFresh(ctx)` first and ignore its error, then `g.Require(ctx)` or `g.Status()`. Start the late refresher with `time.AfterFunc(account.LateRefresher, func() { g.StartRefresher(ctx) })`. `EnsureFresh` and `StartRefresher` are no-ops while dormant, `Refresh` is not (`account status`). A `*LoginRequiredError` carries `Status`; its `Error()` is `LoginRequiredMessage` plus, for most reasons, a second line. A `*account.LoginRequiredError` that comes out of any command (`workflow run`, `chat`) prints the `login_required` document through `reportCommandError` and `withJSONErrors` without being converted: they find `JSONErrorFields()` with `errors.As`. The `code` is in those fields because the CLI does not classify a raw error of this type; `exitCodeFor` still has to map it to 4.

**B3a and B3b (runners and doors).** Call `account.Require(ctx)`; the `/v1` door and MCP use `account.LoginRequiredMessage` as their whole message. Tests use `accounttest.Install(t, mode)` and must not call `t.Parallel()`. `accounttest.Install` is strict; a strict test with no guard installed (`account.StrictForTest`) must also call `account.SetEnforceFromForTest`, because `Require` allows everything while dormant. `OnRefused` callbacks append (none replaces another), each fires once per refusal on its own goroutine, a callback registered while the verdict is already `locked(refused)` is called once at once, and `Refresh` fires them too, also while dormant (`TestRefreshFiresEveryOnRefusedCallbackEvenWhileDormant`), so one may call `CancelExecution` without holding a lock the caller of `Status` holds.

**B5a (the rollout).** Setting the date is one edit of `enforceFrom` in `rollout.go`; pin the first signing key in `pinnedKeys` in `keys.go` in the same change (`pinKey("<kid>", "<64 hex digits>")`): `TestEnforcedBuildPinsAKey` fails otherwise.

## Facts found while proving the code

- Index §3.2 (`docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md:257`) says that `Require` with no guard installed returns a `LoginRequiredError` outside a test binary. Read literally that locks every path without a guard in a release binary even while dormant, which contradicts D22 and spec §4.3 (`docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md:127`: before the date `Require` returns nil whatever the state). This plan follows D22.
- Spec §4.4 (`…-design.md:131`) holds a refresh off only for an expired token that was attempted within the minute; this plan applies the same minute to every CLI-path attempt, successful or not, so a token that lives only a few minutes cannot trigger a refresh on every command.
- Spec §6.4 (`…-design.md:209`) says the guard polls `session.json` every 5 seconds; this plan does it lazily inside `Status` (one `Mtime` stat per `Poll`), so a guard that never starts a refresher owns no goroutine, and the refresher loop wakes every `Poll`.
- `internal/secrets/keyring.go:162-181` routes a first-use key creation to the file keyring whenever `MONOAGENT_ALLOW_FILE_KEYRING=1`, even when the OS keychain answers, while `peekKEK` (`keyring.go:124-132`) says "not found" for an OS keychain that has no entry. The vault copes through its slower creation path; a reader that only peeked would never find the account key. `AccountKEK` looks in the file keyring after an empty OS lookup. No existing behavior changes.
- Go's `base64.RawURLEncoding.Strict()` still skips `\r` and `\n` in its input. A test (`TestVerifyTruncatedAndGarbageInput`, "a newline at the end") found a token with a trailing newline verifying; `decodeSegment` checks the alphabet itself.
- A cancelled refresher loop could run one more pass with a dead context (a `select` between a ready tick and a done context is random); the flake showed up in 2 of 8 runs of `TestCloseStopsARefresherThatIsMidCallAndRecordsNothing` and is fixed in `guard_refresh.go` and `guard_loop.go`.

## Contract change requests

None of these renames a frozen signature of index §3; each is an addition, an amendment the lead approved, or a reading the plan needs.

1. **`Require` with no guard installed (clarification of §3.2).** Name: `account.Require`. Reason: D22. Reading: with no guard installed it judges the process as `locked(not_logged_in)` with enforcement judged normally, so it returns nil while dormant or before the enforcement date, `*LoginRequiredError` once enforced, and nil inside a test binary unless `StrictForTest` is active.
2. **`account.LoginRequiredMessage`.** Reason: `/v1` (index §3.4 item 5), MCP (item 8) and the CLI gate (item 3) must share one string. Signature: `const LoginRequiredMessage = "Log in to monoes.me first: monoagentcli account login"`.
3. **`account.NewInteractiveKeyringSealer`.** Reason: the implicit sealer must never prompt for the file keyring's passphrase (it would read the piped stdin of the command that is about to run), while an explicit sign-in should be able to, as the vault does. Signature: `func NewInteractiveKeyringSealer() Sealer`. `NewKeyringSealer()` keeps its signature and is the non-prompting one.
4. **`account.NewSession`.** Reason: the refresh path and B1b's login must build a session the same way, with `hw` reset to the token's `iat` (spec §4.5). Signature: `func NewSession(host, accessToken string, user *User, now time.Time) (*Session, error)`.
5. **`secrets.AccountKEK`.** Reason: the single exported key-store accessor that index §3.1 allows in `internal/secrets`. Signature: `func AccountKEK(create, interactive bool) (kek []byte, found bool, err error)` in `internal/secrets/account_kek.go`; no existing line of that package changes.
6. **Visible consequence in `secret keyring status`.** When the account key lives in the file keyring, `secret keyring status` lists the id `monoes..account` under `File keyrings` and `secret keyring set-passphrase` also checks that a new passphrase unlocks it (`internal/secrets/keyring_passfile.go:101`, `:171`). Reason: the key is a first-class file keyring; a passphrase that cannot unlock it would orphan the refresh token. The lead accepted this: the account key follows the same passphrase lifecycle as the vault's keyrings, and it appears only after an explicit `account login` creates the key, so dormant users see nothing. B5b documents it in SECURITY.md. To avoid the visible change instead, give the account key a file name outside the `.file-keyring-` prefix, and accept that a later passphrase change can orphan it.
7. **`Session.LastResult` may be `key_unknown`.** Reason: spec §4.7 wants a client that cannot verify a rotated key to lock with `key_unknown` and say `update`. Reading: when a refresh returns a token whose `kid` this build does not pin, the token is not stored, `LastResult` becomes `key_unknown`, the grace reason shows `server_error` until the grace ends, and then the verdict is `locked(key_unknown)` instead of `expired`.
8. **Additions to `accounttest` (§3.3).** `NewClock(t time.Time) *Clock`, `DefaultNow`, `DevKID` (`"monoagent-dev-1"`), `(*Fixture).Sign(header, claims map[string]any) string`, `InstallWithFixture(t testing.TB, m Mode) (*account.Guard, *Fixture)`.
9. **Amendment approved by the lead (raised by plan-b2).** `account.Install(nil)` removes the process guard and `account.InstallForTest(t, nil)` installs no guard and restores the previous one on cleanup. Both take the package lock; `TestInstallCurrentAndInstallForTest` (Task 9) pins them.
10. **`(*LoginRequiredError).JSONErrorFields() map[string]any` (requested by plan-b2).** Reason: the CLI's JSON error wrappers (`cmd/monoagentcli/automation.go:94` `jsonErrorFields`, `main.go:112`) add machine-readable fields only for errors that implement it, and index §3.4 item 2 wants any command that returns `*account.LoginRequiredError` to print the `login_required` document. Returns `{"login_required": true, "code": "auth_or_connection", "account": {"state": string(e.Status.State), "reason": string(e.Status.Reason)}}` (the code is carried here because the CLI does not classify a raw error of this type: it would exit 1); `TestLoginRequiredErrorJSONFields` (Task 1, a table: locked not_logged_in, locked refused, grace, found through a wrapped error with `errors.As`) pins it.
11. **File placement.** `Evaluate` is in `session.go`, `InstallForTest` is in `process.go`; the other three test hooks are in `testhooks.go`. No signature differs.
