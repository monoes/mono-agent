// Package daemonhb is the liveness contract of `monoagentcli daemon`
// (plan §7.6): the daemon rewrites ~/.monoagent/daemon-heartbeat.json every
// Interval, and a reader treats it as live only when the timestamp is
// younger than Stale AND the pid still runs — the same rule monomind applies
// to its serve heartbeat, so a crashed daemon never looks alive.
package daemonhb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	Interval = 10 * time.Second
	Stale    = 30 * time.Second
)

// Heartbeat is the file's content.
type Heartbeat struct {
	V          int       `json:"v"`
	PID        int       `json:"pid"`
	TS         time.Time `json:"ts"`
	APIAddr    string    `json:"api_addr,omitempty"`    // "" when the API is off
	BridgeAddr string    `json:"bridge_addr,omitempty"` // "" when the extension bridge is off
	Version    string    `json:"version,omitempty"`
	// Schedules are the registered schedule triggers with the scheduler's
	// own next fire time, refreshed on every write.
	Schedules []Schedule `json:"schedules,omitempty"`
}

// Schedule is one registered schedule trigger.
type Schedule struct {
	WorkflowID string `json:"workflow_id"`
	NodeID     string `json:"node_id"`
	NextRun    string `json:"next_run"` // RFC3339 UTC
}

// Path returns the heartbeat file path. MONOAGENT_DAEMON_HEARTBEAT
// overrides it (tests, and running two daemons side by side).
func Path() string {
	if p := os.Getenv("MONOAGENT_DAEMON_HEARTBEAT"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".monoagent", "daemon-heartbeat.json")
}

// Write atomically replaces the heartbeat file.
func Write(hb Heartbeat) error {
	hb.V = 1
	if hb.TS.IsZero() {
		hb.TS = time.Now()
	}
	b, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Read returns the heartbeat and whether it is live.
func Read() (Heartbeat, bool) {
	var hb Heartbeat
	b, err := os.ReadFile(Path())
	if err != nil {
		return hb, false
	}
	if err := json.Unmarshal(b, &hb); err != nil {
		return hb, false
	}
	return hb, IsLive(hb, time.Now())
}

// IsLive applies the freshness + pid rule.
func IsLive(hb Heartbeat, now time.Time) bool {
	if hb.PID <= 0 || hb.TS.IsZero() {
		return false
	}
	age := now.Sub(hb.TS)
	if age < -Stale || age > Stale {
		return false
	}
	return ProcessAlive(hb.PID)
}

// Run writes a heartbeat now and every Interval until ctx ends, then removes
// the file if it still names this process.
func Run(ctx context.Context, hb Heartbeat) { RunWith(ctx, hb, nil) }

// RunWith is Run with refresh called before every write, to update fields
// that change while the daemon runs (e.g. Schedules).
func RunWith(ctx context.Context, hb Heartbeat, refresh func(*Heartbeat)) {
	hb.PID = os.Getpid()
	write := func() {
		if refresh != nil {
			refresh(&hb)
		}
		hb.TS = time.Now()
		_ = Write(hb)
	}
	write()
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if cur, _ := Read(); cur.PID == hb.PID {
				_ = os.Remove(Path())
			}
			return
		case <-t.C:
			write()
		}
	}
}
