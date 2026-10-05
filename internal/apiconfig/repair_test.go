package apiconfig

// The repair of a saved row that cannot be read: unset all removes it and says so, and is never
// refused for it; set and unset of a key refuse it and name the recovery; a row written by a
// newer version is never removed. The helpers are those of store_test.go and apply_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

const unsetAll = "monoagentcli api config unset --all"

func TestUnsetAllRemovesARowThatCannotBeRead(t *testing.T) {
	ctx := context.Background()
	for name, doc := range damagedDocuments {
		t.Run(name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			env := shellEnv(nil, nil, &fakeInstaller{})
			r, err := Apply(ctx, db, env, Change{All: true})
			if err != nil {
				t.Fatalf("unset all was refused for the damage: %v", err)
			}
			if !r.Applied || !r.RemovedUnreadableRow || r.Changed == nil || len(r.Changed) != 0 || r.Widening == nil || len(r.Widening) != 0 || len(r.Problems) != 0 {
				t.Errorf("applied %v, removed %v, changed %v, widening %v, problems %v", r.Applied, r.RemovedUnreadableRow, r.Changed, r.Widening, r.Problems)
			}
			if v, ok := row(t, db); ok {
				t.Errorf("the row is still there: %q", v)
			}
			// What reads the settings reads them again, from nothing.
			if saved, err := Load(ctx, db); err != nil || !saved.IsEmpty() {
				t.Errorf("Load after the repair: %+v, %v", saved, err)
			}
			if show, err := Show(ctx, db, env); err != nil || len(show.Problems) != 0 {
				t.Errorf("Show after the repair: %+v, %v", show.Problems, err)
			}
		})
	}
}

// With a dry run it says it would, and removes nothing.
func TestUnsetAllOfAnUnreadableRowDryRunRemovesNothing(t *testing.T) {
	ctx := context.Background()
	for name, doc := range damagedDocuments {
		t.Run(name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			r, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true, DryRun: true})
			if err != nil || r.Applied || !r.RemovedUnreadableRow {
				t.Fatalf("applied %v, removed %v, %v", r.Applied, r.RemovedUnreadableRow, err)
			}
			if v, ok := row(t, db); !ok || v != doc {
				t.Errorf("a dry run touched the row: %q, %v", v, ok)
			}
		})
	}
}

// Only unset all repairs. Set and unset of a key stop at the damage, say what to run, and leave
// the row as it was.
func TestSetAndUnsetOfAKeyRefuseAnUnreadableRowAndNameTheRecovery(t *testing.T) {
	ctx := context.Background()
	for name, doc := range damagedDocuments {
		t.Run(name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			for what, ch := range map[string]Change{
				"set":            {Set: map[string]string{"max_concurrent": "8"}},
				"set, dry run":   {Set: map[string]string{"max_concurrent": "8"}, DryRun: true},
				"set, confirmed": {Set: map[string]string{"max_concurrent": "8"}, Confirm: true},
				"unset a key":    {Unset: []string{"max_concurrent"}},
				"unset, dry run": {Unset: []string{"turn_timeout"}, DryRun: true},
				"unset a few":    {Unset: []string{"max_concurrent", "turn_timeout"}, Confirm: true},
			} {
				_, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), ch)
				if !errors.Is(err, ErrDamaged) || errors.Is(err, ErrTooNew) {
					t.Fatalf("%s: %v, want ErrDamaged", what, err)
				}
				if !strings.Contains(err.Error(), unsetAll) {
					t.Errorf("%s: the message %q must name the recovery", what, err)
				}
				if v, ok := row(t, db); !ok || v != doc {
					t.Fatalf("%s: the row was touched: %q, %v", what, v, ok)
				}
			}
		})
	}
}

// A row a newer version wrote is the exception: nothing here removes it, because it would lose
// what that version saved. The message says to use that version, or to remove the row by hand.
func TestNothingRemovesARowOfANewerFormat(t *testing.T) {
	ctx := context.Background()
	for _, doc := range []string{`{"v":2}`, `{"v":2,"v1_addr":":9443","later":{"x":1}}`, `{"v":99,"max_concurrent":true}`} {
		t.Run(doc, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			for what, ch := range map[string]Change{
				"unset all":            {All: true},
				"unset all, confirmed": {All: true, Confirm: true},
				"unset all, dry run":   {All: true, DryRun: true},
				"unset a key":          {Unset: []string{"max_concurrent"}},
				"set":                  {Set: map[string]string{"max_concurrent": "8"}},
			} {
				_, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), ch)
				if !errors.Is(err, ErrTooNew) || errors.Is(err, ErrDamaged) {
					t.Fatalf("%s: %v, want ErrTooNew", what, err)
				}
				for _, want := range []string{"monoagentcli that wrote it", RemoveRowSQL} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%s: the message %q must say %q", what, err, want)
					}
				}
				if strings.Contains(err.Error(), unsetAll) {
					t.Errorf("%s: the message offers what would not work: %q", what, err)
				}
				if v, ok := row(t, db); !ok || v != doc {
					t.Fatalf("%s: the row was touched: %q, %v", what, v, ok)
				}
			}
		})
	}
}

// A failing database is not damage: nothing is removed on its account.
func TestUnsetAllOfADatabaseThatFailsIsAnErrorAndNotARepair(t *testing.T) {
	db := openDB(t)
	db.Close()
	r, err := Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
	if err == nil || errors.Is(err, ErrDamaged) || errors.Is(err, ErrTooNew) || r.RemovedUnreadableRow {
		t.Errorf("%v, removed %v", err, r.RemovedUnreadableRow)
	}
}

// A row that can be read is not "unreadable", however much is wrong with its values: the
// document says nothing of it, and the ordinary rules (the gate among them) apply.
func TestUnsetAllOfARowThatCanBeReadIsNotARepair(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	putRow(t, db, `{"v":1,"confinement":"everything","max_concurrent":8,"later":1}`) // an invalid value, a field of a later version
	r, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
	if err != nil || r.RemovedUnreadableRow || strings.Join(r.Changed, ",") != "confinement,max_concurrent" {
		t.Fatalf("removed %v, changed %v, %v", r.RemovedUnreadableRow, r.Changed, err)
	}
	if v, _ := row(t, db); v != `{"later":1,"v":1}` {
		t.Errorf("what is left: %s (the fields of a later version are kept)", v)
	}
	// The same with the gate: a confinement of chat-only is below its default, so removing it needs confirmation.
	putRow(t, db, `{"v":1,"confinement":"chat-only"}`)
	var we *WideningError
	if _, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true}); !errors.As(err, &we) {
		t.Errorf("a readable row keeps the gate: %v", err)
	}
}

// The document says so with one field, present only when it is true, after the three of set and
// unset.
func TestTheChangeDocumentSaysWhenItRemovedAnUnreadableRow(t *testing.T) {
	r := ChangeResult{
		ConfigReport: ConfigReport{V: 1, Environment: "shell", Settings: []SettingReport{}, Problems: []Problem{}},
		Applied:      true, Changed: []string{}, Widening: []Widening{}, RemovedUnreadableRow: true,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"environment":"shell","settings":[],"daemon":{"running":false,"reports_settings":false,"autostart":false},"restart_needed":false,"problems":[],` +
		`"applied":true,"changed":[],"widening":[],"removed_unreadable_row":true}`
	if string(b) != want {
		t.Errorf("the document:\n got %s\nwant %s", b, want)
	}
	r.RemovedUnreadableRow = false
	if b, _ = json.Marshal(r); strings.Contains(string(b), "removed_unreadable_row") {
		t.Errorf("the field is there when nothing was removed: %s", b)
	}
}

// Unset all against set on a row that cannot be read: they take turns. Unset all is never
// refused; a set that comes first is refused for the damage, and one that comes second lands.
// Either way the row ends up readable.
func TestUnsetAllRacingSetOnAnUnreadableRow(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		putRow(t, db, `{"v":1,`)
		var wg sync.WaitGroup
		var errAll, errSet error
		var res ChangeResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			res, errAll = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
		}()
		go func() {
			defer wg.Done()
			_, errSet = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{"max_concurrent": "8"}})
		}()
		wg.Wait()
		if errAll != nil || !res.RemovedUnreadableRow {
			t.Fatalf("round %d: unset all: removed %v, %v", i, res.RemovedUnreadableRow, errAll)
		}
		saved, err := Load(ctx, db)
		if err != nil {
			t.Fatalf("round %d: the row cannot be read afterwards: %v", i, err)
		}
		switch {
		case errSet == nil && saved.MaxConcurrent == "8": // unset all went first, and the set landed
		case errors.Is(errSet, ErrDamaged) && saved.IsEmpty(): // the set went first and was refused
		default:
			t.Fatalf("round %d: the set answered %v and %+v is saved", i, errSet, saved)
		}
	}
}

// Two unset alls on the same damaged row: one finds it and removes it, the other finds nothing,
// and neither is refused.
func TestTwoUnsetAllsOnAnUnreadableRowRemoveItOnce(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		putRow(t, db, `not json`)
		var wg sync.WaitGroup
		removed := make([]bool, 4)
		errs := make([]error, 4)
		for k := range removed {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
				removed[k], errs[k] = r.RemovedUnreadableRow, err
			}()
		}
		wg.Wait()
		count := 0
		for k := range removed {
			if errs[k] != nil {
				t.Fatalf("round %d: unset all %d: %v", i, k, errs[k])
			}
			if removed[k] {
				count++
			}
		}
		if _, ok := row(t, db); ok || count != 1 {
			t.Fatalf("round %d: the row is still there (%v) or was removed %d times", i, ok, count)
		}
	}
}

// Unset all against set on a row that can be read, for completeness: nothing of what unset all
// removed survives, and nothing is refused.
func TestUnsetAllRacingSetOnAReadableRow(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		putRow(t, db, `{"v":1,"max_concurrent":3}`)
		var wg sync.WaitGroup
		var errAll, errSet error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errAll = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
		}()
		go func() {
			defer wg.Done()
			_, errSet = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{"turn_timeout": "20m"}})
		}()
		wg.Wait()
		if errAll != nil || errSet != nil {
			t.Fatalf("round %d: %v, %v", i, errAll, errSet)
		}
		saved, err := Load(ctx, db)
		if err != nil || saved.MaxConcurrent != "" || (saved.TurnTimeout != "" && saved.TurnTimeout != "20m") {
			t.Fatalf("round %d: %+v, %v", i, saved, err)
		}
	}
}
