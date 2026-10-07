> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# B1b Task 2 report: internal/library delegates its browser sign-in to account

- Status: DONE
- Worktree / branch: `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b`, `feat/account-b1b`
- Commit: `8ba858d2` refactor(library): sign in through the shared browser sign-in of account (parent `38b5836d`)
- Files changed (only these two, `git status --short` was clean after the commit): `internal/library/auth.go`, `internal/library/client.go`. 2 files, +19 / -162. auth.go is now 258 lines, client.go 371.

## 1. What I implemented

The brief's Step 2, edit for edit (the diff holds nothing else):

- `internal/library/auth.go`
  - imports: the brief's block (adds `internal/account`; `crypto/rand`, `crypto/sha256`, `encoding/base64`, `errors`, `html`, `net` are gone with the code that used them).
  - deleted `oauthMeta` and the three `default*Path` constants.
  - `endpoints` returns `*account.OAuthEndpoints` and calls `account.DiscoverEndpoints(ctx, c.HTTP, c.BaseURL)`, once per client, cached under `c.mu` (the brief's text).
  - deleted `onBase` (see section 2).
  - `LoginPKCE` calls `account.AuthorizeInBrowser` (params `client_id` and `scope`, `Label: "library login"`, the same success text as before), then does the same `postToken` code exchange and `finishLogin` as before.
  - deleted `writeCallbackPage` and `randomString`.
- `internal/library/client.go`: imports `internal/account`; the `oauth` field is `*account.OAuthEndpoints`.
- The public API keeps its signatures: `Client.LoginPKCE`, `SendEmailCode`, `VerifyEmailCode`, `Logout`, `Token`, `LoginOptions`, `TokenStore`. No test file was changed.
- No import cycle: `go build ./...` is clean (account imports only the standard library, `x/sys` and `internal/secrets`, per its import rule and `TestImportsOnlyWhatTheImportRuleAllows`).

## 2. Where `onBase` was, and proof nothing calls it any more

`onBase` was `func (c *Client) onBase(endpoint, fallback string) string` at `internal/library/auth.go:99-116` at `38b5836d` (the parent; the file is unchanged since `f4441a2a`), called three times from `endpoints` (auth.go:90-92). It joined an off-host metadata path to the base URL with `u.Path` (decoded, no leading-slash guard): the same defect Task 1's review fixed in `account.pinToBase`. It is deleted; the library now gets the fixed logic by delegation, and no copy of the old logic remains in `internal/library`.

Exact, word-bounded grep over the whole tree, run after the change (a plain substring grep also hits `notionBaseURL` and `agentInstructionBasenames`, which are unrelated):

```
$ grep -rnw --include='*.go' -e onBase -e oauthMeta -e writeCallbackPage -e randomString -e defaultAuthorizePath -e defaultTokenPath -e defaultRevokePath .
grep exit=1 (no match anywhere in the tree)
```

And no discovery / PKCE / loopback logic is left in the library's production files: `grep -nE 'well-known|EqualFold|net\.Listen|code_challenge|sha256\.Sum256|http\.Server|state'` over `auth.go client.go store.go provenance.go types.go` finds only three unrelated lines (`isLoopbackHost`'s `EqualFold(h, "localhost")`, the artifact download checksum `sha256.Sum256(b)`, and the official-owner check in `types.go:97`).

## 3. TDD evidence

This is a behaviour-preserving move, so its guard is the existing suite: the same 12 tests, green before and green after. The one behavioural change the move brings (the library no longer carries `onBase`'s host-confusion defect) has its own RED/GREEN through a scratch test that is NOT committed (the task's file list and commit command name only auth.go and client.go). Its source is in the appendix.

### Step 1, baseline on the untouched tree (before)

```
$ go test ./internal/library/ -count=1 -race
ok  	github.com/monoes/mono-agent/internal/library	17.435s
```

Verbose, the 12 tests: TestLoginPKCELoopback, TestLoginPKCETimesOutWithoutBrowser, TestTokenRefresh, TestEmailCodeLogin, TestDownloadVerifiesSHA256, TestErrorsAndVisibility, TestReadsNeedALogin, TestReadRefreshesOnceOn401, TestFakeAnonymousReadsToggle, TestPublishRoundTrip, TestBaseURLMustBeHTTPS, TestProvenanceIsProfileScoped (all PASS).

### RED, scratch test on the OLD code (before the edit)

```
$ go test ./internal/library/ -count=1 -race -run 'TestScratchHostileEndpointsStayOnTheBaseHost' -v
--- FAIL: TestScratchHostileEndpointsStayOnTheBaseHost (0.83s)
    --- FAIL: .../at-sign          authorize URL "http://127.0.0.1:55459@evil.example/x?client_id=monoagent&..." is not under http://127.0.0.1:55459/
    --- FAIL: .../leading-dot      authorize URL "http://127.0.0.1:55467.evil.example/x?..." is not under http://127.0.0.1:55467/
    --- FAIL: .../bare-host-path   authorize URL "http://127.0.0.1:55470evil.example/x?..." is not under http://127.0.0.1:55470/
    --- FAIL: .../no-leading-slash authorize URL "http://127.0.0.1:55473oauth/token?..." is not under http://127.0.0.1:55473/
FAIL	github.com/monoes/mono-agent/internal/library	1.536s
```

(this block is condensed, not raw output: each failure message is joined onto its subtest line, and the query strings are trimmed because they carry the PKCE challenge and the state; the hosts, ports, paths and verdicts are verbatim). With the production base `https://monoes.me` the first row reads `https://monoes.me@evil.example/x`: the host is `evil.example` and the base is mere userinfo. The test only calls `OnURL` and never `Open`, so nothing was contacted but the local fake.

### Step 2 applied, then GREEN with the same scratch test

```
$ go test ./internal/library/ -count=1 -race -run 'TestScratchHostileEndpointsStayOnTheBaseHost' -v
--- PASS: TestScratchHostileEndpointsStayOnTheBaseHost (0.83s)
    --- PASS: .../at-sign (0.21s)   --- PASS: .../leading-dot (0.20s)
    --- PASS: .../bare-host-path (0.21s)   --- PASS: .../no-leading-slash (0.21s)
ok  	github.com/monoes/mono-agent/internal/library	2.387s
```

(Also condensed: two PASS lines share one line; the verdicts, names and timings are verbatim.) The scratch file was then deleted with plain `rm` (before Step 3, so build, vet, gofmt, the final run and the commit all saw exactly the two-file tree).

### Step 3, build, vet, format

```
$ go build ./... && go vet ./internal/library/ && gofmt -l internal/library
(no output, exit 0)
```

### Step 4, library tests after

```
$ go test ./internal/library/ -count=1 -race
ok  	github.com/monoes/mono-agent/internal/library	6.704s
```

The verbose pass list (timings stripped) is identical to the baseline's: same 12 tests, all PASS (a `diff` of the two lists is empty).

### Extra evidence (not required by the brief)

- `go test ./internal/library/ -race -count=5 -run 'TestLoginPKCE|TestTokenRefresh|TestErrorsAndVisibility|TestEmailCodeLogin|TestReadRefreshesOnceOn401'` -> `ok ... 4.024s` (stable over five runs).
- `go test ./cmd/monoagentcli -run 'TestLibrary' -count=1 -v` -> 12 tests PASS, `ok ... 251.118s` (slow because the machine is shared by several lanes). These drive `library login` (`TestLibraryLoginStatusLogout`, `TestLibraryEmailLogin`) and the install/publish round trips through the real command and `LoginPKCE`. None of the known-failing tests and not the hanging doctor test match `TestLibrary`.
- `go test ./internal/account/ -count=1 -race -run 'TestAuthorize|TestDiscover|TestImportsOnlyWhatTheImportRuleAllows|TestTheImportRuleForbidsWhatItSays'` -> 6 PASS (Task 1's code the library now depends on; `internal/account` itself is untouched).

## 4. What bounds each step now (the lead's request: `opts.Timeout` no longer covers the whole login)

Before, `LoginPKCE` did `ctx, cancel := context.WithTimeout(ctx, opts.Timeout)` and used that one ctx for everything, so a single 5-minute window (default) ran from the start, discovery included. Now `opts.Timeout` is handed to `account.AuthorizeInBrowser` and bounds only the wait for the browser. The rest runs on the caller's ctx:

| Step | Before | Now |
|---|---|---|
| Discovery GET `/.well-known/oauth-authorization-server` (`c.endpoints(ctx)` -> `account.DiscoverEndpoints(ctx, c.HTTP, ...)`) | the shared `opts.Timeout` window | caller's ctx and `c.HTTP.Timeout` (2 minutes) |
| Wait for the browser redirect | what was left of the shared window after discovery | `AuthorizeInBrowser` wraps its own `context.WithTimeout(ctx, o.Timeout)` (`internal/account/oauth.go:112`): `opts.Timeout`, default 5 minutes (also defaulted there when `<= 0`), counted from the start of the wait. It ends early when the caller's ctx is cancelled. The error text is unchanged: `library login: no answer from the browser within <timeout>` |
| Code exchange (`postToken`) | what was left of the shared window (so a user who took nearly all of it in the browser left the exchange almost no time) | caller's ctx and `c.HTTP.Timeout` (2 minutes), a fresh window |
| `/me` (`finishLogin` -> `c.Me` -> `c.do`) | what was left of the shared window | caller's ctx and `c.HTTP.Timeout` (2 minutes) per request. On a 401 `do` refreshes once (one more token POST, 2 minutes) and retries (another 2 minutes) |
| vault `Store.Save` | what was left of the shared window | caller's ctx only |

Facts behind the "2 minutes": the only constructor of a production client is `library.NewClient` (`internal/library/client.go:64` after this commit, line 62 before it; `&http.Client{Timeout: 2 * time.Minute}`), used by `cmd/monoagentcli/library.go:78`; I grepped the tree (`\.HTTP\s*=` and `HTTP:`) and nothing replaces `Client.HTTP` (a `Client` built by hand with a bare `http.Client` would have no HTTP bound, only the ctx).

For the CLI the caller's ctx is `cmd.Context()`, rooted at `signal.NotifyContext(context.Background(), os.Interrupt, SIGTERM, SIGHUP)` (`cmd/monoagentcli/main.go:85-88`): no deadline. I checked that nothing between the root and `library login` replaces it: no `context.WithTimeout`, `WithDeadline` or `SetContext` in `root.go` or `library*.go` except `library_install.go:220-222`, which is `runSubcommand`'s in-process `workflow import` handoff and not the login, and the root `PersistentPreRun` (`root.go:45`) only reads `cmd.Context()`. So Ctrl-C still cancels every step, and nothing but the 2-minute HTTP timeout bounds discovery, the exchange and `/me`. The whole login can therefore take up to `Timeout` plus the HTTP bounds of the other steps instead of exactly `Timeout`. The CLI flag `--timeout` already reads "How long to wait for the browser sign-in" (`cmd/monoagentcli/library.go:346`), which is what it now does.

One more lifetime change: the loopback listener is closed (graceful `Shutdown`, at most 2 s) as soon as `AuthorizeInBrowser` returns, that is before the token exchange. Before, it lived until `LoginPKCE` returned, after `/me` and the vault write.

Otherwise the flow is unchanged: same error texts (`Label: "library login"`), same callback page text, same authorize-URL parameters (encoded in sorted key order both before and after), same exchange form, the same fallback endpoints when discovery fails.

## 5. Self-review findings

- The diff equals the brief's edits and nothing else (reviewed with `git diff` before committing); each deletion took one adjacent blank line so gofmt stays clean (confirmed by `gofmt -l`).
- After the deletions, `auth.go` still needs exactly the brief's import set: `bytes`, `encoding/json`, `fmt`, `net/http`, `strings`, `time` are used by `SendEmailCode`, `VerifyEmailCode`, `Logout`, `tokenFrom` and `readToken`; the build confirms it.
- `DiscoverEndpoints` always returns non-nil endpoints, so `endpoints` never hands out nil; its error (set only when no HTTP answer came back) is ignored exactly as the old code ignored the transport error, and the fallbacks are cached as before.
- `Client.oauth` is unexported and `account.OAuthEndpoints` has the same JSON tags as the deleted `oauthMeta`, so nothing outside the package can notice the type change.
- Behaviour differences are the ones in section 4 plus the fix of the `onBase` defect (section 2); I found no others.

## 6. Concerns and suggestions (none blocks)

1. No permanent library-level regression test for the hostile-endpoint case. The brief's file list and commit command exclude any new file, so the scratch test is not committed. `account`'s own `TestDiscoverPinsEveryEndpointToTheBaseHost` covers the logic; a library-level test would only guard against someone re-inlining a copy. If you want it kept, it is one new file `internal/library/endpoints_test.go` (package `library_test`, reuses `memStore` from `library_test.go`); say so and I add it.
2. `LoginOptions.Timeout` still says "default 5 minutes", which stays true, but it now bounds only the browser wait. I did not touch the comment (the brief does not list it); a one-line doc tweak is optional.
3. `internal/account/doc.go`'s "Files:" list does not mention `oauth.go` (Task 1 added it). I did not touch `internal/account`.
4. The brief's commit command has no "why" paragraph, the lane rules require one: I kept the brief's subject and trailer verbatim and added the paragraph as the middle `-m`.
5. The monograph index is stale (hints still point `onBase` at `auth.go:99` after the change); the grep above is the authority. A rebuild is not part of this task.

## 7. Contract change requests

None.

## Appendix: the scratch test (never committed; a copy is at `$LOCAL_CLAUDE_SCRATCH/-Users-morteza-Desktop-monoes-mono-agent/ab93060e-329b-4819-88ef-4bab24fc04a4/scratchpad/b1b-t2-scratch-hostile-endpoint_test.go.txt`)

```go
package library_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/library"
)

func TestScratchHostileEndpointsStayOnTheBaseHost(t *testing.T) {
	for _, c := range []struct{ name, bad string }{
		{"at-sign", "@evil.example/x"},
		{"leading-dot", ".evil.example/x"},
		{"bare-host-path", "evil.example/x"},
		{"no-leading-slash", "oauth/token"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/.well-known/oauth-authorization-server" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"authorization_endpoint":"` + c.bad + `","token_endpoint":"` + c.bad + `","revocation_endpoint":"` + c.bad + `"}`))
			}))
			defer srv.Close()
			cl, err := library.NewClient(srv.URL, &memStore{})
			if err != nil {
				t.Fatal(err)
			}
			var shown string
			// No Open: nothing is contacted but the fake; the wait ends at the timeout.
			_, _ = cl.LoginPKCE(context.Background(), library.LoginOptions{OnURL: func(u string) { shown = u }, Timeout: 200 * time.Millisecond})
			if !strings.HasPrefix(shown, srv.URL+"/") {
				t.Fatalf("authorize URL %q is not under %s/", shown, srv.URL)
			}
			u, err := url.Parse(shown)
			if err != nil {
				t.Fatalf("authorize URL %q does not parse: %v", shown, err)
			}
			if u.Host != strings.TrimPrefix(srv.URL, "http://") || u.User != nil {
				t.Fatalf("authorize URL %q: host %q, userinfo %v", shown, u.Host, u.User)
			}
		})
	}
}
```

---

# Fix round 1 (ruling R21): the library-level hostile-endpoint guard is committed

- Status: DONE
- Commit: `a4cabd40` test(library): the library's endpoints never leave the base host, on top of `8ba858d2` (a separate commit; `git add` and `git commit` were separate commands). It was amended once, before it was reported (it was `2a13bece`), so that its browser goroutine is awaited: see "The browser goroutine" below.
- Files: exactly one, the new `internal/library/endpoints_test.go` (228 lines, package `library_test`; it reuses `memStore` and `mustClient` of `library_test.go`). `auth.go` and `client.go` are untouched, byte-identical to `8ba858d2` (proof below). `libraryfake` is neither used nor changed.
- This closes concern 1 of the first report (no permanent library-level regression test).

## What the test does

Three tests over the same five hostile endpoint values, each served for ALL THREE of `authorization_endpoint`, `token_endpoint` and `revocation_endpoint`: `@evil.example/x`, `.evil.example/x`, `evil.example/x`, `oauth/token` and the absolute `http://other-host:8443/x`. That is 15 subtests.

- `hostileIssuer` is a small authorization server inside the test (the shared fake serves same-host metadata only, so it cannot play this). Its discovery document names the hostile value three times. It answers a request by what it is, on whatever path it arrives, so the test does not pin where an off-host endpoint lands on the base host (that is `account`'s decision, with its own tests): a GET carrying `redirect_uri` is the authorize request and sends the browser straight back with a code; a POST with a `grant_type` is a grant (answered with a token); a POST carrying `token` and no grant is a revocation (accepted); anything else is 404. It keeps the forms posted to it, by kind.
- `baseHostGuard` is the client's `HTTP.Transport`. A request whose `URL.Host` is not the base host's host:port is refused before any dial (no DNS, no connection), fails the test with `<METHOD> request to <host><path> leaves the base host <base>` and returns an error; everything else goes on to `http.DefaultTransport`. It prints no query string.
- `TestLoginPKCEStaysOnTheBaseHost` runs a complete `LoginPKCE`. The test's browser (`awaitedBrowser`, below) is only used when the authorize URL is on the base host; otherwise the context is cancelled, so a hostile URL is never followed and a red run ends at once. What it asserts: `OnURL`'s URL is an http URL for the base host with no userinfo (checked before the error, so a red run names the off-host URL, not a timeout); the issuer received exactly one `authorization_code` exchange carrying the code it issued and a non-empty `code_verifier`; the login was stored with the account `/me` reported. Completing the login goes a little beyond the brief's "OnURL must stay on the base": it puts the code and the verifier under the guard too.
- `TestRefreshStaysOnTheBaseHost`: a store holds an expired token with a refresh token, and a fresh client (it discovers on its own) calls `Token()`. `Token()` falls back to the stored login when its refresh fails, so what proves the refresh is the issuer's log (exactly one `refresh_token` grant carrying the stored refresh token) and the answer (the new access token returned and stored).
- `TestLogoutStaysOnTheBaseHost`: a store holds a live token with a refresh token, and a fresh client calls `Logout()`. Logout is best effort and says nothing of a failed revocation, so again the issuer's log decides: exactly one revocation carrying the stored token, and the login gone from the store.
- Fixture tokens are throwaway literals. No assertion message prints a token or a query string; the browser's own error text is Go's URL error, as in the existing `browser`. The guard compares host:port, as specified.

## The browser goroutine, and what the file relies on

- The existing `browser(t)` of `library_test.go` plays the user's browser on a goroutine nobody waits for, and reports with `t.Errorf`: after its test has ended that panics the whole test binary. Pre-flight D7 (plan commit `339066d8`) removed that hazard from `account`'s tests with `inBrowser`, which awaits the goroutine in `t.Cleanup`. `endpoints_test.go` follows the same convention with `awaitedBrowser`, a copy of `browser` that runs on a goroutine the subtest awaits in its cleanup (and uses a 10 s client timeout, so a stuck GET cannot hang that cleanup). `browser` itself is not changed: it is outside the file fence, and the plan's later tasks call it as it is.
- The file depends on `memStore` and `mustClient` of `library_test.go` only. No plan text edits `internal/library/library_test.go` (a grep over the account-gate plans: 0 files name it; the only other test files they create in `internal/library/` are Task 8's `session_test.go` and Task 9's `adopt_test.go`, which call `browser(t)`, `mustClient` and `memStore` as they are). None of the names `endpoints_test.go` declares (`hostileEndpoints`, `hostileIssuer`, `serveJSON`, `baseHostGuard`, `rig`, `newRig`, `onHost`, `awaitedBrowser`, the three `Test...StaysOnTheBaseHost`) appears in any plan for package `library_test` (B5c's `rig` and `newRig` are in package `accountsmoke`). So, as far as the plan texts go, a later task's verbatim edits do not clash with this file.

## RED: against a naive join (temporary, reverted, never committed)

The mutation replaced the single line `m, _ = account.DiscoverEndpoints(ctx, c.HTTP, c.BaseURL)` in `endpoints` (an absolute endpoint is used as it stands, anything else is concatenated onto the base URL, which gives both failure mechanisms below). Its diff, saved before the run:

```diff
@@ -57,7 +57,26 @@ func (c *Client) endpoints(ctx context.Context) *account.OAuthEndpoints {
	if m != nil {
		return m
	}
-	m, _ = account.DiscoverEndpoints(ctx, c.HTTP, c.BaseURL)
+	// MUTATION, never committed: a naive join. An absolute endpoint is used as it stands, anything else is
+	// concatenated onto the base URL.
+	raw := &account.OAuthEndpoints{}
+	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/.well-known/oauth-authorization-server", nil); err == nil {
+		if resp, err := c.HTTP.Do(req); err == nil {
+			_ = json.NewDecoder(resp.Body).Decode(raw)
+			resp.Body.Close()
+		}
+	}
+	join := func(e, fallback string) string {
+		switch {
+		case e == "":
+			return c.BaseURL + fallback
+		case strings.Contains(e, "://"):
+			return e
+		}
+		return c.BaseURL + e
+	}
+	m = &account.OAuthEndpoints{AuthorizationEndpoint: join(raw.AuthorizationEndpoint, "/authorize"),
+		TokenEndpoint: join(raw.TokenEndpoint, "/token"), RevocationEndpoint: join(raw.RevocationEndpoint, "/revoke")}
	c.mu.Lock()
	c.oauth = m
	c.mu.Unlock()
```

(saved copies: `$LOCAL_CLAUDE_SCRATCH/-Users-morteza-Desktop-monoes-mono-agent/ab93060e-329b-4819-88ef-4bab24fc04a4/scratchpad/b1b-t2-fix1-mutation.diff`, and the full red output of the final run next to it as `b1b-t2-fix1-red-full-2.txt`.)

The RED was run twice with this same mutation (checked with `cmp`): once before the amend, and again after it on the file that is committed. Both runs: 15 of 15 subtests FAIL, the same mechanisms, no panic.

```
$ go test ./internal/library/ -count=1 -race -run 'StaysOnTheBaseHost' -v        (exit 1)
--- FAIL: TestLoginPKCEStaysOnTheBaseHost   (its 5 rows FAIL)
--- FAIL: TestRefreshStaysOnTheBaseHost     (its 5 rows FAIL)
--- FAIL: TestLogoutStaysOnTheBaseHost      (its 5 rows FAIL)
FAIL	github.com/monoes/mono-agent/internal/library	2.343s
```

The failure messages of the final run, grouped by mechanism (condensed: one line per distinct message, the port shown as `P`, query strings never printed):

1. The guard refuses without dialing (`endpoints_test.go:100`), 4 subtests: Refresh and Logout, rows at-sign and absolute-other-host.
   - 2x `POST request to evil.example/x leaves the base host 127.0.0.1:P` (the join `http://127.0.0.1:P@evil.example/x` has `evil.example` as its real host)
   - 2x `POST request to other-host:8443/x leaves the base host 127.0.0.1:P` (an absolute endpoint taken as it stands)
2. The request never reached the issuer, all 10 Refresh and Logout subtests (`:200`, `:221`):
   - 5x `the refresh token did not reach the base host's token endpoint (0 refreshes)`
   - 5x `the stored token did not reach the base host's revocation endpoint (0 revocations)`
   - In 6 of them (rows leading-dot, bare-host, no-leading-slash) the joined URL does not even parse (`invalid port`), so nothing was sent and no guard message exists: only this assertion catches them. In the other 4 the guard also fired. Both `Token()` and `Logout()` swallow their HTTP errors, which is why they need the issuer's log and not only the guard.
3. The authorize URL (`:172`), all five Login rows; the test's browser followed none of them:
   - `the authorize URL "http://127.0.0.1:P@evil.example/x" is not on the base host 127.0.0.1:P`, and the same message for `"http://127.0.0.1:P.evil.example/x"`, `"http://127.0.0.1:Pevil.example/x"`, `"http://127.0.0.1:Poauth/token"` and `"http://other-host:8443/x"`

Compared with the scratch test of the first round: it reads only the authorize URL, so it could only ever see mechanism 3. Mechanisms 1 and 2 are the refresh and revocation requests that carry tokens, and there this test fails on its own (10 of the 15 failing subtests are the Refresh and Logout ones). I did not run a second mutation that joins only the token and revocation endpoints (RED once, as asked).

## Revert proof (after the second RED, before the amend)

```
$ git checkout -- internal/library/auth.go
$ git diff --exit-code internal/library/auth.go internal/library/client.go      exit 0 (byte-identical to HEAD)
$ git status --short                                                            ` M internal/library/endpoints_test.go` (the amended test file, the only pending change)
$ grep -c MUTATION internal/library/auth.go                                     0
$ grep -n 'account.DiscoverEndpoints' internal/library/auth.go                  60: m, _ = account.DiscoverEndpoints(ctx, c.HTTP, c.BaseURL)
$ go test ./internal/library/ -count=1 -race                                    ok ... 2.886s
```

After `git add` and `git commit --amend --no-edit`: `git status --short` prints nothing; `git log --oneline -3` is `a4cabd40`, `8ba858d2`, `38b5836d`; the message is unchanged (subject, why paragraph, `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`).

## GREEN: the real code

```
$ go test ./internal/library/ -count=1 -race -run 'StaysOnTheBaseHost' -v
--- PASS: TestLoginPKCEStaysOnTheBaseHost (0.14s)       (its 5 rows PASS)
--- PASS: TestRefreshStaysOnTheBaseHost (0.06s)         (its 5 rows PASS)
--- PASS: TestLogoutStaysOnTheBaseHost (0.02s)          (its 5 rows PASS)
ok  	github.com/monoes/mono-agent/internal/library	2.583s

$ go test ./internal/library/ -count=1 -race
ok  	github.com/monoes/mono-agent/internal/library	1.820s     (15 top-level tests PASS: the 12 existing and the 3 new)

$ go test ./internal/library/ -count=10 -race -run 'StaysOnTheBaseHost'
ok  	github.com/monoes/mono-agent/internal/library	2.641s

$ go vet ./internal/library/                                    ok
$ gofmt -l internal/library                                     (no output)
```

(The per-row PASS lines of the first command are condensed to "its 5 rows PASS"; names, timings and verdicts are verbatim.) The 15 subtests take well under a second together. A first run of the new tests on the real code, before any mutation, passed too (it showed the test itself is valid).

## Contract change requests

None.
