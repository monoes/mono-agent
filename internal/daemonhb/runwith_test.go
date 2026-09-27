package daemonhb

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// RunWith refreshes changing fields (the daemon's schedules) before writing.
func TestRunWithRefreshesBeforeWriting(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunWith(ctx, Heartbeat{Version: "v1"}, func(hb *Heartbeat) {
			hb.Schedules = []Schedule{{WorkflowID: "w", NodeID: "n", NextRun: "2026-09-26T14:00:00Z"}}
		})
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	var hb Heartbeat
	for time.Now().Before(deadline) {
		if hb, _ = Read(); len(hb.Schedules) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if len(hb.Schedules) != 1 || hb.Schedules[0].NextRun != "2026-09-26T14:00:00Z" || hb.Version != "v1" {
		t.Fatalf("heartbeat = %+v", hb)
	}
}
