package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseServeStop(t *testing.T) {
	out, was, err := parseServeStop(`{"v":1,"root":"/r","pid":42,"status":"stopped","stopped_orgs":["growth"],"warnings":[]}`)
	if err != nil || !was || out.PID != 42 || len(out.StoppedOrgs) != 1 {
		t.Fatalf("stopped: %+v %v %v", out, was, err)
	}
	if _, was, err := parseServeStop(`{"v":1,"pid":0,"status":"not-running","stopped_orgs":[],"warnings":[]}`); err != nil || was {
		t.Fatalf("not running: %v %v", was, err)
	}
	if _, _, err := parseServeStop(`{"error":"org daemon (pid 7) did not exit"}`); err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("error payload: %v", err)
	}
	if _, _, err := parseServeStop(`not json`); err == nil {
		t.Fatal("unreadable output accepted")
	}
}

func TestCLIFailure(t *testing.T) {
	if msg, failed := cliFailure(`{"error":"boom"}`); !failed || msg != "boom" {
		t.Fatalf("error payload: %q %v", msg, failed)
	}
	// An org outcome carrying its own error is not a failed command.
	if _, failed := cliFailure(`{"v":1,"orgs":[{"org":"x","error":"unreadable"}],"warnings":["x: unreadable"]}`); failed {
		t.Fatal("per-org error read as a command failure")
	}
	if _, failed := cliFailure(`garbage`); !failed {
		t.Fatal("garbage read as success")
	}
}

// C-24: when the folder's orgs or org daemon cannot be stopped, the folder
// must not move — the daemon would keep running orgs out of a folder the
// profile no longer points at.
func TestMoveProfileFolderRefusesWhenOrgsCannotStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a shell script")
	}
	a := newTestApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	argvLog := filepath.Join(home, "argv.log")
	fake := filepath.Join(home, "monoagentcli")
	script := "#!/bin/sh\necho \"$*\" >> " + argvLog + "\n" +
		"echo 'org daemon (pid 7) did not exit' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENTCLI_BIN", fake)
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO profiles (id, name) VALUES ('work', 'Work')`); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, "elsewhere")
	err := a.MoveProfileFolder("work", dest)
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("MoveProfileFolder = %v, want the stop failure", err)
	}
	var root string
	if err := a.db.QueryRow(`SELECT root_dir FROM profiles WHERE id = 'work'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if root != "" {
		t.Fatalf("root_dir changed to %q although the move was refused", root)
	}
	if _, err := os.Stat(filepath.Join(dest, ".monomind")); !os.IsNotExist(err) {
		t.Fatalf("destination was populated: %v", err)
	}
	b, _ := os.ReadFile(argvLog)
	if got := strings.TrimSpace(string(b)); got != "--profile work --json org serve --stop" {
		t.Fatalf("CLI calls = %q, want only the stop", got)
	}
}
