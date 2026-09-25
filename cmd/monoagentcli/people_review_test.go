package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/peoplereview"
	"github.com/monoes/mono-agent/internal/storage"
)

// newReviewCLITestDB seeds a migrated DB with two people awaiting review
// and returns a config pointing at it. HOME is isolated so the workflow
// file store is empty.
func newReviewCLITestDB(t *testing.T) (*globalConfig, *storage.Database) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "review.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p1", "p2"} {
		if _, err := db.DB.Exec(`INSERT INTO people (id, profile_id, platform, platform_username, full_name, category, introduction)
			VALUES (?, 'default', 'LINKEDIN', ?, 'Sam', ?, 'drafted')`, id, id+"-user", peoplereview.Pending); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}, db
}

func runReview(t *testing.T, cfg *globalConfig, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		cmd := newPeopleReviewCmd(cfg)
		cmd.SetArgs(args)
		err = cmd.Execute()
	})
	return out, err
}

func exitCode(err error) int {
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	if err != nil {
		return 1
	}
	return 0
}

func category(t *testing.T, db *storage.Database, id string) string {
	t.Helper()
	var c string
	if err := db.DB.QueryRow(`SELECT category FROM people WHERE id = ?`, id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPeopleReviewQueue(t *testing.T) {
	cfg, db := newReviewCLITestDB(t)

	out, err := runReview(t, cfg, "list")
	var pending []peoplereview.Person
	if err != nil || json.Unmarshal([]byte(out), &pending) != nil || len(pending) != 2 {
		t.Fatalf("list = %q, %v", out, err)
	}

	out, err = runReview(t, cfg, "approve", "p1", "--intro", "Hi Sam")
	var res reviewApproval
	if err != nil || json.Unmarshal([]byte(out), &res) != nil {
		t.Fatalf("approve = %q, %v", out, err)
	}
	if res.Person.Category != peoplereview.Approved || res.Person.Introduction != "Hi Sam" || res.Send != nil {
		t.Fatalf("approve result: %+v", res)
	}

	if _, err := runReview(t, cfg, "reject", "p2"); err != nil {
		t.Fatal(err)
	}
	if got := category(t, db, "p2"); got != peoplereview.Rejected {
		t.Fatalf("p2 category = %q", got)
	}
	if _, err := runReview(t, cfg, "reject", "nobody"); exitCode(err) != 2 {
		t.Fatalf("reject unknown: exit %d (%v)", exitCode(err), err)
	}
	if _, err := runReview(t, cfg, "approve", "p1", "--send", "--send-plan"); exitCode(err) != 3 {
		t.Fatalf("--send with --send-plan: exit %d (%v)", exitCode(err), err)
	}
}

// A send that can't happen must not take the person out of the queue, and
// the dispatch workflow is found by name within the profile — never a
// fixed id.
func TestPeopleReviewSendPlan(t *testing.T) {
	cfg, db := newReviewCLITestDB(t)

	if _, err := runReview(t, cfg, "approve", "p1", "--send-plan"); exitCode(err) != 2 {
		t.Fatalf("no dispatch workflow: exit %d (%v)", exitCode(err), err)
	}
	if got := category(t, db, "p1"); got != peoplereview.Pending {
		t.Fatalf("p1 left the queue on a failed send: %q", got)
	}

	addWF := func(id, profile, name string, active bool) {
		t.Helper()
		if _, err := db.DB.Exec(`INSERT INTO workflows (id, profile_id, name, is_active) VALUES (?, ?, ?, ?)`, id, profile, name, active); err != nil {
			t.Fatal(err)
		}
	}
	addWF("w-other", "someone-else", "Send Approved DMs", true)
	addWF("w1", "default", "LinkedIn: Send Approved DMs", false)

	if _, err := runReview(t, cfg, "approve", "p1", "--send-plan"); exitCode(err) != 3 {
		t.Fatalf("inactive dispatch workflow: exit %d (%v)", exitCode(err), err)
	}
	if got := category(t, db, "p1"); got != peoplereview.Pending {
		t.Fatalf("p1 left the queue on an inactive workflow: %q", got)
	}

	if _, err := db.DB.Exec(`UPDATE workflows SET is_active = 1 WHERE id = 'w1'`); err != nil {
		t.Fatal(err)
	}
	out, err := runReview(t, cfg, "approve", "p1", "--intro", "Hi Sam", "--send-plan")
	var res reviewApproval
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Send == nil {
		t.Fatalf("send plan = %q, %v", out, err)
	}
	if res.Send.WorkflowID != "w1" || res.Send.Input["introduction"] != "Hi Sam" || res.Send.Input["platform_username"] != "p1-user" {
		t.Fatalf("send plan: %+v", res.Send)
	}
	if got := category(t, db, "p1"); got != peoplereview.Approved {
		t.Fatalf("p1 category = %q", got)
	}

	addWF("w2", "default", "Send Approved DMs (X)", true)
	if _, err := runReview(t, cfg, "approve", "p2", "--send-plan"); exitCode(err) != 3 {
		t.Fatalf("ambiguous: exit %d (%v)", exitCode(err), err)
	}
	out, err = runReview(t, cfg, "approve", "p2", "--send-plan", "--send-workflow", "w2")
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Send.WorkflowID != "w2" {
		t.Fatalf("explicit workflow = %q, %v", out, err)
	}
}
