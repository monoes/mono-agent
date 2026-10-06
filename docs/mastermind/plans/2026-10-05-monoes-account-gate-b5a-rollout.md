# Mandatory monoes.me Account — B5a: the rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Release R: the production signing key is pinned and the enforcement date is set, an older library login is adopted into the machine session on the first run, a daemon that predates the release is restarted or flagged, the project's own CI, scripts and tests survive the date, and a binary built with the `devaccount` tag cannot ship.

**Architecture:** B5a pins the production key, then changes one line of `internal/account` (the date). Around that: the root command makes the one try to adopt an older library login (B1b provides the adoption and leaves the call to this plan); `update` restarts a daemon that runs old code and the `services.daemon` doctor row flags one; the service definitions give a stopping daemon time to finish a refresh; `scripts/check-release-tags.sh` gates `release.yml`; the real-binary consumers in the repository are audited (spike S4), adapted, and the day the date arrives is rehearsed. It adds no gate code: the gates are B1 to B4's, and the real-binary smoke that proves them is B5c's, merged before R.

**Tech Stack:** Go 1.26, cobra, bash, GitHub Actions, `go version -m`.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§1, §8, §9, §11, D9, D11, D22, D23, D24, D28; §13 A7, A8, A11, A12) and the index `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§1, §2, §3.6, §4). Depends on B1a to B4b and on B5c being merged. Release R is this plan and `b5b-docs` merged together as one push; B5d (the license) is independent of R. Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

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

1. **The key is pinned first, and a token signed by the private half proves it** (Task 1). Without a pinned key every real token verifies as `key_unknown` and every install locks on the date; B1a's `TestEnforcedBuildPinsAKey` refuses a date while none is pinned. The owner mints one fixture token from the key plan A's O3 put in the Worker.
2. **The date is the owner's input** (Task 2). The default is the merge day plus 21 days at 00:00 UTC, written once in `rollout.go` and pinned by one literal in a test.
3. **The development variable and the smoke are B5c's** (spec A12, index §3.6). `MONOAGENT_DEV_ENFORCE_FROM` and the real-binary smoke merge before R and must pass before it. This plan consumes the variable (Task 7) and its checklist requires the smoke green (Task 8).
4. **"In flight" means the daemon's own rows:** `status IN ('RUNNING','QUEUED') AND pid = <the heartbeat's pid>`. That works against a daemon that predates R, which has no new heartbeat field and is the daemon an update must handle. Org services and WAITING runs survive a restart and are not counted.
5. **`update` restarts in its own process** (`autostart.RestartRegistered`, which calls `Installer.Restart`: index §3.6), never by running the new binary's `daemon restart`, a gated command the new binary may refuse; the desktop's update flow is the same CLI call. It makes the one check `daemon restart` makes first (the saved `api config` settings must be usable) and reads the database without migrating it. It reconciles when the binary is already current: the first update to R is made by the pre-R binary, which cannot restart anything (A11), so a daemon older than R is flagged by `doctor` until the next `update`, a restart or a reboot.
6. **The doctor flag is the existing `services.daemon` row** turning `warn`, with a manual fix: `daemon restart` interrupts runs, so a person decides.
7. **Real-binary consumers that are not about the gate** (`wails-app`'s `buildTestCLI`, the manual e2e scripts) build with `-tags devaccount` and a date that never arrives, instead of signing in (spec §9 says to sign in, and the smoke does). `scripts/doctor-smoke.sh` stays on the default build and loses one assertion the date would break.
8. **The warnings belong to B2, B1b and B3a**, and B5c pins them end to end. This plan runs every suite with the date set (Task 2) and rehearses the day it arrives (Task 7): a test that assumed dormancy fails there, not on the day.
9. **Adoption is wired here, once** (Task 6). B1b provides `library.AdoptIntoAccount` and leaves the call to this plan; the function removes an older login from the vault once monoes.me has given a verdict, and also when the exchange went out and its answer never arrived or arrived and could not be stored here (spec A24, A24(d): monoes.me may have rotated the token, and presenting it again after the 300-second reuse window would end every login of the account: spike S2, spec A7), and keeps one only when the failure cannot have spent it (nothing was sent, or monoes.me answered with an error status). A retry at every command would make an implicit call before every command of an offline machine, so the wiring claims one try per database before it makes it, in the root command's `PersistentPreRun`: gated and serving commands only, after the CLI gate. Once the exchange is sent it is completed even if the command's context is cancelled meanwhile (spec A20, implemented in B1b's `exchangeOlder`): a Ctrl-C at the first command after the update neither loses the answer nor leaves a spent refresh token in the vault.
10. **One declaration each in files that other phases own** (index §3.1 and the cmd package): `pinnedKeys` in `keys.go` (B1a: "B5a pins the first key"), the initializer of `enforceFrom` in `rollout.go` (B1a), the `PersistentPreRun` of `root.go` (B1b adds its own line to `AddCommand`). `CONTRIBUTING.md` is B5b's and is not touched.
11. **The service definitions allow a stopping daemon 35 seconds** (Task 4b). The guard's `Close` waits for a refresh grant in flight and then for the key-store write of its answer, about 20 seconds and 30 at the worst, and launchd's default `ExitTimeOut` is 20: a kill inside that window loses the answer (spec A24). One constant, `stopGrace`, sets launchd's `ExitTimeOut`, systemd's `TimeoutStopSec` and the wait of the Windows `Restart`.

## Review Focus

The six failure modes the spec implies that no phase's tests exercise from outside and that are most likely to bite a person using this software, most likely first. Each is pinned in the task that owns the code.

1. **A daemon that predates R keeps running ungated for months**, warning nobody, or the update that fixes that interrupts a run or restarts a daemon it cannot see into. Pinned by `TestCheckDaemonFlagsAStaleVersion` (Task 3), `TestDaemonAfterUpdate` (its `an execution in flight`, `saved API settings that cannot be used` and `what cannot be told` rows), `TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings` and `TestUpdateWhenAlreadyCurrentRestartsAStaleDaemon` (Task 4). The first-update gap is a stated limit.
2. **A try is repeated at every command**, which makes an implicit call before every command of an offline machine and, were the older login kept after an exchange whose answer was lost, would present a refresh token that monoes.me may have spent and end every login of the account on every machine (B1b drops that login instead: A24); or adoption is never wired and every user who is logged in to the library has to sign in again. Pinned by `TestAdoptFirstRunTriesOnce` and `TestTheRootCommandAdoptsBeforeAGatedCommandRuns` (Task 6).
3. **The pinned key or claims differ from what monoes.me really signs, and every user is locked on the date.** Pinned by `TestProductionKeyVerifiesAProductionToken` (Task 1: a token signed by the production private key verifies against the pinned set) and by the production dry run of Task 8, which must pass before the merge.
4. **The date arrives and the project's own CI, scripts and tests lock themselves out**: a `doctor-smoke` that counts the account row as an unhealthy core, a desktop test that spawns a gated CLI, a maintainer's e2e script. Pinned by the rehearsal of Task 7: every suite and `scripts/doctor-smoke.sh` run with the date in the past, compared with the committed tree.
5. **A binary built with the `devaccount` tag ships**: it trusts a development key anyone can sign with. Pinned by `scripts/check-release-tags-test.sh` (the guard against binaries built with and without the tag, loose and inside the archives the release ships), run by the CI job `release-guard-test`, and by the `release-guard` job that runs the guard on every artifact before the approval gate (Task 5).
6. **A daemon that its service manager kills in the middle of a refresh** loses the answer of a grant that monoes.me has already rotated: the next attempt is the A24 case, and this machine signs in again (the account survives). launchd's default stop time, 20 seconds, is shorter than the guard's worst case of 30. Pinned by `TestTheStopGraceCoversTheGrantAndTheKeyStoreWrite`, `TestThePlistGivesTheDaemonTimeToFinishARefresh` and `TestTheUnitGivesTheDaemonTimeToFinishARefresh` (Task 4b).

---

### Task 1: Pin the production signing key

Plan A's owner-run steps O3 and O4 put the Ed25519 signing key in the Worker and publish its public half at `https://monoes.me/api/auth/jwks`. B1a ships `pinnedKeys` empty (`internal/account/keys.go`): until a key is pinned every real token verifies as `key_unknown` (clients that never update lock with it: the "run `update`" case, spec §4.7), and B1a's `TestEnforcedBuildPinsAKey` refuses a date while the set is empty. This task pins the key, and proves with a token signed by the private half that the pinned half is the one that matches. It lands before the date (Task 2). `pinnedKeys` lives in `keys.go`; `keys_default.go` only holds `extraKeys()`.

**Files:**
- Modify: `internal/account/keys.go` (`var pinnedKeys = []Key{}`)
- Create: `internal/account/production_key_test.go`, `internal/account/testdata/production-key-token.jwt`

**Interfaces:** Consumes B1a's `pinKey(kid, publicHex string) Key`, `pinnedKeys`, `Verify(token string, now time.Time) (*Receipt, error)` and the claims constants (`claims.go`), and plan A's S1 record in `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md`. Produces the pinned production key(s), the current one first and the next one when the rotation runbook (O5) has produced it.

- [ ] **Step 1: Pin what the server publishes.** This prints the published keys as ready-to-paste lines:

```
curl -s https://monoes.me/api/auth/jwks | python3 -c 'import sys,json,base64
for k in json.load(sys.stdin)["keys"]:
    x=k["x"]; raw=base64.urlsafe_b64decode(x+"="*(-len(x)%4)); print("\tpinKey(%s, %s)," % (json.dumps(k["kid"]), json.dumps(raw.hex())))'
```
Expected: one line per published key, whose kids agree with the S1 record and O3's output (stop if they do not). Replace `var pinnedKeys = []Key{}` in `internal/account/keys.go` with `var pinnedKeys = []Key{` and those lines, current key first, and `}`. If S1 recorded that the server cannot sign with a key that can be pinned and chose the JWKS fallback of spec §4.7, stop: B1a's key code needs its JWKS variant first, and R must not be cut.

- [ ] **Step 2: The owner mints the fixture token.** Only the owner has the private key (plan A's O3: the Worker cannot give it back, so it sits in a password manager). Save it for a minute to `~/pin/private.json` (mode 0600, outside the repository), then run, from the repository root:

```
node -e '
const fs=require("fs"),c=require("crypto");
const jwk=JSON.parse(fs.readFileSync(process.argv[1],"utf8")), src=fs.readFileSync(process.argv[2],"utf8");
const k=(n)=>src.match(new RegExp(n+"\\s*=\\s*\"([^\"]+)\""))[1];
const kid=c.createHash("sha256").update(JSON.stringify({crv:jwk.crv,kty:jwk.kty,x:jwk.x})).digest("base64url");
const b=(o)=>Buffer.from(JSON.stringify(o)).toString("base64url");
const iat=Math.floor(Date.now()/1000);
const head=b({alg:"EdDSA",typ:"at+jwt",kid}), body=b({iss:k("Issuer"),aud:[k("Audience")],azp:k("ClientID"),sub:"pin-test",iat,exp:iat+3600});
const sig=c.sign(null,Buffer.from(head+"."+body),c.createPrivateKey({key:jwk,format:"jwk"})).toString("base64url");
fs.writeFileSync(process.argv[3],head+"."+body+"."+sig+"\n"); console.error("kid:",kid);
' ~/pin/private.json internal/account/claims.go internal/account/testdata/production-key-token.jwt
rm ~/pin/private.json
```
Expected: it prints one line, `kid: <the kid of Step 1's first line>` (the RFC 7638 thumbprint plan A names keys by), and writes the token. It reads the issuer, audience and client from `claims.go`, so a constant that spike S6 corrected is the one signed. The token names a made-up user, expires within the hour and cannot be refreshed: it is safe to commit, and the key is never printed or committed.

- [ ] **Step 3: Write the test.** `internal/account/production_key_test.go`:

```go
package account

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The pinned keys must verify a token signed by the production private key: one the owner minted once
// for a made-up user (Task 1 Step 2) that expired within the hour. Verified at the moment it was
// issued, it proves that the public half in pinnedKeys is the half that matches the key monoes.me
// signs with, and it fails if a later edit drops that key from the set.
func TestProductionKeyVerifiesAProductionToken(t *testing.T) {
	raw, err := os.ReadFile("testdata/production-key-token.jwt")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("the fixture is not a compact JWS")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iat int64 `json:"iat"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Iat == 0 {
		t.Fatalf("the fixture's payload has no iat: %v", err)
	}
	if _, err := Verify(token, time.Unix(claims.Iat, 0).Add(time.Minute)); err != nil {
		t.Fatalf("the pinned keys do not verify a token signed by the production key: %v", err)
	}
}
```

- [ ] **Step 4: Run it.**

```
go test ./internal/account/ -run '^(TestProductionKeyVerifiesAProductionToken|TestEnforcedBuildPinsAKey|TestPinnedKeysAreWellFormed)$' -count=1
```
Expected: `ok`.

- [ ] **Step 5: Commit.**

```
git add internal/account/keys.go internal/account/production_key_test.go internal/account/testdata/production-key-token.jwt
git commit -m "feat(account): pin the production signing key" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Prove the test can fail.** With the `Edit` tool change one hex digit of the first pinned key in `internal/account/keys.go` and run the first test alone: FAIL with `the pinned keys do not verify a token signed by the production key: ... signature does not verify`. Then `git checkout -- internal/account/keys.go` and run it again: `ok`.

### Task 2: The enforcement date

**Files:**
- Modify: `internal/account/rollout.go` (the initializer of `enforceFrom`; index §3.2 names the line: `var enforceFrom = time.Time{}`)
- Create: `internal/account/rollout_date_test.go`

**Interfaces:** Consumes (index §3.2): `account.EnforceDate() time.Time`, `account.Enforced(now, hw time.Time) bool`, `account.Evaluate(sess *Session, now time.Time) Status`, `Status.Allowed() bool`, `Session{V int; HW time.Time}`; from B5c (index §3.6): `MONOAGENT_DEV_ENFORCE_FROM`, which a test that runs a built binary uses to move the date. Produces the date.

- [ ] **Step 1: Ask the owner for the date, and stop until answered.** Say: "Release R turns enforcement on at a date you choose. Until then every gated command works without a sign-in and warns. Default: the day R is merged plus 21 days, at 00:00 UTC. Which date?" Work the default out with `date -u -v+21d +%Y-%m-%d` (macOS) or `date -u -d '+21 days' +%Y-%m-%d` (Linux). If the merge slips, the date moves with it. The code below shows the default for a merge on 2026-10-05 (`2026, time.October, 26`): replace those three numbers, in both places that carry them (`rollout.go` and the test), with the owner's answer.

- [ ] **Step 2: Write the failing test.** `internal/account/rollout_date_test.go`:

```go
package account

import (
	"testing"
	"time"
)

// wantEnforceFrom is release R's enforcement date, the owner's decision: 00:00 UTC, three weeks
// after the merge by default. Moving it is a release decision, made here and in rollout.go together.
var wantEnforceFrom = time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)

func TestEnforceDateIsTheOwnersDate(t *testing.T) {
	if got := EnforceDate(); got.IsZero() || !got.Equal(wantEnforceFrom) {
		t.Fatalf("EnforceDate() = %v, want %v: release R must set the date, and the zero time means dormant", got, wantEnforceFrom)
	}
}

// Enforcement starts at that instant, not a day after it.
func TestEnforcedFlipsAtTheDate(t *testing.T) {
	d := EnforceDate()
	for _, c := range []struct {
		now  time.Time
		want bool
	}{{d.Add(-24 * time.Hour), false}, {d.Add(-time.Nanosecond), false}, {d, true}, {d.Add(time.Nanosecond), true}, {d.AddDate(1, 0, 0), true}} {
		if got := Enforced(c.now, time.Time{}); got != c.want {
			t.Errorf("Enforced(%v, no high-water mark) = %v, want %v", c.now.Sub(d), got, c.want)
		}
	}
}

// Setting the clock back does not postpone the date: it is judged against the later of now and the
// highest time the gate has seen, and a high-water mark before the date does not enforce early.
func TestTheClockGuardCannotPostponeTheDate(t *testing.T) {
	d := EnforceDate()
	if !Enforced(d.AddDate(-1, 0, 0), d.Add(time.Hour)) {
		t.Error("a clock set back a year, with the high-water mark past the date, must still be enforced")
	}
	if Enforced(d.Add(-48*time.Hour), d.Add(-24*time.Hour)) {
		t.Error("a high-water mark before the date must not enforce early")
	}
	if st := Evaluate(&Session{V: 1, HW: d.Add(time.Hour)}, d.AddDate(-1, 0, 0)); !st.Enforced || st.Allowed() {
		t.Errorf("a session whose high-water mark is past the date, read by a clock a year behind: %+v", st)
	}
	if st := Evaluate(nil, d.Add(-time.Hour)); st.Enforced || !st.Allowed() {
		t.Errorf("before the date a machine with no session is not locked: %+v", st)
	}
}
```

- [ ] **Step 3: Run it and watch it fail.**

```
go test ./internal/account/ -run '^(TestEnforceDateIsTheOwnersDate|TestEnforcedFlipsAtTheDate|TestTheClockGuardCannotPostponeTheDate)$' -count=1
```
Expected: FAIL, all three: `EnforceDate() = 0001-01-01 00:00:00 +0000 UTC, want 2026-10-26 00:00:00 +0000 UTC: release R must set the date, and the zero time means dormant`; `Enforced(0s, no high-water mark) = false, want true`; `a clock set back a year, with the high-water mark past the date, must still be enforced`.

- [ ] **Step 4: Set the date.** In `internal/account/rollout.go` replace `var enforceFrom = time.Time{}` with the line below, and run the command of Step 3 again: `ok`. Keep this exact shape: B5b's changelog test finds the date with `grep -n '^var enforceFrom' internal/account/rollout.go`.

```go
var enforceFrom = time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
```

- [ ] **Step 5: Run everything in the warn period.** The date is set and ahead, so every gated command now warns and `Enforced` is false: a test that assumed dormancy (no stderr, no `enforce_from`) fails here, not on the day. B1a's leak checks compare the date with the value captured before the hook, so they hold whatever the date is; confirm that none still compares it with the zero time:

```
grep -rn 'EnforceDate().IsZero()' --include='*_test.go' internal
go test ./... -count=1 -timeout 40m 2>&1 | grep -E '^(--- FAIL|FAIL)'
```
Expected: the grep prints nothing (a line is a leak check written for an always-dormant package: capture `dateBefore := account.EnforceDate()` beside the values the test already captures and compare with `!account.EnforceDate().Equal(dateBefore)`), and the tests fail only on the known failures of index §4. For any other: an in-process test starts with `account.SetEnforceFromForTest(t, time.Time{})` (or `accounttest.Install(t, accounttest.Dormant)`); a test that runs a built binary sets B5c's `MONOAGENT_DEV_ENFORCE_FROM` on a devaccount build. Fix each in that test's file and run again until clean.

- [ ] **Step 6: Commit** (the files of Steps 2 and 4, and any test fixed in Step 5, added by name).

```
git add internal/account/rollout.go internal/account/rollout_date_test.go
git commit -m "feat(account): set the enforcement date for release R" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3: `doctor` flags a daemon that runs old code

**Files:**
- Modify: `internal/health/health.go` (`DaemonInfo`, lines 254-263), `internal/health/services.go` (imports; consts, lines 12-18; `serviceFixes`, 29-41; `checkDaemon`, 56-72), `cmd/monoagentcli/doctor_env_services.go` (line 47, `env.Daemon`)
- Create: `internal/health/services_daemon_version_test.go`

**Interfaces:** Consumes `health.Env{Version string; Daemon func(context.Context) DaemonInfo}` and `daemonhb.Heartbeat.Version` (`internal/daemonhb/heartbeat.go:44`, present since v0.46.0). Produces `health.DaemonVersionStale(daemonVersion, binaryVersion string) bool`, `health.FixDaemonRestart = "services.daemon.restart"`, `health.DaemonInfo.Version`.

- [ ] **Step 1: Write the failing test.** `internal/health/services_daemon_version_test.go`:

```go
package health

import (
	"context"
	"strings"
	"testing"
)

func TestDaemonVersionStale(t *testing.T) {
	for _, c := range []struct {
		daemon, binary string
		want           bool
	}{
		{"v0.105.0", "v0.108.0", true},         // an older daemon
		{"0.105.0", "v0.105.0", false},         // the same version, one side without the v
		{"", "v0.108.0", true},                 // a daemon from before heartbeats carried a version
		{"v0.105.0", "", false},                // this binary has no version to judge by
		{"v0.105.0", "dev", false},             // a development binary
		{"v0.105.0", "v0.105.0-3-gabc", false}, // a development binary (git describe)
		{"v0.46.0-9-gdef", "v0.108.0", false},  // a development daemon is the developer's own
		{"dev", "v0.108.0", false},
	} {
		if got := DaemonVersionStale(c.daemon, c.binary); got != c.want {
			t.Errorf("DaemonVersionStale(%q, %q) = %v, want %v", c.daemon, c.binary, got, c.want)
		}
	}
}

// A daemon that runs the old code is a warning with the manual fix, and the fix refuses to run
// itself: a restart interrupts what the daemon is running, so a person decides.
func TestCheckDaemonFlagsAStaleVersion(t *testing.T) {
	ctx := context.Background()
	env := &Env{Version: "v0.108.0",
		Daemon: func(context.Context) DaemonInfo { return DaemonInfo{Running: true, PID: 42, Version: "v0.105.0"} }}
	res := checkDaemon(ctx, env)
	if res.Status != StatusWarn || res.FixID != FixDaemonRestart {
		t.Fatalf("stale daemon: %+v", res)
	}
	for _, want := range []string{"v0.105.0", "v0.108.0"} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("summary %q should name %s", res.Summary, want)
		}
	}
	f, ok := Default().Fix(FixDaemonRestart)
	if !ok || f.Safety != SafetyManual || f.Command != "monoagentcli daemon restart" {
		t.Fatalf("the fix must be the manual `daemon restart`: %+v, %v", f, ok)
	}
	if err := f.Apply(ctx, env, noop); err == nil {
		t.Error("a manual fix must not run itself")
	}

	env.Daemon = func(context.Context) DaemonInfo { return DaemonInfo{Running: true, PID: 42, Version: "v0.108.0"} }
	if res := checkDaemon(ctx, env); res.Status != StatusOK {
		t.Errorf("the same version is fine: %+v", res)
	}
	env.Version = "v0.105.0-3-gabc"
	env.Daemon = func(context.Context) DaemonInfo { return DaemonInfo{Running: true, PID: 42, Version: "v0.1.0"} }
	if res := checkDaemon(ctx, env); res.Status != StatusOK {
		t.Errorf("a development binary never reports a stale daemon: %+v", res)
	}
}
```

- [ ] **Step 2: Run it and watch it fail.** `go test ./internal/health/ -run '^(TestDaemonVersionStale|TestCheckDaemonFlagsAStaleVersion)$' -count=1`. Expected: build failure: `undefined: DaemonVersionStale`, `undefined: FixDaemonRestart`, `unknown field Version in struct literal of type DaemonInfo`.

- [ ] **Step 3: Implement.** In `internal/health/health.go`, add the field to `DaemonInfo`:

```go
	// Version is the version the daemon started with, from its heartbeat; ""
	// for a daemon that predates the field (v0.46.0).
	Version string
}
```
In `internal/health/services.go`: add `"strings"` to the imports; add `FixDaemonRestart = "services.daemon.restart"` to the const block after `FixDaemonStart`; append this entry to the slice `serviceFixes` returns, after the `FixDaemonStart` entry:

```go
		{FixInfo: FixInfo{ID: FixDaemonRestart, Label: "Restart the workflow daemon", Safety: SafetyManual,
			Command: "monoagentcli daemon restart"}, Apply: func(context.Context, *Env, func(string)) error {
			return fmt.Errorf("restart it yourself: monoagentcli daemon restart (it interrupts whatever the daemon is running)")
		}},
```
Replace the end of `checkDaemon` (`return Result{Status: StatusOK, Summary: summary}`, after the API probe) and add the predicate below it:

```go
	if DaemonVersionStale(d.Version, env.Version) {
		running := d.Version
		if running == "" {
			running = "a version from before heartbeats carried one"
		}
		return Result{Status: StatusWarn, Summary: fmt.Sprintf("%s — it runs %s, this binary is %s", summary, running, env.Version),
			Detail: "a running daemon keeps the code it started with: it does not have this version's changes, among them its monoes.me account check, " +
				"until it restarts. `monoagentcli daemon restart` restarts it and interrupts whatever it is running",
			FixID: FixDaemonRestart}
	}
	return Result{Status: StatusOK, Summary: summary}
}

// DaemonVersionStale reports whether a running daemon predates binaryVersion, the version of the
// binary that asks. A development build on either side has no version to compare, so it is never
// stale; a heartbeat with no version is a daemon from before the field existed, so it is.
func DaemonVersionStale(daemonVersion, binaryVersion string) bool {
	norm := func(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }
	dev := func(v string) bool { return v == "dev" || strings.Contains(v, "-g") }
	bin, d := norm(binaryVersion), norm(daemonVersion)
	if bin == "" || dev(bin) || dev(d) {
		return false
	}
	return d != bin
}
```
In `cmd/monoagentcli/doctor_env_services.go` line 47, pass the heartbeat's version on:

```go
		return health.DaemonInfo{Running: live, PID: hb.PID, APIAddr: hb.APIAddr, BridgeAddr: hb.BridgeAddr, Version: hb.Version, AgeMS: time.Since(hb.TS).Milliseconds()}
```

- [ ] **Step 4: Run the test, expect PASS, then the packages.**

```
go test ./internal/health/ -run '^(TestDaemonVersionStale|TestCheckDaemonFlagsAStaleVersion|TestDaemonCheckAndStart)$' -count=1 -v
go test ./internal/health/ -count=1
go test ./cmd/monoagentcli/ -run '^(TestStartDaemonUsesTheServiceOnlyWithDefaults|TestDaemonStartBlockedIsTheRefusalStartDaemonGives)$' -count=1
gofmt -l internal/health cmd/monoagentcli
```
Expected: three `--- PASS` lines; `ok` for each test command; no gofmt output.

- [ ] **Step 5: Commit.**

```
git add internal/health/health.go internal/health/services.go internal/health/services_daemon_version_test.go cmd/monoagentcli/doctor_env_services.go
git commit -m "feat(doctor): flag a daemon that runs another version than this binary" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4: `update` and `update --app` restart a daemon that runs old code

**Files:**
- Create: `cmd/monoagentcli/update_daemon.go`, `cmd/monoagentcli/update_daemon_test.go`
- Modify: `cmd/monoagentcli/update.go` (the `Long` text, lines 29-34; the call, line 52; `runUpdate`, lines 142, 158-161 and 226), `cmd/monoagentcli/update_app.go` (`appUpdateResult`, lines 22-31; `runUpdateApp`, lines 41-47)

**Interfaces:**
- Consumes: `daemonhb.Read() (Heartbeat, bool)`, `health.DaemonVersionStale` (Task 3), `autostart.RestartRegistered(ctx, in Installer) (RestartResult, error)` and `*autostart.NotRegisteredError` (`internal/autostart/restart.go:36`, the function at `:54`), `newInstaller` (`cmd/monoagentcli/api_config.go:20`), `savedSettings(ctx, db) (apiconfig.Settings, error)` (`api_gateway.go:210`: the check `daemon restart` makes before it restarts anything, `daemon_restart.go:68`), `storage.NewDatabase(path)` (opens without migrating, as `doctor` does), `expandPath`. Test helpers that exist: `fakeAutostart` (`doctor_env_test.go:86`), `useInstaller` (`api_config_test.go:29`), `plantRow` (`daemon_api_unusable_test.go:93`), `fakeAppRelease`, `appInstallDir`, `runUpdateAppCmd`, `tgz` (`update_app_test.go`), `testdb.Path`.
- Produces: `daemonUpdate`, `restartBlockers` (a variable, so a test can replace it), `daemonAfterUpdate(ctx, cfg, newVersion) *daemonUpdate`, `appUpdateResult.Daemon` (JSON `"daemon"`). `daemonUpdate.Action` is `restarted`, `busy`, `settings`, `unknown`, `not_registered` or `failed`.

- [ ] **Step 1: Write the failing tests.** `cmd/monoagentcli/update_daemon_test.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// runningDaemon writes the heartbeat of a daemon that runs version: this test process, so it is alive.
func runningDaemon(t *testing.T, version string) {
	t.Helper()
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), Version: version}); err != nil {
		t.Fatal(err)
	}
}

// fakeBlockers replaces what the update reads from the database, for one test.
func fakeBlockers(t *testing.T, inFlight int, settingsErr, err error) {
	t.Helper()
	was := restartBlockers
	restartBlockers = func(context.Context, *globalConfig, int) (int, error, error) { return inFlight, settingsErr, err }
	t.Cleanup(func() { restartBlockers = was })
}

func TestDaemonAfterUpdate(t *testing.T) {
	damaged := errors.New("the saved settings are damaged")
	for _, c := range []struct {
		name       string
		daemon     string // the version in the heartbeat; "-" means no daemon runs
		inFlight   int
		settings   error
		count      error
		registered bool
		restartErr error
		action     string // "" means nothing to do
		restarted  int
		says       []string
	}{
		{"no daemon runs", "-", 0, nil, nil, true, nil, "", 0, nil},
		{"it already runs the new version", "v0.108.0", 0, nil, nil, true, nil, "", 0, nil},
		{"an old daemon with nothing in flight", "v0.105.0", 0, nil, nil, true, nil, "restarted", 1, []string{"Restarted the daemon", "v0.108.0", "v0.105.0"}},
		{"a heartbeat with no version", "", 0, nil, nil, true, nil, "restarted", 1, nil},
		{"an execution in flight", "v0.105.0", 2, nil, nil, true, nil, "busy", 0, []string{"2 workflow execution(s)", "never interrupts a run", "monoagentcli daemon restart"}},
		{"saved API settings that cannot be used", "v0.105.0", 0, damaged, nil, true, nil, "settings", 0, []string{"OpenAI-compatible API", "the saved settings are damaged"}},
		{"what cannot be told", "v0.105.0", 0, nil, errors.New("database is locked"), true, nil, "unknown", 0, []string{"database is locked"}},
		{"not registered for auto-start", "v0.105.0", 0, nil, nil, false, nil, "not_registered", 0, []string{"daemon install"}},
		{"a service manager that fails", "v0.105.0", 0, nil, nil, true, errors.New("launchctl: exit status 113"), "failed", 1, []string{"exit status 113", "daemon restart"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.daemon == "-" {
				t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
			} else {
				runningDaemon(t, c.daemon)
			}
			fakeBlockers(t, c.inFlight, c.settings, c.count)
			fake := &fakeAutostart{installed: c.registered, restartErr: c.restartErr}
			useInstaller(t, fake)
			d := daemonAfterUpdate(context.Background(), &globalConfig{}, "v0.108.0")
			if c.action == "" {
				if d != nil || fake.restarted != 0 {
					t.Fatalf("d = %+v, restarted %d: nothing to do", d, fake.restarted)
				}
				return
			}
			if d == nil || d.Action != c.action || fake.restarted != c.restarted || d.InFlight != c.inFlight {
				t.Fatalf("d = %+v, restarted %d: want %s (an update never interrupts a run)", d, fake.restarted, c.action)
			}
			for _, want := range c.says {
				if !strings.Contains(d.Message, want) {
					t.Errorf("message %q should say %q", d.Message, want)
				}
			}
		})
	}
}

// What the update reads is the daemon's own: RUNNING and claimed QUEUED rows of its pid (not another
// process's, not WAITING, not finished, not unclaimed), and the saved settings `daemon restart` checks.
func TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings(t *testing.T) {
	path := testdb.Path(t)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec(`INSERT INTO workflows (id, name) VALUES ('w', 'w')`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		id, status string
		pid        int
	}{{"a", "RUNNING", 4242}, {"b", "QUEUED", 4242}, {"c", "RUNNING", 999}, {"d", "WAITING", 4242}, {"e", "SUCCESS", 4242}, {"f", "QUEUED", 0}} {
		if _, err := db.DB.Exec(`INSERT INTO workflow_executions (id, workflow_id, status, pid) VALUES (?, 'w', ?, ?)`, r.id, r.status, r.pid); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &globalConfig{DBPath: path}
	if n, settings, err := restartBlockers(context.Background(), cfg, 4242); err != nil || settings != nil || n != 2 {
		t.Fatalf("count = %d, settings %v, %v; want 2 (a and b) and usable settings", n, settings, err)
	}
	plantRow(t, db.DB, "not json")
	if _, settings, err := restartBlockers(context.Background(), cfg, 4242); err != nil || settings == nil {
		t.Fatalf("a damaged row: settings %v, %v", settings, err)
	}
	if _, _, err := restartBlockers(context.Background(), &globalConfig{DBPath: filepath.Join(t.TempDir(), "absent.db")}, 1); err == nil {
		t.Fatal("no database is not 'nothing in flight'")
	}
}

// `update` on a binary that is already current still brings a stale daemon onto it: the first update
// to a release is made by the binary that predates it, which cannot restart anything.
func TestUpdateWhenAlreadyCurrentRestartsAStaleDaemon(t *testing.T) {
	_ = getVersion() // resolve it once, so that the cleanup restores a value
	was := version
	version = "v9.9.9"
	t.Cleanup(func() { version = was })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9","assets":[]}`)
	}))
	t.Cleanup(srv.Close)
	old := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = old })
	runningDaemon(t, "v9.8.0")
	fakeBlockers(t, 0, nil, nil)
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)
	if err := runUpdate(newUpdateCmd(&globalConfig{}), &globalConfig{}); err != nil || fake.restarted != 1 {
		t.Fatalf("err %v, the stale daemon was restarted %d times, want 1", err, fake.restarted)
	}
}

// update --app --json reports what it did about the daemon, and the progress line carries it to the
// desktop app (the install path is linux/amd64's, as in the other update --app tests).
func TestUpdateAppRestartsTheDaemonAndReportsIt(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("linux/amd64 install path")
	}
	fakeAppRelease(t, tgz(t, map[string]string{"MonoAgent-linux-amd64": "new-app"}), false)
	app, _ := appInstallDir(t)
	runningDaemon(t, "v1.0.0")
	fakeBlockers(t, 0, nil, nil)
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)
	res, progress := runUpdateAppCmd(t, "--app", app, "--current", "v1.0.0")
	if !res.Success || res.Daemon == nil || res.Daemon.Action != "restarted" || fake.restarted != 1 || !strings.Contains(progress, "Restarted the daemon") {
		t.Fatalf("res = %+v, daemon %+v, restarted %d, progress %q", res, res.Daemon, fake.restarted, progress)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```
go test ./cmd/monoagentcli/ -run '^(TestDaemonAfterUpdate|TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings|TestUpdateWhenAlreadyCurrentRestartsAStaleDaemon|TestUpdateAppRestartsTheDaemonAndReportsIt)$' -count=1
```
Expected: build failure: `undefined: restartBlockers`, `undefined: daemonAfterUpdate`, `res.Daemon undefined (type appUpdateResult has no field or method Daemon)`, and a type error on the two-argument `runUpdate`.

- [ ] **Step 3: Implement.** `cmd/monoagentcli/update_daemon.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/health"
	"github.com/monoes/mono-agent/internal/storage"
)

// A running daemon keeps the code it started with, so an update leaves the process that runs the
// schedules on the old version until it restarts (spec D28). `update` and `update --app` therefore
// restart a registered daemon, unless an execution is in flight: an update never interrupts a run.
// The restart goes through the service manager in this process, not through the new binary's
// `daemon restart`, which is a gated command the new binary may refuse.

// daemonUpdate is what an update did about the daemon, in `update --app --json` as "daemon".
type daemonUpdate struct {
	// Action is restarted, busy (an execution is in flight), settings (the saved API settings cannot
	// be used), unknown (the database could not be read), not_registered or failed.
	Action   string `json:"action"`
	Running  string `json:"running_version,omitempty"` // the version the daemon runs, "" when its heartbeat names none
	InFlight int    `json:"in_flight,omitempty"`       // the executions that kept it from restarting
	Via      string `json:"via,omitempty"`             // the service manager that restarted it
	Message  string `json:"message"`
}

// restartBlockers reads the database the command is pointed at, opened as doctor opens it and never
// migrated (the old binary's migrations must not run under a daemon of another version): how many
// executions the daemon with this pid owns, which a restart ends (RUNNING, and QUEUED rows it has
// claimed; a WAITING run is persisted and resumes), and whether the settings saved with `api config`
// can be used, which `daemon restart` checks first because a daemon that cannot use them starts
// without the OpenAI-compatible API. A database that is absent or unreadable is err: the caller does
// not restart what it cannot see into. A variable for tests.
var restartBlockers = func(ctx context.Context, cfg *globalConfig, pid int) (inFlight int, settingsErr, err error) {
	path := expandPath(cfg.DBPath)
	if _, err = os.Stat(path); err != nil {
		return 0, nil, err
	}
	db, err := storage.NewDatabase(path)
	if err != nil {
		return 0, nil, err
	}
	defer db.Close()
	err = db.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workflow_executions WHERE pid = ? AND status IN ('RUNNING', 'QUEUED')`, pid).Scan(&inFlight)
	if err != nil {
		return 0, nil, err
	}
	_, settingsErr = savedSettings(ctx, db.DB)
	return inFlight, settingsErr, nil
}

// daemonAfterUpdate restarts the running daemon onto newVersion, or says why not. nil means there
// was nothing to do: no daemon runs, or it already runs newVersion.
func daemonAfterUpdate(ctx context.Context, cfg *globalConfig, newVersion string) *daemonUpdate {
	hb, live := daemonhb.Read()
	if !live || !health.DaemonVersionStale(hb.Version, newVersion) {
		return nil
	}
	was := hb.Version
	if was == "" {
		was = "an older version"
	}
	d := &daemonUpdate{Running: hb.Version}
	notRestarted := fmt.Sprintf("The daemon (pid %d) runs %s and was not restarted: ", hb.PID, was)
	const keeps = " Run `monoagentcli daemon restart` when that is settled; until then it keeps that version."

	n, settingsErr, err := restartBlockers(ctx, cfg, hb.PID)
	switch {
	case err != nil:
		d.Action, d.Message = "unknown", notRestarted+fmt.Sprintf("it could not be told whether a run is in flight (%v).", err)+keeps
		return d
	case n > 0:
		d.Action, d.InFlight = "busy", n
		d.Message = notRestarted + fmt.Sprintf("%d workflow execution(s) are running in it, and an update never interrupts a run.", n) + keeps
		return d
	case settingsErr != nil:
		d.Action = "settings"
		d.Message = notRestarted + fmt.Sprintf("the settings saved with `monoagentcli api config` cannot be used, and a daemon that cannot use them starts without the OpenAI-compatible API (%v).", settingsErr) + keeps
		return d
	}

	res, err := autostart.RestartRegistered(ctx, newInstaller())
	var notRegistered *autostart.NotRegisteredError
	switch {
	case errors.As(err, &notRegistered):
		d.Action = "not_registered"
		d.Message = fmt.Sprintf("The daemon (pid %d) runs %s and is not registered for auto-start, so nothing can restart it: stop it and start `monoagentcli daemon` again, or run `monoagentcli daemon install` to have the system manage it.", hb.PID, was)
	case err != nil:
		d.Action, d.Message = "failed", notRestarted+fmt.Sprintf("the service manager failed (%v).", err)+keeps
	default:
		d.Action, d.Via = "restarted", res.Via
		d.Message = fmt.Sprintf("Restarted the daemon through %s so that it runs %s (it ran %s).", res.Via, newVersion, was)
	}
	return d
}
```

- [ ] **Step 4: Wire it into `update` and `update --app`.** In `cmd/monoagentcli/update.go`:

```diff
-			return runUpdate(cmd, args)
+			return runUpdate(cmd, cfg)
```
```diff
-func runUpdate(_ *cobra.Command, _ []string) error {
+func runUpdate(cmd *cobra.Command, cfg *globalConfig) error {
```
```diff
 	if latest == current {
 		fmt.Printf("Already on latest version (%s)\n", v)
+		// A daemon started before this binary was installed still runs the old code.
+		if d := daemonAfterUpdate(cmd.Context(), cfg, v); d != nil {
+			fmt.Println(d.Message)
+		}
 		return nil
 	}
```
```diff
 	fmt.Printf("Updated to %s\n", release.TagName)
+	if d := daemonAfterUpdate(cmd.Context(), cfg, release.TagName); d != nil {
+		fmt.Println(d.Message)
+	}
 	return nil
```
and the `Long` help (line 34) gains a paragraph:

```diff
-			"NDJSON on stderr ({\"kind\":\"line\",\"message\":…}) and the result is one JSON object on stdout.",
+			"NDJSON on stderr ({\"kind\":\"line\",\"message\":…}) and the result is one JSON object on stdout.\n\n" +
+			"A running daemon keeps the code it started with, so update restarts one that runs another version, through the " +
+			"service manager it is registered with, unless an execution is in flight or the saved `api config` settings cannot be used: " +
+			"it then says so and leaves the restart to `monoagentcli daemon restart`.",
```
In `cmd/monoagentcli/update_app.go`, give the result the field and fill it after a successful install (the desktop's update flow calls this command, so the app relays the progress line and needs no change):

```diff
 	Restart string `json:"restart,omitempty"`
-	Error   string `json:"error,omitempty"`
+	// Daemon is what the update did about a running daemon (daemonUpdate); absent when none ran
+	// or it already ran this version.
+	Daemon *daemonUpdate `json:"daemon,omitempty"`
+	Error  string        `json:"error,omitempty"`
 }
```
```diff
 		res.Error = err.Error()
-	}
+	} else if !res.UpToDate {
+		// The files are replaced; a daemon still runs the old ones. The app relays this line.
+		if res.Daemon = daemonAfterUpdate(cmd.Context(), cfg, res.NewVersion); res.Daemon != nil {
+			progress(res.Daemon.Message)
+		}
+	}
 	if cfg.JSONOutput {
```

- [ ] **Step 5: Run the tests, expect PASS.**

```
go test ./cmd/monoagentcli/ -run '^(TestDaemonAfterUpdate|TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings|TestUpdateWhenAlreadyCurrentRestartsAStaleDaemon|TestUpdateAppRestartsTheDaemonAndReportsIt)$' -count=1 -race -v
go test ./cmd/monoagentcli/ -run '^(TestUpdateAppUpToDate|TestUpdateCheckAndAppAreExclusive|TestUpdateAppJSONReportsErrors|TestUpdateCheckReportsWithoutDownloading|TestUpdateCheckNetworkFailureIsAResult)$' -count=1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./cmd/monoagentcli/
gofmt -l cmd/monoagentcli
```
Expected: the nine subtests of `TestDaemonAfterUpdate`, `TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings` and the already-current test `--- PASS`, `TestUpdateAppRestartsTheDaemonAndReportsIt` `--- SKIP` anywhere but linux/amd64 (CI runs it there); `ok`; no output from vet or gofmt.

- [ ] **Step 6: Say the limits in the report.** (1) The first update to R is made by the pre-R binary, which cannot restart anything; a daemon older than R is flagged by `doctor` until the next `update`, a restart or a reboot. (2) The in-flight count and the saved-settings check read the database the command was pointed at (`--db-path`); the registered service's daemon uses the default one. (3) Org runs are not counted: they live in `monomind org serve` processes a daemon restart does not end. (4) The update reads the database without migrating it, so the old binary's schema work never runs under a daemon of another version; a database it cannot read is `unknown`, and nothing is restarted.

- [ ] **Step 7: Commit.**

```
git add cmd/monoagentcli/update_daemon.go cmd/monoagentcli/update_daemon_test.go cmd/monoagentcli/update.go cmd/monoagentcli/update_app.go
git commit -m "feat(update): restart a daemon that runs old code unless a run is in flight" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4b: The service definitions give a stopping daemon time to finish a refresh

A24, with A20 and A22. A daemon that its service manager stops (`daemon install` over a registration, `daemon uninstall`, the manager's own restart, a logout or a shutdown) is asked to end and is killed when the manager's stop time runs out. The guard's `Close` waits for a refresh grant that is in flight and then for the key-store write of its answer: about 20 seconds with B1b's refresher, which gives up on a grant after 10, and 30 at the worst. launchd's default `ExitTimeOut` is 20 seconds, so a kill inside that window is possible, and it is the A24 case: the answer is lost, the account survives and this machine signs in again. Every definition this code writes therefore allows 35 seconds, the worst case and a margin of five: launchd's `ExitTimeOut`, systemd's `TimeoutStopSec` (its default is a distribution and user setting, which a unit does not rely on) and, on Windows, the wait of `Restart` for the daemon to let go of its lock after its task was ended (`daemonStopWait`, 10 seconds until now). A scheduled task has no stop time of its own, so that wait is the setting this code has. One constant, `stopGrace`, carries the number.

**Files:**
- Modify: `internal/autostart/autostart.go` (the constant), `internal/autostart/autostart_darwin.go` (the plist template, `renderPlist`, `Install`), `internal/autostart/autostart_linux.go` (the unit template, `renderUnit`), `internal/autostart/autostart_windows.go` (`daemonStopWait`)
- Create: `internal/autostart/stopgrace_test.go`, `internal/autostart/stop_darwin_test.go`, `internal/autostart/stop_linux_test.go`, `internal/autostart/stop_windows_test.go`

**Interfaces:**
- Consumes: `darwinPlistTemplate`, `linuxUnitTemplate`, `renderUnit` and `daemonStopWait`, which exist.
- Produces: `stopGrace`, an unexported `time.Duration` constant of 35 seconds, and `renderPlist(exe, logs string) (string, error)` in the darwin file, the counterpart of `renderUnit`, so that the plist can be read without installing it.

- [ ] **Step 1: Write the failing tests.**

Create `internal/autostart/stopgrace_test.go` with exactly this content:

```go
package autostart

import (
	"testing"
	"time"
)

// A daemon that is stopped while its account guard renews the session waits for the refresh grant in flight and
// then for the key-store write of its answer (A20, A22): B1b's refresher gives up on a grant after 10 seconds and
// the write is bounded to 10 more, so about 20 seconds, and 30 when the guard's 20-second backstop has to end the
// grant. A service manager that kills the daemon sooner loses the answer, which is the A24 case. launchd's default
// ExitTimeOut is 20 seconds, so the definitions say how long they allow.
func TestTheStopGraceCoversTheGrantAndTheKeyStoreWrite(t *testing.T) {
	if stopGrace < 35*time.Second {
		t.Fatalf("stopGrace = %v: a service manager that waits less can kill a daemon that is saving the answer of a refresh", stopGrace)
	}
}
```

Create `internal/autostart/stop_darwin_test.go` with exactly this content:

```go
//go:build darwin

package autostart

import (
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// launchd kills a job that has not ended ExitTimeOut seconds after its SIGTERM, 20 by default: less than a daemon
// needs to finish a refresh grant and save its answer. The job says how long it may take.
func TestThePlistGivesTheDaemonTimeToFinishARefresh(t *testing.T) {
	plist, err := renderPlist("/usr/local/bin/monoagentcli", "/Users/ada/.monoagent/logs")
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal([]byte(plist), new(struct{ XMLName xml.Name })); err != nil {
		t.Fatalf("the plist is not well-formed XML: %v\n%s", err, plist)
	}
	m := regexp.MustCompile(`<key>ExitTimeOut</key>\s*<integer>(\d+)</integer>`).FindStringSubmatch(plist)
	if m == nil {
		t.Fatalf("the plist sets no ExitTimeOut, so launchd kills a stopping daemon after its default 20 seconds:\n%s", plist)
	}
	seconds, _ := strconv.Atoi(m[1])
	if got := time.Duration(seconds) * time.Second; got < stopGrace {
		t.Fatalf("ExitTimeOut is %v, want at least %v", got, stopGrace)
	}
	for _, want := range []string{"<string>/usr/local/bin/monoagentcli</string>", "<string>daemon</string>", "/Users/ada/.monoagent/logs/daemon.log", "<key>KeepAlive</key>"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("the plist lost %q:\n%s", want, plist)
		}
	}
}
```

Create `internal/autostart/stop_linux_test.go` with exactly this content:

```go
//go:build linux

package autostart

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// systemd kills a unit that has not stopped TimeoutStopSec after its SIGTERM. Its default is a distribution and
// user setting, which a unit that needs time to finish a refresh does not rely on: the unit says how long it
// allows, in its [Service] section.
func TestTheUnitGivesTheDaemonTimeToFinishARefresh(t *testing.T) {
	unit, err := renderUnit("/usr/local/bin/monoagentcli")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^TimeoutStopSec=(\d+)$`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatalf("the unit sets no TimeoutStopSec:\n%s", unit)
	}
	seconds, _ := strconv.Atoi(m[1])
	if got := time.Duration(seconds) * time.Second; got < stopGrace {
		t.Fatalf("TimeoutStopSec is %v, want at least %v", got, stopGrace)
	}
	at := strings.Index(unit, "TimeoutStopSec=")
	if service, install := strings.Index(unit, "[Service]"), strings.Index(unit, "[Install]"); at < service || at > install {
		t.Fatalf("TimeoutStopSec is not in the [Service] section:\n%s", unit)
	}
}
```

Create `internal/autostart/stop_windows_test.go` with exactly this content:

```go
//go:build windows

package autostart

import "testing"

// A scheduled task has no stop time of its own: how long Restart waits for the daemon to let go of its lock after
// the task was ended is the setting this code has, and it allows a daemon as long as the other managers do.
func TestRestartWaitsForAStoppingDaemonAsLongAsTheOtherManagersAllowIt(t *testing.T) {
	if daemonStopWait < stopGrace {
		t.Fatalf("daemonStopWait = %v, want at least %v", daemonStopWait, stopGrace)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/autostart/ -run '^(TestTheStopGraceCoversTheGrantAndTheKeyStoreWrite|TestThePlistGivesTheDaemonTimeToFinishARefresh|TestTheUnitGivesTheDaemonTimeToFinishARefresh)$' -count=1
```

Expected: FAIL, a build error. On macOS: `undefined: stopGrace` and `undefined: renderPlist`; on Linux: `undefined: stopGrace`; on Windows the Windows test names `undefined: stopGrace` the same way.

- [ ] **Step 3: Implement.**

In `internal/autostart/autostart.go`, replace this text:

```go
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)
```

with:

```go
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)
```

In `internal/autostart/autostart.go`, replace this text:

```go
// Installer registers and removes the per-user auto-start entry. Each OS
```

with:

```go
// stopGrace is how long a service manager must let the daemon stop before it kills it. A daemon that is
// stopped while its account guard renews the session waits for the refresh grant in flight and then for the
// key-store write of its answer (spec A20, A22): about 20 seconds with B1a's and B1b's timeouts, 30 at the
// worst. A manager that kills it sooner loses the answer, which is the A24 case: the account survives and this
// machine signs in again. launchd's default ExitTimeOut is 20 seconds, so every definition says how long it
// allows, and the Windows wait of Restart is the same number.
const stopGrace = 35 * time.Second

// Installer registers and removes the per-user auto-start entry. Each OS
```

In `internal/autostart/autostart_darwin.go`, replace this text:

```go
	"strconv"
	"strings"
	"text/template"
)
```

with:

```go
	"strconv"
	"strings"
	"text/template"
	"time"
)
```

In `internal/autostart/autostart_darwin.go`, replace this text:

```go
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
```

with:

```go
	<key>KeepAlive</key>
	<true/>
	<key>ExitTimeOut</key>
	<integer>{{.ExitTimeOut}}</integer>
	<key>StandardOutPath</key>
```

In `internal/autostart/autostart_darwin.go`, replace this text:

```go
func plistPath() (string, error) {
```

with:

```go
// renderPlist returns the launchd job for the binary at exe, logging under logs. ExitTimeOut is how long launchd
// waits after SIGTERM before it kills the daemon: stopGrace, where launchd's default is 20 seconds.
func renderPlist(exe, logs string) (string, error) {
	var b strings.Builder
	tmpl := template.Must(template.New("plist").Parse(darwinPlistTemplate))
	err := tmpl.Execute(&b, struct {
		Label, Exe, LogDir string
		ExitTimeOut        int
	}{Label, exe, logs, int(stopGrace / time.Second)})
	return b.String(), err
}

func plistPath() (string, error) {
```

In `internal/autostart/autostart_darwin.go`, replace this text:

```go
	tmpl := template.Must(template.New("plist").Parse(darwinPlistTemplate))
	f, err := os.Create(path)
	if err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}
	execErr := tmpl.Execute(f, struct{ Label, Exe, LogDir string }{Label, exe, logs})
	closeErr := f.Close()
	if execErr != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, execErr)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, closeErr)
	}
```

with:

```go
	plist, err := renderPlist(exe, logs)
	if err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}
```

In `internal/autostart/autostart_linux.go`, replace this text:

```go
	"strings"
	"text/template"
)
```

with:

```go
	"strings"
	"text/template"
	"time"
)
```

In `internal/autostart/autostart_linux.go`, replace this text:

```go
Restart=on-failure
RestartSec=5
```

with:

```go
Restart=on-failure
RestartSec=5
TimeoutStopSec={{.StopSec}}
```

In `internal/autostart/autostart_linux.go`, replace this text:

```go
	if err := tmpl.Execute(&b, struct{ Exe string }{systemdQuote(exe)}); err != nil {
```

with:

```go
	if err := tmpl.Execute(&b, struct {
		Exe     string
		StopSec int
	}{systemdQuote(exe), int(stopGrace / time.Second)}); err != nil {
```

In `internal/autostart/autostart_windows.go`, replace this text:

```go
// daemonStopWait is how long Restart waits for the daemon to let go of its lock after its task
// was ended.
const daemonStopWait = 10 * time.Second
```

with:

```go
// daemonStopWait is how long Restart waits for the daemon to let go of its lock after its task
// was ended: as long as the other service managers allow a daemon to stop (stopGrace), because a
// daemon that is finishing a refresh grant and saving its answer needs that time.
const daemonStopWait = stopGrace
```

- [ ] **Step 4: Run the tests, expect PASS.**

```bash
go test ./internal/autostart/ -count=1 -race -v
```

Expected: `ok`, and `--- PASS` for `TestTheStopGraceCoversTheGrantAndTheKeyStoreWrite` and for the test of the platform the command runs on (`TestThePlistGivesTheDaemonTimeToFinishARefresh` on macOS, `TestTheUnitGivesTheDaemonTimeToFinishARefresh` on Linux); the Windows test is compiled by Step 5 and runs only on Windows.

- [ ] **Step 5: Vet, format and cross-vet.**

```bash
go vet ./internal/autostart/ && gofmt -l internal/autostart
GOOS=linux GOARCH=amd64 go vet ./internal/autostart/
GOOS=windows GOARCH=amd64 go vet ./internal/autostart/
GOOS=darwin GOARCH=arm64 go vet ./internal/autostart/
```

Expected: no output. Each cross-vet type-checks the files and the test of that platform, which CI (Linux only) does not run.

- [ ] **Step 6: Say the limits in the report.** (1) A registration made before this change keeps its old definition until `daemon install` runs again (it replaces the registration): `update` restarts the old definition and does not rewrite it, so the longer stop time reaches an existing install only then, and until then a kill inside launchd's 20 seconds is the A24 case. (2) The stop time applies when the manager stops the daemon through its own stop path (`launchctl bootout`, `systemctl --user stop` or `restart`, a logout, a shutdown). `Restart` on macOS is `launchctl kickstart -k`, which launchd documents as killing the running instance, so a restart that lands inside a refresh is the A24 case whatever `ExitTimeOut` says; the marker covers it. (3) Not changed here: the desktop's own grace period for the CLI children it stops (`healthGracePeriod`, 15 seconds, `wails-app/app_health.go`) is not a service definition. A child that is stopped inside a refresh is the same case, and B4 may want the same number.

- [ ] **Step 7: Commit.**

```
git add internal/autostart/autostart.go internal/autostart/autostart_darwin.go internal/autostart/autostart_linux.go internal/autostart/autostart_windows.go internal/autostart/stopgrace_test.go internal/autostart/stop_darwin_test.go internal/autostart/stop_linux_test.go internal/autostart/stop_windows_test.go
git commit -m "feat(autostart): give a stopping daemon 35 seconds to finish a refresh before its service manager kills it" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 5: The release guard

**Files:**
- Create: `scripts/check-release-tags.sh`, `scripts/check-release-tags-test.sh`
- Modify: `.github/workflows/release.yml` (a new job before `release:` at line 375, and its `needs`, line 376), `.github/workflows/ci.yml` (a new job before the comment `# Closes issue #20's OpenAPI acceptance criterion`: line 95 at master, below B5c's `account-smoke` job once that has merged)

**Interfaces:**
- Consumes: `go version -m <binary>` (prints a `build	-tags=a,b` line for every Go binary of any OS), `unzip`, `tar`. The shipped Go binaries are eleven: `monoagentcli-{linux-amd64,linux-arm64,windows-amd64.exe,darwin-amd64,darwin-arm64}`; `MonoAgent-windows-amd64.exe` and `monoagentcli-windows-amd64-bundled.exe`; the app and `monoagentcli` inside `MonoAgent-linux-amd64.tar.gz`; the app and `monoagentcli` inside `MonoAgent-darwin-arm64.zip` (`MonoAgent.app/Contents/MacOS/`). `gh release view v0.106.1 -R monoes/mono-agent --json assets --jq '.assets[].name'` lists exactly these nine assets plus `monoagent-chrome-extension.zip` and `SHA256SUMS.txt`, which hold no Go binary.
- Produces: `bash scripts/check-release-tags.sh [--min N] <file-or-folder>...` (exit 1 when a binary lists `devaccount`, or fewer than N Go binaries were found; exit 2 on bad usage), the release job `release-guard` and the CI job `release-guard-test`.

- [ ] **Step 1: Write the test script first.** `scripts/check-release-tags-test.sh` builds a tiny Go program with and without the tag, loose and inside a tarball and a zip laid out like the real bundles:

```bash
#!/usr/bin/env bash
# Tests scripts/check-release-tags.sh against real Go binaries built with and without the
# devaccount tag, loose and inside the archives the release ships (a .tar.gz like the Linux
# app's, a .zip like the macOS app's). Runs in CI (job release-guard-test) and locally:
#
#   bash scripts/check-release-tags-test.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
guard() { bash "$here/check-release-tags.sh" "$@"; }
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# A tiny module, built several ways. The tag changes nothing in the program: the guard reads the
# build settings the toolchain stamps into every binary.
mkdir "$work/mod"
printf 'module example.com/guardtest\n\ngo 1.26.0\n' > "$work/mod/go.mod"
printf 'package main\n\nfunc main() {}\n' > "$work/mod/main.go"
build() { (cd "$work/mod" && go build "$@" ) || fail "go build $*"; }
mkdir "$work/bin"
build -o "$work/bin/plain" .
build -tags devaccount -o "$work/bin/tagged" .
build -tags desktop,production,devaccount -o "$work/bin/tagged-among-others" .
build -tags desktop,production -o "$work/bin/other-tags" .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 build -tags devaccount -o "$work/bin/tagged.exe" .

# expect_ok / expect_fail run the guard and check its exit status (and, for a failure, that it
# names the offending file).
expect_ok() {
  local name="$1"; shift
  guard "$@" > "$work/out" 2>&1 || { cat "$work/out"; fail "$name: the guard failed"; }
  echo "ok: $name"
}
expect_fail() {
  local name="$1" culprit="$2"; shift 2
  if guard "$@" > "$work/out" 2>&1; then cat "$work/out"; fail "$name: the guard passed a tagged binary"; fi
  grep -q "$culprit" "$work/out" || { cat "$work/out"; fail "$name: the failure should name $culprit"; }
  echo "ok: $name"
}

expect_ok "an untagged binary" "$work/bin/plain"
expect_ok "other tags are fine" "$work/bin/other-tags"
expect_fail "a tagged binary" tagged "$work/bin/tagged"
expect_fail "the tag among others" tagged-among-others "$work/bin/tagged-among-others"
expect_fail "a tagged Windows binary" tagged.exe "$work/bin/tagged.exe"

# A folder: one tagged binary among clean ones, and a file that is not a binary.
mkdir "$work/flat"
cp "$work/bin/plain" "$work/flat/monoagentcli-linux-amd64"
printf 'not a binary\n' > "$work/flat/SHA256SUMS.txt"
expect_ok "a folder of clean binaries" "$work/flat"
cp "$work/bin/tagged" "$work/flat/monoagentcli-darwin-arm64"
expect_fail "a folder with one tagged binary" monoagentcli-darwin-arm64 "$work/flat"

# The archives of the desktop app: the app's executable and the CLI beside it.
mkdir -p "$work/linux-pkg" "$work/MonoAgent.app/Contents/MacOS"
cp "$work/bin/plain" "$work/linux-pkg/MonoAgent-linux-amd64"
cp "$work/bin/plain" "$work/linux-pkg/monoagentcli"
tar -czf "$work/clean.tar.gz" -C "$work/linux-pkg" MonoAgent-linux-amd64 monoagentcli
expect_ok "a clean tarball" "$work/clean.tar.gz"
cp "$work/bin/tagged" "$work/linux-pkg/monoagentcli"
tar -czf "$work/sidecar.tar.gz" -C "$work/linux-pkg" MonoAgent-linux-amd64 monoagentcli
expect_fail "a tagged CLI sidecar in a tarball" "sidecar.tar.gz:monoagentcli" "$work/sidecar.tar.gz"
cp "$work/bin/tagged" "$work/MonoAgent.app/Contents/MacOS/monoagent-ui"
cp "$work/bin/plain" "$work/MonoAgent.app/Contents/MacOS/monoagentcli"
(cd "$work" && zip -qr app.zip MonoAgent.app)
expect_fail "a tagged app executable in a zip" "app.zip:MonoAgent.app/Contents/MacOS/monoagent-ui" "$work/app.zip"

# --min: a layout that changed must not pass by checking nothing.
expect_ok "enough binaries" --min 2 "$work/clean.tar.gz"
if guard --min 3 "$work/clean.tar.gz" > "$work/out" 2>&1; then fail "--min 3 passed with two binaries"; fi
grep -q "expected at least 3" "$work/out" || fail "--min should say how many it expected"
mkdir "$work/empty"
if guard "$work/empty" > "$work/out" 2>&1; then fail "a folder with no binary passed"; fi
echo "ok: --min"

echo "check-release-tags test: ok"
```

- [ ] **Step 2: Run it and watch it fail.** `bash scripts/check-release-tags-test.sh`. Expected: the first `expect_ok` fails with `bash: <path>/scripts/check-release-tags.sh: No such file or directory` and `FAIL: an untagged binary: the guard failed`.

- [ ] **Step 3: Write the guard.** `scripts/check-release-tags.sh`:

```bash
#!/usr/bin/env bash
# Fails when a Go binary that is about to ship was built with the devaccount tag (spec D11, D24):
# the tag trusts a development signing key anyone can sign with. It reads the build settings every
# Go binary carries (`go version -m`), whatever OS it was built for.
#
#   scripts/check-release-tags.sh [--min N] <file-or-folder>...
#
# A folder is searched; a .tar.gz, .tgz or .zip is unpacked and searched too, so the desktop app's
# executable and the CLI bundled with it are covered. Files that are not Go binaries are ignored.
# --min N fails when fewer than N Go binaries were found, so a release whose layout changed cannot
# pass by checking nothing.
set -euo pipefail

forbidden="devaccount"
min=1
if [ "${1:-}" = "--min" ]; then
  min="${2:?--min needs a number}"
  shift 2
fi
[ "$#" -gt 0 ] || { echo "usage: check-release-tags.sh [--min N] <file-or-folder>..." >&2; exit 2; }
command -v go >/dev/null || { echo "check-release-tags: go is required" >&2; exit 2; }

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
checked=0
bad=0

# check inspects one file: a Go binary is counted and judged, an archive is unpacked and each
# file inside is checked, anything else is skipped. label is the name to report.
check() {
  local f="$1" label="$2" info tags dir
  case "$f" in
    *.tar.gz | *.tgz | *.zip)
      dir="$(mktemp -d "$scratch/unpack.XXXXXX")"
      if [ "${f##*.}" = "zip" ]; then unzip -q "$f" -d "$dir"; else tar -xzf "$f" -C "$dir"; fi
      while IFS= read -r inner; do check "$inner" "$label:${inner#"$dir"/}"; done < <(find "$dir" -type f | sort)
      return
      ;;
  esac
  info="$(go version -m "$f" 2>/dev/null)" || return 0
  [ -n "$info" ] || return 0
  checked=$((checked + 1))
  tags="$(printf '%s\n' "$info" | awk -F'\t' '$2 == "build" && $3 ~ /^-tags=/ { sub(/^-tags=/, "", $3); print $3 }')"
  case ",$tags," in
    *",$forbidden,"*)
      echo "::error::$label was built with the $forbidden tag (-tags=$tags): a release must never carry it" >&2
      bad=$((bad + 1))
      ;;
    *) echo "ok: $label (tags: ${tags:-none})" ;;
  esac
}

for arg in "$@"; do
  if [ -d "$arg" ]; then
    while IFS= read -r f; do check "$f" "${f#"$arg"/}"; done < <(find "$arg" -type f | sort)
  elif [ -f "$arg" ]; then
    check "$arg" "$(basename "$arg")"
  else
    echo "check-release-tags: $arg does not exist" >&2
    exit 2
  fi
done

if [ "$bad" -gt 0 ]; then
  echo "check-release-tags: $bad binary(ies) carry the $forbidden tag" >&2
  exit 1
fi
if [ "$checked" -lt "$min" ]; then
  echo "::error::check-release-tags: found $checked Go binaries, expected at least $min: the release layout changed, or an archive did not unpack" >&2
  exit 1
fi
echo "check-release-tags: $checked Go binaries, none carries the $forbidden tag"
```

- [ ] **Step 4: Run the test, expect PASS.** `bash scripts/check-release-tags-test.sh`. Expected: twelve `ok:` lines and `check-release-tags test: ok`. Then check it on the real CLI, built both ways to a scratch path (never `./monoagentcli` in the repo root):

```
go build -o "${TMPDIR:-/tmp}/cli-plain" ./cmd/monoagentcli
go build -tags devaccount -o "${TMPDIR:-/tmp}/cli-dev" ./cmd/monoagentcli
bash scripts/check-release-tags.sh "${TMPDIR:-/tmp}/cli-plain"
bash scripts/check-release-tags.sh "${TMPDIR:-/tmp}/cli-dev"
```
Expected: the first prints `ok: cli-plain (tags: none)` and `1 Go binaries, none carries the devaccount tag`, exit 0; the second prints `::error::cli-dev was built with the devaccount tag (-tags=devaccount): a release must never carry it`, exit 1. The flags of the release builds change nothing: `go version -m` keeps the build line under `-s -w` and `-trimpath`, and `-tags=webkit2_41,devaccount` is reported and exits 1.

- [ ] **Step 5: Wire it into `release.yml`.** The guard gets its own job so that a tagged binary fails before the `release` job's approval gate is even reached. Insert before `# ─── 3. Publish GitHub Release` (line 374):

```yaml
  # ─── 2g. Release guard: no shipped Go binary carries the devaccount tag ────
  # The tag trusts a development signing key and points the account gate at a fake monoes.me
  # (spec D11, D24), so a binary built with it must never be published. This reads the build
  # settings of every Go binary in every artifact (`go version -m`, scripts/check-release-tags.sh),
  # the archives of the desktop apps included. Eleven ship: five CLI assets, the Windows app and its
  # bundled CLI, the Linux tarball's app and CLI, the macOS bundle's app and CLI.
  release-guard:
    needs: [build-cli, build-cli-macos, build-macos-arm64, build-windows, build-linux]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          path: artifacts/
      - name: No shipped Go binary carries the devaccount tag
        run: bash scripts/check-release-tags.sh --min 11 artifacts/

```
and make `release` wait for it (line 376): append `, release-guard` to its `needs` list. Validate: `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/release.yml')); print(d['jobs']['release']['needs']); print(d['jobs']['release-guard']['needs'])"`. Expected: the first list ends with `'release-guard'`; the second lists the five build jobs.

- [ ] **Step 6: Run the guard's own test in CI.** In `.github/workflows/ci.yml`, insert before the comment `# Closes issue #20's OpenAPI acceptance criterion` (line 95 at master; B5c merged before this plan and its `account-smoke` job sits above that comment, so insert below it):

```yaml
  # The release guard (scripts/check-release-tags.sh) keeps the devaccount tag out of releases: this
  # builds a tiny program with and without the tag, loose and inside a tarball and a zip, and checks
  # that the guard passes and fails as it should (spec D11, D24).
  release-guard-test:
    name: Release guard test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - name: Test the release guard
        run: bash scripts/check-release-tags-test.sh

```
Validate: `python3 -c "import yaml; print(list(yaml.safe_load(open('.github/workflows/ci.yml'))['jobs']))"`. Expected: the list contains `doctor-smoke`, `account-smoke`, `release-guard-test` and `openapi-lint`, in that order.

- [ ] **Step 7: Commit.**

```
git add scripts/check-release-tags.sh scripts/check-release-tags-test.sh .github/workflows/release.yml .github/workflows/ci.yml
git commit -m "ci(release): fail a release whose binaries carry the devaccount tag" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 6: The first run of R adopts an older library login

Spec §8 step 3 and D23: a library login that already exists, on any profile, is exchanged for the machine session on the first run of R, so that nobody who is logged in to the library has to sign in again. B1b provides `library.AdoptIntoAccount` and proves what it does (`internal/library/adopt.go`: a no-op while dormant and when somebody has signed in, never a refusal, a spent refresh token written back; a session with no token that was not refused, the clock-guard record of A25, is nobody's login) and leaves the call to this plan; nothing else calls it, so without this task adoption never happens. The danger is the one plan A's spike S2 records (spec A7): monoes.me answers a spent refresh token with `invalid_grant` and then ends every refresh token of the account, on every machine. `AdoptIntoAccount` removes an older login from the vault once monoes.me has given a verdict, and also when the exchange went out and its answer never arrived (A24: the token may be spent), and keeps one only when the failure cannot have spent it (nothing was sent, or monoes.me answered with an error status); a caller that retried at every command would make an implicit call before every command of an offline machine: the wiring makes one try per database, ever, claimed before it is made (B1b's Task 9 assumes exactly this).

**Files:**
- Create: `cmd/monoagentcli/account_adopt.go`, `cmd/monoagentcli/account_adopt_test.go`
- Modify: `cmd/monoagentcli/root.go` (the root command's `PersistentPreRun`, lines 45-53: one call)

**Interfaces:**
- Consumes: `library.AdoptIntoAccount(ctx context.Context, db *sql.DB, g *account.Guard) (adopted bool, err error)` (B1b); `account.Current()`, `(*Guard).Status()`, `account.EnforceDate()`, `account.StateLocked`, `account.ReasonNotLoggedIn`, `account.Install(g)`, `account.SetEnforceFromForTest(t, at)`; `accounttest.Install(t, mode)` with `SignedIn`, `LockedNoLogin`, `LockedRefused` and `Dormant` (index §3.3); from B2's `account_gate.go`: `applyClassification(root)`, `commandClass(c *cobra.Command) string`, `classOpen`; `storage.NewDatabase(path)`, `expandPath`, `testdb.Path`.
- Produces: `adoptFirstRun(cmd *cobra.Command, cfg *globalConfig)`, the variable `adoptOlderLogin` (a seam for tests), and the settings row `account_adoption` (the time of the one try, RFC 3339).

- [ ] **Step 1: Write the failing tests.** `cmd/monoagentcli/account_adopt_test.go`:

```go
package main

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/testdb"
)

// adoptRig is a migrated database and a counter in place of the library's adoption: what the wiring
// decides is whether to call it, and internal/library proves what the adoption does (B1b). With no
// session the machine is in the warn period (a date ahead), where release R's first run happens,
// unless afterTheDate leaves the enforcement date in the past.
type adoptRig struct {
	dbPath string
	calls  atomic.Int32
}

func newAdoptRig(t *testing.T, mode accounttest.Mode, afterTheDate bool) *adoptRig {
	t.Helper()
	accounttest.Install(t, mode)
	if mode == accounttest.LockedNoLogin && !afterTheDate {
		account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
	}
	r := &adoptRig{dbPath: testdb.Path(t)}
	was := adoptOlderLogin
	adoptOlderLogin = func(context.Context, *sql.DB, *account.Guard) (bool, error) { r.calls.Add(1); return false, nil }
	t.Cleanup(func() { adoptOlderLogin = was })
	return r
}

// adoptionCommand finds a command of the real tree, classified the way the CLI gate classifies it.
func adoptionCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	root := newRootCmd()
	applyClassification(root)
	c, _, err := root.Find(args)
	if err != nil || c == root {
		t.Fatalf("no command %v: %v", args, err)
	}
	return c
}

func TestAdoptFirstRun(t *testing.T) {
	for _, c := range []struct {
		name    string
		mode    accounttest.Mode
		after   bool // the date has passed: a gated command would have been refused before it got here
		args    []string
		noDB    bool
		noGuard bool
		want    int32
	}{
		{"a gated command on a machine with no session", accounttest.LockedNoLogin, false, []string{"workflow", "list"}, false, false, 1},
		{"a serving command after the date, which the gate lets through", accounttest.LockedNoLogin, true, []string{"daemon"}, false, false, 1},
		{"an open command", accounttest.LockedNoLogin, false, []string{"version"}, false, false, 0},
		{"the gate is dormant", accounttest.Dormant, false, []string{"workflow", "list"}, false, false, 0},
		{"a session exists", accounttest.SignedIn, false, []string{"workflow", "list"}, false, false, 0},
		{"a session that monoes.me refused", accounttest.LockedRefused, false, []string{"workflow", "list"}, false, false, 0},
		{"no database yet", accounttest.LockedNoLogin, false, []string{"workflow", "list"}, true, false, 0},
		{"no guard installed", accounttest.LockedNoLogin, false, []string{"workflow", "list"}, false, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newAdoptRig(t, c.mode, c.after)
			if c.noDB {
				r.dbPath = filepath.Join(t.TempDir(), "absent.db")
			}
			if c.noGuard {
				account.Install(nil)
			}
			adoptFirstRun(adoptionCommand(t, c.args...), &globalConfig{DBPath: r.dbPath})
			if got := r.calls.Load(); got != c.want {
				t.Fatalf("the adoption was called %d times, want %d", got, c.want)
			}
			if _, err := os.Stat(r.dbPath); c.noDB && !os.IsNotExist(err) {
				t.Fatalf("the wiring created a database for nothing: %v", err)
			}
		})
	}
}

// A try is made once per database, ever: presenting the older login's refresh token again could end
// every login of the account, so a try that failed is not repeated, and two processes that start
// together make one try between them.
func TestAdoptFirstRunTriesOnce(t *testing.T) {
	r := newAdoptRig(t, accounttest.LockedNoLogin, false)
	cfg, cmd := &globalConfig{DBPath: r.dbPath}, adoptionCommand(t, "workflow", "list")
	for i := 0; i < 3; i++ {
		adoptFirstRun(cmd, cfg)
	}
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("three runs made %d tries, want 1", got)
	}

	r = newAdoptRig(t, accounttest.LockedNoLogin, false)
	cfg = &globalConfig{DBPath: r.dbPath}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); adoptFirstRun(cmd, cfg) }()
	}
	wg.Wait()
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("eight processes starting together made %d tries, want 1", got)
	}
}

// The wiring itself: the root command makes the try before a gated command runs, and not before an
// open one. This is the test that fails when the call in root.go is removed.
func TestTheRootCommandAdoptsBeforeAGatedCommandRuns(t *testing.T) {
	r := newAdoptRig(t, accounttest.LockedNoLogin, false)
	for _, c := range []struct {
		args []string
		want int32
	}{{[]string{"version"}, 0}, {[]string{"workflow", "list"}, 1}, {[]string{"workflow", "list"}, 1}} {
		root := newRootCmd()
		applyClassification(root)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"--db-path", r.dbPath}, c.args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := r.calls.Load(); got != c.want {
			t.Fatalf("after %v the adoption was called %d times, want %d", c.args, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```
go test ./cmd/monoagentcli/ -run '^(TestAdoptFirstRun|TestAdoptFirstRunTriesOnce|TestTheRootCommandAdoptsBeforeAGatedCommandRuns)$' -count=1
```
Expected: build failure: `undefined: adoptFirstRun`, `undefined: adoptOlderLogin`, `undefined: adoptionSetting`.

- [ ] **Step 3: Implement.** `cmd/monoagentcli/account_adopt.go`:

```go
package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/storage"
)

// adoptionSetting is the settings row that says a database has had its one try (spec D23).
const adoptionSetting = "account_adoption"

// adoptOlderLogin is library.AdoptIntoAccount, a variable so that a test can see whether the wiring
// calls it without a monoes.me to call.
var adoptOlderLogin = library.AdoptIntoAccount

// adoptFirstRun is the first run of release R (spec section 8 step 3, D23): a library login that
// already exists, on any profile, is exchanged for the machine session, so that nobody who is logged
// in to the library has to sign in again. It runs before a gated or serving command (an open command
// calls monoes.me only when asked to) and does nothing while the gate is dormant (D22), with no guard
// installed, when somebody has signed in (the status is not locked, not_logged_in: the clock-guard
// record that the guard writes from the date on, A25, is that status and does not stop it), or when
// the database is not there yet (a new machine has no older login, and a command must not create
// the database for this).
//
// It tries once per database, ever. The exchange spends the older login's refresh token, and
// monoes.me answers a spent one with invalid_grant and then ends every refresh token of the account
// (plan A, spike S2), so B1b's AdoptIntoAccount removes an older login whose exchange went out and
// was not answered (A24), and a try that fails is never repeated: the user signs in once more. The try is
// claimed before it is made, with one row inserted only if absent, so two processes that start
// together make one try between them. Whatever it ends in, the command goes on. Once the exchange is
// sent it is completed even if the command's context is cancelled (A20, B1b's exchangeOlder): the
// session is stored and the vault entry updated, so an interrupted try leaves no spent refresh token
// behind. A context cancelled before the exchange is sent stops the try with the older login
// untouched, and the claim stays consumed.
func adoptFirstRun(cmd *cobra.Command, cfg *globalConfig) {
	if commandClass(cmd) == classOpen || account.EnforceDate().IsZero() {
		return
	}
	g := account.Current()
	if g == nil {
		return
	}
	if st := g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
		return
	}
	path := expandPath(cfg.DBPath)
	if _, err := os.Stat(path); err != nil {
		return
	}
	db, err := storage.NewDatabase(path)
	if err != nil {
		return
	}
	defer db.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	claim, err := db.DB.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`,
		adoptionSetting, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n == 1 { // otherwise this database has had its try, or another process is making it
		_, _ = adoptOlderLogin(ctx, db.DB, g)
	}
}
```

and in `cmd/monoagentcli/root.go`, at the end of the root command's `PersistentPreRun`, which runs after the CLI gate and before the command (only `setup` and `doctor`, both open, define one of their own):

```diff
 			// Installed automation packages, before anything reads actions.
 			bootAutomationsFor(cmd)
+			// The first run of release R turns an older library login into the machine
+			// session (spec D23); it does nothing before the gate is on.
+			adoptFirstRun(cmd, cfg)
 		},
```

- [ ] **Step 4: Run the tests, expect PASS, with the race detector for the concurrent one.**

```
go test ./cmd/monoagentcli/ -run '^(TestAdoptFirstRun|TestAdoptFirstRunTriesOnce|TestTheRootCommandAdoptsBeforeAGatedCommandRuns)$' -count=1 -race -v
go vet ./cmd/monoagentcli/
gofmt -l cmd/monoagentcli
```
Expected: the eight subtests of `TestAdoptFirstRun` and the two other tests `--- PASS`; `ok`; no output from vet or gofmt.

- [ ] **Step 5: Say the limits in the report.** (1) The CLI gate runs before anything, this included, so a gated one-shot command that first runs after the date is refused before an adoption can happen: its user signs in (`account login`), and the older library login stays where it is, usable by an older binary. A serving command (`daemon`, `httpapi`, `mcp` and `extension serve`; `org serve` is a launcher and stays gated, spec A1) passes the gate while locked, so one that first runs after the date does adopt (the gate's pass has just written the clock-guard record, A25, and the adoption goes ahead over it: B1b's `signedIn`). Every machine that updates during the warn period adopts at its first gated or serving command, the restarted daemon included. (2) One try per database: a machine that was offline at its first run is not retried, so its user signs in once. (3) An open command never adopts, so `account status`, `doctor` and `update` stay free of implicit calls. (4) The desktop has no adoption of its own: the first gated command it runs through the CLI adopts. (5) The real-binary check of the wiring is Task 8 Step 4's dry run against production: B5c merges before this plan and cannot test code that is not there. (6) `AdoptIntoAccount` removes the older login from the vault when it is adopted, dead, or possibly spent (the exchange went out and was not answered, or was answered and its new refresh token could not be stored: A24, A24(d)), so an older binary cannot present it later; a login whose failure cannot have spent it (nothing was sent, or an error status) stays, and this wiring does not retry it (limit 2). (7) A Ctrl-C during the try: the exchange runs on a context that the command's cancellation does not reach (spec A20, B1b's `exchangeOlder`), so once it is sent it is completed, the session stored and the vault entry updated, and the command ends after that: the grant, then the key-store write of the new refresh token, then the vault update, each bounded (the grant at about 10 seconds with B1b's refresher and 20 at the worst, the other two at most 10 seconds each); a Ctrl-C before it is sent stops the try with the older login untouched, and the claim is not given back (limit 2).

- [ ] **Step 6: Commit.**

```
git add cmd/monoagentcli/account_adopt.go cmd/monoagentcli/account_adopt_test.go cmd/monoagentcli/root.go
git commit -m "feat(account): the first run of release R adopts an older library login, once" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Prove the wiring test can fail.** Delete the line `adoptFirstRun(cmd, cfg)` from `cmd/monoagentcli/root.go` with the `Edit` tool (it is unique), run the command of Step 4 and expect `TestTheRootCommandAdoptsBeforeAGatedCommandRuns` to FAIL with `after [workflow list] the adoption was called 0 times, want 1`; the other tests still pass, because they call the function directly. Then `git checkout -- cmd/monoagentcli/root.go` and run again: PASS.

### Task 7: The audit of real-binary consumers (spike S4) and the rehearsal of the date

After the date, every real binary that runs a gated command needs a sign-in, so anything in the repository that builds the CLI and runs one breaks on that day: the desktop's Go-side tests, the manual e2e scripts, a maintainer's script, and one CI script that counts the account's own doctor row as an unhealthy core. This task finds them, adapts them, and rehearses the day.

**Files:**
- Modify: `wails-app/app_workflow_io_test.go` (`buildTestCLI`, lines 38-54), `wails-app/app_chat_e2e_test.go` (lines 27-31)
- Modify: `scripts/e2e/capture-ask.sh` (line 25), `scripts/e2e/org-unification.sh` (line 26), `scripts/e2e/per-profile-browsers.sh` (line 17), `tests/e2e-automation/setup.sh` (line 13), `tests/e2e-automation/env.sh`, `tests/e2e-automation/README.md` (line 46), `scripts/library-official.sh` (line 24), `scripts/doctor-smoke.sh` (line 90)
- Append: `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md` (create it with the title line `# Account gate: spike findings` if it does not exist yet)

**Interfaces:** Consumes B5c's `MONOAGENT_DEV_ENFORCE_FROM` (index §3.6: an RFC 3339 time, read only by a binary built with `-tags devaccount`; B5c merges before R), B2's `core.monoes_account` row and Task 2's date. Produces the adapted consumers and the S4 table.

- [ ] **Step 1: Re-run the audit.** The table in Step 4 was made at master `f4441a2a`; the tree may have grown since.

```
grep -rn --include='*_test.go' -e '"go", "build"' wails-app cmd internal
grep -rn -e 'go build' scripts tests Makefile Dockerfile install.sh .github/workflows | grep -v -e 'go build ./\.\.\.' -e 'go build -tags'
grep -rln --include='*_test.go' -e 'os.Executable()' -e 'os.Args\[0\]' cmd internal wails-app
```
Expected, in order: `wails-app/app_workflow_io_test.go:48` and `wails-app/app_chat_e2e_test.go:27` (the `go list` in `cmd/monoagentcli/automation_noseed_test.go` builds nothing); the five script lines of Step 3 (`library-official.sh:24`, `capture-ask.sh:25`, `per-profile-browsers.sh:17`, `org-unification.sh:26`, `setup.sh:13`) and `README.md:46`; then lines that stay as they are: `scripts/doctor-smoke.sh:8` (a comment), `scripts/check-release-tags-test.sh:20` (Task 5's test builds a tiny program, not the CLI), `ci.yml:91` (the `doctor-smoke` build, which must stay on the default build), `Makefile` 19 and 33-37, `Dockerfile:21`, and the shipped builds of `release.yml` (lines 130-132, 160-161, 206, 265, 328), which must stay untagged; and nine test files that re-execute the test binary (`-test.run=…`) as a helper process or resolve its own path (`cmd/monoagentcli/update_app_test.go`, `doctor_env_services_test.go`, `doctor_env_accounts_test.go`, `internal/daemonhb/lock_test.go`, `internal/capturetask/lock_test.go`, `internal/monomind/proc_windows_test.go`, `internal/monomind/proc_start_linux_test.go`, `wails-app/proc_windows_test.go`, `wails-app/app_update_test.go`): none calls `run()` or `main()`. Anything else: classify it in the table of Step 4 and adapt it the way the nearest row says.

- [ ] **Step 2: Adapt the desktop's Go-side tests.** They build the CLI the app spawns (`buildTestCLI`, used by `TestExportImportRoundtripViaCLI`, `TestWorkflowBindingsThroughCLI` and `TestRestartImageWatcher_DiscoversProjectImages`) and run gated commands. Built with the dev tag, the binary reads its date from the environment, and a date that never comes keeps the tests about what they test. In `wails-app/app_workflow_io_test.go`:

```diff
-// buildTestCLI builds the repo's monoagentcli into a temp dir so subprocess
-// tests run the same binary the GUI would spawn. Skips when building is not
-// possible in this environment.
+// buildTestCLI builds the repo's monoagentcli (with the dev tag, see below) into a temp dir so
+// subprocess tests run the binary the GUI would spawn. Skips when building is not possible in
+// this environment.
```
```diff
-	build := exec.Command("go", "build", "-o", bin, "./cmd/monoagentcli")
+	build := exec.Command("go", "build", "-tags", "devaccount", "-o", bin, "./cmd/monoagentcli")
 	build.Dir = repoRoot
 	if out, err := build.CombinedOutput(); err != nil {
 		t.Skipf("building monoagentcli failed (%v): %s", err, out)
 	}
+	// The binary the app spawns is gated. Built with the dev tag it reads its enforcement date from
+	// the environment, and a date that never comes keeps these tests about what they test.
+	t.Setenv("MONOAGENT_DEV_ENFORCE_FROM", "2999-01-01T00:00:00Z")
 	return bin
```
and in `wails-app/app_chat_e2e_test.go`:

```diff
-	build := exec.Command("go", "build", "-o", cli, "./cmd/monoagentcli")
+	build := exec.Command("go", "build", "-tags", "devaccount", "-o", cli, "./cmd/monoagentcli")
 	build.Dir = ".."
 	if out, err := build.CombinedOutput(); err != nil {
 		t.Fatalf("building monoagentcli: %v\n%s", err, out)
 	}
+	t.Setenv("MONOAGENT_DEV_ENFORCE_FROM", "2999-01-01T00:00:00Z") // the dev build's enforcement date, a date that never comes
```
Run `(cd wails-app && go test ./ -run '^(TestExportImportRoundtripViaCLI|TestWorkflowBindingsThroughCLI|TestRestartImageWatcher_DiscoversProjectImages)$' -count=1)` (Linux: add `-tags webkit2_41`; `main.go` embeds the git-ignored `frontend/dist`: if it is missing, `mkdir -p wails-app/frontend/dist && printf '<!doctype html>' > wails-app/frontend/dist/index.html`). Expected: `ok` after about two minutes (the CLI is built three times).

- [ ] **Step 3: Adapt the scripts.** Five build the CLI and run gated commands (and `env.sh` is sourced by the e2e scripts that use the built binary); they are not run in CI, so nothing fails until a person runs one after the date. In `scripts/e2e/capture-ask.sh` and `scripts/e2e/per-profile-browsers.sh` (the line is the same):

```diff
-(cd "$REPO" && go build -o "$CLI" ./cmd/monoagentcli)
+# A dev build reads its enforcement date from the environment: a date that never comes keeps this
+# script about what it tests, with no sign-in (spec section 9).
+(cd "$REPO" && go build -tags devaccount -o "$CLI" ./cmd/monoagentcli)
+export MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z
```
`scripts/e2e/org-unification.sh`:

```diff
-(cd "$REPO" && go build -o "$CLI" ./cmd/monoagentcli) || { echo "build failed"; exit 1; }
+(cd "$REPO" && go build -tags devaccount -o "$CLI" ./cmd/monoagentcli) || { echo "build failed"; exit 1; }
+export MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z # a dev build's enforcement date: never (spec section 9)
```
`tests/e2e-automation/setup.sh` (line 13) and `tests/e2e-automation/env.sh` (after its long `export E2E_DIR ...` line):

```diff
-  (cd "$REPO_ROOT" && go build -o "$E2E_BIN" ./cmd/monoagentcli)
+  (cd "$REPO_ROOT" && go build -tags devaccount -o "$E2E_BIN" ./cmd/monoagentcli)
```
```diff
+
+# The binary is built with the devaccount tag (setup.sh), which reads its enforcement date from the
+# environment: a date that never comes keeps these scripts about what they test (spec section 9).
+# A binary left over from before needs E2E_BUILD=1.
+export MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z
```
`tests/e2e-automation/README.md` (line 46, the example for testing a release; for a release older than R the tag changes nothing):

```diff
-(cd ~/scratch/wt-e2e-vX.Y.Z && go build -o ~/scratch/monoagentcli-vX.Y.Z ./cmd/monoagentcli)
+(cd ~/scratch/wt-e2e-vX.Y.Z && go build -tags devaccount -o ~/scratch/monoagentcli-vX.Y.Z ./cmd/monoagentcli)
```
`scripts/library-official.sh` (line 24):

```diff
-go build -o "$work/monoagentcli" ./cmd/monoagentcli
+go build -tags devaccount -o "$work/monoagentcli" ./cmd/monoagentcli
+export MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z # a dev build's enforcement date: never (spec section 9)
```
`scripts/doctor-smoke.sh` is the one script that CI does run, is not about the gate, and still breaks on the date (B2's plan says so). After the date a machine with no sign-in gets a `fail` from B2's `core.monoes_account` row, and line 90 asserts that `setup --group core --yes` leaves no failing row, so the job would fail on the day with `setup --group core left a failing or non-core row: ["core.monoes_account"]`. The row is not required, so no exit code changes; only that assertion has to leave the account's own row out:

```diff
-jq -e 'all(.results[]; .group == "core" and .status != "fail")' "$out/setup-core.json" >/dev/null \
+# core.monoes_account fails on a machine that is not signed in once the enforcement date has passed
+# (spec section 8): it is the account's row, not a sign of an unhealthy core.
+jq -e 'all(.results[] | select(.id != "core.monoes_account"); .group == "core" and .status != "fail")' "$out/setup-core.json" >/dev/null \
```
It stays on the default build: it asserts that `doctor` writes nothing on a fresh HOME. Check the syntax of the seven: `for f in scripts/e2e/capture-ask.sh scripts/e2e/org-unification.sh scripts/e2e/per-profile-browsers.sh tests/e2e-automation/setup.sh tests/e2e-automation/env.sh scripts/library-official.sh scripts/doctor-smoke.sh; do bash -n "$f" && echo "ok $f"; done`. Expected: seven `ok` lines.

- [ ] **Step 4: Append the S4 findings.** Append to the findings file:

```markdown
## S4, real-binary consumers (from plan B5a)

Which CI scripts and tests run the real binary, and which gated commands they use (master f4441a2a). The third question of S4, which processes reach the layer-2 gate sites with no guard installed (the desktop's Go side among them), is answered by plan B3a's section `## S4, layer 2`: the desktop makes no in-process call to a gate site and runs every execution through a `monoagentcli` subprocess, which is what the `wails-app` rows below exercise.

| Consumer | What it runs | Gated? | Result |
|---|---|---|---|
| CI job `doctor-smoke`, `scripts/doctor-smoke.sh` | default-build CLI: `doctor`, `doctor fix`, `setup` | no, all open (D6) | adapted, one assertion: after the date a machine with no sign-in gets a `fail` from `core.monoes_account` (B2), which line 90 would count as an unhealthy core; it stays on the default build, because it asserts that `doctor` writes nothing on a fresh HOME |
| CI jobs `mcp-pin-guard`, `schemagen-check`, `openapi-lint`, `vuln-scan`, `extension-test`, `monomind-smoke` | a `grep` of `.mcp.json`, `go run ./cmd/schemagen -check`, a schema lint, a scanner, the extension's tests, `Handshake` (never `Exec`) and the real `monomind org validate` | no (`schemagen` calls no gate site: plan B3a's survey) | unchanged |
| CI `test`, `test-nosocial`, release `test`, `bench.yml` | `go test` binaries that call `newRootCmd()` or a package directly | layer 1 is bypassed; layer 2 fails open in a test binary (D24) | unchanged; the rehearsal of Step 5 finds stragglers |
| `wails-app` tests through `buildTestCLI` (`app_workflow_io_test.go:41`; callers `app_workflow_io_test.go:67`, `app_workflows_test.go:56`, `app_images_watch_test.go:107`), `app_chat_e2e_test.go:27` (`MONOAGENT_CHAT_E2E=1`, not in CI) | default-build CLI: `workflow import`, `list`, `save`, `image sync`, `chat` | yes | adapted: `-tags devaccount` and a date that never comes |
| `scripts/e2e/capture-ask.sh:25`, `org-unification.sh:26`, `per-profile-browsers.sh:17`; `tests/e2e-automation/setup.sh:13` and its README example; `scripts/library-official.sh:24` (`make library-official`) (manual) | build the CLI; capture, org, daemon, `mcp --grant`, `extension serve`, `record analyze`, `automation pack` | yes | adapted the same way |
| `Dockerfile`, `docker-compose.yml` | source build; `ENTRYPOINT daemon`; `HEALTHCHECK monoagentcli version` | `daemon` is a `serve` command: a locked container's daemon starts, logs the exact sign-in command on stderr, reports `account: locked` in `/health` and its heartbeat, and runs nothing; `version` is open | no code change: a container needs a sign-in (`account login --email`, a volume and the file keyring); B5b documents it |
| `Makefile` build targets, `install.sh` | release-like builds, a download | none run | none |

Decision: a test whose subject is not the gate runs a devaccount build with `MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z` instead of signing in (spec §9 says sign in): before the date nothing locks, and the date cannot arrive (each command prints the warning on stderr, never on stdout). Only the real-binary smoke (B5c) signs in.
```

- [ ] **Step 5: Rehearse the day the date arrives.** Everything still passes today because the date is ahead; on the day, CI and every developer see what an enforced tree does. Run it now: the baseline on the committed tree, then the same suites with the date moved into the past (one temporary line, never committed), then the difference. Two tests pin the date itself and are meant to fail when it moves (`TestEnforceDateIsTheOwnersDate`, and `b5b-docs`'s `TestChangelogNamesTheEnforcementDate`), so both runs skip them:

```
skip='^(TestEnforceDateIsTheOwnersDate|TestChangelogNamesTheEnforcementDate)$'
go test ./... -count=1 -timeout 40m -skip "$skip" 2>&1 | grep -E '^(--- FAIL|FAIL)' | sort > "${TMPDIR:-/tmp}/rehearsal-before.txt"
(cd wails-app && go test ./... -count=1 -timeout 30m 2>&1 | grep -E '^(--- FAIL|FAIL)' | sort >> "${TMPDIR:-/tmp}/rehearsal-before.txt")
perl -pi -e 's/^var enforceFrom = time\.Date\(.*\)$/var enforceFrom = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)/' internal/account/rollout.go
git diff --stat
go build -o "${TMPDIR:-/tmp}/cli-past" ./cmd/monoagentcli
bash scripts/doctor-smoke.sh "${TMPDIR:-/tmp}/cli-past" 2>&1 | tail -1
go test ./... -count=1 -timeout 40m -skip "$skip" 2>&1 | grep -E '^(--- FAIL|FAIL)' | sort > "${TMPDIR:-/tmp}/rehearsal-after.txt"
(cd wails-app && go test ./... -count=1 -timeout 30m 2>&1 | grep -E '^(--- FAIL|FAIL)' | sort >> "${TMPDIR:-/tmp}/rehearsal-after.txt")
git checkout -- internal/account/rollout.go
git diff --stat
diff "${TMPDIR:-/tmp}/rehearsal-before.txt" "${TMPDIR:-/tmp}/rehearsal-after.txt"
```
(The wails module needs `-tags webkit2_41` on Linux and the `frontend/dist` of Step 2; both runs take it, so a failure that exists on the committed tree is in both files and drops out of the diff.) Expected: the first `git diff --stat` shows exactly `internal/account/rollout.go | 2 +-`, the doctor smoke on the past-date build prints `doctor smoke: ok` (without Step 3's edit, the `::error::` line about `core.monoes_account`), the second `git diff --stat` shows nothing, and `diff` shows no line. A line it does show is a test that works only before the date: an in-process test that needs the old behaviour starts with `account.SetEnforceFromForTest(t, time.Time{})`, one that must run gated work through a real guard uses `accounttest.Install(t, accounttest.SignedIn)`, and a test that runs a built binary uses a devaccount build and `MONOAGENT_DEV_ENFORCE_FROM`. Fix each, repeat until `diff` is silent, and confirm the date line is back (`grep -n '^var enforceFrom' internal/account/rollout.go`).

- [ ] **Step 6: The usual gates.**

```
go build ./... && go vet ./... && gofmt -l . | grep -v '^wails-app/frontend/node_modules/'
go build -tags nosocial -o /dev/null ./cmd/monoagentcli
go build -tags devaccount -o /dev/null ./cmd/monoagentcli
go test ./internal/account/... ./internal/health/ -race -count=1
go test ./cmd/monoagentcli/ -run '^(TestDaemonAfterUpdate|TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings|TestUpdateWhenAlreadyCurrentRestartsAStaleDaemon|TestAdoptFirstRun|TestAdoptFirstRunTriesOnce|TestTheRootCommandAdoptsBeforeAGatedCommandRuns)$' -race -count=1
bash scripts/check-release-tags-test.sh
```
Expected: no output from `gofmt`, `ok` from the `go test` lines, and `check-release-tags test: ok`.

- [ ] **Step 7: Commit.**

```
git add wails-app/app_workflow_io_test.go wails-app/app_chat_e2e_test.go scripts/e2e/capture-ask.sh scripts/e2e/org-unification.sh scripts/e2e/per-profile-browsers.sh tests/e2e-automation/setup.sh tests/e2e-automation/env.sh tests/e2e-automation/README.md scripts/library-official.sh scripts/doctor-smoke.sh docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md
git commit -m "test(account): real-binary consumers that survive the enforcement date" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 8: What must be true before release R is merged

No code. Release R is this plan and `b5b-docs` merged together as one push, and merging it releases (every merge to master releases): the first release with a date and with implicit calls to monoes.me. Do not merge until every box is ticked; the owner decides, and nothing here pushes or merges.

- [ ] **Step 1: Everything before R shipped dormant.** `git fetch origin`, then `git log --oneline origin/master` names the merges of B1a, B1b, B2, B3a, B3b, B4, B4b and B5c (index §1, merge order), and `gh release list --limit 12` shows a release after each. `git show origin/master:internal/account/rollout.go | grep -n 'var enforceFrom'` prints `var enforceFrom = time.Time{}`: nothing before R could lock or warn anyone. `grep -rn 'MONOAGENT_DEV_ENFORCE_FROM' internal/account` finds B5c's file, which Task 7 needs, and `grep -n 'func AdoptIntoAccount' internal/library/adopt.go` finds B1b's adoption, which nothing but Task 6 calls.

- [ ] **Step 2: The server half is live, the key is pinned, the spikes are on record.** Plan A is deployed to production monoes.me: audience-bound JWTs, the `plan` claim, blocking that revokes, the hardened deploy workflow, the owner-run steps O2 (the production migration for the audience row), O3 to O5 (the signing key generated, checked, the rotation runbook rehearsed), and the refresh-token reuse window (`refreshTokenReuseInterval`, 300 seconds: spec A7). Task 1 pinned the key plan A's O3 produced and its test passes: without a pinned key every real token verifies as `key_unknown` and every install would lock, and R must not be cut. `grep -n '^## S[1-6]' docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md` prints a heading for each of S1 to S6 (S4 has two: `S4, layer 2` from B3a's Task 8 and `S4, real-binary consumers` from Task 7), and the constants in `internal/account/claims.go` are the ones S6 recorded.

- [ ] **Step 3: The date is the owner's, and still ahead.** Compare the date in `internal/account/rollout.go` with `date -u +%Y-%m-%d`: it must be at least the three weeks D9 promises users from the day of the merge. If the merge slipped, move the date by the slip in both places (Task 2) and run `go test ./internal/account/ -run '^(TestEnforceDateIsTheOwnersDate|TestEnforcedFlipsAtTheDate|TestTheClockGuardCannotPostponeTheDate)$' -count=1`: `ok`. The CHANGELOG test of `b5b-docs` (`TestChangelogNamesTheEnforcementDate`) fails until its text carries the same date.

- [ ] **Step 4: The production dry run, the one check no fake can stand in for.** Every test of this plan, and B5c's smoke, trust the fake's development key (Task 1's fixture token is the one test that uses the real key's output); only a real token from the real server proves the issuer, the audience and the client claim, and a mismatch would lock every user on the date. Build the branch to a scratch path (never `./monoagentcli` in the repo root), in a throwaway HOME:

```
go build -o "${TMPDIR:-/tmp}/r-check" ./cmd/monoagentcli
export HOME="$(mktemp -d)"
"${TMPDIR:-/tmp}/r-check" account login
"${TMPDIR:-/tmp}/r-check" --json account status
"${TMPDIR:-/tmp}/r-check" --json library list --kind automation
"${TMPDIR:-/tmp}/r-check" account logout
```
`account login` opens the browser at https://monoes.me: sign in with the owner's account. Expected from `account status`: `"state":"ok"`, `"plan":"free"`, `"valid_until"` about an hour ahead, `"enforced":false`, and `"enforce_from"` equal to the date. Expected from `library list`: exit 0 with items (the library accepts the machine session's JWT, index §3.4 item 10). A `locked` state with reason `key_unknown` means this build does not pin the key the server signs with (re-run Task 1 Step 1 and compare with O4's output; for users it is the "run `update`" case); `invalid` or `clock_skew` means the claims constants differ from production: stop, correct them from spikes S6 and S1, and never merge.

The adoption is the other half of this dry run: it needs the real server to accept an older library login's refresh token for an audience-bound token (spike S2, plan A). Make an older login with the installed pre-R binary (check `monoagentcli version` first: it must be older than R) in a fresh throwaway HOME, then let R's first command adopt it:

```
export HOME="$(mktemp -d)"
monoagentcli library login
"${TMPDIR:-/tmp}/r-check" --json workflow list > /dev/null
"${TMPDIR:-/tmp}/r-check" --json account status
monoagentcli library status
"${TMPDIR:-/tmp}/r-check" account logout
```
`library login` opens the browser: sign in with the owner's account. Expected: `account status` says `"state":"ok"` for that user without `account login` having been run, and the pre-R binary's `library status` then says it is not logged in (the adoption deleted the vault entry, so the spent refresh token is never used again). If `account status` says `locked` with `not_logged_in`, the server did not accept the older login, S2 was wrong, and every user signs in once more: a fact for the release notes and for `b5b-docs`, not a reason to hold R, because nothing locks before the date. If `library status` of the pre-R binary still says logged in after an `ok`, stop: the vault entry survived, and a later `library login` of an older binary could spend its refresh token again.

- [ ] **Step 5: CI is green on the branch**, in particular `test`, `test-nosocial`, `wails`, `doctor-smoke`, `release-guard-test` and B5c's `account-smoke`; and B5c's real-binary smoke has passed on this tree on at least one machine (its plan names the command). B5c merged before this plan and **must pass before R**: it is what proves the warn period, a block, an outage, the 24 hours, every door and every entry point with the real binary, and this plan has no test of those.

- [ ] **Step 6: One push, with the documentation.** R is this plan's commits and the commits of `b5b-docs` (claims, `ref`, CHANGELOG naming the date) in one pull request, because `release.yml` takes the release notes from `CHANGELOG.md` at the commit it tags, and D26 puts the documentation no later than the first phase that calls monoes.me implicitly. This plan alone must not be pushed. B5d (the license) is independent and not part of R.

- [ ] **Step 7: The release workflow cannot ship the tag.** `grep -n 'devaccount' .github/workflows/release.yml` prints only the lines of the `release-guard` job (B5c adds nothing to `release.yml`); `grep -n -e 'go build' -e 'wails build' .github/workflows/release.yml | grep -c devaccount` prints `0`.

- [ ] **Step 8: Releasing.** The `release` job waits for the owner's approval in its environment: approve only with `release-guard` green. After publication, check the real assets once more and try the update path on the owner's own machine:

```
gh release download <tag> -R monoes/mono-agent -D "${TMPDIR:-/tmp}/r-assets"
bash scripts/check-release-tags.sh --min 11 "${TMPDIR:-/tmp}/r-assets"
monoagentcli update
monoagentcli doctor --check services.daemon
```
Expected: `none carries the devaccount tag` (the checksum file and the extension zip hold no Go binary and are skipped); `update` prints the update line and, when a daemon is running, the sentence of Task 4 (restarted, or why not); the doctor row is `ok`, or `warn` naming the old version when the update could not restart the daemon (the first update to R is made by the pre-R binary).

## Acceptance coverage

The real-binary smoke is B5c's (spec A12), so the end-to-end proof of items 1 to 3 and the warn-period half of item 4 are its scenarios; Step 5 requires them green before the merge. What this plan adds for each item is in the second column.

| Spec §1 acceptance | Proven by |
|---|---|
| 1. After the date, with no valid session, every gated command exits 4 with `login_required` and does nothing else, and every door refuses | B5c: a machine with no session after the date (a command that would write is refused and leaves HOME empty, an open command works, a sign-in opens it, a logout closes it) and a daemon that starts locked with every door refusing; B2's gate tests and B3b's door tests. B5a: Task 7 makes sure the project's own real-binary consumers are not locked out by it. |
| 2. Signed in, then blocked: locked within one refresh interval, work in flight cancelled | B5c: the fake answers `invalid_grant`, the run ends `CANCELLED`, the heartbeat says `locked`/`refused`, `refresh.enc` is deleted, the daemon stays up and resumes after one sign-in; B3a's cancel-on-refusal tests. |
| 3. Signed in, monoes.me unreachable: works until 24 hours after the newest token was issued, then locked; work in flight at that moment finishes | B5c: an outage gives grace with its stderr line, then a token aged past 24 hours gives `locked(expired)` while the job in flight finishes; a 500, `invalid_client`, `invalid_target` or a page that is not JSON keeps the grace (D27). |
| 4. Before the date nothing locks, and once a date is set every surface warns | `TestEnforcedFlipsAtTheDate` and `TestTheClockGuardCannotPostponeTheDate` (Task 2), the whole suite run with the date set (Task 2 Step 5), the rehearsal with the date in the past (Task 7 Step 5), and B5c's warn-period scenario (the stderr line once and never on stdout, `account status`, the doctor row, the heartbeat, work runs). |
| 5. A release binary built with the `devaccount` tag cannot ship | `scripts/check-release-tags-test.sh` (tagged and untagged, loose, in a tarball, in a zip, `--min`), run by the CI job `release-guard-test`; the `release-guard` job on every artifact before the approval gate (Task 5); Task 8 Steps 7 and 8. |

Other spec items: §8 and D9 (release R, the date, the limits, the warn period) are Tasks 1, 2, 7 and 8; D28 and §8 step 5 (the daemon restart and its doctor flag) are Tasks 3 and 4, and A24's stop time for a daemon is Task 4b; §8 step 3 and D23 (an older library login adopted on the first run, once) are Task 6; §9 and D11 (developers, CI, the release guard) are Tasks 5 and 7; D22 (dormant until R) is Task 2's suite run and Task 8 Step 1; §11's release guard is Task 5 (the rest of §11, the real-binary smoke and the signed-in path through every entry point, is B5c's).

## Contract change requests

None. The development variable `MONOAGENT_DEV_ENFORCE_FROM` (spec A12) is approved and B5c's, and the `enforced` key of the account object (A4) is B3a's and B3b's.
