package monomind

import (
	"errors"
	"os"
	"os/exec"
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

// startWatch is how long a detached start is watched: monomind refuses a
// start within a fraction of a second (before any session exists), and a
// refusal after that is in the org's own logs.
var startWatch = 3 * time.Second

// watchStart reaps cmd (already started, writing to out from offset) and,
// if it exits with an error within startWatch, reports monomind's start
// refusal from what it wrote. Any other outcome is nil: a process that
// stays up, or dies for a reason that is not a start refusal, is left to
// the caller's own status polling, as before.
func watchStart(cmd *exec.Cmd, org string, out *os.File, offset int64) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		b, _ := os.ReadFile(out.Name())
		if int64(len(b)) > offset {
			b = b[offset:]
		}
		if r := asStartRefusal(org, string(b)); r != nil {
			return r
		}
	case <-time.After(startWatch):
	}
	return nil
}
