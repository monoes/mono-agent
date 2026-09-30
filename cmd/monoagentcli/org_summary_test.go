package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
)

func runOrgSummary(t *testing.T, root string, args ...string) map[string]interface{} {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "o.db"), ProfileID: "default"}
	cmd := newOrgCmd(cfg)
	cmd.SetArgs(append([]string{"--project", root, "summary"}, args...))
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("org summary printed non-JSON %q: %v", out, err)
	}
	return m
}

// --fast answers from local files only: two org configs, a queue for one.
func TestOrgSummaryFast(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, root, "acme.json", `{"name":"acme"}`)
	writeOrgFile(t, root, "ops.json", `{"name":"ops"}`)
	msg := `{"fromQualified":"hq:ceo","toRole":"lead","subject":"s","body":"b","ts":1789725600000,"messageId":"%s"}` + "\n"
	writeOrgFile(t, root, "acme/inbox.jsonl", strings.ReplaceAll(msg, "%s", "m1")+strings.ReplaceAll(msg, "%s", "m2"))

	m := runOrgSummary(t, root, "--fast")
	if m["fast"] != true || m["serve_running"] != false {
		t.Fatalf("payload = %v", m)
	}
	orgs := m["orgs"].([]interface{})
	if len(orgs) != 2 {
		t.Fatalf("orgs = %v", orgs)
	}
	acme := orgs[0].(map[string]interface{})
	if acme["name"] != "acme" || acme["queued"] != float64(2) || acme["running"] != false || acme["needs_you"] != nil {
		t.Fatalf("acme = %v", acme)
	}
	totals := m["totals"].(map[string]interface{})
	if totals["orgs"] != float64(2) || totals["queued"] != float64(2) || totals["needs_you"] != float64(0) {
		t.Fatalf("totals = %v", totals)
	}
}

func TestOrgSummaryNoOrgs(t *testing.T) {
	m := runOrgSummary(t, t.TempDir(), "--fast")
	if orgs := m["orgs"].([]interface{}); len(orgs) != 0 {
		t.Fatalf("orgs = %v", orgs)
	}
}

// The per-org budget holds even when monomind runs behind a wrapper whose
// child keeps stdout open past the kill (e.g. an npm .cmd shim).
func TestOrgSummaryNeedsYouTimeoutHolds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(t.TempDir(), "monomind")
	script := "#!/bin/sh\ncase \"$*\" in\n  *--version*) echo '{\"v\":1,\"version\":\"9.9.9\",\"capabilities\":[\"agent-exec\",\"agent-scan\",\"org-json-v1\"]}' ;;\n  *) sleep 6; echo '[]' ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOMIND_BIN", bin)
	old := perOrgNeedsYouTimeout
	perOrgNeedsYouTimeout = time.Second
	t.Cleanup(func() { perOrgNeedsYouTimeout = old })

	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "o.db"), ProfileID: "default"}
	env := &orgEnv{cfg: cfg}
	root := env.Root()
	writeOrgFile(t, root, "acme.json", `{"name":"acme"}`)
	cmd := newOrgSummaryCmd(env)
	var runErr error
	start := time.Now()
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	env.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("org summary took %v with a 1s per-org budget", took)
	}
	var m struct {
		Orgs []struct {
			NeedsYou      *int   `json:"needs_you"`
			NeedsYouError string `json:"needs_you_error"`
		} `json:"orgs"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil || len(m.Orgs) != 1 {
		t.Fatalf("out = %s (%v)", out, err)
	}
	if m.Orgs[0].NeedsYou != nil || !strings.Contains(m.Orgs[0].NeedsYouError, "timed out") {
		t.Fatalf("want a timeout error, got %+v", m.Orgs[0])
	}
}

func TestOrgSummaryAllProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "o.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	rootD, rootW := t.TempDir(), t.TempDir()
	if _, err := db.DB.Exec(`UPDATE profiles SET root_dir = ? WHERE id = 'default'`, rootD); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name, root_dir) VALUES ('p-work', 'Work', ?)`, rootW); err != nil {
		t.Fatal(err)
	}
	db.Close()
	writeOrgFile(t, rootD, "home.json", `{"name":"home"}`)
	writeOrgFile(t, rootW, "acme.json", `{"name":"acme"}`)

	cfg := &globalConfig{DBPath: dbPath, ProfileID: "default"}
	cmd := newOrgCmd(cfg)
	cmd.SetArgs([]string{"summary", "--all-profiles", "--fast"})
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var m struct {
		Scope string `json:"scope"`
		Orgs  []struct {
			Name        string `json:"name"`
			ProfileID   string `json:"profile_id"`
			ProfileName string `json:"profile_name"`
		} `json:"orgs"`
		Totals map[string]int `json:"totals"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if m.Scope != "global" || len(m.Orgs) != 2 || m.Totals["orgs"] != 2 {
		t.Fatalf("payload = %s", out)
	}
	if m.Orgs[0].Name != "home" || m.Orgs[0].ProfileID != "default" || m.Orgs[1].Name != "acme" || m.Orgs[1].ProfileName != "Work" {
		t.Fatalf("orgs = %+v", m.Orgs)
	}
}

// #294: an org run by a standalone `monomind org run` (the GUI's Run button,
// no serve daemon) is running while its runtime.json says so and its pid is
// alive; a dead pid or a stopped record is not.
func TestOrgSummaryStandaloneRunIsRunning(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"live", "dead", "stopped"} {
		writeOrgFile(t, root, n+".json", `{"name":"`+n+`"}`)
	}
	const deadPID = 2147483646 // above any pid_max: no such process
	writeOrgFile(t, root, "live/runtime.json", fmt.Sprintf(`{"status":"running","run":"r1","pid":%d}`, os.Getpid()))
	writeOrgFile(t, root, "dead/runtime.json", fmt.Sprintf(`{"status":"running","run":"r2","pid":%d}`, deadPID))
	writeOrgFile(t, root, "stopped/runtime.json", fmt.Sprintf(`{"status":"stopped","run":"r3","pid":%d}`, os.Getpid()))

	m := runOrgSummary(t, root, "--fast")
	got := map[string]interface{}{}
	for _, o := range m["orgs"].([]interface{}) {
		row := o.(map[string]interface{})
		got[row["name"].(string)] = row["running"]
	}
	if got["live"] != true || got["dead"] != false || got["stopped"] != false {
		t.Fatalf("running = %v, want only live", got)
	}
	if m["serve_running"] != false || m["totals"].(map[string]interface{})["running"] != float64(1) {
		t.Fatalf("payload = %v", m)
	}
}
