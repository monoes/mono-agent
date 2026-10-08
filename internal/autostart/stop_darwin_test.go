//go:build darwin

package autostart

import (
	"encoding/xml"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// launchd kills a job 20 s after its SIGTERM unless ExitTimeOut says otherwise.
func TestThePlistGivesTheDaemonTimeToFinishARefresh(t *testing.T) {
	plist, err := renderPlist("/usr/local/bin/monoagentcli", "/Users/ada/.monoagent/logs")
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal([]byte(plist), new(struct{ XMLName xml.Name })); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, plist)
	}
	m := regexp.MustCompile(`<key>ExitTimeOut</key>\s*<integer>(\d+)</integer>`).FindStringSubmatch(plist)
	if m == nil {
		t.Fatalf("no ExitTimeOut:\n%s", plist)
	}
	seconds, _ := strconv.Atoi(m[1])
	if got := time.Duration(seconds) * time.Second; got < stopGrace {
		t.Fatalf("ExitTimeOut is %v, want at least %v", got, stopGrace)
	}
}
