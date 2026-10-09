package openaiapi

import (
	"fmt"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// Class is how much a runtime's turn can do on this machine, weakest
// confinement last. chat-only < sandboxed < unconfined in power. The zero
// Class is invalid on purpose: a class that was never set is allowed by no
// policy.
type Class int

const (
	// ChatOnly: monomind's allow-list gate is the only tool gate, so with no
	// caller tools nothing native is reachable (claude).
	ChatOnly Class = iota + 1
	// Sandboxed: writes are confined to the turn's folder, but reads and the
	// network stay open (codex under workspace-write).
	Sandboxed
	// Unconfined: the runtime's native tools run as the OS user (antigravity).
	Unconfined
)

func (c Class) String() string {
	switch c {
	case ChatOnly:
		return "chat-only"
	case Sandboxed:
		return "sandboxed"
	}
	return "unconfined"
}

// ClassifyRuntime decides a runtime's class from its `agent scan` entry and
// the handshake, before any turn runs. It fails closed: anything it cannot
// vouch for is Unconfined.
//
//   - native_sandbox "monomind": monomind enforces the access mode itself, so
//     claude's default scoped access exposes no native tool: ChatOnly.
//   - otherwise, when Exec would apply the workspace-write sandbox to this
//     runtime (monomind.SandboxArgs says "sandboxed"): Sandboxed, unless the
//     scan reports that mode as rule-based (copilot's allow/deny rules), which
//     is no boundary.
//   - otherwise Unconfined.
func ClassifyRuntime(e monomind.ScanEntry, caps *monomind.CapabilitySet) Class {
	if e.NativeSandbox == "monomind" {
		return ChatOnly
	}
	if _, status := monomind.SandboxArgs(caps, e.SandboxModes, e.ID, monomind.SandboxWorkspaceWrite); status == monomind.SandboxStatusSandboxed && !e.RuleBasedSandbox(monomind.SandboxWorkspaceWrite) {
		return Sandboxed
	}
	return Unconfined
}

// ClassFromNativeSandbox maps the start event's native_sandbox, the
// confinement the turn really runs with, to a class.
func ClassFromNativeSandbox(native string) Class {
	switch native {
	case "monomind":
		return ChatOnly
	case "workspace-write", "read-only":
		return Sandboxed
	}
	// "restricted" is the CLI's own approval rules, not a boundary, and
	// "full", "none" and anything new or missing are not confinement at all.
	return Unconfined
}

// Policy is what a listener serves.
type Policy struct {
	// Max is the strongest class the listener serves.
	Max Class
	// ContextMax is the strongest class a key created with --context may use.
	// Such a request puts excerpts of the profile's own knowledge, which
	// includes captured web pages nobody vetted, into the system prompt, and
	// only a runtime with no native tools can safely read that, so the zero
	// value means chat-only. It never raises Max.
	ContextMax Class
	// AutoMax is the strongest class the auto model may pick. A prompt can steer
	// which model Jev picks, and the author of a prompt need not be the holder
	// of the key, so auto stays among chat-only models until the operator gives
	// it more: the zero value means chat-only. It never raises Max.
	AutoMax Class
}

// ParsePolicy reads a --confinement value: chat-only, sandboxed or any.
func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "chat-only":
		return Policy{Max: ChatOnly}, nil
	case "sandboxed":
		return Policy{Max: Sandboxed}, nil
	case "any":
		return Policy{Max: Unconfined}, nil
	}
	return Policy{}, fmt.Errorf("unknown confinement %q: use chat-only, sandboxed or any", s)
}

// DefaultPolicy is what a listener bound to addr serves when the operator
// set nothing: everything on loopback (same-user trust), chat-only anywhere
// else.
func DefaultPolicy(addr string) Policy {
	if tlsserve.IsLoopbackAddr(addr) {
		return Policy{Max: Unconfined}
	}
	return Policy{Max: ChatOnly}
}

// Allows reports whether a runtime of class c may be served.
func (p Policy) Allows(c Class) bool { return c >= ChatOnly && c <= p.Max }

// ForContextKey is the policy for a request made with a key created with
// --context: the listener's, capped at ContextMax (chat-only when unset).
func (p Policy) ForContextKey() Policy {
	limit := p.ContextMax
	if limit == 0 {
		limit = ChatOnly
	}
	if p.Max > limit {
		p.Max = limit
	}
	return p
}

// ForAuto is the policy the auto model picks within: p, capped at AutoMax
// (chat-only when unset). For a context key call it on ForContextKey's result, so
// that the key's own cap applies too.
func (p Policy) ForAuto() Policy {
	limit := p.AutoMax
	if limit == 0 {
		limit = ChatOnly
	}
	if p.Max > limit {
		p.Max = limit
	}
	return p
}

func (p Policy) String() string {
	switch p.Max {
	case ChatOnly:
		return "chat-only"
	case Sandboxed:
		return "sandboxed"
	case Unconfined:
		return "any"
	}
	return "none"
}
