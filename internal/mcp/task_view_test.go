package mcp

// What a task tool returns is a view of the store's task in which every text a person, an agent
// or a capture wrote has a name ending in _untrusted (spec 4.3 and 8), and what it refuses names
// its code first.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/monoes/mono-agent/internal/tasks"
)

// docKeys are a JSON object's keys, sorted.
func docKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// asDoc is a value as the JSON object a tool would return.
func asDoc(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

func TestATaskViewNamesEveryTextUntrusted(t *testing.T) {
	at := time.Date(2026, 10, 6, 10, 30, 0, 0, time.UTC)
	v := asDoc(t, viewOf(tasks.Task{
		ID: 7, ProfileID: "default", Title: "the title", Notes: "the notes", Status: tasks.StatusInProgress, Position: 2048,
		Source:    tasks.Source{Kind: "chrome", URL: "https://example.com/a", Title: "the page", App: "the app"},
		Claim:     &tasks.Claim{By: "agent:x#0001", Until: at},
		LastEvent: &tasks.LastEvent{Actor: "agent:x#0001", Kind: "claimed", At: at},
		CreatedAt: at, UpdatedAt: at,
	}))
	want := []string{"claim", "created_at", "id", "last_event", "notes_untrusted", "position", "profile_id",
		"source_app_untrusted", "source_kind", "source_title_untrusted", "source_url_untrusted", "status", "title_untrusted", "updated_at"}
	if got := docKeys(v); !equalStrings(got, want) {
		t.Errorf("a task's fields are %v, want exactly %v", got, want)
	}
	for k, want := range map[string]any{
		"title_untrusted": "the title", "notes_untrusted": "the notes", "source_url_untrusted": "https://example.com/a",
		"source_title_untrusted": "the page", "source_app_untrusted": "the app", "source_kind": "chrome",
		"status": "in_progress", "id": float64(7), "profile_id": "default", "created_at": "2026-10-06T10:30:00Z",
	} {
		if v[k] != want {
			t.Errorf("%s = %v, want %v", k, v[k], want)
		}
	}
	if c, _ := v["claim"].(map[string]any); c["by"] != "agent:x#0001" || c["until"] != "2026-10-06T10:30:00Z" || c["stale"] != false {
		t.Errorf("claim = %v", v["claim"])
	}
}

func TestAnEventViewNamesItsNoteUntrusted(t *testing.T) {
	at := time.Date(2026, 10, 6, 10, 30, 0, 0, time.UTC)
	e := asDoc(t, eventViews([]tasks.Event{{ID: 3, At: at, Actor: "you", Kind: "comment", Note: "look here"}})[0])
	if got, want := docKeys(e), []string{"actor", "at", "from_status", "id", "kind", "note_untrusted", "to_status"}; !equalStrings(got, want) {
		t.Errorf("an event's fields are %v, want exactly %v", got, want)
	}
	if e["note_untrusted"] != "look here" || e["kind"] != "comment" || e["actor"] != "you" {
		t.Errorf("event = %v", e)
	}
	for name, v := range map[string]any{"no events": eventViews(nil), "no tasks": listViews(nil)} {
		if b, _ := json.Marshal(v); string(b) != "[]" {
			t.Errorf("%s: %s, want [] (arrays are never null)", name, b)
		}
	}
}

func TestAListCutsLongNotesAndSaysWhere(t *testing.T) {
	exact := strings.Repeat("\U000000e9", listNotesRunes) // two bytes each: a cut must not split one
	if got := cutListNotes(exact); got != exact {
		t.Errorf("notes of exactly %d characters were changed", listNotesRunes)
	}
	got := cutListNotes(exact + "x")
	if !strings.HasPrefix(got, exact+"\n[cut at 1000 characters") || !strings.Contains(got, "task_get") || !utf8.ValidString(got) {
		t.Errorf("notes one character over the limit: %q", got[len(exact):])
	}
	long := tasks.Task{ID: 1, Notes: exact + "tail"}
	if v := listViews([]tasks.Task{long}); strings.Contains(v[0].NotesUntrusted, "tail") {
		t.Error("a list's notes were not cut")
	}
	if v := listView(long); strings.Contains(v.NotesUntrusted, "tail") {
		t.Error("a verb's result must cut the notes as a list does")
	}
	if v := viewOf(long); v.NotesUntrusted != long.Notes {
		t.Error("one task's notes must be whole")
	}
}

func TestTaskToolErrNamesTheCodeFirst(t *testing.T) {
	until := time.Date(2026, 10, 6, 10, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: #3", tasks.ErrNotFound), "not_found: task not found: #3"},
		{fmt.Errorf("%w: a task needs a title", tasks.ErrInvalid), "invalid_input: "},
		{fmt.Errorf("%w: add to Ready", tasks.ErrOperatorOnly), "operator_only: "},
		{fmt.Errorf("%w: task #3 is inbox", tasks.ErrNotReady), "not_ready: "},
		{fmt.Errorf("%w: claim it first", tasks.ErrNotClaimant), "not_claimant: "},
		{fmt.Errorf("%w: 20 an hour", tasks.ErrLimit), "limit: "},
		{&tasks.ClaimedError{By: "agent:b#0002", Until: until}, "claimed: task is claimed by agent:b#0002 until 2026-10-06T10:30:00Z"},
	} {
		got := taskToolErr(c.err)
		if got == nil || !strings.HasPrefix(got.Error(), c.want) || !errors.Is(got, c.err) {
			t.Errorf("%v: %v, want it to start %q and wrap the store's error", c.err, got, c.want)
		}
	}
	for err, want := range map[error]string{
		fmt.Errorf("%w: a task needs a title", tasks.ErrInvalid): "invalid_input: a task needs a title",
		fmt.Errorf("%w: 20 an hour", tasks.ErrLimit):             "limit: 20 an hour",
	} {
		if got := taskToolErr(err).Error(); got != want {
			t.Errorf("%q, want %q: the sentinel's words only repeat the code", got, want)
		}
	}
	if taskToolErr(nil) != nil {
		t.Error("no error must stay no error")
	}
	other := errors.New("tasks: reading the profile: disk I/O error")
	if got := taskToolErr(other); got != other {
		t.Errorf("an error that is not a refusal must pass as it is: %v", got)
	}
}

func TestDecodeTaskArgsRefusesWhatATaskToolDoesNotTake(t *testing.T) {
	type args struct {
		ID    taskIDArg `json:"id"`
		Title string    `json:"title"`
	}
	for _, raw := range []string{"", "null", " {} "} {
		var a args
		if err := decodeTaskArgs(json.RawMessage(raw), &a); err != nil || a.ID != 0 {
			t.Errorf("%q: %v", raw, err)
		}
	}
	var a args
	if err := decodeTaskArgs(json.RawMessage(`{"id": 3, "title": "T"}`), &a); err != nil || a.ID != 3 || a.Title != "T" {
		t.Errorf("the arguments it takes: %+v, %v", a, err)
	}
	for _, raw := range []string{`{"id": 3, "profile": "work"}`, `{"as": "you"}`, `{"ready": true}`, `{"title": 5}`, `[1]`, `"x"`} {
		var a args
		if err := decodeTaskArgs(json.RawMessage(raw), &a); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: ") {
			t.Errorf("%s: %v, want an invalid_input refusal", raw, err)
		}
	}
	err := decodeTaskArgs(json.RawMessage(`{"profile": "work"}`), &args{})
	if err == nil || !strings.Contains(err.Error(), `"profile"`) || !strings.Contains(err.Error(), "one profile") {
		t.Errorf("a profile argument must be refused, saying the server serves one profile: %v", err)
	}
}

func TestATaskIDIsANumberAsAModelWritesIt(t *testing.T) {
	for _, raw := range []string{`12`, `"12"`, `"#12"`, `" #12 "`} {
		var id taskIDArg
		if err := json.Unmarshal([]byte(raw), &id); err != nil || id != 12 {
			t.Errorf("%s: %d, %v", raw, id, err)
		}
	}
	for _, raw := range []string{`0`, `-1`, `"x"`, `1.5`, `true`, `"#"`} {
		var id taskIDArg
		if err := json.Unmarshal([]byte(raw), &id); err == nil {
			t.Errorf("%s: accepted as %d", raw, id)
		}
	}
	var none taskIDArg
	if err := json.Unmarshal([]byte(`null`), &none); err != nil {
		t.Fatal(err)
	}
	if _, err := none.need(); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: id is required") {
		t.Errorf("no id: %v", err)
	}
}

func TestANumberArgumentMayBeAString(t *testing.T) {
	for raw, want := range map[string]numberArg{`20`: 20, `"20"`: 20, `" 7 "`: 7, `-5`: -5, `null`: 0} {
		var n numberArg
		if err := json.Unmarshal([]byte(raw), &n); err != nil || n != want {
			t.Errorf("%s: %d, %v; want %d", raw, n, err, want)
		}
	}
	for _, raw := range []string{`"x"`, `1.5`, `true`, `"20 minutes"`} {
		var n numberArg
		if err := json.Unmarshal([]byte(raw), &n); err == nil || !strings.Contains(err.Error(), "whole number") {
			t.Errorf("%s: %v, want a refusal that asks for a whole number", raw, err)
		}
	}
}

func TestAStatusArgumentIsOneColumnOrSeveral(t *testing.T) {
	for raw, want := range map[string]string{
		`"ready"`:                 "ready",
		`"ready, review"`:         "ready review",
		`["in-progress", "done"]`: "in_progress done",
		`"progress"`:              "in_progress",
		`""`:                      "",
		`[]`:                      "",
	} {
		var s statusArg
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		var names []string
		for _, st := range s {
			names = append(names, string(st))
		}
		if got := strings.Join(names, " "); got != want {
			t.Errorf("%s = %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{`"someday"`, `5`, `["ready", 3]`} {
		var s statusArg
		if err := json.Unmarshal([]byte(raw), &s); err == nil {
			t.Errorf("%s: accepted as %v", raw, s)
		}
	}
}
