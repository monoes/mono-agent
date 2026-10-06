# Mandatory monoes.me Account — B4: the desktop gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The desktop app puts a monoes.me sign-in gate in front of itself while the machine's account is locked (and fails closed when it cannot tell), and warns in grace and before the enforcement date.

**Architecture:** Six Wails bindings shell out to `monoagentcli --json account …`; `AccountStatus` returns the CLI's `account.Status` verbatim (also at exit 4) or a coded failure, and asks nothing while the build has no enforcement date. The React root renders `AccountGate` instead of the app while `lib/accountGate.js` says locked: it asks on start, on window focus and on every `login_required` answer of any binding, and fails closed.

**Tech Stack:** Go 1.26 (Wails v2 bindings, fake-CLI tests), React 19 with react-i18next, vitest with jsdom and Testing Library.

**Depends on:** B1b (the `account` commands and the machine session) and B2 (the CLI gate, whose exit 4 and `login_required` answer the gate reads). The Chrome extension's side panel is B4b, which depends on B3b only.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§6.5 with amendments A4 and A5, D21, D22, the fail-closed gate); plan index `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md`. Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

**Proof.** Every code block below was compiled and run in a scratch export of `f4441a2a`, against a stub of the index §3.2 and §3.3 surface that B4 consumes (the real `internal/account` lands with B1a and B1b; the stub's signatures were compared with the code of the B1a plan on disk: `Sealer`, `OpenStore`, `Store`, `Session`, `User`, `NewGuard`, `Install`, `InstallForTest`, `SetEnforceFromForTest`, `accounttest.New`): `go vet`, `gofmt -l` and `go test ./...` in `wails-app`; `npx vitest run` over the whole frontend (149 files, 1594 tests). The red outputs quoted in the steps were captured by hiding the implementation. The layout was looked at in a browser (vite dev server, `window.go` mocked): the gate, the grace banner, the warn banner with its dialog, the stale-CLI gate.

## Global Constraints

Copied from index §2, the lines that bind this plan; everything else in §2 binds implicitly.

- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace. A grant whose outcome is unknown (the request may have been processed, so monoes.me may have rotated the refresh token) or whose answer could not be saved (A24(d)) is retried within 240 seconds and after that is never presented again: this machine drops its refresh token and the reason is `unconfirmed` (A24, §3.6), a grace reason that ends as `locked(unconfirmed)`; the other installs of the account are untouched.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
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

The last bullet gives the desktop's `account.*` strings to B4: Task 6 appends one `account` object at the end of `wails-app/frontend/src/locales/en.json` and `es.json` and edits nothing else in them. B5b edits only the claim strings in `internal/i18n/locales`, so the two phases cannot conflict.

Other rules that bind this plan. No new `wails-app/*.go` file may import `internal/monomind` (`src/doctrine.test.js`; its two known exceptions are `app.go` and `app_ai.go`), and B4's new Go files do not. The GUI's select rule (AGENTS.md, "UI style guide: form controls") is not in play: B4 adds no `<select>`. Targeted frontend runs use `npx vitest run --maxWorkers=3 <files>` from `wails-app/frontend` (CI runs `npm test -- --run`, `.github/workflows/ci.yml:297`; nothing in the repo sets `--maxWorkers`); a whole-suite run on a loaded machine can time out in unrelated settings render tests (seen: `ApiConfigBlock.reset`, `ApiConfigBlock.restart`, `ApiSection.config`), which pass when rerun alone. Never a bare `go build ./cmd/monoagentcli` in the repo root. Line numbers in the steps refer to the files at `f4441a2a`: when a step edits one file in several places, work from the bottom up, or find each place by the quoted text.

## Review Focus

Failure modes that no task's main tests would otherwise exercise and that bite a person using the software, most likely first. Each is pinned by a named test.

1. **A build with no enforcement date must change nothing a user can see (D22).** The desktop does not even ask the CLI, and a failure, a grace state or a locked state with no date never locks or shows a banner. Pinned in Task 1 (`TestAccountStatusFollowsTheBuildsEnforcementDate`), Task 5 (`viewOf` dormant) and Task 6 ("says nothing, and never locks, for a build with no date").
2. **A locked machine's `account status` exits 4 with its document on stdout.** The other bindings' error path (`cliResultJSON`) drops such a body, and every locked user would read "could not check your sign-in" instead of the sign-in form. Pinned in Task 1 (`TestAccountStatusAnswers/locked exits 4 with its document`).
3. **A button that cannot work must not be offered.** The update installs by running the CLI, so with no CLI the gate's "Update MonoAgent" is a dead end, even once the app has heard of a release. Pinned in Task 6 (`offers no update for a missing CLI, whatever release is known`).
4. **A hiccup in `account status` unmounts a working app and its unsaved state; a stream of refused calls spawns a CLI each.** One retry for a transient failure, none for a stable one, and a gap between focus and `login_required` checks. Pinned in Task 5 (retry and gap tests).
5. **Signing in from the gate must reach the account bindings, and two names must not run two logins.** Pinned in Task 6 (the gate's sign-in calls `AccountLogin`) and Task 1 (`TestLoginBindingsReportUnderTheirOwnEventName`, and the existing `TestLibraryLoginCancel`, which now runs through the alias: one slot and one cancel for both names).
6. **A computer that dropped its saved sign-in is not an outage, and a reason this app has never heard of is not a blank screen (spec A24).** After a refresh whose answer never arrived the guard drops the refresh token: the grace banner for `unconfirmed` says that this computer can no longer renew its sign-in, until when it still works, and offers the sign-in; the gate for `locked(unconfirmed)` offers it too; and a reason the app does not know falls back to generic words in the gate and in the banner. Pinned in Task 6 (`says that this computer can no longer renew its sign-in, and offers to sign in again`, the `unconfirmed` row of `says why for each reason`, and `uses generic words for a reason this app does not know`).

## Decisions this plan adds

1. **A dormant build asks nothing.** The app embeds `internal/account`, so `AccountStatus` reads `account.EnforceDate()`: zero returns a fixed "nothing enforced" document without running the CLI. A failure carries `enforced` and `enforce_from` from the app's own `account.CurrentStatus()`, which a stale CLI cannot give, and locks only when `enforced` is true; before the date it shows the warn banner.
2. **The guard has no refresher.** The index's `NewDefaultGuard` includes one, so the app builds `account.NewGuard` with a nil `Refresher` and a sealer that refuses to open the key store. The finding behind it (which gated functions `wails-app` calls directly): none of `monomind.Exec`, the engine constructors or `ActionExecutor`, so the guard is a safety net, and a source scan pins it. The app does run `monomind` itself outside those sites, in the two files `src/doctrine.test.js` excepts: `wails-app/app.go:613-619` (`bootstrapProfileMonograph` finds `monomind` and runs `monograph build`) and `wails-app/app_ai.go:50` and `:69` (`ScanAgentRuntimes` and `GetAgentRuntimeModels` call `monomind.Scan` and `monomind.ListModels`). Spec §6.5's "every action it takes goes through a gated CLI call" does not hold for these, as spec amendment A5 says. None is a layer-2 site (they seed an empty index and discover installed runtimes and their models), so B4 leaves them alone; the document watcher is not one of them, it syncs through `monoagentcli` (`app_documents_watch.go:78`). Since amendment A15, `monomind.Exec` is gated through a hook that only `cmd/monoagentcli` installs, so a desktop that called `Exec` in process would be refused whatever its guard said: one more reason the scan pins that it does not.
3. **The gate signs in through `LogInToMonoesButton` with a `service` prop** (default: the library's), passing `services/account.js`. The `Library*` Go bindings are aliases of the `Account*` ones (shared slot, shared cancel, each under its own event name), and the library dialog keeps using them.
4. **Fail closed, once.** Any failure to read the status locks (once this build enforces); a failure while the app is up is first repeated after 2 seconds (cause `cli_failed` only), so a busy CLI does not unmount the app.
5. **The update stays reachable while locked, where it can run.** The gate offers `AppSelfUpdate` for `key_unknown`, for a CLI that is too old or failing, and on any other locked screen once the app's own check has fired `update:available` (that check is itself a CLI call, `wails-app/updater.go:47`). Never for a missing CLI: `AppSelfUpdate` installs by running `monoagentcli --json update --app` (`wails-app/app_update.go:34-36` and `:68`), so with no CLI it can only fail, and that gate says to reinstall and offers "Try again" alone, as spec amendment A5 says (§6.5 had it offer `update` for "no CLI at all").

---

## Tasks

### Task 1: The account bindings and the Library* aliases (Go)

**Files:**
- Create: `wails-app/app_account.go`, `wails-app/app_account_test.go`
- Modify: `wails-app/app_library.go` (lines 10-39 and 50-146), `wails-app/app_library_cli_test.go` (lines 63-65 and 130-132)

**Interfaces:**
- Consumes, from `internal/account` (B1a, index §3.2): `type Status struct{ V int; State State; … }` with `StateOK`, `StateGrace`, `StateLocked`; `func EnforceDate() time.Time`; `func CurrentStatus() Status` (nil-safe, fills `Enforced` and `EnforceFrom`); in tests `func SetEnforceFromForTest(t testing.TB, at time.Time)`. From the repo: `findMonoAgentCLI`, `hideWindow`, `stopGracefully`, `cliResultJSON`, `aiError`, `lastLine`, `emitLibraryEvent`, `healthGracePeriod`, and the test helpers `fakeCLI`, `newTestApp`, `loggedArgs`.
- Consumes, from B1b (the CLI surface, spec §7 and the `library login` precedent at `cmd/monoagentcli/library.go:317-328` and `:350-381`): `--json account status` prints `account.Status` at exit 0 (`ok`, `grace`) or 4 (`locked`): for a locked status B1b prints the document and returns a `reportedError`, so stdout stays exactly one document (`cmd/monoagentcli/main.go`, `reportCommandError`: a `reportedError` gets no second `{"error"}` document); `--json account login` prints `{"kind":"url","url":…}` on stderr (B1b `signIn`) and a document with no `"error"` key on stdout when it succeeds; `--json account login --email=<a> --send` and `--email=<a> --code=<c>` (B1b `signInFlags`); `--json account logout` (`{"logged_out":true,"base_url":…}`). On a failure the stdout document is `{"error":…}`, with a `code` only for a classified error (`auth_or_connection`, exit 4; `invalid_input`, exit 3); a local failure such as an unavailable key store exits 1 with `{"error":…}` and no `code`, so the page reads `error` and treats a missing `code` as a generic failure. The desktop passes no `--profile`.
- Produces: `func (a *App) AccountStatus() string`, `AccountLogin() string`, `AccountLoginCancel() string`, `AccountLoginEmailSend(email string) string`, `AccountLoginEmailVerify(email, code string) string`, `AccountLogout() string`; `(a *App) runAccountLogin(event string) string`; `classifyAccountStatus(stdout []byte, stderr string, runErr error, timedOut bool) string`; `const accountDormant`; the failure codes `cli_not_found`, `cli_too_old`, `cli_failed`.

- [ ] **Step 1: Make `wails-app` compile in the worktree.** `main.go` embeds `frontend/dist`, which is gitignored (`.gitignore:80`) and absent. Run `mkdir -p wails-app/frontend/dist` and `printf '<!doctype html><title>x</title>' > wails-app/frontend/dist/index.html`. Expected: no output, and `git status --short` does not list it.

- [ ] **Step 2: Write the failing test** `wails-app/app_account_test.go`:

```go
//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

const (
	acctOK          = `{"v":1,"state":"ok","reason":"","plan":"free","enforce_from":"2026-10-26T00:00:00Z","enforced":false}`
	acctGrace       = `{"v":1,"state":"grace","reason":"unreachable","plan":"free","grace_until":"2026-10-06T22:00:00Z","enforced":true}`
	acctLocked      = `{"v":1,"state":"locked","reason":"not_logged_in","plan":"free","enforced":true}`
	acctUnconfirmed = `{"v":1,"state":"grace","reason":"unconfirmed","plan":"free","grace_until":"2026-10-06T22:00:00Z","enforced":true}`
	acctNewReason   = `{"v":1,"state":"locked","reason":"from_the_future","plan":"free","enforced":true}`
)

// enforceFrom gives this build an enforcement date, as release R does (B5a).
func enforceFrom(t *testing.T, at time.Time) { account.SetEnforceFromForTest(t, at) }

func accountApp(t *testing.T) *App {
	a := newTestApp(t)
	a.ctx = context.Background()
	return a
}

// The account bindings shell out to `monoagentcli --json account …` with no
// --profile, and the Library* login bindings are aliases of them.
func TestAccountBindingsShellOut(t *testing.T) {
	enforceFrom(t, time.Now().Add(-time.Hour))
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+"'\necho '{\"ok\":true}'\n"))
	a := accountApp(t)
	a.setActiveProfileID("work")

	a.AccountStatus()
	a.AccountLoginEmailSend("me@example.com")
	a.AccountLoginEmailVerify("me@example.com", " 123456 ")
	a.AccountLogout()
	a.LibraryLoginEmailSend("-odd@example.com")
	a.LibraryLoginEmailVerify("me@example.com", "654321")
	a.LibraryLogout()

	want := "--json account status|--json account login --email=me@example.com --send|" +
		"--json account login --email=me@example.com --code=123456|--json account logout|" +
		"--json account login --email=-odd@example.com --send|--json account login --email=me@example.com --code=654321|--json account logout"
	if got := strings.Join(loggedArgs(t, log), "|"); got != want {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", got, want)
	}
}

// What the page is told about one `account status` run: the document whatever
// its state, at exit 0 or 4 (`locked` exits 4, which the other bindings' error
// path would have turned into a plain message), else a coded failure. Only the
// schema and the state are checked: a reason, one a newer monoagentcli reports or
// one added since (unconfirmed, A24), passes through, and the page has words for
// every reason, known or not.
func TestAccountStatusAnswers(t *testing.T) {
	tests := []struct{ name, script, want, code, errHas string }{ // want: the document, or "" for a failure
		{"signed in", `echo '` + acctOK + `'`, acctOK, "", ""},
		{"grace", `echo '` + acctGrace + `'`, acctGrace, "", ""},
		{"locked exits 4 with its document", `echo '` + acctLocked + `'; exit 4`, acctLocked, "", ""},
		{"grace after a refresh whose answer never arrived (A24)", `echo '` + acctUnconfirmed + `'`, acctUnconfirmed, "", ""},
		{"locked for a reason it has not heard of", `echo '` + acctNewReason + `'; exit 4`, acctNewReason, "", ""},
		{"a state it has not heard of", `echo '{"v":1,"state":"paused"}'`, "", accountCauseTooOld, "does not understand"},
		{"a newer schema", `echo '{"v":2,"state":"ok"}'`, "", accountCauseTooOld, "does not understand"},
		{"a CLI without the command", `echo 'Error: unknown command "account"' >&2; exit 1`, "", accountCauseTooOld, "no `account` command"},
		{"help text instead of JSON", `echo 'Usage: monoagentcli [command]'`, "", accountCauseTooOld, "does not understand"},
		{"a crash", `echo 'panic: boom' >&2; exit 2`, "", accountCauseFailed, "panic: boom"},
		{"a document at an exit that is neither 0 nor 4", `echo '` + acctOK + `'; exit 1`, "", accountCauseFailed, "exit status 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enforceFrom(t, time.Now().Add(-time.Hour))
			t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, tc.script+"\n"))
			got := accountApp(t).AccountStatus()
			var f struct{ Error, Code string }
			if tc.want != "" && got != tc.want {
				t.Fatalf("AccountStatus = %s, want the CLI's document", got)
			}
			if tc.want == "" && (json.Unmarshal([]byte(got), &f) != nil || f.Code != tc.code || !strings.Contains(f.Error, tc.errHas)) {
				t.Fatalf("AccountStatus = %s, want code %q with %q", got, tc.code, tc.errHas)
			}
		})
	}
}

// No monoagentcli at all, and one that never answers, are failures by name too.
func TestAccountStatusWithoutAnAnsweringCLI(t *testing.T) {
	enforceFrom(t, time.Now().Add(-time.Hour))
	a := accountApp(t)
	prev := accountFindCLI
	accountFindCLI = func() (string, error) { return "", errors.New("monoagentcli binary not found") }
	t.Cleanup(func() { accountFindCLI = prev })
	if got := a.AccountStatus(); !strings.Contains(got, `"code":"`+accountCauseNotFound+`"`) {
		t.Fatalf("no CLI: %s", got)
	}

	accountFindCLI = prev
	prevTimeout, prevGrace := accountStatusTimeout, healthGracePeriod
	accountStatusTimeout, healthGracePeriod = 300*time.Millisecond, time.Second
	t.Cleanup(func() { accountStatusTimeout, healthGracePeriod = prevTimeout, prevGrace })
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, "exec sleep 30\n"))
	if got := a.AccountStatus(); !strings.Contains(got, `"code":"`+accountCauseFailed+`"`) || !strings.Contains(got, "in time") {
		t.Fatalf("a CLI that never answers: %s", got)
	}
}

// A build with no enforcement date asks the CLI nothing and says nothing is
// enforced (D22), even with a CLI that would fail; one whose date has not come
// reports the date with a failure, which then locks nothing.
func TestAccountStatusFollowsTheBuildsEnforcementDate(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+"'\necho 'unknown command' >&2; exit 1\n"))
	a := accountApp(t)

	enforceFrom(t, time.Time{})
	if got := a.AccountStatus(); got != accountDormant {
		t.Fatalf("dormant: AccountStatus = %s", got)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("a dormant build ran the CLI")
	}
	soon := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	enforceFrom(t, soon)
	if got := a.AccountStatus(); !strings.Contains(got, `"enforced":false`) || !strings.Contains(got, `"enforce_from":"`+soon.Format(time.RFC3339)+`"`) {
		t.Fatalf("before the date: %s", got)
	}
	enforceFrom(t, time.Now().Add(-time.Hour))
	if got := a.AccountStatus(); !strings.Contains(got, `"enforced":true`) || !strings.Contains(got, `"code":"`+accountCauseTooOld+`"`) {
		t.Fatalf("after the date: %s", got)
	}
}

// AccountLogin and LibraryLogin are one login (the existing TestLibraryLoginCancel
// covers their shared slot and cancel): the same CLI call, each reporting under
// its own event name.
func TestLoginBindingsReportUnderTheirOwnEventName(t *testing.T) {
	var mu sync.Mutex
	var names []string
	prev := emitLibraryEvent
	emitLibraryEvent = func(_ *App, name string, _ interface{}) { mu.Lock(); names = append(names, name); mu.Unlock() }
	t.Cleanup(func() { emitLibraryEvent = prev })
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+"'\necho '{\"kind\":\"url\",\"url\":\"https://monoes.me/x\"}' >&2\necho '{\"v\":1,\"state\":\"ok\"}'\n"))
	a := accountApp(t)

	if got := a.AccountLogin() + a.LibraryLogin(); strings.Count(got, `"state":"ok"`) != 2 {
		t.Fatalf("logins = %s", got)
	}
	if got := strings.Join(names, ","); got != "account:login,library:login" {
		t.Fatalf("events = %s", got)
	}
	if got := strings.Join(loggedArgs(t, log), "|"); got != "--json account login|--json account login" {
		t.Fatalf("CLI calls = %s", got)
	}
}
```

- [ ] **Step 3: Update the existing library test for the aliases.** In `wails-app/app_library_cli_test.go`, replace lines 63-65:

```go
		"--profile work --json library logout",
		"--profile work --json library login --email=me@example.com --send",
		"--profile work --json library login --email=me@example.com --code=123456",
```

with:

```go
		"--json account logout",
		"--json account login --email=me@example.com --send",
		"--json account login --email=me@example.com --code=123456",
```

and lines 130-132 (`libraryLoginMu` and `libraryLoginCancel`) with the renamed variables:

```go
		accountLoginMu.Lock()
		running := accountLoginCancel != nil
		accountLoginMu.Unlock()
```

- [ ] **Step 4: Run it and see it fail.** From `wails-app`: `go test . -run '^(TestAccountBindingsShellOut|TestAccountStatusAnswers|TestAccountStatusWithoutAnAnsweringCLI|TestAccountStatusFollowsTheBuildsEnforcementDate|TestLoginBindingsReportUnderTheirOwnEventName|TestLibraryBindingsShellOut|TestLibraryLoginCancel)$' -count=1`. Expected: a build failure that starts `./app_account_test.go:43:4: a.AccountStatus undefined (type *App has no field or method AccountStatus)` and goes on with `a.AccountLoginEmailSend undefined`, `undefined: accountCauseTooOld`, and from `app_library_cli_test.go` `undefined: accountLoginMu`.

- [ ] **Step 5: Create `wails-app/app_account.go`:**

```go
// wails-app/app_account.go
//
// The monoes.me account: the machine-wide sign-in every official build needs
// (spec §6.5). Like the library bindings these shell out to `monoagentcli
// --json account …` and return its stdout, with no --profile (the session
// belongs to the machine). What this file adds is the app's half of failing
// closed: an `account status` that cannot be run, or answers something this
// app does not know, comes back as a coded failure and never as a status.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

const (
	accountCLITimeout = 60 * time.Second // a code to send or verify, a logout
	// The CLI's own login wait is 5 minutes; this leaves it room to report.
	accountLoginTimeout = 6 * time.Minute
)

// accountStatusTimeout bounds `account status`, which may refresh over the
// network and open the key store. A variable, like accountFindCLI, for tests.
var (
	accountStatusTimeout = 20 * time.Second
	accountFindCLI       = findMonoAgentCLI
)

// What a failed `account status` is called in its answer's "code".
const (
	accountCauseNotFound = "cli_not_found" // there is no monoagentcli to ask
	accountCauseTooOld   = "cli_too_old"   // it answered, but not with a status this app understands
	accountCauseFailed   = "cli_failed"    // it could not answer: it crashed, or took too long
)

var (
	accountLoginMu     sync.Mutex
	accountLoginCancel context.CancelFunc
)

// accountDormant is the answer of a build with no enforcement date (D22):
// nothing to judge, so nothing is asked.
const accountDormant = `{"v":1,"state":"ok","reason":"","plan":"free","enforced":false}`

// AccountStatus returns `account status --json` (account.Status) verbatim. The
// CLI prints it for ok, grace and locked alike and exits 4 when locked, so
// stdout counts at exit 0 or 4. Anything else is {"error","code","enforced",
// "enforce_from"}: enforced says whether this build already enforces (a failure
// only locks the app then).
func (a *App) AccountStatus() string {
	if account.EnforceDate().IsZero() {
		return accountDormant
	}
	cliBin, err := accountFindCLI()
	if err != nil {
		return accountFailure(accountCauseNotFound, err.Error())
	}
	ctx, cancel := context.WithTimeout(a.ctx, accountStatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin, "--json", "account", "status")
	hideWindow(cmd)
	stopGracefully(cmd)
	out, runErr := cmd.Output()
	var stderr string
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		stderr = string(ee.Stderr)
	}
	return classifyAccountStatus(out, stderr, runErr, ctx.Err() != nil)
}

// classifyAccountStatus decides what the page is told about one run: the
// document when it is one this app knows (schema 1, a known state) at exit 0
// or 4, else a coded failure.
func classifyAccountStatus(stdout []byte, stderr string, runErr error, timedOut bool) string {
	body := strings.TrimSpace(string(stdout))
	var st account.Status
	if code := accountExitCode(runErr); (code == 0 || code == 4) && json.Unmarshal([]byte(body), &st) == nil && st.V == 1 &&
		(st.State == account.StateOK || st.State == account.StateGrace || st.State == account.StateLocked) {
		return body
	}
	switch {
	case timedOut:
		return accountFailure(accountCauseFailed, "monoagentcli did not answer `account status` in time")
	case strings.Contains(stderr, "unknown command") || strings.Contains(stderr, "unknown flag"):
		return accountFailure(accountCauseTooOld, "the monoagentcli this app found has no `account` command")
	case runErr == nil:
		return accountFailure(accountCauseTooOld, "monoagentcli answered `account status` with something this app does not understand")
	}
	msg := lastLine(stderr)
	if msg == "" {
		msg = runErr.Error()
	}
	return accountFailure(accountCauseFailed, msg)
}

func accountExitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitCode()
	}
	return -1
}

func accountFailure(code, msg string) string {
	st := account.CurrentStatus() // this build's own date, which a stale CLI cannot give
	out := map[string]any{"error": msg, "code": code, "enforced": st.Enforced}
	if !st.EnforceFrom.IsZero() {
		out["enforce_from"] = st.EnforceFrom.UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// AccountLogin runs `account login` (PKCE through the system browser, which the
// CLI opens itself) and returns its final document. Each NDJSON progress line
// on the CLI's stderr is forwarded as an "account:login" event, so the page can
// show the sign-in URL as a fallback link. One login runs at a time;
// AccountLoginCancel stops it.
func (a *App) AccountLogin() string { return a.runAccountLogin("account:login") }

func (a *App) runAccountLogin(event string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	accountLoginMu.Lock()
	if accountLoginCancel != nil {
		accountLoginMu.Unlock()
		return `{"error":"a monoes.me login is already in progress","code":"busy"}`
	}
	ctx, cancel := context.WithTimeout(a.ctx, accountLoginTimeout)
	accountLoginCancel = cancel
	accountLoginMu.Unlock()
	defer func() {
		accountLoginMu.Lock()
		accountLoginCancel = nil
		accountLoginMu.Unlock()
		cancel()
	}()

	cmd := exec.CommandContext(ctx, cliBin, "--json", "account", "login")
	hideWindow(cmd)
	stopGracefully(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return aiError(err)
	}
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return aiError(err)
	}
	var lastLogLine string
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			ev = map[string]interface{}{"kind": "line", "message": line}
		}
		lastLogLine = line
		emitLibraryEvent(a, event, ev)
	}
	waitErr := cmd.Wait()
	if ctx.Err() == context.Canceled {
		return `{"error":"login cancelled","code":"cancelled"}`
	}
	if waitErr != nil && strings.TrimSpace(stdout.String()) == "" && lastLogLine != "" {
		return aiError(errors.New(lastLogLine)) // the CLI explained itself on stderr only
	}
	return cliResultJSON(cliBin, []byte(stdout.String()), waitErr)
}

// AccountLoginCancel stops a running AccountLogin or LibraryLogin (the same
// login). {"ok":true,"cancelled":bool}.
func (a *App) AccountLoginCancel() string {
	accountLoginMu.Lock()
	cancel := accountLoginCancel
	accountLoginMu.Unlock()
	if cancel == nil {
		return `{"ok":true,"cancelled":false}`
	}
	cancel()
	return `{"ok":true,"cancelled":true}`
}

// AccountLoginEmailSend asks monoes.me to email a sign-in code (the fallback
// for machines without a usable browser).
func (a *App) AccountLoginEmailSend(email string) string {
	return a.accountCLI("account", "login", "--email="+email, "--send")
}

// AccountLoginEmailVerify trades the emailed code for a session.
func (a *App) AccountLoginEmailVerify(email, code string) string {
	return a.accountCLI("account", "login", "--email="+email, "--code="+strings.TrimSpace(code))
}

// AccountLogout ends this machine's monoes.me session.
func (a *App) AccountLogout() string { return a.accountCLI("account", "logout") }

// accountCLI is rawCLI without the --profile.
func (a *App) accountCLI(args ...string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, accountCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin, append([]string{"--json"}, args...)...)
	hideWindow(cmd)
	out, runErr := cmd.Output()
	return cliResultJSON(cliBin, out, runErr)
}
```

- [ ] **Step 6: Make the library login bindings aliases.** In `wails-app/app_library.go` replace lines 50-146 (from the `// LibraryLogin runs …` comment through the closing brace of `LibraryLogout`; the `errorString` type in that range is no longer needed) with:

```go
// The library login is the machine's monoes.me account (one session), so the
// login bindings below are aliases of the Account* ones (app_account.go).
// LibraryLogin keeps its own event name, "library:login", which the library
// dialog listens for.

// LibraryLogin signs in through the browser: AccountLogin, reporting as
// "library:login" events.
func (a *App) LibraryLogin() string { return a.runAccountLogin("library:login") }

// LibraryLoginCancel stops a running LibraryLogin or AccountLogin.
func (a *App) LibraryLoginCancel() string { return a.AccountLoginCancel() }

// LibraryLoginEmailSend asks monoes.me to email a sign-in code.
func (a *App) LibraryLoginEmailSend(email string) string { return a.AccountLoginEmailSend(email) }

// LibraryLoginEmailVerify trades the emailed code for a session.
func (a *App) LibraryLoginEmailVerify(email, code string) string {
	return a.AccountLoginEmailVerify(email, code)
}

// LibraryLogout ends this machine's monoes.me session.
func (a *App) LibraryLogout() string { return a.AccountLogout() }
```

Then replace lines 10-39 (from `import (` to the `)` that closes the `var (` group with `libraryLoginMu`) with:

```go
import (
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	libraryCLITimeout     = 60 * time.Second
	libraryInstallTimeout = 5 * time.Minute
)

// emitLibraryEvent sends a login progress event to the frontend, named
// "library:login" or "account:login" (tests replace it: a bare context has no
// Wails runtime to emit on).
var emitLibraryEvent = func(a *App, name string, data interface{}) {
	runtime.EventsEmit(a.ctx, name, data)
}
```

- [ ] **Step 7: Run the tests.** The command of step 4 plus `TestLibraryLoginStreamsURL` and `TestLibraryBindingsPassLoginRequired`: `go test . -run '^(TestAccountBindingsShellOut|TestAccountStatusAnswers|TestAccountStatusWithoutAnAnsweringCLI|TestAccountStatusFollowsTheBuildsEnforcementDate|TestLoginBindingsReportUnderTheirOwnEventName|TestLibraryBindingsShellOut|TestLibraryLoginStreamsURL|TestLibraryLoginCancel|TestLibraryBindingsPassLoginRequired)$' -count=1`. Expected: `ok  	github.com/monoes/mono-agent/wails-app`.

- [ ] **Step 8: Vet and format.** `cd wails-app && go vet ./... && gofmt -l .` prints nothing. (`src/doctrine.test.js`, run in Task 6, checks that `app_account.go` imports no `internal/monomind`.)

- [ ] **Step 9: Commit** (two commands):

```bash
git add wails-app/app_account.go wails-app/app_account_test.go wails-app/app_library.go wails-app/app_library_cli_test.go
git commit -m "feat(gui): account bindings, and the library login bindings as aliases of them" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 2: The app's guard that never refreshes (Go)

**Files:**
- Create: `wails-app/app_account_guard.go`, `wails-app/app_account_guard_test.go`
- Modify: `wails-app/app.go` (after line 92 and after line 263)

**Interfaces:**
- Consumes, from `internal/account` (B1a): `NewGuard(GuardOptions{Store Store}) *Guard`, `OpenStore(dir string, s Sealer) Store` (`""` means `DefaultDir()`), `type Sealer interface{ Seal; Open }`, `ErrKeyringUnavailable`, `Install(*Guard)`, `(*Guard).Close()`; in tests `InstallForTest(t, g)`, `Require(ctx)`, `IsLoginRequired(err)`, `DefaultDir()`, `NewMemorySealer()`, `Session`, `User`, `HostURL`, and from `accounttest` `New(t)` (trusts a throwaway key and sets the enforcement date in the past), `(*Fixture).Token(TokenOptions)`, `Fixture.Clock.Set`.
- Produces: `(a *App) startAccountGuard()`, `(a *App) stopAccountGuard()`, `type readOnlySealer`.

- [ ] **Step 1: Write the failing tests** `wails-app/app_account_guard_test.go`:

```go
//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// The guard the app installs judges from session.json: a signed-in machine
// passes account.Require, one with no session is refused (the enforcement date
// is in the past here) and gets no file written for it. Its sealer never opens
// the key store.
func TestStartAccountGuardJudgesFromTheSessionFile(t *testing.T) {
	// Restores the process guard when the test ends.
	account.InstallForTest(t, account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())}))

	t.Run("signed in", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		fx := accounttest.New(t)
		fx.Clock.Set(time.Now())
		sess := &account.Session{V: 1, Host: account.HostURL, User: &account.User{ID: "u1"}, AccessToken: fx.Token(accounttest.TokenOptions{Sub: "u1"})}
		if err := account.OpenStore("", account.NewMemorySealer()).Save(sess); err != nil {
			t.Fatal(err)
		}
		a := newTestApp(t)
		a.startAccountGuard()
		t.Cleanup(a.stopAccountGuard)
		if err := account.Require(context.Background()); err != nil {
			t.Fatalf("Require with a session: %v", err)
		}
	})

	t.Run("no session", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		accounttest.New(t)
		a := newTestApp(t)
		a.startAccountGuard()
		t.Cleanup(a.stopAccountGuard)
		if err := account.Require(context.Background()); !account.IsLoginRequired(err) {
			t.Fatalf("Require with no session = %v, want a login-required error", err)
		}
		if dir, _ := account.DefaultDir(); !errors.Is(statErr(dir), os.ErrNotExist) {
			t.Fatalf("starting the guard created %s", dir)
		}
	})

	var s readOnlySealer
	if _, err := s.Open([]byte("x")); !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("the app's sealer opened the key store: %v", err)
	}
}

func statErr(path string) error { _, err := os.Stat(path); return err }

// startup installs the guard and shutdown stops it: a source check, since
// startup needs the Wails runtime.
func TestStartupInstallsAndShutdownStopsTheGuard(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"a.startAccountGuard()", "a.stopAccountGuard()"} {
		if !strings.Contains(string(src), call) {
			t.Errorf("app.go no longer calls %s", call)
		}
	}
}

// The finding that makes the app's guard a safety net: no file of the Go side
// calls a function the account gate judges (index §6.2). When this fails, send
// the new call through a `monoagentcli` subprocess (which the CLI gate judges),
// or keep it in process and add a test that it passes with a signed-in session
// and fails without one.
func TestDesktopGoSideCallsNoGatedFunction(t *testing.T) {
	gated := []string{"monomind.Exec(", "workflow.NewWorkflowEngine(", "workflow.NewWorkflowEngineWithStore(", "action.NewActionExecutor("}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 10 {
		t.Fatalf("finding the Go sources: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range gated {
			if !strings.HasSuffix(f, "_test.go") && strings.Contains(string(src), call) {
				t.Errorf("%s calls %s: a function the account gate judges", f, call)
			}
		}
	}
}
```

- [ ] **Step 2: Run them and see them fail.** `cd wails-app && go test . -run '^(TestStartAccountGuardJudgesFromTheSessionFile|TestStartupInstallsAndShutdownStopsTheGuard|TestDesktopGoSideCallsNoGatedFunction)$' -count=1`. Expected: a build failure: `./app_account_guard_test.go:35:5: a.startAccountGuard undefined (type *App has no field or method startAccountGuard)`, `a.stopAccountGuard undefined`, `./app_account_guard_test.go:56:8: undefined: readOnlySealer`.

- [ ] **Step 3: Create `wails-app/app_account_guard.go`:**

```go
// wails-app/app_account_guard.go
//
// The app's own process judges nothing today: it calls none of the functions the
// account gate judges (monomind.Exec, the workflow engine, the action executor;
// TestDesktopGoSideCallsNoGatedFunction pins that), and runs its work as
// `monoagentcli` subprocesses, which the CLI gate judges. It still installs a
// guard, because once the gate is enforced account.Require fails closed in a
// process that has none, so a layer-2 call added here later would refuse a
// signed-in user. The guard has no Refresher and its sealer never opens the key
// store (a keychain prompt from the desktop is issue #54); refreshing is the
// CLI's job.
package main

import (
	"sync"

	"github.com/monoes/mono-agent/internal/account"
)

// readOnlySealer is the guard's sealer: the refresh token is never read here.
type readOnlySealer struct{}

func (readOnlySealer) Seal([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }
func (readOnlySealer) Open([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }

var accountGuard struct {
	mu sync.Mutex
	g  *account.Guard
}

// startAccountGuard installs the process guard over the machine's session
// store. A store with no session creates no file.
func (a *App) startAccountGuard() {
	g := account.NewGuard(account.GuardOptions{Store: account.OpenStore("", readOnlySealer{})})
	accountGuard.mu.Lock()
	prev := accountGuard.g
	accountGuard.g = g
	accountGuard.mu.Unlock()
	account.Install(g)
	if prev != nil {
		prev.Close()
	}
}

// stopAccountGuard closes the guard on shutdown. With no Refresher there is
// nothing for Close to stop; it is housekeeping, and a closed guard still answers.
func (a *App) stopAccountGuard() {
	accountGuard.mu.Lock()
	g := accountGuard.g
	accountGuard.g = nil
	accountGuard.mu.Unlock()
	if g != nil {
		g.Close()
	}
}
```

- [ ] **Step 4: Run again; the call-site test still fails.** Same command. Expected: `--- FAIL: TestStartupInstallsAndShutdownStopsTheGuard` with `app.go no longer calls a.startAccountGuard()` and `app.go no longer calls a.stopAccountGuard()`; the other two pass.

- [ ] **Step 5: Wire it into the app.** In `wails-app/app.go`, after line 92 (`a.ctx = ctx`, the first line of `startup`) add:

```go
	// The process guard for any layer-2 call this process makes; see app_account_guard.go.
	a.startAccountGuard()
```

and as the first statement of `shutdown` (line 263 is `func (a *App) shutdown(_ context.Context) {`) add `a.stopAccountGuard()`:

```go
func (a *App) shutdown(_ context.Context) {
	a.stopAccountGuard()
	if a.chatSup != nil {
```

- [ ] **Step 6: Run the three tests; expected `ok  	github.com/monoes/mono-agent/wails-app`.** Then `go vet ./... && gofmt -l .` in `wails-app` prints nothing.

- [ ] **Step 7: Commit:**

```bash
git add wails-app/app_account_guard.go wails-app/app_account_guard_test.go wails-app/app.go
git commit -m "feat(gui): the desktop installs an account guard that never refreshes" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3: The Wails bindings

`wails-app/frontend/src/wailsjs/go/main/App.js` and `App.d.ts` are tracked and generated by `wails generate module`, but the generator re-sorts three hand-appended functions (`AnswerAgentQuestion`, `DeletePeople`, `SetChatOrgMode`), swaps import lines in `App.d.ts` and reorders classes in `models.ts`; commit `4bb3e907` kept only the new entries for the same reason. All six new methods return `string`, so `models.ts` is not touched. The hand edit below is byte-identical to what the generator emits for them (checked against `wails generate module`, v2.11.0 here, `go.mod` pins 2.16.0).

**Files:**
- Modify: `wails-app/frontend/src/wailsjs/go/main/App.js` (before line 49), `wails-app/frontend/src/wailsjs/go/main/App.d.ts` (before line 30)

**Interfaces:**
- Produces: `AccountLogin()`, `AccountLoginCancel()`, `AccountLoginEmailSend(arg1)`, `AccountLoginEmailVerify(arg1, arg2)`, `AccountLogout()`, `AccountStatus()`, each `Promise<string>`.

- [ ] **Step 1: Insert into `App.js`.** Immediately before the line `export function AddApplication(arg1, arg2, arg3, arg4, arg5, arg6) {` (line 49, right after the `APIStatus` function), insert:

```js
export function AccountLogin() {
  return window['go']['main']['App']['AccountLogin']();
}

export function AccountLoginCancel() {
  return window['go']['main']['App']['AccountLoginCancel']();
}

export function AccountLoginEmailSend(arg1) {
  return window['go']['main']['App']['AccountLoginEmailSend'](arg1);
}

export function AccountLoginEmailVerify(arg1, arg2) {
  return window['go']['main']['App']['AccountLoginEmailVerify'](arg1, arg2);
}

export function AccountLogout() {
  return window['go']['main']['App']['AccountLogout']();
}

export function AccountStatus() {
  return window['go']['main']['App']['AccountStatus']();
}

```

- [ ] **Step 2: Insert into `App.d.ts`.** Immediately before the line starting `export function AddApplication(arg1:string,` (line 30, right after `APIStatus`), insert:

```ts
export function AccountLogin():Promise<string>;

export function AccountLoginCancel():Promise<string>;

export function AccountLoginEmailSend(arg1:string):Promise<string>;

export function AccountLoginEmailVerify(arg1:string,arg2:string):Promise<string>;

export function AccountLogout():Promise<string>;

export function AccountStatus():Promise<string>;

```

- [ ] **Step 3: Check them.** `git diff --stat -- wails-app/frontend/src/wailsjs` shows 12 added lines in `App.d.ts`, 24 in `App.js`, no deletions and no other file; `git ls-files -s wails-app/frontend/src/wailsjs/go/main` still shows mode `100755` for both files. `grep -c 'export function Account' wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts` prints `6` for each. Optional cross-check against the generator, in a scratch export (never in the worktree): `git archive HEAD | tar -x -C <scratch>`, copy in the Task 1 and 2 Go files, `mkdir -p <scratch>/wails-app/frontend/dist && printf '<title>x</title>' > <scratch>/wails-app/frontend/dist/index.html`, then in `<scratch>/wails-app` run `~/go/bin/wails generate module`: the six functions in its `App.js` and `App.d.ts` equal the text above.

- [ ] **Step 4: Commit:**

```bash
git add wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts
git commit -m "feat(gui): Wails bindings for the account" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 4: The status answer and the binding watch (frontend)

**Files:**
- Create: `wails-app/frontend/src/services/account.js`, `services/account.test.js`, `lib/accountBindingWatch.js`, `lib/accountBindingWatch.test.js`

**Interfaces:**
- Consumes: the Task 3 bindings through `../wailsjs/go/main/App`; `subscribeEvent(name, cb)` from `services/api.js`.
- Produces: `answerOf(raw) → {status} | {failure: {cause, message, enforced, enforce_from}}` (causes `cli_not_found`, `cli_too_old`, `cli_failed`; a failure that cannot say whether the build enforces counts as enforced); `account = {status, login, cancelLogin, sendCode, verifyCode, logout}` (each resolves, never rejects; `login` and the others to the CLI's JSON or `{error}`); `onAccountLogin(cb)`; `loginRequiredIn(value)`, `loginRequiredInError(e)`; `watchBindings({onLoginRequired, onSessionChange}, root?) → stop`.

- [ ] **Step 1: Install the frontend dependencies once.** `cd wails-app/frontend && npm ci`. Expected: it ends with `added … packages` and exit 0.

- [ ] **Step 2: Write the failing tests.** `wails-app/frontend/src/services/account.test.js`:

```js
import { describe, it, expect, vi, beforeEach } from 'vitest'

const go = vi.hoisted(() => ({
  AccountStatus: vi.fn(), AccountLogin: vi.fn(), AccountLoginCancel: vi.fn(),
  AccountLoginEmailSend: vi.fn(), AccountLoginEmailVerify: vi.fn(), AccountLogout: vi.fn(),
}))
vi.mock('../wailsjs/go/main/App', () => go)

import { account, answerOf } from './account.js'

const OK = { v: 1, state: 'ok', reason: '', plan: 'free', enforced: true }
const failure = (cause, message = '', over = {}) => ({ failure: { cause, message, enforced: true, ...over } })

describe('answerOf: the gate fails closed', () => {
  it('takes a status document of schema 1 in each known state', () => {
    for (const state of ['ok', 'grace', 'locked']) expect(answerOf(JSON.stringify({ ...OK, state }))).toEqual({ status: { ...OK, state } })
  })

  it('takes the Go side\'s coded failures by their cause, with what it says of enforcement', () => {
    for (const code of ['cli_not_found', 'cli_too_old', 'cli_failed']) {
      expect(answerOf(JSON.stringify({ error: 'it said', code, enforced: true }))).toEqual(failure(code, 'it said', { enforce_from: undefined }))
    }
    const early = { error: 'x', code: 'cli_too_old', enforced: false, enforce_from: '2026-10-26T00:00:00Z' }
    expect(answerOf(JSON.stringify(early)).failure).toMatchObject({ enforced: false, enforce_from: '2026-10-26T00:00:00Z' })
    expect(answerOf(JSON.stringify({ error: 'x', code: 'cli_failed' })).failure.enforced).toBe(true) // silent: enforced
  })

  it('reads anything else as a CLI this app cannot talk to, never as a status', () => {
    for (const raw of [{ v: 2, state: 'ok' }, { v: 1, state: 'paused' }, { v: 1 }, { state: 'ok' }, {}, [], null, 42]) {
      expect(answerOf(JSON.stringify(raw)), JSON.stringify(raw)).toEqual(failure('cli_too_old'))
    }
    expect(answerOf(JSON.stringify({ error: 'busy', code: 'busy' })).failure.cause).toBe('cli_failed')
    expect(answerOf('Usage: monoagentcli').failure.cause).toBe('cli_failed')
  })
})

describe('account', () => {
  beforeEach(() => { Object.values(go).forEach(f => f.mockReset()) })

  it('status resolves to the status of a good answer, and a binding that rejects is a failure', async () => {
    go.AccountStatus.mockResolvedValue(JSON.stringify(OK))
    expect(await account.status()).toEqual({ status: OK })
    go.AccountStatus.mockRejectedValue(new Error('bridge down'))
    expect(await account.status()).toEqual(failure('cli_failed', 'bridge down'))
  })

  it('sign-in calls the Account bindings and returns their JSON, or {error}', async () => {
    go.AccountLogin.mockResolvedValue('{"v":1,"state":"ok"}')
    go.AccountLoginEmailVerify.mockRejectedValue(new Error('offline'))
    expect(await account.login()).toEqual({ v: 1, state: 'ok' })
    expect(await account.verifyCode('a@b.c', '123')).toEqual({ error: 'offline' })
    await account.sendCode('a@b.c'); await account.cancelLogin(); await account.logout()
    expect(go.AccountLoginEmailSend).toHaveBeenCalledWith('a@b.c')
    expect(go.AccountLoginCancel).toHaveBeenCalledTimes(1)
    expect(go.AccountLogout).toHaveBeenCalledTimes(1)
  })
})
```

`wails-app/frontend/src/lib/accountBindingWatch.test.js`:

```js
import { describe, it, expect, vi } from 'vitest'
import { loginRequiredIn, loginRequiredInError, watchBindings } from './accountBindingWatch.js'

const LOGIN_REQUIRED = JSON.stringify({
  error: 'Log in to monoes.me first: monoagentcli account login', code: 'auth_or_connection',
  login_required: true, account: { state: 'locked', reason: 'not_logged_in' },
})
const GATE_MESSAGE = 'Log in to monoes.me first: monoagentcli account login'
const appWith = (methods) => ({ go: { main: { App: methods } } })
const callbacks = () => ({ onLoginRequired: vi.fn(), onSessionChange: vi.fn() })

describe('what counts as a login_required answer', () => {
  it('is the CLI\'s JSON, an object with the flag, or the gate\'s own message', () => {
    for (const v of [LOGIN_REQUIRED, { login_required: true }, GATE_MESSAGE]) expect(loginRequiredIn(v)).toBe(true)
    for (const e of [new Error(GATE_MESSAGE), GATE_MESSAGE]) expect(loginRequiredInError(e)).toBe(true)
  })

  it('is not a payload that merely mentions it, nor an ordinary answer, nor a big string', () => {
    const quiet = [JSON.stringify({ rows: [{ error: 'login_required: Log in to monoes.me first' }] }), JSON.stringify({ login_required: false }),
      '{"login_required": broken', ['login_required'], null, 42, 'x'.repeat(100000) + 'login_required']
    for (const v of quiet) expect(loginRequiredIn(v)).toBe(false)
    for (const e of [new Error('workflow failed: login_required: Log in to monoes.me first'), undefined]) expect(loginRequiredInError(e)).toBe(false)
  })
})

describe('watchBindings', () => {
  it('reports a login_required answer from any binding, and a rejection with the gate\'s message, and passes both through', async () => {
    const err = new Error(GATE_MESSAGE)
    const win = appWith({
      ListWorkflows: vi.fn(() => Promise.resolve(LOGIN_REQUIRED)), GetPeople: vi.fn(() => Promise.resolve('[]')),
      GetRecentExecutions: vi.fn(() => Promise.reject(err)), Other: vi.fn(() => Promise.reject(new Error('disk full'))),
    })
    const cb = callbacks()
    watchBindings(cb, win)
    const App = win.go.main.App

    expect(await App.GetPeople(1, 2)).toBe('[]')
    expect(cb.onLoginRequired).not.toHaveBeenCalled()
    expect(await App.ListWorkflows()).toBe(LOGIN_REQUIRED)
    expect(cb.onLoginRequired).toHaveBeenCalledTimes(1)
    await expect(App.GetRecentExecutions()).rejects.toBe(err)
    expect(cb.onLoginRequired).toHaveBeenCalledTimes(2)
    await expect(App.Other()).rejects.toThrow('disk full')
    expect(cb.onLoginRequired).toHaveBeenCalledTimes(2)
  })

  it('looks again after a logout settles, and never wraps the gate\'s own status call', async () => {
    const status = vi.fn(() => Promise.resolve(LOGIN_REQUIRED))
    const win = appWith({ AccountStatus: status, LibraryLogout: vi.fn(() => Promise.resolve('{"logged_out":true}')) })
    const cb = callbacks()
    watchBindings(cb, win)
    expect(win.go.main.App.AccountStatus).toBe(status)
    await win.go.main.App.AccountStatus()
    expect(cb.onLoginRequired).not.toHaveBeenCalled()
    await win.go.main.App.LibraryLogout()
    expect(cb.onSessionChange).toHaveBeenCalledTimes(1)
  })

  it('wraps once, the returned function puts every binding back, and without bindings it does nothing', async () => {
    const original = vi.fn(() => Promise.resolve(LOGIN_REQUIRED))
    const win = appWith({ ListWorkflows: original })
    const cb = callbacks()
    const stop = watchBindings(cb, win)
    const wrapped = win.go.main.App.ListWorkflows
    expect(wrapped).not.toBe(original)
    watchBindings(callbacks(), win) // a second start (StrictMode) wraps nothing again
    expect(win.go.main.App.ListWorkflows).toBe(wrapped)

    stop()
    expect(win.go.main.App.ListWorkflows).toBe(original)
    await win.go.main.App.ListWorkflows()
    expect(cb.onLoginRequired).not.toHaveBeenCalled()
    expect(() => watchBindings(callbacks(), {})()).not.toThrow()
  })
})
```

- [ ] **Step 3: Run them and see them fail.** `npx vitest run --maxWorkers=3 src/services/account.test.js src/lib/accountBindingWatch.test.js`. Expected: `Test Files  2 failed (2)`, `Tests  no tests`, each file failing to load with `Error: Cannot find module '/src/services/account.js' imported from …/account.test.js` and `Error: Cannot find module './accountBindingWatch.js' imported from …/accountBindingWatch.test.js`.

- [ ] **Step 4: Create `wails-app/frontend/src/services/account.js`:**

```js
// The monoes.me account bindings. status() resolves to the page's one reading of
// `account status`: {status} when the CLI answered a document this app knows,
// else {failure: {cause, message, enforced, enforce_from}}. It never rejects and
// never turns something it could not read into a status: the gate fails closed
// (spec §6.5). enforced says whether this build already enforces; a failure
// that cannot say (the binding itself failed) counts as enforced.
import * as GoApp from '../wailsjs/go/main/App'
import { subscribeEvent } from './api.js'

const STATES = ['ok', 'grace', 'locked']
const CAUSES = ['cli_not_found', 'cli_too_old', 'cli_failed'] // wails-app/app_account.go

const failure = (cause, message = '', more = {}) => ({ failure: { cause, message, enforced: true, ...more } })

// answerOf reads what AccountStatus returned: a status document (schema 1, a
// state this app knows), the Go side's coded failure, or neither.
export function answerOf(raw) {
  let v = raw
  if (typeof raw === 'string') {
    try { v = JSON.parse(raw) } catch { return failure('cli_failed', 'monoagentcli answered with something unreadable') }
  }
  if (v && typeof v === 'object' && v.v === 1 && STATES.includes(v.state)) return { status: v }
  if (v && typeof v.error === 'string') {
    return failure(CAUSES.includes(v.code) ? v.code : 'cli_failed', v.error, { enforced: v.enforced !== false, enforce_from: v.enforce_from })
  }
  return failure('cli_too_old')
}

// run: the CLI's JSON, or {error}; never rejects, like services/library.js.
const run = (call) => Promise.resolve().then(call).then(r => (typeof r === 'string' ? JSON.parse(r) : r))
  .catch(e => ({ error: e?.message || String(e) }))

export const account = {
  status: () => Promise.resolve().then(() => GoApp.AccountStatus()).then(answerOf)
    .catch(e => failure('cli_failed', e?.message || String(e))),
  login: () => run(() => GoApp.AccountLogin()),
  cancelLogin: () => run(() => GoApp.AccountLoginCancel()),
  sendCode: (email) => run(() => GoApp.AccountLoginEmailSend(email)),
  verifyCode: (email, code) => run(() => GoApp.AccountLoginEmailVerify(email, code)),
  logout: () => run(() => GoApp.AccountLogout()),
}

// "account:login" carries the CLI's login progress ({kind:"url", url} first).
export const onAccountLogin = (callback) => subscribeEvent('account:login', callback)
```

- [ ] **Step 5: Create `wails-app/frontend/src/lib/accountBindingWatch.js`:**

```js
// Notices when a Wails binding answers "log in to monoes.me first", so the
// account gate can look again (spec §6.5: "re-checks on every login_required
// answer from any binding"). The generated App.js calls
// window.go.main.App[name] at call time, so wrapping that object once covers
// every binding without touching a caller. Results and rejections pass through
// unchanged; the watch only reads them.
const LOGIN_FIRST = /^Log in to monoes\.me first/
// A login_required answer is a few hundred bytes; never parse a data URL.
const MAX_INSPECTED = 8192
// Bindings that end or change the session: look again once they settle.
const SESSION_CHANGING = new Set(['AccountLogout', 'LibraryLogout'])

// loginRequiredIn: a result that is {login_required: true} (an object, or the
// CLI's JSON text) or text that begins with the gate's message.
export function loginRequiredIn(value) {
  if (typeof value === 'string') {
    if (value.length > MAX_INSPECTED) return false
    if (LOGIN_FIRST.test(value)) return true
    if (!value.includes('login_required')) return false
    try { return JSON.parse(value)?.login_required === true } catch { return false }
  }
  return !!value && typeof value === 'object' && !Array.isArray(value) && value.login_required === true
}

export function loginRequiredInError(e) {
  const message = typeof e === 'string' ? e : e?.message
  return typeof message === 'string' && LOGIN_FIRST.test(message)
}

// watchBindings wraps every method of window.go.main.App, except the account
// status call the gate itself makes. It returns the function that restores
// them; without the Wails bindings (a test, a browser) it does nothing.
export function watchBindings({ onLoginRequired, onSessionChange }, root = globalThis.window) {
  const app = root?.go?.main?.App
  if (!app || app.__accountWatch) return () => {}
  const originals = {}
  for (const name of Object.keys(app)) {
    const original = app[name]
    if (typeof original !== 'function' || name === 'AccountStatus') continue
    originals[name] = original
    app[name] = function (...args) {
      const out = original.apply(this, args)
      if (!out || typeof out.then !== 'function') return out
      return out.then(
        (value) => {
          if (loginRequiredIn(value)) onLoginRequired()
          else if (SESSION_CHANGING.has(name)) onSessionChange()
          return value
        },
        (e) => {
          if (loginRequiredInError(e)) onLoginRequired()
          throw e
        },
      )
    }
  }
  Object.defineProperty(app, '__accountWatch', { value: true, configurable: true })
  return () => {
    for (const [name, original] of Object.entries(originals)) app[name] = original
    delete app.__accountWatch
  }
}
```

- [ ] **Step 6: Run the tests.** Same command. Expected: `Test Files  2 passed (2)`, `Tests  10 passed (10)`.

- [ ] **Step 7: Commit:**

```bash
git add wails-app/frontend/src/services/account.js wails-app/frontend/src/services/account.test.js wails-app/frontend/src/lib/accountBindingWatch.js wails-app/frontend/src/lib/accountBindingWatch.test.js
git commit -m "feat(gui): the account status answer and the binding watch" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 5: The gate controller (frontend)

**Files:**
- Create: `wails-app/frontend/src/lib/accountGate.js`, `wails-app/frontend/src/lib/accountGate.test.js`

**Interfaces:**
- Consumes: `watchBindings` (Task 4); an injected `status()` resolving to `account.status()`'s answer.
- Produces: `createAccountGate({status, now?}) → {check(why), start() → stop, subscribe(listener) → unsubscribe, getSnapshot()}` with `why` one of `'start' | 'focus' | 'login_required' | 'manual'`; `viewOf(snapshot) → {phase, locked, status?, failure?, grace?, warn?, enforceFrom?}`; `useAccountView(gate)`; the constants `FOCUS_GAP_MS` (5000), `LOGIN_REQUIRED_GAP_MS` (3000), `RETRY_MS` (2000).

- [ ] **Step 1: Write the failing test** `wails-app/frontend/src/lib/accountGate.test.js`:

```js
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { createAccountGate, viewOf, FOCUS_GAP_MS, LOGIN_REQUIRED_GAP_MS, RETRY_MS } from './accountGate.js'

const DATE = '2026-10-26T00:00:00Z'
const st = (over = {}) => ({ status: { v: 1, state: 'ok', reason: '', plan: 'free', enforced: true, enforce_from: DATE, ...over } })
const FAIL = (cause = 'cli_failed', over = {}) => ({ failure: { cause, message: 'x', enforced: true, ...over } })
const view = (answer) => viewOf({ phase: 'ready', status: answer.status ?? null, failure: answer.failure ?? null })
const locked = (gate) => viewOf(gate.getSnapshot()).locked

afterEach(() => { vi.useRealTimers() })

// A gate whose clock the test moves and whose answers it queues.
function gateWith(...answers) {
  let t = 1_000_000
  const status = vi.fn(() => Promise.resolve(answers.length > 1 ? answers.shift() : answers[0]))
  return { gate: createAccountGate({ status, now: () => t }), status, advance: (ms) => { t += ms } }
}

describe('viewOf', () => {
  it('is checking until the first answer', () => {
    expect(viewOf({ phase: 'checking', status: null, failure: null })).toEqual({ phase: 'checking', locked: false })
  })

  it('locks only a locked status that is enforced', () => {
    expect(view(st({ state: 'locked', reason: 'not_logged_in' })).locked).toBe(true)
    for (const over of [{ state: 'locked', enforced: false }, { state: 'ok' }, { state: 'grace' }]) expect(view(st(over)).locked).toBe(false)
  })

  it('locks a failure only once this build enforces; before the date it warns with the date', () => {
    for (const cause of ['cli_not_found', 'cli_too_old', 'cli_failed']) expect(view(FAIL(cause)).locked, cause).toBe(true)
    expect(view({ failure: { cause: 'cli_too_old', message: '', enforced: undefined } }).locked).toBe(true) // silent: fail closed
    const early = view(FAIL('cli_too_old', { enforced: false, enforce_from: DATE }))
    expect(early).toMatchObject({ locked: false, warn: true, enforceFrom: DATE })
  })

  it('warns in grace and before the date, and says nothing for a build with no date (dormant, D22)', () => {
    expect(view(st({ state: 'grace', reason: 'unreachable' })).grace).toBe(true)
    expect(view(st({ state: 'ok' })).grace).toBe(false)
    expect(view(st({ state: 'locked', enforced: false })).warn).toBe(true)
    expect(view(st({ state: 'ok', enforced: false })).warn).toBe(false)               // already signed in
    expect(view(st({ state: 'locked', enforced: true })).warn).toBe(false)
    const dormant = { enforced: false, enforce_from: undefined }
    for (const state of ['ok', 'grace', 'locked']) {
      expect(view(st({ state, ...dormant })), state).toMatchObject({ locked: false, grace: false, warn: false })
    }
  })
})

describe('the gate', () => {
  it('asks once at start and publishes the answer', async () => {
    const { gate, status } = gateWith(st())
    const seen = vi.fn()
    gate.subscribe(seen)
    expect(gate.getSnapshot().phase).toBe('checking')
    await gate.check('start')
    expect(status).toHaveBeenCalledTimes(1)
    expect(gate.getSnapshot()).toMatchObject({ phase: 'ready', failure: null, status: { state: 'ok' } })
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it('fails closed at the very first answer, with no retry', async () => {
    const { gate, status } = gateWith(FAIL('cli_failed'))
    await gate.check('start')
    expect(status).toHaveBeenCalledTimes(1)
    expect(locked(gate)).toBe(true)
  })

  it('shares one ask between overlapping checks, and drops a focus or a login_required answer that follows an answer closely', async () => {
    const { gate, status, advance } = gateWith(st())
    await Promise.all([gate.check('start'), gate.check('login_required'), gate.check('manual')])
    expect(status).toHaveBeenCalledTimes(1)
    advance(FOCUS_GAP_MS - 1)
    await gate.check('focus')
    expect(status).toHaveBeenCalledTimes(1)
    advance(1)
    await gate.check('focus')
    expect(status).toHaveBeenCalledTimes(2)
    advance(LOGIN_REQUIRED_GAP_MS - 1)
    await gate.check('login_required')
    expect(status).toHaveBeenCalledTimes(2)
    await gate.check('manual')
    expect(status).toHaveBeenCalledTimes(3)
    advance(LOGIN_REQUIRED_GAP_MS)
    await gate.check('login_required')
    expect(status).toHaveBeenCalledTimes(4)
  })

  it('locks when a later ask says locked, and unlocks when it says ok', async () => {
    const { gate, advance } = gateWith(st(), st({ state: 'locked', reason: 'refused' }), st())
    await gate.check('start')
    advance(10_000)
    await gate.check('focus')
    expect(locked(gate)).toBe(true)
    await gate.check('manual')
    expect(locked(gate)).toBe(false)
  })

  it('repeats a failed ask once while the app is up, and locks only if it fails again', async () => {
    vi.useFakeTimers()
    const hiccup = gateWith(st(), FAIL(), st())
    await hiccup.gate.check('start')
    hiccup.advance(10_000)
    const first = hiccup.gate.check('focus')
    await vi.advanceTimersByTimeAsync(RETRY_MS)
    await first
    expect(hiccup.status).toHaveBeenCalledTimes(3)
    expect(locked(hiccup.gate)).toBe(false)

    const down = gateWith(st(), FAIL(), FAIL())
    await down.gate.check('start')
    down.advance(10_000)
    const second = down.gate.check('focus')
    expect(locked(down.gate)).toBe(false) // still up during the wait
    await vi.advanceTimersByTimeAsync(RETRY_MS)
    await second
    expect(locked(down.gate)).toBe(true)
  })

  it('does not retry a failure that cannot be a hiccup', async () => {
    const { gate, status, advance } = gateWith(st(), FAIL('cli_too_old'))
    await gate.check('start')
    advance(10_000)
    await gate.check('focus')
    expect(status).toHaveBeenCalledTimes(2)
    expect(locked(gate)).toBe(true)
  })

  it('start() asks now and on the window focus, and stop() ends it', async () => {
    const { gate, status, advance } = gateWith(st())
    const stop = gate.start()
    await vi.waitFor(() => expect(gate.getSnapshot().phase).toBe('ready'))
    advance(FOCUS_GAP_MS)
    window.dispatchEvent(new Event('focus'))
    await vi.waitFor(() => expect(status).toHaveBeenCalledTimes(2))
    stop()
    advance(FOCUS_GAP_MS)
    window.dispatchEvent(new Event('focus'))
    await Promise.resolve()
    expect(status).toHaveBeenCalledTimes(2)
  })
})
```

- [ ] **Step 2: Run it and see it fail.** `cd wails-app/frontend && npx vitest run --maxWorkers=3 src/lib/accountGate.test.js`. Expected: `Test Files  1 failed (1)`, `Error: Failed to resolve import "./accountGate.js" from "src/lib/accountGate.test.js". Does the file exist?`.

- [ ] **Step 3: Create `wails-app/frontend/src/lib/accountGate.js`:**

```js
// The account gate's one source of truth: what the machine's monoes.me sign-in
// allows right now (spec §6.5). It asks `account status` once at start, again
// when the window gets the focus, and whenever any binding answers
// login_required. A failed ask locks (the gate fails closed); one that fails
// while the app is up is repeated once first, so a CLI that was merely busy
// does not take a working session away.
import { useSyncExternalStore } from 'react'
import { watchBindings } from './accountBindingWatch.js'

export const FOCUS_GAP_MS = 5000           // a focus this soon after an answer does not ask again
export const LOGIN_REQUIRED_GAP_MS = 3000  // nor does a login_required answer
export const RETRY_MS = 2000               // the wait before a failed ask is repeated

// viewOf: what the page shows for one snapshot {phase, status, failure}. Locked
// is the gate; grace and warn are the banners over the app. A build with no
// enforcement date reaches here with no enforce_from: nothing locks and nothing
// warns (D22). Before the date a machine with no usable sign-in is told the date.
export function viewOf(snap) {
  if (snap.phase === 'checking') return { phase: 'checking', locked: false }
  const { status, failure } = snap
  if (failure) {
    const enforced = failure.enforced !== false
    return { phase: 'ready', failure, locked: enforced, warn: !enforced && !!failure.enforce_from, enforceFrom: failure.enforce_from }
  }
  const dated = !!status.enforce_from
  const enforced = status.enforced === true
  return {
    phase: 'ready',
    status,
    enforceFrom: status.enforce_from,
    locked: enforced && status.state === 'locked',
    grace: dated && status.state === 'grace',
    warn: dated && !enforced && status.state === 'locked',
  }
}

// createAccountGate({ status }): status() resolves to account.status()'s answer.
export function createAccountGate({ status, now = Date.now }) {
  let snap = { phase: 'checking', status: null, failure: null }
  let answeredAt = 0
  let inflight = null
  const listeners = new Set()

  const publish = (next) => { snap = next; listeners.forEach(l => l()) }

  async function ask() {
    let answer = await status()
    if (answer.failure?.cause === 'cli_failed' && snap.phase === 'ready' && !viewOf(snap).locked) {
      await new Promise(resolve => setTimeout(resolve, RETRY_MS))
      answer = await status()
    }
    answeredAt = now()
    publish({ phase: 'ready', status: answer.status ?? null, failure: answer.failure ?? null })
  }

  // check(why): why is 'start', 'focus', 'login_required' or 'manual'. Calls
  // that overlap share one ask; a focus or login_required answer that follows an
  // answer closely is dropped.
  function check(why = 'manual') {
    if (inflight) return inflight
    const since = now() - answeredAt
    if (why === 'focus' && since < FOCUS_GAP_MS) return Promise.resolve()
    if (why === 'login_required' && since < LOGIN_REQUIRED_GAP_MS) return Promise.resolve()
    inflight = ask().finally(() => { inflight = null })
    return inflight
  }

  // start asks now and keeps asking on focus and on binding answers; it returns stop.
  function start() {
    check('start')
    const onFocus = () => check('focus')
    const onVisible = () => { if (!document.hidden) check('focus') }
    window.addEventListener('focus', onFocus)
    document.addEventListener('visibilitychange', onVisible)
    const unwatch = watchBindings({
      onLoginRequired: () => check('login_required'),
      onSessionChange: () => check('manual'),
    })
    return () => {
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('visibilitychange', onVisible)
      unwatch()
    }
  }

  return {
    check,
    start,
    subscribe: (listener) => { listeners.add(listener); return () => listeners.delete(listener) },
    getSnapshot: () => snap,
  }
}

export function useAccountView(gate) {
  return viewOf(useSyncExternalStore(gate.subscribe, gate.getSnapshot))
}
```

- [ ] **Step 4: Run the test.** Same command. Expected: `Tests  11 passed (11)`.

- [ ] **Step 5: Commit:**

```bash
git add wails-app/frontend/src/lib/accountGate.js wails-app/frontend/src/lib/accountGate.test.js
git commit -m "feat(gui): the account gate controller" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 6: The gate, the banners, the shell and the strings (frontend)

**Files:**
- Create: `wails-app/frontend/src/components/account/AccountGate.jsx`, `AccountBanners.jsx`, `AccountShell.jsx`, `AccountShell.render.test.jsx`, `wails-app/frontend/src/locales/accountKeys.test.js`
- Modify: `wails-app/frontend/src/components/library/LogInToMonoesButton.jsx` (lines 7, 17, 33, 37, 51, 55, 60, 66, 70), `wails-app/frontend/src/locales/en.json`, `wails-app/frontend/src/locales/es.json` (end of file)

**Interfaces:**
- Consumes: `createAccountGate`, `useAccountView` (Task 5); `account`, `onAccountLogin` (Task 4); `AppSelfUpdate` from the bindings; `subscribeEvent` from `services/api.js`; `LogInToMonoesButton`.
- Produces: `LogInToMonoesButton({…, service = library, onLogin = onLibraryLogin})`; `AccountShell({gate, renderShell}) `, where `renderShell(banners)` returns the app; `AccountGate({gate, view, update})`; `GraceBanner({status})`, `WarnBanner({enforceFrom, onSignedIn})`; the locale keys `account.*`.

- [ ] **Step 1: Write the failing tests.** `wails-app/frontend/src/locales/accountKeys.test.js`:

```js
// Every account.* key the gate and its banners use exists in English and
// Spanish, both locales carry the same account keys with the same placeholders,
// none is dead, and the Spanish is not a copy of the English. Keys are written
// out in full in the sources (no template literals), which is what lets this
// scan see them.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [[`${p}${k}`, v]]))
const keysOf = (locale) => flat(locale.account, 'account.').map(([k]) => k).sort()
const valueOf = (locale, key) => Object.fromEntries(flat(locale))[key]
// i18next plurals: "x_one"/"x_other" satisfy t('x', { count }).
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))
const base = (k) => k.replace(/_(one|other)$/, '')
const placeholders = (s) => [...String(s).matchAll(/\{\{(\w+)\}\}/g)].map(m => m[1]).sort()

const dir = join(__dirname, '..', 'components', 'account')
const sources = readdirSync(dir).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(dir, f))
const used = [...new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](account\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]

describe('account i18n', () => {
  it('en and es have the same account keys', () => {
    expect(keysOf(en).length).toBeGreaterThan(30)
    expect(keysOf(es)).toEqual(keysOf(en))
  })

  it('every key the gate uses exists in en and es', () => {
    expect(used.length).toBeGreaterThan(25)
    expect(used.filter(k => !has(keysOf(en), k))).toEqual([])
    expect(used.filter(k => !has(keysOf(es), k))).toEqual([])
  })

  it('no account key is left unused', () => {
    expect(keysOf(en).filter(k => !used.includes(base(k)))).toEqual([])
  })

  it('both languages use the same placeholders in each string', () => {
    for (const key of keysOf(en)) {
      expect(placeholders(valueOf(es, key)), key).toEqual(placeholders(valueOf(en, key)))
    }
  })

  it('the Spanish is translated, not copied', () => {
    expect(keysOf(en).filter(k => valueOf(es, k) === valueOf(en, k))).toEqual([])
  })
})
```

`wails-app/frontend/src/components/account/AccountShell.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'

const go = vi.hoisted(() => ({
  AccountStatus: vi.fn(), AccountLogin: vi.fn(), AccountLoginCancel: vi.fn(), AccountLogout: vi.fn(),
  AccountLoginEmailSend: vi.fn(), AccountLoginEmailVerify: vi.fn(), AppSelfUpdate: vi.fn(),
}))
vi.mock('../../wailsjs/go/main/App', () => go)

import i18n from '../../i18n.js'
import { createAccountGate } from '../../lib/accountGate.js'
import { account } from '../../services/account.js'
import AccountShell from './AccountShell.jsx'

const j = (v) => Promise.resolve(JSON.stringify(v))
const DATE = '2026-10-26T00:00:00Z'
const doc = (over = {}) => ({ v: 1, state: 'ok', reason: '', plan: 'free', enforced: true, enforce_from: DATE, ...over })
const LOCKED = doc({ state: 'locked', reason: 'not_logged_in' })
const LOGIN_REQUIRED = { error: 'Log in to monoes.me first: monoagentcli account login', code: 'auth_or_connection', login_required: true }
const answer = (v) => go.AccountStatus.mockImplementation(() => j(v))
const WHEN = { dateStyle: 'medium', timeStyle: 'short' }

// The app, standing in for the real shell: it wears the banners it is given.
const shell = (banners) => <div data-testid="shell">{banners}<span>the app</span></div>

function setup(now = () => Date.now()) {
  const gate = createAccountGate({ status: account.status, now })
  return render(<AccountShell gate={gate} renderShell={shell} />)
}
const gateRegion = (name) => screen.findByRole('region', { name })
const signIn = (scope) => scope.getByRole('button', { name: /Log in to monoes/ })

beforeEach(async () => {
  await i18n.changeLanguage('en')
  answer(doc())
  go.AccountLogin.mockImplementation(() => j({ v: 1, state: 'ok' }))
})
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers() })

describe('the account gate over the app', () => {
  it('shows a splash until the first answer, then the app with no banner for a signed-in machine', async () => {
    let reply
    go.AccountStatus.mockImplementation(() => new Promise(resolve => { reply = resolve }))
    setup()
    expect(screen.getByRole('status')).toHaveTextContent('Checking your monoes.me account…')
    expect(screen.queryByTestId('shell')).not.toBeInTheDocument()
    await waitFor(() => expect(go.AccountStatus).toHaveBeenCalledTimes(1))
    await act(async () => { reply(JSON.stringify(doc())) })
    const app = await screen.findByTestId('shell')
    expect(within(app).queryByRole('status')).not.toBeInTheDocument()
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
  })

  it('renders the gate instead of the app when locked, with the sign-in controls', async () => {
    answer(LOCKED)
    setup()
    const region = within(await gateRegion('Sign in to MonoAgent'))
    expect(signIn(region)).toBeInTheDocument()
    expect(region.getByText('Use an email code instead')).toBeInTheDocument()
    expect(screen.queryByTestId('shell')).not.toBeInTheDocument()
  })

  it('says why for each reason, and offers the update where signing in again would not help', async () => {
    const cases = { expired: ['Your sign-in has expired', true], refused: ['monoes.me ended this sign-in', true],
      clock_skew: ["This computer's clock looks wrong", true], key_unknown: ['This version cannot verify your sign-in', false],
      unconfirmed: ['This computer stopped using its saved sign-in', true] }
    for (const [reason, [title, canSignIn]] of Object.entries(cases)) {
      answer(doc({ state: 'locked', reason }))
      const { unmount } = setup()
      const region = within(await gateRegion(title))
      expect(!!region.queryByRole('button', { name: /Log in to monoes/ }), reason).toBe(canSignIn)
      expect(!!region.queryByRole('button', { name: 'Update MonoAgent' }), reason).toBe(!canSignIn)
      unmount()
    }
  })

  it('does not lock before the enforcement date: a warn banner with the date and a way to sign in now', async () => {
    answer(doc({ state: 'locked', enforced: false }))
    setup()
    const app = within(await screen.findByTestId('shell'))
    const date = new Date(DATE).toLocaleDateString('en', { year: 'numeric', month: 'long', day: 'numeric' })
    expect(app.getByRole('status')).toHaveTextContent(`A monoes.me sign-in will be required from ${date}.`)

    answer(doc())
    fireEvent.click(app.getByRole('button', { name: 'Sign in' }))
    fireEvent.click(signIn(within(await screen.findByRole('dialog'))))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByTestId('shell')).toBeInTheDocument()
    expect(screen.queryByText(/will be required from/)).not.toBeInTheDocument()
  })

  it('says nothing, and never locks, for a build with no date (dormant), whatever the state', async () => {
    for (const state of ['locked', 'grace']) {
      answer(doc({ state, reason: 'unreachable', enforced: false, enforce_from: undefined, grace_until: '2026-10-06T21:20:00Z' }))
      const { unmount } = setup()
      expect(within(await screen.findByTestId('shell')).queryByRole('status')).not.toBeInTheDocument()
      unmount()
    }
  })

  it('wears a dismissible grace banner with the time left, and names a key store that cannot be opened', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-06T08:00:00Z'))
    const graceUntil = '2026-10-06T21:20:00Z' // 13 h 20 min away
    answer(doc({ state: 'grace', reason: 'unreachable', grace_until: graceUntil }))
    const first = setup()
    const banner = await screen.findByRole('status')
    const when = new Date(graceUntil).toLocaleString('en', WHEN)
    expect(banner).toHaveTextContent(`monoes.me is unreachable. Your sign-in keeps working offline until ${when} (13 hours left).`)
    fireEvent.click(within(banner).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.getByTestId('shell')).toBeInTheDocument()
    first.unmount()

    answer(doc({ state: 'grace', reason: 'keyring_unavailable', grace_until: '2026-10-06T08:41:00Z' }))
    setup()
    expect(await screen.findByRole('status')).toHaveTextContent(/cannot open the key store.*\(41 minutes left\)/)
  })

  // A24: this computer dropped its saved sign-in because a refresh may have reached monoes.me without its answer
  // arriving. monoes.me is not the problem and "offline" is not the fix: the banner says that the sign-in cannot
  // be renewed, until when it works, and offers to sign in again, which makes the machine whole.
  it('says that this computer can no longer renew its sign-in, and offers to sign in again', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-06T08:00:00Z'))
    const graceUntil = '2026-10-06T21:20:00Z' // 13 h 20 min away
    answer(doc({ state: 'grace', reason: 'unconfirmed', grace_until: graceUntil }))
    setup()
    const banner = await screen.findByRole('status')
    const when = new Date(graceUntil).toLocaleString('en', WHEN)
    expect(banner).toHaveTextContent(`monoes.me may have received a refresh whose answer never arrived, so this computer can no longer renew its sign-in. It works until ${when} (13 hours left); sign in again before then.`)
    expect(banner).not.toHaveTextContent('unreachable')

    answer(doc())
    fireEvent.click(within(banner).getByRole('button', { name: 'Sign in' }))
    fireEvent.click(signIn(within(await screen.findByRole('dialog'))))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByTestId('shell')).toBeInTheDocument()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  // A newer monoagentcli may report a reason this app does not know: the gate and the banner fall back to
  // generic words and never to a blank or a raw key, and a locked machine can always sign in.
  it('uses generic words for a reason this app does not know', async () => {
    answer(doc({ state: 'locked', reason: 'from_the_future' }))
    const first = setup()
    const region = within(await gateRegion('Your sign-in could not be verified'))
    expect(signIn(region)).toBeInTheDocument()
    first.unmount()

    answer(doc({ state: 'grace', reason: 'from_the_future', grace_until: '2026-10-06T21:20:00Z' }))
    setup()
    expect(await screen.findByRole('status')).toHaveTextContent('monoes.me is unreachable.')
  })
})

describe('returning to the gate', () => {
  afterEach(() => { delete window.go; delete window.runtime })

  it('goes back to the gate when any binding answers login_required', async () => {
    window.go = { main: { App: { ListWorkflows: vi.fn(() => Promise.resolve(JSON.stringify(LOGIN_REQUIRED))) } } }
    window.runtime = { EventsOnMultiple: () => () => {} } // the sign-in button subscribes to events
    let t = 1_000_000
    setup(() => t)
    await screen.findByTestId('shell')

    t += 10_000
    answer(doc({ state: 'locked', reason: 'refused' }))
    await act(async () => { await window.go.main.App.ListWorkflows() })
    expect(await gateRegion('monoes.me ended this sign-in')).toBeInTheDocument()
    expect(screen.queryByTestId('shell')).not.toBeInTheDocument()
  })

  it('goes back to the gate when the window gets the focus after the session was refused', async () => {
    let t = 1_000_000
    setup(() => t)
    await screen.findByTestId('shell')
    t += 10_000
    answer(LOCKED)
    await act(async () => { window.dispatchEvent(new Event('focus')) })
    expect(await gateRegion('Sign in to MonoAgent')).toBeInTheDocument()
  })

  it('returns to the app when the person signs in on the gate', async () => {
    answer(LOCKED)
    setup()
    const region = within(await gateRegion('Sign in to MonoAgent'))
    answer(doc())
    fireEvent.click(signIn(region))
    expect(await screen.findByTestId('shell')).toBeInTheDocument()
    expect(go.AccountLogin).toHaveBeenCalledTimes(1)
  })
})

describe('failing closed', () => {
  it('locks, with the cause, when `account status` cannot be run, and offers retry only', async () => {
    answer({ error: 'monoagentcli binary not found', code: 'cli_not_found' })
    setup()
    const region = within(await gateRegion('MonoAgent cannot find its command-line tool'))
    expect(region.getByText('Details: monoagentcli binary not found')).toBeInTheDocument()
    expect(region.queryByRole('button', { name: 'Update MonoAgent' })).not.toBeInTheDocument()
    answer(doc())
    fireEvent.click(region.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByTestId('shell')).toBeInTheDocument()
  })

  it('locks on a CLI that answers something this app does not know, and offers the update', async () => {
    answer({ v: 2, state: 'ok' })
    setup()
    const region = within(await gateRegion("MonoAgent's command-line tool is out of date"))
    go.AppSelfUpdate.mockImplementation(() => Promise.resolve({ success: false, error: 'checksum mismatch' }))
    fireEvent.click(region.getByRole('button', { name: 'Update MonoAgent' }))
    expect(await region.findByRole('alert')).toHaveTextContent('The update failed: checksum mismatch')
    go.AppSelfUpdate.mockImplementation(() => Promise.resolve({ success: true, new_version: 'v9.9.9' }))
    fireEvent.click(region.getByRole('button', { name: 'Update MonoAgent' }))
    expect(await region.findByText('Updated. MonoAgent restarts to finish.')).toBeInTheDocument()
    expect(go.AppSelfUpdate).toHaveBeenCalledTimes(2)
  })

  it('does not lock on a failure before this build enforces: it warns with the date', async () => {
    answer({ error: 'old CLI', code: 'cli_too_old', enforced: false, enforce_from: DATE })
    setup()
    expect(within(await screen.findByTestId('shell')).getByRole('status')).toHaveTextContent('A monoes.me sign-in will be required from')
  })

  it('offers the update on any locked screen once the app has found a release', async () => {
    let found
    window.runtime = { EventsOnMultiple: (name, cb) => { if (name === 'update:available') found = cb; return () => {} } }
    window.go = {}
    answer(LOCKED)
    setup()
    const region = within(await gateRegion('Sign in to MonoAgent'))
    expect(region.queryByRole('button', { name: 'Update MonoAgent' })).not.toBeInTheDocument()
    await act(async () => { found({ update_available: true, latest_version: 'v9.9.9' }) })
    expect(region.getByText('Version v9.9.9 is available.')).toBeInTheDocument()
    expect(region.getByRole('button', { name: 'Update MonoAgent' })).toBeInTheDocument()
    delete window.go; delete window.runtime
  })

  it('offers no update for a missing CLI, whatever release is known: the update installs through that CLI', async () => {
    let found
    window.runtime = { EventsOnMultiple: (name, cb) => { if (name === 'update:available') found = cb; return () => {} } }
    window.go = {}
    answer({ error: 'monoagentcli binary not found', code: 'cli_not_found' })
    setup()
    const region = within(await gateRegion('MonoAgent cannot find its command-line tool'))
    await act(async () => { found({ update_available: true, latest_version: 'v9.9.9' }) })
    expect(region.queryByText('Version v9.9.9 is available.')).not.toBeInTheDocument()
    expect(region.queryByRole('button', { name: 'Update MonoAgent' })).not.toBeInTheDocument()
    expect(region.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
    delete window.go; delete window.runtime
  })

  it('locks when the status binding itself fails', async () => {
    go.AccountStatus.mockImplementation(() => Promise.reject(new Error('binding missing')))
    setup()
    expect(await gateRegion('MonoAgent could not check your sign-in')).toBeInTheDocument()
    expect(screen.getByText('Details: binding missing')).toBeInTheDocument()
  })
})

describe('in Spanish', () => {
  beforeEach(async () => { await i18n.changeLanguage('es') })

  it('words the gate, the failure and both banners', async () => {
    answer(LOCKED)
    const a = setup()
    expect(within(await gateRegion('Inicia sesión en MonoAgent')).getByRole('button', { name: /Iniciar sesión en monoes/ })).toBeInTheDocument()
    a.unmount()

    answer({ v: 2, state: 'ok' })
    const b = setup()
    expect(within(await gateRegion('La herramienta de línea de comandos de MonoAgent está desactualizada')).getByRole('button', { name: 'Actualizar MonoAgent' })).toBeInTheDocument()
    b.unmount()

    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-06T08:00:00Z'))
    answer(doc({ state: 'grace', reason: 'unreachable', grace_until: '2026-10-06T09:00:00Z' }))
    const c = setup()
    expect(await screen.findByRole('status')).toHaveTextContent(/No se puede conectar con monoes\.me\. Tu sesión sigue funcionando sin conexión hasta el .*\(queda 1 hora\)/)
    c.unmount()

    answer(doc({ state: 'locked', enforced: false, enforce_from: '2026-10-26T00:00:00Z' }))
    setup()
    expect(await screen.findByRole('status')).toHaveTextContent('hará falta una sesión de monoes.me')
  })
})
```

- [ ] **Step 2: Run them and see them fail.** `cd wails-app/frontend && npx vitest run --maxWorkers=3 src/components/account src/locales/accountKeys.test.js`. Expected: `Test Files  2 failed (2)`: the render test with `Error: Failed to resolve import "./AccountShell.jsx" from "src/components/account/AccountShell.render.test.jsx". Does the file exist?`, and `accountKeys.test.js` with `Tests  5 failed (5)` (`TypeError: Cannot convert undefined or null to object`, `AssertionError: expected 0 to be greater than 25`).

- [ ] **Step 3: Let the sign-in button drive a given service.** In `wails-app/frontend/src/components/library/LogInToMonoesButton.jsx` make these replacements (the `import { library, onLibraryLogin }` line stays: they are the defaults):
  - after line 7 (`// large: the library's login gate — a big button, the email code as a link.`) add the two lines `// service/onLogin: where signing in goes (default: the library's; the account` and `// gate passes services/account.js, the same session through its own bindings).`
  - line 17: `export default function LogInToMonoesButton({ status: statusProp, onStatusChange, compact = false, large = false }) {` becomes `export default function LogInToMonoesButton({ status: statusProp, onStatusChange, compact = false, large = false, service = library, onLogin = onLibraryLogin }) {`
  - line 33: `library.status(true).then(` becomes `service.status(true).then(`
  - line 37: `useEffect(() => onLibraryLogin(ev =>` becomes `useEffect(() => onLogin(ev =>`
  - line 51: `await library.login()` becomes `await service.login()`
  - line 55: `await library.cancelLogin()` becomes `await service.cancelLogin()`
  - line 60: `await library.sendCode(` becomes `await service.sendCode(`
  - line 66: `await library.verifyCode(` becomes `await service.verifyCode(`
  - line 70: `await library.logout()` becomes `await service.logout()`

- [ ] **Step 4: Add the strings.** In `wails-app/frontend/src/locales/en.json` replace the last three lines:

```json
    "openAsBubbleNamed": "Open {{name}} as a chat bubble"
  }
}
```

with:

```json
    "openAsBubbleNamed": "Open {{name}} as a chat bubble"
  },
  "account": {
    "checking": "Checking your monoes.me account…",
    "gate": {
      "notLoggedIn": {
        "title": "Sign in to MonoAgent",
        "body": "MonoAgent needs a monoes.me account. Sign in to keep going; your data stays on this computer."
      },
      "expired": {
        "title": "Your sign-in has expired",
        "body": "MonoAgent could not reach monoes.me for more than a day. Check your connection, then sign in again."
      },
      "refused": {
        "title": "monoes.me ended this sign-in",
        "body": "This account can no longer use MonoAgent. If you think that is a mistake, contact monoes.me support. You can sign in with another account."
      },
      "clock": {
        "title": "This computer's clock looks wrong",
        "body": "MonoAgent saw a time that does not fit monoes.me's clock. Set the date and time right, then sign in again."
      },
      "keyUnknown": {
        "title": "This version cannot verify your sign-in",
        "body": "monoes.me signs logins with a newer key than this version of MonoAgent knows. Update MonoAgent, then sign in again."
      },
      "unconfirmed": {
        "title": "This computer stopped using its saved sign-in",
        "body": "monoes.me may have received a refresh whose answer never arrived, so this computer stopped using its saved sign-in to protect your other installs. Sign in again on this computer."
      },
      "invalid": {
        "title": "Your sign-in could not be verified",
        "body": "The saved sign-in is damaged or was not issued for MonoAgent. Sign in again."
      },
      "cliNotFound": {
        "title": "MonoAgent cannot find its command-line tool",
        "body": "MonoAgent needs monoagentcli to check your sign-in and cannot find it. Reinstall MonoAgent, or point MONOAGENTCLI_BIN at a current monoagentcli."
      },
      "cliTooOld": {
        "title": "MonoAgent's command-line tool is out of date",
        "body": "The monoagentcli this app found does not support monoes.me accounts, or speaks a newer version than this app. Update MonoAgent to bring both up to date."
      },
      "cliFailed": {
        "title": "MonoAgent could not check your sign-in",
        "body": "monoagentcli did not give an answer. Try again, or update MonoAgent."
      },
      "details": "Details: {{message}}",
      "retry": "Try again"
    },
    "update": {
      "button": "Update MonoAgent",
      "available": "Version {{version}} is available.",
      "working": "Updating…",
      "done": "Updated. MonoAgent restarts to finish.",
      "upToDate": "MonoAgent is already up to date.",
      "failed": "The update failed: {{message}}"
    },
    "grace": {
      "unreachable": "monoes.me is unreachable.",
      "serverError": "monoes.me is not answering properly.",
      "keyringUnavailable": "MonoAgent cannot open the key store to refresh your sign-in.",
      "unconfirmed": "monoes.me may have received a refresh whose answer never arrived, so this computer can no longer renew its sign-in. It works until {{when}} ({{left}}); sign in again before then.",
      "keepsWorking": "Your sign-in keeps working offline until {{when}} ({{left}})."
    },
    "time": {
      "hours_one": "{{count}} hour left",
      "hours_other": "{{count}} hours left",
      "minutes_one": "{{count}} minute left",
      "minutes_other": "{{count}} minutes left"
    },
    "warn": {
      "text": "A monoes.me sign-in will be required from {{date}}.",
      "signIn": "Sign in"
    },
    "banner": {
      "dismiss": "Dismiss",
      "close": "Close"
    }
  }
}
```

and in `es.json` replace the last three lines:

```json
    "openAsBubbleNamed": "Abrir {{name}} como burbuja de chat"
  }
}
```

with:

```json
    "openAsBubbleNamed": "Abrir {{name}} como burbuja de chat"
  },
  "account": {
    "checking": "Comprobando tu cuenta de monoes.me…",
    "gate": {
      "notLoggedIn": {
        "title": "Inicia sesión en MonoAgent",
        "body": "MonoAgent necesita una cuenta de monoes.me. Inicia sesión para continuar; tus datos siguen en este equipo."
      },
      "expired": {
        "title": "Tu sesión ha caducado",
        "body": "MonoAgent no pudo conectar con monoes.me durante más de un día. Revisa tu conexión y vuelve a iniciar sesión."
      },
      "refused": {
        "title": "monoes.me cerró esta sesión",
        "body": "Esta cuenta ya no puede usar MonoAgent. Si crees que es un error, contacta con el soporte de monoes.me. Puedes iniciar sesión con otra cuenta."
      },
      "clock": {
        "title": "El reloj de este equipo parece incorrecto",
        "body": "MonoAgent vio una hora que no encaja con el reloj de monoes.me. Ajusta la fecha y la hora y vuelve a iniciar sesión."
      },
      "keyUnknown": {
        "title": "Esta versión no puede verificar tu sesión",
        "body": "monoes.me firma las sesiones con una clave más nueva que la que conoce esta versión de MonoAgent. Actualiza MonoAgent y vuelve a iniciar sesión."
      },
      "unconfirmed": {
        "title": "Este equipo dejó de usar su sesión guardada",
        "body": "Es posible que monoes.me recibiera una renovación cuya respuesta nunca llegó, así que este equipo dejó de usar su sesión guardada para proteger tus otras instalaciones. Vuelve a iniciar sesión en este equipo."
      },
      "invalid": {
        "title": "No se pudo verificar tu sesión",
        "body": "La sesión guardada está dañada o no se emitió para MonoAgent. Vuelve a iniciar sesión."
      },
      "cliNotFound": {
        "title": "MonoAgent no encuentra su herramienta de línea de comandos",
        "body": "MonoAgent necesita monoagentcli para comprobar tu sesión y no lo encuentra. Reinstala MonoAgent o apunta MONOAGENTCLI_BIN a un monoagentcli actual."
      },
      "cliTooOld": {
        "title": "La herramienta de línea de comandos de MonoAgent está desactualizada",
        "body": "El monoagentcli que encontró la aplicación no admite cuentas de monoes.me o habla una versión más nueva que la aplicación. Actualiza MonoAgent para poner ambos al día."
      },
      "cliFailed": {
        "title": "MonoAgent no pudo comprobar tu sesión",
        "body": "monoagentcli no dio una respuesta. Inténtalo de nuevo o actualiza MonoAgent."
      },
      "details": "Detalles: {{message}}",
      "retry": "Intentar de nuevo"
    },
    "update": {
      "button": "Actualizar MonoAgent",
      "available": "Hay una versión {{version}} disponible.",
      "working": "Actualizando…",
      "done": "Actualizado. MonoAgent se reinicia para terminar.",
      "upToDate": "MonoAgent ya está al día.",
      "failed": "La actualización falló: {{message}}"
    },
    "grace": {
      "unreachable": "No se puede conectar con monoes.me.",
      "serverError": "monoes.me no responde como debe.",
      "keyringUnavailable": "MonoAgent no puede abrir el almacén de claves para renovar tu sesión.",
      "unconfirmed": "Es posible que monoes.me recibiera una renovación cuya respuesta nunca llegó, así que este equipo ya no puede renovar su sesión. Funciona hasta el {{when}} ({{left}}); vuelve a iniciar sesión antes.",
      "keepsWorking": "Tu sesión sigue funcionando sin conexión hasta el {{when}} ({{left}})."
    },
    "time": {
      "hours_one": "queda {{count}} hora",
      "hours_other": "quedan {{count}} horas",
      "minutes_one": "queda {{count}} minuto",
      "minutes_other": "quedan {{count}} minutos"
    },
    "warn": {
      "text": "Desde el {{date}} hará falta una sesión de monoes.me.",
      "signIn": "Iniciar sesión"
    },
    "banner": {
      "dismiss": "Descartar",
      "close": "Cerrar"
    }
  }
}
```

- [ ] **Step 5: Create `wails-app/frontend/src/components/account/AccountGate.jsx`:**

```jsx
// What the app shows instead of itself while the machine's monoes.me sign-in
// does not allow work (spec §6.5): why, and what can be done about it. A locked
// status offers the sign-in controls the library gate uses; a failed or
// unreadable `account status` (a CLI that is missing, too old or not answering)
// names the cause and offers the app update. The keys are written out in full
// so locales/accountKeys.test.js can scan them.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Download, RefreshCw } from 'lucide-react'
import LogInToMonoesButton from '../library/LogInToMonoesButton.jsx'
import { AppSelfUpdate } from '../../wailsjs/go/main/App'
import { subscribeEvent } from '../../services/api.js'
import { account, onAccountLogin } from '../../services/account.js'

// signIn: show the sign-in controls; update: offer the app update; retry: ask again;
// noUpdate: never offer the update (AppSelfUpdate installs by running monoagentcli).
const CLOCK = { title: 'account.gate.clock.title', body: 'account.gate.clock.body', signIn: true }
const REASONS = {
  not_logged_in: { title: 'account.gate.notLoggedIn.title', body: 'account.gate.notLoggedIn.body', signIn: true },
  expired: { title: 'account.gate.expired.title', body: 'account.gate.expired.body', signIn: true },
  refused: { title: 'account.gate.refused.title', body: 'account.gate.refused.body', signIn: true },
  clock_rollback: CLOCK,
  clock_skew: CLOCK,
  key_unknown: { title: 'account.gate.keyUnknown.title', body: 'account.gate.keyUnknown.body', update: true },
  // This computer dropped its saved sign-in because a refresh may have been lost (A24): signing in again here is the way out.
  unconfirmed: { title: 'account.gate.unconfirmed.title', body: 'account.gate.unconfirmed.body', signIn: true },
  // Also the words for a reason this app does not know (a newer monoagentcli may report one).
  invalid: { title: 'account.gate.invalid.title', body: 'account.gate.invalid.body', signIn: true },
}
const CAUSES = {
  cli_not_found: { title: 'account.gate.cliNotFound.title', body: 'account.gate.cliNotFound.body', retry: true, noUpdate: true },
  cli_too_old: { title: 'account.gate.cliTooOld.title', body: 'account.gate.cliTooOld.body', update: true, retry: true },
  cli_failed: { title: 'account.gate.cliFailed.title', body: 'account.gate.cliFailed.body', update: true, retry: true },
}

const mono = { fontFamily: 'var(--font-mono)', fontSize: 11 }
const SIGNED_OUT = { logged_in: false }

function UpdateControl({ version }) {
  const { t } = useTranslation()
  const [state, setState] = useState({ phase: 'idle' }) // idle | working | done | upToDate | error
  useEffect(() => subscribeEvent('update:progress', msg => setState(s => (s.phase === 'working' ? { ...s, progress: msg } : s))), [])

  const run = async () => {
    setState({ phase: 'working', progress: '' })
    try {
      const r = await AppSelfUpdate()
      if (r?.success) setState({ phase: r.up_to_date ? 'upToDate' : 'done' })
      else setState({ phase: 'error', message: r?.error || '' })
    } catch (e) {
      setState({ phase: 'error', message: e?.message || String(e) })
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
      {version && state.phase === 'idle' && <span style={{ ...mono, color: 'var(--text-secondary)' }}>{t('account.update.available', { version })}</span>}
      <button className="btn btn-primary" onClick={run} disabled={state.phase === 'working'} style={{ gap: 8 }}>
        <Download size={14} /> {state.phase === 'working' ? t('account.update.working') : t('account.update.button')}
      </button>
      {state.phase === 'working' && state.progress && <span style={{ ...mono, color: 'var(--text-secondary)' }}>{state.progress}</span>}
      {state.phase === 'done' && <span role="status" style={{ ...mono, color: 'var(--teal)' }}>{t('account.update.done')}</span>}
      {state.phase === 'upToDate' && <span role="status" style={{ ...mono, color: 'var(--text-secondary)' }}>{t('account.update.upToDate')}</span>}
      {state.phase === 'error' && <span role="alert" style={{ ...mono, color: 'var(--red)', maxWidth: 380 }}>{t('account.update.failed', { message: state.message })}</span>}
    </div>
  )
}

export default function AccountGate({ gate, view, update }) {
  const { t } = useTranslation()
  const [asking, setAsking] = useState(false)
  const what = view.failure ? CAUSES[view.failure.cause] : (REASONS[view.status?.reason] ?? REASONS.invalid)
  const message = view.failure?.message || ''

  const tryAgain = async () => {
    setAsking(true)
    try { await gate.check('manual') } finally { setAsking(false) }
  }

  return (
    <div style={{ width: '100vw', height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--void)', position: 'relative' }}>
      {/* The window has no title bar of its own: this strip is how it is moved. */}
      <div aria-hidden="true" style={{ position: 'absolute', top: 0, left: 0, right: 0, height: 36, WebkitAppRegion: 'drag' }} />
      <div role="region" aria-labelledby="account-gate-title"
        style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center', gap: 14, maxWidth: 460, padding: 24 }}>
        <div className="logo-mark" aria-hidden="true" style={{ width: 40, height: 40, fontSize: 18 }}>M</div>
        <h1 id="account-gate-title" style={{ margin: 0, fontFamily: 'var(--font-display)', fontSize: 20, fontWeight: 700, color: 'var(--text)' }}>
          {t(what.title)}
        </h1>
        <p style={{ margin: 0, fontFamily: 'var(--font-body)', fontSize: 13, lineHeight: 1.55, color: 'var(--text-secondary)' }}>
          {t(what.body)}
        </p>
        {message && <p style={{ ...mono, margin: 0, color: 'var(--text-muted)', wordBreak: 'break-word' }}>{t('account.gate.details', { message })}</p>}
        {what.signIn && <LogInToMonoesButton service={account} onLogin={onAccountLogin} status={SIGNED_OUT} onStatusChange={() => gate.check('manual')} large />}
        {(what.update || update) && !what.noUpdate && <UpdateControl version={update?.latest_version} />}
        {what.retry && (
          <button className="btn btn-ghost btn-sm" onClick={tryAgain} disabled={asking} style={{ gap: 6 }}>
            <RefreshCw size={11} /> {t('account.gate.retry')}
          </button>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 6: Create `wails-app/frontend/src/components/account/AccountBanners.jsx`:**

```jsx
// The two notices the app wears while the machine's monoes.me sign-in still
// allows work (spec §6.5): GraceBanner when monoes.me cannot be reached and the
// saved sign-in carries on offline (dismissible, for that grace window; when this
// computer dropped its saved sign-in because a refresh may have been lost, A24, it
// says so and offers to sign in again), and WarnBanner before the enforcement
// date for a machine with no usable sign-in (with a way to sign in now). Keys are
// written out in full for the key scan.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Clock, X, LogIn } from 'lucide-react'
import LogInToMonoesButton from '../library/LogInToMonoesButton.jsx'
import { account, onAccountLogin } from '../../services/account.js'

const bar = {
  display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0, padding: '7px 16px',
  borderBottom: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)',
}
const GRACE_WHY = {
  unreachable: 'account.grace.unreachable',
  server_error: 'account.grace.serverError',
  keyring_unavailable: 'account.grace.keyringUnavailable',
}
const SIGNED_OUT = { logged_in: false }

export function GraceBanner({ status, onSignedIn }) {
  const { t, i18n } = useTranslation()
  const [dismissedFor, setDismissedFor] = useState('')
  const [open, setOpen] = useState(false)
  if (!status.grace_until || dismissedFor === status.grace_until) return null

  const until = new Date(status.grace_until)
  const minutes = Math.max(1, Math.round((until.getTime() - Date.now()) / 60000))
  const left = minutes >= 60 ? t('account.time.hours', { count: Math.floor(minutes / 60) }) : t('account.time.minutes', { count: minutes })
  const when = until.toLocaleString(i18n.language, { dateStyle: 'medium', timeStyle: 'short' })
  // A computer that dropped its saved sign-in cannot renew it, and signing in again is the only fix.
  const dropped = status.reason === 'unconfirmed'
  return (
    <>
      <div role="status" style={{ ...bar, background: 'rgba(234, 179, 8, 0.08)' }}>
        <Clock size={13} aria-hidden="true" style={{ color: 'var(--yellow)', flexShrink: 0 }} />
        <span style={{ flex: 1 }}>
          {dropped
            ? t('account.grace.unconfirmed', { when, left })
            : <>{t(GRACE_WHY[status.reason] ?? 'account.grace.unreachable')} {t('account.grace.keepsWorking', { when, left })}</>}
        </span>
        {dropped && <button className="btn btn-primary btn-sm" onClick={() => setOpen(true)}>{t('account.warn.signIn')}</button>}
        <button className="btn btn-ghost btn-icon" aria-label={t('account.banner.dismiss')} onClick={() => setDismissedFor(status.grace_until)}>
          <X size={13} />
        </button>
      </div>
      {open && <SignInDialog onClose={() => setOpen(false)} onSignedIn={onSignedIn} />}
    </>
  )
}

function SignInDialog({ onClose, onSignedIn }) {
  const { t } = useTranslation()
  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="modal-overlay" style={{ zIndex: 1100 }} onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="account-signin-title"
        style={{ width: 460, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 14, padding: 24, textAlign: 'center' }}>
        <div className="modal-title" style={{ marginBottom: 0, width: '100%' }}>
          <span id="account-signin-title">{t('account.gate.notLoggedIn.title')}</span>
          <button className="btn btn-ghost btn-icon" onClick={onClose} aria-label={t('account.banner.close')}><X size={15} /></button>
        </div>
        <LogInToMonoesButton service={account} onLogin={onAccountLogin} status={SIGNED_OUT} onStatusChange={onSignedIn} large />
      </div>
    </div>
  )
}

export function WarnBanner({ enforceFrom, onSignedIn }) {
  const { t, i18n } = useTranslation()
  const [open, setOpen] = useState(false)
  const date = new Date(enforceFrom).toLocaleDateString(i18n.language, { year: 'numeric', month: 'long', day: 'numeric' })
  return (
    <>
      <div role="status" style={{ ...bar, background: 'rgba(0, 180, 216, 0.06)' }}>
        <LogIn size={13} aria-hidden="true" style={{ color: 'var(--cyan)', flexShrink: 0 }} />
        <span style={{ flex: 1 }}>{t('account.warn.text', { date })}</span>
        <button className="btn btn-primary btn-sm" onClick={() => setOpen(true)}>{t('account.warn.signIn')}</button>
      </div>
      {open && <SignInDialog onClose={() => setOpen(false)} onSignedIn={onSignedIn} />}
    </>
  )
}
```

- [ ] **Step 7: Create `wails-app/frontend/src/components/account/AccountShell.jsx`:**

```jsx
// Puts the monoes.me account in front of the app (spec §6.5). While the machine's
// sign-in does not allow work it renders the gate instead of the app: renderShell
// is not called, so the app and everything it polls is unmounted. Otherwise it
// renders the app, handing it the banners to wear (grace, warn).
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAccountView } from '../../lib/accountGate.js'
import { subscribeEvent } from '../../services/api.js'
import AccountGate from './AccountGate.jsx'
import { GraceBanner, WarnBanner } from './AccountBanners.jsx'

export default function AccountShell({ gate, renderShell }) {
  const { t } = useTranslation()
  const view = useAccountView(gate)
  useEffect(() => gate.start(), [gate])
  // The release the app's own update check found, kept for a gate that comes up later.
  const [update, setUpdate] = useState(null)
  useEffect(() => subscribeEvent('update:available', info => { if (info?.update_available) setUpdate(info) }), [])

  if (view.phase === 'checking') {
    return (
      <div role="status" style={{ width: '100vw', height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--void)', fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
        {t('account.checking')}
      </div>
    )
  }
  if (view.locked) return <AccountGate gate={gate} view={view} update={update} />
  return renderShell(
    <>
      {view.grace && <GraceBanner status={view.status} onSignedIn={() => gate.check('manual')} />}
      {view.warn && <WarnBanner enforceFrom={view.enforceFrom} onSignedIn={() => gate.check('manual')} />}
    </>,
  )
}
```

- [ ] **Step 8: Run the tests.** `npx vitest run --maxWorkers=3 src/components/account src/locales/accountKeys.test.js`. Expected: `Test Files  2 passed (2)`, `Tests  23 passed (23)`. Then the suites that share what changed: `npx vitest run --maxWorkers=3 src/components/library src/doctrine.test.js src/selectStyle.test.js` passes unchanged (the library dialog and its tests keep the default service).

- [ ] **Step 9: Commit:**

```bash
git add wails-app/frontend/src/components/account wails-app/frontend/src/components/library/LogInToMonoesButton.jsx wails-app/frontend/src/locales/en.json wails-app/frontend/src/locales/es.json wails-app/frontend/src/locales/accountKeys.test.js
git commit -m "feat(gui): the account gate, its banners and its strings" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 7: The app starts behind the gate (frontend)

**Files:**
- Create: `wails-app/frontend/src/AccountApp.jsx`
- Modify: `wails-app/frontend/src/App.jsx` (lines 39 and 308-309), `wails-app/frontend/src/main.jsx` (lines 3 and 11)

**Interfaces:**
- Consumes: `AccountShell`, `createAccountGate`, `account.status` (Tasks 4 to 6).
- Produces: `AccountApp()`, the root component; `App({banners})` renders what it is given at the top of its main column. `AccountApp` is its own file so the gate is testable without mounting every page (`AccountShell.render.test.jsx` covers the gate); this task is wiring, checked by the build and by eye.

- [ ] **Step 1: Create `wails-app/frontend/src/AccountApp.jsx`:**

```jsx
// The root of the app: the monoes.me account in front of the shell (spec §6.5).
// Its own file, so it can be tested without mounting every page the shell has.
import { useState } from 'react'
import App from './App.jsx'
import AccountShell from './components/account/AccountShell.jsx'
import { createAccountGate } from './lib/accountGate.js'
import { account } from './services/account.js'

export default function AccountApp() {
  const [gate] = useState(() => createAccountGate({ status: account.status }))
  return <AccountShell gate={gate} renderShell={(banners) => <App banners={banners} />} />
}
```

- [ ] **Step 2: Let `App` wear the banners.** In `wails-app/frontend/src/App.jsx` replace line 39:

```jsx
export default function App() {
```

with:

```jsx
// banners: the account notices AccountApp hands the app to wear (components/account).
export default function App({ banners = null }) {
```

and replace lines 308-309:

```jsx
        <main className="main-content">
          <ErrorBoundary>
```

with:

```jsx
        <main className="main-content">
          {banners}
          <ErrorBoundary>
```

(`.main-content` is a flex column and every page wrapper has `overflow: hidden`, so the banner takes its height off the page below it.)

- [ ] **Step 3: Start from the new root.** In `wails-app/frontend/src/main.jsx` replace line 3 `import App from './App.jsx'` with `import AccountApp from './AccountApp.jsx'` and line 11 `      <App />` with `      <AccountApp />`.

- [ ] **Step 4: Run the whole frontend suite and the build.** From `wails-app/frontend`: `npx vitest run --maxWorkers=3`. Expected: every file passes; a timeout in a settings render test that was not touched (see Global Constraints) passes when its file is rerun alone. Then `npm run build`. Expected: `✓ built in …ms` (the "chunks are larger than 500 kB" notice is old). Then, from `wails-app` (the build created the embed): `go build ./... && go vet ./...` print nothing.

- [ ] **Step 5: Look at it.** Create `wails-app/frontend/mock.html` (never commit it):

```html
<!doctype html><meta charset="utf-8"><div id="root"></div>
<script>
  const DATE = '2026-10-26T00:00:00Z'
  const docs = {
    ok: { v: 1, state: 'ok', reason: '', plan: 'free', enforced: true, enforce_from: DATE },
    locked: { v: 1, state: 'locked', reason: 'not_logged_in', plan: 'free', enforced: true, enforce_from: DATE },
    grace: { v: 1, state: 'grace', reason: 'unreachable', plan: 'free', enforced: true, enforce_from: DATE, grace_until: new Date(Date.now() + 13.3 * 3600e3).toISOString() },
    warn: { v: 1, state: 'locked', reason: 'not_logged_in', plan: 'free', enforced: false, enforce_from: DATE },
    old: { error: 'the monoagentcli this app found has no `account` command', code: 'cli_too_old', enforced: true },
  }
  window.go = { main: { App: new Proxy({}, { get: (_, name) => async () =>
    name === 'AccountStatus' ? JSON.stringify(docs[location.hash.slice(1)] || docs.ok)
      : name === 'GetVersion' ? { version: 'v0.0.0-mock', build_date: '' } : name === 'IsReady' ? true : '[]' }) } }
  window.runtime = { EventsOnMultiple: () => () => {}, EventsOff: () => {}, EventsEmit: () => {} }
</script>
<script type="module" src="/src/main.jsx"></script>
```

Run `npx vite --port 9245 --strictPort` and open `http://localhost:9245/mock.html#locked`, `#grace`, `#warn`, `#old` (reload after changing the hash). Expected: `#locked` is the gate alone (logo, "Sign in to MonoAgent", the sign-in button and "Use an email code instead"); `#grace` is the app with one slim banner on top of the main column, sidebar and status bar untouched; `#warn` the same with a "Sign in" button that opens the dialog; `#old` the gate with "Update MonoAgent" and "Try again". (The mock answers every other binding with `[]`, so the dashboard shows its own error box: ignore it.) Stop the server and delete `mock.html`.

- [ ] **Step 6: Commit:**

```bash
git add wails-app/frontend/src/AccountApp.jsx wails-app/frontend/src/App.jsx wails-app/frontend/src/main.jsx
git commit -m "feat(gui): the app starts behind the account gate" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 8: Whole-branch verification

No new code. Run, in order, from the worktree root unless a directory is named, and fix what fails before offering the branch.

- [ ] **Step 1: Go.** `cd wails-app && gofmt -l . && go vet ./... && go build ./... && go test ./... -count=1` prints nothing from the first three and ends `ok  	github.com/monoes/mono-agent/wails-app` (CI runs the same with `-tags webkit2_41` on Linux). From the root: `gofmt -l .` prints nothing, `go build ./...`, `go build -tags nosocial -o /dev/null ./cmd/monoagentcli` and `go build -tags devaccount -o /dev/null ./cmd/monoagentcli` succeed (B4 touches no root package; the wails module imports `internal/account`).
- [ ] **Step 2: Frontend.** `cd wails-app/frontend && npm ci && npm run build && npm test -- --run` (CI's own command, `ci.yml:296-298`). Expected: the build prints `✓ built`, and every test file passes.
- [ ] **Step 3: Mutation checks.** Break each line below, run the named test, see it fail, restore the line (`git diff` must show nothing for the file afterwards):
  - `classifyAccountStatus`: `(code == 0 || code == 4)` to `code == 0` fails `TestAccountStatusAnswers/locked_exits_4_with_its_document`.
  - `AccountStatus`: delete the `account.EnforceDate().IsZero()` early return; `TestAccountStatusFollowsTheBuildsEnforcementDate` fails.
  - `app.go`: delete `a.startAccountGuard()`; `TestStartupInstallsAndShutdownStopsTheGuard` fails.
  - `viewOf`: `locked: enforced && status.state === 'locked'` to `locked: status.state === 'locked'`; the `viewOf` tests and "does not lock before the enforcement date" fail.
  - `ask()` in `accountGate.js`: replace its retry condition by `false`; "repeats a failed ask once while the app is up" fails.
  - `watchBindings`: delete the `onLoginRequired()` call after `loginRequiredIn(value)`; "reports a login_required answer from any binding" and "goes back to the gate when any binding answers login_required" fail.
  - `AccountShell`: delete the `if (view.locked) return …` line; "renders the gate instead of the app when locked" fails.
  - `AccountGate`: delete `&& !what.noUpdate` from the `UpdateControl` line; "offers no update for a missing CLI, whatever release is known" fails.
- [ ] **Step 4: With B1b and B2 merged, look at the real thing.** Build the CLI with the dev tag outside the repo root (`go build -tags devaccount -o <tmp>/monoagentcli ./cmd/monoagentcli`) and the app with it too (`cd wails-app && wails build -tags devaccount`; its output is git-ignored), and run the app with `MONOAGENTCLI_BIN=<tmp>/monoagentcli`. The app reads the enforcement date in process, so it needs the tag as well. Before B5a sets the date, with no `MONOAGENT_DEV_ENFORCE_FROM` (dormant): no gate, no banner, and no `account status` process is started (point `MONOAGENTCLI_BIN` at a script that logs its arguments and runs the CLI, and see that nothing is logged for `account status`). With B5c merged, that variable (an RFC 3339 time that only a `-tags devaccount` binary reads: B5c) moves the date: `2020-01-01T00:00:00Z` and signed out gives the gate; sign in through the button (against the stand-in monoes.me that a `devaccount` build trusts: B1b, it honors `MONOES_BASE_URL` and the development key): the app; `monoagentcli account logout` in a terminal, then focus the window: the gate. A time a week ahead gives the warn period: a banner with the date and a "Sign in" button, and the app is not locked.

## Contract change requests

None. The desktop reads `account status --json` as spec §7 and index §3.4 item 1 freeze it, and uses the guard API of index §3.2 as written. (The one request about the bridge's `ping`, `enforced`, belongs to B4b.)
