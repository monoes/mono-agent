package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// refTaskGate says who may make each call of the task group. It is the one table the gate is held
// to: the real command tree must have a row for every subcommand (and no row for a command that is
// not there), the real CLI must refuse an agent's call with operator_only exactly where op is true,
// and the sentence of WHO MAY DO WHAT in `ref tasks` that says operator_only must name exactly the
// operator's rows. When a command is added, removed or changes class, edit this table, and make
// ref_tasks.go and AGENTS.md say the same.
var refTaskGate = []struct {
	row  string   // what the text names: a subcommand, or "add --ready"
	op   bool     // the operator's only: an agent's call answers operator_only
	call []string // a call of it; the tests add the agent's --as
}{
	{"add", false, []string{"add", "x"}},
	{"add --ready", true, []string{"add", "x", "--ready"}},
	{"list", false, []string{"list"}},
	{"board", true, []string{"board"}},
	{"show", false, []string{"show", "1"}},
	{"edit", true, []string{"edit", "1", "--title", "x"}},
	{"move", true, []string{"move", "1", "ready"}},
	{"approve", true, []string{"approve", "1"}},
	{"archive", true, []string{"archive", "1"}},
	{"unarchive", true, []string{"unarchive", "1"}},
	{"next", false, []string{"next"}},
	{"claim", false, []string{"claim", "1"}},
	{"comment", false, []string{"comment", "1", "x"}},
	{"finish", false, []string{"finish", "1", "--result", "x"}},
	{"release", false, []string{"release", "1"}},
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

	// The real CLI refuses an agent exactly where the table says.
	db := newTaskTestDB(t)
	for _, r := range refTaskGate {
		out, _, err := runTask(t, db, "default", true, "", append(append([]string{}, r.call...), "--as", "bot")...)
		var doc struct {
			Code string `json:"code"`
		}
		if err != nil {
			_ = json.Unmarshal([]byte(out), &doc)
		}
		if refused := doc.Code == "operator_only"; refused != r.op {
			t.Errorf("`task %s` run by an agent answers the code %q, but refTaskGate (ref_tasks_gate_test.go) says operator_only is %t for it", strings.Join(r.call, " "), doc.Code, r.op)
		}
	}

	// WHO MAY DO WHAT says the same: the sentences that carry the code operator_only name the
	// operator's commands and no other, and every command an agent may run is named elsewhere.
	var gate, rest []string
	for _, s := range regexp.MustCompile(`\.\s+`).Split(refSection("WHO MAY DO WHAT"), -1) {
		if strings.Contains(s, "operator_only") {
			gate = append(gate, s)
		} else {
			rest = append(rest, s)
		}
	}
	if len(gate) == 0 {
		t.Fatal("WHO MAY DO WHAT has no sentence with the code operator_only: it must name the commands the gate refuses")
	}
	word := func(row string) string { return strings.TrimPrefix(row, "add ") } // `add --ready` is named by its flag
	gateText, restText := strings.Join(gate, "\n"), strings.Join(rest, "\n")
	for _, r := range refTaskGate {
		switch {
		case r.op && !refNames(gateText, word(r.row)):
			t.Errorf("the sentence of `ref tasks` that says operator_only does not name `%s`", r.row)
		case !r.op && r.row != "add" && refNames(gateText, word(r.row)):
			t.Errorf("the sentence of `ref tasks` that says operator_only names `%s`, which an agent may run", r.row)
		}
		if !r.op && !refNames(restText, r.row) {
			t.Errorf("WHO MAY DO WHAT never names `%s` outside the sentence that says operator_only, so an agent cannot tell that it may run it", r.row)
		}
	}
	// A sentence that lists what agents may run (three of their commands, and no word about the
	// operator) must not list a command that is the operator's.
	for _, s := range rest {
		agentCommands := 0
		for _, r := range refTaskGate {
			if !r.op && refNames(s, r.row) {
				agentCommands++
			}
		}
		if agentCommands < 3 || regexp.MustCompile(`(?i)operator`).MatchString(s) {
			continue
		}
		for _, r := range refTaskGate {
			if r.op && refNames(s, word(r.row)) {
				t.Errorf("a sentence of WHO MAY DO WHAT lists what an agent may run and names `%s`, which is the operator's: %q", r.row, strings.Join(strings.Fields(s), " "))
			}
		}
	}
}

// A digest that fails (here: a profile that does not exist) prints one line on standard error and
// nothing on standard output, and exits 0, with --json too, so that a session-start hook never
// breaks on it. The places of `ref tasks` that speak of the documents and of the hook say so.
func TestRefTasksSaysWhatADigestThatFailsPrints(t *testing.T) {
	out, errOut, err := runTask(t, newTaskTestDB(t), "no-such-profile", true, "", "digest")
	if out != "" || errOut == "" || err != nil {
		t.Fatalf("a digest that fails printed %q on standard output and %q on standard error, and returned %v", out, errOut, err)
	}
	for _, heading := range []string{"THE AGENT LOOP", "JSON"} {
		text := strings.Join(strings.Fields(refSection(heading)), " ")
		for _, want := range []string{"a digest that fails", "one line on standard error and nothing on standard output"} {
			if !strings.Contains(text, want) {
				t.Errorf("the %s section of `ref tasks` does not say %q", heading, want)
			}
		}
	}
}
