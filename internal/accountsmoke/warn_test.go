//go:build devaccount && !windows

package accountsmoke

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance 4: before the date nothing locks, and every surface warns: the stderr line (once,
// never on stdout), `account status` (what the desktop banner reads), the doctor row and the
// daemon's heartbeat. And nothing is written to the account folder: the clock-guard record of
// A25 is for the days after the date.
func TestNothingLocksBeforeTheDateAndEverySurfaceWarns(t *testing.T) {
	date := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	r := newRig(t, rigOptions{enforce: date})

	res := r.run("--json", "workflow", "list")
	mustExit(t, res, 0)
	if !strings.HasPrefix(strings.TrimSpace(res.stdout), "[") || strings.Contains(res.stdout, warnLine) {
		t.Fatalf("stdout must stay the command's own JSON: %s", res.stdout)
	}
	if n := strings.Count(res.stderr, warnLine); n != 1 || !strings.Contains(res.stderr, "monoagentcli account login") {
		t.Fatalf("the warning must appear once on stderr, with the way out, got %d times:\n%s", n, res.stderr)
	}
	if st, _ := r.status(); st.State != "locked" || st.Reason != "not_logged_in" || st.Enforced || !st.EnforceFrom.Equal(date) {
		t.Fatalf("account status in the warn period: %+v", st)
	}

	var report struct {
		Results []struct{ ID, Status string }
	}
	mustJSON(t, r.run("--json", "doctor", "--check", "core.monoes_account").stdout, &report)
	row := ""
	for _, x := range report.Results {
		if x.ID == "core.monoes_account" {
			row = x.Status
		}
	}
	if row != "warn" {
		t.Fatalf("the doctor row should warn in the warn period, it is %q", row)
	}

	r.startDaemon()
	r.waitFor("the heartbeat's account state", 30*time.Second, func() bool { return r.accountState() != "" })
	// With no session the heartbeat reports the state the warning is about, and that nothing is
	// enforced yet.
	_, hb := r.heartbeatAccount()
	hb.is(t, "locked", false, false)
	id := r.enqueue(r.workflow(waitWorkflow("smoke-fast", 1), true))
	r.waitFor("the daemon to run work although the state is locked", 60*time.Second, func() bool { return r.execution(id).Status == "SUCCESS" })
	if _, err := os.Stat(r.accountDir()); !os.IsNotExist(err) {
		t.Fatalf("before the date nothing is written to the account folder, the daemon's refresher included (A25): %v", err)
	}
}
