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

// refSection is what `ref tasks` says under one of its anchor headings, up to the next one.
func refSection(heading string) string {
	lines := strings.Split(refTasksText, "\n")
	start := slices.Index(lines, heading)
	if start < 0 {
		return ""
	}
	end := len(lines)
	if i := slices.Index(refTasksAnchors, heading); i >= 0 && i+1 < len(refTasksAnchors) {
		if next := slices.Index(lines, refTasksAnchors[i+1]); next > start {
			end = next
		}
	}
	return strings.Join(lines[start+1:end], "\n")
}

// refTasksDocs is every text about the board: the topic and the `ref commands` entries.
func refTasksDocs() string {
	var b strings.Builder
	b.WriteString(refTasksText)
	for _, d := range refTaskEntries() {
		b.WriteString("\n" + d.Short + "\n" + d.Usage + "\n" + d.Flags + "\n" + strings.Join(d.Examples, "\n"))
	}
	return b.String()
}

// refStates checks the statements of one number in the texts against the code: the
// pattern has one group, the number as written, and every statement of it must be want.
// A number that is no longer stated anywhere fails too.
func refStates(t *testing.T, what, pattern, want string) {
	t.Helper()
	found := regexp.MustCompile(pattern).FindAllStringSubmatch(refTasksDocs(), -1)
	if len(found) == 0 {
		t.Errorf("no text states %s (%s), which is %s in the code", what, pattern, want)
	}
	for _, m := range found {
		if m[1] != want {
			t.Errorf("a text says %q for %s, which is %s in the code", m[0], what, want)
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
	refStates(t, "a profile's open tasks", `([\d,]+) open tasks`, thousands(tasks.MaxOpenTasks))
	refStates(t, "the tasks an agent may add an hour", `(\d+) tasks an hour`, fmt.Sprint(tasks.AgentTasksPerHour))
	refStates(t, "a title", `title is (\d+) characters`, fmt.Sprint(tasks.MaxTitleRunes))
	refStates(t, "notes", `notes are (\d+) KiB`, fmt.Sprint(tasks.MaxNotesBytes>>10))
	refStates(t, "a comment", `note (\d+) KiB`, fmt.Sprint(tasks.MaxCommentBytes>>10))
	refStates(t, "the events that stop comments", `(\d+) events takes no more comments`, fmt.Sprint(tasks.MaxEventsPerTask))
	refStates(t, "the events that stop claims", `([\d,]+) events no more claims`, thousands(tasks.MaxEventsToClaim))
	refStates(t, "a lease", `(\d+) minutes`, fmt.Sprint(int(tasks.DefaultLease.Minutes())))
	refStates(t, "the longest lease", `(\d+) hours`, fmt.Sprint(int(tasks.MaxLease.Hours())))
	refStates(t, "what --stdin reads", `at most (\d+) MiB`, fmt.Sprint(maxStdinBytes>>20))
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
	jsonText := refSection("JSON")
	if jsonText == "" {
		t.Fatal("`ref tasks` has no JSON section")
	}
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
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(code) + `\b`).MatchString(jsonText) {
			t.Errorf("the JSON section of `ref tasks` does not name the error code %q", code)
		}
		if got := exitCodeFor(err); got != c.exit {
			t.Errorf("%q is exit %d, but the test (and the text) say %d", code, got, c.exit)
		}
	}
	for _, want := range []string{"not_found (exit 2)", "limit (exit 3)"} {
		if !strings.Contains(jsonText, want) {
			t.Errorf("the JSON section of `ref tasks` does not say %q", want)
		}
	}
}

func TestRefTasksStatesTheNameRulesOfTheStore(t *testing.T) {
	who := refSection("WHO MAY DO WHAT")
	refStates(t, "the length of an agent's name", `1 to (\d+) characters of letters`, fmt.Sprint(tasks.MaxNameLen))
	for _, want := range []string{"characters of letters, digits and ._#@:-", "the labels you, agent, capture, chrome and os are reserved"} {
		if !strings.Contains(who, want) {
			t.Errorf("the WHO MAY DO WHAT section of `ref tasks` does not say %q", want)
		}
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

func TestRefTasksSaysWhichCommandsTheGateRefusesAnAgent(t *testing.T) {
	who := refSection("WHO MAY DO WHAT")
	operator := regexp.MustCompile(`The\s+operator\s+may\s+([^.]*)\.`).FindStringSubmatch(who)
	agent := regexp.MustCompile(`An\s+AI\s+agent\s+may\s+([^.]*)\.`).FindStringSubmatch(who)
	if operator == nil || agent == nil {
		t.Fatal("WHO MAY DO WHAT has lost its sentences 'The operator may ...' and 'An AI agent may ...'")
	}
	db := newTaskTestDB(t)
	// The code an agent's call is refused with, "" when it is not refused.
	refusal := func(args ...string) string {
		out, _, err := runTask(t, db, "default", true, "", append(args, "--as", "bot")...)
		if err == nil {
			return ""
		}
		var doc struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(out), &doc)
		return doc.Code
	}
	for _, c := range [][]string{{"board"}, {"edit", "1", "--title", "x"}, {"move", "1", "ready"}, {"approve", "1"}, {"archive", "1"}, {"unarchive", "1"}, {"add", "x", "--ready"}} {
		if got := refusal(c...); got != "operator_only" {
			t.Errorf("`task %s` run by an agent answers %q, but `ref tasks` gives it to the operator", c[0], got)
		}
		if !strings.Contains(operator[1], c[0]) {
			t.Errorf("the operator's sentence of `ref tasks` does not name %q", c[0])
		}
		if c[0] != "add" && strings.Contains(agent[1], c[0]) {
			t.Errorf("the agent's sentence of `ref tasks` names %q, which an agent may not run", c[0])
		}
	}
	for _, c := range [][]string{{"list"}, {"show", "1"}, {"next"}, {"add", "x"}, {"claim", "1"}, {"comment", "1", "x"}, {"finish", "1", "--result", "x"}, {"release", "1"}} {
		if got := refusal(c...); got == "operator_only" {
			t.Errorf("`task %s` run by an agent is refused as operator_only, but `ref tasks` gives it to agents", c[0])
		}
		if !strings.Contains(agent[1], c[0]) {
			t.Errorf("the agent's sentence of `ref tasks` does not name %q", c[0])
		}
	}
}

func TestRefTasksJSONSectionShowsTheDocumentsTheCommandsPrint(t *testing.T) {
	jsonText := refSection("JSON")
	db := newTaskTestDB(t)
	// Each step is a call, the document the text says it prints, and the word the text names the
	// call by. The steps are in order: a task is added, approved, claimed, finished, given back,
	// archived and restored, so that every call has something to work on.
	for _, step := range []struct {
		doc, word string
		args      []string
	}{
		{`{"profile","created","task"}`, "add", []string{"add", "write the docs"}},
		{`{"profile","tasks"}`, "approve", []string{"approve", "1"}},
		{`{"profile","task"}`, "next", []string{"next", "--as", "bot"}},
		{`{"profile","task"}`, "next", []string{"next", "--claim", "--as", "bot"}},
		{`{"profile","task"}`, "comment", []string{"comment", "1", "halfway", "--as", "bot"}},
		{`{"profile","task","events"}`, "show", []string{"show", "1"}},
		{`{"profile","tasks"}`, "list", []string{"list"}},
		{`{"profile","rev","counts","tasks"}`, "board", []string{"board"}},
		{`{"profile","task"}`, "finish", []string{"finish", "1", "--result", "done", "--as", "bot"}},
		{`{"profile","task"}`, "move", []string{"move", "1", "ready"}},
		{`{"profile","task"}`, "claim", []string{"claim", "1", "--as", "bot"}},
		{`{"profile","task"}`, "release", []string{"release", "1", "--as", "bot"}},
		{`{"profile","task"}`, "edit", []string{"edit", "1", "--title", "write the docs well"}},
		{`{"profile","tasks"}`, "archive", []string{"archive", "1"}},
		{`{"profile","tasks"}`, "unarchive", []string{"unarchive", "1"}},
		{`{"profile","archived"}`, "archive --status", []string{"archive", "--status", "ready"}},
		{`{"profile","ready","next"}`, "digest", []string{"digest"}},
	} {
		out, _, err := runTask(t, db, "default", true, "", step.args...)
		if err != nil {
			t.Fatalf("task %s: %v\n%s", strings.Join(step.args, " "), err, out)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("task %s: %v\n%s", strings.Join(step.args, " "), err, out)
		}
		var got []string
		for k := range doc {
			got = append(got, k)
		}
		var want []string
		for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(step.doc, -1) {
			want = append(want, m[1])
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("task %s prints a document with the keys %v, but the test (and `ref tasks`) say %s", strings.Join(step.args, " "), got, step.doc)
		}
		stated := false
		for _, line := range strings.Split(jsonText, "\n") {
			stated = stated || strings.Contains(line, step.doc) && strings.Contains(line, step.word)
		}
		if !stated {
			t.Errorf("the JSON section of `ref tasks` has no line that gives %s for %q", step.doc, step.word)
		}
	}
	// The last step found nothing ready: the document says so with null, as the text does.
	out, _, _ := runTask(t, db, "default", true, "", "next", "--as", "bot")
	if !regexp.MustCompile(`"task":\s*null`).MatchString(out) || !strings.Contains(jsonText, "task null when nothing is ready") {
		t.Errorf("`task next` with nothing ready prints %s, and the JSON section must say `task null when nothing is ready`", out)
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
		ref.SetArgs([]string{}) // nil would make cobra read the test binary's own flags
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
