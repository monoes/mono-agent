package monomind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Kinds of start refusal (monomind 2.24: GA rows R6 and R1).
const (
	// RefusalPreflight: the host preflight (R6) ran on a start, resume or
	// role replacement and the host can't provide what a sections org needs.
	RefusalPreflight = "host-preflight"
	// RefusalDaemonLock: the per-org daemon lock (R1) refused a network
	// filesystem, or another daemon already owns the org.
	RefusalDaemonLock = "daemon-lock"
)

// OrgStartRefusal is monomind refusing to start or resume an org because
// of the host (R6) or the daemon lock (R1). Text is monomind's own line,
// verbatim; Hint is mono-agent's short pointer on what to do about it.
type OrgStartRefusal struct {
	Org  string
	Kind string
	Text string
	Hint string
}

func (e *OrgStartRefusal) Error() string { return e.Text + "\n" + e.Hint }

// AsStartRefusal finds an OrgStartRefusal in err's chain.
func AsStartRefusal(err error) (*OrgStartRefusal, bool) {
	var r *OrgStartRefusal
	ok := errors.As(err, &r)
	return r, ok
}

// JSONErrorFields gives `--json` callers the machine-readable refusal.
func (e *OrgStartRefusal) JSONErrorFields() map[string]any {
	return map[string]any{"code": "org_start_refused", "org": e.Org, "reason": e.Kind, "detail": e.Text}
}

// monomind's own wording (orgrt/documents/preflight.ts assertHostPreflight,
// orgrt/daemon-lock.ts), checked against 2.24.1:
//   - `org "<n>" cannot start|resume|replace role <r> on this host: ...`
//   - `org root <root> is on a network filesystem (<type>); ...`
//   - `org "<n>" is already owned by another daemon on <root>`
//
// `org run` prefixes the line with "Could not start org <n>: ", on stdout.
var (
	preflightLine = regexp.MustCompile(`(?m)^.*org "[^"\n]*" cannot [^\n]*? on this host: .*$`)
	lockLine      = regexp.MustCompile(`(?m)^.*(?:org root \S+ is on a network filesystem \(|org "[^"\n]*" is already owned by another daemon on ).*$`)
)

const (
	preflightHint = "monomind checks the host before every start, resume and role replacement (R6): fix what the message names on this machine, then start again."
	netFSHint     = "monomind keeps one daemon per org with a lock that needs a local filesystem (R1): move the project to a local disk, then start again."
	heldHint      = "another daemon already owns this org (R1): stop it (`monoagentcli org stop`) before starting it again."
)

// asStartRefusal is the refusal in text (monomind's output of a failed
// start), or nil when it holds none.
func asStartRefusal(org, text string) *OrgStartRefusal {
	if l := preflightLine.FindString(text); l != "" {
		return &OrgStartRefusal{Org: org, Kind: RefusalPreflight, Text: strings.TrimSpace(l), Hint: preflightHint}
	}
	if l := lockLine.FindString(text); l != "" {
		hint := netFSHint
		if strings.Contains(l, "already owned by another daemon") {
			hint = heldHint
		}
		return &OrgStartRefusal{Org: org, Kind: RefusalDaemonLock, Text: strings.TrimSpace(l), Hint: hint}
	}
	return nil
}

// startRefusal turns err into an OrgStartRefusal when text (the failed
// command's output, which may hold more than err does) carries one.
func startRefusal(org, text string, err error) error {
	if err == nil {
		return nil
	}
	if r := asStartRefusal(org, text+"\n"+err.Error()); r != nil {
		return r
	}
	return err
}

// startWatch is how long a detached start is watched. monomind refuses a
// start (R6/R1) within a fraction of a second (0.18s measured), before any
// session exists; a refusal after that is in the org's own logs. It is paid
// by every successful start, so it stays short.
var startWatch = time.Second

// captureTail is how much of a failed start's output is kept for its error.
const captureTail = 4096

// startCapture is a bounded, nameless capture of a detached child's output.
// It must be a file, not a pipe: the child outlives this process, and a
// pipe would break under it (SIGPIPE/EPIPE) when we exit. The file is
// unlinked at once, so no path is left behind; the child writes in append
// mode and we truncate it while we are alive, so it does not accumulate.
type startCapture struct {
	f    *os.File
	left string // path still to remove (a platform that can't unlink an open file)
}

func newStartCapture() (*startCapture, error) {
	dir, err := os.MkdirTemp("", "monomind-org-start-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "out")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		os.Remove(dir)
		return nil, err
	}
	c := &startCapture{f: f}
	if os.Remove(path) != nil || os.Remove(dir) != nil {
		c.left = dir // removed in close, after the child is gone
	}
	return c, nil
}

func (c *startCapture) tail() string {
	st, err := c.f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - captureTail
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	n, _ := c.f.ReadAt(b, off)
	return strings.TrimSpace(string(b[:n]))
}

func (c *startCapture) close() {
	c.f.Close()
	if c.left != "" {
		os.RemoveAll(c.left)
	}
}

// watchStart reaps cmd (started, writing to cap) and watches it for
// startWatch. An early exit with an error is returned: monomind's own
// refusal (OrgStartRefusal) when its output holds one, else the exit error
// with the output's tail — never nil. A process still running after the
// window is left to the caller's status polling, and is drained meanwhile.
// A cancelled ctx stops the start (the child's process group is killed).
func watchStart(ctx context.Context, cmd *exec.Cmd, org string, cap *startCapture) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(startWatch)
	defer timer.Stop()
	select {
	case err := <-done:
		defer cap.close()
		if err == nil {
			return nil
		}
		out := cap.tail()
		if r := asStartRefusal(org, out); r != nil {
			return r
		}
		if out == "" {
			out = err.Error()
		}
		if reason := signatureRefusalReason(out); reason != "" {
			return asSignatureRefusal(org, fmt.Errorf("monomind org run %s: %s", org, out))
		}
		return fmt.Errorf("monomind org run %s exited at start (%v): %s", org, err, out)
	case <-ctx.Done():
		killProcessGroup(cmd, cmd.Process.Pid)
		<-done
		cap.close()
		return ctx.Err()
	case <-timer.C:
	}
	go func() {
		defer cap.close()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				_ = cap.f.Truncate(0)
			}
		}
	}()
	return nil
}
