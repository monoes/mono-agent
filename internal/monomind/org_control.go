package monomind

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// Org process and messaging proxies for the org × workflow unification
// (docs/plans/2026-09-16-org-unification-contracts.md §4).

// InboxMessage is one message delivered with `org inbox`.
type InboxMessage struct {
	To      string // target role ("" = the org's coordinator)
	From    string // qualified "org:role"
	Subject string
	Body    string
}

// InboxReceipt is the delivery outcome (contracts §4 `org send`).
type InboxReceipt struct {
	V         int    `json:"v"`
	Org       string `json:"org"`
	To        string `json:"to"`
	From      string `json:"from"`
	Delivery  string `json:"delivery"` // live | queued
	Receipt   string `json:"receipt"`
	MessageID string `json:"messageId"`
}

// OrgInbox delivers msg to org name. With capability org-tool-providers
// (M3) monomind authenticates the live delivery with the operator
// credential and reports it as JSON. Older monomind can only queue for a
// running org (C-21), and the receipt says so instead of claiming live
// delivery.
func OrgInbox(ctx context.Context, projectRoot, name string, msg InboxMessage) (*InboxReceipt, error) {
	args := []string{"inbox", name}
	if msg.To != "" {
		args = append(args, "--to", msg.To)
	}
	args = append(args, "--from", msg.From, "--subject", msg.Subject, "--body", msg.Body)

	caps, err := Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	if caps.Has(CapOrgToolProviders) {
		raw, err := runOrgJSON(ctx, projectRoot, args...)
		if err != nil {
			return nil, err
		}
		var rc InboxReceipt
		if err := json.Unmarshal(raw, &rc); err != nil {
			return nil, fmt.Errorf("monomind org inbox %s: unreadable receipt: %w", name, err)
		}
		return &rc, nil
	}
	text, err := runOrgText(ctx, projectRoot, args...)
	if err != nil {
		return nil, err
	}
	delivery := "live"
	if strings.Contains(strings.ToLower(text), "queue") {
		delivery = "queued"
	}
	return &InboxReceipt{V: 1, Org: name, To: msg.To, From: msg.From, Delivery: delivery, Receipt: text}, nil
}

// OrgStop asks a running org's daemon to stop.
func OrgStop(ctx context.Context, projectRoot, name string) (string, error) {
	return runOrgText(ctx, projectRoot, "stop", name)
}

// OrgPause pauses an org: current turns finish, no new cycles start.
func OrgPause(ctx context.Context, projectRoot, name string) (string, error) {
	return runOrgText(ctx, projectRoot, "pause", name)
}

// OrgResume resumes a paused org.
func OrgResume(ctx context.Context, projectRoot, name string) (string, error) {
	return runOrgText(ctx, projectRoot, "resume", name)
}

// OrgDelete deletes an org and all its data; force deletes even when it
// appears to be running.
func OrgDelete(ctx context.Context, projectRoot, name string, force bool) (string, error) {
	args := []string{"delete", name, "--yes"}
	if force {
		args = append(args, "--force")
	}
	return runOrgText(ctx, projectRoot, args...)
}

// ServeHeartbeat is monomind's `<root>/.monomind/serve-heartbeat.json`.
type ServeHeartbeat struct {
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updatedAt"`
	Running   []string  `json:"running"`
}

// serveHeartbeatStale mirrors how long monomind's own status treats a
// heartbeat as current.
const serveHeartbeatStale = 60 * time.Second

// ReadServeHeartbeat returns the root's `org serve` heartbeat and whether
// that daemon is alive (fresh timestamp and a running pid).
func ReadServeHeartbeat(projectRoot string) (*ServeHeartbeat, bool) {
	b, err := os.ReadFile(filepath.Join(projectRoot, ".monomind", "serve-heartbeat.json"))
	if err != nil {
		return nil, false
	}
	var hb ServeHeartbeat
	if err := json.Unmarshal(b, &hb); err != nil {
		return nil, false
	}
	if hb.PID <= 0 || time.Since(hb.UpdatedAt) > serveHeartbeatStale {
		return &hb, false
	}
	return &hb, daemonhb.ProcessAlive(hb.PID)
}

// OrgServeStart starts `monomind org serve` for projectRoot as a detached
// process group writing to <root>/.monomind/serve.log, unless a live serve
// daemon already owns the root. Returns the pid and whether it was already
// running.
func OrgServeStart(ctx context.Context, projectRoot string) (pid int, alreadyRunning bool, err error) {
	if hb, live := ReadServeHeartbeat(projectRoot); live {
		return hb.PID, true, nil
	}
	bin, _, err := Ensure(ctx)
	if err != nil {
		return 0, false, err
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".monomind"), 0o755); err != nil {
		return 0, false, err
	}
	logf, err := os.OpenFile(filepath.Join(projectRoot, ".monomind", "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, false, err
	}
	defer logf.Close()
	cmd := exec.Command(bin, "org", "serve", "--cross-process")
	cmd.Dir = projectRoot
	cmd.Stdout = logf
	cmd.Stderr = logf
	// Detached: the daemon must outlive this process, so it gets no job or
	// group tied to us. OrgServeStop takes its tree down (signalServe).
	cmd, err = startDetached(cmd)
	if err != nil {
		return 0, false, fmt.Errorf("start monomind org serve: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, false, nil
}

// OrgServeRun runs `monomind org serve` in the foreground until ctx ends.
func OrgServeRun(ctx context.Context, projectRoot string) error {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "org", "serve", "--cross-process")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setProcessGroup(cmd)
	release, err := startProcessGroup(cmd)
	if err != nil {
		return err
	}
	defer release()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		killProcessGroup(cmd, cmd.Process.Pid)
		<-done
		return nil
	case err := <-done:
		return err
	}
}
