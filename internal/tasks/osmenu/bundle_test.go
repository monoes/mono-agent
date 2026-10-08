package osmenu

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// testDB is the database the test menus file into.
const testDB = "/Users/me/.monoagent/monoagent.db"

func testSpec() Spec {
	return Spec{CLI: "/usr/local/bin/monoagentcli", DBPath: testDB, ProfileID: "work-id", ProfileName: "Work"}
}

// hostileName would break out of a property list or a shell script that
// escaped it wrongly; hostileClean is how the menu shows it.
const (
	hostileName  = "</a><key>x</key> & ]]> \"q\" 'it' $(id)\n\U0000202Eb"
	hostileClean = `<-a><key>x<-key> & ]]> "q" 'it' $(id) b`
)

func TestTheBundleIsAServiceThatReceivesText(t *testing.T) {
	b, err := Render(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Add to MonoAgent Tasks (Work).workflow" || b.Menu != "Add to MonoAgent Tasks: Work" || b.ProfileID != "work-id" || b.DBPath != testDB {
		t.Errorf("name %q, menu %q, profile %q, database %q", b.Name, b.Menu, b.ProfileID, b.DBPath)
	}
	info := parsePlist(t, b.Info)
	for key, want := range map[string]string{
		"CFBundleIdentifier": bundleID("work-id"),
		"CFBundleName":       "Add to MonoAgent Tasks: Work",
		KeyCLI:               "/usr/local/bin/monoagentcli",
		KeyDB:                testDB,
		KeyProfileID:         "work-id",
		KeyVersion:           Version,
	} {
		if got := dig(t, info, key); got != want {
			t.Errorf("Info.plist %s = %v, want %q", key, got, want)
		}
	}
	if services, _ := dig(t, info, "NSServices").([]any); len(services) != 1 {
		t.Fatalf("%d services, want one", len(services))
	}
	svc := dig(t, info, "NSServices", 0)
	for _, c := range []struct {
		key  string
		want any
	}{
		{"NSMessage", "runWorkflowAsService"},
		{"NSSendTypes", []any{"public.utf8-plain-text"}},
		{"NSRequiredContext", map[string]any{}}, // Apple: always present, empty when nothing is filtered
		{"NSMenuItem", map[string]any{"default": "Add to MonoAgent Tasks: Work"}},
	} {
		if got := dig(t, svc, c.key); !reflect.DeepEqual(got, c.want) {
			t.Errorf("the service's %s = %#v, want %#v", c.key, got, c.want)
		}
	}
	doc := parsePlist(t, b.Document)
	if actions, _ := dig(t, doc, "actions").([]any); len(actions) != 1 {
		t.Fatalf("%d actions, want one", len(actions))
	}
	action := dig(t, doc, "actions", 0, "action")
	script, err := renderScript(scriptView{CLI: "/usr/local/bin/monoagentcli", DBPath: testDB,
		ProfileID: "work-id", Name: "Work", Osascript: osascript})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path []any
		want any
	}{
		{[]any{"BundleIdentifier"}, "com.apple.RunShellScript"},
		{[]any{"ActionBundlePath"}, "/System/Library/Automator/Run Shell Script.action"},
		{[]any{"ActionParameters", "COMMAND_STRING"}, script},
		{[]any{"ActionParameters", "inputMethod"}, int64(0)},
		{[]any{"ActionParameters", "shell"}, "/bin/sh"},
		{[]any{"ActionParameters", "CheckedForUserDefaultShell"}, true},
	} {
		if got := dig(t, action, c.path...); got != c.want {
			t.Errorf("the action's %v = %#v, want %#v", c.path, got, c.want)
		}
	}
	meta := dig(t, doc, "workflowMetaData")
	for key, want := range map[string]string{
		"workflowTypeIdentifier":      "com.apple.Automator.servicesMenu",
		"serviceInputTypeIdentifier":  "com.apple.Automator.text",
		"serviceOutputTypeIdentifier": "com.apple.Automator.nothing",
	} {
		if got := dig(t, meta, key); got != want {
			t.Errorf("workflowMetaData %s = %v, want %q", key, got, want)
		}
	}
}

func TestHostileProfileNamesStayText(t *testing.T) {
	spec := testSpec()
	spec.ProfileName = hostileName
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	info := parsePlist(t, b.Info)
	if got := dig(t, info, "NSServices", 0, "NSMenuItem", "default"); got != MenuPrefix+": "+hostileClean {
		t.Errorf("menu item %q", got)
	}
	if got := dig(t, info, "NSServices", 0, "NSMessage"); got != "runWorkflowAsService" {
		t.Errorf("the name rewrote the service's message: %v", got)
	}
	script, _ := dig(t, parsePlist(t, b.Document), "actions", 0, "action", "ActionParameters", "COMMAND_STRING").(string)
	if !strings.Contains(script, "\nname="+shellQuote(hostileClean)+"\n") {
		t.Errorf("the script does not hold the quoted name:\n%s", script)
	}
	if b.Name != "Add to MonoAgent Tasks ("+hostileClean+").workflow" {
		t.Errorf("bundle name %q", b.Name)
	}
}

func TestRenderRefusesValuesABundleCannotCarry(t *testing.T) {
	for _, c := range []struct {
		change func(*Spec)
		want   string
	}{
		{func(s *Spec) { s.CLI = "monoagentcli" }, `the monoagentcli path "monoagentcli" is not absolute`},
		{func(s *Spec) { s.DBPath = "~/.monoagent/monoagent.db" }, `the database path "~/.monoagent/monoagent.db" is not absolute`},
		{func(s *Spec) { s.CLI = "/usr/local/bin/mono\nagentcli" }, `the monoagentcli path "/usr/local/bin/mono\nagentcli" holds a control or hidden character, or is not UTF-8`},
		{func(s *Spec) { s.DBPath = "" }, "the database path is empty"},
		{func(s *Spec) { s.ProfileID = "work\x1bid" }, `the profile id "work\x1bid" holds a control or hidden character, or is not UTF-8`},
	} {
		spec := testSpec()
		c.change(&spec)
		if _, err := Render(spec); err == nil || err.Error() != c.want {
			t.Errorf("Render: %v, want %q", err, c.want)
		}
	}
	// A value with a character XML cannot hold passes plainValue; the last check
	// reads both rendered files back and refuses.
	spec := testSpec()
	spec.ProfileID = "work\U0000FFFEid"
	if _, err := Render(spec); err == nil || !strings.HasPrefix(err.Error(), "the rendered bundle is not well-formed XML: ") {
		t.Errorf("Render with U+FFFE in the profile id: %v", err)
	}
}

func TestRenderIsStableAndCarriesTheCLIPath(t *testing.T) {
	a, _ := Render(testSpec())
	b, _ := Render(testSpec())
	if !bytes.Equal(a.Info, b.Info) || !bytes.Equal(a.Document, b.Document) {
		t.Error("two renders of one spec differ: an installed menu could never be current")
	}
	other := testSpec()
	other.CLI = "/opt/bin/monoagentcli"
	c, _ := Render(other)
	if bytes.Equal(a.Info, c.Info) || bytes.Equal(a.Document, c.Document) {
		t.Error("the CLI path is not in both files")
	}
}
