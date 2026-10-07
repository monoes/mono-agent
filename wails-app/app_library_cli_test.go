//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The library bindings shell out to `monoagentcli library …` and return its
// stdout verbatim; user text travels as --flag=value or after "--", so a
// value starting with "-" is never read as a flag.
func TestLibraryBindingsShellOut(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *"library status"*) echo '{"base_url":"https://monoes.me","logged_in":false,"user":null,"scopes":[]}' ;;
  *"library list"*) echo '{"items":[],"page":1,"per_page":20,"total":0,"scope":"official"}' ;;
  *"library install"*) echo '{"error":"org \"growth\" already exists; pass --rename <name> or --yes","code":"invalid_input"}'; exit 3 ;;
  *) echo '{"ok":true}' ;;
esac
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")

	if got := a.LibraryStatus(true); !strings.Contains(got, `"logged_in":false`) {
		t.Fatalf("LibraryStatus = %s", got)
	}
	if got := a.LibraryList("automation", "official", " -x ", 2); !strings.Contains(got, `"scope":"official"`) {
		t.Fatalf("LibraryList = %s", got)
	}
	got := a.LibraryInstall("org", "growth", "", false)
	var e struct{ Error, Code string }
	if json.Unmarshal([]byte(got), &e) != nil || e.Code != "invalid_input" || !strings.Contains(e.Error, "--rename") {
		t.Fatalf("LibraryInstall kept the CLI's coded error? %s", got)
	}
	a.LibraryInstall("org", "growth", "growth-2", true)
	a.LibraryShow("-odd")
	a.LibraryPublish("workflow", "wf-1", true, "My flow", "", "a, b", "")
	a.LibraryUpdate("")
	a.LibraryUpdate("item-9")
	a.LibraryInstalled("org")
	a.LibraryLogout()
	a.LibraryLoginEmailSend("me@example.com")
	a.LibraryLoginEmailVerify("me@example.com", " 123456 ")

	want := []string{
		"--profile work --json library status --offline",
		"--profile work --json library list --kind=automation --scope=official --search=-x --page=2",
		"--profile work --json library install -- org growth",
		"--profile work --json library install --rename=growth-2 --yes -- org growth",
		"--profile work --json library show -- -odd",
		"--profile work --json library publish --public --name=My flow --tags=a, b -- workflow wf-1",
		"--profile work --json library update --yes",
		"--profile work --json library update --yes -- item-9",
		"--profile work --json library installed --kind=org",
		"--json account logout",
		"--json account login --email=me@example.com --send",
		"--json account login --email=me@example.com --code=123456",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

type libraryEvents struct {
	mu  sync.Mutex
	evs []map[string]interface{}
}

func captureLibraryEvents(t *testing.T) *libraryEvents {
	t.Helper()
	l := &libraryEvents{}
	prev := emitLibraryEvent
	emitLibraryEvent = func(_ *App, name string, data interface{}) {
		if name != "library:login" {
			t.Errorf("event %q", name)
		}
		l.mu.Lock()
		l.evs = append(l.evs, data.(map[string]interface{}))
		l.mu.Unlock()
	}
	t.Cleanup(func() { emitLibraryEvent = prev })
	return l
}

// LibraryLogin forwards the CLI's stderr progress (the sign-in URL) as
// events and returns the final status object.
func TestLibraryLoginStreamsURL(t *testing.T) {
	evs := captureLibraryEvents(t)
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo '{"kind":"url","url":"https://monoes.me/api/auth/oauth2/authorize?x=1"}' >&2
echo 'opening browser' >&2
echo '{"logged_in":true,"user":{"username":"ana"}}'
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	got := a.LibraryLogin()
	if !strings.Contains(got, `"logged_in":true`) {
		t.Fatalf("LibraryLogin = %s", got)
	}
	if len(evs.evs) != 2 || evs.evs[0]["kind"] != "url" || !strings.HasPrefix(evs.evs[0]["url"].(string), "https://monoes.me/") ||
		evs.evs[1]["kind"] != "line" || evs.evs[1]["message"] != "opening browser" {
		t.Fatalf("events = %+v", evs.evs)
	}
}

// Cancel stops the waiting CLI and says so; a second concurrent login is
// refused rather than started.
func TestLibraryLoginCancel(t *testing.T) {
	captureLibraryEvents(t)
	prevGrace := healthGracePeriod
	healthGracePeriod = time.Second
	t.Cleanup(func() { healthGracePeriod = prevGrace })
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo '{"kind":"url","url":"https://monoes.me/x"}' >&2
exec sleep 30
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	done := make(chan string, 1)
	go func() { done <- a.LibraryLogin() }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		accountLoginMu.Lock()
		running := accountLoginCancel != nil
		accountLoginMu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("login never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := a.LibraryLogin(); !strings.Contains(got, `"code":"busy"`) {
		t.Fatalf("second login = %s", got)
	}
	if got := a.LibraryLoginCancel(); got != `{"ok":true,"cancelled":true}` {
		t.Fatalf("cancel = %s", got)
	}
	select {
	case got := <-done:
		if !strings.Contains(got, `"code":"cancelled"`) {
			t.Fatalf("cancelled login = %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not stop")
	}
	if got := a.LibraryLoginCancel(); got != `{"ok":true,"cancelled":false}` {
		t.Fatalf("idle cancel = %s", got)
	}
}

// Browsing without a login is the CLI's exit 4 with login_required; the
// bindings hand that object to the page unchanged, so it can show the
// login gate.
func TestLibraryBindingsPassLoginRequired(t *testing.T) {
	const body = `{"code":"auth_or_connection","error":"Log in to monoes.me first: monoagentcli library login","login_required":true}`
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo '`+body+`'; exit 4
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	for name, got := range map[string]string{
		"LibraryList":    a.LibraryList("automation", "official", "", 1),
		"LibraryShow":    a.LibraryShow("automation/hackernews"),
		"LibraryInstall": a.LibraryInstall("automation", "hackernews", "", false),
		"LibraryUpdate":  a.LibraryUpdate(""),
	} {
		if got != body {
			t.Errorf("%s = %s", name, got)
		}
	}
}
