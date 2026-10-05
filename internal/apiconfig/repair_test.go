package apiconfig

// The repair of a saved row that cannot be read. Unset all removes it, but only with
// confirmation: what the row limited cannot be told, so removing it is a widening of unknown
// size (the key saved_settings). Without confirmation it is a *WideningError and nothing is
// written; with a dry run it says what it would do; set and unset of a key refuse the row and
// name the recovery; a row written by a newer version is never removed. The helpers are those of
// store_test.go and apply_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

const unsetAll = "monoagentcli api config unset --all"

// The widening that removing a row that cannot be read is, as every surface shows it.
var unreadableWidening = Widening{
	Key:    "saved_settings",
	Reason: "The saved settings cannot be read, so what they limited cannot be told: removing them returns every setting to its default, which may reach further.",
}

// Confirmed, unset all removes the row, says so, and lists the widening it is.
func TestConfirmedUnsetAllRemovesARowThatCannotBeRead(t *testing.T) {
	ctx := context.Background()
	for name, doc := range damagedDocuments {
		t.Run(name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			env := shellEnv(nil, nil, &fakeInstaller{})
			r, err := Apply(ctx, db, env, Change{All: true, Confirm: true})
			if err != nil {
				t.Fatalf("a confirmed unset all was refused for the damage: %v", err)
			}
			if !r.Applied || !r.RemovedUnreadableRow || r.Changed == nil || len(r.Changed) != 0 || len(r.Problems) != 0 {
				t.Errorf("applied %v, removed %v, changed %v, problems %v", r.Applied, r.RemovedUnreadableRow, r.Changed, r.Problems)
			}
			if len(r.Widening) != 1 || r.Widening[0] != unreadableWidening {
				t.Errorf("widening %+v, want the one for a row that cannot be read", r.Widening)
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

// Not confirmed, it is refused with the one reason, and nothing is written or asked of the
// service manager. The reason says nothing of what the row held.
func TestUnsetAllOfAnUnreadableRowNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	for name, doc := range damagedDocuments {
		t.Run(name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			inst := &fakeInstaller{}
			_, err := Apply(ctx, db, shellEnv(nil, nil, inst), Change{All: true})
			var we *WideningError
			if !errors.As(err, &we) || len(we.Widening) != 1 || we.Widening[0] != unreadableWidening {
				t.Fatalf("%v, want a *WideningError with the one reason for a row that cannot be read", err)
			}
			for _, want := range []string{"reach further", "cannot be read", "may reach further"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the message %q must say %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "supersecret") {
				t.Errorf("the message repeats what the row held: %q", err)
			}
			if v, ok := row(t, db); !ok || v != doc {
				t.Errorf("a refused change wrote: %q, %v", v, ok)
			}
			if inst.statusCalls != 0 || inst.restartCalls != 0 {
				t.Errorf("the service manager was asked %d times and to restart %d times", inst.statusCalls, inst.restartCalls)
			}
		})
	}
}

// A dry run says what it would do, widening included, whether or not it was confirmed, and
// removes nothing.
func TestUnsetAllOfAnUnreadableRowDryRunSaysWhatItWouldDo(t *testing.T) {
	ctx := context.Background()
	for name, doc := range damagedDocuments {
		t.Run(name, func(t *testing.T) {
			for _, confirm := range []bool{false, true} {
				db := openDB(t)
				putRow(t, db, doc)
				r, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true, DryRun: true, Confirm: confirm})
				if err != nil || r.Applied || !r.RemovedUnreadableRow || len(r.Widening) != 1 || r.Widening[0] != unreadableWidening {
					t.Fatalf("confirm %v: applied %v, removed %v, widening %+v, %v", confirm, r.Applied, r.RemovedUnreadableRow, r.Widening, err)
				}
				if v, ok := row(t, db); !ok || v != doc {
					t.Errorf("confirm %v: a dry run touched the row: %q, %v", confirm, v, ok)
				}
			}
		})
	}
}

// Only unset all repairs. Set and unset of a key stop at the damage, in a message that starts
// the same way every time and names the repair and its gate, and leave the row as it was,
// confirmed or not.
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
				if !strings.HasPrefix(err.Error(), DamagedMessage) {
					t.Errorf("%s: the message must start with %q: %q", what, DamagedMessage, err)
				}
				if !strings.Contains(err.Error(), unsetAll+" --yes") {
					t.Errorf("%s: the message %q must name the repair and its gate, %q", what, err, unsetAll+" --yes")
				}
				if strings.Contains(err.Error(), "supersecret") {
					t.Errorf("%s: the message repeats what the row held: %q", what, err)
				}
				if v, ok := row(t, db); !ok || v != doc {
					t.Fatalf("%s: the row was touched: %q, %v", what, v, ok)
				}
			}
		})
	}
}

// A row a newer version wrote is the exception: nothing here removes it, confirmed or not,
// because it would lose what that version saved. The message says to use that version, or to
// remove the row by hand.
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
				var we *WideningError
				if errors.As(err, &we) {
					t.Errorf("%s: a newer row is not a widening to confirm: %v", what, err)
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

// A failing database is not damage: nothing is removed on its account, confirmed or not.
func TestUnsetAllOfADatabaseThatFailsIsAnErrorAndNotARepair(t *testing.T) {
	db := openDB(t)
	db.Close()
	for _, confirm := range []bool{false, true} {
		r, err := Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true, Confirm: confirm})
		var we *WideningError
		if err == nil || errors.Is(err, ErrDamaged) || errors.Is(err, ErrTooNew) || errors.As(err, &we) || r.RemovedUnreadableRow {
			t.Errorf("confirm %v: %v, removed %v", confirm, err, r.RemovedUnreadableRow)
		}
	}
}

// A row that can be read is not "unreadable", however much is wrong with its values: the
// ordinary rules apply, the gate among them, and the key of the unreadable row never shows.
func TestUnsetAllOfARowThatCanBeReadIsNotARepair(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	putRow(t, db, `{"v":1,"confinement":"everything","max_concurrent":8,"later":1}`) // an invalid value, a field of a later version
	r, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
	if err != nil || r.RemovedUnreadableRow || len(r.Widening) != 0 || strings.Join(r.Changed, ",") != "confinement,max_concurrent" {
		t.Fatalf("removed %v, widening %+v, changed %v, %v: an invalid value needs no confirmation", r.RemovedUnreadableRow, r.Widening, r.Changed, err)
	}
	if v, _ := row(t, db); v != `{"later":1,"v":1}` {
		t.Errorf("what is left: %s (the fields of a later version are kept)", v)
	}
	// The same with the gate: a confinement of chat-only is below its default, so removing it needs confirmation.
	putRow(t, db, `{"v":1,"confinement":"chat-only"}`)
	var we *WideningError
	if _, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true}); !errors.As(err, &we) || len(we.Widening) != 1 || we.Widening[0].Key != "confinement.loopback" {
		t.Errorf("a readable row keeps the ordinary gate: %v", err)
	}
	// And nothing saved is nothing to confirm.
	if _, err := db.Exec(`DELETE FROM settings WHERE key = ?`, Row); err != nil {
		t.Fatal(err)
	}
	r, err = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
	if err != nil || !r.Applied || r.RemovedUnreadableRow || len(r.Widening) != 0 || len(r.Changed) != 0 {
		t.Errorf("nothing saved: %+v, %v", r, err)
	}
}

// The document says so with one field, present only when it is true, after the three of set and
// unset.
func TestTheChangeDocumentSaysWhenItRemovedAnUnreadableRow(t *testing.T) {
	r := ChangeResult{
		ConfigReport: ConfigReport{V: 1, Environment: "shell", Settings: []SettingReport{}, Problems: []Problem{}},
		Applied:      true, Changed: []string{}, Widening: []Widening{unreadableWidening}, RemovedUnreadableRow: true,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"environment":"shell","settings":[],"daemon":{"running":false,"reports_settings":false,"autostart":false},"restart_needed":false,"problems":[],` +
		`"applied":true,"changed":[],"widening":[{"key":"saved_settings","reason":"The saved settings cannot be read, so what they limited cannot be told: ` +
		`removing them returns every setting to its default, which may reach further."}],"removed_unreadable_row":true}`
	if string(b) != want {
		t.Errorf("the document:\n got %s\nwant %s", b, want)
	}
	r.RemovedUnreadableRow = false
	if b, _ = json.Marshal(r); strings.Contains(string(b), "removed_unreadable_row") {
		t.Errorf("the field is there when nothing was removed: %s", b)
	}
}

// Confirmed unset all against set on a row that cannot be read: they take turns. Unset all is
// never refused; a set that comes first is refused for the damage, and one that comes second
// lands. Either way the row ends up readable.
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
			res, errAll = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true, Confirm: true})
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

// Two confirmed unset alls on the same damaged row: one finds it and removes it, the others find
// nothing, and none is refused.
func TestTwoConfirmedUnsetAllsOnAnUnreadableRowRemoveItOnce(t *testing.T) {
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
				r, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true, Confirm: true})
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

// Two unset alls that are not confirmed: every one is refused, and the row is as it was.
func TestTwoUnconfirmedUnsetAllsOnAnUnreadableRowAreBothRefused(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		putRow(t, db, `not json`)
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for k := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[k] = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
			}()
		}
		wg.Wait()
		for k, err := range errs {
			var we *WideningError
			if !errors.As(err, &we) {
				t.Fatalf("round %d: unset all %d answered %v, want it refused", i, k, err)
			}
		}
		if v, ok := row(t, db); !ok || v != `not json` {
			t.Fatalf("round %d: the row was touched: %q, %v", i, v, ok)
		}
	}
}

// A confirmed repair, one that is not confirmed and a set, all at once on a damaged row. Only the
// confirmed one can remove it, so the unconfirmed one is either refused (it came first) or finds
// the row gone and works on what is saved then; it never removes a damaged row. The set is
// refused if it came first and lands if it came after. The row ends up readable.
func TestARepairAnUnconfirmedOneAndASetRaceOnAnUnreadableRow(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		putRow(t, db, `{"v":1,`)
		var wg sync.WaitGroup
		var errConfirmed, errBare, errSet error
		var confirmed, bare ChangeResult
		wg.Add(3)
		go func() {
			defer wg.Done()
			confirmed, errConfirmed = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true, Confirm: true})
		}()
		go func() {
			defer wg.Done()
			bare, errBare = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{All: true})
		}()
		go func() {
			defer wg.Done()
			_, errSet = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{"max_concurrent": "8"}})
		}()
		wg.Wait()
		if errConfirmed != nil || !confirmed.RemovedUnreadableRow {
			t.Fatalf("round %d: the confirmed unset all: removed %v, %v", i, confirmed.RemovedUnreadableRow, errConfirmed)
		}
		var we *WideningError
		if errBare != nil && !errors.As(errBare, &we) {
			t.Fatalf("round %d: the unconfirmed unset all answered %v", i, errBare)
		}
		if errBare == nil && bare.RemovedUnreadableRow {
			t.Fatalf("round %d: an unset all that was not confirmed removed a row that cannot be read", i)
		}
		if errSet != nil && !errors.Is(errSet, ErrDamaged) {
			t.Fatalf("round %d: the set answered %v", i, errSet)
		}
		saved, err := Load(ctx, db)
		if err != nil || (saved.MaxConcurrent != "" && saved.MaxConcurrent != "8") {
			t.Fatalf("round %d: %+v, %v", i, saved, err)
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
