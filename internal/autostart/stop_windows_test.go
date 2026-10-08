//go:build windows

package autostart

import "testing"

// Restart waits for a stopping daemon as long as the other managers allow it.
func TestRestartWaitsForAStoppingDaemonAsLongAsTheOtherManagersAllowIt(t *testing.T) {
	if daemonStopWait < stopGrace {
		t.Fatalf("daemonStopWait = %v, want at least %v", daemonStopWait, stopGrace)
	}
}
