//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeResourcesCLI installs a monoagentcli stand-in that logs one argument
// per line and runs body.
func fakeResourcesCLI(t *testing.T, body string) (argsLog string) {
	t.Helper()
	argsLog = filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `printf '%s\n' "$@" > '`+argsLog+"'\n"+body))
	return argsLog
}

func newResourcesTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	return a
}

// assertNoCredentialInArgv checks the argv carries only picker parameters:
// the binding never sees a token, so none can reach the process list.
func assertNoCredentialInArgv(t *testing.T, argv []string) {
	t.Helper()
	for _, a := range argv {
		if strings.Contains(a, "token") || strings.Contains(a, "ya29.") || strings.Contains(a, "xox") {
			t.Fatalf("argv carries a credential-looking value %q: %q", a, argv)
		}
	}
}

func TestListResourcesShellsOutToCLI(t *testing.T) {
	log := fakeResourcesCLI(t, `echo '{"items":[{"id":"sheet1","name":"Budget","metadata":{"modified_time":"2026-09-02T10:00:00Z"}}]}'`)
	a := newResourcesTestApp(t)

	got := a.ListResources("google_sheets", "spreadsheets", "conn-1", "--bud get")
	want := ResourceListResult{Items: []ResourceItem{{
		ID: "sheet1", Name: "Budget", Metadata: map[string]interface{}{"modified_time": "2026-09-02T10:00:00Z"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListResources = %+v, want %+v", got, want)
	}
	argv := loggedArgs(t, log)
	wantArgv := []string{"--profile", "work", "--json", "connect", "resources",
		"--platform=google_sheets", "--type=spreadsheets", "--query=--bud get", "--", "conn-1"}
	if !reflect.DeepEqual(argv, wantArgv) {
		t.Fatalf("argv = %q\nwant   %q", argv, wantArgv)
	}
	assertNoCredentialInArgv(t, argv)

	// No query: no --query flag; an empty item list stays [] for the picker.
	log = fakeResourcesCLI(t, `echo '{"items":[]}'`)
	got = a.ListResources("slack", "users", "conn-2", "")
	if got.Items == nil || len(got.Items) != 0 || got.Error != "" {
		t.Fatalf("ListResources(slack) = %+v", got)
	}
	wantArgv = []string{"--profile", "work", "--json", "connect", "resources",
		"--platform=slack", "--type=users", "--", "conn-2"}
	if argv := loggedArgs(t, log); !reflect.DeepEqual(argv, wantArgv) {
		t.Fatalf("argv = %q\nwant   %q", argv, wantArgv)
	}
}

// CLI failures come back as the error field; a missing or foreign
// credential and a rejected token set needs_reauth, other failures don't.
func TestListResourcesMapsCLIErrors(t *testing.T) {
	a := newResourcesTestApp(t)
	for _, tc := range []struct {
		name, script, wantErr string
		reauth                bool
	}{
		{"unknown credential", `echo 'credential lookup: connection "conn-1" not found' >&2; exit 2`,
			`credential lookup: connection "conn-1" not found`, true},
		{"rejected token", `echo 'google API returned 401: {"error":"[redacted]"}' >&2; exit 4`,
			`google API returned 401: {"error":"[redacted]"}`, true},
		{"unsupported type", `echo 'gmail: unsupported resource type "drafts"' >&2; exit 3`,
			`gmail: unsupported resource type "drafts"`, false},
	} {
		fakeResourcesCLI(t, tc.script)
		got := a.ListResources("gmail", "labels", "conn-1", "")
		if got.Error != tc.wantErr || got.NeedsReauth != tc.reauth || got.Items == nil {
			t.Fatalf("%s: ListResources = %+v", tc.name, got)
		}
	}
}

func TestCreateResourceShellsOutToCLI(t *testing.T) {
	log := fakeResourcesCLI(t, `echo '{"item":{"id":"sheet-new","name":"Q3 plan"}}'`)
	a := newResourcesTestApp(t)

	got := a.CreateResource("google_sheets", "spreadsheets", "conn-1", "-Q3 plan")
	if got.Error != "" || got.Item == nil || !reflect.DeepEqual(*got.Item, ResourceItem{ID: "sheet-new", Name: "Q3 plan"}) {
		t.Fatalf("CreateResource = %+v", got)
	}
	argv := loggedArgs(t, log)
	wantArgv := []string{"--profile", "work", "--json", "connect", "resources", "create",
		"--platform=google_sheets", "--type=spreadsheets", "--name=-Q3 plan", "--", "conn-1"}
	if !reflect.DeepEqual(argv, wantArgv) {
		t.Fatalf("argv = %q\nwant   %q", argv, wantArgv)
	}
	assertNoCredentialInArgv(t, argv)

	fakeResourcesCLI(t, `echo 'create not supported for platform "slack"' >&2; exit 3`)
	got = a.CreateResource("slack", "channels", "conn-1", "x")
	if got.Item != nil || got.Error != `create not supported for platform "slack"` {
		t.Fatalf("CreateResource(slack) = %+v", got)
	}
}
