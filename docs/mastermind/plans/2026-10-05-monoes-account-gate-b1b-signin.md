# Mandatory monoes.me Account — B1b: sign-in, commands and aliases Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A user signs in to monoes.me once per machine (`monoagentcli account login`, in the browser or with an emailed code), the signed session renews itself against monoes.me, and `library login`, `logout` and `status` become aliases of that same session.

**Architecture:** The network half of `internal/account` (a Refresher that treats only `invalid_grant` as a refusal, a sign-in `Client`, the default guard and the `devaccount` key) carries the browser sign-in helpers that `internal/library` now uses for its own PKCE sign-in too. The library client reads with the machine session through a `SessionSource` and keeps a profile's older vault login only as a read fallback, `library.AdoptIntoAccount` moves an older login into the session, `libraryfake` signs JWTs with the dev key so all of it is provable, and `cmd/monoagentcli` gains the `account` group while the `library` login commands delegate to it.

**Tech Stack:** Go 1.26 standard library (`net/http`, `net/http/httptest`), cobra, the profile vault (SQLite) for adoption, and B1a's `internal/account` core (`Verify`, `Evaluate`, `Store`, `Guard`).

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§4.4, §7, §8 step 3; D10, D21, D22, D23, D24, D27). Index: `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§2, §3.1 to §3.4). Depends on B1a being merged; `account login --email` also needs plan A's Task 7. Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

Plan B1a is merged exactly as index §3 describes. Run every command from the repository root. Tasks run in order: Tasks 1 to 7 touch `internal/account`, `internal/library` and its fake; Tasks 8 to 12 build the library side and the command line on them.

## Global Constraints

- Go is `go 1.26.0`; `internal/account` adds no third-party dependency (D13): `crypto/ed25519` and a small strict JWS parser only. One accepted algorithm (EdDSA); the verifier ignores `jku`, `jwk` and `x5u` headers and never negotiates from the header.
- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- Storage (§4.6): `~/.monoagent/account/` (directory 0700) with `session.json`, `refresh.enc` and `session.lock` (files 0600). One session per OS user, shared by all profiles, whatever `--db-path` says.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `EnsureFresh`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear only on a login or a refresh. The high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- Open commands (D6): `version`, `help`, `completion`, cobra's hidden `__complete` and `__completeNoDesc`, `ref`, `update`, `doctor` (with `doctor fix`), `setup`, `account` (all of it), `library login`, `library logout`, `library status`. Everything else is gated, except the serving commands:
- Serving commands (spec §6.4) start even when locked, because launchd's `KeepAlive` and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop and MCP hosts must see a clear error. The CLI gate's third class `serve` is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`); layers 2 and 3 do the refusing. `org serve` (a launcher) and `daemon install`, `restart` and `uninstall` stay gated.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

## Review Focus

The failure modes this software's users are most likely to meet that the happy-path tests of the tasks would not touch, most likely first. Each is pinned by a named test.

1. **A person already logged in to the library must not notice this phase.** `library login` becomes an alias of a machine session they do not have yet, so library reads fall back to the profile's own vault login until a session replaces it, and `library logout` forgets it. Pinned by `TestAnOlderLoginServesReadsUntilASessionExists` (Task 8) and `TestAnOlderLibraryLoginKeepsWorkingAndLogoutForgetsIt` (Task 12).
2. **monoes.me answers a sign-in or an adoption with something that cannot be a session** (an opaque token, an emailed code answered without a refresh token as the email route does until plan A's Task 7 ships, a refresh grant that ignores `resource`). Nothing is stored and the user is told what to do. A refresh token the exchange spent is replaced by what monoes.me issued, a dead one (`invalid_grant`) is removed, because presenting either again, from an older binary say, ends every refresh token of the account on every machine (plan A, spike S2); so adoption and the library's own refresh of an older login take the same lock. Pinned by `TestLoginAnOpaqueAnswerStoresNothing` and `TestEmailSignInAtAServerThatCannotSignTheSession` (Task 5), `TestAdoptNeverReadsAnAnswerAsARefusal` and `TestAdoptReadsAndUpdatesTheOlderLoginUnderTheStoreLock` (Task 6), `TestOlderLoginRefreshWaitsForAnAdoptionAndUsesWhatItLeft` (Task 8), and `TestAdoptNeverReadsAnAnswerAsARefusalAndDropsADeadLogin`, `TestAdoptKeepsTheOlderLoginsAfterATransientFailureAndAsksOnce`, `TestAdoptKeepsTheOlderLoginAliveWithWhatTheExchangeIssued` and `TestAdoptDropsADeadLoginAndTriesTheNextProfile` (Task 9).
3. **monoes.me failing in any way but `invalid_grant`** (a 500 after a bad deploy, `invalid_client`, a malformed body, a dropped connection) locks nobody, and no error text carries a refresh token. Pinned by `TestRefresherOnlyInvalidGrantIsARefusal` (Task 4) and `TestAccountStatusFollowsMonoesMe` (Task 11); `TestRefresherNeverReturnsATypedNilError` (Task 4) pins that no failure comes back as an error that holds a nil pointer, which the guard would count as an ordinary failure.
4. **The session token reaches only the host that issued it,** and a refresh token never travels in the clear: a `MONOES_BASE_URL` pointed elsewhere neither receives the token nor lets `library login` sign in there. Pinned by `TestSessionTokenIsSentOnlyToItsHost` (Task 8), `TestLibraryLoginNeedsTheAccountHost` (Task 12), `TestLogoutNeverRevokesOverPlainHTTP` (Task 6) and `TestRefresherUnreachableAndInsecureHosts` (Task 4).
5. **A machine with no session stays untouched, and a broken session can always be cleaned.** Status, logout and adoption create nothing when there is nothing to act on, logout replaces an unreadable `session.json` by a session with no token (A23) and works offline, and a new sign-in replaces a refused session. Pinned by `TestAccountLoginStatusLogout` (Task 11), `TestAdoptWritesNothingOnAMachineWithoutAnOlderLogin` (Task 9), `TestLogoutWithNothingToForgetLeavesNoFiles`, `TestLogoutNeverRevokesOverPlainHTTP`, `TestLogoutForgetsAnUnreadableSession` and `TestLogoutRevokesAndForgetsEvenOffline` (Task 6), and `TestLoginReplacesARefusedSession` (Task 5).
6. **A refresh grant the caller abandons mid-call.** monoes.me rotates the refresh token when it answers, and the answer is the only copy of the new one: a Ctrl-C, a SIGTERM or a closing context that aborts the call leaves the dead token on disk, and the next refresh after monoes.me's 300-second reuse window ends every install of the account (spec A20). Every grant this plan sends that rotates a refresh token is therefore completed and stored once it is sent: the adoption exchange, the update of the vault entry it leaves behind, and the library's refresh of a login of its own. Pinned by `TestAdoptStoresTheAnswerWhenTheCallerGivesUpMidCall` (Task 6), `TestOlderLoginRefreshIsStoredWhenTheCallerGivesUp` (Task 8) and `TestAdoptCompletesWhenTheCallerGivesUp` (Task 9).
7. **A logout that unlocks a machine.** The high-water mark that makes a clock set back worthless lives in `session.json`. Deleting the file at logout would let an account that monoes.me has blocked sign out (an open command), set the clock before the enforcement date and run again (spec A23). Logout keeps the record, as a session with no token, and revokes the refresh token it reads under the lock, not one that a refresh in another process has rotated since. Pinned by `TestLogoutKeepsTheClockGuardRecord`, `TestASecondLogoutChangesNothing`, `TestLoginAfterLogoutReplacesTheRecord` and `TestLogoutRevokesTheRefreshTokenThatIsOnDiskWhenItHoldsTheLock` (Task 6), and `TestAccountLoginStatusLogout` (Task 11).

## Decisions and assumptions

- **The shared browser sign-in moves into `internal/account`; `internal/library` delegates.** The PKCE flow, the loopback listener and the endpoint discovery pinned to the base host are exported from `internal/account` (`DiscoverEndpoints`, `AuthorizeInBrowser`; standard library only) and `internal/library`'s `LoginPKCE` calls them, so its public login API and its tests stay as they are. A neutral third package was the other option the index allows; it is not used because B1a's `TestImportsOnlyWhatTheImportRuleAllows` lets `internal/account` import only the standard library and `internal/secrets`, and this way that test stays as B1a wrote it. The email-code calls are not shared: they are two small requests, and the library keeps its own.
- **D21 against D22.** D21 turns the library login commands into aliases of one machine session; D22 says an earlier phase must change nothing a user can see. A user who is logged in to the library today has a vault entry and no session. So the library client reads with the session when there is one for its host and otherwise with the profile's vault login (read, and refreshed as before), `library login` writes only the session, and `library logout` ends the session and also revokes and removes the profile's vault login. `account logout` never opens the database: it revokes and removes the session's refresh token and replaces `session.json` by a session with no token that keeps the machine's clock-guard record (A23), nothing more; another profile's older login is a chain of its own, so after `account logout` `library status` can still report it (method `pkce` or `email`) until `library logout` is run for that profile or adoption removes it, and no refresh token is presented twice because of that. Release R's adoption (Task 9, wired by B5a) moves vault logins into the session; removing the fallback is a later cleanup.
- **`MONOES_BASE_URL` and the session.** The session belongs to `account.Host()` (`HostURL`, or `MONOES_BASE_URL` in a `devaccount` build only; D24). If the library's base URL differs, the session token is not sent there (index §3.4 item 10), `library login` refuses with a message naming `-tags devaccount`, and `library logout` leaves the session alone and forgets only that host's older login.
- **Email sign-in follows plan A's Task 7, in either of the two shapes it offers.** monoes.me's `POST /api/auth/agent/claim/verify` returns today an opaque access token and no refresh token, and ignores `resource` (monoes-landing `src/app/api/auth/agent/claim/verify/route.ts`, lines 92 to 112), so it cannot start a gate session. Plan A's Task 7 (a-server.md, "Task 7") makes the route answer a MonoAgent claim that includes `offline_access` with a `refresh_token`, which the client trades at the token endpoint with `resource`; and, when the body carries `resource`, with the token endpoint's own answer, a signed access token and a refresh token, so that the sign-in is one call. This client sends `resource` with the code. If the answer holds a signed token and a refresh token that is the session; if it holds a refresh token beside an opaque token, the refresh token is traded once, with `resource`, for the signed one, and only the traded token is stored. Against today's route (no refresh token) the result is `ErrEmailSessionUnavailable` and nothing is stored. All three outcomes are tested against the fake (its default, `SetEmailTrade(true)` and `SetEmailOpaque(true)`).
- **A dead or spent refresh token is never presented again (plan A, spike S2).** monoes.me answers a rotated or revoked refresh token with `invalid_grant` and then deletes every MonoAgent refresh token of the account, on every machine, and an older binary would present whatever the vault still holds. So adoption removes the vault copy once its exchange made a session and also when monoes.me answered `invalid_grant` (the token is dead); it writes the rotated tokens back when the exchange succeeded but made no session (an opaque token), and it keeps the login only when monoes.me gave no verdict (no answer, a 500). All of it happens while it holds the account store lock, and it reads the older login only after taking that lock; the library's own refresh of an older login takes the same lock and refreshes what the vault holds then; logout deletes every local copy of the session's refresh token. The fake models the replay (`Server.Replays`) and the tests assert that none happens. Plan A's `refreshTokenReuseInterval: 300` makes a retry inside five minutes of a lost answer safe on the real server; the fake models the strict behavior outside that window, which is the one no client may cause.
- **Spike S2 does not matter to the code, and B5a's claim does not matter to adoption.** Adoption exchanges an older refresh token through the same Refresher; whichever answer comes (a signed token, an opaque one, `invalid_grant`, a 500) is handled and tested (Tasks 6 and 9). B5a (its Task 6) tries once per database, claiming the try with a settings row before it calls `library.AdoptIntoAccount`, so one call tries every profile that has an older login, in order: a dead login is removed and the next profile is tried, because its login is a chain of its own; a login monoes.me gave no verdict on stays, and the call stops there, so an offline machine pays one connect timeout. That is what B5a's Task 6 assumes (its limits 2 and 6).
- **Logout keeps the clock-guard record (A23); the `Store` has no delete, and none is needed.** `Client.Logout` reads the session and the refresh token under the store lock, revokes the refresh token it read, deletes `refresh.enc` and, when a session existed, saves through `Store.Save` a session with no token, `{V: 1, Host, HW: max(its mark, now)}`, instead of removing `session.json`. The high-water mark is what keeps `Enforced(now, hw)` true after the clock is set back before the enforcement date, so an open command must not erase it; B1a's `Evaluate` judges such a session `locked(not_logged_in)` with `Enforced` computed from the mark. A machine with nothing stored still writes nothing (a look at the two file names, before the lock, decides), a record that already has no token is left as it is, and an unreadable `session.json` is replaced by a record that starts from now. The next sign-in replaces the record.
- **A grant, once sent, is completed and stored (A20).** `Client.exchangeOlder` (adoption), the update of the vault entry that `AdoptIntoAccount` makes after it and the library client's refresh of a login of its own run on `context.WithTimeout(context.WithoutCancel(ctx), …)`: a Ctrl-C or a closing context cannot abort them, and each ends by a deadline of its own (`refreshCallTimeout`, the guard's own 20 seconds, for the exchange; 10 seconds for a vault write; `refreshGrantTimeout`, 20 seconds, in the library), so a command interrupted during one waits at most that long. The sign-in's authorization-code exchange needs no such guarantee (a code is single use and rotates nothing), and neither does the emailed code's trade (the emailed refresh token is never stored): an abandoned call leaves no refresh token on disk that could be presented again. `libraryfake.OnToken` (Task 3) lets a test cancel the caller while monoes.me is answering.
- **Sealing.** Sign-in (`Login`, `VerifyEmailCode`) uses `NewInteractiveKeyringSealer` (B1a's file-keyring-capable sealer, which may ask for the file keyring's passphrase as the vault does); everything else, guards, logout and adoption included, uses `NewKeyringSealer`, which never prompts.
- **A test seam for the sealer.** `internal/secrets` remembers the account key it first made for the whole process (`getOrCreateKEK`, `internal/secrets/keyring.go:93`), while most `cmd/monoagentcli` tests re-make the mock keyring (`keyring.MockInit()`), so a second sign-in in one test binary seals a refresh token that the re-made keyring can no longer open: the next renewal ends in `keyring_unavailable` and the test passes or fails by its position in the run. `account.SetSealerForTest` (Task 7) gives the default store a sealer of the test's own, and `libFixture` installs a memory sealer (Task 11). A test of another plan that signs in through the default store more than once per test binary should do the same.
- **Files outside the §3.1 list** that this plan creates or edits, so B2, B3a and B4 know: new files `internal/account/oauth.go`, `logout.go` and `adopt.go`, `internal/library/session.go`, `internal/library/libraryfake/control.go` and `jwt.go`, `cmd/monoagentcli/account.go` and `account_login.go`; and one added line in `cmd/monoagentcli/root.go` (registering `account`). `exitCodeFor`'s mapping of a bare `*account.LoginRequiredError` to exit 4 is B2's (its Task 4) and is not done here.
- **Not in this plan:** calling `AdoptIntoAccount` (B5a), the CLI gate and `run()` (B2), the runner and door checks (B3a, B3b), the desktop (B4), `ref` pages and docs (B5b). No `ref` text is added: no test requires it for these commands.

---

## Part 1: the network side of `internal/account`, and the fake that proves it

### Task 1: account: endpoint discovery and the browser sign-in, shared with internal/library

`internal/account` may not import `internal/library` (index §3.1), and both need the same browser sign-in: PKCE, a loopback listener and endpoint discovery pinned to the base host. The code moves, unchanged in behavior, into `internal/account` (standard library only, so its import rule holds) as exported helpers, and `internal/library` delegates to it in the next task; `internal/library` may import `internal/account`.

**Files:**
- Create: `internal/account/oauth.go`: `OAuthEndpoints`, `DiscoverEndpoints`, `AuthorizeOptions`, `AuthorizeResult`, `AuthorizeInBrowser`
- Test: `internal/account/oauth_test.go`

**Interfaces:**
- Consumes: Standard library only.
- Produces:

```go
package account

type OAuthEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

// DiscoverEndpoints always returns usable endpoints, each on baseURL's host; its error is set only when no HTTP answer came back.
func DiscoverEndpoints(ctx context.Context, hc *http.Client, baseURL string) (*OAuthEndpoints, error)

type AuthorizeOptions struct {
	Open        func(string) error // nil = do not open a browser
	OnURL       func(string)
	Timeout     time.Duration // default 5 minutes
	Label       string        // prefixes the errors, e.g. "library login"
	SuccessText string        // the browser tab's text on success
}

type AuthorizeResult struct{ Code, Redirect, Verifier string }

func AuthorizeInBrowser(ctx context.Context, endpoint string, params url.Values, o AuthorizeOptions) (*AuthorizeResult, error)
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/account/oauth_test.go` with exactly this content:

```go
package account_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// browse plays the user's browser: it follows the authorize URL's redirect_uri back
// to the loopback listener with the given extra query, and returns the status.
func followRedirect(t *testing.T, authURL string, query url.Values) int {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Error(err)
		return 0
	}
	resp, err := http.Get(u.Query().Get("redirect_uri") + "?" + query.Encode())
	if err != nil {
		t.Errorf("browser: %v", err)
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestAuthorizeReturnsTheCodeTheVerifierAndTheRedirect(t *testing.T) {
	var shown string
	o := account.AuthorizeOptions{Label: "test login", Timeout: 10 * time.Second, SuccessText: "done",
		OnURL: func(u string) { shown = u },
		Open: func(u string) error {
			uq, _ := url.Parse(u)
			go followRedirect(t, u, url.Values{"code": {"c1"}, "state": {uq.Query().Get("state")}})
			return nil
		}}
	res, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", url.Values{"client_id": {"monoagent"}, "resource": {"https://r"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(shown)
	got := q.Query()
	sum := sha256.Sum256([]byte(res.Verifier))
	if res.Code != "c1" || res.Redirect != got.Get("redirect_uri") || !strings.HasPrefix(res.Redirect, "http://127.0.0.1:") ||
		got.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || got.Get("code_challenge_method") != "S256" ||
		got.Get("response_type") != "code" || got.Get("client_id") != "monoagent" || got.Get("resource") != "https://r" {
		t.Fatalf("result: code ok %v, redirect %q, verifier of %d bytes; authorize URL %s", res.Code == "c1", res.Redirect, len(res.Verifier), shown)
	}
}

func TestAuthorizeIgnoresAForeignStateAndKeepsWaiting(t *testing.T) {
	o := account.AuthorizeOptions{Timeout: 10 * time.Second, Open: func(u string) error {
		go func() {
			if followRedirect(t, u, url.Values{"code": {"evil"}, "state": {"not-ours"}}) != http.StatusBadRequest {
				t.Error("a foreign state was not refused")
			}
			uq, _ := url.Parse(u)
			followRedirect(t, u, url.Values{"code": {"good"}, "state": {uq.Query().Get("state")}})
		}()
		return nil
	}}
	res, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", nil, o)
	if err != nil || res.Code != "good" {
		t.Fatalf("result: %v, %v", res != nil && res.Code == "good", err)
	}
}

func TestAuthorizeReportsARefusalAndATimeout(t *testing.T) {
	o := account.AuthorizeOptions{Label: "account login", Timeout: 10 * time.Second, Open: func(u string) error {
		uq, _ := url.Parse(u)
		go followRedirect(t, u, url.Values{"error": {"access_denied"}, "state": {uq.Query().Get("state")}})
		return nil
	}}
	if _, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", nil, o); err == nil ||
		!strings.Contains(err.Error(), "account login: monoes.me refused: access_denied") {
		t.Fatalf("refusal: %v", err)
	}
	_, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", nil, account.AuthorizeOptions{Label: "library login", Timeout: 200 * time.Millisecond})
	if err == nil || err.Error() != "library login: no answer from the browser within 200ms" {
		t.Fatalf("timeout: %v", err)
	}
}

func TestDiscoverPinsEveryEndpointToTheBaseHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"authorization_endpoint":"https://evil.example/oauth/authorize?x=1","token_endpoint":"` + "http://" + r.Host + `/oauth/token"}`))
	}))
	defer srv.Close()
	ep, err := account.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
	if err != nil || ep.AuthorizationEndpoint != srv.URL+"/oauth/authorize?x=1" || ep.TokenEndpoint != srv.URL+"/oauth/token" ||
		ep.RevocationEndpoint != srv.URL+"/api/auth/oauth2/revoke" {
		t.Fatalf("endpoints %+v, %v", ep, err)
	}
	dead := httptest.NewServer(nil)
	dead.Close()
	ep, err = account.DiscoverEndpoints(context.Background(), http.DefaultClient, dead.URL)
	if err == nil || ep.TokenEndpoint != dead.URL+"/api/auth/oauth2/token" {
		t.Fatalf("an unreachable server must set the error and still give the defaults: %+v, %v", ep, err)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/account/ -run '^(TestAuthorizeReturnsTheCodeTheVerifierAndTheRedirect|TestAuthorizeIgnoresAForeignStateAndKeepsWaiting|TestAuthorizeReportsARefusalAndATimeout|TestDiscoverPinsEveryEndpointToTheBaseHost)$' -count=1
```

Expected: FAIL, build errors: `undefined: account.AuthorizeOptions`, `undefined: account.AuthorizeInBrowser`, `undefined: account.DiscoverEndpoints`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
FAIL
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/oauth_test.go:37:15: undefined: account.AuthorizeOptions
internal/account/oauth_test.go:44:22: undefined: account.AuthorizeInBrowser
internal/account/oauth_test.go:59:15: undefined: account.AuthorizeOptions
internal/account/oauth_test.go:103:20: too many errors
```

- [ ] **Step 3: Implement the package.**

Create `internal/account/oauth.go` with exactly this content:

```go
package account

// The browser half of the OAuth 2.1 sign-in and the endpoint discovery that
// Login uses and internal/library's own sign-in delegates to: PKCE, a loopback
// listener (RFC 8252) and endpoints pinned to the base host. Standard library
// only, so the import rule of this package holds.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthEndpoints is the part of the authorization server metadata (RFC 8414) MonoAgent uses.
type OAuthEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

// Fallback endpoints (Better-Auth's oauth-provider defaults) when the server
// publishes no metadata.
const (
	oauthAuthorizePath = "/api/auth/oauth2/authorize"
	oauthTokenPath     = "/api/auth/oauth2/token"
	oauthRevokePath    = "/api/auth/oauth2/revoke"
)

// DiscoverEndpoints reads the server metadata at baseURL. It always returns usable
// endpoints. An endpoint on another host than baseURL is used by path on baseURL,
// so a misconfigured issuer can never receive a code verifier or a token. The
// error is set only when no HTTP answer came back at all, so a caller that must
// not wait twice for a dead network can stop there.
func DiscoverEndpoints(ctx context.Context, hc *http.Client, baseURL string) (*OAuthEndpoints, error) {
	m := &OAuthEndpoints{}
	var unreachable error
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/.well-known/oauth-authorization-server", nil); err == nil {
		if resp, err := hc.Do(req); err != nil {
			unreachable = err
		} else {
			if resp.StatusCode == http.StatusOK {
				_ = json.NewDecoder(resp.Body).Decode(m)
			}
			resp.Body.Close()
		}
	}
	m.AuthorizationEndpoint = pinToBase(baseURL, m.AuthorizationEndpoint, oauthAuthorizePath)
	m.TokenEndpoint = pinToBase(baseURL, m.TokenEndpoint, oauthTokenPath)
	m.RevocationEndpoint = pinToBase(baseURL, m.RevocationEndpoint, oauthRevokePath)
	return m, unreachable
}

// pinToBase keeps endpoint when it is on baseURL's host, and otherwise uses its path on baseURL.
func pinToBase(baseURL, endpoint, fallback string) string {
	if endpoint == "" {
		return baseURL + fallback
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Path == "" {
		return baseURL + fallback
	}
	base, _ := url.Parse(baseURL)
	if strings.EqualFold(u.Host, base.Host) && u.Scheme == base.Scheme {
		return endpoint
	}
	p := u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return baseURL + p
}

// AuthorizeOptions controls AuthorizeInBrowser.
type AuthorizeOptions struct {
	Open        func(string) error // opens the authorization URL (the system browser); nil = don't
	OnURL       func(string)       // told the authorization URL before Open runs
	Timeout     time.Duration      // how long to wait for the redirect; default 5 minutes
	Label       string             // prefixes the errors, e.g. "library login"; default "sign-in"
	SuccessText string             // what the browser tab says once the sign-in went through
}

// AuthorizeResult is what the browser half hands to the token exchange: the code,
// the redirect URI it was issued for and the PKCE verifier.
type AuthorizeResult struct{ Code, Redirect, Verifier string }

// AuthorizeInBrowser sends the user to endpoint with params plus response_type, redirect_uri,
// state and a PKCE S256 challenge, and waits on 127.0.0.1:<random> for the redirect
// back (RFC 8252). The caller exchanges the returned code at the token endpoint.
func AuthorizeInBrowser(ctx context.Context, endpoint string, params url.Values, o AuthorizeOptions) (*AuthorizeResult, error) {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.Label == "" {
		o.Label = "sign-in"
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("%s: listen on loopback: %w", o.Label, err)
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	verifier, state := randomURLSafe(48), randomURLSafe(24)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	authURL := endpoint + sep + q.Encode()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		v := r.URL.Query()
		var res result
		switch {
		case v.Get("state") != state:
			callbackPage(w, false, "This sign-in link is not for this login attempt.", "")
			return // keep waiting for the real redirect
		case v.Get("error") != "":
			res.err = fmt.Errorf("%s: monoes.me refused: %s %s", o.Label, v.Get("error"), v.Get("error_description"))
			callbackPage(w, false, "monoes.me did not authorize MonoAgent: "+v.Get("error"), "")
		case v.Get("code") == "":
			res.err = fmt.Errorf("%s: the redirect carried no code", o.Label)
			callbackPage(w, false, "The sign-in redirect carried no code.", "")
		default:
			res.code = v.Get("code")
			callbackPage(w, true, "", o.SuccessText)
		}
		select {
		case done <- res:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	// Shutdown, not Close: Close cuts connections that are still writing, so the
	// browser that delivered the code could get EOF instead of the "you can close
	// this tab" page.
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	if o.OnURL != nil {
		o.OnURL(authURL)
	}
	if o.Open != nil {
		_ = o.Open(authURL) // the URL was shown; the user can open it by hand
	}
	select {
	case res := <-done:
		if res.err != nil {
			return nil, res.err
		}
		return &AuthorizeResult{Code: res.code, Redirect: redirect, Verifier: verifier}, nil
	case <-ctx.Done():
		return nil, errors.New(o.Label + ": no answer from the browser within " + o.Timeout.String())
	}
}

func callbackPage(w http.ResponseWriter, ok bool, msg, success string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	title, body := "Logged in to monoes.me", success
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		title, body = "Sign-in failed", msg
	}
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title><body style="font-family:system-ui;margin:3rem"><h1>%s</h1><p>%s</p>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(body))
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
```

- [ ] **Step 4: Run the tests with the race detector.**

```bash
go test ./internal/account/ -run '^(TestAuthorizeReturnsTheCodeTheVerifierAndTheRedirect|TestAuthorizeIgnoresAForeignStateAndKeepsWaiting|TestAuthorizeReportsARefusalAndATimeout|TestDiscoverPinsEveryEndpointToTheBaseHost)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/account` (the time varies).

- [ ] **Step 5: Vet and format.**

```bash
go vet ./internal/account/ && gofmt -l internal/account
```

Expected: No output.

- [ ] **Step 6: Commit.**

```bash
git add internal/account/oauth.go internal/account/oauth_test.go
```

```bash
git commit -m "feat(account): endpoint discovery and the browser sign-in, shared with the library" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 2: internal/library delegates its browser sign-in to account

A behavior-preserving move: `internal/library`'s public login API (`Client.LoginPKCE`, `Token`, `LoginOptions`, `TokenStore`, `Client.Logout`) keeps its signatures and its tests, and now delegates the browser half to `internal/account`.

**Files:**
- Modify: `internal/library/auth.go` (lines 3-18 imports, 20-35 `oauthMeta`, 72-116 `endpoints` and `onBase`, 199-294 `LoginPKCE`, 318-328 `writeCallbackPage`, 390-397 `randomString`, at f4441a2a)
- Modify: `internal/library/client.go` (lines 3-21 imports, line 43 the `oauth` field)

**Interfaces:**
- Consumes: `account.OAuthEndpoints`, `account.DiscoverEndpoints`, `account.AuthorizeOptions`, `account.AuthorizeInBrowser` (Task 1).
- Produces:

Nothing new: `library.Client` keeps `LoginPKCE`, `SendEmailCode`, `VerifyEmailCode`, `Logout`, `Token`, `LoginOptions`, `TokenStore` exactly as before.

- [ ] **Step 1: Run the library tests first: they are the proof that this move changes nothing.**

```bash
go test ./internal/library/ -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/library`.

- [ ] **Step 2: Move the sign-in code out of `internal/library`.**

In `internal/library/auth.go`, replace this block (16 lines, shown abbreviated: it runs from its first line to its last, inclusive):

```go
import (
	"bytes"
…
	"time"
)
```

with:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)
```

In `internal/library/auth.go`, delete this block (15 lines, shown abbreviated: it runs from its first line to its last, inclusive and the blank line after it):

```go
// oauthMeta is the part of the authorization server metadata (RFC 8414)
// MonoAgent uses.
…
	defaultRevokePath    = "/api/auth/oauth2/revoke"
)
```

In `internal/library/auth.go`, replace the declaration that starts `func (c *Client) endpoints(` (with its doc comment, up to its closing brace) by:

```go
// endpoints discovers the authorization server's endpoints, once per client.
func (c *Client) endpoints(ctx context.Context) *account.OAuthEndpoints {
	c.mu.Lock()
	m := c.oauth
	c.mu.Unlock()
	if m != nil {
		return m
	}
	m, _ = account.DiscoverEndpoints(ctx, c.HTTP, c.BaseURL)
	c.mu.Lock()
	c.oauth = m
	c.mu.Unlock()
	return m
}
```

In `internal/library/auth.go`, delete the declaration that starts `func (c *Client) onBase(` together with its doc comment.

In `internal/library/auth.go`, replace the declaration that starts `func (c *Client) LoginPKCE(` (with its doc comment, up to its closing brace) by:

```go
// LoginPKCE runs the OAuth 2.1 authorization code flow with PKCE and a loopback
// redirect (RFC 8252): it sends the user to the authorize URL, waits for the
// redirect, exchanges the code and stores the token together with the account it
// belongs to.
func (c *Client) LoginPKCE(ctx context.Context, opts LoginOptions) (*Token, error) {
	meta := c.endpoints(ctx)
	res, err := account.AuthorizeInBrowser(ctx, meta.AuthorizationEndpoint,
		url.Values{"client_id": {ClientID}, "scope": {strings.Join(Scopes, " ")}},
		account.AuthorizeOptions{Open: opts.Open, OnURL: opts.OnURL, Timeout: opts.Timeout, Label: "library login",
			SuccessText: "MonoAgent is now connected to your monoes.me library. You can close this tab."})
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {res.Code}, "redirect_uri": {res.Redirect},
		"client_id": {ClientID}, "code_verifier": {res.Verifier}}
	tr, err := c.postToken(ctx, meta.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("library login: exchange the code: %w", err)
	}
	return c.finishLogin(ctx, c.tokenFrom(tr, "pkce", nil))
}
```

In `internal/library/auth.go`, delete the declaration that starts `func writeCallbackPage(` together with its doc comment.

In `internal/library/auth.go`, delete the declaration that starts `func randomString(` together with its doc comment.

In `internal/library/client.go`, replace this text:

```go
	"sync"
	"time"
)
```

with:

```go
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)
```

In `internal/library/client.go`, replace this text:

```go
	oauth  *oauthMeta
```

with:

```go
	oauth  *account.OAuthEndpoints
```

- [ ] **Step 3: Build, vet and format.**

```bash
go build ./... && go vet ./internal/library/ && gofmt -l internal/library
```

Expected: No output.

- [ ] **Step 4: Run the library tests again.**

```bash
go test ./internal/library/ -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/library`: every existing library test still passes.

- [ ] **Step 5: Commit.**

```bash
git add internal/library/auth.go internal/library/client.go
```

```bash
git commit -m "refactor(library): sign in through the shared browser sign-in of account" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3: libraryfake signs JWT access tokens and can fail, block and tell time

Everything after this task is proven against the fake, so it first learns what monoes.me will do (spec §4.1, §4.4, §5; plan A's measured behavior): a signed EdDSA access token exactly when a `resource` is sent, which then sticks to its refresh chain; refresh answers that fail in each way D27 distinguishes; a block that revokes; the replay of a rotated refresh token that ends every token of the account; an emailed code in each shape a route can answer it (signed, to trade, as today); a clock that can be set; a log of the token requests; a hook that runs while a token request is in flight, so a test can cancel a caller while monoes.me is answering (A20). The existing library tests must keep passing against the changed fake: without a `resource` it behaves as before.

**Files:**
- Create: `internal/library/libraryfake/control.go`: the switches, `Block`, `NewGrant`, `LastResource`, `TokenRequests`, `OnToken`
- Create: `internal/library/libraryfake/jwt.go`: `TrustKey` and the signer
- Modify: `internal/library/libraryfake/oauth.go` (replaced whole: lines 1-108 are replaced by the file below)
- Modify: `internal/library/libraryfake/fake.go` (six small edits: lines 50, 82, 90, 168, 209, 231-233)
- Test: `internal/library/libraryfake/fake_test.go`

**Interfaces:**
- Consumes: From B1a: `account.Issuer`, `account.Audience`, `account.ClientID`, `account.Key`, `account.Verify`, `account.SetTrustedKeysForTest(t testing.TB, keys []Key)`, `accounttest.DevKeyPair() (ed25519.PublicKey, ed25519.PrivateKey)` (frozen contract) and `accounttest.DevKID` (B1a's contract change request 8: `"monoagent-dev-1"`).
- Produces:

```go
package libraryfake

func TrustKey(t testing.TB) // account.SetTrustedKeysForTest with the dev key (accounttest.DevKID), as a devaccount build always does

type RefreshMode int

const (
	RefreshOK RefreshMode = iota
	RefreshInvalidGrant  // 400 invalid_grant: the one refusal
	RefreshInvalidClient // 401 invalid_client
	RefreshInvalidTarget // 400 invalid_target
	RefreshServerError   // 500
	RefreshMalformed     // 200, not JSON
	RefreshDrop          // the connection closes unanswered
)

func (s *Server) SetClock(now func() time.Time) // nil: the real clock
func (s *Server) SetRefreshMode(m RefreshMode)
func (s *Server) SetOpaqueTokens(on bool) // ignore resource everywhere: opaque tokens
func (s *Server) SetEmailOpaque(on bool) // claim/verify as today: an opaque access token, no refresh token
func (s *Server) SetEmailTrade(on bool)  // claim/verify answers an opaque token beside a refresh token, whatever is asked
func (s *Server) Block(userID string)     // revokes every token of the user; sign-in and refresh fail
func (s *Server) Unblock(userID string)
func (s *Server) LastResource() string    // the resource of the latest token or emailed-code request
func (s *Server) NewGrant(username string) (access, refresh string) // an older login: opaque access token and a refresh token
func (s *Server) OnToken(fn func()) // fn runs when a token request arrives, before the fake looks at it; nil removes it

type TokenRequest struct {
	Form   url.Values  // never print it: it holds codes and refresh tokens
	Header http.Header // without Authorization
}

func (s *Server) TokenRequests() []TokenRequest // every request to the token endpoint, in order (B5b's contract change request)
// Server.Replays int counts refresh tokens presented after they were rotated or revoked.
```
Behavior: a token request (code, refresh) that carries `resource=account.Audience` is answered with an EdDSA JWT shaped like monoes.me's (header `typ` `at+jwt`, `kid` `accounttest.DevKID`; `iss` `account.Issuer`, `aud` an array holding `account.Audience`, `azp` and `client_id` `monoagent`, `sub`, `scope`, `iat`, `exp` = `AccessTTL` later, `plan` `free`, `jti`); the audience then sticks to the refresh chain; any other `resource` is `invalid_target`; no `resource` on a chain that never had one gives the opaque token as before. An emailed code (`claim/verify`) that asks for `resource=account.Audience` is answered like the token endpoint (a signed access token and a refresh token; plan A, Task 7), another `resource` is `invalid_target`, and a request without one gets an opaque access token and a refresh token (the default); `SetEmailTrade` gives that second shape whatever the request asks, and `SetEmailOpaque` gives the route of today, an opaque access token and no refresh token. Presenting a rotated or revoked refresh token again is `invalid_grant`, counts a replay and revokes every token of that user (spike S2). The fake keeps no reuse window: monoes.me answers a repeat within 300 seconds of the rotation with the answer it gave first (plan A, Task 3), this fake punishes every repeat, so a test that passes here never relies on that forgiveness. `OnToken`'s hook runs in the request's own goroutine after the request has reached the fake and before the fake looks at it: a hook that cancels a caller's context makes that caller give up while the token is rotated and the answer is on its way, which is what a Ctrl-C or a SIGTERM during a refresh does.

- [ ] **Step 1: Write the failing tests.**

Create `internal/library/libraryfake/fake_test.go` with exactly this content:

```go
package libraryfake_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

func postToken(t *testing.T, base string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(base+"/api/auth/oauth2/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func refresh(rt, resource string) url.Values {
	f := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {"monoagent"}}
	if resource != "" {
		f.Set("resource", resource)
	}
	return f
}

func verifies(token string) bool {
	_, err := account.Verify(token, time.Now())
	return err == nil
}

// Spec §4.1 and plan A's findings: a token is a JWT exactly when a resource is
// sent, only for the MonoAgent audience, and the audience sticks to its chain.
func TestAccessTokensAreJWTsOnlyWhenTheAudienceIsSent(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	_, rt := fake.NewGrant("ada")

	status, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	tok, _ := body["access_token"].(string)
	rec, err := account.Verify(tok, time.Now())
	if status != 200 || err != nil || rec.Sub != "u-ada" || rec.ExpiresAt.Sub(rec.IssuedAt) != time.Hour || fake.LastResource() != account.Audience {
		t.Fatalf("with the audience: HTTP %d, %v", status, err)
	}

	status, body = postToken(t, fake.URL, refresh(body["refresh_token"].(string), ""))
	if tok, _ = body["access_token"].(string); status != 200 || !verifies(tok) || fake.LastResource() != "" {
		t.Fatalf("a chain that has the audience keeps it: HTTP %d", status)
	}

	_, older := fake.NewGrant("ada")
	status, body = postToken(t, fake.URL, refresh(older, ""))
	if tok, _ = body["access_token"].(string); status != 200 || verifies(tok) {
		t.Fatalf("a chain that never had a resource stays opaque: HTTP %d", status)
	}

	status, body = postToken(t, fake.URL, refresh(body["refresh_token"].(string), "https://elsewhere.example"))
	if status != 400 || body["error"] != "invalid_target" {
		t.Fatalf("another audience: HTTP %d %v", status, body)
	}
}

// The clock dates the tokens; Block revokes everything the user holds, so the next
// refresh is invalid_grant and the library refuses the access token.
func TestClockDatesTokensAndBlockRevokesThem(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	fake.SetClock(func() time.Time { return at })
	_, rt := fake.NewGrant("ada")
	_, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	access, _ := body["access_token"].(string)
	if rec, err := account.Verify(access, at); err != nil || !rec.IssuedAt.Equal(at) {
		t.Fatalf("the token is not dated by the fake's clock: %v", err)
	}

	fake.Block("u-ada")
	if status, body := postToken(t, fake.URL, refresh(body["refresh_token"].(string), account.Audience)); status != 400 || body["error"] != "invalid_grant" || fake.Replays != 0 {
		t.Fatalf("refresh of a blocked user: HTTP %d %v (replays %d)", status, body, fake.Replays)
	}
	req, _ := http.NewRequest(http.MethodGet, fake.URL+"/api/library/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a blocked user's access token still works: HTTP %d", resp.StatusCode)
	}
}

// Plan A, spike S2: presenting a rotated refresh token again ends every refresh
// token of the account. The fake does the same and counts it, so a test can prove
// that a client never causes one.
func TestReplayingARotatedRefreshTokenRevokesTheAccount(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	_, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	newer := body["refresh_token"].(string)
	if status, _ := postToken(t, fake.URL, refresh(rt, account.Audience)); status != 400 || fake.Replays != 1 {
		t.Fatalf("the replay: HTTP %d, replays %d", status, fake.Replays)
	}
	if status, _ := postToken(t, fake.URL, refresh(newer, account.Audience)); status != 400 {
		t.Fatalf("the newer refresh token survived a replay: HTTP %d", status)
	}
}

// OnToken runs when a token request arrives, in the request's own goroutine, and the fake still
// answers the request afterwards: a test that cancels a caller there sees what the caller does when
// it gives up while monoes.me is answering. nil removes the hook.
func TestOnTokenRunsWhenATokenRequestArrivesAndTheFakeStillAnswers(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	var calls atomic.Int32
	fake.OnToken(func() { calls.Add(1) })
	_, rt := fake.NewGrant("ada")
	status, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	if next, _ := body["refresh_token"].(string); status != 200 || next == "" || calls.Load() != 1 {
		t.Fatalf("HTTP %d, the hook ran %d times, want 200 and once", status, calls.Load())
	}
	fake.OnToken(nil)
	_, other := fake.NewGrant("ada")
	if status, _ := postToken(t, fake.URL, refresh(other, account.Audience)); status != 200 || calls.Load() != 1 {
		t.Fatalf("HTTP %d, the removed hook ran again (%d calls)", status, calls.Load())
	}
}

func postVerify(t *testing.T, base string, body map[string]string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+"/api/auth/agent/claim/verify", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// Plan A, Task 7: the emailed code answers like the token endpoint when the
// MonoAgent audience is asked for, with an opaque access token and a refresh token
// when it is not; SetEmailTrade and SetEmailOpaque give the other two shapes a client
// must survive.
func TestEmailVerifyAnswersLikeTheTokenEndpointWhenTheAudienceIsAsked(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	verify := func(extra map[string]string) (int, map[string]any) {
		body := map[string]string{"email": "ada@example.com", "code": "123456", "client_id": "monoagent"}
		for k, v := range extra {
			body[k] = v
		}
		return postVerify(t, fake.URL, body)
	}
	shape := func(body map[string]any) (signed, refresh bool) {
		tok, _ := body["access_token"].(string)
		rt, _ := body["refresh_token"].(string)
		return verifies(tok), rt != ""
	}

	status, body := verify(map[string]string{"resource": account.Audience})
	if signed, refresh := shape(body); status != 200 || !signed || !refresh {
		t.Fatalf("with the audience: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
	status, body = verify(nil)
	if signed, refresh := shape(body); status != 200 || signed || !refresh {
		t.Fatalf("without the audience: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
	if status, body = verify(map[string]string{"resource": "https://elsewhere.example"}); status != 400 || body["error"] != "invalid_target" {
		t.Fatalf("another audience: HTTP %d %v", status, body)
	}
	if status, body = verify(map[string]string{"resource": account.Audience, "code": "000000"}); status != 400 || body["error"] != "invalid_or_expired_code" {
		t.Fatalf("a wrong code: HTTP %d %v", status, body)
	}

	fake.SetEmailTrade(true)
	status, body = verify(map[string]string{"resource": account.Audience})
	if signed, refresh := shape(body); status != 200 || signed || !refresh {
		t.Fatalf("a route that answers a refresh token to trade: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
	fake.SetEmailOpaque(true)
	status, body = verify(map[string]string{"resource": account.Audience})
	if signed, refresh := shape(body); status != 200 || signed || refresh {
		t.Fatalf("the route of today: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/library/libraryfake/ -count=1
```

Expected: FAIL, build errors: `fake.NewGrant undefined`, `libraryfake.TrustKey undefined`, `fake.SetClock undefined`, `fake.Block undefined`, `fake.OnToken undefined`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/library/libraryfake [build failed]
FAIL
# github.com/monoes/mono-agent/internal/library/libraryfake_test [github.com/monoes/mono-agent/internal/library/libraryfake.test]
internal/library/libraryfake/fake_test.go:45:14: undefined: libraryfake.TrustKey
internal/library/libraryfake/fake_test.go:46:16: fake.NewGrant undefined (type *libraryfake.Server has no field or method NewGrant)
internal/library/libraryfake/fake_test.go:51:111: fake.LastResource undefined (type *libraryfake.Server has no field or method LastResource)
internal/library/libraryfake/fake_test.go:88:162: too many errors
```

- [ ] **Step 3: Make the small edits to `fake.go`.**

In `internal/library/libraryfake/fake.go`, replace this text:

```go
	accessToken, clientID string
}
```

with:

```go
	accessToken, clientID string
	resource              string // the audience the client asked for; "" = none
	spent                 bool   // a refresh token that was rotated or revoked: presenting it again is a replay
}
```

In `internal/library/libraryfake/fake.go`, replace this text:

```go
	Refreshes int
}
```

with:

```go
	Refreshes int
	// Replays counts refresh tokens presented after they were rotated or revoked.
	// monoes.me answers that with invalid_grant and deletes every refresh token of
	// the account (spike S2), so a client must never cause one.
	Replays int

	cmu          sync.Mutex // guards clock and tokenHook
	clock        func() time.Time
	tokenHook    func() // runs when a token request arrives (OnToken)
	blocked      map[string]bool
	refreshMode  RefreshMode
	opaque       bool   // a server that ignores resource: access tokens stay opaque
	emailOpaque  bool   // claim/verify answers as today: an opaque token, no refresh token
	emailTrade   bool   // claim/verify answers an opaque token beside a refresh token to trade
	lastResource string // the resource of the latest token request
	tokenReqs    []TokenRequest
}
```

In `internal/library/libraryfake/fake.go`, replace this text:

```go
		Requests: map[string]int{}}
```

with:

```go
		Requests: map[string]int{}, blocked: map[string]bool{}}
```

In `internal/library/libraryfake/fake.go`, replace this text:

```go
		g.accessExp = time.Now().Add(-time.Minute)
```

with:

```go
		g.accessExp = s.now().Add(-time.Minute)
```

In `internal/library/libraryfake/fake.go`, replace this text:

```go
	if !ok || g.revoked || time.Now().After(g.accessExp) {
```

with:

```go
	if !ok || g.revoked || s.now().After(g.accessExp) {
```

In `internal/library/libraryfake/fake.go`, replace this text:

```go
		if g, ok := s.refr[r.PostForm.Get("token")]; ok {
			g.revoked = true
		}
```

with:

```go
		if g, ok := s.refr[r.PostForm.Get("token")]; ok {
			g.revoked, g.spent = true, true
		}
```

- [ ] **Step 4: Replace `oauth.go` and add `control.go` and `jwt.go`.**

Replace the whole content of `internal/library/libraryfake/oauth.go` with exactly this content:

```go
package libraryfake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// The fake's OAuth side: authorize (as a browser already logged in as
// BrowserUser that consents at once), token (code + PKCE, refresh with
// rotation) and the email-code verify. A request that carries the resource
// indicator of the MonoAgent audience gets a signed JWT access token (spec
// §4.1), one without gets an opaque one, as monoes.me does.

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	ru, err := url.Parse(redirect)
	if q.Get("client_id") != "monoagent" || err != nil || ru.Scheme != "http" || ru.Hostname() != "127.0.0.1" || ru.Path != "/callback" {
		http.Error(w, "bad client or redirect_uri", 400)
		return
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("response_type") != "code" {
		http.Error(w, "PKCE S256 required", 400)
		return
	}
	if res := q.Get("resource"); res != "" && res != account.Audience {
		http.Error(w, "invalid_target", 400)
		return
	}
	s.mu.Lock()
	if s.blocked[s.BrowserUser.ID] {
		s.mu.Unlock()
		http.Redirect(w, r, redirect+"?"+url.Values{"error": {"access_denied"}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
		return
	}
	s.seq++
	code := fmt.Sprintf("code-%d", s.seq)
	s.codes[code] = &grant{user: s.BrowserUser, challenge: q.Get("code_challenge"), redirect: redirect,
		scopes: strings.Fields(q.Get("scope")), clientID: "monoagent", resource: q.Get("resource")}
	s.mu.Unlock()
	http.Redirect(w, r, redirect+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
}

// issue mints the tokens of g; the caller holds s.mu.
func (s *Server) issue(g *grant) map[string]any {
	s.seq++
	now := s.now()
	g.accessToken = fmt.Sprintf("at-%d", s.seq)
	if g.resource != "" && !s.opaque {
		g.accessToken = s.signJWT(g, now, s.seq)
	}
	g.refresh = fmt.Sprintf("rt-%d", s.seq)
	g.accessExp = now.Add(s.AccessTTL)
	s.access[g.accessToken] = g
	s.refr[g.refresh] = g
	return map[string]any{"access_token": g.accessToken, "refresh_token": g.refresh, "token_type": "Bearer",
		"expires_in": int(s.AccessTTL / time.Second), "scope": strings.Join(g.scopes, " ")}
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := r.PostForm
	if hook := s.onToken(); hook != nil {
		hook() // a test acts here, while the request is in flight and nothing is locked
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResource = f.Get("resource")
	hdr := r.Header.Clone()
	hdr.Del("Authorization")
	s.tokenReqs = append(s.tokenReqs, TokenRequest{Form: url.Values(f), Header: hdr})
	switch f.Get("grant_type") {
	case "authorization_code":
		g, ok := s.codes[f.Get("code")]
		delete(s.codes, f.Get("code"))
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		if !ok || g.redirect != f.Get("redirect_uri") || f.Get("client_id") != "monoagent" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "bad code or verifier"})
			return
		}
		if res := f.Get("resource"); res != "" {
			g.resource = res
		}
		if g.resource != "" && g.resource != account.Audience {
			writeJSON(w, 400, map[string]string{"error": "invalid_target"})
			return
		}
		writeJSON(w, 200, s.issue(g))
	case "refresh_token":
		if s.refuse(w) {
			return
		}
		old, ok := s.refr[f.Get("refresh_token")]
		if ok && old.spent {
			// A rotated or revoked token again: monoes.me deletes every refresh token of the account.
			s.Replays++
			s.revokeUser(old.user.ID)
		}
		if !ok || old.revoked {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		res := f.Get("resource")
		if res != "" && res != account.Audience {
			writeJSON(w, 400, map[string]string{"error": "invalid_target"})
			return
		}
		if res == "" {
			res = old.resource // the audience sticks to a chain that has one (spike S2)
		}
		s.Refreshes++
		old.revoked, old.spent = true, true // rotate
		ng := &grant{user: old.user, scopes: old.scopes, clientID: old.clientID, resource: res}
		writeJSON(w, 200, s.issue(ng))
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

// verifyEmail answers the emailed code. By default as monoes.me does once plan A's
// Task 7 is in: a request that asks for the MonoAgent audience gets the
// token endpoint's own answer, a signed access token and a refresh token; another
// audience is invalid_target before the code is looked at; a request without one
// gets an opaque access token and a refresh token, which the client trades at the
// token endpoint. SetEmailTrade and SetEmailOpaque make it answer the other two
// shapes whatever the request asks.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		ClientID string `json:"client_id"`
		Resource string `json:"resource"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	u := s.userByEmail(body.Email)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResource = body.Resource
	signed := body.Resource != "" && !s.emailOpaque && !s.emailTrade
	if signed && (body.Resource != account.Audience || body.ClientID != "monoagent") {
		writeJSON(w, 400, map[string]string{"error": "invalid_target"})
		return
	}
	if u == nil || s.blocked[u.ID] || body.Code != s.EmailCode {
		writeJSON(w, 400, map[string]string{"error": "invalid_or_expired_code"})
		return
	}
	g := &grant{user: u, clientID: "monoagent", resource: body.Resource,
		scopes: []string{"openid", "profile", "email", "offline_access", "library:read", "library:write"}}
	if signed {
		writeJSON(w, 200, s.issue(g)) // the token endpoint's own answer
		return
	}
	g.resource = "" // the answer is opaque: the audience is asked for at the trade
	if s.emailOpaque {
		s.seq++
		g.accessToken, g.accessExp = fmt.Sprintf("at-email-%d", s.seq), s.now().Add(s.AccessTTL)
		s.access[g.accessToken] = g
		writeJSON(w, 200, map[string]any{"access_token": g.accessToken, "token_type": "Bearer",
			"expires_in": int(s.AccessTTL / time.Second), "scope": strings.Join(g.scopes, " ")})
		return
	}
	writeJSON(w, 200, s.issue(g)) // an opaque access token (no resource) beside a refresh token
}

func (s *Server) userByEmail(email string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Email, email) {
			return u
		}
	}
	return nil
}
```

Create `internal/library/libraryfake/control.go` with exactly this content:

```go
package libraryfake

import (
	"net/http"
	"net/url"
	"time"
)

// RefreshMode is how the fake answers a refresh_token grant.
type RefreshMode int

const (
	RefreshOK            RefreshMode = iota // rotate and issue, as monoes.me does
	RefreshInvalidGrant                     // 400 invalid_grant: the one answer that is a refusal (spec D27)
	RefreshInvalidClient                    // 401 invalid_client
	RefreshInvalidTarget                    // 400 invalid_target
	RefreshServerError                      // 500 with a plain body
	RefreshMalformed                        // 200 with a body that is not JSON
	RefreshDrop                             // closes the connection without answering
)

// now is the fake's clock: it dates tokens and decides when they expire.
func (s *Server) now() time.Time {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// SetClock sets the fake's clock (nil: the real one).
func (s *Server) SetClock(now func() time.Time) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.clock = now
}

// OnToken registers fn to run when a request to the token endpoint arrives: in that request's own
// goroutine, before the fake looks at the request, with no lock held, and the fake answers it
// afterwards. A test cancels a caller's context in it to see what the caller does when it gives up
// while monoes.me is answering. nil removes the hook.
func (s *Server) OnToken(fn func()) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.tokenHook = fn
}

// onToken is the registered hook, read under its lock.
func (s *Server) onToken() func() {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return s.tokenHook
}

// SetRefreshMode picks how refresh_token grants are answered from now on.
func (s *Server) SetRefreshMode(m RefreshMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshMode = m
}

// SetOpaqueTokens makes the fake ignore the resource indicator everywhere: access
// tokens stay opaque, as at a monoes.me that does not mint audience-bound JWTs yet.
func (s *Server) SetOpaqueTokens(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opaque = on
}

// SetEmailOpaque makes claim/verify answer as monoes.me does today: an opaque
// access token and no refresh token, whatever the request carries.
func (s *Server) SetEmailOpaque(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailOpaque = on
}

// SetEmailTrade makes claim/verify answer an opaque access token beside a refresh
// token whatever the request carries, so the client has to trade the refresh token
// at the token endpoint for the signed one. (By default a request that asks for the
// MonoAgent audience gets the token endpoint's own answer, as in plan A's Task 7.)
func (s *Server) SetEmailTrade(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailTrade = on
}

// Block blocks a user as monoes.me's admin route does: every token the user holds
// is deleted, so a refresh answers invalid_grant (without counting as a replay),
// and no new sign-in succeeds.
func (s *Server) Block(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[userID] = true
	s.revokeUser(userID)
}

// revokeUser revokes every token of a user; the caller holds s.mu.
func (s *Server) revokeUser(userID string) {
	for _, g := range s.access {
		if g.user != nil && g.user.ID == userID {
			g.revoked = true
		}
	}
	for _, g := range s.refr {
		if g.user != nil && g.user.ID == userID {
			g.revoked = true
		}
	}
}

// Unblock lets the user sign in again. Tokens revoked by Block stay revoked.
func (s *Server) Unblock(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blocked, userID)
}

// TokenRequest is one request to the token endpoint as the fake received it.
type TokenRequest struct {
	Form   url.Values  // the form fields, refresh tokens and codes included: never print them
	Header http.Header // without Authorization
}

// TokenRequests is every request the token endpoint has received, in order. A test
// pins the exact set of fields a sign-in and a refresh send.
func (s *Server) TokenRequests() []TokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TokenRequest(nil), s.tokenReqs...)
}

// LastResource is the resource indicator of the latest token or emailed-code
// request, "" if it carried none.
func (s *Server) LastResource() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResource
}

// NewGrant is a login of username made before the machine session existed: an
// opaque access token and a refresh token, as the token endpoint answers a client
// that sends no resource.
func (s *Server) NewGrant(username string) (access, refresh string) {
	u := s.User(username)
	s.mu.Lock()
	defer s.mu.Unlock()
	g := &grant{user: u, clientID: "monoagent",
		scopes: []string{"openid", "profile", "email", "offline_access", "library:read", "library:write"}}
	s.issue(g)
	return g.accessToken, g.refresh
}

// refuse answers a refresh with the failure refreshMode asks for and says whether
// it did; the caller holds s.mu.
func (s *Server) refuse(w http.ResponseWriter) bool {
	switch s.refreshMode {
	case RefreshInvalidGrant:
		writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "the refresh token was revoked"})
	case RefreshInvalidClient:
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
	case RefreshInvalidTarget:
		writeJSON(w, 400, map[string]string{"error": "invalid_target"})
	case RefreshServerError:
		http.Error(w, "internal error", http.StatusInternalServerError)
	case RefreshMalformed:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("<html>not json"))
	case RefreshDrop:
		if h, ok := w.(http.Hijacker); ok {
			if conn, _, err := h.Hijack(); err == nil {
				conn.Close()
			}
		}
	default:
		return false
	}
	return true
}
```

Create `internal/library/libraryfake/jwt.go` with exactly this content:

```go
package libraryfake

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

var devPrivate = sync.OnceValue(func() ed25519.PrivateKey {
	_, priv := accounttest.DevKeyPair()
	return priv
})

// TrustKey makes the calling test's account.Verify trust the fake's signing key,
// the development key under accounttest.DevKID, as a devaccount build always does.
func TrustKey(t testing.TB) {
	pub, _ := accounttest.DevKeyPair()
	account.SetTrustedKeysForTest(t, []account.Key{{KID: accounttest.DevKID, Public: pub}})
}

// signJWT mints an audience-bound EdDSA access token for g's user, valid for
// AccessTTL from now, shaped like monoes.me's (plan A, spike S6): typ at+jwt, aud
// an array that holds the MonoAgent audience, azp and client_id both the client.
// jti keeps two tokens minted in the same second apart.
func (s *Server) signJWT(g *grant, now time.Time, jti int) string {
	seg := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := seg(map[string]string{"alg": "EdDSA", "typ": "at+jwt", "kid": accounttest.DevKID})
	body := seg(map[string]any{"iss": account.Issuer, "aud": []string{account.Audience, account.Issuer + "/oauth2/userinfo"},
		"azp": account.ClientID, "client_id": account.ClientID, "sub": g.user.ID, "scope": strings.Join(g.scopes, " "),
		"iat": now.Unix(), "exp": now.Add(s.AccessTTL).Unix(), "plan": "free", "jti": fmt.Sprintf("jti-%d", jti)})
	sig := ed25519.Sign(devPrivate(), []byte(head+"."+body))
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}
```

- [ ] **Step 5: Run the fake's tests and the library's.**

```bash
go test ./internal/library/libraryfake/ ./internal/library/ -count=1 -race
```

Expected: Both `ok`: the new fake tests pass and every existing library test still passes against the changed fake.

- [ ] **Step 6: Vet and format.**

```bash
go vet ./internal/library/... && gofmt -l internal/library
```

Expected: No output.

- [ ] **Step 7: Commit.**

```bash
git add internal/library/libraryfake/fake.go internal/library/libraryfake/oauth.go internal/library/libraryfake/control.go internal/library/libraryfake/jwt.go internal/library/libraryfake/fake_test.go
```

```bash
git commit -m "feat(libraryfake): sign JWT access tokens with the dev key, and fail, block and tell time on request" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4: account.NewRefresher: the refresh-token grant, and what counts as a refusal

Spec §4.4 and D27. The refresher is the one place that decides what monoes.me's answer means: only `invalid_grant` answered to a refresh-token grant is a refusal; no network, a timeout, a dropped connection, any other OAuth error, any 4xx or 5xx and a malformed body are transient, so one bad monoes.me deploy cannot log anyone out.

**Files:**
- Create: `internal/account/refresh.go`
- Test: `internal/account/refresh_test.go`

**Interfaces:**
- Consumes: `account.Refresher`, `account.TokenSet`, `account.RefusedError`, `account.TransientError`, `account.ConnectTimeout`, `account.Audience`, `account.ClientID`, `account.Reason*` (B1a); `DiscoverEndpoints`, `OAuthEndpoints` (Task 1); the fake of Task 3.
- Produces:

```go
// NewRefresher returns the Refresher for host: the refresh-token grant with resource=Audience.
func NewRefresher(host string) Refresher

func newHTTPClient(overall time.Duration) *http.Client // ConnectTimeout connect, no redirects
func checkHost(host string) error                      // https, or http to a loopback host
func oauthCode(code string) string                     // an OAuth error code fit to print, or ""
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/account/refresh_test.go` with exactly this content:

```go
package account_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

func TestRefresherSendsTheAudienceAndRotates(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	_, rt := fake.NewGrant("ada")
	ts, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt)
	if err != nil || ts.RefreshToken == "" || ts.RefreshToken == rt || fake.LastResource() != account.Audience {
		t.Fatalf("refresh: %v (rotated %v, resource %q)", err, ts != nil && ts.RefreshToken != rt, fake.LastResource())
	}
	if rec, err := account.Verify(ts.AccessToken, time.Now()); err != nil || rec.Sub != "u-ada" {
		t.Fatalf("the access token must verify and name the user: %v", err)
	}
	var refused *account.RefusedError
	if _, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt); !errors.As(err, &refused) {
		t.Fatalf("a spent refresh token must be a refusal: %v", err)
	}
}

// Spec D27: only invalid_grant is a refusal. Everything else leaves the client in
// grace, and no error ever carries the refresh token.
func TestRefresherOnlyInvalidGrantIsARefusal(t *testing.T) {
	cases := []struct {
		name    string
		mode    libraryfake.RefreshMode
		refused bool
		reason  account.Reason
	}{
		{"invalid_grant", libraryfake.RefreshInvalidGrant, true, ""},
		{"invalid_client", libraryfake.RefreshInvalidClient, false, account.ReasonServerError},
		{"invalid_target", libraryfake.RefreshInvalidTarget, false, account.ReasonServerError},
		{"http 500", libraryfake.RefreshServerError, false, account.ReasonServerError},
		{"malformed body", libraryfake.RefreshMalformed, false, account.ReasonServerError},
		{"dropped connection", libraryfake.RefreshDrop, false, account.ReasonUnreachable},
	}
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	for _, c := range cases {
		fake.SetRefreshMode(c.mode)
		_, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt)
		var refused *account.RefusedError
		var transient *account.TransientError
		switch {
		case err == nil:
			t.Errorf("%s: no error", c.name)
		case c.refused && !errors.As(err, &refused):
			t.Errorf("%s: want a refusal, got %T %v", c.name, err, err)
		case !c.refused && (errors.As(err, &refused) || !errors.As(err, &transient) || transient.Reason != c.reason):
			t.Errorf("%s: want a transient %s failure, got %T %v", c.name, c.reason, err, err)
		}
		if err != nil && strings.Contains(err.Error(), rt) {
			t.Errorf("%s: the error carries the refresh token", c.name)
		}
	}
}

func TestRefresherUnreachableAndInsecureHosts(t *testing.T) {
	dead := httptest.NewServer(nil)
	dead.Close()
	var transient *account.TransientError
	_, err := account.NewRefresher(dead.URL).Refresh(context.Background(), "rt")
	if !errors.As(err, &transient) || transient.Reason != account.ReasonUnreachable {
		t.Fatalf("no server: %T %v", err, err)
	}
	_, err = account.NewRefresher("http://monoes.example").Refresh(context.Background(), "rt")
	if !errors.As(err, &transient) || transient.Reason != account.ReasonServerError || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("plain http to a remote host: %T %v", err, err)
	}
}

// A server that does not rotate leaves the refresh token as it was.
func TestRefresherKeepsTheRefreshTokenWhenNotRotated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"access_token":"new-access"}`))
	}))
	defer srv.Close()
	ts, err := account.NewRefresher(srv.URL).Refresh(context.Background(), "old-refresh")
	if err != nil || ts.AccessToken != "new-access" || ts.RefreshToken != "old-refresh" {
		t.Fatalf("token set: %v (access token as sent %v, refresh token kept %v)", err, ts != nil && ts.AccessToken == "new-access", ts != nil && ts.RefreshToken == "old-refresh")
	}
}

// What a refresh sends is the grant, the token, the client and the audience, and
// nothing that names this machine: SECURITY.md says so, and this keeps it true.
func TestRefreshRequestSendsOnlyTheGrantFields(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	_, rt := fake.NewGrant("ada")
	if _, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt); err != nil {
		t.Fatal(err)
	}
	reqs := fake.TokenRequests()
	got := slices.Sorted(maps.Keys(reqs[len(reqs)-1].Form))
	if want := []string{"client_id", "grant_type", "refresh_token", "resource"}; !slices.Equal(got, want) {
		t.Fatalf("a refresh sent the fields %v, want %v", got, want)
	}
	if h := reqs[len(reqs)-1].Header; h.Get("Authorization") != "" || h.Get("Cookie") != "" {
		t.Fatalf("a refresh carried credentials in its headers: %v", h)
	}
	if fake.Replays != 0 {
		t.Fatal("a refresh replayed a token")
	}
}

// A Refresher that fails never returns a typed-nil error, an interface that holds a nil
// *RefusedError or a nil *TransientError: it is not nil to `err != nil`, errors.As finds a nil
// target in it, and the guard counts it as an ordinary failure. On every error path the token set
// is nil and the error is not nil and is exactly one of the two types.
func TestRefresherNeverReturnsATypedNilError(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	dead := httptest.NewServer(nil)
	dead.Close()
	live := account.NewRefresher(fake.URL)
	for _, p := range []struct {
		name string
		r    account.Refresher
		mode libraryfake.RefreshMode
	}{
		{"invalid_grant", live, libraryfake.RefreshInvalidGrant},
		{"invalid_client", live, libraryfake.RefreshInvalidClient},
		{"invalid_target", live, libraryfake.RefreshInvalidTarget},
		{"http 500", live, libraryfake.RefreshServerError},
		{"malformed body", live, libraryfake.RefreshMalformed},
		{"dropped connection", live, libraryfake.RefreshDrop},
		{"no server", account.NewRefresher(dead.URL), libraryfake.RefreshOK},
		{"plain http to a remote host", account.NewRefresher("http://monoes.example"), libraryfake.RefreshOK},
	} {
		fake.SetRefreshMode(p.mode)
		ts, err := p.r.Refresh(context.Background(), rt)
		var refused *account.RefusedError
		var transient *account.TransientError
		isRefused, isTransient := errors.As(err, &refused), errors.As(err, &transient)
		if ts != nil || err == nil || isRefused == isTransient || (isRefused && refused == nil) || (isTransient && transient == nil) {
			t.Errorf("%s: token set %v, error %T (refusal %v, transient %v)", p.name, ts != nil, err, isRefused, isTransient)
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/account/ -run '^(TestRefresherSendsTheAudienceAndRotates|TestRefresherOnlyInvalidGrantIsARefusal|TestRefresherUnreachableAndInsecureHosts|TestRefresherKeepsTheRefreshTokenWhenNotRotated|TestRefreshRequestSendsOnlyTheGrantFields|TestRefresherNeverReturnsATypedNilError)$' -count=1
```

Expected: FAIL, build error: `undefined: account.NewRefresher`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
FAIL
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/refresh_test.go:23:21: undefined: account.NewRefresher
internal/account/refresh_test.go:31:23: undefined: account.NewRefresher
internal/account/refresh_test.go:57:21: undefined: account.NewRefresher
internal/account/refresh_test.go:111:23: undefined: account.NewRefresher
```

- [ ] **Step 3: Implement the refresher.**

Create `internal/account/refresh.go` with exactly this content:

```go
package account

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// refreshTimeout bounds one refresh round trip. ConnectTimeout bounds the connect
// inside it, so a machine with no network pays two seconds, not ten.
const refreshTimeout = 10 * time.Second

// newHTTPClient is the client every call to monoes.me from this package uses: a
// ConnectTimeout connect (spec §4.4), an overall deadline, and no redirects, so a
// token or a refresh token can never follow a redirect to another host.
func newHTTPClient(overall time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: ConnectTimeout}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: overall,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// checkHost refuses a host a token must not travel to: https only, or plain http
// to a loopback host (a local development server).
func checkHost(host string) error {
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return fmt.Errorf("account: invalid monoes.me host %q", host)
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("account: the monoes.me host %s must be https (plain http only for a loopback development server)", host)
	}
	return nil
}

// NewRefresher returns the Refresher for host: the OAuth refresh-token grant with
// resource=Audience. It answers *RefusedError for invalid_grant and nothing else
// (spec D27); every other failure is a *TransientError, so a bad monoes.me deploy
// never logs anyone out.
func NewRefresher(host string) Refresher {
	return &httpRefresher{host: strings.TrimRight(host, "/"), hc: newHTTPClient(refreshTimeout)}
}

type httpRefresher struct {
	host string
	hc   *http.Client

	mu sync.Mutex
	ep *OAuthEndpoints
}

func (r *httpRefresher) tokenEndpoint(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ep == nil {
		ep, err := DiscoverEndpoints(ctx, r.hc, r.host)
		if err != nil {
			return "", err // no answer at all: the POST would wait as long again
		}
		r.ep = ep
	}
	return r.ep.TokenEndpoint, nil
}

// Refresh sends the grant. The guard calls it with a context that the caller's cancellation does
// not reach and that ends by a deadline of its own (A20), so a grant that has been sent is always
// read to its end: the answer holds the only copy of the refresh token that replaces this one. A
// call is at most two round trips, the endpoint discovery (the first time) and the grant, each
// bounded by refreshTimeout. On every failure the token set is nil and the error is a non-nil
// *RefusedError (invalid_grant only) or a non-nil *TransientError, never a typed-nil error, which
// the guard would count as an ordinary failure (TestRefresherNeverReturnsATypedNilError).
func (r *httpRefresher) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	if err := checkHost(r.host); err != nil {
		return nil, &TransientError{Reason: ReasonServerError, Err: err}
	}
	endpoint, err := r.tokenEndpoint(ctx)
	if err != nil {
		return nil, &TransientError{Reason: ReasonUnreachable, Err: err}
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {ClientID}, "resource": {Audience}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &TransientError{Reason: ReasonServerError, Err: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, &TransientError{Reason: ReasonUnreachable, Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, &TransientError{Reason: ReasonUnreachable, Err: err} // dropped mid-answer
	}
	var tr struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	bad := json.Unmarshal(body, &tr) != nil
	switch {
	case resp.StatusCode/100 == 4 && !bad && tr.Error == "invalid_grant":
		return nil, &RefusedError{Description: tr.ErrorDescription}
	case resp.StatusCode/100 != 2:
		return nil, &TransientError{Reason: ReasonServerError, Err: fmt.Errorf("the token endpoint answered HTTP %d %s", resp.StatusCode, oauthCode(tr.Error))}
	case bad || tr.AccessToken == "":
		return nil, &TransientError{Reason: ReasonServerError, Err: fmt.Errorf("the token endpoint answered something that is not a token")}
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken // not rotated: the same one stays good
	}
	return &TokenSet{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}, nil
}

// oauthCode is an OAuth error code fit to print: short and made of letters and underscores.
func oauthCode(code string) string {
	if len(code) == 0 || len(code) > 40 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz_") != "" {
		return ""
	}
	return "(" + code + ")"
}
```

- [ ] **Step 4: Run the tests with the race detector.**

```bash
go test ./internal/account/ -run '^(TestRefresherSendsTheAudienceAndRotates|TestRefresherOnlyInvalidGrantIsARefusal|TestRefresherUnreachableAndInsecureHosts|TestRefresherKeepsTheRefreshTokenWhenNotRotated|TestRefreshRequestSendsOnlyTheGrantFields|TestRefresherNeverReturnsATypedNilError)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/account`.

- [ ] **Step 5: Vet and format.**

```bash
go vet ./internal/account/ && gofmt -l internal/account
```

Expected: No output.

- [ ] **Step 6: Commit.**

```bash
git add internal/account/refresh.go internal/account/refresh_test.go
```

```bash
git commit -m "feat(account): the network refresher; only invalid_grant is a refusal" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 5: account.Client: browser and emailed-code sign-in

Spec §7. A `Client` over one host and one `Store` signs in through the browser (`resource` in both the authorize and the token request) or with an emailed code, verifies what monoes.me returns with B1a's `Verify`, builds the session with B1a's `NewSession`, and writes the refresh token first and the session second, so a session never exists without the means to renew it. The emailed code follows plan A's Task 7: the request names the audience and monoes.me answers as its token endpoint does, so the sign-in is one call; a route that answers a refresh token beside an opaque token is served by trading it once. Today's route ignores the audience and answers an opaque token with no refresh token (monoes-landing `src/app/api/auth/agent/claim/verify/route.ts`, lines 92 to 112): the client then says so and stores nothing.

**Files:**
- Create: `internal/account/login.go`
- Test: `internal/account/login_test.go`

**Interfaces:**
- Consumes: B1a: `account.Store`, `account.Session`, `account.Status`, `account.User`, `account.Verify`, `account.Evaluate`, `account.VerifyError`, `account.OpenStore`, `account.NewMemorySealer`, `account.ErrKeyringUnavailable` (frozen contract) and `account.NewSession(host, accessToken string, user *User, now time.Time) (*Session, error)` (B1a's contract change request 4). This plan: `OAuthEndpoints`, `DiscoverEndpoints`, `AuthorizeOptions`, `AuthorizeInBrowser` (Task 1), the fake (Task 3), `newHTTPClient`, `checkHost` and `oauthCode` (Task 4).
- Produces:

```go
type LoginOptions struct {
	Open    func(url string) error
	OnURL   func(url string)
	Timeout time.Duration
}

type Client struct {
	Host  string
	HTTP  *http.Client
	Store Store
	Now   func() time.Time
}

func NewClient(host string, store Store) *Client
func (c *Client) Login(ctx context.Context, o LoginOptions) (Status, error)
func (c *Client) SendEmailCode(ctx context.Context, email string) error
func (c *Client) VerifyEmailCode(ctx context.Context, email, code string) (Status, error)

var ErrBadCode error                 // monoes.me refused the emailed code
var ErrEmailSessionUnavailable error // the email answer cannot make a session
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/account/login_test.go` with exactly this content:

```go
package account_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// newFakeClient is a Client for the fake over a temp store, with the fake's key trusted.
func newFakeClient(t *testing.T, fake *libraryfake.Server) (*account.Client, account.Store) {
	t.Helper()
	libraryfake.TrustKey(t)
	st := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	return account.NewClient(fake.URL, st), st
}

// userBrowser plays the user's userBrowser: it opens the authorize URL, which redirects
// back to the loopback listener.
func userBrowser(t *testing.T) func(string) error {
	return func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err != nil {
				t.Errorf("userBrowser: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

// sessionSummary describes a session without its tokens, for failure messages.
func sessionSummary(s *account.Session) string {
	if s == nil {
		return "no session"
	}
	return fmt.Sprintf("host %s, state %q, reason %q, access token of %d bytes", s.Host, s.State, s.Reason, len(s.AccessToken))
}

func signInAtFake(t *testing.T, c *account.Client) account.Status {
	t.Helper()
	st, err := c.Login(context.Background(), account.LoginOptions{Open: userBrowser(t), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return st
}

func TestLoginEstablishesTheSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	var shown string
	st, err := c.Login(context.Background(), account.LoginOptions{Open: userBrowser(t), OnURL: func(u string) { shown = u }, Timeout: 10 * time.Second})
	if err != nil || st.State != account.StateOK || st.User == nil || st.User.Username != "ada" || st.User.Email != "ada@example.com" {
		t.Fatalf("status %+v, %v", st, err)
	}
	if !strings.Contains(shown, "resource=https%3A%2F%2Fmonoes.me%2Fapi%2Fmonoagent") || fake.LastResource() != account.Audience {
		t.Fatalf("the audience was not sent (authorize %s, token %q)", shown, fake.LastResource())
	}
	sess, _ := store.Load()
	rt, _ := store.LoadRefresh()
	rec, verr := account.Verify(sess.AccessToken, time.Now())
	if sess.Host != fake.URL || rt == "" || verr != nil || !sess.HW.Equal(rec.IssuedAt) || sess.LastResult != "ok" || sess.State != "" {
		t.Fatalf("session: %s (refresh token stored: %v)", sessionSummary(sess), rt != "")
	}
	// The code exchange sends the grant, the code, the verifier and the audience, and nothing that names this machine.
	reqs := fake.TokenRequests()
	got := slices.Sorted(maps.Keys(reqs[0].Form))
	if want := []string{"client_id", "code", "code_verifier", "grant_type", "redirect_uri", "resource"}; !slices.Equal(got, want) {
		t.Fatalf("the code exchange sent the fields %v, want %v", got, want)
	}
}

func TestLoginReplacesARefusedSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	if err := store.Save(&account.Session{V: 1, Host: fake.URL, State: "refused", Reason: "refused"}); err != nil {
		t.Fatal(err)
	}
	if st := signInAtFake(t, c); st.State != account.StateOK {
		t.Fatalf("status %+v", st)
	}
	if sess, _ := store.Load(); sess.State != "" || sess.Reason != "" {
		t.Fatalf("the refusal outlived the new sign-in: %s", sessionSummary(sess))
	}
}

// A monoes.me that does not mint audience-bound JWTs yet answers an opaque token;
// nothing may be stored from it.
func TestLoginAnOpaqueAnswerStoresNothing(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetOpaqueTokens(true)
	c, store := newFakeClient(t, fake)
	_, err := c.Login(context.Background(), account.LoginOptions{Open: userBrowser(t), Timeout: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(store.Dir(), "session.json")); !os.IsNotExist(serr) {
		t.Fatalf("a session was written from an opaque token: %v", serr)
	}
}

type unavailableSealer struct{}

func (unavailableSealer) Seal([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }
func (unavailableSealer) Open([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }

// Spec §9: sign-in fails closed without a key store, and leaves no session behind.
func TestLoginWithoutAKeyStoreWritesNoSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	store := account.OpenStore(t.TempDir(), unavailableSealer{})
	_, err := account.NewClient(fake.URL, store).Login(context.Background(), account.LoginOptions{Open: userBrowser(t), Timeout: 10 * time.Second})
	if !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session without its refresh token: %s", sessionSummary(sess))
	}
}

func TestEmailSignInStartsASession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	ctx := context.Background()
	if err := c.SendEmailCode(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VerifyEmailCode(ctx, "ada@example.com", "000000"); !errors.Is(err, account.ErrBadCode) {
		t.Fatalf("a wrong code: %v", err)
	}
	st, err := c.VerifyEmailCode(ctx, " Ada@Example.com ", "123456")
	if err != nil || st.State != account.StateOK || st.User.Username != "ada" {
		t.Fatalf("status %+v, %v", st, err)
	}
	if rt, _ := store.LoadRefresh(); rt == "" || fake.LastResource() != account.Audience || fake.Replays != 0 {
		t.Fatal("the code was verified without the audience, or the session has no refresh token, or a token was replayed")
	}
	if n := len(fake.TokenRequests()); n != 0 {
		t.Fatalf("%d token requests: a code that names the audience is answered with the signed session in one call", n)
	}
}

// A route that answers a refresh token beside an opaque access token, whatever the
// request asks (plan A's Task 7 does so for a request without the audience), is
// served by trading that refresh token at the token endpoint, with the audience,
// for the signed one. The refresh token that comes back is the one kept: the emailed
// one is spent and never stored.
func TestEmailSignInTradesARefreshTokenAnsweredBesideAnOpaqueToken(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetEmailTrade(true)
	c, store := newFakeClient(t, fake)
	before := len(fake.TokenRequests())
	st, err := c.VerifyEmailCode(context.Background(), "ada@example.com", "123456")
	if err != nil || st.State != account.StateOK || st.User.Username != "ada" {
		t.Fatalf("status %+v, %v", st, err)
	}
	reqs := fake.TokenRequests()
	if len(reqs) != before+1 || reqs[before].Form.Get("grant_type") != "refresh_token" || reqs[before].Form.Get("resource") != account.Audience {
		t.Fatalf("%d token requests: want one refresh grant that names the audience", len(reqs)-before)
	}
	if rt, _ := store.LoadRefresh(); rt == "" || rt == reqs[before].Form.Get("refresh_token") || fake.Replays != 0 {
		t.Fatal("the emailed refresh token was kept instead of the rotated one, or was replayed")
	}
}

// Today's claim endpoint ignores the audience and answers an opaque token with no
// refresh token. The sign-in says so, and stores nothing.
func TestEmailSignInAtAServerThatCannotSignTheSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetEmailOpaque(true)
	c, store := newFakeClient(t, fake)
	if _, err := c.VerifyEmailCode(context.Background(), "ada@example.com", "123456"); !errors.Is(err, account.ErrEmailSessionUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session was stored: %s", sessionSummary(sess))
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/account/ -run '^(TestLoginEstablishesTheSession|TestLoginReplacesARefusedSession|TestLoginAnOpaqueAnswerStoresNothing|TestLoginWithoutAKeyStoreWritesNoSession|TestEmailSignInStartsASession|TestEmailSignInTradesARefreshTokenAnsweredBesideAnOpaqueToken|TestEmailSignInAtAServerThatCannotSignTheSession)$' -count=1
```

Expected: FAIL, build errors: `undefined: account.NewClient`, `undefined: account.ErrBadCode`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
FAIL
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/login_test.go:21:70: undefined: account.Client
internal/account/login_test.go:25:17: undefined: account.NewClient
internal/account/login_test.go:52:44: undefined: account.Client
internal/account/login_test.go:192:109: too many errors
```

- [ ] **Step 3: Implement the sign-in client.**

Create `internal/account/login.go` with exactly this content:

```go
package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// loginScopes are what a sign-in asks for: the community scopes, the two library
// scopes (the machine session serves the library too, spec D21) and offline_access
// for the refresh token.
const loginScopes = "openid profile email offline_access library:read library:write"

// identityPath is where the signed-in user's id, email and username are read: the
// library's own "who am I", which every MonoAgent token already works at.
const identityPath = "/api/library/me"

var (
	// ErrBadCode is monoes.me refusing an emailed code.
	ErrBadCode = errors.New("the code is wrong or expired")
	// ErrEmailSessionUnavailable ends an email sign-in whose answer holds no signed
	// token with a refresh token: no session can be kept from it.
	ErrEmailSessionUnavailable = errors.New("monoes.me's email sign-in cannot start a machine session yet; sign in with the browser instead: monoagentcli account login")
)

// LoginOptions controls Login.
type LoginOptions struct {
	Open    func(url string) error // opens the browser; nil does not
	OnURL   func(url string)       // told the URL before Open runs
	Timeout time.Duration          // default 5 minutes
}

// Client talks to one monoes.me host: the sign-in flows, logout and adoption. The
// package functions Login, Logout and the rest use the production host and store.
type Client struct {
	Host  string // base URL without a trailing slash
	HTTP  *http.Client
	Store Store
	Now   func() time.Time
}

// NewClient returns a Client for host over store.
func NewClient(host string, store Store) *Client {
	return &Client{Host: strings.TrimRight(host, "/"), HTTP: newHTTPClient(30 * time.Second), Store: store, Now: time.Now}
}

// answerError is a monoes.me answer that is not a success.
type answerError struct {
	status int
	msg    string
}

func (e *answerError) Error() string { return e.msg }

// unusableError is a sign-in monoes.me answered with something no session can be
// made of: a token that does not verify, or no refresh token.
type unusableError struct{ err error }

func (e *unusableError) Unwrap() error { return e.err }
func (e *unusableError) Error() string {
	var ve *VerifyError
	if errors.As(e.err, &ve) {
		switch ve.Reason {
		case ReasonKeyUnknown:
			return "monoes.me signed the session with a key this version does not know: run `monoagentcli update`, then sign in again"
		case ReasonClockSkew:
			return "this machine's clock is more than 5 minutes behind monoes.me's: correct the date and time, then sign in again"
		}
		return "monoes.me returned a session this version cannot verify: run `monoagentcli update`, then sign in again"
	}
	return "monoes.me did not return a usable session: " + e.err.Error()
}

type tokenAnswer struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// readTokens sends req and returns the tokens of a 200 answer.
func (c *Client) readTokens(req *http.Request) (*TokenSet, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("monoes.me unreachable (%s): %w", c.Host, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var a tokenAnswer
	_ = json.Unmarshal(b, &a)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("monoes.me answered HTTP %d", resp.StatusCode)
		if oauthCode(a.Error) != "" {
			msg += " " + oauthCode(a.Error)
		}
		return nil, &answerError{status: resp.StatusCode, msg: msg}
	}
	if a.AccessToken == "" {
		return nil, errors.New("monoes.me's answer carried no access token")
	}
	return &TokenSet{AccessToken: a.AccessToken, RefreshToken: a.RefreshToken}, nil
}

func (c *Client) exchange(ctx context.Context, endpoint string, form url.Values) (*TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.readTokens(req)
}

// Login runs the browser sign-in (OAuth 2.1, PKCE, a loopback redirect) for the
// MonoAgent audience and stores the session it returns.
func (c *Client) Login(ctx context.Context, o LoginOptions) (Status, error) {
	if err := checkHost(c.Host); err != nil {
		return Status{}, err
	}
	ep, err := DiscoverEndpoints(ctx, c.HTTP, c.Host)
	if err != nil {
		return Status{}, fmt.Errorf("monoes.me unreachable (%s): %w", c.Host, err)
	}
	res, err := AuthorizeInBrowser(ctx, ep.AuthorizationEndpoint,
		url.Values{"client_id": {ClientID}, "scope": {loginScopes}, "resource": {Audience}},
		AuthorizeOptions{Open: o.Open, OnURL: o.OnURL, Timeout: o.Timeout, Label: "account login",
			SuccessText: "MonoAgent is now signed in to monoes.me. You can close this tab."})
	if err != nil {
		return Status{}, err
	}
	// A code is single use and rotates nothing: a call abandoned here leaves no refresh token on disk
	// that could be presented again, so this exchange needs no completion guarantee (A20).
	ts, err := c.exchange(ctx, ep.TokenEndpoint, url.Values{"grant_type": {"authorization_code"}, "code": {res.Code},
		"redirect_uri": {res.Redirect}, "client_id": {ClientID}, "code_verifier": {res.Verifier}, "resource": {Audience}})
	if err != nil {
		return Status{}, fmt.Errorf("account login: exchange the code: %w", err)
	}
	return c.establish(ctx, ts, nil)
}

// SendEmailCode asks monoes.me to email a sign-in code. It answers the same
// whether or not the address has an account.
func (c *Client) SendEmailCode(ctx context.Context, email string) error {
	if err := checkHost(c.Host); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"email": email, "client_id": ClientID, "scope": loginScopes})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+"/api/auth/agent/claim", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("monoes.me unreachable (%s): %w", c.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("monoes.me answered HTTP %d to the code request", resp.StatusCode)
	}
	return nil
}

// VerifyEmailCode trades an emailed code for a session and stores it. The request
// names the MonoAgent audience. monoes.me answers as its token endpoint does, a
// signed access token and a refresh token (plan A, Task 7), and that is the session;
// or it answers a refresh token beside an opaque access token, and the refresh token
// is traded at the token endpoint, with the audience, for the signed one. A monoes.me
// that answers no refresh token, as the route does today, cannot start a machine
// session: the result is ErrEmailSessionUnavailable and nothing is stored. The
// emailed refresh token is presented once at most and never stored.
func (c *Client) VerifyEmailCode(ctx context.Context, email, code string) (Status, error) {
	if err := checkHost(c.Host); err != nil {
		return Status{}, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	body, _ := json.Marshal(map[string]string{"email": email, "code": code, "client_id": ClientID, "resource": Audience})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+"/api/auth/agent/claim/verify", bytes.NewReader(body))
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	ts, err := c.readTokens(req)
	var ae *answerError
	if errors.As(err, &ae) && ae.status == http.StatusBadRequest {
		return Status{}, ErrBadCode
	}
	if err != nil {
		return Status{}, err
	}
	if ts.RefreshToken == "" {
		return Status{}, ErrEmailSessionUnavailable
	}
	// The trade spends the emailed refresh token, which is never stored: a trade the caller abandons
	// leaves no refresh token on disk that could be presented again (A20), and a new code starts over.
	if strings.Count(ts.AccessToken, ".") != 2 { // opaque, not a JWS: the signed one is asked for at the trade
		if ts, err = NewRefresher(c.Host).Refresh(ctx, ts.RefreshToken); err != nil {
			return Status{}, fmt.Errorf("account login: trade the emailed code's refresh token: %w", err)
		}
	}
	st, err := c.establish(ctx, ts, &User{Email: email})
	var ue *unusableError
	var ve *VerifyError
	if errors.As(err, &ue) && errors.As(ue, &ve) && ve.Reason == ReasonInvalid {
		return Status{}, ErrEmailSessionUnavailable // an opaque access token beside a refresh token
	}
	return st, err
}

// session turns ts into the Session to store: it verifies the access token, looks
// up who the user is, and builds the session as B1a does everywhere else
// (NewSession, which starts the clock guard at the token's iat). It reads and
// writes no store.
func (c *Client) session(ctx context.Context, ts *TokenSet, hint *User) (*Session, error) {
	now := c.Now()
	rec, err := Verify(ts.AccessToken, now)
	if err != nil {
		return nil, &unusableError{err}
	}
	if ts.RefreshToken == "" {
		return nil, &unusableError{errors.New("it came without a refresh token")}
	}
	sess, err := NewSession(c.Host, ts.AccessToken, c.lookupUser(ctx, ts.AccessToken, rec.Sub, hint), now)
	if err != nil {
		return nil, &unusableError{err}
	}
	return sess, nil
}

// commit writes the refresh token, then the session, so a session never exists
// without the means to renew it. The caller holds the store lock.
func (c *Client) commit(sess *Session, refresh string) error {
	if err := c.Store.SaveRefresh(refresh); err != nil {
		return err
	}
	return c.Store.Save(sess)
}

// establish stores ts as the machine session, replacing whatever was there
// (including a refusal), and returns its verdict.
func (c *Client) establish(ctx context.Context, ts *TokenSet, hint *User) (Status, error) {
	sess, err := c.session(ctx, ts, hint)
	if err != nil {
		return Status{}, err
	}
	unlock, err := c.Store.Lock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	if err := c.commit(sess, ts.RefreshToken); err != nil {
		return Status{}, err
	}
	return Evaluate(sess, c.Now()), nil
}

// lookupUser names the signed-in user: the token's subject, and the id, email and
// username monoes.me reports for it when it answers and agrees on the subject.
func (c *Client) lookupUser(ctx context.Context, token, sub string, hint *User) *User {
	u := &User{ID: sub}
	if hint != nil {
		u.Email, u.Username = hint.Email, hint.Username
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.Host+identityPath, nil)
	if err != nil {
		return u
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return u
	}
	defer resp.Body.Close()
	var me struct {
		User User `json:"user"`
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me) == nil && me.User.ID == sub {
		return &me.User
	}
	return u
}
```

- [ ] **Step 4: Run the tests with the race detector.**

```bash
go test ./internal/account/ -run '^(TestLoginEstablishesTheSession|TestLoginReplacesARefusedSession|TestLoginAnOpaqueAnswerStoresNothing|TestLoginWithoutAKeyStoreWritesNoSession|TestEmailSignInStartsASession|TestEmailSignInTradesARefreshTokenAnsweredBesideAnOpaqueToken|TestEmailSignInAtAServerThatCannotSignTheSession)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/account`.

- [ ] **Step 5: Vet and format.**

```bash
go vet ./internal/account/ && gofmt -l internal/account
```

Expected: No output.

- [ ] **Step 6: Commit.**

```bash
git add internal/account/login.go internal/account/login_test.go
```

```bash
git commit -m "feat(account): browser and emailed-code sign-in that stores the signed session" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 6: account.Client: logout and adoption of an older refresh token

Logout (spec §7) revokes the refresh token at monoes.me when it can and forgets the login whatever monoes.me answers: it deletes the refresh token and replaces the session by one with no token that keeps the machine's clock-guard record, because the high-water mark is what makes a clock set back worthless and an open command must not erase it (A23); it reads the refresh token it revokes under the store lock, so it never revokes one that another process has rotated since. Adoption (D23) exchanges an older library refresh token for a session without ever producing a refusal. Because monoes.me ends every refresh token of an account when a spent one is presented again (plan A, S2), it runs entirely under the store lock: the older login is read, exchanged and updated by callbacks while no other process can read it; because the exchange spends the older token, the tokens monoes.me issued come back to the caller even when no session could be made, so the older login can be kept alive, and an `invalid_grant` answer comes back as `Dead`, so the caller can remove a token that must never be presented again; and the exchange, once sent, is completed and stored whatever the caller does next (A20).

**Files:**
- Create: `internal/account/logout.go`
- Create: `internal/account/adopt.go`
- Test: `internal/account/logout_test.go`
- Test: `internal/account/adopt_test.go`

**Interfaces:**
- Consumes: `Client`, `Client.session`, `Client.commit`, `unusableError` (Task 5); `NewRefresher` (Task 4); `libraryfake.OnToken` (Task 3), in the tests; B1a's `Session`, `Store.Lock`, `Store.Load`, `Store.Save`, `Store.LoadRefresh`, `Store.DeleteRefresh`, `Store.Dir`, and `refreshCallTimeout` (`guard.go`, 20 seconds: the guard's backstop on one Refresher call).
- Produces:

```go
func (c *Client) Logout(ctx context.Context) error

type AdoptResult struct {
	Adopted bool
	Status  Status    // when Adopted
	Tokens  *TokenSet // what monoes.me issued when it answered with tokens: the exchange spent the older refresh token
	Dead    bool      // monoes.me answered invalid_grant: the older refresh token is spent, revoked or expired
}

// Adopt, all under the store lock: no session yet; older() names the older login's refresh token and user; exchange it;
// verify; store; done(result) before the lock is released. Never a refusal, never a stored "refused" state.
func (c *Client) Adopt(ctx context.Context, older func() (refreshToken string, user *User, ok bool), done func(AdoptResult)) (AdoptResult, error)
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/account/logout_test.go` with exactly this content:

```go
package account_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// tokenless checks what logout leaves of a session (A23): no access token, no refresh token, and the
// machine's clock-guard record, the high-water mark. It returns that record.
func tokenless(t *testing.T, store account.Store) *account.Session {
	t.Helper()
	sess, err := store.Load()
	if err != nil || sess == nil || sess.AccessToken != "" || sess.State != "" || sess.HW.IsZero() {
		t.Fatalf("logout must leave a session with no token and a high-water mark, not %s (%v)", sessionSummary(sess), err)
	}
	if rt, _ := store.LoadRefresh(); rt != "" {
		t.Fatal("refresh token kept")
	}
	return sess
}

func TestLogoutRevokesAndForgetsEvenOffline(t *testing.T) {
	fake := libraryfake.New()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	rt, _ := store.LoadRefresh()
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	var refused *account.RefusedError
	if _, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt); !errors.As(err, &refused) {
		t.Fatalf("the refresh token was not revoked at monoes.me: %v", err)
	}
	tokenless(t, store) // the login is gone; the clock-guard record is not

	// Offline: monoes.me is gone, the local state still goes.
	signInAtFake(t, c)
	fake.Close()
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("offline logout: %v", err)
	}
	tokenless(t, store)
}

// An unreadable session.json is replaced like any other: its mark is lost with it, so the record
// starts from now (the file was there, so the machine had been checked).
func TestLogoutForgetsAnUnreadableSession(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := account.OpenStore(dir, account.NewMemorySealer())
	if err := account.NewClient("https://monoes.example", store).Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	tokenless(t, store)
}

// Spec 4.5 and D5, A23: a clock set back must not postpone the enforcement date, and an account that
// monoes.me has blocked must not get out from under that by logging out, an open command, first.
// Logout keeps the machine's high-water mark in a session with no token, so the date is still judged
// against it: with the clock set before the date the machine is not logged in, and enforced.
func TestLogoutKeepsTheClockGuardRecord(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	date := time.Now().Add(-time.Hour) // the date has passed: this machine is enforced
	account.SetEnforceFromForTest(t, date)
	signInAtFake(t, c)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	sess := tokenless(t, store)
	if sess.HW.Before(date) {
		t.Fatalf("the high-water mark %v is older than the date %v that the machine has already seen", sess.HW, date)
	}
	setBack := date.Add(-48 * time.Hour) // the clock is set to before the date
	if st := account.Evaluate(sess, setBack); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced {
		t.Fatalf("with the clock set back: %+v, want locked(not_logged_in), enforced", st)
	}
	// Without the record there is nothing to judge the date against: the hole logout used to open.
	if st := account.Evaluate(nil, setBack); st.Enforced {
		t.Fatalf("with no record the same clock is not enforced, and this test would prove nothing: %+v", st)
	}
}

// Logging out twice is the same as once: the second finds a record with no token and nothing to
// forget, and leaves it as it is (nothing is written, the mark does not move).
func TestASecondLogoutChangesNothing(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir(), "session.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(path); !bytes.Equal(first, second) {
		t.Fatal("a second logout rewrote the clock-guard record")
	}
}

// A new sign-in replaces the record that logout left: the session is whole again.
func TestLoginAfterLogoutReplacesTheRecord(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := signInAtFake(t, c); st.State != account.StateOK {
		t.Fatalf("status %+v", st)
	}
	sess, _ := store.Load()
	if rt, _ := store.LoadRefresh(); sess == nil || sess.AccessToken == "" || rt == "" {
		t.Fatalf("a sign-in after a logout did not make a whole session: %s (refresh token stored: %v)", sessionSummary(sess), rt != "")
	}
}

// monoes.me ends every refresh token of an account when a rotated one is presented again, and a
// refresh in another process rotates the one that logout would have read first. So logout reads the
// refresh token it revokes under the lock: the live one, not a rotated-away one. Here another
// process refreshes while logout waits for the lock.
func TestLogoutRevokesTheRefreshTokenThatIsOnDiskWhenItHoldsTheLock(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	rt, _ := store.LoadRefresh()
	ctx := context.Background()
	unlock, err := store.Lock(ctx) // another process holds the lock, in the middle of a refresh
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Logout(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("logout did not wait for the lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	rotated, err := account.NewRefresher(fake.URL).Refresh(ctx, rt) // the other process's refresh: rt is spent now
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh(rotated.RefreshToken); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var refused *account.RefusedError
	if _, err := account.NewRefresher(fake.URL).Refresh(ctx, rotated.RefreshToken); !errors.As(err, &refused) {
		t.Fatalf("the refresh token that was on disk when logout held the lock was not revoked: %v", err)
	}
}

func TestLogoutWithNothingToForgetLeavesNoFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	c := account.NewClient("https://monoes.example", account.OpenStore(dir, account.NewMemorySealer()))
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("logout made %s: %v", dir, err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A refresh token never travels in the clear: nothing is sent to a remote host over
// plain http, and the local copy goes all the same.
func TestLogoutNeverRevokesOverPlainHTTP(t *testing.T) {
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	if err := store.SaveRefresh("a-refresh-token"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := account.NewClient("http://monoes.example", store)
	c.HTTP = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("offline")
	})}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt, _ := store.LoadRefresh(); calls != 0 || rt != "" {
		t.Fatalf("%d requests went out, refresh token kept: %v", calls, rt != "")
	}
	// A refresh token with no session behind it (a sign-in that stopped between its two writes)
	// leaves no clock-guard record to keep: none is made.
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("logout made a session out of nothing: %s", sessionSummary(sess))
	}
}
```

Create `internal/account/adopt_test.go` with exactly this content:

```go
package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// olderLogin is what a caller of Adopt hands over: one older login's refresh token.
func olderLogin(rt string) func() (string, *account.User, bool) {
	return func() (string, *account.User, bool) { return rt, &account.User{ID: "u-ada", Username: "ada"}, true }
}

// Spec D23: an older refresh token becomes the session when monoes.me honors it.
func TestAdoptStoresASessionFromAnOlderRefreshToken(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	var told []account.AdoptResult
	res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = append(told, r) })
	if err != nil || !res.Adopted || res.Status.State != account.StateOK || res.Status.User.Username != "ada" || len(told) != 1 || !told[0].Adopted {
		t.Fatalf("adopted %v, state %q, told %d times, %v", res.Adopted, res.Status.State, len(told), err)
	}
	if got, _ := store.LoadRefresh(); got == "" || got == rt {
		t.Fatal("the rotated refresh token was not stored")
	}
	before := fake.Refreshes
	again, err := c.Adopt(context.Background(), func() (string, *account.User, bool) {
		t.Error("a session exists: the older login must not even be read")
		return rt, nil, true
	}, nil)
	if err != nil || again.Adopted || again.Tokens != nil || fake.Refreshes != before || fake.Replays != 0 {
		t.Fatalf("a second adoption must not ask monoes.me anything (adopted %v, refreshes %d -> %d, replays %d, %v)", again.Adopted, before, fake.Refreshes, fake.Replays, err)
	}
}

// Whatever monoes.me answers to the attempt is "sign in once more": never a stored
// refusal (D23). invalid_grant says the older token is Dead, so the caller removes
// it; the tokens monoes.me did issue come back, so the older login survives.
func TestAdoptNeverReadsAnAnswerAsARefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		mode libraryfake.RefreshMode
		dead bool
	}{"invalid_grant": {libraryfake.RefreshInvalidGrant, true}, "http 500": {libraryfake.RefreshServerError, false},
		"dropped": {libraryfake.RefreshDrop, false}} {
		fake := libraryfake.New()
		c, store := newFakeClient(t, fake)
		_, rt := fake.NewGrant("ada")
		fake.SetRefreshMode(tc.mode)
		var told account.AdoptResult
		res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = r })
		if err != nil || res.Adopted || res.Tokens != nil || res.Dead != tc.dead || told.Dead != tc.dead {
			t.Errorf("%s: adopted %v, tokens %v, dead %v, %v", name, res.Adopted, res.Tokens != nil, res.Dead, err)
		}
		if sess, _ := store.Load(); sess != nil {
			t.Errorf("%s: a session was stored (%s)", name, sessionSummary(sess))
		}
		fake.Close()
	}
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetOpaqueTokens(true) // resource ignored: the exchange succeeds, the token cannot be a session
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	var told account.AdoptResult
	res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = r })
	if err != nil || res.Adopted || res.Tokens == nil || res.Tokens.RefreshToken == "" || res.Tokens.RefreshToken == rt || told.Tokens == nil {
		t.Fatalf("opaque answer: adopted %v, tokens %v, told tokens %v, %v", res.Adopted, res.Tokens != nil, told.Tokens != nil, err)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session was stored (%s)", sessionSummary(sess))
	}
}

// monoes.me ends every refresh token of an account when a rotated one is presented
// again (plan A, spike S2), so the older login is read and updated while the store
// lock is held: no other process can present the token between the read and the
// exchange, or read it between the exchange and its update.
func TestAdoptReadsAndUpdatesTheOlderLoginUnderTheStoreLock(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	held := func(when string) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		if unlock, err := store.Lock(ctx); err == nil {
			unlock()
			t.Errorf("the store lock was free %s", when)
		}
	}
	_, err := c.Adopt(context.Background(), func() (string, *account.User, bool) {
		held("while the older login was read")
		return rt, nil, true
	}, func(account.AdoptResult) { held("while the older login was updated") })
	if err != nil {
		t.Fatal(err)
	}
}

// A20: the exchange spends the older refresh token, and its answer holds the only copy of the one that
// replaces it, so once it is sent it is completed and stored even if the caller gives up while
// monoes.me is answering (Ctrl-C, a closing context). The caller's context is cancelled at the moment
// the fake has the request; the session is stored all the same, and nothing is presented twice.
func TestAdoptStoresTheAnswerWhenTheCallerGivesUpMidCall(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.OnToken(cancel)
	res, err := c.Adopt(ctx, olderLogin(rt), nil)
	if err != nil || !res.Adopted || fake.Refreshes != 1 || fake.Replays != 0 {
		t.Fatalf("adopted %v, refreshes %d, replays %d, %v", res.Adopted, fake.Refreshes, fake.Replays, err)
	}
	if got, _ := store.LoadRefresh(); got == "" || got == rt {
		t.Fatal("the rotated refresh token was not stored: the older one is spent and would be presented again")
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/account/ -run '^(TestLogoutRevokesAndForgetsEvenOffline|TestLogoutForgetsAnUnreadableSession|TestLogoutWithNothingToForgetLeavesNoFiles|TestLogoutNeverRevokesOverPlainHTTP|TestLogoutKeepsTheClockGuardRecord|TestASecondLogoutChangesNothing|TestLoginAfterLogoutReplacesTheRecord|TestLogoutRevokesTheRefreshTokenThatIsOnDiskWhenItHoldsTheLock|TestAdoptStoresASessionFromAnOlderRefreshToken|TestAdoptNeverReadsAnAnswerAsARefusal|TestAdoptReadsAndUpdatesTheOlderLoginUnderTheStoreLock|TestAdoptStoresTheAnswerWhenTheCallerGivesUpMidCall)$' -count=1
```

Expected: FAIL, build errors: `undefined: account.AdoptResult`, `c.Adopt undefined`, `c.Logout undefined`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
FAIL
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/adopt_test.go:23:21: undefined: account.AdoptResult
internal/account/adopt_test.go:24:16: c.Adopt undefined (type *account.Client has no field or method Adopt)
internal/account/adopt_test.go:24:75: undefined: account.AdoptResult
internal/account/adopt_test.go:70:75: too many errors
```

- [ ] **Step 3: Implement logout and adoption.**

Create `internal/account/logout.go` with exactly this content:

```go
package account

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logout revokes the refresh token at monoes.me (best effort) and forgets the login on
// this machine, whatever monoes.me answers: the refresh token is deleted and the session
// is replaced by one that holds no token, only the clock-guard record (clockRecord). It
// never erases that record (spec §4.5, A23): the enforcement date is judged against the
// highest time this machine has seen, and a machine that could drop the mark by logging
// out could run again after setting its clock back before the date.
func (c *Client) Logout(ctx context.Context) error {
	if !c.stored() {
		return nil // nothing to forget: leave no files behind
	}
	unlock, err := c.Store.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// Both reads are made under the lock. A refresh in another process before this point has
	// rotated the refresh token: revoking the one read earlier would revoke a dead token and
	// leave the live one valid at monoes.me.
	sess, lerr := c.Store.Load()
	rt, _ := c.Store.LoadRefresh() // a key store that does not answer means no revocation, not no logout
	if rt != "" {
		c.revoke(ctx, rt)
	}
	err = c.Store.DeleteRefresh()
	if keep := c.clockRecord(sess, lerr); keep != nil {
		if saveErr := c.Store.Save(keep); err == nil {
			err = saveErr
		}
	}
	return err
}

// stored says whether the account folder holds anything to forget. It looks at the two
// file names only, reads no secret and makes no file, so a machine with nothing stored
// is left untouched.
func (c *Client) stored() bool {
	for _, name := range []string{"session.json", "refresh.enc"} {
		if _, err := os.Stat(filepath.Join(c.Store.Dir(), name)); !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

// clockRecord is the session that logout leaves in place of sess: no token, the host and
// the high-water mark, the later of the one on file and now. It is nil when there is no
// session to replace (a refresh token alone has no mark to keep) and when sess already is
// such a record, so a second logout writes nothing. An unreadable session.json (lerr)
// loses its mark, so its record starts from now.
func (c *Client) clockRecord(sess *Session, lerr error) *Session {
	now := c.Now()
	switch {
	case lerr != nil:
		return &Session{V: 1, Host: c.Host, HW: now}
	case sess == nil || (sess.AccessToken == "" && sess.State == ""):
		return nil
	}
	hw := now
	if sess.HW.After(hw) {
		hw = sess.HW // a clock set back must not lower the mark
	}
	return &Session{V: 1, Host: sess.Host, HW: hw}
}

// revoke asks monoes.me to revoke token, within five seconds and never twice
// waiting for a dead network.
func (c *Client) revoke(ctx context.Context, token string) {
	if checkHost(c.Host) != nil {
		return // a refresh token never travels in the clear
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ep, err := DiscoverEndpoints(rctx, c.HTTP, c.Host)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, ep.RevocationEndpoint,
		strings.NewReader(url.Values{"token": {token}, "client_id": {ClientID}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := c.HTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}
```

Create `internal/account/adopt.go` with exactly this content:

```go
package account

import (
	"context"
	"errors"
)

// AdoptResult is the outcome of trying to turn an older login's refresh token into
// the machine session (spec D23).
type AdoptResult struct {
	Adopted bool      // the session was stored
	Status  Status    // its verdict, when Adopted
	Tokens  *TokenSet // what monoes.me issued, when it answered with tokens: the exchange spent the older refresh token
	Dead    bool      // monoes.me answered invalid_grant: the older refresh token is spent, revoked or expired
}

// Adopt tries to turn an older library login into the machine session (spec D23),
// all under the store lock, so that no other process can present the same refresh
// token meanwhile: monoes.me ends every refresh token of an account when a rotated
// one is presented again (plan A, spike S2). With the lock held it checks that no
// session exists, asks older for the login's refresh token and user (ok is false
// when there is none), exchanges the token and stores the result, and calls done
// with the outcome before it releases the lock, so the caller updates the older
// login while nobody else can read it.
//
// Whatever monoes.me answers, the outcome is Adopted or not, never a refusal and
// never a stored "refused" state: a failure here only means the user signs in once
// more. The error is for local failures (the lock, the store, the key store). The
// result tells the caller what became of the older refresh token, because it must
// never be presented again once it is dead: Dead when monoes.me answered
// invalid_grant, Tokens when the exchange spent it and issued new ones that no
// session could be made of (the caller keeps the older login alive with them), and
// neither when monoes.me did not answer, so the token is as it was.
//
// Once the exchange is sent it is completed and stored even if ctx is cancelled (A20);
// until then a cancelled ctx stops the call, with the older refresh token untouched.
func (c *Client) Adopt(ctx context.Context, older func() (refreshToken string, user *User, ok bool), done func(AdoptResult)) (AdoptResult, error) {
	unlock, err := c.Store.Lock(ctx)
	if err != nil {
		return AdoptResult{}, err
	}
	defer unlock()
	if sess, err := c.Store.Load(); err != nil || sess != nil {
		return AdoptResult{}, err // somebody signed in meanwhile
	}
	refreshToken, u, ok := older()
	if !ok {
		return AdoptResult{}, nil
	}
	res, err := c.exchangeOlder(ctx, refreshToken, u)
	if done != nil {
		done(res)
	}
	return res, err
}

func (c *Client) exchangeOlder(ctx context.Context, refreshToken string, u *User) (AdoptResult, error) {
	// The exchange spends the older refresh token, and its answer holds the only copy of the one
	// that replaces it, so once it is sent it is completed whatever the caller does next (A20): it
	// runs on a context the caller's cancellation does not reach, bounded by the guard's own
	// deadline. A caller that gives up (Ctrl-C) waits at most that long. What follows asks the
	// network only for the user's name, which falls back to the older login's own when the
	// caller has given up, and commit takes no context.
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshCallTimeout)
	defer cancel()
	ts, err := NewRefresher(c.Host).Refresh(gctx, refreshToken)
	var refused *RefusedError
	switch {
	case errors.As(err, &refused):
		return AdoptResult{Dead: true}, nil
	case err != nil:
		return AdoptResult{}, nil // monoes.me did not answer: the older login is as it was
	}
	res := AdoptResult{Tokens: ts}
	sess, err := c.session(ctx, ts, u)
	var ue *unusableError
	if errors.As(err, &ue) {
		return res, nil // an opaque token, say: the older login stays as it is, with the new tokens
	}
	if err != nil {
		return res, err
	}
	if err := c.commit(sess, ts.RefreshToken); err != nil {
		return res, err
	}
	res.Adopted, res.Status = true, Evaluate(sess, c.Now())
	return res, nil
}
```

- [ ] **Step 4: Run the tests with the race detector.**

```bash
go test ./internal/account/ -run '^(TestLogoutRevokesAndForgetsEvenOffline|TestLogoutForgetsAnUnreadableSession|TestLogoutWithNothingToForgetLeavesNoFiles|TestLogoutNeverRevokesOverPlainHTTP|TestLogoutKeepsTheClockGuardRecord|TestASecondLogoutChangesNothing|TestLoginAfterLogoutReplacesTheRecord|TestLogoutRevokesTheRefreshTokenThatIsOnDiskWhenItHoldsTheLock|TestAdoptStoresASessionFromAnOlderRefreshToken|TestAdoptNeverReadsAnAnswerAsARefusal|TestAdoptReadsAndUpdatesTheOlderLoginUnderTheStoreLock|TestAdoptStoresTheAnswerWhenTheCallerGivesUpMidCall)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/account`.

- [ ] **Step 5: Vet and format.**

```bash
go vet ./internal/account/ && gofmt -l internal/account
```

Expected: No output.

- [ ] **Step 6: Commit.**

```bash
git add internal/account/logout.go internal/account/adopt.go internal/account/logout_test.go internal/account/adopt_test.go
```

```bash
git commit -m "feat(account): logout that keeps the clock guard, and adoption that always completes and never reads an answer as a refusal" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 7: Host, the default guard, the package-level sign-in functions and the devaccount key

D24. `Host()` is `account.HostURL` in every build, or `MONOES_BASE_URL` in a build with `-tags devaccount`; a test seam points it at the fake. A second seam gives the default store a sealer of the test's own, because `internal/secrets` remembers the account key it first made for the whole process (`getOrCreateKEK`, `internal/secrets/keyring.go:93`) while most CLI tests re-make the mock keyring: the second sign-in of a test binary would seal a refresh token that the keyring cannot open. `NewDefaultGuard` and the package-level `Login`, `SendEmailCode`, `VerifyEmailCode` and `Logout` of the contract live here, and the development signing key (the public half of `accounttest.DevKeyPair`) is added to the devaccount build so a real binary verifies what the fake signs.

**Files:**
- Create: `internal/account/defaultguard.go`
- Create: `internal/account/host_default.go`
- Create: `internal/account/host_devaccount.go`
- Modify: `internal/account/keys_devaccount.go` (lines 1-8, B1a's stub: `extraKeys() []Key { return nil }`)
- Test: `internal/account/defaultguard_test.go`
- Test: `internal/account/host_default_test.go`
- Test: `internal/account/keys_devaccount_test.go`

**Interfaces:**
- Consumes: `Client`, `NewClient`, `NewRefresher` (Tasks 4 and 5); B1a: `DefaultDir`, `OpenStore`, `NewKeyringSealer`, `NewInteractiveKeyringSealer` (B1a's contract change request 3), `NewGuard`, `GuardOptions`, `Guard`, and the key plumbing `pinKey(kid, publicHex string) Key` and `extraKeys() []Key`, with `accounttest.DevKeyPair`, `accounttest.DevKID` (request 8).
- Produces:

```go
func Host() string                                  // HostURL, or MONOES_BASE_URL under -tags devaccount
func SetHostForTest(t testing.TB, host string)      // panics outside a test binary
func SetSealerForTest(t testing.TB, s Sealer)       // the default store seals with s instead of the key store; panics outside a test binary
func DefaultStore() (Store, error)                  // ~/.monoagent/account, the non-interactive sealer; creates nothing
func NewDefaultGuard() (*Guard, error)              // the contract's, over DefaultStore and NewRefresher(Host())
func Login(ctx context.Context, o LoginOptions) (Status, error)
func SendEmailCode(ctx context.Context, email string) error
func VerifyEmailCode(ctx context.Context, email, code string) (Status, error)
func Logout(ctx context.Context) error
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/account/defaultguard_test.go` with exactly this content:

```go
package account_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// Index §2: opening the default store and building the default guard create
// nothing, so `doctor` on a fresh home stays write-free.
func TestNewDefaultGuardCreatesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	g, err := account.NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if st := g.Status(); st.Reason != account.ReasonNotLoggedIn {
		t.Fatalf("status %+v", st)
	}
	if _, err := account.DefaultStore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".monoagent")); !os.IsNotExist(err) {
		t.Fatalf("a fresh home was written to: %v", err)
	}
}

func TestSetHostForTestRestoresTheHost(t *testing.T) {
	before := account.Host()
	t.Run("hook", func(t *testing.T) {
		account.SetHostForTest(t, "http://127.0.0.1:3100")
		if got := account.Host(); got != "http://127.0.0.1:3100" {
			t.Fatalf("Host = %q", got)
		}
	})
	if got := account.Host(); got != before {
		t.Fatalf("Host = %q after the test, want %q", got, before)
	}
}

// A test binary re-makes the mock keyring between tests while internal/secrets
// remembers the account key it first made, so the default store would seal a second
// sign-in's refresh token with a key the keyring no longer holds: a test gives the
// store a sealer of its own, and gets the key store back afterwards.
func TestSetSealerForTestSealsTheDefaultStoreWithTheTestsSealer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mine := account.NewMemorySealer()
	dir := filepath.Join(home, ".monoagent", "account")
	t.Run("hook", func(t *testing.T) {
		account.SetSealerForTest(t, mine)
		st, err := account.DefaultStore()
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveRefresh("a-refresh-token"); err != nil {
			t.Fatal(err)
		}
		got, err := account.OpenStore(dir, mine).LoadRefresh()
		if err != nil || got != "a-refresh-token" {
			t.Fatalf("the default store did not seal with the test's sealer: err %v, token matches %v", err, got == "a-refresh-token")
		}
	})
	st, err := account.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.LoadRefresh(); err == nil && got != "" {
		t.Fatal("the test's sealer outlived the test: the default store opened a token sealed under it")
	}
}
```

Create `internal/account/host_default_test.go` with exactly this content:

```go
//go:build !devaccount

package account_test

import (
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// Spec D24: no environment variable redirects the session of a release build.
func TestDefaultBuildIgnoresMonoesBaseURL(t *testing.T) {
	t.Setenv("MONOES_BASE_URL", "http://127.0.0.1:3100")
	if got := account.Host(); got != account.HostURL {
		t.Fatalf("Host = %q: MONOES_BASE_URL redirected a default build", got)
	}
}
```

Create `internal/account/keys_devaccount_test.go` with exactly this content:

```go
//go:build devaccount

package account_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
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

// TrustedKeys hands out a copy, so what a caller does to the keys it was given, the slice and the
// bytes of every public key in it, never changes what the next call trusts. Only a devaccount build
// has an extra key to merge into the pinned set, which is why this is pinned here and not in the
// default build's tests.
func TestDevaccountTrustedKeysReturnsACopy(t *testing.T) {
	pub, _ := accounttest.DevKeyPair()
	holdsDevKey := func(keys []account.Key) bool {
		for _, k := range keys {
			if k.KID == accounttest.DevKID && k.Public.Equal(pub) {
				return true
			}
		}
		return false
	}
	keys := account.TrustedKeys()
	if !holdsDevKey(keys) {
		t.Fatal("a devaccount build does not trust the development key")
	}
	for i := range keys {
		keys[i].KID = "scribbled"
		for j := range keys[i].Public {
			keys[i].Public[j] ^= 0xff
		}
	}
	if !holdsDevKey(account.TrustedKeys()) {
		t.Fatal("changing the keys TrustedKeys returned changed the keys it trusts next")
	}
}

// With no test hook at all, a devaccount binary verifies what libraryfake signs.
func TestDevaccountVerifiesTheFakesTokens(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	ts, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := account.Verify(ts.AccessToken, time.Now()); err != nil || rec.Sub != "u-ada" {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDevaccountHostHonorsMonoesBaseURL(t *testing.T) {
	t.Setenv("MONOES_BASE_URL", " http://127.0.0.1:3100/ ")
	if got := account.Host(); got != "http://127.0.0.1:3100" {
		t.Fatalf("Host = %q", got)
	}
	t.Setenv("MONOES_BASE_URL", "")
	if got := account.Host(); got != account.HostURL {
		t.Fatalf("Host = %q", got)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/account/ -run '^(TestNewDefaultGuardCreatesNothing|TestSetHostForTestRestoresTheHost|TestSetSealerForTestSealsTheDefaultStoreWithTheTestsSealer|TestDefaultBuildIgnoresMonoesBaseURL)$' -count=1
```

Expected: FAIL, build errors: `undefined: account.NewDefaultGuard`, `undefined: account.SetHostForTest`, `undefined: account.SetSealerForTest`, `undefined: account.Host`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/account [build failed]
FAIL
# github.com/monoes/mono-agent/internal/account_test [github.com/monoes/mono-agent/internal/account.test]
internal/account/defaultguard_test.go:17:20: undefined: account.NewDefaultGuard
internal/account/defaultguard_test.go:25:23: undefined: account.DefaultStore
internal/account/defaultguard_test.go:34:20: undefined: account.Host
internal/account/host_default_test.go:14:20: too many errors
```

- [ ] **Step 3: Implement the host seam, the default guard and the dev key.**

Create `internal/account/defaultguard.go` with exactly this content:

```go
package account

import (
	"context"
	"sync"
	"testing"
)

var (
	hostMu       sync.RWMutex
	hostOverride string

	sealerMu       sync.RWMutex
	sealerOverride Sealer
)

// Host is the monoes.me host the machine session belongs to: HostURL, or in a
// devaccount build MONOES_BASE_URL (host_default.go, host_devaccount.go).
func Host() string {
	hostMu.RLock()
	defer hostMu.RUnlock()
	if hostOverride != "" {
		return hostOverride
	}
	return hostFromEnv()
}

// SetHostForTest points Host at a fake monoes.me for the test and restores it on
// cleanup. It panics outside a test binary, like the other test seams, and the
// test must not call t.Parallel().
func SetHostForTest(t testing.TB, host string) {
	if !testing.Testing() {
		panic("account: SetHostForTest outside a test binary")
	}
	hostMu.Lock()
	prev := hostOverride
	hostOverride = host
	hostMu.Unlock()
	t.Cleanup(func() {
		hostMu.Lock()
		hostOverride = prev
		hostMu.Unlock()
	})
}

// SetSealerForTest makes the default store, and so the default guard and the
// package-level calls below, seal the refresh token with s instead of the key
// store, and puts the key store back on cleanup. A test binary re-makes the mock
// keyring in most tests while internal/secrets remembers the account key it first
// made for the whole process, so without this a second sign-in in one test binary
// seals a refresh token that the keyring can no longer open. It panics outside a
// test binary, and the test must not call t.Parallel().
func SetSealerForTest(t testing.TB, s Sealer) {
	if !testing.Testing() {
		panic("account: SetSealerForTest outside a test binary")
	}
	sealerMu.Lock()
	prev := sealerOverride
	sealerOverride = s
	sealerMu.Unlock()
	t.Cleanup(func() {
		sealerMu.Lock()
		sealerOverride = prev
		sealerMu.Unlock()
	})
}

// sealerFor is the sealer of the default store: a test's, or the key store. Only
// an explicit sign-in (interactive) may let the key store ask for a passphrase.
func sealerFor(interactive bool) Sealer {
	sealerMu.RLock()
	s := sealerOverride
	sealerMu.RUnlock()
	switch {
	case s != nil:
		return s
	case interactive:
		return NewInteractiveKeyringSealer()
	}
	return NewKeyringSealer()
}

// DefaultStore is the session store of this OS user: ~/.monoagent/account, its
// refresh token sealed under the key store, which never prompts. Opening it
// creates nothing.
func DefaultStore() (Store, error) { return defaultStore(sealerFor(false)) }

func defaultStore(s Sealer) (Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return OpenStore(dir, s), nil
}

// defaultClient is the client of the package-level calls below. interactive is for
// a sign-in, which owns the terminal: sealing the refresh token for the first time
// may ask for the file keyring's passphrase, as the vault does. Nothing else ever
// prompts.
func defaultClient(interactive bool) (*Client, error) {
	st, err := defaultStore(sealerFor(interactive))
	if err != nil {
		return nil, err
	}
	return NewClient(Host(), st), nil
}

// NewDefaultGuard is the guard of a production process: the default store and the
// network Refresher for Host. It creates no file and calls nothing.
func NewDefaultGuard() (*Guard, error) {
	st, err := DefaultStore()
	if err != nil {
		return nil, err
	}
	return NewGuard(GuardOptions{Store: st, Refresher: NewRefresher(Host())}), nil
}

// Login signs in through the browser against Host and stores the session.
func Login(ctx context.Context, o LoginOptions) (Status, error) {
	c, err := defaultClient(true)
	if err != nil {
		return Status{}, err
	}
	return c.Login(ctx, o)
}

// SendEmailCode emails a sign-in code through Host.
func SendEmailCode(ctx context.Context, email string) error {
	c, err := defaultClient(false)
	if err != nil {
		return err
	}
	return c.SendEmailCode(ctx, email)
}

// VerifyEmailCode trades an emailed code for the machine session.
func VerifyEmailCode(ctx context.Context, email, code string) (Status, error) {
	c, err := defaultClient(true)
	if err != nil {
		return Status{}, err
	}
	return c.VerifyEmailCode(ctx, email, code)
}

// Logout revokes the refresh token (best effort) and forgets the login, keeping the machine's
// clock-guard record: the session becomes one with no token (Client.Logout, A23).
func Logout(ctx context.Context) error {
	c, err := defaultClient(false)
	if err != nil {
		return err
	}
	return c.Logout(ctx)
}
```

Create `internal/account/host_default.go` with exactly this content:

```go
//go:build !devaccount

package account

// hostFromEnv is the monoes.me host of a default build: always HostURL. No
// environment variable redirects the session (spec D24); MONOES_BASE_URL still
// redirects the library, which then has no session to send to that host.
func hostFromEnv() string { return HostURL }
```

Create `internal/account/host_devaccount.go` with exactly this content:

```go
//go:build devaccount

package account

import (
	"os"
	"strings"
)

// hostFromEnv is the monoes.me host of a devaccount build: MONOES_BASE_URL when
// set (a local server that signs with the development key), else HostURL.
func hostFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MONOES_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return HostURL
}
```

Replace the whole content of `internal/account/keys_devaccount.go` with exactly this content:

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

- [ ] **Step 4: Run the whole package in the default build.**

```bash
go test ./internal/account/ -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/account`: the whole package, B1a's tests included.

- [ ] **Step 5: Run it again with the devaccount tag.**

```bash
go test -tags devaccount ./internal/account/ -count=1 -race
```

Expected: `ok` again, now with the devaccount tests (they are skipped by the default build), `TestDevaccountTrustedKeysReturnsACopy` among them.

- [ ] **Step 6: Vet, format and build the devaccount binary.**

```bash
go vet ./internal/account/ && go vet -tags devaccount ./internal/account/ && gofmt -l internal/account && go build -tags devaccount -o /dev/null ./cmd/monoagentcli
```

Expected: No output.

- [ ] **Step 7: Commit.**

```bash
git add internal/account/defaultguard.go internal/account/host_default.go internal/account/host_devaccount.go internal/account/keys_devaccount.go internal/account/defaultguard_test.go internal/account/host_default_test.go internal/account/keys_devaccount_test.go
```

```bash
git commit -m "feat(account): the default guard, the host seam and the devaccount development key" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

## Part 2: the library and the command line

### Task 8: internal/library reads with the machine session, and keeps an older login working

D21 and D22. The library client reads with the machine session when there is one for its host, through a `SessionSource` over the account guard and store. A profile's own vault login (what a release before the session left behind) stays as the read fallback, so merging this phase, which is dormant, changes nothing for a user who is already logged in to the library (index §1). The library never refreshes the session itself: a 401 is "log in first". Its refresh of the older login takes the account lock and refreshes what the vault holds then, so it never presents a token an adoption in another process has just spent; and, once its grant is sent, it is completed and stored whatever the caller does next, because the answer holds the only copy of the refresh token that replaces the spent one (A20).

**Files:**
- Create: `internal/library/session.go`
- Modify: `internal/library/client.go` (lines 36-46, the `Client` struct)
- Modify: `internal/library/auth.go` (as Task 2 leaves it: lines 16-37 `currentToken` and 228-251 `Logout`, only their heads change; 125-137 `refresh`, replaced)
- Test: `internal/library/session_test.go`

**Interfaces:**
- Consumes: `account.Guard.Refresh`, `Guard.Status`, `account.Store.Load`, `account.StateLocked` (B1a); the fake and `account.Client.Login` (Tasks 3 and 5) in the tests.
- Produces:

```go
type SessionSource interface {
	Token(ctx context.Context, host string) (*Token, error) // nil, nil: no usable session for host
}

type AccountSession struct {
	Guard   *account.Guard
	Store   account.Store
	Offline bool // report the saved session without asking monoes.me to renew it
}

func (s *AccountSession) Lock(ctx context.Context) (unlock func(), err error) // the account store's lock

// library.Client gains the field Session SessionSource, and:
func (c *Client) LogoutLegacy(ctx context.Context) error // revoke and forget the profile's own vault login
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/library/session_test.go` with exactly this content:

```go
package library_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// accountSession is the session source of one process: a new guard over store.
func accountSession(fake *libraryfake.Server, store account.Store) *library.AccountSession {
	guard := account.NewGuard(account.GuardOptions{Store: store, Refresher: account.NewRefresher(fake.URL)})
	return &library.AccountSession{Guard: guard, Store: store}
}

// signedIn signs the fake's ada in as the machine session and returns the pieces
// a library client needs to use it.
func signedIn(t *testing.T, fake *libraryfake.Server) *library.AccountSession {
	t.Helper()
	libraryfake.TrustKey(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	_, err := account.NewClient(fake.URL, store).Login(context.Background(),
		account.LoginOptions{Open: browser(t), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("account sign-in: %v", err)
	}
	return accountSession(fake, store)
}

// backdate makes the stored session's last refresh attempt d older: a guard holds
// a second attempt off for a minute (spec §4.4), so a test that wants a new
// process to renew moves the file's clock.
func backdate(t *testing.T, store account.Store, d time.Duration) {
	t.Helper()
	sess, err := store.Load()
	if err != nil || sess == nil {
		t.Fatalf("no session to backdate: %v", err)
	}
	sess.LastAttempt = sess.LastAttempt.Add(-d)
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
}

// methodOf names how a login was made, for failure messages that must not print it.
func methodOf(t *library.Token) string {
	if t == nil {
		return "(no login)"
	}
	return t.Method
}

func sessionClient(t *testing.T, base string, s library.SessionSource, legacy library.TokenStore) *library.Client {
	t.Helper()
	c := mustClient(t, base, legacy)
	c.Session = s
	return c
}

// Spec D21 and index §3.4 item 10: the library reads with the machine session's
// token, and only at the host that issued it.
func TestSessionTokenIsSentOnlyToItsHost(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	elsewhere := libraryfake.New()
	defer elsewhere.Close()
	sess := signedIn(t, fake)
	ctx := context.Background()

	c := sessionClient(t, fake.URL, sess, nil)
	me, err := c.Me(ctx)
	if err != nil || me.User.Username != "ada" {
		t.Fatalf("Me with the session = %+v, %v", me, err)
	}
	if tok, _ := c.Token(ctx); tok == nil || tok.Method != "session" || tok.RefreshToken != "" {
		t.Fatalf("token: present %v, method %q, refresh token kept %v", tok != nil, methodOf(tok), tok != nil && tok.RefreshToken != "")
	}

	other := sessionClient(t, elsewhere.URL, sess, nil)
	if _, err := other.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) {
		t.Fatalf("Me at another host = %v", err)
	}
	if n := elsewhere.Requests["GET /api/library/me"]; n != 0 {
		t.Fatalf("the session token went to another host (%d requests)", n)
	}
}

// Dormant releases must change nothing a user can see: a login made in a profile's
// vault before the session existed keeps serving reads until a session replaces it.
func TestAnOlderLoginServesReadsUntilASessionExists(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	empty := &library.AccountSession{
		Store: account.OpenStore(t.TempDir(), account.NewMemorySealer()),
		Guard: account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())}),
	}
	legacy := &memStore{}
	login(t, fake, legacy) // the older login, in the profile's own store
	ctx := context.Background()

	c := sessionClient(t, fake.URL, empty, legacy)
	if me, err := c.Me(ctx); err != nil || me.User.Username != "ada" {
		t.Fatalf("Me with only the older login = %+v, %v", me, err)
	}
	if tok, _ := c.Token(ctx); tok.Method != "pkce" {
		t.Fatalf("token method = %q, want the older login's", tok.Method)
	}

	c = sessionClient(t, fake.URL, signedIn(t, fake), legacy)
	if tok, _ := c.Token(ctx); tok == nil || tok.Method != "session" {
		t.Fatalf("with a session, token method %q", methodOf(tok))
	}
}

// sharedStore is a TokenStore two goroutines may use.
type sharedStore struct {
	mu sync.Mutex
	t  *library.Token
}

func (s *sharedStore) Load(context.Context) (*library.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.t, nil
}

func (s *sharedStore) Save(_ context.Context, t *library.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *t
	s.t = &c
	return nil
}

func (s *sharedStore) Delete(context.Context) error {
	return s.Save(context.Background(), &library.Token{})
}

// Plan A, spike S2: monoes.me ends every refresh token of the account when a spent
// one is presented again. The library's refresh of a login of its own waits for the
// account lock, which an adoption in another process holds while it spends the
// token, and then refreshes what the vault holds, not the token it read earlier.
func TestOlderLoginRefreshWaitsForAnAdoptionAndUsesWhatItLeft(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &sharedStore{t: &old}
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	sess := &library.AccountSession{Store: store, Guard: account.NewGuard(account.GuardOptions{Store: store})}
	ctx := context.Background()
	unlock, err := store.Lock(ctx) // an adoption in another process holds the account lock
	if err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, fake.URL, sess, legacy)
	done := make(chan error, 1)
	go func() {
		_, err := c.Me(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the refresh did not wait for the account lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// The adoption spends the refresh token, leaves the new tokens in the vault, and lets go.
	spent, err := account.NewRefresher(fake.URL).Refresh(ctx, old.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	fresh := old
	fresh.AccessToken, fresh.RefreshToken, fresh.ExpiresAt = spent.AccessToken, spent.RefreshToken, time.Time{}
	_ = legacy.Save(ctx, &fresh)
	unlock()
	if err := <-done; err != nil || fake.Replays != 0 {
		t.Fatalf("Me: %v (replays %d): the refresh presented a spent refresh token", err, fake.Replays)
	}
}

// A20: the refresh of the profile's own older login spends its refresh token like any other, so once
// the grant is sent it is completed and stored even if the caller gives up while monoes.me answers
// (Ctrl-C, a closing context): the vault keeps the refresh token that replaced the spent one, and
// nothing is presented twice.
func TestOlderLoginRefreshIsStoredWhenTheCallerGivesUp(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &sharedStore{t: &old}
	c := mustClient(t, fake.URL, legacy)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.OnToken(cancel) // the caller gives up while monoes.me is answering
	tok, err := c.Token(ctx)
	kept, _ := legacy.Load(context.Background())
	if err != nil || tok == nil || kept == nil || kept.RefreshToken == old.RefreshToken || tok.RefreshToken != kept.RefreshToken || fake.Replays != 0 {
		t.Fatalf("token %v (%v), vault entry %v, replaced the spent refresh token %v, replays %d",
			tok != nil, err, kept != nil, kept != nil && kept.RefreshToken != old.RefreshToken, fake.Replays)
	}
}

// The account guard refreshes the session before a call when it is due. The client
// never refreshes it itself: a 401 is "log in first", and a refused session stops a
// new process before it asks monoes.me anything.
func TestSessionIsRefreshedByTheGuardAndNeverByTheClient(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.AccessTTL = 2 * time.Minute // inside the 5 minute refresh margin: a refresh is due
	sess := signedIn(t, fake)
	backdate(t, sess.Store, 2*time.Minute)
	c := sessionClient(t, fake.URL, sess, nil)
	ctx := context.Background()
	if _, err := c.Me(ctx); err != nil || fake.Refreshes != 1 {
		t.Fatalf("Me: %v (refreshes %d, want the guard's one)", err, fake.Refreshes)
	}

	fake.RevokeAll() // monoes.me revoked everything
	if _, err := c.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) || fake.Refreshes != 1 {
		t.Fatalf("a 401 with a session: %v (refreshes %d, want no client refresh)", err, fake.Refreshes)
	}
	backdate(t, sess.Store, 2*time.Minute)
	before := fake.Requests["GET /api/library/me"]
	fresh := sessionClient(t, fake.URL, accountSession(fake, sess.Store), nil) // a new process, a new guard: its refresh is refused
	if _, err := fresh.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) || fake.Requests["GET /api/library/me"] != before {
		t.Fatalf("a refused session: %v (%d library requests went out)", err, fake.Requests["GET /api/library/me"]-before)
	}
}

func TestLibraryLogoutLegacyRevokesAndForgetsTheOlderLogin(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	legacy := &memStore{}
	c := login(t, fake, legacy)
	if err := c.LogoutLegacy(context.Background()); err != nil || legacy.t != nil {
		t.Fatalf("LogoutLegacy: %v (kept %v)", err, legacy.t != nil)
	}
	if _, err := c.Me(context.Background()); !errors.Is(err, library.ErrNotLoggedIn) {
		t.Fatalf("Me after LogoutLegacy = %v", err)
	}
	if err := (&library.Client{}).LogoutLegacy(context.Background()); err != nil {
		t.Fatalf("no store: %v", err)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/library/ -run '^(TestSessionTokenIsSentOnlyToItsHost|TestAnOlderLoginServesReadsUntilASessionExists|TestSessionIsRefreshedByTheGuardAndNeverByTheClient|TestOlderLoginRefreshWaitsForAnAdoptionAndUsesWhatItLeft|TestOlderLoginRefreshIsStoredWhenTheCallerGivesUp|TestLibraryLogoutLegacyRevokesAndForgetsTheOlderLogin)$' -count=1
```

Expected: FAIL, build errors: `undefined: library.AccountSession`, `c.Session undefined`, `c.LogoutLegacy undefined`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/library [build failed]
FAIL
# github.com/monoes/mono-agent/internal/library_test [github.com/monoes/mono-agent/internal/library.test]
internal/library/session_test.go:16:77: undefined: library.AccountSession
internal/library/session_test.go:18:18: undefined: library.AccountSession
internal/library/session_test.go:23:64: undefined: library.AccountSession
internal/library/session_test.go:250:32: (&library.Client{}).LogoutLegacy undefined (type *library.Client has no field or method LogoutLegacy)
```

- [ ] **Step 3: Add the session source.**

Create `internal/library/session.go` with exactly this content:

```go
package library

import (
	"context"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// refreshGrantTimeout bounds the refresh of the profile's own login from the moment its grant is
// sent: the grant runs on a context the caller's cancellation does not reach (A20), so it needs a
// deadline of its own. It is the guard's backstop on one grant.
const refreshGrantTimeout = 20 * time.Second

// SessionSource supplies the machine-wide monoes.me session (spec D21): the one
// login that `account login` and `library login` both make and every library
// call uses.
type SessionSource interface {
	// Token returns the session's access token as a login for host, refreshed when
	// it is due, or nil, nil when there is no usable session for that host.
	Token(ctx context.Context, host string) (*Token, error)
}

// AccountSession is the SessionSource over internal/account: the Guard refreshes
// the session and the Store holds its token.
type AccountSession struct {
	Guard   *account.Guard
	Store   account.Store
	Offline bool // report the saved session without asking monoes.me to renew it
}

// Token implements SessionSource. A session issued for another host than the
// library's (MONOES_BASE_URL pointed elsewhere) is never handed out, so its token
// cannot reach a server that did not issue it. A locked session is not handed out
// either: the caller says "log in first".
func (s *AccountSession) Token(ctx context.Context, host string) (*Token, error) {
	sess, err := s.Store.Load()
	if err != nil || sess == nil || sess.Host != host {
		return nil, err
	}
	st := s.Guard.Status()
	if !s.Offline {
		st, _ = s.Guard.Refresh(ctx) // when due; if it cannot, the call itself finds out
	}
	if st.State == account.StateLocked {
		return nil, nil
	}
	if sess, err = s.Store.Load(); err != nil || sess == nil || sess.AccessToken == "" {
		return nil, err // the refresh may have replaced the token
	}
	t := &Token{AccessToken: sess.AccessToken, TokenType: "Bearer", Method: "session", BaseURL: sess.Host, ExpiresAt: st.ValidUntil}
	if u := sess.User; u != nil {
		t.User = &User{ID: u.ID, Email: u.Email, Username: u.Username}
	}
	return t, nil
}

// Lock takes the account store's exclusive lock. The library holds it while it
// refreshes a login of its own, so that never overlaps an adoption in another
// process (see Client.refresh).
func (s *AccountSession) Lock(ctx context.Context) (unlock func(), err error) {
	return s.Store.Lock(ctx)
}

// sessionToken is the machine session's login for this client's host, asked for
// again once it is within a minute of expiring.
func (c *Client) sessionToken(ctx context.Context) (*Token, error) {
	c.mu.Lock()
	t := c.session
	c.mu.Unlock()
	if t != nil && !t.expiring(c.Now(), time.Minute) {
		return t, nil
	}
	t, err := c.Session.Token(ctx, c.BaseURL)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.session = t
	c.mu.Unlock()
	return t, nil
}
```

- [ ] **Step 4: Let the client use it, and add `LogoutLegacy`.**

In `internal/library/client.go`, replace this block (12 lines, shown abbreviated: it runs from its first line to its last, inclusive):

```go
// Client calls the library API for one profile.
type Client struct {
…
	oauth  *account.OAuthEndpoints
}
```

with:

```go
// Client calls the library API for one profile.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Store   TokenStore // the profile's own login (a release before the machine session); nil: none
	// Session is the machine-wide monoes.me session (spec D21). It is used before
	// Store, and only for its own host; nil: Store alone.
	Session SessionSource
	Now     func() time.Time

	mu      sync.Mutex
	token   *Token
	loaded  bool
	session *Token // the session token last handed out
	oauth   *account.OAuthEndpoints
}
```

In `internal/library/auth.go`, replace this text:

```go
// currentToken returns the stored token, refreshing it first when it is
// about to expire. nil, nil when logged out.
func (c *Client) currentToken(ctx context.Context) (*Token, error) {
```

with:

```go
// currentToken returns the login to send. With a machine session for this host
// (spec D21) that is its token: the account guard refreshes it, never this client.
// Otherwise it is the profile's own stored login, refreshed first when it is about
// to expire; that keeps a login made before the session existed working. nil, nil
// when logged out.
func (c *Client) currentToken(ctx context.Context) (*Token, error) {
	if c.Session != nil {
		if t, err := c.sessionToken(ctx); err != nil || t != nil {
			return t, err
		}
	}
```

In `internal/library/auth.go`, replace the declaration that starts `func (c *Client) refresh(` (with its doc comment, up to its closing brace) by:

```go
// refresh trades the refresh token for a new access token and stores it. With a
// machine session source it does so under the account store lock and on what the
// store holds once it has the lock, so it can never present a refresh token that an
// adoption in another process has just spent: monoes.me ends every refresh token
// of the account when a spent one is presented again. Once the grant is sent it is
// completed and stored whatever the caller does next (A20): the answer holds the only
// copy of the refresh token that replaces this one, so the grant and the save run on a
// context the caller's cancellation does not reach, bounded by refreshGrantTimeout.
func (c *Client) refresh(ctx context.Context, t *Token) (*Token, error) {
	if l, ok := c.Session.(interface {
		Lock(ctx context.Context) (unlock func(), err error)
	}); ok && c.Store != nil {
		unlock, err := l.Lock(ctx)
		if err != nil {
			return nil, err
		}
		defer unlock()
		cur, err := c.Store.Load(ctx)
		if err != nil || cur == nil || cur.RefreshToken == "" {
			return nil, fmt.Errorf("library: the saved login is gone")
		}
		t = cur
	}
	endpoint := c.endpoints(ctx).TokenEndpoint // discovery spends nothing: a caller that gives up may stop it
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshGrantTimeout)
	defer cancel()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}, "client_id": {ClientID}}
	tr, err := c.postToken(gctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	nt := c.tokenFrom(tr, t.Method, t)
	if err := c.setToken(gctx, nt); err != nil {
		return nil, err
	}
	return nt, nil
}
```

In `internal/library/auth.go`, replace the declaration that starts `func (c *Client) Logout(` (with its doc comment, up to its closing brace) by:

```go
// Logout revokes the token on monoes.me (best effort) and forgets it.
func (c *Client) Logout(ctx context.Context) error {
	t, _ := c.currentToken(ctx)
	if t != nil {
		c.revoke(ctx, nonEmpty(t.RefreshToken, t.AccessToken))
	}
	c.mu.Lock()
	c.token, c.loaded = nil, true
	c.mu.Unlock()
	if c.Store == nil {
		return nil
	}
	return c.Store.Delete(ctx)
}

// LogoutLegacy revokes and forgets the profile's own login, the one a release before
// the machine-wide session kept in its vault. It never touches the session.
func (c *Client) LogoutLegacy(ctx context.Context) error {
	if c.Store == nil {
		return nil
	}
	if t, _ := c.Store.Load(ctx); t != nil {
		c.revoke(ctx, nonEmpty(t.RefreshToken, t.AccessToken))
	}
	c.mu.Lock()
	c.token, c.loaded = nil, true
	c.mu.Unlock()
	return c.Store.Delete(ctx)
}

// revoke asks monoes.me to revoke tok, best effort.
func (c *Client) revoke(ctx context.Context, tok string) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.endpoints(rctx).RevocationEndpoint,
		strings.NewReader(url.Values{"token": {tok}, "client_id": {ClientID}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := c.HTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}
```

- [ ] **Step 5: Run the whole library package with the race detector.**

```bash
go test ./internal/library/ -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/library`: the new tests and every older one.

- [ ] **Step 6: Build, vet and format.**

```bash
go build ./... && go vet ./internal/library/... && gofmt -l internal/library
```

Expected: No output.

- [ ] **Step 7: Commit.**

```bash
git add internal/library/session.go internal/library/client.go internal/library/auth.go internal/library/session_test.go
```

```bash
git commit -m "feat(library): read with the machine session, keep an older vault login as the fallback" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 9: library.AdoptIntoAccount

D23, with D22: `library.AdoptIntoAccount` lives here because it reads the library's vault entry. Dormant: nothing happens, nothing is called. It looks for an older login without the lock (a machine with none writes nothing), then adopts under the lock through `Client.Adopt`, and decides the fate of each older refresh token itself, because monoes.me ends every refresh token of the account when a spent one is presented again and an older binary would present whatever the vault still holds: adopted or dead (`invalid_grant`), the vault entry is removed; spent without a session, it is replaced by the tokens monoes.me issued; with no verdict (no answer), it stays and the call stops. B5a (its Task 6) calls it once per database, claiming the try before the call, so one call tries every profile in order; this task only provides and proves it.

**Files:**
- Create: `internal/library/adopt.go`
- Test: `internal/library/adopt_test.go`

**Interfaces:**
- Consumes: `account.Client.Adopt` (Task 6), `account.DefaultStore`, `account.Host` (Task 7), `account.NewClient` (Task 5), `account.EnforceDate`, `account.Guard` (B1a); `library.VaultStore`, `profiledir.List` (existing).
- Produces:

```go
func AdoptIntoAccount(ctx context.Context, db *sql.DB, g *account.Guard) (adopted bool, err error) // the contract's
```

- [ ] **Step 1: Write the failing tests.**

Create `internal/library/adopt_test.go` with exactly this content:

```go
package library_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// adoptFixture is a fake monoes.me, a migrated database whose default profile
// holds an older library login (an opaque token and a refresh token, in its vault),
// an enforced gate, and a home of its own for the session.
type adoptFixture struct {
	fake  *libraryfake.Server
	db    *storage.Database
	vault *library.VaultStore
	guard *account.Guard
}

func newAdoptFixture(t *testing.T) *adoptFixture {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fake := libraryfake.New()
	t.Cleanup(fake.Close)
	libraryfake.TrustKey(t)
	account.SetHostForTest(t, fake.URL)
	account.SetSealerForTest(t, account.NewMemorySealer())
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour))
	db := testdb.Open(t)
	vault := &library.VaultStore{DB: db.DB, ProfileID: "default", BaseURL: fake.URL}
	c := mustClient(t, fake.URL, vault)
	if _, err := c.LoginPKCE(context.Background(), library.LoginOptions{Open: browser(t), Timeout: 10 * time.Second}); err != nil {
		t.Fatalf("the older login: %v", err)
	}
	g, err := account.NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	return &adoptFixture{fake: fake, db: db, vault: vault, guard: g}
}

// addProfile makes a second profile whose vault holds an older login of its own.
func (f *adoptFixture) addProfile(t *testing.T, id string) *library.VaultStore {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if _, err := f.db.DB.Exec(`INSERT INTO profiles (id, name, created_at, root_dir, icon) VALUES (?, ?, ?, ?, ?)`, id, id, now, "", ""); err != nil {
		t.Fatal(err)
	}
	access, refresh := f.fake.NewGrant("ada")
	vs := &library.VaultStore{DB: f.db.DB, ProfileID: id, BaseURL: f.fake.URL}
	tok := &library.Token{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", Method: "pkce", BaseURL: f.fake.URL,
		User: &library.User{ID: "u-ada", Username: "ada", Email: "ada@example.com"}}
	if err := vs.Save(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	return vs
}

func hostOf(s *account.Session) string {
	if s == nil {
		return ""
	}
	return s.Host
}

func (f *adoptFixture) session(t *testing.T) *account.Session {
	t.Helper()
	store, err := account.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestAdoptTurnsAnOlderLoginIntoTheSession(t *testing.T) {
	f := newAdoptFixture(t)
	ctx := context.Background()
	adopted, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard)
	if err != nil || !adopted {
		t.Fatalf("adopted %v, %v", adopted, err)
	}
	if sess := f.session(t); sess == nil || sess.Host != f.fake.URL || sess.User == nil || sess.User.Username != "ada" {
		t.Fatalf("session: present %v, host %q", sess != nil, hostOf(sess))
	}
	if tok, _ := f.vault.Load(ctx); tok != nil {
		t.Fatal("the adopted login is still in the vault")
	}
	if g, err := account.NewDefaultGuard(); err != nil || g.Status().State != account.StateOK {
		t.Fatalf("a new guard does not see the session: %v", err)
	}
	before := f.fake.Refreshes
	if again, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || again || f.fake.Refreshes != before {
		t.Fatalf("a second run: adopted %v, %v, refreshes %d -> %d", again, err, before, f.fake.Refreshes)
	}
	if f.fake.Replays != 0 {
		t.Fatal("the spent refresh token was presented again: monoes.me would end every login of the account")
	}
}

// Spec D22: while dormant nothing is called implicitly.
func TestAdoptIsDormantUntilTheGateIs(t *testing.T) {
	f := newAdoptFixture(t)
	account.SetEnforceFromForTest(t, time.Time{})
	if adopted, err := library.AdoptIntoAccount(context.Background(), f.db.DB, f.guard); err != nil || adopted {
		t.Fatalf("adopted %v, %v", adopted, err)
	}
	if f.fake.Refreshes != 0 {
		t.Fatalf("a dormant adoption called monoes.me (%d refreshes)", f.fake.Refreshes)
	}
	if tok, _ := f.vault.Load(context.Background()); tok == nil {
		t.Fatal("the older login was touched")
	}
}

// Spec D23: whatever monoes.me answers, the outcome is "sign in once more", never a
// refusal. An invalid_grant says the older refresh token is dead; presenting it again
// from an older binary would end every session of the account, the new one included,
// so the vault entry goes, and a second call has nothing left to present.
func TestAdoptNeverReadsAnAnswerAsARefusalAndDropsADeadLogin(t *testing.T) {
	f := newAdoptFixture(t)
	ctx := context.Background()
	f.fake.SetRefreshMode(libraryfake.RefreshInvalidGrant)
	if adopted, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || adopted {
		t.Fatalf("invalid_grant: adopted %v, %v", adopted, err)
	}
	if g, err := account.NewDefaultGuard(); err != nil || g.Status().Reason != account.ReasonNotLoggedIn || f.session(t) != nil {
		t.Fatalf("an answer to the adoption was read as a refusal (%v)", err)
	}
	if tok, _ := f.vault.Load(ctx); tok != nil {
		t.Fatal("the dead login is still in the vault: an older binary would present it again")
	}
	asked := len(f.fake.TokenRequests())
	if again, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || again || len(f.fake.TokenRequests()) != asked {
		t.Fatalf("a second call presented something (adopted %v, %v)", again, err)
	}
	if f.fake.Replays != 0 {
		t.Fatal("a refresh token was presented after it was spent")
	}
}

// monoes.me not answering, or answering with an error that says nothing about the
// token, is no verdict: every older login stays as it was, and the call asks once
// (a connect timeout per profile would be paid at the first command of a machine
// that is offline).
func TestAdoptKeepsTheOlderLoginsAfterATransientFailureAndAsksOnce(t *testing.T) {
	f := newAdoptFixture(t)
	ctx := context.Background()
	second := f.addProfile(t, "second-profile")
	first, _ := f.vault.Load(ctx)
	asked := len(f.fake.TokenRequests())
	f.fake.SetRefreshMode(libraryfake.RefreshServerError)
	if adopted, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || adopted {
		t.Fatalf("a failing monoes.me: adopted %v, %v", adopted, err)
	}
	if kept, _ := f.vault.Load(ctx); kept == nil || kept.RefreshToken != first.RefreshToken {
		t.Fatal("a login monoes.me gave no verdict on was changed or removed")
	}
	if other, _ := second.Load(ctx); other == nil {
		t.Fatal("the second profile's login was removed")
	}
	if n := len(f.fake.TokenRequests()) - asked; n != 1 || f.session(t) != nil {
		t.Fatalf("%d token requests, session present %v: want one request and no session", n, f.session(t) != nil)
	}
}

// An exchange that works but whose token cannot be a session spends the older
// refresh token: the tokens monoes.me issued replace it, and the older login keeps
// working.
func TestAdoptKeepsTheOlderLoginAliveWithWhatTheExchangeIssued(t *testing.T) {
	f := newAdoptFixture(t)
	ctx := context.Background()
	f.fake.SetOpaqueTokens(true) // the exchange works, but its token cannot be a session
	old, _ := f.vault.Load(ctx)
	if adopted, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || adopted {
		t.Fatalf("opaque: adopted %v, %v", adopted, err)
	}
	kept, _ := f.vault.Load(ctx)
	if kept == nil || kept.RefreshToken == old.RefreshToken {
		t.Fatalf("the older login lost the refresh token the exchange rotated (entry present %v)", kept != nil)
	}
	c := mustClient(t, f.fake.URL, f.vault)
	if me, err := c.Me(ctx); err != nil || me.User.Username != "ada" || f.fake.Replays != 0 {
		t.Fatalf("the older login no longer works: %v (replays %d)", err, f.fake.Replays)
	}
}

// A dead login does not stop the call: the next profile's login is a chain of its
// own, and B5a makes one try per database.
func TestAdoptDropsADeadLoginAndTriesTheNextProfile(t *testing.T) {
	f := newAdoptFixture(t)
	ctx := context.Background()
	tok, _ := f.vault.Load(ctx)
	dead := *tok
	dead.RefreshToken = "a-refresh-token-monoes-me-never-issued"
	if err := f.vault.Save(ctx, &dead); err != nil {
		t.Fatal(err)
	}
	second := f.addProfile(t, "second-profile")
	if adopted, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || !adopted {
		t.Fatalf("adopted %v, %v", adopted, err)
	}
	if gone, _ := f.vault.Load(ctx); gone != nil {
		t.Fatal("the dead login is still in the vault")
	}
	if gone, _ := second.Load(ctx); gone != nil {
		t.Fatal("the adopted login is still in the vault")
	}
	if sess := f.session(t); sess == nil || sess.Host != f.fake.URL || f.fake.Replays != 0 {
		t.Fatalf("session present %v, replays %d", sess != nil, f.fake.Replays)
	}
}

// A20: the exchange spends the older refresh token, so what becomes of it in the vault is settled even
// when the caller gives up while monoes.me is answering (Ctrl-C at the first command after the update):
// the session is stored, the vault entry is gone, and nothing is presented twice.
func TestAdoptCompletesWhenTheCallerGivesUp(t *testing.T) {
	f := newAdoptFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.fake.OnToken(cancel)
	if adopted, err := library.AdoptIntoAccount(ctx, f.db.DB, f.guard); err != nil || !adopted {
		t.Fatalf("adopted %v, %v", adopted, err)
	}
	if sess := f.session(t); sess == nil || sess.Host != f.fake.URL {
		t.Fatalf("session present %v", sess != nil)
	}
	if tok, _ := f.vault.Load(context.Background()); tok != nil {
		t.Fatal("the adopted login is still in the vault: the spent refresh token would be presented again")
	}
	if f.fake.Replays != 0 {
		t.Fatal("a refresh token was presented after it was spent")
	}
}

// Nothing on disk until a write: a machine with no older login makes no account folder.
func TestAdoptWritesNothingOnAMachineWithoutAnOlderLogin(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	libraryfake.TrustKey(t)
	account.SetHostForTest(t, "http://127.0.0.1:1") // nothing listens there: any call would fail
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour))
	db := testdb.Open(t)
	g, err := account.NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	if adopted, err := library.AdoptIntoAccount(context.Background(), db.DB, g); err != nil || adopted {
		t.Fatalf("adopted %v, %v", adopted, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".monoagent", "account")); !os.IsNotExist(err) {
		t.Fatalf("an adoption with nothing to adopt made the account folder: %v", err)
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./internal/library/ -run '^(TestAdoptTurnsAnOlderLoginIntoTheSession|TestAdoptIsDormantUntilTheGateIs|TestAdoptNeverReadsAnAnswerAsARefusalAndDropsADeadLogin|TestAdoptKeepsTheOlderLoginsAfterATransientFailureAndAsksOnce|TestAdoptKeepsTheOlderLoginAliveWithWhatTheExchangeIssued|TestAdoptDropsADeadLoginAndTriesTheNextProfile|TestAdoptCompletesWhenTheCallerGivesUp|TestAdoptWritesNothingOnAMachineWithoutAnOlderLogin)$' -count=1
```

Expected: FAIL, build error: `undefined: library.AdoptIntoAccount`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/internal/library [build failed]
FAIL
# github.com/monoes/mono-agent/internal/library_test [github.com/monoes/mono-agent/internal/library.test]
internal/library/adopt_test.go:94:26: undefined: library.AdoptIntoAccount
internal/library/adopt_test.go:108:27: undefined: library.AdoptIntoAccount
internal/library/adopt_test.go:120:29: undefined: library.AdoptIntoAccount
internal/library/adopt_test.go:265:29: undefined: library.AdoptIntoAccount
```

- [ ] **Step 3: Implement the adoption.**

Create `internal/library/adopt.go` with exactly this content:

```go
package library

import (
	"context"
	"database/sql"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// vaultWriteTimeout bounds the update of an older login's vault entry once its exchange is done:
// a local database write, made on a context the caller's cancellation does not reach (A20).
const vaultWriteTimeout = 10 * time.Second

// AdoptIntoAccount turns an older monoes.me library login into the machine session
// (spec D23): the first profile, the active one first, whose vault holds a login
// for the account host and whose refresh token monoes.me exchanges for an
// audience-bound token becomes the session, and its vault entry is removed.
//
// It lives here and not in internal/account because it reads the library's vault
// entry. It does nothing while the gate is dormant (nothing is called implicitly)
// or when a session already exists, in any state, and it writes nothing on a
// machine with no older login. Whatever monoes.me answers, the outcome is
// "adopted" or "sign in once more", never a refusal; the error is for local
// failures.
//
// Its caller (B5a) tries once per database, claiming the try before the call, so
// one call tries every profile that has an older login and decides the fate of each
// refresh token itself, because monoes.me ends every refresh token of the account
// when a spent one is presented again, and an older binary would present whatever
// is left in the vault. Adopted: the vault entry is removed. Dead (invalid_grant:
// spent, revoked or expired): removed too, and the next profile is tried, its login
// being a chain of its own. No verdict (monoes.me did not answer, or answered
// something no session can be made of): the login stays, with the tokens monoes.me
// issued when it answered, and the call stops there. The older login is read and
// updated under the account store lock, so no other process presents its token
// meanwhile. Once an exchange is sent it is completed, and what became of the older
// refresh token is written to the vault, even if ctx is cancelled meanwhile (A20): a
// spent refresh token left in the vault would be presented again. A caller that must
// judge at once afterwards builds a new guard.
func AdoptIntoAccount(ctx context.Context, db *sql.DB, g *account.Guard) (adopted bool, err error) {
	if db == nil || g == nil || account.EnforceDate().IsZero() {
		return false, nil
	}
	if st := g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
		return false, nil
	}
	store, err := account.DefaultStore()
	if err != nil {
		return false, err
	}
	host := account.Host()
	client := account.NewClient(host, store)
	profiles, err := profiledir.List(ctx, db)
	if err != nil {
		return false, err
	}
	for _, p := range activeFirst(profiles) {
		vs := &VaultStore{DB: db, ProfileID: p.ID, BaseURL: host}
		if tok, lerr := vs.Load(ctx); lerr != nil || tok == nil || tok.RefreshToken == "" {
			continue // a look without the lock: a machine with no older login writes nothing
		}
		var cur *Token
		res, aerr := client.Adopt(ctx, func() (string, *account.User, bool) {
			tok, lerr := vs.Load(ctx) // again, under the lock: another process may have spent or replaced it
			if lerr != nil || tok == nil || tok.RefreshToken == "" {
				return "", nil, false
			}
			cur = tok
			var u *account.User
			if tok.User != nil {
				u = &account.User{ID: tok.User.ID, Email: tok.User.Email, Username: tok.User.Username}
			}
			return tok.RefreshToken, u, true
		}, func(res account.AdoptResult) {
			// The exchange has spent the older refresh token, so what becomes of it is settled even if
			// the caller gave up meanwhile (A20): a context of its own, bounded.
			vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), vaultWriteTimeout)
			defer cancel()
			switch {
			case res.Adopted, res.Dead:
				_ = vs.Delete(vctx) // adopted, or dead: it must never be presented again
			case res.Tokens != nil && cur != nil:
				keepAlive(vctx, vs, cur, res.Tokens)
			}
		})
		switch {
		case aerr != nil:
			return false, aerr
		case res.Adopted:
			_, _ = g.Refresh(ctx) // best effort: g re-reads the session
			return true, nil
		case cur != nil && !res.Dead:
			return false, nil // no verdict on this login: it stays as it was, and the others wait
		}
	}
	return false, nil
}

// activeFirst puts the active profile in front, the rest in their order.
func activeFirst(ps []profiledir.Profile) []profiledir.Profile {
	out := make([]profiledir.Profile, 0, len(ps))
	for _, p := range ps {
		if p.Default {
			out = append(out, p)
		}
	}
	for _, p := range ps {
		if !p.Default {
			out = append(out, p)
		}
	}
	return out
}

// keepAlive writes the tokens an unsuccessful adoption was answered with back into
// the older login, whose refresh token the exchange spent. The expiry is left open:
// the next call finds out.
func keepAlive(ctx context.Context, vs *VaultStore, tok *Token, ts *account.TokenSet) {
	nt := *tok
	nt.AccessToken, nt.RefreshToken, nt.ExpiresAt = ts.AccessToken, ts.RefreshToken, time.Time{}
	_ = vs.Save(ctx, &nt)
}
```

- [ ] **Step 4: Run the tests.**

```bash
go test ./internal/library/ -run '^(TestAdoptTurnsAnOlderLoginIntoTheSession|TestAdoptIsDormantUntilTheGateIs|TestAdoptNeverReadsAnAnswerAsARefusalAndDropsADeadLogin|TestAdoptKeepsTheOlderLoginsAfterATransientFailureAndAsksOnce|TestAdoptKeepsTheOlderLoginAliveWithWhatTheExchangeIssued|TestAdoptDropsADeadLoginAndTriesTheNextProfile|TestAdoptCompletesWhenTheCallerGivesUp|TestAdoptWritesNothingOnAMachineWithoutAnOlderLogin)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/internal/library` (the first test builds the migrated template database, a few seconds).

- [ ] **Step 5: Vet and format.**

```bash
go vet ./internal/library/ && gofmt -l internal/library
```

Expected: No output.

- [ ] **Step 6: Commit.**

```bash
git add internal/library/adopt.go internal/library/adopt_test.go
```

```bash
git commit -m "feat(library): adopt an older library login into the machine session" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 10: login_required.go: the login-required error moves out of library.go and carries the account state

Index §3.4 item 2. `loginRequiredError` moves out of `library.go` into `login_required.go` and carries the account state for `--json`, with the constructor B2's gate uses; `isLoginRequired` also recognizes a bare `*account.LoginRequiredError`. Giving that bare error exit 4 in `exitCodeFor` is B2's (its Task 4), so it is not done here.

**Files:**
- Create: `cmd/monoagentcli/login_required.go`
- Modify: `cmd/monoagentcli/library.go` (lines 124-139 the type and `isLoginRequired`, lines 153-154 in `libErr`)
- Test: `cmd/monoagentcli/login_required_test.go`

**Interfaces:**
- Consumes: `account.Status`, `account.LoginRequiredError`, `account.IsLoginRequired`, `account.CurrentStatus` (B1a); `cliError`, `jsonErrorFields`, `reportCommandError` (existing).
- Produces:

```go
type loginRequiredError struct { *cliError; status account.Status }

func newLoginRequiredError(st account.Status) error // exit 4; message of account.LoginRequiredError
func libraryLoginRequired() error                   // exit 4; the library's own text; account.CurrentStatus() in --json
func isLoginRequired(err error) bool
```
`--json` adds `"login_required": true`, `"code": "auth_or_connection"` and `"account": {"state": …, "reason": …}`: the document of index §3.4 item 2, the same keys as `(*account.LoginRequiredError).JSONErrorFields`.

- [ ] **Step 1: Write the failing tests.**

Create `cmd/monoagentcli/login_required_test.go` with exactly this content:

```go
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
)

func TestLoginRequiredErrorCarriesTheAccountState(t *testing.T) {
	err := newLoginRequiredError(account.Status{V: 1, State: account.StateLocked, Reason: account.ReasonRefused})
	if exitCodeFor(err) != 4 || !isLoginRequired(err) ||
		!strings.HasPrefix(err.Error(), "Log in to monoes.me first: monoagentcli account login") {
		t.Fatalf("err = %v (exit %d)", err, exitCodeFor(err))
	}
	// The document `main` prints for --json: the error, its class and the account state.
	var out bytes.Buffer
	reportCommandError([]string{"--json", "status"}, err, &out, &bytes.Buffer{})
	var doc struct {
		Error         string
		Code          string
		LoginRequired bool `json:"login_required"`
		Account       struct{ State, Reason string }
	}
	if json.Unmarshal(out.Bytes(), &doc) != nil || doc.Code != "auth_or_connection" || !doc.LoginRequired ||
		doc.Account.State != "locked" || doc.Account.Reason != "refused" || !strings.HasPrefix(doc.Error, "Log in to monoes.me first") {
		t.Fatalf("document = %s", out.String())
	}
}

// Every way the library and the account commands say "log in first" is exit 4 and
// isLoginRequired. A bare *account.LoginRequiredError, which is what a gate or a
// runner returns, is recognized too; giving it an exit code is B2's.
func TestEveryLoginRequiredShapeExitsFour(t *testing.T) {
	for name, err := range map[string]error{
		"the library's own":      libraryLoginRequired(),
		"library.ErrNotLoggedIn": libErr(library.ErrNotLoggedIn),
		"an account refusal":     newLoginRequiredError(account.Status{V: 1, State: account.StateLocked, Reason: account.ReasonNotLoggedIn}),
	} {
		if exitCodeFor(err) != 4 || !isLoginRequired(err) {
			t.Errorf("%s: exit %d, isLoginRequired %v", name, exitCodeFor(err), isLoginRequired(err))
		}
	}
	bare := &account.LoginRequiredError{Status: account.Status{State: account.StateLocked}}
	if !isLoginRequired(bare) || !isLoginRequired(errors.Join(errors.New("context"), bare)) {
		t.Error("a bare account.LoginRequiredError is not recognized")
	}
	if err := libraryLoginRequired(); err.Error() != "Log in to monoes.me first: monoagentcli library login" {
		t.Errorf("the library's message changed: %q", err)
	}
	if isLoginRequired(errors.New("boom")) {
		t.Error("an unrelated error counts as login required")
	}
}
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestLoginRequiredErrorCarriesTheAccountState|TestEveryLoginRequiredShapeExitsFour)$' -count=1
```

Expected: FAIL, build errors: `undefined: newLoginRequiredError`, `undefined: libraryLoginRequired`.

The output includes lines like (timings differ):

```
FAIL	github.com/monoes/mono-agent/cmd/monoagentcli [build failed]
FAIL
# github.com/monoes/mono-agent/cmd/monoagentcli [github.com/monoes/mono-agent/cmd/monoagentcli.test]
cmd/monoagentcli/login_required_test.go:15:9: undefined: newLoginRequiredError
cmd/monoagentcli/login_required_test.go:40:29: undefined: libraryLoginRequired
cmd/monoagentcli/login_required_test.go:42:29: undefined: newLoginRequiredError
cmd/monoagentcli/login_required_test.go:52:12: undefined: libraryLoginRequired
```

- [ ] **Step 3: Add `login_required.go`.**

Create `cmd/monoagentcli/login_required.go` with exactly this content:

```go
package main

import (
	"errors"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
)

// loginRequiredError is exit 4 with "login_required": true and the account's state
// in --json, so a caller can tell "log in" from a connection failure.
type loginRequiredError struct {
	*cliError
	status account.Status
}

func (e loginRequiredError) Unwrap() error { return e.cliError }

// JSONErrorFields adds login_required, the code and the account's state and reason
// to the error document, the same keys as (*account.LoginRequiredError).JSONErrorFields:
// an error that main prints, which no command wrapper has classified, carries its code
// here.
func (e loginRequiredError) JSONErrorFields() map[string]any {
	return map[string]any{"login_required": true, "code": "auth_or_connection",
		"account": map[string]any{"state": e.status.State, "reason": e.status.Reason}}
}

// newLoginRequiredError is the error of a gate that refused st: exit 4, and the
// message of account.LoginRequiredError, whose first line is
// "Log in to monoes.me first: monoagentcli account login".
func newLoginRequiredError(st account.Status) error {
	return loginRequiredError{&cliError{code: 4, msg: (&account.LoginRequiredError{Status: st}).Error()}, st}
}

// libraryLoginRequired is the library's own "log in first". Its text names
// `library login`, which still works: it is an alias of `account login`.
func libraryLoginRequired() error {
	return loginRequiredError{&cliError{code: 4, msg: library.ErrNotLoggedIn.Error()}, account.CurrentStatus()}
}

// isLoginRequired reports whether err means "log in to monoes.me first".
func isLoginRequired(err error) bool {
	var lr loginRequiredError
	return errors.Is(err, library.ErrNotLoggedIn) || errors.As(err, &lr) || account.IsLoginRequired(err)
}
```

- [ ] **Step 4: Move the old definitions out of `library.go`.**

In `cmd/monoagentcli/library.go`, delete this block (15 lines, shown abbreviated: it runs from its first line to its last, inclusive and the blank line after it):

```go
// loginRequiredError is exit 4 with "login_required": true in --json, so
// the app can tell "log in" from a connection failure.
…
	return errors.Is(err, library.ErrNotLoggedIn) || errors.As(err, &lr)
}
```

In `cmd/monoagentcli/library.go`, replace this text:

```go
		return loginRequiredError{&cliError{code: 4, msg: library.ErrNotLoggedIn.Error()}}
```

with:

```go
		return libraryLoginRequired()
```

- [ ] **Step 5: Run the tests.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestLoginRequiredErrorCarriesTheAccountState|TestEveryLoginRequiredShapeExitsFour|TestLibraryReadsNeedALogin|TestLibraryErrorsExitCodes)$' -count=1
```

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli`: the new tests, and the library's exit-code tests that read the moved type.

- [ ] **Step 6: Vet and format.**

```bash
go vet ./cmd/monoagentcli/ && gofmt -l cmd
```

Expected: No output.

- [ ] **Step 7: Commit.**

```bash
git add cmd/monoagentcli/login_required.go cmd/monoagentcli/library.go cmd/monoagentcli/login_required_test.go
```

```bash
git commit -m "feat(cli): the login-required error carries the account state" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 11: The `account` command group: login, logout, status

Spec §7 and D21. `account login [--email | --send | --code]`, `account logout`, `account status [--offline] [--json]` (the exact `account.Status` document; exit 0 for `ok` and `grace`, 4 for `locked`). The sign-in flags and flow are one function that `library login` reuses in the next task. None of them opens the database. The existing library CLI tests start running with the fake as the account host, with its signing key and with a sealer of their own.

**Files:**
- Create: `cmd/monoagentcli/account.go`: the group, `status`, `logout`, `describeStatus`
- Create: `cmd/monoagentcli/account_login.go`: `signInFlags`, `signIn`, the email flow
- Modify: `cmd/monoagentcli/root.go` (line 110, in the `AddCommand` list)
- Modify: `cmd/monoagentcli/library_test.go` (lines 3-16 imports, 25-44 `newLibFixture`)
- Test: `cmd/monoagentcli/account_test.go`

**Interfaces:**
- Consumes: `account.Login`, `SendEmailCode`, `VerifyEmailCode`, `Logout`, `NewDefaultGuard`, `Host`, `LoginOptions` (Task 7); `printLib`, `withJSONErrors`, `writeJSONTo`, `reportedError`, `openLoginURL`, `readLine`, `nonEmptyStr` (existing, in `library.go` and `automation.go`).
- Produces:

```go
func newAccountCmd(cfg *globalConfig) *cobra.Command // login, logout, status

type signInFlags struct{ email, code string; send, noBrowser bool; timeout time.Duration }
func (f *signInFlags) add(cmd *cobra.Command)
// signIn runs the sign-in the flags ask for; sent is true when it only emailed the code.
func signIn(cmd *cobra.Command, cfg *globalConfig, f *signInFlags, name string) (st account.Status, sent bool, err error)
func describeStatus(st account.Status) string
```

- [ ] **Step 1: Point the library test fixture at the fake as the account host.**

In `cmd/monoagentcli/library_test.go`, replace this text:

```go
	t.Setenv("MONOES_BASE_URL", fake.URL)
	// The browser: open the authorize URL and follow its redirect back to
```

with:

```go
	t.Setenv("MONOES_BASE_URL", fake.URL)
	// The machine session is signed at the fake: its host, its signing key, and a
	// sealer of its own (the mock keyring above is re-made by most tests while the
	// account key is remembered for the whole process).
	account.SetHostForTest(t, fake.URL)
	account.SetSealerForTest(t, account.NewMemorySealer())
	libraryfake.TrustKey(t)
	// The browser: open the authorize URL and follow its redirect back to
```

In `cmd/monoagentcli/library_test.go`, replace this text:

```go
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)
```

with:

```go
	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)
```

- [ ] **Step 2: Write the failing tests.**

Create `cmd/monoagentcli/account_test.go` with exactly this content:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// status runs `account status` and returns the document it printed with the exit code.
func (f *libFixture) accountStatus(args ...string) (account.Status, int) {
	f.t.Helper()
	var st account.Status
	out, err := f.run(append([]string{"account", "status"}, args...)...)
	if e := json.Unmarshal([]byte(lastJSONObject([]byte(out))), &st); e != nil {
		f.t.Fatalf("account status: not JSON: %v\n%s", e, out)
	}
	return st, exitCodeFor(err)
}

// backdate makes the stored session's last refresh attempt d older, as if the
// previous command had run d ago. A guard holds a second attempt off for a minute
// (spec §4.4), so a test that wants the next command to renew moves the file's
// clock, not its own.
func (f *libFixture) backdate(d time.Duration) {
	f.t.Helper()
	store := account.OpenStore(filepath.Join(f.home, ".monoagent", "account"), account.NewMemorySealer())
	sess, err := store.Load()
	if err != nil || sess == nil {
		f.t.Fatalf("no session to backdate: %v", err)
	}
	sess.LastAttempt = sess.LastAttempt.Add(-d)
	if err := store.Save(sess); err != nil {
		f.t.Fatal(err)
	}
}

func TestAccountLoginStatusLogout(t *testing.T) {
	f := newLibFixture(t)
	st, code := f.accountStatus()
	if code != 4 || st.V != 1 || st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
		t.Fatalf("logged out: exit %d, %+v", code, st)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".monoagent", "account")); !os.IsNotExist(err) {
		t.Fatalf("a status call on a logged-out machine made the account folder: %v", err)
	}

	// The sign-in URL goes to stderr as one JSON line, for the desktop, with the audience in it.
	_, errOut, err := runCLI(t, f.home, "--json", "account", "login")
	if err != nil || !strings.Contains(errOut, `"kind":"url"`) || !strings.Contains(errOut, "resource=") {
		t.Fatalf("login: %v\nstderr: %s", err, errOut)
	}
	st, code = f.accountStatus()
	if code != 0 || st.State != account.StateOK || st.User == nil || st.User.Username != "ada" || st.Plan != "free" {
		t.Fatalf("after login: exit %d, %+v", code, st)
	}
	if off, code := f.accountStatus("--offline"); code != 0 || off.State != account.StateOK {
		t.Fatalf("offline: exit %d, %+v", code, off)
	}
	var out map[string]any
	f.must(&out, "account", "logout")
	if out["logged_out"] != true {
		t.Fatalf("logout = %v", out)
	}
	if st, code = f.accountStatus(); code != 4 || st.Reason != account.ReasonNotLoggedIn {
		t.Fatalf("after logout: exit %d, %+v", code, st)
	}
	// What a person reads after logging out is the plain "not logged in": the record that logout
	// keeps (a session with no token, A23) is not a login and says nothing of its own. Logging out
	// again is not an error.
	if _, _, err := runCLI(t, f.home, "account", "status"); exitCodeFor(err) != 4 || !strings.Contains(err.Error(), "Not logged in to monoes.me. Run: monoagentcli account login") {
		t.Fatalf("account status after logout: %v", err)
	}
	f.must(nil, "account", "logout")
}

// Spec D5 and D27: a blocked account is locked at its next renewal and signs in
// again once unblocked, while a monoes.me that merely fails leaves it as it was.
func TestAccountStatusFollowsMonoesMe(t *testing.T) {
	f := newLibFixture(t)
	f.fake.AccessTTL = 2 * time.Minute // inside the renewal margin: a renewal is due
	f.must(nil, "account", "login")
	f.backdate(2 * time.Minute)
	if st, code := f.accountStatus(); code != 0 || st.State != account.StateOK || f.fake.Refreshes != 1 {
		t.Fatalf("a due session: exit %d, %+v (renewals %d)", code, st, f.fake.Refreshes)
	}

	f.fake.SetRefreshMode(libraryfake.RefreshServerError)
	f.backdate(2 * time.Minute)
	if st, code := f.accountStatus(); code != 0 || st.State != account.StateOK {
		t.Fatalf("monoes.me failing: exit %d, %+v", code, st)
	}
	f.fake.SetRefreshMode(libraryfake.RefreshOK)

	f.fake.Block("u-ada")
	f.backdate(2 * time.Minute)
	if st, code := f.accountStatus(); code != 4 || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("blocked: exit %d, %+v", code, st)
	}
	if _, err := f.run("account", "login"); exitCodeFor(err) != 4 {
		t.Fatalf("a blocked account signed in: %v", err)
	}
	f.fake.Unblock("u-ada")
	var st account.Status
	f.must(&st, "account", "login")
	if st.State != account.StateOK {
		t.Fatalf("after the unblock: %+v", st)
	}
}
```

- [ ] **Step 3: Run them and watch them fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestAccountLoginStatusLogout|TestAccountStatusFollowsMonoesMe)$' -count=1
```

Expected: FAIL: `account status: not JSON` (the command does not exist yet, so nothing but the cobra error comes back).

The output includes lines like (timings differ):

```
--- FAIL: TestAccountLoginStatusLogout (0.00s)
    account_test.go:45: account status: not JSON: unexpected end of JSON input
--- FAIL: TestAccountStatusFollowsMonoesMe (0.00s)
    account_test.go:80: [account login]: unknown command "account" for "monoagentcli" (available: action, agent, api, application, automation, capture, chat, coder, completion, config, co...
FAIL
FAIL	github.com/monoes/mono-agent/cmd/monoagentcli	0.000s
```

- [ ] **Step 4: Add the command group.**

Create `cmd/monoagentcli/account.go` with exactly this content:

```go
package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
)

// newAccountCmd is `account`: the monoes.me sign-in of this machine. One session
// per OS user is shared by every profile; `library login`, `logout` and `status`
// are aliases of the commands here.
func newAccountCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Sign in to monoes.me: the one account MonoAgent uses on this machine",
		Long: "MonoAgent signs in to monoes.me once per machine. The session is signed by monoes.me, " +
			"checked here without a network call, renewed in the background, and shared by every profile.\n\n" +
			"  monoagentcli account login                     # log in (opens the browser)\n" +
			"  monoagentcli account login --email you@x.com   # a code by email, for a machine without a browser\n" +
			"  monoagentcli account status [--offline]        # who is logged in, and until when\n" +
			"  monoagentcli account logout                    # revoke and forget the session\n\n" +
			"`status` exits 0 while the session is good (including offline grace) and 4 when it is not.",
	}
	for _, c := range []*cobra.Command{newAccountLoginCmd(cfg), newAccountLogoutCmd(cfg), newAccountStatusCmd(cfg)} {
		withJSONErrors(cfg, c)
		cmd.AddCommand(c)
	}
	return cmd
}

func newAccountLoginCmd(cfg *globalConfig) *cobra.Command {
	var f signInFlags
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to monoes.me (opens the browser; --email for a code by email)",
		Long: "Logs this machine in to monoes.me.\n\n" +
			"By default this opens the browser at monoes.me's sign-in page and waits (up to --timeout) for it " +
			"to redirect back to a one-time listener on 127.0.0.1 (OAuth 2.1 with PKCE). --no-browser only " +
			"prints the URL to open. New accounts are made on that page.\n\n" +
			"On a machine without a browser, use a code sent by email:\n" +
			"  monoagentcli account login --email you@example.com            # sends a code, then asks for it\n" +
			"  monoagentcli account login --email you@example.com --send     # only send the code\n" +
			"  monoagentcli account login --email you@example.com --code 123456\n\n" +
			"The session is kept in ~/.monoagent/account, its refresh token sealed by the system key store.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, sent, err := signIn(cmd, cfg, &f, "account login")
			if err != nil || sent {
				return err
			}
			return printAccountStatus(cfg, cmd, st)
		},
	}
	f.add(cmd)
	return cmd
}

func newAccountLogoutCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of monoes.me on this machine (revokes and forgets the session)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := account.Logout(cmd.Context()); err != nil {
				return err
			}
			return printLib(cfg, cmd, map[string]any{"logged_out": true, "base_url": account.Host()}, func(w io.Writer) {
				fmt.Fprintln(w, "Logged out of monoes.me.")
			})
		},
	}
}

func newAccountStatusCmd(cfg *globalConfig) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the monoes.me session: who is logged in, and until when",
		Long: "Shows the session's state: ok, grace (monoes.me is unreachable, the login works offline until the time " +
			"shown) or locked, with the reason. It asks monoes.me to renew the session first when that is due, unless " +
			"--offline. With --json it prints the full status document. Exit 0 for ok and grace, 4 for locked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := account.NewDefaultGuard()
			if err != nil {
				return err
			}
			defer g.Close()
			st := g.Status()
			if !offline && st.Reason != account.ReasonNotLoggedIn { // no session: nothing to renew
				if renewed, _ := g.Refresh(cmd.Context()); renewed.V != 0 {
					st = renewed
				}
			}
			if st.State != account.StateLocked {
				return printAccountStatus(cfg, cmd, st)
			}
			if cfg.JSONOutput {
				if err := writeJSONTo(cmd.OutOrStdout(), st); err != nil {
					return err
				}
			}
			return reportedError{errAuthConnection("%s", describeStatus(st))}
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "Report the saved session without asking monoes.me to renew it")
	return cmd
}

func printAccountStatus(cfg *globalConfig, cmd *cobra.Command, st account.Status) error {
	return printLib(cfg, cmd, st, func(w io.Writer) { fmt.Fprintln(w, describeStatus(st)) })
}

// describeStatus is the sentence for a status: who is logged in, or what to do.
func describeStatus(st account.Status) string {
	as := ""
	if u := st.User; u != nil && (u.Username != "" || u.Email != "") {
		as = " as " + nonEmptyStr(u.Username, u.Email)
		if u.Username != "" && u.Email != "" {
			as += " (" + u.Email + ")"
		}
	}
	switch st.State {
	case account.StateOK:
		return "Logged in to monoes.me" + as + "."
	case account.StateGrace:
		return fmt.Sprintf("Logged in to monoes.me%s, but monoes.me could not be reached (%s). This login works offline until %s.",
			as, st.Reason, st.GraceUntil.Local().Format(time.RFC3339))
	}
	switch st.Reason {
	case account.ReasonRefused:
		return "monoes.me refused this login (the account is disabled, or the login was revoked). Log in again: monoagentcli account login"
	case account.ReasonExpired:
		return "This login has not reached monoes.me for over 24 hours. Connect to the internet, or log in again: monoagentcli account login"
	case account.ReasonClockRollback:
		return "The system clock is earlier than a time this login has already seen. Correct the date and time, then run: monoagentcli account login"
	case account.ReasonClockSkew:
		return "The system clock is more than 5 minutes behind monoes.me's. Correct the date and time, then run: monoagentcli account login"
	case account.ReasonKeyUnknown:
		return "This login was signed with a key this version does not know. Run: monoagentcli update"
	case account.ReasonInvalid:
		return "The saved login cannot be verified. Log in again: monoagentcli account login"
	}
	return "Not logged in to monoes.me. Run: monoagentcli account login"
}
```

Create `cmd/monoagentcli/account_login.go` with exactly this content:

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
)

// signInFlags are the flags of `account login` and of its alias `library login`.
type signInFlags struct {
	email, code     string
	send, noBrowser bool
	timeout         time.Duration
}

func (f *signInFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.email, "email", "", "Log in with a code sent to this address instead of the browser")
	cmd.Flags().BoolVar(&f.send, "send", false, "With --email: only send the code (then run again with --code)")
	cmd.Flags().StringVar(&f.code, "code", "", "With --email: the code from the email")
	cmd.Flags().BoolVar(&f.noBrowser, "no-browser", false, "Print the sign-in URL instead of opening the browser")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "How long to wait for the browser sign-in")
}

// signIn runs the sign-in the flags ask for and returns the session's status. sent
// is true when it only emailed the code. name is the command, for the hints.
func signIn(cmd *cobra.Command, cfg *globalConfig, f *signInFlags, name string) (st account.Status, sent bool, err error) {
	if f.email == "" && (f.send || f.code != "") {
		return st, false, errInvalidInput("--send and --code need --email")
	}
	if f.email != "" {
		return emailSignIn(cmd, cfg, f, name)
	}
	stderr := cmd.ErrOrStderr()
	opts := account.LoginOptions{Timeout: f.timeout, OnURL: func(u string) {
		if cfg.JSONOutput {
			b, _ := json.Marshal(map[string]string{"kind": "url", "url": u})
			fmt.Fprintln(stderr, string(b))
			return
		}
		if f.noBrowser {
			fmt.Fprintf(stderr, "Open this URL to log in to monoes.me:\n\n  %s\n\nWaiting for the sign-in to finish…\n", u)
		} else {
			fmt.Fprintf(stderr, "Opening your browser to log in to monoes.me…\nIf it does not open, visit:\n\n  %s\n\n", u)
		}
	}}
	if !f.noBrowser {
		opts.Open = openLoginURL
	}
	st, err = account.Login(cmd.Context(), opts)
	return st, false, signInErr(err)
}

func emailSignIn(cmd *cobra.Command, cfg *globalConfig, f *signInFlags, name string) (st account.Status, sent bool, err error) {
	ctx := cmd.Context()
	code := f.code
	if code == "" {
		if err := account.SendEmailCode(ctx, f.email); err != nil {
			return st, false, signInErr(err)
		}
		if f.send || cfg.JSONOutput {
			err := printLib(cfg, cmd, map[string]any{"code_sent": true, "email": f.email}, func(w io.Writer) {
				fmt.Fprintf(w, "If %s has a monoes.me account, a code is on its way. Then run:\n  monoagentcli %s --email %s --code <code>\n", f.email, name, f.email)
			})
			return st, true, err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "If %s has a monoes.me account, a code is on its way.\nCode: ", f.email)
		line, err := readLine(cmd.InOrStdin())
		if err != nil {
			return st, false, fmt.Errorf("read the code: %w", err)
		}
		code = line
	}
	if code = strings.TrimSpace(code); code == "" {
		return st, false, errInvalidInput("no code given")
	}
	st, err = account.VerifyEmailCode(ctx, f.email, code)
	if errors.Is(err, account.ErrBadCode) {
		return st, false, errAuthConnection("the code is wrong or expired; ask for a new one with `monoagentcli %s --email %s --send`", name, f.email)
	}
	return st, false, signInErr(err)
}

// signInErr gives a sign-in failure the CLI's exit code: 4 for anything between
// the user and monoes.me (no answer, a refusal, a wrong code), and the error as it
// is for a local failure such as a missing key store.
func signInErr(err error) error {
	var ce *cliError
	if err == nil || errors.As(err, &ce) || errors.Is(err, account.ErrKeyringUnavailable) {
		return err
	}
	return errAuthConnection("%v", err)
}
```

- [ ] **Step 5: Register it.**

In `cmd/monoagentcli/root.go`, replace this text:

```go
		newLibraryCmd(cfg),
	)
```

with:

```go
		newLibraryCmd(cfg),
		newAccountCmd(cfg),
	)
```

- [ ] **Step 6: Run the account tests and every library CLI test.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestAccountLoginStatusLogout|TestAccountStatusFollowsMonoesMe|TestLibrary.*)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli`: the account tests, and every existing library test (they now run with the fake as the account host).

- [ ] **Step 7: Build, vet and format.**

```bash
go build ./... && go vet ./cmd/monoagentcli/ && gofmt -l cmd
```

Expected: No output.

- [ ] **Step 8: Commit.**

```bash
git add cmd/monoagentcli/account.go cmd/monoagentcli/account_login.go cmd/monoagentcli/root.go cmd/monoagentcli/library_test.go cmd/monoagentcli/account_test.go
```

```bash
git commit -m "feat(cli): account login, logout and status" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 12: `library login`, `logout` and `status` become aliases of the account session

D21. `library login` is `account login` (same flags, same JSON progress line on stderr, the same session), `library logout` ends the session and also forgets this profile's older login, `library status` reports the session (or, failing that, the older login) in the same `libStatus` document the desktop reads. A `MONOES_BASE_URL` other than the host the build signs in to makes `library login` fail up front: the session is never sent anywhere else.

**Files:**
- Modify: `cmd/monoagentcli/library.go` (as Task 10 leaves it: lines 3-20 imports, 37 the `Long` text, 54-59 `libEnv`, 61-66 `Close`, 68-84 `open`, 205-219 `printStatus`, 228-269 the status command, 271-332 the login command, 334-365 `emailLogin` removed, 387-405 the logout command)
- Modify: `cmd/monoagentcli/library_test.go` (as Task 11 leaves it: lines 3-17 imports, 142-175 `TestLibraryLoginStatusLogout`, 177-193 `TestLibraryEmailLogin`, 319-358 `TestLibraryRead401RefreshesThenAsksForLogin`)
- Test: `cmd/monoagentcli/library_alias_test.go`
- Test: `cmd/monoagentcli/library_test.go`

**Interfaces:**
- Consumes: `signIn`, `signInFlags` (Task 11); `library.AccountSession`, `Client.Session`, `Client.LogoutLegacy` (Task 8); `account.DefaultStore`, `NewDefaultGuard`, `Host`, `Logout`.
- Produces:

No new names: `library login`, `logout` and `status` keep their flags and their `libStatus` JSON (`logged_in`, `user`, `scopes`, `expires_at`, `method` which is now `session`, `base_url`, `profile`, `error`).

- [ ] **Step 1: Rewrite the three library tests that assert the old behavior.**

In `cmd/monoagentcli/library_test.go`, replace this text:

```go
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
```

with:

```go
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
```

In `cmd/monoagentcli/library_test.go`, replace the declaration that starts `func TestLibraryLoginStatusLogout(` (with its doc comment, up to its closing brace) by:

```go
func TestLibraryLoginStatusLogout(t *testing.T) {
	f := newLibFixture(t)
	var st libStatus
	f.must(&st, "library", "status")
	if st.LoggedIn || st.BaseURL != f.fake.URL {
		t.Fatalf("before login: %+v", st)
	}
	f.login()
	f.must(&st, "library", "status")
	if !st.LoggedIn || st.User.Email != "ada@example.com" || st.Method != "session" {
		t.Fatalf("status = %+v", st)
	}
	// The login is the machine-wide session, never a vault entry (spec D21)...
	var secrets []struct{ Name, URL string }
	f.must(&secrets, "secret", "list")
	for _, s := range secrets {
		if s.Name == "monoes-library" {
			t.Fatalf("a vault entry was written: %+v", secrets)
		}
	}
	// ...so another profile has it too, and `account status` says the same.
	f.must(nil, "profile", "create", "other")
	f.must(&st, "--profile", "other", "library", "status")
	if !st.LoggedIn {
		t.Fatal("the machine session did not reach another profile")
	}
	var acc account.Status
	f.must(&acc, "account", "status", "--offline")
	if acc.State != account.StateOK || acc.User == nil || acc.User.Username != "ada" {
		t.Fatalf("account status = %+v", acc)
	}
	f.must(nil, "library", "logout")
	f.must(&st, "library", "status")
	if st.LoggedIn {
		t.Fatalf("after logout: %+v", st)
	}
	if out, err := f.run("account", "status"); exitCodeFor(err) != 4 || !strings.Contains(out, "not_logged_in") {
		t.Fatalf("account status after a library logout: %v %s", err, out)
	}
}
```

In `cmd/monoagentcli/library_test.go`, replace the declaration that starts `func TestLibraryEmailLogin(` (with its doc comment, up to its closing brace) by:

```go
func TestLibraryEmailLogin(t *testing.T) {
	f := newLibFixture(t)
	var sent map[string]any
	f.must(&sent, "library", "login", "--email", "ada@example.com", "--send")
	if sent["code_sent"] != true {
		t.Fatalf("send = %v", sent)
	}
	out, err := f.run("library", "login", "--email", "ada@example.com", "--code", "999999")
	if err == nil || exitCodeFor(err) != 4 || !strings.Contains(out, "auth_or_connection") {
		t.Fatalf("wrong code: %v %s", err, out)
	}
	var st libStatus
	f.must(&st, "library", "login", "--email", "ada@example.com", "--code", "123456")
	if !st.LoggedIn || st.Method != "session" {
		t.Fatalf("status = %+v", st)
	}
	// Today's claim endpoint answers an opaque token and no refresh token: no session
	// can be made of it, and the user is told to use the browser.
	f.must(nil, "library", "logout")
	f.fake.SetEmailOpaque(true)
	out, err = f.run("library", "login", "--email", "ada@example.com", "--code", "123456")
	if exitCodeFor(err) != 4 || !strings.Contains(out, "cannot start a machine session") {
		t.Fatalf("an email sign-in that cannot make a session: %v %s", err, out)
	}
}
```

In `cmd/monoagentcli/library_test.go`, replace the declaration that starts `func TestLibraryRead401RefreshesThenAsksForLogin(` (with its doc comment, up to its closing brace) by:

```go
// A session close to its expiry is renewed before the call, by the account guard. A
// 401 from monoes.me (its side expired or revoked the token) is "log in first", exit
// 4: the CLI renews nothing in answer to it, and a session whose renewal is refused
// stops before any library request.
func TestLibraryReadRenewsTheSessionThenAsksForLogin(t *testing.T) {
	noSeed(t)
	f := newLibFixture(t)
	f.fake.Add("monoes", "automation", "hackernews", "Hacker News", "official", "1.1.0", pack(t, "hackernews", ""),
		map[string]any{"automation_id": "hackernews"})
	f.fake.AccessTTL = 2 * time.Minute // inside the 5 minute renewal margin: a renewal is due
	f.login()
	f.backdate(2 * time.Minute)

	var list libList
	f.must(&list, "library", "list", "--scope", "official")
	if list.Total != 1 || f.fake.Refreshes != 1 {
		t.Fatalf("list on a due session = %+v (refreshes %d)", list, f.fake.Refreshes)
	}
	var res libInstallResult
	f.must(&res, "library", "install", "automation", "hackernews")
	if !res.Installed {
		t.Fatalf("install = %+v", res)
	}

	f.fake.AccessTTL = time.Hour
	f.login()
	renewals := f.fake.Refreshes
	f.fake.ExpireAccessTokens() // monoes.me says it expired; locally the token is still good
	before := f.fake.Requests["GET /api/library/items"]
	f.loginRequired("library", "list", "--scope", "official")
	if got := f.fake.Requests["GET /api/library/items"] - before; got != 2 || f.fake.Refreshes != renewals { // --json run + text run: one request each, no retry
		t.Fatalf("list requests = %d, renewals %d -> %d", got, renewals, f.fake.Refreshes)
	}

	f.fake.AccessTTL = 2 * time.Minute
	f.login()
	f.backdate(2 * time.Minute)
	f.fake.RevokeAll() // monoes.me revoked everything: the next renewal is invalid_grant
	before = f.fake.Requests["GET /api/library/items"]
	f.loginRequired("library", "list", "--scope", "official")
	f.loginRequired("library", "show", "automation/hackernews")
	f.loginRequired("library", "update", "--dry-run")
	if f.fake.Requests["GET /api/library/items"] != before {
		t.Fatal("a refused session reached the library")
	}

	// Logging in again fixes it.
	f.fake.AccessTTL = time.Hour
	f.login()
	f.must(&list, "library", "list", "--scope", "official")
	if list.Total != 1 || list.Items[0].Installed == nil {
		t.Fatalf("list after a new login = %+v", list)
	}
}
```

- [ ] **Step 2: Write the new tests.**

Create `cmd/monoagentcli/library_alias_test.go` with exactly this content:

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
	"github.com/monoes/mono-agent/internal/storage"
)

// Only the host the build signs in to receives the session; MONOES_BASE_URL elsewhere
// refuses a library login up front. (The short timeout only bounds the run before the
// aliases exist, when this command waits for a browser that never comes.)
func TestLibraryLoginNeedsTheAccountHost(t *testing.T) {
	f := newLibFixture(t)
	t.Setenv("MONOES_BASE_URL", "http://127.0.0.1:9")
	out, err := f.run("library", "login", "--no-browser", "--timeout", "2s")
	if exitCodeFor(err) != 3 || !strings.Contains(out, "-tags devaccount") {
		t.Fatalf("library login at another host: %v %s", err, out)
	}
}

// A login a release before the machine session left in a profile's vault keeps
// serving that profile's reads until a session replaces it, and `library logout`
// forgets it (spec D21, D22: a dormant release changes nothing a user sees).
func TestAnOlderLibraryLoginKeepsWorkingAndLogoutForgetsIt(t *testing.T) {
	noSeed(t)
	f := newLibFixture(t)
	f.fake.Add("monoes", "automation", "hackernews", "Hacker News", "official", "1.1.0", pack(t, "hackernews", ""),
		map[string]any{"automation_id": "hackernews"})
	f.must(nil, "profile", "list") // makes the database
	db, err := initDB(&globalConfig{DBPath: filepath.Join(f.home, ".monoagent", "monoagent.db")})
	if err != nil {
		t.Fatal(err)
	}
	seedOlderLogin(t, db, f.fake)
	db.Close()

	var st libStatus
	f.must(&st, "library", "status")
	if !st.LoggedIn || st.Method != "pkce" {
		t.Fatalf("the older login is not seen: %+v", st)
	}
	var list libList
	f.must(&list, "library", "list", "--scope", "official")
	if list.Total != 1 {
		t.Fatalf("the older login does not serve reads: %+v", list)
	}
	f.must(nil, "library", "logout")
	f.must(&st, "library", "status")
	if st.LoggedIn || f.fake.Requests["POST /api/auth/oauth2/revoke"] == 0 {
		t.Fatalf("after logout: %+v (revocations %d)", st, f.fake.Requests["POST /api/auth/oauth2/revoke"])
	}
}

// seedOlderLogin stores in the default profile's vault the login a release before
// the machine session would have: an opaque token and a refresh token.
func seedOlderLogin(t *testing.T, db *storage.Database, fake *libraryfake.Server) {
	t.Helper()
	access, refresh := fake.NewGrant("ada")
	vs := &library.VaultStore{DB: db.DB, ProfileID: "default", BaseURL: fake.URL}
	tok := &library.Token{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", Method: "pkce", BaseURL: fake.URL,
		User: &library.User{ID: "u-ada", Username: "ada", Email: "ada@example.com"}}
	if err := vs.Save(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 3: Run them and watch the right ones fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestLibrary.*|TestAnOlderLibraryLoginKeepsWorkingAndLogoutForgetsIt)$' -count=1
```

Expected: FAIL: `TestLibraryLoginStatusLogout`, `TestLibraryEmailLogin`, `TestLibraryReadRenewsTheSessionThenAsksForLogin` and `TestLibraryLoginNeedsTheAccountHost`. `TestAnOlderLibraryLoginKeepsWorkingAndLogoutForgetsIt` already passes (it is the regression guard for the next step).

The output includes lines like (timings differ):

```
--- FAIL: TestLibraryLoginNeedsTheAccountHost (0.00s)
    library_alias_test.go:21: library login at another host: library login: no answer from the browser within 2s {"code":"auth_or_connection","error":"library login: no answer from the br...
--- FAIL: TestLibraryLoginStatusLogout (0.00s)
    library_test.go:153: status = {BaseURL:http://127.0.0.1:PORT Profile:default LoggedIn:true User:0xc000000000 Scopes:[openid profile email offline_access library:read library:write] Ex...
--- FAIL: TestLibraryEmailLogin (0.00s)
    library_test.go:198: status = {BaseURL:http://127.0.0.1:PORT Profile:default LoggedIn:true User:0xc000000000 Scopes:[openid profile email offline_access library:read library:write] Ex...
FAIL	github.com/monoes/mono-agent/cmd/monoagentcli	0.000s
```

- [ ] **Step 4: Turn the library commands into aliases.**

In `cmd/monoagentcli/library.go`, replace this block (18 lines, shown abbreviated: it runs from its first line to its last, inclusive):

```go
import (
	"context"
…
	"github.com/monoes/mono-agent/internal/storage"
)
```

with:

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/storage"
)
```

In `cmd/monoagentcli/library.go`, replace this text:

```go
			"The host is https://monoes.me unless MONOES_BASE_URL says otherwise. The login is kept per " +
			"profile in its encrypted vault.",
```

with:

```go
			"The host is https://monoes.me unless MONOES_BASE_URL says otherwise. The login is the machine-wide " +
			"monoes.me session (`monoagentcli account status`), shared by every profile.",
```

In `cmd/monoagentcli/library.go`, replace this text:

```go
type libEnv struct {
	cfg    *globalConfig
	db     *storage.Database
	client *library.Client
	reg    *automation.Registry
}
```

with:

```go
type libEnv struct {
	cfg     *globalConfig
	db      *storage.Database
	client  *library.Client
	reg     *automation.Registry
	session *library.AccountSession // the machine session every library call reads with
	guard   *account.Guard
}
```

In `cmd/monoagentcli/library.go`, replace the declaration that starts `func (e *libEnv) Close(` (with its doc comment, up to its closing brace) by:

```go
func (e *libEnv) Close() {
	if e.db != nil {
		e.db.Close()
		e.db = nil
	}
	if e.guard != nil {
		e.guard.Close()
		e.guard = nil
	}
}
```

In `cmd/monoagentcli/library.go`, replace the declaration that starts `func (e *libEnv) open(` (with its doc comment, up to its closing brace) by:

```go
func (e *libEnv) open() (*library.Client, error) {
	if e.client != nil {
		return e.client, nil
	}
	db, err := initDB(e.cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	e.db = db
	base := library.BaseURL()
	c, err := library.NewClient(base, &library.VaultStore{DB: db.DB, ProfileID: e.cfg.ProfileID, BaseURL: base})
	if err != nil {
		return nil, errInvalidInput("%v", err)
	}
	// The machine session serves every library call. The profile's own vault login is
	// only what a release before the session left behind (spec D21, D22).
	if store, err := account.DefaultStore(); err == nil {
		if g, err := account.NewDefaultGuard(); err == nil {
			e.guard = g
			e.session = &library.AccountSession{Guard: g, Store: store}
			c.Session = e.session
		}
	}
	e.client = c
	return c, nil
}
```

In `cmd/monoagentcli/library.go`, replace this text:

```go
func printStatus(cfg *globalConfig, cmd *cobra.Command, s libStatus) error {
```

with:

```go
// statusFromAccount is the library's view of the machine session st: what
// `library login` prints.
func statusFromAccount(e *libEnv, st account.Status) libStatus {
	s := libStatus{BaseURL: e.client.BaseURL, Profile: e.cfg.ProfileID, Scopes: []string{}, LoggedIn: true, Method: "session"}
	if u := st.User; u != nil {
		s.User = &library.User{ID: u.ID, Email: u.Email, Username: u.Username}
	}
	if !st.ValidUntil.IsZero() {
		s.ExpiresAt = st.ValidUntil.UTC().Format(time.RFC3339)
	}
	return s
}

func printStatus(cfg *globalConfig, cmd *cobra.Command, s libStatus) error {
```

In `cmd/monoagentcli/library.go`, replace this text:

```go
			ctx := cmd.Context()
			t, err := c.Token(ctx)
```

with:

```go
			ctx := cmd.Context()
			if e.session != nil {
				e.session.Offline = offline // --offline never asks monoes.me to renew the session
			}
			t, err := c.Token(ctx)
```

In `cmd/monoagentcli/library.go`, replace the declaration that starts `func newLibraryLoginCmd(` (with its doc comment, up to its closing brace) by:

```go
func newLibraryLoginCmd(e *libEnv) *cobra.Command {
	var f signInFlags
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to monoes.me (an alias of `account login`; --email for a code by email)",
		Long: "An alias of `monoagentcli account login`: one monoes.me login per machine serves the whole CLI, " +
			"the library included, whatever the profile.\n\n" +
			"By default this opens the browser at monoes.me's sign-in page and waits (up to --timeout) for it " +
			"to redirect back to a one-time listener on 127.0.0.1 (OAuth 2.1 with PKCE). --no-browser only " +
			"prints the URL to open.\n\n" +
			"On a machine without a browser, use a code sent by email:\n" +
			"  monoagentcli library login --email you@example.com            # sends a code, then asks for it\n" +
			"  monoagentcli library login --email you@example.com --send     # only send the code\n" +
			"  monoagentcli library login --email you@example.com --code 123456",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if base := library.BaseURL(); base != account.Host() {
				return errInvalidInput("MONOES_BASE_URL is %s but this build logs in to %s; logging in to another monoes.me needs a build made with -tags devaccount", base, account.Host())
			}
			if _, err := e.open(); err != nil {
				return err
			}
			st, sent, err := signIn(cmd, e.cfg, &f, "library login")
			if err != nil || sent {
				return err
			}
			return printStatus(e.cfg, cmd, statusFromAccount(e, st))
		},
	}
	f.add(cmd)
	return cmd
}
```

In `cmd/monoagentcli/library.go`, delete the declaration that starts `func emailLogin(` together with its doc comment.

In `cmd/monoagentcli/library.go`, replace the declaration that starts `func newLibraryLogoutCmd(` (with its doc comment, up to its closing brace) by:

```go
func newLibraryLogoutCmd(e *libEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of monoes.me (an alias of `account logout`; also forgets this profile's older login)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.open()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if c.BaseURL == account.Host() { // MONOES_BASE_URL elsewhere: the machine session is not that host's
				if err := account.Logout(ctx); err != nil {
					return err
				}
			}
			if err := c.LogoutLegacy(ctx); err != nil {
				return err
			}
			return printLib(e.cfg, cmd, map[string]any{"logged_out": true, "base_url": c.BaseURL}, func(w io.Writer) {
				fmt.Fprintf(w, "Logged out of %s.\n", c.BaseURL)
			})
		},
	}
}
```

- [ ] **Step 5: Run the library and account CLI tests with the race detector.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestLibrary.*|TestAccount(LoginStatusLogout|StatusFollowsMonoesMe)|TestAnOlderLibraryLoginKeepsWorkingAndLogoutForgetsIt|TestLoginRequiredErrorCarriesTheAccountState|TestEveryLoginRequiredShapeExitsFour)$' -count=1 -race
```

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli`.

- [ ] **Step 6: Build every variant, vet and format.**

```bash
go build ./... && go build -tags nosocial -o /dev/null ./cmd/monoagentcli && go build -tags devaccount -o /dev/null ./cmd/monoagentcli && go vet ./cmd/... ./internal/account/... ./internal/library/... && gofmt -l .
```

Expected: No output.

- [ ] **Step 7: Run the other packages, including the devaccount build.**

```bash
go test ./internal/account/ ./internal/library/... -count=1 -race && go test -tags devaccount ./internal/account/ ./internal/library/... -count=1
```

Expected: Every package `ok`.

- [ ] **Step 8: Run the whole CLI package.**

```bash
! (go test ./cmd/monoagentcli/ -count=1 2>&1 | grep '^--- FAIL' | grep -v -E 'TestCaptureTaskFilesOnTheBoard|TestCoderRootIsOneSharedFolder|TestWorkflowCancelSignalsAndMarks|TestCoderConversationFolders|TestAgentTestGoDeadline')
```

Expected: No output and exit status 0: the whole CLI package takes about three minutes, and nothing fails that is not on index §4's list: the four tests that already fail on pristine master (on macOS) and the load flake `TestAgentTestGoDeadline`.

- [ ] **Step 9: Commit.**

```bash
git add cmd/monoagentcli/library.go cmd/monoagentcli/library_test.go cmd/monoagentcli/library_alias_test.go
```

```bash
git commit -m "feat(cli): library login, logout and status are aliases of the account session" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

## Proving notes

The code of every task was built and run in a scratch export of the repository at master `f4441a2a` (`git archive`), never in the worktree, on top of B1a's real code: the 42 files its plan adds to `internal/account` and `internal/secrets`, exactly as that plan prints them (they are byte for byte the files of B1a's own scratch export), with B1a's tests. B1a's plan was available, so no stand-in for its contract was used.

The twelve tasks were then replayed in order on that tree exactly as written here (every code block, command and edit of this document is generated from the files that were run): every failing run failed as stated, every implementation made it pass, every commit step's paths existed. Results: the new tests pass with `-race` in `internal/account`, `internal/library` and `cmd/monoagentcli`, and `internal/account` and `internal/library` also pass with `-tags devaccount`; B1a's own tests, its import-rule test and its default-build development-key test included, still pass with this plan's files in the package; `go build ./...`, `go build -tags nosocial -o /dev/null`, `go build -tags devaccount -o /dev/null`, `go vet` of the touched packages and `gofmt -l .` are clean, and the two packages also build and vet for `windows/amd64` and `linux/amd64`. The whole `cmd/monoagentcli` package fails only the four tests that already fail on pristine master (index §4: `TestCaptureTaskFilesOnTheBoard`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCoderConversationFolders`) and the load flake `TestAgentTestGoDeadline`.

Thirteen deliberate breakages of the finished tree each made the named test fail, and were undone: any OAuth error read as a refusal, the session handed to any host, the sign-in without the audience, the emailed code without the audience, an emailed refresh token that is not traded, an older login refreshed without the account lock, adoption reading the older login before it takes the lock, adoption leaving a dead login in the vault, adoption going on after a login it cannot judge, a mismatched development key, logout leaving an unreadable session behind, the login-required document losing its `code`, and the default store ignoring a test's sealer.

The amendments of spec A20 to A23 (what the second security review of B1a found for this plan) were replayed afterwards in a scratch export on B1a's merged branches as they stood then (`feat/account-core`, `feat/account-hardening`, `feat/account-t9` and `feat/account-t10`; the security fix branch was not in them): Tasks 1 to 12 as amended, with `gofmt`, `go vet` (also with `-tags devaccount`), `go build ./cmd/monoagentcli`, `go test -race` of `internal/account`, `internal/library`, `internal/library/libraryfake` and the account and library tests of `cmd/monoagentcli`, and the account and library packages again with `-tags devaccount`, all clean. Each new test failed against the code it replaces. The old `Logout` (the refresh token read before the lock, `session.json` removed) fails `TestLogoutRevokesAndForgetsEvenOffline`, `TestLogoutForgetsAnUnreadableSession`, `TestLogoutKeepsTheClockGuardRecord`, `TestASecondLogoutChangesNothing` and `TestLogoutRevokesTheRefreshTokenThatIsOnDiskWhenItHoldsTheLock`, and reading the refresh token before the lock fails the last alone; an exchange, a library refresh or a vault update on the caller's own context fails `TestAdoptStoresTheAnswerWhenTheCallerGivesUpMidCall` (the fake has rotated the token and the adoption reports "not adopted"), `TestOlderLoginRefreshIsStoredWhenTheCallerGivesUp` and `TestAdoptCompletesWhenTheCallerGivesUp` respectively; a typed-nil error on the HTTP-error path fails `TestRefresherNeverReturnsATypedNilError`; a `TrustedKeys` that passes no extra keys, or shares them, fails `TestDevaccountTrustedKeysReturnsACopy`. `TestLoginAfterLogoutReplacesTheRecord` and the lines added to `TestLogoutNeverRevokesOverPlainHTTP` and `TestAccountLoginStatusLogout` guard the new design and also pass against the old code.

Replaying against B1a's real code found one trap the plan now avoids: `internal/secrets` keeps the account key it first made for the whole process, while the CLI tests re-make the mock keyring in most tests, so the second sign-in of a test binary sealed a refresh token that the next renewal could not open (the decision on the sealer above, `account.SetSealerForTest`, Task 7).

No test prints a token: a failure shows hosts, states, lengths and booleans. Not proven here: the operating system's own key store, a real browser, a real monoes.me, and Windows beyond a cross-compile and vet of the two packages.


## Contract change requests

None changes a name or a signature of index §3. Six notes for the lead, so the plans meet:

1. **Relies on B1a's own contract change requests**, which it lists as additions: `account.NewInteractiveKeyringSealer() Sealer` (its request 3; Task 7 uses it in `Login` and `VerifyEmailCode` only), `account.NewSession(host, accessToken string, user *User, now time.Time) (*Session, error)` (request 4; Task 5), `accounttest.DevKID` (request 8; Tasks 3 and 7) and the `extraKeys()` and `pinKey(kid, publicHex string) Key` slot of its key files (Task 7). If one of them is dropped from B1a, this plan needs the matching one-line change.
2. **Implements the `TokenRequests` request in `b5b-docs.md`** (its Contract change requests section says B1b's plan adds it): Task 3 adds `type TokenRequest struct{ Form url.Values; Header http.Header }` and `func (s *Server) TokenRequests() []TokenRequest` exactly as proposed, and Tasks 4 and 5 pin the field set of a refresh and of a code exchange with it. It keeps what B5c reads (`b5c-smoke.md`, its Task on the rig): `libraryfake.New`, `AccessTTL`, `EmailCode`, `URL`, `Config.Handler`, `Close`, and `SetEmailOpaque` with the meaning that plan gives it (the default fake answers the email-code route with a refresh token; `SetEmailOpaque(true)` turns that off). `Server.Replays` is an `int` field.
3. **Consumes plan A's Task 7** (the email-code route issues a `refresh_token` to the MonoAgent client and honors `resource` in the body, answering like the token endpoint). Task 5 serves both shapes. Until plan A ships, `account login --email` ends in `ErrEmailSessionUnavailable` and a headless machine cannot sign in (spec D10): the lead should keep plan A's Task 7 ahead of B1b in the merge order.
4. **Add this plan's files and names to index §3.** Files (§3.1): `internal/account/oauth.go`, `internal/account/logout.go`, `internal/account/adopt.go`, `internal/library/session.go`, `internal/library/libraryfake/control.go`, `internal/library/libraryfake/jwt.go`, `cmd/monoagentcli/account.go`, `cmd/monoagentcli/account_login.go`, and the one-line registration in `cmd/monoagentcli/root.go`. Exported names beyond §3.2: `account.Host`, `account.SetHostForTest`, `account.SetSealerForTest`, `account.DefaultStore`, `account.NewClient` with `Client`, `account.NewRefresher`, `account.DiscoverEndpoints` and `account.AuthorizeInBrowser`; `library.SessionSource`, `library.AccountSession` and `(*library.Client).LogoutLegacy`; the switches of `libraryfake` (Task 3). The two `*ForTest` hooks follow index line 344: process globals, no `t.Parallel()`.
5. **A trap for the other plans' CLI tests.** A test that signs in through the default store (`account.Login`, `account login` through `newRootCmd`) more than once per test binary must call `account.SetSealerForTest(t, account.NewMemorySealer())`, as `libFixture` now does; see the decision on the sealer above. A test that installs its own guard with `accounttest` is not affected.
6. **Agrees with B5a's adoption wiring** (b5a-rollout.md, its Task 6, lines 991 and 1243). B5a tries once per database and claims the try with a settings row (`INSERT OR IGNORE` of `account_adoption`) before it calls `library.AdoptIntoAccount`; Task 9 matches: one call tries every profile that has an older login, in order, and decides the fate of each refresh token itself; after `invalid_grant` the vault copy is removed, as it is after an adoption, and it stays only when monoes.me gave no verdict, which is what B5a's limits 2 and 6 already say. Nothing to change on either side.

