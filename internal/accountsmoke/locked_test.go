//go:build devaccount && !windows

package accountsmoke

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Acceptance 1, the command line as a person meets it: after the date, with no valid session, a
// gated command exits 4 with login_required and does nothing else but leave the clock-guard record
// of a machine that never signed in (A25); an open command still works. Signing in opens the
// machine, and logging out closes it again. (The doors are Task 4's.)
func TestNoSessionAfterTheDateIsRefusedAndNothingIsWritten(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})

	// A command that would write (it creates the database and a workflow) is refused first: exit 4,
	// the fixed first line, the JSON error, and nothing in HOME but the two files of the record: no
	// first-run writes, no database, no refresh token.
	def := filepath.Join(r.dir, "refused.json")
	must(t, os.WriteFile(def, []byte(waitWorkflow("smoke-refused", 1)), 0o600))
	from := time.Now()
	r.assertLocked(r.run("--json", "workflow", "import", "--file", def), "not_logged_in", true)
	r.assertOnlyTheClockGuardRecord(from, time.Now())
	r.assertLocked(r.run("workflow", "list"), "not_logged_in", false)
	r.assertLocked(r.run("org", "serve", "--foreground"), "not_logged_in", false) // a launcher, not a serving command (spec A1)
	mustExit(t, r.run("version"), 0)                                              // an open command needs no account

	r.signIn()
	mustExit(t, r.run("workflow", "list"), 0)
	mustExit(t, r.run("account", "logout"), 0)
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("logging out deletes the refresh token: %v", err)
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "not_logged_in", true)
}
