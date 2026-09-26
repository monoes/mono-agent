//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The profile, template and monomind-project bindings shell out to
// `monoagentcli` and never read the database themselves.
func TestProfileBindingsShellOut(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *"profile list"*) echo '[{"id":"default","name":"Default","created_at":"2026-01-01T00:00:00Z","root_dir":"/p/default","icon":"","active":true},{"id":"work","name":"Work","created_at":"2026-02-01T00:00:00Z","root_dir":"/p/work","icon":"fox","active":false}]' ;;
  *"profile get work"*) echo '{"id":"work","name":"Work","created_at":"2026-02-01T00:00:00Z","root_dir":"/p/work","icon":"fox","active":false}' ;;
  *"profile create"*) echo '{"id":"new-id","name":"-odd name","created_at":"2026-09-26T10:00:00Z","root_dir":"/chosen","icon":"owl","active":false}' ;;
  *"profile switch"*) echo '{"id":"default"}' ;;
  *"profile folder"*) echo '{"id":"work","root_dir":"/p/work"}' ;;
  *"profile projects"*) echo '[{"path":"/code/app","name":"app"}]' ;;
  *"template list"*) echo '[{"id":3,"name":"Hi","subject":"S","body":"B","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]' ;;
esac
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")

	profiles, err := a.GetProfiles()
	if err != nil || len(profiles) != 2 {
		t.Fatalf("GetProfiles = %+v, %v", profiles, err)
	}
	// is_active is the app's own active profile, not the CLI's persisted one.
	if profiles[0].IsActive || !profiles[1].IsActive || profiles[1].RootDir != "/p/work" || profiles[1].Icon != "fox" {
		t.Fatalf("GetProfiles = %+v", profiles)
	}
	active, err := a.GetActiveProfile()
	if err != nil || active.ID != "work" || !active.IsActive || active.Name != "Work" {
		t.Fatalf("GetActiveProfile = %+v, %v", active, err)
	}
	created, err := a.createProfile("-odd name", " /chosen ", "owl")
	if err != nil || created.ID != "new-id" || created.RootDir != "/chosen" || created.info(false).Icon != "owl" {
		t.Fatalf("createProfile = %+v, %v", created, err)
	}
	if id, err := a.switchProfile("default"); err != nil || id != "default" || a.getActiveProfileID() != "default" {
		t.Fatalf("switchProfile = %q, %v (active %q)", id, err, a.getActiveProfileID())
	}
	if dir, err := a.profileFolder("work"); err != nil || dir != "/p/work" {
		t.Fatalf("profileFolder = %q, %v", dir, err)
	}
	if got := a.ListMonomindProjects(); len(got) != 1 || got[0].Path != "/code/app" || got[0].Name != "app" {
		t.Fatalf("ListMonomindProjects = %+v", got)
	}
	if got := a.GetTemplates(); len(got) != 1 || got[0].ID != 3 || got[0].Name != "Hi" || got[0].Subject != "S" || got[0].Body != "B" {
		t.Fatalf("GetTemplates = %+v", got)
	}

	want := []string{
		"--profile work --json profile list",
		"--profile work --json profile get work",
		"--profile work --json profile create --root-dir /chosen --icon owl -- -odd name",
		"--profile work --json profile switch default",
		"--profile default --json profile folder work",
		"--profile default --json profile projects",
		"--profile default --json template list",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Empty or failed reads keep the shapes the frontend already handles.
func TestProfileBindingsOnCLIFailure(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, "echo 'profile \"nope\" not found' >&2; exit 2\n"))
	a := newTestApp(t)
	a.ctx = context.Background()
	if _, err := a.GetProfiles(); err == nil {
		t.Fatal("GetProfiles hid the CLI failure")
	}
	if _, err := a.GetActiveProfile(); err == nil || !strings.Contains(err.Error(), "active profile not found") {
		t.Fatalf("GetActiveProfile err = %v", err)
	}
	if _, err := a.switchProfile("nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("switchProfile err = %v", err)
	}
	if a.getActiveProfileID() != "default" {
		t.Fatalf("a failed switch moved the app to %q", a.getActiveProfileID())
	}
	if got := a.GetTemplates(); got != nil {
		t.Fatalf("GetTemplates = %+v, want nil", got)
	}
	if got := a.ListMonomindProjects(); got == nil || len(got) != 0 {
		t.Fatalf("ListMonomindProjects = %#v, want an empty list", got)
	}
	if _, err := a.profileFolder("nope"); err == nil || !strings.Contains(err.Error(), "preparing profile folder") {
		t.Fatalf("profileFolder err = %v", err)
	}
}

// A destination the move would refuse never stops the folder's orgs.
func TestMoveProfileFolderChecksBeforeStoppingOrgs(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
echo '"/x/.monomind" already contains a file' >&2; exit 3
`))
	a := newTestApp(t) // no lifecycle context: the org steps log to the Logs page
	if err := a.moveProfileFolder("work", "/x"); err == nil || !strings.Contains(err.Error(), "already contains") {
		t.Fatalf("moveProfileFolder = %v", err)
	}
	if got := loggedArgs(t, log); len(got) != 1 || got[0] != "--profile default --json profile move --check work /x" {
		t.Fatalf("CLI calls = %q, want only the check", got)
	}
	if err := a.moveProfileFolder("work", "  "); err == nil || err.Error() != "no folder chosen" {
		t.Fatalf("blank folder: %v", err)
	}
}

// The whole move: check, stop the folder's orgs, move, then reconcile the
// org files (the daemon was not running, so it is not restarted).
func TestMoveProfileFolderSequence(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *"serve --stop"*) echo '{"v":1,"pid":0,"status":"not-running","stopped_orgs":[],"warnings":[]}' ;;
  *"org"*"reconcile"*) echo '{"v":1,"orgs":[],"warnings":[]}' ;;
  *) echo '{"id":"work","moved":true}' ;;
esac
`))
	a := newTestApp(t) // no lifecycle context: the org steps log to the Logs page
	if err := a.moveProfileFolder("work", " /new/place "); err != nil {
		t.Fatal(err)
	}
	got := loggedArgs(t, log)
	want := []string{
		"--profile default --json profile move --check work /new/place",
		"--profile work --json org serve --stop",
		"--profile default --json profile move work /new/place",
		"--profile work --json org reconcile",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
