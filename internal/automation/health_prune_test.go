package automation

import (
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"
)

func insertHealthRow(t *testing.T, db *sql.DB, id, key string, updated time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO automation_selector_health (automation_id, selector_key, ok_count, updated_at)
		VALUES (?, ?, 1, ?)`, id, key, formatHealthTime(updated)); err != nil {
		t.Fatal(err)
	}
}

func healthKeys(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := LoadSelectorHealth(db, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.AutomationID+"/"+r.Key)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestHealthPruneStaleRows(t *testing.T) {
	db := healthTestDB(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	insertHealthRow(t, db, "a", "kept", old)        // declared: kept however old
	insertHealthRow(t, db, "a", "gone", old)        // undeclared and old: pruned
	insertHealthRow(t, db, "a", "gone-recent", now) // undeclared but recent: kept (doctor shows it stale)
	insertHealthRow(t, db, "b", "whatever", old)    // automation unknown: never pruned
	keysOf := func(id string) (map[string]bool, bool) {
		if id == "a" {
			return map[string]bool{"kept": true}, true
		}
		return nil, false
	}
	n, err := PruneStaleSelectorHealth(db, keysOf, now.Add(-staleRowTTL))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if got := healthKeys(t, db); got != "a/gone-recent,a/kept,b/whatever" {
		t.Fatalf("rows after prune: %s", got)
	}
}

func TestHealthRecorderPrunesOnFlushOncePerInterval(t *testing.T) {
	db := healthTestDB(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	insertHealthRow(t, db, "a", "gone", old)
	calls := 0
	keysOf := func(id string) (map[string]bool, bool) {
		calls++
		return map[string]bool{"k": true}, true
	}
	r := NewHealthRecorder(db, HealthOptions{Interval: time.Hour, SelectorKeys: keysOf})
	defer r.Close()
	r.ObserveSelector("a", "k", 0, true, false)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := healthKeys(t, db); got != "a/k" {
		t.Fatalf("rows after first flush: %s", got)
	}
	// Within pruneInterval a second flush does not prune again.
	insertHealthRow(t, db, "a", "gone2", old)
	before := calls
	r.ObserveSelector("a", "k", 0, true, false)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if calls != before || healthKeys(t, db) != "a/gone2,a/k" {
		t.Fatalf("pruned twice within the interval (calls %d→%d, rows %s)", before, calls, healthKeys(t, db))
	}
}
