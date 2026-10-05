package main

// What the page receives from the settings bindings: Wails marshals a binding's result with
// encoding/json, so a field the CLI did not send must not come out at all (a daemon that does not
// report a setting has no running value: that is not an empty one), what it did send is kept
// as it was (an empty running value, false, a zero), and a list is a list and never null.

import (
	"context"
	"encoding/json"
	"testing"
)

func jsonOf(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func settingsOf(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	list, ok := doc["settings"].([]any)
	if !ok {
		t.Fatalf("settings = %#v, want a list", doc["settings"])
	}
	out := make([]map[string]any, len(list))
	for i, s := range list {
		out[i] = s.(map[string]any)
	}
	return out
}

// What the CLI does not send stays absent: a setting with nothing saved has no `saved`, and one the
// daemon does not report (a daemon that predates the report reports none) has neither `running` nor
// `running_source`, so that the page tells "the daemon runs it as empty" from "nobody knows".
func TestAPIConfigAbsentStaysAbsent(t *testing.T) {
	dir, _ := fakeConfigCLI(t)
	answer(t, dir, "show", `{"v":1,"environment":"shell","settings":[`+
		`{"key":"v1_addr","server_flag":"--v1-addr","env":"MONOAGENT_API_V1_ADDR","default":"","effective":"","source":"default","state":"unknown"},`+
		`{"key":"max_concurrent","server_flag":"--max-concurrent","env":"MONOAGENT_API_MAX_CONCURRENT","saved":"6","default":"4","effective":"6","source":"saved","state":"unknown"}`+
		`],"daemon":{"running":true,"reports_settings":false,"autostart":false},"restart_needed":false,"problems":[]}`, "", 0)
	a := newTestApp(t)
	a.ctx = context.Background()

	info, err := a.APIConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	rows := settingsOf(t, wire(t, info))
	absent(t, "a setting with nothing saved and no report of the daemon", rows[0], "saved", "running", "running_source")
	absent(t, "a setting the daemon does not report", rows[1], "running", "running_source")
	if rows[1]["saved"] != "6" || rows[0]["default"] != "" || rows[0]["effective"] != "" {
		t.Errorf("what the CLI sent must be kept, an empty default and effective included: %v %v", rows[0], rows[1])
	}
}

// What a current CLI says is kept: an empty running value (the daemon says it runs the setting with
// no value) is present and empty, false stays false and a dry run's `applied: false` is not dropped.
func TestAPIConfigPresentZerosAreKept(t *testing.T) {
	dir, _ := fakeConfigCLI(t)
	answer(t, dir, "show", `{"v":1,"environment":"mcp","settings":[`+
		`{"key":"v1_addr","server_flag":"--v1-addr","env":"MONOAGENT_API_V1_ADDR","default":"","effective":"","source":"default","running":"","running_source":"default","state":"applied"}`+
		`],"daemon":{"running":true,"reports_settings":true,"autostart":false},"restart_needed":false,"problems":[]}`, "", 0)
	answer(t, dir, "set-dry", `{"v":1,"environment":"shell","settings":[],"daemon":{"running":false,"reports_settings":false,"autostart":false},`+
		`"restart_needed":false,"problems":[],"applied":false,"changed":[],"widening":[]}`, "", 0)
	a := newTestApp(t)
	a.ctx = context.Background()

	info, err := a.APIConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	page := wire(t, info)
	row := settingsOf(t, page)[0]
	if v, has := row["running"]; !has || v != "" {
		t.Errorf("an empty running value must reach the page as an empty string, got %v (present %v)", v, has)
	}
	if row["running_source"] != "default" || page["environment"] != "mcp" {
		t.Errorf("row = %v, environment = %v", row, page["environment"])
	}
	for path, v := range map[string]any{"restart_needed": page["restart_needed"], "daemon.autostart": page["daemon"].(map[string]any)["autostart"]} {
		if v != false {
			t.Errorf("%s false must reach the page as false, got %v", path, v)
		}
	}

	dry, err := a.APIConfigSet(map[string]string{"max_concurrent": "8"}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	d := wire(t, dry)
	if v, has := d["applied"]; !has || v != false {
		t.Errorf("applied false must reach the page as false, got %v (present %v)", v, has)
	}
	if d["restart_needed"] != false || d["environment"] != "shell" {
		t.Errorf("a change's document carries the state's fields as well: %v", d)
	}
}

// A list the CLI leaves out or sends as null is an empty list on the page, so that it can map over it.
func TestAPIConfigEmptyListsAreNotNull(t *testing.T) {
	dir, _ := fakeConfigCLI(t)
	const bare = `{"v":1,"environment":"shell","settings":null,"daemon":{"running":false,"reports_settings":false,"autostart":false},"restart_needed":false}`
	answer(t, dir, "show", bare, "", 0)
	answer(t, dir, "set", `{"v":1,"environment":"shell","daemon":{"running":false,"reports_settings":false,"autostart":false},"restart_needed":false,"applied":true,"changed":null}`, "", 0)
	answer(t, dir, "unset", `{"v":1,"environment":"shell","settings":null,"daemon":{"running":false,"reports_settings":false,"autostart":false},"restart_needed":false,"problems":null,"applied":true,"widening":null}`, "", 0)
	a := newTestApp(t)
	a.ctx = context.Background()

	info, err := a.APIConfigShow()
	if err != nil {
		t.Fatal(err)
	}
	set, err := a.APIConfigSet(map[string]string{"max_concurrent": "8"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	unset, err := a.APIConfigUnset([]string{"max_concurrent"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for what, doc := range map[string]map[string]any{"show": wire(t, info), "set": wire(t, set), "unset": wire(t, unset)} {
		for _, list := range []string{"settings", "problems"} {
			if l, ok := doc[list].([]any); !ok || len(l) != 0 {
				t.Errorf("%s: %s = %#v, want an empty list", what, list, doc[list])
			}
		}
	}
	for what, doc := range map[string]map[string]any{"set": wire(t, set), "unset": wire(t, unset)} {
		for _, list := range []string{"changed", "widening"} {
			if l, ok := doc[list].([]any); !ok || len(l) != 0 {
				t.Errorf("%s: %s = %#v, want an empty list", what, list, doc[list])
			}
		}
	}
}

// `removed_unreadable_row` is sent by the CLI only when it is true: the page reads it as "the row that could not be read", so
// a false that was never said must not appear as one, and a true that was said must arrive.
func TestAPIConfigRemovedUnreadableRowIsPresentOnlyWhenTrue(t *testing.T) {
	dir, _ := fakeConfigCLI(t)
	answer(t, dir, "unset", configDoc(`,"applied":true,"changed":["max_concurrent"],"widening":[]`), "", 0)
	answer(t, dir, "unset-dry", configDoc(`,"applied":false,"changed":[],"widening":[{"key":"saved_settings","reason":"It cannot be read."}],"removed_unreadable_row":true`), "", 0)
	a := newTestApp(t)
	a.ctx = context.Background()

	healthy, err := a.APIConfigReset(true, false)
	if err != nil {
		t.Fatal(err)
	}
	absent(t, "a reset that found a row that could be read", wire(t, healthy), "removed_unreadable_row")
	unreadable, err := a.APIConfigReset(false, true)
	if err != nil {
		t.Fatal(err)
	}
	page := wire(t, unreadable)
	if v, has := page["removed_unreadable_row"]; !has || v != true {
		t.Errorf("removed_unreadable_row true must reach the page as true, got %v (present %v)", v, has)
	}
	if w := page["widening"].([]any); len(w) != 1 || w[0].(map[string]any)["key"] != "saved_settings" {
		t.Errorf("widening = %v", page["widening"])
	}
	// no other change carries it
	set, err := a.APIConfigUnset([]string{"max_concurrent"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	absent(t, "a change of one setting", wire(t, set), "removed_unreadable_row")
}
