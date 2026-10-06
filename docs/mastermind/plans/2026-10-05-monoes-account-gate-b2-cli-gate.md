# Mandatory monoes.me Account — B2: the CLI gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A gated `monoagentcli` command with no valid monoes.me session is refused before cobra runs anything (exit 4, `login_required`), and `doctor` shows the account in one row.

**Architecture:** One table in `cmd/monoagentcli/account_gate.go` classifies the whole cobra tree as `open`, `gated` or `serve` and is stamped on every command as `Annotations["monoagent.account"]`. `invocationClass` resolves what an argument list would run with `root.Find` on the tree that executes. `main()` becomes `run(args, stdout, stderr)`: it installs the process guard, asks `gateCommand`, and only then lets cobra execute. `internal/health` gets the `core.monoes_account` row, fed from `account.CurrentStatus()`.

**Tech Stack:** Go 1.26, cobra v1.10.2, `internal/account` and `internal/account/accounttest` (B1a, B1b), `internal/health`.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§6.1, §6.4, §7, §12 S5; D6, D8, D20, D22, D25). Index: `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§2, §3.4, §3.5). Depends on B1a and B1b being merged. Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

## Global Constraints

- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `EnsureFresh`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear only on a login or a refresh. The high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- Open commands (D6): `version`, `help`, `completion`, cobra's hidden `__complete` and `__completeNoDesc`, `ref`, `update`, `doctor` (with `doctor fix`), `setup`, `account` (all of it), `library login`, `library logout`, `library status`. Everything else is gated, except the serving commands:
- Serving commands (spec §6.4) start even when locked, because launchd's `KeepAlive` and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop and MCP hosts must see a clear error. The CLI gate's third class `serve` is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`); layers 2 and 3 do the refusing. `org serve` (a launcher) and `daemon install`, `restart` and `uninstall` stay gated.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

This plan adds one class to the open/gated split above, `serve`, for the long-running commands that must start while locked (spec §6.4); see Contract change requests.

## Review Focus

Failure modes the spec implies that a person using the software would meet and that no task's happy-path tests would exercise, most likely first. Each is pinned by a named test.

1. A long-running command started while locked: `daemon` under launchd's `KeepAlive` (`internal/autostart/autostart_darwin.go:33`), the `mcp` an AI client launches, the bridge the side panel connects to. Refused at start they respawn in a loop or cannot say what to do. They must start and say why on stderr. Pinned by `TestGateWhenLocked` and `pinnedServe` in `TestEveryCommandIsClassified` (Tasks 1 and 3).
2. Help, shell completion and the way out must stay open while signed out: `__complete`, `help`, `workflow run --help`, `--profile work account login`. Pinned by `TestOpenCommandsStayOpenWhenLocked` (Task 4), `TestGateWhenLocked` (Task 3) and the S5 rows (Task 2).
3. An argument spelling that cobra runs but the gate lets through: a value flag swallowing `--help` or a command name, `--help=false`, `--`, an alias, an unknown flag. Pinned by `TestGateNeverDowngradesWhatCobraWouldRun` (Task 2).
4. Something written to HOME before the gate says no, or by an open command: the first-run marker, the database, account files on a fresh HOME. From the enforcement date a refusal writes exactly one thing, the clock-guard record of a machine that never signed in (A25): `account/session.lock` and `account/session.json`, a session with no token and a high-water mark equal to the clock, so a clock set back before the date does not un-enforce the gate; before the date and for an open command nothing is written. Pinned by `TestRunRefusesGatedCommandsWhenLocked`, `TestRunRefusesAgainWhenTheClockIsSetBackBeforeTheDate` and `TestRunCreatesNothingForOpenCommands` (Task 4).
5. A script that reads stdout: under `--json` the refusal is exactly one JSON document and no gate text reaches stdout; while enforcement is dormant the gate says nothing and the doctor row neither warns nor fails. Pinned by `TestRunRefusalIsOneJSONDocumentUnderJSON`, `TestRunLetsGatedCommandsRunWhenAllowed` and `TestGateAnnouncesGraceAndWarning` (Tasks 3 and 4), and `TestMonoesAccountCheck` (Task 5).
6. A Ctrl-C that cannot end the process. The guard never abandons a refresh grant it has sent (A20), so a shutdown can wait up to the grant timeout (20 seconds) for monoes.me's answer, and `signal.NotifyContext` keeps swallowing the signals until it is stopped. `run()` therefore lets go of the signals before it releases the guard, so a second Ctrl-C ends a shutdown that waits in the guard's `Close`; while a command is still inside the gate's own refresh (`gateStatus`) the signals stay caught, the wait is bounded, and `gateStatus` says so. Pinned by `TestRunLetsGoOfTheSignalsBeforeItReleasesTheGuard` (Task 4).

---

### Task 1: The classification table and the inventory test

**Files:**
- Create: `cmd/monoagentcli/account_gate.go`
- Test: `cmd/monoagentcli/account_gate_test.go`

**Interfaces:**
- Consumes: `newRootCmd()` (`cmd/monoagentcli/root.go:36`); B1b's `account` command with `login`, `logout` and `status` in that tree; cobra's `InitDefaultHelpCmd` and `InitDefaultCompletionCmd`.
- Produces (package `main`):

```go
const accountAnnotation = "monoagent.account"           // the Annotations key
const classOpen, classGated, classServe = "open", "gated", "serve"
var accountClasses map[string]string                      // the one table, keyed by command path
func commandKey(c *cobra.Command) string                  // "doctor fix"; "" for the root
func applyClassification(root *cobra.Command)             // stamps every command below root
func commandClass(c *cobra.Command) string                // open, serve or gated; the root is never consulted
```

- [ ] **Step 1: Check that B1a and B1b are merged.**

```bash
grep -n "^func NewDefaultGuard\|^func CurrentStatus\|^func Install(\|^func Current()" internal/account/*.go
grep -n "^func newLoginRequiredError" cmd/monoagentcli/login_required.go
ls internal/account/accounttest
go build ./...
```

Expected: one line for each of the four functions, one line for `newLoginRequiredError`, the `accounttest` files, and no build output. If a symbol is missing, stop: this plan needs B1a and B1b.

- [ ] **Step 2: Write the failing test.** Create `cmd/monoagentcli/account_gate_test.go`:

```go
package main

import (
	"slices"
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

// pinnedOpen and pinnedServe are the open list of D6 and the serving commands
// of spec §6.4, spelled out per command path. Opening a command is a decision,
// so it takes an edit here too, in the review that opens it. `account` is all
// of the account commands, `ref` all the reference pages; help and completion
// are cobra's own. __complete is cobra's as well, but exists only while it
// executes, so TestOpenCommandsStayOpenWhenLocked covers it.
var (
	pinnedOpen = []string{
		"account", "account login", "account logout", "account status",
		"completion", "completion bash", "completion fish", "completion powershell", "completion zsh",
		"doctor", "doctor fix",
		"help",
		"library login", "library logout", "library status",
		"ref", "ref api", "ref commands", "ref connections", "ref crawling", "ref examples", "ref expressions",
		"ref node", "ref nodes", "ref org", "ref templates", "ref workflow",
		"setup", "update", "version",
	}
	pinnedServe = []string{"daemon", "extension serve", "httpapi", "mcp"}
)

// allCommands lists every command below root, parents first.
func allCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			out = append(out, sub)
			walk(sub)
		}
	}
	walk(root)
	return out
}

// Every command is stamped, and the open and serving sets are exactly the
// pinned lists: the idiom of TestEveryRegisteredNodeTypeIsClassified.
func TestEveryCommandIsClassified(t *testing.T) {
	root := newRootCmd()
	// Cobra adds help and completion while a command executes; add them so
	// the inventory sees them.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	applyClassification(root)

	all := allCommands(root)
	var open, serve []string
	for _, c := range all {
		class, stamped := c.Annotations[accountAnnotation]
		if !stamped || (class != classOpen && class != classGated && class != classServe) {
			t.Errorf("%q has class %q", commandKey(c), class)
		}
		switch commandClass(c) {
		case classOpen:
			open = append(open, commandKey(c))
		case classServe:
			serve = append(serve, commandKey(c))
		}
	}
	t.Logf("%d commands classified: %d open, %d serving", len(all), len(open), len(serve))

	for name, pair := range map[string][2][]string{"open": {pinnedOpen, open}, "serve": {pinnedServe, serve}} {
		want := append([]string(nil), pair[0]...)
		sort.Strings(want)
		sort.Strings(pair[1])
		if !slices.Equal(want, pair[1]) {
			t.Errorf("the %s commands differ from the pinned list (D6)\nwant %q\n got %q", name, want, pair[1])
		}
	}
}

// A table entry that names no command would silently classify nothing.
func TestAccountClassesNameRealCommands(t *testing.T) {
	root := newRootCmd()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	known := map[string]bool{"__complete": true} // cobra adds it only while it runs it
	for _, c := range allCommands(root) {
		known[commandKey(c)] = true
	}
	for key := range accountClasses {
		if !known[key] {
			t.Errorf("accountClasses lists %q, which is not a command", key)
		}
	}
}

// Default-deny at run time: a command added after the classification, or
// stamped with anything but the exact words open and serve, is gated; a child
// of an open command takes its parent's class, which pinnedOpen then makes visible.
func TestUnlistedCommandsAreGated(t *testing.T) {
	root := newRootCmd()
	applyClassification(root)
	noop := func(*cobra.Command, []string) error { return nil }
	brandNew := &cobra.Command{Use: "brand-new", RunE: noop}
	mistyped := &cobra.Command{Use: "mistyped", RunE: noop, Annotations: map[string]string{accountAnnotation: "opne"}}
	root.AddCommand(brandNew, mistyped)
	for _, c := range []*cobra.Command{brandNew, mistyped} {
		if got := commandClass(c); got != classGated {
			t.Errorf("%s: class %q, want gated", c.Name(), got)
		}
	}
	ref, _, err := root.Find([]string{"ref"})
	if err != nil {
		t.Fatal(err)
	}
	child := &cobra.Command{Use: "brand-new-page", RunE: noop}
	ref.AddCommand(child)
	if commandClass(child) != classOpen {
		t.Error("a child of an open command should inherit open")
	}
}
```

- [ ] **Step 3: Run it and see it fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestEveryCommandIsClassified|TestAccountClassesNameRealCommands|TestUnlistedCommandsAreGated)$' -count=1
```

Expected: FAIL with build errors such as `undefined: applyClassification`, `undefined: accountAnnotation` and `undefined: commandClass`, ending `FAIL	github.com/monoes/mono-agent/cmd/monoagentcli [build failed]`.

- [ ] **Step 4: Implement the table.** Create `cmd/monoagentcli/account_gate.go`:

```go
package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// The CLI gate (spec §6.1, D6, D20): layer 1 of the monoes.me account gate.
// Every command carries a class in Annotations[accountAnnotation]:
//
//	open   runs whatever the account state;
//	gated  with no valid session is refused before cobra runs a single hook
//	       (no first-run check, no database open; from the enforcement date the
//	       guard writes only the clock-guard record, A25): exit 4;
//	serve  a long-running command (spec §6.4): it starts even when locked and
//	       refuses the work itself (layers 2 and 3), because a daemon that
//	       exited on the lock would be respawned in a loop by launchd, and a
//	       locked MCP server or bridge could not tell its client what to do.
//
// One table classifies the whole command tree, so the commands need no edit
// and a command added later is gated until someone opens it here.
const (
	accountAnnotation = "monoagent.account"
	classOpen         = "open"
	classGated        = "gated"
	classServe        = "serve"
)

// accountClasses is that table, keyed by command path without the root's
// name. A command takes the class of its nearest listed ancestor: `ref` covers
// every `ref` subcommand and `account` all the account commands. A command
// that nothing lists is gated, and so is one stamped with any other word.
//
// `org serve` is not a serving command here: it is a launcher that starts the
// external monomind process and returns, so a refusal causes no respawn loop and
// a locked machine does not start org services (D8).
//
// help, completion and __complete are cobra's own: it adds them while a
// command executes, after the gate has looked, so they reach the gate as the
// root with leftover arguments (see invocationClass) and are listed here for
// the inventory test. __completeNoDesc is an alias of __complete.
var accountClasses = map[string]string{
	"version":        classOpen,
	"ref":            classOpen,
	"update":         classOpen,
	"doctor":         classOpen,
	"doctor fix":     classOpen,
	"setup":          classOpen,
	"account":        classOpen,
	"library login":  classOpen,
	"library logout": classOpen,
	"library status": classOpen,
	"help":           classOpen,
	"completion":     classOpen,
	"__complete":     classOpen,

	"daemon":           classServe,
	"daemon install":   classGated,
	"daemon restart":   classGated,
	"daemon uninstall": classGated,
	"httpapi":          classServe,
	"mcp":              classServe,
	"extension serve":  classServe,
}

// commandKey is c's path below the root: "doctor fix", "library login".
func commandKey(c *cobra.Command) string {
	var names []string
	for ; c != nil && c.HasParent(); c = c.Parent() {
		names = append([]string{c.Name()}, names...)
	}
	return strings.Join(names, " ")
}

// applyClassification stamps every command below root with its class from
// accountClasses. It can run again on a tree that gained commands.
func applyClassification(root *cobra.Command) {
	var walk func(parent *cobra.Command, inherited string)
	walk = func(parent *cobra.Command, inherited string) {
		for _, c := range parent.Commands() {
			class := inherited
			if listed, ok := accountClasses[commandKey(c)]; ok {
				class = listed
			}
			if class == "" {
				class = classGated
			}
			if c.Annotations == nil {
				c.Annotations = map[string]string{}
			}
			c.Annotations[accountAnnotation] = class
			walk(c, class)
		}
	}
	walk(root, "")
}

// commandClass is c's own stamp, else its nearest stamped ancestor's, else
// gated; a stamp that is not open or serve counts as gated. The root is never
// consulted: invocationClass decides what the root itself may do.
func commandClass(c *cobra.Command) string {
	for ; c != nil && c.HasParent(); c = c.Parent() {
		if class, ok := c.Annotations[accountAnnotation]; ok {
			if class == classOpen || class == classServe {
				return class
			}
			return classGated
		}
	}
	return classGated
}
```

- [ ] **Step 5: Run it and see it pass.** Same command as Step 3.

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli	<n>s`; with `-v`, `TestEveryCommandIsClassified` logs `… commands classified: 30 open, 4 serving`. If it fails with a `want … got …` pair, the tree differs from the plan: an extra `account` subcommand from B1b belongs in `pinnedOpen` (the spec opens all of `account`); anything else is a decision for the review, not an edit to make quietly.

- [ ] **Step 6: Commit.**

```bash
git add cmd/monoagentcli/account_gate.go cmd/monoagentcli/account_gate_test.go
```

```bash
git commit -m "feat(cli): classify every command for the monoes.me account gate" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: What an invocation runs (spike S5)

**Files:**
- Create: `cmd/monoagentcli/account_gate_resolve.go`
- Test: `cmd/monoagentcli/account_gate_find_test.go`
- Create or modify: `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md`

**Interfaces:**
- Consumes: Task 1's `applyClassification`, `commandClass`, `commandKey`, `allCommands` (test helper, `account_gate_test.go`); `newRootCmd()`; cobra's `Find`, `InitDefaultHelpFlag`, `ParseFlags`.
- Produces (package `main`):

```go
// invocationClass is the class (open, serve, gated) of what cobra would run for args; open when it runs nothing.
func invocationClass(root *cobra.Command, args []string) string
func asksForHelp(args []string) bool    // cobra will print help instead of running the command
func mayAskForHelp(args []string) bool  // cheap pre-check for asksForHelp
```

- [ ] **Step 1: Write the failing test.** Create `cmd/monoagentcli/account_gate_find_test.go`. `findCases` is the S5 table; `TestGateNeverDowngradesWhatCobraWouldRun` executes the same arguments, and every combination of flags before and after fifteen commands, on a tree whose commands record themselves, and fails if cobra would run a command of a stricter class than the gate assigns.

```go
package main

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Spike S5 (spec §12): what root.Find resolves for each way of invoking the
// CLI, and so what the gate decides. path "" is the root; rest is what cobra
// then parses as the command's flags and arguments. The table is the S5
// finding appended to the spike-findings document.
var findCases = []struct {
	args  []string
	path  string
	rest  []string
	class string
	why   string
}{
	// The root, with nothing or with words that are no command.
	{[]string{}, "", nil, classOpen, "a bare root prints help"},
	{[]string{"nosuch", "workflow", "list"}, "", []string{"nosuch", "workflow", "list"}, classOpen, "unknown command: cobra's error, nothing runs"},
	{[]string{"wor"}, "", []string{"wor"}, classOpen, "no prefix matching"},
	{[]string{"WORKFLOW", "list"}, "", []string{"WORKFLOW", "list"}, classOpen, "names are case-sensitive"},
	{[]string{"workflow", "lis"}, "workflow", []string{"lis"}, classGated, "the group's own unknown-command error"},
	// Global flags before the subcommand.
	{[]string{"--json", "workflow", "list"}, "workflow list", []string{"--json"}, classGated, "a bool flag takes no value"},
	{[]string{"-v", "workflow", "list"}, "workflow list", []string{"-v"}, classGated, ""},
	{[]string{"--profile", "work", "workflow", "list"}, "workflow list", []string{"--profile", "work"}, classGated, "a value flag takes the next word"},
	{[]string{"--profile=work", "workflow", "list"}, "workflow list", []string{"--profile=work"}, classGated, ""},
	{[]string{"--db-path", "/tmp/x.db", "workflow", "list"}, "workflow list", []string{"--db-path", "/tmp/x.db"}, classGated, ""},
	{[]string{"--db-path=/tmp/x.db", "doctor"}, "doctor", []string{"--db-path=/tmp/x.db"}, classOpen, ""},
	{[]string{"--lang", "es", "doctor"}, "doctor", []string{"--lang", "es"}, classOpen, ""},
	{[]string{"--profile", "work", "account", "login"}, "account login", []string{"--profile", "work"}, classOpen, "the way out, behind a flag"},
	{[]string{"--db-path", "/tmp/x.db", "account", "status"}, "account status", []string{"--db-path", "/tmp/x.db"}, classOpen, ""},
	{[]string{"--profile", "doctor"}, "", []string{"--profile", "doctor"}, classOpen, "`doctor` is the profile's name: no command"},
	{[]string{"--profile", "doctor", "workflow", "list"}, "workflow list", []string{"--profile", "doctor"}, classGated, "a command name as a flag value is not a command"},
	{[]string{"--no-such-flag", "workflow", "list"}, "list", []string{"--no-such-flag", "workflow"}, classGated, "an unknown flag takes the next word too; cobra then rejects it"},
	// Flags after the subcommand, and between the words of a path.
	{[]string{"workflow", "--json", "list"}, "workflow list", []string{"--json"}, classGated, "flags may sit between the words"},
	{[]string{"workflow", "list", "--profile", "work"}, "workflow list", []string{"--profile", "work"}, classGated, ""},
	{[]string{"workflow", "--profile", "list"}, "workflow", []string{"--profile", "list"}, classGated, "`list` is the profile's name"},
	{[]string{"doctor", "fix", "x"}, "doctor fix", []string{"x"}, classOpen, "an open command's subcommand"},
	{[]string{"library", "--json", "status"}, "library status", []string{"--json"}, classOpen, "library status is open, its siblings are not"},
	{[]string{"library", "list"}, "library list", nil, classGated, ""},
	{[]string{"ref", "node", "http.request"}, "ref node", []string{"http.request"}, classOpen, ""},
	{[]string{"update", "--app"}, "update", []string{"--app"}, classOpen, ""},
	// The long-running commands start locked; their siblings do not.
	{[]string{"daemon"}, "daemon", nil, classServe, ""},
	{[]string{"daemon", "restart"}, "daemon restart", nil, classGated, ""},
	{[]string{"mcp"}, "mcp", nil, classServe, ""},
	{[]string{"mcp", "--grant", "org-1"}, "mcp", []string{"--grant", "org-1"}, classServe, "an mcp --grant child starts locked too"},
	{[]string{"--profile", "work", "httpapi"}, "httpapi", []string{"--profile", "work"}, classServe, ""},
	{[]string{"bridge", "serve"}, "extension serve", nil, classServe, "bridge is an alias of extension"},
	{[]string{"bridge", "status"}, "extension status", nil, classGated, ""},
	{[]string{"org", "serve", "--foreground"}, "org serve", []string{"--foreground"}, classGated, "org serve is a launcher: gated (D8)"},
	// Aliases resolve to the real command.
	{[]string{"person", "list"}, "people list", nil, classGated, "person is an alias of people"},
	{[]string{"people", "history", "list"}, "people messages list", nil, classGated, "history is an alias of people messages"},
	{[]string{"image", "rm", "x"}, "image delete", []string{"x"}, classGated, "rm is an alias of image delete"},
	// `--` ends the flags and the command words.
	{[]string{"--", "workflow", "list"}, "", []string{"--", "workflow", "list"}, classOpen, "nothing before `--` names a command"},
	{[]string{"workflow", "--", "list"}, "workflow", []string{"--", "list"}, classGated, "`list` is an argument of workflow"},
	{[]string{"doctor", "--", "workflow", "run"}, "doctor", []string{"--", "workflow", "run"}, classOpen, "an open command's arguments are never commands"},
	// help, completion and __complete are added by cobra later: the root.
	{[]string{"help", "workflow", "run"}, "", []string{"help", "workflow", "run"}, classOpen, "cobra adds help while executing"},
	{[]string{"completion", "bash"}, "", []string{"completion", "bash"}, classOpen, "cobra adds completion while executing"},
	{[]string{"__complete", "workflow", ""}, "", []string{"__complete", "workflow", ""}, classOpen, "cobra adds __complete while executing"},
	{[]string{"__completeNoDesc", "workflow", ""}, "", []string{"__completeNoDesc", "workflow", ""}, classOpen, "an alias of __complete"},
	// The help flag does not exist yet when Find runs.
	{[]string{"--help"}, "", []string{"--help"}, classOpen, ""},
	{[]string{"workflow", "--help"}, "workflow", []string{"--help"}, classOpen, "help is open on any command"},
	{[]string{"workflow", "list", "-h"}, "workflow list", []string{"-h"}, classOpen, ""},
	{[]string{"daemon", "--help"}, "daemon", []string{"--help"}, classOpen, ""},
	{[]string{"-vh", "workflow", "list"}, "workflow list", []string{"-vh"}, classOpen, "a short group holding h"},
	{[]string{"--help", "workflow", "list"}, "list", []string{"--help", "workflow"}, classOpen, "Find takes `workflow` for the value of --help and resolves `list`; cobra prints list's help"},
	{[]string{"workflow", "list", "--help=false"}, "workflow list", []string{"--help=false"}, classGated, "help switched off"},
	{[]string{"workflow", "list", "-h=false"}, "workflow list", []string{"-h=false"}, classGated, ""},
	{[]string{"workflow", "run", "--", "--help"}, "workflow run", []string{"--", "--help"}, classGated, "help after `--` is data"},
	{[]string{"--profile", "--help", "workflow", "list"}, "workflow list", []string{"--profile", "--help"}, classGated, "--help is the profile's value, not help"},
}

func TestFindResolvesTheTargetForEveryInvocationForm(t *testing.T) {
	root := newRootCmd()
	applyClassification(root)
	for _, tc := range findCases {
		name := strings.Join(tc.args, " ")
		target, rest, err := root.Find(tc.args)
		if err != nil {
			t.Errorf("%q: Find: %v", name, err)
			continue
		}
		if got := commandKey(target); got != tc.path {
			t.Errorf("%q: resolves %q, want %q", name, got, tc.path)
		}
		if !slices.Equal(rest, tc.rest) {
			t.Errorf("%q: rest %q, want %q", name, rest, tc.rest)
		}
		if got := invocationClass(root, tc.args); got != tc.class {
			t.Errorf("%q: class %q, want %q (%s)", name, got, tc.class, tc.why)
		}
	}
}

// The gate resolves every command, under every spelling of its names, to its
// own class: no command and no alias slips past the table.
func TestGateMatchesTheClassOfEveryCommandAndAlias(t *testing.T) {
	root := newRootCmd()
	applyClassification(root)

	// spellings returns every way to type c's path: each word is the
	// command's name or one of its aliases.
	var spellings func(c *cobra.Command) [][]string
	spellings = func(c *cobra.Command) [][]string {
		var out [][]string
		for _, n := range append([]string{c.Name()}, c.Aliases...) {
			if !c.Parent().HasParent() {
				out = append(out, []string{n})
				continue
			}
			for _, prefix := range spellings(c.Parent()) {
				out = append(out, append(append([]string(nil), prefix...), n))
			}
		}
		return out
	}

	count := map[string]int{}
	spelled := 0
	for _, c := range allCommands(root) {
		want := commandClass(c)
		count[want]++
		for _, args := range spellings(c) {
			spelled++
			if got := invocationClass(root, args); got != want {
				t.Errorf("%q: class %q, want %q", strings.Join(args, " "), got, want)
			}
		}
	}
	t.Logf("%d commands (%v), %d spellings", len(allCommands(root)), count, spelled)
	if count[classGated] < 300 {
		t.Errorf("only %d gated commands: did the table open too much?", count[classGated])
	}
}

// Hidden commands (the nosocial build hides `list` and `template`) are found
// and classified like any other.
func TestHiddenCommandsAreStillResolved(t *testing.T) {
	root := newRootCmd()
	applyClassification(root)
	for name, want := range map[string]string{"status": classGated, "doctor": classOpen, "mcp": classServe} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || commandKey(cmd) != name {
			t.Fatalf("%s: found %v, %v", name, cmd, err)
		}
		cmd.Hidden = true
		if got := invocationClass(root, []string{name}); got != want {
			t.Errorf("hidden %s: class %q, want %q", name, got, want)
		}
	}
}

// recordingTree is the real command tree with every command's run replaced
// by one that records the command: executing it shows which command cobra
// would run, without running anything.
func recordingTree(ran *[]*cobra.Command) *cobra.Command {
	root := newRootCmd()
	applyClassification(root)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.PersistentPreRun = nil
	for _, c := range allCommands(root) {
		c.PersistentPreRun, c.PersistentPreRunE, c.PreRun, c.PreRunE = nil, nil, nil, nil
		if c.RunE != nil || c.Run != nil {
			c.Run = nil
			c.RunE = func(cmd *cobra.Command, _ []string) error { *ran = append(*ran, cmd); return nil }
		}
	}
	return root
}

var classRank = map[string]int{classOpen: 0, classServe: 1, classGated: 2}

// gateAgreesWithCobra fails if cobra would run a command of a stricter class
// than the gate calls the invocation. It reports whether cobra ran one that
// is not open.
func gateAgreesWithCobra(t *testing.T, args []string) bool {
	t.Helper()
	fresh := newRootCmd() // the gate looks before anything has been parsed, as in run
	applyClassification(fresh)
	gate := invocationClass(fresh, args)

	var ran []*cobra.Command
	tree := recordingTree(&ran)
	tree.SetArgs(append([]string{}, args...))
	_ = tree.Execute()

	notOpen := false
	for _, c := range ran {
		if commandClass(c) != classOpen {
			notOpen = true
		}
		if classRank[commandClass(c)] > classRank[gate] {
			t.Errorf("%q: cobra runs %q (%s) but the gate calls it %s", strings.Join(args, " "), commandKey(c), commandClass(c), gate)
		}
	}
	return notOpen
}

// The gate's one safety property, checked against cobra itself: no way to
// write the arguments makes cobra run a command of a stricter class than the
// gate says. First the spike's own rows, then every combination of flags
// before and after a spread of commands.
func TestGateNeverDowngradesWhatCobraWouldRun(t *testing.T) {
	ran := 0
	for _, tc := range findCases {
		if gateAgreesWithCobra(t, tc.args) {
			ran++
		}
	}
	prefixes := [][]string{{}, {"--json"}, {"--profile", "p"}, {"--profile", "--help"}, {"-h"}, {"--help"}, {"-v"},
		{"--no-such-flag"}, {"--help=false"}, {"-vh"}, {"--"}}
	paths := [][]string{{"version"}, {"workflow", "list"}, {"workflow"}, {"org", "run"}, {"org", "serve"}, {"doctor", "fix"},
		{"library", "login"}, {"library", "list"}, {"bridge", "serve"}, {"person", "list"}, {"image", "rm"}, {"account", "status"},
		{"daemon"}, {"daemon", "restart"}, {"mcp"}}
	suffixes := [][]string{{}, {"--json"}, {"--help"}, {"-h"}, {"--help=false"}, {"--profile", "--help"}, {"--", "--help"}, {"extra"}}
	for _, prefix := range prefixes {
		for _, path := range paths {
			for _, suffix := range suffixes {
				if gateAgreesWithCobra(t, append(append(append([]string{}, prefix...), path...), suffix...)) {
					ran++
				}
			}
		}
	}
	t.Logf("cobra ran a command that is not open in %d invocations", ran)
	if ran < 100 {
		t.Errorf("only %d invocations ran such a command: the check is not looking at anything", ran)
	}
}
```

- [ ] **Step 2: Run it and see it fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestFindResolvesTheTargetForEveryInvocationForm|TestHiddenCommandsAreStillResolved|TestGateMatchesTheClassOfEveryCommandAndAlias|TestGateNeverDowngradesWhatCobraWouldRun)$' -count=1
```

Expected: FAIL, build error `undefined: invocationClass`, ending `[build failed]`.

- [ ] **Step 3: Implement the resolution.** Create `cmd/monoagentcli/account_gate_resolve.go`:

```go
package main

import (
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// invocationClass is the class of what running args would execute.
//
// It resolves the target with root.Find on the same tree cobra executes, so
// the answer is the command cobra will run (spike S5, spec §12). Three things
// the plain Find does not say:
//
//   - help, completion, __complete and __completeNoDesc exist only once
//     cobra's ExecuteC has added them, so here they resolve to the root with
//     the arguments left over. So does an unknown command, and a bare root.
//     Cobra prints help or an error for the root and runs nothing: open.
//   - Find runs before the help flag exists and takes `--help` for a flag that
//     wants a value, so whether cobra prints help instead of running the
//     command needs the flags parsed: asksForHelp.
//   - After `--` everything is an argument, and a value flag takes the next
//     word whatever it looks like (`--profile doctor workflow list` is
//     `workflow list`): Find already does both.
func invocationClass(root *cobra.Command, args []string) string {
	target, _, err := root.Find(args)
	if err != nil || target == nil || target == root {
		return classOpen
	}
	class := commandClass(target)
	if class == classOpen || asksForHelp(args) {
		return classOpen
	}
	return class
}

// asksForHelp reports whether cobra will print help for args instead of
// running the command: the help flag parses to true for the resolved command.
// Only the flags decide, so they are parsed the way cobra will (which words
// are flag values, what `--` ends, `--help=false`) on a throwaway tree:
// parsing them on the real one would be repeated by the real run, and a
// repeated string-slice flag would collect its values twice.
func asksForHelp(args []string) bool {
	if !mayAskForHelp(args) {
		return false
	}
	probe := newRootCmd()
	probe.SetOut(io.Discard)
	probe.SetErr(io.Discard)
	cmd, flags, err := probe.Find(args)
	if err != nil || cmd == nil {
		return false
	}
	cmd.InitDefaultHelpFlag()
	if cmd.ParseFlags(flags) != nil {
		return false
	}
	help, err := cmd.Flags().GetBool("help")
	return err == nil && help
}

// mayAskForHelp is the cheap test before the throwaway tree: some word could
// be the help flag (`--help`, `--help=x`, or a short group holding h, as in
// `-h` and `-vh`). It over-approximates; asksForHelp decides.
func mayAskForHelp(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--help" || strings.HasPrefix(a, "--help="):
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "h"):
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run it and see it pass.** Same command as Step 2.

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli	<n>s` (a few seconds: the differential test executes about 1,400 trees). With `-v` it logs `cobra ran a command that is not open in 3xx invocations` and `TestGateMatchesTheClassOfEveryCommandAndAlias` logs the counts (about 376 gated, 24 open, 4 serve, 486 spellings; the totals follow B1b's `account` subtree).

- [ ] **Step 5: Commit.**

```bash
git add cmd/monoagentcli/account_gate_resolve.go cmd/monoagentcli/account_gate_find_test.go
```

```bash
git commit -m "feat(cli): resolve what an invocation would run before the gate decides" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Mutation check (nothing to commit).** In `cmd/monoagentcli/account_gate_resolve.go`, in `invocationClass`, replace `asksForHelp(args)` with `mayAskForHelp(args)`. Run the Step 2 command. Expected: FAIL; `TestFindResolvesTheTargetForEveryInvocationForm` and `TestGateNeverDowngradesWhatCobraWouldRun` name `workflow list --help=false`, `workflow run -- --help` and `--profile --help workflow list` (cobra runs the command, the gate said open). Restore the file with `git checkout -- cmd/monoagentcli/account_gate_resolve.go` and run Step 2 again: PASS.

- [ ] **Step 7: Record the S5 finding.** Open `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md`, creating it with the first line `# Mandatory monoes.me account — spike findings` if it does not exist yet (plan A normally creates it), and add this section at the end:

```markdown
## S5 — how `root.Find` resolves the target (plan B2)

Run against cobra v1.10.2 and the real command tree at master `f4441a2a` (41 top-level commands and 359 below them, plus B1b's `account`). `findCases` in `cmd/monoagentcli/account_gate_find_test.go` is the full table (53 rows). `TestGateNeverDowngradesWhatCobraWouldRun` executes those arguments, and 1,320 combinations of flags around 15 commands, on a tree whose commands record themselves, and fails if cobra ever runs a command of a stricter class than the gate assigns.

| Form | Example | `Find` resolves | Gate |
|---|---|---|---|
| bool global flag first | `--json workflow list` | `workflow list` | gated |
| value global flag first | `--profile work workflow list`, `--db-path /x workflow list` | `workflow list`; the flag and its value stay in the leftover arguments | gated |
| a command name as a flag's value | `--profile doctor workflow list` | `workflow list` | gated |
| unknown flag first | `--no-such-flag workflow list` | `list` (the flag swallows `workflow`; cobra then rejects the flag) | gated |
| flags between the words | `workflow --json list` | `workflow list` | gated |
| alias | `bridge serve`, `person list`, `image rm x` | `extension serve`, `people list`, `image delete` | the real command's |
| hidden command | any command with `Hidden = true` | found like any other | unchanged |
| `--` | `workflow -- list`; `-- doctor` | `workflow`, `-- list` left over; the root | gated; open |
| `help`, `completion`, `__complete`, `__completeNoDesc` | `help workflow run` | the root: cobra adds these inside `ExecuteC`, after the gate has looked | open |
| abbreviation, other case, unknown command | `wor`, `WORKFLOW list`, `nosuch` | the root (no prefix matching, names are case-sensitive); cobra prints its own error | open |
| help flag | `workflow list -h`, `-vh workflow list` | `workflow list` | open |
| help switched off or not a flag | `workflow list --help=false`, `workflow run -- --help`, `--profile --help workflow list` | `workflow list` or `workflow run` | gated |
| help before the command | `--help workflow list` | `list`: the help flag does not exist yet, so `--help` swallows `workflow`; cobra prints `list`'s help | open |

What the gate takes from it:

1. `root.Find(args)` on the tree that will execute is the command cobra runs, so the gate asks it before `ExecuteContext` (`invocationClass`). Aliases and hidden commands need nothing extra.
2. `help`, `completion` and `__complete` do not exist when the gate looks, and an unknown command is cobra's error: all resolve to the root with leftovers, which prints help or an error and runs nothing, so the root is open.
3. Whether the help flag is set cannot be read from `Find` or by scanning words (`--profile --help`, `--help=false`, `--`): `asksForHelp` parses the flags on a throwaway tree, because parsing the real one twice would collect a repeated string-slice flag twice.
4. A locked `daemon`, `httpapi`, `mcp` or `extension serve` must start (spec §6.4), so they get their own class, `serve`. `org serve` is a launcher that starts the external monomind process and returns, so it stays gated.
```

- [ ] **Step 8: Commit the finding.**

```bash
git add docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md
```

```bash
git commit -m "docs(account): spike S5, how root.Find resolves the target" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The gate's verdict, its refusal text and its stderr lines

**Files:**
- Create: `cmd/monoagentcli/account_gate_refusal.go`
- Modify: `cmd/monoagentcli/exitcodes.go` (imports; `exitCodeFor`, after line 58)
- Test: `cmd/monoagentcli/account_gate_refusal_test.go`

**Interfaces:**
- Consumes: Tasks 1 and 2; B1a/B1b: `account.Guard` (`Status`, `EnsureFresh`), `account.CurrentStatus()`, `account.Status` (`State`, `Reason`, `Enforced`, `EnforceFrom`, `GraceUntil`, `Allowed()`), `account.State*`, `account.Reason*`, `*account.LoginRequiredError` (`Error()`: the fixed first line and, for most reasons, a second one), `newLoginRequiredError(st account.Status) error` (exit 4, `JSONErrorFields` with `login_required` and `account`), `isLoginRequired`; `exitCodeFor`, `jsonErrorCode`; `accounttest.Install`, `accounttest.New`, `account.NewGuard`, `account.OpenStore`, `account.NewMemorySealer`, `account.InstallForTest`, `account.SetEnforceFromForTest`.
- Produces (package `main`):

```go
func newGateRefusal(st account.Status) error                          // exit 4, login_required fields, account.LoginRequiredError's text
func gateCommand(ctx context.Context, root *cobra.Command, args []string, g *account.Guard, stderr io.Writer) error
func gateStatus(ctx context.Context, g *account.Guard) account.Status // EnsureFresh, then the verdict; g may be nil; may wait up to the grant timeout after a Ctrl-C
func announce(w io.Writer, st account.Status)                         // the grace line, the warn-period line
// exitCodeFor: any *account.LoginRequiredError, wrapped or not, is exit 4
```

- [ ] **Step 1: Write the failing test.** Create `cmd/monoagentcli/account_gate_refusal_test.go`. `loginRequiredLine` pins the text of index §2; `fakeRefresher` and `installExpiredSession` are reused by Task 4's tests:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// loginRequiredLine is the first line of every refusal (index §2), pinned here
// on purpose: a change to the shared text must be a decision.
const loginRequiredLine = "Log in to monoes.me first: monoagentcli account login"

// gate runs the CLI gate on args against the installed guard.
func gate(args ...string) (stderr string, err error) {
	root := newRootCmd()
	applyClassification(root)
	var buf bytes.Buffer
	err = gateCommand(context.Background(), root, args, account.Current(), &buf)
	return buf.String(), err
}

// fakeRefresher answers every refresh with fail, or else with a fresh token.
type fakeRefresher struct {
	calls atomic.Int32
	fail  error
	next  func() string
}

func (r *fakeRefresher) Refresh(context.Context, string) (*account.TokenSet, error) {
	r.calls.Add(1)
	if r.fail != nil {
		return nil, r.fail
	}
	return &account.TokenSet{AccessToken: r.next(), RefreshToken: "refresh-test-2"}, nil
}

// installExpiredSession installs a guard whose access token ran out an hour
// ago (inside the 24 hours), with a refresh token and a refresher that
// answers fail.
func installExpiredSession(t *testing.T, fail error) *fakeRefresher {
	t.Helper()
	f := accounttest.New(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	expired := f.Token(accounttest.TokenOptions{IssuedAt: f.Clock.Now().Add(-2 * time.Hour)})
	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: expired, User: &account.User{ID: "user-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh("refresh-test-1"); err != nil {
		t.Fatal(err)
	}
	r := &fakeRefresher{fail: fail, next: func() string { return f.Token(accounttest.TokenOptions{}) }}
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: r, Now: f.Clock.Now})
	account.InstallForTest(t, g)
	t.Cleanup(g.Close)
	return r
}

// installUnconfirmedSession installs a guard whose access token ran out an hour ago (inside the 24
// hours), with no refresh token and last_result "unconfirmed": what a refresh whose answer never
// arrived leaves once the guard has dropped the token (A24). The refresher counts what is asked of it.
func installUnconfirmedSession(t *testing.T) *fakeRefresher {
	t.Helper()
	f := accounttest.New(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	expired := f.Token(accounttest.TokenOptions{IssuedAt: f.Clock.Now().Add(-2 * time.Hour)})
	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: expired, User: &account.User{ID: "user-1"},
		LastAttempt: f.Clock.Now().Add(-time.Hour), LastResult: string(account.ReasonUnconfirmed)}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRefresher{next: func() string { return f.Token(accounttest.TokenOptions{}) }}
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: r, Now: f.Clock.Now})
	account.InstallForTest(t, g)
	t.Cleanup(g.Close)
	return r
}

// A refusal is the text of account.LoginRequiredError: the fixed first line
// and, for the five reasons of spec §6.1 and for unconfirmed (A24), where signing
// in again is not the whole story, a second one. It exits 4 and is a
// login-required error.
func TestRefusalTextPerReason(t *testing.T) {
	for _, r := range []account.Reason{account.ReasonExpired, account.ReasonRefused, account.ReasonClockRollback,
		account.ReasonClockSkew, account.ReasonKeyUnknown, account.ReasonUnconfirmed} {
		err := newGateRefusal(account.Status{State: account.StateLocked, Reason: r, Enforced: true})
		lines := strings.Split(err.Error(), "\n")
		if len(lines) != 2 || lines[0] != loginRequiredLine || lines[1] == "" || exitCodeFor(err) != 4 || !isLoginRequired(err) {
			t.Errorf("%s: message %q, exit %d", r, err, exitCodeFor(err))
		}
	}
	if got := newGateRefusal(account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}).Error(); got != loginRequiredLine {
		t.Errorf("not logged in: message %q, want the first line alone", got)
	}
}

// A refusal that comes out of a command (the engine, an agent turn, a browser
// action) exits 4 as well, not 1, wrapped or not.
func TestAnyLoginRequiredErrorExitsFour(t *testing.T) {
	err := fmt.Errorf("agent turn: %w", &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonRefused}})
	if exitCodeFor(err) != 4 || jsonErrorCode(err) != "auth_or_connection" {
		t.Errorf("exit %d, code %q", exitCodeFor(err), jsonErrorCode(err))
	}
	if exitCodeFor(errors.New("other")) != 1 {
		t.Error("an ordinary error must keep exit 1")
	}
}

// Locked: a gated command is refused, a long-running one starts and says why
// on stderr, an open one passes in silence, whatever flags come first. Only
// the gate runs here: the serving commands would serve for real.
func TestGateWhenLocked(t *testing.T) {
	accounttest.Install(t, accounttest.LockedRefused)
	why := (&account.LoginRequiredError{Status: account.CurrentStatus()}).Error()

	for _, args := range [][]string{{"workflow", "list"}, {"--profile", "work", "person", "list"}, {"daemon", "restart"},
		{"daemon", "install"}, {"extension", "status"}, {"org", "run"}, {"org", "serve"}, {"org", "serve", "--foreground"},
		{"workflow", "list", "--help=false"}} {
		if stderr, err := gate(args...); err == nil || err.Error() != why || stderr != "" {
			t.Errorf("gated %q: %v, stderr %q", args, err, stderr)
		}
	}
	for _, args := range [][]string{{"daemon"}, {"httpapi"}, {"mcp"}, {"bridge", "serve"}, {"--profile", "work", "extension", "serve"}} {
		if stderr, err := gate(args...); err != nil || stderr != why+"\n" {
			t.Errorf("serving %q: %v, stderr %q", args, err, stderr)
		}
	}
	for _, args := range [][]string{{"doctor"}, {"doctor", "fix", "x"}, {"account", "login"}, {"--profile", "work", "account", "status"},
		{"update", "--app"}, {"setup"}, {"version"}, {"library", "login"}, {"library", "status", "--json"}, {"ref", "node", "http.request"},
		{"workflow", "list", "--help"}, {"daemon", "--help"}, {"help"}, {"completion", "bash"}, {"__complete", "work"}} {
		if stderr, err := gate(args...); err != nil || stderr != "" {
			t.Errorf("open %q: %v, stderr %q", args, err, stderr)
		}
	}
}

// A gated command renews a session that is due before it is judged, so a
// blocked account is found out and a renewable one never reaches the grace
// line; an open command asks nothing of monoes.me.
func TestGateRefreshesBeforeGatedCommandsOnly(t *testing.T) {
	r := installExpiredSession(t, nil)
	for _, args := range [][]string{{"completion", "bash"}, {"doctor"}, {"account", "login"}} {
		if _, err := gate(args...); err != nil || r.calls.Load() != 0 {
			t.Fatalf("%q: %v, %d refreshes: an open command must not refresh", args, err, r.calls.Load())
		}
	}
	if stderr, err := gate("workflow", "list"); err != nil || stderr != "" || r.calls.Load() != 1 {
		t.Errorf("gated: %v, stderr %q, %d refreshes, want a silent pass after 1", err, stderr, r.calls.Load())
	}
}

// The one line an allowed command owes the user goes to stderr; open commands
// and a dormant gate stay silent.
func TestGateAnnouncesGraceAndWarning(t *testing.T) {
	t.Run("grace", func(t *testing.T) {
		installExpiredSession(t, &account.TransientError{Reason: account.ReasonUnreachable, Settled: true, Err: errors.New("offline")})
		stderr, err := gate("workflow", "list")
		st := account.CurrentStatus()
		want := "monoes.me is unreachable; this login works offline until " + st.GraceUntil.Local().Format(time.RFC3339) + "\n"
		if err != nil || st.State != account.StateGrace || stderr != want {
			t.Errorf("state %s: %v, stderr %q, want %q", st.State, err, stderr, want)
		}
		if stderr, _ := gate("doctor"); stderr != "" {
			t.Errorf("an open command printed %q", stderr)
		}
	})
	// A24: monoes.me is not the problem and the refresh token is gone, so the line does not say "unreachable":
	// it says that the login cannot be renewed on this machine, until when it works and what to do. Nothing is
	// asked of monoes.me: there is no refresh token to present.
	t.Run("grace after a refresh whose answer never arrived", func(t *testing.T) {
		r := installUnconfirmedSession(t)
		stderr, err := gate("workflow", "list")
		st := account.CurrentStatus()
		want := "This login can no longer be renewed on this machine and works until " + st.GraceUntil.Local().Format(time.RFC3339) +
			". Sign in again: monoagentcli account login\n"
		if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed || stderr != want || r.calls.Load() != 0 {
			t.Errorf("state %s/%s: %v, stderr %q, want %q, %d refresh calls", st.State, st.Reason, err, stderr, want, r.calls.Load())
		}
	})
	t.Run("warn period", func(t *testing.T) {
		accounttest.Install(t, accounttest.LockedNoLogin)
		date := time.Now().Add(72 * time.Hour)
		account.SetEnforceFromForTest(t, date)
		want := "A monoes.me login will be required from " + date.Local().Format("2006-01-02") + ": monoagentcli account login\n"
		if stderr, err := gate("workflow", "list"); err != nil || stderr != want {
			t.Errorf("%v, stderr %q, want %q", err, stderr, want)
		}
	})
	t.Run("dormant", func(t *testing.T) {
		accounttest.Install(t, accounttest.Dormant)
		if stderr, err := gate("workflow", "list"); err != nil || stderr != "" {
			t.Errorf("%v, stderr %q, want silence", err, stderr)
		}
	})
}
```

- [ ] **Step 2: Run it and see it fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestRefusalTextPerReason|TestAnyLoginRequiredErrorExitsFour|TestGateWhenLocked|TestGateRefreshesBeforeGatedCommandsOnly|TestGateAnnouncesGraceAndWarning)$' -count=1
```

Expected: FAIL, build errors `undefined: gateCommand` and `undefined: newGateRefusal`, ending `[build failed]`.

- [ ] **Step 3: Implement the gate.** Create `cmd/monoagentcli/account_gate_refusal.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
)

// gateRefusal is what a refused command fails with: exit 4 and the
// login_required fields of newLoginRequiredError, under the text of
// account.LoginRequiredError (the fixed first line and, for most reasons, a
// second one), so the gate says what the other doors say.
type gateRefusal struct {
	error
	msg string
}

func (e *gateRefusal) Error() string { return e.msg }
func (e *gateRefusal) Unwrap() error { return e.error }

func newGateRefusal(st account.Status) error {
	return &gateRefusal{error: newLoginRequiredError(st), msg: (&account.LoginRequiredError{Status: st}).Error()}
}

// gateCommand is the CLI gate. It returns nil to let the command run and the
// refusal to fail it. Before it judges, a gated or serving command renews the
// session when that is due (an offline process pays one 2-second attempt a
// minute at most). g is nil when no guard could be built; the verdict is then
// account.CurrentStatus's, which is locked once enforcement is on.
func gateCommand(ctx context.Context, root *cobra.Command, args []string, g *account.Guard, stderr io.Writer) error {
	class := invocationClass(root, args)
	if class == classOpen {
		return nil
	}
	st := gateStatus(ctx, g)
	switch {
	case st.Allowed():
		announce(stderr, st)
	case class == classServe:
		// It starts anyway and refuses the work itself: say why, once, on
		// stderr (an MCP server's stdout is its protocol).
		fmt.Fprintln(stderr, newGateRefusal(st))
	default:
		return newGateRefusal(st)
	}
	return nil
}

// gateStatus renews the session when that is due and reads the verdict. EnsureFresh may block for
// up to the guard's grant timeout (20 seconds) after a Ctrl-C while a refresh grant is in flight:
// the guard never abandons a grant it has sent (A20), because the answer holds the only copy of
// the refresh token that replaces the one monoes.me has already rotated. The signals stay caught
// until the command ends, so a second Ctrl-C does not shorten the wait; only SIGKILL does.
func gateStatus(ctx context.Context, g *account.Guard) account.Status {
	if g == nil {
		return account.CurrentStatus()
	}
	_, _ = g.EnsureFresh(ctx) // refreshes when due, never when dormant; the verdict is read back
	return g.Status()
}

// announce is the one line an allowed command owes the user, on stderr only:
// the grace line when the login works offline (or, once a refresh whose answer
// never arrived has cost this machine its refresh token, A24, the line that says
// to sign in again), the warning when a login will be required from a date.
// While enforcement is dormant (no date) it says nothing.
func announce(w io.Writer, st account.Status) {
	if st.EnforceFrom.IsZero() {
		return
	}
	switch st.State {
	case account.StateGrace:
		if st.Reason == account.ReasonUnconfirmed { // monoes.me is not the problem
			fmt.Fprintf(w, "This login can no longer be renewed on this machine and works until %s. Sign in again: monoagentcli account login\n",
				st.GraceUntil.Local().Format(time.RFC3339))
			return
		}
		fmt.Fprintf(w, "monoes.me is unreachable; this login works offline until %s\n", st.GraceUntil.Local().Format(time.RFC3339))
	case account.StateLocked: // allowed only before the date
		fmt.Fprintf(w, "A monoes.me login will be required from %s: monoagentcli account login\n", st.EnforceFrom.Local().Format("2006-01-02"))
	}
}
```

- [ ] **Step 4: Map a refusal that comes out of a command to exit 4.** `exitCodeFor` knows `*cliError`, the protocol error and the workflow sentinels, so a `*account.LoginRequiredError` that the engine, an agent turn or a browser action returns (layer 2) would exit 1. In `cmd/monoagentcli/exitcodes.go` add `"github.com/monoes/mono-agent/internal/account"` to the imports (before `internal/monomind`) and, in `exitCodeFor` right after the `cliError` check (after line 58), add:

```go
	// A layer-2 refusal that comes out of a command (the engine, an agent turn,
	// a browser action) is the same login_required failure the CLI gate reports.
	var lr *account.LoginRequiredError
	if errors.As(err, &lr) {
		return 4
	}
```

- [ ] **Step 5: Run it and see it pass.** Same command as Step 2. Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli	<n>s`. Then the existing exit-code tests: `go test ./cmd/monoagentcli/ -run '^(TestExitCodeFor_ProtocolErrorPreservesSpecificCode|TestExitCodeFor_CliErrorStillWinsOverGenericDefault|TestExitCodeFor_NilIsZero)$' -count=1` prints `ok`.

- [ ] **Step 6: Commit.**

```bash
git add cmd/monoagentcli/account_gate_refusal.go cmd/monoagentcli/account_gate_refusal_test.go cmd/monoagentcli/exitcodes.go
```

```bash
git commit -m "feat(cli): the account gate's verdict, refusal text and stderr lines" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Mutation checks (nothing to commit).** Each must fail a named test; restore with `git checkout -- <file>` after each.
  - In `account_gate_refusal.go`, delete the line `_, _ = g.EnsureFresh(ctx) // …` in `gateStatus`: `TestGateRefreshesBeforeGatedCommandsOnly` FAILS.
  - In `account_gate_refusal.go`, replace `case class == classServe:` with `case false:` in `gateCommand`: `TestGateWhenLocked` FAILS (the serving commands are refused).
  - In `exitcodes.go`, change `return 4` to `return 1` in the `*account.LoginRequiredError` block: `TestAnyLoginRequiredErrorExitsFour` FAILS.

---

### Task 4: `run()`: the guard, the gate, the late refresher

**Files:**
- Modify: `cmd/monoagentcli/main.go` (imports at lines 18-20; `func main()`, lines 65-92, replaced by the block below)
- Test: `cmd/monoagentcli/account_run_test.go`

**Interfaces:**
- Consumes: Task 3's `gateCommand`; `account.Current() *Guard`, `account.Install(g *Guard)` (`Install(nil)` removes it), `account.NewDefaultGuard() (*Guard, error)`, `(*Guard).Close()`, `(*Guard).StartRefresher(ctx)`, `(*Guard).OnRefused(fn func(Status))`, `account.LateRefresher`; from package `main`: `reportCommandError`, `exitCodeFor`, `jsonErrorCode`, `jsonErrorFields`, `valueFlagName`, `newRootCmd`, `invocationClass`, `offlineEnv` and `healthEnvHook` (`doctor_readonly_test.go`, `doctor_env.go`), and Task 3's test helpers `loginRequiredLine`, `installExpiredSession`.
- Produces (package `main`):

```go
func main()                                                        // os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)), crash report kept
func run(args []string, stdout, stderr io.Writer) int
func reportGateRefusal(args []string, err error, stdout, stderr io.Writer)
func argsWantJSON(args []string) bool
var newDefaultGuard = account.NewDefaultGuard                     // seam: a test makes it fail
func processGuard() (*account.Guard, func())
func cancelsOnRefusal(class string) bool                           // only gated commands: a refused daemon keeps serving (D8)
var cancelWhenRefused = func(g *account.Guard, cancel context.CancelFunc)   // seam; B3a's request for spec §6.4
var afterFunc = time.AfterFunc                                     // seams: a test sees the timer armed and fired
var startRefresher = func(ctx context.Context, g *account.Guard) { g.StartRefresher(ctx) }
var notifyContext = signal.NotifyContext                           // seam: a test sees when the signals are let go
func armLateRefresher(ctx context.Context, g *account.Guard) (disarm func())
```

- [ ] **Step 1: Write the failing test.** Create `cmd/monoagentcli/account_run_test.go`. `workflow list` is the "allowed" command (it opens the database and prints its table header straight to the real stdout, so a few stray lines and `applied migration` log lines appear in the output: they are not failures); the refusal tests use only harmless commands, because a broken gate would run them.

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// freshHome points HOME at an empty folder, as on a machine nothing has run on.
func freshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// runMain runs the command line as main does and returns its exit code and output.
func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func requireEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("the command wrote %s to HOME", e.Name())
	}
}

// filesUnder lists every file below dir, as slash-separated paths relative to it, in lexical order.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// guardOverHome makes run build, as a real process does, a guard over the account folder of the current
// HOME, on the fixture's clock so that a test can move it, with a sealer of its own that keeps the
// operating system's key store out of it.
func guardOverHome(t *testing.T, f *accounttest.Fixture) {
	t.Helper()
	prev := newDefaultGuard
	newDefaultGuard = func() (*account.Guard, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		store := account.OpenStore(filepath.Join(home, ".monoagent", "account"), account.NewMemorySealer())
		return account.NewGuard(account.GuardOptions{Store: store, Now: f.Clock.Now}), nil
	}
	t.Cleanup(func() { newDefaultGuard = prev })
}

// A gated command with no session fails before cobra runs a single hook: exit
// 4, the message on stderr, nothing on stdout, and nothing written but the
// clock-guard record of a machine that never signed in (A25): not the first-run
// marker, not the database, not a refresh token. The commands are harmless ones:
// a broken gate would run them. run builds the guard over HOME, as a real
// process does, on the fixture's clock.
func TestRunRefusesGatedCommandsWhenLocked(t *testing.T) {
	account.InstallForTest(t, nil)
	f := accounttest.New(t) // trusts the test key; the date is a day before the fixture's clock: enforced
	home := freshHome(t)
	guardOverHome(t, f)
	for _, args := range [][]string{
		{"workflow", "list"}, {"--profile", "work", "workflow", "list"}, {"person", "list"}, {"config", "list"},
		{"workflow", "list", "--help=false"}, {"--profile", "--help", "workflow", "list"},
	} {
		code, stdout, stderr := runMain(t, args...)
		if code != 4 || stdout != "" || stderr != loginRequiredLine+"\n" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
	// What the refusals left: the two files of the record, a session with no token whose high-water
	// mark is the clock, and nothing else.
	want := []string{".monoagent/account/session.json", ".monoagent/account/session.lock"}
	if got := filesUnder(t, home); !slices.Equal(got, want) {
		t.Fatalf("the refusals left %v in HOME, want %v", got, want)
	}
	sess, err := account.OpenStore(filepath.Join(home, ".monoagent", "account"), account.NewMemorySealer()).Load()
	if err != nil || sess == nil || sess.AccessToken != "" || sess.State != "" || !sess.HW.Equal(f.Clock.Now()) {
		t.Fatalf("the record: present %v (%v), want a session with no token and the clock as its high-water mark", sess != nil, err)
	}
}

// A25: the record outlives the process. A machine that has been refused once after the date is refused
// again when its clock is then set back to before the date, which is what the record is for.
func TestRunRefusesAgainWhenTheClockIsSetBackBeforeTheDate(t *testing.T) {
	account.InstallForTest(t, nil)
	f := accounttest.New(t)
	freshHome(t)
	guardOverHome(t, f)
	if code, _, stderr := runMain(t, "workflow", "list"); code != 4 || stderr != loginRequiredLine+"\n" {
		t.Fatalf("after the date: exit %d, stderr %q", code, stderr)
	}
	f.Clock.Set(account.EnforceDate().Add(-48 * time.Hour)) // the clock is set to two days before the date
	if code, stdout, stderr := runMain(t, "workflow", "list"); code != 4 || stdout != "" || stderr != loginRequiredLine+"\n" {
		t.Errorf("with the clock set back: exit %d, stdout %q, stderr %q, want the refusal again", code, stdout, stderr)
	}
}

// A25 writes its record once the date has been reached, and not before: in the warn period, and while the
// gate is dormant, an allowed gated command leaves no account folder, not even the lock. (The same clock on
// a machine that has been refused is the test above.)
func TestRunWritesNothingToTheAccountFolderBeforeTheDateOrWhileDormant(t *testing.T) {
	account.InstallForTest(t, nil)
	f := accounttest.New(t)
	guardOverHome(t, f)
	date := account.EnforceDate()
	for _, tc := range []struct {
		name    string
		clock   time.Time
		dormant bool
		stderr  string // what the allowed command says
	}{
		{"the warn period", date.Add(-48 * time.Hour), false,
			"A monoes.me login will be required from " + date.Local().Format("2006-01-02") + ": monoagentcli account login\n"},
		{"dormant", date.Add(48 * time.Hour), true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dormant {
				account.SetEnforceFromForTest(t, time.Time{})
			}
			f.Clock.Set(tc.clock)
			home := freshHome(t)
			if code, _, stderr := runMain(t, "workflow", "list"); code != 0 || stderr != tc.stderr {
				t.Errorf("exit %d, stderr %q, want exit 0 and %q", code, stderr, tc.stderr)
			}
			if _, err := os.Stat(filepath.Join(home, ".monoagent", "account")); !os.IsNotExist(err) {
				t.Errorf("the guard made the account folder: %v", err)
			}
		})
	}
}

// With --json anywhere in the arguments the refusal is one JSON document on
// stdout, and the text stays on stderr; the value of --profile is not --json.
func TestRunRefusalIsOneJSONDocumentUnderJSON(t *testing.T) {
	accounttest.Install(t, accounttest.LockedNoLogin)
	freshHome(t)
	for _, args := range [][]string{{"workflow", "list", "--json"}, {"--profile", "work", "--json", "config", "list"}} {
		code, stdout, stderr := runMain(t, args...)
		var doc struct {
			Error         string `json:"error"`
			Code          string `json:"code"`
			LoginRequired bool   `json:"login_required"`
			Account       struct {
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"account"`
		}
		dec := json.NewDecoder(strings.NewReader(stdout))
		if err := dec.Decode(&doc); err != nil || dec.More() {
			t.Fatalf("%q: stdout is not one JSON document: %q (%v)", args, stdout, err)
		}
		if code != 4 || !strings.HasPrefix(stderr, loginRequiredLine) || !strings.HasPrefix(doc.Error, loginRequiredLine) ||
			doc.Code != "auth_or_connection" || !doc.LoginRequired || doc.Account.State != "locked" || doc.Account.Reason != "not_logged_in" {
			t.Errorf("%q: exit %d, stderr %q, document %+v", args, code, stderr, doc)
		}
	}
	if _, stdout, _ := runMain(t, "--profile", "--json", "workflow", "list"); stdout != "" {
		t.Errorf("--json as a profile name printed %q", stdout)
	}
}

// Tab completion, help, the completion scripts and the open commands keep
// working for someone who is signed out: cobra adds its own commands after the
// gate has looked, and the table opens the rest.
func TestOpenCommandsStayOpenWhenLocked(t *testing.T) {
	accounttest.Install(t, accounttest.LockedRefused)
	home := freshHome(t)
	var called sync.Map
	healthEnvHook = offlineEnv(t, &called)
	t.Cleanup(func() { healthEnvHook = nil })

	for _, tc := range []struct {
		args     []string
		wantCode int
		want     string // in stdout
	}{
		{[]string{"help", "workflow"}, 0, "Usage:"},
		{[]string{"completion", "bash"}, 0, "bash completion"},
		{[]string{"__complete", "work"}, 0, "workflow"},
		{[]string{"__completeNoDesc", "work"}, 0, "workflow"},
		{[]string{"workflow", "list", "--help"}, 0, "Usage:"},
		{[]string{"--help"}, 0, "Available Commands:"},
		{[]string{}, 0, "Available Commands:"},
		// A fresh machine fails doctor's required data-folder check: exit 1, not 4.
		{[]string{"--db-path", filepath.Join(home, ".monoagent", "monoagent.db"), "--json", "doctor", "--group", "core"}, 1, `"v": 1`},
	} {
		code, stdout, stderr := runMain(t, tc.args...)
		if code != tc.wantCode || !strings.Contains(stdout, tc.want) || strings.Contains(stderr, loginRequiredLine) {
			t.Errorf("%q: exit %d, want %d; stdout has %q: %v; stderr %q",
				tc.args, code, tc.wantCode, tc.want, strings.Contains(stdout, tc.want), stderr)
		}
	}
}

// Allowed, a gated command runs (here `workflow list` opens the database).
// In grace its one line goes to stderr and never reaches stdout, JSON included.
func TestRunLetsGatedCommandsRunWhenAllowed(t *testing.T) {
	for name, mode := range map[string]accounttest.Mode{"signed in": accounttest.SignedIn, "dormant, not signed in": accounttest.Dormant} {
		accounttest.Install(t, mode)
		home := freshHome(t)
		code, _, stderr := runMain(t, "workflow", "list")
		if code != 0 || stderr != "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
		}
		if _, err := os.Stat(filepath.Join(home, ".monoagent", "monoagent.db")); err != nil {
			t.Errorf("%s: the command did not run: %v", name, err)
		}
	}

	installExpiredSession(t, &account.TransientError{Reason: account.ReasonUnreachable, Settled: true, Err: errors.New("offline")})
	freshHome(t)
	code, stdout, stderr := runMain(t, "workflow", "list", "--json")
	if code != 0 || !strings.HasPrefix(stderr, "monoes.me is unreachable; this login works offline until ") || strings.Contains(stdout, "unreachable") {
		t.Errorf("grace: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// The refresher of a process that outlives its first five minutes is armed
// for every command, and a command that ends sooner disarms it.
func TestRunArmsTheLateRefresher(t *testing.T) {
	g := accounttest.Install(t, accounttest.SignedIn)
	freshHome(t)
	var delay time.Duration
	var fire func()
	timer := time.NewTimer(time.Hour)
	var started *account.Guard
	prevAfter, prevStart := afterFunc, startRefresher
	afterFunc = func(d time.Duration, f func()) *time.Timer { delay, fire = d, f; return timer }
	startRefresher = func(_ context.Context, g *account.Guard) { started = g }
	t.Cleanup(func() { afterFunc, startRefresher = prevAfter, prevStart })

	if code, _, _ := runMain(t, "completion", "bash"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if fire == nil || delay != account.LateRefresher {
		t.Fatalf("armed %v after %v, want after %v", fire != nil, delay, account.LateRefresher)
	}
	fire()
	if started != g {
		t.Error("the timer did not start the installed guard's refresher")
	}
	if timer.Stop() {
		t.Error("the timer was left armed after the command ended")
	}
}

// A gated command gets the cancel: the refusal of the login ends its context.
// Serving and open commands do not get it.
func TestRunGivesOnlyGatedCommandsTheCancel(t *testing.T) {
	accounttest.Install(t, accounttest.SignedIn)
	freshHome(t)
	calls := 0
	prev := cancelWhenRefused
	cancelWhenRefused = func(*account.Guard, context.CancelFunc) { calls++ }
	t.Cleanup(func() { cancelWhenRefused = prev })

	runMain(t, "completion", "bash")
	runMain(t, "doctor", "--help")
	if calls != 0 {
		t.Fatalf("%d cancels for open commands", calls)
	}
	runMain(t, "workflow", "list")
	if calls != 1 {
		t.Errorf("%d cancels for a gated command, want 1", calls)
	}
}

// Only a gated one-shot command is ended by a refusal: a refused daemon must
// not cancel its own context (D8).
func TestOnlyGatedCommandsAreCancelledOnRefusal(t *testing.T) {
	for class, want := range map[string]bool{classGated: true, classServe: false, classOpen: false} {
		if got := cancelsOnRefusal(class); got != want {
			t.Errorf("%s: cancelsOnRefusal = %v, want %v", class, got, want)
		}
	}
}

// The default cancel ends the context when monoes.me refuses the login.
func TestCancelWhenRefusedEndsTheContext(t *testing.T) {
	installExpiredSession(t, &account.RefusedError{Description: "revoked"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelWhenRefused(account.Current(), cancel)
	_, _ = account.Current().Refresh(context.Background()) // answered invalid_grant: the guard locks and fires OnRefused
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context was not cancelled")
	}
}

// With nobody signed in, an open command makes the guard run installs create
// no file, even once the gate is enforced: the clock-guard record (A25) is
// written by a guard pass, and an open command runs none (scripts/doctor-smoke.sh
// asserts the same of the real binary). run removes the guard it built. A guard
// installed beforehand is left alone.
func TestRunCreatesNothingForOpenCommands(t *testing.T) {
	account.InstallForTest(t, nil)
	accounttest.New(t) // enforced: the date is in the past, so a guard pass would write the record
	home := freshHome(t)
	var called sync.Map
	healthEnvHook = offlineEnv(t, &called)
	t.Cleanup(func() { healthEnvHook = nil })
	built := 0
	prev := newDefaultGuard
	newDefaultGuard = func() (*account.Guard, error) { built++; return account.NewDefaultGuard() }
	t.Cleanup(func() { newDefaultGuard = prev })

	code, _, _ := runMain(t, "--db-path", filepath.Join(home, ".monoagent", "monoagent.db"), "--json", "doctor", "--group", "core")
	if code != 1 { // a fresh home fails the required data-folder check
		t.Fatalf("doctor exit %d, want 1", code)
	}
	if built != 1 || account.Current() != nil {
		t.Errorf("built %d default guards, still installed: %v", built, account.Current() != nil)
	}
	requireEmpty(t, home)

	g := accounttest.Install(t, accounttest.SignedIn)
	runMain(t, "completion", "bash")
	if built != 1 || account.Current() != g {
		t.Error("run replaced or removed a guard that was installed before it")
	}
}

// When no guard can be built a gated command fails closed once enforcement is
// on, and runs while it is dormant.
func TestRunFailsClosedWhenNoGuardCanBeBuilt(t *testing.T) {
	account.InstallForTest(t, nil)
	prev := newDefaultGuard
	newDefaultGuard = func() (*account.Guard, error) { return nil, errors.New("no home directory") }
	t.Cleanup(func() { newDefaultGuard = prev })
	accounttest.New(t) // the date is in the past: enforced
	freshHome(t)
	if code, _, stderr := runMain(t, "workflow", "list"); code != 4 || !strings.HasPrefix(stderr, loginRequiredLine) {
		t.Errorf("enforced: exit %d, stderr %q", code, stderr)
	}
	account.SetEnforceFromForTest(t, time.Time{})
	if code, _, _ := runMain(t, "workflow", "list"); code != 0 {
		t.Errorf("dormant: exit %d", code)
	}
}

// The signals are let go before the guard is released. Releasing the guard (its Close) waits for a
// refresh grant that monoes.me is still answering, up to the guard's call timeout, because a grant that
// was sent is never abandoned (A20); signal.NotifyContext keeps catching the signals until its stop
// function runs, so with the guard released first a second Ctrl-C during that wait would be swallowed
// and only SIGKILL would end the process. The guard run built is still installed when the signals are
// let go, and is gone after.
func TestRunLetsGoOfTheSignalsBeforeItReleasesTheGuard(t *testing.T) {
	account.InstallForTest(t, nil)
	freshHome(t)
	g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())})
	prevGuard := newDefaultGuard
	newDefaultGuard = func() (*account.Guard, error) { return g, nil }
	t.Cleanup(func() { newDefaultGuard = prevGuard })
	var guardWhenLetGo *account.Guard
	prevNotify := notifyContext
	notifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, stop := prevNotify(parent, sigs...)
		return ctx, func() { guardWhenLetGo = account.Current(); stop() }
	}
	t.Cleanup(func() { notifyContext = prevNotify })

	if code, _, _ := runMain(t, "completion", "bash"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if guardWhenLetGo != g {
		t.Error("the signals were let go after the guard was released: a second Ctrl-C during Close would be swallowed")
	}
	if account.Current() != nil {
		t.Error("run left the guard it built installed")
	}
}

// A nil slice must not make cobra read the test binary's own arguments, and
// an ordinary command error keeps its exit code and its place.
func TestRunKeepsCobrasOwnBehaviour(t *testing.T) {
	accounttest.Install(t, accounttest.Dormant)
	freshHome(t)
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "Available Commands:") {
		t.Errorf("nil args: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	if code, stdout, stderr := runMain(t, "nosuch"); code != 1 || stdout != "" || !strings.Contains(stderr, `unknown command "nosuch"`) {
		t.Errorf("unknown command: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
```

- [ ] **Step 2: Run it and see it fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestRunRefusesGatedCommandsWhenLocked|TestRunRefusesAgainWhenTheClockIsSetBackBeforeTheDate|TestRunWritesNothingToTheAccountFolderBeforeTheDateOrWhileDormant|TestRunRefusalIsOneJSONDocumentUnderJSON|TestOpenCommandsStayOpenWhenLocked|TestRunLetsGatedCommandsRunWhenAllowed|TestRunGivesOnlyGatedCommandsTheCancel|TestOnlyGatedCommandsAreCancelledOnRefusal|TestCancelWhenRefusedEndsTheContext|TestRunArmsTheLateRefresher|TestRunCreatesNothingForOpenCommands|TestRunFailsClosedWhenNoGuardCanBeBuilt|TestRunLetsGoOfTheSignalsBeforeItReleasesTheGuard|TestRunKeepsCobrasOwnBehaviour)$' -count=1
```

Expected: FAIL, build errors such as `undefined: run`, `undefined: afterFunc`, `undefined: startRefresher`, `undefined: cancelWhenRefused` and `undefined: notifyContext`, ending `[build failed]`.

- [ ] **Step 3: Implement `run`.** In `cmd/monoagentcli/main.go` add `"github.com/monoes/mono-agent/internal/account"` to the imports (before `internal/i18n`), then replace the whole existing `func main() {…}` (lines 65-92) with:

```go
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

	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the command line without the process: it gates args on the monoes.me
// account (account_gate.go), executes them and returns the exit code, so a
// test can call it. Tests that call newRootCmd().Execute() skip the gate by
// design; the engine and the runners judge the account themselves.
func run(args []string, stdout, stderr io.Writer) int {
	if args == nil {
		args = []string{} // cobra reads os.Args for a nil slice
	}

	// Locale must be resolved before newRootCmd() builds the command tree,
	// since cobra Short/Long/Example strings are evaluated once at
	// construction time, before flags are parsed. See internal/i18n and
	// docs/i18n.md.
	i18n.SetLocale(i18n.Detect(args))

	// The guard is built before the signals are caught, so that the signals are let go before the guard
	// is released: deferred calls run last in, first out. release (the guard's Close) waits for a
	// refresh grant that monoes.me is still answering, up to the guard's call timeout, because a grant
	// that was sent is never abandoned (A20), and signal.NotifyContext keeps catching the signals
	// until its stop function runs. With the guard released first, a second Ctrl-C during that wait
	// would be swallowed and only SIGKILL would end the process.
	g, release := processGuard()
	defer release()

	// SIGHUP too: a CLI whose terminal or parent goes away must cancel, so
	// commands end what they started (monoes/mono-agent#235).
	ctx, cancel := notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	root := newRootCmd()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	applyClassification(root)

	if g != nil {
		defer armLateRefresher(ctx, g)()
	}

	// The gate comes before anything runs: no first-run check, no database.
	if err := gateCommand(ctx, root, args, g, stderr); err != nil {
		reportGateRefusal(args, err, stdout, stderr)
		return exitCodeFor(err)
	}
	if g != nil && cancelsOnRefusal(invocationClass(root, args)) {
		cancelWhenRefused(g, cancel)
	}
	if err := root.ExecuteContext(ctx); err != nil {
		reportCommandError(args, err, stdout, stderr)
		return exitCodeFor(err)
	}
	return 0
}

// reportGateRefusal prints a refusal: its text on stderr and, when --json
// appears anywhere in the arguments, the one JSON document the commands' own
// wrappers would have printed (the gate runs before them, so it prints it).
func reportGateRefusal(args []string, err error, stdout, stderr io.Writer) {
	fmt.Fprintln(stderr, err)
	if !argsWantJSON(args) {
		return
	}
	body := map[string]any{"error": err.Error()}
	if code := jsonErrorCode(err); code != "" {
		body["code"] = code
	}
	var fields jsonErrorFields
	if errors.As(err, &fields) {
		for k, v := range fields.JSONErrorFields() {
			if k != "error" {
				body[k] = v
			}
		}
	}
	b, _ := json.Marshal(body)
	fmt.Fprintln(stdout, string(b))
}

// argsWantJSON reports whether --json is among the arguments. The value of a
// global value flag is skipped, as wantsJSONError does, so `--profile --json`
// names a profile.
func argsWantJSON(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--json" || a == "--json=true" {
			return true
		}
		if name, isFlag := valueFlagName(a); isFlag && name != "" {
			i++
		}
	}
	return false
}

// newDefaultGuard builds the guard of a real process; a test swaps it to see
// what happens when none can be built.
var newDefaultGuard = account.NewDefaultGuard

// processGuard returns this process's account guard and what to call when the
// command is done. A guard installed before run (only a test does that) is
// used as it is. Otherwise run builds and installs the default guard over
// ~/.monoagent/account, which creates nothing until a guard pass or a sign-in
// writes something: from the enforcement date the first pass of a machine that
// never signed in writes the clock-guard record (A25). If it cannot be built
// there is none: a gated command then fails closed once enforcement is on, and
// runs before.
func processGuard() (*account.Guard, func()) {
	if g := account.Current(); g != nil {
		return g, func() {}
	}
	g, err := newDefaultGuard()
	if err != nil {
		return nil, func() {}
	}
	account.Install(g)
	return g, func() {
		account.Install(nil)
		g.Close()
	}
}

// cancelsOnRefusal says whether a command of this class is ended when the
// login is refused while it runs (spec §6.4): only a gated one-shot command.
// A serving command keeps serving and refuses each call (D8); an open one does
// not depend on the login.
func cancelsOnRefusal(class string) bool { return class == classGated }

// cancelWhenRefused ends a command's work when monoes.me refuses the login
// while it runs. A test swaps it to see who is given it.
var cancelWhenRefused = func(g *account.Guard, cancel context.CancelFunc) {
	g.OnRefused(func(st account.Status) {
		if !st.Allowed() {
			cancel()
		}
	})
}

// afterFunc and startRefresher are what armLateRefresher uses; a test swaps
// them to see the timer armed and fired without waiting five minutes.
var (
	afterFunc      = time.AfterFunc
	startRefresher = func(ctx context.Context, g *account.Guard) { g.StartRefresher(ctx) }
)

// notifyContext is signal.NotifyContext; a test swaps it to see when run lets go of the signals.
var notifyContext = signal.NotifyContext

// armLateRefresher starts g's background refresher once the process has run
// for account.LateRefresher, so a long `workflow run` or `chat` keeps its
// login fresh (spec §6.4). The serving commands start it at once in their own
// RunE. The returned func disarms it: a command that ends sooner never starts it.
func armLateRefresher(ctx context.Context, g *account.Guard) (disarm func()) {
	t := afterFunc(account.LateRefresher, func() { startRefresher(ctx, g) })
	return func() { t.Stop() }
}
```

`reportCommandError`, `wantsJSONError` and `valueFlagName` below it stay as they are.

- [ ] **Step 4: Run it and see it pass.** Same command as Step 2. Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli	<n>s`.

- [ ] **Step 5: Run the whole package.** The twelve test files that build `newRootCmd()` directly are untouched and must still pass; this run takes several minutes (run it in the background if the shell's time limit is shorter).

```bash
go test ./cmd/monoagentcli/ -count=1 -timeout 20m
```

Expected: the only failures are the known ones on pristine master (macOS): `TestCaptureTaskFilesOnTheBoard`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCoderConversationFolders`. Any other failure is yours until proven otherwise.

- [ ] **Step 6: Commit.**

```bash
git add cmd/monoagentcli/main.go cmd/monoagentcli/account_run_test.go
```

```bash
git commit -m "feat(cli): run() gates every command before cobra runs it" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Mutation checks (nothing to commit).** Each must fail a named test; restore with `git checkout -- cmd/monoagentcli/main.go` after each.
  - Replace `if err := gateCommand(ctx, root, args, g, stderr); err != nil {` with `if err := error(nil); err != nil {`: `TestRunRefusesGatedCommandsWhenLocked` and `TestRunRefusalIsOneJSONDocumentUnderJSON` FAIL.
  - Delete `applyClassification(root)`: `TestOpenCommandsStayOpenWhenLocked` FAILS (every command is gated, `doctor` included).
  - Delete `defer armLateRefresher(ctx, g)()`: `TestRunArmsTheLateRefresher` FAILS.
  - Delete `cancelWhenRefused(g, cancel)`: `TestRunGivesOnlyGatedCommandsTheCancel` FAILS.
  - Move `g, release := processGuard()` and `defer release()` below `defer cancel()` (the guard released before the signals are let go): `TestRunLetsGoOfTheSignalsBeforeItReleasesTheGuard` FAILS.
  - In `cancelsOnRefusal`, replace `class == classGated` with `class != classOpen`: `TestOnlyGatedCommandsAreCancelledOnRefusal` FAILS (a refused daemon would cancel itself).

- [ ] **Step 8: Run the gate tests under the race detector.**

```bash
go test -race ./cmd/monoagentcli/ -count=1 -timeout 15m -run '^(TestEveryCommandIsClassified|TestGateMatchesTheClassOfEveryCommandAndAlias|TestFindResolvesTheTargetForEveryInvocationForm|TestGateNeverDowngradesWhatCobraWouldRun|TestRefusalTextPerReason|TestAnyLoginRequiredErrorExitsFour|TestGateWhenLocked|TestGateRefreshesBeforeGatedCommandsOnly|TestGateAnnouncesGraceAndWarning|TestRunRefusesGatedCommandsWhenLocked|TestRunRefusesAgainWhenTheClockIsSetBackBeforeTheDate|TestRunWritesNothingToTheAccountFolderBeforeTheDateOrWhileDormant|TestRunRefusalIsOneJSONDocumentUnderJSON|TestOpenCommandsStayOpenWhenLocked|TestRunLetsGatedCommandsRunWhenAllowed|TestRunGivesOnlyGatedCommandsTheCancel|TestOnlyGatedCommandsAreCancelledOnRefusal|TestCancelWhenRefusedEndsTheContext|TestRunArmsTheLateRefresher|TestRunCreatesNothingForOpenCommands|TestRunFailsClosedWhenNoGuardCanBeBuilt|TestRunLetsGoOfTheSignalsBeforeItReleasesTheGuard|TestRunKeepsCobrasOwnBehaviour)$'
```

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli	<n>s` and no `DATA RACE`.

---

### Task 5: The `core.monoes_account` doctor row

**Files:**
- Create: `internal/health/monoes_account.go`, `internal/health/monoes_account_test.go`, `cmd/monoagentcli/doctor_env_monoes.go`, `cmd/monoagentcli/doctor_env_monoes_test.go`
- Modify: `internal/health/registry.go` (after lines 6 and 17), `internal/health/health.go` (before line 213), `cmd/monoagentcli/doctor_env.go` (after line 123), `cmd/monoagentcli/doctor_readonly_test.go` (line 36)

**Interfaces:**
- Consumes: `account.CurrentStatus()`, `account.Status`; `health.Check`, `health.Fix`, `health.Env`, `FixUpdate` (`internal/health/core.go:33`); `runDoctor`, `decodeDoctor`, `resultByID` (`doctor_test.go`).
- Produces:

```go
// internal/health
const CheckMonoesAccount = "core.monoes_account"
const FixMonoesLogin = "core.monoes_account.login"        // manual: `monoagentcli account login`
type AccountInfo struct { State, Reason, Email string; GraceUntil, EnforceFrom time.Time; Enforced bool }
// Env gains: MonoesAccount func(ctx context.Context) AccountInfo
// cmd/monoagentcli
func addMonoesAccountHook(env *health.Env)
func healthAccount(st account.Status) health.AccountInfo
```

The row is not required (a locked account never makes `doctor` exit 1), reads only the guard's verdict (no refresh, no network), has a manual fix (`doctor --fix` and `setup` never try a login), and in the dormant phase is `info`.

- [ ] **Step 1: Write the failing health test.** Create `internal/health/monoes_account_test.go`:

```go
package health

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Every state the session can be in, with enforcement dormant, in its warn
// period and on: what the row says and what it offers.
func TestMonoesAccountCheck(t *testing.T) {
	date := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC)
	on := func(a AccountInfo) AccountInfo { a.EnforceFrom, a.Enforced = date, true; return a }
	warn := func(a AccountInfo) AccountInfo { a.EnforceFrom = date; return a }
	cases := []struct {
		name    string
		in      AccountInfo
		status  Status
		fix     string
		summary string
	}{
		{"signed in", on(AccountInfo{State: "ok", Email: "a@b.c"}), StatusOK, "", "signed in as a@b.c"},
		{"signed in, no address", on(AccountInfo{State: "ok"}), StatusOK, "", "signed in"},
		{"offline", on(AccountInfo{State: "grace", Reason: "unreachable", GraceUntil: until}), StatusWarn, "", "monoes.me is unreachable; this login works offline until " + until.Local().Format("2006-01-02 15:04")},
		{"server error", on(AccountInfo{State: "grace", Reason: "server_error", GraceUntil: until}), StatusWarn, "", "answered with an error"},
		{"key store", on(AccountInfo{State: "grace", Reason: "keyring_unavailable", GraceUntil: until}), StatusWarn, "", "key store cannot be opened"},
		{"unconfirmed, still working", on(AccountInfo{State: "grace", Reason: "unconfirmed", GraceUntil: until}), StatusWarn, FixMonoesLogin, "answer never arrived"},
		{"not signed in", on(AccountInfo{State: "locked", Reason: "not_logged_in"}), StatusFail, FixMonoesLogin, "not signed in to monoes.me"},
		{"refused", on(AccountInfo{State: "locked", Reason: "refused"}), StatusFail, FixMonoesLogin, "monoes.me ended this login"},
		{"expired", on(AccountInfo{State: "locked", Reason: "expired"}), StatusFail, FixMonoesLogin, "more than 24 hours"},
		{"unknown key", on(AccountInfo{State: "locked", Reason: "key_unknown"}), StatusFail, FixUpdate, "does not know the key"},
		{"unconfirmed, ended", on(AccountInfo{State: "locked", Reason: "unconfirmed"}), StatusFail, FixMonoesLogin, "answer never arrived"},
		{"warn period", warn(AccountInfo{State: "locked", Reason: "not_logged_in"}), StatusWarn, FixMonoesLogin, "required from " + date.Local().Format("2006-01-02")},
		{"dormant, not signed in", AccountInfo{State: "locked", Reason: "not_logged_in"}, StatusInfo, "", "not required yet"},
		{"dormant, refused", AccountInfo{State: "locked", Reason: "refused"}, StatusInfo, "", "not required yet"},
		{"dormant, offline", AccountInfo{State: "grace", Reason: "unreachable", GraceUntil: until}, StatusInfo, "", "works offline"},
		{"dormant, unconfirmed", AccountInfo{State: "grace", Reason: "unconfirmed", GraceUntil: until}, StatusInfo, "", "answer never arrived"},
		{"dormant, signed in", AccountInfo{State: "ok", Email: "a@b.c"}, StatusOK, "", "signed in as"},
		{"unknown state", AccountInfo{}, StatusSkip, "", "not available"},
	}
	for _, tc := range cases {
		res := checkMonoesAccount(context.Background(), &Env{MonoesAccount: func(context.Context) AccountInfo { return tc.in }})
		if res.Status != tc.status || res.FixID != tc.fix || !strings.Contains(res.Summary, tc.summary) {
			t.Errorf("%s: %q %q fix %q, want %q fix %q containing %q", tc.name, res.Status, res.Summary, res.FixID, tc.status, tc.fix, tc.summary)
		}
	}
	if res := checkMonoesAccount(context.Background(), &Env{}); res.Status != StatusSkip {
		t.Errorf("no hook: %q, want skip", res.Status)
	}
}

// The registered row is a core row (never a required one), its fix is
// manual and names the command, and it sits among the core rows so the
// printed report keeps one core heading.
func TestMonoesAccountRowIsRegisteredInTheCoreGroup(t *testing.T) {
	reg := Default()
	rep := reg.Run(context.Background(), &Env{MonoesAccount: func(context.Context) AccountInfo {
		return AccountInfo{State: "locked", Reason: "not_logged_in", Enforced: true, EnforceFrom: time.Now().Add(-time.Hour)}
	}}, Options{IDs: []string{CheckMonoesAccount}})
	if len(rep.Results) != 1 {
		t.Fatalf("%d rows", len(rep.Results))
	}
	r := rep.Results[0]
	if r.ID != CheckMonoesAccount || r.Group != GroupCore || r.Required || r.Fix == nil ||
		r.Fix.ID != FixMonoesLogin || r.Fix.Safety != SafetyManual || r.Fix.Command != "monoagentcli account login" {
		t.Fatalf("row %+v fix %+v", r, r.Fix)
	}
	first, n := -1, 0
	for i, c := range reg.Checks() {
		if c.Group == GroupCore {
			if first < 0 {
				first = i
			}
			n++
		}
	}
	for i := first; i < first+n; i++ {
		if reg.Checks()[i].Group != GroupCore {
			t.Fatalf("the core checks are split: %s sits among them", reg.Checks()[i].ID)
		}
	}
}
```

- [ ] **Step 2: Run it and see it fail.**

```bash
go test ./internal/health/ -run '^(TestMonoesAccountCheck|TestMonoesAccountRowIsRegisteredInTheCoreGroup)$' -count=1
```

Expected: FAIL, build errors such as `undefined: AccountInfo` and `undefined: FixMonoesLogin`, ending `[build failed]`.

- [ ] **Step 3: Implement the check.** Create `internal/health/monoes_account.go`:

```go
package health

import (
	"context"
	"fmt"
	"time"
)

// The monoes.me sign-in has its own row in the core group (spec D25): the
// accounts group is about the logins of other platforms.
const (
	CheckMonoesAccount = "core.monoes_account"
	FixMonoesLogin     = "core.monoes_account.login"
)

// AccountInfo is the monoes.me session as this machine sees it: an
// account.Status without the dependency. Reading it is local, so the check
// changes nothing and uses no network.
type AccountInfo struct {
	State       string // ok, grace or locked; "" when unknown
	Reason      string
	Email       string
	GraceUntil  time.Time
	EnforceFrom time.Time // zero while enforcement is dormant
	Enforced    bool
}

func monoesAccountChecks() []Check {
	return []Check{{ID: CheckMonoesAccount, Group: GroupCore, Title: "monoes.me account",
		Features: []string{"every command except version, ref, update, doctor, setup and account"}, Run: checkMonoesAccount}}
}

// The fix is manual: signing in needs a browser or an emailed code, so
// `doctor --fix` and `setup` only name the command.
func monoesAccountFixes() []Fix {
	manual := func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }
	return []Fix{{FixInfo: FixInfo{ID: FixMonoesLogin, Label: "Sign in to monoes.me", Safety: SafetyManual,
		Command: "monoagentcli account login"}, Apply: manual}}
}

// lockedWhy says why a login is locked, by the reason the session reports.
var lockedWhy = map[string]string{
	"not_logged_in":  "not signed in to monoes.me",
	"expired":        "the login expired: monoes.me has not been reachable for more than 24 hours",
	"refused":        "monoes.me ended this login",
	"clock_rollback": "the system clock is earlier than the last time this login was used",
	"clock_skew":     "the system clock is more than 5 minutes behind monoes.me",
	"key_unknown":    "this build does not know the key monoes.me signs logins with",
	"invalid":        "the stored login is not valid",
	"unconfirmed":    "monoes.me may have received a refresh whose answer never arrived, so this machine stopped using its saved login",
}

// checkMonoesAccount: ok when signed in; warn when the login works offline
// (grace) or will be required from a date; fail when locked. While
// enforcement is dormant (no date) it never warns or fails, and says so.
func checkMonoesAccount(ctx context.Context, env *Env) Result {
	if env.MonoesAccount == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	a := env.MonoesAccount(ctx)
	dormant := a.EnforceFrom.IsZero()
	why := lockedWhy[a.Reason]
	if why == "" {
		why = "not signed in to monoes.me"
	}
	switch a.State {
	case "ok":
		if a.Email != "" {
			return Result{Status: StatusOK, Summary: "signed in as " + a.Email}
		}
		return Result{Status: StatusOK, Summary: "signed in"}
	case "grace":
		res := Result{Status: StatusWarn, Summary: graceSummary(a)}
		switch {
		case dormant:
			res.Status = StatusInfo
		case a.Reason == "unconfirmed": // this machine cannot renew the login: the user has to sign in again (A24)
			res.FixID = FixMonoesLogin
		}
		return res
	case "locked":
		switch {
		case dormant:
			res := Result{Status: StatusInfo, Summary: "not signed in; a monoes.me sign-in is not required yet"}
			if a.Reason != "not_logged_in" {
				res.Detail = why
			}
			return res
		case !a.Enforced:
			return Result{Status: StatusWarn, FixID: FixMonoesLogin, Detail: why,
				Summary: "a monoes.me login will be required from " + a.EnforceFrom.Local().Format("2006-01-02")}
		case a.Reason == "key_unknown":
			return Result{Status: StatusFail, Summary: why, FixID: FixUpdate}
		}
		return Result{Status: StatusFail, Summary: why, FixID: FixMonoesLogin}
	}
	return Result{Status: StatusSkip, Summary: "not available"}
}

// graceSummary says why the login was not renewed, and until when it works.
func graceSummary(a AccountInfo) string {
	until := a.GraceUntil.Local().Format("2006-01-02 15:04")
	switch a.Reason {
	case "keyring_unavailable":
		return "the key store cannot be opened, so the login is not renewed; it works offline until " + until
	case "server_error":
		return "monoes.me answered with an error; this login works offline until " + until
	case "unconfirmed":
		return "monoes.me may have received a refresh whose answer never arrived, so this machine stopped using its saved login; it works until " + until + ". Sign in again"
	}
	return "monoes.me is unreachable; this login works offline until " + until
}
```

- [ ] **Step 4: Register the row and add the hook.** The row goes with the core checks so the printed report keeps one `core` heading. In `internal/health/registry.go`, add one line after line 6 and one after line 17:

```go
	checks = append(checks, coreChecks()...)
	checks = append(checks, monoesAccountChecks()...)
```

```go
	fixes = append(fixes, coreFixes()...)
	fixes = append(fixes, monoesAccountFixes()...)
```

In `internal/health/health.go`, add the hook to `Env` above the `// Accounts of the active profile.` comment (line 213):

```go
	// MonoesAccount reads the monoes.me session from this machine (no
	// network, nothing written).
	MonoesAccount func(ctx context.Context) AccountInfo

```

- [ ] **Step 5: Run it and see it pass.**

```bash
go test ./internal/health/ -run '^(TestMonoesAccountCheck|TestMonoesAccountRowIsRegisteredInTheCoreGroup|TestDefaultRegistryIsConsistent)$' -count=1
```

Expected: `ok  	github.com/monoes/mono-agent/internal/health	<n>s`.

- [ ] **Step 6: Write the failing CLI test.** Create `cmd/monoagentcli/doctor_env_monoes_test.go`:

```go
package main

import (
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/health"
)

// doctor reports the machine's monoes.me session in one core row, and it
// stays usable (exit 0, the row is not required) in every state.
func TestDoctorReportsTheMonoesAccount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   accounttest.Mode
		status health.Status
		fix    string
	}{
		{"signed in", accounttest.SignedIn, health.StatusOK, ""},
		{"locked", accounttest.LockedNoLogin, health.StatusFail, health.FixMonoesLogin},
		{"refused", accounttest.LockedRefused, health.StatusFail, health.FixMonoesLogin},
		{"dormant", accounttest.Dormant, health.StatusInfo, ""},
	} {
		accounttest.Install(t, tc.mode)
		out, err := runDoctor(t, t.TempDir(), "doctor", "--json", "--check", health.CheckMonoesAccount)
		if err != nil {
			t.Fatalf("%s: doctor: %v\n%s", tc.name, err, out)
		}
		row := resultByID(decodeDoctor(t, out), health.CheckMonoesAccount)
		fix := ""
		if row.Fix != nil {
			fix = row.Fix.ID
		}
		if row.Status != tc.status || fix != tc.fix || row.Group != health.GroupCore || row.Required {
			t.Errorf("%s: row %+v", tc.name, row)
		}
	}
}

func TestHealthAccountMapsTheStatus(t *testing.T) {
	date, until := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC)
	got := healthAccount(account.Status{State: account.StateGrace, Reason: account.ReasonServerError, User: &account.User{Email: "a@b.c"},
		GraceUntil: until, EnforceFrom: date, Enforced: true})
	want := health.AccountInfo{State: "grace", Reason: "server_error", Email: "a@b.c", GraceUntil: until, EnforceFrom: date, Enforced: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := healthAccount(account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}); got.Email != "" || got.State != "locked" {
		t.Errorf("no user: %+v", got)
	}
}
```

- [ ] **Step 7: Run it and see it fail.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestDoctorReportsTheMonoesAccount|TestHealthAccountMapsTheStatus)$' -count=1
```

Expected: FAIL, build error `undefined: healthAccount`, ending `[build failed]`.

- [ ] **Step 8: Wire the hook.** Create `cmd/monoagentcli/doctor_env_monoes.go`:

```go
package main

import (
	"context"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/health"
)

// addMonoesAccountHook feeds the doctor's core.monoes_account row from the
// process guard's verdict. It is a read of the session already loaded: no
// refresh and no network, because a check must change nothing.
func addMonoesAccountHook(env *health.Env) {
	env.MonoesAccount = func(context.Context) health.AccountInfo {
		return healthAccount(account.CurrentStatus())
	}
}

func healthAccount(st account.Status) health.AccountInfo {
	info := health.AccountInfo{State: string(st.State), Reason: string(st.Reason), GraceUntil: st.GraceUntil,
		EnforceFrom: st.EnforceFrom, Enforced: st.Enforced}
	if st.User != nil {
		info.Email = st.User.Email
	}
	return info
}
```

In `cmd/monoagentcli/doctor_env.go`, in `newHealthEnv`, add one line after `addServiceHooks(env, cfg)` (line 123):

```go
	addMonoesAccountHook(env)
```

- [ ] **Step 9: Keep the hook real in the read-only test.** The hook only reads, so `TestDoctorChangesNothingInAnyGroup` should run the real one rather than `offlineEnv`'s zero-value stand-in. In `cmd/monoagentcli/doctor_readonly_test.go`, line 36 becomes:

```go
	"MonomindProjects": true, "Automations": true, "MonoesAccount": true,
```

- [ ] **Step 10: Run the doctor tests.**

```bash
go test ./cmd/monoagentcli/ -run '^(TestDoctorReportsTheMonoesAccount|TestHealthAccountMapsTheStatus|TestDoctorChangesNothingInAnyGroup|TestDoctorFreshHomeFailsThenFixes)$' -count=1
```

Expected: `ok  	github.com/monoes/mono-agent/cmd/monoagentcli	<n>s`.

- [ ] **Step 11: Commit.**

```bash
git add internal/health/monoes_account.go internal/health/monoes_account_test.go internal/health/registry.go internal/health/health.go cmd/monoagentcli/doctor_env_monoes.go cmd/monoagentcli/doctor_env_monoes_test.go cmd/monoagentcli/doctor_env.go cmd/monoagentcli/doctor_readonly_test.go
```

```bash
git commit -m "feat(doctor): the core.monoes_account row" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 12: Verify the whole change.**

```bash
go build ./...
go vet ./...
gofmt -l .
go build -tags nosocial -o /dev/null ./cmd/monoagentcli
go build -tags devaccount -o /dev/null ./cmd/monoagentcli
go test ./internal/health/ -count=1
go test -tags nosocial ./cmd/monoagentcli/ -count=1 -run '^(TestEveryCommandIsClassified|TestAccountClassesNameRealCommands|TestUnlistedCommandsAreGated|TestGateMatchesTheClassOfEveryCommandAndAlias|TestFindResolvesTheTargetForEveryInvocationForm|TestHiddenCommandsAreStillResolved|TestGateNeverDowngradesWhatCobraWouldRun)$'
go test ./cmd/monoagentcli/ -count=1 -timeout 20m
```

Expected: no output from the first three commands, then the builds succeed silently, then `ok` for the health package and for the nosocial run (`list` and `template` are hidden there and still classified), then the same four known failures as in Task 4 Step 5 and nothing else. `scripts/doctor-smoke.sh` runs in CI (Linux) and still passes while enforcement is dormant: a fresh HOME shows `info core.monoes_account  not signed in; a monoes.me sign-in is not required yet`. To run it on macOS, set `SHELL=/usr/bin/true` (the login shell would otherwise write `.zsh_sessions` into the throwaway HOME) and put a `timeout` shim on `PATH`.

---

## Notes for other plans

- **B3a.** Its requests 2 and 3 (`docs/mastermind/plans/2026-10-05-monoes-account-gate-b3a-runners.md`, Contract change requests) are taken here: a gated one-shot command's context is cancelled when the login is refused (`cancelWhenRefused`; serving and open commands keep going), and any `*account.LoginRequiredError`, wrapped or not, exits 4 (`exitCodeFor`). The JSON fields of such an error need B1a's method (request 4 below). The four serving commands reach their `RunE` even when locked; the gate has already printed the refusal on stderr (never stdout: an MCP server's stdout is its protocol) and does not refuse them. `org serve` stays gated, `--foreground` included (the lead's ruling): a locked one exits 4 at the gate; one already running that becomes locked is B3a's layers. `setup` and `doctor fix services.daemon.start` start the daemon by spawning `monoagentcli daemon` (`cmd/monoagentcli/doctor_env_services.go:125`, `:242`), and `registerClaudeMCP` registers a launch of `mcp` (`:377`): without the `serve` class those children would exit 4 on a locked machine.
- **B5a.** (a) Once a date is set, a fresh HOME makes `core.monoes_account` `fail`, which breaks `scripts/doctor-smoke.sh:90` (`all(.results[]; .group == "core" and .status != "fail")` after `setup --group core --yes`); the row is not required, so no exit code changes, but that assertion needs `and .id != "core.monoes_account"` or a signed-in smoke. (b) Its table row for the Docker `ENTRYPOINT daemon` says the daemon is gated: with the `serve` class a container with no sign-in starts, stays up and logs the command to run (spec §9). (c) `daemon restart` stays gated, so `update` restarting the daemon in its own process (its decision 6) is right.
- **B5b.** `TestOpenListMatchesTheGate` should compare the documented open list with the commands whose annotation is `open`; the serving commands (`serve`) are a separate list. The reason lists of the documentation gain `unconfirmed` (A24): a grace reason, then `locked`, with the sentence of B1a's `errors.go`.
- **A25 (B3a, B3b, B5a, B5c).** From the enforcement date the guard of a machine that never signed in writes a session with no token, so a refused gated command on a fresh HOME leaves `account/session.lock` and `account/session.json`, and a serving command that the gate lets through while locked leaves them too. A test that runs `run()` or the real binary on a fresh HOME after the date and then asserts that HOME is empty must expect those two files (this plan's `TestRunRefusesGatedCommandsWhenLocked` is the model: the guard over HOME on the fixture's clock, `filesUnder`); one that asserts it for an open command, for the warn period or while the gate is dormant still holds. A test that installs its guard with `accounttest.Install` has the account folder in a store of its own and is not affected.

## Contract change requests

1. **A third gate class, `serve` (index §3.5 and the open-list bullet of §2). Approved by the lead.** The annotation `monoagent.account` gains the value `serve`; the table lists `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`) as `serve`; `daemon install`, `daemon restart` and `daemon uninstall` stay gated, and so does `org serve`, `--foreground` included. A serving command passes the CLI gate when locked, still gets `EnsureFresh` and the grace and warn lines, prints the refusal once on stderr, and leaves enforcement to layers 2 and 3. Reason: D6's "everything else is gated" contradicts D8 (a locked daemon stays up and never exits on the lock, because launchd's `KeepAlive` respawns it: `internal/autostart/autostart_darwin.go:33`, and `docker-compose.yml:16` restarts it too), §6.3 and index §3.4.8 (MCP starts and answers `initialize` and `tools/list` while locked), §3.4.5 and §3.4.7 (`/health` stays open, the bridge's `ping` reports the state) and §9 ("a locked daemon logs the exact command to run"): each needs the process to start locked. `org serve` is by default a launcher that starts the detached monomind org process and returns (`cmd/monoagentcli/org_process.go:28-52`), so a refusal causes no respawn loop, and starting monomind while locked would defeat D8. Proposed wording: index §3.5 "annotation values `open`, `gated` or `serve`", and one added bullet in §2: "Serving commands (spec §6.4) start even when locked: `daemon`, `httpapi`, `mcp`, `extension serve`." If rejected, this plan loses one table block, the `case class == classServe:` arm, `pinnedServe` and the `serve` rows.
2. **`account.Install(nil)` removes the process guard; `account.InstallForTest(t, nil)` installs none and restores the previous guard on cleanup. Approved by the lead.** B1a's plan carries both, each pinned by a test. `run()` removes the guard it built, so tests do not leak it, and five tests start from "no guard installed".
3. **`account` has exactly the subcommands `login`, `logout` and `status` (B1b). Approved by the lead.** They are in `pinnedOpen`; any other subcommand needs a deliberate edit there.
4. **B1a: `func (e *LoginRequiredError) JSONErrorFields() map[string]any`.** Reason: index §3.4.2 says any command that returns `*account.LoginRequiredError` prints the `login_required` document under `--json`, but `withJSONErrors` (`cmd/monoagentcli/automation.go:56`) and `reportCommandError` add fields only for errors that implement `jsonErrorFields`. Proposed: it returns `{"login_required": true, "account": {"state": string(e.Status.State), "reason": string(e.Status.Reason)}}`, the keys of B1b's `loginRequiredError`, and no other change is needed. B1a's plan now carries it (`TestLoginRequiredErrorJSONFields`).
5. **Behaviour the tests rely on, no change asked.** `accounttest.Install` installs the guard it returns as `account.Current()`; `accounttest.New(t)` trusts its key, sets enforcement in the past and gives a `Clock` that `Fixture.Token` follows; a `Guard` over an expired-inside-24-hours token with a refresh token renews it on `EnsureFresh`, or, when the `Refresher` answers `*account.TransientError{Reason: account.ReasonUnreachable, Settled: true}` (an offline monoes.me: the request never left), stays in `grace` with `GraceUntil = iat + 24h`, and an unsettled failure (the zero value: the request may have been processed, A24) stays in grace too but keeps `pending_since` and drops the refresh token after 240 seconds as `unconfirmed`; a `Refresher` answering `*account.RefusedError` makes `Refresh` fire the `OnRefused` callbacks on their own goroutines; `account.CurrentStatus()` with no guard is `locked(not_logged_in)` with `Enforced` and `EnforceFrom` filled in; `(*LoginRequiredError).Error()` is the fixed first line (`account.LoginRequiredMessage`) plus, for the reasons that have one, a second line.
