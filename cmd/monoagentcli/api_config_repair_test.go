package main

// A saved row that cannot be read is invalid saved data the user can fix: every command that
// reads it stops with exit 3 and one message that starts the same way, whatever is wrong with
// the row, and names the repair and its gate: `api config unset --all --yes`, which removes it
// and says so. Without --yes that is a widening of unknown size (what the row limited cannot be
// told), like any other. A row written by a newer version is exit 1 and never offered a reset,
// and a failing database is never taken for damage.

import (
	"context"
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

// The two kinds of damage, and more of each: a row that is not JSON at all, and one that is JSON
// this version refuses.
var unreadableRows = map[string]string{
	"not json":               `{"v":1,`,
	"an empty value":         ``,
	"a version of zero":      `{"v":0}`,
	"a field of a bad type":  `{"v":1,"max_concurrent":true}`,
	"a value in broken text": `{"v":1,"v1_addr":"supersecret.example:9443",`,
}

const damagedStart = "the saved settings are damaged"
const repairWithItsGate = "`monoagentcli api config unset --all --yes` removes them"

// Every command that reads the row, in text and in JSON, with what a caller that has only the
// exit code and the last line sees.
var readersOfTheRow = [][]string{
	{"config", "show"},
	{"config", "set", "--max-concurrent", "8"},
	{"config", "set", "--max-concurrent", "8", "--dry-run"},
	{"config", "unset", "max_concurrent"},
	{"config", "unset", "max_concurrent", "--dry-run"},
	{"status"},
	{"models"},
}

func TestAPIConfigUnsetAllWithYesRemovesAnUnreadableRowAndSaysSo(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := configTest(t)
			useInstaller(t, &fakeAutostart{})

			// The text: what was removed and why it needed --yes on stdout, and the note on stderr.
			putRowAt(t, db, doc)
			out, stderr, err := runAPI(t, db, "default", false, "config", "unset", "--all", "--yes")
			if err != nil {
				t.Fatalf("exit %d: %v", exitCodeFor(err), err)
			}
			for _, want := range []string{"Removed", "could not be read", "confirmed with --yes", "The saved settings cannot be read"} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout must say %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Nothing changed") {
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

			// The document: the field and the widening, and nothing on stderr.
			putRowAt(t, db, doc)
			out, stderr, err = runAPI(t, db, "default", true, "config", "unset", "--all", "--yes")
			if err != nil {
				t.Fatalf("exit %d: %v", exitCodeFor(err), err)
			}
			r := decodeChange(t, out)
			if !r.Applied || !r.RemovedUnreadableRow || len(r.Changed) != 0 || len(r.Widening) != 1 || r.Widening[0].Key != "saved_settings" || !strings.Contains(out, `"removed_unreadable_row": true`) {
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

// Without --yes the removal is a widening of unknown size: exit 3 with the one reason, as any
// widening, and nothing is written.
func TestAPIConfigUnsetAllWithoutYesIsRefusedForAnUnreadableRow(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := configTest(t)
			useInstaller(t, &fakeAutostart{})
			putRowAt(t, db, doc)
			for _, jsonOut := range []bool{true, false} {
				out, _, err := runAPI(t, db, "default", jsonOut, "config", "unset", "--all")
				if err == nil || exitCodeFor(err) != 3 || out != "" {
					t.Fatalf("json %v: exit %d, stdout %q, %v", jsonOut, exitCodeFor(err), out, err)
				}
				for _, want := range []string{"reach further", "The saved settings cannot be read, so what they limited cannot be told", "--yes"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("json %v: the message %q must say %q", jsonOut, err, want)
					}
				}
				if strings.Contains(err.Error(), "supersecret") {
					t.Errorf("the message repeats what the row held: %q", err)
				}
			}
			if v, ok := rowAt(t, db); !ok || v != doc {
				t.Errorf("a refused change wrote: %q, %v", v, ok)
			}
		})
	}
}

func TestAPIConfigUnsetAllOfAReadableRowSaysNothingOfRepair(t *testing.T) {
	db := configTest(t)
	useInstaller(t, &fakeAutostart{})
	saveAt(t, db, "max_concurrent=8")
	out, stderr, err := runAPI(t, db, "default", true, "config", "unset", "--all")
	if err != nil || strings.Contains(out, "removed_unreadable_row") || strings.Contains(out, "saved_settings") || stderr != "" {
		t.Errorf("%v\n%s\n%s", err, out, stderr)
	}
	text, stderr, err := runAPI(t, db, "default", false, "config", "unset", "--all")
	if err != nil || strings.Contains(text, "could not be read") || strings.Contains(stderr, "Note:") {
		t.Errorf("%v\n%s\n%s", err, text, stderr)
	}
}

// A dry run says what it would do, reasons included, whether or not --yes is given.
func TestAPIConfigUnsetAllDryRunOfAnUnreadableRowRemovesNothing(t *testing.T) {
	db := configTest(t)
	useInstaller(t, &fakeAutostart{})
	putRowAt(t, db, `nonsense`)
	for _, args := range [][]string{{"config", "unset", "--all", "--dry-run"}, {"config", "unset", "--all", "--dry-run", "--yes"}} {
		out, _, err := runAPI(t, db, "default", true, args...)
		if err != nil {
			t.Fatal(err)
		}
		if r := decodeChange(t, out); r.Applied || !r.RemovedUnreadableRow || len(r.Widening) != 1 || r.Widening[0].Key != "saved_settings" {
			t.Errorf("%v: applied %v, removed %v, widening %v", args, r.Applied, r.RemovedUnreadableRow, r.Widening)
		}
		text, stderr, err := runAPI(t, db, "default", false, args...)
		if err != nil || !strings.Contains(text, "Dry run") || !strings.Contains(text, "would remove") || !strings.Contains(text, "needs --yes") ||
			!strings.Contains(text, "The saved settings cannot be read") || stderr != "" {
			t.Errorf("%v: %v\n%s\n%s", args, err, text, stderr)
		}
	}
	if v, ok := rowAt(t, db); !ok || v != `nonsense` {
		t.Errorf("a dry run touched the row: %q, %v", v, ok)
	}
}

// Every command that reads the row stops at the damage with exit 3, which is invalid saved
// data the user can fix, and one message that starts the same way for every kind of damage.
func TestAPIConfigCommandsStopAtADamagedRowWithExit3AndOneMessage(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := configTest(t)
			fakeAPIMonomind(t)
			useInstaller(t, &fakeAutostart{})
			putRowAt(t, db, doc)
			for _, args := range readersOfTheRow {
				for _, jsonOut := range []bool{true, false} {
					out, _, err := runAPI(t, db, "default", jsonOut, args...)
					if err == nil || exitCodeFor(err) != 3 || out != "" {
						t.Fatalf("%v (json %v): exit %d, stdout %q, %v", args, jsonOut, exitCodeFor(err), out, err)
					}
					msg := err.Error()
					if !strings.HasPrefix(msg, damagedStart) || !strings.HasPrefix(msg, apiconfig.DamagedMessage) {
						t.Errorf("%v: the message must start with %q: %q", args, damagedStart, msg)
					}
					if !strings.Contains(msg, repairWithItsGate) || !strings.Contains(msg, apiconfig.Row) {
						t.Errorf("%v: the message %q must name the row and the repair with its gate", args, msg)
					}
					if strings.Contains(msg, "supersecret") || strings.Contains(msg, "\n") {
						t.Errorf("%v: the message repeats what the row held, or is not one line: %q", args, msg)
					}
				}
			}
			if v, ok := rowAt(t, db); !ok || v != doc {
				t.Errorf("the row was touched: %q, %v", v, ok)
			}
		})
	}
}

// A row written by a newer version is not damaged: exit 1, no offer of a reset, and nothing here
// removes it, unset --all with --yes included. The message says what to do instead.
func TestAPIConfigNeverRemovesARowOfANewerFormat(t *testing.T) {
	db := configTest(t)
	fakeAPIMonomind(t)
	useInstaller(t, &fakeAutostart{})
	const newer = `{"v":2,"v1_addr":":9443","later":{"x":1}}`
	putRowAt(t, db, newer)
	for _, args := range append([][]string{
		{"config", "unset", "--all"},
		{"config", "unset", "--all", "--yes"},
		{"config", "unset", "--all", "--dry-run"},
	}, readersOfTheRow...) {
		out, _, err := runAPI(t, db, "default", true, args...)
		if err == nil || exitCodeFor(err) != 1 || out != "" {
			t.Fatalf("%v: exit %d, stdout %q, %v", args, exitCodeFor(err), out, err)
		}
		for _, want := range []string{"monoagentcli that wrote it", apiconfig.RemoveRowSQL, "--db-path"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the message %q must say %q", args, err, want)
			}
		}
		if strings.Contains(err.Error(), "unset --all") || strings.HasPrefix(err.Error(), damagedStart) {
			t.Errorf("%v: a newer row is not damaged and is not offered a reset: %q", args, err)
		}
	}
	if v, ok := rowAt(t, db); !ok || v != newer {
		t.Errorf("the row was touched: %q, %v", v, ok)
	}
}

// The mapping is in one place, and only damage goes to exit 3: a failing database, a newer row
// and any other error keep the exit they had, and the ones that were invalid input still are.
func TestAsCLIErrorMapsDamageToExit3AndOnlyDamage(t *testing.T) {
	ctx := context.Background()
	damaged := savedAPIDB(t, nil, nil)
	if _, err := damaged.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, `not json`); err != nil {
		t.Fatal(err)
	}
	_, errDamaged := apiconfig.Load(ctx, damaged)
	newer := savedAPIDB(t, nil, nil)
	if _, err := newer.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, `{"v":2}`); err != nil {
		t.Fatal(err)
	}
	_, errNewer := apiconfig.Load(ctx, newer)
	closed := savedAPIDB(t, nil, nil)
	closed.Close()
	_, errDatabase := apiconfig.Load(ctx, closed)
	_, errInvalid := apiconfig.LoadValid(ctx, func() *sql.DB {
		db := savedAPIDB(t, nil, nil)
		if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, `{"v":1,"max_concurrent":99}`); err != nil {
			t.Fatal(err)
		}
		return db
	}())

	for _, c := range []struct {
		name string
		err  error
		code int
	}{
		{"a damaged row", errDamaged, 3},
		{"a row in a newer format", errNewer, 1},
		{"a failing database", errDatabase, 1},
		{"an invalid value, which was exit 3 already", errInvalid, 3},
		{"any other error", errors.New("some other failure"), 1},
	} {
		if c.err == nil {
			t.Fatalf("%s: no error to map", c.name)
		}
		got := asCLIError(c.err)
		if exitCodeFor(got) != c.code {
			t.Errorf("%s: exit %d for %v, want %d", c.name, exitCodeFor(got), got, c.code)
		}
		if got.Error() != c.err.Error() {
			t.Errorf("%s: the mapping changed the message: %q became %q", c.name, c.err, got)
		}
	}
	if errors.Is(errDatabase, apiconfig.ErrDamaged) || errors.Is(errNewer, apiconfig.ErrDamaged) {
		t.Error("a failing database or a newer row is taken for damage")
	}
}

// The server does not start on a row it cannot read: exit 3 and the same message.
func TestTheServerRefusesADamagedRowWithExit3(t *testing.T) {
	for name, doc := range unreadableRows {
		t.Run(name, func(t *testing.T) {
			db := savedAPIDB(t, nil, nil)
			if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, doc); err != nil {
				t.Fatal(err)
			}
			_, err := newAPIRuntime(db, apiFlags{}, func(string, ...any) {})
			if err == nil || exitCode(err) != 3 || !strings.HasPrefix(err.Error(), damagedStart) || !strings.Contains(err.Error(), repairWithItsGate) {
				t.Errorf("exit %d, %v", exitCode(err), err)
			}
		})
	}
}
