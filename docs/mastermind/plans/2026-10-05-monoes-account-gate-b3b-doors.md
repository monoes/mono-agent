# Mandatory monoes.me Account — B3b: the doors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every network door of the daemon (the HTTP API, the org receiver, `/v1` on both of its listeners, the webhook server, the extension bridge and MCP) refuses work while the machine holds no valid monoes.me login, answers in its own wire shape, and stays open in `grace` and while dormant.

**Architecture:** Each door asks `account.Require` (the process guard's cached verdict, no I/O) where it first looks at a request, and answers through a new small package, `internal/accountdoor`, which holds the one sentence, the one code and the one account summary. The HTTP API also gets a default-deny gate in front of its whole mux, because routes mounted through `ExtraRoutes` bring their own authentication and `Server.auth` cannot wrap them. Every door judges each request afresh and refuses call by call without cutting a stream, so a sign-in works on a running process at once and work in flight finishes (spec §6.4).

**Tech Stack:** Go 1.26 (`net/http` and `gorilla/websocket`, both already used), `internal/account` and `internal/account/accounttest` (B1a), OpenAPI 3.0.3 linted with `@redocly/cli@2.49.0` (CI job `openapi-lint`).

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§6.3, §6.4, D7, D22, D27). Index: `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§2, §3.1 to §3.4 items 5 to 8). Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

## Global Constraints

Copied from the index §2; the ones that bind these doors.

- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `EnsureFresh`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear only on a login or a refresh. The high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

## Review Focus

The failure modes the spec implies that the per-door tables alone would not exercise, most likely first, each with the test that pins it.

1. **A sign-in must work at once on a running process.** A socket refused by being cut, or a verdict a door cached, keeps refusing after the person signs in. Pins (Task 7): `TestABridgeSocketRecoversWhenTheAccountDoes` and the subtests of `TestCDPIsRefusedWhileLocked`: the same socket, refused call by call and never cut, works again.
2. **An OpenAI SDK behind a locked daemon.** It parses only `{"error":{…}}` and retries a 5xx, so a flat body or a 500 becomes a crash or a retry storm. Pins (Task 5): `TestV1RoutesAreRefusedWhileLocked`, `TestTheLoopbackMountRefusesInTheEnvelopeAndTheRestFlat`, `TestAnExecRefusedForTheAccountIsA401NotA500` (the account locks after the door let the request in).
3. **A refusal that reads like another failure.** The relay client turns a 401 into "re-pair", and a 404 or 405 on a locked server hides the cause and shows a probe which routes exist. Pins: `TestRelayIsRefusedWhileLocked` (Task 7), `TestLockedServerRefusesEveryPathButHealth` (Task 2).
4. **A webhook sender and the operator.** A 503 without `Retry-After` makes the sender hammer, a body read before the check lets a locked server be fed megabytes, and with no log line a locked daemon looks dead. Pins (Task 6): `TestWebhookServerRefusesRequestsWhileLocked` (a 2 MiB request included), `TestWebhookServerLogsALockedAccountOncePerMinute`.
5. **A door that names the user, or a route nobody guarded.** `/health` and `ping` answer callers with no credential: state, reason and whether it is enforced, never the email; a route added through `ExtraRoutes` with no door of its own is refused anyway. Pins: `TestNoBodyNamesTheUser` (Task 1), `TestHealthIsOpenInEveryStateAndReportsTheAccount`, `TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused` (Task 2).

---


## How the doors answer, and what this plan assumes

| Door | Where it checks | Locked answer | Task |
|---|---|---|---|
| HTTP API | `accountGate` in front of the mux, and `Server.auth` | 401 `{"error":"login_required","login_required":true,"account":{"state","reason"}}` (index §3.4 item 5) | 2 |
| Org receiver | `Receiver.ServeHTTP` | the same 401 body | 4 |
| `/v1`, both listeners | `Gateway.auth` and `turnError` | 401 in the OpenAI envelope, code `login_required`, the fixed message | 5 |
| Webhook server | `WebhookServer.ServeHTTP` | 503, `Retry-After: 60`, `{"error":"login_required"}` | 6 |
| Extension bridge | request frames, the relay, the CDP socket (connect and each command) | reply code `account_locked`; 503 | 7 |
| MCP | `Server.handleToolsCall` | `isError: true`, the login-required text | 8 |

- **B1a is merged** as the index §3 describes. The code below was compiled and run in a scratch export of `HEAD`, first against a stand-in for `internal/account` and `accounttest` with the contract's exact signatures, then against the real package that B1a's plan produces (the real `Require`, `CurrentStatus` and `accounttest.Install`, in strict and dormant mode); every door test and every mutation check below passes there as written.
- **B3a owns** the engine and runner checks, the guard wiring in the serving commands (`cmd/monoagentcli/daemon.go`, `httpapi.go`, `mcp.go`, `extension_serve.go`) and the heartbeat field. This plan edits no file of `cmd/monoagentcli`.
- **While dormant** (D22) no door refuses anything. The one change a caller can see is additive JSON: the `account` key of `GET /health` and of `ping` (`state`, `reason`, `valid_until` and `enforced`, which is false while dormant), which index §3.4 items 5 and 7 require and which no existing consumer reads (`internal/apiconfig/probe.go:63` and `cmd/monoagentcli/doctor_env_services.go:49` look at the status code only).
- **Locked processes reach these doors.** B2's CLI gate has a `serve` class (`daemon`, `httpapi`, `mcp`, `extension serve`) that starts while locked, and `account.Require` allows everything while dormant or before the enforcement date, so a refusal test enforces first: `accounttest.Install` with a locked mode sets a past date.
- **Every door reads the guard per request**, through `account.Require` (and `account.CurrentStatus` for the account summary): no door caches, subscribes or polls. A process that never installs a guard fails closed outside a test binary once enforcement is on (D22, D24), which is what the real-binary smoke of B5a checks for each entry point.
- **Edits** to existing files are shown as diff blocks: lines starting with `-` are removed, lines starting with `+` are added, and the lines that start with a space are context. The context and the `-` lines together are unique in the file, so they are the `old_string` of one `Edit` call and the context and the `+` lines are its `new_string`. Strip the one leading character of each line; the rest, tabs included, is the file's text.
- **Test commands** filter the noisy `go test` output with `grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`. Nothing here needs `-race` except where a task says so: the new HTTP API tests alone take about two minutes under `-race` (each builds a vault), and the HTTP API, `/v1`, receiver and MCP doors add no shared state.

---

### Task 1: The words every door says, and the test table

**Files:**
- Create: `internal/accountdoor/accountdoor.go`, `internal/accountdoor/accountdoor_test.go`, `internal/accountdoor/doortest/doortest.go` (test support: the table every door test walks)

**Interfaces:**
- Consumes (index §3.2, §3.3): `account.Status`, `State`, `Reason`, `User`, `LoginRequiredError{Status}`, `Require(ctx) error`, `CurrentStatus() Status`, `IsLoginRequired(err) bool`, the constant `LoginRequiredMessage` (approved by the lead as B1a's refinement); `accounttest.Install(t, Mode) *account.Guard` and the modes `SignedIn`, `InGrace`, `LockedNoLogin`, `LockedRefused`, `Dormant`.
- Produces:
  ```go
  package accountdoor
  const Message = account.LoginRequiredMessage              // "Log in to monoes.me first: monoagentcli account login"
  const Code = "login_required"
  type Summary struct{ State account.State; Reason account.Reason; ValidUntil time.Time; Enforced bool } // JSON state, reason, valid_until (omitted when zero), enforced
  func SummaryOf(st account.Status) Summary                 // never carries the user
  func Refusal(err error) account.Status                    // the verdict behind an error from account.Require
  func WriteUnauthorized(w http.ResponseWriter, err error)  // 401 {"error":"login_required","login_required":true,"account":{"state","reason"}}
  package doortest
  type Row struct{ Name string; Mode accounttest.Mode; Refused bool; State, Reason string; Enforced bool }
  var Modes []Row // signed in, grace, dormant (allowed; dormant is not enforced); locked/no login, locked/refused (refused)
  ```

- [ ] **Step 1: Create the table** `internal/accountdoor/doortest/doortest.go`:

```go
// Package doortest is the table every door's tests walk: one row for each state
// a door is judged in. It is test support, imported only from _test.go files.
package doortest

import "github.com/monoes/mono-agent/internal/account/accounttest"

// Row is one state of the account and what a door does in it.
type Row struct {
	Name string
	Mode accounttest.Mode
	// Refused is whether a request is turned away in this state.
	Refused bool
	// State and Reason are what a door that reports the account says of it. They
	// are empty for Dormant, whose verdict is the fixture's business: nothing is
	// refused whatever it says.
	State, Reason string
	// Enforced is what such a door says of enforcement: false only while dormant.
	Enforced bool
}

// Modes covers the five fixtures of accounttest. Grace and Dormant must be
// allowed: a login that is offline for under 24 hours keeps working, and until
// the enforcement date nothing locks.
var Modes = []Row{
	{"signed in", accounttest.SignedIn, false, "ok", "", true},
	{"grace", accounttest.InGrace, false, "grace", "unreachable", true},
	{"dormant", accounttest.Dormant, false, "", "", false},
	{"locked, no login", accounttest.LockedNoLogin, true, "locked", "not_logged_in", true},
	{"locked, refused", accounttest.LockedRefused, true, "locked", "refused", true},
}
```

- [ ] **Step 2: Write the failing test** `internal/accountdoor/accountdoor_test.go`:

```go
package accountdoor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
)

func TestWriteUnauthorizedBody(t *testing.T) {
	if Message != "Log in to monoes.me first: monoagentcli account login" || Code != "login_required" {
		t.Errorf("Message = %q, Code = %q", Message, Code)
	}
	rec := httptest.NewRecorder()
	WriteUnauthorized(rec, &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonRefused}})

	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q: the bearer is not what was refused", got)
	}
	const want = `{"error":"login_required","login_required":true,"account":{"state":"locked","reason":"refused"}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

// Both shapes go to callers that hold no login, some of them unauthenticated:
// they say what the account is and why, never who.
func TestNoBodyNamesTheUser(t *testing.T) {
	st := account.Status{
		State: account.StateGrace, Reason: account.ReasonUnreachable, Enforced: true,
		User:       &account.User{ID: "user-123", Email: "someone@example.test", Username: "someone"},
		ValidUntil: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
	}
	rec := httptest.NewRecorder()
	WriteUnauthorized(rec, &account.LoginRequiredError{Status: st})
	summary, _ := json.Marshal(SummaryOf(st))
	for _, body := range []string{rec.Body.String(), string(summary)} {
		for _, leak := range []string{"user-123", "someone", "example.test", `"user"`, `"email"`} {
			if strings.Contains(body, leak) {
				t.Errorf("a door body names the user (%q): %s", leak, body)
			}
		}
	}
	bare, _ := json.Marshal(SummaryOf(account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}))
	if string(summary) != `{"state":"grace","reason":"unreachable","valid_until":"2026-10-05T12:00:00Z","enforced":true}` ||
		string(bare) != `{"state":"locked","reason":"not_logged_in","enforced":false}` {
		t.Errorf("summaries = %s and %s", summary, bare)
	}
}

// Require is the one verdict the doors share: refused exactly where the table
// says, always as a *LoginRequiredError.
func TestRequireInEveryState(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			accounttest.Install(t, c.Mode)
			err := account.Require(context.Background())
			if (err != nil) != c.Refused || (err != nil && !account.IsLoginRequired(err)) {
				t.Fatalf("Require = %v (%T), want refused = %v", err, err, c.Refused)
			}
		})
	}
}
```

- [ ] **Step 3: Run it and watch it fail**

Run: `go test ./internal/accountdoor/... -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `FAIL github.com/monoes/mono-agent/internal/accountdoor [build failed]` with `accountdoor_test.go:…: undefined: Message` (and `Code`, `WriteUnauthorized`, `Refusal`, `SummaryOf`).

- [ ] **Step 4: Implement** `internal/accountdoor/accountdoor.go`:

```go
// Package accountdoor is what every network door of the daemon says to a
// caller it refuses while the monoes.me account is locked: the HTTP API, the
// org receiver, the /v1 gateway, the webhook server, the extension bridge and
// the MCP server each answer in their own wire shape, but with one sentence,
// one code and one account summary. It holds no policy: whether to refuse is
// account.Require's verdict.
package accountdoor

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

const (
	// Message is the sentence a refused caller reads: the first line of every
	// refusal of the account package, said alone.
	Message = account.LoginRequiredMessage
	// Code is the machine-readable code of a refusal.
	Code = "login_required"
)

// Summary is the part of the account state a door may show to a caller that
// holds no monoes.me login: the verdict and why, never who. Enforced says
// whether anything is refused: while dormant, and until the enforcement date,
// State can be locked while every door still answers.
type Summary struct {
	State      account.State  `json:"state"`
	Reason     account.Reason `json:"reason"`
	ValidUntil time.Time      `json:"valid_until,omitzero"`
	Enforced   bool           `json:"enforced"`
}

// SummaryOf reduces a verdict to what a door may show.
func SummaryOf(st account.Status) Summary {
	return Summary{State: st.State, Reason: st.Reason, ValidUntil: st.ValidUntil, Enforced: st.Enforced}
}

// Refusal is the verdict behind an error from account.Require. Any error
// refuses; one that is not a *account.LoginRequiredError is described by the
// current verdict.
func Refusal(err error) account.Status {
	var lre *account.LoginRequiredError
	if errors.As(err, &lre) {
		return lre.Status
	}
	return account.CurrentStatus()
}

type lockedAccount struct {
	State  account.State  `json:"state"`
	Reason account.Reason `json:"reason"`
}

type unauthorizedBody struct {
	Error         string        `json:"error"`
	LoginRequired bool          `json:"login_required"`
	Account       lockedAccount `json:"account"`
}

// WriteUnauthorized answers a refused HTTP request as the HTTP API and the org
// receiver do: 401 and {"error":"login_required","login_required":true,
// "account":{"state","reason"}}. It sets no WWW-Authenticate header: the
// caller's bearer is not what was refused.
func WriteUnauthorized(w http.ResponseWriter, err error) {
	st := Refusal(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(unauthorizedBody{
		Error:         Code,
		LoginRequired: true,
		Account:       lockedAccount{State: st.State, Reason: st.Reason},
	})
}
```

- [ ] **Step 5: Run it and watch it pass**

Run: `go test ./internal/accountdoor/... -count=1 -race 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/accountdoor	…`

- [ ] **Step 6: Commit**

```
git add internal/accountdoor
git commit -m "feat(account): the words every door says to a locked caller" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The HTTP API door

The gate in front of the mux is the door of every route. `Server.auth` keeps its own check, so that a handler it wraps never relies on the gate and a locked server does no vault work (`ensureToken` mints and stores a bearer token on first use).

**Files:**
- Create: `internal/httpapi/account_door_test.go`
- Modify: `internal/httpapi/server.go`: imports (3-14), `Server` (27-32), `NewServer` (48), `Handler` (74-77), `Serve` (94), `auth` (155-157), before `bearerCredential` (172), `handleHealth` (201)

**Interfaces:**
- Consumes: `accountdoor.WriteUnauthorized`, `accountdoor.SummaryOf`, `account.Require`, `account.CurrentStatus`, `doortest.Modes`, `accounttest.Install`; the package's `newTestServer(t, allowMutations bool) *Server`, `testToken(t, s) string`, `doReq(t, s, method, path, token string, body []byte) *httptest.ResponseRecorder` (`server_test.go:37`, `66`, `75`) and `tokenSecretName` (`token.go:20`).
- Produces: `func accountGate(next http.Handler) http.Handler`, `func openWhileLocked(r *http.Request) bool`, the field `Server.handler` (which `Handler()` and `Serve` now use). Open while locked: `GET` and `HEAD /health`, and the `/v1` paths, whose gateway answers in the OpenAI envelope (Task 5). Everything else gets the 401 body of index §3.4 item 5. `GET /health` gains `"account":{"state","reason","valid_until","enforced"}`.

- [ ] **Step 1: Write the failing tests** `internal/httpapi/account_door_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/secrets"
)

// wantLoginRequired fails unless rec is exactly the refusal of index §3.4 item 5:
// 401, {"error":"login_required","login_required":true,"account":{"state","reason"}}.
func wantLoginRequired(t *testing.T, rec *httptest.ResponseRecorder, state, reason string) {
	t.Helper()
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	acct, _ := body["account"].(map[string]any)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" ||
		body["error"] != "login_required" || body["login_required"] != true ||
		acct["state"] != state || acct["reason"] != reason || len(body) != 3 || len(acct) != 2 {
		t.Errorf("got %d %s (WWW-Authenticate %q), want the login_required 401 for %s/%s",
			rec.Code, rec.Body, rec.Header().Get("WWW-Authenticate"), state, reason)
	}
}

// A locked account is refused whatever bearer the request carries; in any other
// state the bearer rules apply as before.
func TestAuthenticatedRoutesAreRefusedWhileLocked(t *testing.T) {
	s := newTestServer(t, false)
	good := testToken(t, s)
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			accounttest.Install(t, c.Mode)
			for _, bearer := range []string{good, "wrong", ""} {
				rec := doReq(t, s, http.MethodGet, "/workflows", bearer, nil)
				switch {
				case c.Refused:
					wantLoginRequired(t, rec, c.State, c.Reason)
				case bearer == good && rec.Code != http.StatusOK:
					t.Errorf("good bearer: %d, want 200", rec.Code)
				case bearer != good && (rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == ""):
					t.Errorf("bearer %q: %d, want the bearer's own 401", bearer, rec.Code)
				}
			}
		})
	}
}

// /health answers 200 in every state, to a caller with no credential, and says
// what the account is: the state and why, never who.
func TestHealthIsOpenInEveryStateAndReportsTheAccount(t *testing.T) {
	s := newTestServer(t, false)
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			accounttest.Install(t, c.Mode)
			if rec := doReq(t, s, http.MethodHead, "/health", "", nil); rec.Code != http.StatusOK {
				t.Fatalf("HEAD /health = %d, want 200", rec.Code)
			}
			rec := doReq(t, s, http.MethodGet, "/health", "", nil)
			var body struct {
				Status, Version string
				Account         map[string]any
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK ||
				body.Status != "ok" || body.Version != "test" || body.Account["state"] == nil {
				t.Fatalf("GET /health = %d %s, want 200 with the old fields and an account", rec.Code, rec.Body)
			}
			if c.State != "" && (body.Account["state"] != c.State || body.Account["reason"] != c.Reason) {
				t.Errorf("account = %v, want %s/%s", body.Account, c.State, c.Reason)
			}
			if body.Account["enforced"] != c.Enforced {
				t.Errorf("account = %v, want enforced = %v", body.Account, c.Enforced)
			}
			if strings.Contains(rec.Body.String(), `"user"`) || strings.Contains(rec.Body.String(), "example.test") {
				t.Errorf("health names the user: %s", rec.Body)
			}
		})
	}
}

// Nothing but /health is open on a locked server. A path the API does not have
// (or a mutating one that is not registered) is refused like any other: a 404
// would tell a caller which routes exist. Odd spellings do not slip past, and the
// /v1 prefix is no way round: the mux only redirects a dot-segment after it.
func TestLockedServerRefusesEveryPathButHealth(t *testing.T) {
	paths := []struct{ method, path string }{
		{"GET", "/workflows"}, {"POST", "/workflows/x/run"}, {"POST", "/hil/x/approve"}, {"GET", "/nodes/x/schema"},
		{"POST", "/org-endpoint/x"}, {"GET", "/nope"}, {"POST", "/health"}, {"GET", "/health/"},
		{"GET", "//workflows"}, {"GET", "/./workflows"}, {"GET", "/WORKFLOWS"}, {"GET", "/v1x"},
	}
	for _, mutations := range []bool{false, true} {
		s := newTestServer(t, mutations)
		tok := testToken(t, s)
		accounttest.Install(t, accounttest.LockedNoLogin)
		for _, p := range paths {
			wantLoginRequired(t, doReq(t, s, p.method, p.path, tok, nil), "locked", "not_logged_in")
		}
		rec := doReq(t, s, http.MethodGet, "/v1/../workflows", tok, nil)
		if loc := rec.Header().Get("Location"); rec.Code == http.StatusOK || loc == "" {
			t.Fatalf("GET /v1/../workflows = %d, want a redirect", rec.Code)
		} else {
			wantLoginRequired(t, doReq(t, s, http.MethodGet, loc, tok, nil), "locked", "not_logged_in")
		}
	}
}

// A route registered through ExtraRoutes brings its own authentication, so auth
// cannot wrap it: one that forgot a door is still refused by the gate.
func TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused(t *testing.T) {
	var reached int
	s := newTestServer(t, false)
	s.opts.ExtraRoutes = func(mux *http.ServeMux) {
		mux.HandleFunc("GET /extra", func(http.ResponseWriter, *http.Request) { reached++ })
	}
	s.mux = s.routes() // registered as NewServer does, behind the same gate
	s.handler = accountGate(s.mux)
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			reached = 0
			accounttest.Install(t, c.Mode)
			rec := doReq(t, s, http.MethodGet, "/extra", "", nil)
			want := 1
			if c.Refused {
				wantLoginRequired(t, rec, c.State, c.Reason)
				want = 0
			}
			if reached != want {
				t.Fatalf("the extra route ran %d times, want %d", reached, want)
			}
		})
	}
}

// auth carries its own check, so a handler it wraps never relies on the gate: a
// locked account is refused before the vault is opened (no bearer token is
// minted for a locked server) and the handler does not run.
func TestAuthRefusesALockedAccountBeforeTheVault(t *testing.T) {
	s := newTestServer(t, false)
	accounttest.Install(t, accounttest.LockedNoLogin)

	var ran bool
	rec := httptest.NewRecorder()
	s.auth(func(http.ResponseWriter, *http.Request) { ran = true }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/workflows", nil))

	wantLoginRequired(t, rec, "locked", "not_logged_in")
	entries, err := secrets.List(context.Background(), s.rt.db.DB, s.rt.profileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == tokenSecretName {
			t.Fatal("a locked server minted its bearer token: the vault was opened for a refused request")
		}
	}
	if ran {
		t.Fatal("the wrapped handler ran for a locked account")
	}
}

// Serve is what runs in production: it must serve the gated handler, not the
// bare mux. A path the API does not have tells them apart: only the gate refuses it.
func TestServeServesTheGatedHandler(t *testing.T) {
	s := newTestServer(t, false)
	accounttest.Install(t, accounttest.LockedNoLogin)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("Serve did not stop")
		}
	})

	resp, err := http.Get("http://" + ln.Addr().String() + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusUnauthorized || body["error"] != "login_required" {
		t.Errorf("GET /nope over the listener = %d %v, want the login_required 401", resp.StatusCode, body)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/httpapi/ -run '^(TestAuthenticatedRoutesAreRefusedWhileLocked|TestHealthIsOpenInEveryStateAndReportsTheAccount|TestLockedServerRefusesEveryPathButHealth|TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused|TestAuthRefusesALockedAccountBeforeTheVault|TestServeServesTheGatedHandler)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: a build failure, `account_door_test.go:…: s.handler undefined` and `undefined: accountGate`.

- [ ] **Step 3: Implement.** Apply these eight edits to `internal/httpapi/server.go`, in order:

**Edit 1** in `internal/httpapi/server.go`:

```diff
 	"os"
+	"strings"
 	"time"
 
+	"github.com/monoes/mono-agent/internal/account"
+	"github.com/monoes/mono-agent/internal/accountdoor"
 	"github.com/monoes/mono-agent/internal/workflow"
 )
```

**Edit 2** in `internal/httpapi/server.go`:

```diff
 	mux  *http.ServeMux
-	rt   *runtime
+	// handler is mux behind the account gate: what Handler and Serve use.
+	handler http.Handler
+	rt      *runtime
 }
```

**Edit 3** in `internal/httpapi/server.go`:

```diff
 	s.mux = s.routes()
+	s.handler = accountGate(s.mux)
 	return s, nil
```

**Edit 4** in `internal/httpapi/server.go`:

```diff
-// Handler returns the server's http.Handler (auth + routing applied),
-// mainly for tests that want to drive it with httptest without a real
-// listener.
-func (s *Server) Handler() http.Handler { return s.mux }
+// Handler returns the server's http.Handler (account gate, auth and routing
+// applied), mainly for tests that want to drive it with httptest without a
+// real listener.
+func (s *Server) Handler() http.Handler { return s.handler }
```

**Edit 5** in `internal/httpapi/server.go`: replace `Handler:      s.mux,` with `Handler:      s.handler,`.

**Edit 6** in `internal/httpapi/server.go`:

```diff
 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		// A locked account is refused before the vault is opened or the bearer is read.
+		if err := account.Require(r.Context()); err != nil {
+			accountdoor.WriteUnauthorized(w, err)
+			return
+		}
 		want, err := ensureToken(r.Context(), s.rt.db.DB, s.rt.profileID)
```

**Edit 7** in `internal/httpapi/server.go`:

```diff
+// accountGate refuses every request but GET /health, and the /v1 paths (whose
+// gateway answers in the OpenAI envelope), while the account is locked. Routes
+// registered through ExtraRoutes bring their own authentication, so auth cannot
+// wrap them: this covers them, and a path the API does not have, so that a
+// locked server's 404 tells nothing about which routes exist.
+func accountGate(next http.Handler) http.Handler {
+	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		if !openWhileLocked(r) {
+			if refusal := account.Require(r.Context()); refusal != nil {
+				accountdoor.WriteUnauthorized(w, refusal)
+				return
+			}
+		}
+		next.ServeHTTP(w, r)
+	})
+}
+
+// openWhileLocked reports whether the gate leaves r to the mux whatever the
+// account says: the health check and the /v1 surface, which has its own door.
+func openWhileLocked(r *http.Request) bool {
+	switch p := r.URL.Path; {
+	case p == "/health":
+		return r.Method == http.MethodGet || r.Method == http.MethodHead
+	case p == "/v1", strings.HasPrefix(p, "/v1/"):
+		return true
+	}
+	return false
+}
+
 func bearerCredential(r *http.Request) string {
```

**Edit 8** in `internal/httpapi/server.go`:

```diff
 		"allow_mutations": s.opts.AllowMutations,
+		"account":         accountdoor.SummaryOf(account.CurrentStatus()),
 	})
```

- [ ] **Step 4: Run the new tests, then the whole package.** The package's existing tests install no guard, and with none installed `account.Require` fails open inside a test binary (D24), so they pass unchanged.

Run the command of Step 2, then `go test ./internal/httpapi/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/httpapi	…` both times (a few seconds, then about ten).

- [ ] **Step 5: Commit**

```
git add internal/httpapi/server.go internal/httpapi/account_door_test.go
git commit -m "feat(httpapi): refuse every route but /health while the monoes.me account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation checks** (after the commit). Make each change with the `Edit` tool (the line is unique in its file), run the command, expect the failure, restore the file with `git checkout -- <file>` (it returns to the commit just made), and go on. A pass would mean a door is not pinned: fix the test, not the mutation.

- **http-auth**: in `internal/httpapi/server.go` change `if err := account.Require(r.Context()); err != nil {` to `if err := account.Require(r.Context()); err != nil && false {`. Run `go test ./internal/httpapi/ -run '^(TestAuthRefusesALockedAccountBeforeTheVault)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/httpapi/server.go`.
- **http-gate**: in `internal/httpapi/server.go` change `s.handler = accountGate(s.mux)` to `s.handler = s.mux`. Run `go test ./internal/httpapi/ -run '^(TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused|TestLockedServerRefusesEveryPathButHealth)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/httpapi/server.go`.
- **http-serve**: in `internal/httpapi/server.go` change `Handler:      s.handler,` to `Handler:      s.mux,`. Run `go test ./internal/httpapi/ -run '^(TestServeServesTheGatedHandler)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/httpapi/server.go`.
- **http-health**: in `internal/httpapi/server.go` change `"account":         accountdoor.SummaryOf(account.CurrentStatus()),` to `"account_":        accountdoor.SummaryOf(account.CurrentStatus()),`. Run `go test ./internal/httpapi/ -run '^(TestHealthIsOpenInEveryStateAndReportsTheAccount)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/httpapi/server.go`.

---

### Task 3: OpenAPI describes the refusal and the account in /health

CI lints the spec (`.github/workflows/ci.yml`, job `openapi-lint`). No Go test reads this file, and none requires new `ref api` text (the tests that read `ref api`, in `cmd/monoagentcli/mcp_command_test.go`, pin its `mcp` paragraph only), so `ref api` is unchanged and B5b owns the prose elsewhere.

**Files:**
- Modify: `internal/httpapi/openapi.yaml`: `info.description` (18-19), `/health` (44), `/org-endpoint/{id}` 401 (301-303), the `/v1` error table (538), `components.responses.Unauthorized` (736-737), `components.schemas.Error` (759-762)

**Interfaces:**
- Produces: the schema `AccountState` (`state` in `ok|grace|locked`, `reason`, optional `valid_until` and `enforced`), `Error` gaining optional `login_required` and `account`, and the documented 401s.

- [ ] **Step 1: Lint the file as it is** (CI's exact command; `npx` needs the network once)

Run: `npx --yes @redocly/cli@2.49.0 lint internal/httpapi/openapi.yaml; echo "exit $?"`
Expected: `Your API description is valid.`, one warning (`operation-4xx-response` for `/health`, which exists today), `exit 0`.

- [ ] **Step 2: Edit the spec.** Apply these six edits to `internal/httpapi/openapi.yaml`:

**Edit 1** in `internal/httpapi/openapi.yaml`:

```diff
     (`monoagentcli api key`) instead of the token above.
+
+
+    Every route also needs a valid monoes.me login on the machine that serves
+    it. While there is none every path but `GET /health` answers `401` with
+    `login_required: true` (the `/v1` paths in their own OpenAI-shaped error),
+    before the bearer token or the API key is looked at. `GET /health` stays
+    open and reports the account state.
   version: "1.0.0"
```

**Edit 2** in `internal/httpapi/openapi.yaml`:

```diff
                   allow_mutations: { type: boolean }
+                  account: { $ref: "#/components/schemas/AccountState" }
```

**Edit 3** in `internal/httpapi/openapi.yaml`:

```diff
         "401":
-          description: Missing or wrong credential for an endpoint that has one
+          description: >
+            Missing or wrong credential for an endpoint that has one, or no
+            valid monoes.me login on this machine (`login_required: true`,
+            answered before the endpoint is looked up)
           content:
```

**Edit 4** in `internal/httpapi/openapi.yaml`:

```diff
-        | 401 | `authentication_error` / `invalid_api_key` | Missing, unknown or revoked key |
+        | 401 | `authentication_error` / `invalid_api_key`, `login_required` | Missing, unknown or revoked key; or no valid monoes.me login on this machine (the message is `Log in to monoes.me first: monoagentcli account login`, answered before the key is looked at) |
```

**Edit 5** in `internal/httpapi/openapi.yaml`:

```diff
     Unauthorized:
-      description: Missing or invalid bearer token
+      description: >
+        Missing or invalid bearer token, or no valid monoes.me login on this
+        machine (`login_required: true`)
```

**Edit 6** in `internal/httpapi/openapi.yaml`:

```diff
     Error:
       type: object
       properties:
         error: { type: string }
+        login_required:
+          type: boolean
+          description: True when the error is the refusal of a locked monoes.me account (`error` is then `login_required`).
+        account: { $ref: "#/components/schemas/AccountState" }
+    AccountState:
+      type: object
+      description: The machine's monoes.me login, as GET /health reports it. It never names the user.
+      required: [state, reason]
+      properties:
+        state: { type: string, enum: [ok, grace, locked] }
+        reason: { type: string, description: "Empty when ok. For grace, why the login was not renewed (unreachable, server_error, keyring_unavailable, unconfirmed); for locked, why it is refused (not_logged_in, expired, refused, clock_rollback, clock_skew, key_unknown, unconfirmed, invalid)." }
+        valid_until: { type: string, format: date-time, description: When the access token expires; absent without a session. }
+        enforced: { type: boolean, description: "Whether anything is refused. False while dormant and before the enforcement date, when state can be locked while every route answers. Present in GET /health; absent from a login_required 401, which implies it." }
 
```

- [ ] **Step 3: Lint again**

Run: `npx --yes @redocly/cli@2.49.0 lint internal/httpapi/openapi.yaml; echo "exit $?"`
Expected: the same single warning, `Your API description is valid.`, `exit 0`. Any other warning or error is yours.

- [ ] **Step 4: Commit**

```
git add internal/httpapi/openapi.yaml
git commit -m "docs(httpapi): openapi describes login_required and the account in /health" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The org receiver door

`POST /org-endpoint/{id}` is mounted through `ExtraRoutes` with its own authentication, so it is a door of its own: the gate of Task 2 covers it behind `httpapi`, but the receiver is also driven directly.

**Files:**
- Create: `internal/orgbridge/account_door_test.go`
- Modify: `internal/orgbridge/receiver.go`: imports (19), `ServeHTTP` (107-108)

**Interfaces:**
- Consumes: `accountdoor.WriteUnauthorized`, `account.Require`, `doortest.Modes`, `accounttest.Install`; the package's `newTestDB(t) *sql.DB` (`orgbridge_test.go:18`), `postDelivery(t, srv, id, d) (int, map[string]interface{})` (`receiver_test.go:19`), `NewMux`, `orggrant.NewStore(db).CreateEndpoint(ctx, profileID, org, role, workflowID)`.
- Produces: `(*Receiver).ServeHTTP` answers 401 with the flat body before the endpoint lookup and before it touches `pending`, `subs` or the database. A delivery accepted before the lock whose bus event arrives after it is not refused here: its run is refused in the engine, which records the failure and replies to the sender.

- [ ] **Step 1: Write the failing test** `internal/orgbridge/account_door_test.go`:

```go
package orgbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/workflow"
)

// The endpoint is mounted outside the HTTP API's own auth, so it is a door of its
// own: a locked account refuses a delivery before the endpoint is looked up (an
// unknown id gets the same 401, not 404) and before anything is recorded.
func TestReceiverRefusesDeliveriesWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			db := newTestDB(t)
			ctx := context.Background()
			if _, err := db.Exec(`INSERT INTO workflows (id, name, profile_id, is_active) VALUES ('wf-pub', 'Publish', 'p', 0)`); err != nil {
				t.Fatal(err)
			}
			ep, err := orggrant.NewStore(db).CreateEndpoint(ctx, "p", "growth", "bot", "wf-pub")
			if err != nil {
				t.Fatal(err)
			}
			rcv := &Receiver{DB: db, Store: workflow.NewSQLiteWorkflowStore(db), VerifyWindow: time.Minute,
				RootOf: func(string) string { return t.TempDir() },
				Mux:    NewMux(func(ctx context.Context, _, _, _ string, _ func([]byte)) error { <-ctx.Done(); return nil })}
			mux := http.NewServeMux()
			rcv.Register(mux)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			accounttest.Install(t, c.Mode)

			d := EndpointDelivery{OrgName: "growth", Run: "run-1", From: "hq:ceo", To: "growth:bot", Subject: "post this", Body: "hello", MessageID: "msg-1"}
			code, out := postDelivery(t, srv, ep.ID, d)

			if !c.Refused {
				if code != http.StatusAccepted || out["accepted"] != true {
					t.Fatalf("delivery = %d %v, want 202 accepted", code, out)
				}
				return
			}
			acct, _ := out["account"].(map[string]interface{})
			if code != http.StatusUnauthorized || out["error"] != "login_required" || out["login_required"] != true ||
				acct["state"] != c.State || acct["reason"] != c.Reason || len(out) != 3 {
				t.Fatalf("delivery = %d %v, want 401 login_required (%s/%s)", code, out, c.State, c.Reason)
			}
			if code, out := postDelivery(t, srv, orggrant.NewEndpointID(), d); code != http.StatusUnauthorized || out["error"] != "login_required" {
				t.Errorf("unknown endpoint = %d %v, want the same 401: a locked receiver does not say which endpoints exist", code, out)
			}
			if len(rcv.pending) != 0 || len(rcv.subs) != 0 {
				t.Errorf("a refused delivery was recorded: %d pending, %d subscriptions", len(rcv.pending), len(rcv.subs))
			}
			var n int
			_ = db.QueryRow(`SELECT COUNT(*) FROM workflow_executions`).Scan(&n)
			if n != 0 {
				t.Errorf("%d executions were created for a refused delivery", n)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/orgbridge/ -run '^TestReceiverRefusesDeliveriesWhileLocked$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `--- FAIL: TestReceiverRefusesDeliveriesWhileLocked` with the subtests `locked,_no_login` and `locked,_refused` (the delivery is accepted with 202).

- [ ] **Step 3: Implement.** Apply these two edits to `internal/orgbridge/receiver.go`:

**Edit 1** in `internal/orgbridge/receiver.go`:

```diff
+	"github.com/monoes/mono-agent/internal/account"
+	"github.com/monoes/mono-agent/internal/accountdoor"
 	"github.com/monoes/mono-agent/internal/credfile"
```

**Edit 2** in `internal/orgbridge/receiver.go`:

```diff
-// ServeHTTP handles one delivery.
+// ServeHTTP handles one delivery. A locked monoes.me account refuses it first,
+// before the endpoint is looked up or anything is recorded. A delivery accepted
+// earlier whose bus event arrives after the lock is refused where every run is,
+// in the engine.
 func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
+	if err := account.Require(req.Context()); err != nil {
+		accountdoor.WriteUnauthorized(w, err)
+		return
+	}
 	r.mu.Lock()
```

- [ ] **Step 4: Run it, then the package**

Run the command of Step 2, then `go test ./internal/orgbridge/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/orgbridge	…` both times (the package takes about 20 seconds).

- [ ] **Step 5: Commit**

```
git add internal/orgbridge/receiver.go internal/orgbridge/account_door_test.go
git commit -m "feat(orgbridge): the org receiver refuses deliveries while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation check** (after the commit). Make each change with the `Edit` tool (the line is unique in its file), run the command, expect the failure, restore the file with `git checkout -- <file>` (it returns to the commit just made), and go on. A pass would mean a door is not pinned: fix the test, not the mutation.

- **receiver**: in `internal/orgbridge/receiver.go` change `if err := account.Require(req.Context()); err != nil {` to `if err := account.Require(req.Context()); err != nil && false {`. Run `go test ./internal/orgbridge/ -run '^(TestReceiverRefusesDeliveriesWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/orgbridge/receiver.go`.

---

### Task 5: The `/v1` door, on both listeners

`Gateway.Mount` registers the four `/v1` routes behind `Gateway.auth`; the main listener mounts it through `httpapi`'s `ExtraRoutes`, the dedicated `--v1-addr` listener serves `Gateway.Handler` (`serve.go`) and never passes through the HTTP API's mux, so the gateway's own check is its only door.

**Files:**
- Create: `internal/openaiapi/account_door_test.go`
- Modify: `internal/openaiapi/auth.go`: imports (3-11), `auth` (46-54); `internal/openaiapi/errors.go`: imports (7-17), before `autoModelID` (78), `turnError` (142)

**Interfaces:**
- Consumes: `accountdoor.Message`, `accountdoor.Code`, `account.Require`, `account.IsLoginRequired`, `doortest.Modes`, `accounttest.Install`, `httpapi.NewServer` (with Task 2's gate); the package's `newHarness(t, exec, mutate...) *harness` (`helpers_test.go:39`), `h.key(t, profile, name, ctx)`, `h.serve(p, method, path, secret, body)` (`http_helpers_test.go:20`), `post(h, p, secret, body)` (`chat_test.go:22`), `decodeErrorBody` (`errors_test.go:15`), `startServe(t, h, p, tlsCfg)` and `get(t, client, url, key)` (`serve_test.go:22`, `43`), `okTurn`, `anyPolicy`, `chatBody`.
- Produces: `func errLoginRequired() *apiError` (401, type `authentication_error`, code `login_required`, message `Log in to monoes.me first: monoagentcli account login`), used by `Gateway.auth` before the key is authenticated, and by `turnError` for an `*account.LoginRequiredError` that `Exec` returns after the door let a request in. The dedicated listener's `GET /health` is unchanged: open, and silent about the account because that listener may face the network.

- [ ] **Step 1: Write the failing tests** `internal/openaiapi/account_door_test.go`:

```go
package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	keyring "github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/monomind"
)

const loginRequiredText = "Log in to monoes.me first: monoagentcli account login"

// wantEnvelopeRefusal fails unless rec is the /v1 refusal of a locked account:
// the OpenAI envelope SDKs parse, status 401, code login_required, the fixed
// message, and the request id every /v1 answer carries. It is never the flat
// body of the HTTP API, and carries no bearer challenge.
func wantEnvelopeRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body)
	}
	e := decodeErrorBody(t, rec)
	if e["type"] != "authentication_error" || e["code"] != "login_required" || e["message"] != loginRequiredText || e["param"] != nil {
		t.Errorf("error = %v, want authentication_error/login_required with the fixed message", e)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("the refusal carries no X-Request-Id")
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q: the key is not what was refused", got)
	}
	var flat map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &flat)
	if _, isFlat := flat["login_required"]; isFlat {
		t.Errorf("the body carries the HTTP API's flat login_required field: %s", rec.Body)
	}
}

// Every /v1 route, in every state, with the right key, a wrong one and none: a
// locked account is refused before the key is looked at (so the answer does not
// say whether a key is good) and before any turn is started.
func TestV1RoutesAreRefusedWhileLocked(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/models", ""},
		{http.MethodGet, "/v1/models/claude/default", ""},
		{http.MethodPost, "/v1/chat/completions", chatBody},
		{http.MethodPost, "/v1/images/generations", `{"model":"x","prompt":"a"}`},
	}
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			var turns atomic.Int32
			h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
				turns.Add(1)
				return okTurn("x")(ctx, o, on)
			})
			key := h.key(t, "default", "app", false)
			accounttest.Install(t, c.Mode)

			for _, r := range routes {
				for _, secret := range []string{key, "sk-ma-wrong", ""} {
					rec := h.serve(anyPolicy, r.method, r.path, secret, r.body)
					if c.Refused {
						wantEnvelopeRefusal(t, rec)
						continue
					}
					// Allowed: the account is no longer in the way and the old
					// rules apply: a good key is not a 401, a bad one is.
					isAuth := rec.Code == http.StatusUnauthorized
					if (secret == key) == isAuth {
						t.Errorf("%s %s with the right key=%v: %d, want the key's own verdict", r.method, r.path, secret == key, rec.Code)
					}
					if isAuth && decodeErrorBody(t, rec)["code"] != "invalid_api_key" {
						t.Errorf("%s %s: %s, want invalid_api_key", r.method, r.path, rec.Body)
					}
				}
			}
			if c.Refused && turns.Load() != 0 {
				t.Fatalf("%d turns started for a locked account", turns.Load())
			}
		})
	}
}

// The dedicated listener (--v1-addr) does not pass through the HTTP API's mux:
// the gateway's own check is all there is, here on a live socket. Its /health
// stays open and says nothing of the account, because the listener may face the
// network.
func TestTheDedicatedListenerRefusesToo(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			h := newHarness(t, okTurn("x"))
			key := h.key(t, "default", "app", false)
			accounttest.Install(t, c.Mode)
			addr, stop := startServe(t, h, anyPolicy, nil)
			defer stop()

			code, body := get(t, http.DefaultClient, "http://"+addr+"/v1/models", key)
			refused := code == http.StatusUnauthorized && strings.Contains(body, `"code":"login_required"`)
			if refused != c.Refused || (!c.Refused && code != http.StatusOK) {
				t.Errorf("GET /v1/models = %d %s, want refused = %v", code, body, c.Refused)
			}
			if code, body := get(t, http.DefaultClient, "http://"+addr+"/health", ""); code != http.StatusOK || strings.Contains(body, "account") {
				t.Errorf("GET /health = %d %s, want 200 with no account object", code, body)
			}
		})
	}
}

// On the main listener the gateway rides the HTTP API's mux. The HTTP API's gate
// stands aside for /v1 so that the gateway answers in its own envelope, and
// answers everything else in the flat body: both on one server, in one state.
func TestTheLoopbackMountRefusesInTheEnvelopeAndTheRestFlat(t *testing.T) {
	keyring.MockInit() // the legacy auth resolves its token in the vault: never the real OS keychain
	h := newHarness(t, okTurn("x"))
	key := h.key(t, "default", "app", false)
	srv, err := httpapi.NewServer(httpapi.Options{
		DB: h.db, Profile: "default", Version: "mount-test",
		ExtraRoutes: func(mux *http.ServeMux) { h.g.Mount(mux, anyPolicy) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	accounttest.Install(t, accounttest.LockedNoLogin)

	do := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec
	}
	wantEnvelopeRefusal(t, do(http.MethodGet, "/v1/models"))
	wantEnvelopeRefusal(t, do(http.MethodPost, "/v1/chat/completions"))

	flat := do(http.MethodGet, "/workflows")
	var body map[string]any
	if err := json.Unmarshal(flat.Body.Bytes(), &body); err != nil || flat.Code != http.StatusUnauthorized ||
		body["error"] != "login_required" || body["login_required"] != true {
		t.Errorf("GET /workflows = %d %s, want the HTTP API's flat login_required body", flat.Code, flat.Body)
	}
	if health := do(http.MethodGet, "/health"); health.Code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", health.Code)
	}
}

// The door let the request in and the account locked before the turn began:
// monomind.Exec refuses with its own typed error, and the caller gets the same
// 401 as the door gives, not an internal error an SDK would retry.
func TestAnExecRefusedForTheAccountIsA401NotA500(t *testing.T) {
	refusal := &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonRefused, Enforced: true}}
	h := newHarness(t, func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
		return nil, refusal
	})
	key := h.key(t, "default", "app", false)
	accounttest.Install(t, accounttest.SignedIn) // the door says yes; Exec says no

	for _, body := range []string{chatBody, strings.Replace(chatBody, `{"model"`, `{"stream":true,"model"`, 1)} {
		wantEnvelopeRefusal(t, post(h, anyPolicy, key, body))
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/openaiapi/ -run '^(TestV1RoutesAreRefusedWhileLocked|TestTheDedicatedListenerRefusesToo|TestTheLoopbackMountRefusesInTheEnvelopeAndTheRestFlat|TestAnExecRefusedForTheAccountIsA401NotA500)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `--- FAIL` for all four (the locked rows get `status = 200, want 401`; the Exec case gets a 500 `internal_error`).

- [ ] **Step 3: Implement.** Apply these edits to `internal/openaiapi/auth.go`, then to `internal/openaiapi/errors.go`:

**Edit 1** in `internal/openaiapi/auth.go`:

```diff
 	"strings"
 
+	"github.com/monoes/mono-agent/internal/account"
 	"github.com/monoes/mono-agent/internal/apikeys"
 )
```

**Edit 2** in `internal/openaiapi/auth.go`:

```diff
 // auth runs next only for a request that carries a valid, unrevoked key. It
 // never consults the vault or the legacy HTTP API token: these are separate
 // credentials for separate routes. Every response, an error included, carries
-// the request's X-Request-Id.
+// the request's X-Request-Id. A locked monoes.me account is refused first, so
+// that every caller gets the same answer and none learns whether a key is good;
+// both listeners mount through Mount, so this is the door of both.
```

**Edit 3** in `internal/openaiapi/auth.go`:

```diff
 		w.Header().Set("X-Request-Id", id)
+		if err := account.Require(r.Context()); err != nil {
+			writeError(w, errLoginRequired())
+			return
+		}
 		key, err
```

**Edit 1** in `internal/openaiapi/errors.go`:

```diff
 	"unicode"
 
+	"github.com/monoes/mono-agent/internal/account"
+	"github.com/monoes/mono-agent/internal/accountdoor"
 	"github.com/monoes/mono-agent/internal/monomind"
 )
```

**Edit 2** in `internal/openaiapi/errors.go`:

```diff
+// errLoginRequired is the answer while the machine holds no valid monoes.me
+// login: in the envelope an OpenAI SDK parses, and a 401 because those SDKs do
+// not retry one (they retry a 5xx).
+func errLoginRequired() *apiError {
+	return &apiError{Status: http.StatusUnauthorized, Type: "authentication_error", Code: accountdoor.Code, Message: accountdoor.Message}
+}
+
 // autoModelID is the model that lets Jev pick a runtime and model for a request.
```

**Edit 3** in `internal/openaiapi/errors.go`:

```diff
 		switch {
+		case account.IsLoginRequired(execErr):
+			// The account locked after the door let the request in: Exec refused.
+			return errLoginRequired()
 		case errors.Is(execErr, errShuttingDown):
```

- [ ] **Step 4: Run the new tests, then the package**

Run the command of Step 2, then `go test ./internal/openaiapi/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/openaiapi	…` both times (the package takes about 30 seconds).

- [ ] **Step 5: Commit**

```
git add internal/openaiapi/auth.go internal/openaiapi/errors.go internal/openaiapi/account_door_test.go
git commit -m "feat(openaiapi): /v1 refuses in the OpenAI envelope while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation checks** (after the commit). Make each change with the `Edit` tool (the line is unique in its file), run the command, expect the failure, restore the file with `git checkout -- <file>` (it returns to the commit just made), and go on. A pass would mean a door is not pinned: fix the test, not the mutation. `http-v1` changes Task 2's gate, which is only pinned once a gateway is mounted behind it.

- **v1-auth**: in `internal/openaiapi/auth.go` change `if err := account.Require(r.Context()); err != nil {` to `if err := account.Require(r.Context()); err != nil && false {`. Run `go test ./internal/openaiapi/ -run '^(TestV1RoutesAreRefusedWhileLocked|TestTheDedicatedListenerRefusesToo)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/openaiapi/auth.go`.
- **v1-exec**: in `internal/openaiapi/errors.go` change `case account.IsLoginRequired(execErr):` to `case false && account.IsLoginRequired(execErr):`. Run `go test ./internal/openaiapi/ -run '^(TestAnExecRefusedForTheAccountIsA401NotA500)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/openaiapi/errors.go`.
- **http-v1**: in `internal/httpapi/server.go` change `strings.HasPrefix(p, "/v1/")` to `strings.HasPrefix(p, "/v1x/")`. Run `go test ./internal/openaiapi/ -run '^(TestTheLoopbackMountRefusesInTheEnvelopeAndTheRestFlat)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/httpapi/server.go`.

---

### Task 6: The webhook server door

**Files:**
- Create: `internal/workflow/webhook_account.go`, `internal/workflow/webhook_account_test.go`
- Modify: `internal/workflow/webhook_server.go`: imports (18), `WebhookServer` fields (72), `ServeHTTP` (271-275). It stays under 500 lines (469 after the edits): the helpers live in the new file.

**Interfaces:**
- Consumes: `accountdoor.Code`, `account.Require`, `doortest.Modes`, `accounttest.Install`; `NewWebhookServer(addr string, logger zerolog.Logger) *WebhookServer`, `Register(*WebhookRegistration) error`, `writeJSONError(w, code, msg)`.
- Produces: `func (s *WebhookServer) refuseWhileLocked(w http.ResponseWriter, r *http.Request) bool` (503, `Retry-After: 60`, `{"error":"login_required"}`; the first statement of `ServeHTTP`, so no path lookup, body read, credential check or `admitTrace` ledger write happens), `func (s *WebhookServer) noteLocked()` (one `Warn` line a minute), `webhookRetryAfter = "60"`, `lockedLogEvery = time.Minute`, the field `lockedLogAt atomic.Int64`. A refused request runs nothing, so spec §6.2's "a webhook trigger that fires while locked is dropped with one log line a minute" holds for HTTP webhooks here; B3a's engine check covers every other trigger.

- [ ] **Step 1: Write the failing tests** `internal/workflow/webhook_account_test.go`:

```go
package workflow

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
)

// A locked account is refused with a 503 and no run, whatever the method or path
// (an unknown webhook looks like a known one), whether the credential is right,
// wrong or missing, for the preflight too, and before the body is read (the last
// request is over the 1 MiB limit an allowed server answers with a 413).
func TestWebhookServerRefusesRequestsWhileLocked(t *testing.T) {
	big := strings.Repeat("x", 2<<20)
	requests := []struct{ method, path, secret, body string }{
		{http.MethodPost, "/webhook/hook", "s3cr3t", `{"a":1}`}, // the one valid call
		{http.MethodPost, "/webhook/hook", "", `{"a":1}`},
		{http.MethodPost, "/webhook/hook", "wrong", `{"a":1}`},
		{http.MethodGet, "/webhook/hook", "s3cr3t", ""},
		{http.MethodPost, "/webhook/nope", "s3cr3t", `{"a":1}`},
		{http.MethodOptions, "/webhook/hook", "", ""},
		{http.MethodPost, "/elsewhere", "", ""},
		{http.MethodPost, "/webhook/hook", "s3cr3t", big},
	}
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			s := NewWebhookServer(":0", zerolog.Nop())
			fired := 0
			if err := s.Register(&WebhookRegistration{
				Path: "hook", Method: "POST", AuthHeader: "X-Webhook-Secret", AuthToken: "s3cr3t",
				TriggerFn: func([]Item) { fired++ },
			}); err != nil {
				t.Fatal(err)
			}
			accounttest.Install(t, c.Mode)

			for i, r := range requests {
				req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
				if r.secret != "" {
					req.Header.Set("X-Webhook-Secret", r.secret)
				}
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, req)
				switch {
				case c.Refused:
					if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "60" ||
						strings.TrimSpace(rec.Body.String()) != `{"error":"login_required"}` {
						t.Errorf("%s %s (secret %q) = %d %q Retry-After %q, want 503 {\"error\":\"login_required\"} with Retry-After 60",
							r.method, r.path, r.secret, rec.Code, rec.Body, rec.Header().Get("Retry-After"))
					}
				case i == 0 && rec.Code != http.StatusOK:
					t.Errorf("the valid webhook call = %d %s, want 200", rec.Code, rec.Body)
				}
			}
			want := 1 // only the first request is a valid call: it runs when the account allows it
			if c.Refused {
				want = 0
			}
			if fired != want {
				t.Fatalf("%d runs started, want %d", fired, want)
			}
		})
	}
}

// The server says once a minute why its callers are getting 503s.
func TestWebhookServerLogsALockedAccountOncePerMinute(t *testing.T) {
	var logs bytes.Buffer
	s := NewWebhookServer(":0", zerolog.New(&logs))
	accounttest.Install(t, accounttest.LockedNoLogin)
	refuse := func() {
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhook/hook", nil))
	}

	for i := 0; i < 5; i++ {
		refuse()
	}
	if n := strings.Count(logs.String(), "no valid monoes.me login"); n != 1 {
		t.Fatalf("logged %d times for 5 refusals, want once:\n%s", n, logs.String())
	}
	s.lockedLogAt.Store(s.lockedLogAt.Load() - int64(2*lockedLogEvery)) // a minute and more has passed
	refuse()
	if n := strings.Count(logs.String(), "no valid monoes.me login"); n != 2 {
		t.Fatalf("logged %d times after a minute passed, want 2", n)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/workflow/ -run '^(TestWebhookServerRefusesRequestsWhileLocked|TestWebhookServerLogsALockedAccountOncePerMinute)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: a build failure: `s.lockedLogAt undefined` and `undefined: lockedLogEvery`.

- [ ] **Step 3: Implement.** Create `internal/workflow/webhook_account.go`:

```go
package workflow

import (
	"net/http"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/accountdoor"
)

const (
	// webhookRetryAfter is the Retry-After (seconds) of a request refused for a
	// locked account: callers that retry on a 503 come back after a minute.
	webhookRetryAfter = "60"
	// lockedLogEvery is how often the server logs that it is refusing requests.
	lockedLogEvery = time.Minute
)

// refuseWhileLocked answers a request with 503, Retry-After and
// {"error":"login_required"} while the machine holds no valid monoes.me login,
// and reports whether it did. Whatever the method or path, the preflight too: an
// unknown webhook looks like a known one.
func (s *WebhookServer) refuseWhileLocked(w http.ResponseWriter, r *http.Request) bool {
	if account.Require(r.Context()) == nil {
		return false
	}
	s.noteLocked()
	w.Header().Set("Retry-After", webhookRetryAfter)
	writeJSONError(w, http.StatusServiceUnavailable, accountdoor.Code)
	return true
}

// noteLocked logs, at most once a minute, that requests are being refused:
// without it the only sign of a locked daemon at the webhook port is the
// callers' 503s.
func (s *WebhookServer) noteLocked() {
	now := time.Now().UnixNano()
	last := s.lockedLogAt.Load()
	if last != 0 && now-last < int64(lockedLogEvery) {
		return
	}
	if s.lockedLogAt.CompareAndSwap(last, now) {
		s.logger.Warn().Msg("webhook refused: no valid monoes.me login (run: monoagentcli account login)")
	}
}
```

Then apply these three edits to `internal/workflow/webhook_server.go`:

**Edit 1** in `internal/workflow/webhook_server.go`:

```diff
 	"sync"
+	"sync/atomic"
 	"syscall"
```

**Edit 2** in `internal/workflow/webhook_server.go`:

```diff
 	keyErrorOnce sync.Once
+	// lockedLogAt is when a request refused for a locked account was last
+	// logged, in Unix nanoseconds (see noteLocked).
+	lockedLogAt atomic.Int64
 }
```

**Edit 3** in `internal/workflow/webhook_server.go`:

```diff
 // Returns 404 if path not found, 405 if method doesn't match, 200 on success.
+//
+// A locked monoes.me account refuses every request first (refuseWhileLocked),
+// before the body is read and before admitTrace writes to the org ledger.
 func (s *WebhookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
+	if s.refuseWhileLocked(w, r) {
+		return
+	}
 	// Parse path: must be /webhook/{path}
```

- [ ] **Step 4: Run the new tests with `-race`, then the package**

Run the command of Step 2 with `-race` after `-count=1`, then `go test ./internal/workflow/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/workflow	…` both times.

- [ ] **Step 5: Commit**

```
git add internal/workflow/webhook_account.go internal/workflow/webhook_server.go internal/workflow/webhook_account_test.go
git commit -m "feat(workflow): the webhook server answers 503 while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation check** (after the commit). Make each change with the `Edit` tool (the line is unique in its file), run the command, expect the failure, restore the file with `git checkout -- <file>` (it returns to the commit just made), and go on. A pass would mean a door is not pinned: fix the test, not the mutation.

- **webhook**: in `internal/workflow/webhook_account.go` change `if account.Require(r.Context()) == nil {` to `if account.Require(r.Context()) == nil || true {`. Run `go test ./internal/workflow/ -run '^(TestWebhookServerRefusesRequestsWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/workflow/webhook_account.go`.

---

### Task 7: The extension bridge door

The bridge has three entries that do work for a caller and `ping`, which tells the browser why nothing else works. A locked account does not close it: the extension still connects and pairs, so that `ping` can answer.

| Entry | Locked answer | Where |
|---|---|---|
| request frame (`kind:"request"`) but `ping` | reply `{"kind":"reply","id":…,"error":"Log in to monoes.me first: monoagentcli account login","code":"account_locked"}` (no `ok` key: `Reply.OK` is `omitempty`, `request.go:122`), before the handler lookup | `serveRequest` |
| `ping` | answers; its data gains `"account":{"state","reason","valid_until","enforced"}` | `registerBuiltinHandlers` |
| `POST /monoagent/relay` | 503 `{"id":"","success":false,"error":<the sentence>}`, after the token check | `handleRelay` |
| `/monoagent/cdp` | upgrade refused with a plain 503; on an open socket each command gets `{"id","success":false,"type","error":<the sentence>}` | `handleCdpSocket` |

The relay refusal is a 503 and not a 401: `RemoteSender` reads a 401 or 403 as `ErrRelayUnauthorized` ("pairing token mismatch, re-pair", `remote.go:119`), while a non-2xx reply that carries a `Response` comes back as the extension's own error (`decodeRelayResponse`, `remote.go:140`).

Open on purpose, pinned by `TestTheOpenEndpointsOfALockedBridge`: the handshake `/monoagent`, `/monoagent/health` (other processes use it to find this bridge and must keep getting a 200, or they would start competing bridges), `/monoagent/auth`, `/monoagent/pair`, `/monoagent/pair/exchange` and `/monoagent/browsers` (names browsers only to a caller with the token). Binding pushes, activity-recording frames (`serveRecording`) and the pages the extension pushes unasked are data, not work, and they stay accepted, pinned by `TestPushedCapturesAndRecordingsAreAcceptedWhileLocked`: the extension counts a successful socket send as delivery and drops its queued copy (`chrome-extension/capture_bridge.js:111-117`, CLIP-08), and installed extensions cannot be updated centrally, so refusing a push would lose a capture without a trace. This is a deliberate deviation from "the bridge refuses everything but `ping`" (spec §6.3), approved by the lead for the owner; refusing pushes later needs an extension release first. What runs on them afterwards (`record.analyze` is a request, a summary goes through `monomind.Exec`) is refused where it runs.

**Files:**
- Create: `internal/extension/account_door.go`, `internal/extension/account_door_test.go`
- Modify: `internal/extension/request.go`: reply codes (84-90), `registerBuiltinHandlers` (246-250), `serveRequest` (284-286); `internal/extension/server.go`: `handleRelay` (710-714); `internal/extension/cdp.go`: imports (13), `handleCdpSocket` (318-323) and its command loop (375-382). `request.go` stays at 495 lines: the explanations live in the new file.

**Interfaces:**
- Consumes: `accountdoor.Message`, `accountdoor.Summary`, `accountdoor.SummaryOf`, `account.Require`, `account.CurrentStatus`, `doortest.Modes`, `accounttest.Install`; the package's `startCaptureServer(t) (*Server, *fakeExtension, string)` (`capture_test.go:34`), `(*fakeExtension).ask(id, method, params)` and `.settled() *Reply` (`request_test.go:25`, `53`), `startCdpRig(t)`, `(*cdpTestRig).dialCdp`, `.token`, `.answerExtension`, `readJSON` (`cdp_test.go:54`, `96`, `114`, `124`, `145`), `sampleMeta()`, `b64Artifact(name, body)`, `(*fakeExtension).sendFinal` (`capture_test.go:173`, `165`, `140`), `.nextAck(id)` and `recFrame(id, op, extra)` (`recording_test.go:22`, `38`), `(*Server).OnCapture`, `CurrentToken()` (`token.go:100`).
- Produces: `const CodeAccountLocked = "account_locked"`; `func accountRefuses() bool`, `func accountSummary() accountdoor.Summary`, `func writeRelayLocked(w http.ResponseWriter)`, `var errAccountLocked error`. The wire shapes above are what B4's side panel reads (see the contract change request at the end).

- [ ] **Step 1: Write the failing tests** `internal/extension/account_door_test.go`:

```go
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/capture"
)

const loginRequiredText = "Log in to monoes.me first: monoagentcli account login"

// settledByID reads n settling replies and indexes them by request id: the
// handlers run on goroutines, so the replies do not come back in order.
func (f *fakeExtension) settledByID(n int) map[string]*Reply {
	f.t.Helper()
	out := map[string]*Reply{}
	for i := 0; i < n; i++ {
		r := f.settled()
		out[r.ID] = r
	}
	return out
}

// wantLocked fails unless r is the refusal of a locked bridge.
func wantLocked(t *testing.T, what string, r *Reply) {
	t.Helper()
	if r.OK || r.Code != CodeAccountLocked || r.Error != loginRequiredText {
		t.Errorf("%s = ok %v code %q error %q, want the account_locked refusal", what, r.OK, r.Code, r.Error)
	}
}

// Every extension request but ping is refused while locked, with the code
// account_locked and the sentence to act on, before its handler is looked up (an
// unknown method gets the same answer). ping answers, and says why; it is matched
// exactly, so another spelling of ping is just another method.
func TestRequestsAreRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			srv, ext, _ := startCaptureServer(t)
			var ran atomic.Int32
			srv.HandleRequest("echo", func(context.Context, *Request, ProgressFunc) (any, error) {
				ran.Add(1)
				return "hi", nil
			})
			accounttest.Install(t, c.Mode)

			ids := map[string]string{"echo": "r-echo", MethodPing: "r-ping", "nope.unknown": "r-unknown", "Ping": "r-case", "ping ": "r-space"}
			if c.Refused { // the built-ins shell out to monomind when they run: sent only where they are refused at once
				ids[MethodDocLookup], ids["record.list"] = "r-lookup", "r-record"
			}
			for method, id := range ids {
				ext.ask(id, method, nil)
			}
			replies := ext.settledByID(len(ids))

			data, _ := replies["r-ping"].Data.(map[string]any)
			acct, _ := data["account"].(map[string]any)
			b, _ := json.Marshal(data)
			if !replies["r-ping"].OK || data["pong"] != true || acct["state"] == nil || strings.Contains(string(b), "email") || strings.Contains(string(b), `"user"`) {
				t.Fatalf("ping = %s, want pong and an account that does not name the user", b)
			}
			if c.State != "" && (acct["state"] != c.State || acct["reason"] != c.Reason) {
				t.Errorf("ping account = %v, want %s/%s", acct, c.State, c.Reason)
			}
			if acct["enforced"] != c.Enforced {
				t.Errorf("ping account = %v, want enforced = %v", acct, c.Enforced)
			}
			for method, id := range ids {
				switch {
				case method == MethodPing:
				case c.Refused:
					wantLocked(t, method, replies[id])
				case replies[id].Code == CodeAccountLocked:
					t.Errorf("%q was refused for the account in state %s", method, c.Name)
				}
			}
			want := int32(1)
			if c.Refused {
				want = 0
			}
			if ran.Load() != want {
				t.Errorf("the handler ran %d times, want %d", ran.Load(), want)
			}
		})
	}
}

// A person who signs in expects the open side panel to work at once: the same
// socket, no reconnect.
func TestABridgeSocketRecoversWhenTheAccountDoes(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	srv.HandleRequest("echo", func(context.Context, *Request, ProgressFunc) (any, error) { return "hi", nil })

	t.Run("locked", func(t *testing.T) {
		accounttest.Install(t, accounttest.LockedRefused)
		ext.ask("r1", "echo", nil)
		wantLocked(t, "echo while locked", ext.settled())
	})
	t.Run("the sign-in lands", func(t *testing.T) {
		accounttest.Install(t, accounttest.SignedIn)
		ext.ask("r2", "echo", nil)
		if r := ext.settled(); !r.OK || r.Data != "hi" {
			t.Fatalf("echo after the sign-in = %+v, want ok", r)
		}
	})
}

// bridgeAddr is where a started server listens, and the token its clients use.
func bridgeAddr(t *testing.T, srv *Server) (addr, token string) {
	t.Helper()
	addr, ok := srv.Addr()
	if !ok {
		t.Fatal("the server has no address")
	}
	token, err := CurrentToken()
	if err != nil {
		t.Fatal(err)
	}
	return addr, token
}

// relayCreateTab relays a command no extension answers: the short timeout ends
// an allowed one with the relay's own error.
func relayCreateTab(t *testing.T, addr, token string) (int, Response) {
	t.Helper()
	cmd, _ := json.Marshal(&Command{ID: "c1", Type: CmdCreateTab})
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/monoagent/relay?timeout_ms=200", bytes.NewReader(cmd))
	req.Header.Set(tokenHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// The relay lets another process drive the browser through this one: refused
// while locked, after the token (a caller without it learns nothing of the
// account). A 503, not a 401: the relay client reads a 401 as a pairing-token
// mismatch and would tell the user to re-pair.
func TestRelayIsRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			srv, _, _ := startCaptureServer(t)
			addr, tok := bridgeAddr(t, srv)
			accounttest.Install(t, c.Mode)

			code, got := relayCreateTab(t, addr, tok)
			if refused := code == http.StatusServiceUnavailable && got.Error == loginRequiredText; refused != c.Refused {
				t.Errorf("relay = %d %+v, want refused = %v (503 with the login-required sentence)", code, got, c.Refused)
			}
			if code, _ := relayCreateTab(t, addr, "not-the-token"); code != http.StatusUnauthorized {
				t.Errorf("relay without the token = %d, want 401 and nothing about the account", code)
			}
		})
	}
}

// The CDP socket reaches the signed-in tabs through chrome.debugger. A locked
// bridge refuses the upgrade with a plain 503; a socket opened before the lock is
// refused command by command (the extension never sees them) and works again, on
// the same socket, once a login lands.
func TestCDPIsRefusedWhileLocked(t *testing.T) {
	rig := startCdpRig(t)
	envelope := func(id string) map[string]any {
		return map[string]any{"id": id, "type": CmdCdp, "tabId": 42, "params": map[string]any{"method": "Page.navigate"}}
	}
	var client *websocket.Conn

	t.Run("a locked bridge refuses the upgrade", func(st *testing.T) {
		accounttest.Install(st, accounttest.LockedNoLogin)
		h := http.Header{}
		h.Set(tokenHeader, rig.token(st))
		conn, resp, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(rig.port)+"/monoagent/cdp", h)
		if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
			st.Fatalf("cdp upgrade: err %v resp %v, want 503", err, resp)
		}
		if conn != nil {
			conn.Close()
		}
	})
	t.Run("signed in: the socket opens", func(st *testing.T) {
		accounttest.Install(st, accounttest.SignedIn)
		client = rig.dialCdp(t, rig.token(t)) // t, not st: the socket outlives this subtest
	})
	t.Run("the account locks under the open socket", func(st *testing.T) {
		accounttest.Install(st, accounttest.LockedRefused)
		_ = client.WriteJSON(envelope("cdp-1"))
		var refused struct{ ID, Error string }
		readJSON(st, client, &refused)
		if refused.ID != "cdp-1" || refused.Error != loginRequiredText {
			st.Fatalf("command on an open socket = %+v, want the login-required refusal", refused)
		}
		if n := rig.srv.inFlightCommands(); n != 0 {
			st.Fatalf("%d commands were sent to the extension for a refused one", n)
		}
	})
	t.Run("the sign-in lands", func(st *testing.T) {
		accounttest.Install(st, accounttest.SignedIn)
		_ = client.WriteJSON(envelope("cdp-2"))
		if cmd := rig.answerExtension(st, map[string]any{"ok": true}, ""); cmd.Type != CmdCdp {
			st.Fatalf("the extension saw %q after the sign-in, want the cdp command", cmd.Type)
		}
		var ok struct {
			ID      string
			Success bool
		}
		readJSON(st, client, &ok)
		if ok.ID != "cdp-2" || !ok.Success {
			st.Fatalf("after the sign-in = %+v, want the command answered", ok)
		}
	})
}

// The extension pushes a capture it queued, and the frames of a recording, with
// no answer, and drops its queued copy once the socket takes the frame
// (chrome-extension/capture_bridge.js:111-117): refusing a push would lose it
// without a trace, and installed extensions cannot be updated centrally. So a
// locked bridge keeps accepting them, and what runs on them afterwards is refused
// where it runs. Refusing pushes later needs an extension release first.
func TestPushedCapturesAndRecordingsAreAcceptedWhileLocked(t *testing.T) {
	srv, ext, inbox := startCaptureServer(t)
	landed := make(chan struct{}, 1)
	srv.OnCapture(func(_ *capture.Result, err error) {
		if err == nil {
			landed <- struct{}{}
		}
	})
	accounttest.Install(t, accounttest.LockedNoLogin)

	ext.sendFinal("ext-pushed-1", sampleMeta(), b64Artifact(capture.ArtifactReadable, "# A Post"))
	select {
	case <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("a capture pushed to a locked bridge was dropped")
	}
	if entries, err := capture.List(inbox); err != nil || len(entries) != 1 {
		t.Fatalf("inbox = %+v, %v, want the pushed capture", entries, err)
	}

	ext.send(recFrame("rec-1", "start", map[string]any{"url": "https://example.com/x", "title": "X", "goal": "g", "tabId": 3}))
	if ack := ext.nextAck("rec-1"); !ack.Success {
		t.Fatalf("a recording frame sent to a locked bridge was refused: %s", ack.Error)
	}
}

// The endpoints that stay open while locked, pinned: a new one is added here on
// purpose. The handshake is open so that ping can reach the browser, the probes
// because other processes use them to find this bridge.
func TestTheOpenEndpointsOfALockedBridge(t *testing.T) {
	srv, _, _ := startCaptureServer(t)
	addr, tok := bridgeAddr(t, srv)
	accounttest.Install(t, accounttest.LockedNoLogin)

	for path, want := range map[string]int{
		"/monoagent/health": http.StatusOK, "/monoagent/auth": http.StatusNoContent, "/monoagent/browsers": http.StatusOK,
		"/monoagent/pair": http.StatusBadRequest, "/monoagent/pair/exchange?n=nope": http.StatusNotFound, // the pages' own checks
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
		req.Header.Set(tokenHeader, tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d (open while locked)", path, resp.StatusCode, want)
		}
	}
	if !srv.IsConnected() {
		t.Error("the extension is not connected to a locked bridge: the socket must stay open for ping")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/extension/ -run '^(TestRequestsAreRefusedWhileLocked|TestABridgeSocketRecoversWhenTheAccountDoes|TestRelayIsRefusedWhileLocked|TestCDPIsRefusedWhileLocked|TestPushedCapturesAndRecordingsAreAcceptedWhileLocked|TestTheOpenEndpointsOfALockedBridge)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: a build failure: `account_door_test.go:…: undefined: CodeAccountLocked`.

- [ ] **Step 3: Implement.** Create `internal/extension/account_door.go`:

```go
package extension

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/accountdoor"
)

// The bridge's door for the monoes.me account. A locked account does not close
// the bridge: the extension still connects and pairs, so that ping can tell the
// side panel why nothing else works. What is refused is what does work for a
// caller: extension requests (serveRequest), commands relayed in from other
// processes (handleRelay) and the CDP socket (handleCdpSocket).
//
// Left open on purpose: the handshake and pairing, the probes (/monoagent/health
// also tells other processes where this bridge is and must keep answering 200)
// and the listing of browsers. Binding pushes, activity-recording frames and
// pages the extension flushes unasked are data, not work: what runs on them
// afterwards is refused where it runs (record.analyze is a request, a summary
// goes through monomind.Exec).

// errAccountLocked is the error of every reply the bridge refuses.
var errAccountLocked = errors.New(accountdoor.Message)

// accountRefuses reports whether the bridge refuses work now. It reads the
// process guard's cached verdict (no I/O), so it is cheap on every frame.
func accountRefuses() bool { return account.Require(context.Background()) != nil }

// accountSummary is what ping reports of the account: the state, why and, for a
// session, until when, never who.
func accountSummary() accountdoor.Summary {
	return accountdoor.SummaryOf(account.CurrentStatus())
}

// writeRelayLocked refuses a relayed command with a 503: the relay client reads
// a 401 or 403 as a pairing-token mismatch (ErrRelayUnauthorized) and would tell
// the user to re-pair, while a non-2xx reply that carries a Response comes back
// as the extension's own error, with the sentence to act on.
func writeRelayLocked(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(&Response{Error: accountdoor.Message})
}
```

Then apply these edits, `request.go` first, then `server.go` and `cdp.go`:

**Edit 1** in `internal/extension/request.go`:

```diff
 	CodeInternal      = "internal"
+	CodeAccountLocked = "account_locked" // no valid monoes.me login: see account_door.go
 )
```

**Edit 2** in `internal/extension/request.go`:

```diff
 			"methods": s.RequestMethods(),
+			"account": accountSummary(),
 		}, nil
```

**Edit 3** in `internal/extension/request.go`:

```diff
 	req.Origin = c.info()
 
+	if req.Method != MethodPing && accountRefuses() { // before the lookup: see account_door.go
+		s.replyError(c, req.ID, CodeAccountLocked, errAccountLocked)
+		return
+	}
+
 	handler, ok := s.handlerFor(req.Method)
```

**Edit 1** in `internal/extension/server.go`:

```diff
 		http.Error(w, "unauthorized", http.StatusUnauthorized)
 		return
 	}
+	// After the token, so a caller without it learns nothing of the account.
+	if accountRefuses() {
+		writeRelayLocked(w)
+		return
+	}
 	var cmd Command
```

**Edit 1** in `internal/extension/cdp.go`:

```diff
 	"github.com/gorilla/websocket"
+
+	"github.com/monoes/mono-agent/internal/accountdoor"
 )
```

**Edit 2** in `internal/extension/cdp.go`:

```diff
 		http.Error(w, "unauthorized", http.StatusUnauthorized)
 		return
 	}
+	// Before the upgrade: a refused client reads a plain 503, not a socket that
+	// closes for no stated reason.
+	if accountRefuses() { // the upgrade
+		http.Error(w, accountdoor.Message, http.StatusServiceUnavailable)
+		return
+	}
 	target := targetFromQuery(r.URL.Query())
```

**Edit 3** in `internal/extension/cdp.go`:

```diff
 				Error: fmt.Sprintf("the CDP relay carries %s/%s/%s only, not %q", CmdCdp, CmdCdpAttach, CmdCdpDetach, cmd.Type),
 			})
 			continue
 		}
+		if accountRefuses() { // each command
+			// A socket opened before the lock is refused command by command, not
+			// cut: what is in flight finishes, and it works again once a login lands.
+			_ = client.write(&Response{ID: cmd.ID, Type: cmd.Type, Error: accountdoor.Message})
+			continue
+		}
 
```

- [ ] **Step 4: Run the new tests with `-race`, then the package**

Run the command of Step 2 with `-race` after `-count=1`, then `go test ./internal/extension/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/extension	…` both times (the package takes about 30 seconds).

- [ ] **Step 5: Commit**

```
git add internal/extension/account_door.go internal/extension/request.go internal/extension/server.go internal/extension/cdp.go internal/extension/account_door_test.go
git commit -m "feat(extension): the bridge refuses requests, relay and CDP while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation checks** (after the commit). Make each change with the `Edit` tool (the line is unique in its file), run the command, expect the failure, restore the file with `git checkout -- <file>` (it returns to the commit just made), and go on. A pass would mean a door is not pinned: fix the test, not the mutation. `ext-ping` swaps the exact `ping` match for a case-folding, trimming one and must be caught: another spelling of `ping` is just another method. `ext-push` makes a locked bridge drop a pushed capture, which must be caught too.

- **ext-request**: in `internal/extension/request.go` change `if req.Method != MethodPing && accountRefuses() {` to `if req.Method != MethodPing && accountRefuses() && false {`. Run `go test ./internal/extension/ -run '^(TestRequestsAreRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/request.go`.
- **ext-ping**: in `internal/extension/request.go` change `if req.Method != MethodPing && accountRefuses() {` to `if !strings.EqualFold(strings.TrimSpace(req.Method), MethodPing) && accountRefuses() {`. Run `go test ./internal/extension/ -run '^(TestRequestsAreRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/request.go`.
- **ext-ping-state**: in `internal/extension/request.go` change `"account": accountSummary(),` to `"account_": accountSummary(),`. Run `go test ./internal/extension/ -run '^(TestRequestsAreRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/request.go`.
- **ext-relay**: in `internal/extension/server.go` change `if accountRefuses() {` to `if accountRefuses() && false {`. Run `go test ./internal/extension/ -run '^(TestRelayIsRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/server.go`.
- **ext-cdp**: in `internal/extension/cdp.go` change `if accountRefuses() { // the upgrade` to `if accountRefuses() && false { // the upgrade`. Run `go test ./internal/extension/ -run '^(TestCDPIsRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/cdp.go`.
- **ext-cdp-call**: in `internal/extension/cdp.go` change `if accountRefuses() { // each command` to `if accountRefuses() && false { // each command`. Run `go test ./internal/extension/ -run '^(TestCDPIsRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/cdp.go`.
- **ext-push**: in `internal/extension/capture.go` change `case isCaptureResponse(resp):` to `case isCaptureResponse(resp) && !accountRefuses():`. Run `go test ./internal/extension/ -run '^(TestPushedCapturesAndRecordingsAreAcceptedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/extension/capture.go`.

---

### Task 8: The MCP door

`handleToolsCall` is the one place a `tools/call` runs, in every mode (read-only, `--allow-mutations`, `--api-only` and `--grant`, which monomind spawns per org role). `initialize`, `ping` and `tools/list` do no work and still answer.

**Files:**
- Create: `internal/mcp/account_door_test.go`
- Modify: `internal/mcp/server.go`: imports (19), `handleToolsCall` (374)

**Interfaces:**
- Consumes: `accountdoor.Message`, `account.Require`, `doortest.Modes`, `accounttest.Install`; the package's `newTestServer(t) *Server` and `newTestServerAllowMutations(t, allow) *Server` (`server_test.go:19`, `46`), `serveLines(t, s, lines...)`, `request(id, method, params)`, `callToolReq(id, name, args)`, `toolText(t, resp) (string, bool)` (`:73`, `:94`, `:104`, `:108`), `newGrantFixture(t, orggrant.Tool) *grantFixture` (`grant_test.go:28`), `codeDaemonRequired` (`grant.go:33`).
- Produces: `tools/call` returns `{"content":[{"type":"text","text":"Log in to monoes.me first: monoagentcli account login"}],"isError":true}` before its params are parsed or a tool is named, so a malformed call and an unknown tool get the same answer.

- [ ] **Step 1: Write the failing tests** `internal/mcp/account_door_test.go`:

```go
package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/orggrant"
)

const loginRequiredText = "Log in to monoes.me first: monoagentcli account login"

// byID indexes responses by JSON-RPC id: requests run on goroutines, so the
// order of the lines is not the order of the requests.
func byID(t *testing.T, resps []map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	t.Helper()
	out := map[string]map[string]json.RawMessage{}
	for _, r := range resps {
		out[string(r["id"])] = r
	}
	return out
}

// The handshake and the tool list still answer while locked, so a client can
// connect and tell its user what to do; every tools/call is refused with a tool
// error (isError) whatever it names, malformed or not, mutating or not.
func TestToolsCallIsRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			s := newTestServerAllowMutations(t, true)
			accounttest.Install(t, c.Mode)

			lines := []string{
				request(1, "initialize", map[string]interface{}{"protocolVersion": "2024-11-05"}),
				request(2, "tools/list", nil),
				callToolReq(3, "workflow_list", map[string]interface{}{}),
			}
			if c.Refused { // allowed, these would start an engine (it binds the webhook port) or fail to parse: sent only where they are refused at once
				lines = append(lines,
					callToolReq(4, "workflow_run", map[string]interface{}{"id": "x"}), // mutating
					callToolReq(5, "no_such_tool", map[string]interface{}{}),
					request(6, "tools/call", "not an object")) // malformed params
			}
			resps := byID(t, serveLines(t, s, lines...))
			if len(resps) != len(lines) {
				t.Fatalf("got %d responses, want %d", len(resps), len(lines))
			}

			var list struct {
				Tools []struct{} `json:"tools"`
			}
			if _, ok := resps["1"]["result"]; !ok || json.Unmarshal(resps["2"]["result"], &list) != nil || len(list.Tools) < 8 {
				t.Errorf("the handshake and the full tool list must answer in every state: %s %s", resps["1"]["error"], resps["2"]["error"])
			}
			for id := range resps {
				if id == "1" || id == "2" {
					continue
				}
				text, isErr := toolText(t, resps[id])
				if _, isProtocolError := resps[id]["error"]; isProtocolError {
					t.Errorf("call %s: a protocol error %s, want the tool error of a locked account", id, resps[id]["error"])
				} else if refused := isErr && text == loginRequiredText; refused != c.Refused {
					t.Errorf("call %s = %q (isError %v), want refused = %v", id, text, isErr, c.Refused)
				}
			}
		})
	}
}

// Grant mode (monomind's role tool provider) is the same door: it lists the
// granted tools, and a call is refused before any run is created.
func TestGrantModeToolsCallIsRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			f := newGrantFixture(t, orggrant.Tool{Wait: true})
			accounttest.Install(t, c.Mode)

			resps := byID(t, serveLines(t, f.server,
				request(1, "tools/list", map[string]interface{}{}),
				callToolReq(2, "automation_publish", map[string]interface{}{"text": "hi"}),
			))
			var list struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(resps["1"]["result"], &list); err != nil || len(list.Tools) != 3 {
				t.Errorf("grant tools/list: %d tools, err %v, want 3", len(list.Tools), err)
			}
			text, isErr := toolText(t, resps["2"])
			if c.Refused {
				if !isErr || text != loginRequiredText {
					t.Errorf("automation_publish = %q (isError %v), want the login-required tool error", text, isErr)
				}
			} else if !strings.HasPrefix(text, codeDaemonRequired) {
				// No daemon runs here: the call got as far as the grant's own check.
				t.Errorf("automation_publish = %q, want it to reach the grant (daemon_required)", text)
			}
			var runs int
			if err := f.db.DB.QueryRow(`SELECT COUNT(*) FROM workflow_executions`).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if runs != 0 {
				t.Errorf("%d runs were created", runs)
			}
		})
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/mcp/ -run '^(TestToolsCallIsRefusedWhileLocked|TestGrantModeToolsCallIsRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `--- FAIL` for both, in the subtests `locked,_no_login` and `locked,_refused`.

- [ ] **Step 3: Implement.** Apply these two edits to `internal/mcp/server.go`:

**Edit 1** in `internal/mcp/server.go`:

```diff
+	"github.com/monoes/mono-agent/internal/account"
+	"github.com/monoes/mono-agent/internal/accountdoor"
 	"github.com/monoes/mono-agent/internal/apiconfig"
```

**Edit 2** in `internal/mcp/server.go`:

```diff
+// handleToolsCall runs one tool. A locked monoes.me account refuses every call,
+// grant mode included, with a tool error (isError) that says what to do, before
+// the call is parsed. initialize and tools/list still answer.
 func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) *rpcResponse {
+	if err := account.Require(ctx); err != nil {
+		return s.result(req.ID, toolResult{
+			Content: []toolContent{{Type: "text", Text: accountdoor.Message}},
+			IsError: true,
+		})
+	}
 	var params struct {
```

- [ ] **Step 4: Run the new tests, then the package**

Run the command of Step 2, then `go test ./internal/mcp/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: `ok  	github.com/monoes/mono-agent/internal/mcp	…` both times (the package takes about a minute; `TestGrantWaitTimeoutNote` and `TestManyUpdateCallsAtOnceAllLand` are load flakes that pass alone, index §4).

- [ ] **Step 5: Commit**

```
git add internal/mcp/server.go internal/mcp/account_door_test.go
git commit -m "feat(mcp): tools/call returns the login-required tool error while the account is locked" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation check** (after the commit). Make each change with the `Edit` tool (the line is unique in its file), run the command, expect the failure, restore the file with `git checkout -- <file>` (it returns to the commit just made), and go on. A pass would mean a door is not pinned: fix the test, not the mutation.

- **mcp**: in `internal/mcp/server.go` change `if err := account.Require(ctx); err != nil {` to `if err := account.Require(ctx); err != nil && false {`. Run `go test ./internal/mcp/ -run '^(TestToolsCallIsRefusedWhileLocked|TestGrantModeToolsCallIsRefusedWhileLocked)$' -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` and expect at least one `--- FAIL`. Restore: `git checkout -- internal/mcp/server.go`.

---

### Task 9: Verify the whole

No new code. This task checks that the doors hold together and that nothing outside them moved.

- [ ] **Step 1: Format, vet and build, every variant** (one command at a time; never a bare `go build ./cmd/monoagentcli` in the repository root)

Run: `gofmt -l .` then `go vet ./...` then `go build ./...` then `go build -tags nosocial -o /dev/null ./cmd/monoagentcli` then `go build -tags devaccount -o /dev/null ./cmd/monoagentcli`
Expected: no output from any of them (the `devaccount` tag exists once B1a is merged).

- [ ] **Step 2: The packages this plan touched, in full**

Run: `go test ./internal/accountdoor/... ./internal/httpapi/ ./internal/orgbridge/ ./internal/openaiapi/ ./internal/workflow/ ./internal/mcp/ ./internal/extension/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: seven `ok` lines (the packages run in parallel: a minute or two). A failure on the index §4 list of load flakes (`TestGrantWaitTimeoutNote`, `TestManyUpdateCallsAtOnceAllLand`) is judged against that list; any other failure is yours.

- [ ] **Step 3: Race the doors that add concurrency.** The HTTP API, `/v1`, receiver and MCP doors add no shared state, and the new HTTP API tests alone take about two minutes under `-race` (each builds a vault), so they are left out.

Run: `go test ./internal/accountdoor/... -count=1 -race`, then `go test ./internal/workflow/ -run '^(TestWebhookServerRefusesRequestsWhileLocked|TestWebhookServerLogsALockedAccountOncePerMinute)$' -count=1 -race`, then `go test ./internal/extension/ -run '^(TestRequestsAreRefusedWhileLocked|TestABridgeSocketRecoversWhenTheAccountDoes|TestRelayIsRefusedWhileLocked|TestCDPIsRefusedWhileLocked|TestPushedCapturesAndRecordingsAreAcceptedWhileLocked|TestTheOpenEndpointsOfALockedBridge)$' -count=1 -race`
Expected: `ok` for each.

- [ ] **Step 4: The OpenAPI lint**

Run: `npx --yes @redocly/cli@2.49.0 lint internal/httpapi/openapi.yaml; echo "exit $?"`
Expected: `Your API description is valid.`, the one existing `operation-4xx-response` warning for `/health`, `exit 0`.

- [ ] **Step 5: The command tests next to the doors.** `cmd/monoagentcli` tests build the daemon's route composition and the dedicated `/v1` listener with no guard installed, where `account.Require` fails open in a test binary, so they must still pass.

Run: `go test ./cmd/monoagentcli/ -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'`
Expected: the package takes about six minutes. The only failures allowed are the pristine-master ones of index §4 (`TestCaptureTaskFilesOnTheBoard`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCoderConversationFolders`); `TestDaemonRoutesKeepTheOrgReceiverAndMountV1OnlyOnLoopback` and `TestStartV1OffLoopbackServesHTTPSWithTheGeneratedCertificate` must pass.

---

## Door inventory: what covers what, and what is open on purpose

Every HTTP, WebSocket and stdio entry the repository registers was enumerated with `grep` for `net.Listen`, `ListenAndServe`, `http.Server{`, `HandleFunc`, `Handle(` and `Upgrade(` in non-test Go. `daemonRoutes` (`cmd/monoagentcli/api_gateway.go:439`) only composes `Receiver.Register` and `Gateway.Mount` on the one mux, which the gate covers.

| Entry | File | Covered by |
|---|---|---|
| the 12 authenticated routes of the HTTP API | `internal/httpapi/server.go:123-139` | `accountGate`, `Server.auth` |
| `POST /org-endpoint/{id}` | `internal/orgbridge/receiver.go:98` | `accountGate` (behind `httpapi`), `Receiver.ServeHTTP` |
| `/v1/models`, `/v1/models/{id...}`, `/v1/chat/completions`, `/v1/images/generations` | `internal/openaiapi/register.go:9-12` | `Gateway.auth`, on the main and on the dedicated listener |
| the webhook server | `internal/workflow/webhook_server.go` | `refuseWhileLocked` |
| bridge: requests, relay, CDP socket | `internal/extension/{request,server,cdp}.go` | Task 7 |
| MCP `tools/call`, grant mode included | `internal/mcp/server.go:374` | `handleToolsCall` |

Registration order and wrapping: `routes()` registers `GET /health` first, the authenticated routes next and the `ExtraRoutes` last (`internal/httpapi/server.go:123-143`), and the gate wraps the finished mux, so order cannot open a route; Go's mux panics on a conflicting pattern instead of letting a later one shadow an earlier one. The dedicated `/v1` listener has no mux of its own beyond `Gateway.Handler`, whose only routes are `/health` and `Mount`'s four.

Open on purpose, each pinned by a test:
- `GET` and `HEAD /health` of the HTTP API (it reports the account: `TestHealthIsOpenInEveryStateAndReportsTheAccount`) and `GET /health` of the dedicated `/v1` listener (it reports nothing of the account: `TestTheDedicatedListenerRefusesToo`).
- The bridge's handshake, probes, pairing and browser listing (`TestTheOpenEndpointsOfALockedBridge`).
- The sign-in loopback callback of `internal/library/auth.go:209`: it is the login itself.
- The one-shot OAuth callback of `internal/connections/oauth.go:118`: started by `connect oauth`, a gated command, for a third-party grant. It is a callback, not a door.

Covered elsewhere, not a request door:
- A delivery the org receiver accepted before the lock whose bus event arrives after it (`Receiver.onEvent`, `dispatch`): `CreateUnownedExecution` queues a run that `handleExecution` refuses and records (B3a), and `sendReplies` then tells the sender. Refusing it in `dispatch` would drop a message the sender was told was accepted.
- In-process callers of `Server.SendCommandTo` (workflow browser nodes, `node run`): `ActionExecutor.executeDef` (B3a).
- Background work of the gateway (catalog refresh, Jev picks, summaries) and every agent turn: `monomind.Exec` (B3a).
- Activity-recording frames, unsolicited page captures and binding pushes on the bridge socket: data, not work, kept accepted (`TestPushedCapturesAndRecordingsAreAcceptedWhileLocked`) because the extension drops its queued copy once the socket takes a push and installed extensions cannot be updated centrally; what runs on them is refused where it runs. Tightening this needs an extension release first.
- The desktop app (`wails-app`) starts no listener (a search of its Go for `net.Listen`, `ListenAndServe` and `http.Server` finds none); its bindings shell out to `monoagentcli`, which the CLI gate covers (B2, B4).

## Contract change requests

All four were ruled on by the lead and are applied in this plan; none changes a Go signature in `internal/account`.

1. **Index §3.4 item 7: the shapes B4's side panel reads.** Approved; the lead writes them into the index. This plan implements, with the existing reply envelope of `internal/extension/request.go`, the refusal `{"kind":"reply","id":"<id>","error":"Log in to monoes.me first: monoagentcli account login","code":"account_locked"}` (no `ok` key: `Reply.OK` is `omitempty` at `request.go:122`, so a reader tests `!reply.ok`), and `ping` data `{"pong":true,"methods":[…],"account":{"state":"ok|grace|locked","reason":"…","valid_until":"<RFC 3339, absent without a session>","enforced":true}}`, the same object as `GET /health`'s `account`, never carrying the user.
2. **`enforced` on the `ping` and `/health` account objects (B4's request).** Approved and applied: `accountdoor.Summary` carries `Enforced` (from `Status.Enforced`), and the `/health` and `ping` tests assert it per mode (false only while dormant). B3a adds the same field to the heartbeat's `AccountState`; B5a asserts `{state, reason, valid_until, enforced}`. The 401 bodies stay `{"state","reason"}`: a refusal implies enforcement.
3. **Pushed captures, recordings and bindings stay accepted while locked (B4's request 2).** Approved as the owner's proxy and pinned by a test; the lead lists it for the owner as a deliberate deviation from "the bridge refuses everything but `ping`". Refusing them later needs an extension release first.
4. **B1a's `account.LoginRequiredMessage`.** Approved: Task 1 defines `const Message = account.LoginRequiredMessage`, and `TestWriteUnauthorizedBody` keeps the literal sentence as the pin.
