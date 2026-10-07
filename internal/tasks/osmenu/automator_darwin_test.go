//go:build darwin

package osmenu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAutomatorRunsTheWorkflow runs the rendered workflow with Automator's own
// runner, against the stubs of the script tests: no real monoagentcli runs and
// no notification appears. Opt-in, because it starts Automator's machinery on
// this Mac: MONOAGENT_AUTOMATOR_TEST=1.
func TestAutomatorRunsTheWorkflow(t *testing.T) {
	if os.Getenv("MONOAGENT_AUTOMATOR_TEST") != "1" {
		t.Skip("set MONOAGENT_AUTOMATOR_TEST=1 to run the workflow with /usr/bin/automator")
	}
	if _, err := os.Stat("/usr/bin/automator"); err != nil {
		t.Skip("no automator")
	}
	r := newRig(t)
	b, err := render(Spec{CLI: r.view.CLI, DBPath: r.view.DBPath, ProfileID: "work-id", ProfileName: "Work"}, r.view.Osascript)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Install(filepath.Join(r.dir, "Services"), b, false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// automator -i makes one input item of each newline-terminated line (so the
	// last line ends with one); a Services click hands one text item. The bytes
	// that arrive are logged for the record.
	out, err := exec.CommandContext(ctx, "/usr/bin/automator", "-i", "Reply to Sam\nabout the invoice\n", res.Path).CombinedOutput()
	if err != nil {
		t.Fatalf("automator: %v\n%s", err, out)
	}
	got := r.read("stdin")
	t.Logf("standard input as monoagentcli read it: %q", got)
	first, second := strings.Index(got, "Reply to Sam"), strings.Index(got, "about the invoice")
	if first < 0 || second < first {
		t.Errorf("the selection did not reach monoagentcli in order: %q", got)
	}
	if args := r.read("args"); !strings.Contains(args, "--source\nos\n--app=Safari\n") {
		t.Errorf("arguments:\n%s", args)
	}
	if note := r.read("notified"); !strings.HasSuffix(note, "\nAdded to Inbox in Work\n") {
		t.Errorf("notification:\n%s", note)
	}
}
