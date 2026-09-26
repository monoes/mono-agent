package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/monoes/mono-agent/internal/automation"
)

func TestAutomationFixtureSiteRouting(t *testing.T) {
	fsys := fstest.MapFS{
		"tests/fixtures/post.html":   {Data: []byte("FALLBACK")},
		"tests/fixtures/done.html":   {Data: []byte("DONE")},
		"tests/pages/form.html":      {Data: []byte("FORM")},
		"tests/post.expect.json":     {Data: []byte(`{"requests":[{"method":"POST","urlMatches":"/create$","bodyContains":"title=hi"}]}`)},
		"tests/post.routes.json":     {Data: []byte(`[{"match":"/new$","fixture":"pages/form.html"},{"match":"/create$","fixture":"done.html","status":201}]`)},
		"tests/fixtures/helper.html": {Data: []byte("not a test: no such action")},
	}
	fx := listFixtures(fsys, []string{"post"})
	if len(fx) != 1 || fx[0].name != "post" || fx[0].expectPath == "" {
		t.Fatalf("fixtures = %+v", fx)
	}
	site, err := loadFixtureSite(fsys, fx[0])
	if err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]string{
		"https://w.test/new": "FORM", "https://w.test/create": "DONE", "https://w.test/other": "FALLBACK",
	} {
		if pg, ok := site.page(url, nil); !ok || pg.body != want {
			t.Errorf("%s → %q", url, pg.body)
		}
	}
	if pg, _ := site.page("https://w.test/create", nil); pg.status != 201 {
		t.Errorf("status = %d", pg.status)
	}

	// "after": a page that changes once the form was posted.
	stateful := fstest.MapFS{
		"tests/fixtures/before.html": {Data: []byte("BEFORE")},
		"tests/fixtures/after.html":  {Data: []byte("AFTER")},
		"tests/s.routes.json": {Data: []byte(`[{"match":"/list$","after":"/create$","fixture":"after.html"},
			{"match":"/list$","fixture":"before.html"}]`)},
	}
	ss, err := loadFixtureSite(stateful, fixtureFile{name: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if pg, _ := ss.page("https://w.test/list", nil); pg.body != "BEFORE" {
		t.Errorf("before post: %q", pg.body)
	}
	posted := []recordedRequest{{Method: "POST", URL: "https://w.test/create"}}
	if pg, _ := ss.page("https://w.test/list", posted); pg.body != "AFTER" {
		t.Errorf("after post: %q", pg.body)
	}

	bad := fstest.MapFS{"tests/x.routes.json": {Data: []byte(`[{"match":".","fixture":"../../automation.json"}]`)},
		"automation.json": {Data: []byte("{}")}}
	if _, err := loadFixtureSite(bad, fixtureFile{name: "x"}); err == nil || !strings.Contains(err.Error(), "outside tests/") {
		t.Fatalf("escape not refused: %v", err)
	}
}

func TestAutomationFixtureExpectRequests(t *testing.T) {
	e, err := parseFixtureExpect([]byte(`{"records":[{"a":1}],"requests":[{"method":"post","urlMatches":"/c$","bodyContains":"x=1"}]}`))
	if err != nil || !e.hasRecords || len(e.requests) != 1 {
		t.Fatalf("expect = %+v, %v", e, err)
	}
	plain, _ := parseFixtureExpect([]byte(`{"items":[]}`))
	if !plain.hasRecords || plain.requests != nil {
		t.Fatalf("plain = %+v", plain)
	}
	got := []recordedRequest{{Method: "POST", URL: "https://w.test/c", Body: "x=1&y=2"}}
	if unmet, _ := unmetRequests(e.requests, got); len(unmet) != 0 {
		t.Fatalf("unmet = %v", unmet)
	}
	if unmet, _ := unmetRequests(e.requests, nil); len(unmet) != 1 {
		t.Fatalf("unmet with no requests = %v", unmet)
	}
}

// TestAutomationFixtureFullRunPostsForm runs a write action end to end in a
// headless browser: the form POST is served in-process and asserted.
// Skipped when no browser is installed.
func TestAutomationFixtureFullRunPostsForm(t *testing.T) {
	if _, ok := launcher.LookPath(); !ok {
		t.Skip("no browser installed")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "poster")
	files := map[string]string{
		"automation.json": `{"schema":"monoagent.automation/v1","id":"poster","name":"Poster","version":"0.1.0",
			"site":{"startUrl":"https://w.test/","domains":["w.test"]},
			"permissions":{"steps":["navigate","type","click","wait_for"],"scripts":[]},
			"actions":["post"],"policy":{"tier":"standard"}}`,
		"actions/post.json": `{"actionType":"post","automation":"poster","sideEffects":"write",
			"inputs":{"required":[{"name":"title","type":"string"}]},
			"steps":[
				{"id":"open","type":"navigate","url":"https://w.test/new"},
				{"id":"title","type":"type","selector":"#title","value":"{{title}}"},
				{"id":"send","type":"click","selector":"#send","sideEffect":true},
				{"id":"done","type":"wait_for","until":{"selector":"#done"},"timeout":10}
			]}`,
		"tests/fixtures/form.html": `<form method="post" action="/create"><input id="title" name="title"><button id="send">Go</button></form>`,
		"tests/fixtures/done.html": `<p id="done">created</p>`,
		"tests/post.routes.json":   `[{"match":"/new$","fixture":"form.html"},{"match":"/create$","fixture":"done.html"}]`,
		"tests/post.inputs.json":   `{"title":"hello"}`,
		"tests/post.expect.json":   `{"requests":[{"method":"POST","urlMatches":"/create$","bodyContains":"title=hello"}]}`,
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	run := func(args ...string) []fixtureResult {
		t.Helper()
		root := newRootCmd()
		var out strings.Builder
		root.SetOut(&out)
		root.SetErr(&strings.Builder{})
		root.SetArgs(append([]string{"automation", "test", dir, "--json", "--db-path", filepath.Join(home, "x.db")}, args...))
		_ = root.Execute()
		var res struct {
			Results []fixtureResult `json:"results"`
		}
		if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
			t.Fatalf("stdout %q: %v", out.String(), err)
		}
		return res.Results
	}
	safe := run()
	if len(safe) != 2 || safe[1].Status != "skipped" || !strings.Contains(safe[1].Message, "--full") {
		t.Fatalf("safe run = %+v", safe)
	}
	full := run("--full")
	if len(full) != 2 || full[1].Status != "pass" {
		t.Fatalf("full run = %+v", full)
	}
	_ = automation.SourceLocal
}

// TestAutomationTestJSONExitCode: a failing test row makes the command fail
// with --json too, and stdout stays a single JSON document.
func TestAutomationTestJSONExitCode(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "broken")
	os.MkdirAll(filepath.Join(dir, "actions"), 0o755)
	// Invalid as a local package (no domains): the validate row fails.
	os.WriteFile(filepath.Join(dir, "automation.json"), []byte(`{"schema":"monoagent.automation/v1","id":"broken",
		"name":"B","version":"0.1.0","site":{"startUrl":"","domains":[]},"permissions":{"steps":["log"],"scripts":[]},
		"actions":["a"],"policy":{"tier":"standard"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "actions", "a.json"), []byte(`{"actionType":"a","automation":"broken","steps":[{"id":"s","type":"log"}]}`), 0o644)

	out, _, err := runAutomationCLI(t, home, "automation", "test", dir, "--json")
	if err == nil {
		t.Fatal("automation test --json succeeded with a failing row")
	}
	var res struct {
		Results []fixtureResult `json:"results"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&res); err != nil || len(res.Results) == 0 || res.Results[0].Status != "fail" {
		t.Fatalf("stdout %q: %v", out, err)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document on stdout: %q", out)
	}
}
