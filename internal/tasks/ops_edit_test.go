package tasks

import (
	"strings"
	"testing"
	"time"
)

// What Edit stores is cleaned like what Add stores: one line for a title, notes with their line
// breaks, and neither with a control, hidden or invalid character.
func TestEditCleansWhatItStores(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "t", false)
	hostile := "x\x1b[31m\x00\U0000202e\U000E0049\U0000FEFFy\n w"
	for _, k := range []struct{ name, title, notes, wantTitle, wantNotes string }{
		{"escape, NUL, bidi, tag and BOM characters", hostile, hostile, "x[31my w", "x[31my\n w"},
		{"line ends and white space", "  a \t b\r\n c ", "  l1\r\nl2\rl3\t  ", "a b c", "l1\nl2\nl3"},
		{"invalid UTF-8", "a\xffb", "c\xfed", "a\U0000FFFDb", "c\U0000FFFDd"},
	} {
		got, err := s.Edit(bg, "default", task.ID, Edit{Title: &k.title, Notes: &k.notes}, human)
		if err != nil || got.Title != k.wantTitle || got.Notes != k.wantNotes {
			t.Errorf("%s: title %q, notes %q, err %v, want %q and %q", k.name, got.Title, got.Notes, err, k.wantTitle, k.wantNotes)
		}
	}
}

func TestEditCutsATitleAtTwoHundredRunesAndNotesAtSixtyFourKiB(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "t", false)
	for _, k := range []struct{ name, title, want string }{
		{"200 runes are kept", strings.Repeat("x", 200), strings.Repeat("x", 200)},
		{"201 runes are cut to 199 and an ellipsis", strings.Repeat("x", 201), strings.Repeat("x", 199) + "\U00002026"},
		{"two-byte runes count as one", strings.Repeat("\U000000e9", 201), strings.Repeat("\U000000e9", 199) + "\U00002026"},
	} {
		if got, err := s.Edit(bg, "default", task.ID, Edit{Title: &k.title}, human); err != nil || got.Title != k.want {
			t.Errorf("%s: %d bytes, err %v, want %d bytes", k.name, len(got.Title), err, len(k.want))
		}
	}

	exact := strings.Repeat("n", MaxNotesBytes)
	if got, err := s.Edit(bg, "default", task.ID, Edit{Notes: &exact}, human); err != nil || got.Notes != exact {
		t.Errorf("notes of exactly the limit: %d bytes kept, err %v", len(got.Notes), err)
	}
	over := exact + "n"
	got, err := s.Edit(bg, "default", task.ID, Edit{Notes: &over}, human)
	if err != nil || !strings.HasSuffix(got.Notes, "\n[truncated: 65537 characters in the original]") {
		t.Fatalf("notes one byte over the limit: err %v, ends %q", err, got.Notes[max(0, len(got.Notes)-60):])
	}
	checkCut(t, got.Notes, MaxNotesBytes)
}

// What a cut text is stored as is what comes back from a read, and an edit sends it back: it must
// be no change, however it was cut. (cutBytes counts its notice in the limit, so a cut text is
// not cut again.)
func TestEditPassingTheStoredTextBackWritesNothing(t *testing.T) {
	s, db, c := newTestStore(t)
	long, longTitle := strings.Repeat("n", MaxNotesBytes+500), strings.Repeat("t", 250)
	task, _, err := s.Add(bg, "default", AddInput{Title: longTitle, Notes: long}, human)
	if err != nil || len(task.Notes) > MaxNotesBytes || !strings.Contains(task.Notes, "[truncated:") {
		t.Fatalf("setup: notes of %d bytes, err %v", len(task.Notes), err)
	}
	before := dumpBoard(t, db)
	c.advance(time.Hour)
	for _, k := range []struct {
		name         string
		title, notes string
	}{
		{"the stored text", task.Title, task.Notes},
		{"the original text again", longTitle, long},
		{"the stored notes alone", "", task.Notes},
		{"the original title alone", longTitle, ""},
	} {
		e := Edit{}
		if k.title != "" {
			e.Title = &k.title
		}
		if k.notes != "" {
			e.Notes = &k.notes
		}
		got, err := s.Edit(bg, "default", task.ID, e, human)
		if err != nil || got.Title != task.Title || got.Notes != task.Notes {
			t.Errorf("%s: %d bytes of notes, err %v", k.name, len(got.Notes), err)
		}
		if after := dumpBoard(t, db); after != before {
			t.Fatalf("%s: an edit that changed nothing wrote to the database:\nbefore:\n%s\nafter:\n%s", k.name, before, after)
		}
	}
}

// The event names what changed, and only that.
func TestEditSaysWhatItChanged(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "title", false)
	set := func(title, notes *string) {
		t.Helper()
		if _, err := s.Edit(bg, "default", task.ID, Edit{Title: title, Notes: notes}, human); err != nil {
			t.Fatal(err)
		}
	}
	p := func(s string) *string { return &s }
	set(p("title two"), nil)
	set(nil, p("notes one"))
	set(p("title three"), p("notes two"))
	set(p("title three"), p("notes three")) // the title is the same: only the notes changed
	set(p("title four"), p("notes three"))  // and the other way round
	want := []string{
		"created|you|>inbox|",
		"edited|you|>|title",
		"edited|you|>|notes",
		"edited|you|>|title, notes",
		"edited|you|>|notes",
		"edited|you|>|title",
	}
	if got := opsEvents(t, s, task.ID); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	got, _, _ := s.Get(bg, "default", task.ID)
	if got.Title != "title four" || got.Notes != "notes three" {
		t.Errorf("stored %q and %q", got.Title, got.Notes)
	}
}

func TestEditCanClearTheNotes(t *testing.T) {
	s, _, _ := newTestStore(t)
	task, _, err := s.Add(bg, "default", AddInput{Title: "t", Notes: "some notes"}, human)
	if err != nil {
		t.Fatal(err)
	}
	none := ""
	got, err := s.Edit(bg, "default", task.ID, Edit{Notes: &none}, human)
	if err != nil || got.Notes != "" || got.Title != "t" {
		t.Errorf("clearing the notes: %+v, %v", got, err)
	}
	if ev := opsEvents(t, s, task.ID); ev[len(ev)-1] != "edited|you|>|notes" {
		t.Errorf("events: %v", ev)
	}
}

// An edit changes the words and the time of the change: not the column, the place in it, the claim
// or the day the task was made. A task in the archive is edited like any other.
func TestEditLeavesColumnPlaceClaimAndCreationAlone(t *testing.T) {
	s, db, c := newTestStore(t)
	held := opsHeld(t, s, db, c, "held", "bob", time.Hour)
	arch := mustAdd(t, s, "default", "archived", false)
	if _, err := s.Archive(bg, "default", []int64{arch.ID}, human); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	for _, task := range []Task{held, arch} {
		before, _, _ := s.Get(bg, "default", task.ID)
		by, until := opsClaim(t, db, task.ID)
		title := "edited " + task.Title
		got, err := s.Edit(bg, "default", task.ID, Edit{Title: &title}, human)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != before.Status || got.Position != before.Position || !got.CreatedAt.Equal(before.CreatedAt) {
			t.Errorf("%s: status %s position %d created %v, want %s %d %v", task.Title, got.Status, got.Position, got.CreatedAt, before.Status, before.Position, before.CreatedAt)
		}
		if by2, until2 := opsClaim(t, db, task.ID); by2 != by || until2 != until {
			t.Errorf("%s: claim %q until %q became %q until %q", task.Title, by, until, by2, until2)
		}
		if !got.UpdatedAt.Equal(c.t) {
			t.Errorf("%s: updated at %v, want %v", task.Title, got.UpdatedAt, c.t)
		}
		if ev := opsEvents(t, s, task.ID); ev[len(ev)-1] != "edited|you|>|title" || strings.Contains(strings.Join(ev, ","), "released") {
			t.Errorf("%s: events %v", task.Title, ev)
		}
	}
	if got, _, _ := s.Get(bg, "default", held.ID); got.Claim == nil || got.Claim.By != "bob" {
		t.Errorf("the held task after an edit: %+v", got.Claim)
	}
}
