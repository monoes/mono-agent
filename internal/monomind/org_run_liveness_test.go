package monomind

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

func TestSameStartID(t *testing.T) {
	cases := []struct{ recorded, now, want string }{
		{"linux:boot:100", "linux:boot:100", "same"},
		{"linux:boot:100", "linux:boot:101", "different"},
		{"linux:bootA:100", "linux:bootB:100", "different"},
		{"linux::100", "linux:bootB:100", "same"}, // no boot id: start time alone
		{"ps:Mon Jan 1", "ps:Mon Jan 1", "same"},
		{"ps:Mon Jan 1", "ps:Tue Jan 2", "different"},
		{"ps:Mon Jan 1", "linux:boot:100", ""}, // read by different methods
	}
	for _, c := range cases {
		if got := sameStartID(c.recorded, c.now); got != c.want {
			t.Errorf("sameStartID(%q, %q) = %q, want %q", c.recorded, c.now, got, c.want)
		}
	}
}

// A record's pid that answers but whose start identity differs is a reused
// pid: the run is dead. Without pidStart the pid alone is trusted.
func TestOrgRunDeadHonoursPidStart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("start identity is read from /proc")
	}
	own := processStartID(os.Getpid())
	if own == "" {
		t.Skip("no /proc start identity here")
	}
	root := t.TempDir()
	rec := func(pidStart string) {
		body := fmt.Sprintf(`{"status":"running","run":"r1","pid":%d`, os.Getpid())
		if pidStart != "" {
			body += fmt.Sprintf(`,"pidStart":%q`, pidStart)
		}
		writeRuntime(t, root, "growth", body+"}")
	}

	rec(own)
	if OrgRunDead(root, "growth") || !OrgRunLive(root, "growth") {
		t.Error("matching pidStart: run should be live")
	}
	rec("")
	if OrgRunDead(root, "growth") || !OrgRunLive(root, "growth") {
		t.Error("no pidStart: the pid alone is trusted")
	}
	rec("linux:no-such-boot:1")
	if !OrgRunDead(root, "growth") || OrgRunLive(root, "growth") {
		t.Error("different pidStart: pid was reused, run should be dead")
	}
	rec("ps:Mon Jan 1 00:00:00 2024")
	if OrgRunDead(root, "growth") {
		t.Error("incomparable pidStart must not call a live run dead")
	}
}
