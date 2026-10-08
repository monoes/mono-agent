//go:build linux

package autostart

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The unit says how long it allows a stopping daemon, in [Service], never less than stopGrace and
// never less than systemd's stock 90 s, because the daemon also drains the runs in flight.
func TestTheUnitGivesTheDaemonTimeToFinishARefresh(t *testing.T) {
	unit, err := renderUnit("/usr/local/bin/monoagentcli")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^TimeoutStopSec=(\d+)$`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatalf("the unit sets no TimeoutStopSec:\n%s", unit)
	}
	seconds, _ := strconv.Atoi(m[1])
	if got := time.Duration(seconds) * time.Second; got < stopGrace || got < 90*time.Second {
		t.Fatalf("TimeoutStopSec is %v, want at least %v and no less than 90s", got, stopGrace)
	}
	at := strings.Index(unit, "TimeoutStopSec=")
	if service, install := strings.Index(unit, "[Service]"), strings.Index(unit, "[Install]"); at < service || at > install {
		t.Fatalf("TimeoutStopSec is not in the [Service] section:\n%s", unit)
	}
}
