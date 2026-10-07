# Task Board, Phase 5 (the macOS menu) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `monoagentcli task os install` puts "Add to MonoAgent Tasks: <profile>" in the Services menu of every Mac app; choosing it files the selected text into that profile's Inbox through `monoagentcli task add --stdin --source os`, with `status` and `uninstall` to manage the menus.

**Architecture:** A new package `internal/tasks/osmenu` renders the Quick Action from three embedded templates (the shell script, `Info.plist`, `document.wflow`) and reads, writes and removes bundles in a folder it is given. It is plain Go, so its tests run on Linux CI. `cmd/monoagentcli/task_os.go` adds `task os install|status|uninstall` to P1's `task` group: it resolves the profile, picks `~/Library/Services` (or `--dest`), refuses away from macOS with a runtime check (no build tag), and asks `pbs` to read the folder again. The menu's script hands the selected text to monoagentcli on standard input; P1's store does the rest (Inbox only, cleaning, limits, the agent rule).

**Tech Stack:** Go (`text/template`, `embed`, `encoding/xml`, `os/exec`), cobra, P1's `internal/tasks`. macOS tools only at run time and in darwin-only tests: `/usr/bin/osascript`, `/System/Library/CoreServices/pbs`, `/usr/bin/plutil`, `/usr/bin/automator`. No new dependencies.

**Spec:** `docs/mastermind/specs/2026-10-05-task-board-design.md`, sections 2 (D6, D7, D26, D27, D31), 4.6, 5.1, 5.3, 7, 12, 13, 14, 17.6. Read section 12 first.

**Evidence:** the bundle copies Apple's own Quick Actions on this Mac (`/System/Library/Services/Show Map.workflow` for the Run Shell Script action, `Add to Music as a Spoken Track.workflow` for a plain-text service) and follows Apple's Services Properties documentation, which asks for an `NSRequiredContext` in every service, empty when nothing is filtered (Ruling 4). That Services lists the item on the user's Mac stays unverified until they run `task os install` (spec 17.6).

**Branch:** `feat/tasks-board-os`, stacked on `feat/tasks-board` (P1). P1 must be complete on it (all thirteen P1 tasks committed).

## Global Constraints

- Commands (spec 12): `monoagentcli task os install [--profile NAME_OR_ID] [--dest DIR]`, `status`, `uninstall [--profile NAME_OR_ID]`, macOS only (elsewhere: exit 3 and the recipe; the group is in the command tree on every platform and refuses at run time). A menu item files into one profile: the one named, else the active profile at install time, which the command prints. `install` and `uninstall` are the operator's: an agent is refused with `operator_only` before the arguments or the platform are looked at; `status` is open to an agent (Ruling 7).
- The bundle `Add to MonoAgent Tasks (<profile name>).workflow` goes to `~/Library/Services` (`--dest` for tests; characters a file name cannot hold are replaced, the profile id is the identity): `Contents/Info.plist` with an `NSServices` entry (menu item `Add to MonoAgent Tasks: <profile name>`, message `runWorkflowAsService`, receives text) and marker keys naming the CLI path, version and profile id, and `Contents/document.wflow` with one Run Shell Script action that reads the selection from standard input and runs `"<absolute CLI path>" --profile "<profile id>" task add --stdin --source os --app "<frontmost app>"`, then shows a notification ("Added to Inbox in <profile name>") (as built: `--flag=value` and `--db-path`, Ruling 1). The text never reaches a shell command line. If the profile has been deleted since, the CLI refuses (exit 3), nothing is filed, and the notification says so.
- Install is idempotent: the same content reports "already installed"; a managed bundle whose CLI path or profile name is stale is rewritten; a bundle of the same name that has no marker is refused unless `--force`. `status` lists the managed bundles with their profile and state (current, stale, profile gone); `uninstall` removes only a managed bundle. After installing it prints where the item appears (right-click selected text, Services) and that macOS may need it enabled once in System Settings, Keyboard, Keyboard Shortcuts, Services, Text.
- A capture lands in Inbox only (spec 5.1). `--source os` from an agent (an agent-context marker, `--as`, or `MONOAGENT_ACTOR`) is refused, `invalid_input` (spec 7). Windows and Linux: no installer; the docs give a hotkey recipe (D27).
- Exit codes 2 (not found) and 3 (invalid or refused); `--json` snake_case, arrays never null, errors `{"error","code"}` on stdout.
- No new network surface, no new dependency, files under 500 lines.
- Commits are `feat(tasks): ...` or `docs(tasks): ...`, each ending with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Environment rules for whoever runs this plan

- You work in the worktree the lead created for `feat/tasks-board-os`. Do not push, merge or switch branches. Other Claude sessions use this repository; touch nothing outside this worktree.
- In this repo's worktree sessions the Bash tool refuses compound git commands and heredocs: run `git add <files>` and `git commit -m "<subject>" -m "<trailer>"` as two separate calls, and write files with the Write and Edit tools.
- A project hook blocks Bash commands whose text contains destructive SQL or `rm -r`. Test code may contain SQL; never put it in a Bash command.
- Go commands use the real Go caches; do not override `HOME` for them. Tests isolate themselves (a temporary `HOME`, a temporary database, temporary folders). Any built `monoagentcli` runs under a throwaway `HOME` only: every CLI run may install skills into `~/.claude/skills`.
- Never write to the real `~/Library/Services`, never run `task os install` or `uninstall` against the real `HOME`, and never run `pbs -update`: the tests replace the refresh, and the smoke run passes `--dest` and a throwaway `HOME`. `plutil -lint`, `plutil -extract` and `pbs -read_bundle` only read; run them only on files under a test's temporary folder or the session scratchpad. `/usr/bin/automator` runs only in the opt-in test of Task 5, against stubs.
- Builder subagents run at most two at a time on this machine. Run only the tests a task names (targeted runs); Task 8 runs the two touched packages in full once. Never select the doctor tests of `cmd/monoagentcli` with a loose `-run` such as `Doc` (one of them hangs when selected that way).
- The code in this plan was written without a compiler. A compile slip (an unused variable, a shadowed name, a missing import) is fixed in place and the task goes on. A failing assertion is different: read the spec section the test comes from before changing the test or the code, and say which of the two was wrong. If `gofmt -l` lists a file, run `gofmt -w` on it.
- Invisible or look-alike characters are written as eight-digit Go escapes (`\U0000202E`); the write tool turns four-digit escapes into raw characters. After writing a file of this phase, run `grep -nP '[^\x00-\x7F]' <file>` and expect no output (every new file is ASCII).
- No daemon or app is started.

## Rulings (decisions the spec does not state)

1. Ruling: the menu's script also passes `--db-path=<absolute database path>`, and every value as `--flag=value` - the profile id belongs to one database, and a value that begins with "-" must never read as a flag - cost if wrong: the path is pinned even for the default database, so a moved database or home folder needs `task os install` again (`status` reports such a menu stale).
2. Ruling: "version" in the marker is the template's revision (`MonoAgentTasksVersion` = `1`), not the CLI release - otherwise every release would make every installed menu stale - cost if wrong: `status` cannot tell which release wrote a bundle.
3. Ruling: the notification is `/usr/bin/osascript` with the message as a run-handler argument (never AppleScript source); the app name comes from `path to frontmost application` (no Automation prompt); a failed capture notifies and also exits non-zero, so macOS shows its own alert - a capture that silently did not happen is the worst outcome, and notifications may be switched off - cost if wrong: two signals for one failure; with notifications off a successful capture shows nothing.
4. Ruling: the bundle copies Apple's own. The action is the Run Shell Script 2.0.3 action of `/System/Library/Services/Show Map.workflow` with input on standard input (under `/bin/sh`, the action's default, where Show Map names `/bin/bash`); the workflow metadata and `Info.plist` are those of Apple's plain-text service `Add to Music as a Spoken Track.workflow`, plus a `CFBundleIdentifier` per profile, the four marker keys and an empty `NSRequiredContext`. Apple's Services Properties documentation asks for that key in every service, as an empty dictionary when nothing is filtered; without it the service is registered but not presented in the Services menu automatically (https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/SysServices/Articles/properties.html). No Automator window `state`. `document.wflow` sits at `Contents/document.wflow`, the layout of Automator-saved workflows and of the third-party Quick Actions registered on this Mac (Apple's system services keep it in `Contents/Resources/`) - cost if wrong: Services ignores the item; Task 5's checks and the user's Mac decide, and whether macOS lists the item without the one-time enable stays the user's to see.
5. Ruling: `/` and `:` in the profile's name become `-` in the menu item and the bundle name (a `/` in a Services item makes a submenu; Finder shows `:` as `/`), names are cleaned with internal/tasks' hidden-character set plus the non-characters U+FFFE and U+FFFF (which XML cannot hold) and cut at 48 runes (a bundle name stays under 255 bytes), and `Render` refuses a bundle whose files do not read as well-formed XML - cost if wrong: a menu item reads slightly differently from the profile's name.
6. Ruling: another profile's menu with the same bundle name is never replaced while that profile exists, not even with `--force` (two profiles can clean to one name, and APFS folds case); the menu of a deleted profile at that name is replaced - cost if wrong: two such profiles cannot both have a menu until one is renamed.
7. Ruling: `install` and `uninstall` are the operator's (code `operator_only` under an agent marker, `--as` or `MONOAGENT_ACTOR`); `status` is open - they change the user's machine outside the board, and the refusal also stops an accidental run from an agent's shell - cost if wrong: an agent asked to set the menu up must ask the person to run it, and `!monoagentcli task os install` inside Claude Code is refused (it inherits `CLAUDECODE=1`). Accepted by the lead on 2026-10-06; Task 7 amends spec D7, 7 and 12.
8. Ruling: `status` judges each menu with the monoagentcli its marker names (stale when that file is gone, or when the menu differs from what install would write with that path), so the answer does not depend on which copy of the CLI asks; a menu whose marker names another database is listed as `other_database` and not judged against this one; it says why a menu is stale.
9. Ruling: `--dest` is on the `os` group (all three commands); `uninstall --profile` also takes a deleted profile's id, as `status` prints it; uninstalling nothing exits 0. `install` refuses a temporary build (a path containing `/go-build`).
10. Ruling: the menu passes no `--client-id` (spec 5.3): one click is one capture and nothing retries it, so a per-run key dedupes nothing and a derived key would drop deliberate repeats - cost if wrong: an accidental double run makes two Inbox cards.
11. Ruling: the CLI path is `os.Executable()` unresolved (autostart resolves symlinks; a package manager's versioned file behind a stable link would then break the menu on upgrade); `install` prints it and returns it as `cli`.
12. Ruling: no doctor check (spec 12 makes it optional "if it stays small"; the health registry needs a check, a fix and tests, and `task os status` already reports stale menus).
13. Ruling: P5 runs its two packages in full, its new CLI tests also under `-race`, `go vet -tags nosocial`, and builds for windows; not `go test ./...` (the brief allows targeted runs; P5 changes no shared package; the PR's CI runs the rest).
14. Ruling: what the spec leaves open is fixed here: the JSON of the three commands (Task 6 Interfaces); `--force` replaces only a bundle without this command's marker or another database's menu; `status` says `why` and has a fourth state, `other_database`; the script exits 1 when monoagentcli is missing and with monoagentcli's own status when it refuses.
15. Ruling: `install` moves an old bundle aside before moving the new one in and puts it back if that fails; `uninstall` also removes the `.monoagent-menu-*` folders an interrupted install left behind.
16. Ruling: a menu's identity is its database and its profile id, recorded in the marker keys `MonoAgentTasksDB` and `MonoAgentTasksProfileID` - every database has a profile `default`, so by the id alone an install run with another `--db-path` would silently take over the user's menu - cost if wrong: a fourth marker key and a fourth `status` state; `uninstall` of a menu of another database needs that `--db-path`.
17. Ruling: P1's gate holds the `os` commands like the others: `refTaskGate` (the table that P1's gate test checks against the command tree, the real CLI and `ref tasks`) gets a row for each, `os install` and `os uninstall` the operator's and `os status` open to an agent, and WHO MAY DO WHAT in `ref tasks` says the same (its `operator_only` sentence names the first two, a sentence of its own says that an agent may run `os status`); AGENTS.md says it in its surfaces row, and P1's list of operator commands in the paragraph above that table is left as it is (no test holds it, and other phases write near it) - cost if wrong: that list in AGENTS.md reads as complete when it is not, until P1's own text is amended.
18. Ruling: `install` and `uninstall` refuse an agent first, then count their arguments, then check the platform (`status`: its arguments, then the platform), so that an agent is refused as an agent on macOS, Linux and Windows alike (the gate test runs the real commands as an agent on a Linux runner too, where a platform check first would answer `invalid_input`) and an argument mistake is exit 3 with the `--json` document, as P1's commands answer it, not cobra's exit 1 with none - cost if wrong: the operator who runs `install` on a Linux machine with a stray argument hears about the argument before the platform.
19. Ruling: `task os` is registered on every platform, with no build tag (spec 12: "elsewhere: exit 3 and the recipe"); a tag would leave the three rows of the gate table without a command on the platforms that lack it - cost if wrong: Linux and Windows list a command that only refuses.
20. Ruling: P5 changes two of P1's test files and adds one, because `os` is the first command group inside the task group and P1's helpers read every `task NAME` as a direct subcommand: `refTaskSub` takes a path (`os install`) and `refHasFlag` sees a group's own flags (cobra shows a command's own persistent flag only after something merged it, so a fresh `os` lacks `--dest`), and with `refTaskCommands` and `refTaskCallee`, which are new, they live in a new file `ref_tasks_group_test.go` (P1's `ref_tasks_test.go` has 497 lines and the limit is 500, as P4's plan also notes); in `ref_tasks_test.go` the entries of a group may give examples that call its subcommands (a flat command's must still call that command), and every command of the group, `os install` too, must show its flags in its entry; in `ref_tasks_gate_test.go` three rows are added and the walk of the command tree goes down to the leaves, so that an `os` command without a row, or a row that names no command, fails the test - cost if wrong: another phase that edits the same helpers or the same table meets a conflict when the branches are merged (keep both edits; P2, P3 and P4 do not touch them today).

## Review Focus

Failure modes no happy-path test catches; each has a test in the task that owns the code.

1. Selected text, an app name or a profile name that looks like shell code never runs, and the text reaches monoagentcli byte for byte on standard input, never on a command line. (Task 2: `TestTheScriptHandsTheTextOverOnStandardInputOnly`, `TestTheAppNameIsDataToo`)
2. A profile name holding XML or shell specials, control or bidi characters can neither break out of `Info.plist`, `document.wflow` or the script, nor spoof the menu. (Task 1: `TestMenuNameCleansTheProfileName`; Task 3: `TestHostileProfileNamesStayText`; Task 5: Apple's `plutil` reads back exactly what was rendered)
3. A capture that fails (monoagentcli moved, the profile deleted, an agent's marker) is never silent: a notification and a non-zero exit. In every case the whole selection is read, also when monoagentcli adds the task after reading only its first 1 MiB, so the writer never meets a closed pipe. (Task 2: `TestARefusalIsShownAndPassedOn`, `TestAMissingCLIIsShownNotRun`, `TestTheScriptReadsTheWholeSelection`; Task 6: the real CLI's error for a deleted profile in `TestTheMenusCommandLineIsACaptureOnlyForTheOperator`)
4. The menu is no way round the agent limit for a caller that keeps its environment: the script passes no `--as` and keeps the environment, and with a marker the menu's own command line is refused. As with the operator guard, it does not stop an agent that deliberately clears its environment (or drives the Services menu through the screen); the human gate still holds. (Task 2: `TestTheScriptLeavesTheEnvironmentAlone`; Task 6: `TestTheMenusCommandLineIsACaptureOnlyForTheOperator`)
5. Install never destroys what it did not write, another profile's menu or (without `--force`) another database's, never leaves two menus for one profile, never deletes the bundle it just wrote on a case-insensitive disk, and no test touches the real Services folder or runs `pbs`: the CLI tests set `HOME` and `USERPROFILE` themselves and stop unless the home folder is their temporary one. (Task 4: `TestInstallKeepsWhatItDidNotWrite`, `TestInstallNeverReplacesAnotherProfilesMenu`, `TestInstallKeepsAnotherDatabasesMenu`, `TestInstallFollowsARenameInCaseOnly`; Task 6: `newTaskOSTest`, `TestTaskOSRefreshesTheRealServicesFolderOnly`, `TestTaskOSInstallNeverTakesAnotherDatabasesMenu`)

## File structure

Create:
- `internal/tasks/osmenu/names.go` (names, escaping, value checks), `script.go` (the script), `bundle.go` (Spec, Bundle, Render), `install.go` (Install, List, Remove, Matches).
- `internal/tasks/osmenu/templates/action.sh.tmpl`, `Info.plist.tmpl`, `document.wflow.tmpl`.
- Tests in `internal/tasks/osmenu/`: `names_test.go`, `script_test.go`, `plist_test.go`, `bundle_test.go`, `install_test.go`, `bundle_darwin_test.go`, `automator_darwin_test.go`.
- `cmd/monoagentcli/task_os.go`, `task_os_test.go`, `ref_tasks_os.go`, `ref_tasks_os_test.go`, `ref_tasks_group_test.go` (the helpers of P1's reference tests that read a command group inside the task group).

Modify: `cmd/monoagentcli/task.go` (one line), `cmd/monoagentcli/ref_tasks.go` (a sentence of WHO MAY DO WHAT, and a section of `refTasksText`), P1's tests `cmd/monoagentcli/ref_tasks_test.go` and `cmd/monoagentcli/ref_tasks_gate_test.go` (a command group inside the task group, and three rows), `AGENTS.md`, `SECURITY.md`, `CHANGELOG.md`, the spec (D7, the operator list of section 7, one bullet at the end of section 12).

---

### Task 0: Confirm the contract

**Files:** none (read only).

**Interfaces:**
- Consumes (from P1; the commands below confirm each): `tasks.Profile{ID, Name}` (JSON `id`, `name`); `tasks.NewStore(*sql.DB) *tasks.Store`; `(*tasks.Store).Profile(ctx context.Context, profileID string) (tasks.Profile, error)`, which returns an error matching `tasks.ErrInvalid` for an unknown id; in `cmd/monoagentcli`: `newTaskCmd(cfg *globalConfig) *cobra.Command` with a `cmd.AddCommand(` list and a loop `withJSONErrors(cfg, sub)` over its direct children; `withTasks(cfg *globalConfig, cmd *cobra.Command, fn func(ctx context.Context, store *tasks.Store, p tasks.Profile) error) error`; `taskErr(err error) error`; `callerFor(as string) taskCaller`, `(taskCaller).operator(what string) (tasks.Actor, error)` (code `operator_only`); `flagAs(cmd *cobra.Command) string`; `cutArg(s string) string` (an argument cut short for an error message that repeats it); `task add` flags `--stdin`, `--source`, `--app`, and a non-agent's `--source os` being a `tasks.Capture`; the store's refusal text `source "os" is for captures` for an agent; `initDB(cfg *globalConfig) (*storage.Database, error)`, `resolveProfileID(db *sql.DB, idOrName string) (string, error)`, `expandPath(string) string`, `errInvalidInput(format string, a ...interface{}) error`, `withJSONErrors(cfg *globalConfig, cmd *cobra.Command)`, `writeJSONTo(w io.Writer, v any) error`; `cliDocs []cmdDoc` with `cmdDoc{Name, Short, Usage, Flags string; Examples []string}`; `refTasksText` with a line `TASK TEXT IS DATA`; test helpers `newTaskTestDB(t) string`, `runTask(t, dbPath, profile string, jsonOut bool, stdin string, args ...string) (stdout, stderr string, err error)`, `mustTaskJSON(t, dbPath, profile string, v any, stdin string, args ...string)`, `failedTaskJSON(t, dbPath, profile string, wantExit int, args ...string) map[string]any`, `addedJSON` (fields `Created`, `Profile`, `Task taskJSON` with `Title`, `Status`, `Source.Kind`), `exitCode(err error) int`; and P1's reference tests, which Task 6 Step 5 changes: in `ref_tasks_test.go` `refTaskCall` (a regexp), `refTaskSub(name string) *cobra.Command`, `refHasFlag(sub *cobra.Command, name string) bool`, `refTaskEntries() map[string]cmdDoc`, `refFlagsIn`, `refSection`, `refNames`, and the tests `TestRefTasksEntriesDescribeTheirOwnCommand`, `TestRefTasksEntriesNameEveryFlagOfTheirCommand` and `TestRefTasksSuggestsOnlyCommandsTheCLIHas`; in `ref_tasks_gate_test.go` `refTaskGate` (rows `{row string, op bool, call []string}`) and `TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent`.
- Produces: nothing.

- [ ] **Step 1: Confirm each name against the code**

Run each command on its own:

```
git branch --show-current
git log --oneline -4
go doc ./internal/tasks Profile
go doc ./internal/tasks Store.Profile
go doc ./internal/tasks NewStore
grep -n 'is for captures' internal/tasks/store.go
grep -n 'func newTaskCmd\|cmd.AddCommand(\|withJSONErrors(cfg, sub)' cmd/monoagentcli/task.go
grep -n 'for _, sub := range cmd.Commands()' cmd/monoagentcli/task.go
grep -n 'PersistentPreRun' cmd/monoagentcli/task.go
grep -n 'func withTasks\|func taskErr\|func callerFor\|func (c taskCaller) operator\|func flagAs' cmd/monoagentcli/task.go
grep -n 'initializing database: %w' cmd/monoagentcli/task.go
grep -n '"stdin"\|"source"\|"app"\|tasks.Capture' cmd/monoagentcli/task.go
grep -n 'func newTaskTestDB\|func runTask\|func mustTaskJSON\|func failedTaskJSON\|type addedJSON\|type taskJSON' cmd/monoagentcli/task_test.go
grep -n 'Setenv("HOME"' cmd/monoagentcli/task_test.go
grep -n 'func exitCode' cmd/monoagentcli/people_review_test.go
grep -n 'func initDB\|func resolveProfileID\|func expandPath' cmd/monoagentcli/root.go
grep -n 'func withJSONErrors\|func writeJSONTo' cmd/monoagentcli/automation.go
grep -n 'func errInvalidInput' cmd/monoagentcli/exitcodes.go
grep -n 'type cmdDoc\|^var cliDocs' cmd/monoagentcli/ref.go
grep -n 'const refTasksText\|^TASK TEXT IS DATA' cmd/monoagentcli/ref_tasks.go
grep -n 'func TestEveryTaskCommandHasAReferenceEntry' cmd/monoagentcli/ref_tasks_test.go
grep -n 'func refTaskSub\|func refHasFlag\|func refTaskEntries\|func TestRefTasksEntriesDescribeTheirOwnCommand\|func TestRefTasksEntriesNameEveryFlagOfTheirCommand\|func TestRefTasksSuggestsOnlyCommandsTheCLIHas' cmd/monoagentcli/ref_tasks_test.go
grep -n 'refTaskGate = \|"digest", false\|func TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent' cmd/monoagentcli/ref_tasks_gate_test.go
grep -n 'Only board, edit, move, approve, archive, unarchive and add --ready are the' cmd/monoagentcli/ref_tasks.go
grep -n 'func cutArg' cmd/monoagentcli/task.go
grep -n '| Surface | Reaches the board through |\|^| Session-start hook |' AGENTS.md
grep -n 'Text is cleaned on the way in' SECURITY.md
grep -n -A9 'func hidden' internal/tasks/clean.go
grep -n 'Task board, phase 1' CHANGELOG.md
grep -n '^## 12. The macOS menu\|^## 13. Security' docs/mastermind/specs/2026-10-05-task-board-design.md
grep -rn 'taskOS\|withTaskStore\|refreshServices' cmd/monoagentcli/
ls internal/tasks
ls -l /usr/bin/plutil /usr/bin/automator /usr/bin/osascript /System/Library/CoreServices/pbs
```

Expected: the branch is `feat/tasks-board-os`; the log shows P1's last commits; every grep and `go doc` finds the name with the signature written in the Interfaces block above; `newTaskCmd` wraps its direct children only (a `for _, sub := range cmd.Commands()` loop calling `withJSONErrors`, not a walk of all descendants: Task 6 wraps the `os` subcommands itself, and a second wrap would print two JSON documents); `task.go` has no `PersistentPreRun` (one would run before `task os`); `withTasks` wraps a failure of `initDB` as `initializing database: %w`; `newTaskTestDB` sets `HOME` (Task 6's helper sets it again itself and checks it); AGENTS.md has P1's surfaces table and SECURITY.md its "Text is cleaned on the way in" bullet (Task 7's anchors); `ref_tasks_test.go` holds its six names once each, `ref_tasks_gate_test.go` its table, the `digest` row and its test once each, and `ref_tasks.go` the first line of the `operator_only` sentence of WHO MAY DO WHAT once (the old text Task 6 Step 5 replaces; if one of them reads differently, Step 2 below applies); `hidden` in clean.go has exactly the ranges of Task 1's copy (0xE0000-0xE007F, 0x202A-0x202E, 0x2066-0x2069, and 0xFEFF); the `taskOS|withTaskStore|refreshServices` grep finds nothing; `internal/tasks` has no `osmenu` folder; the four macOS tools exist.

- [ ] **Step 2: Decide**

If anything differs from this plan (a name, a signature, a JSON field, a flag, the refusal text), spec section 6 and 7 win: stop and tell the lead what differs. Do not adapt the plan on your own. Nothing to commit.

---

### Task 1: Names, escaping and value checks

**Files:**
- Create: `internal/tasks/osmenu/names.go`
- Test: `internal/tasks/osmenu/names_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `const MenuPrefix = "Add to MonoAgent Tasks"`, `maxNameRunes` (48), `hidden(rune) bool`, `cleanName(string) string`, `menuName(name, id string) string`, `MenuTitle(name, id string) string`, `BundleName(name, id string) string`, `bundleID(profileID string) string`, `shellQuote(string) string`, `plistEscape(string) string`, `plainValue(what, v string) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/tasks/osmenu/names_test.go`:

```go
package osmenu

import (
	"encoding/xml"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMenuNameCleansTheProfileName(t *testing.T) {
	for _, c := range []struct{ name, id, want string }{
		{"Work", "w", "Work"},
		{"  Side\tproject\r\nalpha  ", "w", "Side project alpha"},
		{"Clients/2026: Q4", "w", "Clients-2026- Q4"},
		{"Esc\x1b[31mRed\x7f", "w", "Esc[31mRed"},
		{"invoice\U0000202Egnp.exe", "w", "invoicegnp.exe"},
		{"tag\U000E0041\U000E0042s", "w", "tags"},
		{"bom\U0000FEFFx\U00002066y", "w", "bomxy"},
		{"bad\xffutf8", "w", "bad\U0000FFFDutf8"},
		{" \x00\x07 ", "work-id", "work-id"},
		{"a\U0000FFFEb\U0000FFFF", "w", "ab"},
		{"", "a/b", "a-b"},
		{"\x01", "\x02", "profile"},
	} {
		if got := menuName(c.name, c.id); got != c.want {
			t.Errorf("menuName(%q, %q) = %q, want %q", c.name, c.id, got, c.want)
		}
	}
}

// hidden is a copy of internal/tasks' set: each range end is in it, and the
// neighbour outside each end is not.
func TestHiddenMatchesTheTasksSet(t *testing.T) {
	for r, want := range map[rune]bool{
		0xE0000: true, 0xE007F: true, 0xDFFFF: false, 0xE0080: false,
		0x202A: true, 0x202E: true, 0x2029: false, 0x202F: false,
		0x2066: true, 0x2069: true, 0x2065: false, 0x206A: false,
		0xFEFF: true, 0xFEFE: false, 0xFF00: false,
	} {
		if hidden(r) != want {
			t.Errorf("hidden(%U) = %v, want %v", r, !want, want)
		}
	}
}

func TestNamesAreCutAtTheLimit(t *testing.T) {
	exact := strings.Repeat("x", maxNameRunes)
	if got := menuName(exact, "w"); got != exact {
		t.Errorf("a name of exactly %d runes became %q", maxNameRunes, got)
	}
	got := menuName(exact+"y", "w")
	if utf8.RuneCountInString(got) != maxNameRunes || got != strings.Repeat("x", maxNameRunes-1)+"\U00002026" {
		t.Errorf("a name one over the limit became %q", got)
	}
	long := BundleName(strings.Repeat("\U0001F600", 100), "w")
	if len(long) > 255 || !strings.HasPrefix(long, MenuPrefix+" (") || !strings.HasSuffix(long, ").workflow") {
		t.Errorf("a bundle name of %d bytes: %q", len(long), long)
	}
	if got := MenuTitle("Work", "w"); got != "Add to MonoAgent Tasks: Work" {
		t.Errorf("MenuTitle = %q", got)
	}
	if got := BundleName("Work", "w"); got != "Add to MonoAgent Tasks (Work).workflow" {
		t.Errorf("BundleName = %q", got)
	}
}

func TestShellQuoteRoundTrips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	for _, s := range []string{"plain", "it's", "'", "''", `a"b`, "$(echo injected)", "`echo injected`", `back\slash`, "new\nline", "-n", "*", "~", ""} {
		out, err := exec.Command("/bin/sh", "-c", "printf '%s' "+shellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("sh read %s as %q (%v), want %q", shellQuote(s), out, err, s)
		}
	}
}

func TestPlistEscapeKeepsTextAsText(t *testing.T) {
	s := "a & b < c > d ]]> \"e\" 'f'\n\tg"
	esc := plistEscape(s)
	if esc != "a &amp; b &lt; c &gt; d ]]&gt; \"e\" 'f'\n\tg" {
		t.Errorf("plistEscape = %q", esc)
	}
	var back string
	if err := xml.Unmarshal([]byte("<string>"+esc+"</string>"), &back); err != nil || back != s {
		t.Errorf("an XML reader read %q (%v), want %q", back, err, s)
	}
}

func TestPlainValueRefusesWhatABundleCannotCarry(t *testing.T) {
	if err := plainValue("the profile id", "711ef586-9f4b-4b1f-b2fd-cad23eec0a03"); err != nil {
		t.Errorf("a uuid: %v", err)
	}
	if err := plainValue("the profile id", ""); err == nil || err.Error() != "the profile id is empty" {
		t.Errorf("empty: %v", err)
	}
	for _, v := range []string{"a\nb", "a\x1bb", "a\x7fb", "a\U0000202Eb", "a\xffb"} {
		err := plainValue("the profile id", v)
		if err == nil || !strings.HasSuffix(err.Error(), " holds a control or hidden character, or is not UTF-8") {
			t.Errorf("%q: %v", v, err)
		}
	}
}

func TestBundleIDIsOnePerProfile(t *testing.T) {
	a, b := bundleID("default"), bundleID("work-id")
	ok := regexp.MustCompile(`^com\.monoagent\.tasks\.menu\.[0-9a-f]{12}$`)
	if a == b || !ok.MatchString(a) || !ok.MatchString(b) || bundleID("default") != a {
		t.Errorf("bundle ids %q and %q", a, b)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/tasks/osmenu/ -count=1`
Expected: FAIL to compile (`undefined: menuName`).

- [ ] **Step 3: Write `internal/tasks/osmenu/names.go`**

```go
// Package osmenu renders and installs the macOS Quick Action "Add to MonoAgent
// Tasks: <profile>", which files the text selected in any app into one
// profile's task board (spec docs/mastermind/specs/2026-10-05-task-board-design.md,
// section 12). Nothing here needs macOS: rendering, reading and writing a
// bundle take a folder, so the tests run on every OS. The CLI chooses the
// folder (~/Library/Services) and runs the macOS tools.
package osmenu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MenuPrefix starts every menu item and bundle name this package writes.
const MenuPrefix = "Add to MonoAgent Tasks"

// maxNameRunes caps the profile's name in a menu item and a bundle name, so
// that a bundle name stays well under the 255 bytes a file name may hold.
const maxNameRunes = 48

// hidden mirrors internal/tasks: characters that show nothing but carry text
// (the Unicode tag block, bidi controls, the byte order mark).
func hidden(r rune) bool {
	switch {
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return r == 0xFEFF
}

// cleanName is s on one line with no control or hidden character and none of
// the non-characters U+FFFE and U+FFFF (XML cannot hold them), "/" (a submenu
// in the Services menu, a folder in a path) and ":" (shown as "/" by Finder)
// written as "-", and cut to maxNameRunes.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r == '/' || r == ':':
			return '-'
		case unicode.IsControl(r), hidden(r), r == 0xFFFE, r == 0xFFFF:
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\U0000FFFD"))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxNameRunes {
		s = strings.TrimRightFunc(string(r[:maxNameRunes-1]), unicode.IsSpace) + "\U00002026"
	}
	return s
}

// menuName is the profile's name as the menu item, the bundle name and the
// notification show it; a name that cleans to nothing is shown as the id.
func menuName(name, id string) string {
	for _, s := range []string{name, id} {
		if c := cleanName(s); c != "" {
			return c
		}
	}
	return "profile"
}

// MenuTitle is the Services menu item: "Add to MonoAgent Tasks: <name>".
func MenuTitle(name, id string) string { return MenuPrefix + ": " + menuName(name, id) }

// BundleName is the bundle's folder: "Add to MonoAgent Tasks (<name>).workflow".
func BundleName(name, id string) string {
	return MenuPrefix + " (" + menuName(name, id) + ").workflow"
}

// bundleID is a bundle's CFBundleIdentifier: one per profile id, made only of
// characters an identifier may hold.
func bundleID(profileID string) string {
	sum := sha256.Sum256([]byte(profileID))
	return "com.monoagent.tasks.menu." + hex.EncodeToString(sum[:6])
}

// shellQuote quotes s for a POSIX shell: nothing inside single quotes is
// special, and a single quote is written '\''.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// plistEscape escapes the characters XML reserves in text. Quotes, tabs and
// newlines stay as they are, as in Apple's own workflows.
func plistEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// plainValue refuses a value the bundle must carry exactly but cannot: an
// empty one, invalid UTF-8, or a control or hidden character (a property list
// cannot hold most control characters, and a path or an id never needs one).
func plainValue(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if !utf8.ValidString(v) || strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) || hidden(r) }) {
		return fmt.Errorf("%s %q holds a control or hidden character, or is not UTF-8", what, v)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `gofmt -l internal/tasks/osmenu` then `go vet ./internal/tasks/osmenu/` then `go test ./internal/tasks/osmenu/ -count=1` then `grep -nP '[^\x00-\x7F]' internal/tasks/osmenu/names.go internal/tasks/osmenu/names_test.go`
Expected: no output from `gofmt` or `grep`, vet clean, PASS (seven tests).

- [ ] **Step 5: Commit**

```
git add internal/tasks/osmenu/names.go internal/tasks/osmenu/names_test.go
```
then
```
git commit -m "feat(tasks): osmenu names, escaping and value checks for the macOS menu" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The menu's shell script

**Files:**
- Create: `internal/tasks/osmenu/script.go`, `internal/tasks/osmenu/templates/action.sh.tmpl`
- Test: `internal/tasks/osmenu/script_test.go`

**Interfaces:**
- Consumes: Task 1's `shellQuote`, `plistEscape`.
- Produces: `const osascript = "/usr/bin/osascript"`; `funcs template.FuncMap` (`sh`, `plist`; Task 3 uses it); `scriptView{CLI, DBPath, ProfileID, Name, Osascript string}`; `renderScript(v scriptView) (string, error)`. Test helpers used by Task 5: `rig` (fields `dir string`, `view scriptView`), `newRig(t) *rig`, `(*rig).write(t, name, body string, mode os.FileMode)`, `(*rig).read(name string) string`, `(*rig).command(t, stdin io.Reader, env ...string) (*exec.Cmd, *bytes.Buffer)`, `(*rig).run(t, stdin io.Reader, env ...string) (stderr string, code int)`, `exitStatus(t, err error) int`, `lines(items ...string) string`.
- The script's contract (Automator runs it as `/bin/sh -c <script>` with the selection on standard input): it runs `<cli> --db-path=<db> --profile=<id> task add --stdin --source os --app=<frontmost app>`; exit 0 notifies `Added to Inbox in <name>`; a refusal notifies `Not added: <the last line of monoagentcli's error>` and exits with monoagentcli's status; a missing monoagentcli notifies and exits 1; it always reads the whole selection, also after monoagentcli has read its first 1 MiB and added the task (two identical `cat >/dev/null 2>&1` lines do this, one in `fail()` and one in the success branch: when editing either, find it by its context).

- [ ] **Step 1: Write the failing tests**

Create `internal/tasks/osmenu/script_test.go`:

```go
package osmenu

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The stubs stand in for monoagentcli and osascript and record what they get.
// The CLI stub fails (exit 3, the text of the file "fail" on stderr) without
// reading its input when that file exists; when the file "short" exists it
// reads only the first 1 MiB and succeeds, as task add --stdin does.
const stubCLI = `#!/bin/sh
d=$(dirname "$0")
for a in "$@"; do printf '%s\n' "$a"; done > "$d/args"
env > "$d/env"
if [ -f "$d/fail" ]; then cat "$d/fail" >&2; exit 3; fi
if [ -f "$d/short" ]; then head -c 1048576 > "$d/stdin"; exit 0; fi
cat > "$d/stdin"
`

const stubOsascript = `#!/bin/sh
d=$(dirname "$0")
if [ "$2" = 'POSIX path of (path to frontmost application)' ]; then
	cat "$d/frontmost"
	exit 0
fi
for a in "$@"; do printf '%s\n' "$a"; done > "$d/notified"
`

// rig runs the menu's script as Automator does, sh -c with the selected text
// on standard input, against the stubs, in a folder whose name needs quoting.
type rig struct {
	dir  string
	view scriptView
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the menu's script runs under /bin/sh")
	}
	dir := filepath.Join(t.TempDir(), "my 'odd' dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &rig{dir: dir, view: scriptView{
		CLI:       filepath.Join(dir, "monoagentcli"),
		DBPath:    filepath.Join(dir, "my db.sqlite"),
		ProfileID: "work-id",
		Name:      "Work",
		Osascript: filepath.Join(dir, "osascript"),
	}}
	r.write(t, "monoagentcli", stubCLI, 0o755)
	r.write(t, "osascript", stubOsascript, 0o755)
	r.write(t, "frontmost", "/Applications/Safari.app/\n", 0o644)
	return r
}

func (r *rig) write(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// read returns what a stub recorded, or "" when it recorded nothing.
func (r *rig) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(b)
}

// command is the rendered script under sh -c, run in the rig's folder with a
// minimal environment plus env.
func (r *rig) command(t *testing.T, stdin io.Reader, env ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	script, err := renderScript(r.view)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Dir = r.dir
	cmd.Stdin = stdin
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + r.dir}, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd, &stderr
}

// run runs the script to the end and returns its stderr and exit status.
func (r *rig) run(t *testing.T, stdin io.Reader, env ...string) (string, int) {
	t.Helper()
	cmd, stderr := r.command(t, stdin, env...)
	err := cmd.Run()
	return stderr.String(), exitStatus(t, err)
}

func exitStatus(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	t.Fatalf("running the script: %v", err)
	return 0
}

// noCanary fails when "pwned" exists in the rig's folder, where the script
// runs: some text ran as a command.
func (r *rig) noCanary(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(r.dir, "pwned")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("text from the selection, the profile name or the app name ran as a command")
	}
}

// lines is one record line per item, as the stubs write them.
func lines(items ...string) string { return strings.Join(items, "\n") + "\n" }

// hostileText runs commands if it ever reaches a shell command line, and
// carries bytes a careless pipe would mangle.
const hostileText = "Pay the invoice $(touch pwned) `touch pwned` ; touch pwned | cat\n" +
	"'quoted' \"double\" back\\slash \x00 \xff \U0000202E end\n"

func TestTheScriptHandsTheTextOverOnStandardInputOnly(t *testing.T) {
	r := newRig(t)
	r.view.Name = `O'Brien "Q" $(touch pwned) team`
	stderr, code := r.run(t, strings.NewReader(hostileText))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := r.read("stdin"); got != hostileText {
		t.Errorf("monoagentcli read %q on standard input, want the selection byte for byte", got)
	}
	want := lines("--db-path="+r.view.DBPath, "--profile=work-id", "task", "add", "--stdin", "--source", "os", "--app=Safari")
	if got := r.read("args"); got != want {
		t.Errorf("arguments:\n%swant:\n%s", got, want)
	}
	note := lines("-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run",
		"MonoAgent Tasks", `Added to Inbox in O'Brien "Q" $(touch pwned) team`)
	if got := r.read("notified"); got != note {
		t.Errorf("notification:\n%swant:\n%s", got, note)
	}
	r.noCanary(t)
}

func TestTheAppNameIsDataToo(t *testing.T) {
	r := newRig(t)
	r.write(t, "frontmost", "/Applications/Evil $(touch pwned) `touch pwned`.app/\n", 0o644)
	if stderr, code := r.run(t, strings.NewReader("x")); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := r.read("args"); !strings.HasSuffix(got, "\n--app=Evil $(touch pwned) `touch pwned`\n") {
		t.Errorf("arguments:\n%s", got)
	}
	r.noCanary(t)
}

func TestWithoutOsascriptTheTaskIsStillAdded(t *testing.T) {
	r := newRig(t)
	r.view.Osascript = filepath.Join(r.dir, "no-osascript")
	if stderr, code := r.run(t, strings.NewReader("x")); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := r.read("args"); !strings.HasSuffix(got, "\n--app=\n") {
		t.Errorf("arguments:\n%s", got)
	}
	if got := r.read("notified"); got != "" {
		t.Errorf("a notification without osascript: %q", got)
	}
}

// The script passes no --as and keeps its environment: under an agent's
// marker monoagentcli sees the marker and refuses --source os, so the menu is
// no way round the agent limit.
func TestTheScriptLeavesTheEnvironmentAlone(t *testing.T) {
	r := newRig(t)
	if stderr, code := r.run(t, strings.NewReader("x"), "CLAUDECODE=1", "MONOAGENT_ACTOR=bot"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	env := "\n" + r.read("env")
	for _, want := range []string{"\nCLAUDECODE=1\n", "\nMONOAGENT_ACTOR=bot\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("monoagentcli did not get %q:%s", strings.TrimSpace(want), env)
		}
	}
	if strings.Contains(r.read("args"), "--as") {
		t.Error("the script names an agent")
	}
}

func TestARefusalIsShownAndPassedOn(t *testing.T) {
	r := newRig(t)
	r.write(t, "fail", "warning: an unrelated note\ninitializing database: profile \"work-id\" not found (checked both id and name)\n", 0o644)
	stderr, code := r.run(t, strings.NewReader("x"))
	if code != 3 {
		t.Fatalf("exit %d, want monoagentcli's 3: %s", code, stderr)
	}
	reason := `initializing database: profile "work-id" not found (checked both id and name)`
	if got := r.read("notified"); !strings.HasSuffix(got, "\nNot added: "+reason+"\n") || strings.Contains(got, "warning") {
		t.Errorf("notification (the last line of the error only):\n%s", got)
	}
	if stderr != "Not added to MonoAgent Tasks: "+reason+"\n" {
		t.Errorf("stderr %q", stderr)
	}
}

func TestAMissingCLIIsShownNotRun(t *testing.T) {
	r := newRig(t)
	r.view.CLI = filepath.Join(r.dir, "moved", "monoagentcli")
	stderr, code := r.run(t, strings.NewReader("x"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, stderr)
	}
	want := "Not added: monoagentcli is not at " + r.view.CLI +
		" any more. Run monoagentcli task os install again, or remove this menu with monoagentcli task os uninstall."
	if got := r.read("notified"); !strings.HasSuffix(got, "\n"+want+"\n") {
		t.Errorf("notification:\n%s", got)
	}
}

// The script reads the whole selection in every case, so the program writing
// it (Automator) never meets a closed pipe: when nothing is added, and when
// monoagentcli adds the task after reading only the first 1 MiB of it.
func TestTheScriptReadsTheWholeSelection(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(*testing.T, *rig)
		code  int
	}{
		"monoagentcli moved":            {func(_ *testing.T, r *rig) { r.view.CLI = filepath.Join(r.dir, "moved") }, 1},
		"monoagentcli refuses":          {func(t *testing.T, r *rig) { r.write(t, "fail", "refused\n", 0o644) }, 3},
		"monoagentcli adds after 1 MiB": {func(t *testing.T, r *rig) { r.write(t, "short", "", 0o644) }, 0},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c.setup(t, r)
			pr, pw, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd, stderr := r.command(t, pr)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			pr.Close() // only the script holds the reading end now
			wrote := make(chan error, 1)
			go func() {
				_, err := pw.Write(bytes.Repeat([]byte("selection "), 300_000)) // 3 MB: well past the 1 MiB monoagentcli reads plus a pipe's buffer
				pw.Close()
				wrote <- err
			}()
			if code := exitStatus(t, cmd.Wait()); code != c.code {
				t.Fatalf("exit %d, want %d: %s", code, c.code, stderr)
			}
			if err := <-wrote; err != nil {
				t.Errorf("writing the selection failed: %v (the script stopped reading)", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/tasks/osmenu/ -count=1`
Expected: FAIL to compile (`undefined: scriptView`).

- [ ] **Step 3: Write the template `internal/tasks/osmenu/templates/action.sh.tmpl`**

The line `nl='` and the line `'` after it hold a newline between single quotes on purpose.

```sh
# Add to MonoAgent Tasks: files the text selected in any app into the Inbox of
# one profile. Written by "monoagentcli task os install" (which rewrites it);
# "monoagentcli task os uninstall" removes it. The text arrives on standard
# input and reaches monoagentcli on its standard input, never on a command line.
cli={{sh .CLI}}
db={{sh .DBPath}}
profile={{sh .ProfileID}}
name={{sh .Name}}
osa={{sh .Osascript}}

notify() {
	if [ -x "$osa" ]; then
		"$osa" -e 'on run argv' -e 'display notification (item 2 of argv) with title (item 1 of argv)' -e 'end run' 'MonoAgent Tasks' "$1" </dev/null >/dev/null 2>&1
	fi
}

# fail reads the rest of the text, so that the program writing it never meets
# a closed pipe, says why nothing was added, and exits with status $2: a
# failed action also makes macOS show its own alert.
fail() {
	cat >/dev/null 2>&1
	notify "Not added: $1"
	printf 'Not added to MonoAgent Tasks: %s\n' "$1" >&2
	exit "$2"
}

if [ ! -x "$cli" ]; then
	fail "monoagentcli is not at $cli any more. Run monoagentcli task os install again, or remove this menu with monoagentcli task os uninstall." 1
fi

app=
if [ -x "$osa" ]; then
	app=$("$osa" -e 'POSIX path of (path to frontmost application)' </dev/null 2>/dev/null)
	app=${app%/}
	app=${app##*/}
	app=${app%.app}
fi

err=$("$cli" --db-path="$db" --profile="$profile" task add --stdin --source os --app="$app" 2>&1 >/dev/null)
code=$?
if [ "$code" -eq 0 ]; then
	cat >/dev/null 2>&1
	notify "Added to Inbox in $name"
	exit 0
fi
nl='
'
err=${err##*"$nl"}
[ -n "$err" ] || err="monoagentcli stopped with status $code"
fail "$err" "$code"
```

- [ ] **Step 4: Write `internal/tasks/osmenu/script.go`**

```go
package osmenu

import (
	_ "embed"
	"strings"
	"text/template"
)

// osascript runs the menu's two AppleScript lines: the name of the app the
// text was selected in, and the notification. Tests render with a stub.
const osascript = "/usr/bin/osascript"

// funcs escape a template's fields: sh quotes for the shell, plist escapes
// for a property list's text.
var funcs = template.FuncMap{"sh": shellQuote, "plist": plistEscape}

//go:embed templates/action.sh.tmpl
var actionTmpl string

var actionTemplate = template.Must(template.New("action.sh").Funcs(funcs).Parse(actionTmpl))

// scriptView is what the script is rendered from. The template quotes every
// field, and the script only ever expands them inside double quotes.
type scriptView struct {
	CLI, DBPath, ProfileID, Name, Osascript string
}

// renderScript is the Run Shell Script action's script. The selected text
// arrives on its standard input and reaches monoagentcli the same way: it is
// never part of a command line.
func renderScript(v scriptView) (string, error) {
	var b strings.Builder
	if err := actionTemplate.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `gofmt -l internal/tasks/osmenu` then `go vet ./internal/tasks/osmenu/` then `go test ./internal/tasks/osmenu/ -count=1` then `grep -nP '[^\x00-\x7F]' internal/tasks/osmenu/script.go internal/tasks/osmenu/script_test.go internal/tasks/osmenu/templates/action.sh.tmpl`
Expected: no output from `gofmt` or `grep`, vet clean, PASS. If a script test fails, the script must stay POSIX: CI's `/bin/sh` is dash, macOS's is bash in sh mode; do not use `$'...'`, `local`, `echo -e` or arrays.

- [ ] **Step 6: Commit**

```
git add internal/tasks/osmenu/script.go internal/tasks/osmenu/script_test.go internal/tasks/osmenu/templates/action.sh.tmpl
```
then
```
git commit -m "feat(tasks): the macOS menu's script hands the selection to task add on standard input" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The bundle: Info.plist and document.wflow

**Files:**
- Create: `internal/tasks/osmenu/bundle.go`, `internal/tasks/osmenu/templates/Info.plist.tmpl`, `internal/tasks/osmenu/templates/document.wflow.tmpl`
- Test: `internal/tasks/osmenu/plist_test.go`, `internal/tasks/osmenu/bundle_test.go`

**Interfaces:**
- Consumes: Task 1's `menuName`, `MenuTitle`, `BundleName`, `bundleID`, `plainValue`; Task 2's `funcs`, `osascript`, `scriptView`, `renderScript`.
- Produces: `const Version = "1"`; `KeyCLI = "MonoAgentTasksCLI"`, `KeyDB = "MonoAgentTasksDB"`, `KeyProfileID = "MonoAgentTasksProfileID"`, `KeyVersion = "MonoAgentTasksVersion"`; `InfoPath = "Contents/Info.plist"`, `DocumentPath = "Contents/document.wflow"` (slash-separated, inside a bundle); `Spec{CLI, DBPath, ProfileID, ProfileName string}`; `Bundle{Name, Menu, DBPath, ProfileID string; Info, Document []byte}`; `Render(Spec) (Bundle, error)`; `render(Spec, osa string) (Bundle, error)`; `wellFormed([]byte) error`. Test helpers used later: `parsePlist(t, []byte) any`, `dig(t, v any, path ...any) any`, `testDB`, `testSpec() Spec`, `hostileName`, `hostileClean`.

- [ ] **Step 1: Write the plist reader for the tests**

Create `internal/tasks/osmenu/plist_test.go`:

```go
package osmenu

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"testing"
)

// parsePlist reads an XML property list into Go values: map[string]any for a
// dict, []any for an array, string, int64 and bool. It fails the test on
// anything that is not a well-formed property list.
func parsePlist(t *testing.T, data []byte) any {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			t.Fatalf("no <plist> element: %v", err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "plist" {
			v, err := plistNext(d)
			if err != nil {
				t.Fatalf("not a property list: %v", err)
			}
			return v
		}
	}
}

var errEndOfContainer = errors.New("end of container")

// plistNext reads the next value, or errEndOfContainer at a closing tag.
func plistNext(d *xml.Decoder) (any, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return plistValue(d, t)
		case xml.EndElement:
			return nil, errEndOfContainer
		}
	}
}

type plistKey string

func plistValue(d *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "dict":
		m := map[string]any{}
		for {
			k, err := plistNext(d)
			if errors.Is(err, errEndOfContainer) {
				return m, nil
			}
			if err != nil {
				return nil, err
			}
			key, ok := k.(plistKey)
			if !ok {
				return nil, fmt.Errorf("a dict entry without a key: %v", k)
			}
			v, err := plistNext(d)
			if err != nil {
				return nil, fmt.Errorf("the value of %q: %w", key, err)
			}
			m[string(key)] = v
		}
	case "array":
		a := []any{}
		for {
			v, err := plistNext(d)
			if errors.Is(err, errEndOfContainer) {
				return a, nil
			}
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
	case "key", "string", "integer":
		var s string
		if err := d.DecodeElement(&s, &start); err != nil {
			return nil, err
		}
		switch start.Name.Local {
		case "key":
			return plistKey(s), nil
		case "integer":
			return strconv.ParseInt(s, 10, 64)
		}
		return s, nil
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	}
	return nil, fmt.Errorf("unexpected <%s>", start.Name.Local)
}

// dig walks v by dict keys (strings) and array indexes (ints).
func dig(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch p := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%v: not a dict at %q", path, p)
			}
			if v, ok = m[p]; !ok {
				t.Fatalf("%v: no key %q", path, p)
			}
		case int:
			a, ok := v.([]any)
			if !ok || p >= len(a) {
				t.Fatalf("%v: no item %d", path, p)
			}
			v = a[p]
		}
	}
	return v
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/tasks/osmenu/bundle_test.go`:

```go
package osmenu

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// testDB is the database the test menus file into.
const testDB = "/Users/me/.monoagent/monoagent.db"

func testSpec() Spec {
	return Spec{CLI: "/usr/local/bin/monoagentcli", DBPath: testDB, ProfileID: "work-id", ProfileName: "Work"}
}

// hostileName would break out of a property list or a shell script that
// escaped it wrongly; hostileClean is how the menu shows it.
const (
	hostileName  = "</a><key>x</key> & ]]> \"q\" 'it' $(id)\n\U0000202Eb"
	hostileClean = `<-a><key>x<-key> & ]]> "q" 'it' $(id) b`
)

func TestTheBundleIsAServiceThatReceivesText(t *testing.T) {
	b, err := Render(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Add to MonoAgent Tasks (Work).workflow" || b.Menu != "Add to MonoAgent Tasks: Work" || b.ProfileID != "work-id" || b.DBPath != testDB {
		t.Errorf("name %q, menu %q, profile %q, database %q", b.Name, b.Menu, b.ProfileID, b.DBPath)
	}
	info := parsePlist(t, b.Info)
	for key, want := range map[string]string{
		"CFBundleIdentifier": bundleID("work-id"),
		"CFBundleName":       "Add to MonoAgent Tasks: Work",
		KeyCLI:               "/usr/local/bin/monoagentcli",
		KeyDB:                testDB,
		KeyProfileID:         "work-id",
		KeyVersion:           Version,
	} {
		if got := dig(t, info, key); got != want {
			t.Errorf("Info.plist %s = %v, want %q", key, got, want)
		}
	}
	if services, _ := dig(t, info, "NSServices").([]any); len(services) != 1 {
		t.Fatalf("%d services, want one", len(services))
	}
	svc := dig(t, info, "NSServices", 0)
	for _, c := range []struct {
		key  string
		want any
	}{
		{"NSMessage", "runWorkflowAsService"},
		{"NSSendTypes", []any{"public.utf8-plain-text"}},
		{"NSRequiredContext", map[string]any{}}, // Apple: always present, empty when nothing is filtered
		{"NSMenuItem", map[string]any{"default": "Add to MonoAgent Tasks: Work"}},
	} {
		if got := dig(t, svc, c.key); !reflect.DeepEqual(got, c.want) {
			t.Errorf("the service's %s = %#v, want %#v", c.key, got, c.want)
		}
	}
	doc := parsePlist(t, b.Document)
	if actions, _ := dig(t, doc, "actions").([]any); len(actions) != 1 {
		t.Fatalf("%d actions, want one", len(actions))
	}
	action := dig(t, doc, "actions", 0, "action")
	script, err := renderScript(scriptView{CLI: "/usr/local/bin/monoagentcli", DBPath: testDB,
		ProfileID: "work-id", Name: "Work", Osascript: osascript})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path []any
		want any
	}{
		{[]any{"BundleIdentifier"}, "com.apple.RunShellScript"},
		{[]any{"ActionBundlePath"}, "/System/Library/Automator/Run Shell Script.action"},
		{[]any{"ActionParameters", "COMMAND_STRING"}, script},
		{[]any{"ActionParameters", "inputMethod"}, int64(0)},
		{[]any{"ActionParameters", "shell"}, "/bin/sh"},
		{[]any{"ActionParameters", "CheckedForUserDefaultShell"}, true},
	} {
		if got := dig(t, action, c.path...); got != c.want {
			t.Errorf("the action's %v = %#v, want %#v", c.path, got, c.want)
		}
	}
	meta := dig(t, doc, "workflowMetaData")
	for key, want := range map[string]string{
		"workflowTypeIdentifier":      "com.apple.Automator.servicesMenu",
		"serviceInputTypeIdentifier":  "com.apple.Automator.text",
		"serviceOutputTypeIdentifier": "com.apple.Automator.nothing",
	} {
		if got := dig(t, meta, key); got != want {
			t.Errorf("workflowMetaData %s = %v, want %q", key, got, want)
		}
	}
}

func TestHostileProfileNamesStayText(t *testing.T) {
	spec := testSpec()
	spec.ProfileName = hostileName
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	info := parsePlist(t, b.Info)
	if got := dig(t, info, "NSServices", 0, "NSMenuItem", "default"); got != MenuPrefix+": "+hostileClean {
		t.Errorf("menu item %q", got)
	}
	if got := dig(t, info, "NSServices", 0, "NSMessage"); got != "runWorkflowAsService" {
		t.Errorf("the name rewrote the service's message: %v", got)
	}
	script, _ := dig(t, parsePlist(t, b.Document), "actions", 0, "action", "ActionParameters", "COMMAND_STRING").(string)
	if !strings.Contains(script, "\nname="+shellQuote(hostileClean)+"\n") {
		t.Errorf("the script does not hold the quoted name:\n%s", script)
	}
	if b.Name != "Add to MonoAgent Tasks ("+hostileClean+").workflow" {
		t.Errorf("bundle name %q", b.Name)
	}
}

func TestRenderRefusesValuesABundleCannotCarry(t *testing.T) {
	for _, c := range []struct {
		change func(*Spec)
		want   string
	}{
		{func(s *Spec) { s.CLI = "monoagentcli" }, `the monoagentcli path "monoagentcli" is not absolute`},
		{func(s *Spec) { s.DBPath = "~/.monoagent/monoagent.db" }, `the database path "~/.monoagent/monoagent.db" is not absolute`},
		{func(s *Spec) { s.CLI = "/usr/local/bin/mono\nagentcli" }, `the monoagentcli path "/usr/local/bin/mono\nagentcli" holds a control or hidden character, or is not UTF-8`},
		{func(s *Spec) { s.DBPath = "" }, "the database path is empty"},
		{func(s *Spec) { s.ProfileID = "work\x1bid" }, `the profile id "work\x1bid" holds a control or hidden character, or is not UTF-8`},
	} {
		spec := testSpec()
		c.change(&spec)
		if _, err := Render(spec); err == nil || err.Error() != c.want {
			t.Errorf("Render: %v, want %q", err, c.want)
		}
	}
	// A value with a character XML cannot hold passes plainValue; the last check
	// reads both rendered files back and refuses.
	spec := testSpec()
	spec.ProfileID = "work\U0000FFFEid"
	if _, err := Render(spec); err == nil || !strings.HasPrefix(err.Error(), "the rendered bundle is not well-formed XML: ") {
		t.Errorf("Render with U+FFFE in the profile id: %v", err)
	}
}

func TestRenderIsStableAndCarriesTheCLIPath(t *testing.T) {
	a, _ := Render(testSpec())
	b, _ := Render(testSpec())
	if !bytes.Equal(a.Info, b.Info) || !bytes.Equal(a.Document, b.Document) {
		t.Error("two renders of one spec differ: an installed menu could never be current")
	}
	other := testSpec()
	other.CLI = "/opt/bin/monoagentcli"
	c, _ := Render(other)
	if bytes.Equal(a.Info, c.Info) || bytes.Equal(a.Document, c.Document) {
		t.Error("the CLI path is not in both files")
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/tasks/osmenu/ -count=1`
Expected: FAIL to compile (`undefined: Render`).

- [ ] **Step 4: Write the template `internal/tasks/osmenu/templates/Info.plist.tmpl`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en_US</string>
	<key>CFBundleIdentifier</key>
	<string>{{plist .BundleID}}</string>
	<key>CFBundleName</key>
	<string>{{plist .Menu}}</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>MonoAgentTasksCLI</key>
	<string>{{plist .CLI}}</string>
	<key>MonoAgentTasksDB</key>
	<string>{{plist .DBPath}}</string>
	<key>MonoAgentTasksProfileID</key>
	<string>{{plist .ProfileID}}</string>
	<key>MonoAgentTasksVersion</key>
	<string>{{plist .Version}}</string>
	<key>NSServices</key>
	<array>
		<dict>
			<key>NSMenuItem</key>
			<dict>
				<key>default</key>
				<string>{{plist .Menu}}</string>
			</dict>
			<key>NSMessage</key>
			<string>runWorkflowAsService</string>
			<key>NSRequiredContext</key>
			<dict/>
			<key>NSSendTypes</key>
			<array>
				<string>public.utf8-plain-text</string>
			</array>
		</dict>
	</array>
</dict>
</plist>
```

- [ ] **Step 5: Write the template `internal/tasks/osmenu/templates/document.wflow.tmpl`**

Its action follows `/System/Library/Services/Show Map.workflow` (a Run Shell Script action that reads text on standard input) and its `workflowMetaData` Apple's plain-text service `Add to Music as a Spoken Track.workflow` (Ruling 4); no Automator window `state`.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AMApplicationBuild</key>
	<string>346</string>
	<key>AMApplicationVersion</key>
	<string>2.3</string>
	<key>AMDocumentVersion</key>
	<string>2</string>
	<key>actions</key>
	<array>
		<dict>
			<key>action</key>
			<dict>
				<key>AMAccepts</key>
				<dict>
					<key>Container</key>
					<string>List</string>
					<key>Optional</key>
					<true/>
					<key>Types</key>
					<array>
						<string>com.apple.cocoa.string</string>
					</array>
				</dict>
				<key>AMActionVersion</key>
				<string>2.0.3</string>
				<key>AMApplication</key>
				<array>
					<string>Automator</string>
				</array>
				<key>AMParameterProperties</key>
				<dict>
					<key>COMMAND_STRING</key>
					<dict/>
					<key>CheckedForUserDefaultShell</key>
					<dict/>
					<key>inputMethod</key>
					<dict/>
					<key>shell</key>
					<dict/>
					<key>source</key>
					<dict/>
				</dict>
				<key>AMProvides</key>
				<dict>
					<key>Container</key>
					<string>List</string>
					<key>Types</key>
					<array>
						<string>com.apple.cocoa.string</string>
					</array>
				</dict>
				<key>ActionBundlePath</key>
				<string>/System/Library/Automator/Run Shell Script.action</string>
				<key>ActionName</key>
				<string>Run Shell Script</string>
				<key>ActionParameters</key>
				<dict>
					<key>COMMAND_STRING</key>
					<string>{{plist .Script}}</string>
					<key>CheckedForUserDefaultShell</key>
					<true/>
					<key>inputMethod</key>
					<integer>0</integer>
					<key>shell</key>
					<string>/bin/sh</string>
					<key>source</key>
					<string></string>
				</dict>
				<key>BundleIdentifier</key>
				<string>com.apple.RunShellScript</string>
				<key>CFBundleVersion</key>
				<string>2.0.3</string>
				<key>CanShowSelectedItemsWhenRun</key>
				<false/>
				<key>CanShowWhenRun</key>
				<true/>
				<key>Category</key>
				<array>
					<string>AMCategoryUtilities</string>
				</array>
				<key>Class Name</key>
				<string>RunShellScriptAction</string>
				<key>InputUUID</key>
				<string>4E0C2B8A-6F1D-4C3E-9A57-2D8B1F6E3A90</string>
				<key>Keywords</key>
				<array>
					<string>Shell</string>
					<string>Script</string>
					<string>Command</string>
					<string>Run</string>
					<string>Unix</string>
				</array>
				<key>OutputUUID</key>
				<string>9B7D3F15-2A6C-4E88-B0D4-7C1E5A9F2B36</string>
				<key>UUID</key>
				<string>C35A8E2D-91F4-4B07-8D6A-E2F07B4C1D58</string>
				<key>UnlocalizedApplications</key>
				<array>
					<string>Automator</string>
				</array>
				<key>arguments</key>
				<dict>
					<key>0</key>
					<dict>
						<key>default value</key>
						<integer>0</integer>
						<key>name</key>
						<string>inputMethod</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>0</string>
					</dict>
					<key>1</key>
					<dict>
						<key>default value</key>
						<string></string>
						<key>name</key>
						<string>source</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>1</string>
					</dict>
					<key>2</key>
					<dict>
						<key>default value</key>
						<false/>
						<key>name</key>
						<string>CheckedForUserDefaultShell</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>2</string>
					</dict>
					<key>3</key>
					<dict>
						<key>default value</key>
						<string></string>
						<key>name</key>
						<string>COMMAND_STRING</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>3</string>
					</dict>
					<key>4</key>
					<dict>
						<key>default value</key>
						<string>/bin/sh</string>
						<key>name</key>
						<string>shell</string>
						<key>required</key>
						<string>0</string>
						<key>type</key>
						<string>0</string>
						<key>uuid</key>
						<string>4</string>
					</dict>
				</dict>
				<key>isViewVisible</key>
				<true/>
				<key>location</key>
				<string>309.500000:631.000000</string>
				<key>nibPath</key>
				<string>/System/Library/Automator/Run Shell Script.action/Contents/Resources/en.lproj/main.nib</string>
			</dict>
			<key>isViewVisible</key>
			<true/>
		</dict>
	</array>
	<key>connectors</key>
	<dict/>
	<key>workflowMetaData</key>
	<dict>
		<key>serviceApplicationBundleID</key>
		<string></string>
		<key>serviceApplicationPath</key>
		<string></string>
		<key>serviceInputTypeIdentifier</key>
		<string>com.apple.Automator.text</string>
		<key>serviceOutputTypeIdentifier</key>
		<string>com.apple.Automator.nothing</string>
		<key>serviceProcessesInput</key>
		<integer>0</integer>
		<key>workflowTypeIdentifier</key>
		<string>com.apple.Automator.servicesMenu</string>
	</dict>
</dict>
</plist>
```

- [ ] **Step 6: Write `internal/tasks/osmenu/bundle.go`**

```go
package osmenu

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"text/template"
)

// Version is the bundle's format, written into every bundle and raised when
// a template changes, so that older bundles read as stale. It is not the
// CLI's release: upgrading monoagentcli does not make a menu stale.
const Version = "1"

// The marker keys of a bundle's Info.plist. A bundle with a profile id there
// is one this package wrote and manages; its identity is the database and the
// profile id (every database has a profile "default").
const (
	KeyCLI       = "MonoAgentTasksCLI"
	KeyDB        = "MonoAgentTasksDB"
	KeyProfileID = "MonoAgentTasksProfileID"
	KeyVersion   = "MonoAgentTasksVersion"
)

// Where a bundle's two files sit inside it (slash-separated).
const (
	InfoPath     = "Contents/Info.plist"
	DocumentPath = "Contents/document.wflow"
)

//go:embed templates/Info.plist.tmpl
var infoTmpl string

//go:embed templates/document.wflow.tmpl
var documentTmpl string

var (
	infoTemplate     = template.Must(template.New("Info.plist").Funcs(funcs).Parse(infoTmpl))
	documentTemplate = template.Must(template.New("document.wflow").Funcs(funcs).Parse(documentTmpl))
)

// Spec is what one menu item is bound to.
type Spec struct {
	CLI         string // monoagentcli's absolute path
	DBPath      string // the database the profile is in, absolute
	ProfileID   string
	ProfileName string
}

// Bundle is a rendered Quick Action: the folder Name holding InfoPath and
// DocumentPath.
type Bundle struct {
	Name      string // "Add to MonoAgent Tasks (<name>).workflow"
	Menu      string // the Services menu item
	DBPath    string
	ProfileID string
	Info      []byte
	Document  []byte
}

// bundleView is what the two property lists are rendered from; the templates
// escape every field.
type bundleView struct {
	BundleID, Menu, CLI, DBPath, ProfileID, Version, Script string
}

// Render renders the menu item for spec. It refuses a path that is not
// absolute, and a path or profile id the bundle cannot carry exactly.
func Render(spec Spec) (Bundle, error) { return render(spec, osascript) }

func render(spec Spec, osa string) (Bundle, error) {
	for _, p := range []struct{ what, path string }{
		{"the monoagentcli path", spec.CLI},
		{"the database path", spec.DBPath},
	} {
		if err := plainValue(p.what, p.path); err != nil {
			return Bundle{}, err
		}
		if !strings.HasPrefix(p.path, "/") {
			return Bundle{}, fmt.Errorf("%s %q is not absolute", p.what, p.path)
		}
	}
	if err := plainValue("the profile id", spec.ProfileID); err != nil {
		return Bundle{}, err
	}
	script, err := renderScript(scriptView{
		CLI: spec.CLI, DBPath: spec.DBPath, ProfileID: spec.ProfileID,
		Name: menuName(spec.ProfileName, spec.ProfileID), Osascript: osa,
	})
	if err != nil {
		return Bundle{}, err
	}
	v := bundleView{
		BundleID: bundleID(spec.ProfileID), Menu: MenuTitle(spec.ProfileName, spec.ProfileID),
		CLI: spec.CLI, DBPath: spec.DBPath, ProfileID: spec.ProfileID, Version: Version, Script: script,
	}
	var info, doc bytes.Buffer
	if err := infoTemplate.Execute(&info, v); err != nil {
		return Bundle{}, err
	}
	if err := documentTemplate.Execute(&doc, v); err != nil {
		return Bundle{}, err
	}
	for _, f := range [][]byte{info.Bytes(), doc.Bytes()} {
		if err := wellFormed(f); err != nil {
			return Bundle{}, fmt.Errorf("the rendered bundle is not well-formed XML: %w", err)
		}
	}
	return Bundle{
		Name: BundleName(spec.ProfileName, spec.ProfileID), Menu: v.Menu, DBPath: spec.DBPath, ProfileID: spec.ProfileID,
		Info: info.Bytes(), Document: doc.Bytes(),
	}, nil
}

// wellFormed reads data through with an XML reader: the last check that no
// value broke a property list (a character XML cannot hold, say).
func wellFormed(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
```

- [ ] **Step 7: Run the tests to see them pass**

Run: `gofmt -l internal/tasks/osmenu` then `go vet ./internal/tasks/osmenu/` then `go test ./internal/tasks/osmenu/ -count=1` then `grep -nP '[^\x00-\x7F]' internal/tasks/osmenu/bundle.go internal/tasks/osmenu/bundle_test.go internal/tasks/osmenu/plist_test.go internal/tasks/osmenu/templates/Info.plist.tmpl internal/tasks/osmenu/templates/document.wflow.tmpl`
Expected: no output from `gofmt` or `grep`, vet clean, PASS.

- [ ] **Step 8: Commit**

```
git add internal/tasks/osmenu/bundle.go internal/tasks/osmenu/bundle_test.go internal/tasks/osmenu/plist_test.go internal/tasks/osmenu/templates/Info.plist.tmpl internal/tasks/osmenu/templates/document.wflow.tmpl
```
then
```
git commit -m "feat(tasks): render the Add to MonoAgent Tasks Quick Action bundle" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Install, list and remove bundles in a folder

**Files:**
- Create: `internal/tasks/osmenu/install.go`
- Test: `internal/tasks/osmenu/install_test.go`

**Interfaces:**
- Consumes: Task 3's `Bundle`, `Render`, `Spec`, `InfoPath`, `DocumentPath`, `KeyCLI`, `KeyProfileID`, `KeyVersion`, `Version`; test helpers `testSpec`.
- Produces: `ErrTaken`; `Outcome` with `Created` (`installed`), `Unchanged` (`already_installed`), `Updated` (`updated`); `Result{Path, Menu string; Outcome Outcome; Removed []string}`; `Menu{Path, ProfileID, DB, CLI, Version string}`; `Install(dir string, b Bundle, force bool, gone func(profileID string) bool) (Result, error)` (a menu is the profile's when its database and profile id are b's); `List(dir string) ([]Menu, error)` (never nil); `Remove(dir, db, profileID string) ([]string, error)` (never nil; only that database's menus; also sweeps leftover `.monoagent-menu-*` folders); `Matches(path string, b Bundle) bool`; unexported `tmpPrefix`, `rename` (a package variable, `os.Rename`, that a test replaces), `write` (moves an old bundle aside and back on failure), `readMarker(path string) (Menu, bool)`, `topLevelStrings([]byte) map[string]string`. Test helpers used by Task 5: `cliPath`, `noneGone`, `mustRender(t, id, name, cli string) Bundle`, `writeFile(t, path, body string)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/tasks/osmenu/install_test.go`:

```go
package osmenu

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const cliPath = "/usr/local/bin/monoagentcli"

// noneGone says no profile was deleted.
func noneGone(string) bool { return false }

// mustRender renders the menu of profile id, named name.
func mustRender(t *testing.T, id, name, cli string) Bundle {
	t.Helper()
	b, err := Render(Spec{CLI: cli, DBPath: testDB, ProfileID: id, ProfileName: name})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// writeFile writes body at path, creating its folders.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallWritesTheBundleOnceAndThenLeavesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Services") // missing: Install creates it
	b := mustRender(t, "work-id", "Work", cliPath)
	res, err := Install(dir, b, false, noneGone)
	if err != nil || res.Outcome != Created || res.Path != filepath.Join(dir, b.Name) || res.Menu != b.Menu ||
		res.Removed == nil || len(res.Removed) != 0 {
		t.Fatalf("Install: %+v, %v", res, err)
	}
	if !Matches(res.Path, b) {
		t.Fatal("the files on disk are not the rendered bundle")
	}
	extra := filepath.Join(res.Path, "Contents", "QuickLook") // Automator adds such a folder; a rewrite would drop it
	if err := os.Mkdir(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	again, err := Install(dir, b, false, noneGone)
	if err != nil || again.Outcome != Unchanged {
		t.Fatalf("second Install: %+v, %v", again, err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Error("an unchanged menu was rewritten")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the folder holds %d entries, want the bundle alone (no temporary folder left)", len(entries))
	}
}

func TestInstallRewritesAStaleMenu(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, mustRender(t, "work-id", "Work", "/old/monoagentcli"), false, noneGone); err != nil {
		t.Fatal(err)
	}
	b := mustRender(t, "work-id", "Work", cliPath)
	res, err := Install(dir, b, false, noneGone)
	if err != nil || res.Outcome != Updated || !Matches(res.Path, b) {
		t.Errorf("Install over a stale menu: %+v, %v", res, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the folder holds %d entries, want the new bundle alone (the old one moved aside is gone)", len(entries))
	}
}

// When the new bundle cannot be moved in, the old one is put back.
func TestInstallPutsTheOldMenuBackWhenTheNewOneCannotGoIn(t *testing.T) {
	dir := t.TempDir()
	old := mustRender(t, "work-id", "Work", "/old/monoagentcli")
	if _, err := Install(dir, old, false, noneGone); err != nil {
		t.Fatal(err)
	}
	orig := rename
	t.Cleanup(func() { rename = orig })
	rename = func(from, to string) error {
		if base := filepath.Base(from); strings.HasPrefix(base, tmpPrefix) && !strings.HasSuffix(base, "-old") {
			return errors.New("injected: the move in failed")
		}
		return orig(from, to)
	}
	_, err := Install(dir, mustRender(t, "work-id", "Work", cliPath), false, noneGone)
	if err == nil || !strings.Contains(err.Error(), "injected: the move in failed") {
		t.Fatalf("Install: %v", err)
	}
	if !Matches(filepath.Join(dir, old.Name), old) {
		t.Error("the old menu was not put back")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the folder holds %d entries, want the old bundle alone", len(entries))
	}
}

func TestInstallFollowsARenamedProfile(t *testing.T) {
	dir := t.TempDir()
	old, err := Install(dir, mustRender(t, "work-id", "Work", cliPath), false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	b := mustRender(t, "work-id", "Job", cliPath)
	res, err := Install(dir, b, false, noneGone)
	if err != nil || res.Outcome != Updated || !reflect.DeepEqual(res.Removed, []string{old.Path}) {
		t.Fatalf("Install after a rename: %+v, %v", res, err)
	}
	if menus, _ := List(dir); len(menus) != 1 || menus[0].Path != res.Path {
		t.Errorf("menus after a rename: %+v", menus)
	}
}

// On macOS's default disk a rename in case only names the same folder: the
// "older" menu is the new one, and must not be removed once it is written.
func TestInstallFollowsARenameInCaseOnly(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, mustRender(t, "work-id", "work", cliPath), false, noneGone); err != nil {
		t.Fatal(err)
	}
	b := mustRender(t, "work-id", "Work", cliPath)
	res, err := Install(dir, b, false, noneGone)
	if err != nil || res.Outcome != Updated {
		t.Fatalf("Install: %+v, %v", res, err)
	}
	if menus, _ := List(dir); len(menus) != 1 || !Matches(res.Path, b) {
		t.Errorf("after a rename in case only: %+v", menus)
	}
}

func TestInstallKeepsWhatItDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	b := mustRender(t, "work-id", "Work", cliPath)
	foreign := filepath.Join(dir, b.Name)
	writeFile(t, filepath.Join(foreign, "Contents", "Info.plist"),
		`<?xml version="1.0"?><plist version="1.0"><dict><key>NSServices</key><array/></dict></plist>`)
	_, err := Install(dir, b, false, noneGone)
	if !errors.Is(err, ErrTaken) || !strings.Contains(err.Error(), "was not written by monoagentcli task os install (--force replaces it)") {
		t.Fatalf("Install over a foreign bundle: %v", err)
	}
	if Matches(foreign, b) {
		t.Fatal("the foreign bundle was replaced without --force")
	}
	res, err := Install(dir, b, true, noneGone)
	if err != nil || res.Outcome != Updated || !Matches(res.Path, b) {
		t.Errorf("Install --force: %+v, %v", res, err)
	}
}

// Two profiles whose names clean to one name share a bundle name: the other
// profile's menu is never replaced while it exists, even with force; once that
// profile is deleted its menu is replaced.
func TestInstallNeverReplacesAnotherProfilesMenu(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, mustRender(t, "other-id", "Work:A", cliPath), false, noneGone); err != nil {
		t.Fatal(err)
	}
	b := mustRender(t, "work-id", "Work/A", cliPath)
	for _, force := range []bool{false, true} {
		_, err := Install(dir, b, force, noneGone)
		if !errors.Is(err, ErrTaken) || !strings.Contains(err.Error(), "is the menu of profile other-id, whose name reads the same; rename one of the two profiles") {
			t.Fatalf("force=%v: %v", force, err)
		}
	}
	gone := func(id string) bool { return id == "other-id" }
	res, err := Install(dir, b, false, gone)
	if err != nil || res.Outcome != Updated || !Matches(res.Path, b) {
		t.Errorf("over a deleted profile's menu: %+v, %v", res, err)
	}
}

// Every database has a profile "default": a menu's identity is its database
// and its profile id. Another database's menu is taken only with force, and
// never removed by this database's uninstall.
func TestInstallKeepsAnotherDatabasesMenu(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, mustRender(t, "default", "Default", cliPath), false, noneGone); err != nil {
		t.Fatal(err)
	}
	b, err := Render(Spec{CLI: cliPath, DBPath: "/tmp/other.db", ProfileID: "default", ProfileName: "Default"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(dir, b, false, noneGone)
	if !errors.Is(err, ErrTaken) || !strings.Contains(err.Error(), "files into another database, "+testDB+" (--force replaces it)") {
		t.Fatalf("Install from another database: %v", err)
	}
	if removed, err := Remove(dir, "/tmp/other.db", "default"); err != nil || len(removed) != 0 {
		t.Errorf("Remove from another database: %v, %v", removed, err)
	}
	res, err := Install(dir, b, true, noneGone)
	if err != nil || res.Outcome != Updated || !Matches(res.Path, b) {
		t.Errorf("Install --force from another database: %+v, %v", res, err)
	}
}

func TestListFindsOnlyManagedBundles(t *testing.T) {
	dir := t.TempDir()
	res, err := Install(dir, mustRender(t, "work-id", "Work", cliPath), false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(filepath.Join(res.Path, filepath.FromSlash(InfoPath)))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "Other.workflow", "Contents", "Info.plist"),
		`<plist version="1.0"><dict><key>NSServices</key><array/></dict></plist>`)
	writeFile(t, filepath.Join(dir, "Nested.workflow", "Contents", "Info.plist"),
		`<plist version="1.0"><dict><key>NSServices</key><array><dict><key>`+KeyProfileID+`</key><string>work-id</string></dict></array></dict></plist>`)
	writeFile(t, filepath.Join(dir, "Copy.bundle", "Contents", "Info.plist"), string(info))
	writeFile(t, filepath.Join(dir, "Binary.workflow", "Contents", "Info.plist"), "bplist00\x00\x01")
	writeFile(t, filepath.Join(dir, "File.workflow"), "not a folder")
	if err := os.Symlink(res.Path, filepath.Join(dir, "Link.workflow")); err != nil {
		t.Logf("no symlink here (%v): that case goes unchecked", err)
	}
	menus, err := List(dir)
	want := []Menu{{Path: res.Path, ProfileID: "work-id", DB: testDB, CLI: cliPath, Version: Version}}
	if err != nil || !reflect.DeepEqual(menus, want) {
		t.Errorf("List: %+v, %v; want only %+v", menus, err, want)
	}
	none, err := List(filepath.Join(dir, "missing"))
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("List of a missing folder: %#v, %v", none, err)
	}
}

func TestRemoveTakesOnlyThatProfilesMenus(t *testing.T) {
	dir := t.TempDir()
	mine, err := Install(dir, mustRender(t, "work-id", "Work", cliPath), false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := Install(dir, mustRender(t, "home-id", "Home", cliPath), false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "Foreign.workflow")
	writeFile(t, filepath.Join(foreign, "Contents", "Info.plist"), "<plist><dict/></plist>")
	leftover := filepath.Join(dir, tmpPrefix+"123-old") // an interrupted install's
	writeFile(t, filepath.Join(leftover, "Contents", "Info.plist"), "<plist><dict/></plist>")
	removed, err := Remove(dir, testDB, "work-id")
	if err != nil || !reflect.DeepEqual(removed, []string{mine.Path}) {
		t.Fatalf("Remove: %v, %v", removed, err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Error("an interrupted install's temporary folder was left in place")
	}
	for _, p := range []string{theirs.Path, foreign} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
	again, err := Remove(dir, testDB, "work-id")
	if err != nil || again == nil || len(again) != 0 {
		t.Errorf("a second Remove: %#v, %v", again, err)
	}
}

func TestMatchesComparesTheNameAndBothFiles(t *testing.T) {
	dir := t.TempDir()
	b := mustRender(t, "work-id", "Work", cliPath)
	res, err := Install(dir, b, false, noneGone)
	if err != nil || !Matches(res.Path, b) {
		t.Fatalf("Install: %v", err)
	}
	renamed := b
	renamed.Name = "Add to MonoAgent Tasks (Job).workflow"
	if Matches(res.Path, renamed) {
		t.Error("a bundle under another name matched")
	}
	doc := filepath.Join(res.Path, filepath.FromSlash(DocumentPath))
	if err := os.WriteFile(doc, append(append([]byte{}, b.Document...), ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if Matches(res.Path, b) {
		t.Error("a changed document.wflow matched")
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/tasks/osmenu/ -count=1`
Expected: FAIL to compile (`undefined: Install`).

- [ ] **Step 3: Write `internal/tasks/osmenu/install.go`**

```go
package osmenu

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrTaken is Install's refusal to replace a bundle of the same name that is
// not this profile's menu.
var ErrTaken = errors.New("the menu's bundle name is taken")

// Outcome says what Install did.
type Outcome string

const (
	Created   Outcome = "installed"
	Unchanged Outcome = "already_installed"
	Updated   Outcome = "updated"
)

// Result is what Install did, and where.
type Result struct {
	Path    string
	Menu    string
	Outcome Outcome
	Removed []string // the profile's older menus (a renamed profile's old name), never nil
}

// Menu is a bundle this package wrote, found in a Services folder.
type Menu struct {
	Path      string
	ProfileID string
	DB        string // the database it files into
	CLI       string
	Version   string
}

// Install writes b into dir, creating dir when it is missing. A menu is this
// profile's when its database and profile id are b's. What is at b's name
// decides: nothing, or this profile's menu, is written over (an exact copy of b
// is left alone); another database's menu, and a bundle this package did not
// write, are replaced only with force; the menu of a profile gone reports as
// deleted is replaced; the menu of another profile of this database is never
// replaced. The profile's menus under other names are removed.
func Install(dir string, b Bundle, force bool, gone func(profileID string) bool) (Result, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	target := filepath.Join(dir, b.Name)
	res := Result{Path: target, Menu: b.Menu, Outcome: Created, Removed: []string{}}
	at, err := os.Lstat(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, err
	}
	menus, err := List(dir)
	if err != nil {
		return Result{}, err
	}
	var older []string
	for _, m := range menus {
		if m.ProfileID != b.ProfileID || m.DB != b.DBPath {
			continue
		}
		if at != nil {
			// On a case-insensitive disk a name that differs in case only is
			// the target itself.
			if fi, err := os.Lstat(m.Path); err == nil && os.SameFile(fi, at) {
				continue
			}
		}
		older = append(older, m.Path)
	}
	if at != nil {
		var m Menu
		var ok bool
		if at.IsDir() {
			m, ok = readMarker(target)
		}
		switch {
		case ok && m.ProfileID == b.ProfileID && m.DB == b.DBPath:
			if len(older) == 0 && Matches(target, b) {
				res.Outcome = Unchanged
				return res, nil
			}
		case ok && m.DB != b.DBPath:
			if !force {
				return Result{}, fmt.Errorf("%w: %s files into another database, %s (--force replaces it)", ErrTaken, target, m.DB)
			}
		case ok && gone(m.ProfileID):
			// The menu of a deleted profile can only fail: it is replaced.
		case ok:
			return Result{}, fmt.Errorf("%w: %s is the menu of profile %s, whose name reads the same; rename one of the two profiles", ErrTaken, target, m.ProfileID)
		case !force:
			return Result{}, fmt.Errorf("%w: %s was not written by monoagentcli task os install (--force replaces it)", ErrTaken, target)
		}
		res.Outcome = Updated
	}
	if err := write(dir, target, b); err != nil {
		return Result{}, err
	}
	for _, p := range older {
		if err := os.RemoveAll(p); err != nil {
			return res, fmt.Errorf("removing the older menu %s: %w", p, err)
		}
		res.Removed = append(res.Removed, p)
		res.Outcome = Updated
	}
	return res, nil
}

// tmpPrefix starts the temporary folders write uses inside a Services folder;
// a name without ".workflow" is never read as a service.
const tmpPrefix = ".monoagent-menu-"

// rename is os.Rename; a test replaces it to make a move fail.
var rename = os.Rename

// write builds b in a temporary folder inside dir and then moves it to
// target, so that the Services menu never reads a half-written bundle. A
// bundle already at target is moved aside first and put back if the move in
// fails.
func write(dir, target string, b Bundle) error {
	tmp, err := os.MkdirTemp(dir, tmpPrefix)
	if err != nil {
		return fmt.Errorf("writing the menu: %w", err)
	}
	defer os.RemoveAll(tmp) // a no-op once tmp has been moved
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fmt.Errorf("writing the menu: %w", err)
	}
	for _, f := range []struct {
		rel  string
		data []byte
	}{{InfoPath, b.Info}, {DocumentPath, b.Document}} {
		p := filepath.Join(tmp, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("writing the menu: %w", err)
		}
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			return fmt.Errorf("writing the menu: %w", err)
		}
	}
	aside := ""
	if _, err := os.Lstat(target); err == nil {
		aside = tmp + "-old"
		if err := rename(target, aside); err != nil {
			return fmt.Errorf("replacing %s: %w", target, err)
		}
	}
	if err := rename(tmp, target); err != nil {
		if aside != "" {
			_ = rename(aside, target) // the old menu goes back
		}
		return fmt.Errorf("writing the menu: %w", err)
	}
	if aside != "" {
		_ = os.RemoveAll(aside) // the new menu is in place; Remove sweeps what stays
	}
	return nil
}

// List returns the managed bundles in dir (never nil): real folders named
// *.workflow whose Info.plist carries a profile id marker at its top level. A
// folder that does not exist holds none.
func List(dir string) ([]Menu, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Menu{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	menus := []Menu{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".workflow") {
			continue // a symlink is not a folder here: it is never managed
		}
		if m, ok := readMarker(filepath.Join(dir, e.Name())); ok {
			menus = append(menus, m)
		}
	}
	return menus, nil
}

// Remove removes the managed bundles of profile profileID of database db from
// dir and returns their paths (never nil). A bundle this package did not write,
// or another database's, is never touched; the temporary folders an
// interrupted write left behind are removed too.
func Remove(dir, db, profileID string) ([]string, error) {
	menus, err := List(dir)
	if err != nil {
		return nil, err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), tmpPrefix) {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	removed := []string{}
	for _, m := range menus {
		if m.ProfileID != profileID || m.DB != db {
			continue
		}
		if err := os.RemoveAll(m.Path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", m.Path, err)
		}
		removed = append(removed, m.Path)
	}
	return removed, nil
}

// Matches reports whether the bundle at path is exactly b: the same folder
// name and the same two files.
func Matches(path string, b Bundle) bool {
	if filepath.Base(path) != b.Name {
		return false
	}
	info, err1 := os.ReadFile(filepath.Join(path, filepath.FromSlash(InfoPath)))
	doc, err2 := os.ReadFile(filepath.Join(path, filepath.FromSlash(DocumentPath)))
	return err1 == nil && err2 == nil && bytes.Equal(info, b.Info) && bytes.Equal(doc, b.Document)
}

// readMarker reads the marker keys of the bundle at path. ok is false when the
// bundle has no XML Info.plist with a profile id at its top level: not a
// bundle this package manages.
func readMarker(path string) (Menu, bool) {
	data, err := os.ReadFile(filepath.Join(path, filepath.FromSlash(InfoPath)))
	if err != nil {
		return Menu{}, false
	}
	keys := topLevelStrings(data)
	m := Menu{Path: path, ProfileID: keys[KeyProfileID], DB: keys[KeyDB], CLI: keys[KeyCLI], Version: keys[KeyVersion]}
	return m, m.ProfileID != ""
}

// topLevelStrings returns the string values of the top-level dict of an XML
// property list, by key; nested values are skipped. A file that is not XML
// gives what was read before the error.
func topLevelStrings(data []byte) map[string]string {
	out := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, key := 0, ""
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth != 3 { // 1 is <plist>, 2 the top dict, 3 its keys and values
				continue
			}
			switch t.Name.Local {
			case "key", "string":
				var s string
				if d.DecodeElement(&s, &t) != nil {
					return out
				}
				depth-- // DecodeElement also read the end element
				if t.Name.Local == "key" {
					key = s
					continue
				}
				if key != "" {
					out[key] = s
				}
			}
			key = ""
		case xml.EndElement:
			depth--
		}
	}
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `gofmt -l internal/tasks/osmenu` then `go vet ./internal/tasks/osmenu/` then `go test ./internal/tasks/osmenu/ -count=1` then `grep -nP '[^\x00-\x7F]' internal/tasks/osmenu/install.go internal/tasks/osmenu/install_test.go`
Expected: no output from `gofmt` or `grep`, vet clean, PASS. On this Mac `TestInstallFollowsARenameInCaseOnly` takes the case-insensitive path (one folder); on Linux CI it takes the other (the older folder removed); it must pass on both.

- [ ] **Step 5: Commit**

```
git add internal/tasks/osmenu/install.go internal/tasks/osmenu/install_test.go
```
then
```
git commit -m "feat(tasks): install, list and remove the macOS menu's bundles, keeping what they did not write" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Check the bundle with Apple's own tools (darwin only)

**Files:**
- Test: `internal/tasks/osmenu/bundle_darwin_test.go`, `internal/tasks/osmenu/automator_darwin_test.go`

**Interfaces:**
- Consumes: `Render`, `render`, `Install`, `Spec`, `MenuPrefix`, `osascript`, `scriptView`, `renderScript`, `InfoPath`, `DocumentPath`; test helpers `testSpec`, `hostileName`, `hostileClean`, `noneGone`, `newRig`, `(*rig).read`.
- Produces: evidence that `plutil`, `pbs` and `automator` read the bundle as rendered. These tests need no new code when Tasks 3 and 4 are right; a failure means a template is wrong.

What this can and cannot show: `plutil` proves both files are valid property lists and that Apple's parser reads back the menu item and the script exactly. `pbs -read_bundle` prints the service the Services agent would list, without updating its cache. `automator -i` runs the workflow's action with the given input; it is not the Services path (a Services click hands one text item from the app), so whether macOS lists and runs the item from a right-click stays for the user to verify (spec 17.6).

- [ ] **Step 1: Write the plutil and pbs tests**

Create `internal/tasks/osmenu/bundle_darwin_test.go`:

```go
//go:build darwin

package osmenu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Apple's plutil validates both files and reads back the menu item and the
// script exactly as rendered, a hostile profile name included.
func TestPlutilReadsWhatWasRendered(t *testing.T) {
	const plutil = "/usr/bin/plutil"
	if _, err := os.Stat(plutil); err != nil {
		t.Skip("no plutil")
	}
	spec := testSpec()
	spec.ProfileName = hostileName
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Install(t.TempDir(), b, false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(res.Path, filepath.FromSlash(InfoPath))
	doc := filepath.Join(res.Path, filepath.FromSlash(DocumentPath))
	if out, err := exec.Command(plutil, "-lint", info, doc).CombinedOutput(); err != nil || strings.Count(string(out), ": OK") != 2 {
		t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
	menu, err := exec.Command(plutil, "-extract", "NSServices.0.NSMenuItem.default", "raw", "-o", "-", info).Output()
	if want := MenuPrefix + ": " + hostileClean; err != nil || strings.TrimRight(string(menu), "\n") != want {
		t.Errorf("plutil reads the menu item as %q (%v), want %q", menu, err, want)
	}
	want, err := renderScript(scriptView{CLI: spec.CLI, DBPath: spec.DBPath, ProfileID: spec.ProfileID, Name: hostileClean, Osascript: osascript})
	if err != nil {
		t.Fatal(err)
	}
	script, err := exec.Command(plutil, "-extract", "actions.0.action.ActionParameters.COMMAND_STRING", "raw", "-o", "-", doc).Output()
	if err != nil || strings.TrimRight(string(script), "\n") != strings.TrimRight(want, "\n") {
		t.Errorf("plutil reads the script as:\n%s\n(%v), want:\n%s", script, err, want)
	}
}

// pbs, the Services agent, prints the service it would list. -read_bundle
// only reads: it does not update the Services cache.
func TestTheServicesAgentReadsTheBundle(t *testing.T) {
	const pbs = "/System/Library/CoreServices/pbs"
	if _, err := os.Stat(pbs); err != nil {
		t.Skip("no pbs")
	}
	b, err := Render(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Install(t.TempDir(), b, false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(pbs, "-read_bundle", res.Path).CombinedOutput()
	if err != nil {
		t.Fatalf("pbs -read_bundle: %v\n%s", err, out)
	}
	for _, want := range []string{`default = "Add to MonoAgent Tasks: Work";`, "NSMessage = runWorkflowAsService;", "NSRequiredContext =", `"public.utf8-plain-text"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("pbs does not print %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Write the opt-in automator test**

Create `internal/tasks/osmenu/automator_darwin_test.go`:

```go
//go:build darwin

package osmenu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAutomatorRunsTheWorkflow runs the rendered workflow with Automator's own
// runner, against the stubs of the script tests: no real monoagentcli runs and
// no notification appears. Opt-in, because it starts Automator's machinery on
// this Mac: MONOAGENT_AUTOMATOR_TEST=1.
func TestAutomatorRunsTheWorkflow(t *testing.T) {
	if os.Getenv("MONOAGENT_AUTOMATOR_TEST") != "1" {
		t.Skip("set MONOAGENT_AUTOMATOR_TEST=1 to run the workflow with /usr/bin/automator")
	}
	if _, err := os.Stat("/usr/bin/automator"); err != nil {
		t.Skip("no automator")
	}
	r := newRig(t)
	b, err := render(Spec{CLI: r.view.CLI, DBPath: r.view.DBPath, ProfileID: "work-id", ProfileName: "Work"}, r.view.Osascript)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Install(filepath.Join(r.dir, "Services"), b, false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// automator -i makes one input item of each newline-terminated line (so the
	// last line ends with one); a Services click hands one text item. The bytes
	// that arrive are logged for the record.
	out, err := exec.CommandContext(ctx, "/usr/bin/automator", "-i", "Reply to Sam\nabout the invoice\n", res.Path).CombinedOutput()
	if err != nil {
		t.Fatalf("automator: %v\n%s", err, out)
	}
	got := r.read("stdin")
	t.Logf("standard input as monoagentcli read it: %q", got)
	first, second := strings.Index(got, "Reply to Sam"), strings.Index(got, "about the invoice")
	if first < 0 || second < first {
		t.Errorf("the selection did not reach monoagentcli in order: %q", got)
	}
	if args := r.read("args"); !strings.Contains(args, "--source\nos\n--app=Safari\n") {
		t.Errorf("arguments:\n%s", args)
	}
	if note := r.read("notified"); !strings.HasSuffix(note, "\nAdded to Inbox in Work\n") {
		t.Errorf("notification:\n%s", note)
	}
}
```

- [ ] **Step 3: Run the checks**

Run, one per call:

```
go test ./internal/tasks/osmenu/ -run 'TestPlutilReadsWhatWasRendered|TestTheServicesAgentReadsTheBundle' -count=1 -v
MONOAGENT_AUTOMATOR_TEST=1 go test ./internal/tasks/osmenu/ -run TestAutomatorRunsTheWorkflow -count=1 -v
GOOS=linux go vet ./internal/tasks/osmenu/
GOOS=windows go vet ./internal/tasks/osmenu/
```

Expected: the two read checks run (not skipped) and PASS; the automator test PASSES and its log line shows the standard input it received; both vets print nothing. Save the `-v` output for the PR description.

- [ ] **Step 4: If a check fails**

Stop and send the lead the tool's output; do not change the templates or the layout to make it pass. `Contents/document.wflow` is the layout of Automator-saved workflows and of the third-party Quick Actions registered on this Mac (Ruling 4), so a failure is not a reason to move it.

- [ ] **Step 5: Commit**

```
git add internal/tasks/osmenu/bundle_darwin_test.go internal/tasks/osmenu/automator_darwin_test.go
```
then
```
git commit -m "feat(tasks): check the macOS menu's bundle with plutil, pbs and automator" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The `task os` commands

**Files:**
- Create: `cmd/monoagentcli/task_os.go`, `cmd/monoagentcli/ref_tasks_os.go`, `cmd/monoagentcli/ref_tasks_group_test.go` (Step 5)
- Modify: `cmd/monoagentcli/task.go` (one line), `cmd/monoagentcli/ref_tasks.go` (the `operator_only` sentence of WHO MAY DO WHAT), P1's tests `cmd/monoagentcli/ref_tasks_test.go` and `cmd/monoagentcli/ref_tasks_gate_test.go` (Step 5)
- Test: `cmd/monoagentcli/task_os_test.go`

**Interfaces:**
- Consumes: Task 0's P1 names (`withTasks`, `taskErr`, `callerFor`, `operator`, `flagAs`, `cutArg`, `initDB`, `resolveProfileID`, `expandPath`, `errInvalidInput`, `withJSONErrors`, `writeJSONTo`, `cliDocs`, `cmdDoc`, `tasks.Profile`, `tasks.Store`, `tasks.NewStore`, `tasks.ErrInvalid`, and the test helpers `newTaskTestDB`, `runTask`, `mustTaskJSON`, `failedTaskJSON`, `addedJSON`, `exitCode`); `osmenu.Spec`, `osmenu.Render`, `osmenu.Install`, `osmenu.List`, `osmenu.Remove`, `osmenu.Matches`, `osmenu.Menu`, `osmenu.Result`, `osmenu.ErrTaken`, `osmenu.Unchanged`, `osmenu.Updated`, `osmenu.DocumentPath`.
- Produces: `newTaskOSCmd(cfg *globalConfig) *cobra.Command` (registered in `newTaskCmd`); package variables `taskOSGOOS`, `taskOSExecutable`, `refreshServices`; `taskOSOnly() error`, `taskOSDir(cmd) (string, bool, error)`, `taskOSDBPath(cfg) (string, error)`, `taskOSMenuSpec(cfg, cli string, p tasks.Profile) (osmenu.Spec, error)`, `taskOSRunnable(path string) bool`, `withTaskStore(cfg, cmd, fn func(ctx context.Context, store *tasks.Store, db *sql.DB, active string) error) error`, `taskOSState(...)`, the printers; four `cliDocs` entries (`task os`, `task os install`, `task os status`, `task os uninstall`); in P1's tests (Step 5) `refTaskSub` and `refHasFlag` changed and moved to the new `ref_tasks_group_test.go`, `refTaskCommands` and `refTaskCallee` new beside them, `refGatePath` new in `ref_tasks_gate_test.go`, and the rows `os install`, `os status` and `os uninstall` of `refTaskGate`.
- Order of the checks in a command: `install` and `uninstall` (the operator's) refuse an agent, then count their arguments (any argument is `errInvalidInput`: exit 3, the `--json` document, not cobra's `Args`, whose mistake is exit 1 with no document), then check the platform; `status` (open to an agent) counts its arguments, then checks the platform. Only then do they look at `--dest`, the executable or the database.
- JSON: `install` is `{"profile", "path", "menu_item", "cli", "outcome", "removed"}` (`cli` is the monoagentcli the menu runs; `outcome` one of `installed`, `already_installed`, `updated`); `status` is `{"dir", "menus": [{"path", "profile": {"id","name"}, "cli", "db", "state", "why"}]}` (`state` one of `current`, `stale`, `profile_gone`, `other_database`; `name` empty when the profile is gone or in another database); `uninstall` is `{"profile_id", "removed"}`. Arrays are never null.

- [ ] **Step 1: Write the failing tests**

Create `cmd/monoagentcli/task_os_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/tasks/osmenu"
	"github.com/monoes/mono-agent/internal/testdb"
)

// newTaskOSTest is a task test database (the operator's environment) on a
// pretend Mac: a home folder of its own, checked, so that an install without
// --dest writes under it and never under the real one; this binary is a stub
// file in a temporary folder; refreshing the Services menu is counted, never
// run.
func newTaskOSTest(t *testing.T) (db, dest, cli string, refreshes *int) {
	t.Helper()
	db = newTaskTestDB(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	if got, err := os.UserHomeDir(); err != nil || got != home {
		t.Fatalf("the home folder is %q (%v), not the test's own %q: refusing to run near a real Services folder", got, err, home)
	}
	goos, exe, refresh := taskOSGOOS, taskOSExecutable, refreshServices
	t.Cleanup(func() { taskOSGOOS, taskOSExecutable, refreshServices = goos, exe, refresh })
	cli = taskOSStubCLI(t)
	n := 0
	taskOSGOOS = "darwin"
	taskOSExecutable = func() (string, error) { return cli, nil }
	refreshServices = func() { n++ }
	return db, filepath.Join(t.TempDir(), "Services"), cli, &n
}

// taskOSStubCLI is an executable file standing for an installed monoagentcli.
func taskOSStubCLI(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bin", "monoagentcli")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func execTaskOSSQL(t *testing.T, db, query string, args ...any) {
	t.Helper()
	raw, err := storage.NewDatabase(db)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.DB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func addTaskOSProfile(t *testing.T, db, id, name string) {
	t.Helper()
	execTaskOSSQL(t, db, `INSERT INTO profiles (id, name) VALUES (?, ?)`, id, name)
}

// taskOSInstalled is `task os install --json`.
type taskOSInstalled struct {
	Profile struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"profile"`
	Path     string   `json:"path"`
	MenuItem string   `json:"menu_item"`
	CLI      string   `json:"cli"`
	Outcome  string   `json:"outcome"`
	Removed  []string `json:"removed"`
}

func TestTaskOSInstallBindsTheActiveProfile(t *testing.T) {
	db, dest, cli, refreshes := newTaskOSTest(t)
	var got taskOSInstalled
	mustTaskJSON(t, db, "", &got, "", "os", "install", "--dest", dest)
	want := filepath.Join(dest, "Add to MonoAgent Tasks (Default).workflow")
	if got.Profile.ID != "default" || got.Profile.Name != "Default" || got.Path != want || got.CLI != cli ||
		got.MenuItem != "Add to MonoAgent Tasks: Default" || got.Outcome != "installed" || got.Removed == nil {
		t.Errorf("install: %+v", got)
	}
	doc, err := os.ReadFile(filepath.Join(want, filepath.FromSlash(osmenu.DocumentPath)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"cli='" + cli + "'", "db='" + db + "'", "profile='default'", "task add --stdin --source os"} {
		if !strings.Contains(string(doc), s) {
			t.Errorf("the workflow does not hold %q", s)
		}
	}
	if *refreshes != 0 {
		t.Error("a --dest install refreshed the real Services menu")
	}
}

func TestTaskOSInstallNamesAProfileByNameAndOnceIsEnough(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	addTaskOSProfile(t, db, "work-id", "Work")
	var first, second taskOSInstalled
	mustTaskJSON(t, db, "Work", &first, "", "os", "install", "--dest", dest)
	mustTaskJSON(t, db, "work-id", &second, "", "os", "install", "--dest", dest)
	if first.Profile.ID != "work-id" || first.Outcome != "installed" || second.Outcome != "already_installed" || second.Path != first.Path {
		t.Errorf("first %+v, second %+v", first, second)
	}
	out, _, err := runTask(t, db, "Work", false, "", "os", "install", "--dest", dest)
	if err != nil || !strings.Contains(out, "Already installed") {
		t.Errorf("text: %q, %v", out, err)
	}
}

func TestTaskOSInstallSaysWhereTheItemIs(t *testing.T) {
	db, dest, cli, _ := newTaskOSTest(t)
	out, _, err := runTask(t, db, "", false, "", "os", "install", "--dest", dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Installed "Add to MonoAgent Tasks: Default"`, "profile Default (default)", "  runs " + cli + "\n",
		"right-click it and choose Services", "System Settings, Keyboard, Keyboard", "Shortcuts, Services, Text", "--profile NAME task os install"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
}

func TestTaskOSRefusesAwayFromMacOS(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	taskOSGOOS = "linux"
	for _, sub := range []string{"install", "status", "uninstall"} {
		doc := failedTaskJSON(t, db, "", 3, "os", sub, "--dest", dest)
		if msg, _ := doc["error"].(string); doc["code"] != "invalid_input" || !strings.Contains(msg, "this is linux") ||
			!strings.Contains(msg, "task add --stdin --source os") {
			t.Errorf("os %s: %v", sub, doc)
		}
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("something was written away from macOS")
	}
}

// An agent is refused as an agent on every platform, before the platform is looked at: P1's gate
// test runs the real commands as an agent on whatever machine it is on, a Linux runner too.
func TestTaskOSInstallAndUninstallAreTheOperators(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	t.Setenv("CLAUDECODE", "1")
	for _, goos := range []string{"darwin", "linux", "windows"} {
		taskOSGOOS = goos
		for _, sub := range []string{"install", "uninstall"} {
			if doc := failedTaskJSON(t, db, "", 3, "os", sub, "--dest", dest); doc["code"] != "operator_only" {
				t.Errorf("os %s under an agent's marker on %s: %v", sub, goos, doc)
			}
		}
	}
	taskOSGOOS = "darwin"
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("an agent's install wrote the menu")
	}
	var st struct {
		Menus []any `json:"menus"`
	}
	mustTaskJSON(t, db, "", &st, "", "os", "status", "--dest", dest)
}

// A mistake in the arguments is exit 3 with the --json document, like every other one of the group
// (cobra's own Args check would be exit 1 and no document); the operator's two commands refuse an
// agent that makes one as an agent, first; nothing is written.
func TestTaskOSCommandsTakeNoArguments(t *testing.T) {
	db, dest, _, refreshes := newTaskOSTest(t)
	for _, sub := range []string{"install", "status", "uninstall"} {
		doc := failedTaskJSON(t, db, "", 3, "os", sub, "junk", "--dest", dest)
		if msg, _ := doc["error"].(string); doc["code"] != "invalid_input" || !strings.Contains(msg, "task os "+sub+" takes no arguments") {
			t.Errorf("os %s junk: %v", sub, doc)
		}
		if _, _, err := runTask(t, db, "", false, "", "os", sub, "junk", "--dest", dest); exitCode(err) != 3 {
			t.Errorf("os %s junk as text: exit %d (%v), want 3", sub, exitCode(err), err)
		}
	}
	for _, sub := range []string{"install", "uninstall"} {
		if doc := failedTaskJSON(t, db, "", 3, "os", sub, "junk", "--as", "bot", "--dest", dest); doc["code"] != "operator_only" {
			t.Errorf("os %s junk run by an agent: %v, want operator_only before the arguments are read", sub, doc)
		}
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) || *refreshes != 0 {
		t.Errorf("a refused call wrote or refreshed something (%d refreshes)", *refreshes)
	}
}

func TestTaskOSInstallRefusesATemporaryBuild(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	taskOSExecutable = func() (string, error) { return "/var/folders/x/T/go-build123/b001/exe/monoagentcli", nil }
	doc := failedTaskJSON(t, db, "", 3, "os", "install", "--dest", dest)
	if msg, _ := doc["error"].(string); doc["code"] != "invalid_input" || !strings.Contains(msg, "is a temporary build") {
		t.Errorf("%v", doc)
	}
}

func TestTaskOSInstallKeepsWhatItDidNotWrite(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	info := filepath.Join(dest, "Add to MonoAgent Tasks (Default).workflow", "Contents", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(info), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info, []byte("<plist><dict/></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := failedTaskJSON(t, db, "", 3, "os", "install", "--dest", dest)
	if msg, _ := doc["error"].(string); doc["code"] != "invalid_input" || !strings.Contains(msg, "--force replaces it") {
		t.Fatalf("%v", doc)
	}
	var got taskOSInstalled
	mustTaskJSON(t, db, "", &got, "", "os", "install", "--dest", dest, "--force")
	if got.Outcome != "updated" {
		t.Errorf("install --force: %+v", got)
	}
}

func TestTaskOSInstallNeverTakesAnotherProfilesMenu(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	addTaskOSProfile(t, db, "a-id", "Work/A")
	addTaskOSProfile(t, db, "b-id", "Work:A")
	var got taskOSInstalled
	mustTaskJSON(t, db, "a-id", &got, "", "os", "install", "--dest", dest)
	for _, args := range [][]string{{"os", "install", "--dest", dest}, {"os", "install", "--dest", dest, "--force"}} {
		doc := failedTaskJSON(t, db, "b-id", 3, args...)
		if msg, _ := doc["error"].(string); !strings.Contains(msg, "is the menu of profile a-id") {
			t.Errorf("%v: %v", args, doc)
		}
	}
}

func TestTaskOSRefreshesTheRealServicesFolderOnly(t *testing.T) {
	db, dest, _, refreshes := newTaskOSTest(t)
	var got taskOSInstalled
	mustTaskJSON(t, db, "", &got, "", "os", "install")
	home, err := os.UserHomeDir() // newTaskOSTest made it a temporary folder and checked it
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Library", "Services", "Add to MonoAgent Tasks (Default).workflow"); got.Path != want || *refreshes != 1 {
		t.Fatalf("install into the Services folder of the test's HOME: %s, %d refreshes", got.Path, *refreshes)
	}
	mustTaskJSON(t, db, "", &got, "", "os", "install")
	if got.Outcome != "already_installed" || *refreshes != 1 {
		t.Errorf("an unchanged install refreshed the menu: %s, %d", got.Outcome, *refreshes)
	}
	var un struct {
		Removed []string `json:"removed"`
	}
	mustTaskJSON(t, db, "", &un, "", "os", "uninstall")
	if len(un.Removed) != 1 || *refreshes != 2 {
		t.Errorf("uninstall: %v, %d refreshes", un.Removed, *refreshes)
	}
	mustTaskJSON(t, db, "", &got, "", "os", "install", "--dest", dest)
	mustTaskJSON(t, db, "", &un, "", "os", "uninstall", "--dest", dest)
	if *refreshes != 2 {
		t.Errorf("a --dest install or uninstall refreshed the real Services menu: %d", *refreshes)
	}
}

func TestTaskOSStatusSaysWhichMenusAreCurrent(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	var empty struct {
		Menus []any `json:"menus"`
	}
	mustTaskJSON(t, db, "", &empty, "", "os", "status", "--dest", dest)
	if empty.Menus == nil || len(empty.Menus) != 0 {
		t.Errorf("no menus: %#v", empty.Menus)
	}
	for _, p := range [][2]string{{"work-id", "Work"}, {"home-id", "Home"}, {"gone-id", "Gone"}} {
		addTaskOSProfile(t, db, p[0], p[1])
	}
	for _, p := range []string{"default", "work-id", "gone-id"} {
		var got taskOSInstalled
		mustTaskJSON(t, db, p, &got, "", "os", "install", "--dest", dest)
	}
	other := testdb.Path(t) // a second database, with a menu of its own in the same folder
	addTaskOSProfile(t, other, "else-id", "Elsewhere")
	var elsewhere taskOSInstalled
	mustTaskJSON(t, other, "else-id", &elsewhere, "", "os", "install", "--dest", dest)
	moved := taskOSStubCLI(t) // home's menu runs a monoagentcli that is then removed
	taskOSExecutable = func() (string, error) { return moved, nil }
	var home taskOSInstalled
	mustTaskJSON(t, db, "home-id", &home, "", "os", "install", "--dest", dest)
	if err := os.Remove(moved); err != nil {
		t.Fatal(err)
	}
	execTaskOSSQL(t, db, `UPDATE profiles SET name = 'Job' WHERE id = 'work-id'`)
	execTaskOSSQL(t, db, `DELETE FROM profiles WHERE id = 'gone-id'`)
	var st struct {
		Dir   string `json:"dir"`
		Menus []struct {
			Profile struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"profile"`
			State string `json:"state"`
			Why   string `json:"why"`
		} `json:"menus"`
	}
	mustTaskJSON(t, db, "", &st, "", "os", "status", "--dest", dest)
	got := map[string]string{}
	for _, m := range st.Menus {
		got[m.Profile.ID] = m.State + " | " + m.Profile.Name + " | " + m.Why
	}
	want := map[string]string{
		"default": "current | Default | ",
		"work-id": "stale | Job | the profile was renamed, or the menu was changed or written in an older format",
		"home-id": "stale | Home | monoagentcli is not at " + moved,
		"gone-id": "profile_gone |  | the profile was deleted",
		"else-id": "other_database |  | it files into another database",
	}
	if st.Dir != dest || !reflect.DeepEqual(got, want) {
		t.Errorf("status in %s:\n%v\nwant:\n%v", st.Dir, got, want)
	}
	out, _, err := runTask(t, db, "", false, "", "os", "status", "--dest", dest)
	for _, w := range []string{"current", "monoagentcli --profile work-id task os install", "profile gone",
		"monoagentcli --profile gone-id task os uninstall", "other database", "monoagentcli --db-path " + other + " task os status"} {
		if err != nil || !strings.Contains(out, w) {
			t.Errorf("the status text does not say %q (%v):\n%s", w, err, out)
		}
	}
}

// Every database has a profile "default": an install or an uninstall run with
// another database never takes or removes this database's menu (--force takes it).
func TestTaskOSInstallNeverTakesAnotherDatabasesMenu(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	other := testdb.Path(t)
	var got taskOSInstalled
	mustTaskJSON(t, db, "", &got, "", "os", "install", "--dest", dest)
	doc := failedTaskJSON(t, other, "", 3, "os", "install", "--dest", dest)
	if msg, _ := doc["error"].(string); doc["code"] != "invalid_input" || !strings.Contains(msg, "files into another database") {
		t.Errorf("an install from another database: %v", doc)
	}
	var un struct {
		Removed []string `json:"removed"`
	}
	mustTaskJSON(t, other, "", &un, "", "os", "uninstall", "--dest", dest)
	if un.Removed == nil || len(un.Removed) != 0 {
		t.Errorf("an uninstall from another database removed %v", un.Removed)
	}
	mustTaskJSON(t, other, "", &got, "", "os", "install", "--dest", dest, "--force")
	if got.Outcome != "updated" {
		t.Errorf("install --force from another database: %+v", got)
	}
}

func TestTaskOSUninstallRemovesOneProfilesMenu(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	addTaskOSProfile(t, db, "work-id", "Work")
	var def, work taskOSInstalled
	mustTaskJSON(t, db, "", &def, "", "os", "install", "--dest", dest)
	mustTaskJSON(t, db, "Work", &work, "", "os", "install", "--dest", dest)
	var un struct {
		ProfileID string   `json:"profile_id"`
		Removed   []string `json:"removed"`
	}
	mustTaskJSON(t, db, "Work", &un, "", "os", "uninstall", "--dest", dest)
	if un.ProfileID != "work-id" || !reflect.DeepEqual(un.Removed, []string{work.Path}) {
		t.Errorf("uninstall: %+v", un)
	}
	if _, err := os.Stat(def.Path); err != nil {
		t.Error("another profile's menu was removed")
	}
	mustTaskJSON(t, db, "Work", &un, "", "os", "uninstall", "--dest", dest)
	if un.Removed == nil || len(un.Removed) != 0 {
		t.Errorf("a second uninstall: %+v", un)
	}
	out, _, err := runTask(t, db, "Work", false, "", "os", "uninstall", "--dest", dest)
	if err != nil || !strings.Contains(out, "nothing to remove") {
		t.Errorf("text: %q, %v", out, err)
	}
}

func TestTaskOSUninstallFindsADeletedProfilesMenuByItsID(t *testing.T) {
	db, dest, _, _ := newTaskOSTest(t)
	addTaskOSProfile(t, db, "gone-id", "Gone")
	var got taskOSInstalled
	mustTaskJSON(t, db, "gone-id", &got, "", "os", "install", "--dest", dest)
	execTaskOSSQL(t, db, `DELETE FROM profiles WHERE id = 'gone-id'`)
	var un struct {
		Removed []string `json:"removed"`
	}
	mustTaskJSON(t, db, "gone-id", &un, "", "os", "uninstall", "--dest", dest)
	if !reflect.DeepEqual(un.Removed, []string{got.Path}) {
		t.Errorf("uninstall of a deleted profile's menu: %+v", un)
	}
}

// The menu's own command line (spec 7): from the operator's environment it is
// a capture in the Inbox; for a deleted profile it fails with the CLI's own
// error (the line the menu's notification shows) and files nothing; with an
// agent's marker and no --as, --source os is refused.
func TestTheMenusCommandLineIsACaptureOnlyForTheOperator(t *testing.T) {
	db := newTaskTestDB(t)
	args := []string{"add", "--stdin", "--source", "os", "--app=Safari"}
	var added addedJSON
	mustTaskJSON(t, db, "default", &added, "Reply to Sam\nabout the invoice", args...)
	if added.Task.Source.Kind != "os" || added.Task.Status != "inbox" || added.Task.Title != "Reply to Sam" {
		t.Errorf("the menu's capture: %+v", added.Task)
	}
	addTaskOSProfile(t, db, "gone-id", "Gone")
	execTaskOSSQL(t, db, `DELETE FROM profiles WHERE id = 'gone-id'`)
	out, _, err := runTask(t, db, "gone-id", true, "Reply to Sam", args...)
	var doc map[string]any
	_ = json.Unmarshal([]byte(out), &doc)
	if msg, _ := doc["error"].(string); exitCode(err) != 3 || msg != `initializing database: profile "gone-id" not found (checked both id and name)` {
		t.Errorf("the menu's command line for a deleted profile: exit %d, %s", exitCode(err), out)
	}
	if n := countTaskOSTasks(t, db); n != 1 {
		t.Errorf("%d tasks, want only the first capture", n)
	}
	t.Setenv("CLAUDECODE", "1")
	out, _, err = runTask(t, db, "default", true, "Reply to Sam", args...)
	doc = nil
	_ = json.Unmarshal([]byte(out), &doc)
	if msg, _ := doc["error"].(string); exitCode(err) != 3 || doc["code"] != "invalid_input" || !strings.Contains(msg, `source "os" is for captures`) {
		t.Errorf("an agent context filing as the macOS menu: exit %d, %s", exitCode(err), out)
	}
}

// countTaskOSTasks counts every task in the test database.
func countTaskOSTasks(t *testing.T, db string) int {
	t.Helper()
	raw, err := storage.NewDatabase(db)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var n int
	if err := raw.DB.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEveryTaskOSCommandHasAReferenceEntry(t *testing.T) {
	have := map[string]bool{}
	for _, d := range cliDocs {
		have[d.Name] = true
	}
	names := []string{"task os"}
	for _, sub := range newTaskOSCmd(&globalConfig{}).Commands() {
		names = append(names, "task os "+sub.Name())
	}
	for _, n := range names {
		if !have[n] {
			t.Errorf("`ref commands` has no entry for `%s`", n)
		}
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./cmd/monoagentcli/ -run 'TestTaskOS|TestTheMenusCommandLine|TestEveryTaskOSCommand' -count=1`
Expected: FAIL to compile (`undefined: taskOSGOOS`).

- [ ] **Step 3: Write `cmd/monoagentcli/task_os.go`**

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/tasks"
	"github.com/monoes/mono-agent/internal/tasks/osmenu"
)

// What the tests replace: the OS, this binary's path, and the refresh that
// makes the Services menu read ~/Library/Services again.
var (
	taskOSGOOS       = runtime.GOOS
	taskOSExecutable = os.Executable
	refreshServices  = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "/System/Library/CoreServices/pbs", "-update").Run()
	}
)

// newTaskOSCmd is `task os`: the macOS Services menu item that files the text
// selected in any app into one profile's Inbox (spec section 12).
func newTaskOSCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "os",
		Short: "The macOS menu: select text in any app, then Services, Add to MonoAgent Tasks",
		Long: `Installs a macOS Quick Action, "Add to MonoAgent Tasks: <profile>", in the
Services menu of every app (right-click selected text, or the app's menu,
Services). It runs

  monoagentcli --profile <id> task add --stdin --source os

with the selected text on standard input (never on a command line), so the
text lands in the Inbox of one profile: the one --profile names, else the
active profile when you install. Run install once per profile. No app or
daemon has to be running. macOS may list the item only after you enable it
once in System Settings, Keyboard, Keyboard Shortcuts, Services, Text.

Windows and Linux have no such menu: bind a global hotkey to a command that
pipes the selected text into that same command (see monoagentcli ref tasks).`,
	}
	cmd.PersistentFlags().String("dest", "", "The Services folder (default ~/Library/Services)")
	cmd.AddCommand(newTaskOSInstallCmd(cfg), newTaskOSStatusCmd(cfg), newTaskOSUninstallCmd(cfg))
	// The task group wraps only its own children; these are its grandchildren.
	for _, sub := range cmd.Commands() {
		withJSONErrors(cfg, sub)
	}
	return cmd
}

// taskOSOnly refuses the os commands away from macOS, with what to do there
// instead (spec D27).
func taskOSOnly() error {
	if taskOSGOOS == "darwin" {
		return nil
	}
	return errInvalidInput("task os installs a macOS Services menu, and this is %s: bind a global hotkey to a command that pipes the selected text into monoagentcli --profile <id> task add --stdin --source os (see monoagentcli ref tasks)", taskOSGOOS)
}

// taskOSDir is --dest, else ~/Library/Services. The bool says it is the real
// Services folder, which the Services menu is asked to read again.
func taskOSDir(cmd *cobra.Command) (string, bool, error) {
	if d, _ := cmd.Flags().GetString("dest"); d != "" {
		abs, err := filepath.Abs(expandPath(d))
		return abs, false, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("finding your home folder: %w", err)
	}
	return filepath.Join(home, "Library", "Services"), true, nil
}

// taskOSDBPath is the absolute path of the database this command uses: a menu
// files into it, and with the profile id it is the menu's identity.
func taskOSDBPath(cfg *globalConfig) (string, error) { return filepath.Abs(expandPath(cfg.DBPath)) }

// taskOSMenuSpec is the menu of profile p as install writes it: running cli,
// filing into the database this command uses.
func taskOSMenuSpec(cfg *globalConfig, cli string, p tasks.Profile) (osmenu.Spec, error) {
	db, err := taskOSDBPath(cfg)
	if err != nil {
		return osmenu.Spec{}, err
	}
	return osmenu.Spec{CLI: cli, DBPath: db, ProfileID: p.ID, ProfileName: p.Name}, nil
}

// taskOSRunnable reports whether path is a program the menu can run.
func taskOSRunnable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// withTaskStore opens the database without resolving --profile: status and
// uninstall take their profiles from the bundles, and some may be deleted.
// active is the active profile's id.
func withTaskStore(cfg *globalConfig, cmd *cobra.Command, fn func(ctx context.Context, store *tasks.Store, db *sql.DB, active string) error) error {
	open := &globalConfig{DBPath: cfg.DBPath}
	db, err := initDB(open)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer db.Close()
	return fn(cmd.Context(), tasks.NewStore(db.DB), db.DB, open.ProfileID)
}

func newTaskOSInstallCmd(cfg *globalConfig) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: `Add "Add to MonoAgent Tasks: <profile>" to the macOS Services menu (you only)`,
		Long: `Adds the menu item for one profile: --profile (an id or a name), else the
active profile, which the output names. Run it again after renaming the profile
or moving monoagentcli; a menu that is already current is left alone. --force
replaces a bundle of the same name that this command did not write; the menu of
another profile is never replaced.`,
		Example: `  monoagentcli task os install
  monoagentcli --profile Work task os install`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// As P1's operator commands do: an agent is refused first, on every platform; then the
			// arguments are counted (cobra's Args would be exit 1 with no --json document); then the
			// platform is checked.
			if _, err := callerFor(flagAs(cmd)).operator("install the macOS menu"); err != nil {
				return err
			}
			if len(args) != 0 {
				return errInvalidInput("task os install takes no arguments (got %q): the profile is --profile, as in monoagentcli --profile Work task os install", cutArg(args[0]))
			}
			if err := taskOSOnly(); err != nil {
				return err
			}
			dir, services, err := taskOSDir(cmd)
			if err != nil {
				return err
			}
			exe, err := taskOSExecutable()
			if err != nil {
				return fmt.Errorf("finding this monoagentcli: %w", err)
			}
			cli := filepath.Clean(exe)
			if strings.Contains(filepath.ToSlash(cli), "/go-build") {
				return errInvalidInput("this monoagentcli is a temporary build (%s) that is gone after the run: run task os install from the installed monoagentcli", cli)
			}
			return withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				spec, err := taskOSMenuSpec(cfg, cli, p)
				if err != nil {
					return err
				}
				b, err := osmenu.Render(spec)
				if err != nil {
					return errInvalidInput("%v", err)
				}
				gone := func(id string) bool {
					_, err := store.Profile(ctx, id)
					return errors.Is(err, tasks.ErrInvalid)
				}
				res, err := osmenu.Install(dir, b, force, gone)
				if errors.Is(err, osmenu.ErrTaken) {
					return errInvalidInput("%v", err)
				}
				if err != nil {
					return err
				}
				if services && res.Outcome != osmenu.Unchanged {
					refreshServices()
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]any{
						"profile": p, "path": res.Path, "menu_item": res.Menu, "cli": cli, "outcome": res.Outcome, "removed": res.Removed,
					})
				}
				printTaskOSInstalled(cmd.OutOrStdout(), p, res, cli)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Replace a bundle of the same name that this command did not write")
	return cmd
}

func printTaskOSInstalled(w io.Writer, p tasks.Profile, res osmenu.Result, cli string) {
	switch res.Outcome {
	case osmenu.Unchanged:
		fmt.Fprintf(w, "Already installed: %q files into profile %s (%s).\n  %s\n  runs %s\n", res.Menu, p.Name, p.ID, res.Path, cli)
		return
	case osmenu.Updated:
		fmt.Fprintf(w, "Updated %q: it files into profile %s (%s).\n  %s\n  runs %s\n", res.Menu, p.Name, p.ID, res.Path, cli)
		for _, old := range res.Removed {
			fmt.Fprintf(w, "  removed the older %s\n", old)
		}
	default:
		fmt.Fprintf(w, "Installed %q: it files into profile %s (%s).\n  %s\n  runs %s\n", res.Menu, p.Name, p.ID, res.Path, cli)
	}
	fmt.Fprintf(w, `Select text in any app, right-click it and choose Services, %q (or use the
app's menu, Services). The text goes to the Inbox of %s, where you read and approve it.
If the item is not listed, enable it once in System Settings, Keyboard, Keyboard
Shortcuts, Services, Text; you can give it a keyboard shortcut there too.
For another profile: monoagentcli --profile NAME task os install
`, res.Menu, p.Name)
}

// taskOSMenuJSON is one menu in `task os status --json`.
type taskOSMenuJSON struct {
	Path    string        `json:"path"`
	Profile tasks.Profile `json:"profile"` // the name is empty when the profile is gone or in another database
	CLI     string        `json:"cli"`
	DB      string        `json:"db"`
	State   string        `json:"state"` // current, stale, profile_gone or other_database
	Why     string        `json:"why"`   // empty when current
}

func newTaskOSStatusCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List the installed Add to MonoAgent Tasks menus and whether each is current (macOS)",
		Long: `Lists the menus task os install wrote in the Services folder, with the profile
each files into and its state: current; stale (the profile was renamed, the
menu's monoagentcli is gone, or the menu was changed or written in an older
format: install it again); profile_gone (the profile was deleted: uninstall it
with --profile and the id shown); or other_database (it files into another
database: run status with that --db-path to judge it).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errInvalidInput("task os status takes no arguments (got %q): its one option is --dest", cutArg(args[0]))
			}
			if err := taskOSOnly(); err != nil {
				return err
			}
			dir, _, err := taskOSDir(cmd)
			if err != nil {
				return err
			}
			dbPath, err := taskOSDBPath(cfg)
			if err != nil {
				return err
			}
			menus, err := osmenu.List(dir)
			if err != nil {
				return err
			}
			return withTaskStore(cfg, cmd, func(ctx context.Context, store *tasks.Store, _ *sql.DB, _ string) error {
				rows := make([]taskOSMenuJSON, 0, len(menus))
				for _, m := range menus {
					row, err := taskOSState(ctx, cfg, store, dbPath, m)
					if err != nil {
						return err
					}
					rows = append(rows, row)
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]any{"dir": dir, "menus": rows})
				}
				printTaskOSStatus(cmd.OutOrStdout(), dir, rows)
				return nil
			})
		},
	}
}

// taskOSState compares an installed menu with what install would write for
// it now with the monoagentcli it names, so that the answer does not depend on
// which copy of the CLI asks. A menu of another database than db is not judged
// against this one.
func taskOSState(ctx context.Context, cfg *globalConfig, store *tasks.Store, db string, m osmenu.Menu) (taskOSMenuJSON, error) {
	row := taskOSMenuJSON{Path: m.Path, Profile: tasks.Profile{ID: m.ProfileID}, CLI: m.CLI, DB: m.DB,
		State: "other_database", Why: "it files into another database"}
	if m.DB != db {
		return row, nil
	}
	row.State, row.Why = "profile_gone", "the profile was deleted"
	p, err := store.Profile(ctx, m.ProfileID)
	if errors.Is(err, tasks.ErrInvalid) {
		return row, nil
	}
	if err != nil {
		return row, taskErr(err)
	}
	row.Profile, row.State = p, "stale"
	if !taskOSRunnable(m.CLI) {
		row.Why = "monoagentcli is not at " + m.CLI
		return row, nil
	}
	row.Why = "the profile was renamed, or the menu was changed or written in an older format"
	spec, err := taskOSMenuSpec(cfg, m.CLI, p)
	if err != nil {
		return row, nil
	}
	if b, err := osmenu.Render(spec); err == nil && osmenu.Matches(m.Path, b) {
		row.State, row.Why = "current", ""
	}
	return row, nil
}

func printTaskOSStatus(w io.Writer, dir string, rows []taskOSMenuJSON) {
	if len(rows) == 0 {
		fmt.Fprintf(w, "No Add to MonoAgent Tasks menu is installed in %s.\nAdd one with: monoagentcli --profile NAME task os install\n", dir)
		return
	}
	fmt.Fprintf(w, "Add to MonoAgent Tasks menus in %s:\n", dir)
	for _, r := range rows {
		switch r.State {
		case "current":
			fmt.Fprintf(w, "  current       %s (%s)  %s\n", r.Profile.Name, r.Profile.ID, filepath.Base(r.Path))
		case "stale":
			fmt.Fprintf(w, "  stale         %s (%s)  %s\n      %s; to refresh: monoagentcli --profile %s task os install\n",
				r.Profile.Name, r.Profile.ID, filepath.Base(r.Path), r.Why, r.Profile.ID)
		case "other_database":
			fmt.Fprintf(w, "  other database  %s  %s\n      files into %s; to judge it: monoagentcli --db-path %s task os status\n",
				r.Profile.ID, filepath.Base(r.Path), r.DB, r.DB)
		default:
			fmt.Fprintf(w, "  profile gone  %s  %s\n      to remove: monoagentcli --profile %s task os uninstall\n",
				r.Profile.ID, filepath.Base(r.Path), r.Profile.ID)
		}
	}
}

func newTaskOSUninstallCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove a profile's Add to MonoAgent Tasks menu (you only; macOS)",
		Long: `Removes the menu that files into the profile --profile names (an id or a
name), else into the active profile, of the database this command uses
(--db-path). A profile deleted since is named by its id, as task os status
prints it. Only a menu task os install wrote is removed; nothing to remove is
not an error.`,
		Example: `  monoagentcli --profile Work task os uninstall`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := callerFor(flagAs(cmd)).operator("remove the macOS menu"); err != nil {
				return err
			}
			if len(args) != 0 {
				return errInvalidInput("task os uninstall takes no arguments (got %q): the profile is --profile, as in monoagentcli --profile Work task os uninstall", cutArg(args[0]))
			}
			if err := taskOSOnly(); err != nil {
				return err
			}
			dir, services, err := taskOSDir(cmd)
			if err != nil {
				return err
			}
			dbPath, err := taskOSDBPath(cfg)
			if err != nil {
				return err
			}
			return withTaskStore(cfg, cmd, func(_ context.Context, _ *tasks.Store, db *sql.DB, active string) error {
				id := active
				if cfg.ProfileID != "" {
					id = cfg.ProfileID // a deleted profile's id, as status prints it
					if resolved, err := resolveProfileID(db, cfg.ProfileID); err == nil {
						id = resolved
					}
				}
				removed, err := osmenu.Remove(dir, dbPath, id)
				if err != nil {
					return err
				}
				if services && len(removed) > 0 {
					refreshServices()
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]any{"profile_id": id, "removed": removed})
				}
				if len(removed) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "No Add to MonoAgent Tasks menu files into profile %s in %s: nothing to remove.\n", id, dir)
				}
				for _, p := range removed {
					fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", p)
				}
				return nil
			})
		},
	}
}
```

- [ ] **Step 4: Write `cmd/monoagentcli/ref_tasks_os.go`**

```go
package main

// The `task os` entries of `ref commands`, beside ref_tasks.go's.
func init() {
	cliDocs = append(cliDocs,
		cmdDoc{
			Name:  "task os",
			Short: "The macOS menu: select text in any app, then Services, Add to MonoAgent Tasks",
			Usage: "monoagentcli [--profile P] task os install|status|uninstall [--dest DIR]",
			Flags: `  install     Add the menu item for one profile (run once per profile)
  status      The installed menus: current, stale, or profile_gone
  uninstall   Remove one profile's menu
  --dest DIR  The Services folder (default ~/Library/Services)`,
			Examples: []string{
				"monoagentcli --profile Work task os install",
				"monoagentcli task os status",
			},
		},
		cmdDoc{
			Name:  "task os install",
			Short: `Add "Add to MonoAgent Tasks: <profile>" to the macOS Services menu, filing into one profile (the operator only)`,
			Usage: "monoagentcli [--profile P] task os install [--force] [--dest DIR]",
			Flags: `  --force     Replace a bundle of the same name that this command did not write
  --dest DIR  The Services folder (default ~/Library/Services)`,
			Examples: []string{
				"monoagentcli task os install                  # the active profile",
				"monoagentcli --profile Work task os install   # once per profile",
			},
		},
		cmdDoc{
			Name:     "task os status",
			Short:    "The installed Add to MonoAgent Tasks menus: their profile and whether each is current",
			Usage:    "monoagentcli task os status [--dest DIR]",
			Examples: []string{"monoagentcli --json task os status"},
		},
		cmdDoc{
			Name:     "task os uninstall",
			Short:    "Remove a profile's Add to MonoAgent Tasks menu (a deleted profile is named by its id; the operator only)",
			Usage:    "monoagentcli [--profile P] task os uninstall [--dest DIR]",
			Examples: []string{"monoagentcli --profile Work task os uninstall"},
		},
	)
}
```

- [ ] **Step 5: Register the group in `newTaskCmd`, and teach P1's reference tests a command group**

In `cmd/monoagentcli/task.go`, add `newTaskOSCmd(cfg),` as the last entry of the `cmd.AddCommand(` list in `newTaskCmd`, after the entry that is last there now (Task 0 printed the list). Change nothing else in the file. The group is registered on every platform (Ruling 19), so the rows below are right on macOS, Linux and Windows.

`os` is the first command group inside the task group, and P1's reference tests read every `task NAME` as a direct subcommand and every flag as a flag of that command. With Step 4's entries and the group registered, three of them fail: `TestRefTasksEntriesDescribeTheirOwnCommand` (`task os install` "is no command", and its examples are read as calls of `os`), `TestRefTasksSuggestsOnlyCommandsTheCLIHas` (`--force` and `--dest`, written after `task os`, are looked for on the group; and a group's own `--dest` is declared with `PersistentFlags()`, which cobra merges into `Flags()` only when something asks for the inherited flags, so the first look at a command that `Find` has just returned does not see it) and `TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent` (`task os` has no row). Teach them the group in the seven places below. `ref_tasks_test.go` has 497 lines and the limit is 500, so it must not grow: items 1 and 2 move the two helpers that have to change into a new file, and the other items are Edit calls whose old text occurs once in its file (Task 0 checked). If one reads differently by now, amend what says the same, keep what it checks and report it.

1. `cmd/monoagentcli/ref_tasks_test.go`, the helpers leave. `refTaskSub` and `refHasFlag` move to the new file of item 2, where `refTaskSub` takes a path, `refHasFlag` sees a group's own flags, and `refTaskCommands` and `refTaskCallee` are new; the import of cobra goes with them (nothing else in this file names it). Replace

```go
// refTaskSub finds `task NAME` the way the CLI does, in a root command whose global
// flags its subcommands inherit; nil for a name that is no subcommand.
func refTaskSub(name string) *cobra.Command {
	sub, _, err := newRootCmd().Find([]string{"task", name})
	if err != nil || sub.Name() != name || sub.Parent() == nil || sub.Parent().Name() != "task" {
		return nil
	}
	return sub
}

// refHasFlag says whether a subcommand declares the flag or inherits it.
func refHasFlag(sub *cobra.Command, name string) bool {
	return sub.Flags().Lookup(name) != nil || sub.InheritedFlags().Lookup(name) != nil
}

// refTaskEntries are the `ref commands` entries of the task group, by subcommand.
```

with

```go
// refTaskEntries are the `ref commands` entries of the task group, by subcommand.
```

and replace

```go
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/tasks"
```

with

```go
	"time"

	"github.com/monoes/mono-agent/internal/tasks"
```

2. Create `cmd/monoagentcli/ref_tasks_group_test.go`: the helpers that read the commands of the task group by path, `refTaskSub` and `refHasFlag` as they are to be, `refTaskCommands` (every command of the group by its path) and `refTaskCallee` (reads a call written in a text: `task os install --force` calls `install`; the command tree says which commands are groups).

```go
package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// The helpers of the tests that hold the texts and the gate table to the commands of the task group.
// A command of the group is named by its path under task: "add" or, in a group of its own, "os install".

// refTaskSub finds `task NAME` the way the CLI does, in a root command whose global
// flags its subcommands inherit. NAME is a command of the group, or the path to one in a
// group of its own ("os install"); nil for a name that is no command.
func refTaskSub(name string) *cobra.Command {
	words := strings.Fields(name)
	if len(words) == 0 {
		return nil
	}
	sub, rest, err := newRootCmd().Find(append([]string{"task"}, words...))
	if err != nil || len(rest) != 0 || sub.Name() != words[len(words)-1] {
		return nil
	}
	return sub
}

// refTaskCommands are the commands of the task group by their path under task: "add", "os"
// and, below a group, "os install".
func refTaskCommands() map[string]*cobra.Command {
	found := map[string]*cobra.Command{}
	var walk func(prefix string, group *cobra.Command)
	walk = func(prefix string, group *cobra.Command) {
		for _, sub := range group.Commands() {
			found[prefix+sub.Name()] = sub
			walk(prefix+sub.Name()+" ", sub)
		}
	}
	walk("", newTaskCmd(&globalConfig{}))
	return found
}

// refTaskCallee reads a call that refTaskCall found in a text: the command it calls, as its
// path under task ("add", "os install"), and what is left of the line after it, which holds
// the call's flags. A group takes the word after it for its subcommand when that is one, so
// `task os install --force` calls install: the command tree says which commands are groups.
// The command is nil when the call names none.
func refTaskCallee(call []string) (path string, sub *cobra.Command, rest string) {
	path, rest = call[1], call[2]
	if sub = refTaskSub(path); sub == nil {
		return path, nil, rest
	}
	for sub.HasSubCommands() {
		word, tail, _ := strings.Cut(strings.TrimSpace(rest), " ")
		next := refTaskSub(path + " " + word)
		if word == "" || next == nil {
			break
		}
		path, sub, rest = path+" "+word, next, tail
	}
	return path, sub, rest
}

// refHasFlag says whether a command declares the flag, as its own or, for a group, as one
// of all its commands (os has --dest), or inherits it.
func refHasFlag(sub *cobra.Command, name string) bool {
	return sub.Flags().Lookup(name) != nil || sub.PersistentFlags().Lookup(name) != nil || sub.InheritedFlags().Lookup(name) != nil
}
```

3. `cmd/monoagentcli/ref_tasks_test.go`, in `TestRefTasksEntriesDescribeTheirOwnCommand`: an example of a group's entry calls one of its commands (`task os`: `task os install`), and no other entry's example may call anything but its own command. Replace

```go
		for _, ex := range d.Examples {
			if calls := refTaskCall.FindAllStringSubmatch(ex, -1); len(calls) != 1 || calls[0][1] != name {
				t.Errorf("the example %q of `task %s` is not a call of that command", ex, name)
			}
		}
```

with

```go
		for _, ex := range d.Examples {
			calls := refTaskCall.FindAllStringSubmatch(ex, -1)
			if len(calls) != 1 {
				t.Errorf("the example %q of `task %s` is not one call of the task group", ex, name)
				continue
			}
			// A group's examples call its subcommands (task os install), and no other entry's do.
			if path, _, _ := refTaskCallee(calls[0]); path != name && !strings.HasPrefix(path, name+" ") {
				t.Errorf("the example %q of `task %s` is not a call of that command", ex, name)
			}
		}
```

4. The same file, in `TestRefTasksEntriesNameEveryFlagOfTheirCommand`: every command of the group is held to its entry, the leaves of `os` too. Replace

```go
	for _, sub := range newTaskCmd(&globalConfig{}).Commands() {
		d := refTaskEntries()[sub.Name()] // no entry at all is TestEveryTaskCommandHasAReferenceEntry's to say
		documented := append(refFlagsIn(d.Usage), refFlagsIn(d.Flags)...)
		for _, m := range refUsageFlag.FindAllStringSubmatch(sub.LocalFlags().FlagUsages(), -1) {
			if m[1] != "help" && !slices.Contains(documented, m[1]) {
				t.Errorf("`task %s` has --%s, which its `ref commands` entry does not show", sub.Name(), m[1])
			}
		}
	}
```

with

```go
	for path, sub := range refTaskCommands() {
		d := refTaskEntries()[path] // no entry at all is TestEveryTaskCommandHasAReferenceEntry's to say
		documented := append(refFlagsIn(d.Usage), refFlagsIn(d.Flags)...)
		for _, m := range refUsageFlag.FindAllStringSubmatch(sub.LocalFlags().FlagUsages(), -1) {
			if m[1] != "help" && !slices.Contains(documented, m[1]) {
				t.Errorf("`task %s` has --%s, which its `ref commands` entry does not show", path, m[1])
			}
		}
	}
```

5. The same file, in `TestRefTasksSuggestsOnlyCommandsTheCLIHas`: the flags written after a call are checked against the command the call reaches. Replace

```go
		for _, m := range refTaskCall.FindAllStringSubmatch(text, -1) {
			sub := refTaskSub(m[1])
			if sub == nil {
				t.Errorf("%s suggests `task %s`, which is no command", where, m[1])
				continue
			}
			for _, flag := range refFlagsIn(m[2]) {
				if !refHasFlag(sub, flag) {
					t.Errorf("%s suggests `task %s` with --%s, which it does not have", where, m[1], flag)
				}
			}
		}
```

with

```go
		for _, m := range refTaskCall.FindAllStringSubmatch(text, -1) {
			path, sub, rest := refTaskCallee(m)
			if sub == nil {
				t.Errorf("%s suggests `task %s`, which is no command", where, path)
				continue
			}
			for _, flag := range refFlagsIn(rest) {
				if !refHasFlag(sub, flag) {
					t.Errorf("%s suggests `task %s` with --%s, which it does not have", where, path, flag)
				}
			}
		}
```

6. `cmd/monoagentcli/ref_tasks_gate_test.go`: three rows, and a walk of the command tree that goes down to the leaves, so that a command of `os` without a row, a row that names no command, and a row whose call is not its own all fail. `os install` and `os uninstall` are the operator's, `os status` is open (Ruling 17); the test calls `newTaskTestDB`, whose temporary `HOME` is what `os status` reads on a Mac, so no real Services folder is touched, and the tests of Task 6 pin the order that makes an agent's `os install` answer `operator_only` on every platform. Replace

```go
	row  string   // what the text names: a subcommand, or "add --ready"
```

with

```go
	row  string   // what the text names: a subcommand, "add --ready", or a command of a group ("os install")
```

and replace

```go
	{"digest", false, []string{"digest"}},
}

func TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent(t *testing.T) {
	// The table covers the command tree, both ways.
	rows := map[string]bool{}
	for _, r := range refTaskGate {
		rows[strings.Fields(r.row)[0]] = true
		if r.call[0] != strings.Fields(r.row)[0] {
			t.Errorf("the row %q of refTaskGate calls `task %s`", r.row, r.call[0])
		}
	}
	exists := map[string]bool{}
	for _, sub := range newTaskCmd(&globalConfig{}).Commands() {
		exists[sub.Name()] = true
		if !rows[sub.Name()] {
			t.Errorf("`task %s` has no row in refTaskGate (ref_tasks_gate_test.go): add one that says whether an agent may run it, and say the same in WHO MAY DO WHAT (ref_tasks.go) and in AGENTS.md", sub.Name())
		}
	}
	for name := range rows {
		if !exists[name] {
			t.Errorf("refTaskGate (ref_tasks_gate_test.go) has a row for `task %s`, which is no command: remove or rename it", name)
		}
	}
```

with

```go
	{"digest", false, []string{"digest"}},
	{"os install", true, []string{"os", "install"}},
	{"os status", false, []string{"os", "status"}},
	{"os uninstall", true, []string{"os", "uninstall"}},
}

// refGatePath is the command a row of refTaskGate names: its words that are not flags, so that
// "add --ready" is add and "os install" is install in the group os.
func refGatePath(row string) string {
	var words []string
	for _, w := range strings.Fields(row) {
		if !strings.HasPrefix(w, "-") {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

func TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent(t *testing.T) {
	// The table covers the command tree, both ways: every command that is not a group has a row
	// (a group has the rows of its commands), and a row names a command that is there.
	rows := map[string]bool{}
	for _, r := range refTaskGate {
		path := refGatePath(r.row)
		rows[path] = true
		if !strings.HasPrefix(strings.Join(r.call, " ")+" ", path+" ") {
			t.Errorf("the row %q of refTaskGate calls `task %s`", r.row, strings.Join(r.call, " "))
		}
	}
	commands := refTaskCommands()
	for path, sub := range commands {
		if !sub.HasSubCommands() && !rows[path] {
			t.Errorf("`task %s` has no row in refTaskGate (ref_tasks_gate_test.go): add one that says whether an agent may run it, and say the same in WHO MAY DO WHAT (ref_tasks.go) and in AGENTS.md", path)
		}
	}
	for path := range rows {
		if commands[path] == nil {
			t.Errorf("refTaskGate (ref_tasks_gate_test.go) has a row for `task %s`, which is no command: remove or rename it", path)
		}
	}
```

7. `cmd/monoagentcli/ref_tasks.go`, the `operator_only` sentence of WHO MAY DO WHAT: it names the two commands of `os` that are the operator's, and a sentence of its own says that an agent may run `os status` (the gate test wants each name on one line, `os install` and `os uninstall` in the sentence that carries the code and `os status` outside it). Replace

```
  Only board, edit, move, approve, archive, unarchive and add --ready are the
  operator's: they answer an agent with exit 3 and the code operator_only. board
  shows the Inbox, so an agent uses "task list".
```

with

```
  Only board, edit, move, approve, archive, unarchive, add --ready, os install and
  os uninstall are the operator's: they answer an agent with exit 3 and the code
  operator_only. board shows the Inbox, so an agent uses "task list". os install and
  os uninstall change the Services menu of the user's Mac, outside the board.
  os status only lists those menus: an agent may run it.
```

(The first text is the line `  Only board, edit, move, approve, archive, unarchive and add --ready are the` and the two lines after it. If the sentence was reworded since, amend the one that says the same so that its list holds `os install` and `os uninstall`, add the sentence about `os status`, and report it.) The new text has no backtick, so the raw string of `refTasksText` stays intact.

- [ ] **Step 6: Run the tests to see them pass**

Run: `gofmt -l cmd/monoagentcli` then `go vet ./cmd/monoagentcli/` then `go test ./cmd/monoagentcli/ -run 'TestTaskOS|TestTheMenusCommandLine|TestEveryTaskOSCommand|TestEveryTaskCommandHasAReferenceEntry|TestRefTasks|TestTaskAdd' -count=1` then `GOOS=linux go vet ./cmd/monoagentcli/` then `GOOS=windows go vet ./cmd/monoagentcli/` then `grep -nP '[^\x00-\x7F]' cmd/monoagentcli/task_os.go cmd/monoagentcli/task_os_test.go cmd/monoagentcli/ref_tasks_os.go cmd/monoagentcli/ref_tasks_group_test.go` then `wc -l cmd/monoagentcli/task_os.go cmd/monoagentcli/task_os_test.go cmd/monoagentcli/ref_tasks_test.go cmd/monoagentcli/ref_tasks_gate_test.go cmd/monoagentcli/ref_tasks_group_test.go cmd/monoagentcli/ref_tasks.go`
Expected: no output from `gofmt`, the two cross-OS vets or `grep`, vet clean, PASS, and no file of 500 lines or more (`ref_tasks_test.go` is a few lines shorter than before: Step 5 moved two helpers out of it). `TestEveryTaskCommandHasAReferenceEntry` and the `TestRefTasks...` tests are P1's: they now need the `task os` entries (Step 4), the registered group, and the changes of Step 5 (they pass on this Mac as on a Linux runner: Task 6's `TestTaskOSInstallAndUninstallAreTheOperators` pins the order of the checks that the gate test depends on there).

- [ ] **Step 7: Commit**

```
git add cmd/monoagentcli/task_os.go cmd/monoagentcli/task_os_test.go cmd/monoagentcli/ref_tasks_os.go cmd/monoagentcli/task.go cmd/monoagentcli/ref_tasks.go cmd/monoagentcli/ref_tasks_test.go cmd/monoagentcli/ref_tasks_gate_test.go cmd/monoagentcli/ref_tasks_group_test.go
```
then
```
git commit -m "feat(tasks): task os install, status and uninstall for the macOS Services menu" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Documents

**Files:**
- Modify: `cmd/monoagentcli/ref_tasks.go` (a section of `refTasksText`), `AGENTS.md` (one table row), `SECURITY.md` (one bullet), `CHANGELOG.md` (one bullet), `docs/mastermind/specs/2026-10-05-task-board-design.md` (D7, the operator list of section 7, one bullet at the end of section 12)
- Test: `cmd/monoagentcli/ref_tasks_os_test.go`

**Interfaces:**
- Consumes: P1's `refTasksText`; the surfaces table that ends AGENTS.md's `## Task board` section (header `| Surface | Reaches the board through |`, P1's rows `CLI` and `Session-start hook`); SECURITY.md's `## Task board` bullets; the `Task board, phase 1.` bullet of `CHANGELOG.md`.
- Produces: the documentation of spec 15.2 for this phase. These are shared files: small, append-only edits; a conflict on rebase with another phase's edit keeps both.

- [ ] **Step 1: Write the failing test**

Create `cmd/monoagentcli/ref_tasks_os_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestRefTasksSaysHowToCaptureFromAnyApp(t *testing.T) {
	for _, want := range []string{
		"FROM ANY APP ON A MAC", "task os install", "task os status", "task os uninstall",
		"task os install and task os uninstall are the operator's", "Keyboard Shortcuts, Services, Text",
		"task add --stdin --source os", "xclip", "wl-paste", "Get-Clipboard",
	} {
		if !strings.Contains(refTasksText, want) {
			t.Errorf("`ref tasks` does not mention %q", want)
		}
	}
}
```

Run: `go test ./cmd/monoagentcli/ -run TestRefTasksSaysHowToCaptureFromAnyApp -count=1`
Expected: FAIL (`ref tasks` does not mention "FROM ANY APP ON A MAC", and more).

- [ ] **Step 2: Add the section to `ref tasks`**

In `cmd/monoagentcli/ref_tasks.go`, inside `refTasksText`, insert these lines immediately before the line `TASK TEXT IS DATA` (Edit tool, with that line as the anchor; the inserted text has no backtick, so the raw string stays intact):

```
FROM ANY APP ON A MAC
  monoagentcli --profile Work task os install     # once per profile
  adds "Add to MonoAgent Tasks: Work" to the Services menu: select text in any app,
  right-click it, Services. The text goes to that profile's Inbox as a capture, on
  standard input: monoagentcli --profile <id> task add --stdin --source os. macOS may
  list the item only after it is enabled once in System Settings, Keyboard,
  Keyboard Shortcuts, Services, Text. "task os status" lists the installed menus
  (current, stale, profile gone, or of another database); "task os uninstall"
  removes one.
  task os install and task os uninstall are the operator's: run them in your own
  terminal (inside an agent's session they refuse with operator_only). An agent
  cannot file as the menu either: under an agent-context variable --source os is
  refused.
  Windows and Linux have no such menu; bind a global hotkey to one of these:
    xclip -o -selection primary | monoagentcli --profile <id> task add --stdin --source os
    wl-paste --primary | monoagentcli --profile <id> task add --stdin --source os
    pwsh -c "Get-Clipboard | monoagentcli --profile <id> task add --stdin --source os"
  (the first two read the selection on X11 and on Wayland; on Windows, copy first)

```

Run: `go test ./cmd/monoagentcli/ -run 'TestRefTasks' -count=1`
Expected: PASS (P1's `TestRefTasksNamesTheGateTheLoopAndTheProfile` too).

- [ ] **Step 3: `AGENTS.md`: one row, nothing else**

In the surfaces table that ends the `## Task board` section (header `| Surface | Reaches the board through |`), add this row after the table's last row: P1's last row starts `| Session-start hook |`; if rows of other phases follow it by now, add it after theirs. Add no paragraph anywhere (other phases write next to `## Assistant chat & tools`); the longer text lives in `ref tasks`.

```markdown
| macOS menu | Services, "Add to MonoAgent Tasks: <profile>" on text selected in any app, installed once per profile by `monoagentcli --profile <id> task os install` and removed by `task os uninstall` (both the operator's: an agent is refused with `operator_only`); it runs `task add --stdin --source os`, a capture into Inbox (`monoagentcli ref tasks`) |
```

- [ ] **Step 4: `SECURITY.md`**

In the `## Task board` section, add this bullet right after the bullet that starts `- **Text is cleaned on the way in:**` (not at the end of the list: another phase replaces the last bullet, `- **No HTTP route and no new port.** ...`):

```markdown
- **The macOS menu** (`task os install` and `task os uninstall`, the operator's) hands the selected text to `monoagentcli` on standard input, never on a command line, and files it as a capture: Inbox only. It passes no `--as` and keeps the environment it runs in, so under an agent-context variable the CLI refuses `--source os`; as with the operator guard, this does not stop an agent that deliberately clears its environment or drives the Services menu through the screen, and the human gate still holds. It writes only a bundle it marks as its own, never replaces another profile's menu, and replaces a bundle of the same name it did not write only with `--force`.
```

- [ ] **Step 5: `CHANGELOG.md`**

Find the list with `grep -n "Unreleased" CHANGELOG.md` and add this bullet at the top of the `### Added` list under `## [Unreleased]` (if that heading has no `### Added` any more, add one directly under it):

```markdown
- **Task board: the macOS menu.** `monoagentcli task os install` adds "Add to MonoAgent Tasks: <profile>" to the Services menu of every Mac app: select text, right-click, Services, and it lands in that profile's Inbox (the active profile unless `--profile` names another; run it once per profile, in your own terminal). `task os status` lists the installed menus and whether each is current, and `task os uninstall` removes one. The text reaches `monoagentcli` on standard input, never on a command line. macOS may need the item enabled once in System Settings, Keyboard, Keyboard Shortcuts, Services, Text. On Windows and Linux, `monoagentcli ref tasks` shows a hotkey recipe instead.
```

- [ ] **Step 6: Amend the spec (spec 15.2; the lead's ruling on Ruling 7)**

In `docs/mastermind/specs/2026-10-05-task-board-design.md`, make exactly these three edits with the Edit tool and touch nothing else (each first text occurs once in the spec; if a sentence was reworded since, amend the one that says the same thing and report it):

1. D7: the row ends with the first line below; replace it with the second.

```markdown
not one that deliberately unsets its environment. | lead |
not one that deliberately unsets its environment. `task os install` and `task os uninstall` are operator-only as well (P5): they change the user's machine outside the board. | lead |
```

2. Section 7: the operator list starts with the first line below (P1 put `board` first); replace it with the second.

```markdown
- Operator-only commands (`board`, `edit`, `move`, `approve`, `archive`, `unarchive`, `add --ready`, an operator `comment`) refuse
- Operator-only commands (`board`, `edit`, `move`, `approve`, `archive`, `unarchive`, `add --ready`, an operator `comment`, and `os install` and `os uninstall` (P5)) refuse
```

3. Section 12: add this bullet as its last item, immediately before the line `## 13. Security`:

```markdown
- As built in P5: the script runs under `/bin/sh` and passes every value as `--flag=value`, adding `--db-path=<the database the profile was found in>`; `Info.plist` is that of Apple's plain-text service plus a `CFBundleIdentifier` per profile, an empty `NSRequiredContext` (Apple's Services documentation asks for one in every service) and the marker keys `MonoAgentTasksCLI`, `MonoAgentTasksDB`, `MonoAgentTasksProfileID` and `MonoAgentTasksVersion` (the template's revision, not the CLI release); a menu's identity is its database and profile id; `/` and `:` in the profile's name become `-` in the item and the bundle name; `--dest` works for all three commands; `install` and `uninstall` are the operator's (D7) and refuse an agent before they look at their arguments or the platform, `status` is open to an agent; another profile's menu of the same name is never replaced while that profile exists, not even with `--force`, and another database's only with `--force`; `status` judges a menu with the monoagentcli its marker names, says why it is stale, and lists another database's menu as `other_database`; `uninstall --profile` also takes a deleted profile's id; a failed capture shows the CLI's last error line and fails the action, so macOS shows its own alert too; no `--client-id` (one click is one capture); no doctor check.
```

- [ ] **Step 7: Run the tests and commit**

Run: `gofmt -l cmd/monoagentcli` then `go test ./cmd/monoagentcli/ -run 'TestRefTasks|TestEveryTaskCommandHasAReferenceEntry|TestEveryTaskOSCommand' -count=1`
Expected: no output from `gofmt`, PASS. Do not run a built CLI to read `ref tasks`: the test covers the text.

```
git add cmd/monoagentcli/ref_tasks.go cmd/monoagentcli/ref_tasks_os_test.go AGENTS.md SECURITY.md CHANGELOG.md docs/mastermind/specs/2026-10-05-task-board-design.md
```
then
```
git commit -m "docs(tasks): the macOS menu in ref tasks, AGENTS.md, SECURITY.md, the changelog and the spec" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Verify the whole phase

**Files:**
- Create (outside the repository, in the session scratchpad): `os-smoke.sh`
- No source file changes unless a check below fails and the fix belongs to an earlier task.

**Interfaces:**
- Consumes: everything. Produces: the evidence the PR description quotes.

- [ ] **Step 1: Format, vet and build on every platform this repository ships**

CI runs on Linux only, so a macOS or Windows break would surface only after a merge, which releases. Run, one per call:

```
gofmt -l internal/tasks cmd/monoagentcli
go vet ./internal/tasks/... ./cmd/monoagentcli/
GOOS=darwin go vet ./internal/tasks/... ./cmd/monoagentcli/
GOOS=linux go vet ./internal/tasks/... ./cmd/monoagentcli/
GOOS=windows go vet ./internal/tasks/... ./cmd/monoagentcli/
go vet -tags nosocial ./cmd/monoagentcli/
go build -o /dev/null ./cmd/monoagentcli
go build -o /dev/null -tags nosocial ./cmd/monoagentcli
GOOS=windows go build -o /dev/null ./cmd/monoagentcli
grep -rnP '[^\x00-\x7F]' internal/tasks/osmenu cmd/monoagentcli/task_os.go cmd/monoagentcli/task_os_test.go cmd/monoagentcli/ref_tasks_os.go cmd/monoagentcli/ref_tasks_os_test.go
```
Expected: no output from any of them (never a bare `go build ./cmd/monoagentcli`: it overwrites the repository's untracked `monoagentcli` binary).

- [ ] **Step 2: Run the two touched packages in full**

Run, one per call:

```
go test ./internal/tasks/osmenu/ -race -count=1
MONOAGENT_AUTOMATOR_TEST=1 go test ./internal/tasks/osmenu/ -run TestAutomatorRunsTheWorkflow -count=1 -v
go test ./cmd/monoagentcli/ -race -run 'TestTaskOS|TestTheMenusCommandLine|TestEveryTaskOSCommand|TestRefTasks' -count=1
go test ./cmd/monoagentcli/ -count=1 -timeout 20m
```
Expected: the first three PASS (the third runs the new CLI tests, which swap package variables, under the race detector). The fourth may fail only on tests that already fail on a pristine macOS tree (`TestCaptureTaskFilesOnTheBoard`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCoderConversationFolders`, and the load flake `TestAgentTestGoDeadline`, which passes when rerun alone). Any other failure, above all a `TestTaskOS`, `TestTask` or `TestRef` test, is yours: fix it in the task that owns the code.

- [ ] **Step 3: Mutation checks**

For each row: apply the change with the Edit tool, run the test named (`go test ./internal/tasks/osmenu/ -run '<pattern>' -count=1` unless the row names `./cmd/monoagentcli/`; read the table as raw text: a `|` inside backticks is part of the code or of the `-run` pattern), see it FAIL, undo the change with the Edit tool, see it PASS again. A mutation that does not fail its test means a rule has no proof: sharpen the test in the task that owns the code before going on.

| # | File and change | Test that must fail |
|---|---|---|
| 1 | `templates/action.sh.tmpl`: `--source os` becomes `--source cli` | `TestTheScriptHandsTheTextOverOnStandardInputOnly` |
| 2 | `names.go`, `shellQuote`: the body becomes `return "'" + s + "'"` | `TestShellQuoteRoundTrips|TestTheScriptHandsTheTextOver` |
| 3 | `names.go`, `plistEscape`: delete the pair `"&", "&amp;",` | `TestPlistEscapeKeepsTextAsText|TestHostileProfileNamesStayText` |
| 4 | `names.go`, `cleanName`: `case unicode.IsControl(r), hidden(r), r == 0xFFFE, r == 0xFFFF:` becomes `case unicode.IsControl(r), r == 0xFFFE, r == 0xFFFF:` | `TestMenuNameCleansTheProfileName` |
| 5 | `templates/action.sh.tmpl`: delete the `cat >/dev/null 2>&1` line inside `fail()` (the line right after `fail() {`; the same text occurs again below) | `TestTheScriptReadsTheWholeSelection` |
| 6 | `templates/action.sh.tmpl`, `fail()`: `exit "$2"` becomes `exit 0` | `TestARefusalIsShownAndPassedOn|TestAMissingCLIIsShownNotRun` |
| 7 | `install.go`, `Install`: delete the `case ok:` refusal (the case line and its return) | `TestInstallNeverReplacesAnotherProfilesMenu` |
| 8 | `install.go`, `Install`: `case !force:` becomes `case false:` | `TestInstallKeepsWhatItDidNotWrite` |
| 9 | `install.go`, `Install`: delete the `os.SameFile` check and its `continue` | `TestInstallFollowsARenameInCaseOnly` (fails on this Mac's case-insensitive disk; on Linux it passes, say so) |
| 10 | `install.go`, `Remove`: `if m.ProfileID != profileID || m.DB != db {` becomes `if m.DB != db {` | `TestRemoveTakesOnlyThatProfilesMenus` |
| 11 | `install.go`, `topLevelStrings`: `if depth != 3 {` becomes `if depth < 3 {` | `TestListFindsOnlyManagedBundles` |
| 12 | `task_os.go`, install: `if services && res.Outcome != osmenu.Unchanged {` becomes `if res.Outcome != osmenu.Unchanged {` | `./cmd/monoagentcli/` `TestTaskOSRefreshesTheRealServicesFolderOnly` |
| 13 | `task_os.go`, `taskOSOnly`: `if taskOSGOOS == "darwin" {` becomes `if true {` | `./cmd/monoagentcli/` `TestTaskOSRefusesAwayFromMacOS` |
| 14 | `task_os.go`, uninstall: delete the `callerFor(flagAs(cmd)).operator(...)` check | `./cmd/monoagentcli/` `TestTaskOSInstallAndUninstallAreTheOperators` |
| 15 | `task_os.go`, uninstall: delete the line `id = cfg.ProfileID // a deleted profile's id, as status prints it` | `./cmd/monoagentcli/` `TestTaskOSUninstallFindsADeletedProfilesMenuByItsID` |
| 16 | `templates/action.sh.tmpl`: delete the `cat >/dev/null 2>&1` line in the success branch (the line right after `if [ "$code" -eq 0 ]; then`) | `TestTheScriptReadsTheWholeSelection` (its "adds after 1 MiB" case) |
| 17 | `names.go`, `cleanName`: `case unicode.IsControl(r), hidden(r), r == 0xFFFE, r == 0xFFFF:` becomes `case unicode.IsControl(r), hidden(r):` | `TestMenuNameCleansTheProfileName` |
| 18 | `bundle.go`, `render`: delete the `for` loop that calls `wellFormed` | `TestRenderRefusesValuesABundleCannotCarry` |
| 19 | `install.go`, `Remove`: delete the loop that removes `tmpPrefix` folders | `TestRemoveTakesOnlyThatProfilesMenus` |
| 20 | `install.go`, `write`: delete the line `_ = os.RemoveAll(aside) // ...` | `TestInstallRewritesAStaleMenu` |
| 21 | `install.go`, `write`: delete the line `_ = rename(aside, target) // the old menu goes back` | `TestInstallPutsTheOldMenuBackWhenTheNewOneCannotGoIn` |
| 22 | `install.go`, `Install`: delete the case `case ok && m.DB != b.DBPath:` and its body | `TestInstallKeepsAnotherDatabasesMenu` |
| 23 | `install.go`, `Remove`: `if m.ProfileID != profileID || m.DB != db {` becomes `if m.ProfileID != profileID {` | `TestInstallKeepsAnotherDatabasesMenu` |
| 24 | `task_os.go`, install: delete the three lines `if len(args) != 0 {` ... `}` that return `task os install takes no arguments` | `./cmd/monoagentcli/` `TestTaskOSCommandsTakeNoArguments` |
| 25 | `task_os.go`, install: move the block `if err := taskOSOnly(); err != nil {` ... `}` to the first lines of `RunE`, above the operator check | `./cmd/monoagentcli/` `TestTaskOSInstallAndUninstallAreTheOperators` |
| 26 | `ref_tasks.go`, WHO MAY DO WHAT: delete the line `  os status only lists those menus: an agent may run it.` | `./cmd/monoagentcli/` `TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent` |
| 27 | `ref_tasks_group_test.go`, `refHasFlag`: delete `|| sub.PersistentFlags().Lookup(name) != nil` | `./cmd/monoagentcli/` `TestRefTasksSuggestsOnlyCommandsTheCLIHas` |
| 28 | `ref_tasks_group_test.go`, `refTaskCallee`: `for sub.HasSubCommands() {` becomes `for false {` | `./cmd/monoagentcli/` `TestRefTasksEntriesDescribeTheirOwnCommand|TestRefTasksSuggestsOnlyCommandsTheCLIHas` |

- [ ] **Step 4: Smoke-run the built CLI under a throwaway home**

Never run a built `monoagentcli` against the real `HOME`, never without `--dest`, never with `pbs -update`. Write `os-smoke.sh` into the session scratchpad with the Write tool:

```bash
#!/bin/bash
# Builds the CLI (real Go caches) and drives task os and the menu's own script
# under a throwaway HOME, an empty environment and a throwaway Services folder.
set -e
SCRATCH="$(cd "$(dirname "$0")" && pwd)"
TMP="$SCRATCH/os-smoke-$$"
mkdir -p "$TMP/home"
go build -o "$TMP/monoagentcli" ./cmd/monoagentcli
E=(env -i "HOME=$TMP/home" "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
M=("${E[@]}" "$TMP/monoagentcli" --db-path "$TMP/smoke.db")
SVC="$TMP/Services"
B="$SVC/Add to MonoAgent Tasks (Smoke Work).workflow"
echo "--- a profile and its menu"
"${M[@]}" profile create "Smoke Work" >/dev/null
"${M[@]}" --profile "Smoke Work" task os install --dest "$SVC"
echo "--- Apple's tools read the bundle"
plutil -lint "$B/Contents/Info.plist" "$B/Contents/document.wflow"
/System/Library/CoreServices/pbs -read_bundle "$B" 2>&1 | grep -E 'default =|NSMessage|utf8'
echo "--- the menu's script as Automator runs it, notifications off (osascript replaced by true)"
plutil -extract actions.0.action.ActionParameters.COMMAND_STRING raw -o - "$B/Contents/document.wflow" | sed 's#^osa=.*#osa=/usr/bin/true#' > "$TMP/action.sh"
printf 'Smoke selection $(touch pwned)\nsecond line' | (cd "$TMP" && "${E[@]}" /bin/sh "$TMP/action.sh") && echo "script exit 0"
test ! -e "$TMP/pwned" && echo "no command ran from the text"
"${M[@]}" --profile "Smoke Work" --json task list
echo "--- under an agent's marker the capture is refused (exit 3 expected)"
printf 'from an agent' | "${E[@]}" CLAUDECODE=1 /bin/sh "$TMP/action.sh" || echo "refused, exit $?"
echo "--- status, an unknown --profile reaching uninstall unresolved, install again, uninstall, status"
"${M[@]}" task os status --dest "$SVC"
"${M[@]}" --profile no-such-id task os uninstall --dest "$SVC" && echo "unknown profile: exit 0"
"${M[@]}" --profile "Smoke Work" task os install --dest "$SVC"
"${M[@]}" --profile "Smoke Work" task os uninstall --dest "$SVC"
"${M[@]}" task os status --dest "$SVC"
echo "--- leftovers are in $TMP"
rm -f "$TMP/monoagentcli"
```

Run it with `bash <path to os-smoke.sh>` from the repository root.
Expected: install prints `Installed "Add to MonoAgent Tasks: Smoke Work"` with the hints; `plutil` prints two `OK` lines; `pbs` prints the menu item, `runWorkflowAsService` and `public.utf8-plain-text`; the script exits 0 and nothing ran from the text; `task list --json` shows a task titled `Smoke selection $(touch pwned)` in `inbox` with source kind `os`, the second line in its notes; the marker run prints `Not added to MonoAgent Tasks: ...` with `source "os" is for captures` and `refused, exit 3`; status shows the menu `current`; the uninstall with `--profile no-such-id` prints `nothing to remove` and `unknown profile: exit 0` (an exit 3 there means something resolves `--profile` before the command runs, and a deleted profile's menu could not be removed: stop and tell the lead); the second install says `Already installed`; uninstall prints `Removed ...`; the last status says no menu is installed. Nothing appears under the real `~/Library/Services` or `~/.claude`.

- [ ] **Step 5: Hand over**

Do not push, open a PR or merge: the lead does that after the independent reviews, and re-checks before opening the PR that `062_tasks.sql` is still the only migration 062 anywhere (spec D32). Report: the commits (`git log --oneline feat/tasks-board..HEAD`), the results of Steps 1 and 2 (with the automator log line), which of the twenty-eight mutations failed their tests, the smoke output, and anything that behaved differently from this plan or the spec. List for the PR description what only the user's Mac can verify (spec 14, 17.6), after running `monoagentcli task os install` in their own terminal (inside Claude Code, even with `!`, it refuses: Ruling 7): the item appears under Services on right-clicking selected text, with or without enabling it in System Settings; choosing it files the selection into the profile's Inbox with the right app name; the notification "Added to Inbox in <profile>" appears (it may need notifications for Script Editor allowed; with notifications off a successful capture shows nothing at all); after the profile is deleted or monoagentcli moved, choosing it shows "Not added: ..." and macOS's alert; a very large selection (over 1 MiB) ends cleanly. Say also what this build could not show: it was checked on macOS 27 (Darwin 27) only; the workflow runs `/bin/sh` where Apple's Show Map uses `/bin/bash`; the bundle keeps `Contents/document.wflow`, the layout of Automator-saved and third-party Quick Actions, not the `Contents/Resources/` of Apple's own; `automator -i` ran the workflow, which is not the path Services takes.

---

## Self-review (done by the plan's author)

- **Spec coverage.** Section 12: the command group and its flags (Task 6), the bundle and its files, the menu item, the message, the markers (Task 3), the script and its notification, the text on standard input only, the deleted-profile case (Task 2), idempotent install, stale rewrite, `--force`, `status`, `uninstall`, the printed hints (Tasks 4 and 6), Windows and Linux (Task 6's refusal, Task 7's recipe), rendering and paths in untagged files tested on Linux, darwin-only `pbs` and `automator` (Tasks 1 to 5). Section 5.1: a capture lands in Inbox only, `--source os` is refused for an agent (Task 6's pin, the smoke run). Section 5.3: no client id, a Ruling. Section 13: the text never on a command line, no log of it (Task 2). Section 14: rendering tests on any OS, an `automator` run against a stub CLI in a temporary folder (Task 5). Section 15.2: `ref tasks`, `ref commands`, AGENTS.md (one surfaces row), SECURITY.md, CHANGELOG, the spec amended in D7, 7 and 12 (Tasks 6 and 7). Not built: the optional doctor check (Ruling 12).
- **Spec deviations.** Every Ruling above; Task 7's spec edits record the ones a reader of D7, 7 and 12 would otherwise miss.
- **Placeholders.** None: every code step holds its code. Task 5 Step 4 is a stop rule, not a branch.
- **Type consistency.** `osmenu`: `MenuPrefix`, `maxNameRunes`, `hidden`, `cleanName`, `menuName`, `MenuTitle`, `BundleName`, `bundleID`, `shellQuote`, `plistEscape`, `plainValue`, `osascript`, `funcs`, `scriptView`, `renderScript`, `Version`, `KeyCLI`, `KeyDB`, `KeyProfileID`, `KeyVersion`, `InfoPath`, `DocumentPath`, `Spec`, `Bundle`, `Render`, `render`, `wellFormed`, `ErrTaken`, `Outcome` (`Created`, `Unchanged`, `Updated`), `Result`, `Menu`, `Install(dir, b, force, gone)`, `List`, `Remove`, `Matches`, `tmpPrefix`, `rename`, `write`, `readMarker`, `topLevelStrings`; test helpers `rig`, `newRig`, `exitStatus`, `lines`, `hostileText`, `parsePlist`, `dig`, `testDB`, `testSpec`, `hostileName`, `hostileClean`, `cliPath`, `noneGone`, `mustRender`, `writeFile`. CLI: `newTaskOSCmd`, `taskOSGOOS`, `taskOSExecutable`, `refreshServices`, `taskOSOnly`, `taskOSDir`, `taskOSDBPath`, `taskOSMenuSpec`, `taskOSRunnable`, `withTaskStore`, `taskOSState`, `taskOSMenuJSON`, `printTaskOSInstalled(w, p, res, cli)`, `printTaskOSStatus`; test helpers `newTaskOSTest`, `taskOSStubCLI`, `execTaskOSSQL`, `addTaskOSProfile`, `countTaskOSTasks`, `taskOSInstalled`; in P1's tests (Task 6 Step 5) `refTaskSub` and `refHasFlag` changed and moved to `ref_tasks_group_test.go`, `refTaskCommands` and `refTaskCallee` new there, `refGatePath` new in `ref_tasks_gate_test.go`, the used names `refTaskCall`, `refTaskEntries`, `refFlagsIn`, `refSection`, `refNames` and `refTaskGate` as P1 has them.
- **Review Focus.** Each of the five has named tests in the task that owns the code.
