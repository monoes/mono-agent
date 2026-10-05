package mcp

// api_auto_set switches the Jev surface api_auto on or off for the MCP server's profile, as `jev
// enable|disable api_auto` does. It never creates, stores or reads the Jev key, and turning it on
// shows what leaves the machine and needs the caller to say it knows.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/secrets"
)

const jevKeyInTheEnvironment = "jev-test-key-0123456789abcdef"

// autoDoc is what api_auto_set answers.
func autoDoc(t *testing.T, text string) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("not a document: %v\n%s", err, text)
	}
	return doc
}

func field[T any](t *testing.T, doc map[string]json.RawMessage, name string) T {
	t.Helper()
	var v T
	raw, ok := doc[name]
	if !ok {
		t.Fatalf("the document has no %q: its fields are %v", name, fieldNames(doc))
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

func fieldNames(doc map[string]json.RawMessage) []string {
	var names []string
	for k := range doc {
		names = append(names, k)
	}
	return names
}

func TestAPIAutoSetSwitchesTheSurfaceOnForTheServersProfileAndSaysWhatLeavesTheMachine(t *testing.T) {
	f := newConfigFixture(t, configSetup{work: true})
	t.Setenv("TYPESAFE_API_KEY", jevKeyInTheEnvironment)

	text := f.mustCall("api_auto_set", map[string]any{"enabled": true, "acknowledge_egress": true})
	doc := autoDoc(t, text)
	if got := field[string](t, doc, "profile_id"); got != workProfileID {
		t.Errorf("profile_id %q, want the id of the server's profile, %s", got, workProfileID)
	}
	if field[string](t, doc, "surface") != "api_auto" || !field[bool](t, doc, "enabled") {
		t.Errorf("the document: %s", text)
	}
	// What leaves the machine is in the result, as the command's --json has it.
	if got := field[[]string](t, doc, "egress"); !reflect.DeepEqual(got, jevconf.Egress[jevconf.APIAuto]) {
		t.Errorf("egress %v, want %v", got, jevconf.Egress[jevconf.APIAuto])
	}
	if field[float64](t, doc, "threshold") != jevconf.DefaultThreshold[jevconf.APIAuto] {
		t.Errorf("threshold %s", doc["threshold"])
	}
	// And what api_status says of the auto model after the change: it works, with a key from the environment.
	var auto struct {
		Available bool   `json:"available"`
		KeySource string `json:"key_source"`
	}
	if err := json.Unmarshal(doc["auto"], &auto); err != nil || !auto.Available || auto.KeySource != "env" {
		t.Errorf("auto %s (%v)", doc["auto"], err)
	}
	// The key is not part of anything the tool says.
	if strings.Contains(text, jevKeyInTheEnvironment) {
		t.Error("the Jev key is in the result")
	}

	// The profile's, by its id: not "default" and not the name the server was opened by.
	for profile, want := range map[string]bool{workProfileID: true, "default": false, workProfileName: false} {
		if got := jevconf.Enabled(f.Side.DB, profile, jevconf.APIAuto); got != want {
			t.Errorf("api_auto enabled for %q = %v, want %v", profile, got, want)
		}
	}
	// One row, the switch: nothing else of the profile's Jev settings was written.
	var rows int
	if err := f.Side.DB.QueryRow(`SELECT COUNT(*) FROM settings WHERE key LIKE 'jev.%'`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("%d Jev settings written (%v), want the one switch", rows, err)
	}
}

// Turning it on without saying so shows what would leave the machine and changes nothing; the
// acknowledgement is of one call, and the tool keeps no memory of an earlier one.
func TestAPIAutoSetNeedsTheAcknowledgementToSwitchOnAndShowsTheEgressWithout(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	for _, args := range []map[string]any{
		{"enabled": true},
		{"enabled": true, "acknowledge_egress": false},
	} {
		_, err := f.call("api_auto_set", args)
		if err == nil || !strings.Contains(err.Error(), "acknowledge_egress") {
			t.Fatalf("%v: %v, want a refusal that names acknowledge_egress", args, err)
		}
		for _, e := range jevconf.Egress[jevconf.APIAuto] {
			if !strings.Contains(err.Error(), e) {
				t.Errorf("the refusal does not show what is sent: %q is missing from %v", e, err)
			}
		}
		if jevconf.Enabled(f.Side.DB, "default", jevconf.APIAuto) {
			t.Fatal("the surface was switched on without the acknowledgement")
		}
	}
	f.mustCall("api_auto_set", map[string]any{"enabled": true, "acknowledge_egress": true})
	f.mustCall("api_auto_set", map[string]any{"enabled": false})
	if _, err := f.call("api_auto_set", map[string]any{"enabled": true}); err == nil {
		t.Error("an acknowledgement given before was remembered")
	}
}

func TestAPIAutoSetSwitchesTheSurfaceOffWithoutAnAcknowledgement(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	t.Setenv("TYPESAFE_API_KEY", jevKeyInTheEnvironment)
	f.mustCall("api_auto_set", map[string]any{"enabled": true, "acknowledge_egress": true})
	// Other profiles' switches are not touched.
	if err := jevconf.SetEnabled(f.Side.DB, "someone-else", jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}

	text := f.mustCall("api_auto_set", map[string]any{"enabled": false})
	doc := autoDoc(t, text)
	if field[bool](t, doc, "enabled") || field[string](t, doc, "surface") != "api_auto" || field[string](t, doc, "profile_id") != "default" {
		t.Errorf("the document: %s", text)
	}
	// Nothing is sent when it is off, and the document of the command says no more.
	if _, has := doc["egress"]; has {
		t.Error("a switch-off lists what is sent")
	}
	if _, has := doc["threshold"]; has {
		t.Error("a switch-off reports a threshold, as the command's does not")
	}
	var auto struct {
		Available bool   `json:"available"`
		Missing   string `json:"missing"`
	}
	if err := json.Unmarshal(doc["auto"], &auto); err != nil || auto.Available || !strings.Contains(auto.Missing, "jev enable api_auto") {
		t.Errorf("auto %s: off, and what is missing", doc["auto"])
	}
	if jevconf.Enabled(f.Side.DB, "default", jevconf.APIAuto) || !jevconf.Enabled(f.Side.DB, "someone-else", jevconf.APIAuto) {
		t.Error("the switch of the server's profile is off and another profile's is untouched")
	}
	// Off again is fine.
	f.mustCall("api_auto_set", map[string]any{"enabled": false})
}

// The threshold is the profile's, and the tool neither sets nor loses it.
func TestAPIAutoSetReportsTheProfilesThresholdAndKeepsIt(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	if err := jevconf.SetThreshold(f.Side.DB, "default", jevconf.APIAuto, 0.5); err != nil {
		t.Fatal(err)
	}
	doc := autoDoc(t, f.mustCall("api_auto_set", map[string]any{"enabled": true, "acknowledge_egress": true}))
	if got := field[float64](t, doc, "threshold"); got != 0.5 {
		t.Errorf("threshold %v, want the profile's 0.5", got)
	}
	f.mustCall("api_auto_set", map[string]any{"enabled": false})
	if got := jevconf.Threshold(f.Side.DB, "default", jevconf.APIAuto, jevconf.DefaultThreshold[jevconf.APIAuto]); got != 0.5 {
		t.Errorf("the threshold is %v after a switch-off", got)
	}
}

// Without a Jev key the surface can be switched on (the command does, with a note), and auto stays
// unavailable: the result says so. The tool does not make a key or store one, and never shows one.
func TestAPIAutoSetWithoutAJevKeySwitchesOnAndSaysAutoIsUnavailable(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	text := f.mustCall("api_auto_set", map[string]any{"enabled": true, "acknowledge_egress": true})

	var doc struct {
		Enabled bool `json:"enabled"`
		Auto    struct {
			Available bool   `json:"available"`
			Missing   string `json:"missing"`
		} `json:"auto"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Enabled || doc.Auto.Available || !strings.Contains(doc.Auto.Missing, "Jev key") || !strings.Contains(doc.Auto.Missing, "jev key set") {
		t.Errorf("no key: %s", text)
	}
	if !jevconf.Enabled(f.Side.DB, "default", jevconf.APIAuto) {
		t.Error("the surface is not on")
	}
	if entries, err := secrets.List(context.Background(), f.Side.DB, "default"); err != nil || len(entries) != 0 {
		t.Errorf("the vault holds %d entries (%v): the tool must not store a key", len(entries), err)
	}
}

func TestAPIAutoSetTakesABooleanForEnabled(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	for name, c := range map[string]struct {
		args map[string]any
		want string
	}{
		"missing":                 {map[string]any{}, "enabled is required"},
		"null":                    {map[string]any{"enabled": nil}, "enabled is required"},
		"a string":                {map[string]any{"enabled": "yes"}, "invalid arguments"},
		"a number":                {map[string]any{"enabled": 1}, "invalid arguments"},
		"an acknowledgement text": {map[string]any{"enabled": true, "acknowledge_egress": "yes"}, "invalid arguments"},
	} {
		if _, err := f.call("api_auto_set", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if jevconf.Enabled(f.Side.DB, "default", jevconf.APIAuto) {
		t.Error("a refused call switched the surface on")
	}
}

// Nothing a caller sends comes back in an error: a key pasted where a boolean goes, or 1 MiB.
func TestNoErrorOfAPIAutoSetRepeatsAnArgument(t *testing.T) {
	pasted, err := apikeys.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	f := newConfigFixture(t, configSetup{})
	for _, v := range []string{pasted, strings.ToUpper(pasted), pasted[:30], strings.Repeat("Q", 1<<20)} {
		// The key where a boolean goes, and in an argument the tool does not have, which reaches the
		// refusal of a call without enabled.
		for _, args := range []map[string]any{
			{"enabled": v}, {"enabled": true, "acknowledge_egress": v}, {"enabled": v, "acknowledge_egress": v},
			{"note": v}, {"enabled": nil, "note": v}, {"acknowledge_egress": true, "note": v},
		} {
			_, err := f.call("api_auto_set", args)
			if err == nil {
				t.Fatalf("%.30s... was accepted", v)
			}
			if echoes(err.Error(), pasted[:30]) || len(err.Error()) > 1024 {
				t.Errorf("the error repeats an argument or is %d bytes", len(err.Error()))
			}
		}
	}
}

func TestAPIAutoSetIsGatedLikeTheOtherMutatingTools(t *testing.T) {
	closed := newConfigFixture(t, configSetup{readOnly: true})
	if toolsListNames(t, closed.Server)["api_auto_set"] {
		t.Error("api_auto_set sends prompts to TypeSafe once on and must not be listed without --allow-mutations")
	}
	for _, setup := range []configSetup{{readOnly: true}, {readOnly: true, allowExposure: true}} {
		f := newConfigFixture(t, setup)
		if _, err := f.call("api_auto_set", map[string]any{"enabled": true, "acknowledge_egress": true}); err == nil || !strings.Contains(err.Error(), "--allow-mutations") {
			t.Errorf("%+v: %v, want a refusal that names --allow-mutations", setup, err)
		}
		if jevconf.Enabled(f.Side.DB, "default", jevconf.APIAuto) {
			t.Errorf("%+v: a refused call switched the surface on", setup)
		}
	}
}

func TestAPIAutoSetDescriptionSaysWhatItDoesAndDoesNot(t *testing.T) {
	var d string
	for _, tl := range apiAutoTools() {
		if tl.name == "api_auto_set" {
			d = tl.description
		}
	}
	for _, want := range []string{"jev enable|disable api_auto", "acknowledge_egress", "TypeSafe", "4,000 characters", "never creates", "Jev key", "not an access control", "auto_confinement", "api_status"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description does not mention %q: %s", want, d)
		}
	}
}
