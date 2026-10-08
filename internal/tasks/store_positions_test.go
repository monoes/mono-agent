package tasks

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestAtTopSaysWhereEveryColumnGrows(t *testing.T) {
	for st, want := range map[Status]bool{StatusInbox: true, StatusReady: false, StatusInProgress: false, StatusReview: true, StatusDone: true, StatusArchived: false} {
		if got := atTop(st); got != want {
			t.Errorf("atTop(%s) = %v, want %v", st, got, want)
		}
	}
}

func positionsOf(t *testing.T, db *sql.DB, profile, status string) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT position FROM tasks WHERE profile_id = ? AND status = ? ORDER BY id`, profile, status)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var p int64
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestPositionsGoInStepsOfOneGapFromOneGapPerColumnAndProfile(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	for i := 0; i < 3; i++ {
		mustAdd(t, s, "default", "i", false)
		mustAdd(t, s, "default", "r", true)
	}
	mustAdd(t, s, other, "elsewhere", true)
	mustAdd(t, s, other, "elsewhere", false)
	for _, c := range []struct {
		profile, status string
		want            []int64
	}{
		{"default", "inbox", []int64{1024, 0, -1024}},   // newest first: each new card above the top
		{"default", "ready", []int64{1024, 2048, 3072}}, // a queue: each new card below the bottom
		{other, "ready", []int64{1024}},
		{other, "inbox", []int64{1024}},
	} {
		if got := positionsOf(t, db, c.profile, c.status); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s/%s positions %v, want %v", c.profile, c.status, got, c.want)
		}
	}
}

func TestEdgePositionIgnoresTheExceptedCard(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	a := mustAdd(t, s, "default", "a", false) // 1024
	mustAdd(t, s, "default", "b", false)      // 0
	c := mustAdd(t, s, "default", "c", false) // -1024
	for _, k := range []struct {
		name    string
		profile string
		status  Status
		top     bool
		except  int64
		want    int64
	}{
		{"above the top", "default", StatusInbox, true, 0, -2048},
		{"below the bottom", "default", StatusInbox, false, 0, 2048},
		{"above the top, ignoring the top card", "default", StatusInbox, true, c.ID, -1024},
		{"below the bottom, ignoring the bottom card", "default", StatusInbox, false, a.ID, 1024},
		{"an empty column", "default", StatusReview, true, 0, 1024},
		{"an empty column below", "default", StatusReady, false, 0, 1024},
		{"another profile's empty column", other, StatusInbox, true, 0, 1024},
	} {
		got, err := s.edgePosition(bg, db, k.profile, k.status, k.top, k.except)
		if err != nil || got != k.want {
			t.Errorf("%s: %d, %v, want %d", k.name, got, err, k.want)
		}
	}
}
