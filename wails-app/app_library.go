// wails-app/app_library.go
//
// The monoes.me library: log in, browse, add and publish workflows, web
// automations and orgs. Everything shells out to `monoagentcli library …
// --json` and returns its stdout verbatim — the CLI owns the account, the
// downloads, the sha256 checks and the install handoff; this file only
// forwards them.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	libraryCLITimeout     = 60 * time.Second
	libraryInstallTimeout = 5 * time.Minute
	// The CLI's own login wait is 5 minutes; this leaves it room to report.
	libraryLoginTimeout = 6 * time.Minute
)

// emitLibraryEvent sends a login progress event to the frontend (tests
// replace it: a bare context has no Wails runtime to emit on).
var emitLibraryEvent = func(a *App, name string, data interface{}) {
	runtime.EventsEmit(a.ctx, name, data)
}

var (
	libraryLoginMu     sync.Mutex
	libraryLoginCancel context.CancelFunc
)

// LibraryStatus returns `library status [--offline]`.
func (a *App) LibraryStatus(offline bool) string {
	args := []string{"library", "status"}
	if offline {
		args = append(args, "--offline")
	}
	return a.rawCLI(libraryCLITimeout, args...)
}

// LibraryLogin runs `library login` (PKCE through the system browser, which
// the CLI opens itself) and returns its final status object. Each NDJSON
// progress line the CLI writes to stderr is forwarded as a "library:login"
// event, so the page can show the sign-in URL as a fallback link. Only one
// login runs at a time; LibraryLoginCancel stops it.
func (a *App) LibraryLogin() string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	libraryLoginMu.Lock()
	if libraryLoginCancel != nil {
		libraryLoginMu.Unlock()
		return `{"error":"a monoes.me login is already in progress","code":"busy"}`
	}
	ctx, cancel := context.WithTimeout(a.ctx, libraryLoginTimeout)
	libraryLoginCancel = cancel
	libraryLoginMu.Unlock()
	cancelled := false
	defer func() {
		libraryLoginMu.Lock()
		libraryLoginCancel = nil
		libraryLoginMu.Unlock()
		cancel()
	}()

	cmd := exec.CommandContext(ctx, cliBin, "--profile", a.getActiveProfileID(), "--json", "library", "login")
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
	var lastLine string
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
		lastLine = line
		emitLibraryEvent(a, "library:login", ev)
	}
	waitErr := cmd.Wait()
	if ctx.Err() == context.Canceled {
		cancelled = true
	}
	if cancelled {
		return `{"error":"login cancelled","code":"cancelled"}`
	}
	if waitErr != nil && strings.TrimSpace(stdout.String()) == "" && lastLine != "" {
		// The CLI explained itself on stderr only.
		return aiError(errorString(lastLine))
	}
	return cliResultJSON(cliBin, []byte(stdout.String()), waitErr)
}

type errorString string

func (e errorString) Error() string { return string(e) }

// LibraryLoginCancel stops a running LibraryLogin. {"ok":true,"cancelled":bool}.
func (a *App) LibraryLoginCancel() string {
	libraryLoginMu.Lock()
	cancel := libraryLoginCancel
	libraryLoginMu.Unlock()
	if cancel == nil {
		return `{"ok":true,"cancelled":false}`
	}
	cancel()
	return `{"ok":true,"cancelled":true}`
}

// LibraryLoginEmailSend asks monoes.me to email a sign-in code (the
// fallback for machines without a usable browser).
func (a *App) LibraryLoginEmailSend(email string) string {
	return a.rawCLI(libraryCLITimeout, "library", "login", "--email="+email, "--send")
}

// LibraryLoginEmailVerify trades the emailed code for a token.
func (a *App) LibraryLoginEmailVerify(email, code string) string {
	return a.rawCLI(libraryCLITimeout, "library", "login", "--email="+email, "--code="+strings.TrimSpace(code))
}

// LibraryLogout forgets this profile's monoes.me token.
func (a *App) LibraryLogout() string {
	return a.rawCLI(libraryCLITimeout, "library", "logout")
}

// LibraryList returns `library list` for one kind and scope
// (official | public | mine).
func (a *App) LibraryList(kind, scope, search string, page int) string {
	args := []string{"library", "list"}
	if kind != "" {
		args = append(args, "--kind="+kind)
	}
	if scope != "" {
		args = append(args, "--scope="+scope)
	}
	if s := strings.TrimSpace(search); s != "" {
		args = append(args, "--search="+s)
	}
	if page > 1 {
		args = append(args, "--page="+strconv.Itoa(page))
	}
	return a.rawCLI(libraryCLITimeout, args...)
}

// LibraryShow returns one item (`library show <id|kind/slug>`).
func (a *App) LibraryShow(id string) string {
	return a.rawCLI(libraryCLITimeout, "library", "show", "--", id)
}

// LibraryInstall adds a library item to this machine. rename installs an
// org under another name; yes confirms replacing what is already there.
func (a *App) LibraryInstall(kind, id, rename string, yes bool) string {
	args := []string{"library", "install"}
	if r := strings.TrimSpace(rename); r != "" {
		args = append(args, "--rename="+r)
	}
	if yes {
		args = append(args, "--yes")
	}
	args = append(args, "--", kind, id)
	return a.rawCLI(libraryInstallTimeout, args...)
}

// LibraryPublish packs or exports a local workflow (id), automation (id)
// or org (name) and uploads it — private unless public is set.
func (a *App) LibraryPublish(kind, localID string, public bool, name, description, tags, version string) string {
	args := []string{"library", "publish"}
	if public {
		args = append(args, "--public")
	}
	for _, f := range [][2]string{{"name", name}, {"description", description}, {"tags", tags}, {"version", version}} {
		if v := strings.TrimSpace(f[1]); v != "" {
			args = append(args, "--"+f[0]+"="+v)
		}
	}
	args = append(args, "--", kind, localID)
	return a.rawCLI(libraryInstallTimeout, args...)
}

// LibraryUpdate re-installs newer versions of items that came from the
// library (all of them when id is empty).
func (a *App) LibraryUpdate(id string) string {
	args := []string{"library", "update", "--yes"}
	if id != "" {
		args = append(args, "--", id)
	}
	return a.rawCLI(libraryInstallTimeout, args...)
}

// LibraryInstalled lists what this machine installed from the library.
func (a *App) LibraryInstalled(kind string) string {
	args := []string{"library", "installed"}
	if kind != "" {
		args = append(args, "--kind="+kind)
	}
	return a.rawCLI(libraryCLITimeout, args...)
}
