package monomind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// OrgEvents must deliver every line the subprocess wrote, including when the
// process exits the instant it finishes writing.
//
// cmd.Wait closes the stdout pipe as soon as the process exits, so calling it
// concurrently with the scanner raced: a fast-exiting `org events` could have
// its output closed out from under the reader, and the caller silently saw
// fewer events, or none. It surfaced as a CI-only failure ("delivered 0
// lines, want 2") because losing the race needs a busy machine — a burst of
// lines from a process that exits immediately is what makes it reachable
// here.
func TestOrgEventsDrainsEverythingBeforeWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runner is a shell script")
	}
	const want = 500

	dir := t.TempDir()
	bin := filepath.Join(dir, "burst-monomind.sh")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.10.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
i=1
while [ $i -le %d ]; do
  printf '{"v":1,"id":"e%%s"}\n' "$i"
  i=$((i + 1))
done
exit 0
`, want)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)

	// The consumer is deliberately slow on its first line, and the producer
	// exits the moment it has written. That is what makes the race
	// reproducible instead of hoping for a busy machine: by the time the
	// reader wakes up the process is long gone, so the old code's concurrent
	// Wait had already closed the pipe and the rest of the burst was lost.
	got := 0
	err := OrgEvents(context.Background(), ".", "growth", OrgEventsOptions{}, func([]byte) {
		if got == 0 {
			time.Sleep(150 * time.Millisecond)
		}
		got++
	})
	if err != nil {
		t.Fatalf("OrgEvents: %v", err)
	}
	if got != want {
		t.Fatalf("delivered %d lines, want %d — Wait closed the pipe before the reader drained it", got, want)
	}
}
