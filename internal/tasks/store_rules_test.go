package tasks

import (
	"errors"
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
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("capture named %.20q (%d bytes) asking for source %q: %v, want ErrInvalid", name, len(name), source, err)
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
		if msg := err.Error(); len(msg) > 400 || strings.Contains(msg, strings.Repeat("x", 100)) {
			t.Errorf("%s: a message of %d bytes", c.name, len(msg))
		}
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
