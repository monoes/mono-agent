//go:build !windows

package main

import (
	"reflect"
	"testing"
	"time"
)

// #294: the warning names only processes that outlive the runtime. One
// that exits within the settle time (like the MCP servers opencode starts
// and stops with itself) is dropped; one still running is kept.
func TestStillRunningDropsProcessesThatExit(t *testing.T) {
	stays := startBackground(t, "sleep", "60")
	exits := startBackground(t, "sleep", "0.3")
	start := time.Now()
	if got := stillRunning([]int{stays, exits}, 3*time.Second); !reflect.DeepEqual(got, []int{stays}) {
		t.Fatalf("stillRunning = %v, want [%d]", got, stays)
	}
	if d := time.Since(start); d < 2*time.Second {
		t.Errorf("returned after %s: a live process waits out the settle time", d)
	}
	start = time.Now()
	if got := stillRunning([]int{exits}, 3*time.Second); len(got) != 0 {
		t.Fatalf("an exited process = %v", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s with nothing left alive", d)
	}
}
