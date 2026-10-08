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
