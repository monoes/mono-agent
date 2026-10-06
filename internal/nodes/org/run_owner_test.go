package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/monoes/mono-agent/internal/vault"
	"github.com/monoes/mono-agent/internal/workflow"
)

// scheduledOrgRoot is a profile root holding an org with the given schedule
// and, when serve is true, a live `monomind org serve` heartbeat (this very
// process's pid, fresh).
func scheduledOrgRoot(t *testing.T, schedule string, serve bool) (context.Context, string) {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE profiles (id TEXT PRIMARY KEY, root_dir TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO profiles (id, root_dir) VALUES ('default', ?)`, root); err != nil {
		t.Fatal(err)
	}
	orgs := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(orgs, 0o755); err != nil {
		t.Fatal(err)
	}
	def := `{"name":"sec","goal":"g","status":"stopped","schedule":` + schedule + `,"roles":[{"id":"lead","title":"Lead","type":"boss","reports_to":null,"responsibilities":[]}]}`
	if err := os.WriteFile(filepath.Join(orgs, "sec.json"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	if serve {
		hb, _ := json.Marshal(map[string]interface{}{"pid": os.Getpid(), "updatedAt": time.Now().UTC().Format(time.RFC3339Nano), "running": []string{}})
		if err := os.WriteFile(filepath.Join(root, ".monomind", "serve-heartbeat.json"), hb, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := vault.ContextWithProfileID(vault.ContextWithDB(context.Background(), db), "default")
	return ctx, root
}

func runOrgNode(ctx context.Context) ([]workflow.NodeOutput, error) {
	return (&OrgRunNode{}).Execute(ctx, workflow.NodeInput{
		Items: []workflow.Item{{JSON: map[string]interface{}{"x": 1}}},
	}, map[string]interface{}{"org_name": "sec"})
}

// One scheduler owner per org: a workflow's schedule trigger must not start
// an org whose own `schedule` a live monomind `org serve` already fires.
func TestOrgRunSkipsScheduleTriggerWhenMonomindServeOwnsTheSchedule(t *testing.T) {
	ctx, _ := scheduledOrgRoot(t, `"1m"`, true)
	ctx = workflow.WithTrigger(ctx, "trigger.schedule", nil)
	out, err := runOrgNode(ctx)
	if err != nil {
		t.Fatalf("a skipped tick is not an error: %v", err)
	}
	if len(out) != 1 || len(out[0].Items) != 1 {
		t.Fatalf("want the input item passed through, got %+v", out)
	}
	j := out[0].Items[0].JSON
	if j["_org_skipped"] == nil || j["x"] != 1 {
		t.Fatalf("want _org_skipped set and the item kept, got %v", j)
	}
}

// Not owned: no schedule on the org, no live serve daemon, or a trigger that
// is not a schedule. These start through the normal path.
func TestMonomindOwnsScheduleNeedsScheduleServeAndScheduleTrigger(t *testing.T) {
	cases := []struct {
		name     string
		schedule string
		serve    bool
		trigger  string
		want     bool
	}{
		{"owned", `"1m"`, true, "trigger.schedule", true},
		{"numeric schedule", `60000`, true, "trigger.schedule", true},
		{"no org schedule", `null`, true, "trigger.schedule", false},
		{"empty schedule", `""`, true, "trigger.schedule", false},
		{"serve not running", `"1m"`, false, "trigger.schedule", false},
		{"manual run", `"1m"`, true, "trigger.manual", false},
		{"unknown trigger", `"1m"`, true, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, root := scheduledOrgRoot(t, c.schedule, c.serve)
			ctx = workflow.WithTrigger(ctx, c.trigger, nil)
			if got := monomindOwnsSchedule(ctx, root, "sec"); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
