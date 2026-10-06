package tasks

import (
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// queryPlan is the plan SQLite gives a statement: the detail of each of its steps, one to a line.
func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

// Every Counts, every Board and every poll of a Watch counts the profile's cards per column, and the
// archive is the one column that has no limit: the count must seek the five columns of the board in the
// index and not read the whole range of the profile, archive included (with 100,000 archived cards a count
// that reads it takes tens of milliseconds, each time). As the count of open tasks (ops_limits_test.go), it
// is pinned by the plan of the statement the store runs, and not by a timing, which would be flaky: the
// plan must name the index, the status as well as the profile, and no scan of the table.
func TestTheCountPerColumnSeeksTheFiveColumnsAndDoesNotReadTheArchive(t *testing.T) {
	_, db, _ := newTestStore(t)
	plan := queryPlan(t, db, countsSQL, "default")
	if !strings.Contains(plan, "idx_tasks_board") || !strings.Contains(plan, "status=?") || strings.Contains(plan, "SCAN") {
		t.Errorf("the plan of the count per column:\n%s\nwant a seek in idx_tasks_board on the status as well as the profile, and no scan", plan)
	}
}

// What the counts say does not depend on the archive: the same board with 300 archived cards more, a third
// of them with a claim whose lease ran out long ago, has the same counts in Counts and in Board and the same
// board, and the statement that counts the columns has no row for the archive.
func TestTheCountsDoNotDependOnTheArchive(t *testing.T) {
	s, db, clk := newTestStore(t)
	for i, st := range []string{"inbox", "ready", "in_progress", "review", "done"} {
		for n := 0; n <= i; n++ { // 1, 2, 3, 4 and 5 cards
			seedRow(t, db, "default", st)
		}
	}
	past := clk.t.Add(-time.Hour)
	holdUntil(t, db, seedRow(t, db, "default", "in_progress"), past) // a stale claim, which counts
	counts, err := s.Counts(bg, "default")
	if err != nil {
		t.Fatal(err)
	}
	board, err := s.Board(bg, "default", 0)
	if err != nil {
		t.Fatal(err)
	}
	if counts.InProgress != 4 || counts.Stale != 1 {
		t.Fatalf("setup: counts %+v, want 4 in progress and 1 stale", counts)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		by, until := "", ""
		if i%3 == 0 {
			by, until = "bot", past.Format(timeFmt)
		}
		if _, err := tx.Exec(`INSERT INTO tasks (profile_id, title, status, position, claimed_by, claim_until, created_at, updated_at)
			VALUES ('default', 'old', 'archived', ?, ?, ?, ?, ?)`, i, by, until, rowTime, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got, err := s.Counts(bg, "default"); err != nil || got != counts {
		t.Errorf("counts %+v (err %v) with 300 archived cards more, want %+v", got, err, counts)
	}
	if got, err := s.Board(bg, "default", 0); err != nil || !reflect.DeepEqual(got, board) {
		t.Errorf("the board with 300 archived cards more (err %v):\n%+v\nwant\n%+v", err, got, board)
	}
	rows, err := db.Query(countsSQL, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		var n int
		if err := rows.Scan(&column, &n); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"inbox", "ready", "in_progress", "review", "done"}
	slices.Sort(columns)
	slices.Sort(want)
	if !slices.Equal(columns, want) {
		t.Errorf("the statement answers for the columns %v, want the five of the board and none for the archive", columns)
	}
}
