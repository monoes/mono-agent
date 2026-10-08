package tasks

import (
	"strings"
	"testing"
)

// The line and paragraph separators (U+2028 and U+2029) are line ends wherever the store keeps text: a
// newline in notes and in what an agent or the operator writes in a history (a comment, a result, a
// question, a release note), white space in a title, a source title and an app name. None is kept as it
// came, because the printers of the CLI split on \n only and a renderer that breaks lines on a separator
// would show the rest of a line at the margin: no text column of the database holds one afterwards.
func TestTheLineAndParagraphSeparatorsAreLineEndsInEveryTextTheStoreKeeps(t *testing.T) {
	const ls, ps = "\U00002028", "\U00002029"
	s, db, _ := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}

	task, _, err := s.Add(bg, "default", AddInput{
		Title: "t1" + ls + "t2", Notes: "n1" + ls + "n2" + ps + "n3",
		SourceKind: SourceChrome, SourceTitle: "s1" + ls + "s2", SourceApp: "a1" + ps + "a2",
	}, chrome)
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "t1 t2" || task.Notes != "n1\nn2\nn3" || task.Source.Title != "s1 s2" || task.Source.App != "a1 a2" {
		t.Errorf("an added task: title %q, notes %q, source title %q, app %q", task.Title, task.Notes, task.Source.Title, task.Source.App)
	}
	text, _, err := s.Add(bg, "default", AddInput{Text: "first" + ls + "second"}, human)
	if err != nil {
		t.Fatal(err)
	}
	if text.Title != "first" || text.Notes != "first\nsecond" {
		t.Errorf("a task added from a text: title %q, notes %q, want the first line as the title and both as the notes", text.Title, text.Notes)
	}
	title, notes := "e1"+ls+"e2", "m1"+ps+"m2"
	edited, err := s.Edit(bg, "default", task.ID, Edit{Title: &title, Notes: &notes}, human)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Title != "e1 e2" || edited.Notes != "m1\nm2" {
		t.Errorf("an edited task: title %q, notes %q", edited.Title, edited.Notes)
	}

	noteOf := func(id int64) string {
		t.Helper()
		ev := opsEvents(t, s, id)
		return strings.SplitN(ev[len(ev)-1], "|", 4)[3]
	}
	held := func(name string) int64 {
		t.Helper()
		id := mustAdd(t, s, "default", name, true).ID
		claimsClaim(t, s, id, "one", 0)
		return id
	}
	a, b, c := held("for the comments and the result"), held("for a question"), held("for a release")
	if _, err := s.Comment(bg, "default", a, "c1"+ls+"c2", bot("one")); err != nil {
		t.Fatal(err)
	}
	if got := noteOf(a); got != "c1\nc2" {
		t.Errorf("a comment by an agent: %q", got)
	}
	if _, err := s.Comment(bg, "default", a, "d1"+ps+"d2", human); err != nil {
		t.Fatal(err)
	}
	if got := noteOf(a); got != "d1\nd2" {
		t.Errorf("a comment by the operator: %q", got)
	}
	if _, err := s.Finish(bg, "default", a, Outcome{Result: "r1" + ls + "r2"}, bot("one")); err != nil {
		t.Fatal(err)
	}
	if got := noteOf(a); got != "r1\nr2" {
		t.Errorf("a result: %q", got)
	}
	if _, err := s.Finish(bg, "default", b, Outcome{Question: "q1" + ps + "q2"}, bot("one")); err != nil {
		t.Fatal(err)
	}
	if got := noteOf(b); got != "q1\nq2" {
		t.Errorf("a question: %q", got)
	}
	if _, err := s.Release(bg, "default", c, "x1"+ls+ps+"x2", bot("one")); err != nil {
		t.Fatal(err)
	}
	if got := noteOf(c); got != "x1\n\nx2" {
		t.Errorf("a release note: %q", got)
	}

	for _, col := range []struct{ table, column string }{
		{"tasks", "title"}, {"tasks", "notes"}, {"tasks", "source_title"}, {"tasks", "source_app"}, {"task_events", "note"},
	} {
		if n := countWhere(t, db, col.table, "instr("+col.column+", char(8232)) > 0 OR instr("+col.column+", char(8233)) > 0"); n != 0 {
			t.Errorf("%d rows of %s.%s hold a line or a paragraph separator", n, col.table, col.column)
		}
	}
}
