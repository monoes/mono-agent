package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/monoes/mono-agent/internal/i18n"
)

// version/buildDate are set via -ldflags in release builds (see
// .github/workflows/release.yml); empty in a plain `go build`.
var (
	version   = ""
	buildDate = ""
)

// getVersion/getBuildDate resolve version/buildDate lazily via sync.Once
// rather than an unconditional `init()`, which used to shell out to `git
// describe` on EVERY invocation regardless of subcommand — including a bare
// `--help` — costing up to several seconds on a cold cache. monoagentcli is
// spawned as a subprocess repeatedly by the Wails app (each chat message,
// each agent scan, each org observe call), so that fixed per-invocation tax
// was a real, compounding source of UI latency in dev builds. Release
// builds never hit this path at all since version is baked in via ldflags.
var (
	versionOnce sync.Once
	buildOnce   sync.Once
)

func getVersion() string {
	versionOnce.Do(func() {
		if version != "" {
			return
		}
		if out, err := exec.Command("git", "describe", "--tags", "--always").Output(); err == nil {
			version = strings.TrimSpace(string(out))
		} else {
			version = "dev"
		}
	})
	return version
}

func getBuildDate() string {
	buildOnce.Do(func() {
		if buildDate == "" {
			buildDate = time.Now().UTC().Format("2006-01-02T15:04:05Z")
		}
	})
	return buildDate
}

func main() {
	// Report-then-repanic: files a crash report as a side effect, then
	// re-panics so the process still crashes with the original trace and
	// exit behavior a user would otherwise see — this only observes, it
	// never swallows the panic.
	defer func() {
		if r := recover(); r != nil {
			reportCrash(r, debug.Stack())
			panic(r)
		}
	}()

	// Locale must be resolved before newRootCmd() builds the command tree,
	// since cobra Short/Long/Example strings are evaluated once at
	// construction time, before flags are parsed. See internal/i18n and
	// docs/i18n.md.
	i18n.SetLocale(i18n.Detect(os.Args[1:]))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if wantsJSONError(os.Args[1:]) {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			fmt.Fprintln(os.Stdout, string(b))
		}
		os.Exit(exitCodeFor(err))
	}
}

// wantsJSONError reports whether a failed command should also print
// {"error": …} on stdout: `org` commands (whose success output is always
// JSON) and `status`, when --json is passed. The GUI passes that output
// straight to the page (contracts §4).
func wantsJSONError(args []string) bool {
	hasJSON, sub := false, ""
	for _, a := range args {
		if a == "--json" || a == "--json=true" {
			hasJSON = true
			continue
		}
		if sub == "" && !strings.HasPrefix(a, "-") {
			sub = a
		}
	}
	return hasJSON && (sub == "org" || sub == "status")
}
