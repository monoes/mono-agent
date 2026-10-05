package main

// `api config unset --all` is the way out of a saved row that cannot be read: it removes the
// row and says so, where every other command stops at the damage and names it. A row written by
// a newer version is the exception: nothing here removes it.

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/storage"
)

// putRowAt writes the saved row as it is, in the database the commands of the test open.
func putRowAt(t *testing.T, dbPath, value string) {
	t.Helper()
	st, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, apiconfig.Row, value); err != nil {
		t.Fatal(err)
	}
}

// rowAt is the stored text of the saved row, and whether there is one.
func rowAt(t *testing.T, dbPath string) (string, bool) {
	t.Helper()
	st, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var v string
	err = st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, apiconfig.Row).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, true
}

var unreadableRows = map[string]string{
	"not json":              `{"v":1,`,
	"a version of zero":     `{"v":0}`,
	"a field of a bad type": `{"v":1,"max_concurrent":true}`,
	"an empty value":        ``,
}

func TestAPIConfigUnsetAllRemovesAnUnreadableRowAndSaysSo(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := configTest(t)
			useInstaller(t, &fakeAutostart{})

			// The text: what was removed on stdout, and the note on stderr.
			putRowAt(t, db, doc)
			out, stderr, err := runAPI(t, db, "default", false, "config", "unset", "--all")
			if err != nil {
				t.Fatalf("exit %d: %v", exitCodeFor(err), err)
			}
			if !strings.Contains(out, "Removed") || !strings.Contains(out, "could not be read") || strings.Contains(out, "Nothing changed") {
				t.Errorf("stdout:\n%s", out)
			}
			if !strings.Contains(stderr, "Note:") || !strings.Contains(stderr, "could not be read") || !strings.Contains(stderr, "removed") || !strings.Contains(stderr, "gone") {
				t.Errorf("the note on stderr:\n%s", stderr)
			}
			if v, ok := rowAt(t, db); ok {
				t.Errorf("the row is still there: %q", v)
			}
			if _, _, err := runAPI(t, db, "default", false, "config", "show"); err != nil {
				t.Errorf("show after the repair: %v", err)
			}

			// The document: the field, and nothing on stderr.
			putRowAt(t, db, doc)
			out, stderr, err = runAPI(t, db, "default", true, "config", "unset", "--all")
			if err != nil {
				t.Fatalf("exit %d: %v", exitCodeFor(err), err)
			}
			r := decodeChange(t, out)
			if !r.Applied || !r.RemovedUnreadableRow || len(r.Changed) != 0 || len(r.Widening) != 0 || !strings.Contains(out, `"removed_unreadable_row": true`) {
				t.Errorf("applied %v, removed %v, changed %v, widening %v\n%s", r.Applied, r.RemovedUnreadableRow, r.Changed, r.Widening, out)
			}
			if stderr != "" {
				t.Errorf("--json prints the document and nothing else: %q", stderr)
			}
			if _, ok := rowAt(t, db); ok {
				t.Error("the row is still there")
			}
		})
	}
}

func TestAPIConfigUnsetAllOfAReadableRowSaysNothingOfRepair(t *testing.T) {
	db := configTest(t)
	useInstaller(t, &fakeAutostart{})
	saveAt(t, db, "max_concurrent=8")
	out, stderr, err := runAPI(t, db, "default", true, "config", "unset", "--all")
	if err != nil || strings.Contains(out, "removed_unreadable_row") || stderr != "" {
		t.Errorf("%v\n%s\n%s", err, out, stderr)
	}
	text, stderr, err := runAPI(t, db, "default", false, "config", "unset", "--all")
	if err != nil || strings.Contains(text, "could not be read") || strings.Contains(stderr, "Note:") {
		t.Errorf("%v\n%s\n%s", err, text, stderr)
	}
}

func TestAPIConfigUnsetAllDryRunOfAnUnreadableRowRemovesNothing(t *testing.T) {
	db := configTest(t)
	useInstaller(t, &fakeAutostart{})
	putRowAt(t, db, `nonsense`)
	out, _, err := runAPI(t, db, "default", true, "config", "unset", "--all", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if r := decodeChange(t, out); r.Applied || !r.RemovedUnreadableRow {
		t.Errorf("applied %v, removed %v", r.Applied, r.RemovedUnreadableRow)
	}
	text, stderr, err := runAPI(t, db, "default", false, "config", "unset", "--all", "--dry-run")
	if err != nil || !strings.Contains(text, "Dry run") || !strings.Contains(text, "would remove") || stderr != "" {
		t.Errorf("%v\n%s\n%s", err, text, stderr)
	}
	if v, ok := rowAt(t, db); !ok || v != `nonsense` {
		t.Errorf("a dry run touched the row: %q, %v", v, ok)
	}
}

// Everything but unset all stops at the damage, in exit 1, and says what to run.
func TestAPIConfigCommandsStopAtAnUnreadableRowAndNameTheRecovery(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := configTest(t)
			useInstaller(t, &fakeAutostart{})
			putRowAt(t, db, doc)
			for _, args := range [][]string{
				{"config", "show"},
				{"config", "set", "--max-concurrent", "8"},
				{"config", "set", "--max-concurrent", "8", "--dry-run"},
				{"config", "unset", "max_concurrent"},
				{"config", "unset", "max_concurrent", "--dry-run"},
			} {
				for _, jsonOut := range []bool{true, false} {
					out, _, err := runAPI(t, db, "default", jsonOut, args...)
					if err == nil || exitCodeFor(err) != 1 || out != "" {
						t.Fatalf("%v (json %v): exit %d, stdout %q, %v", args, jsonOut, exitCodeFor(err), out, err)
					}
					if !strings.Contains(err.Error(), "monoagentcli api config unset --all") || !strings.Contains(err.Error(), apiconfig.Row) {
						t.Errorf("%v: the message %q must name the row and the recovery", args, err)
					}
				}
			}
			if v, ok := rowAt(t, db); !ok || v != doc {
				t.Errorf("the row was touched: %q, %v", v, ok)
			}
		})
	}
}

// A row written by a newer version is not removed by anything here, unset all included, and
// the message says what to do instead.
func TestAPIConfigNeverRemovesARowOfANewerFormat(t *testing.T) {
	db := configTest(t)
	useInstaller(t, &fakeAutostart{})
	const newer = `{"v":2,"v1_addr":":9443","later":{"x":1}}`
	putRowAt(t, db, newer)
	for _, args := range [][]string{
		{"config", "unset", "--all"},
		{"config", "unset", "--all", "--yes"},
		{"config", "unset", "--all", "--dry-run"},
		{"config", "unset", "v1_addr"},
		{"config", "set", "--max-concurrent", "8"},
		{"config", "show"},
	} {
		out, _, err := runAPI(t, db, "default", true, args...)
		if err == nil || exitCodeFor(err) != 1 || out != "" {
			t.Fatalf("%v: exit %d, stdout %q, %v", args, exitCodeFor(err), out, err)
		}
		for _, want := range []string{"monoagentcli that wrote it", apiconfig.RemoveRowSQL, "--db-path"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the message %q must say %q", args, err, want)
			}
		}
		if strings.Contains(err.Error(), "unset --all") {
			t.Errorf("%v: the message offers what would not work: %q", args, err)
		}
	}
	if v, ok := rowAt(t, db); !ok || v != newer {
		t.Errorf("the row was touched: %q, %v", v, ok)
	}
}

// The server does not start on a row it cannot read, and says how to repair it.
func TestTheServerRefusesAnUnreadableRowAndNamesTheRecovery(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := savedAPIDB(t, nil, nil)
			if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, doc); err != nil {
				t.Fatal(err)
			}
			_, err := newAPIRuntime(db, apiFlags{}, func(string, ...any) {})
			if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "monoagentcli api config unset --all") {
				t.Errorf("exit %d, %v", exitCode(err), err)
			}
		})
	}
}
