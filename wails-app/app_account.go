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
	suppressConsole(cmd)
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
	suppressConsole(cmd)
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
	suppressConsole(cmd)
	out, runErr := cmd.Output()
	return cliResultJSON(cliBin, out, runErr)
}
