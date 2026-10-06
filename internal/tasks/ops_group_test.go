package tasks

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// seedColumn inserts n rows of the status straight into the table, in one transaction, at the
// positions step, 2*step, ... from the top, and returns their ids in that order.
func seedColumn(t *testing.T, db *sql.DB, profile, status string, n int, step int64) []int64 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		res, err := tx.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			profile, fmt.Sprintf("%s %d", status, i), status, int64(i+1)*step, rowTime, rowTime)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// approve ids... --top puts the group on top in the order it is given in, by placing it from the last id
// to the first, each one on the top of the queue. That reads only the end of the column, so the cards
// that are in Ready already are never renumbered, however large the group, and the group lands one gap
// apart from the old top upwards. (Each id put after the one before it halves the room above the old top
// every time, and renumbers the whole queue about every eleventh id: 27 times for this group.) The sizes
// are those of a long group put on a long queue and no more: a thousand ids on a thousand cards took
// a minute under -race.
func TestApproveTopPlacesALargeGroupWithoutRenumberingTheQueue(t *testing.T) {
	const group, queue = 300, 100
	s, db, _ := newTestStore(t)
	ready := seedColumn(t, db, "default", "ready", queue, positionGap) // 1024 ... 1024*queue, with 1024 on top
	given := seedColumn(t, db, "default", "inbox", group, positionGap)
	rand.New(rand.NewSource(11)).Shuffle(len(given), func(i, j int) { given[i], given[j] = given[j], given[i] })
	got, err := s.Approve(bg, "default", given, true, human)
	if err != nil || len(got) != group {
		t.Fatalf("approve: %d tasks, err %v", len(got), err)
	}
	if !slices.Equal(idsOf(got), given) {
		t.Error("the answer is not in the order of the ids")
	}
	b, err := s.Board(bg, "default", 0)
	if err != nil {
		t.Fatal(err)
	}
	col := b.Tasks[StatusReady]
	if want := append(append([]int64{}, given...), ready...); !slices.Equal(idsOf(col), want) {
		t.Fatalf("Ready holds %d cards in another order than the group, in the order given, and then the queue it was put on top of", len(col))
	}
	for i, task := range col {
		want := int64(i+1-group) * positionGap // the old top (1024) is the card at index group; the group sits one gap apart above it
		if task.Position != want {
			t.Fatalf("the card %d of Ready is at %d, want %d: the queue must not be renumbered", i, task.Position, want)
		}
	}
}

// A group that is refused names the first task that is wrong in the order the ids are given, whichever
// way the group is placed afterwards, and nothing is written.
func TestApproveRefusesTheFirstTaskThatIsNotInInboxInTheOrderGiven(t *testing.T) {
	for _, top := range []bool{false, true} {
		t.Run(fmt.Sprintf("top %v", top), func(t *testing.T) {
			s, db, _ := newTestStore(t)
			a, b, c := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false), mustAdd(t, s, "default", "c", false)
			firstBad := mustAdd(t, s, "default", "ready", true)
			secondBad := seedRow(t, db, "default", "done")
			before := dumpBoard(t, db)
			_, err := s.Approve(bg, "default", []int64{a.ID, firstBad.ID, b.ID, secondBad, c.ID}, top, human)
			want := fmt.Sprintf("task #%d is ready, not inbox", firstBad.ID)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), fmt.Sprintf("#%d", secondBad)) {
				t.Errorf("approve: %v, want ErrInvalid naming only %q", err, want)
			}
			if after := dumpBoard(t, db); after != before {
				t.Errorf("a refused approval changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// A task named twice in a call is refused before anything is read or written, and the message says
// what is wrong with the list (not the state the task would be in after its first mention).
func TestATaskNamedTwiceIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	s, db, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false)
	x, y := mustAdd(t, s, "default", "x", false), mustAdd(t, s, "default", "y", false)
	if _, err := s.Archive(bg, "default", []int64{x.ID, y.ID}, human); err != nil {
		t.Fatal(err)
	}
	before, rev := dumpBoard(t, db), boardRev(t, s)
	for _, k := range []struct {
		name string
		id   int64
		call func() error
	}{
		{"Approve, next to each other", a.ID, func() error { _, err := s.Approve(bg, "default", []int64{a.ID, a.ID}, false, human); return err }},
		{"Approve --top", b.ID, func() error { _, err := s.Approve(bg, "default", []int64{a.ID, b.ID, b.ID}, true, human); return err }},
		{"Approve, apart", a.ID, func() error { _, err := s.Approve(bg, "default", []int64{a.ID, b.ID, a.ID}, false, human); return err }},
		{"Archive", a.ID, func() error { _, err := s.Archive(bg, "default", []int64{a.ID, b.ID, a.ID}, human); return err }},
		{"Unarchive", x.ID, func() error { _, err := s.Unarchive(bg, "default", []int64{x.ID, y.ID, x.ID}, human); return err }},
	} {
		err := k.call()
		want := fmt.Sprintf("task #%d is named twice", k.id)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want ErrInvalid saying %q", k.name, err, want)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if now := boardRev(t, s); now != rev {
		t.Errorf("the revision moved from %d to %d", rev, now)
	}
}

// A card placed before or after itself gets a message of its own, whether it is in the column or not:
// "not in ready" would be false for a card that is.
func TestPlacingACardBeforeOrAfterItselfSaysSo(t *testing.T) {
	s, db, _ := newTestStore(t)
	a := mustAdd(t, s, "default", "a", true)
	x := mustAdd(t, s, "default", "x", false)
	elsewhere := seedRow(t, db, "default", "done")
	before := dumpBoard(t, db)
	for _, k := range []struct {
		name string
		id   int64
		p    Placement
		want string
	}{
		{"a card of the column before itself", a.ID, Placement{Before: a.ID}, "a task cannot be placed before or after itself"},
		{"a card of the column after itself", a.ID, Placement{After: a.ID}, "a task cannot be placed before or after itself"},
		{"a card that is not in the column before itself", x.ID, Placement{Before: x.ID}, "a task cannot be placed before or after itself"},
		{"a card that is not in the column after itself", x.ID, Placement{After: x.ID}, "a task cannot be placed before or after itself"},
		{"before a card of another column", x.ID, Placement{Before: elsewhere}, fmt.Sprintf("task #%d is not in ready", elsewhere)},
	} {
		_, err := s.Move(bg, "default", k.id, StatusReady, k.p, human)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), k.want) {
			t.Errorf("%s: %v, want ErrInvalid saying %q", k.name, err, k.want)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused placements changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
