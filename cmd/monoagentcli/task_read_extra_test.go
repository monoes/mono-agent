package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/monoes/mono-agent/internal/storage"
)

// storedStamp is the one format the board writes a time in.
const storedStamp = "2006-01-02T15:04:05Z"

// taskSeed is a tasks row written straight into the table, around the store: the
// commands that bring a card to In progress, Review or Done belong to later tasks.
type taskSeed struct {
	profile   string // "" is default
	title     string
	notes     string
	status    string // "" is inbox
	source    string // "" is cli
	link      string
	pageTitle string
	app       string
	holder    string    // claimed_by
	until     time.Time // claim_until, for a holder
	created   time.Time // zero is now
}

// seedTaskRows writes the rows in one connection, in order, each below the cards
// before it in its column, and returns their ids.
func seedTaskRows(t *testing.T, dbPath string, rows ...taskSeed) []int64 {
	t.Helper()
	raw, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.profile == "" {
			r.profile = "default"
		}
		if r.status == "" {
			r.status = "inbox"
		}
		if r.source == "" {
			r.source = "cli"
		}
		if r.created.IsZero() {
			r.created = time.Now()
		}
		until := ""
		if r.holder != "" {
			until = r.until.UTC().Format(storedStamp)
		}
		created := r.created.UTC().Format(storedStamp)
		res, err := raw.DB.Exec(`INSERT INTO tasks (profile_id, title, notes, status, position, source_kind, source_url, source_title, source_app, claimed_by, claim_until, created_at, updated_at)
			VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM tasks), ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.profile, r.title, r.notes, r.status, r.source, r.link, r.pageTitle, r.app, r.holder, until, created, created)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// seedTaskEvent writes a row of a task's history, around the store.
func seedTaskEvent(t *testing.T, dbPath string, taskID int64, at time.Time, actor, kind, from, to, note string) {
	t.Helper()
	raw, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.DB.Exec(`INSERT INTO task_events (task_id, at, actor, kind, from_status, to_status, note) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, at.UTC().Format(storedStamp), actor, kind, from, to, note); err != nil {
		t.Fatal(err)
	}
}

// addTaskProfile inserts a profile besides the bootstrapped default.
func addTaskProfile(t *testing.T, dbPath, id, name string) {
	t.Helper()
	raw, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.DB.Exec(`INSERT INTO profiles (id, name) VALUES (?, ?)`, id, name); err != nil {
		t.Fatal(err)
	}
}

func titlesOf(l listJSON) []string {
	titles := make([]string, 0, len(l.Tasks))
	for _, task := range l.Tasks {
		titles = append(titles, task.Title)
	}
	return titles
}

// heldJSON is the part of a task document that says who holds it.
type heldJSON struct {
	Tasks []struct {
		Title string `json:"title"`
		Claim *struct {
			By    string `json:"by"`
			Stale bool   `json:"stale"`
		} `json:"claim"`
	} `json:"tasks"`
}

func TestTaskListTextIsATableWithTheProfileTheAgeAndTheHolder(t *testing.T) {
	db := newTaskTestDB(t)
	addTaskProfile(t, db, "work-id", "Work")
	now := time.Now()
	ids := seedTaskRows(t, db,
		taskSeed{profile: "work-id", title: "Draft the report"},
		taskSeed{profile: "work-id", title: "Pay the invoice", status: "ready", source: "os", created: now.Add(-73 * time.Hour)},
		taskSeed{profile: "work-id", title: "Fix the build", status: "in_progress", holder: "bot", until: now.Add(time.Hour), created: now.Add(-5*time.Hour - 10*time.Minute)},
		taskSeed{profile: "work-id", title: "Old claim", status: "in_progress", holder: "gone", until: now.Add(-time.Hour), created: now.Add(-12*time.Minute - 10*time.Second)},
		taskSeed{profile: "work-id", title: "Check the results", status: "review"},
		taskSeed{profile: "work-id", title: "Shipped", status: "done", created: now.Add(-49 * time.Hour)},
	)
	out, _, err := runTask(t, db, "Work", false, "", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 8 || lines[0] != "Profile: Work" {
		t.Fatalf("want the profile line, a header and six rows, got %d lines:\n%s", len(lines), out)
	}
	if !regexp.MustCompile(`^ID\s+STATUS\s+TITLE\s+SOURCE\s+AGE\s+HELD BY$`).MatchString(lines[1]) {
		t.Errorf("the header: %q", lines[1])
	}
	until := now.Add(time.Hour).Local().Format("15:04")
	for i, want := range []struct{ status, title, source, age, held string }{
		{"inbox", "Draft the report", "cli", "now", ""},
		{"ready", "Pay the invoice", "os", "3d", ""},
		{"in_progress", "Fix the build", "cli", "5h", "bot until " + until},
		{"in_progress", "Old claim", "cli", "12m", "stale: gone"},
		{"review", "Check the results", "cli", "now", ""},
		{"done", "Shipped", "cli", "2d", ""},
	} {
		tail := `\s*`
		if want.held != "" {
			tail = `\s{2,}` + regexp.QuoteMeta(want.held) + `\s*`
		}
		row := fmt.Sprintf(`^#%d\s{2,}%s\s{2,}%s\s{2,}%s\s{2,}%s%s$`, ids[i], want.status, regexp.QuoteMeta(want.title), want.source, want.age, tail)
		if !regexp.MustCompile(row).MatchString(lines[2+i]) {
			t.Errorf("row %d is %q, want the pattern %s", i+1, lines[2+i], row)
		}
	}
}

func TestTaskBoardTextListsTheFiveColumnsInOrderWithTheirCards(t *testing.T) {
	db := newTaskTestDB(t)
	addTaskProfile(t, db, "work-id", "Work")
	now := time.Now()
	ids := seedTaskRows(t, db,
		taskSeed{profile: "work-id", title: "Idea one"},
		taskSeed{profile: "work-id", title: "Next up", status: "ready"},
		taskSeed{profile: "work-id", title: "After that", status: "ready"},
		taskSeed{profile: "work-id", title: "Fix the build", status: "in_progress", holder: "bot", until: now.Add(time.Hour)},
		taskSeed{profile: "work-id", title: "Old claim", status: "in_progress", holder: "gone", until: now.Add(-time.Hour)},
		taskSeed{profile: "work-id", title: "Check the results", status: "review"},
		taskSeed{profile: "work-id", title: "Shipped", status: "done"},
		taskSeed{profile: "work-id", title: "Put away", status: "archived"},
	)
	// Two writes through the store: the revision of a board that rows were written into around the store is 0.
	var fromCLI, readyFromCLI addedJSON
	mustTaskJSON(t, db, "Work", &fromCLI, "", "add", "from the cli")
	mustTaskJSON(t, db, "Work", &readyFromCLI, "", "add", "ready from the cli", "--ready")

	out, _, err := runTask(t, db, "Work", false, "", "board")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`Profile: Work (revision 2)

INBOX (2)
  #%d  from the cli
  #%d  Idea one

READY (3)
  #%d  Next up
  #%d  After that
  #%d  ready from the cli

IN PROGRESS (2)
  #%d  Fix the build  [bot until %s]
  #%d  Old claim  [stale: gone]

REVIEW (1)
  #%d  Check the results

DONE (1)
  #%d  Shipped
`, fromCLI.Task.ID, ids[0], ids[1], ids[2], readyFromCLI.Task.ID, ids[3], now.Add(time.Hour).Local().Format("15:04"), ids[4], ids[5], ids[6])
	if out != want {
		t.Errorf("the board as text:\n%s\nwant:\n%s", out, want)
	}
	empty, _, err := runTask(t, db, "default", false, "", "board")
	wantEmpty := "Profile: Default (revision 0)\n\nINBOX (0)\n\nREADY (0)\n\nIN PROGRESS (0)\n\nREVIEW (0)\n\nDONE (0)\n"
	if err != nil || empty != wantEmpty {
		t.Errorf("an empty board as text: %q, %v; want %q", empty, err, wantEmpty)
	}
}

func TestTaskBoardJSONHasTheFiveColumnsTheCountsAndNoArchive(t *testing.T) {
	db := newTaskTestDB(t)
	seedTaskRows(t, db,
		taskSeed{title: "held", status: "in_progress", holder: "gone", until: time.Now().Add(-time.Hour)},
		taskSeed{title: "put away", status: "archived"},
	)
	out, _, err := runTask(t, db, "default", true, "", "board")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	// hasKeys reports whether the object has exactly these keys.
	hasKeys := func(raw json.RawMessage, want ...string) bool {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(m))
		for k := range m {
			got = append(got, k)
		}
		slices.Sort(got)
		slices.Sort(want)
		return slices.Equal(got, want)
	}
	if !hasKeys([]byte(out), "profile", "rev", "counts", "tasks") {
		t.Errorf("the board document: %s", out)
	}
	if !hasKeys(doc["counts"], "inbox", "ready", "in_progress", "review", "done", "stale") {
		t.Errorf("the counts: %s", doc["counts"])
	}
	if !hasKeys(doc["tasks"], "inbox", "ready", "in_progress", "review", "done") {
		t.Errorf("the columns are %s: the five, and never the archive", doc["tasks"])
	}
	var counts struct {
		InProgress int `json:"in_progress"`
		Stale      int `json:"stale"`
	}
	if err := json.Unmarshal(doc["counts"], &counts); err != nil || counts.InProgress != 1 || counts.Stale != 1 {
		t.Errorf("counts %+v, %v: one card in progress, and its claim is stale", counts, err)
	}
}

func TestTaskBoardCutsTheDoneColumnButCountsEveryCard(t *testing.T) {
	db := newTaskTestDB(t)
	rows := make([]taskSeed, 0, 55)
	for i := 1; i <= 55; i++ {
		rows = append(rows, taskSeed{title: fmt.Sprintf("done %02d", i), status: "done"})
	}
	seedTaskRows(t, db, rows...)
	for _, c := range []struct {
		args  []string
		shown int
	}{
		{[]string{"board"}, 50},
		{[]string{"board", "--done-limit", "3"}, 3},
		{[]string{"board", "--done-limit", "0"}, 55},
	} {
		var board struct {
			Counts map[string]int        `json:"counts"`
			Tasks  map[string][]taskJSON `json:"tasks"`
		}
		mustTaskJSON(t, db, "default", &board, "", c.args...)
		if got := len(board.Tasks["done"]); got != c.shown || board.Counts["done"] != 55 {
			t.Errorf("task %s: %d Done cards, counted %d; want %d shown of 55", strings.Join(c.args, " "), got, board.Counts["done"], c.shown)
		}
		text, _, err := runTask(t, db, "default", false, "", c.args...)
		after := text[strings.Index(text, "DONE (55)")+len("DONE (55)"):]
		if err != nil || !strings.Contains(text, "DONE (55)") || strings.Count(after, "\n  #") != c.shown {
			t.Errorf("task %s as text: %v, %d Done lines, want the heading DONE (55) and %d lines", strings.Join(c.args, " "), err, strings.Count(after, "\n  #"), c.shown)
		}
	}
	var board struct {
		Tasks map[string][]taskJSON `json:"tasks"`
	}
	mustTaskJSON(t, db, "default", &board, "", "board")
	if done := board.Tasks["done"]; done[0].Title != "done 01" || done[49].Title != "done 50" {
		t.Errorf("the cut keeps the top of the column: %q to %q", done[0].Title, done[len(done)-1].Title)
	}
}

func TestTaskListFlagsNarrowTheList(t *testing.T) {
	db := newTaskTestDB(t)
	now := time.Now()
	seedTaskRows(t, db,
		taskSeed{title: "ready one", status: "ready"},
		taskSeed{title: "inbox one"},
		taskSeed{title: "live bot", status: "in_progress", holder: "bot", until: now.Add(time.Hour)},
		taskSeed{title: "stale bot", status: "in_progress", holder: "bot", until: now.Add(-time.Hour)},
		taskSeed{title: "stale other", status: "in_progress", holder: "other", until: now.Add(-2 * time.Hour)},
		taskSeed{title: "review one", status: "review"},
		taskSeed{title: "chrome one", source: "chrome"},
	)
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"--claimed-by", []string{"--claimed-by", "bot"}, []string{"live bot", "stale bot"}},
		{"--stale", []string{"--stale"}, []string{"stale bot", "stale other"}},
		{"--claimed-by and --stale", []string{"--claimed-by", "bot", "--stale"}, []string{"stale bot"}},
		{"--status with a list", []string{"--status", "in_progress,review"}, []string{"live bot", "stale bot", "stale other", "review one"}},
		{"--status by another spelling", []string{"--status", "In-Progress"}, []string{"live bot", "stale bot", "stale other"}},
		{"--source", []string{"--source", "chrome"}, []string{"chrome one"}},
		{"--status and --source", []string{"--status", "inbox", "--source", "chrome"}, []string{"chrome one"}},
		{"--limit keeps the top of the order", []string{"--limit", "2"}, []string{"inbox one", "chrome one"}},
		{"--status archived is empty here", []string{"--status", "archived"}, []string{}},
	} {
		var got listJSON
		mustTaskJSON(t, db, "default", &got, "", append([]string{"list"}, c.args...)...)
		if !slices.Equal(titlesOf(got), c.want) {
			t.Errorf("list %s: %q, want %q", c.name, titlesOf(got), c.want)
		}
	}
	if doc := failedTaskJSON(t, db, "default", 3, "list", "--source", "pigeon"); doc["code"] != "invalid_input" {
		t.Errorf("an unknown source: %v", doc)
	}
}

// The store decides what is stale and the commands print its verdict: a lease
// that ended a while ago, one that ends this very second, and one still to run.
func TestTaskStaleClaimsAreMarkedInEveryView(t *testing.T) {
	db := newTaskTestDB(t)
	now := time.Now()
	ids := seedTaskRows(t, db,
		taskSeed{title: "live", status: "in_progress", holder: "bot", until: now.Add(time.Hour)},
		taskSeed{title: "long gone", status: "in_progress", holder: "gone", until: now.Add(-time.Hour)},
		taskSeed{title: "this second", status: "in_progress", holder: "edge", until: now},
	)
	var held heldJSON
	mustTaskJSON(t, db, "default", &held, "", "list")
	for i, want := range []struct {
		by    string
		stale bool
	}{{"bot", false}, {"gone", true}, {"edge", true}} {
		if c := held.Tasks[i].Claim; c == nil || c.By != want.by || c.Stale != want.stale {
			t.Errorf("claim of %q: %+v, want held by %s, stale %v", held.Tasks[i].Title, c, want.by, want.stale)
		}
	}
	var onlyStale listJSON
	mustTaskJSON(t, db, "default", &onlyStale, "", "list", "--stale")
	if !slices.Equal(titlesOf(onlyStale), []string{"long gone", "this second"}) {
		t.Errorf("--stale lists %q", titlesOf(onlyStale))
	}

	live := "bot until " + now.Add(time.Hour).Local().Format("15:04")
	table, _, _ := runTask(t, db, "default", false, "", "list")
	board, _, _ := runTask(t, db, "default", false, "", "board")
	for _, want := range []string{live, "stale: gone", "stale: edge"} {
		if !strings.Contains(table, want) || !strings.Contains(board, "["+want+"]") {
			t.Errorf("%q is missing from the list or the board:\n%s\n%s", want, table, board)
		}
	}
	for i, want := range []string{"Held by:  " + live, "Held by:  stale: gone", "Held by:  stale: edge"} {
		shown, _, err := runTask(t, db, "default", false, "", "show", strconv.FormatInt(ids[i], 10))
		if err != nil || !strings.Contains(shown, want+"\n") {
			t.Errorf("show #%d: %v, want the line %q in:\n%s", ids[i], err, want, shown)
		}
	}
}

func TestTaskReadCommandsRefuseAnUnknownProfileAndWrongArguments(t *testing.T) {
	db := newTaskTestDB(t)
	for _, args := range [][]string{{"list"}, {"board"}, {"show", "1"}} {
		if doc := failedTaskJSON(t, db, "no-such-profile", 3, args...); doc["code"] != "invalid_input" {
			t.Errorf("task %s with an unknown profile: %v", strings.Join(args, " "), doc)
		}
	}
	for _, args := range [][]string{{"list", "extra"}, {"board", "extra"}, {"show"}, {"show", "1", "2"}} {
		if out, _, err := runTask(t, db, "default", false, "", args...); err == nil || out != "" {
			t.Errorf("task %s: %v, %q; want an error and no output", strings.Join(args, " "), err, out)
		}
	}
}

// What the caller sent is repeated in a refusal only in part: a huge argument must not
// make a huge message, and the cut never splits a character (the second value is 2-byte
// characters).
func TestTaskReadRefusalsRepeatOnlyAShortStretchOfWhatTheyRefuse(t *testing.T) {
	db := newTaskTestDB(t)
	for _, huge := range []string{strings.Repeat("x", 10<<10), strings.Repeat("\xc3\xa9", 5<<10)} {
		for _, args := range [][]string{{"list", "--status", huge}, {"list", "--source", huge}, {"show", huge}} {
			out, _, err := runTask(t, db, "default", true, "", args...)
			var doc map[string]any
			if exitCode(err) != 3 || json.Unmarshal([]byte(out), &doc) != nil {
				t.Fatalf("task %s <%d bytes>: exit %d (%v), %.80q", args[0], len(huge), exitCode(err), err, out)
			}
			msg, _ := doc["error"].(string)
			if len(msg) == 0 || len(msg) > 300 || len(out) > 400 || !utf8.ValidString(msg) {
				t.Errorf("task %s <%d bytes>: a refusal of %d bytes (document %d): %.120q", args[0], len(huge), len(msg), len(out), msg)
			}
		}
	}
}
