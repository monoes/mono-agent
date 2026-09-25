package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/recording"
)

type fakePicker struct {
	fp     *recording.Fingerprint
	url    string
	err    error
	prompt string
	closed bool
}

func (f *fakePicker) Pick(_ context.Context, prompt string, _ time.Duration) (*recording.Fingerprint, string, error) {
	f.prompt = prompt
	return f.fp, f.url, f.err
}
func (f *fakePicker) Close() error { f.closed = true; return nil }

type fakeReplacer struct {
	id, key string
	entry   action.SelectorEntry
}

func (f *fakeReplacer) ReplaceSelector(id, key string, e action.SelectorEntry) (string, error) {
	f.id, f.key, f.entry = id, key, e
	return "overlay", nil
}

type dirGetter struct{ dir string }

func (d dirGetter) Get(string) (*automation.Package, error) { return automation.OpenDir(d.dir) }

func rerecordFixturePackage(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme")
	os.MkdirAll(filepath.Join(dir, "actions"), 0o755)
	files := map[string]string{
		"automation.json": `{"schema":"monoagent.automation/v1","id":"acme","name":"Acme","version":"0.1.0",
			"site":{"startUrl":"https://acme.test/","domains":["acme.test"]},
			"permissions":{"steps":["click"],"scripts":[]},"actions":["a"],"policy":{"tier":"standard"}}`,
		"actions/a.json": `{"actionType":"a","automation":"acme","sideEffects":"none","steps":[{"id":"c","type":"click","configKey":"save.button"}]}`,
		"selectors.json": `{"save.button":{"candidates":[{"css":"#old"}],"intent":"the Save button"}}`,
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAutomationRerecordBuildsEntry(t *testing.T) {
	dir := rerecordFixturePackage(t)
	picker := &fakePicker{url: "https://acme.test/edit", fp: &recording.Fingerprint{Candidates: []recording.Candidate{
		{Kind: "css", Value: ".btn", Unique: false, Score: 0.9},
		{Kind: "text", Value: "Save", Unique: true, Score: 0.5},
		{Kind: "aria", Role: "button", Name: "Save", Unique: true, Score: 0.8},
		{Kind: "css", Value: "#save", Unique: true, Score: 0.95},
		{Kind: "bogus", Value: "x", Unique: true, Score: 1},
	}}}
	var openedURL string
	orig := rerecordOpenPicker
	rerecordOpenPicker = func(_ context.Context, url string, _ bool) (elementPicker, error) {
		openedURL = url
		return picker, nil
	}
	t.Cleanup(func() { rerecordOpenPicker = orig })

	rep := &fakeReplacer{}
	res, err := rerecordSelector(context.Background(), dirGetter{dir}, rep, "acme", "save.button", "", time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	if openedURL != "https://acme.test/" || picker.prompt != "Click: the Save button" || !picker.closed {
		t.Fatalf("opened %q prompt %q closed %v", openedURL, picker.prompt, picker.closed)
	}
	c := rep.entry.Candidates
	if len(c) != 4 || c[0].CSS != "#save" || c[1].Aria == nil || c[2].Text != "Save" || c[3].CSS != ".btn" {
		t.Fatalf("candidates = %+v", c)
	}
	if rep.entry.Intent != "the Save button" || rep.entry.VerifiedAt == "" || rep.key != "save.button" {
		t.Fatalf("entry = %+v", rep.entry)
	}
	if res.Where != "overlay" || res.URL != "https://acme.test/edit" || res.Automation != "acme" || len(res.Candidates) != 4 {
		t.Fatalf("result = %+v", res)
	}
}

func TestAutomationRerecordErrors(t *testing.T) {
	dir := rerecordFixturePackage(t)
	orig := rerecordOpenPicker
	t.Cleanup(func() { rerecordOpenPicker = orig })
	for _, tc := range []struct {
		key, want string
		pickErr   error
	}{
		{"no.such.key", `automation acme has no selector "no.such.key"`, nil},
		{"save.button", "cancelled", errors.New("extension: cancelled")},
		{"save.button", "timeout", errors.New("pick_element: timeout")},
		{"save.button", "cancelled", extension.ErrPickCancelled},
		{"save.button", "timeout", context.DeadlineExceeded},
		{"save.button", "browser bridge not connected", extension.ErrBridgeNotConnected},
	} {
		picker := &fakePicker{err: tc.pickErr}
		rerecordOpenPicker = func(context.Context, string, bool) (elementPicker, error) { return picker, nil }
		rep := &fakeReplacer{}
		_, err := rerecordSelector(context.Background(), dirGetter{dir}, rep, "acme", tc.key, "", time.Minute, false)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: err = %v, want %q", tc.key, err, tc.want)
		}
		if rep.key != "" {
			t.Errorf("%s: selector replaced despite the error", tc.key)
		}
	}
}

func TestAutomationRerecordDropsSensitiveText(t *testing.T) {
	fp := &recording.Fingerprint{Sensitive: true, Candidates: []recording.Candidate{
		{Kind: "text", Value: "hunter2", Unique: true, Score: 0.9},
		{Kind: "css", Value: "#pw", Unique: true, Score: 0.8},
	}}
	e := selectorEntryFromFingerprint(fp, "", time.Unix(0, 0))
	if len(e.Candidates) != 1 || e.Candidates[0].CSS != "#pw" {
		t.Fatalf("candidates = %+v", e.Candidates)
	}
}

func TestAutomationDoctorSuggestsRerecord(t *testing.T) {
	if got := rerecordSuggestion("acme", "save.button"); got != "run: monoagentcli automation rerecord acme save.button" {
		t.Fatalf("suggestion = %q", got)
	}
}
