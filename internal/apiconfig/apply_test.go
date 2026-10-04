package apiconfig

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

func TestApplySetsAndReportsWhatChanged(t *testing.T) {
	db := openDB(t)
	inst := &fakeInstaller{registered: true}
	r, err := Apply(context.Background(), db, shellEnv(nil, nil, inst), Change{Set: map[string]string{"max_concurrent": "8", "turn_timeout": "20m"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Applied || strings.Join(r.Changed, ",") != "max_concurrent,turn_timeout" || r.Widening == nil || len(r.Widening) != 0 || r.Changed == nil {
		t.Errorf("applied %v, changed %v, widening %+v", r.Applied, r.Changed, r.Widening)
	}
	saved, err := Load(context.Background(), db)
	if err != nil || saved.MaxConcurrent != "8" || saved.TurnTimeout != "20m" {
		t.Fatalf("Load = %+v, %v", saved, err)
	}
	// The document is the state after the change.
	if row := rowOf(t, r.ConfigReport, "max_concurrent"); row.Saved != "8" || row.Effective != "8" || row.Source != SourceSaved || row.State != StateNotRunning {
		t.Errorf("max_concurrent: %+v", row)
	}
	if r.ConfigReport.Daemon.Autostart != true || inst.statusCalls != 1 || inst.restartCalls != 0 {
		t.Errorf("daemon %+v; the service manager was asked %d times and to restart %d times", r.Daemon, inst.statusCalls, inst.restartCalls)
	}
}

func TestApplyStoresTheCanonicalSpellingAndAcceptsDashedKeys(t *testing.T) {
	db := openDB(t)
	_, err := Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{
		"turn_timeout": "900s", "image_runtimes": "agy, Codex", "max-concurrent": "+5", "v1-addr": "127.0.0.1:9443",
	}})
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := Load(context.Background(), db)
	if saved.TurnTimeout != "15m" || saved.ImageRuntimes != "antigravity,codex" || saved.MaxConcurrent != "5" || saved.V1Addr != "127.0.0.1:9443" {
		t.Errorf("saved: %+v", saved)
	}
}

// A value that equals what is saved is no change, and a value that equals the default is
// saved as given and is no widening.
func TestApplyChangedListsOnlyWhatDiffers(t *testing.T) {
	db := openDB(t)
	env := shellEnv(nil, nil, &fakeInstaller{})
	ctx := context.Background()
	if _, err := Apply(ctx, db, env, Change{Set: map[string]string{"max_concurrent": "8"}}); err != nil {
		t.Fatal(err)
	}
	r, err := Apply(ctx, db, env, Change{Set: map[string]string{"max_concurrent": "8", "turn_timeout": "10m", "context_confinement": "chat-only"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Changed, ","); got != "context_confinement,turn_timeout" || len(r.Widening) != 0 {
		t.Errorf("changed %s, widening %+v: the defaults are saved as given, and are no widening", got, r.Widening)
	}
	saved, _ := Load(ctx, db)
	if saved.TurnTimeout != "10m" || saved.ContextConfinement != "chat-only" {
		t.Errorf("a value that spells the default is saved as given: %+v", saved)
	}
	r, err = Apply(ctx, db, env, Change{Set: map[string]string{"max_concurrent": "+8"}})
	if err != nil || len(r.Changed) != 0 || !r.Applied {
		t.Errorf("the same value in another spelling: %+v, %v", r.Changed, err)
	}
}

func TestApplyUnset(t *testing.T) {
	db := openDB(t)
	env := shellEnv(nil, nil, &fakeInstaller{})
	ctx := context.Background()
	putRow(t, db, `{"v":1,"max_concurrent":8,"turn_timeout":"20m","image_runtimes":"none"}`)

	r, err := Apply(ctx, db, env, Change{Unset: []string{"turn_timeout", "v1-addr", "turn_timeout"}}) // one saved, one not, one twice
	if err != nil || strings.Join(r.Changed, ",") != "turn_timeout" {
		t.Fatalf("changed %v, %v", r.Changed, err)
	}
	if saved, _ := Load(ctx, db); saved.TurnTimeout != "" || saved.MaxConcurrent != "8" {
		t.Errorf("saved: %+v", saved)
	}

	// image_runtimes none is below its default: unsetting it leaves none, a widening (P1).
	if _, err := Apply(ctx, db, env, Change{All: true}); err == nil {
		t.Fatal("unsetting all, with none among them, should need confirmation")
	}
	r, err = Apply(ctx, db, env, Change{All: true, Confirm: true})
	if err != nil || strings.Join(r.Changed, ",") != "max_concurrent,image_runtimes" {
		t.Fatalf("changed %v, %v", r.Changed, err)
	}
	if _, ok := row(t, db); ok {
		t.Error("nothing is saved any more, and the row is still there")
	}
}

func TestApplyAnUnsetOfWhatIsNotSavedIsNothing(t *testing.T) {
	db := openDB(t)
	r, err := Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), Change{Unset: []string{"max_concurrent"}})
	if err != nil || !r.Applied || r.Changed == nil || len(r.Changed) != 0 {
		t.Errorf("%+v, %v", r, err)
	}
	if _, ok := row(t, db); ok {
		t.Error("an update that changes nothing made a row")
	}
}

// What is refused is refused before anything is written, and names the setting and the rule.
func TestApplyRefusesInvalidChangesByName(t *testing.T) {
	for _, c := range []struct {
		name     string
		ch       Change
		contains []string
	}{
		{"nothing to change", Change{}, []string{"nothing to change"}},
		{"an empty set", Change{Set: map[string]string{}}, []string{"nothing to change"}},
		{"an unknown key", Change{Set: map[string]string{"nonsense-supersecret": "x"}}, []string{"not a setting", "v1_addr"}},
		{"an unknown key to unset", Change{Unset: []string{"nonsense-supersecret"}}, []string{"not a setting"}},
		{"an empty value", Change{Set: map[string]string{"max_concurrent": ""}}, []string{"max_concurrent", "unset"}},
		{"a blank value", Change{Set: map[string]string{"image_runtimes": "  "}}, []string{"image_runtimes", "unset"}},
		{"a bad class", Change{Set: map[string]string{"confinement": "everything-supersecret"}}, []string{"confinement must be chat-only, sandboxed or any"}},
		{"a bad number", Change{Set: map[string]string{"max_concurrent": "65"}}, []string{"max_concurrent must be an integer from 1 to 64"}},
		{"a padded duration", Change{Set: map[string]string{"turn_timeout": " 15m"}}, []string{"turn_timeout must be a duration of at least 10s"}},
		{"a bad address", Change{Set: map[string]string{"v1_addr": "9443"}}, []string{"v1_addr must be host:port"}},
		{"a bad list", Change{Set: map[string]string{"tool_runtimes": "co dex"}}, []string{"tool_runtimes must be a comma-separated list"}},
		{"several at once", Change{Set: map[string]string{"max_concurrent": "0", "confinement": "x"}}, []string{"confinement must be", "max_concurrent must be"}},
		{"a certificate alone", Change{Set: map[string]string{"tls_cert_file": "/c.pem"}}, []string{"tls_cert_file and tls_key_file must be set together"}},
		{"a key alone", Change{Set: map[string]string{"tls_key_file": "/k.pem"}}, []string{"tls_cert_file and tls_key_file must be set together"}},
		{"both set and unset", Change{Set: map[string]string{"max_concurrent": "8"}, Unset: []string{"max-concurrent"}}, []string{"max_concurrent", "both"}},
		{"one key in two spellings", Change{Set: map[string]string{"max_concurrent": "8", "max-concurrent": "9"}}, []string{"max_concurrent", "twice"}},
		{"all and keys", Change{All: true, Unset: []string{"max_concurrent"}}, []string{"all", "keys"}},
		{"all and set", Change{All: true, Set: map[string]string{"max_concurrent": "8"}}, []string{"all", "set"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, `{"v":1,"turn_timeout":"20m"}`)
			_, err := Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), c.ch)
			var ve *ValidationError
			if !errors.As(err, &ve) || len(ve.Problems) == 0 {
				t.Fatalf("error %v, want a *ValidationError", err)
			}
			for _, want := range c.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the message %q does not say %q", err.Error(), want)
				}
			}
			if strings.Contains(err.Error(), "supersecret") {
				t.Errorf("the message repeats a value: %q", err.Error())
			}
			if v, _ := row(t, db); v != `{"v":1,"turn_timeout":"20m"}` {
				t.Errorf("a refused change wrote: %s", v)
			}
		})
	}
}

// Every problem is named, once, in the order of the settings, whatever order a map gives them in.
func TestApplyProblemsAreInTheOrderOfTheSettings(t *testing.T) {
	for i := 0; i < 20; i++ {
		_, err := Apply(context.Background(), openDB(t), shellEnv(nil, nil, &fakeInstaller{}), Change{
			Set:   map[string]string{"tool_runtimes": "a b", "max_concurrent": "0", "confinement": "x", "v1_addr": "9", "zzz": "1", "yyy": "2"},
			Unset: []string{"nonsense"},
		})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatal(err)
		}
		var keys []string
		for _, p := range ve.Problems {
			keys = append(keys, p.Key)
		}
		if got := strings.Join(keys, ","); got != ",v1_addr,confinement,max_concurrent,tool_runtimes" {
			t.Fatalf("problems for [%s], want one for the unknown keys (said once) and then the settings in order", got)
		}
	}
}

// The pair is checked for what the change leaves, when the change touches it.
func TestApplyTLSPair(t *testing.T) {
	db := openDB(t)
	env := shellEnv(nil, nil, &fakeInstaller{})
	ctx := context.Background()
	if _, err := Apply(ctx, db, env, Change{Set: map[string]string{"tls_cert_file": "/c.pem", "tls_key_file": "/k.pem"}}); err != nil {
		t.Fatalf("both together: %v", err)
	}
	// One alone: a change to the certificate keeps the key, so it is a pair again.
	if _, err := Apply(ctx, db, env, Change{Set: map[string]string{"tls_cert_file": "/c2.pem"}}); err != nil {
		t.Errorf("replacing the certificate: %v", err)
	}
	var ve *ValidationError
	if _, err := Apply(ctx, db, env, Change{Unset: []string{"tls_key_file"}}); !errors.As(err, &ve) {
		t.Errorf("unsetting the key alone leaves a certificate alone: %v", err)
	}
	r, err := Apply(ctx, db, env, Change{Unset: []string{"tls_cert_file", "tls_key_file"}})
	if err != nil || strings.Join(r.Changed, ",") != "tls_cert_file,tls_key_file" {
		t.Errorf("unsetting both: %v, %v", r.Changed, err)
	}
	// A half pair somebody left in the document (an edit by hand) is not a reason to refuse a change that does not touch it.
	putRow(t, db, `{"v":1,"tls_cert_file":"/c.pem"}`)
	r, err = Apply(ctx, db, env, Change{Set: map[string]string{"max_concurrent": "8"}})
	if err != nil {
		t.Fatalf("a change that does not touch the pair: %v", err)
	}
	if len(r.Problems) != 1 || r.Problems[0].Key != "tls_cert_file" {
		t.Errorf("the report still says what is wrong: %+v", r.Problems)
	}
}

// An invalid value somebody left in the document is not a reason to refuse a change that does
// not touch it, and unset removes it.
func TestApplyOnlyLooksAtWhatItChanges(t *testing.T) {
	db := openDB(t)
	env := shellEnv(nil, nil, &fakeInstaller{})
	ctx := context.Background()
	putRow(t, db, `{"v":1,"confinement":"everything"}`)
	if _, err := Apply(ctx, db, env, Change{Set: map[string]string{"max_concurrent": "8"}}); err != nil {
		t.Fatalf("a change that does not touch the invalid value: %v", err)
	}
	r, err := Apply(ctx, db, env, Change{Unset: []string{"confinement"}})
	if err != nil || strings.Join(r.Changed, ",") != "confinement" || len(r.Problems) != 0 {
		t.Fatalf("unsetting the invalid value: %v, %+v, %v", r.Changed, r.Problems, err)
	}
}

// The gate: a change that makes the server reach further needs confirmation.
func TestApplyTheWideningGate(t *testing.T) {
	ctx := context.Background()
	env := shellEnv(nil, nil, &fakeInstaller{})
	widen := Change{Set: map[string]string{"v1_addr": "0.0.0.0:9443"}}

	// Refused, and nothing is written.
	db := openDB(t)
	_, err := Apply(ctx, db, env, widen)
	var we *WideningError
	if !errors.As(err, &we) || len(we.Widening) != 1 || we.Widening[0].Key != "v1_addr" {
		t.Fatalf("error %v, want a *WideningError for v1_addr", err)
	}
	if !strings.Contains(err.Error(), "reach further") || !strings.Contains(err.Error(), "0.0.0.0:9443") {
		t.Errorf("the message should say what it is and show the reasons: %q", err.Error())
	}
	if _, ok := row(t, db); ok {
		t.Error("a refused change wrote")
	}

	// A dry run says what would happen, and writes nothing, with or without confirmation.
	for _, confirm := range []bool{false, true} {
		r, err := Apply(ctx, db, env, Change{Set: widen.Set, DryRun: true, Confirm: confirm})
		if err != nil || r.Applied || len(r.Widening) != 1 || strings.Join(r.Changed, ",") != "v1_addr" {
			t.Fatalf("dry run (confirm %v): applied %v, changed %v, widening %+v, %v", confirm, r.Applied, r.Changed, r.Widening, err)
		}
		if got := rowOf(t, r.ConfigReport, "v1_addr"); got.Saved != "0.0.0.0:9443" || got.Effective != "0.0.0.0:9443" {
			t.Errorf("the dry run shows the state the change would give: %+v", got)
		}
		if _, ok := row(t, db); ok {
			t.Error("a dry run wrote")
		}
	}

	// Confirmed: saved, and the reasons are in the result.
	r, err := Apply(ctx, db, env, Change{Set: widen.Set, Confirm: true})
	if err != nil || !r.Applied || len(r.Widening) != 1 || r.Widening[0].Reason == "" {
		t.Fatalf("confirmed: %+v, %v", r, err)
	}
	if saved, _ := Load(ctx, db); saved.V1Addr != "0.0.0.0:9443" {
		t.Errorf("saved: %+v", saved)
	}

	// Narrowing and the limits need no confirmation.
	r, err = Apply(ctx, db, env, Change{Set: map[string]string{"confinement": "chat-only", "max_concurrent": "9"}})
	if err != nil || len(r.Widening) != 0 {
		t.Errorf("narrowing: %+v, %v", r.Widening, err)
	}
	// An unset that takes a value back to a higher default is a widening (P1)...
	_, err = Apply(ctx, db, env, Change{Unset: []string{"confinement"}})
	if !errors.As(err, &we) || we.Widening[0].Key != "confinement.loopback" {
		t.Errorf("unsetting a confinement of chat-only: %v", err)
	}
	// ...and one that takes away an exposure is not.
	r, err = Apply(ctx, db, env, Change{Unset: []string{"v1_addr"}})
	if err != nil || len(r.Widening) != 0 {
		t.Errorf("unsetting the address: %+v, %v", r.Widening, err)
	}
}

// The gate judges the row it replaces, not the one the caller last saw: when a change was
// widening against what is saved, it is refused even if the same words were harmless a moment
// ago. Two writers race: one removes the confinement while the other sets it to the value it
// had.
func TestApplyJudgesTheRowItReplaces(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	const rounds = 30
	for i := 0; i < rounds; i++ {
		putRow(t, db, `{"v":1,"confinement":"any"}`)
		var wg sync.WaitGroup
		var errSet error
		wg.Add(2)
		go func() { // sets what is saved: harmless against {confinement: any}
			defer wg.Done()
			_, errSet = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{"confinement": "any"}})
		}()
		go func() { // removes it
			defer wg.Done()
			_, _ = Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{Unset: []string{"confinement"}})
		}()
		wg.Wait()
		saved, err := Load(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		// Either order is possible. If the removal went first, setting `any` raises what a
		// network listener serves from chat-only, so it must have been refused (and then
		// nothing is saved); if it went second, the value is saved and then removed.
		var we *WideningError
		switch {
		case saved.Confinement == "" && errSet == nil:
			// set, then unset: fine
		case saved.Confinement == "" && errors.As(errSet, &we):
			// unset, then a refused set: fine
		default:
			t.Fatalf("round %d: saved confinement %q, the set answered %v", i, saved.Confinement, errSet)
		}
	}
}

// Many callers at once: every change lands.
func TestApplyConcurrentChangesOfDifferentSettingsAllLand(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	sets := map[string]string{"max_concurrent": "8", "turn_timeout": "20m", "image_runtimes": "none", "tool_runtimes": "claude", "context_confinement": "chat-only", "auto_confinement": "chat-only"}
	var wg sync.WaitGroup
	errs := make(chan error, len(sets))
	for k, v := range sets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{k: v}, Confirm: true})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	saved, _ := Load(ctx, db)
	for k, v := range sets {
		if saved.Get(k) != v {
			t.Errorf("%s = %q, want %q: a change was lost", k, saved.Get(k), v)
		}
	}
}

// After a change the states are against the running daemon, so a change shows as pending
// until the daemon restarts.
func TestApplyReportsWhatStillNeedsARestart(t *testing.T) {
	db := openDB(t)
	hb := daemonhb.Heartbeat{PID: 1, APISettings: map[string]daemonhb.APISetting{
		"max_concurrent": {Value: "4", Source: "default"}, "turn_timeout": {Value: "10m", Source: "default"},
	}}
	env := shellEnv(nil, func() (daemonhb.Heartbeat, bool) { return hb, true }, &fakeInstaller{registered: true})
	r, err := Apply(context.Background(), db, env, Change{Set: map[string]string{"max_concurrent": "8", "turn_timeout": "10m"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.RestartNeeded || rowOf(t, r.ConfigReport, "max_concurrent").State != StatePendingRestart || rowOf(t, r.ConfigReport, "turn_timeout").State != StateApplied {
		t.Errorf("restart needed %v; max_concurrent %s, turn_timeout %s", r.RestartNeeded, rowOf(t, r.ConfigReport, "max_concurrent").State, rowOf(t, r.ConfigReport, "turn_timeout").State)
	}
	// With no daemon nothing is pending: the settings apply when it starts.
	r, err = Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{"max_concurrent": "9"}})
	if err != nil || r.RestartNeeded {
		t.Errorf("no daemon: restart needed %v, %v", r.RestartNeeded, err)
	}
}

func TestApplyRefusesADocumentItCannotRead(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":2}`)
	if _, err := Apply(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}), Change{Set: map[string]string{"max_concurrent": "8"}}); !errors.Is(err, ErrTooNew) {
		t.Errorf("a newer format: %v", err)
	}
}

// A request that fails its checks never reaches the database or the service manager.
func TestApplyAnInvalidRequestTouchesNothing(t *testing.T) {
	inst := &fakeInstaller{}
	_, err := Apply(context.Background(), openDB(t), shellEnv(nil, nil, inst), Change{Set: map[string]string{"max_concurrent": "x"}})
	if err == nil || inst.statusCalls != 0 {
		t.Errorf("%v, the service manager was asked %d times", err, inst.statusCalls)
	}
}

// The document is the show document plus three fields.
func TestTheChangeDocumentKeepsItsFieldsAndTheirOrder(t *testing.T) {
	r := ChangeResult{
		ConfigReport: ConfigReport{V: 1, Environment: "shell", Settings: []SettingReport{}, Problems: []Problem{}},
		Applied:      true, Changed: []string{"v1_addr"},
		Widening: []Widening{{Key: "v1_addr", Reason: "The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine."}},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"environment":"shell","settings":[],"daemon":{"running":false,"reports_settings":false,"autostart":false},"restart_needed":false,"problems":[],` +
		`"applied":true,"changed":["v1_addr"],"widening":[{"key":"v1_addr","reason":"The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine."}]}`
	if string(b) != want {
		t.Errorf("the document changed:\n got %s\nwant %s", b, want)
	}
}

func TestWideningErrorListsTheReasons(t *testing.T) {
	e := &WideningError{Widening: []Widening{{Key: "a", Reason: "One thing."}, {Key: "b", Reason: "Another thing."}}}
	if got, want := e.Error(), "this change makes the server reach further: One thing. Another thing."; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
