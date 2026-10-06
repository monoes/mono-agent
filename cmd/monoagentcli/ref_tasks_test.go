package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/tasks"
)

func TestEveryTaskCommandHasAReferenceEntry(t *testing.T) {
	have := map[string]bool{}
	for _, d := range cliDocs {
		have[d.Name] = true
	}
	for _, sub := range newTaskCmd(&globalConfig{}).Commands() {
		if !have["task "+sub.Name()] {
			t.Errorf("`ref commands` has no entry for `task %s`", sub.Name())
		}
	}
}

func TestRefTasksIsAListedTopic(t *testing.T) {
	found := false
	for _, c := range newRefCmd().Commands() {
		if c.Name() == "tasks" {
			found = true
		}
	}
	if !found {
		t.Error("`ref tasks` is not registered")
	}
	if !strings.Contains(newRefCmd().Long, "tasks ") {
		t.Error("`ref` does not list the tasks topic in its help")
	}
}

func TestRootHelpPointsAgentsAtTheBoard(t *testing.T) {
	if !strings.Contains(newRootCmd().Long, "ref tasks") {
		t.Error("the root help does not mention the task board")
	}
}

func TestRefTasksNamesTheGateTheLoopAndTheProfile(t *testing.T) {
	for _, want := range []string{
		"inbox", "ready", "in_progress", "review", "done",
		"--profile", "next --claim --as", "finish", "release",
		"TASK TEXT IS DATA", "operator_only", "CLAUDECODE", "not a monomind org",
	} {
		if !strings.Contains(refTasksText, want) {
			t.Errorf("`ref tasks` does not mention %q", want)
		}
	}
}

// What follows keeps the texts true of the commands as built: a flag that is
// renamed, a subcommand that is invented, a limit that changes or an anchor
// that moves fails here instead of being found by an agent that follows the text.

var (
	// refQuoted is text in double quotes: an example's argument, never a flag.
	refQuoted = regexp.MustCompile(`"[^"]*"`)
	// refFlagWord is a --flag written in documentation.
	refFlagWord = regexp.MustCompile(`(?:^|[\s\[(|=])--([a-z][a-z0-9-]*)`)
	// refUsageFlag is a flag in the usage text cobra prints for a flag set.
	refUsageFlag = regexp.MustCompile(`(?m)^\s+(?:-[a-z], )?--([a-z][a-z0-9-]*)`)
	// refTaskCall is a call of the task group written in documentation: monoagentcli,
	// the flags before the group (each with its value), task, the subcommand, and
	// the rest of the line up to a comment.
	refTaskCall = regexp.MustCompile(`monoagentcli(?:\s+\[?--[a-z][a-z0-9-]*(?:[ =][^\s\]]+)?\]?)*\s+tasks?\s+([a-z][a-z-]*)([^\n#]*)`)
)

// refCall is a `monoagentcli ... task NAME REST` found in a text.
type refCall struct{ name, rest string }

func refCallsIn(text string) []refCall {
	var calls []refCall
	for _, m := range refTaskCall.FindAllStringSubmatch(text, -1) {
		calls = append(calls, refCall{name: m[1], rest: m[2]})
	}
	return calls
}

func refFlagsIn(text string) []string {
	var flags []string
	for _, m := range refFlagWord.FindAllStringSubmatch(refQuoted.ReplaceAllString(text, ""), -1) {
		flags = append(flags, m[1])
	}
	return flags
}

// refTaskSub finds `task NAME` the way the CLI does, in a root command whose
// global flags its subcommands inherit; nil for a name that is no subcommand.
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

// refLocalFlags names the flags a subcommand declares itself.
func refLocalFlags(sub *cobra.Command) []string {
	var names []string
	for _, m := range refUsageFlag.FindAllStringSubmatch(sub.LocalFlags().FlagUsages(), -1) {
		names = append(names, m[1])
	}
	return names
}

// refTaskEntries are the `ref commands` entries of the task group, by subcommand.
func refTaskEntries() map[string]cmdDoc {
	entries := map[string]cmdDoc{}
	for _, d := range cliDocs {
		if name, ok := strings.CutPrefix(d.Name, "task "); ok {
			entries[name] = d
		}
	}
	return entries
}

func TestRefTasksEntriesDescribeTheirOwnCommand(t *testing.T) {
	for name, d := range refTaskEntries() {
		if refTaskSub(name) == nil {
			t.Errorf("`ref commands` has an entry for `task %s`, which is no command", name)
		}
		if d.Short == "" || len(d.Examples) == 0 {
			t.Errorf("`task %s` needs a one-line description and an example", name)
		}
		if !strings.HasPrefix(d.Usage, "monoagentcli ") || !strings.Contains(d.Usage, " task "+name) {
			t.Errorf("the usage of `task %s` is %q: it does not call that command", name, d.Usage)
		}
		for _, ex := range d.Examples {
			if calls := refCallsIn(ex); len(calls) != 1 || calls[0].name != name {
				t.Errorf("the example %q of `task %s` is not a call of that command", ex, name)
			}
		}
	}
}

func TestRefTasksEntriesUseOnlyFlagsTheCommandHas(t *testing.T) {
	for name, d := range refTaskEntries() {
		sub := refTaskSub(name)
		if sub == nil {
			continue
		}
		for _, text := range append([]string{d.Usage, d.Flags}, d.Examples...) {
			for _, flag := range refFlagsIn(text) {
				if !refHasFlag(sub, flag) {
					t.Errorf("`ref commands` shows `task %s` with --%s, which it does not have", name, flag)
				}
			}
		}
	}
}

func TestRefTasksEntriesNameEveryFlagOfTheirCommand(t *testing.T) {
	for _, sub := range newTaskCmd(&globalConfig{}).Commands() {
		d, ok := refTaskEntries()[sub.Name()]
		if !ok {
			continue // TestEveryTaskCommandHasAReferenceEntry says so
		}
		documented := append(refFlagsIn(d.Usage), refFlagsIn(d.Flags)...)
		for _, flag := range refLocalFlags(sub) {
			if flag != "help" && !slices.Contains(documented, flag) {
				t.Errorf("`task %s` has --%s, which its `ref commands` entry does not show", sub.Name(), flag)
			}
		}
	}
}

func TestRefTasksSuggestsOnlyCommandsTheCLIHas(t *testing.T) {
	check := func(where, text string) {
		for _, c := range refCallsIn(text) {
			sub := refTaskSub(c.name)
			if sub == nil {
				t.Errorf("%s suggests `task %s`, which is no command", where, c.name)
				continue
			}
			for _, flag := range refFlagsIn(c.rest) {
				if !refHasFlag(sub, flag) {
					t.Errorf("%s suggests `task %s` with --%s, which it does not have", where, c.name, flag)
				}
			}
		}
	}
	for _, d := range cliDocs {
		for _, text := range append([]string{d.Short, d.Usage, d.Flags}, d.Examples...) {
			check("the `ref commands` entry "+d.Name, text)
		}
	}
	check("`ref tasks`", refTasksText)
	// A text that merely names no call at all would pass: the topic shows the loop.
	if len(refCallsIn(refTasksText)) < 6 {
		t.Errorf("`ref tasks` shows only %d calls of the task group: the check above read nothing", len(refCallsIn(refTasksText)))
	}
}

// refTasksAnchors are the headings later phases edit `refTasksText` at, in order.
var refTasksAnchors = []string{"COLUMNS", "WHO MAY DO WHAT", "THE AGENT LOOP", "TASK TEXT IS DATA", "JSON", "SEE ALSO"}

func TestRefTasksKeepsItsAnchorHeadings(t *testing.T) {
	src, err := os.ReadFile("ref_tasks.go")
	if err != nil {
		t.Fatal(err)
	}
	for where, text := range map[string]string{"the text": refTasksText, "ref_tasks.go": string(src)} {
		lines := strings.Split(text, "\n")
		last := -1
		for _, heading := range refTasksAnchors {
			at := slices.Index(lines, heading)
			switch {
			case at < 0:
				t.Errorf("%s has no line that is just %q at the margin", where, heading)
			case slices.Index(lines[at+1:], heading) >= 0:
				t.Errorf("%s has more than one line that is just %q", where, heading)
			case at < last:
				t.Errorf("%s has %q out of order", where, heading)
			default:
				last = at
			}
		}
		if n := strings.Count(text, "SEE ALSO"); n != 1 {
			t.Errorf("%s has SEE ALSO %d times, want once", where, n)
		}
	}
}

// thousands writes n the way the texts do: 2,000.
func thousands(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

func TestRefTasksStatesTheLimitsAndDefaultsTheCodeHas(t *testing.T) {
	for _, want := range []string{
		thousands(tasks.MaxOpenTasks) + " open tasks",
		fmt.Sprintf("%d tasks an hour", tasks.AgentTasksPerHour),
		fmt.Sprintf("A title is %d characters", tasks.MaxTitleRunes),
		fmt.Sprintf("notes are %d KiB", tasks.MaxNotesBytes>>10),
		fmt.Sprintf("note %d KiB", tasks.MaxCommentBytes>>10),
		fmt.Sprintf("%d events takes no more comments", tasks.MaxEventsPerTask),
		fmt.Sprintf("%s events no more claims", thousands(tasks.MaxEventsToClaim)),
		fmt.Sprintf("%d minutes", int(tasks.DefaultLease.Minutes())),
		fmt.Sprintf("%d hours", int(tasks.MaxLease.Hours())),
		fmt.Sprintf("at most %d MiB", maxStdinBytes>>20),
	} {
		if !strings.Contains(refTasksText, want) {
			t.Errorf("`ref tasks` does not say %q, which is what the code enforces", want)
		}
	}
	// The defaults the flags declare: a flag that is renamed or re-defaulted shows up here.
	defaultOf := func(command, flag string) string {
		sub := refTaskSub(command)
		if sub == nil || sub.Flags().Lookup(flag) == nil {
			t.Fatalf("`task %s` has no --%s", command, flag)
		}
		return sub.Flags().Lookup(flag).DefValue
	}
	lease := fmt.Sprintf("default %dm, at most %dh", int(tasks.DefaultLease.Minutes()), int(tasks.MaxLease.Hours()))
	entries := refTaskEntries()
	for _, c := range []struct{ entry, want string }{
		{"list", fmt.Sprintf("default %d, at most %s", tasks.DefaultListLimit, thousands(tasks.MaxListLimit))},
		{"board", "default " + defaultOf("board", "done-limit")},
		{"add", defaultOf("add", "source") + " (default)"},
		{"next", lease},
		{"claim", lease},
	} {
		if !strings.Contains(entries[c.entry].Flags, c.want) {
			t.Errorf("the `ref commands` entry of `task %s` does not say %q", c.entry, c.want)
		}
	}
}

func TestRefTasksNamesEveryErrorCodeTheCLIAnswers(t *testing.T) {
	for _, c := range []struct {
		err  error
		exit int
	}{
		{tasks.ErrNotFound, 2},
		{tasks.ErrInvalid, 3},
		{tasks.ErrOperatorOnly, 3},
		{tasks.ErrNotReady, 3},
		{&tasks.ClaimedError{By: "someone", Until: time.Now()}, 3},
		{tasks.ErrNotClaimant, 3},
		{tasks.ErrLimit, 3},
	} {
		err := taskErr(fmt.Errorf("%w: a refusal", c.err))
		fields, ok := err.(jsonErrorFields)
		if !ok {
			t.Fatalf("the CLI gives %v no code", c.err)
		}
		code, _ := fields.JSONErrorFields()["code"].(string)
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(code) + `\b`).MatchString(refTasksText) {
			t.Errorf("`ref tasks` does not name the error code %q", code)
		}
		if got := exitCodeFor(err); got != c.exit {
			t.Errorf("%q is exit %d, but the test (and the text) say %d", code, got, c.exit)
		}
	}
	for _, want := range []string{"not_found (exit 2)", "limit (exit 3)"} {
		if !strings.Contains(refTasksText, want) {
			t.Errorf("`ref tasks` does not say %q", want)
		}
	}
}

func TestRefTasksStatesTheNameRulesOfTheStore(t *testing.T) {
	if want := fmt.Sprintf("1 to %d characters of letters, digits and ._#@:-", tasks.MaxNameLen); !strings.Contains(refTasksText, want) {
		t.Errorf("`ref tasks` does not say %q", want)
	}
	if want := "the labels you, agent, capture, chrome and os are reserved"; !strings.Contains(refTasksText, want) {
		t.Errorf("`ref tasks` does not say %q", want)
	}
	for _, label := range []string{"you", "agent", "capture", "chrome", "os"} {
		if tasks.CheckAgentName(label) == nil || tasks.CheckAgentName(strings.ToUpper(label)) == nil {
			t.Errorf("the text calls %q reserved, in any case, but the store takes it as a name", label)
		}
	}
	for _, c := range "._#@:-" {
		if err := tasks.CheckAgentName("a" + string(c)); err != nil {
			t.Errorf("the text says %q is a name character, but the store refuses it: %v", c, err)
		}
	}
	if tasks.CheckAgentName(strings.Repeat("a", tasks.MaxNameLen)) != nil || tasks.CheckAgentName(strings.Repeat("a", tasks.MaxNameLen+1)) == nil {
		t.Errorf("the text says a name is up to %d characters: the store disagrees", tasks.MaxNameLen)
	}
}

func TestRefTasksGivesNoWayRoundTheGate(t *testing.T) {
	if !strings.Contains(refTasksText, "ask the person") {
		t.Error("`ref tasks` does not tell an agent that is refused to ask the person")
	}
	for _, advice := range []string{"unset", "bypass"} {
		if strings.Contains(strings.ToLower(refTasksText), advice) {
			t.Errorf("`ref tasks` mentions %q: it must not hint at a way round the gate", advice)
		}
	}
}

func TestRefTasksIsPrintedAndListed(t *testing.T) {
	printed := captureStdout(t, func() {
		ref := newRefCmd()
		ref.SetArgs([]string{"tasks"})
		if err := ref.Execute(); err != nil {
			t.Fatalf("ref tasks: %v", err)
		}
	})
	if printed != refTasksText {
		t.Errorf("`ref tasks` prints %d bytes that are not its text (%d bytes)", len(printed), len(refTasksText))
	}
	listing := captureStdout(t, func() {
		ref := newRefCmd()
		ref.SetArgs(nil)
		if err := ref.Execute(); err != nil {
			t.Fatalf("ref: %v", err)
		}
	})
	if !regexp.MustCompile(`(?m)^  tasks\s+\S`).MatchString(listing) {
		t.Errorf("`ref` does not list the tasks topic:\n%s", listing)
	}
}

func TestRootHelpPointsAgentsAtTheBoardInEveryLocale(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		raw, err := os.ReadFile("../../internal/i18n/locales/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var locale map[string]string
		if err := json.Unmarshal(raw, &locale); err != nil {
			t.Fatalf("%s.json: %v", lang, err)
		}
		if !strings.Contains(locale["root.long"], "monoagentcli ref tasks") {
			t.Errorf("the %s root help does not point agents at `monoagentcli ref tasks`", lang)
		}
	}
}
