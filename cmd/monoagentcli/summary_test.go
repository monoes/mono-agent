package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/storage"
)

func newSummaryCLITestDB(t *testing.T) *globalConfig {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	dbPath := filepath.Join(t.TempDir(), "s.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w1','A',1,'default');
		INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, status) VALUES ('h1','e','w1','n','N','pending')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}
}

func runSummary(t *testing.T, cfg *globalConfig, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		cmd := newSummaryCmd(cfg)
		cmd.SetArgs(args)
		err = cmd.Execute()
	})
	return out, err
}

func TestSummaryJSONHasEverySection(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	out, err := runSummary(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"v", "generated_at", "profile_id",
		"workflows", "executions", "schedules", "hil", "people", "activity", "applications",
		"services", "automations", "recordings", "jev", "accounts", "vault"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q in %s", k, out)
		}
	}
	var hil struct {
		WorkflowPending int `json:"workflow_pending"`
	}
	if err := json.Unmarshal(got["hil"], &hil); err != nil || hil.WorkflowPending != 1 {
		t.Errorf("hil = %s", got["hil"])
	}
	for _, k := range []string{"executions", "hil", "people", "activity", "applications", "jev", "accounts", "vault"} {
		if strings.Contains(string(got[k]), `"error"`) {
			t.Errorf("section %s reported an error: %s", k, got[k])
		}
	}
}

func TestSummarySectionFilter(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	out, err := runSummary(t, cfg, "--section", "hil,workflows")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["executions"]; ok {
		t.Fatalf("unrequested section present: %s", out)
	}
	if _, ok := got["hil"]; !ok {
		t.Fatalf("requested section missing: %s", out)
	}
	if _, err := runSummary(t, cfg, "--section", "nope"); exitCode(err) != 3 {
		t.Fatalf("unknown section exit = %d, want 3 (invalid input)", exitCode(err))
	}
}

func TestSummaryText(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	cfg.JSONOutput = false
	out, err := runSummary(t, cfg, "--section", "hil,workflows")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "needs you     1") || !strings.Contains(out, "workflows") {
		t.Fatalf("text = %s", out)
	}
}

// The dashboard polls this every 15 s: it must never reach TypeSafe, even
// with a key configured and surfaces enabled.
func TestSummaryNeverCallsJev(t *testing.T) {
	cfg := newSummaryCLITestDB(t)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	srv := jevtest.NewServer(t, jevtest.Fixed(nil))
	out, err := runSummary(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Jev struct {
			KeyConfigured bool `json:"key_configured"`
		} `json:"jev"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || !got.Jev.KeyConfigured {
		t.Fatalf("expected the env key to be seen: %s", out)
	}
	if n := srv.Calls(); n != 0 {
		t.Fatalf("summary made %d Jev calls", n)
	}
}
