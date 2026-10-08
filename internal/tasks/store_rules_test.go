package tasks

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The matrix of spec 5.1: which source a task gets for every actor and every
// source a caller may ask for. "" is a refusal (ErrInvalid).
func TestSourceKindMatrix(t *testing.T) {
	asks := []string{"", SourceCLI, SourceApp, SourceChrome, SourceOS, SourceAgent, "bogus"}
	rows := []struct {
		name  string
		actor Actor
		want  []string // one per entry of asks
	}{
		{"operator", human, []string{SourceCLI, SourceCLI, SourceApp, "", "", "", ""}},
		{"agent", bot("b"), []string{SourceAgent, SourceAgent, "", "", "", SourceAgent, ""}},
		{"chrome capture", Actor{Kind: Capture, Name: SourceChrome}, []string{SourceChrome, "", "", SourceChrome, "", "", ""}},
		{"os capture", Actor{Kind: Capture, Name: SourceOS}, []string{SourceOS, "", "", "", SourceOS, "", ""}},
		{"nameless capture", Actor{Kind: Capture}, []string{"", "", "", SourceChrome, SourceOS, "", ""}},
		{"no kind", Actor{}, []string{"", "", "", "", "", "", ""}},
		{"unknown kind", Actor{Kind: 99, Name: "x"}, []string{"", "", "", "", "", "", ""}},
	}
	s, _, _ := newTestStore(t)
	for _, row := range rows {
		for i, ask := range asks {
			task, _, err := s.Add(bg, "default", AddInput{Title: "t", SourceKind: ask}, row.actor)
			switch {
			case row.want[i] == "" && !errors.Is(err, ErrInvalid):
				t.Errorf("%s asking for %q: err %v, want ErrInvalid", row.name, ask, err)
			case row.want[i] != "" && (err != nil || task.Source.Kind != row.want[i]):
				t.Errorf("%s asking for %q: kind %q, err %v, want %q", row.name, ask, task.Source.Kind, err, row.want[i])
			}
		}
	}
}

// The gate must not depend on a name, and must hold for an actor of no kind.
func TestAddToReadyRefusesEveryActorButTheOperator(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, a := range []Actor{bot("b"), bot(""), {Kind: Capture, Name: SourceOS}, {Kind: Capture}, {}, {Kind: 99, Name: "x"}} {
		for _, source := range []string{"", SourceCLI, SourceOS, SourceChrome} {
			if _, _, err := s.Add(bg, "default", AddInput{Title: "go", Ready: true, SourceKind: source}, a); !errors.Is(err, ErrOperatorOnly) {
				t.Errorf("%+v adding to Ready with source %q: %v, want ErrOperatorOnly", a, source, err)
			}
		}
	}
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 0 {
		t.Errorf("%d tasks stored by refused adds", n)
	}
}

// Who is asking decides before what is asked: a request for Ready from anyone but the operator is
// refused as that, whatever the text, the title, the notes or the source say, and nothing is stored.
// The operator's own empty text is still an invalid task.
func TestTheReadyGateComesBeforeAnyCheckOfTheTask(t *testing.T) {
	s, db, _ := newTestStore(t)
	asks := []AddInput{
		{Ready: true},                                  // no words at all
		{Ready: true, Text: " \n\t "},                  // white space only
		{Ready: true, Text: "\x1b\x00"},                // controls only
		{Ready: true, Text: "buy milk"},                // good words
		{Ready: true, Notes: "notes need a title"},     // notes alone
		{Ready: true, Notes: "n", Text: "t"},           // notes and text together
		{Ready: true, Title: "t", SourceKind: "bogus"}, // a source nobody may use
		{Ready: true, Title: "t", ClientID: "bad id!"}, // a malformed client id
	}
	for _, a := range []Actor{bot("b"), bot(""), {Kind: Capture, Name: SourceChrome}, {Kind: Capture}} {
		for _, in := range asks {
			if _, _, err := s.Add(bg, "default", in, a); !errors.Is(err, ErrOperatorOnly) {
				t.Errorf("%+v asking for Ready with %#v: %v, want ErrOperatorOnly", a, in, err)
			}
		}
	}
	for _, table := range []string{"tasks", "task_events", "task_board_rev"} {
		if n := countWhere(t, db, table, "1 = 1"); n != 0 {
			t.Errorf("%d rows in %s after the refused requests", n, table)
		}
	}
	for _, text := range []string{"", " \n\t "} {
		if _, _, err := s.Add(bg, "default", AddInput{Ready: true, Text: text}, human); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrOperatorOnly) {
			t.Errorf("the operator asking for Ready with text %q: %v, want ErrInvalid", text, err)
		}
	}
}

// A capture's Name is a surface: empty, exactly "chrome" or exactly "os". It is what the events say
// about who acted, so a capture may not put a name of its own on the audit trail, and a surface files
// only its own source.
func TestACapturesNameIsCheckedLikeAnAgentsName(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, name := range []string{
		"x\x1b[31m", "two words", "a\U0000202eb", "you", "agent", "capture", "Chrome", "OS", "chrome ", " os", "chrome\n",
		strings.Repeat("a", MaxNameLen), strings.Repeat("a", MaxNameLen+1), strings.Repeat("a", 100000),
	} {
		for _, source := range []string{"", SourceChrome, SourceOS} {
			_, _, err := s.Add(bg, "default", AddInput{Title: "t", SourceKind: source}, Actor{Kind: Capture, Name: name})
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "named chrome or os") {
				t.Errorf("capture named %.20q (%d bytes) asking for source %q: %v, want ErrInvalid saying a capture is named chrome or os", name, len(name), source, err)
			}
		}
	}
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 0 {
		t.Errorf("%d tasks stored by captures with a bad name", n)
	}
	for _, c := range []struct {
		name, source, want string // want "" means refused
	}{
		{SourceChrome, "", SourceChrome},
		{SourceChrome, SourceChrome, SourceChrome},
		{SourceChrome, SourceOS, ""},
		{SourceChrome, SourceCLI, ""},
		{SourceOS, "", SourceOS},
		{SourceOS, SourceOS, SourceOS},
		{SourceOS, SourceChrome, ""},
		{"", SourceChrome, SourceChrome},
		{"", SourceOS, SourceOS},
		{"", SourceCLI, ""},
	} {
		task, _, err := s.Add(bg, "default", AddInput{Title: "t", SourceKind: c.source}, Actor{Kind: Capture, Name: c.name})
		switch {
		case c.want == "" && !errors.Is(err, ErrInvalid):
			t.Errorf("capture %q asking for %q: err %v, want ErrInvalid", c.name, c.source, err)
		case c.want != "" && (err != nil || task.Source.Kind != c.want):
			t.Errorf("capture %q asking for %q: kind %q, err %v, want %q", c.name, c.source, task.Source.Kind, err, c.want)
		}
	}
	if n := countWhere(t, db, "task_events", "LENGTH(actor) > 64 OR actor GLOB '*[^A-Za-z0-9._#@:-]*'"); n != 0 {
		t.Errorf("%d events carry an actor label that is not a name", n)
	}
}

// "you" is the label of every operator event, "agent" of an unnamed agent's, and "capture", "chrome"
// and "os" of the capture surfaces. An agent that took one would write events that read as theirs (the
// CLI lets the caller choose the name with --as). Case does not matter: a label is read by eye.
func TestAnAgentCannotBeNamedLikeTheOperator(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, name := range []string{"you", "You", "YOU", "agent", "Agent", "capture", "CAPTURE", "chrome", "Chrome", "os", "oS"} {
		_, _, err := s.Add(bg, "default", AddInput{Title: "t"}, bot(name))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("an agent named %q: %v, want ErrInvalid saying the name is reserved", name, err)
		}
	}
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 0 {
		t.Errorf("%d tasks stored under a reserved name", n)
	}
	for _, name := range []string{"you2", "my-agent", "agent-7", "agent:Claude-Code#beef", "os.bot", "chrome#1", "capture:2", "yo"} {
		task, _, err := s.Add(bg, "default", AddInput{Title: "t"}, bot(name))
		if err != nil || task.LastEvent == nil || task.LastEvent.Actor != name {
			t.Errorf("an agent named %q: last event %+v, err %v, want the name on the event", name, task.LastEvent, err)
		}
	}
}

func TestAnUnnamedAgentIsLabelledAgentAndAHumansNameIsIgnored(t *testing.T) {
	s, _, _ := newTestStore(t)
	task, _, err := s.Add(bg, "default", AddInput{Title: "t"}, bot(""))
	if err != nil || task.LastEvent == nil || task.LastEvent.Actor != "agent" || task.Source.Kind != SourceAgent {
		t.Errorf("an agent with no name: last event %+v, err %v, want it accepted and labelled agent", task.LastEvent, err)
	}
	task, _, err = s.Add(bg, "default", AddInput{Title: "t"}, Actor{Kind: Human, Name: "x\x1b[31m"})
	if err != nil || task.LastEvent == nil || task.LastEvent.Actor != "you" {
		t.Errorf("the operator with a name: last event %+v, err %v, want the name ignored and the label you", task.LastEvent, err)
	}
}

// An error that repeats what a caller sent repeats a bounded part of it: a 10 MiB id must not make a
// 10 MiB message.
func TestErrorsRepeatAtMostSixtyFourRunesOfWhatTheCallerSent(t *testing.T) {
	s, _, _ := newTestStore(t)
	huge := strings.Repeat("x", 1<<20)
	for _, c := range []struct {
		name    string
		profile string
		source  string
		actor   Actor
	}{
		{"an unknown profile", huge, "", human},
		{"a source an agent may not ask for", "default", huge, bot("b")},
		{"a source the operator may not ask for", "default", huge, human},
		{"a source a nameless capture may not ask for", "default", huge, Actor{Kind: Capture}},
		{"a source that is not the capture's own", "default", huge, Actor{Kind: Capture, Name: SourceChrome}},
		{"a capture's name", "default", SourceChrome, Actor{Kind: Capture, Name: huge}},
	} {
		_, _, err := s.Add(bg, c.profile, AddInput{Title: "t", SourceKind: c.source}, c.actor)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", c.name, err)
			continue
		}
		// 64 runes of it: 63 and an ellipsis
		msg := err.Error()
		if len(msg) > 400 || strings.Contains(msg, strings.Repeat("x", 64)) || !strings.Contains(msg, strings.Repeat("x", 63)+"\U00002026") {
			t.Errorf("%s: a message of %d bytes, want the caller's text cut to 63 runes and an ellipsis", c.name, len(msg))
		}
	}
}

// CheckAgentName is the store's judge of the name an agent acts under, for a surface that has to judge
// one before it opens the database (the CLI before it runs a command, a server before it starts). It must
// say what the store says, the same refusals (ErrInvalid) in the same words, and the test holds the store's
// own callers to them (the verbs of an agent, and an add) so that the two cannot drift apart. A name is
// 1-64 characters of letters, digits and . _ # @ : -, and the labels you, agent, capture, chrome and os are
// reserved in any case (spec 5.1). An empty name is no name: the judge refuses it like any other name
// outside the shape, and whether an agent may go without one is the caller's business, which the verbs and
// an add decide before they ask: a verb says that the agent needs a name, an add lets it through as agent.
func TestCheckAgentNameJudgesANameAsTheStoreDoes(t *testing.T) {
	const shape = "invalid input: an agent name is 1-64 characters of letters, digits and . _ # @ : -"
	reserved := func(name string) string {
		return fmt.Sprintf("invalid input: an agent may not be named %q: you, agent, capture, chrome and os are reserved", name)
	}
	s, db, _ := newTestStore(t)
	for _, name := range []string{
		"claude-7f3a", "agent:claude-code#a3f9", "a.b_c@d", "A", "yo", "you2", "my-agent", "agent-7", "agents", "os.bot", "chrome#1", "capture:2",
		strings.Repeat("n", MaxNameLen),
	} {
		if err := CheckAgentName(name); err != nil {
			t.Errorf("CheckAgentName(%.40q) = %v, want the name let through", name, err)
		}
		if err := needName(bot(name)); err != nil {
			t.Errorf("a verb of an agent named %.40q: %v, want the name let through", name, err)
		}
	}

	type refusal struct{ what, name, want string }
	refused := []refusal{
		{"an empty name", "", shape},
		{"one character more than a name may have", strings.Repeat("n", MaxNameLen+1), shape},
		{"a name of 100,000 characters", strings.Repeat("n", 100000), shape},
		{"a space", "bad name", shape},
		{"a semicolon", "semi;colon", shape},
		{"a newline", "bad\nname", shape},
		{"a tab", "bad\tname", shape},
		{"an escape byte", "a\x1b[31m", shape},
		{"a NUL after a reserved label", "os\x00", shape},
		{"a space before a reserved label", " you", shape},
		{"a space after a reserved label", "you ", shape},
		{"a newline after a reserved label", "you\n", shape},
		{"an accented letter", "caf\U000000e9", shape},
		{"a Cyrillic o in you", "y\U0000043eu", shape},
		{"a zero-width space after you", "you\U0000200b", shape},
		{"a full-width o in you", "y\U0000ff4fu", shape},
		{"the Kelvin sign in a name", "\U0000212aey", shape},
		{"a combining accent after agent", "agent\U00000301", shape},
		{"a bidi override", "a\U0000202eb", shape},
	}
	for _, name := range []string{
		"you", "You", "YOU", "yOu", "agent", "Agent", "AGENT", "capture", "Capture", "CAPTURE", "chrome", "Chrome", "CHROME", "os", "Os", "oS", "OS",
	} {
		refused = append(refused, refusal{"the reserved label " + name, name, reserved(name)})
	}
	for _, c := range refused {
		if err := CheckAgentName(c.name); !errors.Is(err, ErrInvalid) || err.Error() != c.want {
			t.Errorf("CheckAgentName(%.40q), %s: %v, want ErrInvalid saying %q", c.name, c.what, err, c.want)
		}
		if c.name == "" {
			continue // the caller's business, below
		}
		if err := needName(bot(c.name)); err == nil || err.Error() != c.want {
			t.Errorf("a verb of an agent named %.40q, %s: %v, want the words of CheckAgentName", c.name, c.what, err)
		}
		if _, _, err := s.Add(bg, "default", AddInput{Title: "t"}, bot(c.name)); err == nil || err.Error() != c.want {
			t.Errorf("an add by an agent named %.40q, %s: %v, want the words of CheckAgentName", c.name, c.what, err)
		}
	}
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 0 {
		t.Errorf("%d tasks were stored by agents with a refused name", n)
	}
	if err := needName(bot("")); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "needs a name") {
		t.Errorf("a verb of an agent with no name: %v, want ErrInvalid saying that it needs a name", err)
	}
	if task, _, err := s.Add(bg, "default", AddInput{Title: "t"}, bot("")); err != nil || task.LastEvent == nil || task.LastEvent.Actor != "agent" {
		t.Errorf("an add by an agent with no name: last event %+v, err %v, want it let through as agent", task.LastEvent, err)
	}
}

func TestClientIDAndAgentNameLengthsAndAlphabets(t *testing.T) {
	s, _, _ := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	for _, c := range []struct {
		id string
		ok bool
	}{
		{strings.Repeat("a", 64), true}, {strings.Repeat("a", 65), false}, {"A-b_9", true},
		{"a.b", false}, {"a/b", false}, {"a:b", false}, {"é", false}, {"a b", false}, {"a\x00", false},
	} {
		_, _, err := s.Add(bg, "default", AddInput{Title: "t", ClientID: c.id, SourceKind: SourceChrome}, chrome)
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrInvalid)) {
			t.Errorf("client id %q: err %v, want ok %v", c.id, err, c.ok)
		}
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{strings.Repeat("a", 64), true}, {strings.Repeat("a", 65), false}, {"a.b_c#d@e:f-g", true}, {"a b", false}, {"é", false},
	} {
		_, _, err := s.Add(bg, "default", AddInput{Title: "t"}, bot(c.name))
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrInvalid)) {
			t.Errorf("agent name %q: err %v, want ok %v", c.name, err, c.ok)
		}
	}
}
