package health

import (
	"context"
	"strings"
	"testing"
)

func TestDaemonVersionStale(t *testing.T) {
	for _, c := range []struct {
		daemon, binary string
		want           bool
	}{
		{"v0.105.0", "v0.108.0", true},
		{"0.108.0", "v0.108.0", false},
		{"", "v0.108.0", true},
		{"v0.105.0", "dev", false},
		{"v0.105.0", "v0.108.0-3-gabc", false},
		{"v0.108.0-3-gabc", "v0.108.0", false},
		{"v0.105.0", "", false},
	} {
		if got := DaemonVersionStale(c.daemon, c.binary); got != c.want {
			t.Errorf("DaemonVersionStale(%q, %q) = %v, want %v", c.daemon, c.binary, got, c.want)
		}
	}
}

func TestCheckDaemonFlagsAStaleVersion(t *testing.T) {
	env := &Env{Version: "v0.108.0", Daemon: func(context.Context) DaemonInfo {
		return DaemonInfo{Running: true, PID: 7, Version: "v0.105.0"}
	}}
	res := checkDaemon(context.Background(), env)
	if res.Status != StatusWarn || res.FixID != FixDaemonRestart || !strings.Contains(res.Summary, "v0.105.0") {
		t.Fatalf("stale daemon: %+v", res)
	}
	env.Version = "v0.105.0"
	if res := checkDaemon(context.Background(), env); res.Status != StatusOK {
		t.Fatalf("current daemon: %+v", res)
	}
}

func TestFixDaemonRestartStopsThenStarts(t *testing.T) {
	var calls []string
	running := true
	env := &Env{
		Daemon: func(context.Context) DaemonInfo { return DaemonInfo{Running: running, PID: 7} },
		StopDaemon: func(_ context.Context, _ int, _ func(string)) error {
			calls = append(calls, "stop")
			running = false
			return nil
		},
		StartDaemon: func(context.Context, func(string)) error {
			calls = append(calls, "start")
			running = true
			return nil
		},
	}
	if err := fixDaemonRestart(context.Background(), env, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "stop,start" {
		t.Fatalf("calls = %v", calls)
	}
	for _, f := range serviceFixes() {
		if f.ID == FixDaemonRestart && f.Safety != SafetyConfirm {
			t.Errorf("safety = %s", f.Safety)
		}
	}
}
